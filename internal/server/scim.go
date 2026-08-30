package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/relayedock/relayedock/internal/auth"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/store"
)

const (
	scimUserSchema      = "urn:ietf:params:scim:schemas:core:2.0:User"
	scimGroupSchema     = "urn:ietf:params:scim:schemas:core:2.0:Group"
	scimListSchema      = "urn:ietf:params:scim:api:messages:2.0:ListResponse"
	scimPatchSchema     = "urn:ietf:params:scim:api:messages:2.0:PatchOp"
	scimErrorSchema     = "urn:ietf:params:scim:api:messages:2.0:Error"
	scimConnectionKey   = "relayedock.scim.connection"
	defaultSCIMPageSize = 100
	maximumSCIMPageSize = 200
)

type scimEmail struct {
	Value   string `json:"value"`
	Type    string `json:"type,omitempty"`
	Primary bool   `json:"primary,omitempty"`
}

type scimName struct {
	Formatted  string `json:"formatted,omitempty"`
	GivenName  string `json:"givenName,omitempty"`
	FamilyName string `json:"familyName,omitempty"`
}

type scimMeta struct {
	ResourceType string    `json:"resourceType"`
	Created      time.Time `json:"created"`
	LastModified time.Time `json:"lastModified"`
	Location     string    `json:"location"`
}

type scimUser struct {
	Schemas     []string    `json:"schemas"`
	ID          string      `json:"id,omitempty"`
	ExternalID  string      `json:"externalId,omitempty"`
	UserName    string      `json:"userName"`
	DisplayName string      `json:"displayName,omitempty"`
	Name        scimName    `json:"name,omitempty"`
	Emails      []scimEmail `json:"emails,omitempty"`
	Active      *bool       `json:"active,omitempty"`
	Meta        *scimMeta   `json:"meta,omitempty"`
}

type scimMember struct {
	Value   string `json:"value"`
	Display string `json:"display,omitempty"`
}

type scimGroup struct {
	Schemas     []string     `json:"schemas"`
	ID          string       `json:"id,omitempty"`
	ExternalID  string       `json:"externalId,omitempty"`
	DisplayName string       `json:"displayName"`
	Members     []scimMember `json:"members,omitempty"`
	Meta        *scimMeta    `json:"meta,omitempty"`
}

type scimPatchRequest struct {
	Schemas    []string             `json:"schemas"`
	Operations []scimPatchOperation `json:"Operations"`
}

type scimPatchOperation struct {
	Op    string          `json:"op"`
	Path  string          `json:"path"`
	Value json.RawMessage `json:"value"`
}

func registerSCIMRoutes(r *gin.Engine, d Dependencies) {
	scim := r.Group("/scim/v2/:organizationID")
	scim.Use(scimAuthentication(d))
	scim.GET("/ServiceProviderConfig", func(c *gin.Context) {
		scimJSON(c, http.StatusOK, gin.H{"schemas": []string{"urn:ietf:params:scim:schemas:core:2.0:ServiceProviderConfig"},
			"documentationUri": "https://www.rfc-editor.org/rfc/rfc7644", "patch": gin.H{"supported": true},
			"bulk":   gin.H{"supported": false, "maxOperations": 0, "maxPayloadSize": 0},
			"filter": gin.H{"supported": true, "maxResults": maximumSCIMPageSize}, "changePassword": gin.H{"supported": false},
			"sort": gin.H{"supported": false}, "etag": gin.H{"supported": false},
			"authenticationSchemes": []gin.H{{"type": "oauthbearertoken", "name": "Bearer Token", "description": "Organization-scoped SCIM bearer token", "primary": true}}})
	})
	scim.GET("/ResourceTypes", func(c *gin.Context) {
		resources := []gin.H{
			{"schemas": []string{"urn:ietf:params:scim:schemas:core:2.0:ResourceType"}, "id": "User", "name": "User", "endpoint": "/Users", "schema": scimUserSchema},
			{"schemas": []string{"urn:ietf:params:scim:schemas:core:2.0:ResourceType"}, "id": "Group", "name": "Group", "endpoint": "/Groups", "schema": scimGroupSchema},
		}
		scimList(c, resources, len(resources), 1, len(resources))
	})
	scim.GET("/Schemas", func(c *gin.Context) {
		resources := []gin.H{
			{"schemas": []string{"urn:ietf:params:scim:schemas:core:2.0:Schema"}, "id": scimUserSchema, "name": "User", "description": "RelayDock organization user"},
			{"schemas": []string{"urn:ietf:params:scim:schemas:core:2.0:Schema"}, "id": scimGroupSchema, "name": "Group", "description": "RelayDock organization team"},
		}
		scimList(c, resources, len(resources), 1, len(resources))
	})
	registerSCIMUserRoutes(scim, d)
	registerSCIMGroupRoutes(scim, d)
}

func scimAuthentication(d Dependencies) gin.HandlerFunc {
	return func(c *gin.Context) {
		if d.Auth == nil || d.Store == nil {
			scimError(c, http.StatusServiceUnavailable, "SCIM is unavailable.", "temporarilyUnavailable")
			c.Abort()
			return
		}
		header := strings.TrimSpace(c.GetHeader("Authorization"))
		if len(header) < 8 || !strings.EqualFold(header[:7], "Bearer ") {
			scimError(c, http.StatusUnauthorized, "A bearer token is required.", "invalidToken")
			c.Abort()
			return
		}
		connection, err := d.Store.AuthenticateSCIM(c.Request.Context(), c.Param("organizationID"),
			d.Auth.DigestToken(strings.TrimSpace(header[7:])))
		if err != nil {
			scimError(c, http.StatusUnauthorized, "The SCIM token is invalid or disabled.", "invalidToken")
			c.Abort()
			return
		}
		c.Set(scimConnectionKey, connection)
		c.Next()
	}
}

func registerSCIMUserRoutes(g *gin.RouterGroup, d Dependencies) {
	g.GET("/Users", func(c *gin.Context) {
		items, err := d.Store.ListSCIMUsers(c.Request.Context(), c.Param("organizationID"))
		if err != nil {
			scimStoreError(c, err)
			return
		}
		filterAttribute, filterValue, filterErr := parseSCIMFilter(c.Query("filter"))
		if filterErr != nil || filterAttribute == "displayname" {
			scimError(c, http.StatusBadRequest, "Only userName eq and externalId eq filters are supported for Users.", "invalidFilter")
			return
		}
		resources := make([]scimUser, 0, len(items))
		for _, item := range items {
			if (filterAttribute == "username" && !strings.EqualFold(item.Email, filterValue)) ||
				(filterAttribute == "externalid" && item.ExternalID != filterValue) {
				continue
			}
			resources = append(resources, scimUserView(item, c.Request.URL.Path))
		}
		start, count := scimPage(c, len(resources))
		page := resources[start-1 : min(start-1+count, len(resources))]
		scimList(c, page, len(resources), start, len(page))
	})
	g.POST("/Users", func(c *gin.Context) {
		var input scimUser
		if c.ShouldBindJSON(&input) != nil {
			scimError(c, http.StatusBadRequest, "A valid SCIM User is required.", "invalidValue")
			return
		}
		active := input.Active == nil || *input.Active
		email := scimUserEmail(input)
		if !scimEmailAllowed(c, email) {
			return
		}
		randomPassword, err := auth.NewOpaqueToken()
		if err != nil {
			scimStoreError(c, err)
			return
		}
		passwordHash, err := auth.HashPassword("SCIM-" + randomPassword + "-Aa9!")
		if err != nil {
			scimStoreError(c, err)
			return
		}
		item, err := d.Store.UpsertSCIMUser(c.Request.Context(), c.Param("organizationID"), "", email,
			scimDisplayName(input), input.ExternalID, active, passwordHash)
		if err != nil {
			scimStoreError(c, err)
			return
		}
		location := strings.TrimRight(c.Request.URL.Path, "/") + "/" + item.UserID
		c.Header("Location", location)
		scimJSON(c, http.StatusCreated, scimUserView(item, location))
	})
	g.GET("/Users/:resourceID", func(c *gin.Context) {
		item, err := d.Store.SCIMUser(c.Request.Context(), c.Param("organizationID"), c.Param("resourceID"))
		if err != nil {
			scimStoreError(c, err)
			return
		}
		scimJSON(c, http.StatusOK, scimUserView(item, c.Request.URL.Path))
	})
	upsert := func(c *gin.Context, patch bool) {
		current, err := d.Store.SCIMUser(c.Request.Context(), c.Param("organizationID"), c.Param("resourceID"))
		if err != nil {
			scimStoreError(c, err)
			return
		}
		input := scimUser{UserName: current.Email, DisplayName: current.DisplayName, ExternalID: current.ExternalID, Active: boolPtr(current.Active)}
		if patch {
			var request scimPatchRequest
			if c.ShouldBindJSON(&request) != nil || !containsStringFold(request.Schemas, scimPatchSchema) || applySCIMUserPatch(&input, request) != nil {
				scimError(c, http.StatusBadRequest, "The SCIM User patch is invalid or unsupported.", "invalidValue")
				return
			}
		} else if c.ShouldBindJSON(&input) != nil {
			scimError(c, http.StatusBadRequest, "A valid SCIM User is required.", "invalidValue")
			return
		}
		email := scimUserEmail(input)
		if !scimEmailAllowed(c, email) {
			return
		}
		active := input.Active == nil || *input.Active
		item, err := d.Store.UpsertSCIMUser(c.Request.Context(), c.Param("organizationID"), current.UserID, email,
			scimDisplayName(input), input.ExternalID, active, "")
		if err != nil {
			scimStoreError(c, err)
			return
		}
		scimJSON(c, http.StatusOK, scimUserView(item, c.Request.URL.Path))
	}
	g.PUT("/Users/:resourceID", func(c *gin.Context) { upsert(c, false) })
	g.PATCH("/Users/:resourceID", func(c *gin.Context) { upsert(c, true) })
	g.DELETE("/Users/:resourceID", func(c *gin.Context) {
		if err := d.Store.DeleteSCIMUser(c.Request.Context(), c.Param("organizationID"), c.Param("resourceID")); err != nil {
			scimStoreError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})
}

func registerSCIMGroupRoutes(g *gin.RouterGroup, d Dependencies) {
	g.GET("/Groups", func(c *gin.Context) {
		items, err := d.Store.ListSCIMGroups(c.Request.Context(), c.Param("organizationID"))
		if err != nil {
			scimStoreError(c, err)
			return
		}
		filterAttribute, filterValue, filterErr := parseSCIMFilter(c.Query("filter"))
		if filterErr != nil || (filterAttribute != "" && filterAttribute != "displayname" && filterAttribute != "externalid") {
			scimError(c, http.StatusBadRequest, "Only displayName eq and externalId eq filters are supported for Groups.", "invalidFilter")
			return
		}
		resources := make([]scimGroup, 0, len(items))
		for _, item := range items {
			if (filterAttribute == "displayname" && !strings.EqualFold(item.DisplayName, filterValue)) ||
				(filterAttribute == "externalid" && item.ExternalID != filterValue) {
				continue
			}
			resources = append(resources, scimGroupView(item, c.Request.URL.Path))
		}
		start, count := scimPage(c, len(resources))
		page := resources[start-1 : min(start-1+count, len(resources))]
		scimList(c, page, len(resources), start, len(page))
	})
	g.POST("/Groups", func(c *gin.Context) {
		var input scimGroup
		if c.ShouldBindJSON(&input) != nil || strings.TrimSpace(input.DisplayName) == "" {
			scimError(c, http.StatusBadRequest, "A displayName is required.", "invalidValue")
			return
		}
		item, err := d.Store.UpsertSCIMGroup(c.Request.Context(), c.Param("organizationID"), "", input.DisplayName,
			input.ExternalID, scimMemberIDs(input.Members))
		if err != nil {
			scimStoreError(c, err)
			return
		}
		location := strings.TrimRight(c.Request.URL.Path, "/") + "/" + item.TeamID
		c.Header("Location", location)
		scimJSON(c, http.StatusCreated, scimGroupView(item, location))
	})
	g.GET("/Groups/:resourceID", func(c *gin.Context) {
		item, err := d.Store.SCIMGroup(c.Request.Context(), c.Param("organizationID"), c.Param("resourceID"))
		if err != nil {
			scimStoreError(c, err)
			return
		}
		scimJSON(c, http.StatusOK, scimGroupView(item, c.Request.URL.Path))
	})
	upsert := func(c *gin.Context, patch bool) {
		current, err := d.Store.SCIMGroup(c.Request.Context(), c.Param("organizationID"), c.Param("resourceID"))
		if err != nil {
			scimStoreError(c, err)
			return
		}
		input := scimGroup{DisplayName: current.DisplayName, ExternalID: current.ExternalID}
		for _, memberID := range current.MemberIDs {
			input.Members = append(input.Members, scimMember{Value: memberID})
		}
		if patch {
			var request scimPatchRequest
			if c.ShouldBindJSON(&request) != nil || !containsStringFold(request.Schemas, scimPatchSchema) || applySCIMGroupPatch(&input, request) != nil {
				scimError(c, http.StatusBadRequest, "The SCIM Group patch is invalid or unsupported.", "invalidValue")
				return
			}
		} else if c.ShouldBindJSON(&input) != nil {
			scimError(c, http.StatusBadRequest, "A valid SCIM Group is required.", "invalidValue")
			return
		}
		item, err := d.Store.UpsertSCIMGroup(c.Request.Context(), c.Param("organizationID"), current.TeamID,
			input.DisplayName, input.ExternalID, scimMemberIDs(input.Members))
		if err != nil {
			scimStoreError(c, err)
			return
		}
		scimJSON(c, http.StatusOK, scimGroupView(item, c.Request.URL.Path))
	}
	g.PUT("/Groups/:resourceID", func(c *gin.Context) { upsert(c, false) })
	g.PATCH("/Groups/:resourceID", func(c *gin.Context) { upsert(c, true) })
	g.DELETE("/Groups/:resourceID", func(c *gin.Context) {
		if err := d.Store.DeleteSCIMGroup(c.Request.Context(), c.Param("organizationID"), c.Param("resourceID")); err != nil {
			scimStoreError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})
}

func scimUserView(item domain.SCIMUserRecord, location string) scimUser {
	active := item.Active
	return scimUser{Schemas: []string{scimUserSchema}, ID: item.UserID, ExternalID: item.ExternalID,
		UserName: item.Email, DisplayName: item.DisplayName, Name: scimName{Formatted: item.DisplayName},
		Emails: []scimEmail{{Value: item.Email, Type: "work", Primary: true}}, Active: &active,
		Meta: &scimMeta{ResourceType: "User", Created: item.CreatedAt, LastModified: item.UpdatedAt, Location: location}}
}

func scimGroupView(item domain.SCIMGroupRecord, location string) scimGroup {
	members := make([]scimMember, 0, len(item.MemberIDs))
	for _, memberID := range item.MemberIDs {
		members = append(members, scimMember{Value: memberID})
	}
	return scimGroup{Schemas: []string{scimGroupSchema}, ID: item.TeamID, ExternalID: item.ExternalID,
		DisplayName: item.DisplayName, Members: members,
		Meta: &scimMeta{ResourceType: "Group", Created: item.CreatedAt, LastModified: item.UpdatedAt, Location: location}}
}

func scimUserEmail(input scimUser) string {
	if strings.TrimSpace(input.UserName) != "" {
		return strings.ToLower(strings.TrimSpace(input.UserName))
	}
	for _, email := range input.Emails {
		if email.Primary && strings.TrimSpace(email.Value) != "" {
			return strings.ToLower(strings.TrimSpace(email.Value))
		}
	}
	if len(input.Emails) > 0 {
		return strings.ToLower(strings.TrimSpace(input.Emails[0].Value))
	}
	return ""
}

func scimDisplayName(input scimUser) string {
	if strings.TrimSpace(input.DisplayName) != "" {
		return strings.TrimSpace(input.DisplayName)
	}
	if strings.TrimSpace(input.Name.Formatted) != "" {
		return strings.TrimSpace(input.Name.Formatted)
	}
	return strings.TrimSpace(input.Name.GivenName + " " + input.Name.FamilyName)
}

func scimEmailAllowed(c *gin.Context, email string) bool {
	separator := strings.LastIndex(email, "@")
	if separator <= 0 || separator == len(email)-1 {
		scimError(c, http.StatusBadRequest, "userName must be a valid email address.", "invalidValue")
		return false
	}
	value, exists := c.Get(scimConnectionKey)
	connection, ok := value.(domain.EnterpriseIdentityConnection)
	if !exists || !ok {
		scimError(c, http.StatusUnauthorized, "The SCIM connection is unavailable.", "invalidToken")
		return false
	}
	if len(connection.AllowedDomains) == 0 {
		return true
	}
	domainName := strings.ToLower(email[separator+1:])
	for _, allowed := range connection.AllowedDomains {
		if domainName == strings.ToLower(allowed) {
			return true
		}
	}
	scimError(c, http.StatusBadRequest, "The email domain is not allowed for this organization.", "invalidValue")
	return false
}

func applySCIMUserPatch(user *scimUser, request scimPatchRequest) error {
	for _, operation := range request.Operations {
		if !strings.EqualFold(operation.Op, "replace") && !strings.EqualFold(operation.Op, "add") {
			return errors.New("unsupported User patch operation")
		}
		path := strings.ToLower(strings.TrimSpace(operation.Path))
		switch path {
		case "active":
			var value bool
			if json.Unmarshal(operation.Value, &value) != nil {
				return errors.New("invalid active value")
			}
			user.Active = &value
		case "displayname":
			if json.Unmarshal(operation.Value, &user.DisplayName) != nil {
				return errors.New("invalid displayName")
			}
		case "username":
			if json.Unmarshal(operation.Value, &user.UserName) != nil {
				return errors.New("invalid userName")
			}
		case "externalid":
			if json.Unmarshal(operation.Value, &user.ExternalID) != nil {
				return errors.New("invalid externalId")
			}
		case "":
			var values map[string]json.RawMessage
			if json.Unmarshal(operation.Value, &values) != nil {
				return errors.New("invalid patch object")
			}
			for key, value := range values {
				nested := scimPatchRequest{}
				nested.Operations = append(nested.Operations, scimPatchOperation{Op: operation.Op, Path: key, Value: value})
				if err := applySCIMUserPatch(user, nested); err != nil {
					return err
				}
			}
		default:
			return errors.New("unsupported User patch path")
		}
	}
	return nil
}

func applySCIMGroupPatch(group *scimGroup, request scimPatchRequest) error {
	for _, operation := range request.Operations {
		path := strings.ToLower(strings.TrimSpace(operation.Path))
		switch {
		case path == "displayname" && (strings.EqualFold(operation.Op, "replace") || strings.EqualFold(operation.Op, "add")):
			if json.Unmarshal(operation.Value, &group.DisplayName) != nil {
				return errors.New("invalid displayName")
			}
		case path == "externalid" && (strings.EqualFold(operation.Op, "replace") || strings.EqualFold(operation.Op, "add")):
			if json.Unmarshal(operation.Value, &group.ExternalID) != nil {
				return errors.New("invalid externalId")
			}
		case path == "members" && strings.EqualFold(operation.Op, "replace"):
			if json.Unmarshal(operation.Value, &group.Members) != nil {
				return errors.New("invalid members")
			}
		case path == "members" && strings.EqualFold(operation.Op, "add"):
			var members []scimMember
			if json.Unmarshal(operation.Value, &members) != nil {
				return errors.New("invalid members")
			}
			group.Members = append(group.Members, members...)
		case path == "members" && strings.EqualFold(operation.Op, "remove"):
			group.Members = []scimMember{}
		case strings.HasPrefix(path, "members[value eq ") && strings.EqualFold(operation.Op, "remove"):
			value, err := scimMemberFilterValue(operation.Path)
			if err != nil {
				return err
			}
			kept := group.Members[:0]
			for _, member := range group.Members {
				if member.Value != value {
					kept = append(kept, member)
				}
			}
			group.Members = kept
		case path == "" && (strings.EqualFold(operation.Op, "replace") || strings.EqualFold(operation.Op, "add")):
			var values map[string]json.RawMessage
			if json.Unmarshal(operation.Value, &values) != nil {
				return errors.New("invalid patch object")
			}
			for key, value := range values {
				nested := scimPatchRequest{Operations: []scimPatchOperation{{Op: operation.Op, Path: key, Value: value}}}
				if err := applySCIMGroupPatch(group, nested); err != nil {
					return err
				}
			}
		default:
			return errors.New("unsupported Group patch operation")
		}
	}
	return nil
}

func scimMemberFilterValue(path string) (string, error) {
	start, end := strings.Index(path, "\""), strings.LastIndex(path, "\"")
	if start < 0 || end <= start {
		return "", errors.New("invalid member filter")
	}
	return path[start+1 : end], nil
}

func scimMemberIDs(members []scimMember) []string {
	out := make([]string, 0, len(members))
	seen := map[string]struct{}{}
	for _, member := range members {
		value := strings.TrimSpace(member.Value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func parseSCIMFilter(raw string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", nil
	}
	parts := strings.SplitN(raw, " eq ", 2)
	if len(parts) != 2 {
		return "", "", errors.New("only equality filters are supported")
	}
	attribute := strings.ToLower(strings.TrimSpace(parts[0]))
	value := strings.TrimSpace(parts[1])
	if len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
		return "", "", errors.New("filter values must be quoted")
	}
	value = strings.ReplaceAll(value[1:len(value)-1], `\"`, `"`)
	if attribute != "username" && attribute != "externalid" && attribute != "displayname" {
		return "", "", errors.New("the filter attribute is unsupported")
	}
	return attribute, value, nil
}

func scimPage(c *gin.Context, total int) (int, int) {
	start, _ := strconv.Atoi(c.DefaultQuery("startIndex", "1"))
	count, _ := strconv.Atoi(c.DefaultQuery("count", strconv.Itoa(defaultSCIMPageSize)))
	if start < 1 {
		start = 1
	}
	if start > total+1 {
		start = total + 1
	}
	if count < 0 {
		count = 0
	}
	if count > maximumSCIMPageSize {
		count = maximumSCIMPageSize
	}
	return start, count
}

func scimList(c *gin.Context, resources any, total, start, count int) {
	scimJSON(c, http.StatusOK, gin.H{"schemas": []string{scimListSchema}, "totalResults": total,
		"startIndex": start, "itemsPerPage": count, "Resources": resources})
}

func scimStoreError(c *gin.Context, err error) {
	if errors.Is(err, store.ErrNotFound) {
		scimError(c, http.StatusNotFound, "The SCIM resource was not found.", "")
		return
	}
	message := "The SCIM operation could not be completed."
	status, scimType := http.StatusInternalServerError, ""
	if strings.Contains(strings.ToLower(err.Error()), "invalid") || strings.Contains(strings.ToLower(err.Error()), "cannot") ||
		strings.Contains(strings.ToLower(err.Error()), "not active") || strings.Contains(strings.ToLower(err.Error()), "required") {
		message, status, scimType = err.Error(), http.StatusBadRequest, "invalidValue"
	}
	scimError(c, status, message, scimType)
}

func scimError(c *gin.Context, status int, detail, scimType string) {
	body := gin.H{"schemas": []string{scimErrorSchema}, "status": strconv.Itoa(status), "detail": detail}
	if scimType != "" {
		body["scimType"] = scimType
	}
	scimJSON(c, status, body)
}

func scimJSON(c *gin.Context, status int, value any) {
	raw, err := json.Marshal(value)
	if err != nil {
		c.Data(http.StatusInternalServerError, "application/scim+json", []byte(`{"schemas":["`+scimErrorSchema+`"],"status":"500","detail":"SCIM response encoding failed."}`))
		return
	}
	c.Data(status, "application/scim+json", raw)
}

func containsStringFold(values []string, expected string) bool {
	for _, value := range values {
		if strings.EqualFold(value, expected) {
			return true
		}
	}
	return false
}

func boolPtr(value bool) *bool { return &value }
