package server

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/relayedock/relayedock/internal/auth"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/email"
	"github.com/relayedock/relayedock/internal/id"
	"github.com/relayedock/relayedock/internal/store"
)

var dummyPasswordHash = func() string {
	hash, _ := auth.HashPassword("ModelDock-constant-time-dummy-password-2026!")
	return hash
}()

func registerPublicAccountRoutes(r *gin.Engine, d Dependencies) {
	for _, prefix := range []string{"/api/auth", "/api/admin/auth", "/api/console/auth"} {
		r.GET(prefix+"/config", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"registration_mode": d.Config.RegistrationMode, "email_verification_required": true})
		})
		r.POST(prefix+"/register", func(c *gin.Context) { registerAccount(c, d) })
		r.POST(prefix+"/verify-email", func(c *gin.Context) { verifyEmail(c, d) })
		r.POST(prefix+"/resend-verification", func(c *gin.Context) { resendVerification(c, d) })
		r.POST(prefix+"/forgot-password", func(c *gin.Context) { forgotPassword(c, d) })
		r.POST(prefix+"/reset-password", func(c *gin.Context) { resetPassword(c, d) })
		r.GET(prefix+"/invitations/:token", func(c *gin.Context) { previewInvitation(c, d) })
		r.POST(prefix+"/invitations/:token/accept", func(c *gin.Context) { acceptInvitation(c, d) })
		r.POST(prefix+"/invitations/:token/reject", func(c *gin.Context) { rejectInvitation(c, d) })
	}
}

func registerAuthenticatedAccountRoutes(g *gin.RouterGroup, d Dependencies, realm string) {
	g.POST("/auth/change-password", func(c *gin.Context) { changePassword(c, d, realm) })
	g.POST("/auth/logout-other-sessions", func(c *gin.Context) { logoutOtherSessions(c, d, realm) })
	g.GET("/auth/mfa/status", func(c *gin.Context) { mfaStatus(c, d) })
	g.POST("/auth/mfa/setup", func(c *gin.Context) { mfaSetup(c, d) })
	g.POST("/auth/mfa/confirm", func(c *gin.Context) { mfaConfirm(c, d, realm) })
	g.POST("/auth/mfa/disable", func(c *gin.Context) { mfaDisable(c, d, realm) })
	g.GET("/auth/organization-invitations", func(c *gin.Context) {
		items, err := d.Store.ListUserInvitations(c.Request.Context(), claimsFrom(c).Subject)
		respondList(c, items, err)
	})
	g.POST("/auth/organization-invitations/:id/accept", func(c *gin.Context) {
		err := d.Store.RespondOrganizationInvitation(c.Request.Context(), claimsFrom(c).Subject, c.Param("id"), true, c.ClientIP())
		respondNoContent(c, err)
	})
	g.POST("/auth/organization-invitations/:id/reject", func(c *gin.Context) {
		err := d.Store.RespondOrganizationInvitation(c.Request.Context(), claimsFrom(c).Subject, c.Param("id"), false, c.ClientIP())
		respondNoContent(c, err)
	})
	g.GET("/organizations/:organizationID/invitations", func(c *gin.Context) {
		if _, ok := requireOrganizationAccess(c, d, realm == "admin", "ADMIN"); !ok {
			return
		}
		limit, offset := page(c)
		items, err := d.Store.ListOrganizationInvitations(c.Request.Context(), c.Param("organizationID"), limit, offset)
		respondList(c, items, err)
	})
	g.POST("/organizations/:organizationID/invitations", func(c *gin.Context) { createOrganizationInvitation(c, d, realm) })
	g.DELETE("/organizations/:organizationID/invitations/:invitationID", func(c *gin.Context) {
		if _, ok := requireOrganizationAccess(c, d, realm == "admin", "ADMIN"); !ok {
			return
		}
		invitation, err := d.Store.OrganizationInvitationByID(c.Request.Context(), c.Param("invitationID"))
		if err != nil || invitation.OrganizationID != c.Param("organizationID") {
			respondNoContent(c, store.ErrNotFound)
			return
		}
		if invitation.Role == "OWNER" && !canManageOwnerInvitations(c, d, invitation.OrganizationID, realm) {
			openAIError(c, http.StatusForbidden, "insufficient_permissions", "Owner access is required.")
			return
		}
		err = d.Store.RevokeOrganizationInvitation(c.Request.Context(), c.Param("organizationID"), c.Param("invitationID"), claimsFrom(c).Subject, c.ClientIP())
		respondNoContent(c, err)
	})
	if realm == "admin" {
		g.GET("/registration-invites", func(c *gin.Context) {
			limit, offset := page(c)
			items, err := d.Store.ListRegistrationInvites(c.Request.Context(), limit, offset)
			respondList(c, items, err)
		})
		g.POST("/registration-invites", func(c *gin.Context) { createRegistrationInvite(c, d) })
		g.DELETE("/registration-invites/:id", func(c *gin.Context) {
			err := d.Store.RevokeRegistrationInvite(c.Request.Context(), c.Param("id"), claimsFrom(c).Subject, c.ClientIP())
			respondNoContent(c, err)
		})
		g.GET("/email-outbox", func(c *gin.Context) {
			limit, offset := page(c)
			items, err := d.Store.ListEmailOutbox(c.Request.Context(), strings.TrimSpace(c.Query("status")), limit, offset)
			respondList(c, items, err)
		})
		g.POST("/email-outbox/:id/requeue", func(c *gin.Context) {
			err := d.Store.RequeueEmailOutbox(c.Request.Context(), c.Param("id"), claimsFrom(c).Subject, c.ClientIP())
			respondNoContent(c, err)
		})
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func registerAccount(c *gin.Context, d Dependencies) {
	if d.Config.RegistrationMode == "CLOSED" {
		d.Store.Audit(c.Request.Context(), "", "security.registration_blocked", "user", "", c.ClientIP(), map[string]any{"mode": "CLOSED"})
		openAIError(c, http.StatusForbidden, "registration_closed", "Registration is currently closed.")
		return
	}
	var in struct {
		Email            string `json:"email"`
		Password         string `json:"password"`
		DisplayName      string `json:"display_name"`
		InviteToken      string `json:"invite_token"`
		RegistrationCode string `json:"registration_code"`
	}
	if c.ShouldBindJSON(&in) != nil || !validEmail(in.Email) {
		openAIError(c, http.StatusBadRequest, "invalid_request", "A valid email and password are required.")
		return
	}
	if !allowIdentity(c, d, "register", c.ClientIP()+"|"+strings.ToLower(strings.TrimSpace(in.Email)), d.Config.RegistrationLimit) {
		return
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		openAIError(c, http.StatusBadRequest, "invalid_request", "A valid password is required.")
		return
	}
	var verificationDigest []byte
	var outbox domain.EmailOutbox
	if strings.TrimSpace(in.InviteToken) == "" {
		token, tokenErr := auth.NewOpaqueToken()
		if tokenErr != nil {
			openAIError(c, http.StatusInternalServerError, "internal_error", "Could not start registration.")
			return
		}
		verificationDigest = d.Auth.DigestToken(token)
		outbox, err = buildEmailOutbox(d, "VERIFY_EMAIL", in.Email, token)
		if err != nil {
			openAIError(c, http.StatusInternalServerError, "internal_error", "Could not start registration.")
			return
		}
	}
	var registrationDigest, organizationDigest []byte
	if in.RegistrationCode != "" {
		registrationDigest = d.Auth.DigestToken(strings.TrimSpace(in.RegistrationCode))
	}
	if in.InviteToken != "" {
		organizationDigest = d.Auth.DigestToken(strings.TrimSpace(in.InviteToken))
	}
	result, err := d.Store.RegisterUser(c.Request.Context(), in.Email, hash, in.DisplayName, d.Config.RegistrationMode,
		registrationDigest, organizationDigest, verificationDigest, time.Now().UTC().Add(d.Config.VerificationTTL), outbox, c.ClientIP())
	if errors.Is(err, store.ErrInviteRequired) {
		openAIError(c, http.StatusBadRequest, "invalid_invitation", "The invitation is invalid or expired.")
		return
	}
	if errors.Is(err, store.ErrRegistrationClosed) {
		openAIError(c, http.StatusForbidden, "registration_closed", "Registration is currently closed.")
		return
	}
	if err != nil {
		openAIError(c, http.StatusInternalServerError, "internal_error", "Could not start registration.")
		return
	}
	if !result.Created {
		c.JSON(http.StatusAccepted, gin.H{"message": "If the account can be registered, a verification email will be sent."})
		return
	}
	if result.Active {
		c.JSON(http.StatusCreated, gin.H{"user": publicUser(result.User), "email_verification_required": false})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"message": "If the account can be registered, a verification email will be sent.", "email_verification_required": true})
}

func verifyEmail(c *gin.Context, d Dependencies) {
	if !allowIdentity(c, d, "verification", c.ClientIP(), d.Config.VerificationLimit) {
		return
	}
	token := strings.TrimSpace(tokenFromBody(c))
	if token == "" {
		openAIError(c, http.StatusBadRequest, "invalid_token", "The verification link is invalid or expired.")
		return
	}
	user, err := d.Store.VerifyEmail(c.Request.Context(), d.Auth.DigestToken(token), c.ClientIP())
	if errors.Is(err, store.ErrInvalidToken) {
		openAIError(c, http.StatusBadRequest, "invalid_token", "The verification link is invalid or expired.")
		return
	}
	if err != nil {
		openAIError(c, http.StatusInternalServerError, "internal_error", "Could not verify the email address.")
		return
	}
	c.JSON(http.StatusOK, gin.H{"user": publicUser(user), "verified": true})
}

func resendVerification(c *gin.Context, d Dependencies) {
	var in struct {
		Email string `json:"email"`
	}
	_ = c.ShouldBindJSON(&in)
	emailAddress := strings.ToLower(strings.TrimSpace(in.Email))
	if !allowIdentity(c, d, "verification", c.ClientIP()+"|"+emailAddress, d.Config.VerificationLimit) {
		return
	}
	token, err := auth.NewOpaqueToken()
	if err != nil {
		openAIError(c, http.StatusInternalServerError, "internal_error", "Could not process the request.")
		return
	}
	outbox, err := buildEmailOutbox(d, "VERIFY_EMAIL", emailAddress, token)
	if err != nil {
		openAIError(c, http.StatusInternalServerError, "internal_error", "Could not process the request.")
		return
	}
	_, err = d.Store.ResendVerification(c.Request.Context(), emailAddress, d.Auth.DigestToken(token),
		time.Now().UTC().Add(d.Config.VerificationTTL), outbox, c.ClientIP())
	if err != nil {
		openAIError(c, http.StatusInternalServerError, "internal_error", "Could not process the request.")
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"message": "If the account exists and needs verification, an email will be sent."})
}

func forgotPassword(c *gin.Context, d Dependencies) {
	var in struct {
		Email string `json:"email"`
	}
	_ = c.ShouldBindJSON(&in)
	emailAddress := strings.ToLower(strings.TrimSpace(in.Email))
	if !allowIdentity(c, d, "password_reset", c.ClientIP()+"|"+emailAddress, d.Config.PasswordResetLimit) {
		return
	}
	token, err := auth.NewOpaqueToken()
	if err != nil {
		openAIError(c, http.StatusInternalServerError, "internal_error", "Could not process the request.")
		return
	}
	outbox, err := buildEmailOutbox(d, "PASSWORD_RESET", emailAddress, token)
	if err != nil {
		openAIError(c, http.StatusInternalServerError, "internal_error", "Could not process the request.")
		return
	}
	_, err = d.Store.RequestPasswordReset(c.Request.Context(), emailAddress, d.Auth.DigestToken(token),
		time.Now().UTC().Add(d.Config.PasswordResetTTL), outbox, c.ClientIP())
	if err != nil {
		openAIError(c, http.StatusInternalServerError, "internal_error", "Could not process the request.")
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"message": "If the account exists, a password reset email will be sent."})
}

func resetPassword(c *gin.Context, d Dependencies) {
	var in struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if c.ShouldBindJSON(&in) != nil || strings.TrimSpace(in.Token) == "" {
		openAIError(c, http.StatusBadRequest, "invalid_request", "A valid reset token and password are required.")
		return
	}
	if !allowIdentity(c, d, "password_reset", c.ClientIP(), d.Config.PasswordResetLimit) {
		return
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		openAIError(c, http.StatusBadRequest, "invalid_request", "A valid password is required.")
		return
	}
	_, err = d.Store.ResetPassword(c.Request.Context(), d.Auth.DigestToken(strings.TrimSpace(in.Token)), hash, c.ClientIP())
	if errors.Is(err, store.ErrInvalidToken) {
		openAIError(c, http.StatusBadRequest, "invalid_token", "The reset link is invalid or expired.")
		return
	}
	if err != nil {
		openAIError(c, http.StatusInternalServerError, "internal_error", "Could not reset the password.")
		return
	}
	c.JSON(http.StatusOK, gin.H{"reset": true})
}

func changePassword(c *gin.Context, d Dependencies, realm string) {
	var in struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if c.ShouldBindJSON(&in) != nil {
		openAIError(c, http.StatusBadRequest, "invalid_request", "Current and new passwords are required.")
		return
	}
	claims := claimsFrom(c)
	user, err := d.Store.UserByID(c.Request.Context(), claims.Subject)
	if err != nil || !auth.VerifyPassword(user.PasswordHash, in.CurrentPassword) {
		openAIError(c, http.StatusUnauthorized, "invalid_credentials", "Current and new passwords are required.")
		return
	}
	hash, err := auth.HashPassword(in.NewPassword)
	if err != nil {
		openAIError(c, http.StatusBadRequest, "invalid_request", "A valid password is required.")
		return
	}
	version, err := d.Store.ChangePassword(c.Request.Context(), claims.Subject, user.PasswordHash, hash, c.ClientIP())
	if errors.Is(err, store.ErrInvalidToken) {
		openAIError(c, http.StatusConflict, "password_changed", "The password changed in another session.")
		return
	}
	if err != nil {
		openAIError(c, http.StatusInternalServerError, "internal_error", "Could not change the password.")
		return
	}
	clearSessionCookies(c, d, realm)
	c.JSON(http.StatusOK, gin.H{"changed": true, "session_version": version})
}

func logoutOtherSessions(c *gin.Context, d Dependencies, realm string) {
	claims := claimsFrom(c)
	version, err := d.Store.RevokeOtherSessions(c.Request.Context(), claims.Subject, c.ClientIP())
	if err != nil {
		openAIError(c, http.StatusInternalServerError, "internal_error", "Could not revoke other sessions.")
		return
	}
	user, err := d.Store.UserByID(c.Request.Context(), claims.Subject)
	if err != nil {
		openAIError(c, http.StatusUnauthorized, "invalid_session", "The session user is unavailable.")
		return
	}
	if err = issueSessionCookies(c, d, realm, user, claims.MFA, version); err != nil {
		openAIError(c, http.StatusInternalServerError, "internal_error", "Could not replace the current session.")
		return
	}
	c.JSON(http.StatusOK, gin.H{"revoked": true})
}

func mfaStatus(c *gin.Context, d Dependencies) {
	user, err := d.Store.UserByID(c.Request.Context(), claimsFrom(c).Subject)
	if err != nil {
		openAIError(c, http.StatusUnauthorized, "invalid_session", "The session user is unavailable.")
		return
	}
	c.JSON(http.StatusOK, gin.H{"enabled": user.MFAEnabled, "required": d.Config.AdminMFARequired, "enrollment_pending": false})
}

func mfaSetup(c *gin.Context, d Dependencies) {
	claims := claimsFrom(c)
	user, err := d.Store.UserByID(c.Request.Context(), claims.Subject)
	if err != nil || (user.Role != "ADMIN" && user.Role != "SUPER_ADMIN") {
		openAIError(c, http.StatusForbidden, "insufficient_permissions", "Administrator access is required.")
		return
	}
	secret, uri, err := auth.GenerateTOTP(user.Email)
	if err != nil {
		openAIError(c, http.StatusInternalServerError, "internal_error", "Could not start MFA enrollment.")
		return
	}
	encrypted, err := d.Vault.Encrypt(secret, "mfa:"+user.ID)
	if err != nil || d.Store.SetPendingTOTP(c.Request.Context(), user.ID, encrypted, time.Now().UTC().Add(10*time.Minute), c.ClientIP()) != nil {
		openAIError(c, http.StatusInternalServerError, "internal_error", "Could not start MFA enrollment.")
		return
	}
	c.JSON(http.StatusOK, gin.H{"secret": secret, "otpauth_uri": uri, "expires_at": time.Now().UTC().Add(10 * time.Minute)})
}

func mfaConfirm(c *gin.Context, d Dependencies, realm string) {
	var in struct {
		Code string `json:"code"`
	}
	if c.ShouldBindJSON(&in) != nil {
		openAIError(c, http.StatusBadRequest, "invalid_request", "An MFA code is required.")
		return
	}
	claims := claimsFrom(c)
	user, err := d.Store.UserByID(c.Request.Context(), claims.Subject)
	if err != nil {
		openAIError(c, http.StatusUnauthorized, "invalid_session", "The session user is unavailable.")
		return
	}
	envelope, _, err := d.Store.PendingTOTP(c.Request.Context(), user.ID)
	if err != nil {
		openAIError(c, http.StatusBadRequest, "mfa_not_pending", "Start MFA enrollment before confirming it.")
		return
	}
	secret, decryptErr := d.Vault.Decrypt(envelope, "mfa:"+user.ID)
	step, codeErr := auth.ValidateTOTP(secret, in.Code, time.Now().UTC())
	if decryptErr != nil || codeErr != nil {
		openAIError(c, http.StatusBadRequest, "invalid_mfa", "The MFA code is invalid.")
		return
	}
	if err = d.Store.CompleteTOTPEnrollment(c.Request.Context(), user.ID, step, c.ClientIP()); err != nil {
		openAIError(c, http.StatusConflict, "mfa_not_pending", "MFA enrollment expired or was already completed.")
		return
	}
	user, _ = d.Store.UserByID(c.Request.Context(), user.ID)
	if err = issueSessionCookies(c, d, realm, user, true, user.SessionVersion); err != nil {
		openAIError(c, http.StatusInternalServerError, "internal_error", "Could not create the MFA session.")
		return
	}
	c.JSON(http.StatusOK, gin.H{"enabled": true})
}

func mfaDisable(c *gin.Context, d Dependencies, realm string) {
	if d.Config.AdminMFARequired {
		openAIError(c, http.StatusConflict, "mfa_required", "Administrator MFA is required by deployment policy.")
		return
	}
	var in struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if c.ShouldBindJSON(&in) != nil {
		openAIError(c, http.StatusBadRequest, "invalid_request", "Password and MFA code are required.")
		return
	}
	claims := claimsFrom(c)
	user, err := d.Store.UserByID(c.Request.Context(), claims.Subject)
	if err != nil || !auth.VerifyPassword(user.PasswordHash, in.Password) {
		openAIError(c, http.StatusUnauthorized, "invalid_credentials", "Password and MFA code are required.")
		return
	}
	envelope, err := d.Store.TOTPSecret(c.Request.Context(), user.ID)
	if err != nil {
		openAIError(c, http.StatusBadRequest, "mfa_not_enabled", "MFA is not enabled.")
		return
	}
	secret, decryptErr := d.Vault.Decrypt(envelope, "mfa:"+user.ID)
	step, codeErr := auth.ValidateTOTP(secret, in.Code, time.Now().UTC())
	if decryptErr != nil || codeErr != nil || d.Store.ConsumeTOTPStep(c.Request.Context(), user.ID, step) != nil {
		openAIError(c, http.StatusUnauthorized, "invalid_mfa", "Password and MFA code are required.")
		return
	}
	version, err := d.Store.DisableTOTP(c.Request.Context(), user.ID, c.ClientIP())
	if err != nil {
		openAIError(c, http.StatusInternalServerError, "internal_error", "Could not disable MFA.")
		return
	}
	clearSessionCookies(c, d, realm)
	c.JSON(http.StatusOK, gin.H{"enabled": false, "session_version": version})
}

func createOrganizationInvitation(c *gin.Context, d Dependencies, realm string) {
	organization, ok := requireOrganizationAccess(c, d, realm == "admin", "ADMIN")
	if !ok {
		return
	}
	var in struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if c.ShouldBindJSON(&in) != nil || !validEmail(in.Email) {
		openAIError(c, http.StatusBadRequest, "invalid_request", "email is required.")
		return
	}
	in.Role = strings.ToUpper(strings.TrimSpace(in.Role))
	if !validOrganizationRole(in.Role) {
		openAIError(c, http.StatusBadRequest, "invalid_request", "role is invalid.")
		return
	}
	if in.Role == "OWNER" && !canManageOwnerInvitations(c, d, organization.ID, realm) {
		openAIError(c, http.StatusForbidden, "insufficient_permissions", "Owner access is required.")
		return
	}
	token, err := auth.NewOpaqueToken()
	if err != nil {
		openAIError(c, http.StatusInternalServerError, "internal_error", "Could not create the invitation.")
		return
	}
	outbox, err := buildEmailOutbox(d, "ORGANIZATION_INVITE", in.Email, token)
	if err != nil {
		openAIError(c, http.StatusInternalServerError, "internal_error", "Could not create the invitation.")
		return
	}
	invitation, err := d.Store.CreateOrganizationInvitation(c.Request.Context(), organization.ID, in.Email, in.Role,
		claimsFrom(c).Subject, d.Auth.DigestToken(token), time.Now().UTC().Add(d.Config.InvitationTTL), outbox, c.ClientIP())
	if err != nil {
		openAIError(c, http.StatusInternalServerError, "internal_error", "Could not create the invitation.")
		return
	}
	respondCreated(c, invitation, nil)
}

func createRegistrationInvite(c *gin.Context, d Dependencies) {
	var in struct {
		MaxUses        int `json:"max_uses"`
		ExpiresInHours int `json:"expires_in_hours"`
	}
	if c.ShouldBindJSON(&in) != nil || in.MaxUses <= 0 || in.MaxUses > 10000 || in.ExpiresInHours <= 0 || in.ExpiresInHours > 8760 {
		openAIError(c, http.StatusBadRequest, "invalid_request", "max_uses and expires_in_hours are required.")
		return
	}
	code, err := auth.NewOpaqueToken()
	if err != nil {
		openAIError(c, http.StatusInternalServerError, "internal_error", "Could not create the invitation code.")
		return
	}
	invite, err := d.Store.CreateRegistrationInvite(c.Request.Context(), d.Auth.DigestToken(code), in.MaxUses,
		time.Now().UTC().Add(time.Duration(in.ExpiresInHours)*time.Hour), claimsFrom(c).Subject, c.ClientIP())
	if err != nil {
		openAIError(c, http.StatusInternalServerError, "internal_error", "Could not create the invitation code.")
		return
	}
	c.JSON(http.StatusCreated, gin.H{"invite": invite, "code": code, "warning": "The code is shown once."})
}

func previewInvitation(c *gin.Context, d Dependencies) {
	invitation, err := d.Store.OrganizationInvitationByDigest(c.Request.Context(), d.Auth.DigestToken(c.Param("token")))
	if errors.Is(err, store.ErrNotFound) {
		openAIError(c, http.StatusNotFound, "invalid_invitation", "The invitation is invalid or expired.")
		return
	}
	if err != nil {
		openAIError(c, http.StatusInternalServerError, "internal_error", "Could not load the invitation.")
		return
	}
	invitation.Email = maskEmail(invitation.Email)
	c.JSON(http.StatusOK, invitation)
}

func acceptInvitation(c *gin.Context, d Dependencies) {
	var in struct {
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
	}
	_ = c.ShouldBindJSON(&in)
	hash := ""
	if in.Password != "" {
		var hashErr error
		hash, hashErr = auth.HashPassword(in.Password)
		if hashErr != nil {
			openAIError(c, http.StatusBadRequest, "invalid_request", "A valid password is required.")
			return
		}
	}
	user, err := d.Store.AcceptOrganizationInvitation(c.Request.Context(), d.Auth.DigestToken(c.Param("token")), hash,
		in.DisplayName, d.Config.RegistrationMode != "CLOSED", c.ClientIP())
	if errors.Is(err, store.ErrInvalidToken) {
		openAIError(c, http.StatusBadRequest, "invalid_invitation", "The invitation is invalid or expired.")
		return
	}
	if errors.Is(err, store.ErrPasswordRequired) {
		openAIError(c, http.StatusBadRequest, "password_required", "A password is required for a new account.")
		return
	}
	if errors.Is(err, store.ErrRegistrationClosed) {
		openAIError(c, http.StatusForbidden, "registration_closed", "Registration is currently closed.")
		return
	}
	if err != nil {
		openAIError(c, http.StatusInternalServerError, "internal_error", "Could not accept the invitation.")
		return
	}
	c.JSON(http.StatusOK, gin.H{"user": publicUser(user), "accepted": true})
}

func rejectInvitation(c *gin.Context, d Dependencies) {
	err := d.Store.RejectOrganizationInvitation(c.Request.Context(), d.Auth.DigestToken(c.Param("token")), c.ClientIP())
	if errors.Is(err, store.ErrInvalidToken) {
		openAIError(c, http.StatusBadRequest, "invalid_invitation", "The invitation is invalid or expired.")
		return
	}
	if err != nil {
		openAIError(c, http.StatusInternalServerError, "internal_error", "Could not reject the invitation.")
		return
	}
	c.JSON(http.StatusOK, gin.H{"rejected": true})
}

func buildEmailOutbox(d Dependencies, template, recipient, token string) (domain.EmailOutbox, error) {
	item := domain.EmailOutbox{ID: id.UUID(), Recipient: strings.ToLower(strings.TrimSpace(recipient)), Template: template,
		DedupeKey: template + ":" + hex.EncodeToString(d.Auth.DigestToken(token)), MaxAttempts: d.Config.MailMaxAttempts}
	subject, text := "", ""
	switch template {
	case "VERIFY_EMAIL":
		subject = "Verify your ModelDock email"
		text = "Verify your email address: " + d.Config.PublicConsoleURL + "/verify-email?token=" + token
	case "PASSWORD_RESET":
		subject = "Reset your ModelDock password"
		text = "Reset your password: " + d.Config.PublicConsoleURL + "/reset-password?token=" + token
	case "ORGANIZATION_INVITE":
		subject = "You have been invited to ModelDock"
		text = "Accept your organization invitation: " + d.Config.PublicConsoleURL + "/invitations/" + token
	default:
		return domain.EmailOutbox{}, errors.New("unsupported email template")
	}
	message, err := json.Marshal(email.Message{From: d.Config.MailFrom, To: item.Recipient, Subject: subject, Text: text})
	if err != nil {
		return domain.EmailOutbox{}, err
	}
	item.EncryptedMessage, err = d.Vault.Encrypt(string(message), "email:"+item.ID)
	return item, err
}

func issueSessionCookies(c *gin.Context, d Dependencies, realm string, user domain.User, mfa bool, version int64) error {
	access, accessExpires, err := d.Auth.IssueVersioned(user.ID, user.Email, user.Role, version, mfa)
	if err != nil {
		return err
	}
	refresh, refreshExpires, err := d.Auth.IssueRefreshVersioned(user.ID, user.Email, user.Role, version, mfa)
	if err != nil {
		return err
	}
	csrf, err := newCSRF()
	if err != nil {
		return err
	}
	c.SetSameSite(http.SameSiteStrictMode)
	cookies := controlCookieNames(realm)
	c.SetCookie(cookies.Session, access, int(time.Until(accessExpires).Seconds()), "/", "", d.Config.CookieSecure, true)
	c.SetCookie(cookies.Refresh, refresh, int(time.Until(refreshExpires).Seconds()), "/", "", d.Config.CookieSecure, true)
	c.SetCookie(cookies.CSRF, csrf, int(time.Until(refreshExpires).Seconds()), "/", "", d.Config.CookieSecure, false)
	return nil
}

func clearSessionCookies(c *gin.Context, d Dependencies, realm string) {
	cookies := controlCookieNames(realm)
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(cookies.Session, "", -1, "/", "", d.Config.CookieSecure, true)
	c.SetCookie(cookies.Refresh, "", -1, "/", "", d.Config.CookieSecure, true)
	c.SetCookie(cookies.CSRF, "", -1, "/", "", d.Config.CookieSecure, false)
}

func tokenFromBody(c *gin.Context) string {
	var in struct {
		Token string `json:"token"`
	}
	_ = c.ShouldBindJSON(&in)
	if in.Token != "" {
		return in.Token
	}
	return c.Query("token")
}

func allowIdentity(c *gin.Context, d Dependencies, action, identity string, limit int) bool {
	if d.Limiter == nil {
		return true
	}
	result, err := d.Limiter.AllowIdentity(c.Request.Context(), action, identity, limit, d.Config.IdentityRateWindow)
	if err != nil {
		openAIError(c, http.StatusServiceUnavailable, "service_unavailable", "Authentication protection is temporarily unavailable.")
		return false
	}
	if !result.Allowed {
		if d.Store != nil {
			d.Store.Audit(c.Request.Context(), "", "security.rate_limit_exceeded", "authentication", action, c.ClientIP(), nil)
		}
		c.Header("Retry-After", strconv.Itoa(maxInt(1, int(result.RetryAfter.Seconds()))))
		openAIError(c, http.StatusTooManyRequests, "rate_limit_exceeded", "Too many attempts. Please try again later.")
		return false
	}
	return true
}

func validEmail(value string) bool {
	value = strings.TrimSpace(value)
	address, err := mail.ParseAddress(value)
	return err == nil && strings.EqualFold(address.Address, value) && strings.Contains(address.Address, "@")
}

func canManageOwnerInvitations(c *gin.Context, d Dependencies, organizationID, realm string) bool {
	claims := claimsFrom(c)
	if realm == "admin" || claims.Role == "ADMIN" || claims.Role == "SUPER_ADMIN" {
		return true
	}
	members, err := d.Store.ListOrganizationMembers(c.Request.Context(), organizationID)
	if err != nil {
		return false
	}
	for _, member := range members {
		if member.UserID == claims.Subject && member.Status == "ACTIVE" && member.Role == "OWNER" {
			return true
		}
	}
	return false
}

func publicUser(user domain.User) domain.User {
	user.PasswordHash = ""
	user.SessionVersion = 0
	return user
}

func maskEmail(value string) string {
	parts := strings.SplitN(value, "@", 2)
	if len(parts) != 2 {
		return "hidden"
	}
	local := parts[0]
	if len(local) > 2 {
		local = local[:1] + strings.Repeat("*", len(local)-2) + local[len(local)-1:]
	} else {
		local = strings.Repeat("*", len(local))
	}
	return local + "@" + parts[1]
}
