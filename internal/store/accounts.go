package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
)

type RegistrationResult struct {
	User    domain.User
	Created bool
	Active  bool
}

func (s *Store) RegisterUser(
	ctx context.Context,
	email, passwordHash, displayName, mode string,
	registrationDigest, organizationInviteDigest, verificationDigest []byte,
	verificationExpires time.Time,
	outbox domain.EmailOutbox,
	ip string,
) (RegistrationResult, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	displayName = strings.TrimSpace(displayName)
	if mode == "CLOSED" {
		return RegistrationResult{}, ErrRegistrationClosed
	}
	if email == "" || passwordHash == "" {
		return RegistrationResult{}, errors.New("email and password hash are required")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RegistrationResult{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, email); err != nil {
		return RegistrationResult{}, err
	}

	var invitationID, organizationID, invitationRole string
	hasOrganizationInvite := len(organizationInviteDigest) == 32
	if hasOrganizationInvite {
		var invitationEmail string
		err = tx.QueryRow(ctx, `SELECT id,organization_id,email,role FROM organization_invitations
			WHERE token_digest=$1 AND status='PENDING' AND expires_at>now() FOR UPDATE`, organizationInviteDigest).
			Scan(&invitationID, &organizationID, &invitationEmail, &invitationRole)
		if errors.Is(err, pgx.ErrNoRows) {
			return RegistrationResult{}, ErrInviteRequired
		}
		if err != nil {
			return RegistrationResult{}, err
		}
		if !strings.EqualFold(invitationEmail, email) {
			return RegistrationResult{}, ErrInviteRequired
		}
	}
	if mode == "INVITE_ONLY" && !hasOrganizationInvite {
		var registrationInviteID string
		err = tx.QueryRow(ctx, `SELECT id FROM registration_invites
			WHERE code_digest=$1 AND status='ACTIVE' AND expires_at>now() AND used_count<max_uses
			FOR UPDATE`, registrationDigest).Scan(&registrationInviteID)
		if errors.Is(err, pgx.ErrNoRows) {
			return RegistrationResult{}, ErrInviteRequired
		}
		if err != nil {
			return RegistrationResult{}, err
		}
	}

	var existingID string
	err = tx.QueryRow(ctx, `SELECT id FROM users WHERE lower(email)=$1 FOR UPDATE`, email).Scan(&existingID)
	if err == nil {
		if err = tx.Commit(ctx); err != nil {
			return RegistrationResult{}, err
		}
		existing, lookupErr := s.UserByID(ctx, existingID)
		return RegistrationResult{User: existing, Active: existing.Status == "ACTIVE"}, lookupErr
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return RegistrationResult{}, err
	}
	if mode == "INVITE_ONLY" && !hasOrganizationInvite {
		tag, inviteErr := tx.Exec(ctx, `UPDATE registration_invites SET used_count=used_count+1,
				status=CASE WHEN used_count+1>=max_uses THEN 'EXHAUSTED' ELSE status END,updated_at=now()
			WHERE code_digest=$1 AND status='ACTIVE' AND used_count<max_uses`, registrationDigest)
		if inviteErr != nil {
			return RegistrationResult{}, inviteErr
		}
		if tag.RowsAffected() == 0 {
			return RegistrationResult{}, ErrInviteRequired
		}
	}

	userID := id.UUID()
	status := "PENDING_VERIFICATION"
	if hasOrganizationInvite {
		status = "ACTIVE"
	}
	if displayName == "" {
		displayName = "ModelDock User"
	}
	_, err = tx.Exec(ctx, `INSERT INTO users(id,email,password_hash,display_name,role,status,email_verified_at)
		VALUES($1,$2,$3,$4,'USER',$5,CASE WHEN $5='ACTIVE' THEN now() ELSE NULL END)`,
		userID, email, passwordHash, displayName, status)
	if err != nil {
		return RegistrationResult{}, err
	}
	acquisitionSource := "SELF_REGISTRATION"
	if hasOrganizationInvite {
		acquisitionSource = "INVITATION"
	}
	if _, err = tx.Exec(ctx, `SELECT record_commercial_funnel_event(
		'REGISTERED',$1::uuid,NULL,NULL,'user',$1::uuid::text,'funnel:registered:'||$1::uuid::text,now(),$2::jsonb)`,
		userID, jsonBytes(map[string]any{"acquisition_source": acquisitionSource, "registration_mode": mode})); err != nil {
		return RegistrationResult{}, err
	}
	if status == "ACTIVE" {
		if _, err = tx.Exec(ctx, `SELECT record_commercial_funnel_event(
			'EMAIL_VERIFIED',$1::uuid,NULL,NULL,'user',$1::uuid::text,'funnel:email_verified:'||$1::uuid::text,now(),$2::jsonb)`,
			userID, jsonBytes(map[string]any{"acquisition_source": acquisitionSource, "verification_source": "INVITATION"})); err != nil {
			return RegistrationResult{}, err
		}
	}
	if hasOrganizationInvite {
		if err = enforceOrganizationMemberActivationTx(ctx, tx, organizationID, userID); err != nil {
			return RegistrationResult{}, err
		}
		if _, err = tx.Exec(ctx, `UPDATE organization_invitations SET status='ACCEPTED',accepted_by=$2,
			responded_at=now(),updated_at=now() WHERE id=$1 AND status='PENDING'`, invitationID, userID); err != nil {
			return RegistrationResult{}, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO organization_memberships(organization_id,user_id,role,status)
			VALUES($1,$2,$3,'ACTIVE') ON CONFLICT(organization_id,user_id) DO UPDATE
			SET role=CASE WHEN organization_memberships.role='OWNER' THEN 'OWNER' ELSE EXCLUDED.role END,
				status='ACTIVE',updated_at=now()`, organizationID, userID, invitationRole); err != nil {
			return RegistrationResult{}, err
		}
		if err = ensurePersonalWorkspace(ctx, tx, userID, displayName); err != nil {
			return RegistrationResult{}, err
		}
	} else {
		if len(verificationDigest) != 32 {
			return RegistrationResult{}, errors.New("verification token digest is required")
		}
		if _, err = tx.Exec(ctx, `INSERT INTO account_tokens(id,user_id,purpose,token_digest,expires_at)
			VALUES($1,$2,'EMAIL_VERIFICATION',$3,$4)`, id.UUID(), userID, verificationDigest, verificationExpires); err != nil {
			return RegistrationResult{}, err
		}
		if err = enqueueEmailTx(ctx, tx, outbox); err != nil {
			return RegistrationResult{}, err
		}
	}
	if err = insertSecurityAudit(ctx, tx, userID, "security.registration_created", "user", userID, ip,
		map[string]any{"mode": mode, "status": status}); err != nil {
		return RegistrationResult{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return RegistrationResult{}, err
	}
	user, err := s.UserByID(ctx, userID)
	return RegistrationResult{User: user, Created: true, Active: status == "ACTIVE"}, err
}

func (s *Store) ResendVerification(ctx context.Context, email string, digest []byte, expires time.Time, outbox domain.EmailOutbox, ip string) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var userID string
	err = tx.QueryRow(ctx, `SELECT id FROM users WHERE lower(email)=lower($1) AND status='PENDING_VERIFICATION' FOR UPDATE`, email).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, tx.Commit(ctx)
	}
	if err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE account_tokens SET consumed_at=COALESCE(consumed_at,now())
		WHERE user_id=$1 AND purpose='EMAIL_VERIFICATION' AND consumed_at IS NULL`, userID); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO account_tokens(id,user_id,purpose,token_digest,expires_at)
		VALUES($1,$2,'EMAIL_VERIFICATION',$3,$4)`, id.UUID(), userID, digest, expires); err != nil {
		return false, err
	}
	if err = enqueueEmailTx(ctx, tx, outbox); err != nil {
		return false, err
	}
	if err = insertSecurityAudit(ctx, tx, userID, "security.verification_resent", "user", userID, ip, nil); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func (s *Store) VerifyEmail(ctx context.Context, digest []byte, ip string) (domain.User, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.User{}, err
	}
	defer tx.Rollback(ctx)
	var tokenID, userID, displayName string
	err = tx.QueryRow(ctx, `SELECT t.id,t.user_id,u.display_name FROM account_tokens t
		JOIN users u ON u.id=t.user_id
		WHERE t.token_digest=$1 AND t.purpose='EMAIL_VERIFICATION' AND t.consumed_at IS NULL
			AND t.expires_at>now() FOR UPDATE OF t,u`, digest).Scan(&tokenID, &userID, &displayName)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, ErrInvalidToken
	}
	if err != nil {
		return domain.User{}, err
	}
	tag, err := tx.Exec(ctx, `UPDATE account_tokens SET consumed_at=now() WHERE id=$1 AND consumed_at IS NULL`, tokenID)
	if err != nil || tag.RowsAffected() != 1 {
		if err != nil {
			return domain.User{}, err
		}
		return domain.User{}, ErrInvalidToken
	}
	tag, err = tx.Exec(ctx, `UPDATE users SET status='ACTIVE',email_verified_at=COALESCE(email_verified_at,now()),updated_at=now()
		WHERE id=$1 AND status='PENDING_VERIFICATION'`, userID)
	if err != nil {
		return domain.User{}, err
	}
	if tag.RowsAffected() != 1 {
		return domain.User{}, ErrInvalidToken
	}
	if err = ensurePersonalWorkspace(ctx, tx, userID, displayName); err != nil {
		return domain.User{}, err
	}
	if err = insertSecurityAudit(ctx, tx, userID, "security.email_verified", "user", userID, ip, nil); err != nil {
		return domain.User{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.User{}, err
	}
	return s.UserByID(ctx, userID)
}

func (s *Store) RequestPasswordReset(ctx context.Context, email string, digest []byte, expires time.Time, outbox domain.EmailOutbox, ip string) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var userID string
	err = tx.QueryRow(ctx, `SELECT id FROM users WHERE lower(email)=lower($1) AND status='ACTIVE' FOR UPDATE`, email).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, tx.Commit(ctx)
	}
	if err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE account_tokens SET consumed_at=COALESCE(consumed_at,now())
		WHERE user_id=$1 AND purpose='PASSWORD_RESET' AND consumed_at IS NULL`, userID); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO account_tokens(id,user_id,purpose,token_digest,expires_at)
		VALUES($1,$2,'PASSWORD_RESET',$3,$4)`, id.UUID(), userID, digest, expires); err != nil {
		return false, err
	}
	if err = enqueueEmailTx(ctx, tx, outbox); err != nil {
		return false, err
	}
	if err = insertSecurityAudit(ctx, tx, userID, "security.password_reset_requested", "user", userID, ip, nil); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func (s *Store) ResetPassword(ctx context.Context, digest []byte, passwordHash, ip string) (string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var tokenID, userID string
	err = tx.QueryRow(ctx, `SELECT id,user_id FROM account_tokens
		WHERE token_digest=$1 AND purpose='PASSWORD_RESET' AND consumed_at IS NULL AND expires_at>now()
		FOR UPDATE`, digest).Scan(&tokenID, &userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrInvalidToken
	}
	if err != nil {
		return "", err
	}
	tag, err := tx.Exec(ctx, `UPDATE account_tokens SET consumed_at=now() WHERE id=$1 AND consumed_at IS NULL`, tokenID)
	if err != nil || tag.RowsAffected() != 1 {
		if err != nil {
			return "", err
		}
		return "", ErrInvalidToken
	}
	tag, err = tx.Exec(ctx, `UPDATE users SET password_hash=$2,session_version=session_version+1,updated_at=now()
		WHERE id=$1 AND status='ACTIVE'`, userID, passwordHash)
	if err != nil {
		return "", err
	}
	if tag.RowsAffected() != 1 {
		return "", ErrInvalidToken
	}
	if _, err = tx.Exec(ctx, `UPDATE account_tokens SET consumed_at=COALESCE(consumed_at,now())
		WHERE user_id=$1 AND purpose='PASSWORD_RESET'`, userID); err != nil {
		return "", err
	}
	if err = insertSecurityAudit(ctx, tx, userID, "security.password_reset_completed", "user", userID, ip, nil); err != nil {
		return "", err
	}
	return userID, tx.Commit(ctx)
}

func (s *Store) ChangePassword(ctx context.Context, userID, expectedHash, newHash, ip string) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var version int64
	err = tx.QueryRow(ctx, `UPDATE users SET password_hash=$3,session_version=session_version+1,updated_at=now()
		WHERE id=$1 AND password_hash=$2 AND status='ACTIVE' RETURNING session_version`,
		userID, expectedHash, newHash).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrInvalidToken
	}
	if err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `UPDATE account_tokens SET consumed_at=COALESCE(consumed_at,now())
		WHERE user_id=$1 AND consumed_at IS NULL`, userID); err != nil {
		return 0, err
	}
	if err = insertSecurityAudit(ctx, tx, userID, "security.password_changed", "user", userID, ip, nil); err != nil {
		return 0, err
	}
	return version, tx.Commit(ctx)
}

func (s *Store) RevokeOtherSessions(ctx context.Context, userID, ip string) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var version int64
	err = tx.QueryRow(ctx, `UPDATE users SET session_version=session_version+1,updated_at=now()
		WHERE id=$1 AND status='ACTIVE' RETURNING session_version`, userID).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	if err = insertSecurityAudit(ctx, tx, userID, "security.other_sessions_revoked", "user", userID, ip, nil); err != nil {
		return 0, err
	}
	return version, tx.Commit(ctx)
}

func (s *Store) UpdateUserStatusAudited(ctx context.Context, userID, status, actor, ip string) error {
	if status == "DISABLED" {
		status = "SUSPENDED"
	}
	if status != "ACTIVE" && status != "SUSPENDED" && status != "CLOSED" {
		return errors.New("invalid user status")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE users SET status=$2,session_version=session_version+1,updated_at=now()
		WHERE id=$1 AND status<>$2`, userID, status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	action := "security.account_status_changed"
	if status == "SUSPENDED" {
		action = "security.account_suspended"
	} else if status == "ACTIVE" {
		action = "security.account_restored"
	} else if status == "CLOSED" {
		action = "security.account_closed"
	}
	if err = insertSecurityAudit(ctx, tx, actor, action, "user", userID, ip, map[string]any{"status": status}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func scanOrganizationInvitation(row pgx.Row) (domain.OrganizationInvitation, error) {
	var invitation domain.OrganizationInvitation
	err := row.Scan(&invitation.ID, &invitation.OrganizationID, &invitation.OrganizationName, &invitation.Email,
		&invitation.Role, &invitation.Status, &invitation.ExpiresAt, &invitation.InvitedBy,
		&invitation.AcceptedBy, &invitation.RespondedAt, &invitation.CreatedAt, &invitation.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return invitation, ErrNotFound
	}
	return invitation, err
}

const organizationInvitationColumns = `i.id,i.organization_id,o.name,i.email,i.role,i.status,i.expires_at,
	i.invited_by,i.accepted_by,i.responded_at,i.created_at,i.updated_at`

func (s *Store) CreateOrganizationInvitation(
	ctx context.Context,
	organizationID, email, role, invitedBy string,
	tokenDigest []byte,
	expires time.Time,
	outbox domain.EmailOutbox,
	ip string,
) (domain.OrganizationInvitation, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.OrganizationInvitation{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, organizationID+":"+email); err != nil {
		return domain.OrganizationInvitation{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE organization_invitations SET status='REVOKED',responded_at=now(),updated_at=now()
		WHERE organization_id=$1 AND lower(email)=$2 AND status='PENDING'`, organizationID, email); err != nil {
		return domain.OrganizationInvitation{}, err
	}
	invitationID := id.UUID()
	_, err = tx.Exec(ctx, `INSERT INTO organization_invitations(id,organization_id,email,role,token_digest,expires_at,invited_by)
		VALUES($1,$2,$3,$4,$5,$6,$7)`, invitationID, organizationID, email, role, tokenDigest, expires, invitedBy)
	if err != nil {
		return domain.OrganizationInvitation{}, err
	}
	if err = enqueueEmailTx(ctx, tx, outbox); err != nil {
		return domain.OrganizationInvitation{}, err
	}
	if err = insertSecurityAudit(ctx, tx, invitedBy, "security.organization_invitation_created",
		"organization_invitation", invitationID, ip, map[string]any{"organization_id": organizationID, "role": role}); err != nil {
		return domain.OrganizationInvitation{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.OrganizationInvitation{}, err
	}
	return s.OrganizationInvitationByID(ctx, invitationID)
}

func (s *Store) OrganizationInvitationByID(ctx context.Context, invitationID string) (domain.OrganizationInvitation, error) {
	return scanOrganizationInvitation(s.pool.QueryRow(ctx, `SELECT `+organizationInvitationColumns+
		` FROM organization_invitations i JOIN organizations o ON o.id=i.organization_id WHERE i.id=$1`, invitationID))
}

func (s *Store) OrganizationInvitationByDigest(ctx context.Context, digest []byte) (domain.OrganizationInvitation, error) {
	return scanOrganizationInvitation(s.pool.QueryRow(ctx, `SELECT `+organizationInvitationColumns+
		` FROM organization_invitations i JOIN organizations o ON o.id=i.organization_id
		WHERE i.token_digest=$1 AND i.status='PENDING' AND i.expires_at>now()`, digest))
}

func (s *Store) ListOrganizationInvitations(ctx context.Context, organizationID string, limit, offset int) ([]domain.OrganizationInvitation, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+organizationInvitationColumns+
		` FROM organization_invitations i JOIN organizations o ON o.id=i.organization_id
		WHERE i.organization_id=$1 ORDER BY i.created_at DESC,i.id LIMIT $2 OFFSET $3`,
		organizationID, clamp(limit), max(offset, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.OrganizationInvitation, 0)
	for rows.Next() {
		invitation, scanErr := scanOrganizationInvitation(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, invitation)
	}
	return out, rows.Err()
}

func (s *Store) ListUserInvitations(ctx context.Context, userID string) ([]domain.OrganizationInvitation, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+organizationInvitationColumns+
		` FROM organization_invitations i JOIN organizations o ON o.id=i.organization_id
		JOIN users u ON u.id=$1 AND lower(u.email)=lower(i.email)
		WHERE i.status='PENDING' AND i.expires_at>now() ORDER BY i.created_at DESC,i.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.OrganizationInvitation, 0)
	for rows.Next() {
		invitation, scanErr := scanOrganizationInvitation(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, invitation)
	}
	return out, rows.Err()
}

func (s *Store) AcceptOrganizationInvitation(ctx context.Context, digest []byte, passwordHash, displayName string, allowCreate bool, ip string) (domain.User, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.User{}, err
	}
	defer tx.Rollback(ctx)
	var invitationID, organizationID, email, role, userID string
	err = tx.QueryRow(ctx, `SELECT id,organization_id,email,role FROM organization_invitations
		WHERE token_digest=$1 AND status='PENDING' AND expires_at>now() FOR UPDATE`, digest).
		Scan(&invitationID, &organizationID, &email, &role)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, ErrInvalidToken
	}
	if err != nil {
		return domain.User{}, err
	}
	var status string
	err = tx.QueryRow(ctx, `SELECT id,status,display_name FROM users WHERE lower(email)=lower($1) FOR UPDATE`, email).
		Scan(&userID, &status, &displayName)
	if errors.Is(err, pgx.ErrNoRows) {
		if !allowCreate {
			return domain.User{}, ErrRegistrationClosed
		}
		if passwordHash == "" {
			return domain.User{}, ErrPasswordRequired
		}
		userID = id.UUID()
		if strings.TrimSpace(displayName) == "" {
			displayName = "ModelDock User"
		}
		if _, err = tx.Exec(ctx, `INSERT INTO users(id,email,password_hash,display_name,role,status,email_verified_at)
			VALUES($1,lower($2),$3,$4,'USER','ACTIVE',now())`, userID, email, passwordHash, displayName); err != nil {
			return domain.User{}, err
		}
	} else if err != nil {
		return domain.User{}, err
	} else if status == "CLOSED" || status == "SUSPENDED" {
		return domain.User{}, ErrInvalidToken
	} else if status == "PENDING_VERIFICATION" {
		if _, err = tx.Exec(ctx, `UPDATE users SET status='ACTIVE',email_verified_at=COALESCE(email_verified_at,now()),updated_at=now()
			WHERE id=$1`, userID); err != nil {
			return domain.User{}, err
		}
	}
	if err = enforceOrganizationMemberActivationTx(ctx, tx, organizationID, userID); err != nil {
		return domain.User{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO organization_memberships(organization_id,user_id,role,status)
		VALUES($1,$2,$3,'ACTIVE') ON CONFLICT(organization_id,user_id) DO UPDATE
		SET role=CASE WHEN organization_memberships.role='OWNER' THEN 'OWNER' ELSE EXCLUDED.role END,
			status='ACTIVE',updated_at=now()`, organizationID, userID, role); err != nil {
		return domain.User{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE organization_invitations SET status='ACCEPTED',accepted_by=$2,
		responded_at=now(),updated_at=now() WHERE id=$1 AND status='PENDING'`, invitationID, userID); err != nil {
		return domain.User{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE account_tokens SET consumed_at=COALESCE(consumed_at,now())
		WHERE user_id=$1 AND purpose='EMAIL_VERIFICATION'`, userID); err != nil {
		return domain.User{}, err
	}
	if err = ensurePersonalWorkspace(ctx, tx, userID, displayName); err != nil {
		return domain.User{}, err
	}
	if err = insertSecurityAudit(ctx, tx, userID, "security.organization_invitation_accepted",
		"organization_invitation", invitationID, ip, map[string]any{"organization_id": organizationID}); err != nil {
		return domain.User{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.User{}, err
	}
	return s.UserByID(ctx, userID)
}

func (s *Store) RespondOrganizationInvitation(ctx context.Context, userID, invitationID string, accept bool, ip string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var organizationID, role string
	err = tx.QueryRow(ctx, `SELECT i.organization_id,i.role FROM organization_invitations i
		JOIN users u ON u.id=$1 AND lower(u.email)=lower(i.email)
		WHERE i.id=$2 AND i.status='PENDING' AND i.expires_at>now() FOR UPDATE OF i`, userID, invitationID).
		Scan(&organizationID, &role)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalidToken
	}
	if err != nil {
		return err
	}
	status, action := "REJECTED", "security.organization_invitation_rejected"
	if accept {
		status, action = "ACCEPTED", "security.organization_invitation_accepted"
		if err = enforceOrganizationMemberActivationTx(ctx, tx, organizationID, userID); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO organization_memberships(organization_id,user_id,role,status)
			VALUES($1,$2,$3,'ACTIVE') ON CONFLICT(organization_id,user_id) DO UPDATE
			SET role=CASE WHEN organization_memberships.role='OWNER' THEN 'OWNER' ELSE EXCLUDED.role END,
				status='ACTIVE',updated_at=now()`, organizationID, userID, role); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE organization_invitations SET status=$3,
		accepted_by=CASE WHEN $3='ACCEPTED' THEN $1::uuid ELSE NULL END,responded_at=now(),updated_at=now()
		WHERE id=$2 AND status='PENDING'`, userID, invitationID, status); err != nil {
		return err
	}
	if err = insertSecurityAudit(ctx, tx, userID, action, "organization_invitation", invitationID, ip,
		map[string]any{"organization_id": organizationID}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) RevokeOrganizationInvitation(ctx context.Context, organizationID, invitationID, actor, ip string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE organization_invitations SET status='REVOKED',responded_at=now(),updated_at=now()
		WHERE id=$1 AND organization_id=$2 AND status='PENDING'`, invitationID, organizationID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err = insertSecurityAudit(ctx, tx, actor, "security.organization_invitation_revoked",
		"organization_invitation", invitationID, ip, map[string]any{"organization_id": organizationID}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) RejectOrganizationInvitation(ctx context.Context, digest []byte, ip string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var invitationID string
	err = tx.QueryRow(ctx, `UPDATE organization_invitations SET status='REJECTED',responded_at=now(),updated_at=now()
		WHERE token_digest=$1 AND status='PENDING' AND expires_at>now() RETURNING id`, digest).Scan(&invitationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalidToken
	}
	if err != nil {
		return err
	}
	if err = insertSecurityAudit(ctx, tx, "", "security.organization_invitation_rejected",
		"organization_invitation", invitationID, ip, nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) CreateRegistrationInvite(ctx context.Context, digest []byte, maxUses int, expires time.Time, actor, ip string) (domain.RegistrationInvite, error) {
	invite := domain.RegistrationInvite{ID: id.UUID(), Status: "ACTIVE", MaxUses: maxUses, ExpiresAt: expires}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return invite, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO registration_invites(id,code_digest,max_uses,expires_at,created_by)
		VALUES($1,$2,$3,$4,$5)`, invite.ID, digest, maxUses, expires, actor); err != nil {
		return invite, err
	}
	if err = insertSecurityAudit(ctx, tx, actor, "security.registration_invite_created",
		"registration_invite", invite.ID, ip, map[string]any{"max_uses": maxUses, "expires_at": expires}); err != nil {
		return invite, err
	}
	if err = tx.Commit(ctx); err != nil {
		return invite, err
	}
	return s.RegistrationInviteByID(ctx, invite.ID)
}

func scanRegistrationInvite(row pgx.Row) (domain.RegistrationInvite, error) {
	var invite domain.RegistrationInvite
	err := row.Scan(&invite.ID, &invite.Status, &invite.MaxUses, &invite.UsedCount, &invite.ExpiresAt,
		&invite.CreatedBy, &invite.CreatedAt, &invite.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return invite, ErrNotFound
	}
	return invite, err
}

const registrationInviteColumns = `id,status,max_uses,used_count,expires_at,created_by,created_at,updated_at`

func (s *Store) RegistrationInviteByID(ctx context.Context, inviteID string) (domain.RegistrationInvite, error) {
	return scanRegistrationInvite(s.pool.QueryRow(ctx, `SELECT `+registrationInviteColumns+
		` FROM registration_invites WHERE id=$1`, inviteID))
}

func (s *Store) ListRegistrationInvites(ctx context.Context, limit, offset int) ([]domain.RegistrationInvite, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+registrationInviteColumns+
		` FROM registration_invites ORDER BY created_at DESC,id LIMIT $1 OFFSET $2`, clamp(limit), max(offset, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.RegistrationInvite, 0)
	for rows.Next() {
		invite, scanErr := scanRegistrationInvite(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, invite)
	}
	return out, rows.Err()
}

func (s *Store) RevokeRegistrationInvite(ctx context.Context, inviteID, actor, ip string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE registration_invites SET status='REVOKED',updated_at=now()
		WHERE id=$1 AND status='ACTIVE'`, inviteID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err = insertSecurityAudit(ctx, tx, actor, "security.registration_invite_revoked",
		"registration_invite", inviteID, ip, nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) SetPendingTOTP(ctx context.Context, userID string, encrypted []byte, expires time.Time, ip string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE users SET totp_pending_secret_encrypted=$2,
		totp_pending_expires_at=$3,updated_at=now() WHERE id=$1 AND role IN ('ADMIN','SUPER_ADMIN')
		AND status='ACTIVE'`, userID, encrypted, expires)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err = insertSecurityAudit(ctx, tx, userID, "security.mfa_enrollment_started", "user", userID, ip, nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) PendingTOTP(ctx context.Context, userID string) ([]byte, time.Time, error) {
	var encrypted []byte
	var expires time.Time
	err := s.pool.QueryRow(ctx, `SELECT totp_pending_secret_encrypted,totp_pending_expires_at FROM users
		WHERE id=$1 AND totp_pending_secret_encrypted IS NOT NULL AND totp_pending_expires_at>now()`, userID).
		Scan(&encrypted, &expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, time.Time{}, ErrNotFound
	}
	return encrypted, expires, err
}

func (s *Store) CompleteTOTPEnrollment(ctx context.Context, userID string, step int64, ip string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE users SET totp_secret_encrypted=totp_pending_secret_encrypted,
		totp_pending_secret_encrypted=NULL,totp_pending_expires_at=NULL,totp_enrolled_at=now(),
		totp_last_used_step=$2,session_version=session_version+1,updated_at=now()
		WHERE id=$1 AND totp_pending_secret_encrypted IS NOT NULL AND totp_pending_expires_at>now()`, userID, step)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err = insertSecurityAudit(ctx, tx, userID, "security.mfa_enabled", "user", userID, ip, nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) TOTPSecret(ctx context.Context, userID string) ([]byte, error) {
	var encrypted []byte
	err := s.pool.QueryRow(ctx, `SELECT totp_secret_encrypted FROM users
		WHERE id=$1 AND totp_enrolled_at IS NOT NULL AND totp_secret_encrypted IS NOT NULL`, userID).Scan(&encrypted)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return encrypted, err
}

func (s *Store) ConsumeTOTPStep(ctx context.Context, userID string, step int64) error {
	tag, err := s.pool.Exec(ctx, `UPDATE users SET totp_last_used_step=$2,updated_at=now()
		WHERE id=$1 AND totp_enrolled_at IS NOT NULL
			AND (totp_last_used_step IS NULL OR totp_last_used_step<$2)`, userID, step)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrMFAReplay
	}
	return nil
}

func (s *Store) DisableTOTP(ctx context.Context, userID, ip string) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var version int64
	err = tx.QueryRow(ctx, `UPDATE users SET totp_secret_encrypted=NULL,totp_pending_secret_encrypted=NULL,
		totp_pending_expires_at=NULL,totp_enrolled_at=NULL,totp_last_used_step=NULL,
		session_version=session_version+1,updated_at=now() WHERE id=$1 RETURNING session_version`, userID).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	if err = insertSecurityAudit(ctx, tx, userID, "security.mfa_disabled", "user", userID, ip, nil); err != nil {
		return 0, err
	}
	return version, tx.Commit(ctx)
}

func scanEmailOutbox(row pgx.Row) (domain.EmailOutbox, error) {
	var item domain.EmailOutbox
	err := row.Scan(&item.ID, &item.Recipient, &item.Template, &item.EncryptedMessage, &item.Status,
		&item.Attempts, &item.MaxAttempts, &item.AvailableAt, &item.LockedAt, &item.LockedUntil,
		&item.LockedBy, &item.ClaimToken, &item.DeliveredAt, &item.LastError, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return item, ErrNotFound
	}
	return item, err
}

const emailOutboxProjection = `id,recipient,template,encrypted_message,status,attempts,max_attempts,
	available_at,locked_at,locked_until,COALESCE(locked_by,''),COALESCE(claim_token::text,''),
	delivered_at,COALESCE(last_error,''),created_at,updated_at`

func (s *Store) ClaimEmailOutbox(ctx context.Context, workerID string, limit int, lease time.Duration) ([]domain.EmailOutbox, error) {
	if strings.TrimSpace(workerID) == "" {
		return nil, errors.New("email worker ID is required")
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if lease <= 0 {
		lease = time.Minute
	}
	rows, err := s.pool.Query(ctx, `WITH picked AS (
		SELECT id FROM email_outbox WHERE attempts<max_attempts AND (
			(status IN ('PENDING','RETRY') AND available_at<=now()) OR
			(status='PROCESSING' AND locked_until<=now())
		) ORDER BY available_at,created_at,id FOR UPDATE SKIP LOCKED LIMIT $1
	), claimed AS (
		UPDATE email_outbox e SET status='PROCESSING',attempts=e.attempts+1,locked_by=$2,
			claim_token=gen_random_uuid(),locked_at=now(),locked_until=now()+($3::bigint*interval '1 millisecond'),updated_at=now()
		FROM picked WHERE e.id=picked.id RETURNING e.*
	) SELECT `+emailOutboxProjection+` FROM claimed ORDER BY available_at,created_at,id`,
		limit, workerID, lease.Milliseconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.EmailOutbox, 0)
	for rows.Next() {
		item, scanErr := scanEmailOutbox(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) CompleteEmailOutbox(ctx context.Context, outboxID, claimToken string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE email_outbox SET status='DELIVERED',delivered_at=now(),
		locked_at=NULL,locked_until=NULL,locked_by=NULL,claim_token=NULL,last_error=NULL,updated_at=now()
		WHERE id=$1 AND status='PROCESSING' AND claim_token=$2::uuid`, outboxID, claimToken)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) RetryEmailOutbox(ctx context.Context, outboxID, claimToken, failure string, retryAt time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var attempts, maxAttempts int
	err = tx.QueryRow(ctx, `SELECT attempts,max_attempts FROM email_outbox
		WHERE id=$1 AND status='PROCESSING' AND claim_token=$2::uuid FOR UPDATE`, outboxID, claimToken).
		Scan(&attempts, &maxAttempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	status := "RETRY"
	if attempts >= maxAttempts {
		status = "DEAD"
	}
	if _, err = tx.Exec(ctx, `UPDATE email_outbox SET status=$3,available_at=$4,last_error=$5,
		locked_at=NULL,locked_until=NULL,locked_by=NULL,claim_token=NULL,updated_at=now()
		WHERE id=$1 AND status='PROCESSING' AND claim_token=$2::uuid`,
		outboxID, claimToken, status, retryAt.UTC(), failure); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ExpireEmailLeases(ctx context.Context, now time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE email_outbox SET status='DEAD',
		last_error=COALESCE(last_error,'worker lease expired'),locked_at=NULL,locked_until=NULL,
		locked_by=NULL,claim_token=NULL,updated_at=now()
		WHERE status='PROCESSING' AND locked_until<=$1 AND attempts>=max_attempts`, now.UTC())
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (s *Store) ListEmailOutbox(ctx context.Context, status string, limit, offset int) ([]domain.EmailOutbox, error) {
	query := `SELECT ` + emailOutboxProjection + ` FROM email_outbox`
	args := []any{}
	if status != "" {
		query += ` WHERE status=$1`
		args = append(args, status)
	}
	args = append(args, clamp(limit), max(offset, 0))
	query += ` ORDER BY created_at DESC,id DESC LIMIT $` + itoa(len(args)-1) + ` OFFSET $` + itoa(len(args))
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.EmailOutbox, 0)
	for rows.Next() {
		item, scanErr := scanEmailOutbox(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		item.EncryptedMessage = nil
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) RequeueEmailOutbox(ctx context.Context, outboxID, actor, ip string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE email_outbox SET status='PENDING',attempts=0,available_at=now(),
		locked_at=NULL,locked_until=NULL,locked_by=NULL,claim_token=NULL,last_error=NULL,updated_at=now()
		WHERE id=$1 AND status='DEAD'`, outboxID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err = insertSecurityAudit(ctx, tx, actor, "security.email_dead_letter_requeued", "email_outbox", outboxID, ip, nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func ensurePersonalWorkspace(ctx context.Context, tx pgx.Tx, userID, displayName string) error {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM organization_memberships WHERE user_id=$1 AND role='OWNER' AND status='ACTIVE'
	)`, userID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	organizationID, projectID := id.UUID(), id.UUID()
	suffix := strings.ReplaceAll(userID, "-", "")
	if len(suffix) > 12 {
		suffix = suffix[:12]
	}
	name := strings.TrimSpace(displayName)
	if name == "" {
		name = "ModelDock"
	}
	if _, err := tx.Exec(ctx, `INSERT INTO organizations(id,name,slug,status,metadata)
		VALUES($1,$2,$3,'ACTIVE','{"created_by":"self_registration"}'::jsonb)`,
		organizationID, name+" Workspace", "workspace-"+suffix); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO organization_memberships(organization_id,user_id,role,status)
		VALUES($1,$2,'OWNER','ACTIVE')`, organizationID, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO projects(id,organization_id,name,slug,description,status,metadata)
		VALUES($1,$2,'Default','default','Default project created during account activation','ACTIVE',
			'{"created_by":"self_registration"}'::jsonb)`, projectID, organizationID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO project_memberships(organization_id,project_id,user_id,role,status)
		VALUES($1,$2,$3,'ADMIN','ACTIVE')`, organizationID, projectID, userID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO project_model_routes(id,project_id,model_route_id,alias,enabled,routing_config)
		SELECT gen_random_uuid(),$1,r.id,r.alias,true,'{}'::jsonb FROM model_routes r WHERE r.enabled
		ON CONFLICT(project_id,model_route_id) DO NOTHING`, projectID)
	return err
}

func enqueueEmailTx(ctx context.Context, tx pgx.Tx, outbox domain.EmailOutbox) error {
	if outbox.ID == "" || outbox.Recipient == "" || outbox.Template == "" || len(outbox.EncryptedMessage) == 0 || outbox.DedupeKey == "" {
		return errors.New("complete encrypted email outbox item is required")
	}
	if outbox.MaxAttempts <= 0 {
		outbox.MaxAttempts = 6
	}
	if outbox.AvailableAt.IsZero() {
		outbox.AvailableAt = time.Now().UTC()
	}
	_, err := tx.Exec(ctx, `INSERT INTO email_outbox(id,recipient,template,encrypted_message,dedupe_key,max_attempts,available_at)
		VALUES($1,lower($2),$3,$4,$5,$6,$7) ON CONFLICT(dedupe_key) DO NOTHING`,
		outbox.ID, outbox.Recipient, outbox.Template, outbox.EncryptedMessage, outbox.DedupeKey, outbox.MaxAttempts, outbox.AvailableAt)
	return err
}

func insertSecurityAudit(ctx context.Context, tx pgx.Tx, actor, action, resourceType, resourceID, ip string, after any) error {
	_, err := tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_id,action,resource_type,resource_id,after_state,ip)
		VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,'')::inet)`,
		id.UUID(), nullString(actor), action, resourceType, nullString(resourceID), jsonBytes(after), ip)
	return err
}
