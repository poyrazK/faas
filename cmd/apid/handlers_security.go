// handlers_security.go — apid admin-gated handlers for per-app
// signature and deploy-posture controls (issue #472 / ADR-054).
//
// Route (registered in cmd/apid/server.go::handler with the admin+MFA
// chain — see the mount block):
//
//	GET   /v1/apps/{slug}/security  → getAppSecurity
//	PATCH /v1/apps/{slug}/security  → patchAppSecurity
//
// Why a dedicated endpoint (instead of folding these controls into
// updateApp):
//
//   - Signature enforcement is an operator control, NOT a customer
//     knob. A customer who can PATCH require_signed=true on their own
//     app can immediately circumvent the gate they're turning on
//     (they can pre-stage the trusted_signers table however they
//     want). The dedicated endpoint restricts the toggle to the
//     admin scope (ScopesAdminOnly), matching the trusted-signer
//     surface below.
//
//   - The wire is intentionally narrow: only signature enforcement and
//     deploy-posture policy are settable today. Future admin-only knobs
//     (e.g. an allow-list of trusted registries, a customer-side
//     key-rotation policy) land here so the PATCH /v1/apps/{slug}
//     endpoint stays a customer-safe surface.
//
//   - The mount-time chain (authLimited → requireMFA →
//     requireScope(api.ScopesAdminOnly...)) mirrors
//     `PATCH /v1/account/plan` at server.go:516 — same posture, same
//     idempotency wrapper, same problem-code surface.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	postureSeverityCritical = "critical"
	postureSeverityHigh     = "high"
	postureSeverityMedium   = "medium"
	postureSeverityLow      = "low"
)

// AppSecurityRequest is the body of PATCH /v1/apps/{slug}/security.
// (Alias for api.AppSecurityRequest — defined here only so the
// handler file reads self-contained; the spec-compliance gate
// matches on the api package's DTO.)
//   - See api.AppSecurityRequest.
//
// AppSecurityResponse is the success body of PATCH
// /v1/apps/{slug}/security.
//   - See api.AppSecurityResponse.
//
// patchAppSecurity applies admin-scoped per-app security knobs.
// Signature enforcement and deploy-posture policy are admin-only; future
// security knobs land here so the customer PATCH surface stays admin-free.
//
// Hand-rolled phases (resolve app → validate body → persist →
// notify → audit), not a helper, because the line budget is well
// under the §Conventions 50-line cap and the phase order matters
// for auditing.
//
// Mounted with authLimited → requireMFA → requireScope(ScopesAdminOnly);
// the per-field admin check is therefore redundant (the route is
// already admin-only), but it's documented in the docstring so a
// future caller doesn't accidentally widen the route's scope.
func (s *server) patchAppSecurity(w http.ResponseWriter, r *http.Request, acct state.Account) {
	slug := r.PathValue("slug")
	app, ok := s.loadApp(w, r, acct, slug)
	if !ok {
		return
	}
	var req api.AppSecurityRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid JSON body"))
		return
	}
	// nil = no field set; treat as a no-op rather than 400 so an
	// empty-body probe from the dashboard's "Save" button (with no
	// fields changed) doesn't fail. A future field here that has
	// required values can override this branch.
	if req.RequireSigned == nil && req.SecurityPolicy == nil {
		writeJSON(w, http.StatusOK, api.AppSecurityResponse{
			RequireSigned: app.RequireSigned, SecurityPolicy: normalizedAppSecurityPolicy(app.SecurityPolicy),
		})
		return
	}
	if req.SecurityPolicy != nil && !req.SecurityPolicy.Valid() {
		api.WriteProblem(w, api.ErrValidation("security_policy must be one of: off, warn, enforce"))
		return
	}
	updated, err := s.store.UpdateApp(r.Context(), app.ID, state.UpdateAppParams{
		RequireSigned:     req.RequireSigned,
		SetRequireSigned:  req.RequireSigned != nil,
		SecurityPolicy:    req.SecurityPolicy,
		SetSecurityPolicy: req.SecurityPolicy != nil,
	})
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not update app security"))
		return
	}
	// pg_notify on the deployment-changed channel — imaged's verify
	// path reads apps.require_signed at buildImageLayer time, so
	// flipping the flag takes effect on the NEXT deploy (no
	// in-flight deploy re-evaluates). Same posture as the other
	// app-config toggles.
	_ = s.notif.Notify(r.Context(), "app_changed", fmt.Sprintf(
		`{"kind":"security","app_id":"%s","require_signed":%t}`, app.ID, updated.RequireSigned))
	// Audit — IAM-4 (issue #291) shape: record what the admin
	// altered, old vs new. The `app.security_updated` kind is the
	// distinct taxonomy entry so the audit-log panel can filter
	// signature-related config changes separately from generic
	// app.updated.
	s.audit.Emit(r.Context(), "app.security_updated", &acct.ID, map[string]any{
		"app_id":              updated.ID,
		"slug":                updated.Slug,
		"old_require":         app.RequireSigned,
		"new_require":         updated.RequireSigned,
		"old_security_policy": normalizedAppSecurityPolicy(app.SecurityPolicy),
		"new_security_policy": normalizedAppSecurityPolicy(updated.SecurityPolicy),
	})
	writeJSON(w, http.StatusOK, api.AppSecurityResponse{
		RequireSigned: updated.RequireSigned, SecurityPolicy: normalizedAppSecurityPolicy(updated.SecurityPolicy),
	})
}

// getAppSecurity returns a deterministic, read-only posture report. It is
// intentionally configuration-only: the response contains no credentials,
// raw IP ranges, or rule action payloads.
func (s *server) getAppSecurity(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	posture, err := s.appSecurityPosture(r.Context(), app)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load app security posture"))
		return
	}
	writeJSON(w, http.StatusOK, posture)
}

// recoverAppSecurityQuarantine restores an app only after the caller has
// supplied a newer live deployment whose scan is complete, digest-matched,
// and free of HIGH, CRITICAL, and UNKNOWN findings. Every other live canary
// row must satisfy the same evidence gate so recovery cannot expose an unsafe
// sibling through weighted traffic.
func (s *server) recoverAppSecurityQuarantine(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	var req api.SecurityQuarantineRecoveryRequest
	if err := decodeJSON(r, &req); err != nil || strings.TrimSpace(req.DeploymentID) == "" {
		api.WriteProblem(w, api.ErrValidation("deployment_id is required"))
		return
	}
	target, err := s.store.DeploymentByID(r.Context(), req.DeploymentID)
	if err != nil || target.AppID != app.ID {
		s.notFound(w, "no such deployment")
		return
	}
	parked, err := s.store.LatestParkedDeploymentForApp(r.Context(), app.ID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.ErrSecurityQuarantineRecoveryBlocked("the app has no recorded security quarantine; deploy a clean image before retrying"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not load security quarantine"))
		return
	}
	if parked.ParkedReason != string(state.ParkReasonSecurityScanRegressed) {
		api.WriteProblem(w, api.ErrSecurityQuarantineRecoveryBlocked("the latest parked deployment was not quarantined for a security scan regression"))
		return
	}
	if app.Status != state.AppEvictedCold && app.Status != state.AppActive {
		api.WriteProblem(w, api.ErrSecurityQuarantineRecoveryBlocked("the app is not in a recoverable security-quarantine state"))
		return
	}
	if target.Status != state.DeployLive {
		api.WriteProblem(w, api.ErrSecurityQuarantineRecoveryBlocked("recovery requires a live replacement deployment"))
		return
	}
	if parked.ImageDigest == "" || target.ImageDigest == "" || target.ImageDigest == parked.ImageDigest {
		api.WriteProblem(w, api.ErrSecurityQuarantineRecoveryBlocked("the replacement deployment must use a different image digest from the quarantined deployment"))
		return
	}
	if parked.ParkedAt == nil || target.CreatedAt.IsZero() || !target.CreatedAt.After(*parked.ParkedAt) {
		api.WriteProblem(w, api.ErrSecurityQuarantineRecoveryBlocked("the replacement deployment must have been created after the quarantine"))
		return
	}
	live, err := s.store.LiveDeployments(r.Context(), app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load live security deployments"))
		return
	}
	if len(live) == 0 {
		api.WriteProblem(w, api.ErrSecurityQuarantineRecoveryBlocked("recovery requires at least one live replacement deployment"))
		return
	}
	for _, dep := range live {
		if !s.securityScanEvidenceClean(dep) {
			api.WriteProblem(w, api.ErrSecurityQuarantineRecoveryBlocked(fmt.Sprintf("deployment %s does not have complete, digest-matched clean scan evidence", dep.ID)))
			return
		}
	}
	if app.Status != state.AppActive {
		claimed, casErr := transitionSecurityRecovery(r.Context(), s.store, app.ID, app.Status)
		if casErr != nil {
			api.WriteProblem(w, api.ErrCapacity("could not restore quarantined app"))
			return
		}
		if !claimed {
			current, readErr := s.store.AppByID(r.Context(), app.ID)
			if readErr != nil || current.Status != state.AppActive {
				api.WriteProblem(w, api.ErrSecurityQuarantineRecoveryBlocked("the app lifecycle changed while recovery was being validated; retry"))
				return
			}
		}
		if claimed && s.audit != nil {
			s.audit.Emit(r.Context(), "app.security_quarantine_recovered", &acct.ID, map[string]any{
				"app_id": app.ID, "slug": app.Slug,
				"quarantined_deployment_id": parked.ID,
				"quarantined_image_digest":  parked.ImageDigest,
				"deployment_id":             target.ID,
				"image_digest":              target.ImageDigest,
			})
		}
	}
	payload, err := json.Marshal(map[string]any{
		"kind": "security_recovered", "app_id": app.ID, "slug": app.Slug,
		"status": string(state.AppActive), "deployment_id": target.ID,
		"lifecycle_changed": true,
	})
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not encode security recovery notification"))
		return
	}
	if err := s.notif.Notify(r.Context(), db.NotifyAppChanged, string(payload)); err != nil {
		s.log.Warn("apid: notify security recovery", "app", app.ID, "err", err)
		api.WriteProblem(w, api.ErrCapacity("could not publish security recovery"))
		return
	}
	recoveredAt := time.Now().UTC()
	writeJSON(w, http.StatusOK, api.SecurityQuarantineRecoveryResponse{
		AppID: app.ID, Slug: app.Slug, DeploymentID: target.ID,
		ImageDigest: target.ImageDigest, RecoveredAt: recoveredAt,
		Status: string(state.AppActive),
	})
}

func (s *server) securityScanEvidenceClean(dep state.Deployment) bool {
	if dep.ScanStatus != "complete" || dep.ScannedAt.IsZero() || dep.ImageDigest == "" {
		return false
	}
	if len(dep.ScanResult) == 0 {
		return false
	}
	var raw api.ScanResult
	if err := json.Unmarshal(dep.ScanResult, &raw); err != nil || raw.Status != "complete" ||
		strings.TrimSpace(raw.ImageDigest) != strings.TrimSpace(dep.ImageDigest) ||
		raw.ArtifactDigest == "" || raw.ScannerVersion == "" || raw.ScannerDBVersion == "" ||
		raw.ScannerDBBuiltAt == "" || !strings.EqualFold(raw.ScannerDBStatus, "valid") ||
		raw.Vulnerabilities == nil || raw.Error != "" {
		return false
	}
	now := time.Now().UTC()
	scannedAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(raw.ScannedAt))
	if err != nil || scannedAt.After(now) || now.Sub(scannedAt) > 5*time.Minute {
		return false
	}
	dbBuiltAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(raw.ScannerDBBuiltAt))
	if err != nil || dbBuiltAt.After(now) || now.Sub(dbBuiltAt) > 30*24*time.Hour {
		return false
	}
	scan := s.scanResponse(dep)
	if scan == nil || scan.Status != "complete" || scan.ImageDigest != dep.ImageDigest || scan.Error != "" {
		return false
	}
	counts := scan.SeverityCounts
	return counts.Critical == 0 && counts.High == 0 && counts.Unknown == 0
}

type securityRecoveryStatusStore interface {
	CompareAndSetAppStatus(context.Context, string, state.AppStatus, state.AppStatus) (bool, error)
}

func transitionSecurityRecovery(ctx context.Context, store state.Store, appID string, current state.AppStatus) (bool, error) {
	if cas, ok := store.(securityRecoveryStatusStore); ok {
		return cas.CompareAndSetAppStatus(ctx, appID, current, state.AppActive)
	}
	active := state.AppActive
	_, err := store.UpdateApp(ctx, appID, state.UpdateAppParams{Status: &active})
	return err == nil, err
}

func normalizedAppSecurityPolicy(policy api.AppSecurityPolicy) api.AppSecurityPolicy {
	if !policy.Valid() {
		return api.AppSecurityPolicyOff
	}
	return policy
}

func (s *server) appSecurityPosture(ctx context.Context, app state.App) (api.AppSecurityPostureResponse, error) {
	rules, err := s.store.ListEdgeRulesForApp(ctx, app.ID)
	if err != nil {
		return api.AppSecurityPostureResponse{}, err
	}

	profile := "public"
	if app.Visibility == api.AppVisibilityInternal || app.PublicAuthMode == state.AppPublicAuthModeInternalOnly {
		profile = "internal"
	} else if app.RequireAuthn || app.PublicAuthMode != state.AppPublicAuthModeOpen {
		profile = "authenticated"
	}

	findings := make([]api.AppSecurityFinding, 0, 5)
	if profile == "public" {
		findings = append(findings, api.AppSecurityFinding{
			Code: "anonymous_access", Severity: postureSeverityHigh,
			Title:       "App accepts anonymous requests",
			Detail:      "The public URL is open and the app does not require authentication.",
			Remediation: "Set public_auth.mode to bearer, basic, ip_allowlist, or internal_only.",
		})
	} else if app.RequireAuthn && app.PublicAuthMode == state.AppPublicAuthModeOpen {
		findings = append(findings, api.AppSecurityFinding{
			Code: "ambiguous_auth_policy", Severity: postureSeverityMedium,
			Title:       "Authentication policy is split across legacy controls",
			Detail:      "require_authn is enabled while public_auth.mode remains open.",
			Remediation: "Use public_auth.mode=bearer so the public policy is explicit.",
		})
	}

	hasThrottle := false
	wildcardCORS := false
	wildcardCredentials := false
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		if rule.Kind == state.EdgeRuleKindThrottle {
			hasThrottle = true
		}
		if rule.Kind == state.EdgeRuleKindCORSA && rule.Action.CORS != nil {
			for _, origin := range rule.Action.CORS.AllowOrigins {
				if origin == "*" {
					wildcardCORS = true
					wildcardCredentials = wildcardCredentials || rule.Action.CORS.AllowCredentials
				}
			}
		}
	}
	if !hasThrottle {
		findings = append(findings, api.AppSecurityFinding{
			Code: "missing_rate_limit", Severity: postureSeverityMedium,
			Title:       "No app-level rate limit is configured",
			Detail:      "Traffic can reach the app without a customer-configured throttle rule.",
			Remediation: "Add an enabled throttle edge rule for the public routes.",
		})
	}
	if wildcardCORS {
		severity := postureSeverityMedium
		detail := "A CORS rule allows requests from every origin."
		if wildcardCredentials {
			severity = postureSeverityHigh
			detail = "A CORS rule allows every origin while also allowing credentials."
		}
		findings = append(findings, api.AppSecurityFinding{
			Code: "wildcard_cors", Severity: severity,
			Title: "CORS allows a wildcard origin", Detail: detail,
			Remediation: "Replace * with the smallest set of trusted origins.",
		})
	}
	if !app.RequireSigned {
		findings = append(findings, api.AppSecurityFinding{
			Code: "unsigned_deploys", Severity: postureSeverityLow,
			Title:       "Image signature enforcement is disabled",
			Detail:      "OCI deploys are not required to carry a trusted signature.",
			Remediation: "Have an administrator enable require_signed after adding a trusted signer.",
		})
	}
	if !app.OnlyAllowDeclaredRoutes {
		findings = append(findings, api.AppSecurityFinding{
			Code: "undeclared_routes_allowed", Severity: postureSeverityLow,
			Title:       "Undeclared routes can reach the app",
			Detail:      "The gateway does not reject paths absent from the app route contract before wake.",
			Remediation: "Enable only_allow_declared_routes after importing or declaring the API routes.",
		})
	}

	// Keep the report stable if checks are added or reordered in the future.
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Severity != findings[j].Severity {
			return postureSeverityRank(findings[i].Severity) < postureSeverityRank(findings[j].Severity)
		}
		return findings[i].Code < findings[j].Code
	})
	score := 100
	for _, finding := range findings {
		switch finding.Severity {
		case "high":
			score -= 25
		case "medium":
			score -= 15
		case "low":
			score -= 5
		}
	}
	if score < 0 {
		score = 0
	}
	quarantine, err := s.securityQuarantineForApp(ctx, app)
	if err != nil {
		return api.AppSecurityPostureResponse{}, err
	}
	return api.AppSecurityPostureResponse{
		AppID: app.ID, Slug: app.Slug, Profile: profile, Score: score,
		SecurityPolicy: normalizedAppSecurityPolicy(app.SecurityPolicy), Findings: findings,
		Quarantine: quarantine,
	}, nil
}

func (s *server) securityQuarantineForApp(ctx context.Context, app state.App) (*api.AppSecurityQuarantine, error) {
	if app.Status != state.AppEvictedCold {
		return nil, nil
	}
	dep, err := s.store.LatestParkedDeploymentForApp(ctx, app.ID)
	if errors.Is(err, state.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if dep.ParkedReason != string(state.ParkReasonSecurityScanRegressed) {
		return nil, nil
	}
	return &api.AppSecurityQuarantine{
		DeploymentID: dep.ID, ImageDigest: dep.ImageDigest,
		Reason: dep.ParkedReason, ParkedAt: dep.ParkedAt,
	}, nil
}

// enforceSecurityPostureGate rejects image deploys when an app has opted into
// enforcement and the current configuration contains a high-severity finding.
// Warn and off remain non-blocking so customers can stage the policy safely.
func (s *server) enforceSecurityPostureGate(ctx context.Context, app state.App) *api.Problem {
	if normalizedAppSecurityPolicy(app.SecurityPolicy) != api.AppSecurityPolicyEnforce {
		return nil
	}
	posture, err := s.appSecurityPosture(ctx, app)
	if err != nil {
		return api.ErrCapacity("could not load app security posture")
	}
	codes := make([]string, 0, len(posture.Findings))
	for _, finding := range posture.Findings {
		if finding.Severity == postureSeverityHigh || finding.Severity == postureSeverityCritical {
			codes = append(codes, finding.Code)
		}
	}
	if len(codes) == 0 {
		return nil
	}
	return api.ErrSecurityPostureBlocked(strings.Join(codes, ", "))
}

func postureSeverityRank(severity string) int {
	switch severity {
	case "critical":
		return 0
	case "high":
		return 1
	case "medium":
		return 2
	case "low":
		return 3
	default:
		return 4
	}
}
