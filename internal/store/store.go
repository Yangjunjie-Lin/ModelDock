package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/relayedock/relayedock/internal/auth"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
	"github.com/relayedock/relayedock/migrations"
)

var (
	ErrNotFound           = errors.New("not found")
	ErrInvalidToken       = errors.New("invalid or expired token")
	ErrInviteRequired     = errors.New("a valid invitation is required")
	ErrRegistrationClosed = errors.New("registration is closed")
	ErrPasswordRequired   = errors.New("a password is required")
	ErrMFAReplay          = errors.New("MFA code was already used")
	ErrRiskRestricted     = errors.New("risk policy restricts this operation")
	ErrRiskFrozen         = errors.New("risk policy has frozen this account or key")
	ErrKeyCreationRate    = errors.New("API key creation rate limit exceeded")

	// ErrCredentialGroupProviderMismatch is returned when a credential is
	// assigned to a group owned by another provider.
	ErrCredentialGroupProviderMismatch = errors.New("credential and credential group providers do not match")

	// ErrModelRouteProviderMismatch is returned when a route points at a
	// primary or fallback credential group owned by another provider.
	ErrModelRouteProviderMismatch = errors.New("model route and credential group providers do not match")
)

type Store struct{ pool *pgxpool.Pool }

func Open(ctx context.Context, databaseURL string) (*Store, error) {
	return OpenWithPoolConfig(ctx, databaseURL, PoolConfig{})
}

type PoolConfig struct {
	MaxConns        int32
	MinConns        int32
	MaxConnIdleTime time.Duration
	MaxConnLifetime time.Duration
}

func OpenWithPoolConfig(ctx context.Context, databaseURL string, poolConfig PoolConfig) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	if poolConfig.MaxConns <= 0 {
		poolConfig.MaxConns = 20
	}
	if poolConfig.MinConns <= 0 {
		poolConfig.MinConns = 2
	}
	if poolConfig.MaxConnIdleTime <= 0 {
		poolConfig.MaxConnIdleTime = 5 * time.Minute
	}
	if poolConfig.MaxConnLifetime <= 0 {
		poolConfig.MaxConnLifetime = 30 * time.Minute
	}
	cfg.MaxConns = poolConfig.MaxConns
	cfg.MinConns = poolConfig.MinConns
	cfg.MaxConnIdleTime = poolConfig.MaxConnIdleTime
	cfg.MaxConnLifetime = poolConfig.MaxConnLifetime
	// Financial business dates and monthly boundaries are defined in UTC. Set
	// the PostgreSQL session explicitly so deployment host/database time zones
	// cannot move timestamptz evidence into a neighboring accounting period.
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	cfg.ConnConfig.RuntimeParams["timezone"] = "UTC"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close()                         { s.pool.Close() }
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// VerifySchemaCurrent is used by stateless application replicas when an
// external migration job owns DDL. It requires every migration known to this
// binary and validates checksums, while allowing newer additive versions so an
// older replica can stay online during a rolling compatibility window.
func (s *Store) VerifySchemaCurrent(ctx context.Context) error {
	for _, migration := range migrations.All {
		sum := sha256.Sum256([]byte(migration.SQL))
		var checksum string
		err := s.pool.QueryRow(ctx, `SELECT checksum FROM schema_migrations WHERE version=$1`, migration.Version).Scan(&checksum)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("database migration %04d_%s has not been applied", migration.Version, migration.Name)
		}
		if err != nil {
			return fmt.Errorf("verify database migration %04d_%s: %w", migration.Version, migration.Name, err)
		}
		if checksum != hex.EncodeToString(sum[:]) {
			return fmt.Errorf("migration %04d_%s checksum mismatch", migration.Version, migration.Name)
		}
	}
	return nil
}

func (s *Store) Migrate(ctx context.Context) error {
	if len(migrations.All) == 0 {
		return errors.New("no database migrations are embedded")
	}
	knownVersions := make(map[int64]struct{}, len(migrations.All))
	var previousVersion int64
	for _, migration := range migrations.All {
		if migration.Version <= previousVersion || migration.Name == "" || migration.SQL == "" {
			return fmt.Errorf("invalid migration manifest at version %d", migration.Version)
		}
		knownVersions[migration.Version] = struct{}{}
		previousVersion = migration.Version
	}

	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	locked := false
	defer func() {
		if !locked {
			conn.Release()
			return
		}
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var unlocked bool
		unlockErr := conn.QueryRow(unlockCtx, `SELECT pg_advisory_unlock($1)`, int64(0x52444f434b)).Scan(&unlocked)
		if unlockErr != nil || !unlocked {
			// Never return a session that may still own the advisory lock to
			// the pool. Hijacking and closing guarantees the server releases it.
			raw := conn.Hijack()
			_ = raw.Close(unlockCtx)
			return
		}
		conn.Release()
	}()

	// A session advisory lock serializes startup across replicas.  Each
	// migration still has its own transaction, so a failed statement cannot
	// leave a partially-applied version behind.
	const migrationLockID int64 = 0x52444f434b // "RDOCK"
	if _, err = conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLockID); err != nil {
		return fmt.Errorf("lock database migrations: %w", err)
	}
	locked = true

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin migration metadata transaction: %w", err)
	}
	if _, err = tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version bigint PRIMARY KEY,
		name text NOT NULL,
		checksum text NOT NULL,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit migration metadata: %w", err)
	}
	rows, err := conn.Query(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		return fmt.Errorf("list applied migrations: %w", err)
	}
	for rows.Next() {
		var version int64
		if err = rows.Scan(&version); err != nil {
			rows.Close()
			return fmt.Errorf("read applied migration: %w", err)
		}
		if _, ok := knownVersions[version]; !ok {
			rows.Close()
			return fmt.Errorf("database contains unknown schema migration version %d", version)
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("list applied migrations: %w", err)
	}
	rows.Close()

	var newestApplied int64
	if err = conn.QueryRow(ctx, `SELECT COALESCE(max(version),0) FROM schema_migrations`).Scan(&newestApplied); err != nil {
		return fmt.Errorf("read migration version: %w", err)
	}
	latestKnown := migrations.All[len(migrations.All)-1].Version
	if newestApplied > latestKnown {
		return fmt.Errorf("database schema version %d is newer than this RelayDock binary (latest %d)", newestApplied, latestKnown)
	}

	for _, migration := range migrations.All {
		sum := sha256.Sum256([]byte(migration.SQL))
		checksum := hex.EncodeToString(sum[:])
		tx, err = conn.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin migration %04d_%s: %w", migration.Version, migration.Name, err)
		}

		var recordedChecksum string
		err = tx.QueryRow(ctx, `SELECT checksum FROM schema_migrations WHERE version=$1 FOR UPDATE`, migration.Version).Scan(&recordedChecksum)
		switch {
		case err == nil:
			if recordedChecksum != checksum {
				_ = tx.Rollback(ctx)
				return fmt.Errorf("migration %04d_%s checksum mismatch", migration.Version, migration.Name)
			}
			if err = tx.Commit(ctx); err != nil {
				return fmt.Errorf("finish migration %04d_%s check: %w", migration.Version, migration.Name, err)
			}
			continue
		case !errors.Is(err, pgx.ErrNoRows):
			_ = tx.Rollback(ctx)
			return fmt.Errorf("inspect migration %04d_%s: %w", migration.Version, migration.Name, err)
		}

		if _, err = tx.Exec(ctx, migration.SQL); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply migration %04d_%s: %w", migration.Version, migration.Name, err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO schema_migrations(version,name,checksum) VALUES($1,$2,$3)`, migration.Version, migration.Name, checksum); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("record migration %04d_%s: %w", migration.Version, migration.Name, err)
		}
		if err = tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit migration %04d_%s: %w", migration.Version, migration.Name, err)
		}
	}
	return nil
}

func (s *Store) BootstrapAdmin(ctx context.Context, email, password, displayName string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var adminID string
	err = tx.QueryRow(ctx, `SELECT id FROM users WHERE role IN ('SUPER_ADMIN','ADMIN') ORDER BY created_at,id LIMIT 1 FOR UPDATE`).Scan(&adminID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		if len(password) < 12 {
			return errors.New("RELAYDOCK_ADMIN_PASSWORD must contain at least 12 characters for first startup")
		}
		hash, hashErr := auth.HashPassword(password)
		if hashErr != nil {
			return hashErr
		}
		adminID = id.UUID()
		tag, insertErr := tx.Exec(ctx, `INSERT INTO users (id,email,password_hash,display_name,role,status,email_verified_at)
			VALUES ($1,$2,$3,$4,'SUPER_ADMIN','ACTIVE',now()) ON CONFLICT (email) DO NOTHING`, adminID, email, hash, displayName)
		if insertErr != nil {
			return insertErr
		}
		if tag.RowsAffected() == 0 {
			return errors.New("RELAYDOCK_ADMIN_EMAIL already belongs to a non-administrator; choose a different bootstrap email")
		}
	}

	if _, err = tx.Exec(ctx, `INSERT INTO organization_memberships(organization_id,user_id,role,status)
		VALUES($1,$2,'OWNER','ACTIVE') ON CONFLICT(organization_id,user_id) DO UPDATE
		SET role='OWNER',status='ACTIVE',updated_at=now()`, domain.LegacyOrganizationID, adminID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO project_memberships(organization_id,project_id,user_id,role,status)
		VALUES($1,$2,$3,'ADMIN','ACTIVE') ON CONFLICT(project_id,user_id) DO UPDATE
		SET organization_id=EXCLUDED.organization_id,role='ADMIN',status='ACTIVE',updated_at=now()`,
		domain.LegacyOrganizationID, domain.LegacyProjectID, adminID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func jsonBytes(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func scanUser(row pgx.Row) (domain.User, error) {
	var u domain.User
	var monthlyCostLimit *string
	err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.DisplayName, &u.Role, &u.Status,
		&u.MonthlyTokenLimit, &monthlyCostLimit, &u.CreatedAt, &u.UpdatedAt, &u.LastLoginAt,
		&u.EmailVerifiedAt, &u.SessionVersion, &u.MFAEnabled, &u.RiskScore, &u.VerificationLevel,
		&u.PaymentRisk, &u.AbuseStatus, &u.ManualReviewStatus, &u.NewAccountSpendLimit, &u.ClosedAt, &u.LegalHold)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, ErrNotFound
	}
	if err != nil {
		return u, err
	}
	u.MonthlyCostLimit, err = decimalFromStringPointer(monthlyCostLimit)
	return u, err
}

const userColumns = `id,email,password_hash,display_name,role,status,monthly_token_limit,COALESCE(monthly_cost_limit_exact,monthly_cost_limit)::text,created_at,updated_at,last_login_at,email_verified_at,session_version,(totp_enrolled_at IS NOT NULL),risk_score,verification_level,payment_risk,abuse_status,manual_review_status,COALESCE(new_account_spend_limit::text,''),closed_at,legal_hold`

func (s *Store) UserByEmail(ctx context.Context, email string) (domain.User, error) {
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE lower(email)=lower($1)`, email))
}

func (s *Store) UserByID(ctx context.Context, userID string) (domain.User, error) {
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id=$1`, userID))
}

func (s *Store) TouchLogin(ctx context.Context, userID string) {
	_, _ = s.pool.Exec(ctx, `UPDATE users SET last_login_at=now() WHERE id=$1`, userID)
}

func (s *Store) UpgradePasswordHash(ctx context.Context, userID, hash string) {
	_, _ = s.pool.Exec(ctx, `UPDATE users SET password_hash=$2,updated_at=now() WHERE id=$1 AND password_hash LIKE '$2%'`, userID, hash)
}

func (s *Store) ListUsers(ctx context.Context, limit, offset int) ([]domain.User, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+userColumns+` FROM users ORDER BY created_at DESC LIMIT $1 OFFSET $2`, clamp(limit), max(offset, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.User, 0)
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) CreateUser(ctx context.Context, email, password, displayName, role string) (domain.User, error) {
	hash, err := auth.HashPassword(password)
	if err != nil {
		return domain.User{}, err
	}
	if role != "ADMIN" && role != "USER" {
		role = "USER"
	}
	userID := id.UUID()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.User{}, err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `INSERT INTO users(id,email,password_hash,display_name,role,status,email_verified_at) VALUES($1,lower($2),$3,$4,$5,'ACTIVE',now())`, userID, email, hash, displayName, role)
	if err != nil {
		return domain.User{}, err
	}
	organizationRole, projectRole := "MEMBER", "DEVELOPER"
	if role == "ADMIN" {
		organizationRole, projectRole = "ADMIN", "ADMIN"
	}
	if err = enforceOrganizationMemberActivationTx(ctx, tx, domain.LegacyOrganizationID, userID); err != nil {
		return domain.User{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO organization_memberships(organization_id,user_id,role,status) VALUES($1,$2,$3,'ACTIVE')`,
		domain.LegacyOrganizationID, userID, organizationRole); err != nil {
		return domain.User{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO project_memberships(organization_id,project_id,user_id,role,status) VALUES($1,$2,$3,$4,'ACTIVE')`,
		domain.LegacyOrganizationID, domain.LegacyProjectID, userID, projectRole); err != nil {
		return domain.User{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.User{}, err
	}
	return s.UserByID(ctx, userID)
}

func (s *Store) UpdateUserStatus(ctx context.Context, userID, status string) error {
	if status == "DISABLED" {
		status = "SUSPENDED"
	}
	if status != "ACTIVE" && status != "SUSPENDED" && status != "CLOSED" {
		return errors.New("invalid user status")
	}
	tag, err := s.pool.Exec(ctx, `UPDATE users SET status=$2,session_version=session_version+1,updated_at=now() WHERE id=$1`, userID, status)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func clamp(n int) int {
	if n <= 0 {
		return 50
	}
	if n > 200 {
		return 200
	}
	return n
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
