package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
)

func scanOrganization(row pgx.Row) (domain.Organization, error) {
	var out domain.Organization
	var metadata, allowedProviders, prohibitedProviders, requiredData []byte
	err := row.Scan(&out.ID, &out.Name, &out.Slug, &out.Status, &out.BillingRegion, &metadata, &out.CreatedAt, &out.UpdatedAt,
		&allowedProviders, &prohibitedProviders, &requiredData, &out.MinimumGrossMargin, &out.RiskScore, &out.VerificationLevel,
		&out.PaymentRisk, &out.AbuseStatus, &out.ManualReviewStatus, &out.NewAccountSpendLimit, &out.LegalHold)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	_ = json.Unmarshal(metadata, &out.Metadata)
	_ = json.Unmarshal(allowedProviders, &out.AllowedProviderIDs)
	_ = json.Unmarshal(prohibitedProviders, &out.ProhibitedProviderIDs)
	_ = json.Unmarshal(requiredData, &out.RequiredDataRegions)
	if out.Metadata == nil {
		out.Metadata = map[string]any{}
	}
	return out, nil
}

const organizationColumns = `id,name,slug,status,billing_region,metadata,created_at,updated_at,allowed_provider_ids,prohibited_provider_ids,required_data_regions,minimum_gross_margin::text,risk_score,verification_level,payment_risk,abuse_status,manual_review_status,COALESCE(new_account_spend_limit::text,''),legal_hold`

// organizationColumnsQualified intentionally qualifies each column explicitly.
// A simple comma replacement is unsafe here because the COALESCE expression
// contains its own comma and would produce invalid SQL.
const organizationColumnsQualified = `o.id,o.name,o.slug,o.status,o.billing_region,o.metadata,o.created_at,o.updated_at,o.allowed_provider_ids,o.prohibited_provider_ids,o.required_data_regions,o.minimum_gross_margin::text,o.risk_score,o.verification_level,o.payment_risk,o.abuse_status,o.manual_review_status,COALESCE(o.new_account_spend_limit::text,''),o.legal_hold`

func (s *Store) OrganizationByID(ctx context.Context, organizationID string) (domain.Organization, error) {
	return scanOrganization(s.pool.QueryRow(ctx, `SELECT `+organizationColumns+` FROM organizations WHERE id=$1`, organizationID))
}

// ListOrganizations returns all organizations for administrators when userID
// is nil, or only active memberships for the supplied user.
func (s *Store) ListOrganizations(ctx context.Context, userID *string, limit, offset int) ([]domain.Organization, error) {
	query := `SELECT ` + organizationColumnsQualified + ` FROM organizations o`
	args := []any{}
	if userID != nil {
		query += ` JOIN organization_memberships m ON m.organization_id=o.id AND m.user_id=$1 AND m.status='ACTIVE'`
		args = append(args, *userID)
	}
	args = append(args, clamp(limit), max(offset, 0))
	query += ` ORDER BY o.created_at,o.id LIMIT $` + itoa(len(args)-1) + ` OFFSET $` + itoa(len(args))
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.Organization, 0)
	for rows.Next() {
		organization, err := scanOrganization(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, organization)
	}
	return out, rows.Err()
}

func (s *Store) CreateOrganization(ctx context.Context, organization domain.Organization, ownerUserID string) (domain.Organization, error) {
	organization.ID = id.UUID()
	organization.Name = strings.TrimSpace(organization.Name)
	organization.Slug = strings.ToLower(strings.TrimSpace(organization.Slug))
	if organization.Status == "" {
		organization.Status = "ACTIVE"
	}
	if organization.Metadata == nil {
		organization.Metadata = map[string]any{}
	}
	if organization.AllowedProviderIDs == nil {
		organization.AllowedProviderIDs = []string{}
	}
	if organization.ProhibitedProviderIDs == nil {
		organization.ProhibitedProviderIDs = []string{}
	}
	if organization.RequiredDataRegions == nil {
		organization.RequiredDataRegions = []string{}
	}
	if strings.TrimSpace(organization.BillingRegion) == "" {
		organization.BillingRegion = "*"
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Organization{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO organizations(id,name,slug,status,billing_region,metadata,allowed_provider_ids,prohibited_provider_ids,required_data_regions,minimum_gross_margin) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		organization.ID, organization.Name, organization.Slug, organization.Status, organization.BillingRegion, jsonBytes(organization.Metadata), jsonBytes(organization.AllowedProviderIDs), jsonBytes(organization.ProhibitedProviderIDs), jsonBytes(organization.RequiredDataRegions), zeroIfEmpty(organization.MinimumGrossMargin)); err != nil {
		return domain.Organization{}, err
	}
	if ownerUserID != "" {
		if _, err = tx.Exec(ctx, `INSERT INTO organization_memberships(organization_id,user_id,role,status) VALUES($1,$2,'OWNER','ACTIVE')`, organization.ID, ownerUserID); err != nil {
			return domain.Organization{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.Organization{}, err
	}
	return s.OrganizationByID(ctx, organization.ID)
}

func (s *Store) UpdateOrganization(ctx context.Context, organization domain.Organization) (domain.Organization, error) {
	if organization.Metadata == nil {
		organization.Metadata = map[string]any{}
	}
	if organization.AllowedProviderIDs == nil {
		organization.AllowedProviderIDs = []string{}
	}
	if organization.ProhibitedProviderIDs == nil {
		organization.ProhibitedProviderIDs = []string{}
	}
	if organization.RequiredDataRegions == nil {
		organization.RequiredDataRegions = []string{}
	}
	if strings.TrimSpace(organization.BillingRegion) == "" {
		organization.BillingRegion = "*"
	}
	tag, err := s.pool.Exec(ctx, `UPDATE organizations SET name=$2,slug=lower($3),status=$4,billing_region=$5,metadata=$6,allowed_provider_ids=$7,prohibited_provider_ids=$8,required_data_regions=$9,minimum_gross_margin=$10,updated_at=now() WHERE id=$1`,
		organization.ID, strings.TrimSpace(organization.Name), strings.TrimSpace(organization.Slug), organization.Status, organization.BillingRegion, jsonBytes(organization.Metadata), jsonBytes(organization.AllowedProviderIDs), jsonBytes(organization.ProhibitedProviderIDs), jsonBytes(organization.RequiredDataRegions), zeroIfEmpty(organization.MinimumGrossMargin))
	if err != nil {
		return domain.Organization{}, err
	}
	if tag.RowsAffected() == 0 {
		return domain.Organization{}, ErrNotFound
	}
	return s.OrganizationByID(ctx, organization.ID)
}

func (s *Store) DeleteOrganization(ctx context.Context, organizationID string) error {
	if organizationID == domain.LegacyOrganizationID {
		return errors.New("the Legacy organization cannot be deleted")
	}
	tag, err := s.pool.Exec(ctx, `UPDATE organizations SET status='ARCHIVED',updated_at=now() WHERE id=$1 AND status<>'ARCHIVED'`, organizationID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) ListOrganizationMembers(ctx context.Context, organizationID string) ([]domain.OrganizationMembership, error) {
	rows, err := s.pool.Query(ctx, `SELECT m.organization_id,m.user_id,u.email,u.display_name,m.role,m.status,m.created_at,m.updated_at
		FROM organization_memberships m JOIN users u ON u.id=m.user_id
		WHERE m.organization_id=$1 ORDER BY u.email,u.id`, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.OrganizationMembership, 0)
	for rows.Next() {
		var member domain.OrganizationMembership
		if err := rows.Scan(&member.OrganizationID, &member.UserID, &member.Email, &member.DisplayName, &member.Role, &member.Status, &member.CreatedAt, &member.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, member)
	}
	return out, rows.Err()
}

func (s *Store) SetOrganizationMember(ctx context.Context, member domain.OrganizationMembership) error {
	if member.Status == "" {
		member.Status = "ACTIVE"
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = effectivePlanVersionTx(ctx, tx, member.OrganizationID); err != nil {
		return err
	}
	var currentRole, currentStatus string
	err = tx.QueryRow(ctx, `SELECT role,status FROM organization_memberships
		WHERE organization_id=$1 AND user_id=$2 FOR UPDATE`, member.OrganizationID, member.UserID).
		Scan(&currentRole, &currentStatus)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if member.Status == "ACTIVE" && (errors.Is(err, pgx.ErrNoRows) || currentStatus != "ACTIVE") {
		if limitErr := enforceOrganizationMemberActivationTx(ctx, tx, member.OrganizationID, member.UserID); limitErr != nil {
			return limitErr
		}
	}
	if err == nil && currentRole == "OWNER" && currentStatus == "ACTIVE" && (member.Role != "OWNER" || member.Status != "ACTIVE") {
		var otherOwners int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM organization_memberships
			WHERE organization_id=$1 AND role='OWNER' AND status='ACTIVE' AND user_id<>$2`, member.OrganizationID, member.UserID).
			Scan(&otherOwners); err != nil {
			return err
		}
		if otherOwners == 0 {
			return errors.New("an organization must retain at least one active owner")
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO organization_memberships(organization_id,user_id,role,status)
		VALUES($1,$2,$3,$4) ON CONFLICT(organization_id,user_id) DO UPDATE
		SET role=EXCLUDED.role,status=EXCLUDED.status,updated_at=now()`, member.OrganizationID, member.UserID, member.Role, member.Status)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) RemoveOrganizationMember(ctx context.Context, organizationID, userID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var role string
	if err = tx.QueryRow(ctx, `SELECT role FROM organization_memberships WHERE organization_id=$1 AND user_id=$2 FOR UPDATE`, organizationID, userID).Scan(&role); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if role == "OWNER" {
		var owners int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM organization_memberships WHERE organization_id=$1 AND role='OWNER' AND status='ACTIVE'`, organizationID).Scan(&owners); err != nil {
			return err
		}
		if owners <= 1 {
			return errors.New("an organization must retain at least one active owner")
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE project_memberships pm SET status='DISABLED',updated_at=now()
		FROM projects p WHERE pm.project_id=p.id AND p.organization_id=$1 AND pm.user_id=$2`, organizationID, userID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE organization_memberships SET status='DISABLED',updated_at=now()
		WHERE organization_id=$1 AND user_id=$2`, organizationID, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func scanProject(row pgx.Row) (domain.Project, error) {
	var out domain.Project
	var metadata []byte
	err := row.Scan(&out.ID, &out.OrganizationID, &out.Name, &out.Slug, &out.Description, &out.Status, &metadata, &out.CreatedAt, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	_ = json.Unmarshal(metadata, &out.Metadata)
	if out.Metadata == nil {
		out.Metadata = map[string]any{}
	}
	return out, nil
}

const projectColumns = `id,organization_id,name,slug,description,status,metadata,created_at,updated_at`

func (s *Store) ProjectByID(ctx context.Context, projectID string) (domain.Project, error) {
	return scanProject(s.pool.QueryRow(ctx, `SELECT `+projectColumns+` FROM projects WHERE id=$1`, projectID))
}

func (s *Store) ListProjects(ctx context.Context, organizationID string, userID *string, limit, offset int) ([]domain.Project, error) {
	query := `SELECT DISTINCT p.id,p.organization_id,p.name,p.slug,p.description,p.status,p.metadata,p.created_at,p.updated_at FROM projects p`
	args := []any{organizationID}
	if userID != nil {
		query += ` LEFT JOIN organization_memberships om ON om.organization_id=p.organization_id AND om.user_id=$2 AND om.status='ACTIVE'
			LEFT JOIN project_memberships pm ON pm.project_id=p.id AND pm.user_id=$2 AND pm.status='ACTIVE'`
		args = append(args, *userID)
		query += ` WHERE p.organization_id=$1 AND (om.role IN ('OWNER','ADMIN','VIEWER') OR pm.user_id IS NOT NULL)`
	} else {
		query += ` WHERE p.organization_id=$1`
	}
	args = append(args, clamp(limit), max(offset, 0))
	query += ` ORDER BY p.created_at,p.id LIMIT $` + itoa(len(args)-1) + ` OFFSET $` + itoa(len(args))
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.Project, 0)
	for rows.Next() {
		project, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, project)
	}
	return out, rows.Err()
}

func (s *Store) CreateProject(ctx context.Context, project domain.Project) (domain.Project, error) {
	project.ID = id.UUID()
	project.Name = strings.TrimSpace(project.Name)
	project.Slug = strings.ToLower(strings.TrimSpace(project.Slug))
	if project.Status == "" {
		project.Status = "ACTIVE"
	}
	if project.Metadata == nil {
		project.Metadata = map[string]any{}
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO projects(id,organization_id,name,slug,description,status,metadata) VALUES($1,$2,$3,$4,$5,$6,$7)`,
		project.ID, project.OrganizationID, project.Name, project.Slug, project.Description, project.Status, jsonBytes(project.Metadata))
	if err != nil {
		return domain.Project{}, err
	}
	return s.ProjectByID(ctx, project.ID)
}

func (s *Store) UpdateProject(ctx context.Context, project domain.Project) (domain.Project, error) {
	if project.Metadata == nil {
		project.Metadata = map[string]any{}
	}
	tag, err := s.pool.Exec(ctx, `UPDATE projects SET name=$2,slug=lower($3),description=$4,status=$5,metadata=$6,updated_at=now() WHERE id=$1 AND organization_id=$7`,
		project.ID, strings.TrimSpace(project.Name), strings.TrimSpace(project.Slug), project.Description, project.Status, jsonBytes(project.Metadata), project.OrganizationID)
	if err != nil {
		return domain.Project{}, err
	}
	if tag.RowsAffected() == 0 {
		return domain.Project{}, ErrNotFound
	}
	return s.ProjectByID(ctx, project.ID)
}

func (s *Store) DeleteProject(ctx context.Context, projectID string) error {
	if projectID == domain.LegacyProjectID {
		return errors.New("the Legacy project cannot be deleted")
	}
	tag, err := s.pool.Exec(ctx, `UPDATE projects SET status='ARCHIVED',updated_at=now() WHERE id=$1 AND status<>'ARCHIVED'`, projectID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) ListProjectMembers(ctx context.Context, projectID string) ([]domain.ProjectMembership, error) {
	rows, err := s.pool.Query(ctx, `SELECT m.organization_id,m.project_id,m.user_id,u.email,u.display_name,m.role,m.status,m.created_at,m.updated_at
		FROM project_memberships m JOIN users u ON u.id=m.user_id
		WHERE m.project_id=$1 ORDER BY u.email,u.id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.ProjectMembership, 0)
	for rows.Next() {
		var member domain.ProjectMembership
		if err := rows.Scan(&member.OrganizationID, &member.ProjectID, &member.UserID, &member.Email, &member.DisplayName, &member.Role, &member.Status, &member.CreatedAt, &member.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, member)
	}
	return out, rows.Err()
}

func (s *Store) SetProjectMember(ctx context.Context, member domain.ProjectMembership) error {
	if member.Status == "" {
		member.Status = "ACTIVE"
	}
	// Project membership cannot escape the parent organization.  Require an
	// active organization membership in the same transaction.
	tag, err := s.pool.Exec(ctx, `INSERT INTO project_memberships(organization_id,project_id,user_id,role,status)
		SELECT p.organization_id,p.id,$2,$3,$4 FROM projects p
		JOIN organization_memberships om ON om.organization_id=p.organization_id AND om.user_id=$2 AND om.status='ACTIVE'
		WHERE p.id=$1
		ON CONFLICT(project_id,user_id) DO UPDATE SET role=EXCLUDED.role,status=EXCLUDED.status,updated_at=now()`,
		member.ProjectID, member.UserID, member.Role, member.Status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) RemoveProjectMember(ctx context.Context, projectID, userID string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE project_memberships SET status='DISABLED',updated_at=now()
		WHERE project_id=$1 AND user_id=$2 AND status<>'DISABLED'`, projectID, userID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// CheckProjectAccess resolves the effective tenant role. minimumRole accepts
// VIEWER, DEVELOPER, ADMIN, or OWNER.  Global SUPER_ADMIN is allowed, while a
// disabled user/membership always fails closed with ErrNotFound to avoid
// leaking cross-tenant identifiers.
func (s *Store) CheckProjectAccess(ctx context.Context, userID, projectID, minimumRole string) (domain.ProjectAccess, error) {
	var access domain.ProjectAccess
	var globalRole, userStatus string
	var organizationRole, organizationStatus, projectRole, projectStatus *string
	err := s.pool.QueryRow(ctx, `SELECT p.organization_id,p.id,u.role,u.status,om.role,om.status,pm.role,pm.status
		FROM projects p JOIN organizations o ON o.id=p.organization_id CROSS JOIN users u
		LEFT JOIN organization_memberships om ON om.organization_id=p.organization_id AND om.user_id=u.id
		LEFT JOIN project_memberships pm ON pm.project_id=p.id AND pm.user_id=u.id
		WHERE p.id=$1 AND u.id=$2 AND p.status='ACTIVE' AND o.status='ACTIVE'`, projectID, userID).
		Scan(&access.OrganizationID, &access.ProjectID, &globalRole, &userStatus, &organizationRole, &organizationStatus, &projectRole, &projectStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ProjectAccess{}, ErrNotFound
	}
	if err != nil {
		return domain.ProjectAccess{}, err
	}
	if userStatus != "ACTIVE" {
		return domain.ProjectAccess{}, ErrNotFound
	}
	if globalRole != "SUPER_ADMIN" && (organizationStatus == nil || *organizationStatus != "ACTIVE") {
		return domain.ProjectAccess{}, ErrNotFound
	}
	if organizationRole != nil && organizationStatus != nil && *organizationStatus == "ACTIVE" {
		access.OrganizationRole = *organizationRole
	}
	if projectRole != nil && projectStatus != nil && *projectStatus == "ACTIVE" {
		access.ProjectRole = *projectRole
	}

	effective := ""
	if globalRole == "SUPER_ADMIN" {
		effective = "OWNER"
	} else if access.OrganizationRole == "OWNER" || access.OrganizationRole == "ADMIN" {
		effective = access.OrganizationRole
	} else if access.ProjectRole != "" {
		effective = access.ProjectRole
	} else if access.OrganizationRole == "VIEWER" {
		effective = "VIEWER"
	}
	roleRank := map[string]int{"VIEWER": 1, "MEMBER": 1, "DEVELOPER": 2, "ADMIN": 3, "OWNER": 4}
	if roleRank[effective] < roleRank[strings.ToUpper(minimumRole)] {
		return domain.ProjectAccess{}, ErrNotFound
	}
	return access, nil
}

func scanProjectRoute(row pgx.Row) (domain.ProjectModelRoute, error) {
	var out domain.ProjectModelRoute
	var routeConfig, fallbackConfig []byte
	err := row.Scan(&out.ID, &out.OrganizationID, &out.ProjectID, &out.ModelRouteID, &out.Alias, &out.Enabled,
		&routeConfig, &out.ProviderID, &out.ProviderType, &out.ProviderBaseURL, &out.UpstreamModel, &out.CredentialGroupID,
		&out.FallbackGroupID, &out.RoutingPolicy, &fallbackConfig, &out.CreatedAt, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	_ = json.Unmarshal(routeConfig, &out.RoutingConfig)
	_ = json.Unmarshal(fallbackConfig, &out.FallbackConfig)
	if out.RoutingConfig == nil {
		out.RoutingConfig = map[string]any{}
	}
	if out.FallbackConfig == nil {
		out.FallbackConfig = map[string]any{}
	}
	return out, nil
}

const projectRouteSelect = `SELECT pmr.id,p.organization_id,pmr.project_id,pmr.model_route_id,pmr.alias,pmr.enabled,
	pmr.routing_config,r.provider_id,pr.provider_type,pr.base_url,r.upstream_model,r.credential_group_id,r.fallback_group_id,
	r.routing_policy,r.fallback_config,pmr.created_at,pmr.updated_at
	FROM project_model_routes pmr JOIN projects p ON p.id=pmr.project_id
	JOIN model_routes r ON r.id=pmr.model_route_id JOIN providers pr ON pr.id=r.provider_id`

func (s *Store) ProjectRouteByAlias(ctx context.Context, projectID, alias string) (domain.ProjectModelRoute, error) {
	return scanProjectRoute(s.pool.QueryRow(ctx, projectRouteSelect+` WHERE pmr.project_id=$1 AND pmr.alias=$2 AND pmr.deleted_at IS NULL AND pmr.enabled AND r.enabled AND pr.enabled`, projectID, alias))
}

func (s *Store) ListProjectRoutes(ctx context.Context, projectID string) ([]domain.ProjectModelRoute, error) {
	rows, err := s.pool.Query(ctx, projectRouteSelect+` WHERE pmr.project_id=$1 AND pmr.deleted_at IS NULL ORDER BY pmr.alias,pmr.id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.ProjectModelRoute, 0)
	for rows.Next() {
		route, err := scanProjectRoute(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, route)
	}
	return out, rows.Err()
}

func (s *Store) UpsertProjectRoute(ctx context.Context, route domain.ProjectModelRoute) (domain.ProjectModelRoute, error) {
	if route.ID == "" {
		route.ID = id.UUID()
	}
	if route.Alias == "" {
		if err := s.pool.QueryRow(ctx, `SELECT alias FROM model_routes WHERE id=$1`, route.ModelRouteID).Scan(&route.Alias); errors.Is(err, pgx.ErrNoRows) {
			return domain.ProjectModelRoute{}, ErrNotFound
		} else if err != nil {
			return domain.ProjectModelRoute{}, err
		}
	}
	if route.RoutingConfig == nil {
		route.RoutingConfig = map[string]any{}
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO project_model_routes(id,project_id,model_route_id,alias,enabled,routing_config)
		VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(project_id,alias) DO UPDATE
		SET model_route_id=EXCLUDED.model_route_id,enabled=EXCLUDED.enabled,routing_config=EXCLUDED.routing_config,
			deleted_at=NULL,updated_at=now()`,
		route.ID, route.ProjectID, route.ModelRouteID, route.Alias, route.Enabled, jsonBytes(route.RoutingConfig))
	if err != nil {
		return domain.ProjectModelRoute{}, err
	}
	// Administrative mutations must return the persisted row even when the
	// route was just disabled. ProjectRouteByAlias intentionally filters out
	// disabled rows because it is used by the gateway's active-route lookup.
	return scanProjectRoute(s.pool.QueryRow(ctx, projectRouteSelect+` WHERE pmr.project_id=$1 AND pmr.alias=$2`, route.ProjectID, route.Alias))
}

func (s *Store) RemoveProjectRoute(ctx context.Context, projectID, routeID string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE project_model_routes
		SET enabled=false,deleted_at=now(),updated_at=now()
		WHERE project_id=$1 AND id=$2 AND deleted_at IS NULL`, projectID, routeID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func scanBudgetPolicy(row pgx.Row) (domain.ProjectBudgetPolicy, error) {
	var out domain.ProjectBudgetPolicy
	var costLimit *string
	var alertThreshold string
	err := row.Scan(&out.ID, &out.OrganizationID, &out.ProjectID, &out.Name, &out.Period, &out.TokenLimit,
		&costLimit, &alertThreshold, &out.EnforceHardLimit, &out.Status, &out.CreatedAt, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	out.CostLimit = decimalFromStringPointer(costLimit)
	out.AlertThreshold = domain.Decimal(alertThreshold)
	return out, err
}

const budgetPolicySelect = `SELECT b.id,p.organization_id,b.project_id,b.name,b.period,b.token_limit,COALESCE(b.cost_limit_exact,b.cost_limit)::text,
	b.alert_threshold::text,b.enforce_hard_limit,b.status,b.created_at,b.updated_at
	FROM project_budget_policies b JOIN projects p ON p.id=b.project_id`

func (s *Store) ListProjectBudgetPolicies(ctx context.Context, projectID string) ([]domain.ProjectBudgetPolicy, error) {
	rows, err := s.pool.Query(ctx, budgetPolicySelect+` WHERE b.project_id=$1 ORDER BY b.name,b.id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.ProjectBudgetPolicy, 0)
	for rows.Next() {
		policy, err := scanBudgetPolicy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, policy)
	}
	return out, rows.Err()
}

func (s *Store) UpsertProjectBudgetPolicy(ctx context.Context, policy domain.ProjectBudgetPolicy) (domain.ProjectBudgetPolicy, error) {
	if policy.ID == "" {
		policy.ID = id.UUID()
	}
	if policy.Period == "" {
		policy.Period = "MONTHLY"
	}
	if policy.Status == "" {
		policy.Status = "ACTIVE"
	}
	if policy.AlertThreshold.IsZero() {
		policy.AlertThreshold = domain.Decimal("0.8")
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO project_budget_policies(id,project_id,name,period,token_limit,cost_limit,cost_limit_exact,alert_threshold,enforce_hard_limit,status)
		VALUES($1,$2,$3,$4,$5,round($6::numeric,8),$6,$7,$8,$9) ON CONFLICT(project_id,name) DO UPDATE SET
		period=EXCLUDED.period,token_limit=EXCLUDED.token_limit,cost_limit=EXCLUDED.cost_limit,cost_limit_exact=EXCLUDED.cost_limit_exact,
		alert_threshold=EXCLUDED.alert_threshold,enforce_hard_limit=EXCLUDED.enforce_hard_limit,status=EXCLUDED.status,updated_at=now()`,
		policy.ID, policy.ProjectID, policy.Name, policy.Period, policy.TokenLimit, decimalPointer(policy.CostLimit),
		policy.AlertThreshold.String(), policy.EnforceHardLimit, policy.Status)
	if err != nil {
		return domain.ProjectBudgetPolicy{}, err
	}
	return scanBudgetPolicy(s.pool.QueryRow(ctx, budgetPolicySelect+` WHERE b.project_id=$1 AND b.name=$2`, policy.ProjectID, policy.Name))
}

func (s *Store) DeleteProjectBudgetPolicy(ctx context.Context, projectID, policyID string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM project_budget_policies WHERE project_id=$1 AND id=$2`, projectID, policyID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) ProjectUsage(ctx context.Context, projectID string, from, to time.Time) (domain.ProjectBudgetUsage, error) {
	var out domain.ProjectBudgetUsage
	out.ProjectID = projectID
	out.From = from.UTC()
	out.To = to.UTC()
	var cost string
	err := s.pool.QueryRow(ctx, `SELECT p.organization_id,COALESCE(sum(u.requests),0),COALESCE(sum(u.input_tokens),0),
		COALESCE(sum(u.cached_input_tokens),0),COALESCE(sum(u.output_tokens),0),COALESCE(sum(u.cost_exact),0)::text,COALESCE(sum(u.errors),0)
		FROM projects p LEFT JOIN usage_daily u ON u.project_id=p.id
			AND u.date >= ($2::timestamptz AT TIME ZONE 'UTC')::date
			AND u.date < ($3::timestamptz AT TIME ZONE 'UTC')::date
		WHERE p.id=$1 GROUP BY p.organization_id`, projectID, out.From, out.To).
		Scan(&out.OrganizationID, &out.Requests, &out.InputTokens, &out.CachedTokens, &out.OutputTokens, &cost, &out.Errors)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ProjectBudgetUsage{}, ErrNotFound
	}
	out.Cost = domain.Decimal(cost)
	out.TotalTokens = out.InputTokens + out.OutputTokens
	return out, err
}

func (s *Store) ProjectBudgetUsage(ctx context.Context, projectID, period string, at time.Time) (domain.ProjectBudgetUsage, error) {
	at = at.UTC()
	var from, to time.Time
	switch strings.ToUpper(period) {
	case "DAILY":
		from = time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)
		to = from.AddDate(0, 0, 1)
	case "MONTHLY", "":
		period = "MONTHLY"
		from = time.Date(at.Year(), at.Month(), 1, 0, 0, 0, 0, time.UTC)
		to = from.AddDate(0, 1, 0)
	default:
		return domain.ProjectBudgetUsage{}, errors.New("invalid budget period")
	}
	out, err := s.ProjectUsage(ctx, projectID, from, to)
	out.Period = strings.ToUpper(period)
	return out, err
}

func scanBudgetEvent(row pgx.Row) (domain.BudgetEvent, error) {
	var out domain.BudgetEvent
	var metadata []byte
	var cost string
	err := row.Scan(&out.ID, &out.OrganizationID, &out.ProjectID, &out.PolicyID, &out.UserID, &out.APIKeyID,
		&out.RequestID, &out.EventType, &out.Tokens, &cost, &out.IdempotencyKey, &metadata, &out.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	out.Cost = domain.Decimal(cost)
	_ = json.Unmarshal(metadata, &out.Metadata)
	if out.Metadata == nil {
		out.Metadata = map[string]any{}
	}
	return out, nil
}

const budgetEventColumns = `id,organization_id,project_id,policy_id,user_id,api_key_id,COALESCE(request_id,''),event_type,tokens,cost_exact::text,COALESCE(idempotency_key,''),metadata,created_at`

func (s *Store) CreateBudgetEvent(ctx context.Context, event domain.BudgetEvent) (domain.BudgetEvent, error) {
	if event.ID == "" {
		event.ID = id.UUID()
	}
	if event.Metadata == nil {
		event.Metadata = map[string]any{}
	}
	if event.OrganizationID == "" {
		if err := s.pool.QueryRow(ctx, `SELECT organization_id FROM projects WHERE id=$1`, event.ProjectID).Scan(&event.OrganizationID); errors.Is(err, pgx.ErrNoRows) {
			return domain.BudgetEvent{}, ErrNotFound
		} else if err != nil {
			return domain.BudgetEvent{}, err
		}
	}
	tag, err := s.pool.Exec(ctx, `INSERT INTO budget_events(id,organization_id,project_id,policy_id,user_id,api_key_id,request_id,event_type,tokens,cost,cost_exact,idempotency_key,metadata)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,round($10::numeric,8),$10,$11,$12) ON CONFLICT DO NOTHING`, event.ID, event.OrganizationID,
		event.ProjectID, event.PolicyID, event.UserID, event.APIKeyID, nullString(event.RequestID), event.EventType,
		event.Tokens, event.Cost.String(), nullString(event.IdempotencyKey), jsonBytes(event.Metadata))
	if err != nil {
		return domain.BudgetEvent{}, err
	}
	if tag.RowsAffected() == 0 && event.IdempotencyKey != "" {
		return scanBudgetEvent(s.pool.QueryRow(ctx, `SELECT `+budgetEventColumns+` FROM budget_events WHERE project_id=$1 AND idempotency_key=$2 AND event_type=$3`, event.ProjectID, event.IdempotencyKey, event.EventType))
	}
	return scanBudgetEvent(s.pool.QueryRow(ctx, `SELECT `+budgetEventColumns+` FROM budget_events WHERE id=$1`, event.ID))
}

func (s *Store) ListBudgetEvents(ctx context.Context, projectID string, from, to time.Time, limit, offset int) ([]domain.BudgetEvent, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+budgetEventColumns+` FROM budget_events
		WHERE project_id=$1 AND created_at >= $2 AND created_at < $3 ORDER BY created_at DESC,id DESC LIMIT $4 OFFSET $5`,
		projectID, from.UTC(), to.UTC(), clamp(limit), max(offset, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.BudgetEvent, 0)
	for rows.Next() {
		event, err := scanBudgetEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	return out, rows.Err()
}
