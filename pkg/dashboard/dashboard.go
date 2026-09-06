// Package dashboard holds the server-rendered dashboard surface that
// apid exposes for M7.5 (ADR-011). The decision is to ship a thin Go
// html/template + HTMX shell — no SPA build chain, no JS framework —
// so the whole funnel fits inside the 6 GB control-plane slice
// (spec §13). gatewayd-internal reverse-proxies /dashboard/* and /oauth/* to
// apid's loopback listener so the §11 single-public-listener invariant
// stays intact (ADR-011).
//
// Templates live under templates/ and are baked into the binary via
// embed.FS so deploys ship one artefact per daemon (CLAUDE.md).
// Slice 2 ships just enough to prove the surface renders; slice 4
// fills in the real data.
package dashboard

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/dashboard/views"
	"github.com/onebox-faas/faas/pkg/presetwhy"
	"github.com/onebox-faas/faas/pkg/state"
)

//go:embed templates/*.html
var tmplFS embed.FS

// Page is the data each dashboard handler hands to Render. Keeping it
// in this package (not pkg/api) means dashboard handlers don't grow
// new fields in the public DTO surface by accident.
type Page struct {
	// Title is the <title> tag content.
	Title string
	// Flash is a one-line status banner rendered above the page body.
	// Used by /login and /auth/verify to surface "check your email".
	Flash string
	// Account is the signed-in account for authed pages. nil for
	// /login + /auth/*; dashboardAuth rejects unauthed requests for the
	// rest of /dashboard/*.
	Account *AccountView
	// Auth is the sign-in OAuth capability surface (issue #419 /
	// ADR-046). Set only on /login (the only unauthed template that
	// needs to render OAuth buttons); nil/zero elsewhere means
	// "render no OAuth buttons" so a future template that fails to
	// populate Auth can't accidentally show a 500-bound button.
	Auth *AuthCapabilitiesView
	// Nonce is the per-request CSP nonce minted by the
	// httpsec.Nonce middleware (cmd/apid/server.go). Render stamps it
	// onto every <script> and <style> tag inside the rendered HTML so
	// the browser accepts the inline code under the page's
	// Content-Security-Policy header. Empty string is tolerated (the
	// template renders the tags without a nonce attribute) so unit
	// tests don't need to mint a nonce; production always sets it.
	Nonce string
	// Body is the per-page template name (without the .html suffix).
	// Render looks up templates/<Body>.html inside the layout.
	Body string
	// Data is the page-specific payload (struct, map, etc.).
	Data any
}

// AccountView is the dashboard-facing slice of state.Account. Never
// log secrets here. Source data is pkg/state.Account; slices 3+4 expand.
type AccountView struct {
	ID                         string
	Email                      string
	Plan                       string
	AppCount                   int
	EmailVerified              bool
	EmailVerificationGraceEnds string
}

// DPAView is the page-specific payload for the dashboard DPA route
// (/dashboard/account/dpa). Markdown is the rendered DPA plaintext
// the apid main passes in after reading the configured dpaPath file.
// Keeping the body opaque here means a future maintainer can swap
// the markdown processor (goldmark, blackfriday, etc.) without
// touching the page template.
type DPAView struct {
	Markdown string
}

// IndexData is the /dashboard/ overview payload.
type IndexData struct {
	DeployedAppCount   int
	DeveloperAppCount  int
	DeveloperAppsLimit int
	Plan               string
}

// AppListItem is one row on /dashboard/apps.
type AppListItem struct {
	Slug         string
	Status       string
	URL          string
	LastDeployed string // empty when no deploys yet
	// StateBadge* is the cold-wake status glyph ux_spec §6.3 asks
	// for: ● running / ◌ sleeping / ⟳ waking / · idle (failed/
	// stopped). Set by renderAppsList via BadgeFor based on the
	// newest instance row for the app; "" until the dashboard
	// plumbs it (kept on the type so templates can render an empty
	// glyph during partial migrations).
	StateBadge      string
	StateBadgeGlyph string
	StateBadgeLabel string
	// Finding 6 (issue #314): per-app rate-limit bucket column. The
	// handler renders a static placeholder ("—" — "not yet wired") at
	// this PR; the dashboard /v1/internal/quota wiring is the
	// follow-up that adds the apid→gatewayd-internal loopback dial so a real
	// Peek value can land here without going through the self-DoS-ing
	// public listener. AppID + Plan are pre-supplied as data-attrs on
	// the cell so the follow-up PR can wire hx-get without re-templating.
	AppID      string
	Plan       string
	QuotaLabel string
	// SLO is the per-row SLO badge (issue #696 / ADR-082
	// dashboard follow-up PR). nil = no badge (Prometheus not
	// configured, or the row is beyond the apps-list badge
	// cap). When non-nil the template renders the Label +
	// Glyph ("ok" / "warn" / "down") inside the row's
	// "SLO" cell.
	SLO *views.SLOBadge
	// IsPreview (ADR-095 PR-C / issue #272) is true when this row
	// is a preview-app entry (apps.preview_of_slug != ''). The
	// dashboard's apps list uses it to add a "preview" badge and
	// indent the row so production apps and their previews read
	// as a hierarchy. Mirrors state.App.PreviewOfSlug but lives
	// here so pkg/dashboard stays free of pkg/state imports.
	IsPreview bool
	// Scope is the canonical preview subdomain ("pr-42.acme" for a
	// PR #42 preview of the acme app) used as the copy target for
	// the dashboard's preview-panel "Copy URL" button. Empty when
	// IsPreview is false. Pre-formatted at the handler edge so
	// the template stays a pure renderer.
	Scope string
}

// PreviewListItem is one row on /dashboard/previews (issue #961
// Mega-C PR-1 leaf 3). The shape mirrors AppListItem for the
// columns the customer expects (slug, parent, pr, state,
// expires) plus a Hostname (the customer-facing preview URL)
// and a DestroyAction (the dashboard's CSRF-wrapped POST target
// for the new destroy endpoint).
type PreviewListItem struct {
	Slug          string
	ParentSlug    string
	PRNumber      int
	IsDev         bool
	PRState       string
	ExpiresAt     *time.Time
	CreatedAt     time.Time
	Hostname      string
	DestroyAction string
}

// ManifestView is the runner-scaffold snapshot shown on the app detail
// page. Names are JSONish to avoid a second copy of pkg/api.AppManifest.
type ManifestView struct {
	Entrypoint []string
	Env        map[string]string
	WorkingDir string
	Port       int
	Healthz    string
	User       string
}

// DeploymentItem is one row on the app detail page's deploy list.
type DeploymentItem struct {
	ID        string
	Status    string
	Kind      string
	CreatedAt string
	Error     string
	// Issue #606 / SAFE-RELEASES-E.1: structured deployer
	// attribution. All four fields are server-stamped from the
	// HTTP request context (never client-supplied) and rendered
	// by deployment_detail.html as a chip row. The dashboard
	// deploy list (app_detail.html) shows a compact via-only
	// chip on each row; the drill-down page surfaces the full
	// triple (user / pusher / IP). Pre-#606 rows carry empty
	// strings here; the via-chip conditional render keeps the
	// wire + UI byte-identical for those rows.
	DeployedByUserID string
	DeployedVia      string
	DeployedFromIP   string
	PusherLogin      string
	// Reason / Tag / DeployedBy / PRNumber (issue #977 / ADR-116)
	// are the deploy-annotation projection. The detail page renders
	// the full Reason in a `<code>` block; the list view renders a
	// 40-char preview chip when Reason is non-empty and suppresses
	// the chip entirely when blank. Tag mirrors the DB CHECK closed
	// set (incident_recovery|hotfix|scheduled_maintenance|
	// compliance_hold|partner_request) and renders as a coloured
	// badge per the existing chip vocabulary (audit_events.html).
	// DeployedBy carries the operator label (CLI: git config
	// user.name; githubd: pusher.name; Action: github.actor); the
	// template renders "—" on empty so the column stays dense.
	// PRNumber is the GitHub PR number when the deploy came in via
	// a pull_request event or the Action's --pr-number input;
	// 0 means absent (push-to-main, local-tarball without PR).
	Reason     string
	Tag        string
	DeployedBy string
	PRNumber   int
	// RepoFullName (issue #977 / ADR-116 review fix) is the
	// "owner/name" string parsed off the deployment's SourceURL
	// (which carries the github:// scheme for githubd deploys).
	// Empty when the deploy didn't come through GitHub (local
	// tarball, image-deploy). The list-view template uses it to
	// build the PR link target instead of App.Slug (which is the
	// app slug, not the repo owner/name) so a clickable `#4242`
	// chip actually lands on GitHub.
	RepoFullName string
	// ScanSummary is the per-deploy grype scan chip rendered
	// in the deploy list (issue #464 / ADR-055). Nil when no
	// scan has run yet (the deploy is mid-pipeline or predates
	// #464 entirely) — the template renders "scan pending"
	// on the nil. Non-nil carries the Status string
	// (complete|failed|skipped) plus the severity counts;
	// the dashboard's deploy detail page (deployment_detail.html)
	// is the drill-down surface for the full CVE list.
	ScanSummary *ScanSummary
	// Error-explanations cluster (spec §6.4 amendment 1):
	// typed prose stamped on the deployments row alongside the
	// legacy raw Error string. Empty on pre-cluster rows;
	// the deployment_detail template gates the new section
	// on ErrorCode != "" so legacy renders unchanged.
	ErrorCode         string
	ErrorHint         string
	ErrorWhy          string
	ErrorFix          string
	ErrorRelevantLogs []api.LogExcerpt
}

// LogsData is the dashboard-facing payload for /dashboard/apps/{slug}/logs.
// The page consumes the existing gateway SSE endpoint directly; the handler
// keeps the URL and selector options together so templates remain render-only.
type LogsData struct {
	AppSlug           string
	AppStatus         string
	StreamURL         string
	ArchiveBase       string
	ArchiveURL        string
	ArchiveDate       string
	ArchiveInstanceID string
	ArchiveEnabled    bool
	Level             string
	Grep              string
	Since             string
	Deployment        string
	Deployments       []LogDeploymentItem
	Instances         []LogInstanceItem
}

// LogDeploymentItem is one deployment option in the logs filter.
type LogDeploymentItem struct {
	ID        string
	Status    string
	CreatedAt string
}

// LogInstanceItem is one instance option in the archived-log selector.
type LogInstanceItem struct {
	ID        string
	State     string
	StartedAt string
}

// EnvSecretsData is the dashboard-facing payload for the combined
// environment and secrets editor (/dashboard/apps/{slug}/env and
// /dashboard/apps/{slug}/secrets). Values for runtime env vars are shown
// because env is explicitly non-sensitive configuration; secret values are
// never projected and can only be supplied to a write or rotation form.
type EnvSecretsData struct {
	AppSlug       string
	AppStatus     string
	SelectedScope string
	WriteScope    string
	ScopeOptions  []string
	Env           []EnvItem
	Secrets       []SecretItem
	EnvCount      int
	EnvQuota      int
	SecretCount   int
	SecretQuota   int
	EnvCSRF       string
	SecretCSRF    string
	Flash         string
}

// EnvItem is one editable, non-sensitive app environment variable.
type EnvItem struct {
	Scope     string
	Key       string
	Value     string
	CreatedAt string
	UpdatedAt string
}

// SecretItem is the write-only projection of an app secret. ValueHashPrefix
// is intentionally short so the page can help identify a row without
// disclosing its value; Managed is true for provider-owned credentials that
// reject customer delete/rotate mutations.
type SecretItem struct {
	Scope           string
	Key             string
	ValueHashPrefix string
	Kid             string
	CreatedAt       string
	UpdatedAt       string
	Managed         bool
}

// AppErrorsData is the dashboard-facing payload for the automatic error
// grouping page (/dashboard/apps/{slug}/errors). It mirrors the existing
// summary, drill-down, and first-sample API surfaces without exposing any
// unredacted request data.
type AppErrorsData struct {
	AppSlug             string
	AppStatus           string
	Plan                string
	PlanAllowed         bool
	WindowStart         string
	WindowEnd           string
	WindowClamped       bool
	Limit               int
	SummaryURL          string
	Window24hURL        string
	Window7dURL         string
	Items               []ErrorSummaryItem
	NextSummaryURL      string
	SelectedFingerprint string
	Detail              *ErrorDetail
	ErrorMessage        string
}

// ErrorSummaryItem is one grouped error fingerprint in the dashboard.
type ErrorSummaryItem struct {
	Fingerprint   string
	ErrorClass    string
	Route         string
	HTTPStatus    int32
	Count         int64
	RequestCount  int64
	FirstSeenAt   string
	LastSeenAt    string
	SampleMessage string
	TriageState   string
	DetailURL     string
}

// ErrorDetail contains the bounded request history and oldest sample for a
// selected fingerprint. Headers and messages are already redacted by the
// app-errors writer before they reach this projection.
type ErrorDetail struct {
	Fingerprint     string
	ErrorClass      string
	Route           string
	HTTPStatus      int32
	Requests        []ErrorRequestItem
	NextRequestsURL string
	Sample          *ErrorSample
	TriageState     string
}

// ErrorRequestItem is one redacted request associated with a fingerprint.
type ErrorRequestItem struct {
	RequestID     string
	ReceivedAt    string
	Route         string
	HTTPStatus    int32
	ErrorClass    string
	SampleMessage string
	DeploymentID  string
}

// ErrorSample is the oldest redacted request sample for a fingerprint.
type ErrorSample struct {
	RequestID         string
	ReceivedAt        string
	Route             string
	HTTPStatus        int32
	ErrorClass        string
	SampleMessage     string
	DeploymentID      string
	HeadersSample     map[string]string
	RedactionsApplied []string
}

// ScanSummary is the per-deploy grype scan summary
// (issue #464 / ADR-055). The full typed payload lives on
// the deployments row (state.Deployment.ScanResult +
// ScanStatus + ScannedAt); this struct is the dashboard's
// trimmed projection for the deploy list view (counts only,
// no CVE list — the list view would overflow with a 200-row
// CVE list per deployment).
type ScanSummary struct {
	Status    string // complete|failed|skipped
	ScannedAt string // RFC 3339 UTC; empty when not scanned
	Critical  int
	High      int
	Medium    int
	Low       int
	Unknown   int
}

// PreviewItem (ADR-095 PR-C / issue #272) is one row on the
// app detail page's preview-environments panel. The panel
// surfaces every preview app whose preview_of_slug matches the
// parent (apps.preview_of_slug), with status, the full preview
// URL ("https://pr-42-acme.gregale.dev"), the underlying
// PR number, and the current PR state (open / closed / stale /
// torn_down). Pre-format all labels at the handler edge so the
// template stays a pure renderer.
//
// Branch / HeadSHA are intentionally absent: the preview-app
// schema (migrations/00220_preview_app_columns.sql) only carries
// preview_of_slug / preview_pr_number / preview_pr_state /
// preview_expires_at; per-deploy source-ref details are out of
// scope for PR-C and arrive with the preview-deploy webhook
// follow-up tracked separately.
//
// URL is the FULL https form (matching apps-list appListItem.URL)
// so the rendered <a href> and the Copy URL button both hand the
// user a clickable absolute URL. A bare host label here would
// emit a relative href and a non-clickable clipboard copy.
type PreviewItem struct {
	Slug       string // preview app slug (e.g. "demo-pr-42")
	URL        string // full preview URL (e.g. "https://pr-42-demo.gregale.dev")
	PrNumber   int
	IsDev      bool
	PrState    string // closed vocab: open / closed / stale / torn_down
	CreatedAt  string // RFC 3339 UTC
	ExpiresAt  string // RFC 3339 UTC; empty when no TTL
	StateLabel string // pre-formatted chip label ("open", "closed", etc.)
	StateClass string // CSS class matching PrState for the chip
}

// DomainItem is the compact durable custom-domain projection rendered on an
// app detail page. The cert fields come from custom_domains, so a dashboard
// refresh does not block on a live TLS handshake.
type DomainItem struct {
	Domain           string
	Verified         bool
	CertStatus       string
	CertExpiresAt    string
	CertLastError    string
	DNSLastCheckedAt string
	DoctorURL        string
}

// AppDomainsData is the dashboard-facing payload for the per-app custom
// domains page. The page is read-only: domain mutations continue through the
// existing API/CLI paths, while this view combines durable TLS state with the
// cached domain-doctor observation.
type AppDomainsData struct {
	App          AppListItem
	Domains      []DomainPageItem
	WWWApexHint  string
	ErrorMessage string
}

// DomainPageItem is one custom-domain row on the app domains page.
type DomainPageItem struct {
	Domain           string
	Verified         bool
	VerifiedAt       string
	TXTRecord        string
	CertStatus       string
	CertExpiresAt    string
	CertLastError    string
	DNSLastCheckedAt string
	DoctorURL        string
	Doctor           *DomainDoctorSummary
}

// DomainDoctorSummary is the latest cached doctor result. The page does not
// trigger a network probe for every row; the doctor link performs the existing
// bounded refresh when the observation is missing or stale.
type DomainDoctorSummary struct {
	Healthy    bool
	Stale      bool
	ObservedAt string
	Checks     []DashboardDoctorCheck
}

// CronItem is one row on the app detail page's crons tab
// (issue #791 PR-E / ADR-090 §"Sub-decision 7"). The inline runs
// panel + fire-now form are projected into the same struct so the
// template is a pure renderer — handler does the formatting.
type CronItem struct {
	ID          string
	Schedule    string
	Path        string
	Enabled     bool
	LastFiredAt string // empty until first fire

	// Runs is the bounded last-N invocations for this cron
	// (default 10). Rendered inside a server-rendered <details> on
	// the app detail page so the customer can scan "Last 10 runs"
	// without leaving the page. Empty slice → template renders an
	// empty-state hint rather than zero rows.
	Runs []CronRunRow
	// RunsCount is len(Runs), exposed as a sibling field so the
	// Go html/template parser can render "Last N runs" in the
	// <details> summary without needing a FuncMap. The template
	// can't call `len` on a slice directly (no FuncMap wired),
	// so the handler computes it once and stores the integer.
	RunsCount int
	// FireNowConfirmToken is the per-request CSRF envelope for the
	// "Fire now" POST form. Mandated by handlers_dashboard.go's
	// CSRF middleware (delete-account uses the same shape, see
	// handlers_dashboard.go:915). Always set when the cron is
	// enabled; empty (zero) when disabled → template suppresses
	// the form.
	FireNowConfirmToken string
}

// CronRunRow is one projected row inside CronItem.Runs. Pre-formatted
// at the handler edge so the template is a pure renderer (same
// pattern as RecentInstanceItem and InstanceChipDurationMS). The
// closed-vocabulary Outcome string matches api.CronRunOutcome — the
// handler projects both fields so the template never invents its own
// outcome label.
type CronRunRow struct {
	Glyph      string // "✓" | "✗" | "⟳" (running)
	StartedAt  string // pre-formatted HH:MM (relative-day suppressed for density)
	DurationMS string // "1.2s" | "980ms" | "timeout" | "—" — pre-formatted at handler
	Outcome    string // closed vocab matching api.CronRunOutcome
}

// AppDetailData combines the bits the app detail page renders.
type AppDetailData struct {
	App      AppListItem
	Manifest ManifestView
	// EffectiveLimits is the customer-visible resource and request
	// envelope derived from the app plus its current plan.
	EffectiveLimits     api.AppEffectiveLimits
	ConfiguredResources api.AppConfiguredResources
	Deployments         []DeploymentItem
	Crons               []CronItem
	// Workflows is the bounded recent-run view for the app detail page.
	// It intentionally carries step status and operator-facing errors, but
	// never the workflow input/output payloads.
	Workflows []WorkflowRunItem
	// Previews (ADR-095 PR-C / issue #272) lists every preview app
	// whose preview_of_slug matches this app's slug. Empty when
	// this app is itself a preview (or a production app with no
	// previews); the template renders a single "no preview
	// environments yet" hint in the empty case. The panel is
	// suppressed entirely for a preview row (the parent already
	// surfaces its previews) so a preview-of-preview loop can't
	// occur.
	Previews []PreviewItem
	// Domains carries the app's legacy custom-domain bindings and their
	// durable certificate lifecycle (issue #1397 / F1).
	Domains []DomainItem
	// FiredFlash is the post-redirect banner surfaced after a
	// dashboard cron fire-now POST. Values:
	//   "ok"    — handler redirected with ?fired=1
	//   "error" — handler redirected with ?fired=error
	// Empty string → no banner. The template's empty-state branch
	// suppresses the banner entirely so a fresh page load renders
	// the section without any success/error chrome.
	FiredFlash string
	// RollbackConfirmToken is the named CSRF token shared by the
	// deployment rollback forms on the app detail page.
	RollbackConfirmToken string
	// RollbackFlash is the post-redirect banner for the app rollback form.
	// Values are "ok", "error", or empty.
	RollbackFlash string
	// RecentInstances is the most recent N wake rows for this app
	// (parked → waking → running → …). Each carries its WakeID so
	// operators can paste the ID from a gateway response header
	// (`x-faas-wake-id`) and find which run it was. The list is
	// derived from ListInstancesForApp ordered DESC by started_at;
	// if the store returns no rows (cold-install, fully parked),
	// the section renders as empty and the template shows a hint.
	RecentInstances []RecentInstanceItem
	// Metrics is the per-app Prometheus snapshot (issue #273 / ADR-042).
	// nil = skip the section entirely (Prometheus not configured, or
	// the fetch failed non-fatally). When non-nil with RequestCount=0
	// the template renders an "no requests in this window" empty
	// state instead of a row of zeros. Source is the same
	// "prometheus" / "degraded: <err>" vocabulary the public status
	// page uses so the dashboard has one empty-state path.
	Metrics *AppMetricsView
	// SLOApp is the per-app SLO panel (issue #696 / ADR-082,
	// dashboard follow-up PR). nil = skip the section entirely
	// (the fetch failed non-fatally OR the window query-string was
	// invalid). When non-nil with the "degraded:" prefix on Source
	// the template renders the same empty-state badge the Metrics
	// card uses. The window is echoed via SLODuration so the
	// page's window-selector tab strip knows which tab is active.
	SLOApp *views.AppSLOView
	// SLODuration is the page-level helper that surfaces the
	// current SLO window ("1h" / "24h" / "7d") and the as-of
	// timestamp. The template uses the window to mark the
	// active tab in the window-selector nav; the timestamp is
	// pre-formatted at the handler edge so the template stays
	// a pure renderer.
	SLODuration views.SLOStamp
	// RequestAnalytics is the durable request_telemetry rollup shown below
	// the live Prometheus panels. nil means the plan does not include the
	// telemetry retention feature or the best-effort read failed.
	RequestAnalytics *RequestAnalyticsView
	// Alerts is the per-app (and account-wide) alert-rule snapshot
	// (issue #396 / ADR-045, PR 4). nil means the apid dashboard
	// query failed non-fatally (the page renders the "Alerts"
	// section as a warning); an empty slice renders the empty-state
	// line. RecentDeliveries per rule is capped at 5 by the handler.
	Alerts *AlertDetailData
	// Presets is the per-app alert-preset catalog rendered as a
	// grid between the Alerts panel and the recent-deliveries
	// sub-table (issue #1233 / ADR-123). Each row carries the
	// closed-set fields the form needs (DisplayName,
	// DefaultCooldownMinutes, etc.) plus the dashboard-side
	// Enabled boolean (= EnabledInCatalog && AccountPlanMeetsMinimum).
	// Disabled rows render as a greyed card with a "coming soon"
	// badge — the same affordance the enable endpoint returns
	// 400 alert_preset_disabled on. Below-minimum-plan rows
	// render with an "upgrade to <plan>" hint so a Hobby customer
	// sees what api_down would do without a clickable Enable.
	Presets []AlertPresetItem
}

// WorkflowRunItem is the dashboard projection of one durable workflow run.
// Timestamps are pre-formatted at the handler edge so the template remains
// a pure renderer and does not need date helpers.
type WorkflowRunItem struct {
	ID           string
	WorkflowName string
	Status       string
	CurrentStep  string
	CreatedAt    string
	StartedAt    string
	FinishedAt   string
	LastError    string
	Steps        []WorkflowStepItem
}

// WorkflowStepItem is the safe, compact projection of one workflow step.
// Input and output are deliberately omitted from the dashboard surface.
type WorkflowStepItem struct {
	Name    string
	Status  string
	Attempt int
	Error   string
}

// DeploymentDetailData is the dashboard-facing payload for
// /dashboard/apps/{slug}/deployments/{id} — the per-deploy grype
// scan drill-down page (issue #464 / ADR-055 / PR-A). The shape
// mixes the dashboard's existing DeploymentItem (the header line:
// id, kind, status, created_at) with an optional Scan pointer
// projected from state.Deployment by the handler. nil Scan means
// the deploy is mid-pipeline (status: live but the scan hasn't
// landed yet) — the template renders "scan pending" on the nil;
// non-nil Scan carries the full typed payload (severity counts,
// CVE list, error string on failed).
//
// We redefine the scan payload here (rather than importing
// pkg/api.ScanResult) so pkg/dashboard stays free of api-package
// imports — the same package-isolation rule that drove
// AppListItem vs pkg/api's app-shaped DTO. The handler
// (cmd/apid/handlers_dashboard.go::renderDeploymentDetail) is
// the only thing that materialises this struct from the wire
// types.
//
// Stages (ADR-117 v2 follow-on, A2): the closed-6-stage
// post-stream summary the customer sees via `gregale deploys show
// <id>`. nil Stages means the row was created before the
// jsonb column existed (migrations/00302) OR the jsonb is empty
// for an in-flight deploy — the template omits the section
// entirely. non-nil Stages carries a pre-rendered HTML block
// (handler-edge projection via pkg/dashboard/stages) so the
// template only inlines the result and needs zero FuncMap wiring.
//
// PreviewURL (issue #976 / ADR-122 / SAFE-RELEASES-C.3) is the
// read seam for the per-deployment preview URL
// `deploy-{N}.{slug}.gregale.dev`. Populated by the dashboard
// handler via the same Store call chain the apid
// getDeploymentURL handler uses; nil when the deployment is
// NOT preview-active (failed/superseded) so the template
// renders the "preview closed" chip in place of a copy
// button. Alive=false still carries a non-nil pointer with
// Host="" — the same shape api.DeploymentPreviewURL returns.
type DeploymentDetailData struct {
	App        AppListItem
	Deployment DeploymentItem
	Scan       *ScanPayload
	Stages     *StagePayload
	// CanRollback is true for a superseded deployment that can be
	// selected as the rollback target. The handler binds the form to
	// the same app-scoped rollback endpoint used by the app detail
	// page; keeping the gate in the view model prevents a stale or
	// failed row from rendering a misleading action.
	CanRollback bool
	// RollbackConfirmToken is the named CSRF envelope consumed by
	// POST /dashboard/apps/{slug}/rollback. It is empty when
	// CanRollback is false.
	RollbackConfirmToken string
	// HostingReceipt is the durable post-readiness evidence emitted by
	// imaged. It is optional because older deployments and non-API
	// deployment paths may not have a receipt yet.
	HostingReceipt *HostingReceiptView
	// PreviewURL carries the resolved per-deployment preview
	// URL. nil when the deployment doesn't exist or belongs to
	// another account (handler already 404s in that case).
	// Non-nil with Alive=false on failed/superseded rows so
	// the template renders the closed-state copy.
	PreviewURL *DeploymentPreviewURL
	// CanRetry drives the per-stage retry form
	// (deployment_detail.html:280). True when the deployment
	// row is in a failed terminal state AND the jsonb
	// stage_state carries a recoverable failed-stage name
	// (cmd/apid/dashboard_retry_deployment.go::failedStageFromJSON).
	// False otherwise — the form is hidden, not disabled, so
	// the page layout doesn't shift row-by-row.
	CanRetry bool
	// RetryFromStage is the from_stage query param + hidden
	// form input mirrored on the retry POST
	// (`/dashboard/apps/{slug}/deployments/{id}/retry?from=<stage>`).
	// Empty when CanRetry is false.
	RetryFromStage string
	// DeploymentRetryCSRF is the sealed
	// (action="retry_deployment", account_id) form token the
	// handler re-validates via
	// middleware.VerifyAuthenticated in
	// cmd/apid/dashboard_retry_deployment.go::dashboardRetryDeployment.
	// Empty when CanRetry is false.
	DeploymentRetryCSRF string
	// DeploymentAudit is the per-deployment audit timeline
	// (issue #976 / ADR-122 / SAFE-RELEASES-E.2 + production
	// leveling Stream A). Newest-first; capped at
	// listDeploymentAuditLimitDefault rows in the handler.
	// nil/empty → the template renders the empty-state block.
	// Each row carries a pre-rendered SeverityClass so the
	// template can pick a CSS palette without re-implementing
	// the kind→severity mapping.
	DeploymentAudit []DeploymentAuditRow
}

// HostingReceiptView is the dashboard-safe projection of the durable API
// hosting receipt. The raw receipt stays on the API/state boundary; this
// view keeps the template focused on the fields that answer "is it ready,
// what was checked, and what artifact went live?" without exposing storage
// internals such as the rootfs key.
type HostingReceiptView struct {
	AppURL          string
	SourceKind      string
	SourceURL       string
	CommitSHA       string
	ImageDigest     string
	ProfileVersion  string
	Framework       string
	FrameworkVer    string
	Port            int
	HealthPath      string
	SmokeStatus     string
	SmokePath       string
	SmokeStatusCode int
	SmokeLatencyMS  int64
	SmokeVerifiedAt string
	SmokeErrorCode  string
	SmokeError      string
}

// DeploymentAuditRow is the dashboard-local projection of one
// pkg/state.DeploymentAudit row. The internal id (BIGINT) stays
// server-side — the dashboard keys the timeline by (at, kind,
// actor). SeverityClass maps the kind to a CSS palette name
// (info / warn / high) so the template can branch on a single
// string instead of re-implementing the kind→severity mapping.
//
// DataPretty is the verbatim jsonb payload pretty-printed (or
// the empty string when the row has no data) — same shape as
// audit_events.html's DataPretty.
type DeploymentAuditRow struct {
	At            time.Time
	Kind          string
	Actor         string
	SeverityClass string
	DataPretty    string
}

// DeploymentAuditSeverityClass maps a deployment_audit row's
// closed-set kind to the dashboard's CSS palette name. The
// mapping mirrors the wider audit events palette
// (.high / .warn / .info) so the deployment timeline block
// reuses the same color chips without a new palette. Exported
// because the handler's projection edge lives in
// cmd/apid/handlers_dashboard.go, outside this package.
//
// Mapping (issue #976 / ADR-122 / SAFE-RELEASES-E.2 closed set):
//
//	high → deploy.rolled_back (customer-affecting state flip)
//	warn → deploy.traffic_changed / deploy.health_probe_failed
//	info → everything else (deploy.created / source_ref / etc.)
func DeploymentAuditSeverityClass(kind string) string {
	switch kind {
	case "deploy.rolled_back":
		return "high"
	case "deploy.traffic_changed", "deploy.health_probe_failed":
		return "warn"
	default:
		return "info"
	}
}

// PrettyAuditData renders the verbatim jsonb payload of a
// deployment_audit row for the dashboard timeline block. Kept as
// an exported free function (not a method on DeploymentAuditRow)
// so the handler's projection edge stays the single source of
// truth for the data-pretty render — same pattern as
// audit_events.html's DataPretty field (cmd/apid/audit_receiver.go
// project side). Returns the empty string when the payload is
// nil/empty so the template renders the "—" muted placeholder.
//
// Pretty-printing uses encoding/json.Indent; the cost is bounded
// by listDeploymentAuditLimitDefault rows × jsonb size, which is
// trivial at the dashboard's 50-row cap.
func PrettyAuditData(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		// Unparseable jsonb — fall through to the verbatim
		// string so the operator sees something instead of
		// a missing column.
		return string(raw)
	}
	return buf.String()
}

// DeploymentPreviewURL is the dashboard-local mirror of
// pkg/api.DeploymentPreviewURL (issue #976 / ADR-122 /
// SAFE-RELEASES-C.3). Carries only the fields the template
// needs — Host, URL, Alive — so pkg/dashboard stays free of
// pkg/api imports. Host and URL are empty when Alive=false OR
// when the deployment-preview zone is disabled
// (wire.DeployWildcardSuffix == "").
type DeploymentPreviewURL struct {
	Host  string
	URL   string
	Alive bool
}

// StagePayload is the dashboard-local mirror of the closed-6-stage
// post-stream summary (ADR-117 §3, migration 00302). BodyHTML is
// the pre-rendered `<section class="stage-timeline">…</section>`
// block the template inlines via {{ .Data.Stages.BodyHTML }} —
// rendered at the handler edge (cmd/apid/handlers_dashboard.go::
// dashboardStagePayload) so the template stays a pure renderer
// and pkg/dashboard stays free of html/template FuncMap wiring.
//
// Status / TerminalAt are surfaced as plain fields so the
// template can branch on the deployment's terminal state (e.g.
// to render a "live" pill even when the section's footer doesn't
// include the timestamp). The customer-facing footer copy lives
// inside BodyHTML so the template can't drift from the CLI's
// text renderer.
//
// Failure (ADR-117 §Production-ready follow-on, C4) is a
// handler-edge whycopy projection for failed rows. When the
// deployment row's ErrorCode is in the CodeStage* set, the
// handler calls pkg/whycopy.Decorate against the typed
// api.Problem to lift title/hint/why/fix prose, then renders
// the cluster-A `.error-explanation` HTML block alongside the
// timeline. nil Failure omits the section in the template;
// non-nil Failure carries the pre-rendered, html/template-safe
// fragment. The seam lives at the handler edge so pkg/dashboard
// stays free of pkg/whycopy and pkg/api imports.
type StagePayload struct {
	BodyHTML           template.HTML
	Status             string
	TerminalAt         time.Time
	FailureExplanation *StageFailureExplanation
}

// StageFailureExplanation is the pre-rendered structured
// hint/why/fix block for one failed deployment row. Mirrors the
// cluster-A `.error-explanation` CSS convention
// (pkg/dashboard/templates/deployment_detail.html). All fields
// carry html-escaped strings so the template can inline them via
// {{ .Hint | safeHTML }} without a template.HTML cast (cluster A
// precedent at pkg/dashboard/views/render.go:274/311).
type StageFailureExplanation struct {
	Title string
	Hint  string
	Why   string
	Fix   string
}

// DomainDoctorView (ADR-120 Tier A2) is the dashboard-facing
// payload for the per-domain doctor drill-down at
// /dashboard/apps/{slug}/domains/{domain}/doctor. Mirrors the
// wire api.DomainDoctorReport shape (pkg/api/dto.go) so the
// dashboard renders the same 5 checks the CLI's `gregale
// domains doctor <domain>` prints. The handler is the
// per-domain data source — see
// cmd/apid/handlers_dashboard.go::renderDomainDoctor.
//
// Checks carries one row per Render-style probe (DNS record
// found / points to Gregale / TLS certificate / CAA permits /
// IPv6 conflict). Status is the closed enum {ok, fail, pending,
// na} from pkg/api/dto.go::DomainDoctorCheck — the dashboard
// uses the same vocabulary so the badge classes in
// domain_doctor.html match the CLI's glyph table without a
// mapping helper. Healthy is the "all checks OK" boolean the
// doctor returns; Stale flips on when the observation row is
// older than FAAS_DOMAIN_DOCTOR_TTL_SECONDS and the handler
// triggered a synchronous re-probe.
type DomainDoctorView struct {
	App        AppListItem
	Domain     string
	AppID      string
	Healthy    bool
	Stale      bool
	ObservedAt string
	Checks     []DashboardDoctorCheck
}

// DashboardDoctorCheck is one row of the DomainDoctorView.Cheks
// slice — the dashboard-local mirror of pkg/api.DomainDoctorCheck.
// The field set is the wire DTO verbatim (Name, Status, Detail,
// Observed, Remediation, CheckedAt) so a future wire-DTO
// regen can swap the type without rewriting the template.
type DashboardDoctorCheck struct {
	Name        string
	Status      string
	Detail      string
	Observed    string
	Remediation string
	CheckedAt   string
}

// ScanPayload is the dashboard-local mirror of the per-deploy
// grype scan payload. Status is the closed enum
// {complete, failed, skipped, pending} — the dashboard reads
// the same vocabulary pkg/api.ScanResult uses so the chips are
// uniform across the list view (app_detail.html) and the
// detail view (deployment_detail.html).
//
// Vulnerabilities is the dashboard's "top 10" view (handler-edge
// cap in cmd/apid/handlers_dashboard.go::dashboardScanPayload);
// TotalCount carries the pre-truncation count so the template
// can render the AC #3 "Showing N of M" copy + a "View full scan"
// link to GET /v1/deployments/{id}/scan. TotalCount == len(
// Vulnerabilities) when the underlying scan had ≤ dashboardScanTopN
// findings (the common case for small base images).
type ScanPayload struct {
	Status          string
	ScannedAt       string
	ScannerVersion  string
	ImageDigest     string
	SeverityCounts  SeverityBucket
	Vulnerabilities []VulnerabilityRow
	TotalCount      int
	Error           string
}

// SeverityBucket mirrors the CRITICAL|HIGH|MEDIUM|LOW|UNKNOWN
// buckets the dashboard reads off the deploy list chips too.
// Kept as a flat struct (not a map) so go's html/template can
// render `.Data.Scan.SeverityCounts.Critical` directly without
// a map lookup.
type SeverityBucket struct {
	Critical int
	High     int
	Medium   int
	Low      int
	Unknown  int
}

// VulnerabilityRow is one row in the deployment-detail CVE
// table. Trimmed from pkg/api.Vulnerability to the columns the
// dashboard renders (id, severity, package, version, fixed_in,
// paths). Paths is empty when the scan matched a package
// without per-file locations — the template renders "—" on the
// empty case (the existing dashboard convention for blank
// cells, see FormatAlertError for the truncation precedent).
type VulnerabilityRow struct {
	ID       string
	Severity string
	Package  string
	Version  string
	FixedIn  string
	Paths    []string
}

// AlertDetailData is the dashboard-facing payload for the alert-rules
// panel. The shape matches what pkg/dashboard/templates/app_alerts.html
// renders — one row per rule with up to 5 most-recent deliveries.
// FailureSource / Metric / Comparison / Threshold / WindowSpec are
// already strings in state.AlertRule (closed vocabularies) so the
// template renders them directly; no enum mapping needed on this
// surface (the API DTO side does the normalisation per PR 3).
type AlertDetailData struct {
	// Rules is the alert-rule list scoped to the current app +
	// account-wide. The handler filters state.Store.ListAlertRulesForAccount
	// down to rule.AppID == app.ID || rule.AppID == "" — the same
	// visibility filter the public API uses (handlers_alerts.go).
	Rules []AlertItem
}

// AlertItem is one row on the dashboard's "Alerts" panel. RecentDeliveries
// is rendered underneath the rule row so operators see the last 5
// dispatch attempts without leaving the page. Status on AlertDelivery
// is "delivered" / "failed" / "pending"; LastError is truncated at
// the handler edge so a 32 KiB SSRF-rejected URL doesn't blow the
// dashboard layout.
type AlertItem struct {
	Rule             state.AlertRule
	RecentDeliveries []state.AlertDelivery
	// LastFiredAtLabel is a pre-formatted relative timestamp
	// ("3m ago" / "just now" / "—") computed at the handler edge so
	// the template stays a pure renderer. Empty until LastFiredAt
	// goes non-zero.
	LastFiredAtLabel string
}

// AlertPresetItem is one card on the dashboard's "Alert presets"
// grid (issue #1233 / ADR-123). The shape mirrors api.AlertPresetResponse
// with two dashboard-side booleans pre-computed by the handler:
// Enabled (the click-vs-coming-soon gate) and MeetsPlan (the
// below-minimum-plan gate). Both feed the same UX the API
// surfaces — the dashboard never asks for a value the server
// would 400/402 on. AppSlug is the calling app's slug so the
// form's POST URL is one template variable, not three.
type AlertPresetItem struct {
	Name                   string
	DisplayName            string
	Description            string
	Category               string
	Metric                 string
	Comparison             string
	Threshold              float64
	WindowSpec             string
	DefaultCooldownMinutes int
	MinimumPlan            string
	EnabledInCatalog       bool
	// Enabled is the dashboard-side AND of EnabledInCatalog and
	// the customer's plan >= preset.MinimumPlan. When false, the
	// card renders as "coming soon" (greyed) and the Enable button
	// is suppressed.
	Enabled bool
	// MeetsPlan is the per-row plan-tier check. When false, the
	// card renders with an "upgrade to <plan>" hint instead of the
	// Enable button — distinct from Enabled so the user knows
	// whether the preset is staged-for-future (Enabled=false,
	// MeetsPlan=true) or plan-gated (Enabled=true, MeetsPlan=false).
	MeetsPlan bool
	// AppSlug is the calling app's slug. The form's POST URL is
	// /apps/{AppSlug}/alert-presets/{Name}/enable (form-encoded,
	// NOT JSON — see the dashboard handler at cmd/apid/handlers_dashboard.go).
	AppSlug string
	// EnableConfirmToken is the per-card CSRF token minted by
	// middleware.IssueForAuthenticated against
	// (action="enable_alert_preset", acct.ID). The dashboard form
	// renders it as a hidden <input name="csrf_token"> so the
	// form-encoded POST handler at cmd/apid/dashboard_preset_enable.go:72
	// (middleware.VerifyAuthenticated) accepts the submission.
	// Empty on cards that don't render a form (coming-soon /
	// upgrade-required) — no token needed there.
	EnableConfirmToken string
	// TestAlertConfirmToken is the per-card CSRF envelope for the
	// "Send test alert" form (issue #1233 / ADR-123 PR-C commit 2).
	// Separate from EnableConfirmToken because the verifier seals
	// (action, account_id) and refuses cross-action replays — sharing
	// the envelope would let a replayed enable-click attempt a
	// dispatch. Empty on cards where the test button does not
	// render (card is not yet instantiated or is plan-gated).
	TestAlertConfirmToken string
	// Instantiated is true when the customer has already enabled
	// this preset for this app — i.e. an alert_rules row exists
	// whose name begins with the catalog's DisplayName + " (".
	// Populated by the handler edge in
	// cmd/apid/handlers_dashboard.go::fetchDashboardPresets using a
	// single ListAlertRulesForAccount scan (the per-account row
	// count is bounded by AlertRuleLimitPerAccount = 100 on Scale).
	// When true the card renders a "Send test alert" button (issue
	// #1233 / ADR-123 PR-C commit 2) instead of the enable form;
	// when false the enable form stays as-is.
	Instantiated bool
	// RuleID is the alert_rules.id of the instantiated rule, when
	// Instantiated=true. Empty otherwise. Wired into the test-button
	// form action as a hidden <input> so the sidebar-style 308
	// redirect on the dashboard side doesn't need to carry the id
	// in the URL.
	RuleID string
	// Explanation is the dashboard-side "What does this alert mean?"
	// panel (issue #1233 / ADR-123 PR-C commit 3). Populated by the
	// handler edge via presetwhy.Decorate(p.Name, 0) — observed=0
	// keeps the static prose, which is what the preset grid renders
	// (the Observed renderer takes over in the alert-detail panel).
	// nil when the preset name has no presetwhy catalog row — the
	// template uses `with` to skip the <details> panel cleanly.
	Explanation *presetwhy.Explanation
}

// alertDeliveryErrorLimit caps the LastError string we render on the
// dashboard so a rejected SSRF URL ("http://10.0.0.1:8080/...: egress
// denied: …") doesn't blow the panel column width.
const alertDeliveryErrorLimit = 200

// FormatAlertError trims LastError to alertDeliveryErrorLimit bytes.
// The handler applies this before handing to the template; the
// helper is exported because cmd/apid/handlers_dashboard.go is
// outside pkg/dashboard and needs to reach it. Kept as a thin
// formatter rather than a method on AlertDelivery so the truncation
// policy is testable in isolation (pkg/dashboard/dashboard_test.go).
func FormatAlertError(s string) string {
	if len(s) <= alertDeliveryErrorLimit {
		return s
	}
	return s[:alertDeliveryErrorLimit-1] + "…"
}

// RelativeTime labels a timestamp with a coarse "just now / Nm ago /
// Nh ago" string suitable for the dashboard's "Last fired" column.
// Negative diffs (clock skew) render as "just now" rather than
// "<future>". Exported for the same reason as FormatAlertError;
// cmd/apid/handlers_dashboard.go applies it on the data-loader side
// before the template renders.
func RelativeTime(t time.Time, now time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d / time.Minute)
		return fmt.Sprintf("%dm ago", m)
	default:
		h := int(d / time.Hour)
		if h < 48 {
			return fmt.Sprintf("%dh ago", h)
		}
		return t.UTC().Format("2006-01-02")
	}
}

// AppMetricsView is the dashboard-facing snapshot of one app's
// metrics (issue #273 / ADR-042). Numbers are pre-formatted; the
// template renders them via plain {{printf "%g" .}}. P50/P95/P99 are
// the 2xx-class only — failures surface as ErrorRatePct. WakeP95MS is
// the FLEET p95; the dashboard template labels it as such.
type AppMetricsView struct {
	Range        string // echoed window, e.g. "5m"
	Source       string // "prometheus" / "degraded: <reason>"
	RequestCount int64
	LatencyP50MS float64
	LatencyP95MS float64
	LatencyP99MS float64
	ErrorRatePct float64
	ColdStartPct float64
	WakeP95MS    float64
}

// RequestAnalyticsView is the dashboard-facing projection of the customer
// request analytics API. It deliberately mirrors only aggregate fields;
// request IDs and trace data belong to the debugger surface.
type RequestAnalyticsView struct {
	Since                 string
	From                  string
	Until                 string
	WindowClamped         bool
	Requests              int64
	ErrorRequests         int64
	ErrorRatePct          float64
	ColdBoots             int64
	P50MS                 int
	P95MS                 int
	P99MS                 int
	Routes                []RequestAnalyticsRouteView
	RoutesLimit           int
	RoutesTruncated       bool
	AsOf                  string
	Bucket                string
	SelectedRoute         string
	SelectedMethod        string
	SelectedQuery         string
	TimeseriesURL         string
	LatencySparkline      views.LatencySparklineView
	LatencySparklineHTML  template.HTML
	ErrorSparkline        []appmetrics.SparklinePoint
	ErrorSparklineHTML    template.HTML
	ColdBootSparkline     []appmetrics.SparklinePoint
	ColdBootSparklineHTML template.HTML
}

type RequestAnalyticsRouteView struct {
	Route         string
	Method        string
	Requests      int64
	ErrorRequests int64
	ErrorRatePct  float64
	ColdBoots     int64
	P50MS         int
	P95MS         int
	P99MS         int
	TrendURL      string
}

// RecentInstanceItem is one row of the Recent Wakes table on the
// dashboard app-detail page.
//
// ADR-123: Trigger / QueuedCount / ConcurrencyAtAdmit surface
// the wake-boot telemetry from the jsonb `events.data` payload.
// Source: LEFT JOIN LATERAL against the first wake.boot_started
// event for the wake_id (gated by the existing events_wake_id_idx
// partial index from migration 00114). Empty/zero values are
// rendered as em-dash per the existing convention for absent
// fields — pre-ADR-123 fleet rows have no wake.boot_started yet.
//
// PR-A extends the row with AtCapacity (the bool stamped by
// pkg/sched.Engine.admitGate's wakeAdmit branch — closes the
// "2/2 at concurrency limit" reference line) and ReadyInMS
// (millisecond delta between boot_started.at and boot_completed.at
// — closes the "Ready in: 112 ms" reference line). Both surface
// naturally as columns in the dashboard table; em-dash on zero
// for pre-PR-A fleet rows.
type RecentInstanceItem struct {
	ID                 string // instance row PK (stable across wakes)
	WakeID             string // per-wake UUIDv7; distinct from ID
	State              string // wire vocabulary; the template badge maps parked → sleeping
	StartedAt          string // empty when not yet started
	LastRequestAt      string // empty when no traffic yet
	Trigger            string // ADR-123 — pkg/sched/triggers.go closed enum
	QueuedCount        int    // ADR-123 — ledger.Concurrency at admit
	ConcurrencyAtAdmit int    // ADR-123 — same reading; 0 = cold start
	AtCapacity         bool   // PR-A — true when admitted at the plan's per-app MaxConcurrency ceiling
	AtCapacityPresent  bool   // PR-A — true when the at_capacity key was present in jsonb (false = absent; em-dash)
	ReadyInMS          int    // PR-A — wall-clock boot_started → boot_completed delta in ms; 0 if still booting or rejected
}

// WakeTimelinePageData is the /dashboard/apps/{slug}/wake-timeline
// page payload (PR-A follow-on to ADR-123, per the spec §17 G17
// follow-on subsection). Renders one page per app with a 24h summary
// card (wake count + trigger histogram + at-capacity count/%) plus a
// "Recent wakes" body table — the same column set the app-detail
// recent-wakes table exposes, but with up to 50 rows instead of 10
// and a summary header pre-aggregated at the handler edge.
//
// The dashboard does NOT use html/template FuncMap
// (pkg/dashboard/dashboard.go:903 — templates parsed via
// template.New("").ParseFS). Following the stages pattern, every
// HTML block is pre-rendered at the handler edge:
//
//   - RenderTable is the views.RenderWakeTimelineTable output
//     (one static <table> chassis, all values escaped via
//     template.HTMLEscapeString before the chassis cast).
//   - TriggerHistogramHTML is the views.RenderTriggerHistogram
//     output, pre-sorted by trigger key so the rendered text is
//     stable across requests (snapshot diff = nil).
//
// AtCapacityPct is rounded to 0 decimals at the template edge
// ("{{printf \"%.0f\" .Data.AtCapacityPct}}") — the dashboard
// never needs a finer-grained number for an at-cap readout.
//
// WakeCountWithMeta is the denominator the at-cap% uses (vs
// WakeCount24h which includes pre-ADR-123 fleet rows that lack a
// boot_started event). When WakeCountWithMeta < WakeCount24h the
// template renders "of {N} known wakes" alongside the at-cap count
// so a customer sees the divergence explicitly (PR-A review cluster
// finding #5 — wakeCount24h and the histogram were previously
// inconsistent with no label).
type WakeTimelinePageData struct {
	App                  AppListItem
	WakeCount24h         int
	WakeCountWithMeta    int
	AtCapacityCount      int
	AtCapacityPct        float64
	TriggerHistogramHTML template.HTML // pre-rendered at the handler
	RenderTable          template.HTML // pre-rendered at the handler
}

// UsageAppData is the customer-facing monthly usage projection for one app.
// The handler resolves the app slug and computes display units so the
// template remains a pure renderer.
type UsageAppData struct {
	Slug        string
	Linkable    bool
	UsedGBHours float64
	SharePct    float64
	Requests    int64
	CPUHours    float64
	EgressGB    float64
	IngressGB   float64
	ColdBoots   int64
}

// UsageDailyPoint is one row in the account's trailing daily usage trend.
type UsageDailyPoint struct {
	Date          string
	GBHours       float64
	TopAppSlug    string
	TopAppGBHours float64
}

// UsageData is the /dashboard/usage page payload.
type UsageData struct {
	Month           string
	UsedGBHours     float64
	IncludedGBHours int64
	OverageGBHours  float64
	UsedPct         float64 // 0..100+
	Requests        int64
	// UsedEgressGB (ADR-046, step 10) is the per-month
	// informational egress roll-up (Σ tx_bytes +
	// net_tx_bytes across all apps). Not billed; the
	// template renders it next to the GB-h panel as a
	// "this much egress" line. The gateway-side tx_bytes
	// producer lands in PR-2; until then the value is
	// 0 because NetTxBytes is the only source populated.
	UsedEgressGB       float64
	UsedIngressGB      float64
	UsedCPUHours       float64
	ColdBoots          int64
	PerApp             []UsageAppData
	Daily              []UsageDailyPoint
	DailySparklineHTML template.HTML
}

// BillingData is the /dashboard/billing page payload (issue #253).
//
// HasPaidPlan gates the "Manage billing" + "Last invoice" sections so a
// Free-tier account never sees a billing portal link. Provider identifies
// the active billing integration for customer-facing copy. PortalURL is a
// provider-authenticated session when supported, otherwise the
// operator-configured FAAS_BILLING_PORTAL_URL template (already substituted
// with the account's ID by the handler).
//
// Current-month usage is informational only (mirrors /dashboard/usage);
// the billable floor is included GB-hours, not raw mb_seconds.
//
// LastInvoiceDate / LastInvoiceStatus / LastInvoiceTotalFormatted are
// sourced from the most recent row in state.Invoices (LIMIT 1). The
// fields are pre-formatted at the handler edge so the template is a
// pure renderer. Currency is the provider's three-letter code (USD /
// EUR / etc.); empty when no invoice exists.
type BillingData struct {
	Plan     string
	RAMMB    int
	Included int64
	AppsCap  int
	AppLayer int
	IdleSec  int

	// MaxConcurrency is sourced from api.MustLimitsFor(acct.Plan);
	// distinct from the live HealthyCount on the Apps page so the
	// customer can see the per-app ceiling without leaving the
	// billing surface.
	MaxConcurrency int

	// Current-month usage (informational; not billed here).
	UsedGBHours  float64
	UsedPct      float64
	UsedEgressGB float64 // mirrors renderUsage; informational (eth framing caveat)

	// Last invoice (empty fields for free or no-invoice accounts).
	LastInvoiceDate           string
	LastInvoiceStatus         string
	LastInvoiceTotalFormatted string
	LastInvoiceCurrency       string

	// Billing portal link; empty for free accounts or an unavailable provider
	// session. Provider is a closed set emitted by the apid provider resolver.
	HasPaidPlan bool
	Provider    string
	PortalURL   string

	// Issue #561 — spend cap pause-workload. *int64 so the template
	// can distinguish "no cap" (nil pointer) from "cap is 0 cents"
	// (no overage allowed) and "cap is N cents" (a positive monthly
	// ceiling). The handler reads accounts.overage_cap_cents via
	// state.Store.GetAccountOverageCapCents; the form on the
	// billing page POSTs back to /dashboard/raise-overage-cap (see
	// the apid dashboard handler).
	OverageCapCents      *int64
	OverageUsedCents     int64   // current-month overage; informational, used to compute the % bar
	OverageUsedThisMBCap float64 // ratio over cap (or 0 if no cap) for the inline progress meter

	// RaiseCapConfirmToken is the issue #561 CSRF envelope — minted
	// alongside DeleteConfirmToken / RestoreConfirmToken by
	// renderBilling via middleware.IssueForAuthenticated, so the
	// /dashboard/raise-overage-cap POST verifies the (action=
	// "raise_overage_cap", account_id) sealed envelope before any
	// state change. Same shape as the dashboard delete/restore
	// forms.
	RaiseCapConfirmToken string

	// CanCheckout is true when the active billing provider exposes a
	// hosted checkout and the account has no subscription yet — the
	// Free → paid path. The template then renders one
	// /dashboard/upgrade?plan=… link per UpgradeOptions entry; otherwise
	// it points at the CLI / portal. UpgradeNotice is the one-line
	// outcome banner after a POST /dashboard/upgrade redirect
	// (?upgrade=error|unavailable).
	CanCheckout    bool
	UpgradeOptions []UpgradeOption
	UpgradeNotice  string
}

// UpgradeOption is one paid plan the account can upgrade to. Money is
// formatted at the handler boundary (formatPriceEuros) so the template
// never touches integer millicents.
type UpgradeOption struct {
	Plan            string
	PriceFormatted  string
	IncludedGBHours int64
	RAMMB           int
	MaxConcurrency  int
	DeployedApps    int
}

// UpgradeData is the /dashboard/upgrade page payload — the one-form
// confirmation step in front of the provider's hosted checkout. The
// page exists because the dashboard CSRF cookie carries exactly one
// sealed token per response, so the checkout form gets its own page
// (mirrors the account delete confirmation) instead of sharing the
// billing page with the spend-cap form.
type UpgradeData struct {
	CurrentPlan string
	// Target is nil when ?plan= is missing or not an eligible upgrade;
	// the template then renders Options as a chooser.
	Target  *UpgradeOption
	Options []UpgradeOption
	// Available is true when POST /dashboard/upgrade can start a hosted
	// checkout for Target. Reason explains a false value.
	Available bool
	Reason    string
	// Notice is the outcome banner after a failed POST redirect.
	Notice string
	// Provider is the closed provider name; ProviderLabel the
	// customer-facing brand ("Polar").
	Provider      string
	ProviderLabel string
	// PortalURL is the fallback for accounts that already have a
	// subscription (the provider portal owns product changes).
	PortalURL string
	// ConfirmToken is the (action="upgrade_plan", account_id) CSRF
	// envelope minted by renderUpgrade.
	ConfirmToken string
}

// PricingData is the /dashboard/pricing page payload (issue #259).
// Plans is the four-plan table authoritative from pkg/api/limits.go;
// Highlighted marks the row that matches the current account's plan
// so the dashboard can render a "Your plan" badge. PriceFormatted is
// the on-the-wire "€X.YY" / "Free" string — money stays integer
// millicents upstream and is divided into euros at template time only.
type PricingData struct {
	Plans []PricingPlanView
}

// PricingPlanView is one row on /dashboard/pricing. Every field is
// derived from api.Limits — never inline a quota here, the limit
// table is the single source of truth (CLAUDE.md Hard limits).
type PricingPlanView struct {
	Plan                    string
	PriceFormatted          string
	Highlighted             bool
	DeployedApps            int
	MaxConcurrency          int
	RAMMB                   int
	AppLayerMaxMB           int
	SourceTarballMaxMB      int
	IdleTimeoutS            int
	IncludedGBHours         int64
	RateLimitRPS            int
	RateLimitBurst          int
	EgressMbit              int
	SecretCountMax          int
	AsyncInvokeAllowed      bool
	MinInstancesAllowed     bool
	ScaleUpTargetRPSAllowed bool
	ScaleUpTargetCPUAllowed bool
	EgressAllowlistAllowed  bool
	EgressAllowlistMaxSize  int
}

// InvoicesData is the /dashboard/invoices page payload (issue #259).
// Items is one row per invoice; NextBefore is the RFC3339Nano cursor
// for the older page (empty when this is the end). Month is the
// currently-applied filter, echoed back so the template can
// pre-fill the form input.
type InvoicesData struct {
	Month      string
	Items      []InvoiceRow
	NextBefore string
}

// InvoiceRow is one dashboard row. TotalFormatted is pre-formatted at
// the handler edge (integer cents → "€X.YY" / "€0.00"; never float).
// PDFAvailable shows the marker (Y / -) but never exposes the provider
// PDF URL. HostedURL is intentionally absent: the column lives in
// invoices.hosted_url for PR-B audit only; PR A never puts it on the
// wire (see state.Invoice docstring).
type InvoiceRow struct {
	ID             string
	Number         string
	Provider       string
	Status         string
	Period         string // "2026-07"
	TotalFormatted string // "€X.YY" — pre-formatted at the handler edge
	Currency       string
	PDFAvailable   bool
}

// APIKeyItem is one row on the /dashboard/account page's keys tab.
//
// Scopes is the fine-grained permission set the apid IAM-1 (ADR-034
// rev2) vocabulary grants the key (e.g. ["admin"], ["apps:read",
// "deploy:write"], or the legacy single-element form). The dashboard
// template renders the slice as a comma-separated list. Defaults to
// empty for older accounts that pre-date the migration's CHECK
// constraint guarantee (the renderer treats nil as "—" — see
// account.html).
type APIKeyItem struct {
	ID         string
	Prefix     string
	Label      string
	Scopes     []string
	CreatedAt  string
	LastUsedAt string // empty until first use
	CanRevoke  bool
}

// AccountData is the /dashboard/account page payload.
type AccountData struct {
	Keys []APIKeyItem
	// KeyDeleteConfirmToken is shared by the account page's key-revoke
	// forms. Its sidecar uses a dedicated cookie name so it can coexist
	// with the account-delete and GitHub-connect CSRF tokens rendered on
	// the same page.
	KeyDeleteConfirmToken string
	// ShowDelete + DeleteConfirmToken drive the "Danger zone" partial
	// in templates/account.html. The token is a sealed envelope bound
	// to (action="delete", account_id) that the POST handler verifies
	// via pkg/middleware.VerifyAuthenticated. The matching faas_csrf
	// sidecar cookie is set by the renderer (handlers_dashboard.go)
	// so a cross-site POST cannot read it. ShowRestore +
	// RestoreUntil render the matching "Restore account" form when
	// the row is in deleted_pending — the deadline is the human-
	// readable restore_until the dashboard template surfaces.
	ShowDelete          bool
	DeleteConfirmToken  string
	ShowRestore         bool
	RestoreUntil        string
	RestoreConfirmToken string
	// ConnectGithubConfirmToken (issue #961 / Mega-B PR-3) backs the
	// dashboard's "Connect GitHub" button. The form posts to
	// /dashboard/install/connect with this token + the matching
	// faas_csrf sidecar cookie. Same envelope shape as the delete /
	// restore tokens above — sealed by (action, account_id).
	ConnectGithubConfirmToken string
	// PlanConfirmToken backs the account-page plan form. Its sidecar uses
	// a dedicated cookie name because the account page renders several
	// independently action-bound forms at once.
	PlanConfirmToken string
	// FlashSurface holds "scheduled for deletion" / "restored" banners
	// the dashboard reads from ?deleted=1 / ?restored=1 in the URL.
	// Kept here (not Page.Flash) so the danger-zone partial stays a
	// self-contained block the layout file can render unconditionally.
	FlashSurface string
	// ActionRequiredSurface (issue #695 / ADR-080) holds the
	// one-time per-account banner that surfaces after the
	// apps-auth-default flip. Populated by the apid
	// dashboard handler when the account has at least one
	// pre-flip app (auth_default_flipped_at IS NOT NULL AND
	// the customer hasn't PATCHed every pre-flip app back
	// to the public open path). Empty string = no banner; no
	// dismissal cookie — count-zero is the natural off-switch.
	// Renders on account.html beneath the FlashSurface block.
	ActionRequiredSurface string
	// SLOAccount is the per-account SLO panel (issue #696 /
	// ADR-082, dashboard follow-up PR). nil = skip the
	// section entirely (the fetch failed non-fatally OR the
	// window query-string was invalid). When non-nil with the
	// "degraded:" prefix on Source the template renders the
	// same empty-state badge the per-app SLO card uses.
	SLOAccount *views.AccountSLOView
	// SLODuration mirrors the per-app page's helper. The
	// template uses Window to mark the active tab in the
	// window-selector nav; AsOf is the pre-formatted
	// timestamp.
	SLODuration views.SLOStamp
}

// AuthCapabilitiesView is the dashboard-facing slice of
// auth.SignInConfig (issue #419 / ADR-046). The handler populates
// these bools from the boot-resolved s.oauthConfig so the login
// template can conditionally render each OAuth button. Source is
// auth.SignInProvider.Enabled() — never re-read os.Getenv from a
// template or a handler.
type AuthCapabilitiesView struct {
	GoogleEnabled bool
	GitHubEnabled bool
}

// AdminData is the operator-only dashboard payload. It currently carries the
// durable TLS cutover state so an operator can see that a rollback was tested
// without losing the context when the edge returns to its normal state.
type AdminData struct {
	TLSCutover          TLSCutoverState
	TLSCutoverStateFile string
	TLSCutoverPresent   bool
}

// Render writes the page to w. It parses the templates on first use
// and caches the parsed tree in a sync.Once — the hot path is a
// single Execute call.
//
// nonce is the per-request CSP nonce minted by httpsec.Nonce
// (cmd/apid/server.go). Render stamps it onto page.Nonce so every
// <script> and <style> tag inside the layout and body template can
// emit `nonce="…"` via {{.Nonce}}. Empty nonce is allowed (unit
// tests pass "") and the templates render the tags without a nonce
// attribute — production always supplies a value via
// httpsec.NonceFromContext.
//
// Body is the per-page template name ("index", "apps_list", …). It
// MUST be present under templates/ or Render writes a 500 problem.
func Render(w http.ResponseWriter, log *slog.Logger, nonce string, page Page) error {
	page.Nonce = nonce
	if page.Body == "" {
		page.Body = "index"
	}
	t, err := parseTemplates()
	if err != nil {
		log.Error("dashboard template parse failed", "err", err)
		return err
	}
	tplName := page.Body + ".html"
	if t.Lookup(tplName) == nil {
		log.Error("dashboard template missing", "name", page.Body)
		return fmt.Errorf("dashboard: template %q not found", page.Body)
	}
	var buf bytes.Buffer
	// Execute the page template directly with the Page struct. Each
	// page template defines the full <html>…</html> wrapper (not a
	// shared layout) — slices stay small enough that the duplication
	// is cheaper than a layout-include dance.
	if err := t.ExecuteTemplate(&buf, tplName, page); err != nil {
		log.Error("dashboard template execute failed", "err", err, "page", page.Body)
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, err = buf.WriteTo(w)
	return err
}

var (
	tmplOnce sync.Once
	tmplTree *template.Template
	tmplErr  error
)

func parseTemplates() (*template.Template, error) {
	tmplOnce.Do(func() {
		// Parse the layout + every page so {{template "name"}} lookups
		// resolve inside the layout. Failure is fatal for the daemon.
		tmplTree, tmplErr = template.New("").ParseFS(tmplFS, "templates/*.html")
	})
	return tmplTree, tmplErr
}

// AuditEventsData is the dashboard-facing payload for
// /dashboard/audit-events — the customer-facing drill-down on the
// stateless-advisory audit log. Wave 0 PR-C / ADR-047. The rows
// come from GET /v1/audit-events?kind_prefix=stateless.advisory
// (handled by cmd/apid/handlers_dashboard.go::renderAuditEvents);
// the dashboard re-renders them in a tabular view with a
// "scroll for more" hint and a link back to the per-app detail.
//
// Filter reflects the URL query: empty AppID/KindPrefix render the
// account-wide unified history (the same rows the HTTP API returns
// without a filter). AppID is the dashboard's primary entry point
// — the app_detail.html "Stateless advisories" link passes
// ?kind_prefix=stateless.advisory&app_id={uuid}.
type AuditEventsData struct {
	KindPrefix string
	AppID      string
	AppSlug    string
	Events     []AuditEventRow
	// IncludeAnonymous is echoed back so the template can render a
	// checkbox pre-toggled to the operator's previous choice.
	IncludeAnonymous bool
}

// SafeReleasesData is the dashboard-facing payload for
// /dashboard/safe-releases — the operator's "everything
// in-flight" surface (issue #976 / ADR-122 / SAFE-RELEASES-OBS
// PR-C). Three sections, each backed by a single Store call so the
// page renders in bounded time even on a busy fleet:
//
//	InFlight   — every deployment currently in 'pending' or
//	             'rolling_out' (state.Store.SafedeployListPendingRollouts).
//	RecentAudit — the latest deployment_audit rows for each
//	             in-flight deployment, filtered to the 5 audit kinds
//	             PR-A widened (deploy.rollout_started,
//	             deploy.rollout_completed, deploy.rollout_aborted,
//	             deploy.canary_step_advanced,
//	             deploy.alert_rule_fired). N+1 query — one
//	             ListDeploymentAudit per in-flight deployment —
//	             acceptable because in-flight count is bounded by
//	             safedeploy_in_flight_rollouts (PR-B's
//	             canary_fleet_in_flight_high alert tripwires at 50).
//	ActiveAlerts — every alert_rule whose metric is one of the 4
//	             PR-B safe-releases kinds and which is currently
//	             enabled. Operator uses this to see "who's
//	             subscribed to my canary_stuck_step alert right now".
type SafeReleasesData struct {
	InFlight     []DeploymentItem
	RecentAudit  []SafeReleasesAuditRow
	ActiveAlerts []SafeReleasesAlertRow
}

// SafeReleasesAuditRow is one row of the recent-audit table.
// DeploymentID + AppSlug are surfaced so the template can deep-link
// back to /dashboard/apps/{slug}/deployments/{id}. TimeLabel is the
// same pre-formatted relative-timestamp shape as AuditEventRow.
type SafeReleasesAuditRow struct {
	DeploymentID string
	AppSlug      string
	Kind         string
	TimeLabel    string
	Actor        string
}

// SafeReleasesAlertRow is one row of the active-alerts table.
// Name + Metric come straight from alert_rules; the plan-gate lives
// on the alert_presets catalog row (alert_rules only carries rows
// that already passed the gate) so MinPlan is empty here.
// Enabled reflects the per-rule toggle (separate from the catalog
// enabled_in_catalog flag).
type SafeReleasesAlertRow struct {
	Name    string
	Metric  string
	Enabled bool
}

// AlertRuleDetailData (SAFE-RELEASES-OBS PR-D, issue #976 / ADR-122)
// is the dashboard-facing payload for /dashboard/alerts/{rule_id}.
// The handler at cmd/apid/handlers_dashboard.go::renderAlertRuleDetail
// populates three slices via three Store calls: AlertRuleByID,
// ListDeploymentAuditByAlertRule (the new partial-index-backed
// query), and ListAlertDeliveriesForRule. The template
// pkg/dashboard/templates/alert_rule_detail.html renders each.
//
// Why no MinPlan: AlertRule doesn't carry a plan tier — the plan
// gate lives on alert_presets (only operator-tier accounts can
// instantiate rules from those presets). A rule instantiated
// against any plan renders identically across plans.
//
// AuditRows and Deliveries are bounded slices (cap=100 / cap=50
// from the handler). Empty when no rows have been stamped yet —
// the template renders a "no audit rows yet" empty state.
type AlertRuleDetailData struct {
	Page       Page
	Rule       state.AlertRule
	AuditRows  []state.DeploymentAudit
	Deliveries []state.AlertDelivery
}

// AuditEventRow is one row of the audit table. TimeLabel is a
// pre-formatted relative timestamp ("3m ago" / "just now" / "—")
// computed at the handler edge so the template stays a pure renderer.
//
// Severity is populated only for stateless.advisory rows (Move 1
// PR-A) — the apid receiver writes a "severity" key into the
// audit row's data map and the dashboard handler hoists it here.
// Closed vocabulary: "high" | "warn" | "info" | "" (empty for
// non-stateless kinds, which the template renders as a muted
// dash so the column stays present but unobtrusive).
//
// Mega-PR B: the same value is also exposed at the top level of
// api.AuditEventResponse (json:"severity",omitempty) — the
// /v1/audit-events JSON wire shape carries Severity alongside
// the data JSONB. Pre-PR-427 rows render with no Severity at all
// (the omitempty tag is the backwards-compat contract).
type AuditEventRow struct {
	ID         string
	TimeLabel  string
	Actor      string
	Kind       string
	Subject    string
	DataPretty string
	Severity   string
}

// StatelessDenylistEntry is one row of the customer-facing contract
// page (/dashboard/stateless). Name is the lower-cased base image
// substring that imaged rejects (e.g. "postgres"). Hint is the
// managed-service suggestion embedded in the RFC 7807 Detail field.
//
// Mirrored in the dashboard package (not imported from pkg/imaged)
// so a future read-only rendering surface (the docs site, the SDKs)
// can reuse the same shape without dragging in the imaged
// dependency. Keep these two lists in lockstep with
// pkg/imaged/base.go's StatefulBaseImageDenylist.
type StatelessDenylistEntry struct {
	Name string
	Hint string
}

// StatelessClosedPath is one path the guest-init fanotify advisory
// watches. Severity is "high" for /data, /db, and the well-known
// stateful daemon data dirs the customer is most likely to trip
// (postgres, mysql); "warn" for the rest. Mirrors
// guest/init/stateless_advisory_linux.go's statelessRuntimePaths.
//
// The dashboard classifies by prefix so a future watch-dir addition
// (e.g. /var/lib/etcd) lands in lockstep with guest-init without
// re-touching this list.
type StatelessClosedPath struct {
	Path     string
	Severity string // "high" | "warn"
}

// StatelessDenylist is the static copy rendered on the /dashboard/stateless
// landing page. Source of truth remains pkg/imaged/base.go — this
// slice exists so the dashboard template renders the contract
// without re-importing imaged's StatefulBaseImageDenylist (which
// would couple the dashboard to the build pipeline).
var StatelessDenylist = []StatelessDenylistEntry{
	{Name: "postgres", Hint: "use Neon (https://neon.tech) or Supabase Postgres"},
	{Name: "redis", Hint: "use Upstash Redis (https://upstash.com)"},
	{Name: "mysql", Hint: "use PlanetScale (https://planetscale.com)"},
	{Name: "mariadb", Hint: "use PlanetScale (https://planetscale.com)"},
	{Name: "mongo", Hint: "use MongoDB Atlas (https://mongodb.com/atlas)"},
	{Name: "cockroach", Hint: "use CockroachDB Cloud (https://cockroachlabs.cloud)"},
	{Name: "cassandra", Hint: "use Astra DB (https://astra.datastax.com)"},
	{Name: "clickhouse", Hint: "use ClickHouse Cloud (https://clickhouse.cloud)"},
}

// StatelessClosedPaths mirrors guest/init/stateless_advisory_linux.go's
// statelessRuntimePaths. "high" severity for the top-level dirs + the
// best-known stateful daemon data dirs; "warn" for the rest. Adding
// a path means a new entry in BOTH this slice AND the guest-init
// source — see ADR-047 for the rationale.
var StatelessClosedPaths = []StatelessClosedPath{
	{Path: "/data", Severity: "high"},
	{Path: "/db", Severity: "high"},
	{Path: "/var/lib/postgresql", Severity: "high"},
	{Path: "/var/lib/mysql", Severity: "high"},
	{Path: "/var/lib/mongodb", Severity: "warn"},
	{Path: "/var/lib/mongo", Severity: "warn"},
	{Path: "/var/lib/redis", Severity: "warn"},
	{Path: "/var/lib/cockroach", Severity: "warn"},
	{Path: "/var/lib/cassandra", Severity: "warn"},
	{Path: "/var/lib/clickhouse", Severity: "warn"},
}

// StatelessData is the /dashboard/stateless landing page payload.
// RecentAdvisories is the last 50 stateless.advisory rows scoped to
// the account (same shape as AuditEventRow but capped + sorted by
// recency). RecentAdvisoriesEmpty is true when there are zero rows
// so the template can render the empty-state copy without inspecting
// the slice. StatelessDenylist + ClosedPaths are the package-level
// constants re-exported as struct fields so the template can reach
// them without a separate {{$pkg.Var}} lookup.
type StatelessData struct {
	RecentAdvisories      []AuditEventRow
	RecentAdvisoriesEmpty bool
	RecentAdvisoriesTotal int
	StatelessDenylist     []StatelessDenylistEntry
	ClosedPaths           []StatelessClosedPath
}

// OrgListItem is one row on /dashboard/orgs — every org the
// signed-in account is a member of (mirrors GET /v1/orgs/me via
// state.Store.ListOrgsForAccount). Role is the caller's role on
// the org; SeatCount is the live member count surfaced via
// CountActiveOrgMembers so the customer can size the "upgrade"
// nudge without leaving the page. Personal orgs surface with a
// muted "(personal)" tag and skip the seat count (ADR-061 §C —
// personal orgs have a deterministic 1-of-1 membership set).
//
// PR-8 scope: read-only. The "New organization" form (which
// would issue a personal→shared promote + slug rename) lands in
// PR-9 alongside the per-seat billing cut-over (plan §"Out of
// scope"); PR-8 ships the table so the customer can navigate to
// an existing org first.
type OrgListItem struct {
	Slug      string
	Name      string
	Plan      string
	Role      string
	Personal  bool
	SeatUsed  int
	SeatLimit int // 0 when the org plan returns "personal org only"
}

// OrgListData is the /dashboard/orgs payload. Orgs already
// filtered to "active + caller has a non-removed membership" by
// the handler; the template is a pure renderer.
type OrgListData struct {
	Orgs []OrgListItem
}

// OrgMemberItem is one row on the org detail page's "Members"
// table. Joined with state.Account so the dashboard renders
// account.Email (never the bare account ID, which would be
// unreadable). The join is best-effort: when AccountByID returns
// ErrNotFound (race vs DeleteAccount cascade), the row renders
// Email="(deleted account)" + Role preserved — the audit table
// still ties to AccountID via the underlying state row, so the
// compliance pivot stays intact even when the membership row
// outlives the account row.
type OrgMemberItem struct {
	AccountID string
	Email     string
	Role      string
	JoinedAt  string
}

// OrgInvitationItem is one row on the org detail page's
// "Pending invitations" table (ADR-035 §"Kind taxonomy" — also
// covers consumed/revoked/expired rows; the Status column
// disambiguates). TokenPrefix is the 8-char hash prefix (same
// "copy token_id for support ticket" affordance already on
// revoke audit rows; never the full hash, never the plaintext).
// Status vocabulary matches api.OrgInvitationStatus so the
// table mirrors the public /v1/orgs/{slug}/invitations surface
// without a translate layer.
//
// PR-8 ships the read-only table; the "Revoke" column header is
// rendered but the form is left blank — wiring the form needs
// /dashboard CSRF + a dashboard→apid reverse-call that PR-8
// doesn't introduce (the api-Org-061 §"Dashboard reverse-call"
// seam is held for a follow-up PR). The customer's two
// workarounds until then: (a) `faas orgs invitations revoke`
// CLI, (b) re-issue a new invite after revoking the old one
// via the public API.
type OrgInvitationItem struct {
	ID          string
	Email       string
	Role        string
	Status      string
	CreatedAt   string
	ExpiresAt   string
	TokenPrefix string
}

// OrgDetailData is the /dashboard/orgs/{slug} payload. The page
// fetches members + invitations via the store directly (apid is
// the dashboard's data layer — no reverse-call needed because
// the dashboard and apid share the process per ADR-011 §"Surface
// partition"). The seat chip lives on the embedded OrgListItem
// (PR-8 review — duplicating SeatUsed/SeatLimit at the top level
// created two sources of truth for the same value).
//
// Error is a non-empty string when one of the three lookups
// failed non-fatally (the page still renders whatever rows came
// back, with the error surfaced above the table as a banner).
// The full nil-out path is for the truly catastrophic case where
// the org row itself is missing — the handler short-circuits to
// 404 then.
type OrgDetailData struct {
	Org         OrgListItem
	Members     []OrgMemberItem
	Invitations []OrgInvitationItem
	CallersRole string
	Error       string
}
