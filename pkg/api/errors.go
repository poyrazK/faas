package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api/canary"
)

// docsBase is the canonical documentation URL prefix sourced
// from pkg/wire.DocsHost. Every WithDocs() / Type: / example
// value in this file composes against this constant so a future
// host rotation only edits pkg/wire/docs.go + this constant, not
// every call site.
//
// Duplication note: pkg/wire.DocsHost and this constant must
// stay in lock-step — pkg/api cannot import pkg/wire (pkg/wire
// imports pkg/api for api.Plans, creating a cycle). The
// TestLintTripwire_NoLiteralDocsDomainEverywhere tripwire in
// cmd/gregale/lint_tripwires_test.go enforces the invariant via
// an explicit "docs.gregale.dev must appear in pkg/wire/docs.go
// AND pkg/api/errors.go (and nowhere else)" check (see the
// host-mirror assertion added in PR-BC).
//
// Mirrors cmd/gregale/output.go:118's docsURLBase precedent.
const docsBase = "https://docs.gregale.dev"

// AsProblem walks err's chain and returns the first *Problem. Returns nil
// if none of the wrapped errors is a *Problem. Used by gRPC handlers in
// pkg/vmmdgrpc to lift a Manager-emitted error without leaking internal
// strings through the wire.
func AsProblem(err error) *Problem {
	if err == nil {
		return nil
	}
	var p *Problem
	if errors.As(err, &p) {
		return p
	}
	return nil
}

// Problem is an RFC 7807 problem+json body. It is the platform's single error
// contract: apid emits it, the CLI and dashboard render it verbatim (spec
// §Conventions, UX spec §7). Every limit error carries the limit, the observed
// value, and a docs URL so the surface never has to invent copy.
type Problem struct {
	// Type is a docs URL identifying the problem class (RFC 7807 "type").
	Type string `json:"type"`
	// Title is a short, stable, human-readable summary.
	Title string `json:"title"`
	// Status is the HTTP status code, duplicated in the body per RFC 7807.
	Status int `json:"status"`
	// Code is a stable machine-readable string (e.g. "plan_limit_apps") that
	// clients branch on. It must never change once shipped.
	Code string `json:"code"`
	// Detail is the specific cause including the observed value.
	Detail string `json:"detail,omitempty"`
	// Limit and Observed are set on quota/limit errors (spec §Conventions).
	Limit    *int64 `json:"limit,omitempty"`
	Observed *int64 `json:"observed,omitempty"`
	// DocsURL points the user at the single next action.
	DocsURL string `json:"docs_url,omitempty"`
	// CheckoutURL is the provider-neutral hosted checkout URL for a paid
	// upgrade. PaddleCheckoutURL remains below for backwards compatibility
	// with older SDKs that only know the Paddle-specific field.
	CheckoutURL string `json:"checkout_url,omitempty"`
	// BillingPortalURL is set on payment_required (CodePayment) errors
	// when the customer must manage an existing subscription. It may be a
	// provider-created session URL or an operator-controlled URL with the
	// account id substituted.
	BillingPortalURL string `json:"billing_portal_url,omitempty"`
	// PaddleCheckoutURL is set on payment_required (CodePayment) errors
	// when the platform is running on the Paddle provider. Mirrors
	// BillingPortalURL's shape — the customer's next action is to land
	// on a Paddle-hosted checkout page for the target plan. Optional +
	// omitempty so responses that use the provider-neutral field remain
	// compact. CheckoutURL and PaddleCheckoutURL are mutually exclusive
	// for Polar; legacy Paddle responses continue to carry both checkout
	// aliases as needed.
	PaddleCheckoutURL string `json:"paddle_checkout_url,omitempty"`
	// TxID is the provider's transaction handle (Paddle: txn_…,
	// Stripe: empty). The dashboard renders this as a confirmation id
	// after the customer completes checkout. Empty on the Stripe path.
	TxID string `json:"tx_id,omitempty"`
	// Errors carries per-field validation detail (Cloudflare / Stripe
	// shape). Populated by 422 sites that emit a list of field-level
	// failures — used today by the kind=validate edge rule so a
	// customer's JSON Schema rejection renders as a form-field list
	// the dashboard can iterate without parsing prose. Optional +
	// omitempty so every other problem+json site keeps its existing
	// flat shape unchanged.
	Errors []FieldError `json:"errors,omitempty"`
	// SecretFindings carries per-line secret-scan detail for the
	// 422 secret_scan_strict path. Populated by cmd/apid/secretscan.go
	// when the server-side scan rejects an upload and by
	// cmd/gregale/printErr when --secret-scan=strict fires locally. The
	// shape is shared with the on-disk Deployment.SecretScan response
	// (cmd/apid/handlers_ext.go::secretScanResponse) so a programmatic
	// consumer can render the same UI for either rejection path.
	// Optional + omitempty so every other problem+json site keeps its
	// existing flat shape unchanged.
	SecretFindings []SecretFinding `json:"secret_findings,omitempty"`
	// SecretHint is the customer-facing remediation nudge attached to
	// a strict-mode 422 envelope (e.g. "move detected secrets to
	// `gregale secrets set`"). Mirrors FieldError-shaped metadata so
	// the dashboard / SDK can render the hint as a one-line footer
	// without parsing prose. Optional + omitempty.
	SecretHint string `json:"secret_hint,omitempty"`
	// Hint is the single short next-action line shown on the CLI's
	// 3-5 line renderer (spec §6.4 amendment 1). Mirrors SecretHint
	// shape — a one-line remediation nudge. Distinct from SecretHint:
	// Hint is generic across all error codes, SecretHint is
	// narrow to the strict secret-scan path. Optional + omitempty so
	// every other problem+json site keeps its existing flat shape
	// unchanged.
	Hint string `json:"hint,omitempty"`
	// Why is the human-readable explanation of why the failure
	// happened, including the observed value (e.g. "bound to
	// 127.0.0.1; guest at 10.0.0.2 only sees requests proxied via
	// the bridge"). Distinct from Detail: Detail is the platform's
	// machine-stable message; Why is the customer-facing prose
	// surfaced only on error UX paths. Multi-line ok (≤ 512 bytes,
	// CLI tripwire enforces). Optional + omitempty.
	Why string `json:"why,omitempty"`
	// Fix is the prescriptive remediation (e.g. "set
	// `app.listen('0.0.0.0')` or run `gregale env set PORT 8080`").
	// Distinct from Hint: Hint is a single short line; Fix may be
	// 1-3 lines. Optional + omitempty.
	Fix string `json:"fix,omitempty"`
	// RelevantLogs are the last N log lines that explain the failure,
	// surfaced inline by the CLI renderer when the server attaches
	// them. Capped at 20 entries × 512 bytes per Message (CLI
	// tripwire enforces). Distinct from SecretFindings which is
	// secret-scan specific. Optional + omitempty so every other
	// problem+json site keeps its existing flat shape unchanged.
	RelevantLogs []LogExcerpt `json:"relevant_logs,omitempty"`
	// extraHeaders are non-JSON response headers attached via WithHeader.
	// Kept unexported so the wire body (RFC 7807 problem+json) is
	// exactly the spec; WriteProblem flushes these onto the wire
	// before WriteHeader. nil = no extras.
	extraHeaders map[string][]string `json:"-"`
}

// LogExcerpt is one entry of Problem.RelevantLogs — a small,
// shape-stable slice of a log line that explains the failure inline.
// Distinct from SecretFinding (which is narrow to secret-scan): the
// CLI renderer prints this as a fenced block under the 3-5 line
// explanation. Timestamp is RFC 3339; Level is one of info|warn|error;
// Source tags the log origin so the customer can attribute the line
// (build vs vm-init vs app vs gateway); Message is the line content,
// capped at 512 bytes server-side (CLI tripwire enforces the cap
// client-side as well).
type LogExcerpt struct {
	Timestamp string `json:"ts"`
	Level     string `json:"level"`
	Source    string `json:"source,omitempty"`
	Message   string `json:"message"`
}

// FieldError is one per-field entry of Problem.Errors. The shape mirrors
// Cloudflare's API Shield 422 + Stripe's card_errors family so an SDK can
// iterate `errors[]` to drive form-field UI without parsing prose. Field
// uses dotted-path JSON Pointer notation ("address.zip"), expected / got
// are short stable strings; consumers should not depend on the prose.
type FieldError struct {
	Field    string `json:"field"`
	Expected string `json:"expected"`
	Got      string `json:"got,omitempty"`
}

// SecretFinding is one per-line entry of Problem.SecretFindings. The
// shape mirrors pkg/secretscan.Finding but is decoupled so the wire
// schema can evolve independently of the scanner's internal fields
// (which carry Line + Severity as unexported-int enums). Snippet is the
// pre-truncated safe representation (first 6 chars + "…" + last 4) —
// never the raw value, matching the snippet policy documented in
// pkg/secretscan/scan.go.
type SecretFinding struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Key      string `json:"key,omitempty"`
	Provider string `json:"provider"`
	Severity string `json:"severity"`
	Snippet  string `json:"snippet"`
	// Layer is the per-walk source label ("app" for the main
	// image, "sidecar-<slug>" for each sidecar, "" for the apid
	// source-tree scanner). Added in PR-A so the dashboard can
	// attribute findings to the right image segment. The
	// omitempty keeps the apid source-tree rejections at
	// /v1/projects[/scan] from picking up an empty layer field
	// (the cmd/apid 422 path doesn't know about image layers).
	Layer string `json:"layer,omitempty"`
}

// Error implements the error interface so a Problem can flow through %w chains.
func (p *Problem) Error() string {
	if p.Detail != "" {
		return fmt.Sprintf("%s: %s", p.Code, p.Detail)
	}
	return p.Code
}

// WriteProblem renders p as an RFC 7807 problem+json response with its status
// code. Every HTTP surface (gatewayd-internal, apid) uses this so error shape is uniform.
func WriteProblem(w http.ResponseWriter, p *Problem) {
	w.Header().Set("Content-Type", "application/problem+json")
	for k, vs := range p.extraHeaders {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}

// WriteProblemWithErrors is the kind=validate-shaped variant: the
// same problem+json envelope but with a populated Errors []FieldError
// so a customer's JSON-Schema rejection renders as a structured
// per-field list (Cloudflare API Shield 422 + Stripe card_errors
// shape). The Errors slice is assigned to p before the encode so the
// wire body matches the in-memory struct; nil errs produces an
// empty-array (no `errors` key, since FieldError.Errors is omitempty).
//
// Used by pkg/gateway/handler.go::applyEdgeRuleValidate only; all
// other error sites keep the flat WriteProblem shape.
func WriteProblemWithErrors(w http.ResponseWriter, p *Problem, errs []FieldError) {
	p.Errors = errs
	WriteProblem(w, p)
}

// NewProblem builds a Problem with the common fields set.
func NewProblem(status int, code, title, detail string) *Problem {
	return &Problem{Status: status, Code: code, Title: title, Detail: detail}
}

// WithLimit annotates a Problem with the limit and observed value that tripped
// it, returning the same pointer for chaining.
func (p *Problem) WithLimit(limit, observed int64) *Problem {
	p.Limit = &limit
	p.Observed = &observed
	return p
}

// WithDocs sets the docs URL and returns the same pointer for chaining.
func (p *Problem) WithDocs(url string) *Problem {
	p.DocsURL = url
	return p
}

// WithSecretScan attaches the per-line findings + customer-facing
// hint that the cmd/apid server-side secret-scan rejection (and the
// CLI's --secret-scan=strict mode) emit. The fields are flat on the
// RFC 7807 problem body so a programmatic consumer can render the
// same one-line-per-finding UI for both rejection paths. Returns the
// same pointer for chaining.
func (p *Problem) WithSecretScan(findings []SecretFinding, hint string) *Problem {
	p.SecretFindings = findings
	p.SecretHint = hint
	return p
}

// WithHint attaches the single short next-action line shown on the
// CLI's 3-5 line renderer (spec §6.4 amendment 1). Mirrors
// SecretHint shape — a one-line remediation nudge. Returns the same
// pointer for chaining.
func (p *Problem) WithHint(hint string) *Problem {
	p.Hint = hint
	return p
}

// WithWhy attaches the human-readable explanation of why the failure
// happened (spec §6.4 amendment 1). Distinct from Detail: Detail is
// the platform's machine-stable message; Why is the customer-facing
// prose surfaced only on error UX paths. Multi-line ok (≤ 512 bytes;
// CLI tripwire enforces). Returns the same pointer for chaining.
func (p *Problem) WithWhy(why string) *Problem {
	p.Why = why
	return p
}

// WithFix attaches the prescriptive remediation (spec §6.4
// amendment 1). Distinct from Hint: Hint is a single short line; Fix
// may be 1-3 lines. Returns the same pointer for chaining.
func (p *Problem) WithFix(fix string) *Problem {
	p.Fix = fix
	return p
}

// WithRelevantLogs attaches the last N log lines that explain the
// failure, surfaced inline by the CLI renderer. Capped at 20
// entries × 512 bytes per Message (CLI tripwire enforces). Returns
// the same pointer for chaining.
func (p *Problem) WithRelevantLogs(logs []LogExcerpt) *Problem {
	p.RelevantLogs = logs
	return p
}

// WithHeader attaches a single response header to the Problem so
// gatewayd-internal's writeWakeError can write it onto the wire without
// branches on each error code. Used today by the build-attestation
// transient-I/O path (Retry-After: 5 — review finding #1a on
// PR #322). Multiple WithHeader calls compose: each call appends a
// new key. Returns the same pointer for chaining.
func (p *Problem) WithHeader(key, value string) *Problem {
	if p.extraHeaders == nil {
		p.extraHeaders = map[string][]string{}
	}
	p.extraHeaders[key] = append(p.extraHeaders[key], value)
	return p
}

// HasHeader returns the slice of values attached to key (nil if
// none). Exposed so tests + callers can verify the header was
// recorded without reaching into the unexported field.
func (p *Problem) HasHeader(key string) []string {
	if p.extraHeaders == nil {
		return nil
	}
	return p.extraHeaders[key]
}

// Stable error codes (spec Appendix A, UX spec §7). Keep in sync with docs and
// the CLI's exit-code mapping.
const (
	CodePlanLimitApps   = "plan_limit_apps"
	CodePlanLimitRAM    = "plan_limit_ram"
	CodePlanLimitConcur = "plan_limit_concurrency"
	CodeSourceTooLarge  = "source_too_large"
	CodeSourceInvalid   = "source_invalid"
	// CodeSecretScanStrict is the 422 sentinel returned by both
	//   - cmd/apid/scan_service.go (server-side tree scan rejected)
	//   - cmd/gregale/printErr (--secret-scan=strict client-side rejected)
	// The Problem body carries SecretFindings []SecretFinding +
	// SecretHint string so the SDK can render the same UI for either
	// rejection path. Distinct from CodeSourceInvalid (tarball shape
	// is fine — the SECRET inside is the problem).
	CodeSecretScanStrict = "secret_scan_strict"
	// CodeImageSecretDetected is the wire-stable 422 sentinel
	// for imaged-side secret findings in the assembled image
	// (PR-A). Distinct from CodeSecretScanStrict (the
	// cmd/apid source-tree upload path) because the scan
	// source is different: CodeSecretScanStrict fires on the
	// customer's source-tree bytes; CodeImageSecretDetected
	// fires on the post-build OCI image bytes (Dockerfile
	// ENV, --build-arg, COPY'd .env files). Both surface as
	// 422 envelopes with `extra.secret_findings`, but the
	// dashboard renders the image-layer case separately so
	// the customer knows to look at the build path rather
	// than the source tree.
	CodeImageSecretDetected = "image_secret_detected"
	// CodeInvalidRef is the DEPLOY-PROV-4 / ADR-092 (issue #739)
	// 400 sentinel for POST /v1/apps/{slug}/deployments/source-ref
	// when the supplied ref is not a valid commit SHA / branch /
	// tag, OR the GitHub API failed to resolve it to a SHA. Distinct
	// from CodeSourceInvalid (which fires after the tarball lands
	// and the shape check rejects it): this is upstream of the
	// fetch, on the wire itself.
	CodeInvalidRef = "invalid_ref"
	// CodeGitHubInstallNotFound is the DEPLOY-PROV-4 / ADR-092 (issue
	// #739) 404 sentinel for POST /v1/apps/{slug}/deployments/source-ref
	// when the account has no durable github_installations row. The
	// customer must complete the dashboard `gregale connect` bind
	// once to seed the install row before CI can drive source-ref
	// deploys. Distinct from CodeNotFound so the dashboard can render
	// a "bind first" CTA rather than a generic 404.
	CodeGitHubInstallNotFound = "github_install_not_found"
	// CodeSourceRefUnavailable is the DEPLOY-PROV-4 / ADR-092 (issue
	// #739) 503 sentinel for POST /v1/apps/{slug}/deployments/source-ref
	// when the githubd bridge is down (StreamSourceRef returns
	// Unavailable) or when a 401 from codeload.github.com survives
	// one cache-invalidate + retry. The 503 + Retry-After pair
	// mirrors CodeCapacity / CodeBuildXXX — the failure is transient
	// and the customer's CLI/CI will retry on the backoff.
	CodeSourceRefUnavailable = "source_ref_unavailable"
	CodeAppLayerTooBig       = "app_layer_too_large"
	CodeBuildUndetected      = "build_undetected"
	CodeBuildOOM             = "build_oom"
	CodeBuildTimeout         = "build_timeout"
	// CodeStage* (ADR-117 §Production-ready follow-on): per-stage
	// RFC 7807 stable codes for the closed-6 deploy stage vocabulary.
	// Distinct from CodeBuildXXX (which mark the whole build VM's
	// fate) — CodeStageXXX marks which stage tripped. The same code
	// may surface as the deployment row's error_code (cluster-A
	// path) AND on the stage row's failure reason (the renderer
	// path). The renderer (pkg/dashboard/stages.FormatStageDuration
	// + StageFailureHTML) consults pkg/whycopy for the customer-
	// facing prose; these constants are the catalog keys.
	//
	// Codes are emitted by pkg/imaged.transitionWithStage /
	// markDeployFailed when they can identify a single stage cause.
	// A failure that does not map to one of these codes still flows
	// through the renderer — it just renders the bare "failed:
	// <reason>" string without the structured Hint/Why/Fix block.
	CodeStageSourceDownloadFailed    = "stage_source_download_failed"
	CodeStageDependencyRestoreFailed = "stage_dependency_restore_failed"
	CodeStageImageBuildOOM           = "stage_image_build_oom"
	CodeStageImageBuildTimeout       = "stage_image_build_timeout"
	CodeStageSecurityScanFindings    = "stage_security_scan_findings"
	CodeStageSnapshotPrepareTimeout  = "stage_snapshot_prepare_timeout"
	CodeStageReadinessFailed         = "stage_readiness_failed"
	CodeQuotaExhausted               = "quota_exhausted"
	CodeBillingPastDue               = "billing_past_due"
	// CodeBillingNotImplemented is returned when the selected
	// billing provider (FAAS_BILLING_PROVIDER) does not implement the
	// requested method (issue #279: Paddle's Refund). Distinct from
	// CodeForbidden / CodeValidation so the dashboard / CLI can
	// surface "switch providers to use this surface" instead of a
	// generic error. Maps to HTTP 501.
	CodeBillingNotImplemented = "billing_not_implemented"
	CodeCapacity              = "capacity_unavailable"
	// CodeWaitForWarm marks a wake that was held by the customer's
	// per-app scale-out cooldown (issue #462 / PR-D). Distinct
	// from CodePlanLimitConcur (the customer's plan is fine; their
	// ScaleOutCooldownS is holding the wake) and from
	// CodeAppConcurReached (per-app concurrency exhausted, not
	// relevant at the wake-gate). The 503 status mirrors
	// CodeCapacity / CodeBuildXXX / CodeOAuthProviderUnavailable
	// (existing 503 surface for transient / recoverable conditions).
	// The Retry-After header is the canonical UX: the constructor
	// bounds it at 1 second so the wire always emits a non-zero
	// hint.
	CodeWaitForWarm = "wait_for_warm"
	// CodeMirrorSlotAtCapacity (issue #72 / ADR-125 PR-A3) is the
	// per-rule mirror VM concurrency cap reached. Wire shape
	// mirrors CodeWaitForWarm (gRPC ResourceExhausted, HTTP 503):
	// the cap is a deliberate, bounded, transient outcome — the
	// customer's next mirror request will likely succeed once an
	// in-flight mirror VM parks. The gateway dispatch goroutine
	// catches this code and writes a ledger entry with
	// status_diff=true + metric result=cap_at_max (see
	// pkg/gateway/mirror_dispatch.go).
	CodeMirrorSlotAtCapacity = "mirror_slot_at_capacity"
	// CodeEdgeRuleMaintenance marks a kind=maintenance edge-rule
	// hit on the gatewayd hot path (ADR-091 amendment, PR-A
	// #???). The customer configured an (host, path, http_method)
	// tuple to return 503 + Retry-After; the gate fires BEFORE
	// auth and BEFORE wake so a maintenance 503 never pays a
	// cold-boot cost. Distinct from CodeCapacity / CodeWaitForWarm
	// (transient platform-state 503s) — this is a deliberate,
	// long-lived customer off-switch. 503 status via StatusForCode.
	// The Retry-After header is the canonical UX; the builder
	// below bounds it at 1 second so the wire always emits a
	// non-zero hint. Distinct from CodeAppMaintenance (the
	// per-app coarse sibling on apps.maintenance_mode) so the two
	// primitives are differentiable on dashboards.
	CodeEdgeRuleMaintenance = "edge_rule_maintenance"
	// CodeAppMaintenance marks an apps.maintenance_mode coarse-gate
	// hit on the gatewayd hot path (ADR-091 amendment). The
	// customer pinned the whole app via PATCH /v1/apps/{slug};
	// the gatewayd applier (applyAppsMaintenanceMode, §4.1.2.0)
	// short-circuits every request to this app with 503 +
	// Retry-After BEFORE auth, BEFORE wake. Distinct from
	// CodeEdgeRuleMaintenance (the per-route fine-grained kind).
	CodeAppMaintenance = "app_maintenance_mode"
	// CodeAdmissionRefused marks a wake that schedd refused because
	// the account's current-month overage cents met/exceeded
	// accounts.overage_cap_cents (issue #561 / PR-XXX). Distinct
	// from CodeCapacity (503, transient) and CodePlanLimitConcur
	// (429, plan-shape per-app cap): the cap is a deliberate customer
	// budget that requires customer action (raise the cap) — not a
	// retry. HTTP 402 is consistent with CodePlanFeatureGated,
	// CodePlanCronsNotAllowed, and CodePlanAlertRulesNotAllowed —
	// "your account's setting is blocking us"; no Retry-After header.
	// The Problem's Limit + Observed pointer fields carry
	// cap_cents + current_overage_cents (via WithLimit at the
	// builder below) so a script can compute the raise amount
	// without parsing prose.
	CodeAdmissionRefused = "admission_refused"
	// CodeExportRateLimited marks a GET /v1/account/export that
	// landed inside the per-account 24h rate window (issue #755 /
	// PR-5.1). Distinct from CodeQuotaExhausted (plan-level monthly
	// usage cap, 429) and CodePlanLimitConcur (per-app concurrency,
	// 429) — this is a self-imposed abuse-mitigation on a GDPR
	// endpoint, not a billing gate. Maps to HTTP 429 + Retry-After:
	// the window is 24h so the retry hint is in seconds-until-reset.
	CodeExportRateLimited = "export_rate_limited"
	CodeUnauthorized      = "unauthorized"
	// CodeForbidden is returned when the authenticated principal lacks
	// the scope required by the route (IAM-1, ADR-034). Distinct from
	// CodeUnauthorized so a customer can tell "I need to log in" from
	// "my key does not have permission for this endpoint".
	CodeForbidden  = "insufficient_scope"
	CodeNotFound   = "not_found"
	CodeValidation = "validation_failed"
	CodeConflict   = "conflict"
	// CodeInternal is returned by handlers when an unexpected server-side
	// failure surfaces to the caller (DB Tx commit, network blip, partial
	// state). Distinct from CodeCapacity (503, "we ran out of headroom")
	// because the failure mode is "we tried and didn't succeed" rather
	// than "we deliberately refused". Use this for any 500 where the
	// handler can't recover; pair with api.ErrInternal for a one-liner.
	CodeInternal = "internal_error"
	// CodeBadRequest is returned by handlers for a 400 on a
	// malformed inbound body that isn't covered by a more specific
	// code (e.g. the validate rule's body-read failure). Distinct
	// from CodeValidation (422, schema-level rejection) so the
	// dashboard pivots the message from "fix the format" to
	// "the schema is wrong".
	CodeBadRequest = "bad_request"
	// CodeBadGateway is returned by the gateway when an upstream
	// dependency (the validate-rule compile-time defense, a JWKS
	// fetch, etc.) fails in a way that's clearly the gateway's
	// fault rather than the customer's. 502 + this code = the
	// operator's on-call should look at the daemon.
	CodeBadGateway = "bad_gateway"
	// CodeUnsupportedMediaType is returned when a kind=validate
	// rule's ContentTypes gate rejects the inbound request.
	// Distinct from CodeBadRequest so the dashboard pivots the
	// message to "send a different Content-Type".
	CodeUnsupportedMediaType = "unsupported_media_type"
	// CodeRequestTooLarge is returned when the inbound body
	// exceeds the per-rule cap (kind=validate MaxBodyBytes) or
	// the plan's outer cap (api.MaxRequestBodyBytes). Distinct
	// from CodeBadRequest so the dashboard pivots the message
	// to "send a smaller body" — the customer's app's UI can
	// chunk on receipt.
	CodeRequestTooLarge = "request_too_large"
	// CodeMFARequired is returned by requireMFA when a session-cookie
	// principal is mfa_pending and the route is not on the MFA
	// allowlist (IAM-2 / issue #186). Distinct from CodeForbidden so
	// the dashboard can pivot the message from "your key is wrong"
	// to "complete enrollment or step-up to continue".
	CodeMFARequired = "mfa_required"
	// CodeStepUpRequired is returned by RequireStepUp (ADR-077 +
	// PR-8 acceptance) when a session-cookie principal's
	// Envelope.StepUpAt stamp is missing or older than the route's
	// configured TTL. Distinct from CodeMFARequired so the dashboard
	// can pivot the message from "enable MFA to continue" (the
	// enrollment path) to "re-enter your authenticator code"
	// (the step-up path). The audit kind "auth.step_up_required"
	// (with reason: "missing"|"expired", ttl_sec) is the load-
	// bearing security signal; the wire code is the UX affordance.
	CodeStepUpRequired = "step_up_required"
	// CodeMFAInvalidCode is returned when /confirm, /verify, or
	// /recover validate a presented TOTP code / recovery code and
	// the comparison fails. The audit Emit fires regardless.
	CodeMFAInvalidCode = "mfa_invalid_code"
	// CodeSessionExpired is returned by the IAM-3 (ADR-039) cookie-
	// branch cross-check when the cookie's sid is empty (pre-
	// rollout), the row is gone, or the row is revoked. Distinct
	// from CodeUnauthorized so the dashboard can pivot to
	// "sign in again" rather than the umbrella "unauthorized"
	// message.
	CodeSessionExpired = "session_expired"
	// CodeSessionInvalid is the defensive sibling when the cookie's
	// AEAD-bound AccountID disagrees with the sessions row. AEAD
	// forgery on the same key ought to be unreachable; if it ever
	// fires the operator should investigate the key-sealing path.
	CodeSessionInvalid = "session_invalid"
	// CodeUnsupportedByCLI is returned by the SDK client when a caller
	// targets a route that requires the dashboard session cookie
	// (e.g. /v1/auth/sessions, /v1/auth/capabilities, or
	// /dashboard/account/set-password). The bearer-key CLI cannot
	// reach these routes — they are mounted behind
	// sessionAuth (cmd/apid/server.go:1097) and reject 401 on a
	// bearer header. The guard in pkg/api/client.go lifts this into
	// a clean 403 before the request is even issued so the failure
	// mode is honest, not a confusing auth error. See
	// pkg/api/client.go's cookieOnlyPathRE and the tripwire in
	// pkg/api/lint_tripwires_test.go.
	CodeUnsupportedByCLI  = "unsupported_by_cli"
	CodeDomainNotVerified = "domain_not_verified"
	// CodeDomainVerificationFailed (issue #961 / Mega-A PR-3) is
	// returned by `gregale domains verify` when the DNS + cert walk
	// finds a missing/mismatched TXT record, a CNAME loop, or a
	// reachability failure. Distinct from CodeDomainNotVerified
	// (which is the SHAPE-mismatch case from create-time binding).
	CodeDomainVerificationFailed = "domain_verification_failed"
	// CodeDomainCertNotIssued (issue #961 / Mega-A PR-3) is returned
	// by `gregale domains verify` when the port-443 cert is a CDN
	// cert whose SANs do not include the customer's domain. The
	// customer needs to either wait for Gregale cert propagation
	// or point the edge at a Gregale-issued cert.
	CodeDomainCertNotIssued = "domain_cert_not_issued"
	// CodeDoctorDisabled (ADR-120) is returned by the doctor
	// endpoint when FAAS_DOMAIN_DOCTOR_ENABLED is unset. The
	// route stays registered so the CLI renders a clear
	// "doctor is dark-launched" message rather than a generic
	// 404. Distinct from CodeDoctorUnavailable (which fires
	// when the flag IS on but a probe pass failed).
	CodeDoctorDisabled    = "doctor_disabled"
	CodeDoctorUnavailable = "doctor_unavailable"
	CodeCronInvalid       = "cron_invalid"
	CodeAlertRuleInvalid  = "alert_rule_invalid"
	CodeHandlerMissing    = "handler_missing"
	CodeImageRequired     = "image_required"
	CodeDeployFailed      = "deploy_failed"
	// CodeDeploymentCancelLiveForbidden (ADR-124) is returned by
	// POST /v1/apps/{slug}/deployments/{id}/cancel when the row
	// is already in DeployLive. Cancel of a live row would
	// either scale the app to zero (kills §6.2 INV 4) or park
	// the app (kills INV 3). The user-correct escape is the
	// existing rollback surface — see api.ErrDeploymentCancelLiveForbidden
	// for the canonical 409 Problem with the deploys-rollback
	// hint.
	CodeDeploymentCancelLiveForbidden = "deployment_cancel_live_forbidden"
	// CodeDeploymentCancelNotCancellable is returned when the
	// row's status is in {failed, superseded, cancelled} — i.e.
	// the row is already terminal and the cancel is meaningless.
	// ADR-124; mirrors the state.CancelReason invalid path.
	CodeDeploymentCancelNotCancellable = "deployment_cancel_not_cancellable"
	// CodeDeploymentReorderNotPending is returned by reorder
	// when the row's status is not 'pending'. ADR-124.
	CodeDeploymentReorderNotPending = "deployment_reorder_not_pending"
	// CodeDeploymentReorderPriorityInvalid is returned when the
	// request priority falls outside the closed range [0, 1000].
	// ADR-124; the schema CHECK is the durable backstop.
	CodeDeploymentReorderPriorityInvalid = "deployment_reorder_priority_invalid"
	// CodePlanReorderDisabled is returned when the caller's
	// plan tier locks them out of the reorder + deploy-immediately
	// surface (cancel + clear-obsolete are Free-allowed; reorder
	// + deploy-immediately are Hobby+ only). ADR-124.
	CodePlanReorderDisabled = "plan_reorder_disabled"
	// CodeSigInvalid is returned by schedd when the layer's
	// signature fails verification (or is missing) on cold-boot.
	// The deployment transitions to DeployFailed with this code;
	// the wake that triggered the verify returns 503 to gatewayd-internal
	// with the same code. ADR-038 §Consequences Compatibility.
	CodeSigInvalid       = "sig_invalid"
	CodeNoRollbackTarget = "no_rollback_target"
	// CodeRollbackTargetNotFound is returned when the caller passes an
	// explicit target_deployment_id that doesn't match any deployment of
	// this app (or doesn't exist at all). Distinct from
	// CodeNoRollbackTarget (no superseded deployment exists at all) so
	// the CLI can render different remediation: "wrong id" vs "deploy
	// twice first". SAFE-RELEASES-G (issue #976).
	CodeRollbackTargetNotFound = "rollback_target_not_found"
	// CodeRollbackTargetAlreadyLive is returned when the caller passes
	// an explicit target_deployment_id that exists but has status !=
	// 'superseded' (e.g. status='live' — caller is asking to rollback
	// to the already-current deployment). Rejected explicitly rather
	// than silently no-op'd. SAFE-RELEASES-G.
	CodeRollbackTargetAlreadyLive = "rollback_target_already_live"
	// CodeDeploySignatureInvalid is returned by apid when the
	// customer's OCI image deploy is rejected at the accept-time
	// signature-enforcement gate (issue #472 / ADR-054). Three
	// triggers: (a) apps.require_signed=true but no trusted signers
	// are configured (fail-closed — the operator toggled the flag
	// but forgot to onboard a publisher); (b) the image carries no
	// cosign signature under the registry's well-known sha256-<digest>.sig
	// location; (c) the signature was made by a key not in the
	// per-app trusted-publisher list. 403 because the deploy is
	// REJECTED at accept time, distinct from CodeSigInvalid's 503
	// (which fires on the cold-boot layer-verify path).
	CodeDeploySignatureInvalid = "deploy_signature_invalid"
	// CodeTrustedSignerInvalid is returned when the PUT body fails
	// the PEM-shape validation (size 64..1024 bytes after
	// base64-decode, ECDSA P-256 SPKI per ADR-038). 400 with the
	// same Problem body shape as CodeSecretInvalidKey.
	CodeTrustedSignerInvalid = "trusted_signer_invalid"
	// CodeTrustedSignerNotFound is the 404 mirror of
	// CodeSecretNotFound / CodeEnvVarNotFound for the DELETE path.
	CodeTrustedSignerNotFound = "trusted_signer_not_found"

	// CodeScanCritical is returned by vmmd when the staged base
	// ext4's Grype scan sidecar reports a CRITICAL finding (or
	// is missing/unreadable) at boot time (issue #299). The
	// failure mode is policy-driven (a CRITICAL CVE is a known
	// bad, not an operator fault), so the code is SLO-exempt —
	// it's not a customer-actionable signal in the same way
	// capacity / build-failure codes are, but a sustained
	// non-zero rate signals an imaged regression (the
	// fail-closed scan-sidecar write path in
	// pkg/imaged/base_stage.go) or a fresh CVE drop in a base
	// image. The 503 status mirrors CodeSigInvalid's posture —
	// the wake request fails closed, the caller can retry after
	// the operator rebuilds the base.
	CodeScanCritical = "scan_critical"

	// CodeBuildSBOMUnavailable is returned by the SBOM GET handler
	// when no SBOM populator has run for this build (pre-PR build, or
	// the imaged syft populator in pkg/imaged/loop.go has not yet
	// landed). Distinct from build_provenance_not_found (which means
	// the populator INSERT itself failed at WARN — the build is
	// genuine but observability broke). 503 — the artefact may exist
	// later if the operator re-deploys imaged; the customer can
	// branch on the code and re-fetch.
	CodeBuildSBOMUnavailable = "build_sbom_unavailable"

	// CodePayment is the 402 response when an API-only plan change requires
	// a Stripe subscription the customer does not have (issue #142 / PR).
	// The Problem carries a BillingPortalURL extension so the dashboard
	// renders an actionable upsell button without a separate /v1/billing
	// endpoint. Distinct from CodeBillingPastDue because the failure mode
	// is "you cannot upgrade via API" rather than "your account is past
	// due" — the dashboard renders different copy for each.
	CodePayment = "payment_required"

	// Customer secrets (spec §11/G2). Plaintext VALUES never enter logs;
	// these codes are returned for quota / shape / size violations only.
	CodePlanLimitSecrets    = "plan_limit_secrets"
	CodeSecretInvalidKey    = "secret_invalid_key"
	CodeSecretValueTooLarge = "secret_value_too_large"
	CodeSecretNotFound      = "secret_not_found"

	// CodeRekeyDisabled — ADR-089 PR-C. Returned with 503 by
	// GET /v1/admin/secrets/rekey-progress when FAAS_REKEY_ENABLED
	// is unset. The runner is nil on those daemons, so the
	// handler can't report progress; we surface the
	// misconfiguration as a distinct code rather than a zero-
	// progress 200, so a dashboard can distinguish "feature is
	// off" from "feature is on and idle".
	CodeRekeyDisabled = "rekey_disabled"

	// CodeRekeyNoIdentities — ADR-089 PR-C follow-up (PR #825).
	// Returned with 503 when FAAS_REKEY_ENABLED=true BUT no host
	// age identities are loaded. The operator has opted in but
	// the runner is silently skipped because mfaIdentities() is
	// empty (typically FAAS_HOST_AGE_IDENTITY_PATH unset). The
	// distinct code lets a dashboard surface "you set the flag
	// but the host-age identity isn't loaded" instead of the
	// misleading "set FAAS_REKEY_ENABLED=true and restart".
	CodeRekeyNoIdentities = "rekey_no_identities"

	// Customer env vars (issue #395 / ADR-045). Distinct codes from
	// CodeSecret* so the quota + audit shape is unambiguous to
	// dashboards and SDK callers — a `plan_limit_env_vars` is a config
	// quota, not a credential one.
	CodePlanLimitEnvVars    = "plan_limit_env_vars"
	CodeEnvVarInvalidKey    = "env_var_invalid_key"
	CodeEnvVarValueTooLarge = "env_value_too_large"
	CodeEnvVarNotFound      = "env_var_not_found"

	// Customer env-var scopes (ADR-090). The scope query param on
	// /v1/apps/{slug}/envs?scope= accepts a domain-valid slug (3..40
	// lowercase alnum + dash) OR the reserved sentinel "__all__" on
	// the read path. Two distinct codes so a CLI author can tell
	// "you used the all-scopes sentinel on a write" (400
	// env_scope_reserved) apart from "your scope name has the wrong
	// shape" (400 env_scope_invalid). Both render the same HTTP 400
	// to the customer; the `code` discriminator is for the SDK's
	// retry-guidance branch.
	CodeEnvScopeInvalid  = "env_scope_invalid"
	CodeEnvScopeReserved = "env_scope_reserved"

	// ADR-098 §D5: data-placement hints quota (Free customer + 0 cap
	// gates the capture path; Hobby+ customers can hold up to their
	// per-plan DataPlacementHintsPerApp). Distinct codes so a
	// dashboard can render "your plan doesn't include data
	// placement" (402 plan_limit_data_upstreams) apart from
	// "delete one to add another" (403 plan_limit_data_upstreams —
	// same code, different defaultStatus; the StateError wrapper
	// lifts the right one).
	//
	// Wait — the two are distinct codes below:
	//   * CodePlanDataUpstreamsNotAllowed = 402, plan doesn't
	//     unlock the surface (Free today).
	//   * CodePlanLimitDataUpstreams = 403, the per-plan cap is
	//     reached.
	// Mirrors the CodePlanWebhooksNotAllowed vs CodePlanWebhookQuota
	// split at line 533/534.
	CodePlanDataUpstreamsNotAllowed = "plan_data_upstreams_not_allowed" // 402, Free
	CodePlanLimitDataUpstreams      = "plan_limit_data_upstreams"       // 403, per-app cap reached

	// ADR-098 §D4 + §11: explicit-upstream write surface validation.
	// Distinct codes from CodeEnvVarInvalidKey / CodeEnvVarValueTooLarge
	// because the lifecycle and quota shape differ: a data upstream is
	// keyed by (scope, kind, host, port) and the value is the host
	// (not a connection string), and the §11 barrier requires the
	// plaintext host never reaches the response body or the audit
	// surface. Mirrors the CodePlanRegistryCredentialNotAllowed /
	// CodeInvalidRegistryHost split at line 533/535.
	CodeUpstreamInvalidKind = "upstream_invalid_kind" // 400, closed-vocab check
	CodeUpstreamInvalidHost = "upstream_invalid_host" // 400, RFC-952/1123 hostname check
	CodeUpstreamInvalidPort = "upstream_invalid_port" // 400, 1..65535 range check
	CodeUpstreamNotFound    = "upstream_not_found"    // 404, DELETE/GET absent

	// ADR-091 / PR-D: per-deployment env scope collision. The
	// partial unique index `deployments_app_scope_live_uniq`
	// (migration 00213) makes two live rows on the same
	// (app_id, scope) impossible — the second create returns
	// state.ErrConflict wrapping the constraint name. The handler
	// decodes the wrapped error to surface this code (409) so a
	// customer can branch on "supersede the prior prod deployment
	// first" rather than retry blindly. Renders 409 Conflict,
	// matching the rest of state.ErrConflict's 4xx family.
	CodeDeploymentScopeCollision = "deployment_scope_collision"

	// Trusted cosign signers (issue #472 / ADR-054). Same shape as
	// the env-var quota — config cap, not a credential one — but a
	// distinct code so the dashboard can surface "trusted publishers"
	// as its own row and so SDK callers don't accidentally decode a
	// trusted-signer quota error as an env-var quota error.
	CodePlanLimitTrustedSigners = "plan_limit_trusted_signers"

	// IAM-5 API-key lifecycle (issue #189). Distinct from the
	// secret/env surface because the lifecycle diverges: secrets
	// and env vars are config rows, API keys are revocable
	// credentials with a status state machine (active | grace |
	// revoked) and a per-account grace override. Three codes:
	//   * CodeAPIKeyExpired = 401, the bearer key has expired
	//     (auth-time gate; the auth middleware emits key.expired).
	//   * CodeAPIKeyRevoked = 401, the key is in status='revoked'
	//     (manual delete, rotation atomic, or lazy-expiry).
	//   * CodeAPIKeyLimitExceeded = 409, the per-account cap
	//     (Plan.KeysMax, IAM-5) is reached. Mirrors ErrPlanLimitSecrets
	//     shape but on 409 because the customer's history (one
	//     revoking-key insistence) is idempotent — they can re-issue
	//     after a delete and the operation completes, but adding a
	//     fresh key while at the cap is a quota block.
	CodeAPIKeyExpired       = "api_key_expired"
	CodeAPIKeyRevoked       = "api_key_revoked"
	CodeAPIKeyLimitExceeded = "api_key_limit_exceeded"

	// Per-app private-registry Basic Auth (issue #461 / ADR-062).
	// Distinct from CodeSecret* because the lifecycle and quota shape
	// differ: a registry credential is keyed by (app, host) and the
	// password is sealed + transiently unsealed in imaged, never
	// returned to the customer. Codes are surfaced for the dashboard
	// to render plan-tier upsell vs. quota guidance without parsing
	// prose.
	CodePlanRegistryCredentialNotAllowed = "plan_registry_credentials_not_allowed" // 403, Free
	CodePlanRegistryCredentialQuota      = "plan_registry_credential_quota"        // 413, per-app cap reached
	CodeInvalidRegistryHost              = "invalid_registry_host"                 // 400, normalized-host gate
	CodeRegistryCredentialNotFound       = "registry_credential_not_found"         // 404, DELETE absent

	// Plan-tier feature gates (M8 §6.5). Distinct from CodePlanLimit*
	// because the failure mode is "your plan doesn't unlock this knob
	// at all" rather than "you used more than the plan allows".
	// Pro + Scale unlock min_instances; Free + Hobby get 403 and the
	// docs URL tells them which plans do.
	CodePlanMinInstancesNotAllowed = "plan_min_instances_not_allowed"
	// CodeInvalidMinInstances is a 422 for shape violations: < 0 or
	// > plan MaxConcurrency. Distinct from CodeValidation so the CLI
	// can render actionable retry guidance ("raise your plan or lower
	// --max-concurrency").
	CodeInvalidMinInstances = "invalid_min_instances"
	// CodeMaxMinInstancesExceeded (issue #557 / ADR-071 §Decision 5)
	// is a 422 for shape violations: the requested min_instances
	// exceeds the per-plan MaxMinInstances cap (Hobby 1, Pro 3,
	// Scale 10). Distinct from CodeInvalidMinInstances because the
	// request shape is fine (non-negative int), it's the
	// plan-bounded value that was wrong. WithLimit carries the
	// plan cap and the observed value so the CLI renders
	// actionable retry guidance ("raise your plan or lower
	// --min-instances").
	CodeMaxMinInstancesExceeded = "max_min_instances_exceeded"

	// CodePlanTrafficSplitNotAllowed (issue #556 / traffic
	// splitting across deployments) is a 403 for plan-tier
	// rejections: a Free/Hobby customer tries to set a
	// non-default traffic_percent on a deployment. Mirrors
	// CodePlanMinInstancesNotAllowed so the CLI renders the same
	// "your plan doesn't allow this — upgrade" shape. The
	// migration (00160) column default is 100, so customers
	// who never opt-in see no behavioural change; the gate
	// only fires when a customer explicitly opts in to a
	// non-100 traffic_percent on create or PATCH-traffic.
	CodePlanTrafficSplitNotAllowed = "plan_traffic_split_not_allowed"
	// CodeInvalidTrafficPercent (issue #556) is a 422 for shape
	// violations: traffic_percent is outside [0, 100] (or
	// negative). Distinct from CodeValidation so the CLI can
	// render the cap+observed pair via WithLimit (mirrors the
	// CodeInvalidMinInstances pattern at line 1692). The state
	// layer (pkg/state.UpdateDeploymentTraffic) also lifts
	// this code on out-of-range input as a defence-in-depth
	// backstop.
	CodeInvalidTrafficPercent = "invalid_traffic_percent"
	// CodeInvalidCanaryPreset (issue #976 / ADR-122 /
	// SAFE-RELEASES-A) is a 422 for an out-of-catalog canary
	// preset name. The pkg/api/canary closed-set is the source
	// of truth (mirrored on disk by deployments_canary_preset_chk
	// at migration 00480); the handler validates membership
	// before INSERT so a typo doesn't surface as a CHECK
	// violation deep in the pgstore layer.
	CodeInvalidCanaryPreset = "invalid_canary_preset"
	// CodeTrafficPercentSumInvalid (issue #556) is a 409
	// (Conflict) for the defensive backstop: post-write
	// Σ(traffic_percent WHERE status='live') != 100. In
	// practice this is unreachable — the schema CHECK
	// (00160) gates the per-row range and the
	// UpdateDeploymentTraffic transaction zeroes siblings
	// before stamping the target. The state layer asserts
	// the invariant explicitly and lifts to this code if
	// violated, so a future refactor that breaks the Σ
	// tripwire surfaces a 409 (not a silent DB drift).
	CodeTrafficPercentSumInvalid = "traffic_percent_sum_invalid"

	// Traffic mirroring (issue #72 / ADR-125 PR-A2). Seven RFC 7807
	// codes for the /v1/apps/{slug}/mirrors CRUD surface. The
	// plan-gate (403) and per-app quota (422) are the load-bearing
	// 4xx shapes; the shape errors (invalid percent, source==mirror,
	// cross-app mismatch, deployment not live) are first-line
	// defences before the SQL CHECKs in migration 00384. The 404
	// is reserved for post-IDOR "rule deleted between requests"
	// cases — a cross-account lookup never emits it (s.notFound
	// returns the generic "not_found" instead, so cross-account
	// probing cannot distinguish "exists" from "doesn't").
	CodePlanMirrorNotAllowed    = "plan_mirror_not_allowed"
	CodeMirrorRuleQuotaExceeded = "mirror_rule_quota_exceeded"
	CodeInvalidMirrorPercent    = "invalid_mirror_percent"
	CodeMirrorSourceTargetSame  = "mirror_source_target_same"
	CodeMirrorDeploymentNotLive = "mirror_deployment_not_live"
	CodeMirrorCrossAppMismatch  = "mirror_cross_app_mismatch"
	CodeMirrorRuleNotFound      = "mirror_rule_not_found"
	CodeInvalidMirrorWindow     = "invalid_mirror_window"

	// Sidecar containers (issue #463 / ADR-068). Eight RFC 7807
	// codes for the sidecar surface. The cap and type-uniqueness
	// codes are the load-bearing 400-class shapes; the stateful
	// and not-on-plan codes are defence-in-depth for future
	// per-plan tier-ups (PR-A does NOT apply the plan gate; the
	// code is reserved so a follow-up PR doesn't have to invent
	// a new one).
	CodeSidecarCapExceeded      = "sidecar_cap_exceeded"
	CodeSidecarInvalidType      = "sidecar_invalid_type"
	CodeSidecarInvalidImage     = "sidecar_invalid_image"
	CodeSidecarStatefulDenied   = "sidecar_stateful_denied"
	CodeSidecarInvalidName      = "sidecar_invalid_name"
	CodeSidecarInvalidPort      = "sidecar_invalid_port"
	CodeSidecarInvalidRamMB     = "sidecar_invalid_ram_mb"
	CodeSidecarNotAllowedOnPlan = "sidecar_not_allowed_on_plan"

	// CodeInitSidecarFailed (issue #463 / ADR-069 / PR-B AC #1) is
	// the RFC 7807 stable code vmmd stamps onto a deployments row
	// when an `init` sidecar exec returns a non-zero exit before
	// framework-ready. Distinct from the DTO-side validation codes
	// above because this is a runtime failure (the customer shape
	// was fine; the workload itself failed). Surfaced via
	// state.Store.SetDeploymentFailed(error_code = CodeInitSidecarFailed)
	// so the SDK can branch on the literal code, not on prose.
	CodeInitSidecarFailed = "init_sidecar_failed"

	// Move 1 event-shaped surfaces (spec §4.4, §4.9). The CLI exit-code
	// table treats them as 403/422/402; surfacing the codes separately
	// lets the dashboard render a "move to Scale to lift the cap"
	// hint without parsing prose.
	CodePlanQueueDepth     = "plan_queue_depth"
	CodePlanSourceBytes    = "plan_source_bytes"
	CodePlanFeatureGated   = "plan_feature_gated"
	CodePlanDelayedCap     = "plan_delayed_tasks_cap"
	CodeInvocationNotFound = "invocation_not_found"
	// CodeInvocationNotReplayable (issue #315 / tier-2 DX) is the
	// 409 surfaced by POST /v1/invocations/{id}/replay when the
	// original invocation is in a state that cannot be re-issued
	// (anything other than {failed, dead_letter}). Distinct from
	// CodeConflict so the CLI's error template can render the
	// "use `gregale invocation <id>` to inspect the state" hint
	// without parsing prose.
	CodeInvocationNotReplayable = "invocation_not_replayable"
	// CodeBuildProvenanceNotFound is the ADR-038 / Tier 3 #197
	// B3.10-read sentinel. Distinct from a generic "no such build"
	// so the customer can branch: a build that exists with no
	// provenance row is the "populator INSERT failed + WARN logged"
	// outcome, not a 404 of the build itself.
	CodeBuildProvenanceNotFound = "build_provenance_not_found"
	// CodeBuildNotFound is the DEPLOY-PROV-6 / ADR-089 (issue
	// #741) 404 sentinel for GET /v1/builds/{id} when the build
	// row does not exist OR belongs to another account. The 404
	// surface is uniform (the server's IDOR chain collapses every
	// negative path) so cross-account probes can't enumerate —
	// distinct from CodeBuildProvenanceNotFound (which means
	// "build exists, populator INSERT failed").
	CodeBuildNotFound = "build_not_found"

	// ADR-031 (tier-2 of the network roadmap) — per-app egress
	// allowlist. Same gate shape as MinInstances: the feature is
	// plan-locked (Pro/Scale only), and there are two distinct
	// failure modes that warrant distinct codes so the CLI can
	// render actionable retry guidance.
	//   * CodePlanEgressAllowlistNotAllowed = 403 "your plan does
	//     not unlock this knob at all" (Free/Hobby).
	//   * CodeEgressAllowlistTooLong = 400 "the PATCH carries more
	//     CIDRs than your plan caps" (Pro/Scale but the slice is
	//     too long; not a billing failure).
	CodePlanEgressAllowlistNotAllowed = "plan_egress_allowlist_not_allowed"
	CodeEgressAllowlistTooLong        = "egress_allowlist_too_long"

	// Issue #477 / ADR-118 — per-app ingress IP allowlist (extends
	// the reserved 'ip_allowlist' enum value, ADR-079). Same shape
	// as the egress pair: 403 plan-gate + 400 count cap, distinct
	// codes so the CLI can render actionable retry guidance.
	//   * CodePlanPublicAuthIPAllowlistNotAllowed = 403 "your plan
	//     does not unlock this knob at all" (Free/Hobby).
	//   * CodePublicAuthIPAllowlistTooLong = 400 "the PATCH carries
	//     more CIDRs than your plan caps" (Pro/Scale but the slice
	//     is too long).
	CodePlanPublicAuthIPAllowlistNotAllowed = "plan_public_auth_ip_allowlist_not_allowed"
	CodePublicAuthIPAllowlistTooLong        = "public_auth_ip_allowlist_too_long"

	// Issue #679 / PR-B / ADR-082 — per-account egress allowlist
	// additive budget. Distinct code from CodeEgressAllowlistTooLong
	// so the CLI can render the "your admin override is too big,
	// talk to support" message separately from the "you hit the
	// plan cap" message. The DB CHECK constraint (>= 0) is the
	// wire-bypass backstop; the apid gate is the soft cap.
	CodeAccountEgressAllowlistExtraOutOfRange = "account_egress_allowlist_extra_out_of_range"

	// Issue #471 — per-app streaming responses. Same gate shape as
	// EgressAllowlist: a single plan-locked feature with a 403 code
	// when the PATCH asks for a capability the plan does not unlock.
	// Free customers see this; Hobby/Pro/Scale don't. Distinct code
	// from CodePlanEgressAllowlistNotAllowed so the CLI can render
	// "streaming is a paid feature" vs "allowlist is a paid feature"
	// without conflating them in telemetry.
	CodePlanStreamingNotAllowed = "plan_streaming_not_allowed"

	// ADR-119 — per-app static egress IP. Two error codes split
	// the gate (402: plan doesn't unlock the surface) from the
	// quota trip (403: per-app quota reached, or alias-IP collision
	// on br-tenants — a different app on the same account has the
	// same IP pinned). 402 mirrors the existing CodePlanXxxNotAllowed
	// family so the CLI's "your plan does not unlock X" template
	// renders uniformly. The shape error (malformed IP, IPv6, IP
	// inside the egress denylist) is 400 — distinct from the
	// gate/quota codes so the SDK can branch on actionable retry
	// guidance ("upgrade plan" vs "fix IP" vs "use a different IP").
	CodePlanStaticEgressIPNotAllowed = "plan_static_egress_ip_not_allowed"
	CodePlanStaticEgressIPQuota      = "plan_static_egress_ip_quota"
	CodeAppStaticEgressIPInvalid     = "app_static_egress_ip_invalid"
	CodeStaticEgressIPNotProvisioned = "static_egress_ip_not_provisioned"
	// CodeStaticEgressIPNotEnabled is the dark-launch 402 — the
	// cluster has the schema + handlers wired but the operator
	// has not flipped FAAS_STATIC_EGRESS_IP_ENABLED. Same posture
	// as CodeTenantSurfacesNotAllowed.
	CodeStaticEgressIPNotEnabled = "static_egress_ip_not_enabled"

	// Issue #470 / ADR-055: per-app two-tier-snapshot flag (warm.snap
	// on top of init.snap). Pro/Scale opt in by default; Free/Hobby
	// reject PATCH-true with 403 plan_warm_snapshot_not_allowed so
	// customers see the gate before the SQL CHECK trips on the
	// INSERT. Same shape as CodePlanStreamingNotAllowed / egress:
	// a single plan-locked feature with a distinct code so the
	// CLI can render "warm-snapshot is a paid feature" alongside
	// the streaming + allowlist copy without conflating them.
	CodePlanWarmSnapshotNotAllowed = "plan_warm_snapshot_not_allowed"

	// Issue #560: per-deployment require_authn opt-in (Cloud Run
	// analogue: `--no-allow-unauthenticated`). Pro/Scale opt in by
	// default; Free/Hobby reject PATCH-true with 403
	// plan_require_authn_not_allowed so customers see the gate
	// before any column write. Same shape as
	// CodePlanStreamingNotAllowed / warm-snapshot: a single
	// plan-locked feature with a distinct code so the CLI can
	// render "auth-required is a paid feature" alongside the
	// streaming + warm-snapshot copy without conflating them in
	// telemetry. The gatewayd-internal 401/403 audit events
	// (`instances.authn_missing` / `invalid` / `scope`) carry
	// separate kinds and are emitted by the per-deployment
	// authz branch in pkg/gateway/handler.go::Handler.enforceRequireAuthn
	// (kind constants live next to the call sites) — the plan
	// gate and the request gate are distinct failure modes.
	CodePlanRequireAuthnNotAllowed = "plan_require_authn_not_allowed"

	// ADR-124 §Plan gating — per-app app_protocol=grpc gate.
	// Free apps cannot opt-in to gRPC framing at the customer
	// edge; Hobby+/Pro/Scale may freely PATCH app_protocol=grpc.
	// 403 mirrors the streaming / warm-snapshot / require-authn /
	// public-auth / traffic-split gate family — a deliberate
	// plan-tier choice that requires customer action (upgrade),
	// not a retry. The literal carries observed value (always
	// "grpc") + docs URL (/docs/app-protocol#plan-gating).
	CodePlanAppProtocolGrpcNotAllowed = "plan_app_protocol_grpc_not_allowed"

	// ADR-124 §Decision 1 — closed-set validator. Any value
	// outside {http1, http2, grpc} surfaces as 400 with this
	// code. Carry observed value + docs URL
	// (/docs/app-protocol#closed-set) so the CLI can render
	// "the value 'h2c' is not in the closed set http1|http2|grpc"
	// alongside the existing 400 copy without conflating in
	// telemetry.
	CodeAppProtocolInvalid = "app_protocol_invalid"

	// Issue #477 / ADR-079 — public-URL auth mode gate. Free apps
	// stay on the no-signup-friction path (open-only); Hobby unlocks
	// 'bearer'; Pro+ unlocks both 'bearer' and 'basic'. 402 mirrors
	// the streaming / warm-snapshot / eviction-priority / crons /
	// alert-rules / webhooks gate family — a deliberate plan-tier
	// choice that requires customer action (upgrade), not a retry.
	// Distinct codes per mode so the CLI can render "bearer is
	// Hobby+" vs "basic is Pro+" alongside the existing 402-family
	// copy without conflating them in telemetry.
	CodePlanPublicAuthBearerNotAllowed = "plan_public_auth_bearer_not_allowed"
	CodePlanPublicAuthBasicNotAllowed  = "plan_public_auth_basic_not_allowed"

	// Issue #676 / ADR-080 — per-app raw-bytes Upgrade bridge gate.
	// 403 returned when a customer on a plan that does not enable
	// WebSocket (Free) attempts to PATCH apps.websocket_enabled=true.
	// Same gate shape as CodePlanStreamingNotAllowed /
	// CodePlanWarmSnapshotNotAllowed / CodePlanRequireAuthnNotAllowed;
	// distinct code so the CLI can render "websocket is a paid
	// feature" alongside the streaming + warm-snapshot copy without
	// conflating them in telemetry. The Free plan is the abuse-floor
	// tier where a single long-lived WS would pin a wake past
	// wake_idle_timeout, so the gate is fail-closed at create time
	// AND at PATCH time (no override path).
	CodePlanWebSocketNotAllowed = "plan_websocket_not_allowed"

	// ADR-093: customer attempted PATCH-true on Free for
	// apps.route_metrics_enabled. Same gate shape as
	// CodePlanWebSocketNotAllowed above; distinct code so the
	// CLI can render "per-route metrics is a paid feature"
	// alongside the streaming + warm-snapshot copy without
	// conflating them in telemetry. The Free plan is the
	// abuse-floor tier where per-route cardinality would not
	// have a budget alongside the per-app rollups, so the gate
	// is fail-closed at create time AND at PATCH time (no
	// override path).
	CodePlanRouteMetricsNotAllowed = "plan_route_metrics_not_allowed"

	// Issue #470 / ADR-055: out-of-range warm-snapshot threshold
	// values from a PATCH (warm_snapshot_min_requests outside [1,
	// 100] or warm_snapshot_min_ms outside [100, 60000]). 422 with
	// these codes so the customer sees a validation error, not a
	// SQL CHECK violation that the apid layer is supposed to
	// intercept. Distinct codes per field so the CLI can render
	// "min_requests out of range" vs "min_ms out of range".
	CodeInvalidWarmSnapshotMinRequests = "invalid_warm_snapshot_min_requests"
	CodeInvalidWarmSnapshotMinMs       = "invalid_warm_snapshot_min_ms"

	// Issue #475 — per-app eviction_priority tier ('best_effort'|'reserved').
	// 403 plan_eviction_priority_reserved_not_allowed when the customer's
	// plan does not unlock the reserved tier (Free), so the customer sees
	// the gate before the SQL CHECK trips on the UPDATE. Same gate shape
	// as CodePlanStreamingNotAllowed / CodePlanWarmSnapshotNotAllowed;
	// distinct code so the CLI can render "reserved is a paid feature"
	// alongside the streaming + warm-snapshot copy without conflating
	// them in telemetry.
	CodePlanEvictionPriorityReservedNotAllowed = "plan_eviction_priority_reserved_not_allowed"

	// Issue #475 — per-account reserved-tier quota exhausted. 422 with
	// this code when the per-plan ReservedConcurrencyPerAccount cap is
	// reached (Hobby 1, Pro 2, Scale 4). The customer must flip an
	// existing reserved app to best_effort first; the count is over
	// APPS (not instances) per the user-confirmed contract — single
	// reserved app with 5 concurrent instances counts as 1 against the
	// cap. Distinct code from the plan-gate (403) so a CLI can render
	// "you've hit your reserved cap" vs "your plan doesn't unlock
	// reserved" without conflating them.
	CodePlanEvictionPriorityReservedQuota = "plan_eviction_priority_reserved_quota"

	// Tier A10 / ADR-088 — per-app overflow_node preference. 422
	// returned when (a) the wire name does not resolve via
	// Store.ComputeNodeByName (404→422 mapping, mirrors the
	// soft-404→404 surface at compute_nodes.go:267) or
	// (b) the named compute_nodes row is active=false. Distinct
	// code from the per-app column-set gates so the CLI can
	// render "spill target not found" / "spill target offline"
	// along the existing 422-family copy without conflating them
	// in telemetry.
	CodeInvalidOverflowNode = "invalid_overflow_node"

	// Issue #471 PR-B (the meat) — emitted when an active stream
	// exceeds the per-plan MaxResponseBodyBytes cap (Hobby+: 100 MB;
	// Free: 25 MB). 413 + this code so the client sees a distinct
	// error from the plan-gate (403 plan_streaming_not_allowed) and
	// can retry with a smaller payload. Distinct from the cap-exceeded
	// path on the proxy buffer (which currently surfaces as a 502
	// from stdlib net/http); PR-B wraps the streaming response
	// writer in a custom MaxBytesWriter that emits this code instead.
	CodeStreamingNotAvailable = "streaming_not_available"

	// CodeResponseTooLarge (issue #995 Phase 2 / ADR-121) — emitted
	// when the buffered reverse-proxy path exceeds the per-plan
	// MaxResponseBodyBytes cap. Distinct from CodeStreamingNotAvailable
	// because the buffered path applies to non-streaming apps
	// (the streaming code is gated by the streaming opt-in and the
	// plan_streaming_not_allowed error); 413 + this code so the
	// buffered-cap surface has its own stable error contract.
	CodeResponseTooLarge = "response_too_large"

	// Issue #169 / #172 — per-app reactive scale-up targets. Same gate
	// shape as MinInstances: a single plan-locked feature with two
	// failure modes that warrant distinct codes so the CLI can render
	// actionable retry guidance.
	//   * CodePlanScaleUpNotAllowed = 403 "your plan does not unlock
	//     this knob at all" (Free for either target; Hobby for CPU).
	//   * CodeInvalidAutoscaleTargetRPS = 422 "value < 1 — RPS target
	//     must be positive".
	//   * CodeInvalidAutoscaleTargetCPUPct = 422 "value outside [1, 100]".
	CodePlanScaleUpNotAllowed     = "plan_autoscale_not_allowed"
	CodeInvalidAutoscaleTargetRPS = "invalid_autoscale_target_rps"
	CodeInvalidAutoscaleTargetCPU = "invalid_autoscale_target_cpu_pct"
	// CodeInvalidEgressAllowlist is a 400 for shape violations:
	// an entry that doesn't ParsePrefix, or a v6 CIDR (v1 is v4
	// only; v6 mirror is a separate ADR).
	CodeInvalidEgressAllowlist = "invalid_egress_allowlist"

	// CodeInvalidPublicAuthIPAllowlist (ADR-118) is a 400 for
	// ingress allowlist shape violations: entries that don't
	// ParsePrefix as v4/v6 CIDRs, masklen /0, or `ip_allowlist`
	// mode set with an empty list (the 500-on-misconfig path in
	// the gateway surfaces a different code; the 400 here covers
	// the PATCH-time shape checks).
	CodeInvalidPublicAuthIPAllowlist = "invalid_public_auth_ip_allowlist"

	// Issue #462 / ADR-058 — per-app scaling policy (PR-A). Three
	// new codes, mirroring the existing autoscale shape (one
	// plan-gate 403 + two shape 422's). The codes are clustered
	// here so a future reader can see the full #462 surface in
	// one block. The worker-class gate (concurrent_requests not
	// allowed for `WorkloadClassWorker`) is the third 422 — PR-A
	// adds the customer-facing reject; PR-D closes the engine
	// bypass.
	//
	//   * CodePlanMaxInstancesNotAllowed = 403 "your plan does not
	//     unlock max_instances" (Free stays off; Hobby+ in). 403
	//     mirrors CodePlanMinInstancesNotAllowed so the CLI
	//     surfaces the same shape.
	//   * CodeInvalidMaxInstances = 422 "value outside
	//     [min_instances, plan.MaxConcurrency]". Distinct from
	//     CodeInvalidMinInstances so the CLI can render
	//     "lower your max" vs "fix your min" without conflating.
	//   * CodeInvalidCooldown = 422 "scale_*_cooldown_s outside
	//     [Min, Max]". Distinct from CodeValidation so the API
	//     stable string is reusable for telemetry.
	//   * CodeScalingTargetIncompatibleWithWorkloadClass = 422
	//     "worker-class apps cannot use concurrent_requests as the
	//     scale-up target metric". PR-A closes the customer side;
	//     PR-D carves out the engine side.
	CodePlanMaxInstancesNotAllowed                 = "plan_max_instances_not_allowed"
	CodeInvalidMaxInstances                        = "invalid_max_instances"
	CodeInvalidCooldown                            = "invalid_cooldown"
	CodeScalingTargetIncompatibleWithWorkloadClass = "scaling_target_incompatible_with_workload_class"

	// Issue #554 / ADR-078: liveness probe plan gate. The 403
	// surfaces on a Free-customer request that includes
	// `overrides.liveness_probe`; apid reads
	// Plan.LivenessAllowed() and short-circuits BEFORE the DB is
	// touched. Mirrors the CodePlanRequireAuthnNotAllowed /
	// CodePlanMinInstancesNotAllowed shape so the CLI renders the
	// same "your plan does not unlock X" template.
	CodePlanLivenessProbeNotAllowed = "plan_liveness_probe_not_allowed"

	// Account self-service (spec §17 G6, ADR-021). The
	// "confirm_required" code is returned when a DELETE arrives without
	// the confirmation header so a stale CLI prompt can't silently wipe
	// an account. The "pending" code carries the restore_until envelope
	// the customer needs to call POST /v1/account/restore. The
	// "not_restorable" code is the post-grace 409.
	CodeAccountDeletionConfirm = "account_deletion_confirm_required"
	CodeAccountDeletionPending = "account_deletion_pending"
	CodeAccountNotRestorable   = "account_not_restorable"

	// App rename (issue #63). One code covers both "slug taken by
	// another live app" and "DB unique violation"; the Detail field
	// distinguishes the two so the CLI can render actionable guidance.
	CodeAppRenameFailed = "app_rename_failed"

	// Image pull failure modes (ADR-021, spec §17 G1). The three codes
	// here are the customer-facing stable string for the puller-side
	// sentinels in pkg/oci/errors.go. imaged's buildImageLayer failure
	// path runs SentinelToCode(err) to pick one of these, persists it on
	// deployments.error_code, and the wake path lifts it into the
	// RFC 7807 Problem at the corresponding HTTP status below.
	//
	// Why three codes, not one: each signals a different remediation
	// path. image_not_found → check the digest / tag. image_egress_denied
	// → check the registry is in the public ranges (and isn't metadata
	// 169.254/16). image_manifest_invalid → pin to a single-arch digest,
	// the manifest-list rejection is part of the same code so dashboards
	// can group "wrong artifact shape" together.
	CodeImageNotFound        = "image_not_found"
	CodeImageEgressDenied    = "image_egress_denied"
	CodeImageManifestInvalid = "image_manifest_invalid"

	// CodeStatelessOnlyViolation is the single customer-facing code for
	// the stateless-only contract (Wave 0, year-one positioning). It
	// fires in three cases:
	//   - apid at deploy-accept time when a Dockerfile contains a
	//     VOLUME instruction, a mkfs/mount -t ext4|xfs call inside a
	//     RUN, or a top-level data/ or db/ directory (cmd/apid/deploy_inputs.go).
	//   - imaged at build time when the resolved OCI base image matches
	//     StatefulBaseImageDenylist (pkg/imaged/base.go).
	//   - guest-init at runtime (advisory only, never blocking — see
	//     guest/init/stateless_advisory_linux.go).
	// The single code keeps the customer-facing remediation path
	// identical (bring your own managed state) regardless of where the
	// violation was caught. The Detail field distinguishes the three.
	CodeStatelessOnlyViolation = "stateless_only_violation"

	// Error-explanations cluster (spec §6.4 amendment 1, ADR-110
	// amendment 1). All 9 codes emit status 422 (RFC 7807) and pair
	// with pkg/whycopy catalog rows that render hint/why/fix/relevant
	// logs prose on the CLI's 3-5 line renderer. The 422 status keeps
	// them in the same family as CodeDeployFailed / CodeStatelessOnly
	// Violation so the dashboard's error-explanation template picks
	// them up uniformly. Each code's ErrXxx constructor lives next to
	// its constant; the StatusForCode switch arm below (line ~1267)
	// adds them to the 422 bucket.
	CodeAppNotListening        = "app_not_listening"
	CodeAppLoopbackBound       = "app_loopback_bound"
	CodeAppArchMismatch        = "app_arch_mismatch"
	CodeEnvVarMissing          = "env_var_missing"
	CodeAppHealthzUnauthorized = "app_healthz_unauthorized"
	CodeAppRuntimeOOM          = "app_runtime_oom"
	CodeDepInstallFailed       = "dep_install_failed"
	CodeAppStartupTimeout      = "app_startup_timeout"

	// CLI auth (spec §2.2 device-code flow). Pending is the "user has
	// not yet approved" signal the CLI's poll loop keys off; the CLI
	// keeps polling until it sees 200 OK or a different 4xx. The
	// "unavailable" code covers every other failure mode (expired,
	// already used, unknown) — the CLI does not need to distinguish
	// them, and returning a single code avoids probing.
	CodeCliAuthPending     = "cli_auth_code_pending"
	CodeCliAuthUnavailable = "cli_auth_code_unavailable"

	// CodeAppConcurReached is the typed "already at max_concurrency"
	// result from Engine.AdmitInstance (issue #168). Distinct from
	// CodePlanLimitConcur because the gateway treats this as a benign
	// no-op when it already has ≥1 cached target, while plan_limit
	// (the Wake path) is always fatal to the requesting call.
	CodeAppConcurReached = "app_concurrency_reached"

	// Dashboard auth (issue #165, ADR-032). Pre-#165, POST /login
	// auto-created an account + minted a "web-console" API key + set
	// the session cookie on ANY email with zero verification, which
	// was a full pre-auth account-takeover (spec §11 violation).
	// Post-#165, the dashboard surfaces are real auth:
	//
	//   - invalid_credentials: 401 for both "no such email" and
	//     "wrong password" — the two paths share the same response
	//     body so the surface doesn't leak which case it hit. The
	//     constant-time Argon2id pad on the no-account path closes
	//     the timing oracle; see cmd/apid/handlers_auth.go.
	//   - email_not_verified: 401 when a Google / GitHub OAuth
	//     callback returns a profile whose primary email is not
	//     verified by the provider. Distinct from invalid_credentials
	//     because the customer can fix this by verifying the email
	//     upstream; we never mint an unverified session.
	//   - password_too_weak: 400 at /signup when the password fails
	//     the NIST-style floor (≥12 chars). The Detail names the
	//     rule so the dashboard form can highlight which constraint
	//     tripped.
	//   - reset_token_invalid / reset_token_expired: 410 for GET /
	//     POST /auth/reset when the token doesn't exist (invalid)
	//     or has aged past 15 minutes (expired). 410 Gone is the
	//     semantically correct status: the resource was a one-shot
	//     and is no longer addressable.
	//   - account_exists: never returned directly. Anti-enumeration
	//     keeps the body identical between "signed in via /signup"
	//     and "email already taken"; the constant exists so future
	//     surfaces (e.g. an explicit "claim this email" admin tool)
	//     can branch on it without inventing a new code.
	CodeInvalidCredentials = "invalid_credentials"
	CodeEmailNotVerified   = "email_not_verified"
	CodePasswordTooWeak    = "password_too_weak"
	CodeResetTokenInvalid  = "reset_token_invalid"
	CodeResetTokenExpired  = "reset_token_expired"
	CodeAccountExists      = "account_exists"

	// CodeOAuthProviderUnavailable is the 503 returned by the
	// /v1/auth/{google,github}{,/callback} handlers when the
	// boot-resolved auth.SignInConfig reports the provider
	// Disabled — i.e. both ID and SECRET are unset on this host
	// (issue #419 / ADR-046). The half-set case refuses to boot at
	// runWithDeps, so this code is the operator-chose-not-to-ship-it
	// shape (single-box dev with OAuth off, or a tier-2 fleet where
	// only one provider is wired). The same code covers the stale-
	// cookie / direct-callback-hit case on a host whose operator
	// later unset both vars; the dashboard's /v1/auth/capabilities
	// signal keeps the button off in steady state.
	CodeOAuthProviderUnavailable = "oauth_provider_unavailable"

	// Organizations (issue #190 / IAM-6 / ADR-061). Twelve stable
	// strings surface the full org lifecycle: slug shape, slug
	// collision, role gating, member/invitation caps, invitation
	// lifecycle, ownership-transfer guards, personal-org
	// immutability, and the legacy API-key re-bind requirement.
	//
	// Why so many: each code signals a different remediation path
	// the dashboard or CLI can branch on without parsing prose.
	// org_member_cap_exceeded / org_invitation_cap_exceeded
	// surface the per-plan cap (PR 1 ships 0/0; PR 2 reads actual
	// values from the financial model). org_already_member covers
	// the re-invite case. org_invitation_invalid vs
	// org_invitation_expired split the 410 so the dashboard can
	// render "link expired, request a new one" vs "link is invalid".
	// org_last_owner / org_personal_immutable pin the two
	// immutable-from-PR-1 invariants from the ADR.
	//
	// The 12 codes are a complete set: every org-scoped handler
	// error returns exactly one of these.
	CodeOrgNotFound              = "org_not_found"
	CodeOrgSlugInvalid           = "org_slug_invalid"
	CodeOrgSlugTaken             = "org_slug_taken"
	CodeOrgMemberCapExceeded     = "org_member_cap_exceeded"
	CodeOrgInvitationCapExceeded = "org_invitation_cap_exceeded"
	CodeOrgRoleForbidden         = "org_role_forbidden"
	CodeOrgAlreadyMember         = "org_already_member"
	CodeOrgInvitationInvalid     = "org_invitation_invalid"
	CodeOrgInvitationExpired     = "org_invitation_expired"
	CodeOrgLastOwner             = "org_last_owner"
	CodeOrgPersonalImmutable     = "org_personal_immutable"
	CodeOrgAPIKeyRequiresOrg     = "org_api_key_requires_org"
	// CodeTenantSurfacesNotAllowed marks POST /v1/apps/{slug}/tenant-surfaces
	// when the account's plan does not enable surfaces (Free today, ADR-100
	// / issue #879). Distinct from CodeTenantSurfaceQuota (the surface
	// cap is met) so the customer sees "upgrade your plan" vs "delete a
	// surface" — the next action is different. 402 mirrors the
	// *NotAllowed siblings (CodePlanCronsNotAllowed, etc.).
	CodeTenantSurfacesNotAllowed = "tenant_surfaces_not_allowed"
	// CodeTenantSurfaceQuota marks the per-account tenant_surfaces cap
	// (Hobby 1 / Pro 5 / Scale 25). The Problem carries Limit +
	// Observed (the observed count is the cap) so the dashboard can
	// show "you have N surfaces, the cap is M". 429.
	CodeTenantSurfaceQuota = "tenant_surface_quota"
	// CodeTenantHostnameQuota marks the per-surface tenant_hostnames
	// cap (Hobby 10 / Pro 50 / Scale 250). The Problem carries Limit
	// + Observed + SurfaceID so the customer can name the overflowing
	// surface in the support ticket. 429.
	CodeTenantHostnameQuota = "tenant_hostname_quota"
	// CodeTenantHostnameAlreadyClaimed marks POST
	// /v1/apps/{slug}/tenant-surfaces/{id}/hostnames when the
	// hostname is already attached to another surface on any account
	// (the UQ on tenant_hostnames.hostname is global, not
	// account-scoped). 409.
	CodeTenantHostnameAlreadyClaimed = "tenant_hostname_already_claimed"
	// CodeTenantSurfaceCertKindInvalid marks a surface create / update
	// with cert_kind not in the closed set (per_host_san only today;
	// shared_wildcard is schema-accepted but issuer-rejected per
	// ADR-100 D4). Distinct from CodeValidation because the wire
	// contract for the customer surface is "the cert engine can't
	// mint this kind yet". 400.
	CodeTenantSurfaceCertKindInvalid = "tenant_surface_cert_kind_invalid"

	// Jobs (issue #1184 Workstream A / ADR-099 supplement).
	//
	// CodeJobsNotAllowed is the Free-plan gate for all /v1/jobs
	// routes. Returned as a 404 (not 403) so a Free account can
	// probe the endpoint surface without leaking the existence of
	// Jobs to the Free tier's UI. Pairs with Plan.JobsAllowed() in
	// the handler preamble.
	CodeJobsNotAllowed = "jobs_not_allowed"
	// CodeJobQuotaExceeded wraps every per-plan job quota failure
	// (JobMaxPerAccount, JobConcurrentPerAccount, JobRAMMB,
	// JobTaskTimeoutSec, JobMaxParallelismPerRun, JobMaxTasksPerRun,
	// JobMaxRetries). The Problem carries Limit + Observed + Plan +
	// the specific quota name so the dashboard can pivot the
	// message from "make it smaller" to "upgrade your plan". 429.
	CodeJobQuotaExceeded = "job_quota_exceeded"
	// CodeJobTaskNotFound marks a 404 on GET/PATCH /v1/jobs/{name}/runs/{id}/tasks/{idx}
	// when the (run_id, task_index) tuple doesn't exist OR belongs
	// to a different account. Distinct from CodeNotFound so the
	// dashboard can render a job-specific empty state.
	CodeJobTaskNotFound = "job_task_not_found"
	// CodeJobRunCancelled marks POST /v1/jobs/{name}/runs/{id}/cancel
	// when the run is already in a terminal state (succeeded /
	// failed / cancelled / dead_letter). 409.
	CodeJobRunCancelled = "job_run_cancelled"
	// CodeJobDeadlineExceeded marks a run that exceeded its
	// deadline_at wall-clock cap before all tasks reached a
	// terminal state. Returned by GET /v1/jobs/{name}/runs/{id}
	// when the run is in 'deadline_exceeded' aggregate status;
	// distinct from CodeJobRunCancelled because the cause is the
	// customer's deadline (not an explicit cancel call).
	CodeJobDeadlineExceeded = "job_deadline_exceeded"
	// CodeJobHasLiveInstances is the soft-delete sentinel. Returned
	// by DELETE /v1/jobs/{name} when soft_delete_job_if_no_live_instances()
	// returns FALSE because at least one kind='job_task' instance
	// is in a non-terminal state. The customer must cancel + wait
	// for those tasks to reach a terminal state (or call retry /
	// delete on them) before soft-deleting the template. 409.
	CodeJobHasLiveInstances = "job_has_live_instances"
	// CodeJobImageMissing marks POST /v1/jobs when the referenced
	// image_id doesn't exist OR isn't a job-runnable kind (the
	// shipped 00255 jobs.image_id is a plain UUID reference with
	// no kind check; the engine validates at run-create time).
	// Distinct from CodeInvalidRef because the field shape is
	// valid — the referent is the problem.
	CodeJobImageMissing = "job_image_missing"
	// CodeJobCommandInvalid marks a command[] violation:
	// array_length > JobMaxCommandLen (per-run: 64) OR a command
	// element with embedded NUL. Distinct from CodeValidation
	// because the wire contract for the field is "the executor
	// can't run this argv" rather than "the schema is wrong".
	CodeJobCommandInvalid = "job_command_invalid"

	// Workflows (ADR-081).
	CodePlanWorkflowsNotAllowed       = "plan_workflows_not_allowed"
	CodePlanWorkflowsQuota            = "plan_workflows_quota"
	CodeWorkflowDAGCycle              = "workflow_dag_cycle"
	CodeWorkflowStepNotFound          = "workflow_step_not_found"
	CodeWorkflowRunNotFound           = "workflow_run_not_found"
	CodeWorkflowEventNotFound         = "workflow_event_not_found"
	CodeWorkflowNotRunning            = "workflow_not_running"
	CodeWorkflowDeploymentUnavailable = "workflow_deployment_unavailable"
)

// SecretKeyPattern is the regex enforced by the app_secrets.key CHECK constraint
// (migrations/00005_secrets.sql) AND the apid input validator. Uppercase ASCII,
// digits, underscores; must start with a letter. Plain ASCII keeps the path
// stable across runtimes (no Unicode normalization gotchas) and matches what
// every shell / k8s / systemd treats as an env-var name.
const SecretKeyPattern = `^[A-Z][A-Z0-9_]*$`

// MaxSecretKeyLen bounds the secret key name. Mirrors Unix env-var limits
// (NAME_MAX is 255 on Linux) and keeps per-row index size reasonable.
const MaxSecretKeyLen = 128

// OrgSlugPattern is the regex enforced by the ORG slug validator in PR 5
// (issue #190 / IAM-6 / ADR-061) and reused by ErrOrgSlugInvalid's detail
// string so the rejection copy carries the same shape the handler enforces.
// Lowercase ASCII letters, digits, and single dashes; must start and end
// with a letter or digit (no leading or trailing dash); 3..32 chars total.
// Reserved keywords are checked at the handler layer, not in this regex,
// because the keyword set changes over time and belongs alongside the
// reserved-slug seed data in cmd/apid.
const OrgSlugPattern = `^[a-z0-9][a-z0-9-]{1,30}[a-z0-9]$`

// MaxOrgSlugLen bounds the org slug length. Mirrors the regex's upper
// bound so a future contributor tightening the pattern keeps the two in
// lock-step.
const MaxOrgSlugLen = 32

// StatusForCode returns the HTTP status a given stable Code maps to. It is the
// inverse of the per-code status the constructors below hardcode, kept in one
// table so any surface that reconstructs a Problem without a Status (notably
// pkg/grpcerr.FromStatus, which lifts a gRPC error back into a Problem carrying
// only the Code) can recover the right HTTP status. Unknown codes default to
// 500 — a reconstructed Problem is never served without a real status.
func StatusForCode(code string) int {
	switch code {
	case CodePlanLimitApps, CodePlanLimitRAM, CodeAppLayerTooBig, CodeBillingPastDue,
		CodePlanPublicAuthIPAllowlistNotAllowed:
		return http.StatusForbidden
	case CodePlanLimitConcur, CodeQuotaExhausted, CodeAppConcurReached, CodeExportRateLimited:
		return http.StatusTooManyRequests
	case CodeSourceTooLarge:
		return http.StatusRequestEntityTooLarge
	case CodeSourceInvalid, CodeBuildUndetected, CodeValidation, CodeCronInvalid,
		CodeAlertRuleInvalid, CodeAppWebhookInvalid, CodeHandlerMissing, CodeImageRequired,
		CodeEgressAllowlistTooLong, CodePublicAuthIPAllowlistTooLong,
		CodeInvalidEgressAllowlist, CodeInvalidPublicAuthIPAllowlist:
		return http.StatusBadRequest
	case CodeWorkflowDeploymentUnavailable:
		return http.StatusNotImplemented
	case CodeCapacity, CodeBuildOOM, CodeBuildTimeout, CodeOAuthProviderUnavailable, CodeWaitForWarm,
		CodeEdgeRuleMaintenance, CodeAppMaintenance, CodeMirrorSlotAtCapacity:
		return http.StatusServiceUnavailable
	case CodeScanCritical:
		// 503 — the base ext4 has a CRITICAL Grype finding
		// (issue #299). SLO-exempt: a CRITICAL CVE is a known
		// bad, not an operator fault, and the wake must fail
		// closed regardless of customer SLO posture. The
		// operator rebuilds the base to clear the sidecar.
		return http.StatusServiceUnavailable
	case CodeBuildSBOMUnavailable:
		// 503 — the SBOM populator hasn't run (issue #299 /
		// ADR-038 Phase 3). The build row itself is final; the
		// SBOM artefact is best-effort post-mortem. SLO-exempt
		// for the same reason as CodeScanCritical: "missing
		// observational metadata" is not a customer-impacting
		// fault, and the SDK distinguishes 404 build-not-found
		// from 503 SBOM-missing so customer agents can branch.
		return http.StatusServiceUnavailable
	case CodeUnauthorized:
		return http.StatusUnauthorized
	case CodeSessionExpired, CodeSessionInvalid:
		return http.StatusUnauthorized
	case CodeNotFound:
		return http.StatusNotFound
	// ADR-124 deployment queue controls. Cancel-of-live +
	// reorder-of-non-pending map to 409 Conflict; range-error
	// priority maps to 422 (handled at the Problem constructor
	// since the StatusForCode fallback returns 422 generically).
	case CodeConflict, CodeDomainNotVerified, CodeNoRollbackTarget,
		CodeDeploymentCancelLiveForbidden, CodeDeploymentCancelNotCancellable,
		CodeDeploymentReorderNotPending:
		return http.StatusConflict
	case CodeTrafficPercentSumInvalid:
		// 409 — issue #556. Σ(traffic_percent WHERE status='live')
		// != 100 after UpdateDeploymentTraffic. Defensive backstop;
		// unreachable in practice. Sits next to CodeConflict /
		// CodeDomainNotVerified / CodeNoRollbackTarget because the
		// semantics are "the requested state cannot be applied
		// alongside the existing row set", not "your plan forbids
		// this".
		return http.StatusConflict
	case CodeDeployFailed:
		return http.StatusUnprocessableEntity
	case CodeDeploySignatureInvalid:
		// 403 — the deploy is REJECTED at accept time, distinct from
		// CodeSigInvalid's 503 (which fires on the cold-boot layer-verify
		// path). See CodeDeploySignatureInvalid declaration above.
		return http.StatusForbidden
	case CodeUnsupportedByCLI:
		// 403 — the cookie-only-route guard (pkg/api/client.go)
		// rejected the path before issuing the request. 403 is
		// the closest sibling to CodeDeploySignatureInvalid's
		// "this caller cannot complete this action" semantic.
		// Companion to CodeSessionExpired (401) which is the
		// server-side mirror for a cookie that WAS sent but is
		// gone.
		return http.StatusForbidden
	case CodeImageNotFound, CodeImageManifestInvalid:
		return http.StatusUnprocessableEntity
	case CodeInvalidTrafficPercent:
		// 422 — issue #556. traffic_percent outside [0, 100].
		// Sits next to CodeImageNotFound / CodeImageManifestInvalid
		// (422 family). Mirrors CodeInvalidMinInstances's
		// shape-second 422 contract; the plan-gate cousin
		// (CodePlanTrafficSplitNotAllowed) is the 403 above.
		return http.StatusUnprocessableEntity
	case CodePlanMirrorNotAllowed:
		// 403 — issue #72 / ADR-125 PR-A2. Free/Hobby plan tier
		// gate for the mirror surface. Mirrors CodePlanTrafficSplitNotAllowed
		// (403, plan-gate cousin). The 422 sibling family below
		// (quota / percent / cross-app / source-target-same /
		// invalid-window) handles shape errors; this is the only
		// 403 in the mirror family.
		return http.StatusForbidden
	case CodeMirrorRuleQuotaExceeded,
		CodeInvalidMirrorPercent,
		CodeMirrorSourceTargetSame,
		CodeMirrorCrossAppMismatch,
		CodeInvalidMirrorWindow:
		// 422 — issue #72 / ADR-125 PR-A2 mirror shape family.
		// Per-app quota exceeded, percent outside [0, 100],
		// source==mirror, cross-app deployment pair, and
		// malformed `?window=` all land here. The 409 sibling
		// (CodeMirrorDeploymentNotLive) handles "rule is legal
		// but referenced deployment state has moved".
		return http.StatusUnprocessableEntity
	case CodeMirrorDeploymentNotLive:
		// 409 — issue #72 / ADR-125 PR-A2. Mirrors
		// CodeTrafficPercentSumInvalid (409 family): the rule
		// body is internally consistent but the deployment
		// state has moved between the customer's GET and POST.
		// Sits next to CodeTrafficPercentSumInvalid because the
		// retry path is identical (GET fresh deployment ids,
		// retry).
		return http.StatusConflict
	case CodeMirrorRuleNotFound:
		// 404 — issue #72 / ADR-125 PR-A2. Reserved for the
		// post-IDOR "rule was deleted between requests" case.
		// Cross-account lookups return s.notFound with the
		// generic "not_found" code (not this), so probing
		// cannot distinguish "exists in another account" from
		// "doesn't exist anywhere".
		return http.StatusNotFound
	case CodeImageEgressDenied:
		return http.StatusForbidden
	case CodeStatelessOnlyViolation:
		// 422 — the deploy shape (or resolved base image) is a stateful
		// one this platform does not support in year one. Sits next to
		// CodeDeployFailed: well-formed request, content policy refuses.
		// imaged also lifts this code onto deployments.error_code, so
		// the GET /v1/deployments/{id} response and the CLI's
		// `faas deployment <id>` render it identically.
		return http.StatusUnprocessableEntity
	case CodeAppNotListening,
		CodeAppLoopbackBound,
		CodeAppArchMismatch,
		CodeEnvVarMissing,
		CodeAppHealthzUnauthorized,
		CodeAppRuntimeOOM,
		CodeDepInstallFailed,
		CodeAppStartupTimeout:
		// 422 — error-explanations cluster (spec §6.4 amendment 1).
		// Same family as CodeStatelessOnlyViolation / CodeDeployFailed:
		// well-formed request, content policy refuses. The Detail
		// field distinguishes the 9 failures; the pkg/whycopy catalog
		// renders hint/why/fix prose on the CLI's 3-5 line renderer.
		return http.StatusUnprocessableEntity
	case CodeRequestValidationFailed:
		// 422 — kind=validate edge rule rejected the request body.
		// Sits next to CodeStatelessOnlyViolation / CodeDeployFailed
		// in the 422 family: well-formed request, content policy
		// refuses. The detail body carries Problem.Errors; an SDK
		// distinguishes this from CodeValidation by the `code`
		// (gate lives on the gateway hot path, not the apid layer).
		return http.StatusUnprocessableEntity
	case CodePayment:
		return http.StatusPaymentRequired
	case CodePlanLimitSecrets:
		return http.StatusForbidden
	case CodePlanTrafficSplitNotAllowed:
		// 403 — issue #556. Free/Hobby trying to set a
		// non-default traffic_percent; mirrors CodePlanMinInstancesNotAllowed.
		return http.StatusForbidden
	case CodeSecretInvalidKey, CodeSecretNotFound:
		return http.StatusBadRequest
	case CodeSecretValueTooLarge:
		return http.StatusRequestEntityTooLarge
	// Issue #461 / ADR-062 — per-app private-registry Basic Auth.
	// The DELETE-absent posture is 400 (mirrors CodeSecretNotFound
	// convention; the URL resource IS the host, distinct from
	// CodeNotFound which is reserved for app-not-found). Plan
	// quota is 403, value size is 413, host validation is 400.
	case CodePlanRegistryCredentialNotAllowed:
		return http.StatusForbidden
	case CodePlanRegistryCredentialQuota:
		return http.StatusRequestEntityTooLarge
	case CodeInvalidRegistryHost, CodeRegistryCredentialNotFound:
		return http.StatusBadRequest
	// ADR-098: data-placement hints (issue #395 mirror + Free gate).
	// CodePlanDataUpstreamsNotAllowed = 402 (plan doesn't unlock the
	// surface, like CodePlanWebhooksNotAllowed). CodePlanLimitDataUpstreams
	// = 403 (per-plan cap reached, like CodePlanWebhookQuota).
	case CodePlanDataUpstreamsNotAllowed:
		return http.StatusPaymentRequired
	case CodePlanLimitDataUpstreams:
		return http.StatusForbidden
	// ADR-119 — per-app static egress IP. Gate (402) mirrors
	// CodePlanDataUpstreamsNotAllowed; quota (403) mirrors
	// CodePlanWebhookQuota; shape error (400) sits next to
	// CodeUpstreamInvalidHost / CodeEnvVarInvalidKey. Same family
	// pattern so the CLI's "your plan does not unlock X" / "fix
	// the IP shape" templates render uniformly.
	case CodePlanStaticEgressIPNotAllowed:
		return http.StatusPaymentRequired
	case CodePlanStaticEgressIPQuota:
		return http.StatusForbidden
	case CodeAppStaticEgressIPInvalid:
		return http.StatusBadRequest
	case CodeStaticEgressIPNotProvisioned:
		return http.StatusNotFound
	case CodeUpstreamInvalidKind, CodeUpstreamInvalidHost, CodeUpstreamInvalidPort:
		return http.StatusBadRequest
	case CodeUpstreamNotFound:
		return http.StatusNotFound
	// Env vars (issue #395 / ADR-045): mirror the secrets status shape
	// so SDK callers can reuse the same error-decoding pattern. Plan
	// quota is 403, value size is 413, key regex + not-found are 400.
	case CodePlanLimitEnvVars:
		return http.StatusForbidden
	case CodeEnvVarInvalidKey, CodeEnvVarNotFound:
		return http.StatusBadRequest
	case CodeEnvVarValueTooLarge:
		return http.StatusRequestEntityTooLarge
	// IAM-5 (issue #189): api-key lifecycle. Both gate errors are
	// 401 (the bearer key is invalid for the same reason any
	// unauthenticated call is — the customer's credential is no
	// longer usable). The quota error is 409 to mirror the alert-rule
	// quota shape (issue #396) — the operation is well-formed but
	// caps the platform; the SDK can branch on the code for
	// actionable retry guidance.
	case CodeAPIKeyExpired, CodeAPIKeyRevoked:
		return http.StatusUnauthorized
	case CodeAPIKeyLimitExceeded:
		return http.StatusConflict
	// Trusted cosign signers (issue #472 / ADR-054): mirror the env
	// status shape. PUT body shape is 400, quota is 403, not-found is
	// 404 (the URL resource IS the signer name; we deliberately
	// diverge from the secret/env 400 to make the resource model
	// explicit on the new surface).
	case CodePlanLimitTrustedSigners:
		return http.StatusForbidden
	case CodeTrustedSignerInvalid:
		return http.StatusBadRequest
	case CodeTrustedSignerNotFound:
		return http.StatusNotFound
	case CodeAccountDeletionConfirm, CodeAccountDeletionPending, CodeAccountNotRestorable:
		return http.StatusConflict
	case CodeAppRenameFailed:
		return http.StatusConflict
	case CodeCliAuthPending:
		return http.StatusNotFound
	case CodeCliAuthUnavailable:
		return http.StatusGone
	case CodePlanQueueDepth, CodePlanDelayedCap:
		return http.StatusForbidden
	case CodePlanSourceBytes:
		return http.StatusRequestEntityTooLarge
	case CodePlanFeatureGated:
		return http.StatusPaymentRequired
	case CodePlanCronsNotAllowed:
		return http.StatusPaymentRequired
	case CodePlanCronQuota:
		return http.StatusForbidden
	case CodePlanAlertRulesNotAllowed:
		return http.StatusPaymentRequired
	case CodePlanAlertRuleQuota:
		return http.StatusForbidden
	case CodePlanLogArchiveNotAllowed:
		// Issue #562 / PR-B: Free customers don't have log
		// archive read-back. Same shape as the other plan-
		// gated "X unavailable on this plan" codes: 402 +
		// a deliberate upsell. The gatewayd-internal archive
		// handler maps LogArchiveEnabled() == false to this
		// code via ErrPlanLogArchiveNotAllowed.
		return http.StatusPaymentRequired
	case CodePlanPerAppMetricsNotAllowed:
		// Per-app observability surface (per-app metrics +
		// wake-timeline JSON mirror) is Hobby+. Free gets the
		// 402 + upsell at the apid handler edge
		// (cmd/apid/handlers_metrics.go +
		// cmd/apid/handlers_wake_timeline.go). Mirrors the
		// LogArchiveNotAllowed posture.
		return http.StatusPaymentRequired
	case CodePlanAppUsageSummaryNotAllowed:
		// Per-app billing-usage read is Hobby+. Free gets the
		// 402 + upsell at cmd/apid/handlers_usage.go. Same
		// family as the other "X unavailable on this plan"
		// codes above.
		return http.StatusPaymentRequired
	case CodePlanAppErrorsNotAllowed:
		// Per-app error-fingerprint read is Hobby+. Free gets
		// the 402 + upsell at
		// cmd/apid/handlers_app_errors.go. Same family as
		// the other "X unavailable on this plan" codes
		// above. The retention ceiling is enforced separately
		// via Limits.AppErrorsRetentionDays (the handler
		// clamps the `since` window to the new bound on
		// every call).
		return http.StatusPaymentRequired
	// Issue #561 — spend cap pauses workload. 402 mirrors the existing
	// `CodePlanFeatureGated` / `CodePlanCronsNotAllowed` /
	// `CodePlanAlertRulesNotAllowed` family: a deliberate account-level
	// setting that requires customer action (raise the cap), not a
	// retry. The customer's Stripe-invoice worldview matches 402
	// (plan-tier and budget-shape refusals all live here).
	case CodeAdmissionRefused:
		return http.StatusPaymentRequired
	// Issue #476 / ADR-076 — webhook subscription gate + per-plan
	// per-app/per-account webhook quota. Webhooks are Hobby+; the
	// Free plan gets a 402 mirroring the existing Cron/AlertRules
	// 402 family. Quota overage (Hobby cap=5/acct, Pro cap=100/acct,
	// Scale cap=500/acct) is a 403 like the existing cron quota.
	case CodePlanWebhooksNotAllowed:
		return http.StatusPaymentRequired
	case CodePlanWebhookQuota:
		return http.StatusForbidden
	// Issue #462 / ADR-058 — scaling policy gate. PR-A History
	// (2026-07-31): Hobby+ tier-up for max_instances. 403 mirrors
	// CodePlanMinInstancesNotAllowed.
	case CodePlanMaxInstancesNotAllowed:
		return http.StatusForbidden
	// Issue #554 / ADR-078: liveness probe plan gate. 403 mirrors
	// CodePlanMaxInstancesNotAllowed / CodePlanEgressAllowlistNotAllowed
	// so the CLI's "your plan does not unlock X" template renders
	// uniformly.
	case CodePlanLivenessProbeNotAllowed:
		return http.StatusForbidden
	case CodeInvalidMaxInstances, CodeInvalidCooldown,
		CodeScalingTargetIncompatibleWithWorkloadClass:
		// 422 — request shape is well-formed but the value
		// conflicts with the plan or the workload class. Sits
		// next to CodeInvalidMinInstances on the same `422`
		// branch so a single switch case covers the cluster.
		return http.StatusUnprocessableEntity
	case CodeInvocationNotFound:
		return http.StatusNotFound
	case CodeInvocationNotReplayable:
		// Issue #315: replay attempts on a non-replayable state
		// surface 409 — the conflict is between the request and
		// the resource's current state, not between two writes.
		return http.StatusConflict
	case CodeInvalidCredentials, CodeEmailNotVerified:
		return http.StatusUnauthorized
	case CodePasswordTooWeak, CodeAccountExists:
		return http.StatusBadRequest
	case CodeResetTokenInvalid, CodeResetTokenExpired:
		return http.StatusGone
	// Organizations (issue #190 / IAM-6 / ADR-061). 404 for slug
	// not-found matches the IDOR convention used by LoadApp
	// (cross-tenant access returns 404, never 403). 422 for slug
	// invalid is a shape failure (matching the convention of
	// CodeCronInvalid / CodeEnvVarInvalidKey). 409 for slug
	// taken / already member / last-owner / personal-org
	// immutability surfaces the lifecycle collisions. 410 for
	// invitation invalid / expired mirrors the password-reset
	// one-shot links (the resource was a one-shot and is no
	// longer addressable). 403 for role / member-cap /
	// invitation-cap refusal surfaces the plan-limit + RBAC
	// failure modes. 409 for the legacy API key re-bind so
	// existing keys can be told to mint an org-bound successor
	// without deleting the row.
	case CodeOrgNotFound:
		return http.StatusNotFound
	case CodeOrgSlugInvalid:
		return http.StatusUnprocessableEntity
	case CodeOrgSlugTaken, CodeOrgAlreadyMember, CodeOrgLastOwner,
		CodeOrgPersonalImmutable, CodeOrgAPIKeyRequiresOrg:
		return http.StatusConflict
	case CodeOrgInvitationInvalid, CodeOrgInvitationExpired:
		return http.StatusGone
	case CodeOrgRoleForbidden, CodeOrgMemberCapExceeded, CodeOrgInvitationCapExceeded:
		return http.StatusForbidden
	case CodeBadRequest:
		return http.StatusBadRequest
	case CodeBadGateway:
		return http.StatusBadGateway
	case CodeUnsupportedMediaType:
		return http.StatusUnsupportedMediaType
	case CodeRequestTooLarge:
		return http.StatusRequestEntityTooLarge
	// Jobs (issue #1184 Workstream A / ADR-099 supplement). Eight
	// codes that ship with Mega-1 (CR-8 / code-review #8 — the
	// gRPC error path lifts a gRPC status into a Problem carrying
	// only the Code, so any of these eight codes that landed on a
	// gRPC error without an explicit case here was reconstructed
	// as 500). Reverse mapping pinned to the wire contract:
	//
	//   404 jobs_not_allowed       — Free-plan gate (404 not 403 so
	//                                a Free account can probe the
	//                                surface without leaking Jobs
	//                                exists on paid tiers).
	//   404 job_task_not_found     — single-task lookup miss.
	//   404 job_image_missing      — referenced image_id doesn't
	//                                exist or isn't job-runnable.
	//   409 job_run_cancelled      — cancel of already-terminal run.
	//   409 job_has_live_instances — soft-delete denied; live
	//                                kind='job_task' instances
	//                                still need to drain.
	//   410 job_deadline_exceeded  — wall-clock deadline cap
	//                                (distinct from cancel — the
	//                                customer authored the
	//                                deadline, not an explicit
	//                                cancel call).
	//   429 job_quota_exceeded     — every per-plan job quota
	//                                family (JobMaxPerAccount /
	//                                JobConcurrentPerAccount /
	//                                JobRAMMB / JobTaskTimeoutSec /
	//                                JobMaxParallelismPerRun /
	//                                JobMaxTasksPerRun / JobMaxRetries).
	//   400 job_command_invalid    — command[] shape violation
	//                                (length > 64 or embedded NUL).
	case CodeJobsNotAllowed, CodeJobTaskNotFound, CodeJobImageMissing:
		return http.StatusNotFound
	case CodeJobRunCancelled, CodeJobHasLiveInstances:
		return http.StatusConflict
	case CodeJobDeadlineExceeded:
		return http.StatusGone
	case CodeJobQuotaExceeded:
		return http.StatusTooManyRequests
	case CodeJobCommandInvalid:
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

// Convenience constructors for the most common limit errors keep call sites to
// one line and guarantee the limit/observed/docs fields are always populated.

// ErrPlanLimitApps is returned when a deploy would exceed the plan's app count.
func ErrPlanLimitApps(l Limits, observed int) *Problem {
	return NewProblem(http.StatusForbidden, CodePlanLimitApps,
		"App limit reached",
		fmt.Sprintf("%s plan allows %d deployed app(s); you have %d.", l.Plan, l.DeployedApps, observed)).
		WithLimit(int64(l.DeployedApps), int64(observed)).
		WithDocs(docsBase + "/plans#apps")
}

// ErrPlanLimitRAM is returned when a requested ram_mb exceeds the plan cap.
func ErrPlanLimitRAM(l Limits, requestedMB int) *Problem {
	return NewProblem(http.StatusForbidden, CodePlanLimitRAM,
		"RAM over plan limit",
		fmt.Sprintf("%s plan caps %d MB/app; requested %d MB.", l.Plan, l.RAMMB, requestedMB)).
		WithLimit(int64(l.RAMMB), int64(requestedMB)).
		WithDocs(docsBase + "/plans#ram")
}

// ErrAppLayerTooLarge is returned when the built app layer (deps + code) exceeds
// the plan's drive1 cap (spec §4.6). The message names the cap and observed size
// so the deploy failure is actionable.
func ErrAppLayerTooLarge(l Limits, observedBytes int64) *Problem {
	capBytes := int64(l.AppLayerMaxMB) * 1024 * 1024
	return NewProblem(http.StatusForbidden, CodeAppLayerTooBig,
		"App too large",
		fmt.Sprintf("%s plan caps the app layer at %d MB; built layer is %.1f MB.",
			l.Plan, l.AppLayerMaxMB, float64(observedBytes)/(1024*1024))).
		WithLimit(capBytes, observedBytes).
		WithDocs(docsBase + "/build/limits#app-layer")
}

// ErrPlanLimitConcurrency is returned when waking another instance would exceed
// the app's concurrency (spec §4.3 admission, invariant §6.2-1).
func ErrPlanLimitConcurrency(l Limits, observed int) *Problem {
	return NewProblem(http.StatusTooManyRequests, CodePlanLimitConcur,
		"Concurrency limit reached",
		fmt.Sprintf("%s plan allows %d concurrent instance(s) per app; %d already live.", l.Plan, l.MaxConcurrency, observed)).
		WithLimit(int64(l.MaxConcurrency), int64(observed)).
		WithDocs(docsBase + "/plans#concurrency")
}

// ErrCapacity is returned when admission is refused for lack of box capacity
// (RAM headroom or vCPU slots, spec §4.3). This should be near-impossible in
// practice — admission alerts fire long before customers see it (spec §12) — so
// it is a page for us, not just a message for them (UX spec §7).
// ErrAppConcurrencyReached is returned by Engine.AdmitInstance when the
// app is already at its effective max_concurrency (issue #168). The
// gateway treats this as a benign no-op when it already has ≥1 cached
// target; the Wire RPC carries the same information as a typed
// at_capacity boolean so the gateway never has to parse problems.
func ErrAppConcurrencyReached(l Limits, observed int) *Problem {
	return NewProblem(http.StatusTooManyRequests, CodeAppConcurReached,
		"App concurrency reached",
		fmt.Sprintf("%s plan allows %d concurrent instance(s) per app; %d already live.", l.Plan, l.MaxConcurrency, observed)).
		WithLimit(int64(l.MaxConcurrency), int64(observed)).
		WithDocs(docsBase + "/plans#concurrency")
}

func ErrCapacity(detail string) *Problem {
	return NewProblem(http.StatusServiceUnavailable, CodeCapacity,
		"Briefly at capacity", detail).
		WithDocs("https://gregale.dev/status")
}

// ErrWaitForWarm is returned when a wake is held by the customer's
// per-app scale-out cooldown (issue #462 / PR-D). The 503 + Retry-After
// wire shape is the v1 contract: StatusForCode must map CodeWaitForWarm
// to http.StatusServiceUnavailable so pkg/grpcerr.FromStatus can lift
// the gRPC code back to the right HTTP status. The cooldownS argument
// is the seconds remaining until the cooldown expires; the
// constructor bounds it at 1 (cooldownS <= 0 is treated as 1) so the
// Retry-After header is always a positive integer. The Retry-After
// header is the canonical UX — clients that consult the header can
// back off without polling the 503 + body alone.
func ErrWaitForWarm(cooldownS int, l Limits, observed int) *Problem {
	if cooldownS <= 0 {
		cooldownS = 1
	}
	return NewProblem(http.StatusServiceUnavailable, CodeWaitForWarm,
		"Scale-out cooldown in effect",
		fmt.Sprintf(
			"App is on a scale-out cooldown; %d more second(s) before the next wake is allowed. "+
				"Plan %s allows %d concurrent instance(s); %d live.",
			cooldownS, l.Plan, l.MaxConcurrency, observed)).
		WithLimit(int64(cooldownS), int64(observed)).
		WithDocs(docsBase+"/scaling-policy#cooldown").
		WithHeader("Retry-After", strconv.Itoa(cooldownS))
}

// ErrEdgeRuleMaintenance is returned by the gatewayd hot-path
// applier (pkg/gateway.(*Handler).applyEdgeRuleMaintenance,
// §4.1.2.13) when a kind=maintenance edge-rule matches the inbound
// (host, path, http_method). The Retry-After header is always a
// positive integer; per-rule retryAfterSeconds (from the rule's
// EdgeRuleMaintenanceAction) overrides the platform default
// (EdgeRuleMaintenanceRetryAfterSeconds, 60 s) when > 0. msg is
// the customer-facing detail string from the rule's action body
// (≤ 512 B; same payload-size budget as
// EdgeRuleValidateAction.Schema), surfaced as Problem.detail so
// monitoring / curl users see why the endpoint is dark without
// scraping the rule row. The builder clamps retryAfterS at 1 s on
// the floor so the wire always emits a non-zero hint, matching
// the ErrWaitForWarm convention.
func ErrEdgeRuleMaintenance(retryAfterS int, msg string) *Problem {
	if retryAfterS <= 0 {
		retryAfterS = EdgeRuleMaintenanceRetryAfterSeconds
	}
	detail := "This endpoint is in maintenance mode"
	if msg != "" {
		detail = "Maintenance: " + msg
	}
	return NewProblem(http.StatusServiceUnavailable, CodeEdgeRuleMaintenance,
		"Endpoint in maintenance", detail).
		WithHeader("Retry-After", strconv.Itoa(retryAfterS))
}

// ErrAppMaintenanceMode is the coarse-gate sibling of
// ErrEdgeRuleMaintenance — returned by the gatewayd hot-path
// applier (pkg/gateway.(*Handler).applyAppsMaintenanceMode,
// §4.1.2.0) when the matched app's apps.maintenance_mode boolean
// is true. retryAfterS is the platform default
// (EdgeRuleMaintenanceRetryAfterSeconds, 60 s) today; a future
// per-app retry_after_seconds column is D20.X. appSlug is surfaced
// in the Problem.detail so monitoring / curl users see which app
// is in maintenance. The builder clamps retryAfterS at 1 s on the
// floor so the wire always emits a non-zero hint.
func ErrAppMaintenanceMode(retryAfterS int, appSlug string) *Problem {
	if retryAfterS <= 0 {
		retryAfterS = EdgeRuleMaintenanceRetryAfterSeconds
	}
	return NewProblem(http.StatusServiceUnavailable, CodeAppMaintenance,
		"App in maintenance", fmt.Sprintf("App %q is in maintenance mode", appSlug)).
		WithHeader("Retry-After", strconv.Itoa(retryAfterS))
}

// ErrAdmissionRefused is the typed Problem builder for the issue #561
// spend-cap pause-workload path. schedd.Engine.admitGate returns
// wakeOverageCapReached when accounts.overage_cap_cents is set and
// meterd's CurrentMonthOverageCents meets/exceeds the cap; the
// caller lifts to this Problem. The HTTP 402 status travels via
// StatusForCode (line 826 family: 402 = "your account's setting is
// blocking us"). observedCents / capCents are exposed as the Problem's
// Limit (cap) and Observed (overage) pointer fields; a script-side
// caller can compute "how much to raise" without parsing the
// human-readable Detail string. No Retry-After: the cap is a
// deliberate customer budget, not back-pressure; the customer action
// is to raise or clear the cap, not to retry.
func ErrAdmissionRefused(observedCents, capCents int64) *Problem {
	return NewProblem(http.StatusPaymentRequired, CodeAdmissionRefused,
		"Spend cap reached",
		fmt.Sprintf(
			"Current-month overage is %d cents; configured cap is %d cents. "+
				"Raise the cap (POST /v1/account/overage-cap) to resume wake traffic.",
			observedCents, capCents)).
		WithLimit(capCents, observedCents).
		WithDocs(docsBase + "/billing#spend-cap")
}

// ErrExportRateLimited is returned by GET /v1/account/export when
// the account has already served an export inside the 24h rate
// window (issue #755 / PR-5.1). 429 + Retry-After: the wire carries
// seconds-until-reset so a well-behaved SDK can back off without
// re-parsing the body. distinct from CodeQuotaExhausted (plan-level
// monthly usage, 429) and CodePlanLimitConcur (per-app concurrency,
// 429): this is a self-imposed abuse-mitigation on a GDPR endpoint,
// not a billing gate, so the human-readable Title is "export rate
// limited" rather than "quota exhausted". retryAfterS is bounded at
// 1 second (matches the ErrWaitForWarm convention) so the wire
// always emits a positive Retry-After.
func ErrExportRateLimited(retryAfterS int) *Problem {
	if retryAfterS <= 0 {
		retryAfterS = 1
	}
	return NewProblem(http.StatusTooManyRequests, CodeExportRateLimited,
		"Export rate limited",
		"Only one account export is allowed per 24h window; retry after the indicated back-off.").
		WithHeader("Retry-After", fmt.Sprintf("%d", retryAfterS)).
		WithDocs("https://docs.gregale.dev/gdpr#export-rate-limit")
}

// ErrInternal is the catch-all 500 envelope for handler-side failures
// that aren't a deliberate refusal (use ErrCapacity for those) — DB
// commit errors, partial state, unexpected plumbing. Pairs with
// CodeInternal; the detail rides through verbatim because it surfaces
// in the operator's browser console as the only breadcrumb for the
// on-call engineer (the audit row carries the same text).
func ErrInternal(detail string) *Problem {
	return NewProblem(http.StatusInternalServerError, CodeInternal,
		"Internal Error", detail)
}

// ErrStepUpRequired is returned by RequireStepUp (ADR-077 +
// PR-8 acceptance) when the Envelope.StepUpAt stamp is missing or
// stale. The 403 carries CodeStepUpRequired (not CodeMFARequired)
// so the dashboard can render "re-enter your authenticator code"
// copy instead of "enable MFA to continue". The audit kind
// "auth.step_up_required" with reason: "missing"|"expired" is the
// load-bearing security signal; this helper is the UX affordance.
func ErrStepUpRequired() *Problem {
	return NewProblem(http.StatusForbidden, CodeStepUpRequired,
		"Step-up required",
		"step-up MFA required for this action: complete /v1/account/mfa/verify to refresh")
}

// ErrBillingNotImplemented is returned by an apid handler when the selected
// provider (per FAAS_BILLING_PROVIDER) does not support the requested billing
// surface. The 501 makes provider capability gaps explicit to operators;
// callers branch on errors.Is(err, billing.ErrNotImplemented) and route here.
func ErrBillingNotImplemented(detail string) *Problem {
	return NewProblem(http.StatusNotImplemented, CodeBillingNotImplemented,
		"Billing provider does not support this surface", detail).
		WithDocs(docsBase + "/billing/providers")
}

// ErrSourceTooLarge is returned when an uploaded tarball exceeds the plan cap.
func ErrSourceTooLarge(l Limits, observedBytes int64) *Problem {
	capBytes := int64(l.SourceTarballMaxMB) * 1024 * 1024
	return NewProblem(http.StatusRequestEntityTooLarge, CodeSourceTooLarge,
		"Source too large",
		fmt.Sprintf("%s plan caps source at %d MB.", l.Plan, l.SourceTarballMaxMB)).
		WithLimit(capBytes, observedBytes).
		WithDocs(docsBase + "/build/limits")
}

// ErrSourceInvalid is returned when a tarball fails shape validation
// (symlink escape, absolute path, >10k files, wrong magic bytes, etc.).
func ErrSourceInvalid(reason string) *Problem {
	return NewProblem(http.StatusBadRequest, CodeSourceInvalid,
		"Source invalid", reason).
		WithDocs(docsBase + "/build/source")
}

// ErrStatelessOnlyViolation is returned when a deploy shape (or resolved
// base image) requires persistent state — VOLUME in Dockerfile, mkfs/mount
// of a block device, a top-level data/ or db/ directory in the tarball, or
// a base image like postgres:16 / redis:7 / mysql:8 — and the platform is
// stateless-only in year one.
//
// kind classifies where the violation was caught so the customer can fix
// the right thing: "dockerfile" → edit the Dockerfile, "tarball" → move
// data/, "base_image" → switch to a managed service. detail is the offending
// path/image and lands verbatim in the RFC 7807 body.
func ErrStatelessOnlyViolation(kind, detail string) *Problem {
	return NewProblem(http.StatusUnprocessableEntity, CodeStatelessOnlyViolation,
		"Stateless-only platform",
		fmt.Sprintf("%s: %s — this platform is stateless in year one; "+
			"use a managed service (S3/R2/Neon/Upstash/MongoDB Atlas).",
			kind, detail)).
		// Every WithDocs() in this file is sourced from
		// docsBase (the package-local constant), which is
		// duplicated from pkg/wire.DocsHost because pkg/api
		// cannot import pkg/wire (cycle). The /storage page
		// is added by PR-B (Wave 0, faas init + reference
		// templates) — until then the URL 404s, consistent
		// with every other docs URL in the file.
		WithDocs(docsBase + "/storage")
}

// Error-explanations cluster constructors (spec §6.4 amendment 1,
// ADR-110 amendment 1). Each constructor returns the canonical Problem
// shape (status 422, code in the family, Title stable, Detail carries
// the observed value) WITHOUT hint/why/fix — those are attached later
// by the detection site via WithHint/WithWhy/WithFix so each failure
// can carry code-specific prose. The pkg/whycopy catalog is the
// single source of truth for the prose; constructors here only
// anchor the code → status → docs URL plumbing.
//
// Why this shape: the wire spine (Hint/Why/Fix/RelevantLogs on Problem)
// is already committed (commit 1); the detection sites (commits 7-13)
// attach the prose at the point where they have the observed value;
// the central catalog (commit 3) owns the prose body. Constructors
// stay minimal so a new code is a 3-line addition.

// ErrAppNotListening is returned when the wake readiness probe finds
// no process listening on the customer's $PORT (typically ECONNREFUSED
// on the TCP-accept dial, or a 4xx/5xx on the healthcheck path). The
// detection site (pkg/fcvm/vmm.go) attaches hint/why/fix prose from
// pkg/whycopy with the observed port + last-dial-error.
func ErrAppNotListening(observedPort string) *Problem {
	return NewProblem(http.StatusUnprocessableEntity, CodeAppNotListening,
		"No process listening on $PORT",
		fmt.Sprintf("readiness probe found no listener on port %s after the wake timeout.",
			observedPort)).
		WithDocs(docsBase + "/errors/app-not-listening")
}

// ErrAppLoopbackBound is returned when the customer's app binds its
// listener to 127.0.0.1 (or ::1) — the per-VM bridge proxies from
// 10.0.0.2, so a loopback bind never receives traffic even though the
// wake readiness probe passes. The detection site (pkg/fcvm/vmm.go via
// WaitCharacterizationReport's listening_addrs) attaches prose from
// pkg/whycopy with the observed bind address.
func ErrAppLoopbackBound(observedBind string) *Problem {
	return NewProblem(http.StatusUnprocessableEntity, CodeAppLoopbackBound,
		"Application bound to loopback",
		fmt.Sprintf("app is listening on %s; the per-VM bridge proxies requests to 10.0.0.2, "+
			"so loopback-only binds never receive traffic.", observedBind)).
		WithDocs(docsBase + "/errors/app-loopback-bound")
}

// ErrAppArchMismatch is returned when the build VM cannot execute the
// customer's binary because the host/target architecture disagrees
// (e.g. darwin/arm64 binary on a linux/amd64 control plane). The
// detection site (pkg/builderd/vm_metal.go::classifyBuildFailure)
// attaches prose with the observed binary arch + the required target.
func ErrAppArchMismatch(observedArch, requiredArch string) *Problem {
	return NewProblem(http.StatusUnprocessableEntity, CodeAppArchMismatch,
		"Unsupported CPU architecture",
		fmt.Sprintf("binary is %s; this control plane runs %s. "+
			"rebuild with the matching target.",
			observedArch, requiredArch)).
		WithDocs(docsBase + "/errors/app-arch-mismatch")
}

// ErrEnvVarMissing is returned when a deploy-time preflight detects
// that the customer's source references an env var (os.Getenv in Go,
// process.env in Node, os.environ in Python) that is not declared in
// the app's env config. Preflight is warn-only; this error fires only
// when --strict is set on `gregale deploy` or when the runtime
// supervisor observes an execve crash from a missing key. Detection
// site attaches prose with the observed env var name.
func ErrEnvVarMissing(envVar string) *Problem {
	return NewProblem(http.StatusUnprocessableEntity, CodeEnvVarMissing,
		"Missing environment variable",
		fmt.Sprintf("source references $%s but it is not declared in the app's env config.",
			envVar)).
		WithDocs(docsBase + "/errors/env-var-missing")
}

// ErrAppHealthzUnauthorized is returned when the customer's health
// endpoint returns 401/403 within the liveness window — the host
// can't distinguish "the app is up but the /healthz path is gated"
// from "the app is down", so the deployment flips to failed after
// ConsecutiveFailures consecutive 401s. The detection site
// (cmd/vmmd/liveness_recv.go) attaches prose with the observed status.
func ErrAppHealthzUnauthorized(observedStatus int) *Problem {
	return NewProblem(http.StatusUnprocessableEntity, CodeAppHealthzUnauthorized,
		"Health endpoint returning 401",
		fmt.Sprintf("/healthz returned %d; consecutive 401s flip the deployment to failed.",
			observedStatus)).
		WithDocs(docsBase + "/errors/app-healthz-unauthorized")
}

// ErrAppRuntimeOOM is returned when the cgroup OOM-killer terminates
// the customer's main workload inside the microVM (memory.max = plan +
// 8 MB was exceeded). Distinct from CodeBuildOOM (the *build* VM's
// OOM, which is a separate code with a different remediation path).
//
// Detection chain (Cluster C, ADR-121):
//
//	guest/init/cgroup_partition_linux.go::WatchOOM  (cgroup.events
//	  poll on the per-workload cgroup v2 leaf)
//	  → guest/init/framework_ready_emit.go::EmitWorkloadOOM
//	    (AF_VSOCK DGRAM, port 1027, type byte 0x05, JSON body)
//	  → cmd/vmmd/framework_ready_recv.go::dispatchWorkloadOOM
//	  → pkg/fcvm/manager.go::ReportWorkloadOOM
//	  → pkg/scheddgrpc::Server.ReportWorkloadOOM
//	  → pkg/sched/engine.go::DestroyForWorkloadOOMFailure
//	    (stamps the deployment row via SetDeploymentFailedEx with
//	     CodeAppRuntimeOOM + the whycopy Observed payload)
//
// The host-side cgroup (which sees only the firecracker process) is
// NOT a detection source — only the guest can see the workload OOM
// because the per-VM workload lives under the guest's cgroup
// namespace, invisible from the host. The constructor receives the
// observed peak MB and the plan cap MB; the engine handler stamps
// the deployment detail row with the templated Hint/Why/Fix (see
// pkg/whycopy/whycopy.go::CodeAppRuntimeOOM.Observed).
func ErrAppRuntimeOOM(observedPeakMB, planMB int) *Problem {
	return NewProblem(http.StatusUnprocessableEntity, CodeAppRuntimeOOM,
		"Container out of memory",
		fmt.Sprintf("cgroup OOM-kill fired at %d MB (plan cap %d MB); upgrade plan or trim in-memory state.",
			observedPeakMB, planMB)).
		WithDocs(docsBase + "/errors/app-runtime-oom")
}

// ErrDepInstallFailed is returned when the build VM's dependency
// installation step fails (npm install / pip install / go build /
// etc.). The discriminator (pkg=npm|pip|go|...) is carried in the
// Detail field and surfaced via pkg/whycopy so the CLI renders
// per-package prose. Detection site
// (pkg/builderd/vm_metal.go::classifyBuildFailure) attaches the
// pkg + the observed exit code + the last 20 lines of build output.
func ErrDepInstallFailed(pkgManager, observedCmd string) *Problem {
	return NewProblem(http.StatusUnprocessableEntity, CodeDepInstallFailed,
		"Dependency installation failure",
		fmt.Sprintf("%s install failed: %s — see build log for the failing command.",
			pkgManager, observedCmd)).
		WithDocs(docsBase + "/errors/dep-install-failed")
}

// ErrAppStartupTimeout is returned when the customer's app does not
// become ready within the wake timeout (35s by default; per-app
// startup_timeout_s column). Distinct from idle_timeout_s (which is
// the wake→park timer, not the boot timer). Detection site
// (pkg/sched/engine.go::StuckReason) carries the observed StuckReason
// ("waking_timeout" / "cold_boot_timeout") into the prose.
func ErrAppStartupTimeout(stuckReason, observedDuration string) *Problem {
	return NewProblem(http.StatusUnprocessableEntity, CodeAppStartupTimeout,
		"Application startup timeout",
		fmt.Sprintf("app did not become ready after %s (stuck_reason=%s).",
			observedDuration, stuckReason)).
		WithDocs(docsBase + "/errors/app-startup-timeout")
}

// ErrDomainNotVerified is returned when a customer tries to bind a domain
// whose TXT challenge hasn't been satisfied yet (spec §7).
func ErrDomainNotVerified(domain string) *Problem {
	return NewProblem(http.StatusConflict, CodeDomainNotVerified,
		"Domain not verified",
		fmt.Sprintf("TXT challenge for %q not yet satisfied; publish the required TXT record and retry.", domain)).
		WithDocs(docsBase + "/domains/verify")
}

// ErrDomainVerificationFailed (issue #961 / Mega-A PR-3) is the 422
// returned by `gregale domains verify` when the DNS + cert walk
// finds a missing/mismatched TXT record, a CNAME loop, or a
// reachability failure. The CLI prints the problem code verbatim so
// the customer can grep their dashboard for the exact failure mode.
func ErrDomainVerificationFailed(domain, reason string) *Problem {
	return NewProblem(http.StatusUnprocessableEntity, CodeDomainVerificationFailed,
		"Domain verification failed",
		fmt.Sprintf("%s: %s — fix the DNS record and retry.", domain, reason)).
		WithDocs(docsBase + "/domains/verify")
}

// ErrDomainCertNotIssued (issue #961 / Mega-A PR-3) is the 422
// returned by `gregale domains verify` when the port-443 cert is a
// CDN cert whose SANs do not include the customer's domain. The
// hint names the failure mode so the customer knows to either wait
// for Gregale cert propagation or rebuild the edge against a
// Gregale-issued cert.
func ErrDomainCertNotIssued(domain, reason string) *Problem {
	return NewProblem(http.StatusUnprocessableEntity, CodeDomainCertNotIssued,
		"Domain cert not issued",
		fmt.Sprintf("port-443 cert for %q is not Gregale-issued: %s", domain, reason)).
		WithDocs(docsBase + "/domains/verify")
}

// ErrDoctorDisabled (ADR-120) is the 503 returned by
// `GET /v1/domains/{domain}/doctor` when
// FAAS_DOMAIN_DOCTOR_ENABLED is unset. The route stays
// registered (per the pre-#911 pattern in api/flags.go) so
// the CLI gets a deterministic error code rather than a
// generic 404. The detail line is the operator-facing
// "set FAAS_DOMAIN_DOCTOR_ENABLED=1" hint.
func ErrDoctorDisabled() *Problem {
	return NewProblem(http.StatusServiceUnavailable, CodeDoctorDisabled,
		"Domain doctor is dark-launched",
		"the FAAS_DOMAIN_DOCTOR_ENABLED flag is not set on this cluster; ask the operator to enable it or use `gregale domains verify` for a one-shot check").
		WithDocs(docsBase + "/domains/doctor")
}

// ErrDoctorUnavailable (ADR-120) is the 503 returned when
// the doctor flag IS on but the probe pass failed in a way
// the doctor handler can't recover from (e.g. a Postgres
// round-trip error reading the observation row). Distinct
// from ErrDoctorDisabled (the dark-launch case).
func ErrDoctorUnavailable(domain, reason string) *Problem {
	return NewProblem(http.StatusServiceUnavailable, CodeDoctorUnavailable,
		"Domain doctor unavailable",
		fmt.Sprintf("doctor probes for %s failed: %s", domain, reason)).
		WithDocs(docsBase + "/domains/doctor")
}

// ErrCronInvalid is returned for malformed cron expressions.
func ErrCronInvalid(reason string) *Problem {
	return NewProblem(http.StatusBadRequest, CodeCronInvalid,
		"Invalid cron schedule", reason).
		WithDocs(docsBase + "/crons")
}

// CodePlanCronsNotAllowed is the 402 the customer sees when the
// plan doesn't unlock crons at all (Free today). Mirrors the
// CodePlanFeatureGated shape — the dashboard renders an upsell
// prompt, not a "delete a cron to add another" hint, because the
// only path forward is a plan upgrade.
const CodePlanCronsNotAllowed = "plan_crons_not_allowed"

// CodePlanCronQuota is the 403 the customer sees when the plan
// DOES unlock crons but the per-app or per-account cap was reached.
// Distinct from CodePlanCronsNotAllowed so the CLI can branch on
// upsell-vs-delete copy without parsing the body.
const CodePlanCronQuota = "plan_cron_quota"

// CodePlanGateBlocked is the 403 the operator sees when a scan
// project's plan gate was blocked pre-exclude AND the post-exclude
// --exclude filter did not rescue it. ADR-124 follow-up #1
// (cmd/gregale/commands_decompose.go::planProblem 4th branch);
// distinct from CodePlanLimitApps/CodePlanCronQuota because the
// rejection carries the full can_apply_reasons list (one or more
// of the gate's pre-exclude evaluations) so the operator sees
// every blocker, not just the first one. The wire sets this only
// when gateRescuedByExclude is false (otherwise CanApply is true
// and the apply path is reachable). Mirrors the rescue info
// printPlanText renders on can_apply: true.
const CodePlanGateBlocked = "plan_gate_blocked"

// CodePlanAlertRulesNotAllowed is the 402 the customer sees when
// the plan doesn't unlock alert rules at all (Free today; the
// plan-tier gate fires before loadApp so the slug's existence is
// never leaked). Mirrors the CodePlanFeatureGated shape — the
// dashboard renders an upsell prompt, not a quota hint, because
// the only path forward is a plan upgrade.
const CodePlanAlertRulesNotAllowed = "plan_alert_rules_not_allowed"

// CodePlanAlertRuleQuota is the 403 the customer sees when the
// plan DOES unlock alert rules but the per-app or per-account
// cap was reached. Distinct from CodePlanAlertRulesNotAllowed so
// the CLI can branch on upsell-vs-delete copy without parsing
// the body.
const CodePlanAlertRuleQuota = "plan_alert_rule_quota"

// CodeAlertPresetInvalid is the 400 the customer sees when an
// enable-from-preset body is malformed (closed-set drift on
// cooldown_minutes, oversized webhook_secret, etc). Issue
// #1233 / ADR-123.
const CodeAlertPresetInvalid = "alert_preset_invalid"

// CodeAlertPresetDisabled is the 400 the customer sees when the
// catalog row is enabled_in_catalog=false (the preset is staged
// for a future release). Mirrors CodeAlertPresetInvalid's
// status so the dashboard renders the same "coming soon"
// affordance the catalog grid already shows for the disabled
// rows.
const CodeAlertPresetDisabled = "alert_preset_disabled"

// CodePlanAlertPresetsNotAllowed is the 402 the customer sees
// when their plan is below the preset's minimum_plan (e.g. a
// Hobby customer trying to enable api_down whose minimum_plan
// is Pro). Fires BEFORE loadApp so a low-plan customer posting
// to a non-existent slug gets a clean 402 — same slug-leak
// guard as CodePlanAlertRulesNotAllowed. Issue #1233 / ADR-123.
const CodePlanAlertPresetsNotAllowed = "plan_alert_presets_not_allowed"

// CodePlanConsumerKeyQuotaReached is the RFC 7807 stable code
// returned when the per-app or per-account consumer_keys quota is
// exhausted. The intent is "your plan allows N keys per app (or per
// account) and you already hold N — revoke one or upgrade before
// minting another." PR #5-B's apid CreateConsumerKey handler returns
// 403 with this code on POST attempts.
// Why stable: the apid sdk-go / sdk-node / sdk-python surfaces map
// this code to a typed error class so customers can write a
// one-line handler for the quota-exhausted case without parsing the
// prose body — same shape as CodePlanAlertRuleQuota.
const CodePlanConsumerKeyQuotaReached = "plan_consumer_key_quota_reached"

// CodeConsumerKeysNotAllowed is the RFC 7807 stable code returned when
// the customer's plan is gated off the consumer_keys feature entirely
// (Free tier today; mirrors CronLimitPerApp posture). The apid handler
// returns 402 plan_not_allowed with this code on POST attempts.
// Why stable: mirrors CodePlanAlertRulesNotAllowed shape so SDK
// authors can write a single switch on plan-gated codes.
const CodeConsumerKeysNotAllowed = "consumer_keys_not_allowed"

// CodePlanCorsPresetQuotaReached is the RFC 7807 stable code
// returned when the per-app or per-account cors_presets quota
// is exhausted (issue #975 #4 / PR-B / ADR-129). The apid POST
// /v1/cors-presets handler returns 403 with this code on
// attempts that exceed Plan.CorsPresetsPerApp or
// Plan.CorsPresetsPerAccount. Mirrors
// CodePlanOpenAPIDocQuotaReached (ADR-122) — same shape, same
// "delete one to add another" copy, same WithLimit doc URL.
const CodePlanCorsPresetQuotaReached = "plan_cors_preset_quota_reached"

// CodePlanCorsPresetsNotAllowed is the RFC 7807 stable code
// returned when the customer's plan is gated off the
// cors_presets feature entirely (Free tier; mirrors
// CodePlanOpenAPIDocsNotAllowed and CodeConsumerKeysNotAllowed).
// The apid POST /v1/cors-presets handler returns 402
// plan_not_allowed with this code on attempts. Why stable:
// SDK authors can write a single switch on plan-gated codes.
const CodePlanCorsPresetsNotAllowed = "plan_cors_presets_not_allowed"

// ErrPlanCorsPresetsNotAllowed (issue #975 #4 PR-B / ADR-129)
// is returned by the apid cors_presets POST handler when the
// customer's plan has CorsPresetsPerAccount == 0 (Free today).
// 402 plan_not_allowed because the upsell posture is the same
// as the inline CORS rules — Free customers stay on inline
// kind=cors rules (the abuse-floor tier), presets are an
// abstraction above the floor.
func ErrPlanCorsPresetsNotAllowed(p Plan) *Problem {
	return NewProblem(http.StatusPaymentRequired, CodePlanCorsPresetsNotAllowed,
		"CORS presets unavailable on this plan",
		fmt.Sprintf("the %s plan does not include CORS presets; upgrade to Hobby or above to manage reusable CORS configurations.", p)).
		WithLimit(0, 0).
		WithDocs(docsBase + "/plans#cors-presets")
}

// ErrPlanCorsPresetQuotaReached (issue #975 #4 PR-B / ADR-129)
// is returned when the per-account or per-app cors_presets
// quota is reached. 403 because the plan DOES unlock presets —
// the right copy is "delete one to add another", not "upgrade
// to Hobby". Mirrors ErrPlanOpenAPIDocQuota.
func ErrPlanCorsPresetQuotaReached(plan Plan, scope string, limit, observed int) *Problem {
	return NewProblem(http.StatusForbidden, CodePlanCorsPresetQuotaReached,
		"CORS preset limit reached",
		fmt.Sprintf("%s plan caps CORS presets at %d per %s; you have %d. Delete one to add another.",
			plan, limit, scope, observed)).
		WithLimit(int64(limit), int64(observed)).
		WithDocs(docsBase + "/plans#cors-presets")
}

// CodePlanOpenAPIDocQuotaReached is the RFC 7807 stable code
// returned when the per-deployment or per-account deployment_openapi_docs
// quota is exhausted (ADR-122 / issue #975 item #1). The apid PATCH
// handler returns 403 with this code on attempts that exceed
// Plan.OpenAPIDocsPerDeployment or Plan.OpenAPIDocsPerAccount.
// Why stable: the apid SDK surfaces map this code to a typed
// error class so customers can write a single handler for the
// quota-exhausted case without parsing the prose body — same
// shape as CodePlanConsumerKeyQuotaReached.
const CodePlanOpenAPIDocQuotaReached = "plan_openapi_doc_quota_reached"

// CodePlanOpenAPIDocTooLarge is the RFC 7807 stable code returned
// when the customer-uploaded OpenAPI body exceeds the per-plan
// byte cap (ADR-122 §D4). The apid PATCH handler returns 413 with
// this code on attempts that exceed Plan.OpenAPIDocMaxBytes.
// Why stable: the apid SDK surfaces map this code to a typed
// error class so customers can pre-validate the doc size before
// attempting the upload — same shape as CodePlanConsumerKeyQuotaReached.
const CodePlanOpenAPIDocTooLarge = "plan_openapi_doc_too_large"

// CodePlanOpenAPIDocsNotAllowed is the RFC 7807 stable code
// returned when the customer's plan is gated off the endpoint
// discovery feature entirely (Free tier; mirrors
// CodeConsumerKeysNotAllowed). The apid GET / PATCH handlers
// return 402 plan_not_allowed with this code on attempts.
// Why stable: mirrors CodeConsumerKeysNotAllowed shape so SDK
// authors can write a single switch on plan-gated codes.
const CodePlanOpenAPIDocsNotAllowed = "openapi_docs_not_allowed"

// ErrPlanOpenAPIDocsNotAllowed (ADR-122 / issue #975 item #1) is
// returned by the apid endpoint-discovery handlers when the
// customer's plan has OpenAPIDocsPerDeployment == 0 (Free today).
// Fires BEFORE loadApp so a Free customer posting to a non-existent
// slug gets a clean 402 instead of a 404 that would leak the slug's
// existence. Mirrors ErrPlanAlertRulesNotAllowed.
func ErrPlanOpenAPIDocsNotAllowed(p Plan) *Problem {
	return NewProblem(http.StatusPaymentRequired, CodePlanOpenAPIDocsNotAllowed,
		"Endpoint discovery unavailable on this plan",
		fmt.Sprintf("the %s plan does not include endpoint discovery; upgrade to Hobby or above to capture OpenAPI documents.", p)).
		WithLimit(0, 0).
		WithDocs(docsBase + "/plans#endpoint-discovery")
}

// ErrPlanOpenAPIDocQuota (ADR-122 / issue #975 item #1) is returned
// when the per-account OpenAPI doc quota is reached. 403 because
// the plan DOES unlock discovery — the right copy is "delete a
// doc to add another", not "upgrade to Hobby". Mirrors
// ErrPlanAlertRuleQuota.
func ErrPlanOpenAPIDocQuota(plan Plan, limit, observed int) *Problem {
	return NewProblem(http.StatusForbidden, CodePlanOpenAPIDocQuotaReached,
		"OpenAPI document limit reached",
		fmt.Sprintf("%s plan caps OpenAPI documents at %d per account; you have %d. Delete one to add another.",
			plan, limit, observed)).
		WithLimit(int64(limit), int64(observed)).
		WithDocs(docsBase + "/plans#endpoint-discovery")
}

// ErrPlanOpenAPIDocTooLarge (ADR-122 / issue #975 item #1) is
// returned when the customer-uploaded OpenAPI body exceeds the
// per-plan byte cap. 413 (not 422) because the body is a
// well-formed JSON document that just happens to be too large —
// the same posture as a request-payload Too Large. Mirrors
// ErrPlanConsumerKeyQuotaReached.
func ErrPlanOpenAPIDocTooLarge(plan Plan, limit, observed int) *Problem {
	return NewProblem(http.StatusRequestEntityTooLarge, CodePlanOpenAPIDocTooLarge,
		"OpenAPI document exceeds the per-plan byte cap",
		fmt.Sprintf("%s plan caps OpenAPI documents at %d bytes; yours is %d. Trim paths or split the spec.",
			plan, limit, observed)).
		WithLimit(int64(limit), int64(observed)).
		WithDocs(docsBase + "/plans#endpoint-discovery")
}

// PlanQuotaScopeAccount / PlanQuotaScopeApp are the values the
// *Quota functions receive in their `scope` argument. Mirrors
// state.CronQuotaScopeAccount / CronQuotaScopeApp and the alert /
// webhook analogues (state.AlertRuleQuotaScopeAccount etc).
// Kept here as plain string consts so pkg/api does not import
// pkg/state (the pkg/api ↔ pkg/state cycle is a load-bearing
// constraint — memory: pkg-api-cannot-import-pkg-state). The three
// site-name strings ("this account" / "this app") that drive the
// goconst rule live below alongside these.
const (
	PlanQuotaScopeAccount = "account"
	PlanQuotaScopeApp     = "app"
)

// PlanQuotaScopeDisplayName returns the human-readable scope label
// used in the per-plan-quota 403 body ("this account" / "this app").
// Exported so handler tests can assert against the same strings
// without re-declaring the literals.
func PlanQuotaScopeDisplayName(scope string) string {
	switch scope {
	case PlanQuotaScopeAccount:
		return "this account"
	default:
		return "this app"
	}
}

// CodePlanWebhooksNotAllowed is the 402 the customer sees when
// the plan doesn't unlock outbound webhooks at all (Free today).
// Issue #476 / ADR-076. Mirrors CodePlanAlertRulesNotAllowed.
const CodePlanWebhooksNotAllowed = "plan_webhooks_not_allowed"

// CodePlanWebhookQuota is the 403 the customer sees when the plan
// DOES unlock webhooks but the per-app or per-account cap was
// reached. Distinct from CodePlanWebhooksNotAllowed so the CLI
// can branch on upsell-vs-delete copy without parsing the body.
const CodePlanWebhookQuota = "plan_webhook_quota"

// CodePlanTriggersNotAllowed is the 402 the customer sees when
// the plan doesn't unlock the unified Trigger primitive at all
// (Free today, issue #757 / ADR-0NN). Mirrors CodePlanCronsNotAllowed
// and CodePlanWebhooksNotAllowed. The handler rejects BEFORE loadApp
// so a Free customer posting to a non-existent slug gets the upsell
// instead of a 404 that would leak the slug's existence (PR review
// finding F4 mirrored from createAlertRule / createAppWebhook).
const CodePlanTriggersNotAllowed = "plan_triggers_not_allowed"

// CodePlanTriggerQuota is the 403 the customer sees when the plan
// DOES unlock triggers but the per-app or per-account cap was
// reached. Distinct from CodePlanTriggersNotAllowed so the CLI
// can branch on upsell-vs-delete copy without parsing the body.
// Mirrors CodePlanCronQuota / CodePlanWebhookQuota.
const CodePlanTriggerQuota = "plan_trigger_quota"

// CodeTriggerBatchWindowTooLarge is the 403 returned when
// POST/PATCH /v1/triggers carries a batch_window_ms that exceeds the
// plan cap (limits.TriggerBatchWindowMaxSec — Hobby 30s, Pro/Scale
// 300s). Mirrors CodePlanTriggerQuota's plan-cap semantic; a
// distinct code keeps the CLI's batch_window field-specific
// guidance independent of the count-quota advice.
// Added for PR #993 / issue #757 review MED-4.
const CodeTriggerBatchWindowTooLarge = "trigger_batch_window_too_large"

// CodeTriggerTLSSkipVerifyNotAllowed is the 403 returned when a
// Kafka trigger carries skip_verify=true on a plan whose
// TLSSkipVerifyAllowed flag is false (Free + Hobby today).
// skip_verify is dangerous — it disables hostname + cert
// verification — and we ship it only to plans that have signed off
// on the operational risk (Pro + Scale today). Mirrors the
// CodeTenantSurfacesNotAllowed shape (capability not plan-quota).
// Added for PR #993 / issue #757 review MED-4.
const CodeTriggerTLSSkipVerifyNotAllowed = "trigger_tls_skip_verify_not_allowed"

// CodePlanLogArchiveNotAllowed is the 402 the customer sees when
// they request ?archive=1 against an app on a plan whose
// LogArchiveEnabled() returns false (Free today, issue #562).
// Fires BEFORE the gatewayd-internal handler touches S3 so a
// Free customer gets a clean 402 instead of an S3 403 from a
// bucket they don't have read access to. The wire is the same
// as ErrPlanCronsNotAllowed: 402 + a stable `code` the SDK
// branches on without parsing the body.
const CodePlanLogArchiveNotAllowed = "plan_log_archive_not_allowed"

// ErrPlanLogArchiveNotAllowed is returned by the gatewayd-internal
// archive log read-back handler when the customer's plan has
// LogArchiveEnabled() == false (Free today). Mirrors
// ErrPlanCronsNotAllowed and ErrPlanAlertRulesNotAllowed so the
// upsell surface is consistent across plan-gated features. The
// Hobby+ copy is the deliberate upgrade hint; Free never sees
// the bucket-proxy path so the customer gets the upsell message
// rather than a silent 404.
func ErrPlanLogArchiveNotAllowed(p Plan) *Problem {
	return NewProblem(http.StatusPaymentRequired, CodePlanLogArchiveNotAllowed,
		"Log archive unavailable on this plan",
		fmt.Sprintf("the %s plan does not include log archive read-back; upgrade to Hobby or above to query historical logs from object storage.", p)).
		WithDocs(docsBase + "/plans#log-archive")
}

// CodePlanPerAppMetricsNotAllowed (per-app observability surface)
// gates GET /v1/apps/{slug}/metrics and the JSON mirror of the
// wake-timeline page (GET /v1/apps/{slug}/wake-timeline). Mirrors
// ErrPlanLogArchiveNotAllowed / ErrPlanCronsNotAllowed: 402 + a
// stable `code` the SDK branches on without parsing the body. Free
// never sees the latency / cold-boot / wake-narrative surface so
// the customer gets the upsell rather than a silent 404.
const CodePlanPerAppMetricsNotAllowed = "plan_per_app_metrics_not_allowed"

// ErrPlanPerAppMetricsNotAllowed is returned by the apid
// per-app observability handlers (cmd/apid/handlers_metrics.go +
// cmd/apid/handlers_wake_timeline.go) when the customer's plan has
// PerAppMetricsAllowed() == false (Free today). The Hobby+ copy is
// the deliberate upgrade hint; Free never sees the metrics surface
// so the customer gets the upsell message rather than a silent 404.
func ErrPlanPerAppMetricsNotAllowed(p Plan) *Problem {
	return NewProblem(http.StatusPaymentRequired, CodePlanPerAppMetricsNotAllowed,
		"Per-app metrics unavailable on this plan",
		fmt.Sprintf("the %s plan does not include per-app metrics; upgrade to Hobby or above to see latency, error rate, cold-boot ratio and the wake-narrative.", p)).
		WithDocs(docsBase + "/plans#per-app-metrics")
}

// CodePlanAppUsageSummaryNotAllowed (per-app billing-usage read)
// gates GET /v1/apps/{slug}/usage. Mirrors the ErrPlanLogArchive
// shape: 402 + a stable `code` the SDK branches on without parsing
// the body. Free never sees the billing-usage surface so the
// customer gets the upsell rather than a silent 404.
const CodePlanAppUsageSummaryNotAllowed = "plan_app_usage_summary_not_allowed"

// ErrPlanAppUsageSummaryNotAllowed is returned by the apid usage
// handler (cmd/apid/handlers_usage.go) when the customer's plan has
// AppUsageSummaryAllowed() == false (Free today). The Hobby+ copy
// is the deliberate upgrade hint; Free never sees the
// billing-usage surface so the customer gets the upsell message
// rather than a silent 404.
func ErrPlanAppUsageSummaryNotAllowed(p Plan) *Problem {
	return NewProblem(http.StatusPaymentRequired, CodePlanAppUsageSummaryNotAllowed,
		"App usage summary unavailable on this plan",
		fmt.Sprintf("the %s plan does not include per-app billing-usage read-back; upgrade to Hobby or above to see this-month GB-hours and the plan-included vs overage split.", p)).
		WithDocs(docsBase + "/plans#app-usage-summary")
}

// CodePlanAppErrorsNotAllowed (per-app error-fingerprint read)
// gates GET /v1/apps/{slug}/errors/summary. Mirrors the
// ErrPlanLogArchive shape: 402 + a stable `code` the SDK branches
// on without parsing the body. Free never sees the grouped-error
// surface so the customer gets the upsell rather than a silent
// 404.
const CodePlanAppErrorsNotAllowed = "plan_app_errors_not_allowed"

// ErrPlanAppErrorsNotAllowed is returned by the apid
// error-summary handler (cmd/apid/handlers_app_errors.go) when the
// customer's plan has AppErrorsAllowed() == false (Free today).
// The Hobby+ copy is the deliberate upgrade hint; Free never sees
// the grouped-error surface so the customer gets the upsell
// message rather than a silent 404. The retention ceiling is
// separately enforced via Limits.AppErrorsRetentionDays — the
// handler clamps the `since` window to the new bound on every
// call so a downgraded customer sees "no history visible" rather
// than a torn page.
func ErrPlanAppErrorsNotAllowed(p Plan) *Problem {
	return NewProblem(http.StatusPaymentRequired, CodePlanAppErrorsNotAllowed,
		"App error grouping unavailable on this plan",
		fmt.Sprintf("the %s plan does not include per-app error-fingerprint grouping; upgrade to Hobby or above to see top failing endpoints and drill-down samples.", p)).
		WithDocs(docsBase + "/plans#app-errors")
}

// CodeAppWebhookInvalid is the 400 the customer sees for any
// malformed webhook body — missing target_url, invalid retry_policy,
// out-of-vocabulary event, oversize webhook_secret, etc.
const CodeAppWebhookInvalid = "app_webhook_invalid"

// Edge rules (ADR-089). Each code maps to one wire-level failure
// mode so the CLI can surface a stable, machine-readable error.
// Naming follows the alert_rules / webhooks convention
// (`<resource>_<verb>_<state>`).
const (
	CodeEdgeRuleNotFound           = "edge_rule_not_found"
	CodeEdgeRuleConflict           = "edge_rule_conflict"
	CodePlanLimitEdgeRules         = "plan_limit_edge_rules"
	CodePlanEdgeRuleKindNotAllowed = "plan_edge_rule_kind_not_allowed"
	// CodePlanEdgeRuleKindQuotaReached is the per-kind quota error
	// (ADR-091 D22). Distinct from CodePlanLimitEdgeRules so the
	// customer sees the specific kind that tripped ("1/1 geo rules
	// on Free; upgrade to Hobby for 5") rather than the generic
	// "edge rules cap reached" message. The href in the problem
	// payload points to the per-kind paragraphs in the docs.
	CodePlanEdgeRuleKindQuotaReached = "plan_edge_rule_kind_quota_reached"
	CodeCORSOriginNotAllowed         = "cors_origin_not_allowed"
	CodeJWTMissingToken              = "jwt_missing_token"
	CodeJWTMissingIssuer             = "jwt_missing_issuer"
	CodeJWTAudienceMismatch          = "jwt_audience_mismatch"
	CodeJWTSignatureInvalid          = "jwt_signature_invalid"
	CodeIPDenied                     = "ip_denied"
	// CodeGeoDenied mirrors CodeIPDenied for the kind=geo primitive
	// (ADR-091 D21). Distinct so dashboards / metrics can
	// disambiguate a geo deny from any other 403 on the wire — a
	// geo deny is policy-driven (ISO 3166-1 allow/deny), not an
	// auth failure or scope check, and the stable code lets
	// customers write runbooks that key on code !=
	// insufficient_scope without parsing titles.
	CodeGeoDenied = "geo_denied"
	// CodeRequestValidationFailed is the 422 a kind=validate edge
	// rule emits when the inbound request body fails the customer's
	// JSON Schema. Carries Problem.Errors (Cloudflare / Stripe shape)
	// with one FieldError per mismatch (field + expected + got) so an
	// SDK can drive form-field UI without parsing prose. Distinct
	// from CodeValidation (the apid body-shape guard) because the
	// gating policy and the actor are different.
	CodeRequestValidationFailed = "request_validation_failed"
	CodeHeaderMutationForbidden = "header_mutation_forbidden"
	// CodeRequestBudgetExceeded is the 504 a kind=budget edge rule
	// (or its plan-level default) emits when the per-request
	// wall-clock budget fires before the handler can write a
	// response. The platform-enforced budget — the load-bearing
	// contract is "deadline fires from any hop, not just the
	// handler body" — surfaces this single stable code on every
	// outbound problem envelope so an SDK can branch on it
	// without parsing prose. ADR-093 §Decision.
	CodeRequestBudgetExceeded = "request_budget_exceeded"
	// CodeInvalidRecoverAction (issue #976 / ADR-122 /
	// SAFE-RELEASES-R) is the 422 the recover_rollout handler
	// emits when the request body's `action` is outside the
	// closed set {"advance","promote","abort"}. Mirrors
	// CodeInvalidTrafficPercent (422 for shape errors that the
	// plan-gate / state-machine guards downstream would not
	// trip on).
	CodeInvalidRecoverAction = "invalid_recover_action"
	// CodeRolloutNotStuck (issue #976 / ADR-122 / SAFE-RELEASES-R)
	// is the 409 the recover_rollout handler emits when the
	// operator asks for action="advance" on a rollout that is
	// NOT stuck (canary_step_started_at within the stuck-after
	// window). Distinct from CodeRolloutStateInvalid because
	// the customer-facing fix is different ("use promote
	// instead" vs "rollout already terminal").
	CodeRolloutNotStuck = "rollout_not_stuck"
	// CodeRolloutStateInvalid (issue #976 / ADR-122 /
	// SAFE-RELEASES-R) is the 409 the recover_rollout handler
	// emits when the rollout is already in a terminal state
	// ('complete' or 'aborted') and the requested recovery
	// cannot proceed. Distinct from CodeRolloutNotStuck because
	// the failure mode is "already done" vs "not stuck yet".
	CodeRolloutStateInvalid = "rollout_state_invalid"
)

// ErrPlanCronsNotAllowed is returned by apid's createCron handler
// when the customer's plan has CronLimitPerApp == 0 (Free today).
// Fires BEFORE the store is touched so a Free customer gets a clean
// 402 instead of a quota-error round-trip.
func ErrPlanCronsNotAllowed(p Plan) *Problem {
	return NewProblem(http.StatusPaymentRequired, CodePlanCronsNotAllowed,
		"Crons unavailable on this plan",
		fmt.Sprintf("the %s plan does not include cron; upgrade to Hobby or above to schedule synthetic requests.", p)).
		WithDocs(docsBase + "/plans#crons")
}

// ErrPlanCronQuota is returned when CreateCronIfUnderQuota surfaces
// a *state.CronQuotaError. Scope "app" or "account" tells the
// handler which cap fired so the body can name it. 403 (not 402)
// because the plan DOES unlock crons — the right copy is
// "delete a cron to add another", not "upgrade to Hobby".
func ErrPlanCronQuota(plan Plan, scope string, limit, observed int) *Problem {
	scopeName := PlanQuotaScopeDisplayName(scope)
	return NewProblem(http.StatusForbidden, CodePlanCronQuota,
		"Cron limit reached",
		fmt.Sprintf("%s plan caps crons at %d for %s; you have %d. Delete one to add another.",
			plan, limit, scopeName, observed)).
		WithLimit(int64(limit), int64(observed)).
		WithDocs(docsBase + "/plans#crons")
}

// ErrPlanJobsNotAllowed is returned by apid's createJob handler
// when the customer's plan has JobsAllowed == false (Free today).
// Fires BEFORE the store is touched so a Free customer gets a clean
// 402 instead of a quota round-trip. Pairs with Plan.JobsAllowed()
// in the handler preamble. Mirrors the ErrPlanCronsNotAllowed shape
// (the closest cousin: another per-plan Workstream feature).
func ErrPlanJobsNotAllowed(p Plan) *Problem {
	return NewProblem(http.StatusPaymentRequired, CodeJobsNotAllowed,
		"Jobs unavailable on this plan",
		fmt.Sprintf("the %s plan does not include jobs; upgrade to Hobby or above to run batch / one-shot workloads.", p)).
		WithDocs(docsBase + "/plans#jobs")
}

// ErrJobQuota is returned when the JobCreateIfUnderQuota /
// JobRunCreateIfUnderQuota / dispatch gate surfaces a
// *state.JobQuotaError. QuotaName names the specific cap that
// fired (e.g. "per_account", "concurrent", "ram_mb",
// "task_timeout", "parallelism_per_run", "tasks_per_run",
// "retries"). The Problem carries Limit + Observed so the dashboard
// can render the same numeric envelope as the crons quota copy.
// 403 (not 402) because the plan DOES unlock jobs — the right copy
// is "delete / cancel / shrink to add another", not "upgrade".
func ErrJobQuota(plan Plan, quotaName string, limit, observed int) *Problem {
	return NewProblem(http.StatusForbidden, CodeJobQuotaExceeded,
		"Job limit reached",
		fmt.Sprintf("%s plan caps jobs at %d for %s; you have %d. Cancel a task or delete a job to free capacity.",
			plan, limit, quotaName, observed)).
		WithLimit(int64(limit), int64(observed)).
		WithDocs(docsBase + "/plans#jobs")
}

// ErrPlanWorkflowsNotAllowed is returned by apid workflow endpoints
// when the customer's plan has WorkflowsAllowed == false (Free today).
func ErrPlanWorkflowsNotAllowed(p Plan) *Problem {
	return NewProblem(http.StatusPaymentRequired, CodePlanWorkflowsNotAllowed,
		"Workflows unavailable on this plan",
		fmt.Sprintf("the %s plan does not include durable workflows; upgrade to Hobby or above to orchestrate multi-step processes.", p)).
		WithDocs(docsBase + "/plans#workflows")
}

// ErrPlanWorkflowsQuota is returned when an app exceeds its concurrent
// workflow runs quota.
func ErrPlanWorkflowsQuota(plan Plan, limit, observed int) *Problem {
	return NewProblem(http.StatusForbidden, CodePlanWorkflowsQuota,
		"Workflow run quota exceeded",
		fmt.Sprintf("concurrent workflow run quota reached (%d/%d) for the %s plan.", observed, limit, plan)).
		WithLimit(int64(limit), int64(observed)).
		WithDocs(docsBase + "/plans#workflows")
}

// ErrWorkflowDeploymentUnavailable prevents a client from mistaking the
// schema-only workflow foundation for a deploy path that can persist and
// serve definitions. It is temporary until the workflow runtime deployment
// endpoint is present; returning 501 is safer than accepting and dropping
// customer configuration.
func ErrWorkflowDeploymentUnavailable() *Problem {
	return NewProblem(http.StatusNotImplemented, CodeWorkflowDeploymentUnavailable,
		"Workflow deployment unavailable",
		"workflow definitions are validated by this release but require the workflow runtime deployment endpoint to be enabled")
}

// ErrWorkflowRunNotFound returns a 404 when a workflow run is not found.
func ErrWorkflowRunNotFound() *Problem {
	return NewProblem(http.StatusNotFound, CodeWorkflowRunNotFound,
		"Workflow run not found", "the requested workflow run was not found.")
}

// ErrWorkflowStepNotFound returns a 404 when a workflow step is not found.
func ErrWorkflowStepNotFound() *Problem {
	return NewProblem(http.StatusNotFound, CodeWorkflowStepNotFound,
		"Workflow step not found", "the requested workflow step was not found.")
}

// ErrWorkflowNotRunning returns a 409 when attempting an action on a non-running workflow run.
func ErrWorkflowNotRunning() *Problem {
	return NewProblem(http.StatusConflict, CodeWorkflowNotRunning,
		"Workflow run not running", "the workflow run is not in running or awaiting_event status.")
}

// ErrJobTaskNotFound marks a 404 on (run_id, task_index) lookups
// when the tuple doesn't exist OR belongs to a different account.
// Distinct from CodeNotFound so the dashboard can render a
// job-specific empty state. 404.
func ErrJobTaskNotFound(runID, taskIndex string) *Problem {
	return NewProblem(http.StatusNotFound, CodeJobTaskNotFound,
		"Job task not found",
		fmt.Sprintf("no job task at run %s / task %s on this account.", runID, taskIndex)).
		WithDocs(docsBase + "/jobs#tasks")
}

// ErrJobRunCancelled marks POST /v1/jobs/{name}/runs/{id}/cancel
// when the run is already in a terminal state. 409.
func ErrJobRunCancelled(runID, currentStatus string) *Problem {
	return NewProblem(http.StatusConflict, CodeJobRunCancelled,
		"Job run is already terminal",
		fmt.Sprintf("run %s is in status %s and cannot be cancelled; only queued / dispatched / running runs accept cancel.", runID, currentStatus)).
		WithDocs(docsBase + "/jobs#cancel")
}

// ErrJobDeadlineExceeded marks GET /v1/jobs/{name}/runs/{id} on
// a run in 'deadline_exceeded' aggregate status. Distinct from
// ErrJobRunCancelled because the cause is the customer's deadline
// (not an explicit cancel call) and the dashboard pivots the
// message to "raise the deadline or shorten the work". 410 Gone.
func ErrJobDeadlineExceeded(runID string, deadlineRFC3339 string) *Problem {
	return NewProblem(http.StatusGone, CodeJobDeadlineExceeded,
		"Job run deadline exceeded",
		fmt.Sprintf("run %s did not complete all tasks before its deadline_at of %s.", runID, deadlineRFC3339)).
		WithDocs(docsBase + "/jobs#deadlines")
}

// ErrJobHasLiveInstances is the soft-delete sentinel. Returned
// by DELETE /v1/jobs/{name} when soft_delete_job_if_no_live_instances()
// returns FALSE because at least one kind='job_task' instance
// is in a non-terminal state. The customer must cancel + wait
// for those tasks to reach a terminal state (or call retry /
// delete on them) before soft-deleting the template. 409.
func ErrJobHasLiveInstances(jobName string, liveCount int) *Problem {
	return NewProblem(http.StatusConflict, CodeJobHasLiveInstances,
		"Job has live instances",
		fmt.Sprintf("job %s has %d live task instance(s); cancel them and wait for termination before deleting.", jobName, liveCount)).
		WithDocs(docsBase + "/jobs#delete")
}

// ErrJobImageMissing marks POST /v1/jobs when the referenced
// image_id doesn't exist OR isn't a job-runnable kind. The
// engine validates at run-create time so a create-time check
// is a fast-fail. 422 (unprocessable) because the request shape
// is valid but the referent is missing — the customer can fix
// the field. Distinct from CodeInvalidRef because the wire
// contract for the field is "the referent doesn't exist" rather
// than "the format is wrong".
func ErrJobImageMissing(imageID string) *Problem {
	return NewProblem(http.StatusUnprocessableEntity, CodeJobImageMissing,
		"Job image not found",
		fmt.Sprintf("image %s does not exist or is not a job-runnable kind.", imageID)).
		WithDocs(docsBase + "/jobs#image")
}

// ErrJobCommandInvalid marks a command[] violation:
// array_length > JobMaxCommandLen (per-run: 64) OR a command
// element with embedded NUL. Distinct from CodeValidation
// because the wire contract is "the executor can't run this
// argv" rather than "the schema is wrong". 422.
func ErrJobCommandInvalid(reason string) *Problem {
	return NewProblem(http.StatusUnprocessableEntity, CodeJobCommandInvalid,
		"Job command invalid",
		reason).
		WithDocs(docsBase + "/jobs#command")
}

// ErrPlanEvictionPriorityReservedNotAllowed is the 403 apid returns
// when a Free customer PATCHes eviction_priority='reserved' (issue #475).
// The plan DOES have a tier — 'best_effort' — that the customer can
// already use; the upgrade copy is "reserved is a paid feature".
// 402 PaymentRequired mirrors the streaming / warm-snapshot / cron
// gate shape so the dashboard renders the same "upgrade to X" CTA.
func ErrPlanEvictionPriorityReservedNotAllowed(p Plan) *Problem {
	return NewProblem(http.StatusPaymentRequired, CodePlanEvictionPriorityReservedNotAllowed,
		"Reserved eviction priority is not available on this plan",
		fmt.Sprintf("the %s plan does not include the reserved eviction tier; upgrade to Hobby or above to opt in. The 'best_effort' tier is available on every plan.", p)).
		WithDocs(docsBase + "/plans#eviction-priority")
}

// ErrPlanPublicAuthBearerNotAllowed is the 402 apid returns when a
// Free customer PATCHes public_auth_mode='bearer' (issue #477 /
// ADR-079). The plan DOES have a tier — 'open' — that the customer
// can already use; the upgrade copy is "bearer is a paid feature".
// 402 PaymentRequired mirrors the streaming / warm-snapshot /
// eviction-priority / cron / alert-rules / webhooks gate family so
// the dashboard renders the same "upgrade to X" CTA. Distinct code
// from ErrPlanPublicAuthBasicNotAllowed below so the CLI + telemetry
// can pivot on the mode without parsing the message body.
func ErrPlanPublicAuthBearerNotAllowed(p Plan) *Problem {
	return NewProblem(http.StatusPaymentRequired, CodePlanPublicAuthBearerNotAllowed,
		"Bearer public-URL auth is not available on this plan",
		fmt.Sprintf("the %s plan does not include bearer-mode public auth; upgrade to Hobby or above to opt in. The 'open' mode is available on every plan.", p)).
		WithDocs(docsBase + "/plans#public-auth")
}

// ErrPlanPublicAuthBasicNotAllowed is the 402 apid returns when a
// Free/Hobby customer PATCHes public_auth_mode='basic' (issue #477 /
// ADR-079). The plan DOES have tiers — 'open' (every plan) and
// 'bearer' (Hobby+) — that the customer can already use; the upgrade
// copy is "basic is Pro+". 402 PaymentRequired mirrors the existing
// 402 gate family. Distinct code from
// ErrPlanPublicAuthBearerNotAllowed above so the CLI + telemetry can
// pivot on the mode.
func ErrPlanPublicAuthBasicNotAllowed(p Plan) *Problem {
	return NewProblem(http.StatusPaymentRequired, CodePlanPublicAuthBasicNotAllowed,
		"Basic public-URL auth is not available on this plan",
		fmt.Sprintf("the %s plan does not include basic-mode public auth; upgrade to Pro or above to opt in. The 'open' and 'bearer' modes are available on lower plans.", p)).
		WithDocs(docsBase + "/plans#public-auth")
}

// ErrPlanEvictionPriorityReservedQuota is the 422 apid returns when
// the per-account ReservedConcurrencyPerAccount cap is exhausted
// (issue #475). The customer must flip an existing reserved app to
// best_effort first; the count is over APPS (not instances) per the
// user-confirmed contract — single reserved app with 5 concurrent
// instances counts as 1 against the cap. 422 (not 403) because the
// plan DOES unlock the reserved tier — the right copy is "flip an
// existing reserved app to best_effort", not "upgrade".
func ErrPlanEvictionPriorityReservedQuota(p Plan, observed, limit int) *Problem {
	return NewProblem(http.StatusUnprocessableEntity, CodePlanEvictionPriorityReservedQuota,
		"Reserved eviction priority quota reached",
		fmt.Sprintf("%s plan caps the reserved eviction tier at %d app(s) per account; you have %d. Flip an existing reserved app to best_effort to add another.",
			p, limit, observed)).
		WithLimit(int64(limit), int64(observed)).
		WithDocs(docsBase + "/plans#eviction-priority")
}

// ErrAlertRuleInvalid is returned for malformed alert-rule bodies:
// closed-set metric/comparison/window_spec/failure_source drift,
// non-finite threshold, cooldown band breach, oversized webhook
// secret, or a metric-family swap that the xor_chk constraint
// would reject at the DB. Mirrors ErrCronInvalid's shape so the
// CLI can use one problem-code table for both surfaces.
func ErrAlertRuleInvalid(reason string) *Problem {
	return NewProblem(http.StatusBadRequest, CodeAlertRuleInvalid,
		"Invalid alert rule", reason).
		WithDocs(docsBase + "/alerts")
}

// ErrPlanAlertRulesNotAllowed is returned by apid's createAlertRule
// / listAlertRules handlers when the customer's plan has
// AlertRuleLimitPerApp == 0 (Free today). Fires BEFORE loadApp so a
// Free customer posting to a non-existent slug gets a clean 402
// instead of a 404 that would leak the slug's existence (PR review
// finding F4). Mirrors ErrPlanCronsNotAllowed.
func ErrPlanAlertRulesNotAllowed(p Plan) *Problem {
	return NewProblem(http.StatusPaymentRequired, CodePlanAlertRulesNotAllowed,
		"Alert rules unavailable on this plan",
		fmt.Sprintf("the %s plan does not include alert rules; upgrade to Hobby or above to fire alerts.", p)).
		WithDocs(docsBase + "/plans#alerts")
}

// ErrPlanAlertRuleQuota is returned when
// CreateAlertRuleIfUnderQuota surfaces a *state.AlertRuleQuotaError.
// Scope "app" or "account" tells the handler which cap fired so
// the body can name it. 403 (not 402) because the plan DOES unlock
// alert rules — the right copy is "delete a rule to add another",
// not "upgrade to Hobby". Mirrors ErrPlanCronQuota.
func ErrPlanAlertRuleQuota(plan Plan, scope string, limit, observed int) *Problem {
	scopeName := PlanQuotaScopeDisplayName(scope)
	return NewProblem(http.StatusForbidden, CodePlanAlertRuleQuota,
		"Alert rule limit reached",
		fmt.Sprintf("%s plan caps alert rules at %d for %s; you have %d. Delete one to add another.",
			plan, limit, scopeName, observed)).
		WithLimit(int64(limit), int64(observed)).
		WithDocs(docsBase + "/plans#alerts")
}

// ErrAlertPresetInvalid is the closed-set + shape error for
// enable-from-preset requests (issue #1233, ADR-123). Mirrors
// ErrAlertRuleInvalid's shape so the CLI's problem-code table is
// one row deep.
func ErrAlertPresetInvalid(reason string) *Problem {
	return NewProblem(http.StatusBadRequest, CodeAlertPresetInvalid,
		"Invalid alert preset", reason).
		WithDocs(docsBase + "/alerts/presets")
}

// ErrAlertPresetDisabled is returned when the customer POSTs to
// enable a catalog row whose enabled_in_catalog=false. The 8
// catalog rows ship 5 disabled in PR-A (the same 5 the dashboard
// renders with a "coming soon" badge); the 402 is the API-side
// mirror of that UX so a CLI caller gets the same hint.
func ErrAlertPresetDisabled(presetName string) *Problem {
	return NewProblem(http.StatusBadRequest, CodeAlertPresetDisabled,
		"Alert preset not yet available",
		fmt.Sprintf("the %q preset is staged for a future release; check the catalog for available presets.", presetName)).
		WithDocs(docsBase + "/alerts/presets")
}

// ErrPlanAlertPresetsNotAllowed is returned when the customer's
// plan is below the preset's minimum_plan (e.g. Hobby trying
// api_down whose floor is Pro). 402 mirrors ErrPlanAlertRulesNotAllowed
// so the slug-leak guard pattern is one row deep.
func ErrPlanAlertPresetsNotAllowed(plan Plan, presetName, minimumPlan string) *Problem {
	return NewProblem(http.StatusPaymentRequired, CodePlanAlertPresetsNotAllowed,
		"Alert preset not available on this plan",
		fmt.Sprintf("the %q preset requires the %s plan or higher; the %s plan does not include it.",
			presetName, minimumPlan, plan)).
		WithDocs(docsBase + "/plans#alerts")
}

// ErrPlanWebhooksNotAllowed is returned by apid's createAppWebhook
// / listAppWebhooks handlers when the customer's plan has
// WebhookPerApp == 0 (Free today). Fires BEFORE loadApp so a Free
// customer posting to a non-existent slug gets a clean 402 instead
// of a 404 (and the reverse — a Free customer on a real slug gets
// 402, not a 404 masquerading as plan-gating). PR review finding F4
// mirrored from createAlertRule.
func ErrPlanWebhooksNotAllowed(p Plan) *Problem {
	return NewProblem(http.StatusPaymentRequired, CodePlanWebhooksNotAllowed,
		"Outbound webhooks unavailable on this plan",
		fmt.Sprintf("the %s plan does not include outbound webhooks; upgrade to Hobby or above to subscribe.", p)).
		WithDocs(docsBase + "/plans#webhooks")
}

// ErrPlanWebhookQuota is returned when
// CreateAppWebhookIfUnderQuota surfaces a *state.AppWebhookQuotaError.
// Scope "app" or "account" tells the handler which cap fired so the
// body can name it. 403 (not 402) because the plan DOES unlock
// webhooks — the right copy is "delete a webhook to add another",
// not "upgrade to Hobby". Mirrors ErrPlanAlertRuleQuota.
func ErrPlanWebhookQuota(plan Plan, scope string, limit, observed int) *Problem {
	scopeName := PlanQuotaScopeDisplayName(scope)
	return NewProblem(http.StatusForbidden, CodePlanWebhookQuota,
		"Webhook limit reached",
		fmt.Sprintf("%s plan caps webhooks at %d for %s; you have %d. Delete one to add another.",
			plan, limit, scopeName, observed)).
		WithLimit(int64(limit), int64(observed)).
		WithDocs(docsBase + "/plans#webhooks")
}

// ErrPlanTriggersNotAllowed is returned by apid's createTrigger /
// listTriggers handlers when the customer's plan has
// TriggerLimitPerApp == 0 (Free today, issue #757 / ADR-0NN).
// Fires BEFORE loadApp so a Free customer posting to a non-existent
// slug gets a clean 402 instead of a 404 (and the reverse — a
// Free customer on a real slug gets 402, not a 404 masquerading as
// plan-gating). PR review finding F4 mirrored from createAlertRule
// and createAppWebhook.
func ErrPlanTriggersNotAllowed(p Plan) *Problem {
	return NewProblem(http.StatusPaymentRequired, CodePlanTriggersNotAllowed,
		"Triggers unavailable on this plan",
		fmt.Sprintf("the %s plan does not include event-source mappings (Kafka, NATS, Redis Streams, SQS-compatible, in-platform queue); upgrade to Hobby or above to subscribe.", p)).
		WithDocs(docsBase + "/plans#triggers")
}

// ErrPlanTriggerQuota is returned when CreateTriggerIfUnderQuota
// surfaces a *state.TriggerQuotaError. Scope "app" or "account"
// tells the handler which cap fired so the body can name it. 403
// (not 402) because the plan DOES unlock triggers — the right
// copy is "delete a trigger to add another", not "upgrade to
// Hobby". Mirrors ErrPlanCronQuota / ErrPlanWebhookQuota.
func ErrPlanTriggerQuota(plan Plan, scope string, limit, observed int) *Problem {
	scopeName := PlanQuotaScopeDisplayName(scope)
	return NewProblem(http.StatusForbidden, CodePlanTriggerQuota,
		"Trigger limit reached",
		fmt.Sprintf("%s plan caps triggers at %d for %s; you have %d. Delete one to add another.",
			plan, limit, scopeName, observed)).
		WithLimit(int64(limit), int64(observed)).
		WithDocs(docsBase + "/plans#triggers")
}

// ErrTriggerBatchWindowTooLarge is returned by createTrigger /
// updateTrigger when batch_window_ms / 1000 exceeds the plan cap
// (limits.TriggerBatchWindowMaxSec). Mirror of ErrPlanTriggerQuota
// for a different limit field. Added for PR #993 / issue #757
// review MED-4.
func ErrTriggerBatchWindowTooLarge(plan Plan, limitSec, observedSec int) *Problem {
	return NewProblem(http.StatusForbidden, CodeTriggerBatchWindowTooLarge,
		"batch_window too large for this plan",
		fmt.Sprintf("%s plan caps trigger batch_window at %d s; you requested %d s. Lower the value or upgrade to Scale.",
			plan, limitSec, observedSec)).
		WithLimit(int64(limitSec), int64(observedSec)).
		WithDocs(docsBase + "/plans#triggers")
}

// ErrTriggerTLSSkipVerifyNotAllowed is returned by createTrigger /
// updateTrigger when the Kafka trigger config carries
// tls.skip_verify=true but the account's plan does not have
// TLSSkipVerifyAllowed (Free + Hobby today). 403 because the plan
// supports triggers, it just doesn't ship the certificate-skip
// knob — the operator decision the customer has to revisit. Added
// for PR #993 / issue #757 review MED-4.
func ErrTriggerTLSSkipVerifyNotAllowed(plan Plan) *Problem {
	return NewProblem(http.StatusForbidden, CodeTriggerTLSSkipVerifyNotAllowed,
		"tls.skip_verify unavailable on this plan",
		fmt.Sprintf("the %s plan does not allow tls.skip_verify on a trigger (the certificate-skip knob is reserved for plans that have signed off on the operational risk). Drop tls.skip_verify or upgrade to Pro.", plan)).
		WithDocs(docsBase + "/plans#triggers")
}

// ErrAppWebhookInvalid is returned for malformed webhook bodies:
// missing target_url, invalid retry_policy, out-of-vocabulary event,
// oversize webhook_secret, etc. Mirrors ErrAlertRuleInvalid.
func ErrAppWebhookInvalid(reason string) *Problem {
	return NewProblem(http.StatusBadRequest, CodeAppWebhookInvalid,
		"Invalid webhook", reason)
}

// ErrTenantSurfacesNotAllowed is returned by apid's createTenantSurface
// handler when the account's plan does not enable surfaces (Free today,
// ADR-100 / issue #879). Fires BEFORE the store is touched so a Free
// customer gets a clean 402 instead of a quota round-trip. Mirrors the
// ErrPlanCronsNotAllowed shape.
func ErrTenantSurfacesNotAllowed(p Plan) *Problem {
	return NewProblem(http.StatusPaymentRequired, CodeTenantSurfacesNotAllowed,
		"Tenant surfaces unavailable on this plan",
		fmt.Sprintf("the %s plan does not include tenant surfaces; upgrade to Hobby or above to expose one app to many customer hostnames under a single cert.", p)).
		WithDocs(docsBase + "/plans#tenant-surfaces")
}

// ErrStaticEgressIPNotEnabled is the dark-launch 402 — the
// cluster has the schema + handlers wired (ADR-119) but the
// operator has not flipped FAAS_STATIC_EGRESS_IP_ENABLED. The
// check runs before loadApp so the surface is invisible until
// the flag is set. Mirrors the ErrTenantSurfacesNotAllowed
// shape (same env-flag pattern in pkg/api/flags.go).
func ErrStaticEgressIPNotEnabled() *Problem {
	return NewProblem(http.StatusPaymentRequired, CodeStaticEgressIPNotEnabled,
		"Static egress IP feature is not enabled on this cluster",
		"the FAAS_STATIC_EGRESS_IP_ENABLED env var is not set; ask the cluster operator to enable the static egress IP surface.").
		WithDocs(docsBase + "/static-egress-ip")
}

// ErrTenantSurfaceQuota is returned when
// CreateTenantSurfaceIfUnderQuota surfaces a *state.TenantSurfaceQuotaError.
// 403 (not 402) because the plan DOES unlock surfaces — the right copy
// is "delete a surface to add another", not "upgrade to Hobby". The
// Problem's Limit/Observed carry the cap values so the dashboard can
// render the count.
func ErrTenantSurfaceQuota(p Plan, limit, observed int) *Problem {
	return NewProblem(http.StatusForbidden, CodeTenantSurfaceQuota,
		"Tenant surface limit reached",
		fmt.Sprintf("%s plan caps tenant surfaces at %d per account; you have %d. Delete one to add another.",
			p, limit, observed)).
		WithLimit(int64(limit), int64(observed)).
		WithDocs(docsBase + "/plans#tenant-surfaces")
}

// ErrTenantHostnameQuota is returned when
// CreateTenantHostnameIfUnderQuota surfaces a *state.TenantHostnameQuotaError.
// The Problem's Limit/Observed/SurfaceID together name the overflowing
// surface in the support ticket. 403 mirrors the sibling per-scope
// quota factories.
func ErrTenantHostnameQuota(p Plan, surfaceID string, limit, observed int) *Problem {
	return NewProblem(http.StatusForbidden, CodeTenantHostnameQuota,
		"Tenant hostname limit reached",
		fmt.Sprintf("%s plan caps tenant hostnames at %d per surface; you have %d on surface %s. Remove one to add another.",
			p, limit, observed, surfaceID)).
		WithLimit(int64(limit), int64(observed)).
		WithDocs(docsBase + "/plans#tenant-surfaces")
}

// ErrTenantHostnameAlreadyClaimed is returned when the global UQ on
// tenant_hostnames.hostname trips (the same hostname is already
// attached to another surface on any account). 409 surfaces the
// ownership invariant — the customer must pick a unique hostname.
func ErrTenantHostnameAlreadyClaimed(hostname string) *Problem {
	return NewProblem(http.StatusConflict, CodeTenantHostnameAlreadyClaimed,
		"Tenant hostname already claimed",
		fmt.Sprintf("hostname %q is already attached to another tenant surface; pick a unique hostname.", hostname))
}

// ErrTenantSurfaceCertKindInvalid is returned when the apid validator
// rejects an unsupported cert_kind at create / update time. The
// schema accepts (per_host_san, shared_wildcard) for forward
// compatibility, but the issuer rejects shared_wildcard today (the
// customer-zone DNS-01 solver ships in a follow-up ADR per ADR-100
// D4). 400 mirrors the other *Invalid code family.
func ErrTenantSurfaceCertKindInvalid(kind string) *Problem {
	return NewProblem(http.StatusBadRequest, CodeTenantSurfaceCertKindInvalid,
		"Unsupported cert kind",
		fmt.Sprintf("cert kind %q is not supported in v1; use per_host_san.", kind))
}

// ErrPlanDataUpstreamsNotAllowed (ADR-098 §D5) is the 402 returned
// by apid's createUpstream handler when the customer's plan has
// DataPlacementHintsPerApp == 0 (Free today). Mirrors
// ErrPlanWebhooksNotAllowed at line 1883: fires BEFORE loadApp so a
// Free customer posting to a non-existent slug gets a clean 402
// instead of a 404. The 402 (PaymentRequired) shape signals
// "upgrade-required" rather than "forbidden".
func ErrPlanDataUpstreamsNotAllowed(p Plan) *Problem {
	return NewProblem(http.StatusPaymentRequired, CodePlanDataUpstreamsNotAllowed,
		"Data-placement hints unavailable on this plan",
		fmt.Sprintf("the %s plan does not include data-placement hints; upgrade to Hobby or above to capture upstreams.", p)).
		WithDocs(docsBase + "/plans#data-placement")
}

// ErrPlanLimitDataUpstreams (ADR-098 §D5) is the 403 returned when
// CreateDataUpstreamIfUnderQuota surfaces a state-layer quota
// error. Mirrors ErrPlanWebhookQuota at line 1900 — the plan DOES
// unlock the surface, the right copy is "delete one to add another",
// not "upgrade". The handler renders the (limit, observed) pair via
// WithLimit so the dashboard can show "3/3 — at the cap".
func ErrPlanLimitDataUpstreams(plan Plan, limit, observed int) *Problem {
	return NewProblem(http.StatusForbidden, CodePlanLimitDataUpstreams,
		"Data-upstream limit reached",
		fmt.Sprintf("%s plan caps data upstreams at %d per app; you have %d. Delete one to add another.",
			plan, limit, observed)).
		WithLimit(int64(limit), int64(observed)).
		WithDocs(docsBase + "/plans#data-placement")
}

// ErrUpstreamInvalidKind (ADR-098 §D4) is the 400 returned when
// the customer's PUT body names a kind that's not in the 14-value
// closed vocabulary (postgres, redis, mongo, ...). Distinct from
// CodeEnvVarInvalidKey because the surface is a typed DTO, not a
// free-form string. The handler reads DataUpstreamKindIsValid
// (PR-B) to surface this code.
func ErrUpstreamInvalidKind(reason string) *Problem {
	return NewProblem(http.StatusBadRequest, CodeUpstreamInvalidKind,
		"Invalid data-upstream kind", reason)
}

// ErrUpstreamInvalidHost (ADR-098 §D4) is the 400 returned when
// the host fails the RFC-952/1123 regex (with the IPv4 backstop
// that PR-A added at migration 00226's
// `data_upstreams_host_check` CHECK constraint). Mirrors
// CodeInvalidRegistryHost at line 535.
func ErrUpstreamInvalidHost(reason string) *Problem {
	return NewProblem(http.StatusBadRequest, CodeUpstreamInvalidHost,
		"Invalid data-upstream host", reason)
}

// ErrUpstreamInvalidPort (ADR-098 §D4) is the 400 returned when
// the port is outside [1, 65535] (matching the migration CHECK).
// Distinct from CodeUpstreamInvalidHost so a customer with a
// valid host + invalid port gets a precise error code rather than
// a generic "invalid host".
func ErrUpstreamInvalidPort(reason string) *Problem {
	return NewProblem(http.StatusBadRequest, CodeUpstreamInvalidPort,
		"Invalid data-upstream port", reason)
}

// ErrUpstreamNotFound (ADR-098 §D4) is the 404 returned when a
// DELETE or GET targets an upstream_id that doesn't exist on this
// app. Mirrors CodeRegistryCredentialNotFound at line 568 — the
// 404 vs 400 distinction is what lets the SDK distinguish "delete
// a row that's already gone" (idempotent) from "fix the URL".
func ErrUpstreamNotFound(upstreamID string) *Problem {
	return NewProblem(http.StatusNotFound, CodeUpstreamNotFound,
		"Data upstream not found",
		fmt.Sprintf("no data upstream with id %q on this app.", upstreamID))
}

// ErrHandlerMissing is returned when a function source upload doesn't
// include a handler (spec §4.9).
func ErrHandlerMissing() *Problem {
	return NewProblem(http.StatusBadRequest, CodeHandlerMissing,
		"Handler required",
		"function deploys require a handler path (e.g. handler.handler)").
		WithDocs(docsBase + "/functions")
}

// ErrDeployFailed wraps a deployment failure message into a Problem so the
// CLI can render it uniformly with quota errors.
func ErrDeployFailed(detail string) *Problem {
	return NewProblem(http.StatusUnprocessableEntity, CodeDeployFailed,
		"Deploy failed", detail).
		WithDocs(docsBase + "/deploys")
}

// ErrDeploySignatureInvalid is returned by apid when an OCI image
// deploy is rejected at the accept-time signature-enforcement gate
// (issue #472 / ADR-054). Detail carries the human-readable reason
// (one of: "no signature", "signature by untrusted publisher", or
// "no trusted publishers configured"). The customer sees this code
// only when apps.require_signed=true; imaged surfaces the deeper
// failure_reason (signature_missing / signature_invalid) on
// deployments.error_code and via the audit events.
func ErrDeploySignatureInvalid(detail string) *Problem {
	return NewProblem(http.StatusForbidden, CodeDeploySignatureInvalid,
		"Signed-image enforcement rejected the deploy", detail).
		WithDocs(docsBase + "/deploys#signed-images")
}

// ErrTrustedSignerInvalid is the 400 mirror of ErrSecretInvalidKey
// for the PUT /v1/apps/{slug}/trusted_signers/{name} body. Detail
// carries the shape failure ("public_key_pem must be 64..1024 bytes
// after base64-decode", etc.).
func ErrTrustedSignerInvalid(detail string) *Problem {
	return NewProblem(http.StatusBadRequest, CodeTrustedSignerInvalid,
		"Trusted signer invalid", detail).
		WithDocs(docsBase + "/deploys#trusted-signers")
}

// ErrTrustedSignerNotFound is the 404 mirror of ErrSecretNotFound
// for the DELETE path. The URL resource IS the signer name, so a
// missing row is rendered as 400 in the legacy secret/env shape —
// issue #472 deliberately uses 404 to make the resource model
// explicit on the new surface.
func ErrTrustedSignerNotFound(signer string) *Problem {
	return NewProblem(http.StatusNotFound, CodeTrustedSignerNotFound,
		"Trusted signer not found",
		"no trusted signer named "+signer+" on this app")
}

// ErrNoRollbackTarget is returned by POST /v1/apps/{slug}/rollback when no
// superseded deployment exists (spec §9 line 376).
func ErrNoRollbackTarget() *Problem {
	return NewProblem(http.StatusConflict, CodeNoRollbackTarget,
		"No previous deployment",
		"there's no superseded deployment to roll back to; deploy at least twice.").
		WithDocs(docsBase + "/deploys#rollback")
}

// ErrRollbackTargetNotFound is returned by POST /v1/apps/{slug}/rollback when
// the caller passes an explicit target_deployment_id (SAFE-RELEASES-G) that
// does not match any deployment of this app, or does not exist. The detail
// names the bad id so the CLI can echo it back. 404 (not 409) because the
// resource the caller asked for genuinely doesn't exist — distinct from
// CodeNoRollbackTarget (409: "no superseded deployment exists at all").
func ErrRollbackTargetNotFound(detail string) *Problem {
	return NewProblem(http.StatusNotFound, CodeRollbackTargetNotFound,
		"Rollback target not found",
		detail).
		WithDocs(docsBase + "/deploys#rollback")
}

// ErrRollbackTargetAlreadyLive is returned when the caller passes an
// explicit target_deployment_id that exists but has status != 'superseded'
// (most commonly status='live'). Caller asked to "rollback" to the
// already-current deployment. Rejected explicitly rather than silently
// no-op'd per the SAFE-RELEASES-G plan. 409 because the request is
// well-formed but cannot proceed in current state.
func ErrRollbackTargetAlreadyLive(detail string) *Problem {
	return NewProblem(http.StatusConflict, CodeRollbackTargetAlreadyLive,
		"Rollback target is already live",
		detail).
		WithDocs(docsBase + "/deploys#rollback")
}

// ErrPlanLimitSecrets is returned when a secret PUT would exceed the plan's
// per-app secret count (spec §11/G2). Observed is the post-write count.
func ErrPlanLimitSecrets(l Limits, observed int) *Problem {
	return NewProblem(http.StatusForbidden, CodePlanLimitSecrets,
		"Secret count limit reached",
		fmt.Sprintf("%s plan allows %d secret(s) per app; you have %d.", l.Plan, l.SecretCountMax, observed)).
		WithLimit(int64(l.SecretCountMax), int64(observed)).
		WithDocs(docsBase + "/secrets#limits")
}

// ErrSecretInvalidKey is returned when a secret key fails the
// ^[A-Z][A-Z0-9_]*$ pattern. Detail names the specific failure so the CLI can
// render an actionable message.
func ErrSecretInvalidKey(detail string) *Problem {
	return NewProblem(http.StatusBadRequest, CodeSecretInvalidKey,
		"Invalid secret key",
		fmt.Sprintf("secret keys must match %s; %s", SecretKeyPattern, detail)).
		WithDocs(docsBase + "/secrets#keys")
}

// ErrSecretValueTooLarge is returned when a PUT value exceeds
// Limits.SecretValueMaxBytes. apid checks the byte length of the request body
// BEFORE sealing so the cap is enforced on the wire (no over-quota ciphertext
// ever lands in PG).
func ErrSecretValueTooLarge(l Limits, observedBytes int) *Problem {
	return NewProblem(http.StatusRequestEntityTooLarge, CodeSecretValueTooLarge,
		"Secret value too large",
		fmt.Sprintf("%s plan caps secret values at %d bytes; got %d.", l.Plan, l.SecretValueMaxBytes, observedBytes)).
		WithLimit(int64(l.SecretValueMaxBytes), int64(observedBytes)).
		WithDocs(docsBase + "/secrets#limits")
}

// ErrSecretNotFound is returned by DELETE /v1/apps/{slug}/secrets/{key} when
// the key isn't set on the app. Distinct from CodeNotFound because the URL
// shape (the resource IS the secret) is intentional.
func ErrSecretNotFound(key string) *Problem {
	return NewProblem(http.StatusBadRequest, CodeSecretNotFound,
		"Secret not set",
		fmt.Sprintf("no secret named %q on this app.", key)).
		WithDocs(docsBase + "/secrets")
}

// ErrPlanLimitEnvVars is returned when an env PUT would exceed the plan's
// per-app env-var count (issue #395 / ADR-045). Observed is the post-write
// count. The 403 mirrors ErrPlanLimitSecrets so the SDK's error decoder can
// share the quota-reached branch.
func ErrPlanLimitEnvVars(l Limits, observed int) *Problem {
	return NewProblem(http.StatusForbidden, CodePlanLimitEnvVars,
		"Env var count limit reached",
		fmt.Sprintf("%s plan allows %d env var(s) per app; you have %d.", l.Plan, l.EnvVarsMax, observed)).
		WithLimit(int64(l.EnvVarsMax), int64(observed)).
		WithDocs(docsBase + "/env#limits")
}

// ErrAPIKeyExpired is returned by the auth middleware when the
// bearer key's expires_at is in the past (issue #189 / IAM-5).
// The store has lazily marked the key revoked before the middleware
// sees the error; the middleware emits the key.expired audit event.
// 401 mirrors the legacy "invalid token" surface — the customer's
// SDK sees an auth failure and re-auths, the dashboard surfaces
// "key expired, rotate it" copy.
func ErrAPIKeyExpired() *Problem {
	return NewProblem(http.StatusUnauthorized, CodeAPIKeyExpired,
		"API key expired",
		"the bearer key has expired; rotate it and use the new plaintext").
		WithDocs(docsBase + "/auth#expiry")
}

// ErrAPIKeyRevoked is returned by the auth middleware when the
// bearer key's status is 'revoked' (issue #189 / IAM-5). The
// revocation may have been manual (DELETE), atomic rotation
// (grace_window_days=0), or lazy-expiry (the auth path observed
// expires_at < now). 401 mirrors the legacy "invalid token" surface.
func ErrAPIKeyRevoked() *Problem {
	return NewProblem(http.StatusUnauthorized, CodeAPIKeyRevoked,
		"API key revoked",
		"the bearer key has been revoked; mint a new one via POST /v1/keys").
		WithDocs(docsBase + "/auth#revocation")
}

// ErrAPIKeyLimitExceeded is returned when a POST /v1/keys would
// push the account over Plan.KeysMax (issue #189 / IAM-5). Rotated
// keys (status='revoked') are excluded from the count so the
// customer's history can grow without bound; revoking a key is the
// path to free up a slot. 409 mirrors the alert-rule quota shape
// (issue #396) — the operation is well-formed, the cap is the gate.
func ErrAPIKeyLimitExceeded(l Limits, observed int) *Problem {
	return NewProblem(http.StatusConflict, CodeAPIKeyLimitExceeded,
		"API key limit reached",
		fmt.Sprintf("%s plan allows %d API key(s) per account; you have %d active or in grace. Revoke a key before minting a new one.",
			l.Plan, l.KeysMax, observed)).
		WithLimit(int64(l.KeysMax), int64(observed)).
		WithDocs(docsBase + "/auth#quotas")
}

// ErrPlanLimitTrustedSigners is returned when a trusted-signer PUT
// would exceed the plan's per-app count (issue #472 / ADR-054).
// Observed is the post-write count. The 403 mirrors ErrPlanLimitEnvVars
// so the SDK's quota-reached branch decodes this code without
// hand-rolling a new switch arm. Distinct `code` keeps the dashboard
// row count for "trusted publishers" separate from "env vars".
func ErrPlanLimitTrustedSigners(l Limits, observed int) *Problem {
	return NewProblem(http.StatusForbidden, CodePlanLimitTrustedSigners,
		"Trusted signer count limit reached",
		fmt.Sprintf("%s plan allows %d trusted signer(s) per app; you have %d.", l.Plan, l.TrustedSignerCountMax, observed)).
		WithLimit(int64(l.TrustedSignerCountMax), int64(observed)).
		WithDocs(docsBase + "/deploys#trusted-signers")
}

// ErrEnvVarInvalidKey is returned when an env key fails the
// ^[A-Z][A-Z0-9_]*$ pattern. Detail names the specific failure so the CLI
// can render an actionable message. The regex intentionally reuses the
// SecretKeyPattern constant because POSIX env-var naming and the secrets
// naming surface share the same ASCII identifier grammar — keeping one
// pattern avoids the drift where two regexes diverge over time.
func ErrEnvVarInvalidKey(detail string) *Problem {
	return NewProblem(http.StatusBadRequest, CodeEnvVarInvalidKey,
		"Invalid env var key",
		fmt.Sprintf("env var keys must match %s; %s", SecretKeyPattern, detail)).
		WithDocs(docsBase + "/env#keys")
}

// ErrEnvVarValueTooLarge is returned when a PUT value exceeds
// Limits.EnvValueMaxBytes. The byte length is checked against the request
// body BEFORE the row hits PG so the cap is enforced on the wire (no
// over-quota value ever lands in app_envs).
func ErrEnvVarValueTooLarge(l Limits, observedBytes int) *Problem {
	return NewProblem(http.StatusRequestEntityTooLarge, CodeEnvVarValueTooLarge,
		"Env var value too large",
		fmt.Sprintf("%s plan caps env values at %d bytes; got %d.", l.Plan, l.EnvValueMaxBytes, observedBytes)).
		WithLimit(int64(l.EnvValueMaxBytes), int64(observedBytes)).
		WithDocs(docsBase + "/env#limits")
}

// ErrEnvVarNotFound is returned by DELETE /v1/apps/{slug}/env/{key} when
// the key isn't set on the app. Distinct from CodeNotFound for the same
// reason as ErrSecretNotFound: the URL shape makes the resource the env
// var, not the app.
func ErrEnvVarNotFound(key string) *Problem {
	return NewProblem(http.StatusBadRequest, CodeEnvVarNotFound,
		"Env var not set",
		fmt.Sprintf("no env var named %q on this app.", key)).
		WithDocs(docsBase + "/env")
}

// ErrEnvScopeInvalid is returned when the scope query param or
// --scope flag fails the EnvScopePattern check (empty, too long, or
// out-of-shape). 400, code env_scope_invalid. Mirrors ErrEnvVarInvalidKey
// on the 400 status + detail shape so the SDK's existing
// `isInvalidKey()` switch arm decodes this without a new branch.
//
// Reserved-sentinel collisions (e.g. the literal "__all__") get the
// dedicated ErrEnvScopeReserved code instead — see that helper's
// comment for why a separate code is worth the one-line SDK switch.
func ErrEnvScopeInvalid(detail string) *Problem {
	return NewProblem(http.StatusBadRequest, CodeEnvScopeInvalid,
		"Invalid env scope",
		fmt.Sprintf("env scope must match %s; %s", EnvScopePattern, detail)).
		WithDocs(docsBase + "/env#scopes")
}

// ErrEnvScopeReserved is returned when the scope query param is the
// reserved sentinel "__all__" on a WRITE path (PUT/DELETE). The
// sentinel is read-only — it triggers the nested `env_by_scope`
// response shape on GET (ADR-090 D3) and MUST NOT be set as a scope
// name. 400, code env_scope_reserved. Detail names the literal
// sentinel so the CLI can render "you used the all-scopes sentinel
// on a write — drop the ?scope= flag" without a separate API call.
func ErrEnvScopeReserved(sentinel string) *Problem {
	return NewProblem(http.StatusBadRequest, CodeEnvScopeReserved,
		"Env scope reserved",
		fmt.Sprintf("scope %q is reserved for the read path; omit ?scope= on writes.", sentinel)).
		WithDocs(docsBase + "/env#scopes")
}

// ErrPlanRegistryCredentialsNotAllowed is returned when the customer's
// plan has RegistryCredentialMax == 0 (Free today, issue #461 /
// ADR-062). The 403 fires BEFORE the store is touched so a Free
// customer gets a clean upsell signal — not a quota hint. The plan
// truly doesn't unlock the surface, so the only path forward is a
// plan upgrade.
func ErrPlanRegistryCredentialsNotAllowed(p Plan) *Problem {
	return NewProblem(http.StatusForbidden, CodePlanRegistryCredentialNotAllowed,
		"Plan doesn't allow private-registry credentials",
		fmt.Sprintf("the %s plan cannot store private-registry credentials; upgrade to Hobby or higher.", p)).
		WithDocs(docsBase + "/registry-credentials")
}

// ErrPlanRegistryCredentialQuota is returned when the customer's plan
// unlocks the surface but the per-app cap was reached. Distinct from
// ErrPlanRegistryCredentialsNotAllowed so the CLI can branch on
// upsell-vs-delete copy without parsing the body. The detail exposes
// the cap + current count so a customer knows which (app, host) pair
// to drop.
func ErrPlanRegistryCredentialQuota(l Limits, observed int) *Problem {
	return NewProblem(http.StatusRequestEntityTooLarge, CodePlanRegistryCredentialQuota,
		"Per-app registry credential quota reached",
		fmt.Sprintf("the %s plan caps private-registry credentials at %d per app; got %d. Delete one before adding another.", l.Plan, l.RegistryCredentialMax, observed)).
		WithLimit(int64(l.RegistryCredentialMax), int64(observed)).
		WithDocs(docsBase + "/registry-credentials#quota")
}

// ErrInvalidRegistryHost is returned when the request body's registry
// field fails the normalized-host gate (lowercase DNS[:port], no
// scheme/path). Wrapping `detail` keeps the specific failure visible
// to the CLI without leaking the input verbatim into a 5xx.
func ErrInvalidRegistryHost(detail error) *Problem {
	return NewProblem(http.StatusBadRequest, CodeInvalidRegistryHost,
		"Invalid registry host",
		detail.Error()).
		WithDocs(docsBase + "/registry-credentials#registry-format")
}

// ErrRegistryCredentialNotFound is returned by DELETE
// /v1/apps/{slug}/registry-credentials?registry=... when no row exists
// for the (app, host). Distinct from CodeNotFound because the URL
// resource is the registry host, not the app.
func ErrRegistryCredentialNotFound(host string) *Problem {
	return NewProblem(http.StatusBadRequest, CodeRegistryCredentialNotFound,
		"Registry credential not set",
		fmt.Sprintf("no credential stored for registry %q on this app.", host)).
		WithDocs(docsBase + "/registry-credentials")
}

// ErrPlanMinInstancesNotAllowed is returned when a Free account tries
// to set apps.min_instances or deployments.min_instances (ux_spec §6.5,
// plan-tier gate). The customer's bill on Free is built around
// scale-to-zero; a floor keeps N × RAMMB resident at all times, which
// is the cost shape of Hobby / Pro / Scale. Hobby was promoted to
// the gate's "allowed" set by issue #462 / ADR-058 / PR-A — the
// tier-up is as far as the gate goes, so Free is the remaining
// locked tier.
func ErrPlanMinInstancesNotAllowed(p Plan) *Problem {
	return NewProblem(http.StatusForbidden, CodePlanMinInstancesNotAllowed,
		"Plan doesn't allow a min-instances floor",
		fmt.Sprintf("the %s plan always scales to zero; upgrade to Hobby, Pro, or Scale to keep instances warm.", p)).
		WithDocs(docsBase + "/plans#min-instances")
}

// ErrDeploymentCancelLiveForbidden (ADR-124) is returned when
// the caller tries to cancel a DeployLive deployment. 409 (not
// 403) because the operation is well-formed; the row's state
// forbids it. The fix-hint points at the existing rollback
// surface.
func ErrDeploymentCancelLiveForbidden(id string) *Problem {
	return NewProblem(http.StatusConflict, CodeDeploymentCancelLiveForbidden,
		"Cannot cancel a live deployment",
		fmt.Sprintf("deployment %s is live; cancelling a live deploy would orphan the §6.2 INV 3 'always-live-snapshot-OR-rootfs' guarantee.", id)).
		WithHint("Use 'gregale deploys rollback <id>' to swap to a previous deployment.").
		WithFix("Run: gregale deploys rollback --app <slug> --to <previous-deployment-id>").
		WithWhy("Cancelling a live deployment has no well-defined semantics: it would either scale the app to zero (kills §6.2 INV 4) or park the app (kills INV 3). The deploys-rollback path is the user-correct escape.").
		WithDocs(docsBase + "/deploys#cancel")
}

// ErrDeploymentCancelNotCancellable (ADR-124) is returned when
// the deployment's current status is in
// {failed, superseded, cancelled} — i.e. terminal and therefore
// not cancellable. 409 Conflict.
func ErrDeploymentCancelNotCancellable(id string) *Problem {
	return NewProblem(http.StatusConflict, CodeDeploymentCancelNotCancellable,
		"Deployment is not in a cancellable state",
		fmt.Sprintf("deployment %s is already terminal; cancel is only valid for pending, building, imaging, or snapshotting rows.", id)).
		WithDocs(docsBase + "/deploys#cancel")
}

// ErrDeploymentReorderNotPending (ADR-124) is returned when a
// reorder request lands on a row that has already left
// DeployPending. 409 Conflict — the operation is well-formed;
// the row's state forbids it.
func ErrDeploymentReorderNotPending(id string) *Problem {
	return NewProblem(http.StatusConflict, CodeDeploymentReorderNotPending,
		"Reorder only valid for pending deployments",
		fmt.Sprintf("deployment %s is no longer pending; reorder is a planning-queue operation and cannot affect a row that has been claimed by builderd.", id)).
		WithHint("Cancel this deployment and re-deploy with --priority if you need queue control.").
		WithDocs(docsBase + "/deploys#reorder")
}

// ErrDeploymentReorderPriorityInvalid (ADR-124) is the range
// backstop for priority. 422 Unprocessable Entity — the request
// shape is wrong.
func ErrDeploymentReorderPriorityInvalid(got int) *Problem {
	return NewProblem(http.StatusUnprocessableEntity, CodeDeploymentReorderPriorityInvalid,
		"priority must be in [0, 1000]",
		fmt.Sprintf("priority=%d; valid range is [0, 1000] (0 = deploy immediately, 100 = FIFO default, 1000 = background rebuild).", got)).
		WithLimit(1000, int64(got)).
		WithDocs(docsBase + "/deploys#reorder")
}

// ErrPlanReorderDisabled (ADR-124) is the plan-tier gate for
// reorder + deploy-immediately. 402 Payment Required — the
// operation is well-formed but the plan tier forbids it.
// Cancel + clear-obsolete are NOT gated by this check.
func ErrPlanReorderDisabled(p Plan) *Problem {
	return NewProblem(http.StatusPaymentRequired, CodePlanReorderDisabled,
		"Plan doesn't allow reorder or deploy-immediately",
		fmt.Sprintf("the %s plan locks the deploy queue (no reorder, no deploy-immediately); cancel and clear-obsolete remain available. Upgrade to Hobby, Pro, or Scale to unlock the queue controls.", p)).
		WithHint("Cancel + clear-obsolete remain available on Free; upgrade to unlock reorder/deploy-immediately.").
		WithDocs(docsBase + "/plans#queue-controls")
}

// ErrInvalidMinInstances is returned when the requested min_instances
// is negative or exceeds the plan's max concurrency. 422 (not 403)
// because the request shape is wrong, not the plan.
func ErrInvalidMinInstances(got, maxConcur int) *Problem {
	return NewProblem(http.StatusUnprocessableEntity, CodeInvalidMinInstances,
		"Invalid min_instances",
		fmt.Sprintf("min_instances must be in [0, %d] (plan max_concurrency); got %d.", maxConcur, got)).
		WithLimit(int64(maxConcur), int64(got)).
		WithDocs(docsBase + "/apps#min-instances")
}

// ErrMaxMinInstancesExceeded (issue #557 / ADR-071 §Decision 5) is
// returned when the requested min_instances exceeds the per-plan
// MaxMinInstances cap (Hobby 1, Pro 3, Scale 10). 422 with
// WithLimit carrying the cap and the observed value so the CLI
// can render actionable retry guidance ("raise your plan or lower
// --min-instances"). Distinct from ErrInvalidMinInstances which
// rejects negative values or values above MaxConcurrency.
func ErrMaxMinInstancesExceeded(got, planMax int) *Problem {
	return NewProblem(http.StatusUnprocessableEntity, CodeMaxMinInstancesExceeded,
		"min_instances exceeds plan cap",
		fmt.Sprintf("min_instances must be in [0, %d] for this plan; got %d.", planMax, got)).
		WithLimit(int64(planMax), int64(got)).
		WithDocs(docsBase + "/apps#min-instances")
}

// ErrPlanTrafficSplitNotAllowed (issue #556 / traffic splitting
// across deployments) is returned when a Free/Hobby account tries
// to set a non-default traffic_percent on a deployment (ux_spec
// §6.5 plan-tier gate family, joined by issue #556). The customer's
// bill on Free/Hobby is built around scale-to-zero; keeping two or
// more live deployments warm simultaneously is the cost shape of
// Pro / Scale (per-running-second RAM × N deployments). Hobby was
// deliberately NOT promoted to the gate's "allowed" set (unlike
// MinInstancesAllowed which Hobby unlocks per issue #462) — see
// the Limits.TrafficSplit field comment in pkg/api/limits.go.
func ErrPlanTrafficSplitNotAllowed(p Plan) *Problem {
	return NewProblem(http.StatusForbidden, CodePlanTrafficSplitNotAllowed,
		"Plan doesn't allow traffic splitting",
		fmt.Sprintf("the %s plan routes 100%% to the most recent deployment; upgrade to Pro or Scale to keep N canary deployments warm.", p)).
		WithDocs("https://docs.gregale.dev/plans#traffic-split")
}

// ErrInvalidTrafficPercent (issue #556) is returned when the
// requested traffic_percent is outside [0, 100] (the schema CHECK
// in migration 00160 is the second-line defence; this surfaces
// before that check, the API gate fires first). 422 mirrors the
// ErrInvalidMinInstances shape at line 1688 (plan-gate-first vs
// shape-second; this is shape, so 422 not 403).
func ErrInvalidTrafficPercent(got int) *Problem {
	const cap = 100
	return NewProblem(http.StatusUnprocessableEntity, CodeInvalidTrafficPercent,
		"Invalid traffic_percent",
		fmt.Sprintf("traffic_percent must be in [0, %d]; got %d.", cap, got)).
		WithLimit(int64(cap), int64(got)).
		WithDocs("https://docs.gregale.dev/deployments#traffic-percent")
}

// ErrInvalidCanaryPreset (issue #976 / ADR-122 / SAFE-RELEASES-A)
// is returned by buildDeploymentForInsert when the request body's
// canary.preset is not in pkg/api/canary.AllowedCanaryPresets. 422
// mirrors ErrInvalidTrafficPercent — shape violation, distinct from
// the plan-gate 403 above it. The allowed-set is also rendered on
// the wire so the CLI can suggest the closest match in its error
// chip.
func ErrInvalidCanaryPreset(got string) *Problem {
	return NewProblem(http.StatusUnprocessableEntity, CodeInvalidCanaryPreset,
		"Invalid canary preset",
		fmt.Sprintf("canary preset %q is not in the closed-set catalog (%v); see --canary-preset in `gregale deploy --help`.", got, canary.AllowedCanaryPresets)).
		WithDocs("https://docs.gregale.dev/deployments#canary-presets")
}

// ErrTrafficPercentSumInvalid (issue #556) is the defensive
// backstop for the Σ = 100 invariant. The state layer's
// UpdateDeploymentTraffic transaction asserts the post-write sum
// and lifts to this code if violated. In practice this is
// unreachable: the schema CHECK gates the per-row range and the
// transaction zeroes siblings before stamping the target. The
// code exists so a future refactor that breaks the Σ tripwire
// surfaces a 409 (not a silent DB drift). 409 Conflict because
// the requested state is internally consistent (range-check
// passed) but cannot be applied alongside the existing row set.
func ErrTrafficPercentSumInvalid(observed int) *Problem {
	return NewProblem(http.StatusConflict, CodeTrafficPercentSumInvalid,
		"traffic_percent sum invariant violated",
		fmt.Sprintf("sum of traffic_percent across live deployments must be 100; observed %d.", observed)).
		WithDocs("https://docs.gregale.dev/deployments#traffic-percent")
}

// ErrPlanMirrorNotAllowed (issue #72 / ADR-125 traffic mirroring
// PR-A2) is returned when a Free/Hobby account tries to create a
// mirror rule (issue #72 / ADR-125). The customer's bill on
// Free/Hobby is built around scale-to-zero; a mirror rule wakes
// a separate VM for every customer request (billed per running
// second), which is the cost shape of Pro / Scale. Hobby is
// deliberately NOT promoted to the gate's "allowed" set — see
// the Limits.MirrorRuleAllowed comment in pkg/api/limits.go for
// the cost-shaping rationale (mirror's bill is N×ram_mb×seconds
// per request, distinct from traffic split's canary floor).
func ErrPlanMirrorNotAllowed(p Plan) *Problem {
	return NewProblem(http.StatusForbidden, CodePlanMirrorNotAllowed,
		"Plan doesn't allow traffic mirroring",
		fmt.Sprintf("the %s plan wakes a mirror VM on every request (billed per running second); upgrade to Pro or Scale to mirror traffic in the background.", p)).
		WithDocs("https://docs.gregale.dev/plans#traffic-mirror")
}

// ErrMirrorRuleQuotaExceeded (issue #72 / ADR-125 PR-A2) is
// returned when the customer already holds the plan's per-app
// cap (Pro 1, Scale 3). 422 (not 403) because the request shape
// is legal and the plan permits the feature — only the per-app
// count is at the cap. WithLimit carries the cap and the
// observed count so the CLI renders actionable retry guidance
// (raise your plan, or delete an existing rule first).
func ErrMirrorRuleQuotaExceeded(l Limits, observed int) *Problem {
	return NewProblem(http.StatusUnprocessableEntity, CodeMirrorRuleQuotaExceeded,
		"mirror rule quota exceeded",
		fmt.Sprintf("this app already has %d mirror rule(s); the plan cap is %d.", observed, l.MirrorTargetsPerApp)).
		WithLimit(int64(l.MirrorTargetsPerApp), int64(observed)).
		WithDocs("https://docs.gregale.dev/apps#mirror-rules")
}

// ErrInvalidMirrorPercent (issue #72 / ADR-125 PR-A2) is
// returned when the requested mirror percent is outside [0, 100]
// (the SQL CHECK in migration 00384 is the second-line defence;
// this surfaces before that check). 422 mirrors
// ErrInvalidTrafficPercent above — range-before-plan, so a
// malformed value is loud regardless of plan.
func ErrInvalidMirrorPercent(got int) *Problem {
	const cap = 100
	return NewProblem(http.StatusUnprocessableEntity, CodeInvalidMirrorPercent,
		"Invalid mirror percent",
		fmt.Sprintf("mirror percent must be in [0, %d]; got %d.", cap, got)).
		WithLimit(int64(cap), int64(got)).
		WithDocs("https://docs.gregale.dev/apps#mirror-rules")
}

// ErrMirrorSourceTargetSame (issue #72 / ADR-125 PR-A2) is
// returned when the customer POSTs a rule whose source and
// mirror deployments resolve to the same row. 422 because the
// request shape is legal — only the rule body is self-referential,
// which is meaningless (the mirror VM would call itself).
func ErrMirrorSourceTargetSame() *Problem {
	return NewProblem(http.StatusUnprocessableEntity, CodeMirrorSourceTargetSame,
		"source and mirror deployments must differ",
		"source_deployment_id and mirror_deployment_id cannot reference the same deployment.").
		WithDocs("https://docs.gregale.dev/apps#mirror-rules")
}

// ErrMirrorDeploymentNotLive (issue #72 / ADR-125 PR-A2) is
// returned when one or both referenced deployments has been
// superseded or deleted between the customer's GET and POST.
// 409 Conflict because the request shape is legal and the
// referenced IDs are valid — only the state has moved (a
// deployment was rolled back / superseded mid-request). The
// customer's retry path is to GET the deployments and pick a
// fresh live target.
func ErrMirrorDeploymentNotLive() *Problem {
	return NewProblem(http.StatusConflict, CodeMirrorDeploymentNotLive,
		"referenced deployment is not live",
		"one or both of source_deployment_id / mirror_deployment_id points at a deployment that is not 'live'; mirror targets must both be live.").
		WithDocs("https://docs.gregale.dev/apps#mirror-rules")
}

// ErrMirrorCrossAppMismatch (issue #72 / ADR-125 PR-A2) is
// returned when source and mirror deployments belong to
// different apps. 422 because the request shape is legal — the
// IDs are real — only the cross-app pair is meaningless (a
// mirror VM cannot serve a source VM from a different app; the
// per-app quotas would be split across two owners).
func ErrMirrorCrossAppMismatch() *Problem {
	return NewProblem(http.StatusUnprocessableEntity, CodeMirrorCrossAppMismatch,
		"source and mirror deployments must belong to the same app",
		"source_deployment_id and mirror_deployment_id must reference deployments of the same app (slug in the URL path).").
		WithDocs("https://docs.gregale.dev/apps#mirror-rules")
}

// ErrMirrorRuleNotFound (issue #72 / ADR-125 PR-A2) is the
// 404 sentinel for the customer-facing mirror surface. Used by
// handlers only AFTER the IDOR guard passes — a cross-account
// lookup returns s.notFound (a generic Problem with code
// "not_found"), never this. Emitted when the rule was deleted
// between the customer's GET and PATCH/DELETE.
func ErrMirrorRuleNotFound(id string) *Problem {
	return NewProblem(http.StatusNotFound, CodeMirrorRuleNotFound,
		"mirror rule not found",
		fmt.Sprintf("no mirror rule with id %q on this app.", id)).
		WithDocs("https://docs.gregale.dev/apps#mirror-rules")
}

// ErrInvalidMirrorWindow (issue #72 / ADR-125 PR-A2) is the
// 422 sentinel for malformed `?window=…` query arguments on the
// summary endpoint. Three discrete values are accepted (1h,
// 24h, 7d); anything else surfaces this 422 so the CLI renders
// the accepted set without consulting the docs.
func ErrInvalidMirrorWindow(got string) *Problem {
	return NewProblem(http.StatusUnprocessableEntity, CodeInvalidMirrorWindow,
		"Invalid mirror window",
		fmt.Sprintf("window must be one of: 1h, 24h, 7d; got %q.", got)).
		WithDocs("https://docs.gregale.dev/apps#mirror-summary")
}

// ErrSidecarCapExceeded is returned when the request carries more
// than SidecarCapMax sidecars (issue #463 / ADR-068 §Decision 1).
// 400 because the request shape is wrong; the cap is the load-bearing
// invariant. The schema CHECK on `deployments.sidecars` is the
// second-line defence; this error surfaces before that check
// (the API gate fires first).
func ErrSidecarCapExceeded(seen, cap int) *Problem {
	return NewProblem(http.StatusBadRequest, CodeSidecarCapExceeded,
		"Too many sidecars",
		fmt.Sprintf("request carried %d sidecars; the cap is %d (issue #463 / ADR-068 §Decision 1).", seen, cap)).
		WithLimit(int64(cap), int64(seen)).
		WithDocs(docsBase + "/sidecars#cap")
}

// ErrSidecarInvalidType is returned when a sidecar carries a `type`
// other than {init, sidecar}, or when the request carries more than
// one init or more than one sidecar (the per-type-uniqueness rule).
// 400 because the request shape is wrong.
func ErrSidecarInvalidType(name, got string) *Problem {
	if got == "" {
		return NewProblem(http.StatusBadRequest, CodeSidecarInvalidType,
			"Invalid sidecar type",
			fmt.Sprintf("sidecar %q must declare type=init or type=sidecar.", name))
	}
	return NewProblem(http.StatusBadRequest, CodeSidecarInvalidType,
		"Invalid sidecar type",
		fmt.Sprintf("sidecar %q type %q; must be init or sidecar.", name, got))
}

// ErrSidecarInvalidImage is returned when the sidecar image is not
// digest-pinned (issue #463 / ADR-068 §Decision 5). Tag-pinning is
// the documented OCI supply-chain attack vector; the runtime
// already enforces this; the API gate surfaces a useful error at
// the client side.
func ErrSidecarInvalidImage(name string, err error) *Problem {
	return NewProblem(http.StatusBadRequest, CodeSidecarInvalidImage,
		"Invalid sidecar image",
		fmt.Sprintf("sidecar %q image must be digest-pinned (repo@sha256:...): %v", name, err))
}

// ErrSidecarStatefulDenied is returned when the sidecar image is in
// the `pkg/imaged` `StatefulBaseImageDenylist` set (Postgres,
// Redis, MySQL, MongoDB, etc.). 403 because the request shape is
// valid but the policy denies it. Stateful workloads go on
// dedicated infra, not FaaS.
//
// Deprecated for new callers: prefer ErrSidecarStatefulDeniedWithHint
// (issue #463 / ADR-068 §Decision 4 followup), which surfaces the
// remediation hint from pkg/statefuldenylist.Set in the RFC 7807
// Detail field. Kept for symmetry with the existing pkg/imaged
// surface that takes (name, image) only.
func ErrSidecarStatefulDenied(name, image string) *Problem {
	return NewProblem(http.StatusForbidden, CodeSidecarStatefulDenied,
		"Stateful sidecar image is not allowed",
		fmt.Sprintf("sidecar %q image %q is on the stateful denylist; stateless sidecars only (issue #463 / ADR-068 §Decision 4).", name, image)).
		WithDocs(docsBase + "/sidecars#stateless")
}

// ErrSidecarStatefulDeniedWithHint is the API-gate sidecar variant
// of ErrSidecarStatefulDenied that surfaces the remediation hint
// from pkg/statefuldenylist.Set ("use Neon", "use Upstash", …) in
// the RFC 7807 Detail field. The customer-facing copy is the hint
// (so the dashboard / CLI can render actionable remediation);
// name + image are present in the body so audit-log consumers can
// still attribute the rejection to a specific sidecar.
//
// Empty hint is gracefully degraded (the message still names the
// sidecar + image even when the Set row has no remediation copy —
// defence against a future Set entry being added without a hint).
func ErrSidecarStatefulDeniedWithHint(name, image, hint string) *Problem {
	detail := fmt.Sprintf("sidecar %q image %q is on the stateful denylist; stateless sidecars only (issue #463 / ADR-068 §Decision 4).", name, image)
	if hint != "" {
		detail += " Remediation: " + hint + "."
	}
	return NewProblem(http.StatusForbidden, CodeSidecarStatefulDenied,
		"Stateful sidecar image is not allowed", detail).
		WithDocs(docsBase + "/sidecars#stateless")
}

// ErrSidecarInvalidName is returned when the sidecar name does
// not match the RFC 1123 label grammar (lowercase alphanumeric +
// dash, 1..63 chars, starts with [a-z0-9]).
func ErrSidecarInvalidName(name string) *Problem {
	return NewProblem(http.StatusBadRequest, CodeSidecarInvalidName,
		"Invalid sidecar name",
		fmt.Sprintf("sidecar name %q must match RFC 1123 label (lowercase alphanumeric + dash, 1..63 chars, starts with [a-z0-9]).", name))
}

// ErrSidecarInvalidPort is returned when the sidecar port is out
// of range. Port 0 means "absent" (the OCI image's default port
// is used by the runtime).
func ErrSidecarInvalidPort(port int) *Problem {
	return NewProblem(http.StatusBadRequest, CodeSidecarInvalidPort,
		"Invalid sidecar port",
		fmt.Sprintf("sidecar port %d must be 0 (absent) or in [1, 65535].", port))
}

// ErrSidecarInvalidRamMB is returned when the sidecar ram_mb is
// out of range. 0 means "absent / inherit the plan RAM". The
// 32..512 range is the platform floor/ceiling (the guest-init +
// watchdog overhead is the binding floor at 32 MB; 512 MB is the
// soft per-sidecar ceiling).
func ErrSidecarInvalidRamMB(ramMB int) *Problem {
	return NewProblem(http.StatusBadRequest, CodeSidecarInvalidRamMB,
		"Invalid sidecar ram_mb",
		fmt.Sprintf("sidecar ram_mb %d must be 0 (inherit plan RAM) or in [32, 512].", ramMB))
}

// ErrSidecarNotAllowedOnPlan is reserved for a future per-plan
// gate (PR-A does NOT apply this gate — the global SidecarCapMax
// is the load-bearing surface; see ADR-068 §Decision 1). The
// constructor exists so a follow-up PR doesn't have to invent
// a new code. 403 because it's a plan-tier decision, not a
// shape violation.
func ErrSidecarNotAllowedOnPlan(p Plan) *Problem {
	return NewProblem(http.StatusForbidden, CodeSidecarNotAllowedOnPlan,
		"Plan doesn't allow sidecars",
		fmt.Sprintf("the %s plan doesn't allow sidecars (issue #463 / ADR-068).", p)).
		WithDocs(docsBase + "/plans#sidecars")
}

// ErrPlanMaxInstancesNotAllowed (issue #462 / ADR-058) is the
// 403 plan-gate mirror of ErrPlanMinInstancesNotAllowed. Free
// stays off; Hobby + Pro + Scale opt in. The 403 runs first
// (before the 422 bounds check) so a Free customer PATCHing a
// valid value still sees the plan error, not the bounds error.
func ErrPlanMaxInstancesNotAllowed(p Plan) *Problem {
	return NewProblem(http.StatusForbidden, CodePlanMaxInstancesNotAllowed,
		"Plan doesn't allow a max_instances ceiling",
		fmt.Sprintf("the %s plan does not expose a per-app max_instances; upgrade to Hobby or higher to set it.", p)).
		WithDocs(docsBase + "/apps#max-instances")
}

// ErrInvalidMaxInstances (issue #462 / ADR-058) is the 422
// bounds check on `scaling_policy.max_instances`. The PATCH is
// rejected when the value is below min_instances or above the
// plan's MaxConcurrency. Distinct from ErrInvalidMinInstances so
// the CLI can render "raise your max" vs "fix your min" without
// conflating the two telemetry streams.
func ErrInvalidMaxInstances(got, minInstances, maxConcur int) *Problem {
	return NewProblem(http.StatusUnprocessableEntity, CodeInvalidMaxInstances,
		"Invalid max_instances",
		fmt.Sprintf("max_instances must be in [%d, %d] (plan max_concurrency); got %d.", minInstances, maxConcur, got)).
		WithLimit(int64(maxConcur), int64(got)).
		WithDocs(docsBase + "/apps#max-instances")
}

// ErrInvalidCooldown (issue #462 / ADR-058) is the 422 bounds
// check on `scale_out_cooldown_s` / `scale_in_cooldown_s`. The
// PATCH is rejected when the value is outside the per-direction
// [Min, Max] range (1..3600 for scale-out, 5..86400 for
// scale-in). Distinct from CodeValidation so the API stable
// string is reusable for telemetry.
func ErrInvalidCooldown(field string, got, minSeconds, maxSeconds int) *Problem {
	return NewProblem(http.StatusUnprocessableEntity, CodeInvalidCooldown,
		"Invalid cooldown",
		fmt.Sprintf("%s must be in [%d, %d]; got %d.", field, minSeconds, maxSeconds, got)).
		WithLimit(int64(maxSeconds), int64(got)).
		WithDocs(docsBase + "/apps#scaling-policy")
}

// ErrScalingTargetIncompatibleWithWorkloadClass (issue #462 /
// ADR-058 / PR-D carve-out) is the 422 returned when a
// worker-class app sets `target.metric = concurrent_requests`.
// The signal source is `pkg/vmmd/activity.ActivityTracker` (PR-B)
// which counts in-flight requests — a worker-class app has none
// (no inbound HTTP), so the metric is forever 0 and the engine
// would never admit. The customer-facing reject closes the
// misconfiguration at PATCH time; PR-D carves out the engine
// side as a defense-in-depth check.
func ErrScalingTargetIncompatibleWithWorkloadClass(metric string) *Problem {
	return NewProblem(http.StatusUnprocessableEntity, CodeScalingTargetIncompatibleWithWorkloadClass,
		"Target metric is not compatible with this app's workload class",
		fmt.Sprintf("target.metric=%q is not compatible with worker-class apps; use an rps or p99_latency_ms target instead.", metric)).
		WithDocs(docsBase + "/apps#scaling-policy")
}

// ErrPlanEgressAllowlistNotAllowed (ADR-031) is returned when a Free or Hobby
// account tries to set apps.egress_allowlist. Same gate shape as
// ErrPlanMinInstancesNotAllowed: the knob is plan-locked, and Pro/Scale
// is where the operator surface lives. The plan is named in the body so
// a CLI prompt can render "upgrade to Pro to unlock this knob" without
// a second lookup.
func ErrPlanEgressAllowlistNotAllowed(p Plan) *Problem {
	return NewProblem(http.StatusForbidden, CodePlanEgressAllowlistNotAllowed,
		"Plan doesn't allow an egress allowlist",
		fmt.Sprintf("the %s plan cannot pin an egress IP allowlist; upgrade to Pro or Scale to unlock this operator surface.", p)).
		WithDocs(docsBase + "/apps#egress-allowlist")
}

// ErrPlanPublicAuthIPAllowlistNotAllowed (ADR-118) is returned when a
// Free or Hobby account tries to set apps.public_auth_ip_allowlist.
// Same gate shape as ErrPlanEgressAllowlistNotAllowed: the knob is
// plan-locked, and Pro/Scale is where the operator surface lives.
// Free/Hobby use edge rules (kind='ip') for the abuse-floor posture.
// The plan is named in the body so a CLI prompt can render "upgrade
// to Pro to unlock this knob" without a second lookup.
func ErrPlanPublicAuthIPAllowlistNotAllowed(p Plan) *Problem {
	return NewProblem(http.StatusForbidden, CodePlanPublicAuthIPAllowlistNotAllowed,
		"Plan doesn't allow a public-auth IP allowlist",
		fmt.Sprintf("the %s plan cannot pin a public-auth IP allowlist; upgrade to Pro or Scale to unlock this operator surface.", p)).
		WithDocs(docsBase + "/apps#public-auth-ip-allowlist")
}

// ErrPlanLivenessProbeNotAllowed (issue #554 / ADR-078) is returned when a
// Free account tries to pin a per-deployment liveness probe override. The
// gate is the same shape as ErrPlanEgressAllowlistNotAllowed /
// ErrPlanMinInstancesNotAllowed: Free's Plan.LivenessAllowed() returns
// false; the apid createDeployment handler short-circuits with this 403
// BEFORE the DB is touched. Hobby/Pro/Scale inherit the 5s / 3 / 60s / 3 in
// 300s defaults and accept the override. The plan is named in the body so
// a CLI prompt can render "upgrade to Hobby to unlock liveness" without a
// second lookup.
func ErrPlanLivenessProbeNotAllowed(p Plan) *Problem {
	return NewProblem(http.StatusForbidden, CodePlanLivenessProbeNotAllowed,
		"Plan doesn't allow a liveness probe",
		fmt.Sprintf("the %s plan cannot pin a liveness probe; upgrade to Hobby or above to unlock the Cloud-Run-parity primitive.", p)).
		WithDocs(docsBase + "/deploy-overrides#liveness-probe")
}

// ErrEgressAllowlistTooLong (ADR-031) is returned when the PATCH carries more
// CIDRs than the plan's per-app cap. 400 (not 422) because the request shape is
// well-formed — only the count is over budget. The limit + observed pair rides
// on the Problem so the CLI can branch on its own copy of the cap (no re-fetch).
func ErrEgressAllowlistTooLong(got, maxSize int) *Problem {
	return NewProblem(http.StatusBadRequest, CodeEgressAllowlistTooLong,
		"Egress allowlist too long",
		fmt.Sprintf("egress_allowlist has %d entries; plan caps it at %d.", got, maxSize)).
		WithLimit(int64(maxSize), int64(got)).
		WithDocs(docsBase + "/apps#egress-allowlist")
}

// ErrPublicAuthIPAllowlistTooLong (ADR-118) is returned when the
// PATCH carries more CIDRs than the plan's per-app cap. 400 (not
// 422) because the request shape is well-formed — only the count
// is over budget. The limit + observed pair rides on the Problem
// so the CLI can branch on its own copy of the cap (no re-fetch).
func ErrPublicAuthIPAllowlistTooLong(got, maxEntries int) *Problem {
	return NewProblem(http.StatusBadRequest, CodePublicAuthIPAllowlistTooLong,
		"Public-auth IP allowlist too long",
		fmt.Sprintf("public_auth_ip_allowlist has %d entries; plan caps it at %d.", got, maxEntries)).
		WithLimit(int64(maxEntries), int64(got)).
		WithDocs(docsBase + "/apps#public-auth-ip-allowlist")
}

// ErrAccountEgressAllowlistExtraOutOfRange (issue #679 / PR-B /
// ADR-082) is the 400 returned by PATCH
// /v1/account/egress_allowlist_extra when the value is < 0 or
// > api.MaxAccountEgressAllowlistExtra. Negative values are also
// rejected at the DB CHECK layer (postgres 23514), but exposing
// the cap at the API lets the operator-side CLI render the
// exact upper bound without a follow-up call. The limit +
// observed pair rides on the Problem so the dashboard can pivot
// from the error message to the cap slider.
func ErrAccountEgressAllowlistExtraOutOfRange(got, maxExtra int) *Problem {
	return NewProblem(http.StatusBadRequest, CodeAccountEgressAllowlistExtraOutOfRange,
		"Account egress allowlist extra out of range",
		fmt.Sprintf("egress_allowlist_extra=%d; max is %d.", got, maxExtra)).
		WithLimit(int64(maxExtra), int64(got)).
		WithDocs(docsBase + "/account#egress-allowlist-extra")
}

// ErrInvalidEgressAllowlist (ADR-031 + ADR-032) is a 400 for
// entries that don't ParsePrefix as a v4 or v6 CIDR, or that
// have masklen /0. The detail names the offending entry so an
// operator triaging a rejected PATCH sees exactly which line is
// bad. ADR-032 — v6 entries are accepted alongside v4 entries;
// the non-/0 contract is shared with the DB trigger.
func ErrInvalidEgressAllowlist(entry string, reason error) *Problem {
	return NewProblem(http.StatusBadRequest, CodeInvalidEgressAllowlist,
		"Invalid egress allowlist entry",
		fmt.Sprintf("entry %q is not a valid v4 or v6 CIDR (non-/0): %v.", entry, reason)).
		WithDocs(docsBase + "/apps#egress-allowlist")
}

// ErrPlanStaticEgressIPNotAllowed (ADR-119) is returned when a
// Free/Hobby/Pro customer tries to PUT
// /v1/apps/{slug}/static-egress-ip. 402 mirrors the existing
// CodePlanDataUpstreamsNotAllowed / CodePlanWebhooksNotAllowed
// family so the CLI's "your plan does not unlock X" template
// renders uniformly. The dashboard's upgrade CTA surfaces this
// from `Limits.StaticEgressIPAllowed == false`.
func ErrPlanStaticEgressIPNotAllowed(p Plan) *Problem {
	return NewProblem(http.StatusPaymentRequired, CodePlanStaticEgressIPNotAllowed,
		"Plan does not unlock static egress IP",
		fmt.Sprintf("plan %q does not unlock static egress IP; upgrade to Scale.", p)).
		WithLimit(int64(0), int64(0)).
		WithDocs(docsBase + "/apps#static-egress-ip")
}

// ErrPlanStaticEgressIPQuota (ADR-119) is the 403 returned by
// the apid handler when the PATCH would either (a) exceed the
// per-app quota (currently 1 for Scale — bumping is a per-plan
// int change with no schema impact), or (b) alias-IP-collide
// with another app on the same account (defended at the DB
// layer by `apps_static_egress_ip_key` partial unique index;
// this surfaces the SQLSTATE 23505 in plan-uniform wording).
// The limit + observed pair rides on the Problem so the CLI
// can branch on its own copy of the cap.
func ErrPlanStaticEgressIPQuota(p Plan, limit int, observed int, detail string) *Problem {
	return NewProblem(http.StatusForbidden, CodePlanStaticEgressIPQuota,
		"Static egress IP quota reached",
		fmt.Sprintf("plan %q caps static egress IP at %d per app (observed=%d): %s.", p, limit, observed, detail)).
		WithLimit(int64(limit), int64(observed)).
		WithDocs(docsBase + "/apps#static-egress-ip")
}

// ErrAppStaticEgressIPInvalid (ADR-119) is a 400 for shape
// failures on the static egress IP value: malformed IP string,
// IPv6 family (deferred to follow-up ADR), or the IP falling
// inside one of the egress denylist ranges (RFC1918,
// link-local, multicast, CGN 100.64/10, loopback). The detail
// names the failure mode so the CLI's `gregale app security
// static-egress-ip set` rejection renders actionable guidance
// ("use a public IPv4 outside 10/8, 172.16/12, 192.168/16,
// 169.254/16, 224/4, 100.64/10, 127/8"). Mirrors
// ErrInvalidEgressAllowlist's "name the offending entry"
// contract.
func ErrAppStaticEgressIPInvalid(ip string, reason string) *Problem {
	return NewProblem(http.StatusBadRequest, CodeAppStaticEgressIPInvalid,
		"Invalid static egress IP",
		fmt.Sprintf("static_egress_ip=%q is not accepted: %s.", ip, reason)).
		WithDocs(docsBase + "/apps#static-egress-ip")
}

// ErrStaticEgressIPNotProvisioned (ADR-119 redesign) is the 404
// returned by the apid handler when the customer's pin attempts to
// attach an IP that isn't in the operator-provisioned bundle
// (provisioned_static_egress_ips table; migration 00337).
//
// The deployment-shape gate: customer-supplied IPs that aren't
// routed to the host's AS are (a) outbound-spoofed at the switch
// (no internet path) and (b) inbound replies route to the customer
// AS (lost). The operator must attach the IP as an additional IP
// on the host's AS first, then declare it in the operator TOML
// /etc/faas/egress/static_egress_ips.toml. vmmd writes the
// (account_id, customer_ip) tuple to the Postgres gate on SIGHUP.
// A missing tuple means the customer requested an IP that
// isn't provisioned — the handler must refuse the pin so a
// customer can never silently route to an unroutable IP.
//
// 404 (not 403) is the canonical framing: the IP is not
// provisioned on this cluster's host. The customer-facing
// copy directs the customer to the host operator.
func ErrStaticEgressIPNotProvisioned(ip string) *Problem {
	return NewProblem(http.StatusNotFound, CodeStaticEgressIPNotProvisioned,
		"Static egress IP not provisioned on this host",
		fmt.Sprintf("static_egress_ip=%q is not in the operator bundle; ask the host operator to provision it on the host's AS before pinning.", ip)).
		WithDocs(docsBase + "/apps#static-egress-ip")
}

// ErrInvalidPublicAuthIPAllowlist (ADR-118) is a 400 for ingress
// allowlist shape violations: an entry that doesn't ParsePrefix as
// a v4 or v6 CIDR, masklen /0, or `ip_allowlist` mode set with an
// empty list. The detail names the offending entry so an operator
// triaging a rejected PATCH sees exactly which line is bad. The
// non-/0 contract is shared with the DB trigger at
// migrations/00308_apps_public_auth_ip_allowlist.sql — the apid
// layer is defence-in-depth, not the primary guard.
func ErrInvalidPublicAuthIPAllowlist(entry string, reason error) *Problem {
	return NewProblem(http.StatusBadRequest, CodeInvalidPublicAuthIPAllowlist,
		"Invalid public-auth IP allowlist entry",
		fmt.Sprintf("entry %q is not a valid v4 or v6 CIDR (non-/0): %v.", entry, reason)).
		WithDocs(docsBase + "/apps#public-auth-ip-allowlist")
}

// ErrValidation is a 400 fallback for malformed request bodies. Used by
// handlers when JSON decode fails — the underlying error detail isn't
// surfaced (it's attacker-influenced) but the cause class is the same
// across handlers.
func ErrValidation(detail string) *Problem {
	return NewProblem(http.StatusBadRequest, CodeValidation,
		"Validation failed", detail)
}

// ErrPlanQueueDepth is returned by the apid handlers on POST
// .../queues/invocations:send (and on delayed-task create) when
// accepting the row would push the per-app live queue/depth past
// the plan cap. Observed is the current live count (matching the
// response payload so dashboards can render the gauge without a
// second round-trip).
func ErrPlanQueueDepth(limit, observed int) *Problem {
	return NewProblem(http.StatusForbidden, CodePlanQueueDepth,
		"Per-app queue depth exceeded",
		fmt.Sprintf("the plan caps this app at %d pending + dispatching rows; observed %d.", limit, observed)).
		WithLimit(int64(limit), int64(observed)).
		WithDocs(docsBase + "/event-driven#queue-depth")
}

// ErrPlanSourceBytes is returned when a request body for an event-shaped
// surface (sync / async / queue :send / delayed-task create) exceeds
// the per-plan MaxSourceBytesPerInvocation.
func ErrPlanSourceBytes(limit int, observed int64) *Problem {
	return NewProblem(http.StatusRequestEntityTooLarge, CodePlanSourceBytes,
		"Invocation payload too large",
		fmt.Sprintf("this plan caps each invocation at %d bytes; observed %d.", limit, observed)).
		WithLimit(int64(limit), observed).
		WithDocs(docsBase + "/event-driven#payload-size")
}

// ErrPlanFeatureGated is returned when the customer's plan does not
// unlock the requested surface (spec §4.4 reserves async invoke and
// queues for paid tiers; Free customers get 402 with the upgrade
// nudge). Code differs from CodePlanLimit* because the failure mode
// is plan-gating, not "you used more than the plan allows".
func ErrPlanFeatureGated(feature string, p Plan) *Problem {
	return NewProblem(http.StatusPaymentRequired, CodePlanFeatureGated,
		"Plan doesn't include this feature",
		fmt.Sprintf("the %s plan doesn't unlock %s; upgrade to Hobby or higher to use event-driven features.", p, feature)).
		WithDocs(docsBase + "/plans#event-driven")
}

// ErrPlanDelayedTasksCap is the variant surfaced when a delayed-task
// schedule would push the per-app delayed-task count past the plan
// cap. Distinct code so the dashboard can suggest "schedule later"
// vs the queue-depth case which is a stricter 403.
func ErrPlanDelayedTasksCap(limit, observed int) *Problem {
	return NewProblem(http.StatusForbidden, CodePlanDelayedCap,
		"Per-app delayed-task cap exceeded",
		fmt.Sprintf("the plan caps this app at %d scheduled delayed_tasks; observed %d.", limit, observed)).
		WithLimit(int64(limit), int64(observed)).
		WithDocs(docsBase + "/event-driven#delayed-tasks")
}

// ErrInvocationNotFound is the Move 1 counterpart to ErrSecretNotFound:
// the URL name (the resource IS the invocation) is intentional, and a
// generic not_found would force the CLI to parse the message.
func ErrInvocationNotFound(id string) *Problem {
	return NewProblem(http.StatusNotFound, CodeInvocationNotFound,
		"Invocation not found",
		fmt.Sprintf("no invocation with id %q on this account.", id)).
		WithDocs(docsBase + "/event-driven#invocations")
}

// ErrInvocationNotReplayable (issue #315 / tier-2 DX) is returned by
// POST /v1/invocations/{id}/replay when the original's State is
// outside the replayable allow-list ({failed, dead_letter}).
// Rendered with the current state in the message so the customer
// can decide whether to wait for a pending invocation, or accept
// the completed result without re-running.
func ErrInvocationNotReplayable(state string) *Problem {
	return NewProblem(http.StatusConflict, CodeInvocationNotReplayable,
		"Invocation is not in a replayable state",
		fmt.Sprintf("only invocations in state 'failed' or 'dead_letter' can be replayed; current state is %q.", state)).
		WithDocs("https://docs.gregale.dev/event-driven#invocations")
}

// ErrBuildProvenanceNotFound is the ADR-038 surface for a build
// whose populator INSERT never landed (best-effort WARN inside
// builderd.recordProvenance) OR for a pre-PR build that pre-dates
// build_provenance entirely. Distinct from "no such build" so the
// customer (and the dashboard) can branch on it. The build row is
// authoritative for the success/fail transition; the missing
// provenance is observational metadata.
func ErrBuildProvenanceNotFound() *Problem {
	return NewProblem(http.StatusNotFound, CodeBuildProvenanceNotFound,
		"Build provenance not found",
		"the build succeeded but no provenance row exists; builderd logged a warning when the populator failed").
		WithDocs(docsBase + "/builds#provenance")
}

// ErrBuildNotFound is the DEPLOY-PROV-6 / ADR-089 (issue #741)
// surface for GET /v1/builds/{id} when the build id is unknown
// OR belongs to another account. The 404 surface is uniform so
// cross-account probes can't enumerate — distinct from
// CodeBuildProvenanceNotFound, which means "the build exists but
// its provenance populator INSERT failed."
func ErrBuildNotFound() *Problem {
	return NewProblem(http.StatusNotFound, CodeBuildNotFound,
		"No such build",
		"the build id does not exist, or belongs to another account").
		WithDocs(docsBase + "/builds#status")
}

// ErrInvalidRef is the DEPLOY-PROV-4 / ADR-092 (issue #739)
// 400 surface for POST /v1/apps/{slug}/deployments/source-ref
// when the supplied ref fails gitfetch.IsValidCommitSHA, fails
// the path-traversal guard, OR when GitHub's commits/{ref}
// resolution returns 404. The ref is echoed verbatim so the
// CI script's `--ref <value>` can be debugged without a second
// call. Distinct from CodeSourceInvalid (post-fetch tarball
// shape) and CodeSourceTooLarge (per-plan cap): this fires
// upstream of the fetch.
func ErrInvalidRef(ref string) *Problem {
	return NewProblem(http.StatusBadRequest, CodeInvalidRef,
		"Invalid ref",
		fmt.Sprintf("ref %q is not a valid commit SHA, branch, or tag.", ref)).
		WithDocs(docsBase + "/build/source-ref")
}

// ErrGitHubInstallNotFound is the DEPLOY-PROV-4 / ADR-092 (issue
// #739) 404 surface for POST /v1/apps/{slug}/deployments/source-ref
// when state.GitHubInstallForAccount returns ErrNotFound for the
// caller's account. Distinct from the generic CodeNotFound so the
// dashboard can render the "complete `gregale connect` first"
// CTA — CI has no browser, so the bind row must already exist.
func ErrGitHubInstallNotFound() *Problem {
	return NewProblem(http.StatusNotFound, CodeGitHubInstallNotFound,
		"No GitHub installation",
		"this account has no GitHub App installation row; complete `gregale connect` once before retrying from CI").
		WithDocs(docsBase + "/build/source-ref#prereq")
}

// ErrSourceRefUnavailable is the DEPLOY-PROV-4 / ADR-092 (issue
// #739) 503 surface for POST /v1/apps/{slug}/deployments/source-ref
// when the githubd bridge is down (StreamSourceRef returned
// Unavailable) or when a 401 from codeload.github.com survived
// one cache-invalidate + retry. The detail echoes the upstream
// reason; Retry-After is left to the caller (typically 30s).
// Distinct from CodeCapacity (the per-plan concurrency cap) and
// CodeWaitForWarm (the per-app scale-out cooldown): both are
// plan-shape gates, while this is "the GitHub bridge failed".
func ErrSourceRefUnavailable(reason string) *Problem {
	return NewProblem(http.StatusServiceUnavailable, CodeSourceRefUnavailable,
		"Source-ref fetch unavailable",
		reason).
		WithDocs(docsBase + "/build/source-ref#errors")
}

// ErrBuildSBOMUnavailable is the issue #299 / ADR-038 Phase 3 surface
// for `faas build sbom <id>` (and the SDK GetBuildsIdSbom) when no
// SBOM artefact has been stored for this build yet — either the imaged
// syft populator in pkg/imaged/loop.go hasn't landed (pre-PR build) or
// the populator INSERT was best-effort WARNed away. The 503 distinguishes
// "may exist later, retry" from the 404 "no such build". The SDK errors
// stays parseable so the CLI's "no SBOM for this build" path can branch
// on the code.
func ErrBuildSBOMUnavailable() *Problem {
	return NewProblem(http.StatusServiceUnavailable, CodeBuildSBOMUnavailable,
		"Build SBOM unavailable",
		"no SBOM has been generated for this build; imaged's syft populator did not run or did not persist the artefact").
		WithDocs(docsBase + "/builds#sbom")
}

// ErrLongPollTimeout is returned by the long-poll handlers (sync
// invoke, queueReceive) when the server-side wait budget ran out.
// Distinct code so the CLI can retry transparently — a 504 Gateway
// Timeout would force the customer to disambiguate "server is down"
// from "no event yet, retry". The HTTP status is 504 (the SLO is
// server-side); the body type is the only ordering.
func ErrLongPollTimeout() *Problem {
	return NewProblem(http.StatusGatewayTimeout, "long_poll_timeout",
		"Long-poll wait budget ran out",
		"the server waited for the configured long-poll window and the event did not arrive; retry.").
		WithDocs(docsBase + "/event-driven#long-poll")
}

// ErrInvalidScheduledAt is returned when a delayed-task POST carries a
// scheduled_at that is in the past (or zero). The handler uses time.Now()
// as the source of truth so a clock-skewed client gets a 400 rather than
// a row that fires immediately on insert.
func ErrInvalidScheduledAt() *Problem {
	return NewProblem(http.StatusBadRequest, "invalid_scheduled_at",
		"Invalid scheduled_at",
		"scheduled_at must be a future timestamp; the server clock rejected the value").
		WithDocs(docsBase + "/event-driven#delayed-tasks")
}

// --- Dashboard auth (issue #165, ADR-032 PR #2) ----------------------------

// ErrInvalidCredentials is the 401 returned by POST /login (and the
// colliding /signup anti-enumeration path). The body is identical
// whether the email is unbound, the password is wrong, or the account
// has no password row — the spec §11 anti-enumeration invariant. The
// constant-time Argon2id pad on the no-account path closes the timing
// oracle; the response body and the wire status are the same on both
// branches.
func ErrInvalidCredentials() *Problem {
	return NewProblem(http.StatusUnauthorized, CodeInvalidCredentials,
		"Sign in failed",
		"email or password is incorrect.").
		WithDocs(docsBase + "/auth/sign-in")
}

// ErrEmailNotVerified is the 401 returned by the Google / GitHub OAuth
// callback when the provider's profile has no primary verified email.
// Distinct from invalid_credentials because the customer can fix it
// upstream (verify the email on the provider) and retry. We never
// mint an unverified session.
func ErrEmailNotVerified(provider string) *Problem {
	return NewProblem(http.StatusUnauthorized, CodeEmailNotVerified,
		"Email not verified",
		fmt.Sprintf("the %s account's primary email is not verified; verify it on the provider and retry.", provider)).
		WithDocs(docsBase + "/auth/oauth")
}

// ErrPasswordTooWeak is the 400 returned by POST /signup and POST
// /auth/reset when the password fails the NIST-style floor (≥12 chars,
// no complexity rules). The Detail names the rule so the form can
// highlight which constraint tripped.
func ErrPasswordTooWeak(reason string) *Problem {
	return NewProblem(http.StatusBadRequest, CodePasswordTooWeak,
		"Password too weak", reason).
		WithDocs(docsBase + "/auth/password")
}

// ErrResetTokenInvalid is the 410 returned by GET / POST /auth/reset
// when the token doesn't exist (unknown / typo'd / already consumed).
// 410 Gone is the right status: the resource was a one-shot and is
// no longer addressable.
func ErrResetTokenInvalid() *Problem {
	return NewProblem(http.StatusGone, CodeResetTokenInvalid,
		"Reset link invalid",
		"this password-reset link is unknown or has already been used.").
		WithDocs(docsBase + "/auth/reset")
}

// ErrResetTokenExpired is the 410 returned by GET / POST /auth/reset
// when the token has aged past the 15-minute TTL. Same 410 as the
// invalid-token case but distinct code so the dashboard can render
// "link expired, request a new one" vs "link is invalid".
func ErrResetTokenExpired() *Problem {
	return NewProblem(http.StatusGone, CodeResetTokenExpired,
		"Reset link expired",
		"this password-reset link has expired; request a new one.").
		WithDocs(docsBase + "/auth/reset")
}

// --- Organizations (issue #190 / IAM-6 / ADR-061) --------------------------
//
// Twelve stable constructors cover the full org lifecycle. Each
// helper is the one-liner PR 5 / PR 6 handlers call; the prefix-
// shared RFC 7807 body keeps the dashboard / CLI / SDK surface
// predictable. The 409-vs-410-vs-403-vs-404-vs-422 status mapping
// lives in StatusForCode so pkg/grpcerr.FromStatus can lift a
// gRPC code back into the right HTTP status (defence in depth
// against future code additions that forget the switch case).

// ErrOrgNotFound is the 404 returned when the org slug does not
// resolve to an org the principal can see. Mirrors the IDOR
// convention used by LoadApp — cross-tenant access returns 404,
// never 403, so the surface never leaks existence.
func ErrOrgNotFound(slug string) *Problem {
	return NewProblem(http.StatusNotFound, CodeOrgNotFound,
		"Organization not found",
		fmt.Sprintf("no organization with slug %q is visible to this account.", slug)).
		WithDocs(docsBase + "/orgs")
}

// ErrOrgSlugInvalid is the 422 returned when a slug fails the
// regex or shape check (lowercase ASCII letters, digits, dashes;
// 3..32 chars; not a reserved keyword). Detail names the rule
// so the dashboard form can highlight which constraint tripped.
// The regex comes from OrgSlugPattern so PR 5's handler
// validator and this constructor share one source of truth.
func ErrOrgSlugInvalid(reason string) *Problem {
	return NewProblem(http.StatusUnprocessableEntity, CodeOrgSlugInvalid,
		"Invalid organization slug",
		fmt.Sprintf("org slugs must match %s; %s", OrgSlugPattern, reason)).
		WithDocs(docsBase + "/orgs#slugs")
}

// ErrOrgSlugTaken is the 409 returned when the slug is already in
// use (either live or in the deleted-pending window). Detail
// distinguishes the two so the CLI can render actionable retry
// guidance.
func ErrOrgSlugTaken(slug string) *Problem {
	return NewProblem(http.StatusConflict, CodeOrgSlugTaken,
		"Organization slug in use",
		fmt.Sprintf("slug %q is already taken; pick another.", slug)).
		WithDocs(docsBase + "/orgs#slugs")
}

// ErrOrgMemberCapExceeded is the 403 returned when the plan's
// OrgMembersMax is reached. limit and observed ride on the
// Problem so the CLI can render the cap without re-fetching.
func ErrOrgMemberCapExceeded(limit, observed int) *Problem {
	return NewProblem(http.StatusForbidden, CodeOrgMemberCapExceeded,
		"Organization member limit reached",
		fmt.Sprintf("the plan caps this organization at %d member(s); you have %d.",
			limit, observed)).
		WithLimit(int64(limit), int64(observed)).
		WithDocs(docsBase + "/orgs#member-cap")
}

// ErrOrgInvitationCapExceeded is the 403 returned when the
// plan's OrgPendingInvitationsMax is reached. Independent of the
// member cap — defends against the N-invites × fast-accept botnet
// signature.
func ErrOrgInvitationCapExceeded(limit, observed int) *Problem {
	return NewProblem(http.StatusForbidden, CodeOrgInvitationCapExceeded,
		"Organization invitation limit reached",
		fmt.Sprintf("the plan caps this organization at %d pending invitation(s); you have %d.",
			limit, observed)).
		WithLimit(int64(limit), int64(observed)).
		WithDocs(docsBase + "/orgs#invitation-cap")
}

// ErrOrgRoleForbidden is the 403 returned when the authenticated
// member lacks the role required for this action. The action
// rides in the detail so the dashboard can render "you need to
// be an admin to invite members" without a separate lookup.
func ErrOrgRoleForbidden(action string) *Problem {
	return NewProblem(http.StatusForbidden, CodeOrgRoleForbidden,
		"Insufficient role for this action",
		fmt.Sprintf("your role does not allow %s on this organization.", action)).
		WithDocs(docsBase + "/orgs#roles")
}

// ErrOrgAlreadyMember is the 409 returned when the accepting
// account already has a membership in the org. Detail names the
// existing role so the dashboard can offer a "switch to this
// role" path instead of a plain "already a member" copy.
func ErrOrgAlreadyMember(role string) *Problem {
	return NewProblem(http.StatusConflict, CodeOrgAlreadyMember,
		"Already a member of this organization",
		fmt.Sprintf("this account is already a member of the organization with role %q.", role)).
		WithDocs(docsBase + "/orgs#members")
}

// ErrOrgInvitationInvalid is the 410 returned when the invitation
// token is unknown, already consumed, or revoked. 410 Gone is the
// semantically correct status — the resource was a one-shot and
// is no longer addressable.
func ErrOrgInvitationInvalid() *Problem {
	return NewProblem(http.StatusGone, CodeOrgInvitationInvalid,
		"Invitation invalid",
		"this invitation is unknown, already consumed, or has been revoked.").
		WithDocs(docsBase + "/orgs#invitations")
}

// ErrOrgInvitationExpired is the 410 returned when the invitation
// token has aged past its expires_at. Same 410 as the invalid
// case but distinct code so the dashboard can render "link
// expired, request a new one" vs "link is invalid".
func ErrOrgInvitationExpired() *Problem {
	return NewProblem(http.StatusGone, CodeOrgInvitationExpired,
		"Invitation expired",
		"this invitation has expired; ask the inviter to send a new one.").
		WithDocs(docsBase + "/orgs#invitations")
}

// ErrOrgLastOwner is the 409 returned when removing, demoting,
// or deleting the only owner of a non-personal org. Ownership
// transfer is the only way to vacate the role.
func ErrOrgLastOwner() *Problem {
	return NewProblem(http.StatusConflict, CodeOrgLastOwner,
		"Cannot remove the last owner",
		"transfer ownership to another member before removing or demoting this role.").
		WithDocs(docsBase + "/orgs#ownership")
}

// ErrOrgPersonalImmutable is the 409 returned when a caller
// tries to mutate a personal org (add members, transfer
// ownership, delete standalone). Personal orgs are immutable
// for the lifetime of the owning account.
func ErrOrgPersonalImmutable() *Problem {
	return NewProblem(http.StatusConflict, CodeOrgPersonalImmutable,
		"Personal organizations are immutable",
		"this organization is the personal organization of one account; it cannot accept members or be deleted independently.").
		WithDocs(docsBase + "/orgs#personal-orgs")
}

// ErrOrgAPIKeyRequiresOrg is the 409 returned when a legacy API
// key (no org binding) is used to address an org-scoped endpoint.
// The dashboard surfaces "re-mint this key as an org-bound key"
// rather than a generic 403.
func ErrOrgAPIKeyRequiresOrg() *Problem {
	return NewProblem(http.StatusConflict, CodeOrgAPIKeyRequiresOrg,
		"API key must be bound to an organization",
		"this legacy API key has no organization binding; create a new key via /v1/orgs/{slug}/keys.").
		WithDocs(docsBase + "/orgs#api-keys")
}

// ErrInvalidOverflowNode is the 422 returned when the
// overflow_node PATCH or create-time value does not resolve
// to a real, active compute_node (Tier A10 / ADR-088). Names
// the offending value back to the customer so dashboards can
// surface "spill target X not found" without grepping logs.
func ErrInvalidOverflowNode(name string) *Problem {
	return NewProblem(http.StatusUnprocessableEntity, CodeInvalidOverflowNode,
		"Invalid overflow_node",
		fmt.Sprintf("no active compute_node named %q; check the operator-supplied spill target name and try again.", name)).
		WithDocs(docsBase + "/apps#overflow_node")
}

// ----------------------------------------------------------------------------
// Edge rule errors (ADR-089). Mirrors the alert_rules / webhook
// helper shape so the CLI can render all four 402/403/404/422
// flavours uniformly.
// ----------------------------------------------------------------------------

// ErrEdgeRuleNotFound is the 404 returned by getEdgeRule /
// updateEdgeRule / deleteEdgeRule when the rule id doesn't exist
// OR belongs to another account.
func ErrEdgeRuleNotFound(id string) *Problem {
	return NewProblem(http.StatusNotFound, CodeEdgeRuleNotFound,
		"Edge rule not found",
		fmt.Sprintf("no edge rule with id %q belongs to your account.", id))
}

// ErrEdgeRuleConflict is the 409 returned on a UNIQUE violation
// (no UNIQUE constraints today, but the seam stays so a future
// per-account name uniqueness lands without an API rename).
func ErrEdgeRuleConflict(reason string) *Problem {
	return NewProblem(http.StatusConflict, CodeEdgeRuleConflict,
		"Edge rule conflict", reason)
}

// ErrPlanLimitEdgeRules is the 403 returned when
// CreateEdgeRuleIfUnderQuota surfaces a *state.EdgeRuleQuotaError.
// Per-app scope only — there's no account-wide flavour. 403 (not
// 402) because the plan DOES unlock the cheap kinds.
func ErrPlanLimitEdgeRules(plan Plan, limit, observed int) *Problem {
	return NewProblem(http.StatusForbidden, CodePlanLimitEdgeRules,
		"Edge rule limit reached",
		fmt.Sprintf("%s plan caps edge rules at %d per app; you have %d. Delete one to add another.",
			plan, limit, observed)).
		WithLimit(int64(limit), int64(observed)).
		WithDocs(docsBase + "/plans#edge-rules")
}

// ErrPlanEdgeRuleKindNotAllowed is the 402 returned when a Free
// customer posts kind=jwt or kind=ip. Hobby+ unlocks both.
func ErrPlanEdgeRuleKindNotAllowed(plan Plan, kind string) *Problem {
	return NewProblem(http.StatusPaymentRequired, CodePlanEdgeRuleKindNotAllowed,
		"Edge rule kind unavailable on this plan",
		fmt.Sprintf("the %s plan does not include edge rule kind %q; upgrade to Hobby or above to use it.", plan, kind)).
		WithDocs(docsBase + "/plans#edge-rules")
}

// ErrPlanEdgeRuleKindQuotaReached is the 403 returned when the
// per-kind edge-rule quota is reached (ADR-091 D22 — currently only
// kind=geo has a separate per-kind cap; future paid-kind additions
// can reuse the same RFC 7807 code with their own kind arg). distinct
// from ErrPlanEdgeRulesQuotaReached which is the GENERAL cap trip
// (no kind-specific signal). Surfacing the kind lets the customer see
// "kind=geo: 1/1 rules used on Free; upgrade to Hobby for 5".
func ErrPlanEdgeRuleKindQuotaReached(plan Plan, kind string, observed, limit int) *Problem {
	return NewProblem(http.StatusForbidden, CodePlanEdgeRuleKindQuotaReached,
		"Edge rule kind quota reached",
		fmt.Sprintf("the %s plan allows %d %s edge rule(s) per app; you have used %d. Upgrade to a higher tier for a larger quota.",
			plan, limit, kind, observed)).
		WithDocs(docsBase + "/plans#edge-rules")
}

// ErrCORSOriginNotAllowed is the 403 returned when a CORS rule's
// allow_origins list rejects the request's Origin header.
//
// Exported for apid test fixtures and future per-app audit emit
// (CORS improvements D1); not consumed on the gateway hot path
// today, where origin rejection is silent — the gateway stamps
// no Access-Control-Allow-Origin and the browser drops the
// response client-side. A future ADR can switch the gateway to
// emit this Problem on a per-deployment "fail-closed" opt-in;
// today the failure mode matches the pre-PR-#841 contract
// documented in spec §4.1.2.6.
func ErrCORSOriginNotAllowed(origin string) *Problem {
	return NewProblem(http.StatusForbidden, CodeCORSOriginNotAllowed,
		"CORS origin not allowed",
		fmt.Sprintf("origin %q is not in the allowlist for this route.", origin))
}

// ErrJWTMissingToken is the 401 returned when a kind=jwt rule
// matches but the request has no Bearer token. Carries the
// WWW-Authenticate header via WithHeader so browsers / SDKs can
// prompt for credentials.
func ErrJWTMissingToken() *Problem {
	return NewProblem(http.StatusUnauthorized, CodeJWTMissingToken,
		"JWT bearer token required",
		"this route requires a Bearer token; supply one in the Authorization header.").
		WithHeader("WWW-Authenticate", `Bearer realm="gregale"`)
}

// ErrJWTMissingIssuer is the 401 returned when the JWT's `iss`
// claim doesn't match the rule's expected issuer.
func ErrJWTMissingIssuer(want string) *Problem {
	return NewProblem(http.StatusUnauthorized, CodeJWTMissingIssuer,
		"JWT issuer mismatch",
		fmt.Sprintf("this route requires tokens issued by %q.", want)).
		WithHeader("WWW-Authenticate", `Bearer realm="gregale", error="invalid_issuer"`)
}

// ErrJWTAudienceMismatch is the 401 returned when the JWT's `aud`
// claim isn't in the rule's audience list.
func ErrJWTAudienceMismatch(want []string) *Problem {
	return NewProblem(http.StatusUnauthorized, CodeJWTAudienceMismatch,
		"JWT audience mismatch",
		fmt.Sprintf("this route requires tokens whose audience is one of %v.", want)).
		WithHeader("WWW-Authenticate", `Bearer realm="gregale", error="invalid_audience"`)
}

// ErrJWTSignatureInvalid is the 401 returned when JWKS
// verification fails (bad signature, unknown kid, expired token).
// The detail carries the underlying reason for log search.
func ErrJWTSignatureInvalid(reason string) *Problem {
	return NewProblem(http.StatusUnauthorized, CodeJWTSignatureInvalid,
		"JWT signature invalid",
		reason).
		WithHeader("WWW-Authenticate", `Bearer realm="gregale", error="invalid_token"`)
}

// ErrIPDenied is the 403 returned when a kind=ip rule's allow/deny
// evaluator rejects the client IP.
func ErrIPDenied(ip string) *Problem {
	return NewProblem(http.StatusForbidden, CodeIPDenied,
		"IP address not allowed",
		fmt.Sprintf("client IP %s is not in the allowlist and matched the deny list for this route.", ip))
}

// ErrGeoDenied is the 403 returned when a kind=geo rule's allow/deny
// evaluator rejects the client country. The country is the ISO 3166-1
// alpha-2 code resolved by the pkg/geoip.Reader lookup; decision is
// either "deny" (rule.Deny matched) or "implicit_deny" (Allow was
// non-empty and the country was not on it).
func ErrGeoDenied(country, decision string) *Problem {
	var detail string
	switch decision {
	case "deny":
		detail = fmt.Sprintf("client country %s is on the deny list for this route.", country)
	case "implicit_deny":
		detail = fmt.Sprintf("client country %s is not on the allow list for this route.", country)
	default:
		detail = fmt.Sprintf("client country %s is not allowed for this route.", country)
	}
	return NewProblem(http.StatusForbidden, CodeGeoDenied,
		"Country not allowed", detail)
}

// ErrHeaderMutationForbidden is the 422 returned when a kind=headers
// rule tries to mutate a forbidden header (Host, Content-Length,
// Transfer-Encoding, Connection, or any x-faas-*). Per-app
// configurability of the blacklist is deferred to v2.
func ErrHeaderMutationForbidden(name string) *Problem {
	return NewProblem(http.StatusUnprocessableEntity, CodeHeaderMutationForbidden,
		"Header mutation forbidden",
		fmt.Sprintf("the header %q is reserved and cannot be mutated by edge rules.", name))
}

// ErrRequestValidationFailed is the 422 returned by the gateway hot path
// when a kind=validate edge rule rejects the inbound request body. errs
// carries one FieldError per JSON Schema mismatch (Cloudflare / Stripe
// shape: field + expected + got). Title + detail stay stable so SDKs
// that don't yet iterate `errors[]` can render the prose.
func ErrRequestValidationFailed(ruleID string, errs []FieldError) *Problem {
	p := NewProblem(http.StatusUnprocessableEntity, CodeRequestValidationFailed,
		"Invalid request",
		"the request body does not match the validate-edge-rule schema")
	if ruleID != "" {
		// Surface the rule id on the wire so a customer support agent
		// can locate which rule fired without re-reading the audit
		// log. Detail stays unchanged for SDK prose paths.
		p.Detail = fmt.Sprintf("the request body does not match the validate-edge-rule schema (rule %s)", ruleID)
	}
	p.Errors = errs
	return p
}

// ErrInvalidRecoverAction (issue #976 / ADR-122 / SAFE-RELEASES-R)
// is the 422 the recover_rollout handler emits when the request
// body's `action` field is outside the closed set
// {"advance","promote","abort"}. Detail echoes the bad value so
// the CLI renders it. 422 mirrors the shape-error convention used
// by ErrInvalidTrafficPercent (above) — the plan-gate and
// state-machine guards downstream would not trip on this input,
// so 422 (shape) not 403 (plan) or 409 (state).
func ErrInvalidRecoverAction(got string) *Problem {
	return NewProblem(http.StatusUnprocessableEntity, CodeInvalidRecoverAction,
		"Invalid recover_rollout action",
		fmt.Sprintf("recover_rollout.action must be one of {advance, promote, abort}; got %q.", got)).
		WithDocs(docsBase + "/deploys#recover-rollout")
}

// ErrRolloutNotStuck (issue #976 / ADR-122 / SAFE-RELEASES-R) is
// the 409 the recover_rollout handler emits when the operator
// asks for action="advance" on a rollout that is NOT stuck. The
// detail suggests the alternative ("use --action promote
// instead") so the CLI surfaces a usable next step. 409 because
// the request is well-formed but cannot proceed in current
// state.
func ErrRolloutNotStuck() *Problem {
	return NewProblem(http.StatusConflict, CodeRolloutNotStuck,
		"Rollout is not stuck",
		"canary_step_started_at is within the stuck-after window; wait for the rollout to age, or use --action promote to force-step.").
		WithDocs(docsBase + "/deploys#recover-rollout")
}

// ErrRolloutStateInvalid (issue #976 / ADR-122 / SAFE-RELEASES-R)
// is the 409 the recover_rollout handler emits when the
// deployment's rollout_state is 'complete' or 'aborted' and the
// requested recovery cannot proceed. Distinct from
// ErrRolloutNotStuck because the failure mode is "already
// terminal" vs "not stuck yet".
func ErrRolloutStateInvalid(state string) *Problem {
	return NewProblem(http.StatusConflict, CodeRolloutStateInvalid,
		"Rollout state does not permit recovery",
		fmt.Sprintf("rollout_state=%q; recovery requires rollout_state in {pending, rolling_out}.", state)).
		WithDocs(docsBase + "/deploys#recover-rollout")
}
