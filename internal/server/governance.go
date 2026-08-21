package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/relayedock/relayedock/internal/contentpolicy"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/store"
)

func registerGovernanceRoutes(g *gin.RouterGroup, d Dependencies, admin bool) {
	if admin {
		g.GET("/risk/events", func(c *gin.Context) {
			limit, offset := page(c)
			rows, err := d.Store.ListRiskEvents(c.Request.Context(), limit, offset)
			respondList(c, rows, err)
		})
		g.POST("/risk/events", func(c *gin.Context) {
			var in domain.RiskEvent
			if c.ShouldBindJSON(&in) != nil {
				openAIError(c, 400, "invalid_request", "A valid risk event is required.")
				return
			}
			in.UserID = strings.TrimSpace(in.UserID)
			in.OrganizationID = strings.TrimSpace(in.OrganizationID)
			out, replayed, err := d.Store.RecordRiskEvent(c.Request.Context(), in, governanceSignalHash(d.Config.APIKeyHMACSecret, "ip", c.ClientIP()), governanceSignalHash(d.Config.APIKeyHMACSecret, "device", c.GetHeader("X-Device-Id")))
			if err != nil {
				respond(c, nil, err)
				return
			}
			c.JSON(http.StatusCreated, gin.H{"event": out, "replayed": replayed})
		})
		g.PATCH("/risk/:subjectType/:subjectID", func(c *gin.Context) {
			var in struct {
				RiskScore            int    `json:"risk_score"`
				VerificationLevel    string `json:"verification_level"`
				PaymentRisk          string `json:"payment_risk"`
				AbuseStatus          string `json:"abuse_status"`
				ManualReviewStatus   string `json:"manual_review_status"`
				NewAccountSpendLimit string `json:"new_account_spend_limit"`
			}
			if c.ShouldBindJSON(&in) != nil {
				openAIError(c, 400, "invalid_request", "A valid risk profile is required.")
				return
			}
			if in.RiskScore < 0 || in.RiskScore > 100 || !oneOf(strings.ToUpper(in.VerificationLevel), "UNVERIFIED", "EMAIL", "IDENTITY", "BUSINESS") || !oneOf(strings.ToUpper(in.PaymentRisk), "UNKNOWN", "LOW", "MEDIUM", "HIGH", "BLOCKED") || !oneOf(strings.ToUpper(in.AbuseStatus), "CLEAR", "WATCH", "RESTRICTED", "FROZEN") || !oneOf(strings.ToUpper(in.ManualReviewStatus), "NOT_REQUIRED", "PENDING", "IN_REVIEW", "APPROVED", "REJECTED") || (in.NewAccountSpendLimit != "" && !financeDecimalPattern.MatchString(in.NewAccountSpendLimit)) {
				openAIError(c, http.StatusBadRequest, "invalid_request", "Risk status or exact spend limit is invalid.")
				return
			}
			err := d.Store.UpdateRiskProfile(c.Request.Context(), strings.ToUpper(c.Param("subjectType")), c.Param("subjectID"), in.RiskScore, strings.ToUpper(in.VerificationLevel), strings.ToUpper(in.PaymentRisk), strings.ToUpper(in.AbuseStatus), strings.ToUpper(in.ManualReviewStatus), in.NewAccountSpendLimit, claimsFrom(c).Subject, c.ClientIP())
			respondNoContent(c, err)
		})
		g.POST("/api-keys/:keyID/freeze", func(c *gin.Context) {
			var in struct {
				Reason string `json:"reason"`
			}
			_ = c.ShouldBindJSON(&in)
			if strings.TrimSpace(in.Reason) == "" {
				in.Reason = "suspected_leak"
			}
			err := d.Store.FreezeAPIKeyForLeak(c.Request.Context(), c.Param("keyID"), in.Reason, claimsFrom(c).Subject, c.ClientIP())
			respondNoContent(c, err)
		})
		g.GET("/content-policies", func(c *gin.Context) {
			limit, offset := page(c)
			_ = limit
			_ = offset
			rows, err := d.Store.ListContentPolicies(c.Request.Context(), c.Query("organization_id"), strings.ToUpper(c.Query("phase")))
			respondList(c, rows, err)
		})
		g.POST("/content-policies", func(c *gin.Context) {
			var in domain.ContentPolicy
			if c.ShouldBindJSON(&in) != nil || in.Phase == "" || in.Action == "" {
				openAIError(c, 400, "invalid_request", "phase and action are required.")
				return
			}
			in.Phase, in.Action, in.FailureMode = strings.ToUpper(in.Phase), strings.ToUpper(in.Action), strings.ToUpper(in.FailureMode)
			if in.FailureMode == "" {
				in.FailureMode = d.Config.ContentPolicyFailureMode
			}
			if !oneOf(in.Phase, "PRE_REQUEST", "PROVIDER_NATIVE", "POST_RESPONSE") || !oneOf(in.Action, "ALLOW", "BLOCK", "REVIEW", "REDACT") || !oneOf(in.FailureMode, "FAIL_OPEN", "FAIL_CLOSED") || (in.OrganizationID == "" && in.ModelID == "") {
				openAIError(c, http.StatusBadRequest, "invalid_request", "Policy scope, phase, action, and failure_mode are invalid.")
				return
			}
			in.Enabled = true
			out, err := d.Store.CreateContentPolicy(c.Request.Context(), in, claimsFrom(c).Subject)
			if err == nil {
				audit(c, d, "content_policy.create", "content_policy", out.ID, out)
			}
			respondCreated(c, out, err)
		})
		g.GET("/manual-reviews", func(c *gin.Context) {
			limit, offset := page(c)
			rows, err := d.Store.ListManualReviews(c.Request.Context(), c.Query("status"), limit, offset)
			respondList(c, rows, err)
		})
		g.PATCH("/manual-reviews/:id", func(c *gin.Context) {
			var in struct {
				Status     string `json:"status"`
				Resolution string `json:"resolution"`
			}
			if c.ShouldBindJSON(&in) != nil || !oneOf(strings.ToUpper(in.Status), "PENDING", "IN_REVIEW", "APPROVED", "REJECTED", "EXPIRED") || len(in.Resolution) > 10000 {
				openAIError(c, 400, "invalid_request", "Review status or resolution is invalid.")
				return
			}
			respondNoContent(c, d.Store.UpdateManualReview(c.Request.Context(), c.Param("id"), strings.ToUpper(in.Status), in.Resolution, claimsFrom(c).Subject, c.ClientIP()))
		})
		g.GET("/reports", func(c *gin.Context) {
			limit, offset := page(c)
			rows, err := d.Store.ListUserReports(c.Request.Context(), c.Query("status"), limit, offset)
			respondList(c, rows, err)
		})
		g.PATCH("/reports/:id", func(c *gin.Context) {
			var in struct {
				Status     string `json:"status"`
				Resolution string `json:"resolution"`
			}
			if c.ShouldBindJSON(&in) != nil || in.Status == "" {
				openAIError(c, 400, "invalid_request", "status is required.")
				return
			}
			if !oneOf(strings.ToUpper(in.Status), "OPEN", "ACKNOWLEDGED", "IN_REVIEW", "RESOLVED", "REJECTED") || len(in.Resolution) > 10000 {
				openAIError(c, 400, "invalid_request", "Report status or resolution is invalid.")
				return
			}
			err := d.Store.UpdateUserReport(c.Request.Context(), c.Param("id"), strings.ToUpper(in.Status), in.Resolution, claimsFrom(c).Subject)
			respondNoContent(c, err)
		})
		g.GET("/lifecycle/jobs", func(c *gin.Context) {
			limit, offset := page(c)
			rows, err := d.Store.ListLifecycleJobs(c.Request.Context(), limit, offset)
			respondList(c, rows, err)
		})
	}
	if !admin {
		g.GET("/reports", func(c *gin.Context) {
			limit, offset := page(c)
			rows, err := d.Store.ListUserReportsForReporter(c.Request.Context(), claimsFrom(c).Subject, c.Query("status"), limit, offset)
			respondList(c, rows, err)
		})
	}

	// Reports and privacy are available to both administrators and scoped users.
	g.POST("/reports", func(c *gin.Context) {
		var in domain.UserReport
		if c.ShouldBindJSON(&in) != nil || strings.TrimSpace(in.ReportType) == "" || strings.TrimSpace(in.Description) == "" {
			openAIError(c, 400, "invalid_request", "report_type and description are required.")
			return
		}
		in.ReportType = strings.ToUpper(in.ReportType)
		if !oneOf(in.ReportType, "CONTENT", "API_KEY_ABUSE", "ORDER", "CHARGE", "PRIVACY", "OTHER") || len(in.Description) > 10000 {
			openAIError(c, 400, "invalid_request", "Report type or description is invalid.")
			return
		}
		if in.OrganizationID != "" && !admin {
			if err := d.Store.CheckOrganizationGovernanceAccess(c.Request.Context(), claimsFrom(c).Subject, in.OrganizationID, 2); err != nil {
				respond(c, nil, err)
				return
			}
		}
		in.ReporterUserID = claimsFrom(c).Subject
		in.SLAHours = d.Config.ReportSLAHours
		out, err := d.Store.CreateUserReport(c.Request.Context(), in, claimsFrom(c).Subject)
		respondCreated(c, out, err)
	})
	g.GET("/privacy/:subjectType/:subjectID", func(c *gin.Context) {
		typ, id, ok := requirePrivacySubjectAccess(c, d, admin, 1)
		if !ok {
			return
		}
		p, err := d.Store.PrivacySettingsBySubject(c.Request.Context(), typ, id)
		respond(c, p, err)
	})
	g.PUT("/privacy/:subjectType/:subjectID", func(c *gin.Context) {
		typ, id, ok := requirePrivacySubjectAccess(c, d, admin, 3)
		if !ok {
			return
		}
		var p domain.PrivacySettings
		if c.ShouldBindJSON(&p) != nil {
			openAIError(c, 400, "invalid_request", "A valid privacy settings body is required.")
			return
		}
		p.CrossBorderRoute = strings.ToUpper(strings.TrimSpace(p.CrossBorderRoute))
		if p.RetentionDays < 1 || p.RetentionDays > 3650 || !oneOf(p.CrossBorderRoute, "UNSPECIFIED", "DOMESTIC", "CROSS_BORDER", "REGION_LOCKED") {
			openAIError(c, http.StatusBadRequest, "invalid_request", "Retention days or cross-border route is invalid.")
			return
		}
		if !admin {
			current, err := d.Store.PrivacySettingsBySubject(c.Request.Context(), typ, id)
			if err != nil {
				respond(c, nil, err)
				return
			}
			p.LegalHold = current.LegalHold
		}
		p.SubjectType = typ
		p.SubjectID = id
		out, err := d.Store.UpsertPrivacySettings(c.Request.Context(), p, claimsFrom(c).Subject)
		if err == nil {
			audit(c, d, "privacy.update", "privacy_settings", p.SubjectID, out)
		}
		respond(c, out, err)
	})
	g.POST("/privacy/:subjectType/:subjectID/jobs", func(c *gin.Context) {
		typ, id, ok := requirePrivacySubjectAccess(c, d, admin, 3)
		if !ok {
			return
		}
		var in domain.DataLifecycleJob
		if c.ShouldBindJSON(&in) != nil || in.JobType == "" || in.IdempotencyKey == "" {
			openAIError(c, 400, "invalid_request", "job_type and idempotency_key are required.")
			return
		}
		in.JobType = strings.ToUpper(strings.TrimSpace(in.JobType))
		if !oneOf(in.JobType, "EXPORT", "CLOSE", "DELETE", "PURGE") || len(in.IdempotencyKey) > 200 {
			openAIError(c, http.StatusBadRequest, "invalid_request", "Lifecycle job type or idempotency key is invalid.")
			return
		}
		in.SubjectType = typ
		in.SubjectID = id
		out, replayed, err := d.Store.CreateLifecycleJob(c.Request.Context(), in, claimsFrom(c).Subject)
		if err != nil {
			respond(c, nil, err)
			return
		}
		audit(c, d, "privacy.lifecycle_requested", "data_lifecycle_job", out.ID, out)
		c.JSON(http.StatusAccepted, gin.H{"job": out, "replayed": replayed})
	})
	g.GET("/privacy/:subjectType/:subjectID/jobs", func(c *gin.Context) {
		typ, id, ok := requirePrivacySubjectAccess(c, d, admin, 1)
		if !ok {
			return
		}
		limit, offset := page(c)
		jobs, err := d.Store.ListLifecycleJobsForSubject(c.Request.Context(), typ, id, limit, offset)
		respondList(c, jobs, err)
	})
	g.GET("/privacy/jobs/:jobID/export", func(c *gin.Context) {
		job, err := d.Store.LifecycleJobByID(c.Request.Context(), c.Param("jobID"))
		if err != nil {
			respond(c, nil, err)
			return
		}
		_, _, ok := requirePrivacySubjectAccessValues(c, d, admin, job.SubjectType, job.SubjectID, 1)
		if !ok {
			return
		}
		if job.JobType != "EXPORT" || job.Status != "COMPLETED" {
			openAIError(c, http.StatusConflict, "export_not_ready", "The export job is not complete.")
			return
		}
		data, err := d.Store.ExportSubjectData(c.Request.Context(), job.SubjectType, job.SubjectID)
		respond(c, data, err)
	})
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func requirePrivacySubjectAccess(c *gin.Context, d Dependencies, admin bool, organizationRank int) (string, string, bool) {
	return requirePrivacySubjectAccessValues(c, d, admin, strings.ToUpper(c.Param("subjectType")), c.Param("subjectID"), organizationRank)
}
func requirePrivacySubjectAccessValues(c *gin.Context, d Dependencies, admin bool, typ, subjectID string, organizationRank int) (string, string, bool) {
	if typ != "USER" && typ != "ORGANIZATION" {
		openAIError(c, 400, "invalid_request", "subjectType must be USER or ORGANIZATION.")
		return "", "", false
	}
	if admin || claimsFrom(c).Role == "SUPER_ADMIN" {
		return typ, subjectID, true
	}
	if typ == "USER" {
		if claimsFrom(c).Subject != subjectID {
			respond(c, nil, store.ErrNotFound)
			return "", "", false
		}
		return typ, subjectID, true
	}
	if err := d.Store.CheckOrganizationGovernanceAccess(c.Request.Context(), claimsFrom(c).Subject, subjectID, organizationRank); err != nil {
		respond(c, nil, err)
		return "", "", false
	}
	return typ, subjectID, true
}

func evaluateContentPolicy(c *gin.Context, d Dependencies, req contentpolicy.Request) error {
	policies, err := d.Store.EffectiveContentPolicies(c.Request.Context(), req.OrganizationID, req.Model, string(req.Phase))
	if err != nil {
		return err
	}
	for _, policy := range policies {
		decision := contentpolicy.Decision{Allowed: true, Action: policy.Action, FailureMode: policy.FailureMode, Reason: "policy " + policy.ID}
		if policy.ProviderName != "builtin" {
			if d.ContentPolicy == nil {
				err = errors.New("content policy provider unavailable")
			} else {
				decision, err = d.ContentPolicy.Evaluate(c.Request.Context(), req)
			}
			if err != nil {
				mode := policy.FailureMode
				if mode == "" {
					mode = d.Config.ContentPolicyFailureMode
				}
				if contentPolicyFailOpen(mode) {
					d.Logger.Warn("content_policy_unavailable", "request_id", requestID(c), "policy_id", policy.ID, "mode", mode)
					continue
				}
				return errors.New("content policy unavailable")
			}
		}
		if decision.ReviewRequired || decision.Action == "REVIEW" || decision.Action == "REDACT" {
			dueAt := time.Now().UTC().Add(72 * time.Hour)
			_, reviewErr := d.Store.CreateManualReview(c.Request.Context(), domain.ManualReview{OrganizationID: req.OrganizationID, UserID: req.UserID, RequestID: req.RequestID, PolicyID: policy.ID, Reason: "Content policy decision requires manual review", DueAt: &dueAt})
			if reviewErr != nil {
				return reviewErr
			}
			return errors.New("content policy requires manual review")
		}
		if !decision.Allowed || decision.Action == "BLOCK" {
			return errors.New("content policy blocked request")
		}
	}
	return nil
}

func contentPolicyFailOpen(mode string) bool {
	return strings.EqualFold(strings.TrimSpace(mode), "FAIL_OPEN")
}

func governanceSignalHash(secret []byte, signalType, value string) []byte {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	digest := hmac.New(sha256.New, secret)
	_, _ = digest.Write([]byte("relaydock-governance:" + signalType + ":" + value))
	return digest.Sum(nil)
}
