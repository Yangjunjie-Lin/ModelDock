package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/store"
)

func publicStatusHandler(c *gin.Context, d Dependencies) {
	if d.Store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "DEGRADED", "components": map[string]any{"gateway": map[string]any{"status": "OPERATIONAL"}, "dashboard": map[string]any{"status": "UNKNOWN"}, "billing": map[string]any{"status": "UNKNOWN"}}, "events": []any{}})
		return
	}
	status, err := d.Store.PublicStatus(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "DEGRADED", "components": map[string]any{"gateway": map[string]any{"status": "OPERATIONAL"}, "dashboard": map[string]any{"status": "UNKNOWN"}, "billing": map[string]any{"status": "UNKNOWN"}}, "events": []any{}})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 1500*time.Millisecond)
	defer cancel()
	postgresOK := d.Store.Ping(ctx) == nil
	redisOK := d.Redis == nil || d.Redis.Ping(ctx).Err() == nil
	components, _ := status["components"].(map[string]any)
	if !postgresOK {
		components["billing"] = map[string]any{"status": "MAJOR_OUTAGE", "message": "Billing data is temporarily unavailable"}
		status["status"] = "DEGRADED"
	}
	if !redisOK {
		components["gateway"] = map[string]any{"status": "DEGRADED", "message": "Gateway capacity state is temporarily unavailable"}
		status["status"] = "DEGRADED"
	}
	status["components"] = components
	status["updated_at"] = time.Now().UTC()
	c.JSON(http.StatusOK, status)
}

func registerObservabilityRoutes(g *gin.RouterGroup, d Dependencies, admin bool) {
	if admin {
		g.GET("/observability", func(c *gin.Context) {
			metrics := map[string]any{}
			if d.Metrics != nil {
				metrics = d.Metrics.Snapshot()
			}
			runtime := gin.H{
				"migration_mode": d.Config.MigrationMode, "max_request_body_bytes": d.Config.MaxBodyBytes,
				"max_stream_bytes": d.Config.StreamMaxBytes, "provider_allowlist_hosts": len(d.Config.ProviderAllowedHosts),
				"provider_private_network": d.Config.ProviderAllowPrivate, "cookie_secure": d.Config.CookieSecure,
				"admin_mfa_required": d.Config.AdminMFARequired, "redis_failure_mode": "FAIL_CLOSED",
			}
			if d.Store == nil {
				respond(c, gin.H{"metrics": metrics, "runtime": runtime}, nil)
				return
			}
			slos, err := d.Store.ListSLOs(c.Request.Context())
			if err != nil {
				respond(c, nil, err)
				return
			}
			c.JSON(http.StatusOK, gin.H{"metrics": metrics, "slos": slos, "runtime": runtime})
		})
		g.GET("/status/events", func(c *gin.Context) {
			events, err := d.Store.ListStatusEvents(c.Request.Context(), 100, true)
			respondList(c, events, err)
		})
		g.GET("/observability/requests/:requestID", func(c *gin.Context) {
			result, err := d.Store.InvestigateRequest(c.Request.Context(), strings.TrimSpace(c.Param("requestID")))
			respond(c, result, err)
		})
		g.POST("/status/events", func(c *gin.Context) {
			var event domain.StatusEvent
			if c.ShouldBindJSON(&event) != nil || !validStatusComponent(event.Component) || !validStatusValue(event.Status) || strings.TrimSpace(event.Summary) == "" || len(event.Summary) > 200 || strings.TrimSpace(event.PublicMessage) == "" || len(event.PublicMessage) > 2000 || len(event.DedupeKey) > 200 {
				openAIError(c, http.StatusBadRequest, "invalid_request", "component, status, summary, and public_message are required.")
				return
			}
			created, err := d.Store.CreateStatusEvent(c.Request.Context(), event, claimsFrom(c).Subject)
			respondCreated(c, created, err)
		})
		g.POST("/status/events/:id/resolve", func(c *gin.Context) {
			err := d.Store.ResolveStatusEvent(c.Request.Context(), c.Param("id"), claimsFrom(c).Subject)
			respondNoContent(c, err)
		})
	}
	statusPath := "/status"
	if admin {
		statusPath = "/status/summary"
	}
	g.GET(statusPath, func(c *gin.Context) { publicStatusHandler(c, d) })
}

func registerSupportRoutes(g *gin.RouterGroup, d Dependencies, admin bool) {
	g.GET("/support/tickets", func(c *gin.Context) {
		limit, offset := page(c)
		var userID, organizationID *string
		if !admin {
			user := claimsFrom(c).Subject
			userID = &user
		} else if value := strings.TrimSpace(c.Query("organization_id")); value != "" {
			organizationID = &value
		}
		tickets, err := d.Store.ListSupportTickets(c.Request.Context(), userID, organizationID, limit, offset, admin)
		respondList(c, tickets, err)
	})
	g.POST("/support/tickets", func(c *gin.Context) {
		var input struct {
			Subject         string         `json:"subject"`
			Body            string         `json:"body"`
			Priority        string         `json:"priority"`
			UserID          string         `json:"user_id"`
			OrganizationID  string         `json:"organization_id"`
			RequestID       string         `json:"request_id"`
			OrderID         string         `json:"order_id"`
			LedgerJournalID string         `json:"ledger_journal_id"`
			Context         map[string]any `json:"context"`
		}
		if c.ShouldBindJSON(&input) != nil || strings.TrimSpace(input.Subject) == "" || strings.TrimSpace(input.Body) == "" || len(input.Subject) > 200 || len(input.Body) > 10000 {
			openAIError(c, http.StatusBadRequest, "invalid_request", "subject and body are required and must be within the documented limits.")
			return
		}
		priority := strings.ToUpper(strings.TrimSpace(input.Priority))
		if priority == "" {
			priority = "NORMAL"
		}
		if priority != "LOW" && priority != "NORMAL" && priority != "HIGH" && priority != "URGENT" {
			openAIError(c, http.StatusBadRequest, "invalid_request", "priority must be LOW, NORMAL, HIGH, or URGENT.")
			return
		}
		claims := claimsFrom(c)
		if len(input.RequestID) > 200 || !validOptionalUUID(input.OrganizationID) || !validOptionalUUID(input.OrderID) || !validOptionalUUID(input.LedgerJournalID) || (admin && !validOptionalUUID(input.UserID)) {
			openAIError(c, http.StatusBadRequest, "invalid_request", "association identifiers are invalid or exceed their documented limits.")
			return
		}
		userID := claims.Subject
		if admin && strings.TrimSpace(input.UserID) != "" {
			userID = strings.TrimSpace(input.UserID)
		}
		organizationID := strings.TrimSpace(input.OrganizationID)
		if !admin && organizationID != "" {
			if err := d.Store.CheckOrganizationPricingAccess(c.Request.Context(), claims.Subject, organizationID); err != nil {
				openAIError(c, http.StatusForbidden, "organization_access_denied", "The organization is not available to this user.")
				return
			}
		}
		ticket, err := d.Store.CreateSupportTicket(c.Request.Context(), store.SupportTicketCreateRequest{
			Subject: input.Subject, Body: redactSupportText(input.Body), Priority: priority, UserID: userID,
			OrganizationID: organizationID, RequestID: strings.TrimSpace(input.RequestID), OrderID: strings.TrimSpace(input.OrderID), LedgerJournalID: strings.TrimSpace(input.LedgerJournalID),
			CreatedBy: claims.Subject, Context: redactSupportContext(input.Context),
		})
		respondCreated(c, ticket, err)
	})
	g.GET("/support/tickets/:id", func(c *gin.Context) {
		includeInternal := admin
		ticket, err := d.Store.SupportTicketByID(c.Request.Context(), c.Param("id"), includeInternal)
		if err == nil && !admin && ticket.UserID != claimsFrom(c).Subject {
			err = store.ErrNotFound
		}
		respond(c, ticket, err)
	})
	g.POST("/support/tickets/:id/messages", func(c *gin.Context) {
		var input struct {
			Body       string `json:"body"`
			Visibility string `json:"visibility"`
		}
		if c.ShouldBindJSON(&input) != nil || strings.TrimSpace(input.Body) == "" || len(input.Body) > 10000 {
			openAIError(c, http.StatusBadRequest, "invalid_request", "body is required and must be at most 10000 characters.")
			return
		}
		visibility := "PUBLIC"
		if admin && strings.EqualFold(input.Visibility, "INTERNAL") {
			visibility = "INTERNAL"
		}
		ticket, err := d.Store.SupportTicketByID(c.Request.Context(), c.Param("id"), true)
		if err == nil && !admin && ticket.UserID != claimsFrom(c).Subject {
			err = store.ErrNotFound
		}
		if err != nil {
			respond(c, nil, err)
			return
		}
		message, err := d.Store.AddSupportTicketMessage(c.Request.Context(), ticket.ID, claimsFrom(c).Subject, visibility, redactSupportText(input.Body))
		respondCreated(c, message, err)
	})
	if admin {
		g.PATCH("/support/tickets/:id", func(c *gin.Context) {
			var input struct {
				Status     string `json:"status"`
				Priority   string `json:"priority"`
				AssignedTo string `json:"assigned_to"`
			}
			if c.ShouldBindJSON(&input) != nil {
				openAIError(c, http.StatusBadRequest, "invalid_request", "status or priority is required.")
				return
			}
			input.Status = strings.ToUpper(strings.TrimSpace(input.Status))
			input.Priority = strings.ToUpper(strings.TrimSpace(input.Priority))
			if input.Status == "" {
				input.Status = "IN_PROGRESS"
			}
			if input.Priority == "" {
				input.Priority = "NORMAL"
			}
			if !validTicketStatus(input.Status) || !validTicketPriority(input.Priority) {
				openAIError(c, http.StatusBadRequest, "invalid_request", "status or priority is not supported.")
				return
			}
			if !validOptionalUUID(input.AssignedTo) {
				openAIError(c, http.StatusBadRequest, "invalid_request", "assigned_to must be a UUID when provided.")
				return
			}
			ticket, err := d.Store.UpdateSupportTicket(c.Request.Context(), c.Param("id"), input.Status, input.Priority, strings.TrimSpace(input.AssignedTo), claimsFrom(c).Subject)
			respond(c, ticket, err)
		})
	}
}

func validStatusComponent(value string) bool {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "GATEWAY", "DASHBOARD", "BILLING", "PROVIDER", "DATABASE", "REDIS", "PAYMENTS", "LEDGER":
		return true
	default:
		return false
	}
}

func validStatusValue(value string) bool {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "OPERATIONAL", "DEGRADED", "PARTIAL_OUTAGE", "MAJOR_OUTAGE", "MAINTENANCE":
		return true
	default:
		return false
	}
}

func validTicketStatus(value string) bool {
	switch value {
	case "OPEN", "IN_PROGRESS", "WAITING_FOR_USER", "RESOLVED", "CLOSED":
		return true
	default:
		return false
	}
}

func validTicketPriority(value string) bool {
	switch value {
	case "LOW", "NORMAL", "HIGH", "URGENT":
		return true
	default:
		return false
	}
}

func validOptionalUUID(value string) bool {
	value = strings.TrimSpace(value)
	return value == "" || uuid.Validate(value) == nil
}
