package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
)

func scanMarketplaceListing(row pgx.Row) (domain.MarketplaceListing, error) {
	var listing domain.MarketplaceListing
	var models, price, metadata []byte
	err := row.Scan(&listing.ID, &listing.ProviderID, &listing.ProviderName, &listing.Endpoint, &models, &price,
		&listing.Status, &listing.Uptime, &listing.Verified, &metadata, &listing.CreatedAt, &listing.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return listing, ErrNotFound
	}
	if err == nil {
		_ = json.Unmarshal(models, &listing.SupportedModels)
		_ = json.Unmarshal(price, &listing.Price)
		_ = json.Unmarshal(metadata, &listing.Metadata)
		if listing.SupportedModels == nil {
			listing.SupportedModels = []string{}
		}
		if listing.Price == nil {
			listing.Price = map[string]any{}
		}
		if listing.Metadata == nil {
			listing.Metadata = map[string]any{}
		}
	}
	return listing, err
}

const marketplaceColumns = `l.id,l.provider_id,p.name,l.endpoint,l.supported_models,l.price,l.status,l.uptime::float8,
	l.verified,l.metadata,l.created_at,l.updated_at`

func (s *Store) ListMarketplaceListings(ctx context.Context) ([]domain.MarketplaceListing, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+marketplaceColumns+` FROM provider_marketplace_listings l
		JOIN providers p ON p.id=l.provider_id ORDER BY l.status,l.uptime DESC,p.name,l.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.MarketplaceListing
	for rows.Next() {
		listing, err := scanMarketplaceListing(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, listing)
	}
	return out, rows.Err()
}

func (s *Store) UpsertMarketplaceListing(ctx context.Context, listing domain.MarketplaceListing) (domain.MarketplaceListing, error) {
	if listing.SupportedModels == nil {
		listing.SupportedModels = []string{}
	}
	if listing.Price == nil {
		listing.Price = map[string]any{}
	}
	if listing.Metadata == nil {
		listing.Metadata = map[string]any{}
	}
	if listing.ID != "" {
		tag, err := s.pool.Exec(ctx, `UPDATE provider_marketplace_listings SET provider_id=$2,endpoint=$3,supported_models=$4,
			price=$5,status=$6,uptime=$7,verified=$8,metadata=$9,updated_at=now() WHERE id=$1`, listing.ID,
			listing.ProviderID, listing.Endpoint, jsonBytes(listing.SupportedModels), jsonBytes(listing.Price), listing.Status,
			listing.Uptime, listing.Verified, jsonBytes(listing.Metadata))
		if err == nil && tag.RowsAffected() == 0 {
			return domain.MarketplaceListing{}, ErrNotFound
		}
		if err != nil {
			return domain.MarketplaceListing{}, err
		}
		return scanMarketplaceListing(s.pool.QueryRow(ctx, `SELECT `+marketplaceColumns+` FROM provider_marketplace_listings l
			JOIN providers p ON p.id=l.provider_id WHERE l.id=$1`, listing.ID))
	}
	listing.ID = id.UUID()
	_, err := s.pool.Exec(ctx, `INSERT INTO provider_marketplace_listings(id,provider_id,endpoint,supported_models,price,
		status,uptime,verified,metadata) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT(provider_id,endpoint) DO UPDATE SET supported_models=EXCLUDED.supported_models,price=EXCLUDED.price,
		status=EXCLUDED.status,uptime=EXCLUDED.uptime,verified=EXCLUDED.verified,metadata=EXCLUDED.metadata,updated_at=now()`,
		listing.ID, listing.ProviderID, listing.Endpoint, jsonBytes(listing.SupportedModels), jsonBytes(listing.Price),
		listing.Status, listing.Uptime, listing.Verified, jsonBytes(listing.Metadata))
	if err != nil {
		return domain.MarketplaceListing{}, err
	}
	return scanMarketplaceListing(s.pool.QueryRow(ctx, `SELECT `+marketplaceColumns+` FROM provider_marketplace_listings l
		JOIN providers p ON p.id=l.provider_id WHERE l.provider_id=$1 AND l.endpoint=$2`, listing.ProviderID, listing.Endpoint))
}

func (s *Store) DeleteMarketplaceListing(ctx context.Context, listingID string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM provider_marketplace_listings WHERE id=$1`, listingID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func scanTeam(row pgx.Row) (domain.Team, error) {
	var team domain.Team
	var metadata []byte
	var monthlyCostLimit *string
	err := row.Scan(&team.ID, &team.OrganizationID, &team.Name, &team.Slug, &team.Status, &team.MonthlyTokenLimit,
		&monthlyCostLimit, &metadata, &team.CreatedAt, &team.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return team, ErrNotFound
	}
	if err == nil {
		team.MonthlyCostLimit, err = decimalFromStringPointer(monthlyCostLimit)
		if err != nil {
			return team, err
		}
		_ = json.Unmarshal(metadata, &team.Metadata)
		if team.Metadata == nil {
			team.Metadata = map[string]any{}
		}
	}
	return team, err
}

const teamColumns = `id,organization_id,name,slug,status,monthly_token_limit,COALESCE(monthly_cost_limit_exact,monthly_cost_limit)::text,metadata,created_at,updated_at`

func (s *Store) ListTeams(ctx context.Context, organizationID string) ([]domain.Team, error) {
	query := `SELECT ` + teamColumns + ` FROM teams`
	args := []any{}
	if organizationID != "" {
		query += ` WHERE organization_id=$1`
		args = append(args, organizationID)
	}
	query += ` ORDER BY organization_id,name,id`
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Team
	for rows.Next() {
		team, err := scanTeam(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, team)
	}
	return out, rows.Err()
}

func (s *Store) TeamByID(ctx context.Context, teamID string) (domain.Team, error) {
	return scanTeam(s.pool.QueryRow(ctx, `SELECT `+teamColumns+` FROM teams WHERE id=$1`, teamID))
}

func (s *Store) TeamMonthlyUsage(ctx context.Context, teamID string) (int64, domain.Decimal, error) {
	var tokens int64
	var cost string
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(sum(r.input_tokens+r.output_tokens),0),
		COALESCE(sum(r.estimated_cost_exact),0)::text FROM request_logs r JOIN api_keys k ON k.id=r.api_key_id
		WHERE k.team_id=$1 AND r.created_at>=date_trunc('month',now())`, teamID).Scan(&tokens, &cost)
	if err != nil {
		return 0, "", err
	}
	parsedCost, err := parseStoredDecimal(cost, "request_logs.estimated_cost.team_sum")
	return tokens, parsedCost, err
}

func (s *Store) UpsertTeam(ctx context.Context, team domain.Team) (domain.Team, error) {
	if team.Slug == "" {
		team.Slug = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(team.Name), " ", "-"))
	}
	if team.Status == "" {
		team.Status = "ACTIVE"
	}
	if team.Metadata == nil {
		team.Metadata = map[string]any{}
	}
	if team.ID != "" {
		tag, err := s.pool.Exec(ctx, `UPDATE teams SET organization_id=$2,name=$3,slug=$4,status=$5,monthly_token_limit=$6,
			monthly_cost_limit=round($7::numeric,8),monthly_cost_limit_exact=$7,metadata=$8,updated_at=now() WHERE id=$1`, team.ID, team.OrganizationID, team.Name,
			team.Slug, team.Status, team.MonthlyTokenLimit, decimalPointer(team.MonthlyCostLimit), jsonBytes(team.Metadata))
		if err == nil && tag.RowsAffected() == 0 {
			return domain.Team{}, ErrNotFound
		}
		if err != nil {
			return domain.Team{}, err
		}
		return s.TeamByID(ctx, team.ID)
	}
	team.ID = id.UUID()
	_, err := s.pool.Exec(ctx, `INSERT INTO teams(id,organization_id,name,slug,status,monthly_token_limit,monthly_cost_limit,monthly_cost_limit_exact,metadata)
		VALUES($1,$2,$3,$4,$5,$6,round($7::numeric,8),$7,$8) ON CONFLICT(organization_id,slug) DO UPDATE SET name=EXCLUDED.name,
		status=EXCLUDED.status,monthly_token_limit=EXCLUDED.monthly_token_limit,monthly_cost_limit=EXCLUDED.monthly_cost_limit,monthly_cost_limit_exact=EXCLUDED.monthly_cost_limit_exact,
		metadata=EXCLUDED.metadata,updated_at=now()`, team.ID, team.OrganizationID, team.Name, team.Slug, team.Status,
		team.MonthlyTokenLimit, decimalPointer(team.MonthlyCostLimit), jsonBytes(team.Metadata))
	if err != nil {
		return domain.Team{}, err
	}
	return scanTeam(s.pool.QueryRow(ctx, `SELECT `+teamColumns+` FROM teams WHERE organization_id=$1 AND slug=$2`, team.OrganizationID, team.Slug))
}

func (s *Store) DeleteTeam(ctx context.Context, teamID string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE teams SET status='ARCHIVED',updated_at=now() WHERE id=$1 AND status<>'ARCHIVED'`, teamID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) ListTeamMembers(ctx context.Context, teamID string) ([]domain.TeamMembership, error) {
	rows, err := s.pool.Query(ctx, `SELECT tm.team_id,tm.organization_id,tm.user_id,u.email,u.display_name,tm.role,
		tm.status,tm.created_at,tm.updated_at FROM team_memberships tm JOIN users u ON u.id=tm.user_id
		WHERE tm.team_id=$1 ORDER BY u.email,tm.user_id`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.TeamMembership
	for rows.Next() {
		var member domain.TeamMembership
		if err := rows.Scan(&member.TeamID, &member.OrganizationID, &member.UserID, &member.Email, &member.DisplayName,
			&member.Role, &member.Status, &member.CreatedAt, &member.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, member)
	}
	return out, rows.Err()
}

func (s *Store) UpsertTeamMember(ctx context.Context, member domain.TeamMembership) (domain.TeamMembership, error) {
	if member.Role == "" {
		member.Role = "MEMBER"
	}
	if member.Status == "" {
		member.Status = "ACTIVE"
	}
	if err := s.pool.QueryRow(ctx, `SELECT organization_id FROM teams WHERE id=$1 AND status<>'ARCHIVED'`, member.TeamID).Scan(&member.OrganizationID); errors.Is(err, pgx.ErrNoRows) {
		return domain.TeamMembership{}, ErrNotFound
	} else if err != nil {
		return domain.TeamMembership{}, err
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO team_memberships(team_id,organization_id,user_id,role,status)
		VALUES($1,$2,$3,$4,$5) ON CONFLICT(team_id,user_id) DO UPDATE SET role=EXCLUDED.role,status=EXCLUDED.status,updated_at=now()`,
		member.TeamID, member.OrganizationID, member.UserID, member.Role, member.Status)
	if err != nil {
		return domain.TeamMembership{}, err
	}
	members, err := s.ListTeamMembers(ctx, member.TeamID)
	if err != nil {
		return domain.TeamMembership{}, err
	}
	for _, stored := range members {
		if stored.UserID == member.UserID {
			return stored, nil
		}
	}
	return domain.TeamMembership{}, ErrNotFound
}

func (s *Store) DeleteTeamMember(ctx context.Context, teamID, userID string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM team_memberships WHERE team_id=$1 AND user_id=$2`, teamID, userID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}
