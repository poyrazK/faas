package api

import (
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
	Runtime        string `json:"runtime,omitempty"` // node22|python312 for functions
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
}

// CreateDeploymentRequest ships a version (JSON variant; the multipart
// variant is used for tarball/dockerfile deploys).
type CreateDeploymentRequest struct {
	Image string `json:"image,omitempty"` // registry.DOMAIN/...@sha256:...
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
// + label + last_used_at + id are returned thereafter.
type APIKeyResponse struct {
	ID         string `json:"id"`
	Prefix     string `json:"prefix"` // "fp_live_abc12345…" (first 16 chars)
	Label      string `json:"label,omitempty"`
	LastUsedAt string `json:"last_used_at,omitempty"`
	CreatedAt  string `json:"created_at"`
	// Plaintext appears ONLY on the create response, never persisted.
	Plaintext string `json:"plaintext,omitempty"`
}

// CreateKeyRequest is the body of POST /v1/keys. Label is optional
// (max 100 chars per spec); empty label is allowed and renders as
// `{}` so the server's optional-field handling stays in scope.
type CreateKeyRequest struct {
	Label string `json:"label,omitempty"`
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
// the create response, per §4.2). Only the prefix + label + timestamps.
type APIKeyExportResponse struct {
	ID        string `json:"id"`
	Prefix    string `json:"prefix"`
	Label     string `json:"label,omitempty"`
	CreatedAt string `json:"created_at"`
	LastUsed  string `json:"last_used_at,omitempty"`
}

// GdprAuditExportResponse is one row of the customer's own GDPR audit
// trail as surfaced in the export bundle. CompletedAt is empty when
// the action is still in flight (a delete row whose grace window
// hasn't lapsed yet); the export consumer can correlate the row
// against the deletion_requested_at timestamp on the Account envelope.
type GdprAuditExportResponse struct {
	Action      string `json:"action"` // "export" | "delete" | "restore"
	RequestedAt string `json:"requested_at"`
	CompletedAt string `json:"completed_at,omitempty"`
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
	// AsOf is the UTC timestamp the snapshot was taken. The HTML
	// renders "Updated 3 min ago" off this.
	AsOf time.Time `json:"as_of"`
	// Source is "prometheus" or "degraded: <reason>" so an
	// operator tailing the JSON can tell at a glance.
	Source string `json:"source"`
}
