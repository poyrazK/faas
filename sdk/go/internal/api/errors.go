package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// docsBase is the canonical documentation URL prefix for SDK-side
// problem constructors. Duplicated from pkg/wire.DocsHost in the
// root module because the SDK is a separate Go module with its own
// go.mod (sdk/go/) and cannot import the root module's pkg. Keep
// in lock-step with the root module's pkg/wire/docs.go — the
// docs are not auto-rotated, so a future host rotation in the
// root module must also edit this constant. The tripwire
// TestLintTripwire_NoLiteralDocsDomainEverywhere in the root
// module's cmd/gregale does NOT cover the SDK; verify manually
// when the host rotates.
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
	// upgrade. PaddleCheckoutURL remains below for older SDK compatibility.
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
	// Hint is the single short next-action line shown on the CLI's
	// 3-line renderer (spec §6.4 amendment 1). Mirrors SecretHint
	// shape — a one-line remediation nudge, omitempty so every
	// other problem+json site keeps its existing flat shape
	// unchanged. Distinct from Detail: Detail is the platform's
	// machine-stable message; Hint is the human-readable
	// one-liner surfaced only on error UX paths.
	Hint string `json:"hint,omitempty"`
	// Why is the cause with the observed value. Multi-line ok (e.g.
	// "bound to 127.0.0.1; guest at 10.0.0.2 only sees requests
	// proxied via the bridge"). Distinct from Detail: Detail is
	// the platform's machine-stable message; Why is the
	// human-readable explanation surfaced only on error UX paths.
	Why string `json:"why,omitempty"`
	// Fix is the prescriptive remediation (e.g. "set
	// `app.listen('0.0.0.0')` or run `gregale env set PORT 8080`").
	// Distinct from Hint: Hint is a single line, Fix may be 1-3
	// lines.
	Fix string `json:"fix,omitempty"`
	// RelevantLogs are the last N log lines that explain the
	// failure, surfaced inline by the CLI renderer when the server
	// attaches them. Capped at 20 entries × 512 bytes each (CLI
	// tripwire).
	RelevantLogs []LogExcerpt `json:"relevant_logs,omitempty"`
}

// LogExcerpt is a single log line attached to an error explanation
// (spec §6.4 amendment 1). Mirrors pkg/api.LogExcerpt — the SDK
// is a separate Go module that cannot import the root module's
// pkg, so the shape is duplicated here. Keep in lock-step with
// the root module's pkg/api/errors.go (the regen via `make
// sdk-gen-go` is hand-rolled since the SDK is a separate module).
//
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
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
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

// Stable error codes (spec Appendix A, UX spec §7). Keep in sync with docs and
// the CLI's exit-code mapping.
const (
	CodePlanLimitApps   = "plan_limit_apps"
	CodePlanLimitRAM    = "plan_limit_ram"
	CodePlanLimitConcur = "plan_limit_concurrency"
	CodeSourceTooLarge  = "source_too_large"
	CodeSourceInvalid   = "source_invalid"
	CodeAppLayerTooBig  = "app_layer_too_large"
	CodeBuildUndetected = "build_undetected"
	CodeBuildOOM        = "build_oom"
	CodeBuildTimeout    = "build_timeout"
	CodeQuotaExhausted  = "quota_exhausted"
	CodeBillingPastDue  = "billing_past_due"
	CodeCapacity        = "capacity_unavailable"
	CodeUnauthorized    = "unauthorized"
	// CodeForbidden is returned when the authenticated principal lacks
	// the scope required by the route (IAM-1, ADR-034). Distinct from
	// CodeUnauthorized so a customer can tell "I need to log in" from
	// "my key does not have permission for this endpoint".
	CodeForbidden         = "insufficient_scope"
	CodeNotFound          = "not_found"
	CodeValidation        = "validation_failed"
	CodeConflict          = "conflict"
	CodeDomainNotVerified = "domain_not_verified"
	CodeCronInvalid       = "cron_invalid"
	CodeHandlerMissing    = "handler_missing"
	CodeImageRequired     = "image_required"
	CodeDeployFailed      = "deploy_failed"
	CodeNoRollbackTarget  = "no_rollback_target"

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

	// Sidecar containers (issue #463 / ADR-068). Eight RFC 7807
	// codes for the sidecar surface. The cap and type-uniqueness
	// codes are the load-bearing 400-class shapes; the stateful
	// and not-on-plan codes are defence-in-depth for future
	// per-plan tier-ups. PR-A mirrors the codes here so the
	// hand-curated sdk-go subset keeps the Go SDK compile green.
	// Mirrored in pkg/api/errors.go (the source of truth).
	CodeSidecarCapExceeded      = "sidecar_cap_exceeded"
	CodeSidecarInvalidType      = "sidecar_invalid_type"
	CodeSidecarInvalidImage     = "sidecar_invalid_image"
	CodeSidecarStatefulDenied   = "sidecar_stateful_denied"
	CodeSidecarInvalidName      = "sidecar_invalid_name"
	CodeSidecarInvalidPort      = "sidecar_invalid_port"
	CodeSidecarInvalidRamMB     = "sidecar_invalid_ram_mb"
	CodeSidecarNotAllowedOnPlan = "sidecar_not_allowed_on_plan"

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

	// Move 1 event-shaped surfaces (spec §4.4, §4.9). The CLI exit-code
	// table treats them as 403/422/402; surfacing the codes separately
	// lets the dashboard render a "move to Scale to lift the cap"
	// hint without parsing prose.
	CodePlanQueueDepth     = "plan_queue_depth"
	CodePlanSourceBytes    = "plan_source_bytes"
	CodePlanFeatureGated   = "plan_feature_gated"
	CodePlanDelayedCap     = "plan_delayed_tasks_cap"
	CodeInvocationNotFound = "invocation_not_found"

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

	// Issue #679 / PR-B / ADR-082 — per-account additive budget on
	// top of the plan's apps.egress_allowlist cap. Out-of-range
	// PATCH (negative or > the global MaxAccountEgressAllowlistExtra
	// ceiling) reports this code so the CLI can render the
	// "max_extra=1024" hint without parsing the problem detail.
	CodeAccountEgressAllowlistExtraOutOfRange = "account_egress_allowlist_extra_out_of_range"

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

	// IAM-5 API-key lifecycle (issue #189). Distinct from the
	// secret/env surface because the lifecycle diverges: secrets
	// and env vars are config rows, API keys are revocable
	// credentials with a status state machine (active | grace |
	// revoked) and a per-account grace override. Three codes:
	//
	//   * CodeAPIKeyExpired = 401, the bearer key has expired
	//     (auth-time gate; the auth middleware emits key.expired).
	//   * CodeAPIKeyRevoked = 401, the key is in status='revoked'
	//     (manual delete, rotation atomic, or lazy-expiry).
	//   * CodeAPIKeyLimitExceeded = 409, the per-account cap
	//     (Plan.KeysMax, IAM-5) is reached.
	CodeAPIKeyExpired       = "api_key_expired"
	CodeAPIKeyRevoked       = "api_key_revoked"
	CodeAPIKeyLimitExceeded = "api_key_limit_exceeded"

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

	// CodeRateLimited is the wire string the authlimiter middleware
	// emits on a 429. It is plain-text, not a Problem-shaped body, so
	// the SDK's *APIError.Unwrap path uses it as the lookup key when
	// surfacing the rate-limited sentinel — see apierror.go::Unwrap.
	CodeRateLimited = "rate_limited"
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

// StatusForCode returns the HTTP status a given stable Code maps to. It is the
// inverse of the per-code status the constructors below hardcode, kept in one
// table so any surface that reconstructs a Problem without a Status (notably
// pkg/grpcerr.FromStatus, which lifts a gRPC error back into a Problem carrying
// only the Code) can recover the right HTTP status. Unknown codes default to
// 500 — a reconstructed Problem is never served without a real status.
func StatusForCode(code string) int {
	switch code {
	case CodePlanLimitApps, CodePlanLimitRAM, CodeAppLayerTooBig, CodeBillingPastDue:
		return http.StatusForbidden
	case CodePlanLimitConcur, CodeQuotaExhausted, CodeAppConcurReached:
		return http.StatusTooManyRequests
	case CodeSourceTooLarge:
		return http.StatusRequestEntityTooLarge
	case CodeSourceInvalid, CodeBuildUndetected, CodeValidation, CodeCronInvalid,
		CodeHandlerMissing, CodeImageRequired:
		return http.StatusBadRequest
	case CodeCapacity, CodeBuildOOM, CodeBuildTimeout:
		return http.StatusServiceUnavailable
	case CodeUnauthorized:
		return http.StatusUnauthorized
	case CodeNotFound:
		return http.StatusNotFound
	case CodeConflict, CodeDomainNotVerified, CodeNoRollbackTarget:
		return http.StatusConflict
	case CodeDeployFailed:
		return http.StatusUnprocessableEntity
	case CodeImageNotFound, CodeImageManifestInvalid:
		return http.StatusUnprocessableEntity
	case CodeImageEgressDenied:
		return http.StatusForbidden
	case CodePayment:
		return http.StatusPaymentRequired
	case CodePlanLimitSecrets:
		return http.StatusForbidden
	case CodeSecretInvalidKey, CodeSecretNotFound:
		return http.StatusBadRequest
	case CodeSecretValueTooLarge:
		return http.StatusRequestEntityTooLarge
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
	case CodeInvocationNotFound:
		return http.StatusNotFound
	case CodeInvalidCredentials, CodeEmailNotVerified:
		return http.StatusUnauthorized
	case CodePasswordTooWeak, CodeAccountExists:
		return http.StatusBadRequest
	case CodeResetTokenInvalid, CodeResetTokenExpired:
		return http.StatusGone
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
		WithDocs("https://status.gregale.dev")
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

// ErrDomainNotVerified is returned when a customer tries to bind a domain
// whose TXT challenge hasn't been satisfied yet (spec §7).
func ErrDomainNotVerified(domain string) *Problem {
	return NewProblem(http.StatusConflict, CodeDomainNotVerified,
		"Domain not verified",
		fmt.Sprintf("TXT challenge for %q not yet satisfied; publish the required TXT record and retry.", domain)).
		WithDocs(docsBase + "/domains/verify")
}

// ErrCronInvalid is returned for malformed cron expressions.
func ErrCronInvalid(reason string) *Problem {
	return NewProblem(http.StatusBadRequest, CodeCronInvalid,
		"Invalid cron schedule", reason).
		WithDocs(docsBase + "/crons")
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

// ErrNoRollbackTarget is returned by POST /v1/apps/{slug}/rollback when no
// superseded deployment exists (spec §9 line 376).
func ErrNoRollbackTarget() *Problem {
	return NewProblem(http.StatusConflict, CodeNoRollbackTarget,
		"No previous deployment",
		"there's no superseded deployment to roll back to; deploy at least twice.").
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

// ErrPlanMinInstancesNotAllowed is returned when a Free or Hobby account
// tries to set apps.min_instances (ux_spec §6.5, plan-tier gate). The
// customer's bill on these plans is built around scale-to-zero; a
// floor keeps N × RAMMB resident at all times, which is the cost
// shape of Pro / Scale.
func ErrPlanMinInstancesNotAllowed(p Plan) *Problem {
	return NewProblem(http.StatusForbidden, CodePlanMinInstancesNotAllowed,
		"Plan doesn't allow a min-instances floor",
		fmt.Sprintf("the %s plan always scales to zero; upgrade to Pro or Scale to keep instances warm.", p)).
		WithDocs(docsBase + "/plans#min-instances")
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

// ErrAccountEgressAllowlistExtraOutOfRange (issue #679 / PR-B /
// ADR-082) is a 400 for PATCH /v1/account/egress_allowlist_extra
// values outside [0, MaxAccountEgressAllowlistExtra]. The
// MaxAccountEgressAllowlistExtra value is the same global ceiling
// the server enforces (flat 1024 — see pkg/api/dto.go).
func ErrAccountEgressAllowlistExtraOutOfRange(got, maxExtra int) *Problem {
	return NewProblem(http.StatusBadRequest, CodeAccountEgressAllowlistExtraOutOfRange,
		"egress_allowlist_extra out of range",
		fmt.Sprintf("extra=%d is outside [0, %d]; clear with extra=0 to fall back to the plan cap.", got, maxExtra)).
		WithLimit(int64(maxExtra), int64(got)).
		WithDocs(docsBase + "/account#egress-allowlist-extra")
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

// ErrInvalidRegistryHost is the 400 returned when the request body's
// registry field fails the normalized-host gate (lowercase DNS[:port],
// no scheme/path). Wrapping the underlying detail keeps the specific
// failure visible to the CLI without leaking the input verbatim into
// a 5xx. Mirrors pkg/api/errors.go::ErrInvalidRegistryHost for SDK-side
// validation (issue #461 / ADR-064).
func ErrInvalidRegistryHost(detail error) *Problem {
	return NewProblem(http.StatusBadRequest, "invalid_registry_host",
		"Invalid registry host", detail.Error()).
		WithDocs(docsBase + "/registry-credentials#registry-format")
}
