package api

import (
	"encoding/json"
	"fmt"
	"time"
)

// Wire DTOs for the v1 REST API (spec Appendix A). Defined once here so apid and
// the faas CLI share exactly one contract; `--json` output stability (UX §3.2)
// depends on these shapes.

// CreateAppRequest creates an app or function.
type CreateAppRequest struct {
	Slug           string `json:"slug"`
	Type           string `json:"type,omitempty"`    // "app" (default) | "function"
	Runtime        string `json:"runtime,omitempty"` // node22|python312|go124|go124-alpine for functions
	RAMMB          int    `json:"ram_mb,omitempty"`  // 0 => plan default
	MaxConcurrency int    `json:"max_concurrency,omitempty"`
	IdleTimeoutS   int    `json:"idle_timeout_s,omitempty"`
}

// UpdateAppRequest is the partial-update payload for PATCH /v1/apps/{slug}.
// All fields are pointers so the wire form can distinguish "not set" from
// "set to zero".
type UpdateAppRequest struct {
	RAMMB          *int `json:"ram_mb,omitempty"`
	IdleTimeoutS   *int `json:"idle_timeout_s,omitempty"`
	MaxConcurrency *int `json:"max_concurrency,omitempty"`
	// MinInstances is the per-app cold-wake floor (ux_spec §6.5).
	// 0 / unset => scale to zero; >0 => keep at least this many
	// RUNNING instances alive. Pro/Scale only — Free/Hobby get
	// 403 plan_min_instances_not_allowed (apid gate). Must be <=
	// plan MaxConcurrency (422 invalid_min_instances).
	MinInstances *int `json:"min_instances,omitempty"`
	// EgressAllowlist (ADR-031 + ADR-032, tier-2 of the network
	// roadmap) is the per-app outbound IP allowlist. Each entry is
	// a CIDR string ("1.2.3.0/24" for v4, "2001:db8::/32" for v6);
	// the slice replaces the full list (atomic full-overwrite at the
	// apps row). Plan-gated upstream (Free/Hobby return 403
	// plan_egress_allowlist_not_allowed); size-capped at
	// plan.EgressAllowlistMaxSize() (Pro 16, Scale 64) — v4 + v6
	// entries share the same count budget. Empty slice / nil
	// pointer = clear the allowlist (back to the default-accept
	// chain policy). The non-/0 contract is enforced by the DB
	// trigger `apps_egress_allowlist_cidr` (migration 00033).
	EgressAllowlist *[]string `json:"egress_allowlist,omitempty"`
	// AutoscaleTargetRPS is the per-instance RPS target for the
	// reactive scale-up trigger (issue #169 / #172 / pkg/sched/scaleup).
	// When measured RPS / live_instance_count exceeds this value,
	// schedd admits another instance up to plan.MaxConcurrency. Plan-gated
	// upstream: Free returns 403 CodePlanScaleUpNotAllowed. Hobby/Pro/Scale
	// accept values > 0; values <= 0 return 422 CodeInvalidAutoscaleTargetRPS.
	// Autoscale is "enabled" iff at least one of AutoscaleTargetRPS /
	// AutoscaleTargetCPUPct is non-nil (no separate boolean, per user
	// direction).
	AutoscaleTargetRPS *int `json:"autoscale_target_rps,omitempty"`
	// AutoscaleTargetCPUPct is the per-instance CPU% target (1..100)
	// for the scale-up trigger. Same semantics as AutoscaleTargetRPS
	// but the signal source is pkg/sched/instancestats.Reader (PR #205);
	// nil reader falls back to RPS-only mode (PR #169 never lands the
	// CPU path). Pro/Scale only; Free/Hobby return 403 CodePlanScaleUpNotAllowed.
	// Values outside [1, 100] return 422 CodeInvalidAutoscaleTargetCPUPct.
	AutoscaleTargetCPUPct *int `json:"autoscale_target_cpu_pct,omitempty"`
}

// RenameAppRequest is the body of POST /v1/apps/{slug}/rename (issue #63).
// Validated server-side via the same validSlug regex used at CreateApp
// time; rejected on conflict with 409 CodeAppRenameFailed when another
// live app already holds NewSlug.
type RenameAppRequest struct {
	NewSlug string `json:"new_slug"`
}

// AppResponse is an app as returned by the API.
type AppResponse struct {
	ID             string `json:"id"`
	Slug           string `json:"slug"`
	Type           string `json:"type"`
	Runtime        string `json:"runtime,omitempty"`
	RAMMB          int    `json:"ram_mb"`
	MaxConcurrency int    `json:"max_concurrency"`
	IdleTimeoutS   int    `json:"idle_timeout_s,omitempty"`
	// MinInstances is the per-app cold-wake floor (ux_spec §6.5).
	// 0 => scale to zero; >0 => keep N warm. Pro/Scale only.
	MinInstances int    `json:"min_instances"`
	Status       string `json:"status"`
	URL          string `json:"url"`
	// Manifest is the runner-scaffold payload (env, healthz path,
	// entrypoint). Surfaced so the dashboard's app detail page can
	// show the function handler + env without a separate round-trip.
	// The DTO reuses the existing api.AppManifest (defined in
	// appmanifest.go) so the wire shape stays a single source of truth.
	Manifest AppManifest `json:"manifest"`
	// EgressAllowlist (ADR-031 + ADR-032, tier-2 of the network
	// roadmap) is the per-app outbound CIDR allowlist. Each entry
	// is the canonical CIDR string form: v4 ("1.2.3.0/24") or v6
	// ("2001:db8::/32"). The v4-mapped v6 form ("::ffff:1.2.3.0/120")
	// is silently rewritten to its v4 form at PATCH time by
	// validateUpdateApp, so the read-back never carries a
	// "::ffff:" prefix. Materialised as `[]` (never `null`) at
	// the conversion boundary (cmd/apid/handlers.go::appResponse)
	// so Free / Hobby and pre-PATCH apps always have a predictable
	// JSON shape — the per-netns renderer treats the empty list as
	// "no allowlist rule" (the chain falls back to default-accept).
	// The list is first-seen-wins-dedup'd at write time; the read
	// order matches insertion order. NOT in `required:` because the
	// empty-slice case is the contract.
	EgressAllowlist []string `json:"egress_allowlist"`
	// AutoscaleTargetRPS / AutoscaleTargetCPUPct are the per-app
	// reactive scale-up targets (issue #169 / #172 / pkg/sched/scaleup).
	// Each is 0 when unset ("disabled") and > 0 when configured.
	// Surfaces on GET /v1/apps/{slug} so dashboards can show the
	// current target. Plan-gated upstream.
	AutoscaleTargetRPS    int `json:"autoscale_target_rps"`
	AutoscaleTargetCPUPct int `json:"autoscale_target_cpu_pct"`
}

// CreateDeploymentRequest ships a version (JSON variant; the multipart
// variant is used for tarball/dockerfile deploys).
type CreateDeploymentRequest struct {
	Image string `json:"image,omitempty"` // registry.DOMAIN/...@sha256:...
}

// BuildProvenanceResponse is the public surface of build_provenance
// (ADR-038, Tier 3 / issue #197 B3.10-read half). Field names mirror
// the table columns with snake_case naming so the customer-visible
// JSON stays self-documenting on a `curl`.
//
// Fields are nullable strings; empty values map to "" so the customer
// reads "buildkit_version = \"\"" for a pre-Phase-3 build that the
// populator hasn't back-filled. The dashboard branches on
// `sbom_storage_key != ""` to enable the "Download SBOM" link;
// every other field is observational metadata for audits.
type BuildProvenanceResponse struct {
	ID             string `json:"id"`
	BuildID        string `json:"build_id"`
	BuildkitVer    string `json:"buildkit_version"`
	RailpackVer    string `json:"railpack_version"`
	BaseDigest     string `json:"base_digest"`
	SourceSHA256   string `json:"source_sha256"`
	SourceURL      string `json:"source_url"`
	CommitSHA      string `json:"commit_sha"`
	Plan           string `json:"plan"`
	RunnerDigest   string `json:"runner_digest"`
	BuilderNodeID  string `json:"builder_node_id"`
	StartedAt      string `json:"started_at"`
	FinishedAt     string `json:"finished_at"`
	SBOMStorageKey string `json:"sbom_storage_key"`
}

// DeploymentResponse is a deployment as returned by the API.
type DeploymentResponse struct {
	ID          string `json:"id"`
	AppID       string `json:"app_id"`
	BuildID     string `json:"build_id,omitempty"`
	ImageDigest string `json:"image_digest"`
	Kind        string `json:"kind"`
	Status      string `json:"status"`
	Error       string `json:"error,omitempty"`
	// ErrorCode carries the RFC 7807 code ADR-021 lifted from the
	// puller-side sentinels (image_not_found / image_egress_denied /
	// image_manifest_invalid). Empty for every deployment created
	// before migrations/00021 OR that is not in a failure state —
	// api/state.SerializeDeployment knows the column is a string and
	// that "" is the canonical empty value, so the dashboard /
	// programmatic consumer can branch on ErrorCode != "".
	ErrorCode string `json:"error_code,omitempty"`
	CreatedAt string `json:"created_at"`
}

// AccountResponse is the whoami payload. Limits is the plan's
// quota/limit table (RAM MB, max concurrency, included GB-h,
// deployed-app cap) so the dashboard /account page can show
// "you have X of Y apps" without a second round trip. UsageGBHours
// is the roll-up for the current month (caller-aggregated from
// Store.UsageByHour in apid; included here so the dashboard can
// render the meter in one fetch).
type AccountResponse struct {
	ID            string        `json:"id"`
	Email         string        `json:"email"`
	Plan          string        `json:"plan"`
	Status        string        `json:"status"`
	Limits        AccountLimits `json:"limits"`
	UsageGBHours  float64       `json:"usage_gb_hours"`
	AppCount      int           `json:"app_count"`
	GitHubInstall string        `json:"github_install_id,omitempty"`
}

// AccountLimits is the read-only copy of api.Limits that survives
// serialization. Stripped of fields the dashboard doesn't need
// (eg. internal ops); mirror pkg/api/limits.go for the wiring.
type AccountLimits struct {
	Plan            string `json:"plan"`
	RAMMB           int    `json:"ram_mb"`
	MaxConcurrency  int    `json:"max_concurrency"`
	DeployedApps    int    `json:"deployed_apps"`
	IncludedGBHours int64  `json:"included_gb_hours"`
	AppLayerMaxMB   int    `json:"app_layer_max_mb"`
}

// APIKeyResponse is an API key returned to the customer. The plaintext
// appears ONLY on creation (POST /v1/keys), never on GET — only the prefix
// + label + scopes + last_used_at + id are returned thereafter. Scopes is
// the explicit permission set attached to the key (e.g. ["admin"],
// ["apps:read", "deploy:write"]); see ADR-034 rev2.
type APIKeyResponse struct {
	ID         string   `json:"id"`
	Prefix     string   `json:"prefix"` // "fp_live_abc12345…" (first 16 chars)
	Label      string   `json:"label,omitempty"`
	Scopes     []string `json:"scopes"`
	LastUsedAt string   `json:"last_used_at,omitempty"`
	CreatedAt  string   `json:"created_at"`
	// Plaintext appears ONLY on the create response, never persisted.
	Plaintext string `json:"plaintext,omitempty"`
}

// CreateKeyRequest is the body of POST /v1/keys. Label is optional
// (max 100 chars per spec); empty label is allowed and renders as
// `{}` so the server's optional-field handling stays in scope. Scopes
// is the requested permission set; the server validates each entry
// against the closed vocabulary (admin, apps:read, deploy:write,
// secrets:read, secrets:write, usage:read) and defaults to
// ["admin"] when omitted so existing callers keep full access. See
// ADR-034 rev2.
type CreateKeyRequest struct {
	Label  string   `json:"label,omitempty"`
	Scopes []string `json:"scopes,omitempty"`
}

// CustomDomainResponse is a custom domain's wire shape. VerifiedAt is the
// zero time on unverified rows; the verifier goroutine polls DNS and updates
// it (spec §7).
type CustomDomainResponse struct {
	Domain         string `json:"domain"`
	AppID          string `json:"app_id"`
	ChallengeToken string `json:"challenge_token,omitempty"`
	Verified       bool   `json:"verified"`
	VerifiedAt     string `json:"verified_at,omitempty"`
	TXTRecord      string `json:"txt_record,omitempty"` // convenience for the customer
}

// CreateCustomDomainRequest accepts a domain to bind.
type CreateCustomDomainRequest struct {
	Domain string `json:"domain"`
	AppID  string `json:"app_id"`
}

// CronResponse mirrors the crons table. LastFiredAt is the most
// recent fire stamp schedd wrote (MarkCronFired). Zero-valued
// crons serialize as "" — the dashboard only shows the column
// when populated.
type CronResponse struct {
	ID          string `json:"id"`
	AppID       string `json:"app_id"`
	Schedule    string `json:"schedule"`
	Path        string `json:"path"`
	Enabled     bool   `json:"enabled"`
	CreatedAt   string `json:"created_at"`
	LastFiredAt string `json:"last_fired_at,omitempty"`
}

// CreateCronRequest creates a scheduled synthetic POST.
type CreateCronRequest struct {
	AppID    string `json:"app_id"`
	Schedule string `json:"schedule"`
	Path     string `json:"path,omitempty"`
	Enabled  *bool  `json:"enabled,omitempty"`
}

// UpdateCronRequest is a partial update.
type UpdateCronRequest struct {
	Schedule *string `json:"schedule,omitempty"`
	Path     *string `json:"path,omitempty"`
	Enabled  *bool   `json:"enabled,omitempty"`
}

// InstanceResponse is the read-only instance view (spec §4.2 / §6).
type InstanceResponse struct {
	ID            string `json:"id"`
	AppID         string `json:"app_id"`
	DeploymentID  string `json:"deployment_id"`
	State         string `json:"state"`
	HostIP        string `json:"host_ip,omitempty"`
	RAMMB         int    `json:"ram_mb"`
	StartedAt     string `json:"started_at,omitempty"`
	LastRequestAt string `json:"last_request_at,omitempty"`
	ParkedAt      string `json:"parked_at,omitempty"`
	// WakeID is the per-wake stable identifier minted by schedd at
	// CreateInstance time (UUIDv7). Distinct from `id` (the row PK):
	// one row can carry many WakeIDs over its lifetime as the app is
	// parked and re-woken. Surfaced on `faas ps` and the dashboard
	// detail page so operators can correlate the request that woke
	// the app against gateway logs and slog entries (which also
	// carry this field).
	WakeID string `json:"wake_id,omitempty"`
}

// UsageResponse is one app's monthly usage slice (spec §10).
type UsageResponse struct {
	AppID     string `json:"app_id"`
	MBSeconds int64  `json:"mb_seconds"`
	Requests  int64  `json:"requests"`
	// IncludedGBHours is the included quota for the account's plan at the
	// requested month; the CLI computes the overage from this and the rows.
	IncludedGBHours int64 `json:"included_gb_hours"`
}

// DeploymentListResponse is the page shape for GET /v1/deployments.
// Items is the page (in created_at DESC order); NextBefore is the
// cursor the caller should pass on the next request to page BACKWARDS
// (the dashboard's "older deploys" link). Empty NextBefore means the
// page is the end of the list.
//
// Cursor format: RFC3339Nano (matches state.Deployment.CreatedAt).
type DeploymentListResponse struct {
	Items      []DeploymentResponse `json:"items"`
	NextBefore string               `json:"next_before,omitempty"`
}

// --- Dashboard auth (issue #165, ADR-032 PR #2) ----------------------------

// OAuthProvider is the issuer name used by the dashboard OAuth flows
// (the email/identity brokers). The set is intentionally closed — adding
// a new provider is a Store + handler + OpenAPI change, not a config
// flag. "google" and "github" are wired in PR #2.
type OAuthProvider string

const (
	OAuthProviderGoogle OAuthProvider = "google"
	OAuthProviderGitHub OAuthProvider = "github"
)

// PasswordLoginRequest is the body of POST /login. The email is the
// canonical handle (lowercase + trim — the handler runs the same
// canonicalisation the account-create path uses so an "alice@example.com
// vs ALICE@example.com" login pair collapses to one row). Password is
// the plaintext the client sent over TLS; the Argon2id verify is in
// pkg/auth.Verify and runs on the server only.
type PasswordLoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// PasswordLoginResponse is what POST /login (and POST /signup) return
// on success. The session cookie rides on the Set-Cookie header — the
// body deliberately carries NO api_key field. Pre-#165 (PR #1) the
// response minted a "web-console" key and returned it in the body; that
// was the takeover surface. The SDK path is the device-code CLI
// (MintCliAuthCode / ExchangeCliAuthCode), not a login-bundled key, so
// removing the field here doesn't break programmatic auth.
type PasswordLoginResponse struct {
	AccountID string `json:"account_id"`
	Plan      string `json:"plan"`
}

// PasswordSignupRequest is the body of POST /signup. Same shape as
// PasswordLoginRequest — we accept the same argon2id-shaped ciphertext
// at signup and re-verify at login, so the handler-side error
// equivalence ("wrong password" vs "no account" vs "weak password") is
// kept intact under the same JSON keys.
type PasswordSignupRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// PasswordResetRequest is the body of POST /login/forgot. The email
// is optional — the same-shape internal handler is hit by the form
// page (no body) and the SDK (email in body). The handler always
// returns 200 with an identical body and identical timing whether or
// not the email exists, so the surface does not leak account presence.
type PasswordResetRequest struct {
	Email string `json:"email,omitempty"`
}

// PasswordResetConfirm is the body of POST /auth/reset. Token is the
// 32-byte value the email link carried (base64url-encoded, NOT the
// SHA-256 hash the server stored). NewPassword is the plaintext the
// user is opting into; the server Argon2id-encodes it server-side and
// runs ConsumeLoginToken atomically so a replay returns 410.
type PasswordResetConfirm struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}

// SetPasswordRequest is the body of POST /dashboard/account/set-password.
// Lets OAuth-only users opt into password login. Same shape as the
// reset-confirm NewPassword field — the handler runs auth
// (sessionAuth) before encoding, so this is an authenticated surface.
type SetPasswordRequest struct {
	Password string `json:"password"`
}

// UsageSummaryResponse is the roll-up for the current month (or any
// month passed as a query param). Used by the dashboard usage page so
// the customer sees a single number ("used X of Y GB-h, overage $Z")
// without having to sum rows.
//
// Overage math: anything above IncludedGBHours is billable at the
// overage rate in the financial model (€0.01/GB-h). Cents are integer.
type UsageSummaryResponse struct {
	Month           string  `json:"month"`             // YYYY-MM
	UsedGBHours     float64 `json:"used_gb_hours"`     // Σ mb_seconds / 3_600_000
	IncludedGBHours int64   `json:"included_gb_hours"` // from plan limits
	OverageGBHours  float64 `json:"overage_gb_hours"`  // max(0, used - included)
	OverageCents    int64   `json:"overage_cents"`     // overage * 1.0 (€0.01/GB-h in cents)
}

// ValidateAppConfig checks a requested app config against its plan caps (spec
// §4.2: validation before work). It returns the first violating *Problem, or nil.
// The deployed-app COUNT check is done in apid (it needs the store).
func ValidateAppConfig(l Limits, ramMB, maxConcurrency int) *Problem {
	if ramMB > l.RAMMB {
		return ErrPlanLimitRAM(l, ramMB)
	}
	if maxConcurrency > l.MaxConcurrency {
		return NewProblem(403, CodePlanLimitConcur,
			"Concurrency over plan limit",
			fmt.Sprintf("%s plan caps max_concurrency at %d; requested %d.", l.Plan, l.MaxConcurrency, maxConcurrency)).
			WithLimit(int64(l.MaxConcurrency), int64(maxConcurrency)).
			WithDocs("https://docs.DOMAIN/plans#concurrency")
	}
	return nil
}

// --- G6 account self-service (spec §17 G6, ADR-021) -------------------------

// AccountExportResponse is the GET /v1/account/export bundle. A
// single JSON document with one slice per resource type the customer
// owns (apps, deployments, builds, instances, usage, domains, crons,
// API keys, app_secrets). Ciphertext passthrough for the secrets
// slice — the plaintext VALUE never lands in PG (ADR-020), so the
// customer can rotate their host age key after a restore-from-export
// without losing the per-secret envelope.
type AccountExportResponse struct {
	ExportedAt  string                    `json:"exported_at"`
	Account     AccountResponse           `json:"account"`
	Apps        []AppResponse             `json:"apps"`
	Deployments []DeploymentResponse      `json:"deployments"`
	Builds      []BuildExportResponse     `json:"builds"`
	Instances   []InstanceResponse        `json:"instances"`
	Usage       []UsageExportResponse     `json:"usage"`
	Domains     []CustomDomainResponse    `json:"domains"`
	Crons       []CronResponse            `json:"crons"`
	APIKeys     []APIKeyExportResponse    `json:"api_keys"`
	AppSecrets  []AppSecretExportResponse `json:"app_secrets"`
	// AuditTrail is the customer's own GDPR ledger slice: every
	// export/delete/restore the customer has hit. Surfaced in the
	// bundle so the export is self-describing (the customer can see
	// "yes, my last deletion request fired at <ts>") without a
	// separate GET round trip.
	AuditTrail []GdprAuditExportResponse `json:"audit_trail,omitempty"`
}

// BuildExportResponse is the per-build row in the export bundle.
// Reduced shape (no internal IDs the customer can't act on).
type BuildExportResponse struct {
	ID           string `json:"id"`
	DeploymentID string `json:"deployment_id"`
	AppID        string `json:"app_id"`
	Kind         string `json:"kind"`
	Status       string `json:"status"`
	SourceBytes  int64  `json:"source_bytes"`
	StartedAt    string `json:"started_at,omitempty"`
	FinishedAt   string `json:"finished_at,omitempty"`
}

// UsageExportResponse is the per-month roll-up in the export bundle.
// `month` is YYYY-MM (matches the dashboard's usage page render).
type UsageExportResponse struct {
	AppID     string `json:"app_id"`
	Month     string `json:"month"`
	MBSeconds int64  `json:"mb_seconds"`
	Requests  int64  `json:"requests"`
}

// APIKeyExportResponse is one row in the export's API key slice.
// The plaintext key never appears here (and never reappears after
// the create response, per §4.2). Only the prefix + label + scopes +
// timestamps. Scopes is included so the customer's GDPR export carries
// the full audit trail of which keys had which permissions at the
// moment of export (ADR-034 rev2).
type APIKeyExportResponse struct {
	ID        string   `json:"id"`
	Prefix    string   `json:"prefix"`
	Label     string   `json:"label,omitempty"`
	Scopes    []string `json:"scopes"`
	CreatedAt string   `json:"created_at"`
	LastUsed  string   `json:"last_used_at,omitempty"`
}

// GdprAuditExportResponse is one row of the customer's own audit trail
// as surfaced in the export bundle. Two row kinds live here:
//
//   - source="gdpr"   — a self-service GDPR action (export/delete/restore
//     from the gdpr_requests table). Action is "export" | "delete" |
//     "restore"; CompletedAt is empty when the action is still in
//     flight.
//   - source="event"  — a security event from the events table (IAM-4,
//     ADR-035). Kind is the namespaced event kind (e.g. "auth.login",
//     "key.created"); Data is the original jsonb payload.
//
// Rows from both sources are interleaved by timestamp descending in
// the bundle so a reviewer sees one ordered timeline. Existing GDPR
// consumers can ignore unknown fields per the standard JSON rule.
type GdprAuditExportResponse struct {
	Source      string          `json:"source"`           // "gdpr" | "event"
	Action      string          `json:"action,omitempty"` // "export" | "delete" | "restore" (gdpr)
	RequestedAt string          `json:"requested_at"`     // RFC 3339 (event.at for source="event")
	CompletedAt string          `json:"completed_at,omitempty"`
	Kind        string          `json:"kind,omitempty"` // auth.*|key.*|account.*|secret.* (event)
	Data        json.RawMessage `json:"data,omitempty"` // kind-specific payload (event)
}

// AppSecretExportResponse is one row in the export's app_secrets slice.
// Ciphertext is the age-sealed envelope (base64). Plaintext never
// lands here — the customer imports the envelope into another faas
// install (or their own age tool) to unseal.
type AppSecretExportResponse struct {
	AppID      string `json:"app_id"`
	Key        string `json:"key"`
	Ciphertext string `json:"ciphertext"` // base64
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

// AccountDeletionResponse is the response from DELETE /v1/account
// (and the same shape is replayed on every repeat call — the
// idempotent endpoint guarantees the response body is identical
// across retries inside the 24 h window).
type AccountDeletionResponse struct {
	Status       string `json:"status"`        // always "deleted_pending"
	ScheduledAt  string `json:"scheduled_at"`  // deletion_requested_at, RFC 3339
	RestoreUntil string `json:"restore_until"` // scheduled_at + 30 d, RFC 3339
}

// StatusPage is the JSON shape served by GET /status/slo.json (spec
// §12, M8 acceptance). Lives in pkg/api so the CLI can import it
// without a back-reference into cmd/apid; cmd/apid/status.go embeds
// the same JSON tags so the wire shape stays identical.
//
// Fields are documented in deploy/statuspage/index.html; renames here
// must propagate to that file (and to the statusCache JSON encoder in
// cmd/apid/status.go).
type StatusPage struct {
	// APIAvailabilityPct is the rolling 5-minute 2xx rate over
	// gateway_requests_total, expressed 0..100.
	APIAvailabilityPct float64 `json:"api_availability_pct"`
	// WakeP95MS is the p95 of gateway_wake_latency_seconds over the
	// last 5 minutes, in milliseconds.
	WakeP95MS float64 `json:"wake_p95_ms"`
	// BuildSuccessPct is the rolling 5-minute success rate of
	// builderd builds (completed/success ÷ (completed/success +
	// completed/failure)).
	BuildSuccessPct float64 `json:"build_success_pct"`
	// Degraded is true when at least one page- or warn-severity alert
	// is currently firing on the local Prometheus. The public status
	// page renders a "degraded" pill when this is true so prospects
	// and customers see the same picture the operator's pager sees.
	//
	// The flag is intentionally conservative: a transient PromQL
	// error against ALERTS{} is treated as "no firing alerts" rather
	// than poisoning the snapshot. Prometheus being completely
	// unreachable still surfaces via Source = "degraded: <reason>"
	// (the pre-existing contract).
	Degraded bool `json:"degraded"`
	// AsOf is the UTC timestamp the snapshot was taken. The HTML
	// renders "Updated 3 min ago" off this.
	AsOf time.Time `json:"as_of"`
	// Source is "prometheus", "degraded: firing alerts", or
	// "degraded: <reason>" so an operator tailing the JSON can tell
	// at a glance why a snapshot is or isn't trustworthy.
	Source string `json:"source"`
}

// --- Move 2: event-driven surface response shapes ----------------------------
//
// AsyncInvokeResponse is the 202-side of POST /v1/apps/{slug}/invoke/async.
// StatusURL is the well-known read endpoint so the dashboard (and the
// SDK) can poll without parsing the id.
type AsyncInvokeResponse struct {
	ID        string `json:"id"`
	StatusURL string `json:"status_url"`
}

// InvokeResponse is the sync-side of POST /v1/apps/{slug}/invoke.
// Status is the final row state (one of "completed" | "failed"
// | "cancelled"); Result is the per-app payload the drain stamped
// (nil while pending, populated by drain.emitDone).
type InvokeResponse struct {
	ID     string          `json:"id"`
	Status string          `json:"status"`
	Result json.RawMessage `json:"result,omitempty"`
}

// QueueSendResponse is returned on POST /v1/apps/{slug}/queues/invocations:send.
// 201 Created with the new id; the customer pairs this with the
// /receive long-poll.
type QueueSendResponse struct {
	ID string `json:"id"`
}

// QueueReceiveResponse is returned on POST /v1/apps/{slug}/queues/invocations:receive.
// 200 with the dequeued row's payload + result; 204 on timeout.
type QueueReceiveResponse struct {
	ID      string          `json:"id"`
	Payload json.RawMessage `json:"payload"`
	Result  json.RawMessage `json:"result,omitempty"`
}

// DelayedTaskResponse is the create/get shape for delayed tasks.
// ScheduledAt is the customer-facing UTC dispatch time; State is
// populated on get, omitted on create (always "pending" there).
type DelayedTaskResponse struct {
	ID          string    `json:"id"`
	ScheduledAt time.Time `json:"scheduled_at"`
	State       string    `json:"state,omitempty"`
}

// ListInvocationsResponse lives in cmd/apid because pkg/api cannot
// import pkg/state (cyclic). The handler-local type is `[]state.Invocation`
// — the wire shape is identical, only the package differs.

// InvokeRequest is the body for POST /v1/apps/{slug}/invoke[/async].
// Method defaults to POST; path defaults to `/` (the handler fills
// defaults; the zero values are not persisted).
type InvokeRequest struct {
	Payload json.RawMessage `json:"payload,omitempty"`
	Headers json.RawMessage `json:"headers,omitempty"`
	Method  string          `json:"method,omitempty"`
	Path    string          `json:"path,omitempty"`
}

// QueueSendRequest is the body for POST /v1/apps/{slug}/queues/send.
// Cap-checked against MaxQueueDepth at the handler.
type QueueSendRequest struct {
	Payload json.RawMessage `json:"payload,omitempty"`
}

// DelayedTaskRequest is the body for POST /v1/apps/{slug}/delayed-tasks.
// ScheduledAt must be in the future (UTC); the handler rejects past
// timestamps with invalid_scheduled_at.
type DelayedTaskRequest struct {
	Payload     json.RawMessage `json:"payload,omitempty"`
	ScheduledAt time.Time       `json:"scheduled_at"`
}

// Invocation is the SDK-side mirror of state.Invocation. The wire
// is the same JSON the handler emits (writeJSON(w, 200, inv) where
// inv is a state.Invocation), but pkg/api cannot import pkg/state
// (import cycle — state pkg is the lowest layer). The mirror is
// exhaustive: every field with a JSON tag on state.Invocation gets a
// typed row here so the SDK gets proper Go types and JSON tags. The
// name `Invocation` matches the OpenAPI schema (api/openapi.yaml
// `Invocation`) so the spec_compliance test sees a 1:1 mapping.
type Invocation struct {
	ID             string          `json:"id"`
	AppID          string          `json:"app_id"`
	AccountID      string          `json:"account_id"`
	InstanceID     string          `json:"instance_id,omitempty"`
	Source         string          `json:"source"`
	State          string          `json:"state"`
	Method         string          `json:"method"`
	Path           string          `json:"path"`
	Payload        json.RawMessage `json:"payload"`
	Headers        json.RawMessage `json:"headers"`
	DueAt          time.Time       `json:"due_at"`
	ScheduledAt    *time.Time      `json:"scheduled_at,omitempty"`
	AckURL         string          `json:"ack_url,omitempty"`
	Result         json.RawMessage `json:"result,omitempty"`
	LeaseExpiresAt *time.Time      `json:"lease_expires_at,omitempty"`
	ReceivedAt     *time.Time      `json:"received_at,omitempty"`
	CompletedAt    *time.Time      `json:"completed_at,omitempty"`
	Attempts       int             `json:"attempts"`
	LastError      string          `json:"last_error,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
}

// ListInvocationsResponse is the wire shape for GET /v1/invocations.
// The handler emits a `[]state.Invocation` under the `invocations`
// key; here we declare the same shape with the SDK-side mirror type
// so pkg/api stays decoupled from pkg/state.
type ListInvocationsResponse struct {
	Invocations []Invocation `json:"invocations"`
}

// --- IAM-4 (ADR-035) — auth audit event surface -----------------------------
//
// AuditEventResponse is one row of the customer's own security event
// timeline. The kind taxonomy is documented in
// docs/adr/035-auth-audit-events.md; common values include
// "auth.login", "auth.logout", "key.created", "key.deleted",
// "secret.set", "secret.deleted", "account.plan_changed",
// "account.deletion_scheduled", "account.deletion_restored".
//
// Subject is the account_id the event was recorded against (string,
// not the raw uuid UUID type — pkg/api stays string-typed for wire
// stability). Data is the raw jsonb the apid auditor wrote; the schema
// varies by kind and is documented per-kind in the ADR.
type AuditEventResponse struct {
	ID      string          `json:"id"`    // bigint as string
	At      string          `json:"at"`    // RFC 3339
	Actor   string          `json:"actor"` // "apid" today; "schedd" for state-transition events
	Kind    string          `json:"kind"`
	Subject string          `json:"subject,omitempty"` // account_id (uuid string form)
	Data    json.RawMessage `json:"data"`
}

// ListAuditEventsResponse is the wire shape for GET /v1/audit-events.
// Limit echoes the effective limit applied by the handler (capped at
// 100), so the SDK can display "showing 50 of N" without re-issuing
// the request.
type ListAuditEventsResponse struct {
	Events []AuditEventResponse `json:"events"`
	Limit  int                  `json:"limit"`
}
