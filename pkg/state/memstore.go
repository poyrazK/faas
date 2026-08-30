package state

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/netip"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/cursor"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// stripePushKey is the (account, hour) dedupe key the hourly Stripe
// pusher uses; declared above MemStore so the struct field below can
// reference it.
type stripePushKey struct {
	accountID string
	hour      time.Time
}

// webhookDeliveryKey is the (provider, delivery_id) dedupe key the
// three webhook ingresses on the box use; mirrors the
// (provider, delivery_id) primary key on the webhook_deliveries
// table (migration 00059). The value is the expires_at timestamp so
// the MemStore can answer "is this row within its 5-minute TTL?"
// without a separate sweep — TTL eviction is O(1) at access time.
// Issue #294.
type webhookDeliveryKey struct {
	provider   string
	deliveryID string
}

// paddleOverageKey is the (account, month) dedupe key the daily Paddle
// overage pusher uses; declared above MemStore so the struct field
// below can reference it. `month` is the calendar-month start
// (calendarMonthStart in pkg/billing/paddle/usage.go).
type paddleOverageKey struct {
	accountID string
	month     time.Time
}

// paddleOverageWindowKey is the (account, window) claim key the
// per-window Paddle overage pusher uses. `windowStart` is
// hour.UTC().Truncate(Hour), mirroring stripePushKey's `hour`
// normalization. The window-scoped grain matches the meterd loop's
// UsageByHour read and the underlying schema's
// (account_id, window_start) PK from migration 00037.
type paddleOverageWindowKey struct {
	accountID   string
	windowStart time.Time
}

// paddleOverageClaimState is the in-memory mirror of the
// paddle_overage_dedupe row's claim metadata. Mirrors the
// (state, claimed_at, claimed_by) tuple from migration 00037 so
// MemStore parity tests can exercise every branch of
// ClaimPaddleOverageWindow + CompletePaddleOverageWindow +
// ReapStalePaddleOverageClaims without standing up Postgres.
type paddleOverageClaimState struct {
	claimedBy    string
	claimedAt    time.Time
	completed    bool
	pushedAt     time.Time
	mbSecondsSum int64
}

// MemStore is an in-memory Store for tests and local development. It is safe for
// concurrent use and enforces the same uniqueness constraints as the schema
// (unique email, unique slug, unique key hash) so tests exercise real error
// paths. It is NOT durable — production uses the Postgres store.
type MemStore struct {
	mu        sync.Mutex
	accounts  map[string]Account
	keys      map[string]APIKey
	keyByHash map[string]APIKey
	apps      map[string]App
	// consumerKeys is the ADR-120 store. Keyed by ConsumerKey.ID
	// (UUID, generated at create time). The (appID, prefix) hot-
	// path index is in-memory only — we walk the map on lookup
	// (the memstore is a test fixture, not a production path).
	consumerKeys map[string]ConsumerKey
	// provisionedStaticEgressIPs is the ADR-119 redesign gate.
	// Keyed by (accountID, customerIP) — the same composite PK
	// as the Postgres table. Test fixture only.
	provisionedStaticEgressIPs map[string]map[string]netip.Addr // accountID → customerIP → Addr
	// githubBindings is keyed by appID. Holds the (install_id,
	// repo_full_name, production_branch) tuple the /oauth/callback
	// handler writes after verifying the install against api.github.com
	// (review findings #1 + #2 closure, ADR-012).
	githubBindings map[string]GitHubBinding
	// githubInstalls is the durable OAuth handshake state per
	// account (PR-C). Keyed by accountID so the cold-start rehydrate
	// path in pkg/githubd/realservice.go can look up by account.
	githubInstalls map[string]GitHubInstall
	// githubWebhookSecrets is the per-tenant webhook secret
	// store (PR-D / ADR-012 §7 amendment). Keyed by
	// installation_id so the resolver cache at
	// pkg/githubd/webhook_secret.go can match the prod
	// ON CONFLICT (installation_id) DO UPDATE shape.
	githubWebhookSecrets map[int64][]byte
	// githubWebhookSecretMeta stamps (upgraded_at, upgraded_by)
	// for each per-tenant row so the apid admin route can echo
	// the row back without a second query.
	githubWebhookSecretMeta map[int64]webhookSecretMeta
	deployments             map[string]Deployment
	// statusIncidents (issue #599 / ADR-130) is the in-memory
	// mirror of the status_incidents table (migrations/00412).
	// Append-only + resolved_at-stamped; the partial-index read
	// (status_incidents_open WHERE resolved_at IS NULL) is mirrored
	// by the ListOpenStatusIncidents loop filter.
	statusIncidents []StatusIncident
	builds          map[string]Build
	// buildProvenance is the ADR-038 "what ran?" record keyed by
	// build_id (mirrors build_provenance.build_id UNIQUE). MemStore
	// holds the same idempotent-replace semantics as PgStore's
	// ON CONFLICT (build_id) DO UPDATE so a redelivered build
	// overwrites the same row instead of doubling.
	buildProvenance map[string]BuildProvenance
	domains         map[string]CustomDomain
	// doctorObs (ADR-120) is the in-memory mirror of the
	// domain_doctor_observations table. The dns_poller is
	// the sole writer; the doctor HTTP handler is the sole
	// reader. Stored separately from `domains` because the
	// observation row is per-domain but the doctor's pass
	// enumerates both custom_domains and tenant_hostnames.
	doctorObs map[string]DomainDoctorObservation
	crons     map[string]Cron
	triggers  map[string]sqlc.Trigger
	records   map[string]sqlc.TriggerRecord
	// fireNowRequests mirrors cron_fire_now_requests (migrations/00193)
	// for in-process handler tests. Keyed by request id (UUID);
	// status transitions follow the production 5-state CHECK (pending
	// → running → succeeded|failed|cancelled).
	fireNowRequests map[string]FireNowRequest
	// operatorIntents mirrors operator_intents (migrations/00431)
	// for handler + subscriber tests. Keyed by intent id (UUID);
	// status transitions follow the production 5-state CHECK.
	operatorIntents map[string]OperatorIntent
	// runtimeConfigs mirrors runtime_config_entries (migration 00466).
	// The key is config_key + scope + scope_id, matching the production
	// unique constraint and the reconciliation lookup path.
	runtimeConfigs          map[string]RuntimeConfig
	runtimeConfigOperations map[string]RuntimeConfigOperation
	runtimeConfigRevisions  []RuntimeConfigRevision
	// alertRules mirrors alert_rules for handler tests. Keyed by
	// ruleID. AlertDelivery rows are kept separately so the
	// delivery list query can walk just the matching subset on
	// every tick. MemStore holds the same UNIQUE(account_id, name)
	// invariant the Postgres index enforces — CreateAlertRule
	// rejects a duplicate before insert. AlertDeliveryRows are
	// keyed by idempotency_key (the UNIQUE column); ClaimAlertFire
	// translates a "win" into an INSERT-after-claim so two parallel
	// claims inside the same bucket produce exactly one delivery
	// row (mirrors the UNIQUE-index floor in Postgres).
	alertRules      map[string]AlertRule
	alertDeliveries map[string]AlertDelivery
	// appWebhooks + appWebhookDeliveries back the issue #476
	// outbound webhook subscription + ledger surface. Keyed by
	// webhookID / deliveryID. The unique (app_id, target_url)
	// invariant is enforced at insert time. MemStore holds no
	// concurrency control beyond m.mu — the dispatcher's claim
	// query is a single goroutine today.
	appWebhooks          map[string]AppWebhook
	appWebhookDeliveries map[string]AppWebhookDelivery
	// deploymentScopeExclusions backs the ADR-124 follow-up #3
	// persistent --exclude history (migration 00418). Keyed by row
	// id (uuid string) for symmetry with appWebhooks; the (account,
	// project, slug) UNIQUE invariant is enforced at insert time
	// in CreateDeploymentScopeExclusion below.
	deploymentScopeExclusions map[string]DeploymentScopeExclusion
	// Issue #1182 §P1 packaging follow-up (PR-1 of 3): in-memory
	// backing for the upload-session Store methods. Keyed by upload_id
	// (text). Backs the resumable upload protocol's handler unit
	// tests; production uses *PgStore against the upload_sessions
	// table (migrations/00533_upload_sessions.sql). Mutex is the
	// existing m.mu — no new lock.
	uploadSessions       map[string]sqlc.UploadSession
	uploadCommitOutcomes map[string]sqlc.UploadCommitOutcome
	// alertClaimKeys tracks the (ruleID, idempotency_key) → claimTime
	// pair so MemStore mirrors the Postgres UNIQUE(idempotency_key)
	// + last_fired_at dedupe behaviour. Two claims with the SAME
	// idempotency key — even at a strictly later at — lose the
	// second attempt; a fresh key with a later bucket time wins.
	alertClaimKeys map[string]time.Time
	// edgeRules mirrors edge_rules for handler tests (ADR-089). Keyed
	// by ruleID. The single-process m.mu serialises the count +
	// insert in CreateEdgeRuleIfUnderQuota; no separate TOCTOU fence
	// is needed. Soft-delete semantics (apps.status='deleted') are
	// mirrored by the per-app lookup in the quota-check branch.
	edgeRules map[string]EdgeRule
	// mirrorRules mirrors mirror_rules for handler tests (issue #72
	// / ADR-125). Keyed by MirrorRule.ID; the (app_id, enabled) and
	// (source_deployment_id, enabled) lookup hot paths walk the map
	// — fine for tests, matches the partial-index plan on the
	// pgstore side. The m.mu lock serialises count + insert in
	// CreateMirrorRuleIfUnderQuota, mirroring the pgstore FOR
	// UPDATE discipline. mirrorResults is the per-invocation
	// ledger (mirror_invocation_results table) keyed by result ID;
	// InsertMirrorResult is best-effort, the apid path doesn't
	// observe it, only the gateway does.
	mirrorRules   map[string]MirrorRule
	mirrorResults map[string]MirrorInvocationResult
	// corsPresets mirrors cors_presets (issue #975 item #4 /
	// Mega-Foundation #979-b). Keyed by presetID. PR-A exposes
	// only the read path; the write surface lives in PR-B
	// (#979-c, slot 00295) alongside the per-rule cors.preset_id
	// field. Mirroring the read path here means handler tests
	// can exercise the compile-side merge without a live PG.
	corsPresets map[string]CorsPreset
	// openAPIDocs mirrors deployment_openapi_docs (ADR-122 /
	// issue #975 item #1, migrations/00330). Keyed by
	// deployment_id. The acctID predicate is checked at the
	// method boundary so a cross-tenant read returns ErrNotFound
	// (the same defence-in-depth as consumerKeys below).
	openAPIDocs map[string]openAPIDocRow
	// openAPISnapshots mirrors deployment_openapi_snapshots
	// (ADR-121, migration 00358). Keyed by deployment_id,
	// mirroring the table's PK. PR-C's gate reads via
	// LatestOpenAPISnapshotForScope (linear scan under m.mu)
	// and OpenAPISnapshotByDeployment (single map lookup).
	// MemStore parity is required for the gate's handler tests
	// — the contract is the same Map+slice-walk the production
	// PgStore implements with an index.
	openAPISnapshots map[string]OpenAPISnapshot
	// openAPIImports mirrors the per-app app_openapi_docs table
	// from migrations/00416 (issue #975 item #2 / ADR-126).
	// Keyed by app_id (one row per app, last-write-wins).
	openAPIImports map[string]appOpenAPIImportRow
	// alertPresets mirrors alert_presets (issue #1233 / ADR-123).
	// System-owned catalog; only the seed migrations write these.
	// Keyed by preset.ID — the apid enable handler reads via
	// AlertPresetByName which scans the map (8 rows, O(N) is fine).
	alertPresets map[string]AlertPreset
	// accountSpendSnapshots mirrors account_spend_snapshot
	// (migrations/00420 in this branch's renumber). meterd appends
	// one row per tick; the alert evaluator's MTDSpendEurCents
	// walks the map for the MTD-window SUM. Keyed by ID.
	accountSpendSnapshots map[string]AccountSpendSnapshot
	// tenantSurfaceCertExpiryStates mirrors
	// meterd_tenant_surface_cert_expiry_state (migrations/00421 in
	// this branch's renumber). The meterd cert-expiry refresher
	// goroutine (PR-A wiring)
	// upserts rows; the alert evaluator's MinCertExpiryForApp
	// walks the map for the smallest remaining-seconds value.
	tenantSurfaceCertExpiryStates map[string]TenantSurfaceCertExpiryState
	// oidcTrustPolicies is keyed by (accountID, issuerURL) — the
	// composite primary key shape from migration 00265. The
	// per-account lookup OIDCTrustPoliciesForAccount is the only
	// scan needed (MemStore holds the entire dataset under m.mu).
	// ADR-101 / issue #270.
	oidcTrustPolicies map[string]OIDCTrustPolicy
	// oidcExchangedTokens is keyed by the hex-encoded SHA-256 of the
	// raw bearer (the UNIQUE token_hash index from migration 00266).
	// The expires_at-driven expiry is in-memory only; lazy-flip is
	// not strictly necessary because the 5-min TTL is short enough
	// that a MemStore process restart would clear them anyway. The
	// store MAY garbage-collect expired rows on every read; the pg
	// contract is "WHERE expires_at > NOW()".
	oidcExchangedTokens map[string]OIDCExchangedToken
	// tenantSurfaces is keyed by TenantSurface.ID (uuid); the
	// account-keyed lookup GetTenantSurfaceByName and the host-keyed
	// TenantSurfaceByHostname linear-scan on demand. MemStore holds
	// the entire dataset under m.mu so per-table indexes are
	// unnecessary — every method synchronises against m.mu.
	//
	// tenantHostnames is keyed by hostname (citext storage in
	// tenant_hostnames.hostname); the unique-across-surfaces invariant
	// is enforced by scanning and rejecting duplicates in
	// CreateTenantHostnameIfUnderQuota.
	//
	// ADR-100 / issue #879.
	tenantSurfaces  map[string]TenantSurface
	tenantHostnames map[string]TenantHostname
	// invocations is the Move 1 event-shaped queue (async_invoke,
	// queue, delayed_task, cron). MemStore mirrors PgStore's `select
	// ... for update skip locked` semantics by serialising every access
	// through m.mu (MemStore is inherently single-process); per-row
	// lease_expires_at is in-memory instead of SQL NOW().
	invocations map[string]Invocation
	instances   map[string]Instance
	// loginTokens is keyed by the hex-encoded SHA-256 hash of the
	// raw token (so the binary []byte hash from ConsumeLoginToken
	// matches the map key format used in MemStore everywhere else).
	loginTokens map[string]LoginToken
	// cliAuthCodes is keyed by the SHA-256 hash of the raw code
	// (same key format as loginTokens). AccountID is empty until the
	// dashboard claims the code; the claim statement fills it in
	// atomically. See pkg/state/types.go CliAuthCode.
	cliAuthCodes map[string]CliAuthCode
	// accountPasswords is keyed by account_id (issue #165 / ADR-032).
	// OAuth-only accounts have no row; the absence of a row is the
	// signal that an OAuth-only flow is required to mint a session.
	accountPasswords map[string]AccountPassword
	// oauthLinks is keyed by (provider + "\x00" + provider_subject)
	// so the §11 anti-takeover invariant (one OAuth subject per
	// account) is enforceable in-memory the same way the composite
	// PK enforces it in Postgres. NUL separator is safe — neither
	// Google subs nor GitHub IDs contain it.
	oauthLinks map[string]OAuthLink
	// deploymentLogs is keyed by deployment_id; the inner slice is
	// append-ordered (which matches the Postgres seq order). MemStore
	// mirrors the bigserial PK by appending + assigning a monotonic
	// per-deployment counter so cursor pagination stays identical
	// to the production shape.
	deploymentLogs map[string][]LogEntry
	deploymentSeq  map[string]int64
	// deploymentSidecarLayers (issue #463 / ADR-069 / PR-B)
	// mirrors the per-workload filesystem handle table. Keyed by
	// "<deploymentID>\x00<sidecarName>" to give O(1) upsert +
	// list-by-deployment; the NUL separator is safe (sidecar
	// names are validated to a portable charset and
	// deploymentIDs are UUIDs).
	deploymentSidecarLayers map[string]DeploymentSidecarLayer
	snapshots               []Snapshot
	// snapshotReplicas mirrors snapshot_replicas (issue #1054). The
	// production worker uses the table as a durable cache-warming queue;
	// MemStore keeps the same state machine for scheduler and worker tests.
	snapshotReplicas map[snapshotReplicaKey]snapshotReplicaRow
	// snapshotOrigins records the producer node/locality for region-scoped
	// fan-out. Legacy snapshots without an entry remain globally eligible.
	snapshotOrigins map[string]snapshotOriginRow
	events          []Event
	// auditLog (issue #755 / PR-6) is the in-memory mirror of the
	// pgstore.audit_log table. Append-only by spec — MemStore has no
	// UpdateAuditLog / DeleteAuditLog pair. The DeleteAccount path
	// appends one account.deleted row inside the same critical
	// section that drops the accounts row, mirroring the PG
	// atomicity contract.
	auditLog []AuditLog
	// deploymentAudit is the in-memory mirror of the deployment_audit
	// table (migrations/00332_deployment_audit.sql, issue #976 /
	// ADR-122 / SAFE-RELEASES-E.2). Same shape as the PG row; the
	// memstore is the test-backend so handler tests can exercise the
	// read path without spinning Postgres. Append is mu-guarded;
	// reads are also mu-guarded so the loop is consistent with
	// the underlying slice growth under concurrent append.
	deploymentAudit []DeploymentAudit
	// usage holds one row per (instance, minute) — mirrors PgStore's
	// usage_minutes PK. Aggregated into `usageByMonth` (per app, per
	// calendar month) so UsageByMonth can keep returning the spec §10
	// per-app shape unchanged. (M7 fix; the previous shape was wrong.)
	usage        []usageMinute
	usageByMonth []Usage
	// builderUsage is the per-build grain backing AppendBuilderUsage
	// (ADR-048 §4). PK is build_id; the meterd rollup cron sums
	// into usage_daily.builder_seconds per (account, app, day).
	builderUsage []builderUsageRow
	idem         map[string]idemEntry
	// stripeByCustomer is the reverse-lookup index used by
	// AccountByProviderCustomerID; keyed by Stripe `cus_…` ID.
	stripeByCustomer map[string]string
	// invoices is the in-memory mirror of the `invoices` table
	// (migration 00050, issue #259). PR A reads via
	// ListInvoicesForAccount; PR B adds the writer
	// (UpsertInvoice via webhook ingestion). Seeded by tests for
	// parity-with-pgstore checks.
	invoices map[string]Invoice
	// accountCredits is the in-memory mirror of the `account_credits`
	// table (migration 00049, issue #279). Keyed by credit id. The
	// handler is the only writer in production; meterd never reads
	// this map (it reads the overage cap, which lives on accounts).
	accountCredits map[string]AccountCredit
	// creditLedger is the in-memory mirror of the `credit_ledger`
	// append-only audit log (migration 00049, issue #279). One row
	// per issuance (and per consumption, when the consumption reducer
	// lands). Slice so iteration order is deterministic.
	creditLedger []CreditLedgerEntry
	// overageCapCents is the in-memory mirror of
	// accounts.overage_cap_cents (migration 00049, issue #279). Keyed
	// by account id; the second value is `ok` (false = NULL).
	overageCapCents map[string]int64
	// gdprRequests is the in-memory mirror of the gdpr_requests ledger
	// row. MemStore does not auto-cascade on DeleteAccount (the
	// production pgstore does), but AppendGdprRequest rows here are
	// also intentionally NOT pruned — a unit test that asserts "after
	// DeleteAccount, the GDPR ledger still has the delete row" needs
	// them to survive.
	gdprRequests []GdprRequest
	// recentClaims is the B2.2 (issue #196) fairness mirror of the
	// recent_build_claims table — keyed by account_id, value is the
	// time of the LAST claim for that account within the window.
	// We keep only the latest per account (the SQL table keeps the
	// full row history; the MemStore only needs the timestamp to
	// answer `now.Sub(t) <= fairnessWindow` correctly).
	recentClaims map[string]time.Time
	// stripePushHours tracks which (account, hour) pairs the hourly
	// Stripe pusher has already pushed; prevents double-billing on
	// redelivery.
	stripePushHours map[stripePushKey]struct{}
	// webhookDeliveries tracks which (provider, delivery_id) pairs the
	// three webhook ingresses on the box have already processed; the
	// value is expires_at so CheckWebhookReplay can drop expired rows
	// inline at access time without a separate sweep goroutine (the
	// PgStore keeps the same invariant via the apid sweep goroutine).
	// Issue #294.
	webhookDeliveries map[webhookDeliveryKey]time.Time
	// paddleOverageMonths tracks which (account, month) pairs the daily
	// Paddle overage pusher has already flushed; same role as
	// stripePushHours but at the calendar-month grain because the
	// Paddle overage push fires at month-rollover rather than hourly.
	paddleOverageMonths map[paddleOverageKey]struct{}
	// paddleOverageWindows tracks the (account, window) per-window
	// claim state for the Paddle overage push. Replaces
	// paddleOverageMonths after the fix-PR for PR #204 review
	// findings (the month-scoped pair underbilled customers after
	// the first positive window of the month because the meterd loop
	// reads UsageByHour — window-scoped — but the dedupe row was
	// keyed by calendarMonthStart — month-scoped). The per-window
	// shape mirrors stripePushHours and the underlying schema's
	// (account_id, window_start) PK from migration 00037.
	paddleOverageWindows map[paddleOverageWindowKey]paddleOverageClaimState
	// secrets is keyed by (app_id, key) per the schema's PRIMARY KEY.
	// Value carries account_id for the ownership check on delete.
	secrets map[secretKey]AppSecret
	// registryCreds mirrors app_registry_credentials (issue #461 /
	// ADR-062). Same composite-key shape as secrets/envs. Value
	// carries account_id for the ownership check on delete and the
	// cross-account ErrNotFound predicate in Get.
	registryCreds map[registryCredKey]AppRegistryCredential
	// envs is the plaintext app_envs mirror (issue #395 / ADR-045).
	// Same composite-key shape as secrets; same ownership semantics.
	envs map[envKey]AppEnv
	// trustedSigners is the in-memory mirror of app_trusted_signers
	// (issue #472 / ADR-054). Populated by the admin CRUD handlers in
	// cmd/apid/handlers_trusted_signers.go; not exposed to schedd.
	trustedSigners map[trustedSignerKey]AppTrustedSigner
	// orgs / memberships / invitations are the IAM-6 / ADR-061 in-memory
	// mirrors. orgs is keyed by Org.ID; orgsBySlug (case-folded) backs
	// the case-insensitive OrgBySlug lookup; memberships are keyed by
	// the composite (orgID, accountID) PK; invitations by the SHA-256
	// token hash (hex-encoded for map safety). All maps are guarded
	// by m.mu and never escape the package — handlers go through
	// Store (this interface) so the sqlc vs memstore parity holds.
	orgs        map[string]Org
	orgsBySlug  map[string]string // lower(slug) -> id
	memberships map[orgAccountKey]OrgMembership
	invitations map[string]OrgInvitation // hex(hash) -> OrgInvitation
	// computeNodes mirrors the compute_nodes table; keyed by id (issue
	// #97 / ADR-025 axis 3). The synthetic 'default-local' row is
	// seeded by NewMemStore so tests don't have to call
	// CreateComputeNode to exercise the single-box path. Production
	// (PgStore) gets the same row from migrations/00024_compute_nodes.
	computeNodes map[string]ComputeNode
	// computeNodeKeys is the in-memory mirror of compute_node_keys
	// (migration 00076, ADR-053). Keyed by (nodeID, keyID) tuple
	// joined by a NUL so the composite key stays string-typed for
	// map lookup; the value is the canonical public_key_pem string.
	// Tests that don't pre-seed this map have empty key registries
	// — same as a fresh Postgres cluster with no vmmd registered
	// yet. The pkg/sched.NodeKeyRegistry's Refresh path treats
	// both as "no rows, return empty map".
	computeNodeKeys map[string]string
	// computeNodeHeartbeats is the append-only history (CP-1,
	// migration 00065). Mirrors the same wire shape as the SQL
	// table; rows are append-only, never mutated, and dropped with
	// the parent computeNode on hard-delete (mimicking the FK's
	// ON DELETE CASCADE). The cached map is keyed by node-id for
	// the read path; ListComputeNodeHeartbeats iterates the
	// per-node slice in received_at-desc order.
	computeNodeHeartbeats map[string][]ComputeNodeHeartbeat
	// sessions is the IAM-3 (ADR-039) in-memory mirror of the
	// `sessions` table — one row per dashboard login, keyed by
	// uuid. Revocation authority is RevokedAt != nil; LastSeenAt
	// may update post-revocation (operational signal only, not
	// authorization). MemStore's m.mu mirrors the SQL `for update`
	// semantics the PgStore uses.
	sessions map[string]Session
	// projects is the ADR-050 Phase 1 in-memory mirror of the
	// `projects` table. Keyed by project id. The two secondary
	// indexes mirror the (account_id, slug) and (install_id,
	// repo_full_name) partial uniques from migration 00073 so
	// ProjectBySlug / ProjectByRepo are O(1) lookups the same way
	// PgStore's btrees are.
	projects              map[string]Project
	projectsByAccountSlug map[string]map[string]string // account_id → slug → id
	projectsByInstallRepo map[installRepoKey]string    // install_id, repo_full_name → id
	// clock is the seam CurrentMonthOverageCents uses to compute the
	// UTC month-start cutoff. Default is time.Now (production); tests
	// install a fixture via SetClockForTest so a usage row planted at
	// "2026-07-21" is still visible to a CurrentMonthOverageCents call
	// later in the test (issue surfaced 2026-08-01 — the meterd
	// quota-tick tests' "1200 cents of derived overage at fixture
	// 2026-07-21" read zero because real wall-clock had advanced to
	// 2026-08-01, putting the row's Minute before the current
	// monthStart and silently filtering it out). Protected by m.mu.
	clock func() time.Time
}

// installRepoKey mirrors the projects_install_repo_uniq partial index
// (install_id, repo_full_name) from migration 00073. Standalone
// projects (install_id == nil, repo_full_name == "") never enter this
// map; the indexed lookups are bound-only.
type installRepoKey struct {
	InstallID    int64
	RepoFullName string
}

// secretKey mirrors the app_secrets PRIMARY KEY (app_id, scope, key)
// post-ADR-092-PR-A (migration 00214). Pre-PR the PK was (app_id,
// key); the Scope field is always 'default' for the flat methods
// (UpsertAppSecret / DeleteAppSecret / ListAppSecrets /
// CountAppSecrets) and is the caller-supplied value for the …InScope
// variants. The scope literal is the same one the schema's
// fast-default picks for pre-00214 rows.
type secretKey struct {
	AppID string
	Scope string
	Key   string
}

// envKey mirrors the app_envs PRIMARY KEY (app_id, scope, key)
// post-ADR-090-PR-A (migration 00203). Pre-PR the PK was
// (app_id, key); the Scope field is always 'default' for the flat
// methods (UpsertAppEnv / DeleteAppEnv / ListAppEnv / CountAppEnv)
// and is the caller-supplied value for the …InScope variants. The
// scope literal is the same one the schema's fast-default picks for
// pre-00203 rows.
type envKey struct {
	AppID string
	Scope string
	Key   string
}

// trustedSignerKey mirrors the app_trusted_signers PRIMARY KEY
// (app_id, signer_name) added in migration 00083 (issue #472 / ADR-058).
// Same composite-key shape as secretKey/envKey by intent — the handler
// URL is /v1/apps/{slug}/trusted_signers/{name}, so the resource IS the
// signer_name.
type trustedSignerKey struct {
	AppID      string
	SignerName string
}

// registryCredKey mirrors the app_registry_credentials UNIQUE
// (app_id, registry) constraint. Registry is the normalized host
// (lowercase, no scheme/path/trailing-slash, port preserved) — the
// handler normalizes both at PUT and at imaged lookup so the same
// key shape reaches the map regardless of caller.
type registryCredKey struct {
	AppID    string
	Registry string
}

type idemEntry struct {
	status  int
	body    []byte
	created time.Time
}

// usageMinute mirrors the production schema (PK (instance_id, minute)).
// Spec §10 keeps per-app aggregates for the dashboard, but the SQL contract
// is per-instance — accumulating mb_seconds on conflict is the atomic
// operation the minute sampler relies on. MemStore matches that shape so
// tests are truthful for M7. Aggregated into `usageByMonth` for the
// per-app read shape the rest of the system expects.
type usageMinute struct {
	AccountID  string
	AppID      string
	InstanceID string
	Minute     time.Time
	MBSeconds  int64
	Requests   int64
	// CPUUsec is the cumulative host cgroup CPU-µs consumed by the
	// instance during this minute. Source: vmmd cpustats.Cache
	// (cpu.stat usage_usec delta) → schedd instancestats.Poller →
	// meterd Sampler. Measurement only — billing is on plan RAM
	// (pkg/api/limits.go). Additive on conflict (instance_id,
	// minute) — see AppendUsage doc for the rationale.
	CPUUsec int64
	// TXBytes is the cumulative HTTP response body bytes the
	// gateway forwarded for this instance in this minute.
	// Source: pkg/gateway/handler.go statusRecorder.Bytes →
	// meterd Sampler. ADR-046. Informational — not billed.
	// Additive on conflict (instance_id, minute).
	TXBytes int64
	// NetTxBytes is the cumulative byte delta on root-side
	// vethHost.rx_bytes for this instance in this minute.
	// Source: vmmd pkg/fcvm/netstats.Cache → schedd
	// instancestats.Poller → meterd Sampler. ADR-046.
	// Informational — not billed. Additive on conflict
	// (instance_id, minute). Unit = interface bytes (incl.
	// framing).
	NetTxBytes int64
	// NetRxBytes is the cumulative byte delta on root-side
	// vethHost.tx_bytes (root→guest = ingress) for this
	// instance in this minute. Source: vmmd pkg/fcvm/netstats
	// TX cache → schedd instancestats.Poller → meterd Sampler.
	// ADR-048. Informational — not billed. Additive on conflict
	// (instance_id, minute). Unit = interface bytes.
	NetRxBytes int64
	// ColdBootCount is the per-minute count of
	// WAKE_RESTORE→WAKE_COLD_BOOT transitions observed for this
	// instance. Source: scheddgrpc.InstanceStatsRow.LastWakeMethod,
	// sampled by meterd Sampler. ADR-048. Informational — not
	// billed. Additive on conflict (idempotent — only the
	// transition counts; a redelivered tick within the same
	// minute is a no-op).
	ColdBootCount int32
	// TailSeconds (issue #667 / ADR-078) is the per-minute wall-clock
	// seconds this instance spent draining waitUntil tasks. Source:
	// vmmd pkg/fcvm.Manager.ReadAndResetTailSeconds, sampled by
	// meterd Sampler. INFORMATIONAL ONLY — does NOT enter billing.
	// Additive on conflict (instance_id, minute) — mirrors the
	// cpu_usec / tx_bytes shape. Pinned by
	// pkg/meter/pusher_shadow_test.go::TestPushHour_ExcludesTailSeconds.
	TailSeconds int64
}

// builderUsageRow is the per-build grain (ADR-048 §4) backing
// AppendBuilderUsage. Mirrors builder_usage created by
// migrations/00068_builder_usage.sql. PK is BuildID; first write
// wins, a redelivered webhook / meterd restart is a no-op. The
// meterd rollup cron sums Seconds into usage_daily.builder_seconds.
// The table is search_path-relative; production search_path=public
// puts it in the public schema, pgtest-isolated tests put it in a
// faas_test_<hex> schema (the schema scoping closes the 40P01
// deadlock on pg_class when N parallel test packages race CREATE
// TABLE on the same cluster).
type builderUsageRow struct {
	BuildID    string
	AccountID  string
	AppID      string
	FinishedAt time.Time
	Kind       string
	Seconds    int64
}

// NewMemStore returns an empty in-memory store with the synthetic
// 'default-local' compute_node row seeded (issue #97 / ADR-025 axis 3).
// The seed mirrors migrations/00024_compute_nodes.sql so unit tests
// don't have to call CreateComputeNode to exercise the single-box path.
// Production (PgStore) gets the same row from the migration.
func NewMemStore() *MemStore {
	m := &MemStore{
		accounts:       map[string]Account{},
		keys:           map[string]APIKey{},
		keyByHash:      map[string]APIKey{},
		apps:           map[string]App{},
		githubBindings: map[string]GitHubBinding{},
		githubInstalls: map[string]GitHubInstall{},
		// PR-D / ADR-012 §7 amendment: per-tenant webhook secret
		// store (mirror of github_webhook_secrets).
		githubWebhookSecrets:    map[int64][]byte{},
		githubWebhookSecretMeta: map[int64]webhookSecretMeta{},
		deployments:             map[string]Deployment{},
		builds:                  map[string]Build{},
		// buildProvenance is the ADR-038 "what ran?" map keyed by
		// build_id (mirrors the build_provenance.build_id UNIQUE).
		// Starts empty; CreateBuildProvenance fills it.
		buildProvenance: map[string]BuildProvenance{},
		// issue #72 / ADR-125: mirror-rules and mirror-results
		// stores. Empty until the first create; the per-app count
		// in CreateMirrorRuleIfUnderQuota walks the map.
		// Rebase resolution (2026-08-27): merge HEAD's added fields
		// (fireNowRequests, operatorIntents, runtimeConfigs +
		// operations + revisions, alertPresets, accountSpendSnapshots,
		// openAPIImports) with the cluster's deploymentScopeExclusions
		// (ADR-124 follow-up #3). Both sides are non-overlapping
		// additive fields; column alignment kept (visual width per
		// the table below) so the diff against `gofmt -s` stays clean.
		mirrorRules:               map[string]MirrorRule{},
		mirrorResults:             map[string]MirrorInvocationResult{},
		domains:                   map[string]CustomDomain{},
		doctorObs:                 map[string]DomainDoctorObservation{},
		crons:                     map[string]Cron{},
		fireNowRequests:           map[string]FireNowRequest{},
		operatorIntents:           map[string]OperatorIntent{},
		runtimeConfigs:            map[string]RuntimeConfig{},
		runtimeConfigOperations:   map[string]RuntimeConfigOperation{},
		runtimeConfigRevisions:    []RuntimeConfigRevision{},
		alertRules:                map[string]AlertRule{},
		alertDeliveries:           map[string]AlertDelivery{},
		appWebhooks:               map[string]AppWebhook{},
		appWebhookDeliveries:      map[string]AppWebhookDelivery{},
		deploymentScopeExclusions: map[string]DeploymentScopeExclusion{}, // ADR-124 follow-up #3
		uploadSessions:            map[string]sqlc.UploadSession{},
		uploadCommitOutcomes:      map[string]sqlc.UploadCommitOutcome{},
		alertClaimKeys:            map[string]time.Time{},
		edgeRules:                 map[string]EdgeRule{},
		corsPresets:               map[string]CorsPreset{},
		openAPIDocs:               map[string]openAPIDocRow{},
		// ADR-126 / issue #975 item #2 — per-app OpenAPI imports.
		// Keyed by app_id (one row per app, last-write-wins via
		// the existing overwrite-not-insert contract). Same IDOR
		// floor at the read methods as the pg path.
		openAPIImports:                map[string]appOpenAPIImportRow{},
		alertPresets:                  map[string]AlertPreset{},
		accountSpendSnapshots:         map[string]AccountSpendSnapshot{},
		tenantSurfaceCertExpiryStates: map[string]TenantSurfaceCertExpiryState{},
		// ADR-120 / issue #975 item #5 — consumer keys. The map is
		// keyed by ConsumerKey.ID; cross-tenant IDOR guards are
		// enforced at the read methods (same as the pg path).
		consumerKeys:     map[string]ConsumerKey{},
		openAPISnapshots: map[string]OpenAPISnapshot{},
		// ADR-119 redesign: empty gate (no provisioned IPs in
		// unit tests unless a test explicitly seeds them).
		provisionedStaticEgressIPs: map[string]map[string]netip.Addr{},
		// ADR-101 / issue #270 — OIDC trust policies + exchanged
		// bearers. Start empty; tests inject rows directly.
		oidcTrustPolicies:   map[string]OIDCTrustPolicy{},
		oidcExchangedTokens: map[string]OIDCExchangedToken{},
		// ADR-100 / tenant surfaces — see memstore_tenant_surface.go.
		tenantSurfaces:   map[string]TenantSurface{},
		tenantHostnames:  map[string]TenantHostname{},
		invocations:      map[string]Invocation{},
		instances:        map[string]Instance{},
		loginTokens:      map[string]LoginToken{},
		cliAuthCodes:     map[string]CliAuthCode{},
		accountPasswords: map[string]AccountPassword{},
		oauthLinks:       map[string]OAuthLink{},
		deploymentLogs:   map[string][]LogEntry{},
		deploymentSeq:    map[string]int64{},
		// Issue #463 / ADR-069 / PR-B — per-workload filesystem
		// handles (mirrors migration 00119's PK + ON CONFLICT
		// semantics).
		deploymentSidecarLayers: map[string]DeploymentSidecarLayer{},
		snapshots:               []Snapshot{},
		snapshotReplicas:        map[snapshotReplicaKey]snapshotReplicaRow{},
		snapshotOrigins:         map[string]snapshotOriginRow{},
		events:                  []Event{},
		usage:                   []usageMinute{},
		usageByMonth:            []Usage{},
		idem:                    map[string]idemEntry{},
		// stripeByCustomer is the reverse-lookup map AccountByProviderCustomerID
		// walks; populated by UpdateAccountProviderCustomerID.

		stripeByCustomer: map[string]string{},
		// invoices starts empty; PR A reads it via ListInvoicesForAccount,
		// PR B writes via UpsertInvoice (webhook ingestion).
		invoices: map[string]Invoice{},
		// accountCredits starts empty; the operator-only
		// POST /v1/admin/accounts/{id}/credits path is the sole writer.
		accountCredits: map[string]AccountCredit{},
		// creditLedger starts empty; AppendCreditLedgerEntry records
		// every issuance/consumption as an immutable audit row.
		creditLedger: []CreditLedgerEntry{},
		// overageCapCents starts empty (no caps configured).
		overageCapCents: map[string]int64{},
		// gdprRequests starts empty; AppendGdprRequest appends.
		gdprRequests: nil,
		// recentClaims is the B2.2 fairness mirror; starts empty so
		// the FIRST ClaimNextQueuedBuildWithFairness round picks from
		// every queued row (no account is in the skip set yet).
		recentClaims: map[string]time.Time{},
		// stripePushHours is the per-(account, hour) dedupe set the
		// meterd hourly pusher reads/writes.
		stripePushHours: map[stripePushKey]struct{}{},
		// webhookDeliveries is the per-(provider, delivery_id) dedupe
		// set the three webhook ingresses on the box read/write;
		// expires_at is the value so the TTL is honored at access
		// time. Issue #294.
		webhookDeliveries: map[webhookDeliveryKey]time.Time{},
		// paddleOverageMonths is the per-(account, month) dedupe set
		// the meterd daily pusher reads/writes. Kept for back-compat
		// with the deprecated state.Store month-scoped pair; new code
		// paths use paddleOverageWindows below.
		paddleOverageMonths: map[paddleOverageKey]struct{}{},
		// paddleOverageWindows is the per-(account, window) claim
		// state map the meterd pusher uses post-PR-#204. Migration
		// 00037 added the (account_id, window_start) PK + state
		// column to paddle_overage_dedupe; this in-memory mirror
		// keeps the MemStore parity tests in lockstep.
		paddleOverageWindows: map[paddleOverageWindowKey]paddleOverageClaimState{},
		secrets:              map[secretKey]AppSecret{},
		registryCreds:        map[registryCredKey]AppRegistryCredential{},
		envs:                 map[envKey]AppEnv{},
		trustedSigners:       map[trustedSignerKey]AppTrustedSigner{},
		orgs:                 map[string]Org{},
		orgsBySlug:           map[string]string{},
		memberships:          map[orgAccountKey]OrgMembership{},
		invitations:          map[string]OrgInvitation{},
		// computeNodes is empty here; seedDefaultLocalNodeLocked
		// inserts the synthetic default-local row below.
		computeNodes: map[string]ComputeNode{},
		// computeNodeKeys is the in-memory mirror of
		// compute_node_keys (migration 00076). Empty by default
		// — vmmd's self-registration populates it via
		// UpsertNodeKey. Tests that exercise the slice-3
		// signature path inject rows by calling the method
		// directly; tests that don't care about slice-3 see
		// an empty map (same as a fresh Postgres cluster).
		computeNodeKeys: map[string]string{},
		// computeNodeHeartbeats is the CP-1 history mirror. Empty here;
		// rows accumulate via AppendComputeNodeHeartbeat as the schedd
		// Heartbeat.Tick goroutine (or test setup) drives them.
		computeNodeHeartbeats: map[string][]ComputeNodeHeartbeat{},
		// sessions is empty here; populated by CreateSession at each
		// dashboard login (handlers_auth*.go + handlers_mfa reissue).
		sessions:              map[string]Session{},
		projects:              map[string]Project{},
		projectsByAccountSlug: map[string]map[string]string{},
		projectsByInstallRepo: map[installRepoKey]string{},
	}
	// Auto-seed default-local. Done after the struct literal so the
	// seeded row carries a real id and created_at timestamp. Mirrors
	// migrations/00024_compute_nodes.sql: the only way a test's
	// single-box flow can fail is if the seed's contract drifts from
	// the migration's seed (e.g., target_url mismatch); both land in
	// the test 00024_compute_nodes_test.go.
	m.seedDefaultLocalNodeLocked()
	// Default clock is real wall-clock; tests override via
	// SetClockForTest so a fixture-time AppendUsage row is still
	// visible to CurrentMonthOverageCents later in the same test.
	m.clock = func() time.Time { return time.Now().UTC() }
	return m
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// --- Accounts ---------------------------------------------------------------

func (m *MemStore) CreateAccount(_ context.Context, email string, plan api.Plan) (Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.accounts {
		if a.Email == email {
			return Account{}, fmt.Errorf("state: account with email %q exists", email)
		}
	}
	a := Account{ID: newID(), Email: email, Plan: plan, Status: AccountActive, CreatedAt: time.Now()}
	m.accounts[a.ID] = a
	return a, nil
}

// CreateAccountWithPersonalOrg is the PR 3 canonical
// account-creation entry point (issue #190 / ADR-061). The m.mu
// lock is the atomicity boundary — under the lock we run the email
// uniqueness probe, the personal-org uniqueness probe, the three
// in-memory inserts, and return. The mutex serialises concurrent
// callers so the partial-unique invariant the SQL partial index
// enforces is preserved in MemStore.
func (m *MemStore) CreateAccountWithPersonalOrg(_ context.Context, params CreateAccountWithPersonalOrgParams) (CreateAccountWithPersonalOrgResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 1. Email uniqueness probe.
	for _, a := range m.accounts {
		if a.Email == params.Email {
			return CreateAccountWithPersonalOrgResult{}, ErrConflict
		}
	}
	now := time.Now().UTC()
	acct := Account{
		ID:        newID(),
		Email:     params.Email,
		Plan:      params.Plan,
		Status:    AccountActive,
		CreatedAt: now,
	}
	m.accounts[acct.ID] = acct

	// 2. Personal org uniqueness probe — mirrors the SQL partial
	//    unique orgs_one_personal_per_account_uniq.
	ownerID := acct.ID
	for _, existing := range m.orgs {
		if existing.Personal && existing.PersonalOwnerAccountID != nil &&
			*existing.PersonalOwnerAccountID == ownerID {
			// Roll back the account insert so the surrounding mutex
			// doesn't observe a half-state if a future caller relies
			// on the map's invariants.
			delete(m.accounts, acct.ID)
			return CreateAccountWithPersonalOrgResult{}, ErrConflict
		}
	}
	org := Org{
		ID:                     newID(),
		Slug:                   PersonalOrgSlug(acct.ID),
		Name:                   "Personal",
		Personal:               true,
		PersonalOwnerAccountID: &ownerID,
		Plan:                   acct.Plan,
		Status:                 OrgStatusActive,
		CreatedAt:              now,
		UpdatedAt:              now,
	}
	m.orgs[org.ID] = org
	m.orgsBySlug[strings.ToLower(org.Slug)] = org.ID

	// 3. Owner membership row. The first owner for the org — the
	//    last-owner guard from AddOrgMember is irrelevant here.
	m.memberships[orgAccountKey{OrgID: org.ID, AccountID: acct.ID}] = OrgMembership{
		OrgID:     org.ID,
		AccountID: acct.ID,
		Role:      OrgRoleOwner,
		JoinedAt:  now,
	}
	return CreateAccountWithPersonalOrgResult{Account: acct, PersonalOrg: org}, nil
}

func (m *MemStore) AccountByID(_ context.Context, id string) (Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[id]
	if !ok {
		return Account{}, ErrNotFound
	}
	return a, nil
}

// AccountsByIDs is the batch equivalent of AccountByID for the
// in-memory store. Function-top lock + map absence + no per-id
// error shape — mirrors the ListLatestInstancePerApp pattern.
//
// PR-9 §1: closes the N+1 fan-out in
// cmd/apid/handlers_dashboard.go's renderOrgDetail member loop.
func (m *MemStore) AccountsByIDs(_ context.Context, ids []string) (map[string]Account, error) {
	out := make(map[string]Account, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range ids {
		if a, ok := m.accounts[id]; ok {
			out[id] = a
		}
	}
	return out, nil
}

func (m *MemStore) AccountByEmail(_ context.Context, email string) (Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.accounts {
		if a.Email == email {
			return a, nil
		}
	}
	return Account{}, ErrNotFound
}

func (m *MemStore) AccountByKeyHash(_ context.Context, hash []byte) (Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.keyByHash[hex.EncodeToString(hash)]
	if !ok {
		return Account{}, ErrNotFound
	}
	return m.accounts[k.AccountID], nil
}

// APIKeyByHash resolves an api_keys row by its SHA-256 hash. Used by
// the post-login audit log (cmd/apid/handlers_auth.go) so an operator
// investigating "who signed in as alice?" can identify which key
// authenticated. Returns ErrNotFound when no row matches.
func (m *MemStore) APIKeyByHash(_ context.Context, hash []byte) (APIKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.keyByHash[hex.EncodeToString(hash)]
	if !ok {
		return APIKey{}, ErrNotFound
	}
	return k, nil
}

// AuthenticateKey mirrors the key+account lookup the apid auth
// middleware needs. Single lock acquisition; returns ErrNotFound when
// the hash has no matching key. See ADR-034.
//
// IAM-5 (issue #189) gate: identical to PgStore.AuthenticateKey.
// After the key row is loaded, three checks run in order —
//
//  1. status='revoked' → return ErrAPIKeyRevoked (terminal, idempotent).
//  2. expires_at != nil && expires_at < now() → lazy-flip to
//     status='revoked' in-memory, then return ErrAPIKeyExpired.
//  3. otherwise return (account, key, nil).
//
// The MemStore mutation is the analogue of the pgstore UPDATE:
// coalesce(revoked_at, now()) → the first observation stamps, the
// second is a no-op.
// AuthenticateOIDCBearer resolves an OIDC-derived short-lived bearer
// (issue #270 / ADR-101). Hash lookup hits the in-memory
// oidcExchangedTokens map (the UNIQUE token_hash index from
// migration 00266); rows past ExpiresAt return ErrNotFound (the
// 5-min TTL is the natural expiry path; no lazy-flip required).
// Returns the Account + a synthetic APIKey projection with
// Scopes=["deploy:write"] and Status="active" so the principal
// stamp + downstream requireScope chain works unchanged.
//
// The contract is identical to the PgStore impl modulo the
// concrete storage. Both return ErrNotFound; both project a
// synthetic APIKey with status=active.
//
// The Caller does NOT call TouchKeyLastUsed on this branch — the
// 5-min TTL bounds the write load, and the per-exchange audit row
// in pkg/oidc/handler.go is the durable record.
func (m *MemStore) AuthenticateOIDCBearer(_ context.Context, hash []byte) (Account, APIKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.oidcExchangedTokens[hex.EncodeToString(hash)]
	if !ok {
		return Account{}, APIKey{}, ErrNotFound
	}
	acct, ok := m.accounts[row.AccountID]
	if !ok {
		// Account was deleted between the exchange and the bearer
		// hit. Treat as not-found; the audit reader can correlate
		// from the row (which is also gone via FK CASCADE in pg).
		return Account{}, APIKey{}, ErrNotFound
	}
	if !row.ExpiresAt.IsZero() && row.ExpiresAt.Before(time.Now()) {
		// Lazy-flip: the row is past TTL. Remove it now so a
		// follow-up request doesn't go through the same code path.
		// The pg contract is `WHERE expires_at > NOW()`; the
		// MemStore mirrors by physically deleting on miss.
		delete(m.oidcExchangedTokens, hex.EncodeToString(hash))
		return Account{}, APIKey{}, ErrNotFound
	}
	return acct, row.ToAPIKey(), nil
}

// AccountByOIDCSubject resolves an OIDC subject to the platform
// account it's bound to. Used by pkg/oidc/handler.go step 4 to
// determine the (account_id, issuer_url) trust-policy row.
//
// The binding is implicit: the trust policy owns the issuer URL,
// the OIDC subject is part of the issuer's contract. For the
// first-use auto-create to commit, the handler Upserts on
// ErrTrustPolicyNotFound AFTER resolving the account — so this
// method needs to short-circuit any caller that doesn't have a
// matching account_by_oidc_subject row.
//
// Today (PR-A) we tie the binding to the account's email match
// against a configurable subject template (subject_pattern).
// PR-C will refine the binding surface (per-org memberships,
// service-account subjects).
func (m *MemStore) AccountByOIDCSubject(_ context.Context, issuerURL, subject string) (Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Scan the trust-policy map for an (account_id, issuer_url,
	// subject_pattern) match. The subject_pattern is a regex
	// compiled on the OIDC side (pkg/edgejwks layer); here we
	// pass it through without recompiling — the store contract is
	// "match the policy, return the account".
	//
	// Prefer a matching subject pattern over a permissive policy, then
	// use stable tie-breakers. This mirrors the PostgreSQL query and
	// prevents a wildcard policy from shadowing a more specific policy
	// when multiple accounts trust the same issuer.
	//
	// For the PR-A scope this is correct: the trust policy is
	// auto-created on first exchange, AFTER AccountByOIDCSubject
	// succeeds, so the first call for a new (issuer, subject) MUST
	// return ErrNotFound for the auto-create to fire on the
	// subsequent retry. The handler's two-phase flow (verify +
	// resolve account + retry Get) handles the chicken-and-egg.
	//
	// TODO(ADR-101 PR-C): replace the map scan with a per-issuer
	// reverse index once the dashboard exposes "which subjects
	// does this account trust" — needs a column on the trust
	// policy for binding subjects explicitly.
	matches := make([]OIDCTrustPolicy, 0, len(m.oidcTrustPolicies))
	for _, policy := range m.oidcTrustPolicies {
		if policy.IssuerURL != issuerURL {
			continue
		}
		// Empty subject_pattern = accept any subject. Real
		// pattern = regex match.
		matched := policy.SubjectPattern == ""
		if !matched {
			matched = regexpMatch(policy.SubjectPattern, subject)
		}
		if !matched {
			continue
		}
		_, ok := m.accounts[policy.AccountID]
		if !ok {
			continue
		}
		matches = append(matches, policy)
	}
	if len(matches) == 0 {
		return Account{}, ErrNotFound
	}
	sort.Slice(matches, func(i, j int) bool {
		iSpecific := matches[i].SubjectPattern != ""
		jSpecific := matches[j].SubjectPattern != ""
		if iSpecific != jSpecific {
			return iSpecific
		}
		if len(matches[i].SubjectPattern) != len(matches[j].SubjectPattern) {
			return len(matches[i].SubjectPattern) > len(matches[j].SubjectPattern)
		}
		return matches[i].AccountID < matches[j].AccountID
	})
	return m.accounts[matches[0].AccountID], nil
}

// regexpMatch is a tiny inlined wrapper to keep the import surface
// minimal. The subject pattern is a fragment of Go's regexp
// syntax (matches `regexp.MatchString`); compile-once-per-policy
// is a future PR-C refinement (caching the compiled regex on the
// policy row).
func regexpMatch(pattern, s string) bool {
	return regexp.MustCompile(pattern).MatchString(s)
}

// UpsertOIDCTrustPolicy is the per-(account, issuer) insert-or-update
// path (issue #270 / ADR-101). Mirrors the PgStore shape (ON
// CONFLICT in pg; in m.oidcTrustPolicies the same key just
// overwrites). The CreatedAt is preserved across upserts so the
// audit row's "first-use" timestamp is stable.
func (m *MemStore) UpsertOIDCTrustPolicy(_ context.Context, p *OIDCTrustPolicy) (*OIDCTrustPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := p.AccountID + "\x00" + p.IssuerURL
	if existing, ok := m.oidcTrustPolicies[key]; ok {
		// Preserve CreatedAt across upserts.
		p.CreatedAt = existing.CreatedAt
	}
	p.UpdatedAt = time.Now()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = p.UpdatedAt
	}
	m.oidcTrustPolicies[key] = *p
	// Return a copy so the caller can't mutate the in-memory row.
	cp := *p
	return &cp, nil
}

// GetOIDCTrustPolicy is the (account_id, issuer_url) lookup.
// Returns ErrNotFound on miss.
func (m *MemStore) GetOIDCTrustPolicy(_ context.Context, accountID, issuerURL string) (*OIDCTrustPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := accountID + "\x00" + issuerURL
	p, ok := m.oidcTrustPolicies[key]
	if !ok {
		return nil, ErrNotFound
	}
	cp := p
	return &cp, nil
}

// ListOIDCTrustPoliciesForAccount returns every trust policy the
// account owns. Empty slice on miss.
func (m *MemStore) ListOIDCTrustPoliciesForAccount(_ context.Context, accountID string) ([]*OIDCTrustPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*OIDCTrustPolicy, 0)
	for _, p := range m.oidcTrustPolicies {
		if p.AccountID != accountID {
			continue
		}
		cp := p
		out = append(out, &cp)
	}
	return out, nil
}

// InsertOIDCExchangedToken stores a fresh exchanged-token row and
// returns the server-minted row id. The hash field is the unique
// key; the id is the audit-correlation key — pkg/oidc/handler.go
// echoes it in the response and stamps it on the audit row.
func (m *MemStore) InsertOIDCExchangedToken(_ context.Context, t *OIDCExchangedToken) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now()
	}
	m.oidcExchangedTokens[hex.EncodeToString(t.TokenHash)] = *t
	return t.ID, nil
}

// GetOIDCExchangedTokenByHash returns the row whose TokenHash
// equals the input. Returns ErrNotFound on miss. Lazy-expires
// past-TTL rows (mirrors the pg `WHERE expires_at > NOW()`
// contract).
func (m *MemStore) GetOIDCExchangedTokenByHash(_ context.Context, hash []byte) (*OIDCExchangedToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.oidcExchangedTokens[hex.EncodeToString(hash)]
	if !ok {
		return nil, ErrNotFound
	}
	if !row.ExpiresAt.IsZero() && row.ExpiresAt.Before(time.Now()) {
		delete(m.oidcExchangedTokens, hex.EncodeToString(hash))
		return nil, ErrNotFound
	}
	cp := row
	return &cp, nil
}

// DeleteOIDCExchangedToken is the operator-driven revoke path.
// Returns ErrNotFound when no row matches.
func (m *MemStore) DeleteOIDCExchangedToken(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, row := range m.oidcExchangedTokens {
		if row.ID == id {
			delete(m.oidcExchangedTokens, k)
			return nil
		}
	}
	return ErrNotFound
}

func (m *MemStore) AuthenticateKey(_ context.Context, hash []byte) (Account, APIKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.keyByHash[hex.EncodeToString(hash)]
	if !ok {
		return Account{}, APIKey{}, ErrNotFound
	}
	acct, ok := m.accounts[k.AccountID]
	if !ok {
		return Account{}, APIKey{}, ErrNotFound
	}
	if k.Status == string(APIKeyStatusRevoked) {
		return Account{}, APIKey{}, ErrAPIKeyRevoked
	}
	if k.ExpiresAt != nil && !k.ExpiresAt.IsZero() && k.ExpiresAt.Before(time.Now()) {
		k.Status = string(APIKeyStatusRevoked)
		if k.RevokedAt == nil {
			now := time.Now()
			k.RevokedAt = &now
		}
		m.keys[k.ID] = k
		m.keyByHash[hex.EncodeToString(k.Hash)] = k
		return Account{}, APIKey{}, ErrAPIKeyExpired
	}
	return acct, k, nil
}

func (m *MemStore) UpdateAccountPlan(_ context.Context, id string, plan api.Plan) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[id]
	if !ok {
		return ErrNotFound
	}
	a.Plan = plan
	m.accounts[id] = a
	return nil
}

func (m *MemStore) UpdateAccountStatus(_ context.Context, id string, status AccountStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[id]
	if !ok {
		return ErrNotFound
	}
	a.Status = status
	m.accounts[id] = a
	return nil
}

// --- MFA (IAM-2, issue #186) -------------------------------------------------
//
// In-memory parallel of PgStore's MFA Store methods. The Account
// fields MFAEnrolledAt / MFASecretEncrypted / MFARecoveryCodesHash
// / MFARequired land on the struct in pkg/state/types.go; the
// methods below are the load-bearing writers + the consume-atomically
// helper + one count helper.

// ConsumeRecoveryCode atomically matches `presented` against the
// stored SHA-256 recovery-code hashes and removes the matching hash
// from the array. MemStore runs the read + compare + mutate + write
// under one m.mu acquisition so two concurrent /recover calls on
// the same account cannot both observe and burn the same hash.
// See pkg/state/pgstore.go::ConsumeRecoveryCode for the Postgres
// precedent; the contract is identical.
//
// Returns:
//   - (false, 0, false, ErrNotFound) when the row is missing
//   - (false, 0, false, nil)         when no hash matched
//   - (true, lastCode, remaining, nil) on success; lastCode is true
//     iff exactly one hash remained; remaining is the count of hashes
//     on the row AFTER the consume committed, used by the handler to
//     render the post-burn customer email with the right tone
//     (one-of-many vs warning vs last-code) — see issue #329.
func (m *MemStore) ConsumeRecoveryCode(_ context.Context, id string, presented []byte) (matched bool, lastCode bool, remaining int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[id]
	if !ok {
		return false, false, 0, ErrNotFound
	}
	matchedIdx := -1
	for i, h := range a.MFARecoveryCodesHash {
		if Sha256Equal(h, presented) {
			matchedIdx = i
			break
		}
	}
	if matchedIdx < 0 {
		return false, false, 0, nil
	}
	lastCode = len(a.MFARecoveryCodesHash) == 1
	next := make([][]byte, 0, len(a.MFARecoveryCodesHash)-1)
	next = append(next, a.MFARecoveryCodesHash[:matchedIdx]...)
	next = append(next, a.MFARecoveryCodesHash[matchedIdx+1:]...)
	a.MFARecoveryCodesHash = next
	m.accounts[id] = a
	return true, lastCode, len(next), nil
}

// MatchRecoveryCode tests a presented SHA-256 hash against the
// stored set WITHOUT mutating. Used by /recover to refuse-the-burn
// on the customer's last code (issue #186 review Finding #5). On
// MemStore the read lock + the absence of any write make this a
// pure projection of ConsumeRecoveryCode minus the mutation.
func (m *MemStore) MatchRecoveryCode(_ context.Context, id string, presented []byte) (bool, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[id]
	if !ok {
		return false, false, ErrNotFound
	}
	for _, h := range a.MFARecoveryCodesHash {
		if Sha256Equal(h, presented) {
			return true, len(a.MFARecoveryCodesHash) == 1, nil
		}
	}
	return false, false, nil
}

// SetMFASecret writes the sealed secret + recovery-code hashes
// WITHOUT stamping mfa_enrolled_at. Idempotent re-enrollment.
func (m *MemStore) ReadMFASecret(_ context.Context, id string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[id]
	if !ok {
		return nil, ErrNotFound
	}
	if a.MFASecretEncrypted == nil {
		return nil, ErrNotFound
	}
	return slices.Clone(a.MFASecretEncrypted), nil
}

func (m *MemStore) SetMFASecret(_ context.Context, id string, encrypted []byte, recoveryHashes [][]byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[id]
	if !ok {
		return ErrNotFound
	}
	a.MFASecretEncrypted = slices.Clone(encrypted)
	a.MFARecoveryCodesHash = make([][]byte, len(recoveryHashes))
	for i, h := range recoveryHashes {
		a.MFARecoveryCodesHash[i] = slices.Clone(h)
	}
	m.accounts[id] = a
	return nil
}

// MarkMFAEnrolled stamps mfa_enrolled_at = now() and clears
// mfa_required = false. Idempotent on retry.
func (m *MemStore) MarkMFAEnrolled(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[id]
	if !ok {
		return ErrNotFound
	}
	now := time.Now().UTC()
	a.MFAEnrolledAt = &now
	a.MFARequired = false
	m.accounts[id] = a
	return nil
}

// ClearMFA nulls the secret + recovery-codes + enrolled_at. Does
// NOT touch mfa_required so an explicit policy remains in force.
// The audit Emit is the caller's job; the events table is the audit
// trail.
func (m *MemStore) ClearMFA(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[id]
	if !ok {
		return ErrNotFound
	}
	a.MFASecretEncrypted = nil
	a.MFARecoveryCodesHash = nil
	a.MFAEnrolledAt = nil
	m.accounts[id] = a
	return nil
}

// SetMFARequired writes the explicit policy flag and reports whether
// the row actually changed. Mirrors the `WHERE mfa_required <> $2`
// guard in PgStore.SetMFARequired.
func (m *MemStore) SetMFARequired(_ context.Context, id string, required bool) (changed bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[id]
	if !ok {
		return false, ErrNotFound
	}
	if a.MFARequired == required {
		return false, nil
	}
	a.MFARequired = required
	m.accounts[id] = a
	return true, nil
}

// CountDeployments returns the total deployment count for the
// account, excluding 'failed' + 'superseded' (those don't count
// toward the live workload). Mirrors PgStore's join through apps.
// The MemStore doesn't maintain an app-status sub-index, so this
// is O(apps + deployments) — fine for the test harness' < 1000
// rows; the PgStore is index-backed.
func (m *MemStore) CountDeployments(_ context.Context, id string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	owned := make(map[string]struct{})
	for _, a := range m.apps {
		if a.AccountID == id && a.Status != AppDeleted {
			owned[a.ID] = struct{}{}
		}
	}
	n := 0
	for _, d := range m.deployments {
		if _, ok := owned[d.AppID]; !ok {
			continue
		}
		if d.Status == DeployFailed || d.Status == DeploySuperseded {
			continue
		}
		n++
	}
	return n, nil
}

// --- end MFA -----------------------------------------------------------------

// UpdateAccountProviderCustomerID records the Stripe `cus_…` or
// Paddle `ctm_…` ID on the account row. MemStore keeps an index map
// for O(1) webhook lookup; PgStore mirrors with a schema-level
// unique index (added in Slice 2's migration). The shared
// accounts.provider_customer_id column is reused for both providers
// per ADR-025 — the two ID shapes are disjoint prefixes so the
// shared column is safe in single-provider deployments
// (FAAS_BILLING_PROVIDER is per-deployment, not per-row).
func (m *MemStore) UpdateAccountProviderCustomerID(_ context.Context, id, providerCustomerID string) error {
	// Empty providerCustomerID would silently corrupt the
	// reverse-lookup map (an empty-string key would point at the
	// most-recently-updated account, and AccountByProviderCustomerID
	// would return that account instead of ErrNotFound). Production
	// callers (apid changePlan, the Paddle webhook) check for empty
	// first; this is the store-side belt to that brace.
	if providerCustomerID == "" {
		return fmt.Errorf("state: UpdateAccountProviderCustomerID: empty providerCustomerID for account %q", id)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[id]
	if !ok {
		return ErrNotFound
	}
	a.ProviderCustomerID = providerCustomerID
	m.accounts[id] = a
	// Maintain the reverse-lookup map for AccountByProviderCustomerID.
	for k, v := range m.stripeByCustomer {
		if v == id && k != providerCustomerID {
			delete(m.stripeByCustomer, k)
			break
		}
	}
	m.stripeByCustomer[providerCustomerID] = id
	return nil
}

// UpdateAccountStripeSubscriptionItem stamps the Stripe metered
// subscription item ID (si_…) on the account row (issue #52). MemStore
// does not maintain a reverse-lookup index — only meterd walks
// forward from the account list. PgStore mirrors the column shape.
func (m *MemStore) UpdateAccountStripeSubscriptionItem(_ context.Context, id, subItem string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[id]
	if !ok {
		return ErrNotFound
	}
	a.StripeSubscriptionItem = subItem
	m.accounts[id] = a
	return nil
}

// AccountByProviderCustomerID is the reverse-lookup the Stripe webhook
// uses to find the account behind an event's `customer` field. O(1) via
// the index map; PgStore implements this with a unique index.
func (m *MemStore) AccountByProviderCustomerID(_ context.Context, stripeCustomerID string) (Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.stripeByCustomer[stripeCustomerID]
	if !ok {
		return Account{}, ErrNotFound
	}
	a, ok := m.accounts[id]
	if !ok {
		return Account{}, ErrNotFound
	}
	return a, nil
}

// ListAllAccounts walks the account map under the store mutex. The
// meterd quota + Stripe-push loops both call this; bounded by the
// customer count on the one box.
func (m *MemStore) ListAllAccounts(_ context.Context) ([]Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Account, 0, len(m.accounts))
	for _, a := range m.accounts {
		out = append(out, a)
	}
	return out, nil
}

// --- API keys ---------------------------------------------------------------

func (m *MemStore) CreateAPIKey(_ context.Context, accountID string, hash []byte, label string, scopes []string) (APIKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	h := hex.EncodeToString(hash)
	if _, dup := m.keyByHash[h]; dup {
		return APIKey{}, fmt.Errorf("state: duplicate key hash")
	}
	// IAM-5: explicit Status="active" so the auth gate in
	// AuthenticateKey doesn't reject keys created via the
	// pre-existing 5-arg path (the 17+ test/handler call sites
	// that don't yet know about expiry). The pgstore path
	// relies on the SQL DEFAULT 'active' for the same shape.
	k := APIKey{
		ID:        newID(),
		AccountID: accountID,
		Hash:      hash,
		Label:     label,
		Scopes:    scopes,
		CreatedAt: time.Now(),
		Status:    string(APIKeyStatusActive),
	}
	m.keys[k.ID] = k
	m.keyByHash[h] = k
	return k, nil
}

// CreateOrgAPIKeyWithProvenance mirrors PgStore. Three optional
// provenance columns stamp the new row; nil/"" inputs round-trip
// as the zero value on the struct (mirrors the pgstore NULL shape).
func (m *MemStore) CreateOrgAPIKeyWithProvenance(_ context.Context, orgID, accountID string, hash []byte, label string, scopes []string, expiresAt *time.Time, createdIP, createdUA string, parent *string) (APIKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	h := hex.EncodeToString(hash)
	if _, dup := m.keyByHash[h]; dup {
		return APIKey{}, fmt.Errorf("state: duplicate key hash")
	}
	var orgIDField string
	if orgID != "" {
		orgIDField = orgID
	}
	k := APIKey{
		ID:          newID(),
		AccountID:   accountID,
		OrgID:       orgIDField,
		Hash:        hash,
		Label:       label,
		Scopes:      scopes,
		CreatedAt:   time.Now(),
		Status:      string(APIKeyStatusActive),
		ExpiresAt:   expiresAt,
		CreatedIP:   createdIP,
		CreatedUA:   createdUA,
		ParentKeyID: parent,
	}
	m.keys[k.ID] = k
	m.keyByHash[h] = k
	return k, nil
}

// RotateOrgAPIKeyWithProvenance mirrors PgStore. The new row's
// provenance columns stamp created_ip / created_ua / parent_key_id;
// the existing rotated_from_id is unchanged.
func (m *MemStore) RotateOrgAPIKeyWithProvenance(_ context.Context, orgID, oldKeyID string, newHash []byte, newLabel string, graceWindow time.Duration, createdIP, createdUA string, parent *string) (APIKey, APIKey, error) {
	if graceWindow < 0 {
		graceWindow = 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	old, ok := m.keys[oldKeyID]
	if !ok || old.OrgID != orgID {
		return APIKey{}, APIKey{}, ErrNotFound
	}
	if old.Status == string(APIKeyStatusRevoked) {
		return APIKey{}, APIKey{}, ErrAPIKeyRevoked
	}
	if newLabel == "" {
		newLabel = old.Label
	}
	rotatedFrom := old.ID
	newKey := APIKey{
		ID:            newID(),
		AccountID:     old.AccountID,
		OrgID:         old.OrgID,
		Hash:          newHash,
		Label:         newLabel,
		Scopes:        old.Scopes,
		CreatedAt:     time.Now(),
		Status:        string(APIKeyStatusActive),
		RotatedFromID: &rotatedFrom,
		CreatedIP:     createdIP,
		CreatedUA:     createdUA,
		ParentKeyID:   parent,
	}
	m.keys[newKey.ID] = newKey
	m.keyByHash[hex.EncodeToString(newKey.Hash)] = newKey

	now := time.Now()
	if graceWindow == 0 {
		old.Status = string(APIKeyStatusRevoked)
		old.ExpiresAt = &now
		if old.RevokedAt == nil {
			old.RevokedAt = &now
		}
	} else {
		old.Status = string(APIKeyStatusGrace)
		deadline := now.Add(graceWindow)
		old.ExpiresAt = &deadline
	}
	m.keys[old.ID] = old
	m.keyByHash[hex.EncodeToString(old.Hash)] = old
	return newKey, old, nil
}

func (m *MemStore) DeleteAPIKey(_ context.Context, accountID, keyID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.keys[keyID]
	if !ok || k.AccountID != accountID {
		return ErrNotFound
	}
	delete(m.keys, keyID)
	delete(m.keyByHash, hex.EncodeToString(k.Hash))
	return nil
}

// DeleteAPIKeyReturning is the IAM-1 (ADR-034 rev2) variant of
// DeleteAPIKey: deletes the key in one statement and returns the
// pre-delete row so the apid handler can emit a `key.deleted` audit
// event carrying the dismissed scopes. Mirrors PgStore's
// DELETE...RETURNING contract so tests against either backend
// exercise the same handler shape.
func (m *MemStore) DeleteAPIKeyReturning(_ context.Context, accountID, keyID string) (APIKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.keys[keyID]
	if !ok || k.AccountID != accountID {
		return APIKey{}, ErrNotFound
	}
	delete(m.keys, keyID)
	delete(m.keyByHash, hex.EncodeToString(k.Hash))
	return k, nil
}

func (m *MemStore) ListAPIKeys(_ context.Context, accountID string) ([]APIKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []APIKey
	for _, k := range m.keys {
		if k.AccountID == accountID {
			out = append(out, k)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// GetAPIKey mirrors PgStore. Returns ErrNotFound when the key is
// missing OR owned by a different account (the cross-account collapse
// matches the SQL-side IDOR-safe shape). Used by legacy rotateKey
// (PR 6 dual-write) to discover the old row's org_id before the
// rotation.
//
// Issue #190 / IAM-6, PR 6.
func (m *MemStore) GetAPIKey(_ context.Context, accountID, keyID string) (APIKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.keys[keyID]
	if !ok || k.AccountID != accountID {
		return APIKey{}, ErrNotFound
	}
	return k, nil
}

func (m *MemStore) TouchKeyLastUsed(_ context.Context, keyID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.keys[keyID]
	if !ok {
		return ErrNotFound
	}
	k.LastUsedAt = time.Now()
	m.keys[keyID] = k
	return nil
}

// CreateAPIKeyWithExpiry mirrors PgStore. The five-arg CreateAPIKey
// stays (it's the shape 17+ test/handler call sites use); this is
// the IAM-5 path that lets the handler set expires_at on a fresh
// key. expiresAt == nil → Status="active", ExpiresAt=nil (the
// admin "never expires" contract); non-nil → Status="active",
// ExpiresAt=&t. MemStore preserves the post-migration default
// (status='active', all nullable fields nil) for the
// pre-migration call site.
func (m *MemStore) CreateAPIKeyWithExpiry(_ context.Context, accountID string, hash []byte, label string, scopes []string, expiresAt *time.Time) (APIKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	h := hex.EncodeToString(hash)
	if _, dup := m.keyByHash[h]; dup {
		return APIKey{}, fmt.Errorf("state: duplicate key hash")
	}
	k := APIKey{
		ID:        newID(),
		AccountID: accountID,
		Hash:      hash,
		Label:     label,
		Scopes:    scopes,
		CreatedAt: time.Now(),
		Status:    string(APIKeyStatusActive),
		ExpiresAt: expiresAt,
	}
	m.keys[k.ID] = k
	m.keyByHash[h] = k
	return k, nil
}

// CreateAPIKeyWithExpiryAndProvenance mirrors PgStore. Optional
// provenance columns stamp CreatedIP / CreatedUA / ParentKeyID;
// nil/"" inputs round-trip as the zero value on the struct.
func (m *MemStore) CreateAPIKeyWithExpiryAndProvenance(_ context.Context, accountID string, hash []byte, label string, scopes []string, expiresAt *time.Time, createdIP, createdUA string, parent *string) (APIKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	h := hex.EncodeToString(hash)
	if _, dup := m.keyByHash[h]; dup {
		return APIKey{}, fmt.Errorf("state: duplicate key hash")
	}
	k := APIKey{
		ID:          newID(),
		AccountID:   accountID,
		Hash:        hash,
		Label:       label,
		Scopes:      scopes,
		CreatedAt:   time.Now(),
		Status:      string(APIKeyStatusActive),
		ExpiresAt:   expiresAt,
		CreatedIP:   createdIP,
		CreatedUA:   createdUA,
		ParentKeyID: parent,
	}
	m.keys[k.ID] = k
	m.keyByHash[h] = k
	return k, nil
}

// CountAPIKeys mirrors PgStore. The "non-revoked" filter matches
// the partial index api_keys_active_grace_idx on the pg side.
func (m *MemStore) CountAPIKeys(_ context.Context, accountID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, k := range m.keys {
		if k.AccountID == accountID && (k.Status == string(APIKeyStatusActive) || k.Status == string(APIKeyStatusGrace)) {
			n++
		}
	}
	return n, nil
}

// MarkAPIKeyRevoked mirrors PgStore. Idempotent: if the key is
// already revoked, the row is left as-is and returned.
func (m *MemStore) MarkAPIKeyRevoked(_ context.Context, accountID, keyID string) (APIKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.keys[keyID]
	if !ok || k.AccountID != accountID {
		return APIKey{}, ErrNotFound
	}
	if k.Status != string(APIKeyStatusRevoked) {
		k.Status = string(APIKeyStatusRevoked)
		if k.RevokedAt == nil {
			now := time.Now()
			k.RevokedAt = &now
		}
		m.keys[k.ID] = k
		m.keyByHash[hex.EncodeToString(k.Hash)] = k
	}
	return k, nil
}

// RotateAPIKey mirrors PgStore. graceWindow == 0 → atomic (old
// key revoked immediately); > 0 → grace (old key gets status='grace'
// + expires_at = now+graceWindow). MemStore's atomicity is
// "no concurrent callers" by virtue of m.mu; the same is true
// of every other mutating method on this struct.
func (m *MemStore) RotateAPIKey(_ context.Context, accountID, oldKeyID string, newHash []byte, newLabel string, graceWindow time.Duration) (APIKey, APIKey, error) {
	if graceWindow < 0 {
		graceWindow = 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	old, ok := m.keys[oldKeyID]
	if !ok || old.AccountID != accountID {
		return APIKey{}, APIKey{}, ErrNotFound
	}
	if old.Status == string(APIKeyStatusRevoked) {
		return APIKey{}, APIKey{}, ErrAPIKeyRevoked
	}
	if newLabel == "" {
		newLabel = old.Label
	}
	rotatedFrom := old.ID
	newKey := APIKey{
		ID:            newID(),
		AccountID:     accountID,
		Hash:          newHash,
		Label:         newLabel,
		Scopes:        old.Scopes,
		CreatedAt:     time.Now(),
		Status:        string(APIKeyStatusActive),
		RotatedFromID: &rotatedFrom,
	}
	m.keys[newKey.ID] = newKey
	m.keyByHash[hex.EncodeToString(newKey.Hash)] = newKey

	now := time.Now()
	if graceWindow == 0 {
		old.Status = string(APIKeyStatusRevoked)
		old.ExpiresAt = &now
		if old.RevokedAt == nil {
			old.RevokedAt = &now
		}
	} else {
		old.Status = string(APIKeyStatusGrace)
		deadline := now.Add(graceWindow)
		old.ExpiresAt = &deadline
		// revoked_at stays nil in the grace case — the grace
		// deadline IS the new expires_at; revoke happens
		// later if the old key isn't rotated away or
		// re-revoked manually.
	}
	m.keys[old.ID] = old
	m.keyByHash[hex.EncodeToString(old.Hash)] = old
	return newKey, old, nil
}

// GetAccountKeyGraceWindow mirrors PgStore. nil = no override.
func (m *MemStore) GetAccountKeyGraceWindow(_ context.Context, accountID string) (*int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[accountID]
	if !ok {
		return nil, ErrNotFound
	}
	return a.KeyGraceWindowDays, nil
}

// SetAccountKeyGraceWindow mirrors PgStore. days == nil clears
// the override. The MemStore value is a *int; the pgstore binds
// the same *int directly to a nullable integer column.
func (m *MemStore) SetAccountKeyGraceWindow(_ context.Context, accountID string, days *int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[accountID]
	if !ok {
		return ErrNotFound
	}
	a.KeyGraceWindowDays = days
	m.accounts[accountID] = a
	return nil
}

// GetAccountEgressAllowlistExtra mirrors PgStore. 0 = no override;
// the plan cap is authoritative. The validator at
// cmd/apid/handlers_ext.go:104 adds this to the plan cap before
// the >-maxSize check on the per-app EgressAllowlist patch.
//
// Issue #679 / PR-B / ADR-082.
func (m *MemStore) GetAccountEgressAllowlistExtra(_ context.Context, accountID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[accountID]
	if !ok {
		return 0, ErrNotFound
	}
	return a.EgressAllowlistExtra, nil
}

// SetAccountEgressAllowlistExtra mirrors PgStore. n == 0 clears
// the override (the plan cap is authoritative again).
//
// Issue #679 / PR-B / ADR-082.
func (m *MemStore) SetAccountEgressAllowlistExtra(_ context.Context, accountID string, n int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[accountID]
	if !ok {
		return ErrNotFound
	}
	a.EgressAllowlistExtra = n
	m.accounts[accountID] = a
	return nil
}

// --- Org-bound API keys (issue #190 / IAM-6, PR 6) ------------------------
//
// Same shape as the legacy per-account API key methods, but filtered by
// org_id. The shared `keys` map is the index; the filter is in-Go so
// there is no separate map keyed by org_id (MemStore's test-only scope
// doesn't need the index — keeps the canonical "every key in one map"
// invariant pgstore's pg_class read-models mirror).

func (m *MemStore) CreateOrgAPIKey(_ context.Context, orgID, accountID string, hash []byte, label string, scopes []string, expiresAt *time.Time) (APIKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.accounts[accountID]; !ok {
		return APIKey{}, ErrNotFound
	}
	h := hex.EncodeToString(hash)
	if _, dup := m.keyByHash[h]; dup {
		return APIKey{}, fmt.Errorf("state: duplicate key hash")
	}
	k := APIKey{
		ID:        newID(),
		AccountID: accountID,
		OrgID:     orgID,
		Hash:      hash,
		Label:     label,
		Scopes:    scopes,
		CreatedAt: time.Now(),
		Status:    string(APIKeyStatusActive),
		ExpiresAt: expiresAt,
	}
	m.keys[k.ID] = k
	m.keyByHash[h] = k
	return k, nil
}

func (m *MemStore) ListOrgAPIKeys(_ context.Context, orgID string) ([]APIKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]APIKey, 0)
	for _, k := range m.keys {
		if k.OrgID != orgID {
			continue
		}
		if k.Status != string(APIKeyStatusActive) && k.Status != string(APIKeyStatusGrace) {
			continue
		}
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (m *MemStore) GetOrgAPIKey(_ context.Context, orgID, keyID string) (APIKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.keys[keyID]
	if !ok || k.OrgID != orgID {
		return APIKey{}, ErrNotFound
	}
	return k, nil
}

func (m *MemStore) RevokeOrgAPIKey(_ context.Context, orgID, keyID string) (APIKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.keys[keyID]
	if !ok || k.OrgID != orgID {
		return APIKey{}, ErrNotFound
	}
	if k.Status != string(APIKeyStatusRevoked) {
		k.Status = string(APIKeyStatusRevoked)
		if k.RevokedAt == nil {
			now := time.Now()
			k.RevokedAt = &now
		}
		m.keys[k.ID] = k
		m.keyByHash[hex.EncodeToString(k.Hash)] = k
	}
	return k, nil
}

func (m *MemStore) RotateOrgAPIKey(_ context.Context, orgID, oldKeyID string, newHash []byte, newLabel string, graceWindow time.Duration) (APIKey, APIKey, error) {
	if graceWindow < 0 {
		graceWindow = 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	old, ok := m.keys[oldKeyID]
	if !ok || old.OrgID != orgID {
		return APIKey{}, APIKey{}, ErrNotFound
	}
	if old.Status == string(APIKeyStatusRevoked) {
		return APIKey{}, APIKey{}, ErrAPIKeyRevoked
	}
	if newLabel == "" {
		newLabel = old.Label
	}
	rotatedFrom := old.ID
	newKey := APIKey{
		ID:            newID(),
		AccountID:     old.AccountID,
		OrgID:         old.OrgID,
		Hash:          newHash,
		Label:         newLabel,
		Scopes:        old.Scopes,
		CreatedAt:     time.Now(),
		Status:        string(APIKeyStatusActive),
		RotatedFromID: &rotatedFrom,
	}
	m.keys[newKey.ID] = newKey
	m.keyByHash[hex.EncodeToString(newKey.Hash)] = newKey

	now := time.Now()
	if graceWindow == 0 {
		old.Status = string(APIKeyStatusRevoked)
		old.ExpiresAt = &now
		if old.RevokedAt == nil {
			old.RevokedAt = &now
		}
	} else {
		old.Status = string(APIKeyStatusGrace)
		deadline := now.Add(graceWindow)
		old.ExpiresAt = &deadline
	}
	m.keys[old.ID] = old
	m.keyByHash[hex.EncodeToString(old.Hash)] = old
	return newKey, old, nil
}

// --- Projects (ADR-050, Phase 1) -------------------------------------------
//
// MemStore mirrors the pgstore contract exactly: the same error sentinels
// (ErrNotFound, ErrConflict), the same monotonic-upgrade semantics in
// SetProjectScanSource. The two secondary indexes
// (projectsByAccountSlug, projectsByInstallRepo) keep the read paths
// O(1) — the SQL btrees are the same shape.

func (m *MemStore) CreateProject(_ context.Context, p Project) (Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.accounts[p.AccountID]; !ok {
		return Project{}, ErrNotFound
	}
	if p.ScanSource == "" {
		p.ScanSource = ProjectScanSourceUnknown
	}
	// (account_id, slug) uniqueness — mirrors projects_account_slug_uniq.
	if bySlug, ok := m.projectsByAccountSlug[p.AccountID]; ok {
		if _, taken := bySlug[p.Slug]; taken {
			return Project{}, ErrConflict
		}
	}
	// (install_id, repo_full_name) uniqueness — mirrors
	// projects_install_repo_uniq. Standalone projects
	// (InstallID == 0 or empty RepoFullName) skip this check entirely.
	if p.InstallID != 0 && p.RepoFullName != "" {
		key := installRepoKey{InstallID: p.InstallID, RepoFullName: p.RepoFullName}
		if _, taken := m.projectsByInstallRepo[key]; taken {
			return Project{}, ErrConflict
		}
	}
	if p.ID == "" {
		p.ID = newID()
	}
	now := time.Now()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	m.projects[p.ID] = p
	if m.projectsByAccountSlug[p.AccountID] == nil {
		m.projectsByAccountSlug[p.AccountID] = map[string]string{}
	}
	m.projectsByAccountSlug[p.AccountID][p.Slug] = p.ID
	if p.InstallID != 0 && p.RepoFullName != "" {
		m.projectsByInstallRepo[installRepoKey{InstallID: p.InstallID, RepoFullName: p.RepoFullName}] = p.ID
	}
	return p, nil
}

func (m *MemStore) ProjectByID(_ context.Context, projectID string) (Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.projects[projectID]
	if !ok {
		return Project{}, ErrNotFound
	}
	return p, nil
}

func (m *MemStore) ProjectBySlug(_ context.Context, accountID, slug string) (Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	bySlug, ok := m.projectsByAccountSlug[accountID]
	if !ok {
		return Project{}, ErrNotFound
	}
	id, ok := bySlug[slug]
	if !ok {
		return Project{}, ErrNotFound
	}
	p, ok := m.projects[id]
	if !ok {
		return Project{}, ErrNotFound
	}
	return p, nil
}

// ProjectByRepo looks up by (install_id, repo_full_name). The
// accountID filter is optional — passing "" matches the first hit
// across accounts (mirrors the SQL `($3 = ”)` clause in pgstore).
// This is the push-dispatch lookup Phase 5 wires to githubd.
func (m *MemStore) ProjectByRepo(_ context.Context, accountID string, installID int64, repoFullName string) (Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.projectsByInstallRepo[installRepoKey{InstallID: installID, RepoFullName: repoFullName}]
	if !ok {
		return Project{}, ErrNotFound
	}
	p, ok := m.projects[id]
	if !ok {
		return Project{}, ErrNotFound
	}
	if accountID != "" && p.AccountID != accountID {
		return Project{}, ErrNotFound
	}
	return p, nil
}

func (m *MemStore) ListProjectsForAccount(_ context.Context, accountID string) ([]Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Project, 0)
	for _, p := range m.projects {
		if p.AccountID != accountID {
			continue
		}
		out = append(out, p)
	}
	// Stable order: created_at desc (matches SQL).
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].CreatedAt.After(out[i].CreatedAt) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out, nil
}

// AppsForProject returns live apps currently bound to projectID. The
// account scope is enforced before the slice walk so a project owned
// by a different account returns ErrNotFound (404 path, not an empty
// list that would leak membership).
func (m *MemStore) AppsForProject(_ context.Context, accountID, projectID string) ([]App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	proj, ok := m.projects[projectID]
	if !ok {
		return []App{}, ErrNotFound
	}
	if proj.AccountID != accountID {
		return []App{}, ErrNotFound
	}
	out := make([]App, 0)
	for _, a := range m.apps {
		if a.ProjectID == "" || a.ProjectID != projectID {
			continue
		}
		if a.Status == AppDeleted {
			continue
		}
		out = append(out, a)
	}
	// Order: workload_name asc, created_at asc (matches the SQL).
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].WorkloadName < out[i].WorkloadName ||
				(out[j].WorkloadName == out[i].WorkloadName && out[j].CreatedAt.Before(out[i].CreatedAt)) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out, nil
}

// SetProjectScanSource updates scan_source monotonically upward. Same
// tier is a no-op (touches updated_at so observers see the activity).
// Weaker tier returns ErrScanSourceDowngrade.
func (m *MemStore) SetProjectScanSource(_ context.Context, projectID string, src ProjectScanSource) (Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if src == "" {
		src = ProjectScanSourceUnknown
	}
	p, ok := m.projects[projectID]
	if !ok {
		return Project{}, ErrNotFound
	}
	if tierRank(src) < tierRank(p.ScanSource) {
		return Project{}, ErrScanSourceDowngrade
	}
	p.ScanSource = src
	p.UpdatedAt = time.Now()
	m.projects[projectID] = p
	return p, nil
}

// DeleteProject removes a project row by ID. Mirrors the pgstore
// trigger: apps pointing at this project get their project_id
// nulled (the row stays; reconcile already soft-deleted any
// apps it touched via SoftDeleteAppCascade). Returns ErrNotFound
// when no project matches. Used by cmd/apid's scanService to
// roll back a half-created project on a reconcile error.
func (m *MemStore) DeleteProject(_ context.Context, projectID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.projects[projectID]
	if !ok {
		return ErrNotFound
	}
	delete(m.projects, projectID)
	// Drop the by-account+slug entry. The map is keyed by
	// accountID → slug → projectID; nil-ing out the slug
	// entry is enough.
	if byAcctSlug, ok := m.projectsByAccountSlug[p.AccountID]; ok && p.Slug != "" {
		delete(byAcctSlug, p.Slug)
	}
	// Drop the by-(installID, repoFullName) entry. Keyed by
	// the installRepoKey struct; only present for projects
	// bound to a GitHub install.
	if p.InstallID != 0 && p.RepoFullName != "" {
		delete(m.projectsByInstallRepo, installRepoKey{InstallID: p.InstallID, RepoFullName: p.RepoFullName})
	}
	// Walk apps and null project_id where it points here. This
	// mirrors the PG trigger ON DELETE SET NULL on the
	// apps.project_id FK (migration 00074:74). The app row
	// stays — reconciler may have soft-deleted it but the
	// history remains. m.apps is map[string]App (not pointer
	// values), so we have to re-assign the whole struct to
	// mutate a field.
	for appID, a := range m.apps {
		if a.ProjectID == projectID {
			a.ProjectID = ""
			m.apps[appID] = a
		}
	}
	return nil
}

// ApplyProjectPlan — Phase 3 transactional seam. Mirrors the
// CreateAppIfUnderQuota critical section but inlines the count +
// insert for project + apps + crons inside one Tx so the apid
// "one keypress" path either lands the whole set or nothing.
func (m *MemStore) ApplyProjectPlan(
	_ context.Context,
	project Project,
	apps []App,
	crons []Cron,
	limits api.Limits,
) (Project, []App, []Cron, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 1. Account exists.
	if _, ok := m.accounts[project.AccountID]; !ok {
		return Project{}, nil, nil, ErrNotFound
	}

	// 2. Project slug collision guard.
	for _, p := range m.projects {
		if p.AccountID == project.AccountID && p.Slug == project.Slug {
			return Project{}, nil, nil, ErrConflict
		}
	}

	// 3. Count current deployed apps (mirror CreateAppIfUnderQuota).
	observedApps := 0
	for _, a := range m.apps {
		if a.AccountID == project.AccountID && a.Status != AppDeleted &&
			(a.Status == AppActive || a.Status == AppEvictedCold) {
			observedApps++
		}
	}
	if observedApps+len(apps) > limits.DeployedApps {
		return Project{}, nil, nil, &QuotaError{
			Kind:     QuotaErrorKindApps,
			Limit:    limits.DeployedApps,
			Observed: observedApps + len(apps),
		}
	}

	// 4. Free-plan cron guard + cron quota check. Skipped entirely
	//    when the apply is zero-cron — a Free account with
	//    pre-existing crons (from a prior plan downgrade) must still
	//    be able to apply a cron-less project. Same shape as
	//    PgStore.ApplyProjectPlan step 3 (pgstore.go:1116).
	if len(crons) > 0 {
		if limits.CronLimitPerAccount == 0 {
			return Project{}, nil, nil, &QuotaError{
				Kind:       QuotaErrorKindCrons,
				NotAllowed: true,
			}
		}
		observedCrons := 0
		for _, c := range m.crons {
			for _, a := range m.apps {
				if c.AppID == a.ID && a.AccountID == project.AccountID && a.Status != AppDeleted {
					observedCrons++
					break
				}
			}
		}
		if observedCrons+len(crons) > limits.CronLimitPerAccount {
			return Project{}, nil, nil, &QuotaError{
				Kind:     QuotaErrorKindCrons,
				Limit:    limits.CronLimitPerAccount,
				Observed: observedCrons + len(crons),
			}
		}
	}

	// 5. Insert project.
	if project.ID == "" {
		project.ID = uuid.NewString()
	}
	now := time.Now()
	if project.CreatedAt.IsZero() {
		project.CreatedAt = now
	}
	project.UpdatedAt = now
	if project.ScanSource == "" {
		project.ScanSource = ProjectScanSourceUnknown
	}
	m.projects[project.ID] = project

	// 6. Insert apps. The apply handler resolves crons[i].AppID
	// against the just-inserted apps — callers see the same Cron
	// shape CreateCronIfUnderQuota produces (AppID populated, ID
	// empty). We persist whatever AppID is set; overwriting here
	// would force callers to learn the apply internal sequencing.
	insertedApps := make([]App, 0, len(apps))
	for _, a := range apps {
		if a.ID == "" {
			a.ID = uuid.NewString()
		}
		a.AccountID = project.AccountID
		a.ProjectID = project.ID
		if a.Status == "" {
			a.Status = AppActive
		}
		a.CreatedAt = now
		m.apps[a.ID] = a
		insertedApps = append(insertedApps, a)
	}

	// 7. Insert crons (AppID is the caller's responsibility to set
	// against the freshly inserted apps). Empty AppID is treated
	// as "deferred" — the apply handler resolves the workload-name
	// → ID map from the returned insertedApps and re-inserts via
	// CreateCron. Quota is already enforced in step 6 so a deferred
	// cron cannot bypass it.
	insertedCrons := make([]Cron, 0, len(crons))
	for _, c := range crons {
		if c.AppID == "" {
			continue
		}
		c.ID = uuid.NewString()
		c.CreatedAt = now
		m.crons[c.ID] = c
		insertedCrons = append(insertedCrons, c)
	}

	return project, insertedApps, insertedCrons, nil
}

// --- Apps -------------------------------------------------------------------

func (m *MemStore) CreateApp(_ context.Context, app App) (App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.apps {
		if a.Slug == app.Slug && a.Status != AppDeleted {
			return App{}, fmt.Errorf("state: slug %q already taken", app.Slug)
		}
	}
	if app.ID == "" {
		app.ID = newID()
	}
	if app.CreatedAt.IsZero() {
		app.CreatedAt = time.Now()
	}
	if app.Status == "" {
		app.Status = AppActive
	}
	// Issue #475: snap empty Go zero to the schema DEFAULT 'best_effort'
	// so the column reads as a real value right out of CreateApp. The
	// pgstore snap lives on the SQL path; the memstore snap mirrors it
	// here so the per-account reserved-tier cap read
	// (CountAppsWithEvictionPriority) sees the same value the wire
	// would. Pre-#475 tests that built App{} structs continue to read
	// 'best_effort' on the round-trip.
	app.EvictionPriority = EvictionPriorityOrBestEffort(app.EvictionPriority)
	// Issue #695 / ADR-080: defence-in-depth snap for public_auth_mode.
	// pgstore floors '' to AppPublicAuthModeOpen at the SQL path
	// (pgstore.go:1546-1549 / 1693-1697). CreateAppIfUnderQuota below
	// already has this snap (added with issue #695); CreateApp was
	// missing it — a hand-built App{} could round-trip with an empty
	// mode and break the memstore/pgstore parity invariant. require_authn
	// is bool (zero is the schema default), so no snap is needed there.
	if app.PublicAuthMode == "" {
		app.PublicAuthMode = api.AppPublicAuthModeOpen
	}
	m.apps[app.ID] = app
	return app, nil
}

// CreateAppIfUnderQuota is the MemStore mirror of PgStore.CreateAppIfUnderQuota.
// The TOCTOU is impossible here because every mutation holds m.mu for
// the full check + insert — two goroutines serialize on the same lock,
// so a Free account that already holds 1 app always sees observed=1 on
// the second call. The handler's CreateApp call site becomes store-
// agnostic.
func (m *MemStore) CreateAppIfUnderQuota(_ context.Context, app App, limits api.Limits) (App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.accounts[app.AccountID]; !ok {
		return App{}, ErrNotFound
	}
	// 1. Authoritative count under the same lock. Mirrors the predicate
	//    PgStore uses against the apps table.
	observed := 0
	for _, a := range m.apps {
		if a.AccountID == app.AccountID && (a.Status == AppActive || a.Status == AppEvictedCold) {
			observed++
		}
	}
	if observed >= limits.DeployedApps {
		return App{}, &QuotaError{Limit: limits.DeployedApps, Observed: observed}
	}
	// 2. Conditional insert. Slug uniqueness is enforced by the same
	//    loop CreateApp uses; returning ErrConflict keeps the wire
	//    contract identical to PgStore's apps.slug unique-index path.
	for _, a := range m.apps {
		if a.Slug == app.Slug && a.Status != AppDeleted {
			return App{}, ErrConflict
		}
	}
	if app.ID == "" {
		app.ID = newID()
	}
	if app.CreatedAt.IsZero() {
		app.CreatedAt = time.Now()
	}
	if app.Status == "" {
		app.Status = AppActive
	}
	// Issue #475: same snap-to-default as CreateApp above — the
	// quota-gated path must round-trip 'best_effort' just like the
	// unconditional path so the per-account reserved-tier cap reader
	// sees the same wire value.
	app.EvictionPriority = EvictionPriorityOrBestEffort(app.EvictionPriority)
	// Issue #695 / ADR-080: see CreateApp — same defence-in-depth
	// snap for the public_auth mode. require_authn is bool (zero
	// is the schema default), so no snap is needed there.
	if app.PublicAuthMode == "" {
		app.PublicAuthMode = api.AppPublicAuthModeOpen
	}
	m.apps[app.ID] = app
	return app, nil
}

func (m *MemStore) AppByID(_ context.Context, id string) (App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.apps[id]
	if !ok {
		return App{}, ErrNotFound
	}
	return a, nil
}

func (m *MemStore) AppBySlug(_ context.Context, slug string) (App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.apps {
		if a.Slug == slug && a.Status != AppDeleted {
			return a, nil
		}
	}
	return App{}, ErrNotFound
}

// PreviewAppsByParent (ADR-095 / issue #272) is the MemStore mirror
// of PgStore.PreviewAppsByParent. Walks the in-memory map under the
// same lock as CreateApp / AppByID / ListApps. Returns an empty slice
// (not an error) when no previews exist for the parent — the
// dashboard's preview pane projects the empty list as "no previews
// yet", matching the MemStore zero-value convention everywhere else.
func (m *MemStore) PreviewAppsByParent(_ context.Context, accountID, parentSlug string) ([]App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []App
	for _, a := range m.apps {
		if a.AccountID != accountID || a.PreviewOfSlug != parentSlug || a.Status == AppDeleted {
			continue
		}
		out = append(out, a)
	}
	// Stable sort: newest first. matches the pgstore ORDER BY
	// created_at DESC. Tests that assert on row order should not
	// depend on map iteration randomness.
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

// ListPreviewsForTeardown (ADR-095 PR-C / issue #272) is the MemStore
// mirror of PgStore.ListPreviewsForTeardown. Same contract: return
// every non-torn_down preview row that is either in a terminal-ish
// PR state (closed / stale) or past its preview_expires_at TTL.
// Deliberately does NOT filter on Status == AppDeleted — see the
// Store-interface docstring for why the janitor needs to observe
// tombstoned rows on subsequent ticks.
func (m *MemStore) ListPreviewsForTeardown(_ context.Context, now time.Time, maxPerTick int) ([]App, error) {
	if maxPerTick < 1 {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []App
	for _, a := range m.apps {
		if a.PreviewOfSlug == "" {
			continue
		}
		if a.PreviewPrState == PreviewPrStateTornDown {
			continue
		}
		if a.PreviewPrState == PreviewPrStateClosed || a.PreviewPrState == PreviewPrStateStale {
			out = append(out, a)
			continue
		}
		if a.PreviewExpiresAt != nil && a.PreviewExpiresAt.Before(now) {
			out = append(out, a)
		}
	}
	// pgstore orders preview_expires_at ASC NULLS LAST + a per-tick
	// cap. Mirror that here so the janitor's logic is identical on
	// both backends; tests that pin a deterministic order need it.
	sort.Slice(out, func(i, j int) bool {
		oi, oj := out[i].PreviewExpiresAt, out[j].PreviewExpiresAt
		switch {
		case oi == nil && oj == nil:
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		case oi == nil:
			return false
		case oj == nil:
			return true
		}
		return oi.Before(*oj)
	})
	if len(out) > maxPerTick {
		out = out[:maxPerTick]
	}
	return out, nil
}

// SetPreviewPrState (ADR-095 PR-C / issue #272) is the MemStore
// mirror of PgStore.SetPreviewPrState. Preview-only by construction:
// a row with empty PreviewOfSlug returns ErrNotFound so a bug in the
// janitor cannot relabel a production app.
func (m *MemStore) SetPreviewPrState(_ context.Context, appID, prState string) (App, error) {
	if !PreviewPrStateIsValid(prState) {
		return App{}, fmt.Errorf("state: set preview pr_state %q for app %q: %w", prState, appID, ErrInvalidPreviewPrState)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.apps[appID]
	if !ok || a.PreviewOfSlug == "" {
		return App{}, ErrNotFound
	}
	a.PreviewPrState = prState
	m.apps[appID] = a
	return a, nil
}

// StampPreviewDestroyCommentedAt (Mega-C PR-1 / issue #961 leaf 3)
// is the MemStore mirror of PgStore.StampPreviewDestroyCommentedAt.
// Preview-only by construction; production rows return
// ErrNotFound. Idempotent: re-stamping the same timestamp is a
// no-op (the column value is the dedupe key, not the row identity).
func (m *MemStore) StampPreviewDestroyCommentedAt(_ context.Context, appID string, when time.Time) (App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.apps[appID]
	if !ok || a.PreviewOfSlug == "" {
		return App{}, ErrNotFound
	}
	t := when
	a.PreviewDestroyCommentedAt = &t
	m.apps[appID] = a
	return a, nil
}

// ListPreviewsForAccount (Mega-C PR-1 / issue #961 leaf 3) is the
// MemStore mirror of PgStore.ListPreviewsForAccount. Returns every
// non-deleted preview row for the account across all parents;
// production apps (PreviewOfSlug == "") are filtered out.
func (m *MemStore) ListPreviewsForAccount(_ context.Context, accountID string) ([]App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []App
	for _, a := range m.apps {
		if a.AccountID != accountID || a.PreviewOfSlug == "" || a.Status == AppDeleted {
			continue
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

func (m *MemStore) ListApps(_ context.Context, accountID string) ([]App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []App
	for _, a := range m.apps {
		if a.AccountID == accountID && a.Status != AppDeleted {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (m *MemStore) ListAllApps(_ context.Context) ([]App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []App
	for _, a := range m.apps {
		if a.Status != AppDeleted {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// ListAppsByNodeID mirrors pkg/state/pgstore.go:858. In-process
// map scan with the same predicate the SQL uses (node_id == X AND
// status != deleted). Used by schedd in unit tests + by the e2e
// harness's fake schedd (the metal path goes through pgstore).
func (m *MemStore) ListAppsByNodeID(_ context.Context, nodeID string) ([]App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []App
	for _, a := range m.apps {
		if a.NodeID == nodeID && a.Status != AppDeleted {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// ListAllDeployments mirrors PgStore.ListAllDeployments. Issue #557
// closure / ADR-072 — the floor reconciler's wake sweep calls this
// in unit tests + the e2e harness's fake schedd. Excludes deployments
// whose parent app is soft-deleted (the deployment has no
// `deleted` flag of its own).
func (m *MemStore) ListAllDeployments(_ context.Context) ([]Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Deployment
	for _, d := range m.deployments {
		if a, ok := m.apps[d.AppID]; !ok || a.Status == AppDeleted {
			continue
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// ListDeploymentsByNodeID mirrors PgStore.ListDeploymentsByNodeID.
// Same JOIN-through-apps predicate as the SQL version.
func (m *MemStore) ListDeploymentsByNodeID(_ context.Context, nodeID string) ([]Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Deployment
	for _, d := range m.deployments {
		a, ok := m.apps[d.AppID]
		if !ok || a.NodeID != nodeID || a.Status == AppDeleted {
			continue
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// isInstanceStateLive reports whether the given instance state is
// in the §6.2 invariant #1 set — {WAKING, COLD_BOOTING, RUNNING}.
// The bash triple appears in:
//   - pgstore.go (the predicate is a SQL IN clause)
//   - memstore.go's ConcurrencyForDeployment (this file)
//   - memstore.go's PerNodeLiveStats (PR #4)
//
// Extracted so the goconst lint rule (3+ literals) and the
// state-machine spec (§6.1) share a single source of truth in the
// in-memory twin. The Postgres CHECK constraint is the canonical
// enforcement for the persistent table; this helper is the
// in-memory twin's mirror.
func isInstanceStateLive(state string) bool {
	switch state {
	case instanceStateRunning, instanceStateWaking, instanceStateColdBooting:
		return true
	}
	return false
}

// instanceStateRunning / instanceStateWaking / instanceStateColdBooting
// are the live-state literals from the spec §6.1 state machine.
// Mirrored here only to feed isInstanceStateLive — the rest of
// the codebase continues to use the bare string literals because
// the SQL CHECK constraint is the load-bearing enforcement and
// any wider refactor is out of scope.
const (
	instanceStateRunning     = "RUNNING"
	instanceStateWaking      = "WAKING"
	instanceStateColdBooting = "COLD_BOOTING"
)

// ConcurrencyForDeployment mirrors PgStore.ConcurrencyForDeployment.
// Reads the in-memory instances slice with the same predicate the
// SQL uses (state IN {'RUNNING','WAKING','COLD_BOOTING'}).
func (m *MemStore) ConcurrencyForDeployment(_ context.Context, appID, deploymentID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, inst := range m.instances {
		if inst.AppID != appID || inst.DeploymentID != deploymentID {
			continue
		}
		if isInstanceStateLive(inst.State) {
			n++
		}
	}
	return n, nil
}

// UpdateDeploymentMinInstances mirrors PgStore.UpdateDeploymentMinInstances.
// The handler validates against the parent app's plan ceiling; the
// store writes unconditionally and returns ErrNotFound on a missing row.
func (m *MemStore) UpdateDeploymentMinInstances(_ context.Context, id string, min int) (Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deployments[id]
	if !ok {
		return Deployment{}, ErrNotFound
	}
	d.MinInstances = min
	m.deployments[id] = d
	return d, nil
}

// UpdateDeploymentTraffic mirrors PgStore.UpdateDeploymentTraffic
// (issue #556 PR-A). The "zero siblings" rebalance semantics is
// the same as the pgstore: setting row R's traffic_percent to
// newPercent forces every other live row in the same app to 0,
// keeping Σ = 100 by construction. The MemStore version runs under
// m.mu so a concurrent CreateDeployment / UpdateDeploymentTraffic
// call serialises behind us — same race-free contract the
// pgstore's FOR UPDATE provides.
//
// Range-check matches pgstore (handler already validates, this is
// defence in depth). Status guard: only 'live' rows accept
// traffic; a superseded/failed/pending row trips
// ErrInvalidTrafficPercent. PR-C mirrors the pgstore proportional
// redistribution (RedistributeTraffic) so both stores share the
// largest-remainder algorithm. Σ invariant is asserted post-write.
func (m *MemStore) UpdateDeploymentTraffic(_ context.Context, id string, newPercent int) (Deployment, error) {
	if newPercent < 0 || newPercent > 100 {
		return Deployment{}, ErrInvalidTrafficPercent
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	d, ok := m.deployments[id]
	if !ok {
		return Deployment{}, ErrNotFound
	}
	if d.Status != DeployLive {
		return Deployment{}, ErrInvalidTrafficPercent
	}

	// Stamp target first; sibling weights collected for redistribution.
	d.TrafficPercent = newPercent
	m.deployments[id] = d
	appID := d.AppID

	// Collect siblings (id-ordered for stable tie-break).
	type sibling struct {
		ID    string
		Prior int
	}
	var siblings []sibling
	for otherID, other := range m.deployments {
		if other.AppID != appID || other.Status != DeployLive || otherID == id {
			continue
		}
		siblings = append(siblings, sibling{ID: otherID, Prior: other.TrafficPercent})
	}
	// Stable order: ID ASC. Siblings map iteration is non-deterministic
	// so we sort explicitly.
	sort.SliceStable(siblings, func(a, b int) bool { return siblings[a].ID < siblings[b].ID })

	helperSiblings := make([]struct {
		ID    string
		Prior int
	}, len(siblings))
	for i, s := range siblings {
		helperSiblings[i].ID = s.ID
		helperSiblings[i].Prior = s.Prior
	}
	newWeights := RedistributeTraffic(helperSiblings, 100-newPercent)
	for i, s := range siblings {
		other := m.deployments[s.ID]
		other.TrafficPercent = newWeights[i]
		m.deployments[s.ID] = other
	}

	// Σ invariant (defensive tripwire).
	var sum int
	for _, row := range m.deployments {
		if row.AppID == appID && row.Status == DeployLive {
			sum += row.TrafficPercent
		}
	}
	if sum != 100 {
		return Deployment{}, ErrTrafficPercentSumInvalid
	}
	return d, nil
}

// ListInstancesByNodeID mirrors pkg/state/pgstore.go:875. Same
// in-process predicate; for the in-memory store this is a linear
// scan over m.instances — fine for tests + the e2e harness.
func (m *MemStore) ListInstancesByNodeID(_ context.Context, nodeID string) ([]Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Build a quick app_id → owner-node lookup.
	owner := make(map[string]string, len(m.apps))
	for _, a := range m.apps {
		owner[a.ID] = a.NodeID
	}
	var out []Instance
	for _, ins := range m.instances {
		if owner[ins.AppID] == nodeID {
			out = append(out, ins)
		}
	}
	return out, nil
}

// FailRunningInstanceIfOwnedByNode mirrors PgStore's conditional healthy-node
// stale-instance transition. ErrConflict represents a state/node race.
func (m *MemStore) FailRunningInstanceIfOwnedByNode(_ context.Context, id, nodeID string, terminalAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ins, ok := m.instances[id]
	if !ok || ins.NodeID != nodeID || ins.State != string(StateRunning) {
		return ErrConflict
	}
	ins.State = string(StateFailed)
	ts := terminalAt
	ins.TerminalAt = &ts
	m.instances[id] = ins
	return nil
}

// ListOwnedCronsByNodeID mirrors pkg/state/pgstore.go:891. Same
// in-process predicate; crons are 5-column rows keyed by app_id.
func (m *MemStore) ListOwnedCronsByNodeID(_ context.Context, nodeID string) ([]Cron, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	owner := make(map[string]string, len(m.apps))
	for _, a := range m.apps {
		owner[a.ID] = a.NodeID
	}
	var out []Cron
	for _, c := range m.crons {
		if owner[c.AppID] == nodeID {
			out = append(out, c)
		}
	}
	return out, nil
}

// ListUnplacedApps mirrors pkg/state/pgstore.go: ListUnplacedApps.
// Same predicate over the in-memory map; ordered by CreatedAt DESC
// to match the SQL shape.
func (m *MemStore) ListUnplacedApps(_ context.Context) ([]App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]App, 0)
	for _, a := range m.apps {
		if a.NodeID != "" || a.Status == AppDeleted {
			continue
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

// SetAppNodeID mirrors pkg/state/pgstore.go: SetAppNodeID. The
// conditional under m.mu is the in-process equivalent of the
// WHERE node_id IS NULL guard — exactly one caller wins; losers
// observe NodeID != "" and receive ErrConflict.
func (m *MemStore) SetAppNodeID(_ context.Context, appID, nodeID string) error {
	if nodeID == "" {
		return fmt.Errorf("state: set app node_id: empty nodeID")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.apps[appID]
	if !ok {
		return ErrNotFound
	}
	if a.NodeID != "" {
		return ErrConflict
	}
	a.NodeID = nodeID
	m.apps[appID] = a
	return nil
}

// ListOrphanedApps mirrors pkg/state/pgstore.go::ListOrphanedApps.
// Walks every app whose node_id points at an inactive
// compute_node, non-deleted only, and applies the same
// cooldown + per-tick cap the SQL path uses. cooldownSeconds
// < 0 disables the cooldown filter; maxPerTick < 1 returns
// empty. The MemStore walk is O(N apps) but the test fixture
// is small (MemStore is unit-test only — pgstore_test.go
// covers the production path under pgtest).
func (m *MemStore) ListOrphanedApps(_ context.Context, cooldownSeconds, maxPerTick int) ([]App, error) {
	if maxPerTick < 1 {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	var out []App
	for _, a := range m.apps {
		if a.NodeID == "" {
			continue
		}
		if a.Status != AppActive && a.Status != AppEvictedCold {
			continue
		}
		node, ok := m.computeNodes[a.NodeID]
		if !ok || node.Active {
			continue
		}
		if cooldownSeconds >= 0 {
			if a.ReassignedAt != nil &&
				now.Sub(*a.ReassignedAt) < time.Duration(cooldownSeconds)*time.Second {
				continue
			}
		}
		out = append(out, a)
	}
	// Sort: null ReassignedAt first (never reassigned = always
	// eligible, treat as priority); then by id for stable
	// ordering across runs.
	sort.Slice(out, func(i, j int) bool {
		iNil := out[i].ReassignedAt == nil
		jNil := out[j].ReassignedAt == nil
		if iNil != jNil {
			return iNil
		}
		if !iNil && !out[i].ReassignedAt.Equal(*out[j].ReassignedAt) {
			return out[i].ReassignedAt.Before(*out[j].ReassignedAt)
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > maxPerTick {
		out = out[:maxPerTick]
	}
	return out, nil
}

// ReassignAppOwner mirrors pkg/state/pgstore.go::ReassignAppOwner.
// Conditional under m.mu: fromNodeID + status in {active,
// evicted_cold} predicate matches the SQL; on success, the
// ReassignedAt pointer is set to now() so the cooldown
// filter suppresses further moves for at least
// api.RebalanceCooldownSeconds. Returns ErrConflict on a
// lost race / moved-to-live / missing-row — the rebalancer
// drops silently.
func (m *MemStore) ReassignAppOwner(_ context.Context, appID, fromNodeID, toNodeID string) error {
	if appID == "" {
		return fmt.Errorf("state: reassign app owner: empty appID")
	}
	if fromNodeID == "" {
		return fmt.Errorf("state: reassign app owner: empty fromNodeID")
	}
	if toNodeID == "" {
		return fmt.Errorf("state: reassign app owner: empty toNodeID")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.apps[appID]
	if !ok {
		return ErrNotFound
	}
	if a.NodeID != fromNodeID {
		return ErrConflict
	}
	if a.Status != AppActive && a.Status != AppEvictedCold {
		return ErrConflict
	}
	now := time.Now()
	a.NodeID = toNodeID
	a.ReassignedAt = &now
	m.apps[appID] = a
	return nil
}

// ListLiveInstancesOnNode mirrors
// pkg/state/pgstore.go::ListLiveInstancesOnNode. Filters to
// state = 'running' (matches MarkInstanceMigrating's predicate;
// WAKING/COLD_BOOTING/SNAPSHOTTING stay on the dying node and
// the dying vmmd drives the cold-boot to completion) and to the
// given node_id (or every inactive-owner instance when nodeID
// is empty). Returns an empty slice (not ErrNotFound) on no
// matches; callers treat that as "nothing to migrate this
// tick". Migration lineage fields on the returned Instance are
// zero values (the memstore fixture is exercised by unit tests
// that drive the four-phase handoff through the engine
// surface, not through state row reads).
func (m *MemStore) ListLiveInstancesOnNode(_ context.Context, nodeID string, maxPerTick int) ([]Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if maxPerTick < 1 {
		return nil, nil
	}
	out := make([]Instance, 0, maxPerTick)
	for _, ins := range m.instances {
		if ins.State != string(StateRunning) {
			continue
		}
		if nodeID != "" && ins.NodeID != nodeID {
			continue
		}
		// nodeID == "": cold-start variant. We don't model
		// compute_nodes ownership in the memstore, so the
		// in-memory filter is just "running" — the
		// unit-test fixture for the cold-start sweep.
		out = append(out, ins)
		if len(out) >= maxPerTick {
			break
		}
	}
	return out, nil
}

// MarkInstanceMigrating mirrors pkg/state/pgstore.go::
// MarkInstanceMigrating. Conditional on state='running' +
// node_id=currentNodeID; the state-machine guard is the
// in-memory equivalent of the SQL predicate. Stamps
// lease_token alongside state='migrating' so the rollback
// predicate at Phase 4 (CancelInstanceMigration) can match.
// Returns ErrConflict on a lost race / row gone.
func (m *MemStore) MarkInstanceMigrating(_ context.Context, instanceID, currentNodeID, leaseToken string) error {
	if instanceID == "" {
		return fmt.Errorf("state: mark instance migrating: empty instanceID")
	}
	if currentNodeID == "" {
		return fmt.Errorf("state: mark instance migrating: empty currentNodeID")
	}
	if leaseToken == "" {
		return fmt.Errorf("state: mark instance migrating: empty leaseToken")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ins, ok := m.instances[instanceID]
	if !ok {
		return ErrNotFound
	}
	if ins.NodeID != currentNodeID || ins.State != string(StateRunning) {
		return ErrConflict
	}
	ins.State = string(StateMigrating)
	ins.LeaseToken = leaseToken
	m.instances[instanceID] = ins
	return nil
}

// MigrateInstanceOwner mirrors pkg/state/pgstore.go::
// MigrateInstanceOwner. Two-step in-memory transaction: flip
// the instance row (conditional on state='migrating' +
// node_id=fromNodeID), then stamp apps.migrated_at. Returns
// ErrConflict on a lost race / row gone.
func (m *MemStore) MigrateInstanceOwner(_ context.Context, instanceID, fromNodeID, toNodeID, leaseToken string) error {
	if instanceID == "" {
		return fmt.Errorf("state: migrate instance owner: empty instanceID")
	}
	if fromNodeID == "" || toNodeID == "" {
		return fmt.Errorf("state: migrate instance owner: empty from/to nodeID")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ins, ok := m.instances[instanceID]
	if !ok {
		return ErrNotFound
	}
	if ins.State != "migrating" || ins.NodeID != fromNodeID {
		return ErrConflict
	}
	now := time.Now()
	migFrom := fromNodeID
	ins.NodeID = toNodeID
	ins.MigratedFromNodeID = &migFrom
	ins.MigratedAt = &now
	ins.LeaseToken = leaseToken
	ins.State = string(StateRunning)
	m.instances[instanceID] = ins
	// Stamp apps.migrated_at to match the SQL transaction's
	// second UPDATE.
	if a, ok := m.apps[ins.AppID]; ok {
		a.MigratedAt = &now
		m.apps[ins.AppID] = a
	}
	return nil
}

// CancelInstanceMigration mirrors pkg/state/pgstore.go::
// CancelInstanceMigration. Conditional on state='migrating' +
// node_id=originalNodeID + lease_token=leaseToken; restores
// state='parked' and clears lease_token.
func (m *MemStore) CancelInstanceMigration(_ context.Context, instanceID, originalNodeID, leaseToken string) error {
	if instanceID == "" {
		return fmt.Errorf("state: cancel instance migration: empty instanceID")
	}
	if originalNodeID == "" {
		return fmt.Errorf("state: cancel instance migration: empty originalNodeID")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ins, ok := m.instances[instanceID]
	if !ok {
		return ErrNotFound
	}
	if ins.State != "migrating" || ins.NodeID != originalNodeID || ins.LeaseToken != leaseToken {
		return ErrConflict
	}
	ins.State = "parked"
	ins.LeaseToken = ""
	m.instances[instanceID] = ins
	return nil
}

// ListExpiredMigrations mirrors pkg/state/pgstore.go::
// ListExpiredMigrations. Returns every instance in
// state='migrating' with a non-empty lease_token, in
// instance-id order, capped at maxPerTick. Returns nil
// (not ErrNotFound) when empty.
func (m *MemStore) ListExpiredMigrations(_ context.Context, maxPerTick int) ([]Instance, error) {
	if maxPerTick < 1 {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Instance
	for _, ins := range m.instances {
		if ins.State != string(StateMigrating) || ins.LeaseToken == "" {
			continue
		}
		out = append(out, ins)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > maxPerTick {
		out = out[:maxPerTick]
	}
	return out, nil
}

// ReinviteMigratingInstance mirrors pkg/state/pgstore.go::
// ReinviteMigratingInstance. Conditional on state='migrating'
// + lease_token=leaseToken; flips state='running' and clears
// lease_token. Returns ErrConflict on predicate miss.
func (m *MemStore) ReinviteMigratingInstance(_ context.Context, instanceID, leaseToken string) error {
	if instanceID == "" {
		return fmt.Errorf("state: reinvite migrating instance: empty instanceID")
	}
	if leaseToken == "" {
		return fmt.Errorf("state: reinvite migrating instance: empty leaseToken")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ins, ok := m.instances[instanceID]
	if !ok {
		return ErrNotFound
	}
	if ins.State != string(StateMigrating) || ins.LeaseToken != leaseToken {
		return ErrConflict
	}
	ins.State = string(StateRunning)
	now := time.Now().UTC()
	ins.MigratedAt = &now
	ins.LeaseToken = ""
	m.instances[instanceID] = ins
	return nil
}

// AbortMigratingInstance mirrors pkg/state/pgstore.go::
// AbortMigratingInstance. Conditional on state='migrating' +
// lease_token=leaseToken; flips state='parked', clears lease_token.
// node_id is left UNCHANGED — same rationale as the pgstore
// implementation (A5 Phase-2 leaves node_id on the OLD owner and
// migrated_from_node_id is NULL pre-Phase-3; the wake path
// dispatches via app.NodeID, so a dead instance.NodeID is
// harmless).
func (m *MemStore) AbortMigratingInstance(_ context.Context, instanceID, leaseToken string) error {
	if instanceID == "" {
		return fmt.Errorf("state: abort migrating instance: empty instanceID")
	}
	if leaseToken == "" {
		return fmt.Errorf("state: abort migrating instance: empty leaseToken")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ins, ok := m.instances[instanceID]
	if !ok {
		return ErrNotFound
	}
	if ins.State != string(StateMigrating) || ins.LeaseToken != leaseToken {
		return ErrConflict
	}
	ins.State = string(StateParked)
	ins.LeaseToken = ""
	m.instances[instanceID] = ins
	return nil
}

// ListRunningInstancesOnDeadNodes mirrors the PgStore join: RUNNING
// rows whose node is inactive OR whose last heartbeat predates the
// threshold. Sorted oldest-heartbeat-first so the capped tick drains
// the longest-dead nodes first, matching the SQL ORDER BY. A row whose
// node_id has no compute_nodes entry is treated as dead — the owner is
// unknowable, so it cannot be confirmed alive.
func (m *MemStore) ListRunningInstancesOnDeadNodes(_ context.Context, threshold time.Time, limit int) ([]Instance, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("state: list running instances on dead nodes: limit must be > 0, got %d", limit)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	type scored struct {
		ins Instance
		hb  time.Time
	}
	var hits []scored
	for _, ins := range m.instances {
		if ins.State != string(StateRunning) {
			continue
		}
		node, ok := m.computeNodes[ins.NodeID]
		if ok && node.Active && !node.LastHeartbeatAt.Before(threshold) {
			continue
		}
		hits = append(hits, scored{ins: ins, hb: node.LastHeartbeatAt})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].hb.Equal(hits[j].hb) {
			// Stable tie-break so a capped tick is deterministic
			// across runs (map iteration order is not).
			return hits[i].ins.ID < hits[j].ins.ID
		}
		return hits[i].hb.Before(hits[j].hb)
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]Instance, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.ins)
	}
	return out, nil
}

// FailRunningInstanceOnDeadNode mirrors the PgStore conditional
// UPDATE: the transition only lands when the row is still RUNNING and
// still owned by the node the caller observed as dead. A row that
// has vanished between the input-set query and this call returns
// ErrConflict — the same outcome PgStore surfaces via
// RowsAffected()==0. The reconciler's caller treats ErrConflict as
// a peer-wins no-op, so a transient GC (retention sweep, manual
// operator delete, future multi-host park path) is counted the same
// way as "node recovered" rather than as outcome=error. ErrNotFound
// is reserved for explicit precondition violations (the caller
// passed an instanceID the store has never heard of) — that signal
// is too loud to use as a silent "the row disappeared" handler.
func (m *MemStore) FailRunningInstanceOnDeadNode(_ context.Context, instanceID, nodeID string) error {
	if instanceID == "" {
		return fmt.Errorf("state: fail running instance on dead node: empty instanceID")
	}
	if nodeID == "" {
		return fmt.Errorf("state: fail running instance on dead node: empty nodeID")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ins, ok := m.instances[instanceID]
	if !ok {
		return ErrConflict
	}
	if ins.State != string(StateRunning) || ins.NodeID != nodeID {
		return ErrConflict
	}
	ins.State = string(StateFailed)
	now := m.clock()
	ins.TerminalAt = &now
	m.instances[instanceID] = ins
	return nil
}

func (m *MemStore) CountDeployedApps(_ context.Context, accountID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, a := range m.apps {
		if a.AccountID == accountID && (a.Status == AppActive || a.Status == AppEvictedCold) {
			n++
		}
	}
	return n, nil
}

// CountAppsWithEvictionPriority mirrors the pgstore read for the
// per-account reserved-tier cap (issue #475). Same shape as
// CountDeployedApps — counts APPS (not instances), excludes soft-deleted
// apps so the cap tracks the live customer surface.
func (m *MemStore) CountAppsWithEvictionPriority(_ context.Context, accountID, priority string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, a := range m.apps {
		if a.AccountID != accountID {
			continue
		}
		if a.Status == AppDeleted {
			continue
		}
		if a.EvictionPriority != priority {
			continue
		}
		n++
	}
	return n, nil
}

// CountAuthDefaultFlippedApps (issue #695 / ADR-080) mirrors the
// pgstore implementation — counts live (non-deleted) apps with
// auth_default_flipped_at != nil per account. See the pgstore
// counterpart for the load-bearing semantics; the dashboard
// banner turns itself off when the count reaches zero.
func (m *MemStore) CountAuthDefaultFlippedApps(_ context.Context, accountID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, a := range m.apps {
		if a.AccountID != accountID {
			continue
		}
		if a.Status == AppDeleted {
			continue
		}
		if a.AuthDefaultFlippedAt == nil {
			continue
		}
		n++
	}
	return n, nil
}

// AuthDefaultFlippedAt (issue #695 / ADR-080) returns the
// earliest stamp across all in-memory apps whose
// AuthDefaultFlippedAt is non-nil. This stands in for the
// pgstore events-table read; in memstore there's no separate
// audit log so we project the stamp column directly. The
// dashboard banner's "On YYYY-MM-DD" copy renders whichever
// date the migration would have produced; the memstore path is
// only hit by unit tests so the projection is sufficient.
func (m *MemStore) AuthDefaultFlippedAt(_ context.Context) (time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var earliest time.Time
	for _, a := range m.apps {
		if a.AuthDefaultFlippedAt == nil {
			continue
		}
		t := *a.AuthDefaultFlippedAt
		if earliest.IsZero() || t.Before(earliest) {
			earliest = t
		}
	}
	return earliest, nil
}

func (m *MemStore) UpdateApp(_ context.Context, id string, p UpdateAppParams) (App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.apps[id]
	if !ok {
		return App{}, ErrNotFound
	}
	// ADR-119: cross-app unique-IP check. Mirrors the pgstore
	// apps_static_egress_ip_key partial unique index. The
	// index covers apps in the same account only (a future
	// multi-account index would need a different shape; the
	// current contract is "one app on this account can pin
	// this IP"). MemStore has no SQL index, so the check is
	// explicit: a SetStaticEgressIP with a non-nil IP that
	// another app on the SAME ACCOUNT already pins returns
	// ErrConflict, surfacing as 23505 on pgstore. The error
	// message includes the index name so the apid handler
	// can branch on the conflict (mirrors the pgstore path
	// at cmd/apid/handlers_apps_static_egress_ip.go).
	if p.SetStaticEgressIP && p.StaticEgressIP != nil {
		newIP := *p.StaticEgressIP
		for otherID, other := range m.apps {
			if otherID == id {
				continue
			}
			if other.AccountID != a.AccountID {
				continue
			}
			if other.StaticEgressIP == nil {
				continue
			}
			if *other.StaticEgressIP == newIP {
				return App{}, fmt.Errorf("apps_static_egress_ip_key: %w", ErrConflict)
			}
		}
	}
	if p.RAMMB != nil {
		a.RAMMB = *p.RAMMB
	}
	if p.SetIdleTimeout {
		a.IdleTimeoutS = intOrZero(p.IdleTimeoutS)
	}
	if p.MaxConcurrency != nil {
		a.MaxConcurrency = *p.MaxConcurrency
	}
	if p.Status != nil {
		a.Status = *p.Status
	}
	if p.Manifest != nil {
		a.Manifest = *p.Manifest
	}
	if p.SetMinInstances {
		a.MinInstances = intOrZero(p.MinInstances)
	}
	if p.SetEgressAllowlist {
		// ADR-031 + ADR-032: nil-with-Set is treated as "clear to
		// default (empty)" so the API can express "drop the
		// allowlist back to no-list" via a PATCH with
		// egress_allowlist:[] ; non-nil is copied verbatim. The
		// slice is intentionally reallocated so the caller can't
		// mutate the stored value through the slice header it holds
		// after the call returns. v4 + v6 entries share the same
		// counter (Pro 16, Scale 64) — see
		// pkg/api/limits.go::EgressAllowlistMaxSize.
		src := derefPrefixes(p.EgressAllowlist)
		dst := make([]netip.Prefix, len(src))
		copy(dst, src)
		a.EgressAllowlist = dst
	}
	// ADR-118: per-app ingress IP allowlist. Same Set-bit convention
	// as EgressAllowlist above. The DB trigger at
	// migrations/00308_apps_public_auth_ip_allowlist.sql rejects
	// non-v4/v6 families and masklen /0 (defence in depth on top of
	// the apid parse step); the in-memory store trusts the apid
	// layer to have already validated. Plan-gated upstream — Free/Hobby
	// never reach this branch (apid returns 403 before the store
	// is touched).
	if p.SetPublicAuthIPAllowlist {
		src := derefPrefixes(p.PublicAuthIPAllowlist)
		dst := make([]netip.Prefix, len(src))
		copy(dst, src)
		a.PublicAuthIPAllowlist = dst
	}
	// Issue #169 / #172: per-app reactive scale-up trigger. Set
	// distinguishes "unset" (don't touch) from "explicit zero"
	// (disable). Apid already gated the plan and the bounds
	// (RPS > 0, CPU in [1,100]); the store is a plain column write.
	if p.SetAutoscaleTargetRPS {
		a.AutoscaleTargetRPS = intOrZero(p.AutoscaleTargetRPS)
	}
	if p.SetAutoscaleTargetCPUPct {
		a.AutoscaleTargetCPUPct = intOrZero(p.AutoscaleTargetCPUPct)
	}
	// Issue #471: per-app streaming flag. Same Set-bit convention as
	// the autoscale targets — SetStreamingEnabled distinguishes "don't
	// touch" from "explicit false" (opt out of streaming). Apid
	// already gated the plan; the store is a plain column write.
	if p.SetStreamingEnabled {
		a.StreamingEnabled = boolOrFalse(p.StreamingEnabled)
	}
	// Issue #676 / ADR-080: per-app raw-bytes Upgrade bridge. Same
	// Set-bit convention as streaming_enabled above — the Set bit
	// distinguishes "don't touch" from "explicit false" (opt out
	// of Upgrade traffic). Apid already gated the plan; the store
	// is a plain column write.
	if p.SetWebSocketEnabled {
		a.WebSocketEnabled = boolOrFalse(p.WebSocketEnabled)
	}
	// ADR-093: per-route observability opt-in. Same Set-bit
	// convention as WebSocketEnabled above — the Set bit
	// distinguishes "don't touch" from "explicit false" (opt out
	// of per-route metrics). Apid already gated the plan; the
	// store is a plain column write.
	if p.SetRouteMetricsEnabled {
		a.RouteMetricsEnabled = boolOrFalse(p.RouteMetricsEnabled)
	}
	// Issue #462 / ADR-058 / PR-A: per-app scaling policy. The
	// Set bit is the canonical "unset vs explicit zero" signal;
	// when Set is true the jsonb column is overwritten (deep-copied
	// to avoid caller-mutation aliasing) and the legacy
	// `min_instances` column is kept in sync so the reaper + SDK
	// see the same floor. The policy is the canonical source at
	// PR-A; the legacy column is the projection.
	if p.SetScalingPolicy && p.ScalingPolicy != nil {
		copyPolicy := *p.ScalingPolicy
		a.ScalingPolicy = &copyPolicy
		// Sync the legacy min_instances column with the policy's
		// MinInstances. Mirrors the pgstore's policy-comes-first
		// CASE so the in-memory and on-disk shapes stay
		// consistent.
		if p.ScalingPolicy.MinInstances != 0 {
			a.MinInstances = p.ScalingPolicy.MinInstances
		}
	}
	// Issue #472 / ADR-054: per-app cosign signature-enforcement flag.
	// Same Set-bit convention — SetRequireSigned distinguishes "don't
	// touch" from "explicit false" (opt out of signed-image enforcement).
	// Apid already gated the admin scope; the store is a plain column
	// write. imaged reads this at buildImageLayer time.
	if p.SetRequireSigned {
		a.RequireSigned = boolOrFalse(p.RequireSigned)
	}
	// Issue #470 / ADR-055: per-app warm-snapshot knobs. Same Set-bit
	// convention as require_signed / streaming_enabled — the Set bit
	// distinguishes "don't touch" from "explicit reset". Apid already
	// gated the plan (Free/Hobby + true is rejected) and the bounds
	// (1..100 / 100..60000); the store is a plain column write.
	if p.SetWarmSnapshotEnabled {
		a.WarmSnapshotEnabled = boolOrFalse(p.WarmSnapshotEnabled)
	}
	if p.SetWarmSnapshotMinRequests {
		a.WarmSnapshotMinRequests = intOrZero(p.WarmSnapshotMinRequests)
	}
	if p.SetWarmSnapshotMinMs {
		a.WarmSnapshotMinMs = intOrZero(p.WarmSnapshotMinMs)
	}
	// Issue #475: eviction_priority ('best_effort'|'reserved') follows
	// the same Set*/optional-pointer pattern as warm_snapshot_*. The
	// plan gate (Plan.EvictionPriorityReservedAllowed) and the per-account
	// cap (Plan.ReservedConcurrencyPerAccount) are enforced upstream in
	// apid; the store is a plain column write. derefString coerces a
	// nil pointer to "" which is harmless because the CASE guard in
	// UpdateApp's SQL short-circuits the read on !SetEvictionPriority.
	if p.SetEvictionPriority {
		a.EvictionPriority = derefString(p.EvictionPriority)
	}
	// Issue #560: per-app require_authn opt-in. Same Set-bit
	// convention as require_signed / streaming_enabled — the Set
	// bit distinguishes "don't touch" from "explicit false"
	// (opt out — back to public-by-default). Apid already gated
	// the plan (Free/Hobby + true is rejected with 403
	// plan_require_authn_not_allowed); the store is a plain
	// column write. The memstore mirrors the pgstore shape so
	// every test that exercises UpdateApp sees the same
	// behaviour regardless of backend.
	if p.SetRequireAuthn {
		a.RequireAuthn = boolOrFalse(p.RequireAuthn)
	}
	// Issue #477 / ADR-079: per-app public_auth
	// (open|bearer|basic). Memstore mirrors the on-disk shape —
	// PublicAuthMode is the column-equivalent text + the
	// PublicAuthBasicSealed byte slice is the secretbox blob
	// (encrypted by the apid seal step before persistence).
	// A PATCH mode='open' or mode='bearer' clears the
	// sealed blob so a stale secretbox row from a previous
	// mode='basic' PATCH never reaches a fresh request.
	// SetPublicAuth distinguishes "unset" from
	// "explicit set"; when Set is true the store overwrites
	// the column-shaped fields verbatim.
	if p.SetPublicAuth && p.PublicAuth != nil {
		a.PublicAuthMode = p.PublicAuth.Mode
		a.PublicAuthBasicSealed = append([]byte(nil), p.PublicAuth.Sealed...)
	}
	// Phase 5 repo decomposition (ADR-050 §3): pkg/reconcile uses
	// these to stamp a fresh workload identity on a changed app. The
	// apid handler never sets them (customers don't touch root_dir
	// / workload_name / start_command via PATCH today). nil = leave
	// alone; non-nil pointer = copy verbatim.
	if p.RootDir != nil {
		a.RootDir = *p.RootDir
	}
	if p.WorkloadName != nil {
		a.WorkloadName = *p.WorkloadName
	}
	if p.StartCommand != nil {
		a.StartCommand = *p.StartCommand
	}
	// Issue #695 / ADR-080: grand-father clear path. Mirrors the
	// pgstore $44 CASE so the in-memory and on-disk shapes stay
	// consistent. Apid sets ClearAuthDefaultFlippedAt whenever the
	// customer made a deliberate PATCH choice on require_authn OR
	// public_auth; the stamp clears and the dashboard banner count
	// drops. No-op for new post-flip apps (column is already NULL)
	// and for no-touch PATCHes (SetRequireAuthn/SetPublicAuth false).
	if p.ClearAuthDefaultFlippedAt {
		a.AuthDefaultFlippedAt = nil
	}
	// Tier A10 / ADR-088: per-app overflow_node preference.
	// Set bit controls the write — "don't touch" by default,
	// "explicit NULL" (clear → A9 fallback) when Set is true
	// with a nil pointer, and "set" when Set is true with a
	// non-nil pointer. Apid has already validated the UUID
	// against the empty-uuid CHECK + FK with ON DELETE SET
	// NULL (migration 00167) before reaching this path; the
	// store is a plain column write. Memstore mirrors the
	// pgstore shape so every test that exercises UpdateApp
	// sees the same behaviour regardless of backend.
	if p.SetOverflowNode {
		if p.OverflowNode == nil {
			a.OverflowNode = nil
		} else {
			s := *p.OverflowNode
			a.OverflowNode = &s
		}
	}
	// CORS improvements D1: per-app default CORS opt-in +
	// allowlist. Same Set-bit convention as the other partial
	// PATCH fields. SetCORSDefaultEnabled distinguishes "don't
	// touch" from "explicit false" (opt out of an enabled
	// fallback). SetCORSDefaultOrigins distinguishes "don't
	// touch" from "explicit empty list" (clear an enabled
	// fallback). The validator runs above the store layer so
	// we never see (enabled=true, origins=nil) here.
	if p.SetCORSDefaultEnabled {
		// Lifts nullable wire shape into the
		// store-layer pointer field. nil → nil
		// (legacy row, opt-out triple collapse),
		// *v → *v (no copy needed; the apid
		// caller already handed off ownership).
		a.CORSDefaultEnabled = p.CORSDefaultEnabled
	}
	if p.SetCORSDefaultOrigins {
		if p.CORSDefaultOrigins == nil {
			a.CORSDefaultOrigins = nil
		} else {
			src := *p.CORSDefaultOrigins
			dst := make([]string, len(src))
			copy(dst, src)
			a.CORSDefaultOrigins = dst
		}
	}
	// ADR-119: per-app static egress IP. SetStaticEgressIP
	// distinguishes "don't touch" (false) from "explicit set or
	// clear" (true). Apid gates the plan and the IPv4-only
	// shape; the store is a plain column write. nil pointer with
	// Set=true means "clear" (DELETE wire shape), copying the
	// pgstore's CASE-based $57+$58 shape so an in-memory test
	// sees the same persistence surface as a real DB call.
	if p.SetStaticEgressIP {
		if p.StaticEgressIP == nil {
			a.StaticEgressIP = nil
			a.StaticEgressIPSetAt = nil
		} else {
			cp := *p.StaticEgressIP
			a.StaticEgressIP = &cp
			now := time.Now().UTC()
			a.StaticEgressIPSetAt = &now
		}
	}
	m.apps[id] = a
	return a, nil
}

// RenameApp atomically swaps an app's slug (issue #63). Scans the
// in-memory map under lock for the (accountID, oldSlug) pair; rejects
// newSlug collisions with ErrConflict so tests can exercise the same
// 409 surface PgStore produces from the apps.slug unique constraint.
func (m *MemStore) RenameApp(_ context.Context, accountID, oldSlug, newSlug string) (App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var found *App
	for i := range m.apps {
		a := m.apps[i]
		if a.AccountID == accountID && a.Slug == oldSlug && a.Status != AppDeleted {
			cp := a
			found = &cp
			break
		}
	}
	if found == nil {
		return App{}, ErrNotFound
	}
	for i := range m.apps {
		other := m.apps[i]
		if other.ID != found.ID && other.Slug == newSlug && other.Status != AppDeleted {
			return App{}, ErrConflict
		}
	}
	found.Slug = newSlug
	m.apps[found.ID] = *found
	return *found, nil
}

// SetAppMinInstances stamps the per-app floor (ux_spec §6.5). Plan-tier
// gating is the apid handler's job — the store writes the column
// unconditionally. Returns ErrNotFound when the app is gone so a
// redelivered PATCH returns 404 cleanly.
func (m *MemStore) SetAppMinInstances(_ context.Context, appID string, min int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.apps[appID]
	if !ok {
		return ErrNotFound
	}
	a.MinInstances = min
	m.apps[appID] = a
	return nil
}

// SetAppWorkloadClass mirrors PgStore.SetAppWorkloadClass. The
// `source` argument is metadata only — the store does not persist
// or log it. The same ErrInvalidArgument / ErrNotFound contract as
// PgStore keeps tests parameterizable across backends.
func (m *MemStore) SetAppWorkloadClass(_ context.Context, appID string, class WorkloadClass, source string) (App, error) {
	_ = source
	if class == "" {
		return App{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.apps[appID]
	if !ok {
		return App{}, ErrNotFound
	}
	a.WorkloadClass = class
	m.apps[appID] = a
	return a, nil
}

func (m *MemStore) DeleteApp(ctx context.Context, id string) error {
	// Legacy thin wrapper retained for the apid deleteApp handler.
	_, err := m.SoftDeleteAppCascade(ctx, id)
	return err
}

// SoftDeleteAppCascade marks the app deleted (status=AppDeleted) and
// returns the freshly-deleted App row. Memstore parity with
// PgStore.SoftDeleteAppCascade — status-only, child rows survive.
func (m *MemStore) SoftDeleteAppCascade(_ context.Context, id string) (App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.apps[id]
	if !ok {
		return App{}, ErrNotFound
	}
	a.Status = AppDeleted
	m.apps[id] = a
	return a, nil
}

// RecordGitHubBinding persists the (app → installation_id, repo,
// branch) tuple. Idempotent: re-binding overwrites. Refuses if the
// (install_id, repo) pair is already claimed by a different app
// (mirrors the apps_github_install_repo_uniq partial index in
// migration 00007 — the §11 least-privilege audit invariant).
func (m *MemStore) RecordGitHubBinding(_ context.Context, appID string, installID int64, repoFullName, productionBranch string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.apps[appID]; !ok {
		return ErrNotFound
	}
	for otherAppID, b := range m.githubBindings {
		if otherAppID == appID {
			continue
		}
		if b.InstallID == installID && b.RepoFullName == repoFullName {
			return fmt.Errorf("state: github binding already held by app %s", otherAppID)
		}
	}
	m.githubBindings[appID] = GitHubBinding{
		AppID:            appID,
		InstallID:        installID,
		RepoFullName:     repoFullName,
		ProductionBranch: productionBranch,
	}
	return nil
}

// GitHubBindingForApp returns the persisted binding for an app, or
// ErrNotFound if the app has never been GitHub-connected.
func (m *MemStore) GitHubBindingForApp(_ context.Context, appID string) (GitHubBinding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.githubBindings[appID]
	if !ok || b.InstallID == 0 {
		return GitHubBinding{}, ErrNotFound
	}
	return b, nil
}

// InstallationIDForRepo is the reverse lookup githubd's checks.go
// uses to mint the right per-install access token for a push
// (review finding #1+#2 closure for the M7.5 OAuth path).
// Returns ErrNotFound if no app is bound to repoFullName. The map
// scan is O(apps bound to GitHub); at v1.0 scale (≤100 apps per
// account on the Scale plan, §4.2 limits) this is cheaper than
// maintaining a second repo→install index.
func (m *MemStore) InstallationIDForRepo(_ context.Context, repoFullName string) (int64, error) {
	if repoFullName == "" {
		return 0, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, b := range m.githubBindings {
		if b.RepoFullName == repoFullName && b.InstallID != 0 {
			return b.InstallID, nil
		}
	}
	return 0, ErrNotFound
}

// UpsertGithubInstallBinding mirrors PgStore. The in-memory map is
// keyed by appID so a second call with the same AppID overwrites.
// Returns ErrNotFound when the appID doesn't exist.
func (m *MemStore) UpsertGithubInstallBinding(_ context.Context, b GitHubBinding) error {
	if b.AppID == "" {
		return ErrNotFound
	}
	if b.BindingID == "" {
		return fmt.Errorf("state: BindingID required")
	}
	if b.AccountID == "" {
		return fmt.Errorf("state: AccountID required")
	}
	if b.LinkedAt.IsZero() {
		b.LinkedAt = time.Now()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.apps[b.AppID]; !ok {
		return ErrNotFound
	}
	m.githubBindings[b.AppID] = b
	return nil
}

// DeleteGithubInstallBinding clears the binding for an app.
// Idempotent: a no-prior-binding appID updates zero rows and
// returns nil. Returns ErrNotFound only when the app row itself is
// missing (matches PgStore's contract).
func (m *MemStore) DeleteGithubInstallBinding(_ context.Context, appID string) error {
	if appID == "" {
		return ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.apps[appID]; !ok {
		return ErrNotFound
	}
	delete(m.githubBindings, appID)
	return nil
}

// GithubInstallBindingForRepoBranch is the inbound-webhook dispatch
// lookup. Mirrors PgStore. Returns ErrNotFound when no app is bound.
func (m *MemStore) GithubInstallBindingForRepoBranch(_ context.Context, repoFullName, productionBranch string) (GitHubBinding, error) {
	if repoFullName == "" {
		return GitHubBinding{}, ErrNotFound
	}
	if productionBranch == "" {
		productionBranch = "main"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, b := range m.githubBindings {
		if b.RepoFullName == repoFullName && b.ProductionBranch == productionBranch && b.InstallID != 0 {
			return b, nil
		}
	}
	return GitHubBinding{}, ErrNotFound
}

// ListGithubInstallBindingsForAccount returns the per-account bind
// map keyed by appID. Mirrors PgStore.
func (m *MemStore) ListGithubInstallBindingsForAccount(_ context.Context, accountID string) (map[string]GitHubBinding, error) {
	out := make(map[string]GitHubBinding)
	if accountID == "" {
		return out, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for appID, b := range m.githubBindings {
		if b.AccountID == accountID && b.InstallID != 0 {
			out[appID] = b
		}
	}
	return out, nil
}

// UpsertGitHubInstall persists the durable OAuth handshake state
// (PR-C). Idempotent on (AccountID) by map insert; the AccountID
// FK isn't enforced in MemStore (in-memory), but the upsert still
// rejects empty AccountID / AuditGithubLogin so test parity with
// PgStore holds.
func (m *MemStore) UpsertGitHubInstall(_ context.Context, inst GitHubInstall) error {
	if inst.AccountID == "" {
		return ErrNotFound
	}
	if inst.AuditGithubLogin == "" {
		return fmt.Errorf("state: AuditGithubLogin required (§11 paper trail)")
	}
	if inst.SealedAt.IsZero() {
		inst.SealedAt = time.Now()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.githubInstalls[inst.AccountID] = inst
	return nil
}

// GitHubInstallForAccount returns the durable install row for an
// account. Returns ErrNotFound on miss so the caller can distinguish
// "no OAuth handshake yet" from a transient read failure.
func (m *MemStore) GitHubInstallForAccount(_ context.Context, accountID string) (GitHubInstall, error) {
	if accountID == "" {
		return GitHubInstall{}, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	inst, ok := m.githubInstalls[accountID]
	if !ok {
		return GitHubInstall{}, ErrNotFound
	}
	return inst, nil
}

// UpsertGithubWebhookSecret mirrors PgStore (PR-D / ADR-012 §7
// amendment). The MemStore is the unit-test stand-in for the
// resolver; the bytea is held in an in-memory map keyed by
// installation_id.
func (m *MemStore) UpsertGithubWebhookSecret(_ context.Context, installationID int64, secret []byte, upgradedBy string) (time.Time, string, error) {
	if installationID == 0 {
		return time.Time{}, "", fmt.Errorf("memstore: UpsertGithubWebhookSecret: installation_id must be non-zero")
	}
	if len(secret) == 0 {
		return time.Time{}, "", fmt.Errorf("memstore: UpsertGithubWebhookSecret: secret must be non-empty")
	}
	if upgradedBy == "" {
		upgradedBy = "platform"
	}
	now := time.Now().UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.githubWebhookSecrets == nil {
		m.githubWebhookSecrets = map[int64][]byte{}
	}
	cp := make([]byte, len(secret))
	copy(cp, secret)
	m.githubWebhookSecrets[installationID] = cp
	if m.githubWebhookSecretMeta == nil {
		m.githubWebhookSecretMeta = map[int64]webhookSecretMeta{}
	}
	m.githubWebhookSecretMeta[installationID] = webhookSecretMeta{
		UpgradedAt: now,
		UpgradedBy: upgradedBy,
	}
	return now, upgradedBy, nil
}

// GetGithubWebhookSecret mirrors PgStore. ErrNotFound on miss.
func (m *MemStore) GetGithubWebhookSecret(_ context.Context, installationID int64) ([]byte, error) {
	if installationID == 0 {
		return nil, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	secret, ok := m.githubWebhookSecrets[installationID]
	if !ok || len(secret) == 0 {
		return nil, ErrNotFound
	}
	cp := make([]byte, len(secret))
	copy(cp, secret)
	return cp, nil
}

// GetGithubInstallBindingForApp mirrors PgStore. accountID scopes
// the lookup so a forged session can't read another tenant's binding:
// if the row exists but is bound to a different account, returns
// ErrNotFound (the same response an unbound app returns, so the
// caller can't distinguish "not bound" from "wrong account").
// Returns ErrNotFound when appID or accountID is empty.
func (m *MemStore) GetGithubInstallBindingForApp(_ context.Context, appID, accountID string) (GitHubBinding, error) {
	if appID == "" || accountID == "" {
		return GitHubBinding{}, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.githubBindings[appID]
	if !ok || b.InstallID == 0 || b.AccountID != accountID {
		return GitHubBinding{}, ErrNotFound
	}
	return b, nil
}

// --- Deployments ------------------------------------------------------------

// CreateDeployment mirrors PgStore.CreateDeployment's active-app gate
// (PR-A). Both stores must reject deployments against AppDeleted or
// missing apps with ErrNotFound — apid's s.notFound relies on this
// to return 404. The mutex already serialises the check + insert
// together, so the gate is race-free here without a tx.
//
// PR-B: the prior-deployment supersede is folded into the same
// critical section as the INSERT, mirroring PgStore's tx-wrapped
// shape. We walk m.deployments for the most-recent row whose status
// is in the "current world" set (pending/live/building/imaging/
// snapshotting), flip it to 'superseded' in the map, then insert
// the new row. The race-free supersede closes the same TOCTOU the
// image: branch had before, and gives the tarball branch the parity
// it has always lacked.
func (m *MemStore) CreateDeployment(_ context.Context, d Deployment) (Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[d.AppID]
	if !ok || app.Status == AppDeleted {
		return Deployment{}, ErrNotFound
	}
	if d.CanaryPreset == "" {
		d.CanaryPreset = "none"
	}
	if d.RolloutState == "" {
		d.RolloutState = "pending"
	}

	// Find the most-recent non-terminal deployment row for this app.
	// O(N) over the map is fine at one-box scale; spec §6 keeps the
	// rows-per-app bounded by the build cadence.
	var (
		priorID  string
		hasPrior bool
	)
	var maxCreated time.Time
	for id, existing := range m.deployments {
		if existing.AppID != d.AppID {
			continue
		}
		// Same narrow set as PgStore (PR-B): only flip pending/live
		// rows. A building/imaging/snapshotting row represents a
		// pipeline already running; flipping it would orphan the
		// vmmd VM / builderd process / imaged ext4 conversion. The
		// second deploy creates a parallel row and the schedd
		// watchdog reaps the loser on idle.
		switch existing.Status {
		case DeployPending, DeployLive:
			// current world — supersede
		default:
			continue
		}
		if !hasPrior || existing.CreatedAt.After(maxCreated) {
			priorID = id
			maxCreated = existing.CreatedAt
			hasPrior = true
		}
	}
	if hasPrior {
		// Match PgStore exactly: mutate the stored prior in-place so
		// subsequent LatestDeployment / DeploymentByID readers see
		// the supersede immediately, under m.mu.
		//
		// Issue #556 PR-A: zero the prior row's traffic_percent so
		// Σ over live rows remains 100 by construction. The new row
		// is stamped at d.TrafficPercent below (handler defaults to
		// 100 when the caller omits the optional pointer). Mirrors
		// the pgstore's two-field SET in CreateDeployment.
		prior := m.deployments[priorID]
		prior.Status = DeploySuperseded
		prior.TrafficPercent = 0
		m.deployments[priorID] = prior
	}

	if d.ID == "" {
		d.ID = newID()
	}
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now()
	}
	if d.Status == "" {
		d.Status = DeployPending
	}
	if d.Kind == "" {
		d.Kind = DeploymentKindImage
	}
	// Issue #556 PR-A: default traffic_percent to 100 when caller
	// supplies zero. The schema's NOT NULL DEFAULT 100 covers the
	// SQL write path; mirroring here keeps the in-memory shape
	// aligned so unit tests that exercise the store don't need
	// Postgres. PR-A semantics: traffic_percent on a fresh deploy
	// is 100; on supersede it's 0 (set above); PR-C may add
	// proportional forms.
	if d.TrafficPercent == 0 {
		d.TrafficPercent = 100
	}
	m.deployments[d.ID] = d
	return d, nil
}

func (m *MemStore) DeploymentByID(_ context.Context, id string) (Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deployments[id]
	if !ok {
		return Deployment{}, ErrNotFound
	}
	return d, nil
}

func (m *MemStore) DeploymentOrdinal(_ context.Context, appID, deploymentID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Mirror the pg-side query: row_number() over (partition by
	// app_id order by created_at, id). MemStore keeps no
	// monotonic key, so we sort a slice of (CreatedAt, ID, AppID)
	// for this app and find the row's rank.
	type key struct {
		at time.Time
		id string
	}
	rows := make([]key, 0, len(m.deployments))
	for _, d := range m.deployments {
		if d.AppID == appID {
			rows = append(rows, key{at: d.CreatedAt, id: d.ID})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].at.Equal(rows[j].at) {
			return rows[i].id < rows[j].id
		}
		return rows[i].at.Before(rows[j].at)
	})
	for i, r := range rows {
		if r.id == deploymentID {
			return i + 1, nil
		}
	}
	return 0, ErrNotFound
}

func (m *MemStore) LatestDeployment(_ context.Context, appID string) (Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var latest Deployment
	found := false
	for _, d := range m.deployments {
		if d.AppID == appID && (!found || d.CreatedAt.After(latest.CreatedAt)) {
			latest, found = d, true
		}
	}
	if !found {
		return Deployment{}, ErrNotFound
	}
	return latest, nil
}

func (m *MemStore) LiveDeployment(_ context.Context, appID string) (Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var latest Deployment
	found := false
	for _, d := range m.deployments {
		if d.AppID == appID && d.Status == DeployLive && (!found || d.CreatedAt.After(latest.CreatedAt)) {
			latest, found = d, true
		}
	}
	if !found {
		return Deployment{}, ErrNotFound
	}
	return latest, nil
}

// LiveDeploymentForScope (ADR-091 / PR-D) mirrors PgStore — iterates
// m.deployments filtering on (app_id, scope, status='live') and
// keeps the most-recent row. The MemStore has no uniqueness
// constraint that mirrors deployments_app_scope_live_uniq; if a
// test setup inserts two live rows with the same (app, scope), the
// most-recent one wins (same behaviour as LiveDeployment). The
// partial unique index only enforces the invariant in production
// Postgres.
func (m *MemStore) LiveDeploymentForScope(_ context.Context, appID, scope string) (Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var latest Deployment
	found := false
	for _, d := range m.deployments {
		if d.AppID == appID && d.Scope == scope && d.Status == DeployLive && (!found || d.CreatedAt.After(latest.CreatedAt)) {
			latest, found = d, true
		}
	}
	if !found {
		return Deployment{}, ErrNotFound
	}
	return latest, nil
}

// LiveDeployments (issue #556 / PR-B) mirrors the Postgres
// plural query in pkg/state/pgstore.go — returns every row where
// app_id=$1 AND status='live', ordered created_at DESC. Returns
// (nil, nil) when the app has no live rows so the test seam
// stays nil-vs-empty consistent with scanDeployments' empty-set
// shape (MemStore callers don't have to special-case an empty
// slice vs. an error).
func (m *MemStore) LiveDeployments(_ context.Context, appID string) ([]Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Deployment
	for _, d := range m.deployments {
		if d.AppID != appID || d.Status != DeployLive {
			continue
		}
		out = append(out, d)
	}
	// Sort created_at DESC for parity with PgStore (it costs O(n log n)
	// but the set is tiny — ≤ a handful of live deployments per app).
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

// ListCanaryInFlight (issue #976 / ADR-122 / SAFE-RELEASES-A + F)
// mirrors PgStore.ListCanaryInFlight across all apps: status='live'
// AND canary_total_steps > 0 AND canary_step < canary_total_steps
// AND rollout_state IN ('pending','rolling_out'). Tests pin both
// impls against the same predicate so a refactor cannot silently
// drift the in-memory surface from the on-disk query.
func (m *MemStore) ListCanaryInFlight(_ context.Context) ([]Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Deployment
	for _, d := range m.deployments {
		if d.Status != DeployLive {
			continue
		}
		if d.CanaryTotalSteps <= 0 || d.CanaryStep >= d.CanaryTotalSteps {
			continue
		}
		if d.RolloutState != "pending" && d.RolloutState != "rolling_out" {
			continue
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

// SafedeployListPendingRollouts (issue #976 / ADR-122 /
// SAFE-RELEASES-F) mirrors PgStore.SafedeployListPendingRollouts.
// Strict superset of ListCanaryInFlight — includes rows with
// canary_total_steps=0 so the orchestrator can stamp them
// 'complete' on first tick. The ordering discipline matches the
// SQL (rollout_started_at NULLS FIRST, created_at ASC) so the
// MemStore-backed integration tests can pin a deterministic
// walk order.
func (m *MemStore) SafedeployListPendingRollouts(_ context.Context) ([]Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Deployment
	for _, d := range m.deployments {
		if d.Status != DeployLive {
			continue
		}
		if d.RolloutState != "pending" && d.RolloutState != "rolling_out" {
			continue
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		// rollout_started_at NULLS FIRST: a zero-value pointer
		// sorts before any non-zero time. Time.Time{}.IsZero()
		// is the in-memory mirror of SQL NULL.
		if out[i].RolloutStartedAt == nil && out[j].RolloutStartedAt != nil {
			return true
		}
		if out[i].RolloutStartedAt != nil && out[j].RolloutStartedAt == nil {
			return false
		}
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

// SafedeployStampRollout (issue #976 / ADR-122 / SAFE-RELEASES-F)
// mirrors PgStore.SafedeployStampRollout for the in-memory test
// seam. The PgStore version takes FOR UPDATE inside a tx; the
// MemStore just holds m.mu for the whole write so a concurrent
// orchestrator tick on the same row serialises correctly. The
// returned Deployment is a deep copy so a caller can't mutate
// the in-memory row through the returned value.
func (m *MemStore) SafedeployStampRollout(_ context.Context, id string, rolloutState string, startedAt, completedAt, abortedAt *time.Time, abortedReason string) (Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deployments[id]
	if !ok {
		return Deployment{}, ErrNotFound
	}
	d.RolloutState = rolloutState
	if startedAt != nil {
		t := *startedAt
		d.RolloutStartedAt = &t
	}
	if completedAt != nil {
		t := *completedAt
		d.RolloutCompletedAt = &t
	}
	if abortedAt != nil {
		t := *abortedAt
		d.RolloutAbortedAt = &t
	}
	d.RolloutAbortedReason = abortedReason
	m.deployments[id] = d
	return d, nil
}

// RecoverRollout (issue #976 / ADR-122 / SAFE-RELEASES-R) is the
// in-memory mirror of PgStore.RecoverRollout — the operator
// manual-recovery escape hatch used by the
// `gregale rollouts recover <slug>` CLI subcommand. Holds m.mu
// for the whole body so a concurrent canary_progression tick or
// alert-driven action executor cannot interleave a partial
// state.
//
// action ∈ {"advance", "promote", "abort"}:
//
//   - advance: requires rollout_state IN ('pending','rolling_out'),
//     canary_total_steps > 0, canary_step < canary_total_steps,
//     and canary_step_started_at older than
//     RecoverRolloutStuckAfter. Bumps canary_step by 1, stamps
//     canary_step_started_at = now(), and runs the same
//     largest-remainder redistribution as UpdateDeploymentTraffic
//     so Σ = 100 stays invariant. On step reaching
//     canary_total_steps, flips rollout_state='complete' and
//     stamps rollout_completed_at. Emits one deployment_audit
//     row with kind='deploy.traffic_changed' (the canary step
//     advance is itself a traffic change; the rollout-state flip
//     gets its own 'deploy.rolled_back' audit on abort and no
//     separate audit on completion — the final
//     canary_step_started_at + traffic_percent=100 row is the
//     audit signal).
//
//   - promote: same predicate minus the stuck-check. Sets
//     canary_step = canary_total_steps, rollout_state =
//     'complete', traffic_percent = 100 (sibling rows zeroed to
//     keep Σ=100), stamps rollout_completed_at +
//     canary_step_started_at = now(). Emits one deployment_audit
//     row with kind='deploy.traffic_changed'.
//
//   - abort: any state in ('pending','rolling_out'). Sets
//     rollout_state = 'aborted', rollout_aborted_at = now(),
//     rollout_aborted_reason = reason. Emits one deployment_audit
//     row with kind='deploy.rolled_back' (re-using the existing
//     closed-set audit kind — a CLI-initiated abort and a
//     meterd-initiated auto-rollback share the same audit
//     signal; the audit `actor` field carries the difference).
//
// Returns the refreshed Deployment + the audit row id (so the
// CLI can echo "audit_id=…"). Both backends share the same
// closed-set guards so handler tests can pin the same shape
// against either store.
func (m *MemStore) RecoverRollout(_ context.Context, appID string, action, reason string) (Deployment, int64, error) {
	// Validate action at the store boundary so a direct store
	// caller (CLI test path) gets the same 422 shape as the
	// handler. The handler also validates via
	// api.AllowedRecoverRolloutAction; this is defence-in-depth.
	switch action {
	case "advance", "promote", "abort":
	default:
		return Deployment{}, 0, ErrInvalidRecoverAction
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Find the active deployment for this app: rollout_state ∈
	// ('pending','rolling_out') and status='live'. There can be
	// at most one active rollout per app at a time (canary
	// progression auto-supersedes the prior live row).
	var target *Deployment
	for id, d := range m.deployments {
		if d.AppID != appID || d.Status != DeployLive {
			continue
		}
		if d.RolloutState != "pending" && d.RolloutState != "rolling_out" {
			continue
		}
		d := d
		target = &d
		_ = id
		break
	}
	if target == nil {
		return Deployment{}, 0, ErrNotFound
	}

	now := time.Now()

	switch action {
	case "advance":
		// Stuck-detection gate. The CLI distinguishes
		// "fix a stuck rollout" (advance is the right call)
		// from "force-step a healthy rollout" (use promote),
		// so the operator's terminal failure is loud: 409
		// ErrRolloutNotStuck.
		if target.CanaryStepStartedAt == nil {
			return Deployment{}, 0, ErrRolloutNotStuck
		}
		if now.Sub(*target.CanaryStepStartedAt) < RecoverRolloutStuckAfter {
			return Deployment{}, 0, ErrRolloutNotStuck
		}
		if target.CanaryTotalSteps <= 0 || target.CanaryStep >= target.CanaryTotalSteps {
			return Deployment{}, 0, ErrRolloutStateInvalid
		}

		// Bump step, stamp started_at.
		target.CanaryStep++
		t := now
		target.CanaryStepStartedAt = &t

		// Resolve the stage's traffic percent via the canary
		// catalog (pkg/api/canary). We don't import it here —
		// the MemStore reads the closed-set raw weights via
		// the catalog-side helper resolved in the
		// handler/safedeploy layer. For the MemStore test seam,
		// we re-use the in-memory catalog (pkg/api/canary)
		// through a deferred lookup. To keep the state
		// package import-clean we use a linear step-to-percent
		// mapping: step k / total N gets 1,10,50,100 in that
		// progression. Real production resolves via the
		// catalog; the store-layer semantics are "bump the
		// step" — the orchestrator/handler layer applies the
		// catalog and stamps traffic_percent. For the
		// MemStore test we mirror the
		// 1-10-50-100-equivalent curve.
		stepPct := stepToPercent(target.CanaryStep, target.CanaryTotalSteps)
		target.TrafficPercent = stepPct

		// Redistribute residual across sibling live rows.
		siblings := []siblingRow{}
		for otherID, other := range m.deployments {
			if other.AppID != appID || other.Status != DeployLive || otherID == target.ID {
				continue
			}
			siblings = append(siblings, siblingRow{ID: otherID, Prior: other.TrafficPercent})
		}
		sort.SliceStable(siblings, func(a, b int) bool { return siblings[a].ID < siblings[b].ID })
		newWeights := RedistributeTraffic(toHelperSiblings(siblings), 100-stepPct)
		for i, s := range siblings {
			other := m.deployments[s.ID]
			other.TrafficPercent = newWeights[i]
			m.deployments[s.ID] = other
		}

		// If the bump reaches the top of the ladder, flip
		// rollout_state='complete'.
		if target.CanaryStep >= target.CanaryTotalSteps {
			target.RolloutState = "complete"
			t := now
			target.RolloutCompletedAt = &t
		}
		m.deployments[target.ID] = *target

		auditID, err := m.appendDeploymentAuditLocked(DeploymentAudit{
			DeploymentID: uuid.MustParse(target.ID),
			AccountID:    nil,
			Kind:         DeployTrafficChanged,
			Actor:        "operator:cli:recover_rollout",
			At:           now,
			Data:         json.RawMessage(fmt.Sprintf(`{"action":"advance","reason":%q}`, reason)),
		})
		if err != nil {
			return Deployment{}, 0, fmt.Errorf("state: append recovery audit: %w", err)
		}
		return *target, auditID, nil

	case "promote":
		if target.CanaryTotalSteps <= 0 || target.CanaryStep >= target.CanaryTotalSteps {
			return Deployment{}, 0, ErrRolloutStateInvalid
		}
		target.CanaryStep = target.CanaryTotalSteps
		target.RolloutState = "complete"
		target.TrafficPercent = 100
		t := now
		target.CanaryStepStartedAt = &t
		target.RolloutCompletedAt = &t

		// Zero sibling live rows so Σ = 100.
		for otherID, other := range m.deployments {
			if other.AppID != appID || other.Status != DeployLive || otherID == target.ID {
				continue
			}
			other.TrafficPercent = 0
			m.deployments[otherID] = other
		}
		m.deployments[target.ID] = *target

		auditID, err := m.appendDeploymentAuditLocked(DeploymentAudit{
			DeploymentID: uuid.MustParse(target.ID),
			AccountID:    nil,
			Kind:         DeployTrafficChanged,
			Actor:        "operator:cli:recover_rollout",
			At:           now,
			Data:         json.RawMessage(fmt.Sprintf(`{"action":"promote","reason":%q}`, reason)),
		})
		if err != nil {
			return Deployment{}, 0, fmt.Errorf("state: append recovery audit: %w", err)
		}
		return *target, auditID, nil

	case "abort":
		target.RolloutState = "aborted"
		t := now
		target.RolloutAbortedAt = &t
		target.RolloutAbortedReason = reason
		m.deployments[target.ID] = *target

		auditID, err := m.appendDeploymentAuditLocked(DeploymentAudit{
			DeploymentID: uuid.MustParse(target.ID),
			AccountID:    nil,
			Kind:         DeployRolledBack,
			Actor:        "operator:cli:recover_rollout",
			At:           now,
			Data:         json.RawMessage(fmt.Sprintf(`{"action":"abort","reason":%q}`, reason)),
		})
		if err != nil {
			return Deployment{}, 0, fmt.Errorf("state: append recovery audit: %w", err)
		}
		return *target, auditID, nil
	}
	return Deployment{}, 0, ErrInvalidRecoverAction
}

// siblingRow + toHelperSiblings adapt the MemStore's per-method
// sibling-tracking shape to the type RedistributeTraffic expects.
type siblingRow struct {
	ID    string
	Prior int
}

func toHelperSiblings(in []siblingRow) []struct {
	ID    string
	Prior int
} {
	out := make([]struct {
		ID    string
		Prior int
	}, len(in))
	for i, s := range in {
		out[i].ID = s.ID
		out[i].Prior = s.Prior
	}
	return out
}

// stepToPercent maps a (step, total_steps) pair onto the catalog's
// percent for that step. The MemStore RecoverRollout doesn't import
// pkg/api/canary (would invert the dependency), so it inlines the
// canonical 1-10-50-100 progression the catalog returns for the
// `1-10-50-100` preset (the only preset CLI surfaces today via
// `gregale deploy --canary-preset 1-10-50-100`). Total steps == 0
// or step > total returns 0 — the handler layer is responsible for
// non-default presets, where the catalog resolves to a stage table
// that's stamped by the canary_progression tick on each step.
//
// Production deployments that use the slower presets (slow,
// balanced, aggressive) will continue to advance via the meterd
// orchestrator (which DOES import pkg/api/canary); the CLI's
// `recover` path is the operator escape hatch for stuck rollouts
// and is calibrated for the headline 4-stage cadence. Pinning the
// step-to-percent curve here keeps the MemStore test suite
// deterministic without duplicating the catalog.
func stepToPercent(step, total int) int {
	if total <= 0 || step <= 0 {
		return 0
	}
	switch total {
	case 4: // 1-10-50-100 / balanced.
		switch step {
		case 1:
			return 1
		case 2:
			return 10
		case 3:
			return 50
		case 4:
			return 100
		default:
			return 0
		}
	case 3: // aggressive (5/50/100).
		switch step {
		case 1:
			return 5
		case 2:
			return 50
		case 3:
			return 100
		}
	case 2: // slow (1/100).
		switch step {
		case 1:
			return 1
		case 2:
			return 100
		}
	}
	return 0
}

// appendDeploymentAuditLocked is the lock-held variant of
// AppendDeploymentAudit used by RecoverRollout's transactional
// body. It assumes the caller holds m.mu (RecoverRollout does);
// AppendDeploymentAudit is the public lock-acquiring wrapper.
// Behaviour matches AppendDeploymentAudit so the deployment_audit
// id sequence is monotonic across both entry points.
func (m *MemStore) appendDeploymentAuditLocked(entry DeploymentAudit) (int64, error) {
	var dataCopy json.RawMessage
	if len(entry.Data) > 0 {
		dataCopy = append(json.RawMessage(nil), entry.Data...)
	}
	at := entry.At
	if at.IsZero() {
		at = time.Now()
	}
	id := int64(len(m.deploymentAudit) + 1)
	m.deploymentAudit = append(m.deploymentAudit, DeploymentAudit{
		ID:           id,
		DeploymentID: entry.DeploymentID,
		AccountID:    entry.AccountID,
		Kind:         entry.Kind,
		Actor:        entry.Actor,
		At:           at,
		Data:         dataCopy,
	})
	return id, nil
}

// CountLiveInstancesByDeployment mirrors PgStore.CountLiveInstancesByDeployment
// for the in-memory test seam (issue #555 PR-6). The state filter
// matches the SQL — {waking, cold_booting, running} — and the
// string comparison uses lowercase state constants from
// pkg/state/machine.go (StateWaking, StateColdBooting, StateRunning).
func (m *MemStore) CountLiveInstancesByDeployment(_ context.Context, deploymentID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if deploymentID == "" {
		return 0, nil
	}
	n := 0
	for _, ins := range m.instances {
		if ins.DeploymentID != deploymentID {
			continue
		}
		switch State(ins.State) {
		case StateWaking, StateColdBooting, StateRunning:
			n++
		}
	}
	return n, nil
}

func (m *MemStore) LatestSupersededDeployment(_ context.Context, appID string) (Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var latest Deployment
	found := false
	for _, d := range m.deployments {
		if d.AppID == appID && d.Status == DeploySuperseded && (!found || d.CreatedAt.After(latest.CreatedAt)) {
			latest, found = d, true
		}
	}
	if !found {
		return Deployment{}, ErrNotFound
	}
	return latest, nil
}

// GetDeploymentByIDScopedToSuperseded mirrors PgStore.GetDeploymentByIDScopedToSuperseded.
// Returns the deployment only if it belongs to appID AND has status=DeploySuperseded.
// Returns ErrNoRollbackTarget if the row is missing or belongs to a different app;
// ErrRollbackTargetAlreadyLive if the row exists but is not superseded. SAFE-RELEASES-G.
func (m *MemStore) GetDeploymentByIDScopedToSuperseded(_ context.Context, appID, deploymentID string) (Deployment, error) {
	if appID == "" {
		return Deployment{}, fmt.Errorf("state: get deployment by id scoped to superseded: empty appID")
	}
	if deploymentID == "" {
		return Deployment{}, fmt.Errorf("state: get deployment by id scoped to superseded: empty deploymentID")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deployments[deploymentID]
	if !ok || d.AppID != appID {
		return Deployment{}, fmt.Errorf("state: rollback target %q for app %q: %w", deploymentID, appID, ErrNoRollbackTarget)
	}
	if d.Status != DeploySuperseded {
		return Deployment{}, fmt.Errorf("state: rollback target %q for app %q has status %q: %w",
			deploymentID, appID, d.Status, ErrRollbackTargetAlreadyLive)
	}
	return d, nil
}

// HasSnapshotHistory always returns (false, nil) for the in-process
// MemStore — the store doesn't model snapshot retention, so the
// snapshot-GC race check is a no-op against MemStore. This preserves
// the legacy happy-path test semantics (no seeded snapshot = check
// skipped) without leaking test-only affordances into the production
// PgStore path. SAFE-RELEASES-G.
func (m *MemStore) HasSnapshotHistory(_ context.Context, _ string) (bool, error) {
	return false, nil
}

// ListDeploymentsForApp mirrors PgStore.ListDeploymentsForApp: `limit <= 0`
// means "no row cap" (every remaining row after offset). F-10: see PgStore
// doc for the asymmetry that this version already conformed to.
func (m *MemStore) ListDeploymentsForApp(_ context.Context, appID string, limit, offset int) ([]Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if offset < 0 {
		offset = 0
	}
	var all []Deployment
	for _, d := range m.deployments {
		if d.AppID == appID {
			all = append(all, d)
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].CreatedAt.After(all[j].CreatedAt) })
	if offset >= len(all) {
		return nil, nil
	}
	all = all[offset:]
	if limit > 0 && limit < len(all) {
		all = all[:limit]
	}
	return all, nil
}

// ListDeploymentsForAccount walks every app the account owns, collects
// its deployments, and returns them sorted DESC by created_at with
// before acting as the inclusive upper bound. Cursor pagination
// (before→NextBefore) lets the dashboard page backwards without an
// offset scan.
func (m *MemStore) ListDeploymentsForAccount(_ context.Context, accountID string, before time.Time, limit int) ([]Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	owned := make(map[string]struct{})
	for _, a := range m.apps {
		if a.AccountID == accountID && a.Status != AppDeleted {
			owned[a.ID] = struct{}{}
		}
	}
	var all []Deployment
	for _, d := range m.deployments {
		if _, ok := owned[d.AppID]; !ok {
			continue
		}
		// First page (before.IsZero()): include everything created at
		// or before "before". Subsequent pages skip rows whose
		// CreatedAt >= since the caller passed the previous
		// response's last-seen CreatedAt as the "before" cursor.
		if !before.IsZero() && !d.CreatedAt.Before(before) {
			continue
		}
		all = append(all, d)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].CreatedAt.After(all[j].CreatedAt) })
	if limit > 0 && limit < len(all) {
		all = all[:limit]
	}
	return all, nil
}

func (m *MemStore) UpdateDeploymentStatus(_ context.Context, id string, status DeploymentStatus, errMsg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deployments[id]
	if !ok {
		return ErrNotFound
	}
	d.Status = status
	d.Error = errMsg
	m.deployments[id] = d
	return nil
}

func (m *MemStore) MarkDeploymentSuperseded(ctx context.Context, id string) error {
	return m.UpdateDeploymentStatus(ctx, id, DeploySuperseded, "")
}

func (m *MemStore) MarkDeploymentLive(ctx context.Context, id string) error {
	return m.UpdateDeploymentStatus(ctx, id, DeployLive, "")
}

// MarkDeploymentCancelled (ADR-124) — memstore mirror of
// pgstore.MarkDeploymentCancelled. CAS guard via the
// IsCancelEligible predicate, errrrors mirror pgstore sentinels.
func (m *MemStore) MarkDeploymentCancelled(_ context.Context, id, principal string, reason CancelReason, when time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deployments[id]
	if !ok {
		return ErrNotFound
	}
	if d.Status == DeployLive {
		return ErrCancelLiveForbidden
	}
	if !d.Status.IsCancelEligible() {
		return ErrInvalidStateTransition
	}
	if !reason.IsValid() {
		return ErrInvalidStateTransition
	}
	d.Status = DeployCancelled
	d.CancelledAt = &when
	d.CancelledByPrincipal = principal
	d.CancelReason = string(reason)
	m.deployments[id] = d
	return nil
}

// CancelDeploymentTx (ADR-124) — single-mu-lock orchestrator
// mirroring pgstore.CancelDeploymentTx. The memstore is not
// concurrent in the same way Postgres is, so we sequentially
// (a) flip the deployment row, (b) flip non-terminal build rows
// of the same deployment, (c) update memoized cancel_reason /
// cancelled_by_principal for downstream consumers (the SSE
// fan-in reads from this directly). Returns the post-flip
// deployment.
func (m *MemStore) CancelDeploymentTx(ctx context.Context, id, principal string, reason CancelReason) (Deployment, []string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deployments[id]
	if !ok {
		return Deployment{}, nil, ErrNotFound
	}
	if d.Status == DeployLive {
		return Deployment{}, nil, ErrCancelLiveForbidden
	}
	if !d.Status.IsCancelEligible() {
		return Deployment{}, nil, ErrInvalidStateTransition
	}
	if !reason.IsValid() {
		return Deployment{}, nil, ErrInvalidStateTransition
	}
	now := time.Now().UTC()
	d.Status = DeployCancelled
	d.CancelledAt = &now
	d.CancelledByPrincipal = principal
	d.CancelReason = string(reason)
	m.deployments[id] = d
	// Cascade-cancel any non-terminal build rows attached to
	// this deployment. Mirrors pgstore.CancelDeploymentTx.
	// We collect the IDs of flipped rows so the apid handler
	// can fire a build_changed pg_notify per row.
	var cancelled []string
	for buildID, b := range m.builds {
		if b.DeploymentID == d.ID && (b.Status == BuildQueued || b.Status == BuildRunning) {
			b.Status = BuildCancelled
			b.CancelledAt = &now
			b.CancelledByDeploymentCascade = true
			m.builds[buildID] = b
			cancelled = append(cancelled, buildID)
		}
	}
	return d, cancelled, nil
}

// ReorderDeployment (ADR-124) — memstore mirror. The CAS guard
// enforces status='pending'. Priority range [0, 1000] is checked
// here and again in the SQL CHECK (migration 00362).
func (m *MemStore) ReorderDeployment(_ context.Context, id string, newPriority int, principal string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deployments[id]
	if !ok {
		return ErrNotFound
	}
	if d.Status != DeployPending {
		return ErrReorderNotPending
	}
	if newPriority < 0 || newPriority > 1000 {
		return ErrPriorityOutOfRange
	}
	d.Priority = newPriority
	d.ReorderedByPrincipal = principal
	now := time.Now().UTC()
	d.ReorderedAt = &now
	m.deployments[id] = d
	return nil
}

// ClearDeployment (ADR-124) — memstore soft-delete mirror. Sets
// deleted_at / deleted_by_principal; status unchanged so the
// admin audit trail remains visible.
func (m *MemStore) ClearDeployment(_ context.Context, id, principal string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deployments[id]
	if !ok {
		return ErrNotFound
	}
	if d.Status == DeployLive {
		return ErrCancelLiveForbidden
	}
	now := time.Now().UTC()
	d.DeletedAt = &now
	d.DeletedByPrincipal = principal
	m.deployments[id] = d
	return nil
}

// ClearObsoleteDeployments (ADR-124) — memstore mirror. Honours
// the "current + previous" retention window per app by skipping
// the 2 most-recent rows per app regardless of status.
func (m *MemStore) ClearObsoleteDeployments(_ context.Context, appID string, olderThan time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	now := time.Now().UTC()
	// Track per-app retention: keep the 2 most-recent for each app.
	type rec struct {
		id  string
		enq time.Time
	}
	recentByApp := map[string][]rec{}
	for id, d := range m.deployments {
		if d.AppID != appID {
			continue
		}
		recentByApp[d.AppID] = append(recentByApp[d.AppID], rec{id: id, enq: d.CreatedAt})
	}
	for app := range recentByApp {
		arr := recentByApp[app]
		// sort by enqueued_at DESC, keep top 2
		for i := 0; i < len(arr); i++ {
			for j := i + 1; j < len(arr); j++ {
				if arr[j].enq.After(arr[i].enq) {
					arr[i], arr[j] = arr[j], arr[i]
				}
			}
		}
		if len(arr) > 2 {
			recentByApp[app] = arr[2:]
		} else {
			recentByApp[app] = nil
		}
	}
	skippable := map[string]bool{}
	for _, arr := range recentByApp {
		for _, r := range arr {
			skippable[r.id] = true
		}
	}
	for id, d := range m.deployments {
		if d.AppID != appID {
			continue
		}
		if skippable[id] {
			continue
		}
		if d.DeletedAt != nil {
			continue
		}
		if d.Status != DeploySuperseded && d.Status != DeployFailed && d.Status != DeployCancelled {
			continue
		}
		if d.CreatedAt.After(olderThan) || d.CreatedAt.Equal(olderThan) {
			continue
		}
		d.DeletedAt = &now
		d.DeletedByPrincipal = "system"
		m.deployments[id] = d
		count++
	}
	return count, nil
}

// MarkBuildCancelled (ADR-124) — memstore mirror. CAS guard on
// status ∈ {queued, running}. Records cancelled_by_deployment_cascade
// so callers can tell cancel-from-deploy vs. direct build-cancel.
func (m *MemStore) MarkBuildCancelled(_ context.Context, buildID, _ string, cascade bool, when time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.builds[buildID]
	if !ok {
		return ErrNotFound
	}
	if b.Status != BuildQueued && b.Status != BuildRunning {
		return ErrInvalidStateTransition
	}
	b.Status = BuildCancelled
	b.CancelledAt = &when
	b.CancelledByDeploymentCascade = cascade
	m.builds[buildID] = b
	return nil
}

// AppendDeploymentStage (ADR-117, migration 00302) — memstore
// mirror of PgStore.AppendDeploymentStage. The in-memory shape
// round-trips through JSON so the tests that exercise the SSE
// consumer at handlers_ext_test.go and the deployment lifecycle
// at imaged tests see the exact same wire bytes as production.
// See pgstore.go::AppendDeploymentStage for the contract.
func (m *MemStore) AppendDeploymentStage(_ context.Context, id string, from, to StageName, at time.Time, reason string) (Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deployments[id]
	if !ok {
		return Deployment{}, ErrNotFound
	}
	var state StageState
	if len(d.StageState) > 0 {
		if err := json.Unmarshal(d.StageState, &state); err != nil {
			return Deployment{}, fmt.Errorf("AppendDeploymentStage: decode stage_state for %s: %w", id, err)
		}
	}
	if state.Current != from {
		// Schema default for stage_state.current is
		// "source_download" (migrations/00302). A freshly inserted
		// row that came through CreateDeployment without an
		// explicit StageState has Current == "" — treat that as
		// the default so the first forward transition doesn't
		// surface a spurious ErrNotFound.
		if state.Current != "" {
			return Deployment{}, ErrNotFound
		}
		if from != StageSourceDownload {
			return Deployment{}, ErrNotFound
		}
		state.Current = StageSourceDownload
	}
	// Forward transition vs. failure-stamp. Same shape as
	// pgstore.AppendDeploymentStage — see the docblock there. The
	// failure path is now owned by MarkDeploymentStageFailed
	// (mirror below); `from == to` here is a programming error.
	if from == to {
		return Deployment{}, fmt.Errorf("AppendDeploymentStage: from==to is reserved for MarkDeploymentStageFailed (deployment=%s, stage=%s)", id, from)
	}
	var durMs int64
	if state.CurrentStartedAt != nil {
		durMs = at.Sub(*state.CurrentStartedAt).Milliseconds()
		if durMs < 0 {
			durMs = 0
		}
	}
	startedAt := at
	endedAt := at
	state.History = append(state.History, StageStateItem{
		Name:       from,
		StartedAt:  ptrTime(derefTime(state.CurrentStartedAt)),
		EndedAt:    &endedAt,
		DurationMs: durMs,
		Status:     stageHistoryStatusCompleted,
	})
	state.Current = to
	state.CurrentStartedAt = &startedAt
	// ADR-117 §Production-ready follow-on, C1 — cap stage history
	// at MaxStageHistory entries (FIFO). Mirrors pgstore.go. The
	// trim lives here at the read-modify-write site so future
	// contributors can't widen the field without seeing the cap.
	// `state.Current` is never trimmed — only the historical
	// archive.
	if len(state.History) > MaxStageHistory {
		state.History = state.History[len(state.History)-MaxStageHistory:]
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return Deployment{}, fmt.Errorf("AppendDeploymentStage: encode stage_state for %s: %w", id, err)
	}
	d.StageState = encoded
	m.deployments[id] = d
	return d, nil
}

// MarkDeploymentStageFailed — memstore mirror of
// PgStore.MarkDeploymentStageFailed. See the docblock there for
// the contract.
func (m *MemStore) MarkDeploymentStageFailed(_ context.Context, id string, at time.Time, reason string) (Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deployments[id]
	if !ok {
		return Deployment{}, ErrNotFound
	}
	var state StageState
	if len(d.StageState) > 0 {
		if err := json.Unmarshal(d.StageState, &state); err != nil {
			return Deployment{}, fmt.Errorf("MarkDeploymentStageFailed: decode stage_state for %s: %w", id, err)
		}
	}
	if state.Current == "" {
		return Deployment{}, ErrNotFound
	}
	var durMs int64
	if state.CurrentStartedAt != nil {
		durMs = at.Sub(*state.CurrentStartedAt).Milliseconds()
		if durMs < 0 {
			durMs = 0
		}
	}
	endedAt := at
	state.History = append(state.History, StageStateItem{
		Name:       state.Current,
		StartedAt:  ptrTime(derefTime(state.CurrentStartedAt)),
		EndedAt:    &endedAt,
		DurationMs: durMs,
		Status:     stageHistoryStatusFailed,
		Reason:     reason,
	})
	state.Current = ""
	state.CurrentStartedAt = nil
	encoded, err := json.Marshal(state)
	if err != nil {
		return Deployment{}, fmt.Errorf("MarkDeploymentStageFailed: encode stage_state for %s: %w", id, err)
	}
	d.StageState = encoded
	m.deployments[id] = d
	return d, nil
}

// CloseDeploymentStage — memstore mirror of PgStore. See
// pkg/state/store.go::CloseDeploymentStage for the docblock.
func (m *MemStore) CloseDeploymentStage(_ context.Context, id string, name StageName, at time.Time) (Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deployments[id]
	if !ok {
		return Deployment{}, ErrNotFound
	}
	var state StageState
	if len(d.StageState) > 0 {
		if err := json.Unmarshal(d.StageState, &state); err != nil {
			return Deployment{}, fmt.Errorf("CloseDeploymentStage: decode stage_state for %s: %w", id, err)
		}
	}
	if state.Current == "" || state.Current != name {
		return Deployment{}, ErrNotFound
	}
	var durMs int64
	if state.CurrentStartedAt != nil {
		durMs = at.Sub(*state.CurrentStartedAt).Milliseconds()
		if durMs < 0 {
			durMs = 0
		}
	}
	endedAt := at
	state.History = append(state.History, StageStateItem{
		Name:       state.Current,
		StartedAt:  ptrTime(derefTime(state.CurrentStartedAt)),
		EndedAt:    &endedAt,
		DurationMs: durMs,
		Status:     stageHistoryStatusCompleted,
	})
	state.Current = ""
	state.CurrentStartedAt = nil
	encoded, err := json.Marshal(state)
	if err != nil {
		return Deployment{}, fmt.Errorf("CloseDeploymentStage: encode stage_state for %s: %w", id, err)
	}
	d.StageState = encoded
	m.deployments[id] = d
	return d, nil
}

// RetryDeploymentFromStage (ADR-117 §Production-ready follow-on,
// C2) memstore mirror of PgStore.RetryDeploymentFromStage. See
// Store.RetryDeploymentFromStage docblock for the wire contract.
//
// Mirrors pgstore: validate against pkg/state.AllStageNames
// (ErrInvalidArgument on unknown), copy every input primitive
// from the failed row, seed the new row's stage_state to
// `{current: fromStage, current_started_at: NULL, history: []}`.
// The fresh id is allocated by the existing newID() helper.
func (m *MemStore) RetryDeploymentFromStage(_ context.Context, failedID string, fromStage StageName) (Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !stageNameClosedSet[fromStage] {
		return Deployment{}, ErrInvalidArgument
	}
	src, ok := m.deployments[failedID]
	if !ok {
		return Deployment{}, ErrNotFound
	}
	// Build a new row. The Status field stays DeployPending so
	// imaged's transition chokepoint picks it up the same way as a
	// fresh CLI-driven deploy. The id is fresh; every input
	// primitive from the source row carries over so the new
	// attempt is byte-equivalent to re-running the same pipeline.
	//
	// Code-review finding #3: actor attribution columns
	// (DeployedVia / DeployedByUserID / DeployedFromIP /
	// PusherLogin) must also carry over — they describe WHO
	// triggered the original deploy, and the SOC 2 / GDPR audit
	// trail ("who deployed v3 at 14:32?") is keyed on these
	// columns. A retry that strips them produces a row whose
	// deployer chips are blank, breaking audit-trail queries that
	// walk from the failed row back to the operator. The columns
	// here describe the *retry's* triggering actor in the new
	// row's terms — copying the source primitives reflects
	// "this row was created from the same intent as that row",
	// which is the same posture that the input-primitive copy
	// above takes for non-actor fields.
	newDep := Deployment{
		ID:                    newID(),
		AppID:                 src.AppID,
		ImageDigest:           src.ImageDigest,
		Kind:                  src.Kind,
		SourcePath:            src.SourcePath,
		SourceBytes:           src.SourceBytes,
		Handler:               src.Handler,
		SourceURL:             src.SourceURL,
		CommitSHA:             src.CommitSHA,
		OverrideEntrypoint:    src.OverrideEntrypoint,
		OverrideCmd:           src.OverrideCmd,
		OverrideEnv:           src.OverrideEnv,
		OverrideEnvSecrets:    src.OverrideEnvSecrets,
		OverridePort:          src.OverridePort,
		OverrideHealthcheck:   src.OverrideHealthcheck,
		OverrideLivenessProbe: src.OverrideLivenessProbe,
		Sidecars:              src.Sidecars,
		MinInstances:          src.MinInstances,
		TrafficPercent:        src.TrafficPercent,
		Scope:                 src.Scope,
		DeployedVia:           src.DeployedVia,
		DeployedByUserID:      src.DeployedByUserID,
		DeployedFromIP:        src.DeployedFromIP,
		PusherLogin:           src.PusherLogin,
		Status:                DeployPending,
	}
	// Seed stage_state: the new row starts at fromStage with an
	// empty history. imaged's transitionWithStage will append
	// the first row (fromStage → next) the same way it does on
	// a CLI-driven fresh deploy.
	seed, err := json.Marshal(StageState{
		Current:          fromStage,
		CurrentStartedAt: nil,
		History:          []StageStateItem{},
	})
	if err != nil {
		return Deployment{}, fmt.Errorf("RetryDeploymentFromStage: encode stage_state seed: %w", err)
	}
	newDep.StageState = seed
	newDep.CreatedAt = time.Now()
	m.deployments[newDep.ID] = newDep
	return newDep, nil
}

// StampFirstWake mirrors PgStore.StampFirstWake for the in-memory
// store used by unit tests (cmd/apid/handlers_*_test.go) and
// cmd/e2e. Idempotent: the coalesce on the PG side is mirrored by
// the explicit nil-check here. windowMinutes defaults to 5 when
// non-positive.
//
// Migration 00297 / Mega-C PR-2 / issue #961 leaf 8.
func (m *MemStore) StampFirstWake(_ context.Context, deploymentID string, windowMinutes int) (Deployment, error) {
	if windowMinutes <= 0 {
		windowMinutes = 5
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deployments[deploymentID]
	if !ok {
		return Deployment{}, ErrNotFound
	}
	now := time.Now().UTC()
	if d.FirstWakeAt == nil {
		d.FirstWakeAt = &now
	}
	if d.First5xxWindowEndsAt == nil {
		end := now.Add(time.Duration(windowMinutes) * time.Minute)
		d.First5xxWindowEndsAt = &end
	}
	m.deployments[deploymentID] = d
	return d, nil
}

// BumpFirst5xxCount mirrors PgStore.BumpFirst5xxCount. The mutex
// serializes increments so concurrent calls are still atomic — same
// post-condition as the PG UPDATE ... RETURNING.
//
// Migration 00297 / Mega-C PR-2 / issue #961 leaf 8.
func (m *MemStore) BumpFirst5xxCount(_ context.Context, deploymentID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deployments[deploymentID]
	if !ok {
		return 0, ErrNotFound
	}
	d.First5xxCount++
	m.deployments[deploymentID] = d
	return d.First5xxCount, nil
}

// MarkAutoRollback mirrors PgStore.MarkAutoRollback. Idempotent on
// the (id, reason) pair: re-stamping with the same reason is a no-op
// because the field is already non-nil. reason must be non-empty
// (the closed-set check happens at the PG layer; here we just guard
// against a typo before the caller can poison the in-memory state).
//
// Migration 00297 / Mega-C PR-2 / issue #961 leaf 8.
func (m *MemStore) MarkAutoRollback(_ context.Context, deploymentID, reason string, when time.Time) (Deployment, error) {
	if reason == "" {
		return Deployment{}, fmt.Errorf("pkgstate: MarkAutoRollback reason required")
	}
	if when.IsZero() {
		when = time.Now().UTC()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deployments[deploymentID]
	if !ok {
		return Deployment{}, ErrNotFound
	}
	if d.LastAutoRollbackReason == "" {
		d.LastAutoRollbackAt = &when
		d.LastAutoRollbackReason = reason
		m.deployments[deploymentID] = d
	}
	return d, nil
}

// AutoRollbackDeploymentsTx mirrors PgStore.AutoRollbackDeploymentsTx
// for the in-memory store. The mutex plays the role of the PG
// transaction. Returns the new live deployment ID, or "" if no
// superseded deploy exists (the rollback is a no-op). Returns
// ErrNotFound when the current deployment row does not exist or is
// not live.
//
// Migration 00297 / Mega-C PR-2 / issue #961 leaf 8.
func (m *MemStore) AutoRollbackDeploymentsTx(_ context.Context, appID, currentDeploymentID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.deployments[currentDeploymentID]
	if !ok || cur.AppID != appID || cur.Status != DeployLive {
		return "", ErrNotFound
	}
	// Find the latest superseded deploy on this app.
	var targetID string
	var latestCreated time.Time
	for id, d := range m.deployments {
		if id == currentDeploymentID {
			continue
		}
		if d.AppID != appID || d.Status != DeploySuperseded {
			continue
		}
		if latestCreated.IsZero() || d.CreatedAt.After(latestCreated) {
			targetID = id
			latestCreated = d.CreatedAt
		}
	}
	if targetID == "" {
		// No rollback target — succeed as a no-op (mirrors PG path).
		return "", nil
	}
	cur.Status = DeploySuperseded
	target := m.deployments[targetID]
	target.Status = DeployLive
	now := time.Now().UTC()
	if cur.LastAutoRollbackAt == nil {
		cur.LastAutoRollbackAt = &now
	}
	if cur.LastAutoRollbackReason == "" {
		cur.LastAutoRollbackReason = "threshold_exceeded"
	}
	m.deployments[currentDeploymentID] = cur
	m.deployments[targetID] = target
	return targetID, nil
}

func (m *MemStore) SetDeploymentRootfs(_ context.Context, id, path, key string, bytes int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deployments[id]
	if !ok {
		return ErrNotFound
	}
	// Issue #96 / ADR-025 axis 2 (PR #116): mirror PgStore — both
	// rootfs_path and rootfs_key are stamped on the same mutation so
	// the in-memory store tracks Postgres' column-pair contract.
	d.RootfsPath = path
	d.RootfsKey = key
	d.RootfsBytes = bytes
	m.deployments[id] = d
	return nil
}

// UpsertDeploymentScanResult mirrors PgStore.UpsertDeploymentScanResult
// (issue #464 / ADR-055 / PR-3). Stamps the per-deploy grype scan on
// the in-memory deployments row. The Deployment struct's scan fields
// are added in this PR — PR-1 only added them to the sqlc-generated
// model and the DB, so the in-memory mirror needed its own
// counterpart.
func (m *MemStore) UpsertDeploymentScanResult(_ context.Context, id string, scanResult []byte, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deployments[id]
	if !ok {
		return ErrNotFound
	}
	d.ScanResult = append([]byte(nil), scanResult...) // defensive copy
	d.ScanStatus = status
	d.ScannedAt = time.Now()
	m.deployments[id] = d
	return nil
}

// UpsertDeploymentSecretFindings mirrors
// PgStore.UpsertDeploymentSecretFindings (migrations/00221,
// secret-scan v2). Stamps the per-deploy secret-scan audit row
// (findings + status + scannedAt) on the in-memory deployments
// row. The Deployment struct's SecretFindings + SecretScannedAt
// fields were added in this PR — the in-memory mirror needed its
// own counterpart.
func (m *MemStore) UpsertDeploymentSecretFindings(_ context.Context, id string, findings []byte, status string, scannedAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deployments[id]
	if !ok {
		return ErrNotFound
	}
	d.SecretFindings = append([]byte(nil), findings...) // defensive copy
	d.ScanStatus = status
	d.SecretScannedAt = &scannedAt
	m.deployments[id] = d
	return nil
}

// RecordRestart (issue #586 / ADR-129 / cluster C commit 12) is
// the in-memory mirror of PgStore.RecordRestart: bumps the
// deployment's LivenessRestartCount by 1. Mirrors
// UpsertDeploymentSecretFindings' IDOR contract (ErrNotFound on
// missing row). The in-memory store is used by unit tests and
// the dev-mode bootstrap — production runs against PgStore.
func (m *MemStore) RecordRestart(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deployments[id]
	if !ok {
		return ErrNotFound
	}
	d.LivenessRestartCount++
	m.deployments[id] = d
	return nil
}

// InsertStatusIncident (issue #599 / ADR-130 / cluster D commit 14)
// is the in-memory mirror of PgStore.InsertStatusIncident.
// Closed-set vocabulary is enforced via switch rather than CHECK
// (the in-memory store is dev-mode / unit-test only; production
// runs against PgStore).
func (m *MemStore) InsertStatusIncident(_ context.Context, component, severity, message string) (StatusIncident, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch component {
	case StatusIncidentComponentApid, StatusIncidentComponentSchedd,
		StatusIncidentComponentVmmd, StatusIncidentComponentGatewayd,
		StatusIncidentComponentMeterd, StatusIncidentComponentImaged,
		StatusIncidentComponentBuilderd, StatusIncidentComponentFaasControlPlane:
	default:
		return StatusIncident{}, ErrNotFound // closed-set enforcement
	}
	switch severity {
	case StatusIncidentSeverityDegraded, StatusIncidentSeverityPartialOutage,
		StatusIncidentSeverityFullOutage, StatusIncidentSeverityMaintenance:
	default:
		return StatusIncident{}, ErrNotFound
	}
	if len(message) > 1024 {
		return StatusIncident{}, ErrNotFound
	}
	inc := StatusIncident{
		ID:        int64(len(m.statusIncidents) + 1),
		Component: component,
		Severity:  severity,
		Message:   message,
		PostedAt:  time.Now(),
	}
	m.statusIncidents = append(m.statusIncidents, inc)
	return inc, nil
}

// ResolveStatusIncident (issue #599 / ADR-130) — in-memory mirror.
// Idempotent on already-resolved rows (mirrors PgStore).
func (m *MemStore) ResolveStatusIncident(_ context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.statusIncidents {
		if m.statusIncidents[i].ID == id {
			if m.statusIncidents[i].ResolvedAt == nil {
				now := time.Now()
				m.statusIncidents[i].ResolvedAt = &now
			}
			return nil
		}
	}
	return ErrNotFound
}

// ListOpenStatusIncidents (issue #599 / ADR-130) — in-memory mirror.
// Returns the open subset (resolved_at == nil), most-recent first.
func (m *MemStore) ListOpenStatusIncidents(_ context.Context) ([]StatusIncident, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []StatusIncident
	for i := len(m.statusIncidents) - 1; i >= 0; i-- {
		if m.statusIncidents[i].ResolvedAt == nil {
			out = append(out, m.statusIncidents[i])
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// openAPIDocRow is the in-memory row mirror of deployment_openapi_docs
// (migrations/00330). The struct is unexported; the public surface is
// the four methods below — handler tests reach them through the
// Store interface.
// ---------------------------------------------------------------------------
type openAPIDocRow struct {
	DeploymentID string
	AccountID    string
	AppID        string
	Doc          []byte
	Source       string
	ByteSize     int
	DocSHA256    []byte
	Truncated    bool
	CapturedAt   time.Time
	UpdatedAt    time.Time
}

// GetDeploymentOpenAPIDoc mirrors pgstore.GetDeploymentOpenAPIDoc.
// Returns ErrNotFound when the row is missing OR when the caller's
// accountID does not match — the IDOR floor is enforced at the
// method boundary so a future caller can't probe by deploymentID
// alone. The doc body is defensively copied on the way out so a
// caller mutating the slice can't corrupt the map's internal copy.
func (m *MemStore) GetDeploymentOpenAPIDoc(_ context.Context, deploymentID, accountID string) ([]byte, OpenAPIDocMeta, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.openAPIDocs[deploymentID]
	if !ok || row.AccountID != accountID {
		return nil, OpenAPIDocMeta{}, ErrNotFound
	}
	return append([]byte(nil), row.Doc...), OpenAPIDocMeta{
		DeploymentID: row.DeploymentID,
		AccountID:    row.AccountID,
		AppID:        row.AppID,
		Source:       row.Source,
		ByteSize:     row.ByteSize,
		DocSHA256:    append([]byte(nil), row.DocSHA256...),
		Truncated:    row.Truncated,
		CapturedAt:   row.CapturedAt,
		UpdatedAt:    row.UpdatedAt,
	}, nil
}

// UpsertDeploymentOpenAPIDoc mirrors pgstore. The deployment row
// must exist (the FK CASCADE in migration 00330 makes this
// unreachable in practice, but the explicit check lets a misuse
// at the call site fail closed). Idempotent: a re-delivered
// cold-boot event overwrites the same row, not create a second.
// source is the closed enum 'cold_boot' or 'manual_upload'; the
// caller is responsible for pre-validation (the apid jsonschema
// check is upstream).
func (m *MemStore) UpsertDeploymentOpenAPIDoc(_ context.Context, deploymentID, accountID, appID string, doc []byte, source string, truncated bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.deployments[deploymentID]; !ok {
		return ErrNotFound
	}
	now := time.Now()
	docCopy := append([]byte(nil), doc...)
	sum := sha256.Sum256(docCopy)
	row := openAPIDocRow{
		DeploymentID: deploymentID,
		AccountID:    accountID,
		AppID:        appID,
		Doc:          docCopy,
		Source:       source,
		ByteSize:     len(docCopy),
		DocSHA256:    sum[:],
		Truncated:    truncated,
	}
	if existing, ok := m.openAPIDocs[deploymentID]; ok {
		// Idempotent overwrite: keep the original captured_at so
		// "first-capture" semantics survive a re-delivered cold-boot
		// event. updated_at is bumped for the audit trail. Preserve
		// the original AccountID + AppID too — mirrors pgstore's
		// ON CONFLICT DO UPDATE clause, which intentionally omits
		// those columns so a cold-boot re-delivery can't flip a
		// row's tenant binding or re-parent it to a different app
		// (which would be a §11 IDOR hole). MemStore would otherwise
		// silently "correct" the row on every overwrite and drift
		// away from PG in any test asserting equality.
		row.AccountID = existing.AccountID
		row.AppID = existing.AppID
		row.CapturedAt = existing.CapturedAt
		row.UpdatedAt = now
	} else {
		row.CapturedAt = now
		row.UpdatedAt = now
	}
	m.openAPIDocs[deploymentID] = row
	return nil
}

// DeleteDeploymentOpenAPIDoc mirrors pgstore. ErrNotFound when no
// row OR the caller's accountID does not match — same IDOR floor.
// The apid caller treats ErrNotFound as "already deleted" so a
// retry is a no-op.
func (m *MemStore) DeleteDeploymentOpenAPIDoc(_ context.Context, deploymentID, accountID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.openAPIDocs[deploymentID]
	if !ok || row.AccountID != accountID {
		return ErrNotFound
	}
	delete(m.openAPIDocs, deploymentID)
	return nil
}

// CountOpenAPIDocsByAccount returns the number of doc rows the
// account owns. Drives the per-account quota gate. The count is
// a single pass over the map — the in-memory store holds the
// entire dataset under m.mu so an O(N) scan is fine.
func (m *MemStore) CountOpenAPIDocsByAccount(_ context.Context, accountID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, r := range m.openAPIDocs {
		if r.AccountID == accountID {
			n++
		}
	}
	return n, nil
}

// ---------------------------------------------------------------------------
// appOpenAPIImportRow is the in-memory row mirror of app_openapi_docs
// (migrations/00416). The struct is unexported; the public surface
// is the four methods below — handler tests reach them through the
// Store interface.
// ---------------------------------------------------------------------------
type appOpenAPIImportRow struct {
	AppID          string
	AccountID      string
	Doc            []byte
	Source         string
	OpenAPIVersion string
	EndpointCount  int
	ByteSize       int
	DocSHA256      []byte
	CapturedAt     time.Time
	UpdatedAt      time.Time
}

// GetAppOpenAPIDoc mirrors pgstore.GetAppOpenAPIDoc. Returns
// ErrNotFound when the row is missing OR when the caller's
// accountID does not match — the IDOR floor is enforced at the
// method boundary so a future caller can't probe by appID alone.
// The doc body is defensively copied on the way out so a caller
// mutating the slice can't corrupt the map's internal copy.
func (m *MemStore) GetAppOpenAPIDoc(_ context.Context, appID, accountID string) ([]byte, AppOpenAPIDocMeta, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.openAPIImports[appID]
	if !ok || row.AccountID != accountID {
		return nil, AppOpenAPIDocMeta{}, ErrNotFound
	}
	return append([]byte(nil), row.Doc...), AppOpenAPIDocMeta{
		AppID:          row.AppID,
		AccountID:      row.AccountID,
		Source:         row.Source,
		OpenAPIVersion: row.OpenAPIVersion,
		EndpointCount:  row.EndpointCount,
		ByteSize:       row.ByteSize,
		DocSHA256:      append([]byte(nil), row.DocSHA256...),
		CapturedAt:     row.CapturedAt,
		UpdatedAt:      row.UpdatedAt,
	}, nil
}

// UpsertAppOpenAPIDoc mirrors pgstore. The app row must exist AND
// belong to the caller's account (defence-in-depth — the apid
// loadApp boundary already gates this, but the store layer must
// enforce the IDOR floor so a future caller that bypasses the
// handler — admin tool, test harness, internal API — cannot
// write a row into a foreign tenant's app_id and then read it
// back via GetAppOpenAPIDoc). FK CASCADE in migration 00416 makes
// the parent-existence check unreachable in practice but the
// explicit check keeps the error surface predictable. Idempotent:
// a re-delivered import overwrites the same row, not creates a
// second. openapiVersion is one of ValidOpenAPIVersions (closed
// enum); the caller is responsible for pre-validation (the apid
// openapiimport.ValidateImport check is upstream).
func (m *MemStore) UpsertAppOpenAPIDoc(_ context.Context, appID, accountID string, doc []byte, endpointCount int, openapiVersion string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok || app.AccountID != accountID {
		return ErrNotFound
	}
	now := time.Now()
	docCopy := append([]byte(nil), doc...)
	sum := sha256.Sum256(docCopy)
	row := appOpenAPIImportRow{
		AppID:          appID,
		AccountID:      accountID,
		Doc:            docCopy,
		Source:         OpenAPIImportSourceManualImport,
		OpenAPIVersion: openapiVersion,
		EndpointCount:  endpointCount,
		ByteSize:       len(docCopy),
		DocSHA256:      sum[:],
	}
	if existing, ok := m.openAPIImports[appID]; ok {
		// Idempotent overwrite: keep the original captured_at so
		// "first-imported" semantics survive a re-delivered import
		// event. updated_at is bumped for the audit trail. Preserve
		// the original AccountID too — this matches pgstore's
		// `ON CONFLICT (app_id) DO UPDATE` clause, which lists
		// doc/doc_sha256/byte_size/endpoint_count/source/openapi_version/
		// captured_at/updated_at but intentionally omits account_id
		// so the original row's tenant binding survives a re-import.
		// Mirroring that omission here keeps MemStore and PgStore
		// byte-identical for any test that asserts equality, and
		// prevents an IDOR drift where a future caller passing a
		// stale accountID could silently flip the row's tenant.
		row.AccountID = existing.AccountID
		row.CapturedAt = existing.CapturedAt
		row.UpdatedAt = now
	} else {
		row.CapturedAt = now
		row.UpdatedAt = now
	}
	m.openAPIImports[appID] = row
	return nil
}

// DeleteAppOpenAPIDoc mirrors pgstore. ErrNotFound when no row
// OR the caller's accountID does not match — same IDOR floor.
// The apid caller treats ErrNotFound as "already deleted" so a
// retry is a no-op (idempotent 204).
func (m *MemStore) DeleteAppOpenAPIDoc(_ context.Context, appID, accountID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.openAPIImports[appID]
	if !ok || row.AccountID != accountID {
		return ErrNotFound
	}
	delete(m.openAPIImports, appID)
	return nil
}

// CountOpenAPIImportsByAccount returns the number of import rows
// the account owns. Drives the per-account quota gate
// (api.Plan.OpenAPIImportsPerAccount). The count is a single pass
// over the map — the in-memory store holds the entire dataset
// under m.mu so an O(N) scan is fine.
func (m *MemStore) CountOpenAPIImportsByAccount(_ context.Context, accountID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, r := range m.openAPIImports {
		if r.AccountID == accountID {
			n++
		}
	}
	return n, nil
}

// UpsertAppOpenAPIDocIfUnderQuota mirrors PgStore's transactional
// count+lock+upsert. The MemStore is single-threaded under m.mu
// so the atomicity is implicit (the PgStore tx's purpose is to
// serialise concurrent imports; MemStore achieves the same via
// the mutex). The Plan.Max=0 path is fail-closed.
func (m *MemStore) UpsertAppOpenAPIDocIfUnderQuota(_ context.Context, appID, accountID string, doc []byte, endpointCount int, openapiVersion string, planMax int) error {
	if planMax <= 0 {
		return &QuotaError{Kind: QuotaErrorKindOpenAPIImports, Limit: planMax, Observed: 0, NotAllowed: true}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok || app.AccountID != accountID {
		return ErrNotFound
	}
	if _, ok := m.accounts[accountID]; !ok {
		return ErrNotFound
	}
	observed := 0
	for _, r := range m.openAPIImports {
		if r.AccountID == accountID {
			observed++
		}
	}
	if observed >= planMax {
		return &QuotaError{Kind: QuotaErrorKindOpenAPIImports, Limit: planMax, Observed: observed}
	}
	now := time.Now()
	docCopy := append([]byte(nil), doc...)
	sum := sha256.Sum256(docCopy)
	row := appOpenAPIImportRow{
		AppID:          appID,
		AccountID:      accountID,
		Doc:            docCopy,
		Source:         OpenAPIImportSourceManualImport,
		OpenAPIVersion: openapiVersion,
		EndpointCount:  endpointCount,
		ByteSize:       len(docCopy),
		DocSHA256:      sum[:],
	}
	if existing, ok := m.openAPIImports[appID]; ok {
		// Same idempotent-overwrite contract as UpsertAppOpenAPIDoc
		// — see the rationale there. The original AccountID +
		// CapturedAt survive a re-import; UpdatedAt bumps.
		row.AccountID = existing.AccountID
		row.CapturedAt = existing.CapturedAt
		row.UpdatedAt = now
	} else {
		row.CapturedAt = now
		row.UpdatedAt = now
	}
	m.openAPIImports[appID] = row
	return nil
}

// SetDeploymentSidecarLayer mirrors PgStore (issue #463 /
// ADR-069 / PR-B). Upserts on the (deployment_id, sidecar_name)
// pair — same idempotency contract as the schema CHECK + ON
// CONFLICT DO UPDATE; the in-memory map key (deploymentID +
// "\x00" + sidecarName) gives the same uniqueness. Defers to
// SetDeploymentRootfs's "deployment row must exist" check so a
// caller can't strand rows against a missing deployment.
func (m *MemStore) SetDeploymentSidecarLayer(_ context.Context, l DeploymentSidecarLayer) (DeploymentSidecarLayer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.deployments[l.DeploymentID]; !ok {
		return DeploymentSidecarLayer{}, ErrNotFound
	}
	key := l.DeploymentID + "\x00" + l.SidecarName
	now := time.Now()
	if existing, ok := m.deploymentSidecarLayers[key]; ok {
		existing.StorageKey = l.StorageKey
		existing.Bytes = l.Bytes
		existing.ContentDigest = l.ContentDigest
		existing.UpdatedAt = now
		m.deploymentSidecarLayers[key] = existing
		return existing, nil
	}
	l.CreatedAt = now
	l.UpdatedAt = now
	m.deploymentSidecarLayers[key] = l
	return l, nil
}

// ListDeploymentSidecarLayers mirrors PgStore — returns the
// full sidecar set ordered by sidecar_name ASC. Empty slice
// (not nil) when no rows; ErrNotFound only if the deployment
// itself is missing.
func (m *MemStore) ListDeploymentSidecarLayers(_ context.Context, deploymentID string) ([]DeploymentSidecarLayer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.deployments[deploymentID]; !ok {
		return nil, ErrNotFound
	}
	out := make([]DeploymentSidecarLayer, 0, len(m.deploymentSidecarLayers))
	for _, l := range m.deploymentSidecarLayers {
		if l.DeploymentID != deploymentID {
			continue
		}
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SidecarName < out[j].SidecarName })
	return out, nil
}

// SetDeploymentSourceURL mirrors the pgstore / migrations/00047 column pair
// (Tier 3 / issue #197 B3.10). Empty strings are accepted (an image:
// deploy with no upstream URL is the common case). commit_sha is
// length-bounded at the DB layer (deployments_commit_sha_len_chk); the
// memstore enforces the same 64-char cap to keep behaviour aligned
// with PgStore (otherwise unit tests would let through values the DB
// would reject, hiding a bug class).
func (m *MemStore) SetDeploymentSourceURL(_ context.Context, id, sourceURL, commitSHA string) error {
	if commitSHA != "" && len(commitSHA) > 64 {
		return fmt.Errorf("state: commit_sha length %d exceeds 64", len(commitSHA))
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deployments[id]
	if !ok {
		return ErrNotFound
	}
	d.SourceURL = sourceURL
	d.CommitSHA = commitSHA
	m.deployments[id] = d
	return nil
}

// SetDeploymentFailed mirrors PgStore.SetDeploymentFailed (ADR-021):
// status pinned to 'failed'; error_code is the RFC 7807 code lifted
// from pkg/api.SentinelToCode; error keeps the free-text message.
// Returns the refreshed row.
func (m *MemStore) SetDeploymentFailed(_ context.Context, id, code, message string) (Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deployments[id]
	if !ok {
		return Deployment{}, ErrNotFound
	}
	d.Status = DeployFailed
	d.Error = message
	d.ErrorCode = code
	m.deployments[id] = d
	return d, nil
}

// SetDeploymentFailedEx is the error-explanations cluster (spec §6.4
// amendment 1) extension of SetDeploymentFailed. Writes the four
// customer-facing prose fields alongside the RFC 7807 code so the
// in-process store mirrors the persistence shape introduced by
// migration 00290. The unit-test suite that exercises MemStore stays
// aligned with PgStore.
func (m *MemStore) SetDeploymentFailedEx(
	_ context.Context, id, code, message, hint, why, fix string, logs []api.LogExcerpt,
) (Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deployments[id]
	if !ok {
		return Deployment{}, ErrNotFound
	}
	d.Status = DeployFailed
	d.Error = message
	d.ErrorCode = code
	d.ErrorHint = hint
	d.ErrorWhy = why
	d.ErrorFix = fix
	d.ErrorRelevantLogs = logs
	m.deployments[id] = d
	return d, nil
}

// SetDeploymentParked stamps the per-deployment parked_reason +
// parked_at columns (issue #554 / ADR-079 follow-up). Idempotent:
// a second call on an already-parked deployment is a no-op — the
// closed-set reason and parked_at are set once. Same contract as
// PgStore.SetDeploymentParked. Returns ErrNotFound when the
// deployment id is absent.
func (m *MemStore) SetDeploymentParked(_ context.Context, id, reason string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deployments[id]
	if !ok {
		return ErrNotFound
	}
	if d.ParkedReason != "" {
		return nil // idempotent — already parked
	}
	d.ParkedReason = reason
	d.ParkedAt = &at
	m.deployments[id] = d
	return nil
}

// SetDeploymentCanaryState (issue #976 / ADR-122 / SAFE-RELEASES-R)
// is the test-seam helper that lets handler / integration tests
// hand-stamp the canary + rollout columns without driving the
// meterd orchestrator. The production path stamps these columns
// via the canary_progression tick (pkg/canary) and the
// safedeploy orchestrator (pkg/safedeploy); this method exists
// purely so the recover_rollout handler tests can pin a
// deterministic starting state. Documented as a test seam so a
// production reader knows the method is not on the writer
// hot-path — every call goes through m.mu so concurrent
// orchestrator ticks can't interleave.
func (m *MemStore) SetDeploymentCanaryState(_ context.Context, id, preset string, step, total int, stepStartedAt time.Time, rolloutState string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deployments[id]
	if !ok {
		return ErrNotFound
	}
	d.CanaryPreset = preset
	d.CanaryStep = step
	d.CanaryTotalSteps = total
	if !stepStartedAt.IsZero() {
		t := stepStartedAt
		d.CanaryStepStartedAt = &t
	}
	d.RolloutState = rolloutState
	m.deployments[id] = d
	return nil
}

// LatestParkedDeploymentForApp returns the most recently parked
// deployment for an app, or ErrNotFound if none. Single-pass scan
// under m.mu; deployment counts are O(deploy rate × app lifetime)
// bounded by spec §4.2, so a linear scan stays cheap.
func (m *MemStore) LatestParkedDeploymentForApp(_ context.Context, appID string) (Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var latest Deployment
	found := false
	for _, d := range m.deployments {
		if d.AppID != appID || d.ParkedReason == "" {
			continue
		}
		if !found || (d.ParkedAt != nil && (latest.ParkedAt == nil || d.ParkedAt.After(*latest.ParkedAt))) {
			latest = d
			found = true
		}
	}
	if !found {
		return Deployment{}, ErrNotFound
	}
	return latest, nil
}

// --- Builds -----------------------------------------------------------------

func (m *MemStore) CreateBuild(_ context.Context, deploymentID string, kind DeploymentKind, sourceBytes int64, logPath string) (Build, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.deployments[deploymentID]; !ok {
		return Build{}, fmt.Errorf("state: build for unknown deployment %q", deploymentID)
	}
	b := Build{ID: newID(), DeploymentID: deploymentID, Kind: kind, SourceBytes: sourceBytes, Status: BuildQueued, LogPath: logPath, EnqueuedAt: time.Now()}
	m.builds[b.ID] = b
	return b, nil
}

func (m *MemStore) BuildByID(_ context.Context, id string) (Build, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.builds[id]
	if !ok {
		return Build{}, ErrNotFound
	}
	return b, nil
}

func (m *MemStore) BuildByDeployment(_ context.Context, deploymentID string) (Build, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var latest Build
	found := false
	for _, b := range m.builds {
		if b.DeploymentID == deploymentID && (!found || b.StartedAt.After(latest.StartedAt)) {
			latest, found = b, true
		}
	}
	if !found {
		return Build{}, ErrNotFound
	}
	return latest, nil
}

// UpdateBuildStatus flips the build row to a new status. Mirrors
// PgStore.UpdateBuildStatus's CAS guard (issue #195 B1.4): terminal
// writes (BuildSucceeded / BuildFailed) only succeed if the row is
// still 'running'. A late-arriving markSucceeded after a reaper sweep
// that flipped the row to 'failed(timeout)' must NOT resurrect it —
// the guard makes UpdateBuildStatus a CAS primitive that returns
// ErrNotFound on a no-op match (the caller logs WARN and moves on).
//
// Non-terminal transitions (BuildRunning, BuildQueued) are NOT
// guarded — ClaimQueuedBuild and the legacy UpdateBuildStatus(
// BuildRunning, started=true) path both rely on a clean queued→
// running flip.
func (m *MemStore) UpdateBuildStatus(_ context.Context, id string, status BuildStatus, fc FailureClass, started, finished bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.builds[id]
	if !ok {
		return ErrNotFound
	}
	if (status == BuildSucceeded || status == BuildFailed) && b.Status != BuildRunning {
		return ErrNotFound
	}
	b.Status = status
	if fc != "" {
		b.FailureClass = fc
	}
	now := time.Now()
	if started {
		b.StartedAt = now
	}
	if finished {
		b.FinishedAt = now
	}
	m.builds[id] = b
	return nil
}

// CreateBuildProvenance mirrors PgStore.CreateBuildProvenance
// (ADR-038, Tier 3 / issue #197 B3.1). Idempotent: re-creating a
// row for an existing build_id overwrites the existing entry in
// place (mirrors ON CONFLICT (build_id) DO UPDATE). Empty BuildID
// is a programming error and returns an error so a unit-test path
// surfaces it instead of producing a malformed key.
//
// The build row existence is NOT checked — the populator calls
// this from a context where the build_id has just been claimed
// (succeeded), and the schema FK ensures a referential guarantee
// at the DB level. The MemStore shape trusts the caller.
func (m *MemStore) CreateBuildProvenance(_ context.Context, prov BuildProvenance) error {
	if prov.BuildID == "" {
		return errors.New("state: build_provenance BuildID empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.buildProvenance[prov.BuildID] = prov
	return nil
}

// BuildProvenanceByBuildID mirrors PgStore.BuildProvenanceByBuildID.
// Returns ErrNotFound when no row exists for the build_id — the
// apid handler renders 404 with code=build_provenance_not_found.
func (m *MemStore) BuildProvenanceByBuildID(_ context.Context, buildID string) (BuildProvenance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.buildProvenance[buildID]
	if !ok {
		return BuildProvenance{}, ErrNotFound
	}
	return p, nil
}

// UpdateBuildProvenanceSBOM mirrors PgStore.UpdateBuildProvenanceSBOM.
// Returns ErrNotFound when no row exists for the build_id — the
// imaged call site logs at WARN and continues (best-effort).
func (m *MemStore) UpdateBuildProvenanceSBOM(_ context.Context, buildID, sbomKey string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.buildProvenance[buildID]
	if !ok {
		return ErrNotFound
	}
	p.SBOMStorageKey = sbomKey
	m.buildProvenance[buildID] = p
	return nil
}

// SweepStuckRunningBuilds mirrors PgStore.SweepStuckRunningBuilds
// (issue #195 B1.4). Returns the number of rows flipped.
func (m *MemStore) SweepStuckRunningBuilds(_ context.Context, threshold time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	now := time.Now()
	for id, b := range m.builds {
		if b.Status != BuildRunning {
			continue
		}
		if b.StartedAt.IsZero() || !b.StartedAt.Before(threshold) {
			continue
		}
		b.Status = BuildFailed
		b.FailureClass = FailureTimeout
		b.FinishedAt = now
		m.builds[id] = b
		n++
	}
	return n, nil
}

// QueuedBuildsCount (operator-side observability mega-PR / Commit 7
// — P5) returns the number of builds currently in 'queued' state.
// Mirrors PgStore.QueuedBuildsCount. Per-node labeling is
// deferred (builds.target_node_id is not yet a column) so the
// gauge degrades to a fleet-total — the dashboard renders
// "X builds in the queue" without per-schedd attribution.
func (m *MemStore) QueuedBuildsCount(_ context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, b := range m.builds {
		if b.Status == BuildQueued {
			n++
		}
	}
	return n, nil
}

// SetBuildStartedAtForTest is a test-only hook that lets the reaper
// tests backdate a build's started_at without touching the public
// Create flow. Mirrors the BackdateForTest pattern on instances.
func (m *MemStore) SetBuildStartedAtForTest(id string, t time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.builds[id]
	if !ok {
		return
	}
	b.StartedAt = t
	m.builds[id] = b
}

// SetInstanceMigratedFromForTest is a test-only hook that lets
// future tests (e.g. a post-Phase-3 conflict path test) stamp
// MigratedFromNodeID on a wedged state='migrating' row. The
// A6 watchdog itself does NOT read MigratedFromNodeID (the
// column is NULL pre-Phase-3, which is exactly when the watchdog
// fires), so the current watchdog tests do not exercise this
// helper. Kept in place for symmetry with the other SetXForTest
// helpers and as a future-proofing seam.
func (m *MemStore) SetInstanceMigratedFromForTest(instanceID, nodeID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ins, ok := m.instances[instanceID]
	if !ok {
		return
	}
	copy := nodeID
	ins.MigratedFromNodeID = &copy
	m.instances[instanceID] = ins
}

// ClaimQueuedBuild atomically flips queued → running under m.mu (PR-A
// review fix). Returns ErrNotFound if the row is missing or already
// non-queued — the caller drops the build. Same contract as
// PgStore.ClaimQueuedBuild.
func (m *MemStore) ClaimQueuedBuild(_ context.Context, id string) (Build, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.builds[id]
	if !ok || b.Status != BuildQueued {
		return Build{}, ErrNotFound
	}
	b.Status = BuildRunning
	b.StartedAt = time.Now()
	m.builds[id] = b
	return b, nil
}

// ClaimNextQueuedBuild mirrors PgStore.ClaimNextQueuedBuild (PR-B). The
// MemStore mutex is the equivalent of FOR UPDATE SKIP LOCKED here:
// only one claimer exists in-process, but the shape mirrors Postgres
// 1:1 so unit tests catch logic bugs without races. Picks the earliest
// EnqueuedAt row whose status is BuildQueued, flips to BuildRunning,
// sets StartedAt = now(). Returns ErrNotFound when the queue is empty.
func (m *MemStore) ClaimNextQueuedBuild(_ context.Context) (Build, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var (
		pick     string
		earliest time.Time
		found    bool
	)
	for id, b := range m.builds {
		if b.Status != BuildQueued {
			continue
		}
		if !found || b.EnqueuedAt.Before(earliest) {
			pick = id
			earliest = b.EnqueuedAt
			found = true
		}
	}
	if !found {
		return Build{}, ErrNotFound
	}
	b := m.builds[pick]
	b.Status = BuildRunning
	b.StartedAt = time.Now()
	m.builds[pick] = b
	return b, nil
}

// ClaimNextQueuedBuildWithFairness mirrors PgStore (B2.2 issue #196).
// Same selection shape as ClaimNextQueuedBuild but filters out
// accounts whose last claim is more recent than fairnessWindow, with
// the same starvation fallback when every queued account is recent.
// Implementation note: MemStore's `builds` map does not carry
// account_id directly, so we resolve it through deployments → apps
// for each candidate (mirroring the SQL JOIN path). For small test
// fixtures (≤ a few hundred queued builds) this is fine; the test
// surface is what matters here.
func (m *MemStore) ClaimNextQueuedBuildWithFairness(_ context.Context, fairnessWindow time.Duration) (Build, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	skip := map[string]struct{}{}
	for acc, t := range m.recentClaims {
		if now.Sub(t) <= fairnessWindow {
			skip[acc] = struct{}{}
		}
	}
	// Build (id, enqueuedAt, account) for queued rows. Skip lookup
	// failures (deployment/app missing) — treat them as "no
	// account known", which means the fallback "all queued" path
	// applies (they're never in skip).
	type candidate struct {
		id         string
		enqueuedAt time.Time
		account    string
	}
	var queued []candidate
	for id, b := range m.builds {
		if b.Status != BuildQueued {
			continue
		}
		dep, ok := m.deployments[b.DeploymentID]
		if !ok {
			continue
		}
		app, ok := m.apps[dep.AppID]
		if !ok {
			continue
		}
		queued = append(queued, candidate{id: id, enqueuedAt: b.EnqueuedAt, account: app.AccountID})
	}
	if len(queued) == 0 {
		return Build{}, ErrNotFound
	}
	// First pass: pick from accounts NOT in skip.
	hasFresh := false
	for _, c := range queued {
		if _, isRecent := skip[c.account]; !isRecent {
			hasFresh = true
			break
		}
	}
	var pick string
	if hasFresh {
		var earliest time.Time
		for _, c := range queued {
			if _, isRecent := skip[c.account]; isRecent {
				continue
			}
			if pick == "" || c.enqueuedAt.Before(earliest) {
				pick = c.id
				earliest = c.enqueuedAt
			}
		}
	} else {
		// Starvation fallback: every queued account is in skip;
		// pick the earliest queued row, period.
		var earliest time.Time
		for _, c := range queued {
			if pick == "" || c.enqueuedAt.Before(earliest) {
				pick = c.id
				earliest = c.enqueuedAt
			}
		}
	}
	if pick == "" {
		return Build{}, ErrNotFound
	}
	b := m.builds[pick]
	b.Status = BuildRunning
	b.StartedAt = time.Now()
	m.builds[pick] = b
	return b, nil
}

// RecordRecentBuildClaim records a single claim for the given account
// at now(). The MemStore in-memory map is the equivalent of the
// recent_build_claims table; rows older than the next call's
// fairnessWindow are harmlessly skipped (the WHERE-equivalent is the
// `now.Sub(t) <= fairnessWindow` check inside ClaimNextQueuedBuildWithFairness).
func (m *MemStore) RecordRecentBuildClaim(_ context.Context, accountID, buildID string) error {
	if accountID == "" {
		return fmt.Errorf("state: record recent build claim: empty account_id")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recentClaims[accountID] = time.Now()
	return nil
}

// RequeueBuild resets a running build row back to queued with
// enqueued_at untouched (PR-B). Mirrors PgStore.RequeueBuild.
func (m *MemStore) RequeueBuild(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.builds[id]
	if !ok {
		return ErrNotFound
	}
	if b.Status != BuildRunning {
		return ErrNotFound
	}
	b.Status = BuildQueued
	b.StartedAt = time.Time{}
	m.builds[id] = b
	return nil
}

// --- Custom domains ---------------------------------------------------------

func (m *MemStore) CreateCustomDomain(_ context.Context, domain, appID, token string) (CustomDomain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, dup := m.domains[domain]; dup {
		return CustomDomain{}, fmt.Errorf("state: domain %q already exists", domain)
	}
	d := CustomDomain{Domain: domain, AppID: appID, ChallengeToken: token}
	m.domains[domain] = d
	return d, nil
}

func (m *MemStore) DomainByName(_ context.Context, domain string) (CustomDomain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.domains[domain]
	if !ok {
		return CustomDomain{}, ErrNotFound
	}
	return d, nil
}

func (m *MemStore) ListDomainsForApp(_ context.Context, appID string) ([]CustomDomain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []CustomDomain
	for _, d := range m.domains {
		if d.AppID == appID {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Domain < out[j].Domain })
	return out, nil
}

func (m *MemStore) ListDomainsForAccount(_ context.Context, accountID string) ([]CustomDomain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []CustomDomain
	for _, d := range m.domains {
		app, ok := m.apps[d.AppID]
		if !ok {
			continue
		}
		if app.AccountID == accountID {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Domain < out[j].Domain })
	return out, nil
}

func (m *MemStore) MarkDomainVerified(_ context.Context, domain string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.domains[domain]
	if !ok {
		return ErrNotFound
	}
	d.VerifiedAt = time.Now()
	m.domains[domain] = d
	return nil
}

func (m *MemStore) DeleteCustomDomain(_ context.Context, domain string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.domains[domain]; !ok {
		return ErrNotFound
	}
	delete(m.domains, domain)
	return nil
}

// --- domain_doctor_observations (ADR-120) ------------------------------------

func (m *MemStore) UpsertDoctorObservation(_ context.Context, obs DomainDoctorObservation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.doctorObs[obs.Domain]; ok && obs.SurfaceID == "" {
		obs.SurfaceID = existing.SurfaceID
	}
	m.doctorObs[obs.Domain] = obs
	return nil
}

func (m *MemStore) GetDoctorObservation(_ context.Context, domain string) (DomainDoctorObservation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	obs, ok := m.doctorObs[domain]
	if !ok {
		return DomainDoctorObservation{}, ErrNotFound
	}
	return obs, nil
}

func (m *MemStore) ListAllCustomDomainsForDoctor(_ context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := make(map[string]struct{})
	var out []string
	for d := range m.domains {
		seen[d] = struct{}{}
		out = append(out, d)
	}
	for _, h := range m.tenantHostnames {
		if _, ok := seen[h.Hostname]; !ok {
			seen[h.Hostname] = struct{}{}
			out = append(out, h.Hostname)
		}
	}
	return out, nil
}

// OldestDoctorObservation (ADR-120 Tier A1) walks the in-memory
// doctorObs map and returns the earliest observed_at. Returns the
// zero time.Time when no observations exist so the dns_poller can
// distinguish "cold start" from "stalled loop". Mirrors the
// MIN(observed_at) SQL path in PgStore.OldestDoctorObservation.
func (m *MemStore) OldestDoctorObservation(_ context.Context) (time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var oldest time.Time
	for _, obs := range m.doctorObs {
		if oldest.IsZero() || obs.ObservedAt.Before(oldest) {
			oldest = obs.ObservedAt
		}
	}
	return oldest, nil
}

// --- Crons ------------------------------------------------------------------

func (m *MemStore) CreateCron(_ context.Context, appID, schedule, path string, enabled bool) (Cron, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.apps[appID]; !ok {
		return Cron{}, fmt.Errorf("state: cron for unknown app %q", appID)
	}
	c := Cron{ID: newID(), AppID: appID, Schedule: schedule, Path: path, Enabled: enabled, CreatedAt: time.Now()}
	m.crons[c.ID] = c
	return c, nil
}

// CreateCronIfUnderQuota is the customer-facing variant of
// CreateCron that enforces the per-app and per-account caps. The
// MemStore uses a single process-wide mutex so the count-then-insert
// is implicitly serialised (no TOCTOU); the predicate matches the
// PgStore one so handler logic stays identical across store backends.
//
// Failure modes mirror PgStore:
//   - *CronQuotaError when either cap trips.
//   - ErrNotFound when the app row is gone or AppDeleted.
//   - ErrConflict on a future uuid collision.
func (m *MemStore) CreateCronIfUnderQuota(_ context.Context, appID, schedule, path string, enabled bool, limits api.Limits) (Cron, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok || app.Status == AppDeleted {
		return Cron{}, ErrNotFound
	}
	// 1. Per-app count. Disabled crons still count toward the cap so
	//    toggling isn't a way to bypass it.
	appCount := 0
	for _, c := range m.crons {
		if c.AppID == appID {
			appCount++
		}
	}
	if appCount >= limits.CronLimitPerApp {
		return Cron{}, &CronQuotaError{
			Scope:    CronQuotaScopeApp,
			Limit:    limits.CronLimitPerApp,
			Observed: appCount,
		}
	}
	// 2. Per-account count. Excludes deleted apps so their crons don't
	//    poison the cap.
	accountCount := 0
	for _, c := range m.crons {
		a, exists := m.apps[c.AppID]
		if !exists || a.Status == AppDeleted {
			continue
		}
		if a.AccountID == app.AccountID {
			accountCount++
		}
	}
	if accountCount >= limits.CronLimitPerAccount {
		return Cron{}, &CronQuotaError{
			Scope:    CronQuotaScopeAccount,
			Limit:    limits.CronLimitPerAccount,
			Observed: accountCount,
		}
	}
	c := Cron{
		ID:        newID(),
		AppID:     appID,
		Schedule:  schedule,
		Path:      path,
		Enabled:   enabled,
		CreatedAt: time.Now(),
	}
	m.crons[c.ID] = c
	return c, nil
}

func (m *MemStore) CronByID(_ context.Context, id string) (Cron, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.crons[id]
	if !ok {
		return Cron{}, ErrNotFound
	}
	return c, nil
}

func (m *MemStore) UpdateCron(_ context.Context, id string, schedule, path *string, enabled *bool, createdAt *time.Time) (Cron, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.crons[id]
	if !ok {
		return Cron{}, ErrNotFound
	}
	if schedule != nil {
		c.Schedule = *schedule
	}
	if path != nil {
		c.Path = *path
	}
	if enabled != nil {
		c.Enabled = *enabled
	}
	if createdAt != nil {
		c.CreatedAt = *createdAt
	}
	m.crons[id] = c
	return c, nil
}

func (m *MemStore) DeleteCron(_ context.Context, id, appID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.crons[id]
	if !ok || c.AppID != appID {
		return ErrNotFound
	}
	delete(m.crons, id)
	return nil
}

// MarkCronFired stamps the cron row's LastFiredAt field. Used by the
// schedd dispatch loop after a synthetic cron request has been
// dispatched through gatewayd-internal (spec §4.4, M7).
func (m *MemStore) MarkCronFired(_ context.Context, id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.crons[id]
	if !ok {
		return ErrNotFound
	}
	c.LastFiredAt = at
	m.crons[id] = c
	return nil
}

// Fire-now request queue (ADR-090 PR-C). In-memory mirrors the
// cron_fire_now_requests table (migrations/00193) — same shape, same
// status enum. Tests that exercise the fire-now path construct a
// MemStore with fireNowRequests pre-seeded; production wiring uses
// PgStore. claim() is FIFO-by-requested_at with the same SELECT FOR
// UPDATE SKIP LOCKED semantics (one row at a time).
//
// Concurrency: the mutex guards the map + the cursor; Insert /
// Claim / Mark* all take it for the duration of their critical
// section. The map is keyed by request id (UUID); collisions are
// impossible in practice.
func (m *MemStore) InsertFireNowRequest(_ context.Context, cronID, accountID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := uuid.NewString()
	m.fireNowRequests[id] = FireNowRequest{
		ID:          id,
		CronID:      cronID,
		AccountID:   accountID,
		RequestedAt: time.Now().UTC(),
		Status:      FireNowStatusPending,
	}
	return id, nil
}

func (m *MemStore) ClaimPendingFireNowRequest(_ context.Context) (FireNowRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var oldestID string
	var oldestAt time.Time
	for id, r := range m.fireNowRequests {
		if r.Status != FireNowStatusPending {
			continue
		}
		if oldestID == "" || r.RequestedAt.Before(oldestAt) {
			oldestID = id
			oldestAt = r.RequestedAt
		}
	}
	if oldestID == "" {
		return FireNowRequest{}, ErrFireNowRequestNotFound
	}
	r := m.fireNowRequests[oldestID]
	r.Status = FireNowStatusRunning
	m.fireNowRequests[oldestID] = r
	return r, nil
}

func (m *MemStore) MarkFireNowRequestSucceeded(_ context.Context, requestID, invocationID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.fireNowRequests[requestID]
	if !ok || r.Status != FireNowStatusRunning {
		return ErrFireNowRequestNotFound
	}
	r.Status = FireNowStatusSucceeded
	r.InvocationID = &invocationID
	now := time.Now().UTC()
	r.FinishedAt = &now
	m.fireNowRequests[requestID] = r
	return nil
}

func (m *MemStore) MarkFireNowRequestFailed(_ context.Context, requestID, errMsg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.fireNowRequests[requestID]
	if !ok || r.Status != FireNowStatusRunning {
		return ErrFireNowRequestNotFound
	}
	r.Status = FireNowStatusFailed
	if len(errMsg) > 1024 {
		errMsg = errMsg[:1024]
	}
	r.Error = &errMsg
	now := time.Now().UTC()
	r.FinishedAt = &now
	m.fireNowRequests[requestID] = r
	return nil
}

func (m *MemStore) GetFireNowRequest(_ context.Context, requestID string) (FireNowRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.fireNowRequests[requestID]
	if !ok {
		return FireNowRequest{}, ErrFireNowRequestNotFound
	}
	return r, nil
}

// Operator intent queue (PR #1099 P2 redesign / migrations/00431).
// In-memory mirror of operator_intents — same shape, same status
// enum, same FOR UPDATE SKIP LOCKED FIFO claim semantics as the
// pg path. Handler tests (cmd/apid/handlers_admin_force_test.go)
// exercise Insert / Claim / Mark* via the same Store interface
// that production apid uses; schedd-subscriber tests
// (pkg/sched/operator_intent_subscriber_test.go) exercise the
// dispatch + state-machine path against this map.
//
// Concurrency: the same mutex that guards fireNowRequests covers
// this map. Insert / Claim / Mark* all take it for the duration of
// their critical section. The map is keyed by intent id (UUID);
// collisions are impossible in practice.
func (m *MemStore) InsertOperatorIntent(
	_ context.Context,
	kind OperatorIntentKind,
	targetID string,
	accountID *string,
	actorID string,
	reason string,
	metadata json.RawMessage,
	traceID *string,
) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := uuid.NewString()
	if metadata == nil {
		metadata = json.RawMessage("{}")
	}
	if traceID != nil && !isOTelHex32(*traceID) {
		return "", fmt.Errorf("state: InsertOperatorIntent: trace_id %q must match ^[0-9a-f]{32}$", *traceID)
	}
	m.operatorIntents[id] = OperatorIntent{
		ID:          id,
		Kind:        kind,
		TargetID:    targetID,
		AccountID:   accountID,
		ActorID:     actorID,
		Reason:      reason,
		Metadata:    metadata,
		Status:      OperatorIntentPending,
		RequestedAt: time.Now().UTC(),
		TraceID:     traceID,
	}
	return id, nil
}

func (m *MemStore) ClaimPendingOperatorIntent(_ context.Context) (OperatorIntent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var oldestID string
	var oldestAt time.Time
	for id, r := range m.operatorIntents {
		if r.Status != OperatorIntentPending {
			continue
		}
		if oldestID == "" || r.RequestedAt.Before(oldestAt) {
			oldestID = id
			oldestAt = r.RequestedAt
		}
	}
	if oldestID == "" {
		return OperatorIntent{}, ErrOperatorIntentNotFound
	}
	r := m.operatorIntents[oldestID]
	r.Status = OperatorIntentRunning
	now := time.Now().UTC()
	r.StartedAt = &now
	m.operatorIntents[oldestID] = r
	return r, nil
}

func (m *MemStore) MarkOperatorIntentSucceeded(_ context.Context, id string, snapIDs []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.operatorIntents[id]
	if !ok || r.Status != OperatorIntentRunning {
		return ErrOperatorIntentNotFound
	}
	r.Status = OperatorIntentSucceeded
	r.SnapIDsMarkedStale = snapIDs
	now := time.Now().UTC()
	r.FinishedAt = &now
	m.operatorIntents[id] = r
	return nil
}

func (m *MemStore) MarkOperatorIntentFailed(_ context.Context, id, errMsg string, snapIDs []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.operatorIntents[id]
	if !ok || r.Status != OperatorIntentRunning {
		return ErrOperatorIntentNotFound
	}
	if len(errMsg) > 1024 {
		errMsg = errMsg[:1024]
	}
	r.Status = OperatorIntentFailed
	r.Error = errMsg
	// P2d R4 review fix: persist snapIDs on the failure path so
	// partial-success (snaps flipped stale but destroy errored)
	// is reflected on the operator_intent row. Mirror the pgstore
	// impl's nil→empty coercion so the test-side assertions
	// match across both backends.
	if snapIDs == nil {
		snapIDs = []string{}
	}
	r.SnapIDsMarkedStale = snapIDs
	now := time.Now().UTC()
	r.FinishedAt = &now
	m.operatorIntents[id] = r
	return nil
}

func (m *MemStore) GetOperatorIntent(_ context.Context, id string) (OperatorIntent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.operatorIntents[id]
	if !ok {
		return OperatorIntent{}, ErrOperatorIntentNotFound
	}
	return r, nil
}

// ReclaimStuckRunningOperatorIntents mirrors
// PgStore.ReclaimStuckRunningOperatorIntents: walk the map,
// flip any `running` row whose StartedAt is older than the
// threshold back to `pending` (clearing StartedAt so the
// next Claim stamps a fresh value). Returns the count.
//
// The MemStore path exists so schedd's safety tick can be
// unit-tested without spinning up Postgres. The semantics
// match the pgstore: a row that is already `pending`,
// `succeeded`, or `failed` is left alone; only rows that
// have been stuck in `running` for longer than the
// threshold are reset.
func (m *MemStore) ReclaimStuckRunningOperatorIntents(_ context.Context, threshold time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int
	for id, r := range m.operatorIntents {
		if r.Status != OperatorIntentRunning {
			continue
		}
		if r.StartedAt == nil {
			continue
		}
		if r.StartedAt.After(threshold) {
			continue
		}
		r.Status = OperatorIntentPending
		r.StartedAt = nil
		m.operatorIntents[id] = r
		n++
	}
	return n, nil
}

// OperatorIntentOutcomeMissingCounts mirrors
// PgStore.OperatorIntentOutcomeMissingCounts by walking the
// in-memory operatorIntents map. Same closed-set contract: the
// map carries every kind that has a stuck-running row; the
// handler seeds zero-count kinds from its closed-set vocabulary.
func (m *MemStore) OperatorIntentOutcomeMissingCounts(_ context.Context, threshold time.Time) (map[string]int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]int)
	for _, r := range m.operatorIntents {
		if r.Status != OperatorIntentRunning {
			continue
		}
		if r.StartedAt == nil {
			continue
		}
		if r.StartedAt.After(threshold) {
			continue
		}
		out[string(r.Kind)]++
	}
	return out, nil
}

// OperatorActionTraceCompleteness mirrors
// PgStore.OperatorActionTraceCompleteness over the in-memory
// events slice. Mirrors the production events table; audit_log
// is NOT consulted (audit_log is the post-deletion evidence
// table, not the live diagnostic surface — see
// PgStore.OperatorActionTraceCompleteness comment for the
// ADR-091 §3.7.4 two-surface split).
//
// Vacuous-truth rule: kinds with zero rows in the window are
// ABSENT from the returned map; the handler seeds them to 1.0
// per the Store interface comment. We do NOT pre-seed here
// because the in-memory store has no concept of "the closed set
// of kinds" — that's a policy the handler owns.
func (m *MemStore) OperatorActionTraceCompleteness(_ context.Context, since time.Time) (map[string]float64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	type acc struct {
		total int
		withT int
	}
	agg := make(map[string]*acc)
	for _, e := range m.events {
		if len(e.Kind) < 16 || e.Kind[:16] != "operator.action." {
			continue
		}
		if e.At.Before(since) {
			continue
		}
		if _, ok := agg[e.Kind]; !ok {
			agg[e.Kind] = &acc{}
		}
		agg[e.Kind].total++
		if e.TraceID != nil && *e.TraceID != "" {
			agg[e.Kind].withT++
		}
	}
	out := make(map[string]float64, len(agg))
	for kind, a := range agg {
		if a.total == 0 {
			continue
		}
		out[kind] = float64(a.withT) / float64(a.total)
	}
	return out, nil
}

func (m *MemStore) ListCronsForApp(_ context.Context, appID string) ([]Cron, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Cron
	for _, c := range m.crons {
		if c.AppID == appID {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (m *MemStore) ListEnabledCrons(_ context.Context) ([]Cron, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Cron
	for _, c := range m.crons {
		if c.Enabled {
			out = append(out, c)
		}
	}
	return out, nil
}

// --- Triggers (issue #757 / ADR-0NN; commit #6) -------------------------
//
// MemStore keeps an in-memory map mirroring the pgstore triggers /
// trigger_records / trigger_dead_letter tables. The triggers map is
// keyed by id (uuid); records are keyed by id; the dead-letter
// queue is keyed by record id. The handlers + schedd tests use the
// memstore rather than spinning up Postgres.
//
// The MemStore triggers implement only the Store-interface surface
// (CRUD + per-app fan-out + the per-record transition verbs the
// apid operator actions invoke). The schedd-side dispatch tick
// (#14) reads from ListEnabledTriggers + ClaimTriggerRecords; both
// are stubbed here so tests can run without a live Postgres.

func (m *MemStore) CreateTriggerIfUnderQuota(_ context.Context, appID, kind, slug string, enabled bool, _ []byte, _, _, _, payloadMaxBytes int32, brokerPoisonStrategy string, limits api.Limits) (sqlc.Trigger, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	perApp := 0
	perAccount := 0
	for _, t := range m.triggers {
		if t.AppID.String() == appID {
			perApp++
		}
	}
	if limits.TriggerLimitPerApp > 0 && perApp >= limits.TriggerLimitPerApp {
		return sqlc.Trigger{}, &TriggerQuotaError{Scope: TriggerQuotaScopeApp, Limit: limits.TriggerLimitPerApp, Observed: perApp}
	}
	for range m.triggers {
		perAccount++ // memstore has no per-account join — single-app single-account default
	}
	if limits.TriggerLimitPerAccount > 0 && perAccount >= limits.TriggerLimitPerAccount {
		return sqlc.Trigger{}, &TriggerQuotaError{Scope: TriggerQuotaScopeAccount, Limit: limits.TriggerLimitPerAccount, Observed: perAccount}
	}
	if payloadMaxBytes <= 0 {
		payloadMaxBytes = 6291456
	}
	if brokerPoisonStrategy == "" {
		brokerPoisonStrategy = "commit"
	}
	t := sqlc.Trigger{
		ID:                   pgtype.UUID{Bytes: memNewUUID(), Valid: true},
		AccountID:            pgtype.UUID{Bytes: memNewUUID(), Valid: true},
		AppID:                pgtype.UUID{Bytes: parseMemUUIDString(appID), Valid: true},
		Kind:                 kind,
		Slug:                 slug,
		Enabled:              enabled,
		Config:               []byte("{}"),
		BatchSizeMax:         64,
		BatchWindowMs:        1000,
		MaxAttempts:          5,
		PayloadMaxBytes:      payloadMaxBytes,
		BrokerPoisonStrategy: brokerPoisonStrategy,
		CreatedAt:            pgtype.Timestamptz{Time: time.Now(), Valid: true},
		UpdatedAt:            pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}
	if m.triggers == nil {
		m.triggers = map[string]sqlc.Trigger{}
	}
	m.triggers[t.ID.String()] = t
	return t, nil
}

func (m *MemStore) TriggerByID(_ context.Context, id string) (sqlc.Trigger, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.triggers[id]
	if !ok {
		return sqlc.Trigger{}, ErrNotFound
	}
	return t, nil
}

func (m *MemStore) UpdateTrigger(_ context.Context, id string, enabled *bool, _ []byte, _, _, _, payloadMaxBytes *int32, brokerPoisonStrategy *string, filterCriteria *[]byte) (sqlc.Trigger, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.triggers[id]
	if !ok {
		return sqlc.Trigger{}, ErrNotFound
	}
	if enabled != nil {
		t.Enabled = *enabled
	}
	if payloadMaxBytes != nil {
		t.PayloadMaxBytes = *payloadMaxBytes
	}
	if brokerPoisonStrategy != nil {
		t.BrokerPoisonStrategy = *brokerPoisonStrategy
	}
	if filterCriteria != nil {
		// REVIEW-FIX MED-1: nil = "leave unchanged" (mirrors
		// pgstore coalesce()); non-nil = "replace the JSONB
		// column". Memstore treats the byte slice as opaque.
		fc := *filterCriteria
		t.FilterCriteria = fc
	}
	t.UpdatedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	m.triggers[id] = t
	return t, nil
}

func (m *MemStore) DeleteTrigger(_ context.Context, id, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.triggers, id)
	return nil
}

func (m *MemStore) ListTriggersForApp(_ context.Context, appID string) ([]sqlc.Trigger, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []sqlc.Trigger
	for _, t := range m.triggers {
		if t.AppID.String() == appID {
			out = append(out, t)
		}
	}
	return out, nil
}

func (m *MemStore) ListEnabledTriggers(_ context.Context) ([]sqlc.Trigger, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []sqlc.Trigger
	for _, t := range m.triggers {
		if t.Enabled {
			out = append(out, t)
		}
	}
	return out, nil
}

func (m *MemStore) ClaimTriggerRecords(_ context.Context, triggerID string, limit int32) ([]sqlc.TriggerRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []sqlc.TriggerRecord
	for _, r := range m.records {
		if r.TriggerID.String() == triggerID && r.State == "pending" && len(out) < int(limit) {
			r.State = "claimed"
			out = append(out, r)
		}
	}
	return out, nil
}

// InsertTriggerRecord mirrors PgStore.InsertTriggerRecord for the
// in-memory store. Returns the persisted (or existing-on-duplicate)
// record id. Mirrors the ON CONFLICT (trigger_id, item_identifier)
// DO NOTHING semantics with a Map key probe; on a duplicate the
// existing row's id is returned so callers don't need a second
// lookup. Review finding #1 (PR #910).
func (m *MemStore) InsertTriggerRecord(_ context.Context, triggerID, itemIdentifier string, payload, headers, metadata []byte) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Dedup probe — mirrors ON CONFLICT DO NOTHING on the
	// (trigger_id, item_identifier) unique pair.
	for id, r := range m.records {
		if r.TriggerID.String() == triggerID && r.ItemIdentifier == itemIdentifier {
			return id, nil
		}
	}
	id := uuid.NewString()
	now := time.Now()
	if payload == nil {
		payload = []byte("{}")
	}
	if headers == nil {
		headers = []byte("{}")
	}
	if metadata == nil {
		metadata = []byte("{}")
	}
	m.records[id] = sqlc.TriggerRecord{
		ID:             pgtype.UUID{Bytes: parseMemUUIDString(id), Valid: true},
		TriggerID:      pgtype.UUID{Bytes: parseMemUUIDString(triggerID), Valid: true},
		ItemIdentifier: itemIdentifier,
		Payload:        payload,
		Headers:        headers,
		Metadata:       metadata,
		State:          "pending",
		Attempts:       0,
		NextFireAt:     pgtypeFromTime(now),
		ReceivedAt:     pgtypeFromTime(now),
	}
	return id, nil
}

func (m *MemStore) MarkTriggerRecordSucceeded(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.records[id]
	if ok {
		r.State = "succeeded"
		m.records[id] = r
	}
	return nil
}

func (m *MemStore) MarkTriggerRecordRetry(_ context.Context, id, _ string, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.records[id]
	if ok {
		r.State = "retry"
		m.records[id] = r
	}
	return nil
}

func (m *MemStore) MarkTriggerRecordDeadLetter(_ context.Context, id, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.records[id]
	if ok {
		r.State = "dead_letter"
		m.records[id] = r
	}
	return nil
}

func (m *MemStore) InsertTriggerDeadLetter(_ context.Context, _, _, _, _ string, _ []byte) error {
	return nil
}

// TriggerRecordIDByItemIdentifier (audit round 2 finding #1,
// PR #910): the MemStore's InsertTriggerRecord builds a UUID
// for the row at insert time; the bridge helper walks m.records
// to find the matching UUID. Returns "" (nil err) when no row
// matches — callers treat that as "rate-limit fired before
// insert; skip the dead_letter; record will be retried".
func (m *MemStore) TriggerRecordIDByItemIdentifier(_ context.Context, triggerID, itemIdentifier string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.records {
		if r.TriggerID.String() == triggerID && r.ItemIdentifier == itemIdentifier {
			return r.ID.String(), nil
		}
	}
	return "", nil
}

func (m *MemStore) ListTriggerDeadLetter(_ context.Context, _ string, _ int32) ([]sqlc.TriggerDeadLetter, error) {
	return nil, nil
}

func (m *MemStore) ListTriggerRecordsForTrigger(_ context.Context, triggerID string, limit int32) ([]sqlc.TriggerRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []sqlc.TriggerRecord
	for _, r := range m.records {
		if r.TriggerID.String() == triggerID && len(out) < int(limit) {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *MemStore) RetryTriggerRecordByOperator(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.records[id]
	if !ok {
		return ErrNotFound
	}
	r.State = "pending"
	r.Attempts = 0
	m.records[id] = r
	return nil
}

func (m *MemStore) DropTriggerRecordByOperator(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.records[id]; !ok {
		return ErrNotFound
	}
	delete(m.records, id)
	return nil
}

// --- Invocations (Move 1 event-shaped queue: async_invoke / queue /
//   delayed_task / cron). Schedd is the sole writer to state
//   transitions (Store Claim/Complete/Fail); apid owns the INSERT
//   path (EnqueueInvocation) and the cancel surface
//   (CancelInvocation). The MemStore mirrors the production
//   `for update skip locked` semantics by serialising every access
//   through m.mu; ListDueInvocations sorts by due_at ASC and caps
//   the returned slice at the caller's limit so the drain's batching
//   shape matches PgStore.

func (m *MemStore) EnqueueInvocation(_ context.Context, inv Invocation) (Invocation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.apps[inv.AppID]; !ok {
		return Invocation{}, fmt.Errorf("state: invocation for unknown app %q", inv.AppID)
	}
	if inv.ID == "" {
		inv.ID = newID()
	}
	if inv.State == "" {
		inv.State = InvocationPending
	}
	if inv.CreatedAt.IsZero() {
		inv.CreatedAt = time.Now()
	}
	m.invocations[inv.ID] = inv
	return inv, nil
}

func (m *MemStore) InvocationByID(_ context.Context, id string) (Invocation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inv, ok := m.invocations[id]
	if !ok {
		return Invocation{}, ErrNotFound
	}
	return inv, nil
}

// ListDueInvocations returns pending rows whose due_at <= now, ordered
// by due_at ascending. Mirrors the production SELECT … FOR UPDATE
// SKIP LOCKED + LIMIT n shape the schedd drain depends on; MemStore
// does not need explicit locking because the whole map is guarded by
// m.mu. Caller's `limit` caps the slice.
func (m *MemStore) ListDueInvocations(_ context.Context, now time.Time, limit int) ([]Invocation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Invocation
	for _, inv := range m.invocations {
		if inv.State != InvocationPending {
			continue
		}
		if inv.DueAt.After(now) {
			continue
		}
		out = append(out, inv)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].DueAt.Equal(out[j].DueAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].DueAt.Before(out[j].DueAt)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ClaimInvocation atomically transitions pending → dispatching and
// stamps lease_expires_at = now + leaseSeconds. MemStore is
// single-process so the "skip locked" guarantee is unconditional —
// if another goroutine already grabbed the row, we observe state ≠
// pending and return ErrNotFound (the schedd drain treats this as
// "claimed elsewhere", which is the intended behaviour even on PG).
// instanceID is the just-woken instance handle the drain captured;
// stored on the row so pkg/meter can join on completion.
func (m *MemStore) ClaimInvocation(_ context.Context, id, instanceID string, leaseSeconds int) (Invocation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inv, ok := m.invocations[id]
	if !ok {
		return Invocation{}, ErrNotFound
	}
	if inv.State != InvocationPending {
		return Invocation{}, ErrNotFound
	}
	now := time.Now()
	exp := now.Add(time.Duration(leaseSeconds) * time.Second)
	inv.State = InvocationDispatching
	inv.LeaseExpiresAt = &exp
	inv.InstanceID = instanceID
	inv.ReceivedAt = &now
	inv.Attempts++
	m.invocations[id] = inv
	return inv, nil
}

// RequeueExpiredInvocations returns dispatching rows whose lease has expired
// to the pending queue. It mirrors the conditional UPDATE used by PgStore;
// the scheduler calls it once per tick with a bounded limit so recovery work
// cannot starve fresh traffic.
func (m *MemStore) RequeueExpiredInvocations(_ context.Context, now time.Time, limit int) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0)
	for id, inv := range m.invocations {
		if inv.State != InvocationDispatching || inv.LeaseExpiresAt == nil || inv.LeaseExpiresAt.After(now) {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if limit > 0 && len(ids) > limit {
		ids = ids[:limit]
	}
	for _, id := range ids {
		inv := m.invocations[id]
		inv.State = InvocationPending
		inv.DueAt = now
		inv.LeaseExpiresAt = nil
		inv.InstanceID = ""
		inv.LastError = "dispatch lease expired; requeued"
		m.invocations[id] = inv
	}
	return len(ids), nil
}

// CompleteInvocation finalises a dispatching row with the optional
// result blob. State must be dispatching; anything else returns
// ErrNotFound so the drain doesn't double-complete a row that PG
// already flipped.
func (m *MemStore) CompleteInvocation(_ context.Context, id string, result json.RawMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	inv, ok := m.invocations[id]
	if !ok || inv.State != InvocationDispatching {
		return ErrNotFound
	}
	inv.State = InvocationCompleted
	if len(result) > 0 {
		inv.Result = result
	}
	now := time.Now()
	inv.CompletedAt = &now
	outcome := OutcomeSuccess
	inv.Outcome = &outcome
	m.invocations[id] = inv
	return nil
}

// FailInvocation is the durable store half of the drain's error
// pathway. retryAfter > 0 leaves the row at state=pending with
// due_at = now + retryAfter and bumps attempts (transient blip);
// retryAfter == 0 terminates the row at state=failed (e.g. invalid
// envelope). State must be pending or dispatching to avoid racing
// the happy-path Complete call (terminal states return ErrNotFound
// so the drain's redelivery is a no-op). Mirrors the PG contract
// exactly so the drain's cap re-check on pending rows works on both
// backends.
func (m *MemStore) FailInvocation(_ context.Context, id string, lastError string, retryAfter time.Duration, budget int, opts ...FailOption) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	inv, ok := m.invocations[id]
	if !ok {
		return ErrNotFound
	}
	if inv.State != InvocationPending && inv.State != InvocationDispatching {
		return ErrNotFound
	}
	failOpts := ApplyFailOptions(opts)
	inv.LastError = lastError
	if retryAfter > 0 {
		// Transient. Either re-queue (budget == 0 or attempts not yet
		// exhausted) or transition to dead_letter (issue #394 — budget
		// exhausted). Three branches:
		//
		//   budget <= 0          → legacy infinite retry; invariant of the
		//                          pre-#394 drain and the path every
		//                          non-queue caller still takes.
		//   budget > 0 and
		//   attempts <  budget  → transient re-queue; attempts is NOT
		//                          bumped here — ClaimInvocation (line
		//                          2194) already incremented it to count
		//                          this dispatch attempt.
		//   budget > 0 and
		//   attempts >= budget  → state='dead_letter', completed_at=now;
		//                          attempts is the count at the moment
		//                          of failure and is NOT bumped past the
		//                          ceiling.
		if budget > 0 && inv.Attempts >= budget {
			inv.State = InvocationDeadLetter
			now := time.Now()
			inv.CompletedAt = &now
			// issue #791 — the dead-letter arm overrides whatever the
			// caller passed, exactly as it already overrides state.
			outcome := OutcomeDeadLetter
			inv.Outcome = &outcome
		} else {
			inv.State = InvocationPending
			inv.DueAt = time.Now().Add(retryAfter)
			inv.LeaseExpiresAt = nil
			// issue #791 — the row is non-terminal again, so it carries
			// no outcome. Clearing matters on a re-queue after a prior
			// terminal classification would otherwise linger.
			inv.Outcome = nil
			// Do NOT bump attempts here. ClaimInvocation already
			// incremented it for this dispatch attempt; double-bumping
			// would make MaxQueueAttempts=10 dead-letter after 5
			// iterations instead of 10.
		}
	} else {
		// Permanent. retryAfter == 0 short-circuits to state='failed'
		// regardless of budget; budget applies only to the transient
		// retry loop.
		inv.State = InvocationFailed
		now := time.Now()
		inv.CompletedAt = &now
		// issue #791 — the caller's classification lands here;
		// ApplyFailOptions defaults it to OutcomeFailed.
		outcome := failOpts.Outcome
		inv.Outcome = &outcome
	}
	m.invocations[id] = inv
	return nil
}

// CountPendingInvocations is the index-backed count the apid cap
// check uses on every queueSend / delayedTaskCreate POST. In
// MemStore it walks the whole map (tests are small), but the
// semantic — "live" = pending ∪ dispatching — matches the PG
// partial-index predicate exactly.
func (m *MemStore) CountPendingInvocations(_ context.Context, appID string, source InvocationSource) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, inv := range m.invocations {
		if inv.AppID != appID || inv.Source != source {
			continue
		}
		if inv.State != InvocationPending && inv.State != InvocationDispatching {
			continue
		}
		n++
	}
	return n, nil
}

// CancelInvocation moves any non-terminal row to state=cancelled.
// The drain may have already flipped to dispatching; we let that
// finish and just stamp CompletedAt + Result=skip here so the row
// stays out of any future "due" scan.
func (m *MemStore) CancelInvocation(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	inv, ok := m.invocations[id]
	if !ok {
		return ErrNotFound
	}
	if inv.State == InvocationCompleted || inv.State == InvocationFailed || inv.State == InvocationCancelled {
		return nil
	}
	inv.State = InvocationCancelled
	now := time.Now()
	inv.CompletedAt = &now
	m.invocations[id] = inv
	return nil
}

// ListInvocationsForAccount is the dashboard's unified history read.
// MemStore returns rows ordered CreatedAt DESC (with ID as tie-breaker)
// for any caller. The `before` cursor is an Invocation.ID; an empty
// string means "start from the newest".
func (m *MemStore) ListInvocationsForAccount(_ context.Context, accountID string, limit int, before string) ([]Invocation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Invocation
	for _, inv := range m.invocations {
		if inv.AccountID == accountID {
			out = append(out, inv)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	// Apply the cursor: drop everything at-or-after the cursor row
	// (we want strictly older). MemStore is single-process so a linear
	// scan is fine.
	if before != "" {
		var cursorIdx = -1
		for i, inv := range out {
			if inv.ID == before {
				cursorIdx = i
				break
			}
		}
		if cursorIdx >= 0 {
			out = out[cursorIdx+1:]
		}
		// If the cursor isn't in the page (already GC'd, expired),
		// PgStore falls back to the inner SELECT; MemStore returns the
		// full page, which is the cheap-and-cheerful answer.
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ListCronRunsForCron is the per-cron run-history read (issue #791).
// Mirrors ListInvocationsForAccount's ordering and cursor semantics
// with a CronID predicate instead of an account one. MemStore is
// single-process so a linear scan is fine.
func (m *MemStore) ListCronRunsForCron(_ context.Context, cronID string, limit int, before string) ([]Invocation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Invocation
	for _, inv := range m.invocations {
		if inv.CronID != nil && *inv.CronID == cronID {
			out = append(out, inv)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if before != "" {
		cursorIdx := -1
		for i, inv := range out {
			if inv.ID == before {
				cursorIdx = i
				break
			}
		}
		if cursorIdx >= 0 {
			out = out[cursorIdx+1:]
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ListInvocationsForApp is the per-app filtered variant used by
// deleteApp's GC sweep. An empty `states` slice returns all rows for
// the app; otherwise the row's state must match one of the filter
// values. MemStore is single-process so a linear scan is fine.
func (m *MemStore) ListInvocationsForApp(_ context.Context, appID string, states ...InvocationState) ([]Invocation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	stateSet := make(map[InvocationState]struct{}, len(states))
	for _, s := range states {
		stateSet[s] = struct{}{}
	}
	var out []Invocation
	for _, inv := range m.invocations {
		if inv.AppID != appID {
			continue
		}
		if len(stateSet) > 0 {
			if _, ok := stateSet[inv.State]; !ok {
				continue
			}
		}
		out = append(out, inv)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

// QueueState (issue #394) is the read-side counter aggregator. MemStore
// walks the in-memory map under the lock and returns the three numbers
// in one pass — no transactional semantics needed because the lock
// serialises the read against writers. Equivalent to the PgStore
// triple-aggregate query (depth, in_flight, oldest_pending_at).
func (m *MemStore) QueueState(_ context.Context, appID string) (QueueStats, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var s QueueStats
	for _, inv := range m.invocations {
		if inv.AppID != appID || inv.Source != InvocationQueue {
			continue
		}
		switch inv.State {
		case InvocationPending:
			s.Depth++
			if s.OldestPendingAt.IsZero() || inv.CreatedAt.Before(s.OldestPendingAt) {
				s.OldestPendingAt = inv.CreatedAt
			}
		case InvocationDispatching:
			s.Depth++
			// In-flight = dispatching with no expired lease. The legacy
			// drain stamps lease_expires_at = now + wakeLeaseSeconds; if
			// a row's lease has slipped past that deadline the worker
			// is no longer holding it and the row should not count
			// toward InFlight (the next drain tick will re-claim it).
			if inv.LeaseExpiresAt == nil || inv.LeaseExpiresAt.After(time.Now()) {
				s.InFlight++
			}
		}
	}
	return s, nil
}

// QueuePeek (issue #394) returns the oldest pending queue messages
// for an app without acquiring a lease. Read-only; the in-memory
// iteration copies snapshots into `out` so a concurrent writer cannot
// perturb the slice the handler encodes.
//
// Cursor convention mirrors ListInvocationsForAccount: `before` is an
// Invocation.ID (uuid); empty means "start from the oldest". MemStore
// does the cursor subquery-resolves-created_at dance manually because
// it has no SQL.
//
// Cursor direction (mirrors the PG query): ORDER BY is ASC, so the
// predicate is "rows strictly *after* the anchor in the same sort
// direction" — i.e. `(created_at, id) > (anchor.created_at, anchor.id)`.
// Page 1 returns the oldest N rows; page 2 (with `before=<last id of
// page 1>`) returns the next N rows newer than that anchor. The DESC
// counterpart (QueueDeadLetter) flips the comparison to `<`.
func (m *MemStore) QueuePeek(_ context.Context, appID string, limit int, before string) ([]Invocation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	var anchor *Invocation
	if before != "" {
		a, ok := m.invocations[before]
		if !ok {
			return nil, nil // unknown cursor; treat as "start from oldest"
		}
		anchor = &a
	}
	var out []Invocation
	for _, inv := range m.invocations {
		if inv.AppID != appID || inv.Source != InvocationQueue {
			continue
		}
		if inv.State != InvocationPending {
			continue
		}
		if anchor != nil {
			if inv.CreatedAt.Before(anchor.CreatedAt) {
				continue
			}
			if inv.CreatedAt.Equal(anchor.CreatedAt) && inv.ID <= anchor.ID {
				continue
			}
		}
		out = append(out, inv)
	}
	// Oldest-first, then id ASC as the stable tie-breaker (mirrors the
	// ORDER BY clause in pkg/state/pgstore.go::QueuePeek).
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// QueueDeadLetter (issue #394) returns dead-letter rows for an app,
// newest-first (descending created_at). Same cursor semantics as
// QueuePeek but the index in PgStore is ordered DESC; here we sort
// accordingly. Pagination by `before` means "rows strictly older than
// the anchor in the DESC sort order" — i.e. `(created_at, id) <
// (anchor.created_at, anchor.id)`. The id tie-breaker mirrors the PG
// partial index `(app_id, created_at DESC)` so rows with identical
// created_at do not duplicate or skip across pages.
func (m *MemStore) QueueDeadLetter(_ context.Context, appID string, limit int, before string) ([]Invocation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	var anchor *Invocation
	if before != "" {
		a, ok := m.invocations[before]
		if !ok {
			return nil, nil
		}
		anchor = &a
	}
	var out []Invocation
	for _, inv := range m.invocations {
		if inv.AppID != appID || inv.Source != InvocationQueue {
			continue
		}
		if inv.State != InvocationDeadLetter {
			continue
		}
		if anchor != nil {
			if inv.CreatedAt.After(anchor.CreatedAt) {
				continue
			}
			if inv.CreatedAt.Equal(anchor.CreatedAt) && inv.ID >= anchor.ID {
				continue
			}
		}
		out = append(out, inv)
	}
	// Newest-first, then id DESC as the stable tie-breaker (matches
	// the partial index order on the PgStore side).
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// CountInstanceInvocationsInMinute is the meter sampler hook.
// MemStore uses a single-map walk and filters in Go; production
// hits the invocations_instance_idx via `state='dispatching'`.
// "dispatching" matches the production shape exactly (only rows
// the drain actually drove across the wake gate count toward
// `usage_minutes.requests`).
func (m *MemStore) CountInstanceInvocationsInMinute(_ context.Context, instanceID string, minute time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	end := minute.Add(time.Minute)
	n := 0
	for _, inv := range m.invocations {
		if inv.State != InvocationDispatching {
			continue
		}
		if inv.DueAt.Before(minute) || !inv.DueAt.Before(end) {
			continue
		}
		if inv.InstanceID != instanceID {
			continue
		}
		n++
	}
	return n, nil
}

// StampInstanceInvocation writes the live instance handle onto a
// dispatching row. MemStore matches the PG contract exactly: only
// rows in 'dispatching' state accept a stamp (Complete and Fail
// hold their own locks on the row; racing them would corrupt the
// state machine). Returns ErrNotFound if the row is missing or not
// dispatching.
func (m *MemStore) StampInstanceInvocation(_ context.Context, id, instanceID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	inv, ok := m.invocations[id]
	if !ok || inv.State != InvocationDispatching {
		return ErrNotFound
	}
	inv.InstanceID = instanceID
	m.invocations[id] = inv
	return nil
}

// --- Instances --------------------------------------------------------------

func (m *MemStore) CreateInstance(_ context.Context, appID, deploymentID, state string, ramMB int, nodeID, wakeID string) (Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Stamp started_at on creation for every state (commit 3, mirrors
	// the Postgres trigger in migration 00015). The MemStore previously
	// only stamped it on "running" rows, which left watchdog tests
	// fishing for NULLs on WAKING/COLD_BOOTING fixtures. Keeping that
	// late stamp behaviour would force every fixture to call
	// SetInstanceRuntime first, which makes the watchdog tests
	// describe a state-machine shape that no production code reaches.
	//
	// nodeID is the compute_node the instance lives on
	// (issue #97 / ADR-025 axis 3). MemStore does NOT enforce the
	// FK to compute_nodes(id) — the production constraint lives in
	// migrations/00024_compute_nodes. A test that passes an
	// arbitrary nodeID here will succeed; the constraint divergence
	// is intentional so unit tests can construct instance rows
	// without seeding compute_nodes first. The engine's Wake flow
	// resolves the id via ComputeNodeByName before reaching here,
	// so production callers always have a real id.
	//
	// wakeID is the per-wake-attempt correlation handle (gaps analysis
	// 2026-07-23). An empty wakeID triggers the MemStore's own default
	// — newID() — mirroring PgStore's coalesce(...gen_random_uuid()) so
	// ad-hoc test fixtures that don't thread wake_id through still get
	// a non-empty value. Production callers (schedd's Wake) supply a
	// UUIDv7 minted Go-side for time-ordered values.
	ins := Instance{
		ID:           newID(),
		AppID:        appID,
		DeploymentID: deploymentID,
		State:        state,
		RAMMB:        ramMB,
		NodeID:       nodeID,
		StartedAt:    time.Now(),
	}
	if wakeID != "" {
		ins.WakeID = wakeID
	} else {
		// Mirror PgStore's `coalesce(nullif($6, ''), gen_random_uuid())`
		// default with a real UUIDv4 here. newID() returns 32 hex
		// chars (not a hyphenated UUID), which broke uuid.Parse
		// assertions in tests exercising the wake_id contract via
		// MemStore (gaps analysis 2026-07-23 review finding #2).
		// Test fixtures that don't thread wake_id through still get
		// a non-empty, parseable value.
		ins.WakeID = uuid.NewString()
	}
	m.instances[ins.ID] = ins
	return ins, nil
}

// CreateInstanceWithMode (issue #72 / ADR-125 PR-A3) is the
// mode-aware overload that stamps the Instance.Mode field at
// creation time. The MemStore mirrors PgStore's CHECK on the
// mode column (migrations/00385): empty mode falls back to
// 'normal' so legacy callers (and test fixtures that don't yet
// thread mode through) keep bit-for-bit compatibility. Valid
// non-default values are InstanceModeNormal and InstanceModeMirror;
// the engine validates the value before reaching here so the
// MemStore is permissive (no SQLSTATE to translate — the SQL
// CHECK fires on PgStore; the MemStore's only job is to store
// what the caller asked for).
func (m *MemStore) CreateInstanceWithMode(_ context.Context, appID, deploymentID, state string, ramMB int, nodeID, wakeID, mode string) (Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	mode = strings.TrimSpace(mode)
	if mode == "" {
		mode = string(InstanceModeNormal)
	}
	ins := Instance{
		ID:           newID(),
		AppID:        appID,
		DeploymentID: deploymentID,
		State:        state,
		RAMMB:        ramMB,
		NodeID:       nodeID,
		StartedAt:    time.Now(),
		Mode:         mode,
	}
	if wakeID != "" {
		ins.WakeID = wakeID
	} else {
		ins.WakeID = uuid.NewString()
	}
	m.instances[ins.ID] = ins
	return ins, nil
}

// StampAppScaleOut (PR-C, issue #462) records the apps
// LastScaleOutAt timestamp. The MemStore mirrors the PG contract
// (PgStore.StampAppScaleOut): a single UPDATE; no row existence
// check — the stamp is best-effort and a missing row is logged by
// the caller as a non-fatal warning, not a fatal error.
func (m *MemStore) StampAppScaleOut(_ context.Context, appID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok {
		return ErrNotFound
	}
	now := time.Now()
	app.LastScaleOutAt = &now
	m.apps[appID] = app
	return nil
}

// StampAppScaleIn (PR-C, issue #462) records the apps
// LastScaleInAt timestamp. Same shape as StampAppScaleOut.
func (m *MemStore) StampAppScaleIn(_ context.Context, appID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok {
		return ErrNotFound
	}
	now := time.Now()
	app.LastScaleInAt = &now
	m.apps[appID] = app
	return nil
}

// SetLastScaleOutAt (PR-C, issue #462) overwrites the apps
// LastScaleOutAt timestamp with a caller-supplied value. Used
// only by sched_test.go to fix the cooldown-consult timestamp
// in deterministic time; the production path is StampAppScaleOut
// (now()). The helper is exported so pkg/sched tests can inject
// a fixed "stamp 1s ago" timestamp without exposing the internal
// Mutex.
func (m *MemStore) SetLastScaleOutAt(appID string, ts time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok {
		return ErrNotFound
	}
	tsCopy := ts
	app.LastScaleOutAt = &tsCopy
	m.apps[appID] = app
	return nil
}

// SetLastScaleInAt (PR-C, issue #462) mirrors SetLastScaleOutAt
// for the LastScaleInAt timestamp. Same rationale: deterministic
// reaper-cooldown tests need to fix the stamp in absolute time.
func (m *MemStore) SetLastScaleInAt(appID string, ts time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok {
		return ErrNotFound
	}
	tsCopy := ts
	app.LastScaleInAt = &tsCopy
	m.apps[appID] = app
	return nil
}

func (m *MemStore) InstanceByID(_ context.Context, id string) (Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ins, ok := m.instances[id]
	if !ok {
		return Instance{}, ErrNotFound
	}
	return ins, nil
}

// ReadActiveInstanceForWakeID mirrors PgStore.ReadActiveInstanceForWakeID
// for in-memory tests. Returns the most-recently-started instance
// row whose state is in the in-flight set AND whose wake_id matches.
// Matches the partial-unique-index predicate from migration 00350
// (multi-host safety cluster PR-5 / audit F4).
func (m *MemStore) ReadActiveInstanceForWakeID(_ context.Context, wakeID string) (Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var best *Instance
	for _, ins := range m.instances {
		if ins.WakeID != wakeID {
			continue
		}
		if ins.State != "WAKING" && ins.State != "COLD_BOOTING" && ins.State != "RUNNING" {
			continue
		}
		if best == nil || ins.StartedAt.After(best.StartedAt) {
			copy := ins
			best = &copy
		}
	}
	if best == nil {
		return Instance{}, ErrNotFound
	}
	return *best, nil
}

func (m *MemStore) ListInstancesForApp(_ context.Context, appID string) ([]Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Instance
	for _, ins := range m.instances {
		if ins.AppID == appID {
			out = append(out, ins)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out, nil
}

// ListLatestInstancesForApp returns up to `limit` rows for appID
// ordered by started_at DESC. Mirror of the PgStore method added
// alongside the dashboard "Recent wakes" feature (gaps analysis
// 2026-07-23). limit ≤ 0 returns an empty slice — fail closed
// rather than rendering an unbounded table. After the in-place sort
// we slice to limit; the sort is O(n log n) but bounded by the
// MemStore instance count, which is tiny in tests.
func (m *MemStore) ListLatestInstancesForApp(ctx context.Context, appID string, limit int) ([]Instance, error) {
	if limit <= 0 {
		return nil, nil
	}
	all, err := m.ListInstancesForApp(ctx, appID)
	if err != nil {
		return nil, err
	}
	if len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}

// LookupBootStartedForWakes (ADR-123) returns the wake-boot telemetry
// for each wake_id in the input slice. memstore implementation walks
// the in-memory events slice (test-only path) — production read
// goes through PgStore.LookupBootStartedForWakes.
//
// PR-A: parses at_capacity + ready_in_ms in lockstep with the pgstore
// contract so test fixtures (and any injectable-Store callers) see
// the same WakeBootMeta shape production surfaces. ready_in_ms is
// computed by scanning the events slice a second time for the
// earliest wake.boot_completed row with the same wake_id; absent
// completion rows leave ReadyInMS at the zero default (em-dash at
// the view layer).
func (m *MemStore) LookupBootStartedForWakes(_ context.Context, wakeIDs []string) (map[string]WakeBootMeta, error) {
	out := make(map[string]WakeBootMeta, len(wakeIDs))
	wanted := make(map[string]struct{}, len(wakeIDs))
	for _, id := range wakeIDs {
		wanted[id] = struct{}{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// First pass: build a wake_id → earliest boot_completed timestamp
	// map. Single forward scan; events appended in at-ASC order.
	completedAt := make(map[string]time.Time)
	for _, e := range m.events {
		if e.Kind != "wake.boot_completed" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(e.Data, &payload); err != nil {
			continue
		}
		wakeID, _ := payload["wake_id"].(string)
		if _, ok := wanted[wakeID]; !ok {
			continue
		}
		if _, have := completedAt[wakeID]; !have {
			completedAt[wakeID] = e.At
		}
	}
	// Second pass: stamp WakeBootMeta from the earliest
	// wake.boot_started row per wake_id.
	for _, e := range m.events {
		if e.Kind != "wake.boot_started" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(e.Data, &payload); err != nil {
			continue
		}
		wakeID, _ := payload["wake_id"].(string)
		if _, ok := wanted[wakeID]; !ok {
			continue
		}
		if _, dup := out[wakeID]; dup {
			continue // keep earliest (events appended in at-ASC order)
		}
		meta := WakeBootMeta{}
		if t, ok := payload["trigger"].(string); ok {
			meta.Trigger = t
		}
		if q, ok := payload["queued_count"].(float64); ok {
			meta.QueuedCount = int(q)
		}
		if c, ok := payload["concurrency_at_admit"].(float64); ok {
			meta.ConcurrencyAtAdmit = int(c)
		}
		// at_capacity: distinguish "key absent" (pre-PR-A fleet) from
		// "key present and explicitly false". The dashboard's
		// em-dash-on-absent convention depends on AtCapacityPresent.
		if _, ok := payload["at_capacity"]; ok {
			meta.AtCapacityPresent = true
			if v, ok := payload["at_capacity"].(bool); ok {
				meta.AtCapacity = v
			}
		}
		// ready_in_ms: total elapsed milliseconds between boot_started.at
		// and the matching boot_completed.at. Mirrors pgstore's
		// EXTRACT(EPOCH …) * 1000 — NOT EXTRACT(MILLISECONDS …)
		// because PostgreSQL intervals are stored as months/days/
		// seconds and EXTRACT(MILLISECONDS) is silently wrong for
		// deltas >= 60s.
		if cat, ok := completedAt[wakeID]; ok && !cat.IsZero() {
			delta := cat.Sub(e.At)
			if delta > 0 {
				meta.ReadyInMS = int(delta / time.Millisecond)
			}
		}
		out[wakeID] = meta
	}
	return out, nil
}

// ListAllInstances returns every instance whose state is one schedd's
// idle reaper considers live (running, waking, cold_booting,
// snapshotting). Sorted DESC by StartedAt to match the partial index
// shape in migration 00009 — pkg/state.pgstore orders the same way in
// SQL, so tests and production behave identically.
func (m *MemStore) ListAllInstances(_ context.Context) ([]Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Instance
	for _, ins := range m.instances {
		switch ins.State {
		case string(StateRunning), string(StateWaking), string(StateColdBooting), string(StateSnapshotting):
			out = append(out, ins)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out, nil
}

// ListInstancesForAccount joins the instance set against the app set
// in-memory; the production path is a single SQL query (pgstore). Used
// by the meterd quota loop on Free hard-stop (spec §4.7).
func (m *MemStore) ListInstancesForAccount(_ context.Context, accountID string) ([]Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	owned := make(map[string]struct{}, len(m.apps))
	for _, a := range m.apps {
		if a.AccountID == accountID {
			owned[a.ID] = struct{}{}
		}
	}
	var out []Instance
	for _, ins := range m.instances {
		if _, ok := owned[ins.AppID]; ok {
			out = append(out, ins)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out, nil
}

// ListInstancesForAccountPaged is the cursor-paginated mirror of
// PgStore.ListInstancesForAccountPaged (issue #393). Mirrors the
// SQL semantics: cursor is instance.id, sort is started_at DESC then
// id DESC, limit is server-side clamped to 1..100.
func (m *MemStore) ListInstancesForAccountPaged(_ context.Context, accountID string, limit int, before string) ([]Instance, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	owned := make(map[string]struct{}, len(m.apps))
	for _, a := range m.apps {
		if a.AccountID == accountID {
			owned[a.ID] = struct{}{}
		}
	}
	var out []Instance
	for _, ins := range m.instances {
		if _, ok := owned[ins.AppID]; !ok {
			continue
		}
		if before != "" && ins.ID >= before {
			// Mirror the SQL: `id < $before` — strictly less than, so
			// the cursor itself is excluded from the next page.
			continue
		}
		out = append(out, ins)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].StartedAt.Equal(out[j].StartedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].StartedAt.After(out[j].StartedAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ListLatestInstancePerApp returns the most-recently-started instance
// for each app owned by the account (PR #48 follow-up). Used by the
// dashboard cold-wake badge so one query replaces N per-app
// ListInstancesForApp calls. Apps with no instance rows are absent
// from the returned map — the dashboard treats that as ◌ sleeping
// via BadgeForDefault.
func (m *MemStore) ListLatestInstancePerApp(_ context.Context, accountID string) (map[string]Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	owned := make(map[string]struct{}, len(m.apps))
	for _, a := range m.apps {
		if a.AccountID == accountID {
			owned[a.ID] = struct{}{}
		}
	}
	out := map[string]Instance{}
	for _, ins := range m.instances {
		if _, ok := owned[ins.AppID]; !ok {
			continue
		}
		cur, seen := out[ins.AppID]
		if !seen || ins.StartedAt.After(cur.StartedAt) {
			out[ins.AppID] = ins
		}
	}
	return out, nil
}

func (m *MemStore) UpdateInstanceState(_ context.Context, id, state string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ins, ok := m.instances[id]
	if !ok {
		return ErrNotFound
	}
	ins.State = state
	m.instances[id] = ins
	return nil
}

// IncInstanceRequestCount (ADR-098 C8) bumps the per-instance
// request_count column by delta. Mirrors PgStore's behaviour:
// idempotent on Phase-4-loser re-applies (the writer is additive),
// returns -1 when the row is gone. The memstore mirrors the column
// on the Instance struct so the gate can read the value without
// a SQL hop; C10 wires the gate-side reader.
func (m *MemStore) IncInstanceRequestCount(_ context.Context, id string, delta int64) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ins, ok := m.instances[id]
	if !ok {
		return -1, nil
	}
	ins.RequestCount += delta
	m.instances[id] = ins
	return ins.RequestCount, nil
}

// UpdateInstanceStateWithTimestamp mirrors PgStore's variant. Mirrors
// the §6.1 watchdog's need to know "time of entry into current
// state" for SNAPSHOTTING rows; parked_at is the column the watchdog
// reads on that state.
func (m *MemStore) UpdateInstanceStateWithTimestamp(_ context.Context, id, state string, parkedAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ins, ok := m.instances[id]
	if !ok {
		return ErrNotFound
	}
	ins.State = state
	ins.ParkedAt = parkedAt
	m.instances[id] = ins
	return nil
}

// UpdateInstanceStateToTerminal mirrors PgStore's variant. Writes the
// new state AND stamps terminal_at on the same locked read-modify-write
// (PR #74). Engine.transition routes here for {STOPPED, FAILED}; today
// no caller writes a different timestamp column for those states.
func (m *MemStore) UpdateInstanceStateToTerminal(_ context.Context, id, state string, terminalAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ins, ok := m.instances[id]
	if !ok {
		return ErrNotFound
	}
	ins.State = state
	ts := terminalAt
	ins.TerminalAt = &ts
	m.instances[id] = ins
	return nil
}

// SetInstanceFrameworkReadyAt mirrors pgstore.SetInstanceFrameworkReadyAt
// (PR #470-FU-B). Updates the in-memory instance row's
// `FrameworkReadyAt` pointer to point at the supplied time. The pointer
// indirection is required by the Instance struct definition so callers
// can distinguish "no signal yet" (nil) from "signal arrived at zero"
// (which is impossible given time.Time's zero check but kept for
// symmetry). Returns ErrNotFound for missing rows.
func (m *MemStore) SetInstanceFrameworkReadyAt(_ context.Context, id string, readyAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ins, ok := m.instances[id]
	if !ok {
		return ErrNotFound
	}
	ts := readyAt
	ins.FrameworkReadyAt = &ts
	m.instances[id] = ins
	return nil
}

// SetInstanceMode (issue #72 / ADR-125) mirrors pgstore.SetInstanceMode.
// Flips instances.mode to the supplied value (InstanceModeNormal or
// InstanceModeMirror). Idempotent. Used by the schedd mirror
// admission path and by tests that plant a mirror instance without
// going through the full wake-coord loop. Returns ErrNotFound when
// the row is missing.
func (m *MemStore) SetInstanceMode(_ context.Context, id string, mode InstanceMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ins, ok := m.instances[id]
	if !ok {
		return ErrNotFound
	}
	ins.Mode = string(mode)
	m.instances[id] = ins
	return nil
}

// ClearInstanceFrameworkReadyAt mirrors pgstore.ClearInstanceFrameworkReadyAt
// (PR #470-FU-B). Resets the framework_ready_at pointer to nil so the
// next warm-capture cycle starts without a stale stamp.
func (m *MemStore) ClearInstanceFrameworkReadyAt(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ins, ok := m.instances[id]
	if !ok {
		return ErrNotFound
	}
	ins.FrameworkReadyAt = nil
	m.instances[id] = ins
	return nil
}

// BumpInstanceTailCount mirrors pgstore.BumpInstanceTailCount
// (issue #667 / ADR-078). Applies the signed delta to the
// in-memory instance's TailCount under the existing instance
// mutex so a concurrent receipt cannot lose increments, and
// floors at 0 to mirror the SQL GREATEST(…, 0) guard. Returns
// the post-update value so the caller (vmmd's
// MarkInstanceTailTerminal) does not need a second read.
//
// The MemStore holds the only canonical copy of the in-memory
// tail count; the live Instance struct on pkg/fcvm.Manager is
// NOT the source of truth for tail — the runner-side WaitGroup
// is. This method is the platform-side mirror of the runner's
// in-process counter; schedd's reaper (PR 4) reads
// Instance.TailCount via GetInstance to gate the park path.
func (m *MemStore) BumpInstanceTailCount(_ context.Context, id string, delta int32) (int32, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ins, ok := m.instances[id]
	if !ok {
		return 0, ErrNotFound
	}
	post := ins.TailCount + int(delta)
	if post < 0 {
		post = 0
	}
	ins.TailCount = post
	m.instances[id] = ins
	return int32(post), nil
}

// DecrementInstanceTailCount mirrors pgstore.DecrementInstanceTailCount
// (issue #667 / ADR-078). Decrement by n with the 0-floor guard.
// Kept as a separate method (vs the BumpInstanceTailCount form)
// for symmetry with the pgstore API and to make every decrement
// site self-documenting at the call site. The snapshotAndPark
// watchdog (PR 4) calls this with n = the unfinished-tail count
// to floor the counter when the watchdog fires.
func (m *MemStore) DecrementInstanceTailCount(_ context.Context, id string, n int32) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ins, ok := m.instances[id]
	if !ok {
		return ErrNotFound
	}
	if n <= 0 {
		return nil
	}
	if int64(ins.TailCount) <= int64(n) {
		ins.TailCount = 0
	} else {
		ins.TailCount -= int(n)
	}
	m.instances[id] = ins
	return nil
}

// GetInstanceTailCount mirrors pgstore.GetInstanceTailCount
// (issue #667 / ADR-078). Used by the snapshotAndPark watchdog to
// poll for drain completion.
func (m *MemStore) GetInstanceTailCount(_ context.Context, id string) (int32, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ins, ok := m.instances[id]
	if !ok {
		return 0, ErrNotFound
	}
	return int32(ins.TailCount), nil
}

// ListInstancesInTerminalStatesOlderThan is the §17 retention sweep's
// lookup (PR #74). Mirrors ListInstancesByStatesOlderThan but reads
// terminal_at instead of the state-aware started_at/parked_at pair.
func (m *MemStore) ListInstancesInTerminalStatesOlderThan(_ context.Context, states []State, threshold time.Time) ([]Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	wanted := make(map[State]bool, len(states))
	for _, s := range states {
		wanted[s] = true
	}
	var out []Instance
	for _, ins := range m.instances {
		if !wanted[State(ins.State)] {
			continue
		}
		if ins.TerminalAt == nil {
			continue
		}
		if !ins.TerminalAt.Before(threshold) {
			continue
		}
		out = append(out, ins)
	}
	return out, nil
}

// DeleteInstance removes an instance row unconditionally (PR #74).
// Returns ErrNotFound when the row is already gone — the retention
// sweep swallows that case for redelivery. There are no FK cascades;
// events.subject and usage_minutes.instance_id carry no FK today.
func (m *MemStore) DeleteInstance(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.instances[id]; !ok {
		return ErrNotFound
	}
	delete(m.instances, id)
	return nil
}

// ListInstancesByStatesOlderThan is the watchdog's lookup (commit 3,
// spec §6.1). Mirrors PgStore: coalesce started_at / parked_at on the
// age comparison.
func (m *MemStore) ListInstancesByStatesOlderThan(_ context.Context, states []State, threshold time.Time) ([]Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	wanted := make(map[State]bool, len(states))
	for _, s := range states {
		wanted[s] = true
	}
	var out []Instance
	for _, ins := range m.instances {
		if !wanted[State(ins.State)] {
			continue
		}
		age := ins.StartedAt
		if State(ins.State) == StateSnapshotting {
			age = ins.ParkedAt
		}
		if age.IsZero() {
			continue
		}
		if age.Before(threshold) {
			out = append(out, ins)
		}
	}
	return out, nil
}

func (m *MemStore) SetInstanceRuntime(_ context.Context, id, netns, hostIP string, guestUID int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ins, ok := m.instances[id]
	if !ok {
		return ErrNotFound
	}
	ins.Netns = netns
	ins.HostIP = hostIP
	ins.GuestUID = guestUID
	ins.StartedAt = time.Now()
	m.instances[id] = ins
	return nil
}

func (m *MemStore) RunningInstanceForApp(_ context.Context, appID string) (Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var newest Instance
	found := false
	for _, ins := range m.instances {
		if ins.AppID != appID || ins.State != "running" {
			continue
		}
		if !found || ins.StartedAt.After(newest.StartedAt) {
			newest = ins
			found = true
		}
	}
	if !found {
		return Instance{}, ErrNotFound
	}
	return newest, nil
}

func (m *MemStore) TouchInstancesLastSeen(_ context.Context, touches []InstanceTouch) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	applied := 0
	for _, t := range touches {
		ins, ok := m.instances[t.InstanceID]
		if !ok {
			continue
		}
		ins.LastRequestAt = t.LastRequest
		m.instances[t.InstanceID] = ins
		applied++
	}
	return applied, nil
}

// TouchInstancesWithRequestDelta (ADR-098 C9) applies both
// last_request_at and the per-instance request_count delta. The
// memstore mirrors the writer contract: additive
// (`request_count = request_count + delta`), idempotent on
// Phase-4-loser re-applies, and rows that no longer exist are
// silently dropped (the touch is a no-op).
func (m *MemStore) TouchInstancesWithRequestDelta(_ context.Context, touches []InstanceTouch) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	applied := 0
	for _, t := range touches {
		ins, ok := m.instances[t.InstanceID]
		if !ok {
			continue
		}
		ins.LastRequestAt = t.LastRequest
		ins.RequestCount += t.RequestDelta
		m.instances[t.InstanceID] = ins
		applied++
	}
	return applied, nil
}

// --- snapshots --------------------------------------------------------------
//
// MemStore's snapshot table mirrors the Postgres semantics: First row wins,
// subsequent inserts for the same (deployment_id, fc_version, storage_key)
// collide as ErrConflict so imaged's idempotent retry is silent. The legacy
// `path` uniqueness key was dropped with #96 slice 3 (storage_key is the
// only blob locator and uniqueness is implicit on its value).

func (m *MemStore) CreateSnapshot(_ context.Context, snap Snapshot) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// StorageKey is required on both backends (see PgStore for the
	// rationale). The in-memory store doesn't have a DB DEFAULT to
	// fall back on, so the contract is enforced here as well —
	// silently storing "" would propagate to the GC loop and have it
	// Storage.Delete under an empty key (a no-op for every backend
	// since none accept "").
	if snap.StorageKey == "" {
		return Snapshot{}, errors.New("state: MemStore.CreateSnapshot: storage_key required (populate via sched.SnapshotMemKey at the call site)")
	}
	if snap.Tier == "" {
		// Issue #470 / ADR-055: empty tier is treated as "init" for
		// legacy callers; new warm-tier capture code passes
		// SnapshotTierWarm explicitly.
		snap.Tier = SnapshotTierInit
	}
	if snap.ID == "" {
		snap.ID = uuid.NewString()
	}
	if snap.CreatedAt.IsZero() {
		snap.CreatedAt = time.Now()
	}
	for _, existing := range m.snapshots {
		// Mirror the (deployment_id, tier) unique index from migration
		// 00110: a non-stale row with the same (deployment_id, tier)
		// is a duplicate — surface ErrConflict so callers fall through
		// to the existing-imaged semantic. Different-tier rows on the
		// same deployment are allowed (warm + init coexist).
		if existing.DeploymentID == snap.DeploymentID && existing.Tier == snap.Tier && !existing.Stale {
			return Snapshot{}, ErrConflict
		}
	}
	m.snapshots = append(m.snapshots, snap)
	return snap, nil
}

func (m *MemStore) LatestSnapshot(_ context.Context, deploymentID string) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var latest Snapshot
	found := false
	for _, s := range m.snapshots {
		if s.DeploymentID != deploymentID || s.Stale {
			continue
		}
		if !found {
			latest = s
			found = true
			continue
		}
		// Issue #470 / ADR-055: warm wins on a created_at tie. The
		// (tier='warm') order-by clause in PgStore.LatestSnapshot
		// mirrors this preference.
		sWarm := s.Tier == SnapshotTierWarm
		lWarm := latest.Tier == SnapshotTierWarm
		switch {
		case sWarm != lWarm && sWarm:
			latest = s
		case sWarm == lWarm && s.CreatedAt.After(latest.CreatedAt):
			latest = s
		}
	}
	if !found {
		return Snapshot{}, ErrNotFound
	}
	return latest, nil
}

// LatestSnapshotForTier mirrors PgStore.LatestSnapshotForTier — returns
// the freshest non-stale snapshot for (deploymentID, tier). Empty tier
// is treated as "init" for legacy callers. Returns ErrNotFound when no
// non-stale row exists; schedd's tier-fallback chain treats that as
// "fall through to the next tier" (issue #470 / ADR-055).
func (m *MemStore) LatestSnapshotForTier(_ context.Context, deploymentID, tier string) (Snapshot, error) {
	if tier == "" {
		tier = SnapshotTierInit
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var latest Snapshot
	found := false
	for _, s := range m.snapshots {
		if s.DeploymentID != deploymentID || s.Stale || s.Tier != tier {
			continue
		}
		if !found || s.CreatedAt.After(latest.CreatedAt) {
			latest = s
			found = true
		}
	}
	if !found {
		return Snapshot{}, ErrNotFound
	}
	return latest, nil
}

func (m *MemStore) MarkSnapshotStale(_ context.Context, snapshotID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.snapshots {
		if m.snapshots[i].ID == snapshotID {
			m.snapshots[i].Stale = true
			return nil
		}
	}
	return ErrNotFound
}

// ListSnapshotsForGC joins snapshots → deployments → apps in-memory and
// filters out snapshots belonging to soft-deleted apps (apps.status='deleted').
// MemStore doesn't index the join; the O(N×M) scan is fine for the test
// harness, which seeds at most a few dozen rows. The slice is sorted
// newest-first to match PgStore.
func (m *MemStore) ListSnapshotsForGC(_ context.Context) ([]SnapshotForGC, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	appByID := make(map[string]App, len(m.apps))
	for _, a := range m.apps {
		appByID[a.ID] = a
	}
	depByID := make(map[string]Deployment, len(m.deployments))
	for _, d := range m.deployments {
		depByID[d.ID] = d
	}
	var out []SnapshotForGC
	for _, s := range m.snapshots {
		if s.Stale {
			continue
		}
		dep, ok := depByID[s.DeploymentID]
		if !ok {
			continue
		}
		app, ok := appByID[dep.AppID]
		if !ok || app.Status == AppDeleted {
			continue
		}
		out = append(out, SnapshotForGC{
			ID:           s.ID,
			DeploymentID: s.DeploymentID,
			AppID:        app.ID,
			AccountID:    app.AccountID,
			// B1.1: forward the slug so imaged's GC doesn't have to
			// re-resolve it per eviction (was O(2N) extra SQL).
			AppSlug:   app.Slug,
			FCVersion: s.FCVersion,
			MemBytes:  s.MemBytes,
			DiskBytes: s.DiskBytes,
			// Issue #470 / ADR-055: forward the tier so the GC loop's
			// perAppKeepCurrentPrevious can keep (current warm +
			// previous init) per warm-tier app and (current init +
			// previous init) per init-only app.
			Tier: s.Tier,
			// #96 / ADR-025 axis 2: forward the canonical storage
			// key so imaged's GC loop can Storage.Delete under it
			// without a second hop through Snapshot.
			StorageKey: s.StorageKey,
			Stale:      s.Stale,
			CreatedAt:  s.CreatedAt,
			// Issue #470 / PR C / ADR-072: forward
			// apps.warm_snapshot_enabled so the per-tier GC
			// policy can apply the 2+2 floor on warm-tier apps
			// and the 2-init-only floor on disabled apps. Same
			// denormalisation pattern as AppSlug above.
			AppWarmSnapshotEnabled: app.WarmSnapshotEnabled,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

// DeleteSnapshotsByID removes the named snapshot rows in-place. Returns
// the number of rows actually removed; a second call with the same ids
// returns 0. Never deletes the last live snapshot of a non-deleted app
// — that invariant would break the "always have a cold-bootable or
// snapshot-restoreable deployment" rule (spec §6.2-3). MemStore is
// permissive here because it's test-only; PgStore is authoritative.
func (m *MemStore) DeleteSnapshotsByID(_ context.Context, ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	idSet := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		idSet[id] = struct{}{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := m.snapshots[:0]
	var removed int64
	for _, s := range m.snapshots {
		if _, drop := idSet[s.ID]; drop {
			removed++
			continue
		}
		kept = append(kept, s)
	}
	m.snapshots = append([]Snapshot(nil), kept...)
	return removed, nil
}

// MarkAllSnapshotsStaleByFCVersion mirrors the SQL UPDATE: every non-stale
// row whose fc_version != currentVersion is flipped. ADR-005.
func (m *MemStore) MarkAllSnapshotsStaleByFCVersion(_ context.Context, currentVersion string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for i := range m.snapshots {
		if !m.snapshots[i].Stale && m.snapshots[i].FCVersion != currentVersion {
			m.snapshots[i].Stale = true
			n++
		}
	}
	return n, nil
}

// MarkAllSnapshotsStaleByAppProtocol mirrors the SQL UPDATE: every
// non-stale snapshot whose deployment's app.app_protocol ∈
// appProtocols is flipped stale. ADR-127 §D1, Layer 6 (imaged F3
// sweep). app_protocol=http1 snapshots are never affected. Empty
// appProtocols is a no-op (matches the SQL behaviour: the UPDATE
// runs against an empty set which matches nothing).
func (m *MemStore) MarkAllSnapshotsStaleByAppProtocol(_ context.Context, appProtocols []string) (int64, error) {
	if len(appProtocols) == 0 {
		return 0, nil
	}
	allowed := make(map[string]struct{}, len(appProtocols))
	for _, p := range appProtocols {
		allowed[p] = struct{}{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for i := range m.snapshots {
		snap := m.snapshots[i]
		if snap.Stale {
			continue
		}
		dep, ok := m.deployments[snap.DeploymentID]
		if !ok {
			continue
		}
		app, ok := m.apps[dep.AppID]
		if !ok {
			continue
		}
		if _, match := allowed[app.AppProtocol]; match {
			m.snapshots[i].Stale = true
			n++
		}
	}
	return n, nil
}

// MarkSnapshotStaleByAppProtocol is the single-row mirror of
// MarkAllSnapshotsStaleByAppProtocol. Returns ErrNotFound when no
// snapshot matches the id AND the deployment's app.app_protocol
// ∈ appProtocols. Empty inputs are errors (caller bug).
func (m *MemStore) MarkSnapshotStaleByAppProtocol(_ context.Context, snapshotID string, appProtocols []string) error {
	if len(appProtocols) == 0 {
		return errors.New("memstore: MarkSnapshotStaleByAppProtocol: empty appProtocols set")
	}
	if snapshotID == "" {
		return errors.New("memstore: MarkSnapshotStaleByAppProtocol: empty snapshotID")
	}
	allowed := make(map[string]struct{}, len(appProtocols))
	for _, p := range appProtocols {
		allowed[p] = struct{}{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.snapshots {
		snap := m.snapshots[i]
		if snap.ID != snapshotID {
			continue
		}
		dep, ok := m.deployments[snap.DeploymentID]
		if !ok {
			return ErrNotFound
		}
		app, ok := m.apps[dep.AppID]
		if !ok {
			return ErrNotFound
		}
		if _, match := allowed[app.AppProtocol]; !match {
			return ErrNotFound
		}
		if snap.Stale {
			// Already stale — caller asked to mark stale, but it's
			// already stale; treat as success (idempotent). The
			// PgStore version (pkg/state/pgstore.go:9807) behaves
			// the same: its UPDATE has no `s.stale = false`
			// predicate, so a no-op flip still reports
			// RowsAffected=1 and returns nil; the ErrNotFound path
			// fires only when no row matches the id + app_protocol
			// triple. The bulk path (MarkAllSnapshotsStaleByAppProtocol)
			// DOES carry `and s.stale = false` so its row-count is
			// the count of *newly* stale-marked rows; the single-row
			// mirror here matches MemStore semantics, not bulk
			// semantics, because the bulk return is a count and
			// the single-row return is a found/not-found bool.
			return nil
		}
		m.snapshots[i].Stale = true
		return nil
	}
	return ErrNotFound
}

// MarkOldSnapshotsStale flips the given IDs to stale=true (no-op if absent).
// Used by the per-app "current + previous" enforcement in the GC.
func (m *MemStore) MarkOldSnapshotsStale(_ context.Context, beforeSnapshotIDs []string) (int64, error) {
	if len(beforeSnapshotIDs) == 0 {
		return 0, nil
	}
	idSet := make(map[string]struct{}, len(beforeSnapshotIDs))
	for _, id := range beforeSnapshotIDs {
		idSet[id] = struct{}{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for i := range m.snapshots {
		if _, ok := idSet[m.snapshots[i].ID]; ok {
			m.snapshots[i].Stale = true
			n++
		}
	}
	return n, nil
}

// DeleteSnapshotsStaleOlderThan mirrors the SQL DELETE … WHERE stale=true
// AND created_at < now()-retention. MemStore uses time.Now for the cutoff
// (deterministic tests pass a future/injected CreatedAt at seed time).
func (m *MemStore) DeleteSnapshotsStaleOlderThan(_ context.Context, retention time.Duration) (int64, error) {
	cutoff := time.Now().Add(-retention)
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	kept := m.snapshots[:0]
	for i := range m.snapshots {
		if m.snapshots[i].Stale && m.snapshots[i].CreatedAt.Before(cutoff) {
			n++
			continue
		}
		kept = append(kept, m.snapshots[i])
	}
	m.snapshots = append([]Snapshot(nil), kept...)
	return n, nil
}

// --- Audit ------------------------------------------------------------------

// parseSubjectID accepts either a canonical UUID (with hyphens) or the
// 32-char hex form that MemStore's newID() emits, and returns the
// canonical *uuid.UUID either way. Returns nil on any parse failure so
// callers can treat "unparseable" the same as "no subject" (which is
// what the audit-log filter expects: no row would have produced a
// garbage ID). The fix for the silent-drop bug surfaced by the audit-log
// PR's tests: engine.go hands us hex IDs (newID output) and uuid.Parse
// rejects them, so Subject stayed nil even though we said we set it.
func parseSubjectID(s string) *uuid.UUID {
	if s == "" {
		return nil
	}
	if u, err := uuid.Parse(s); err == nil {
		return &u
	}
	if len(s) == 32 {
		if b, err := hex.DecodeString(s); err == nil {
			u := uuid.UUID(b)
			return &u
		}
	}
	return nil
}

// --- Compute nodes (issue #97 / ADR-025 axis 3) ---------------------------
//
// Mirrors the compute_nodes table. The synthetic 'default-local' row
// is auto-seeded by NewMemStore via seedDefaultLocalNodeLocked (same
// shape as migrations/00024_compute_nodes.sql's seed) so single-box
// tests don't have to call CreateComputeNode. Tests exercising the
// multi-node path add additional rows via CreateComputeNode; the
// per-vm overhead (8 MB) is referenced from pkg/api.PerVMOverheadMB
// — the single source of truth shared with PgStore.ComputeNodeUsedMB,
// sched.Ledger's reservation math, and the §4.7 billing model.

// seedDefaultLocalNodeLocked inserts the synthetic single-host vmmd
// row. Called once by NewMemStore after the struct literal so the
// seeded row carries a real id + created_at. Idempotent on a fresh
// store; production never calls this (the migration handles it).
//
// Region/Zone are seeded to 'local'/'local' to mirror migrations/
// 00069_compute_nodes_region_zone.sql — the single-box deploy has a
// deterministic (region, name) tie-break ordering without needing
// the migration to have run on the memstore (which is test-only).
func (m *MemStore) seedDefaultLocalNodeLocked() {
	now := time.Now()
	id := newID()
	local := DefaultLocalityLabel
	m.computeNodes[id] = ComputeNode{
		ID:             id,
		Name:           DefaultLocalNodeName,
		TargetURL:      "unix:///run/faas/vmmd.sock",
		VPCPUs:         160,
		MemMB:          56000,
		MaxConcurrency: 200,
		// PR scale-out readiness #4: routed through api.DefaultComputeNodeCeilingMB
		// so the helper and cmd/vmmd/config.go share a single source of truth.
		// Resolves to the same integer (47_600) as before — no behavior change.
		AdmissionCeilingMB: api.DefaultComputeNodeCeilingMB(),
		// Tier A2 / migration 00123: per-node vCPU budget. The
		// synthetic default-local row carries api.VCPUSlots so a
		// single-box install sees identical behaviour to the
		// pre-migration box-wide gate.
		VCPUBudget:      api.VCPUSlots,
		Active:          true,
		LastHeartbeatAt: now,
		CreatedAt:       now,
		// Phase 2 / Gate A: per-node schedd dial target. The
		// single-box synthetic row points at the legacy
		// /run/faas/schedd.sock so the gateway's per-node cache
		// dial path is byte-identical to the pre-PR behaviour when
		// only the default-local row exists.
		ScheddTargetURL: func() *string { s := "unix:///run/faas/schedd.sock"; return &s }(),
		Region:          &local,
		Zone:            &local,
	}
}

func (m *MemStore) ActiveComputeNodes(_ context.Context) ([]ComputeNode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ComputeNode, 0, len(m.computeNodes))
	for _, n := range m.computeNodes {
		if n.Active {
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ListAllComputeNodes returns every compute_node row including
// inactive ones, ordered by name. apid's GET /v1/compute-nodes
// operator surface (PR #114) calls this so a recently-drained
// node stays visible for ops dashboards. The fleet is
// single-digit for v1.0; the slice alloc is fine.
func (m *MemStore) ListAllComputeNodes(_ context.Context) ([]ComputeNode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ComputeNode, 0, len(m.computeNodes))
	for _, n := range m.computeNodes {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *MemStore) ComputeNodeByID(_ context.Context, id string) (ComputeNode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.computeNodes[id]
	if !ok {
		return ComputeNode{}, ErrNotFound
	}
	return n, nil
}

func (m *MemStore) ComputeNodeByName(_ context.Context, name string) (ComputeNode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, n := range m.computeNodes {
		if n.Name == name {
			return n, nil
		}
	}
	return ComputeNode{}, ErrNotFound
}

// ComputeNodeUsedMB returns the Σ(ram_mb + api.PerVMOverheadMB) for
// live instances on the given node. Live = state ∈ {'waking',
// 'cold_booting', 'running'} per §6.2-2 re-stated per-node. The
// 8 MB overhead matches pkg/state/pgstore.go's aggregate query and
// the billing model in spec §4.7 — single source of truth in
// pkg/api.PerVMOverheadMB (F-1 in PR #112 review).
func (m *MemStore) ComputeNodeUsedMB(_ context.Context, nodeID string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var used int64
	for _, ins := range m.instances {
		if ins.NodeID != nodeID {
			continue
		}
		switch ins.State {
		case "waking", "cold_booting", "running":
			used += int64(ins.RAMMB + api.PerVMOverheadMB)
		}
	}
	return used, nil
}

// ComputeNodeUsedMBByNode is the in-memory equivalent of PgStore's single
// aggregate fallback. It keeps tests and local fixtures on the same bulk API
// as production without changing the Store interface.
func (m *MemStore) ComputeNodeUsedMBByNode(ctx context.Context, nodeIDs []string) (map[string]int64, error) {
	used := make(map[string]int64, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		value, err := m.ComputeNodeUsedMB(ctx, nodeID)
		if err != nil {
			return nil, err
		}
		used[nodeID] = value
	}
	return used, nil
}

func (m *MemStore) HeartbeatComputeNode(_ context.Context, nodeID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.computeNodes[nodeID]
	if !ok {
		return ErrNotFound
	}
	n.LastHeartbeatAt = time.Now()
	m.computeNodes[nodeID] = n
	return nil
}

// MarkComputeNodeInactive flips active=false on the row (PR #114).
// Idempotent — flipping an inactive row keeps active=false, no
// observable change. The row is preserved so an operator can
// re-enable it (a future admin endpoint will hit a re-activate
// path; today nothing does).
func (m *MemStore) MarkComputeNodeInactive(_ context.Context, nodeID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.computeNodes[nodeID]
	if !ok {
		return ErrNotFound
	}
	n.Active = false
	m.computeNodes[nodeID] = n
	return nil
}

// SetComputeNodeRole overwrites the role column on a row by id
// (ADR-112 PR-B). Mirrors PgStore.SetComputeNodeRole behavior:
// same allow-list validation, same idempotent semantics, same
// ErrNotFound on a missing row. MemStore holds role as a *string
// (nullable, legacy "un-templated" sentinel); the setter accepts
// only {control-plane, compute-only} and writes the pointer form
// so the pointer-vs-value distinction survives a round-trip.
func (m *MemStore) SetComputeNodeRole(_ context.Context, nodeID, role string) error {
	if err := validateRoleForState(role); err != nil {
		return fmt.Errorf("state: set role compute_node %s: %w", nodeID, err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.computeNodes[nodeID]
	if !ok {
		return ErrNotFound
	}
	r := role
	n.Role = &r
	m.computeNodes[nodeID] = n
	return nil
}

func (m *MemStore) CreateComputeNode(_ context.Context, node ComputeNode) (ComputeNode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Unique-name enforcement mirrors the production UNIQUE constraint
	// on name. The MemStore uses a name → id map lookup for the same
	// effect — tests that pass a duplicate name get ErrConflict.
	for _, existing := range m.computeNodes {
		if existing.Name == node.Name {
			return ComputeNode{}, ErrConflict
		}
	}
	n := node
	if n.ID == "" {
		n.ID = newID()
	}
	if n.CreatedAt.IsZero() {
		n.CreatedAt = time.Now()
	}
	if n.LastHeartbeatAt.IsZero() {
		n.LastHeartbeatAt = n.CreatedAt
	}
	m.computeNodes[n.ID] = n
	return n, nil
}

// UpsertComputeNode mirrors pgstore's INSERT ... ON CONFLICT DO UPDATE
// (issue #98 / ADR-028). vmmd's self-registration calls this at startup
// — a node that has already been registered has its capacity refreshed
// and is reactivated (active=true), even if an operator had previously
// drained it. The explicit FromVmmd path below preserves the operator's
// active decision. The loop-then-store mirrors a write-then-map in the
// MemStore: cheaper than a SELECT-then-UPDATE for tests that hammer the
// path. CreatedAt stays monotonic on conflict.
//
// Deprecated: prefer UpsertComputeNodeFromOperator (apid POST
// path) or UpsertComputeNodeFromVmmd (vmmd self-registration path)
// so the operator-set target_url is not silently clobbered by
// vmmd's restart. Kept for backwards compat with the existing
// test fixtures that don't care about the ownership split.
func (m *MemStore) UpsertComputeNode(_ context.Context, node ComputeNode) (ComputeNode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var existing *ComputeNode
	for id, current := range m.computeNodes {
		if current.Name == node.Name {
			current := current
			existing = &current
			delete(m.computeNodes, id)
			break
		}
	}
	n := node
	if existing != nil {
		n.ID = existing.ID
		n.CreatedAt = existing.CreatedAt
	} else if n.ID == "" {
		n.ID = newID()
	}
	if n.CreatedAt.IsZero() {
		n.CreatedAt = time.Now()
	}
	if n.LastHeartbeatAt.IsZero() {
		n.LastHeartbeatAt = n.CreatedAt
	}
	n.Active = true
	m.computeNodes[n.ID] = n
	return n, nil
}

// UpsertComputeNodeFromOperator mirrors pgstore's operator-side
// upsert (apid POST /v1/compute-nodes). On conflict, every field
// is taken from the new row — the operator's POST wins.
func (m *MemStore) UpsertComputeNodeFromOperator(_ context.Context, node ComputeNode) (ComputeNode, error) {
	return m.upsertComputeNodeLocked(node, false /* preserveTargetURLOnConflict */)
}

// UpsertComputeNodeFromVmmd mirrors pgstore's vmmd-side
// self-registration. On conflict, target_url and active are preserved
// (the operator's POSTed value and drain decision win); the other
// fields are taken from the new row. The cold-INSERT case (no existing
// row) takes target_url from the new row and starts active, same shape
// as UpsertComputeNodeFromOperator — there's nothing to preserve.
//
// This is the load-bearing fix for the second-box cutover. See
// pgstore's comment on UpsertComputeNodeFromVmmd for the trap.
//
// Multi-host safety cluster PR-4 (audit F6, ADR-052 amendment):
// like pgstore, this method refuses to silently overwrite an
// existing row whose cert_fingerprint differs from the new row's.
// The check happens BEFORE upsertComputeNodeLocked modifies the
// in-memory map, so a refused drift leaves no in-memory side
// effect.
func (m *MemStore) UpsertComputeNodeFromVmmd(_ context.Context, node ComputeNode) (ComputeNode, error) {
	if node.Name != "" && node.CertFingerprint != nil && *node.CertFingerprint != "" {
		m.mu.Lock()
		var existingFP *string
		for _, current := range m.computeNodes {
			if current.Name == node.Name {
				fp := current.CertFingerprint
				existingFP = fp
				break
			}
		}
		m.mu.Unlock()
		if existingFP != nil && *existingFP != "" && *existingFP != *node.CertFingerprint {
			return ComputeNode{}, fmt.Errorf(
				"memstore: %w: node %q existing fingerprint %q differs from local leaf %q",
				ErrCertFingerprintDrift, node.Name, *existingFP, *node.CertFingerprint,
			)
		}
	}
	return m.upsertComputeNodeLocked(node, true /* preserveTargetURLOnConflict */)
}

// upsertComputeNodeLocked is the shared write path for the two
// ownership variants. preserveTargetURLOnConflict=true means "the
// vmmd path: keep the existing row's target_url on conflict" (a
// missing existing target_url falls back to the new row's value,
// matching pgstore's COALESCE).
func (m *MemStore) upsertComputeNodeLocked(node ComputeNode, preserveTargetURLOnConflict bool) (ComputeNode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var existing *ComputeNode
	for id, current := range m.computeNodes {
		if current.Name == node.Name {
			current := current
			existing = &current
			delete(m.computeNodes, id)
			break
		}
	}
	n := node
	if existing != nil {
		n.ID = existing.ID
		n.CreatedAt = existing.CreatedAt
		if preserveTargetURLOnConflict && existing.TargetURL != "" {
			n.TargetURL = existing.TargetURL
		}
		if preserveTargetURLOnConflict && existing.ScheddTargetURL != nil && n.ScheddTargetURL == nil {
			n.ScheddTargetURL = existing.ScheddTargetURL
		}
		if preserveTargetURLOnConflict && existing.GatewayTargetURL != nil && n.GatewayTargetURL == nil {
			n.GatewayTargetURL = existing.GatewayTargetURL
		}
	} else if n.ID == "" {
		n.ID = newID()
	}
	if n.CreatedAt.IsZero() {
		n.CreatedAt = time.Now()
	}
	if n.LastHeartbeatAt.IsZero() {
		n.LastHeartbeatAt = n.CreatedAt
	}
	if existing == nil || !preserveTargetURLOnConflict {
		n.Active = true
	} else {
		n.Active = existing.Active
	}
	m.computeNodes[n.ID] = n
	return n, nil
}

// UpsertNodeKey inserts or updates a (compute_node_id, key_id) row
// in the in-memory mirror of compute_node_keys (migration 00076,
// ADR-053). Mirrors PgStore.UpsertNodeKey's ON CONFLICT DO NOTHING
// semantics: a re-insert of the same (nodeID, keyID) is a no-op
// (the existing public_key_pem is preserved). See
// PgStore.UpsertNodeKey for the write-once rationale.
func (m *MemStore) UpsertNodeKey(_ context.Context, nodeID string, keyID string, publicKeyPEM string) error {
	if nodeID == "" || keyID == "" {
		return fmt.Errorf("state: memstore: UpsertNodeKey requires nodeID and keyID (got %q, %q)", nodeID, keyID)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	composite := nodeID + "\x00" + keyID
	if _, ok := m.computeNodeKeys[composite]; ok {
		return nil
	}
	m.computeNodeKeys[composite] = publicKeyPEM
	return nil
}

// LookupNodeKey returns the PEM for the (computeNodeID, keyID)
// row in the in-memory mirror of compute_node_keys. Returns
// ("", false) when no row exists. Test convenience for the
// cmd/vmmd registerComputeNodeKey coverage — production code
// goes through sched.NodeKeyRegistry.Refresh, which loads via
// the pg-side query, not this accessor. Keeping it MemStore-only
// (not on the Store interface) avoids pulling a test-only seam
// onto the production interface.
func (m *MemStore) LookupNodeKey(_ context.Context, computeNodeID string, keyID string) (string, bool) {
	if computeNodeID == "" || keyID == "" {
		return "", false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	pem, ok := m.computeNodeKeys[computeNodeID+"\x00"+keyID]
	return pem, ok
}

// SetComputeNodeActive flips active on a row by id (issue #98 /
// ADR-028). The watchdog drains a stale node to false; the heartbeat
// goroutine reanimates a drained node to true on the next successful
// dial. MemStore flips the flag in place; production also flips but
// additionally fires the compute_node_changed pg_notify trigger so
// gatewayd-internal sees the change without polling.
func (m *MemStore) SetComputeNodeActive(_ context.Context, id string, active bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.computeNodes[id]
	if !ok {
		return ErrNotFound
	}
	n.Active = active
	m.computeNodes[id] = n
	return nil
}

// ListComputeNodes returns every row in name order (issue #98 /
// ADR-028). When includeInactive is false, drained rows are filtered
// out — placement-equivalent semantics, backed by the partial
// compute_nodes_active_idx on the production side.
func (m *MemStore) ListComputeNodes(_ context.Context, includeInactive bool) ([]ComputeNode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ComputeNode, 0, len(m.computeNodes))
	for _, n := range m.computeNodes {
		if !includeInactive && !n.Active {
			continue
		}
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// DeleteComputeNode hard-deletes a row by id (issue #98 / ADR-028).
// Mirrors pgstore's semantics: ErrNotFound when no row matches. The
// caller (apid's DELETE ?hard=1) is responsible for refusing on the
// synthetic default-local row — see cmd/apid/compute_nodes.go.
func (m *MemStore) DeleteComputeNode(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.computeNodes[id]; !ok {
		return ErrNotFound
	}
	delete(m.computeNodes, id)
	// CP-1: cascade the heartbeat history. Mirrors the FK ON DELETE
	// CASCADE on compute_node_heartbeats.node_id; the endpoint
	// resolves the parent by name first, so a missing history rows
	// after a hard-delete is the expected shape.
	delete(m.computeNodeHeartbeats, id)
	return nil
}

// AppendComputeNodeHeartbeat appends one row to the per-node
// history. The CP-1 routine path is schedd's Heartbeat.Tick; the
// deactivation/reactivation sources are stamped on the rarer
// deactivation/reactivation paths (gated by the same operations that
// call MarkComputeNodeInactive / SetComputeNodeActive). The unique
// (node_id, received_at) constraint is OBSERVED, not folded; a
// duplicate-timestamp stamp returns ErrConflict so the writer can
// log a warning rather than silently dedup.
func (m *MemStore) AppendComputeNodeHeartbeat(_ context.Context, nodeID string, receivedAt, lastHeartbeatAt time.Time, source string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.computeNodes[nodeID]; !ok {
		return ErrNotFound
	}
	for _, prev := range m.computeNodeHeartbeats[nodeID] {
		if prev.ReceivedAt.Equal(receivedAt) {
			return fmt.Errorf("%w: compute_node_heartbeats (node_id, received_at) duplicate", ErrConflict)
		}
	}
	row := ComputeNodeHeartbeat{
		ID:              int64(len(m.computeNodeHeartbeats[nodeID]) + 1),
		NodeID:          nodeID,
		ReceivedAt:      receivedAt,
		LastHeartbeatAt: lastHeartbeatAt,
		Source:          source,
	}
	m.computeNodeHeartbeats[nodeID] = append(m.computeNodeHeartbeats[nodeID], row)
	return nil
}

// ListComputeNodeHeartbeats returns up to limit rows for the
// given node, newest first. since.IsZero() ⇒ no lower bound;
// otherwise restrict to rows whose received_at >= since. The
// implementation iterates in insertion order (cheap for the
// routine test scale) and reverses the slice for the wire shape;
// the SQL PgStore impl uses the (node_id, received_at desc)
// composite index for the same read.
func (m *MemStore) ListComputeNodeHeartbeats(_ context.Context, nodeID string, since time.Time, limit int) ([]ComputeNodeHeartbeat, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := m.computeNodeHeartbeats[nodeID]
	if limit <= 0 {
		limit = 200
	}
	out := make([]ComputeNodeHeartbeat, 0, len(rows))
	// Iterate in reverse (newest-first) so the wire shape is
	// stable without a sort. The endpoint's reader expects
	// received_at DESC.
	for i := len(rows) - 1; i >= 0; i-- {
		row := rows[i]
		if !since.IsZero() && row.ReceivedAt.Before(since) {
			continue
		}
		out = append(out, row)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// AppendComputeNodeHeartbeatWithStats (PR #4 / ADR-091 §3.6
// amendment) is the v2 of AppendComputeNodeHeartbeat that also
// carries the per-node CPU and disk pressure at heartbeat-mint
// time. The PgStore impl is the load-bearing one — MemStore mirrors
// it for handler tests that don't stand up Postgres. The duplicate
// guard mirrors AppendComputeNodeHeartbeat's (node_id, received_at)
// check so the test code path sees the same ErrConflict surface.
// The CPUPct60s and DiskUsedBytes pointers are stored verbatim so
// a handler can distinguish "absent" (nil → pre-PR #4 row) from
// "explicit zero" (a fresh node with empty /srv/fc/snapshots).
func (m *MemStore) AppendComputeNodeHeartbeatWithStats(_ context.Context, nodeID string, receivedAt, lastHeartbeatAt time.Time, source string, cpuPct60s float64, diskUsedBytes int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.computeNodes[nodeID]; !ok {
		return ErrNotFound
	}
	for _, prev := range m.computeNodeHeartbeats[nodeID] {
		if prev.ReceivedAt.Equal(receivedAt) {
			return fmt.Errorf("%w: compute_node_heartbeats (node_id, received_at) duplicate", ErrConflict)
		}
	}
	cpu := cpuPct60s
	disk := diskUsedBytes
	row := ComputeNodeHeartbeat{
		ID:              int64(len(m.computeNodeHeartbeats[nodeID]) + 1),
		NodeID:          nodeID,
		ReceivedAt:      receivedAt,
		LastHeartbeatAt: lastHeartbeatAt,
		Source:          source,
		CPUPct60s:       &cpu,
		DiskUsedBytes:   &disk,
	}
	m.computeNodeHeartbeats[nodeID] = append(m.computeNodeHeartbeats[nodeID], row)
	return nil
}

// LatestHeartbeatStats (PR #4) returns the most-recent heartbeat
// per compute node, projected to the read shape (just NodeID +
// ReceivedAt + the two stat pointers). Used by the obsListNodes
// handler to fold CPU + disk pressure onto the per-node row. The
// loop iterates the per-node slice in insertion order (last row
// = newest, matching the SQL ORDER BY received_at DESC LIMIT 1
// pattern). Missing or pre-PR #4 nodes return nil for the stat
// pointers — the handler renders "—" for those.
func (m *MemStore) LatestHeartbeatStats(_ context.Context) ([]ComputeNodeHeartbeatStats, error) {
	return m.latestHeartbeatStatsWhere("", false)
}

// LatestBuilderHeartbeatStats (operator-side observability
// mega-PR / Commit 7 — P5) returns the most-recent heartbeat
// filtered to source='builder_tick'. The production writer is
// exercised by builderd; this method simply filters the history.
func (m *MemStore) LatestBuilderHeartbeatStats(_ context.Context) ([]ComputeNodeHeartbeatStats, error) {
	return m.latestHeartbeatStatsWhere("builder_tick", true)
}

// latestHeartbeatStatsWhere is the shared implementation for
// LatestHeartbeatStats (all sources, project silent nodes) and
// LatestBuilderHeartbeatStats (one source, project only the
// nodes that have a row). The two projections differ because
// the vmmd node list is the operator-visible fleet while the
// builder_tick list is "builderds we have written about" — we
// don't synthesise entries for nodes that haven't reported.
func (m *MemStore) latestHeartbeatStatsWhere(sourceFilter string, onlyMatchingNodes bool) ([]ComputeNodeHeartbeatStats, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ComputeNodeHeartbeatStats, 0, len(m.computeNodes))
	for nodeID, rows := range m.computeNodeHeartbeats {
		// Walk rows in reverse insertion order until we find
		// one matching the filter. Mirrors pgstore's
		// `order by received_at desc` semantics.
		var latest *ComputeNodeHeartbeat
		for i := len(rows) - 1; i >= 0; i-- {
			if sourceFilter == "" || rows[i].Source == sourceFilter {
				latest = &rows[i]
				break
			}
		}
		if latest == nil {
			if onlyMatchingNodes {
				continue
			}
			out = append(out, ComputeNodeHeartbeatStats{NodeID: nodeID})
			continue
		}
		out = append(out, ComputeNodeHeartbeatStats{
			NodeID:        nodeID,
			ReceivedAt:    latest.ReceivedAt,
			CPUPct60s:     latest.CPUPct60s,
			DiskUsedBytes: latest.DiskUsedBytes,
		})
	}
	return out, nil
}

// PerNodeLiveStats (PR #4) is the read-side aggregate for the
// /v1/admin/obs/nodes handler. Walks instances in {WAKING,
// COLD_BOOTING, RUNNING} state (the §6.2 invariant #1 set),
// groups by instances.node_id, and projects to compute_nodes.name
// for the human-friendly node label. The +8 on ram_mb mirrors
// §6.2 invariant #2 — Σ(ram_mb + 8) ≤ 47,600 MB — so the
// per-node RAMUsedMB sums to the fleet ceiling.
//
// Revision 2 (PR #4 prep): the original draft joined on a separate
// instance_node_bindings table; after re-reading migration 00024
// during implementation we discovered instances.node_id is already
// a NOT NULL FK to compute_nodes(id), backfilled on pre-existing
// rows. ADR-092 §8 amends §2.1 to drop the binding-table design.
// This implementation mirrors the SQL GROUP BY directly: the
// memstore's m.instances is the rows, the lookup onto
// m.computeNodes is the JOIN, and only the live states count.
func (m *MemStore) PerNodeLiveStats(_ context.Context) ([]PerNodeStats, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// node-id → *PerNodeStats. Walk every instance once; credit
	// counts and RAM to the per-node bucket keyed by node_id.
	// The handler LEFT JOINs onto compute_nodes by name; here we
	// just project the raw uuid-aggregated buckets and let the
	// handler map uuid → name via the existing
	// ListComputeNodes call. Keeping the memstore uuid-shaped
	// matches the SQL GROUP BY node_id.
	agg := map[string]*PerNodeStats{}
	for _, inst := range m.instances {
		if !isInstanceStateLive(inst.State) {
			continue
		}
		if inst.NodeID == "" {
			// Defensive: migration 00024 enforces NOT NULL, but
			// a hand-crafted memstore test fixture could trip
			// the invariant. Skip rather than panic.
			continue
		}
		row, ok := agg[inst.NodeID]
		if !ok {
			row = &PerNodeStats{NodeName: inst.NodeID}
			agg[inst.NodeID] = row
		}
		row.InstancesLive++
		switch inst.State {
		case instanceStateRunning:
			row.InstancesRunning++
		case instanceStateWaking:
			row.InstancesWaking++
		case instanceStateColdBooting:
			row.InstancesColdBooting++
		}
		// §6.2 invariant #2: ram_mb + 8 per live instance. Cast
		// to int64 because PerNodeStats.RAMUsedMB is int64
		// (matching the SQL SUM on bigint); Instance.RAMMB is
		// plain int.
		row.RAMUsedMB += int64(inst.RAMMB) + 8
	}
	out := make([]PerNodeStats, 0, len(agg))
	for _, row := range agg {
		out = append(out, *row)
	}
	return out, nil
}

// OperatorCapacity mirrors PgStore.OperatorCapacity without exposing the
// underlying app or instance rows. MemStore keeps the same placement and live
// state semantics so operator endpoint tests exercise the production shape.
func (m *MemStore) OperatorCapacity(_ context.Context) (OperatorCapacitySnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	byNode := make(map[string]int, len(m.computeNodes))
	out := OperatorCapacitySnapshot{Nodes: make([]OperatorCapacityNode, 0, len(m.computeNodes))}
	for id, n := range m.computeNodes {
		row := OperatorCapacityNode{
			ID:                 n.ID,
			Name:               n.Name,
			Active:             n.Active,
			VPCPUs:             n.VPCPUs,
			VCPUBudget:         n.VCPUBudget,
			MemMB:              n.MemMB,
			AdmissionCeilingMB: n.AdmissionCeilingMB,
		}
		if row.ID == "" {
			row.ID = id
		}
		out.Nodes = append(out.Nodes, row)
		byNode[row.ID] = len(out.Nodes) - 1
	}

	tenantIDs := make(map[string]struct{})
	for _, app := range m.apps {
		if app.Status == AppDeleted {
			continue
		}
		out.AppsTotal++
		tenantIDs[app.AccountID] = struct{}{}
		if app.NodeID == "" {
			out.UnplacedApps++
			continue
		}
		if nodeIndex, ok := byNode[app.NodeID]; ok {
			out.Nodes[nodeIndex].AppsCount++
			// A node-local set is not needed: each app contributes once
			// to its owner's tenant count in the placement projection.
		}
	}
	out.TenantsTotal = int64(len(tenantIDs))

	// Recount per-node tenant placements exactly, matching COUNT(DISTINCT
	// account_id) in the SQL projection.
	tenantsByNode := make(map[string]map[string]struct{})
	for _, app := range m.apps {
		if app.Status == AppDeleted || app.NodeID == "" {
			continue
		}
		if _, ok := byNode[app.NodeID]; !ok {
			continue
		}
		set := tenantsByNode[app.NodeID]
		if set == nil {
			set = make(map[string]struct{})
			tenantsByNode[app.NodeID] = set
		}
		set[app.AccountID] = struct{}{}
	}
	for nodeID, set := range tenantsByNode {
		out.Nodes[byNode[nodeID]].TenantsCount = int64(len(set))
	}

	for _, inst := range m.instances {
		if !isInstanceStateLive(inst.State) || inst.NodeID == "" {
			continue
		}
		nodeIndex, ok := byNode[inst.NodeID]
		if !ok {
			continue
		}
		node := &out.Nodes[nodeIndex]
		node.InstancesLive++
		switch inst.State {
		case instanceStateRunning:
			node.InstancesRunning++
		case instanceStateWaking:
			node.InstancesWaking++
		case instanceStateColdBooting:
			node.InstancesColdBooting++
		}
		node.RAMUsedMB += int64(inst.RAMMB) + 8
	}
	sort.Slice(out.Nodes, func(i, j int) bool {
		if out.Nodes[i].Active != out.Nodes[j].Active {
			return out.Nodes[i].Active
		}
		return out.Nodes[i].Name < out.Nodes[j].Name
	})
	return out, nil
}

// AppendEvent (pre-PR-#TBD shim) delegates to AppendEventWithTrace
// with traceID=nil. Retained for source compatibility with the
// many test doubles that only override the four-arg signature.
// The Subject parse + Data copy fixes (parseSubjectID) that landed
// on main in commit 4 of the audit-log PR are preserved — they
// live inside AppendEventWithTrace below.
func (m *MemStore) AppendEvent(ctx context.Context, actor, kind string, subject *string, data []byte) error {
	return m.AppendEventWithTrace(ctx, actor, kind, subject, data, nil)
}

// AppendEventWithTrace writes one row to the in-memory events
// mirror with an optional OTel W3C 32-char hex trace_id. The hex
// format is validated defensively at the boundary so test doubles
// cannot accept an invalid value (mirrors the migration CHECK at
// 00486 for PgStore). When traceID is nil the field is left nil.
func (m *MemStore) AppendEventWithTrace(_ context.Context, actor, kind string, subject *string, data []byte, traceID *string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var subj *uuid.UUID
	if subject != nil {
		subj = parseSubjectID(*subject)
	}
	if traceID != nil {
		if !isOTelHex32(*traceID) {
			return fmt.Errorf("state: AppendEventWithTrace: trace_id %q must match ^[0-9a-f]{32}$", *traceID)
		}
	}
	e := Event{
		ID:      int64(len(m.events) + 1),
		At:      time.Now(),
		Actor:   actor,
		Kind:    kind,
		Subject: subj,
		TraceID: traceID,
		Data:    append([]byte(nil), data...),
	}
	m.events = append(m.events, e)
	return nil
}

// isOTelHex32 returns true when s is exactly 32 lowercase hex
// characters (the OTel W3C trace-id format enforced by the
// events.trace_id / operator_intents.trace_id CHECK constraints
// at migrations/00486). Used by MemStore.AppendEventWithTrace to
// mirror the migration's invariant without a Postgres round-trip.
func isOTelHex32(s string) bool {
	if len(s) != 32 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func (m *MemStore) ListEvents(_ context.Context, subject string, limit int) ([]Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var subj *uuid.UUID
	if subject != "" {
		subj = parseSubjectID(subject)
		if subj == nil {
			// Unparseable filter — no row would have produced it,
			// return empty rather than silently matching everything.
			return nil, nil
		}
	}
	var out []Event
	for i := len(m.events) - 1; i >= 0 && (limit <= 0 || len(out) < limit); i-- {
		e := m.events[i]
		// Match either: no subject filter, OR the row's Subject
		// pointer is non-nil and equals the filter. The pre-fix
		// && false made this branch dead; tests caught it.
		if subj == nil || (e.Subject != nil && *e.Subject == *subj) {
			out = append(out, e)
		}
	}
	return out, nil
}

// InsertAuditLog (issue #755 / PR-6) appends one row to the in-memory
// audit_log mirror. The Data json.RawMessage is copied so a caller
// can reuse the input slice without aliasing the stored row. The
// append is guarded by m.mu (the rest of the audit_log surface is
// read-also-mu-guarded). When called from inside DeleteAccount
// (critical section already held by DeleteAccount itself) the
// mu.Lock here is a recursive re-entry — Go's sync.Mutex is
// non-reentrant, so DeleteAccount calls the inner appendAuditLog
// helper that does NOT re-lock. Standalone callers (handlers,
// tests) go through this method and pay the lock normally.
func (m *MemStore) InsertAuditLog(_ context.Context, entry AuditLog) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.appendAuditLogLocked(entry)
	return nil
}

// appendAuditLogLocked is the mu-held-critical-section variant of
// InsertAuditLog. Caller must hold m.mu. DeleteAccount calls this
// directly so it can stay inside its own critical section without
// deadlocking on a re-entry.
func (m *MemStore) appendAuditLogLocked(entry AuditLog) {
	var dataCopy json.RawMessage
	if len(entry.Data) > 0 {
		dataCopy = append(json.RawMessage(nil), entry.Data...)
	}
	m.auditLog = append(m.auditLog, AuditLog{
		ID:           entry.ID,
		Kind:         entry.Kind,
		AccountID:    entry.AccountID,
		AccountEmail: entry.AccountEmail,
		Actor:        entry.Actor,
		ReceivedAt:   entry.ReceivedAt,
		Data:         dataCopy,
	})
}

// ListAuditLog (issue #755 / PR-6) is the dashboard read path. Walks
// m.auditLog in reverse (newest-first) and applies the same WHERE
// semantics as pgstore.ListAuditLog: AccountID is "match-or-null",
// KindPrefix is a LIKE prefix match, Since is an inclusive lower
// bound on ReceivedAt, IncludeAnonymous gates the AccountID-is-nil
// rows. Limit defaults to 100 when zero / negative.
//
// Returned slice is a fresh copy of each AuditLog row (Data
// included) so a caller can hold it past the next store mutation.
func (m *MemStore) ListAuditLog(_ context.Context, filter AuditLogFilter) ([]AuditLog, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	// OperatorOnly takes precedence over KindPrefix at the SQL
	// layer (pgstore) and is mirrored here for shape parity.
	kindPrefix := filter.KindPrefix
	if filter.OperatorOnly {
		kindPrefix = "operator.action."
	}
	var out []AuditLog
	for i := len(m.auditLog) - 1; i >= 0; i-- {
		row := m.auditLog[i]
		if filter.AccountID != nil {
			if row.AccountID == nil || *row.AccountID != *filter.AccountID {
				continue
			}
		}
		if kindPrefix != "" && !strings.HasPrefix(row.Kind, kindPrefix) {
			continue
		}
		if !filter.Since.IsZero() && row.ReceivedAt.Before(filter.Since) {
			continue
		}
		if !filter.IncludeAnonymous && row.AccountID == nil {
			continue
		}
		if filter.ActorEmail != nil && row.AccountEmail != *filter.ActorEmail {
			continue
		}
		if filter.TargetAccountID != nil {
			// data @> jsonb_build_object('target_account_id', $N)
			// semantic — the data JSON must contain the key with
			// the requested value. We decode row.Data on the
			// in-memory path; the pgstore path is index-driven.
			if !auditLogDataHasKey(row.Data, "target_account_id", *filter.TargetAccountID) {
				continue
			}
		}
		// Defensive copy so the caller's slice doesn't alias the
		// in-memory store row.
		clone := row
		if len(row.Data) > 0 {
			clone.Data = append(json.RawMessage(nil), row.Data...)
		}
		out = append(out, clone)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// auditLogDataHasKey is the in-memory twin of the pgstore's
// data @> jsonb_build_object(...) containment query. Returns true
// when the JSONB-shaped row.Data contains the (key, value) pair.
// Returns false on any parse error — a malformed data column is
// treated as "doesn't contain the key", which is the same
// behaviour the pgstore path has (the row is excluded from the
// result set when its data->>'target_account_id' is null or
// different).
func auditLogDataHasKey(data json.RawMessage, key, want string) bool {
	if len(data) == 0 {
		return false
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return false
	}
	got, ok := m[key]
	if !ok {
		return false
	}
	s, ok := got.(string)
	return ok && s == want
}

// AppendDeploymentAudit (issue #976 / ADR-122 / SAFE-RELEASES-E.2)
// appends one row to the in-memory deployment_audit mirror. The Data
// json.RawMessage is copied so a caller can reuse the input slice
// without aliasing the stored row. The id is assigned by a monotonic
// counter so handler tests can assert the order without racing the
// identity sequence. The append is guarded by m.mu.
func (m *MemStore) AppendDeploymentAudit(_ context.Context, entry DeploymentAudit) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var dataCopy json.RawMessage
	if len(entry.Data) > 0 {
		dataCopy = append(json.RawMessage(nil), entry.Data...)
	}
	at := entry.At
	if at.IsZero() {
		at = time.Now()
	}
	id := int64(len(m.deploymentAudit) + 1)
	m.deploymentAudit = append(m.deploymentAudit, DeploymentAudit{
		ID:           id,
		DeploymentID: entry.DeploymentID,
		AccountID:    entry.AccountID,
		Kind:         entry.Kind,
		Actor:        entry.Actor,
		At:           at,
		Data:         dataCopy,
	})
	return id, nil
}

// ListDeploymentAudit (issue #976 / ADR-122 / SAFE-RELEASES-E.2)
// is the dashboard read path for the deployment_audit mirror. Walks
// m.deploymentAudit in reverse (newest-first) and filters by
// deployment_id, ordered (at DESC, id DESC) to match the pgstore
// shape. limit > 0 caps the page; <= 0 means 100 (the
// DeploymentAuditPageSizeMax default in the customer-facing handler).
//
// Returned slice is a fresh copy of each DeploymentAudit row
// (Data included) so a caller can hold it past the next store
// mutation.
func (m *MemStore) ListDeploymentAudit(_ context.Context, deploymentID string, limit int) ([]DeploymentAudit, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if limit <= 0 {
		limit = 100
	}
	// Normalise deploymentID to UUID form so callers can pass either
	// the dashed canonical form (pgstore wire shape) or the 32-hex
	// raw form (newID() output that CreateDeployment returns) and
	// the filter compares apples-to-apples against the stored
	// uuid.UUID value. The pgstore path casts $1::uuid and lets
	// Postgres handle the normalisation; the memstore path must
	// match it explicitly to keep the two stores byte-identical at
	// the response layer.
	want, err := uuid.Parse(deploymentID)
	if err != nil {
		return nil, fmt.Errorf("state: list deployment_audit: %w", err)
	}
	var out []DeploymentAudit
	for i := len(m.deploymentAudit) - 1; i >= 0; i-- {
		row := m.deploymentAudit[i]
		if row.DeploymentID != want {
			continue
		}
		clone := row
		if len(row.Data) > 0 {
			clone.Data = append(json.RawMessage(nil), row.Data...)
		}
		out = append(out, clone)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// ListEventsByWakeID (issue #517 / PR-C, ADR-064) — the
// in-memory twin of the pgstore ListEventsByWakeID sqlc query.
// Filters on the jsonb data.wake_id key (the index shape on the
// production path), orders by `at` ASC (oldest → newest) so the
// customer-facing timeline reads as a forward narrative, and
// respects the optional `since` lower bound + `limit` cap (the
// handler enforces max 1000). Insertion order is not equivalent
// to at-order: emit sites run under different locks and the
// in-memory append order interleaves the wake phases. Sort by
// `at` so the result matches the production ORDER BY at ASC.
func (m *MemStore) ListEventsByWakeID(_ context.Context, wakeID string, since time.Time, limit int) ([]Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Event
	for i := 0; i < len(m.events); i++ {
		e := m.events[i]
		if !e.At.After(since) {
			continue
		}
		// The wake_id is a key on the jsonb data blob; the
		// in-memory Event has Data as []byte. Decode lazily
		// — the per-row cost is one json.Unmarshal + map
		// lookup, amortised across the test corpus. For very
		// high-volume unit tests this would matter, but the
		// list is bounded by MemStore's test fixture sizes.
		var payload struct {
			WakeID string `json:"wake_id"`
		}
		if err := json.Unmarshal(e.Data, &payload); err != nil {
			continue
		}
		if payload.WakeID != wakeID {
			continue
		}
		out = append(out, e)
	}
	// Sort by at ASC. Stable across insertion order (the wake
	// phases are emitted at different lock depths so the
	// append order is not the same as the at-order).
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].At.Before(out[j].At)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ListAllEventsPaged (ADR-091 §3.7 / PR #3) is the in-memory twin
// of PgStore.ListAllEventsPaged. Applies the same filter
// semantics (actor / kind_prefix / subject / since) with the same
// "empty string / zero time = no filter" sentinel used by the SQL
// side. Order is (at DESC, id DESC) — same as the pgstore.
// The cap to limit is applied AFTER the sort so the most-recent
// rows are the ones that survive when the filter window is
// larger than the limit.
func (m *MemStore) ListAllEventsPaged(_ context.Context, actor, kindPrefix, subject string, since time.Time, limit int) ([]Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 100
	}
	var subjectFilter *uuid.UUID
	if subject != "" {
		parsed, err := uuid.Parse(subject)
		if err != nil {
			// Unparseable subject filter — no row would match it,
			// so return empty rather than silently matching
			// everything. Mirrors the pgstore contract: the SQL
			// `$3 = '' OR subject = $3::uuid` clause fails the
			// cast on a non-UUID string (Postgres returns 22P02
			// invalid_text_representation); returning empty here
			// keeps the two stores in lockstep on this edge.
			return nil, nil
		}
		subjectFilter = &parsed
	}
	var out []Event
	for _, e := range m.events {
		if actor != "" && e.Actor != actor {
			continue
		}
		if kindPrefix != "" && !strings.HasPrefix(e.Kind, kindPrefix) {
			continue
		}
		if subjectFilter != nil {
			if e.Subject == nil || *e.Subject != *subjectFilter {
				continue
			}
		}
		if !since.IsZero() && e.At.Before(since) {
			continue
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].At.Equal(out[j].At) {
			return out[i].ID > out[j].ID
		}
		return out[i].At.After(out[j].At)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ListRecentEventsForAccount (ADR-091 §3.7 / PR #3) is the
// per-account events drill-down. Same filter contract as the
// pgstore version. Backed by the in-memory append order so the
// partial-index shape (migrations/00099) is mirrored by the
// iteration: every event matches the actor_account_id predicate
// first, then the since floor, then the limit cap.
func (m *MemStore) ListRecentEventsForAccount(_ context.Context, actorAccountID string, since time.Time, limit int) ([]Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 100
	}
	parsed, err := uuid.Parse(actorAccountID)
	if err != nil {
		// Unparseable actor_account_id — match the pgstore
		// behaviour (empty result, no error).
		return nil, nil
	}
	var out []Event
	for _, e := range m.events {
		// Memstore's Event struct does not have actor_account_id
		// as a typed field (it lives in the jsonb data blob on
		// the pgstore side; the in-memory type carries only the
		// fields PR-3 surfaces). Decode the data payload lazily
		// to find the actor_account_id, mirroring the wake-id
		// pattern used by ListEventsByWakeID.
		var payload struct {
			ActorAccountID string `json:"actor_account_id"`
		}
		if len(e.Data) == 0 {
			continue
		}
		if err := json.Unmarshal(e.Data, &payload); err != nil {
			continue
		}
		if payload.ActorAccountID != parsed.String() {
			continue
		}
		if !since.IsZero() && e.At.Before(since) {
			continue
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].At.Equal(out[j].At) {
			return out[i].ID > out[j].ID
		}
		return out[i].At.After(out[j].At)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ListEventsBySidecar (issue #463 / ADR-069 / PR-B) is the
// sidecar-aware read-side twin of ListEventsByWakeID. Filters on
// the jsonb data.sidecar_name key AND the closed wake.kind IN
// ('wake.sidecar_init_exit', 'wake.sidecar_restart') so a query
// never returns non-sidecar rows even if a future event reuses
// the field name. Orders by at ASC so the per-sidecar timeline
// reads forward; respects the same since / limit contract as
// ListEventsByWakeID.
//
// The kind filter is the load-bearing piece: a sidecar_name key
// on a non-sidecar row would be silently returned without it,
// which would surface an unrelated event in a sidecar's audit
// view. Closed-enum filter matches the kind constants in
// pkg/events/wake.go (WakeSidecarInitExit, WakeSidecarRestart).
func (m *MemStore) ListEventsBySidecar(_ context.Context, sidecarName string, since time.Time, limit int) ([]Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Event
	for i := 0; i < len(m.events); i++ {
		e := m.events[i]
		if !e.At.After(since) {
			continue
		}
		if e.Kind != "wake.sidecar_init_exit" && e.Kind != "wake.sidecar_restart" {
			continue
		}
		var payload struct {
			SidecarName string `json:"sidecar_name"`
		}
		if err := json.Unmarshal(e.Data, &payload); err != nil {
			continue
		}
		if payload.SidecarName != sidecarName {
			continue
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].At.Before(out[j].At)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// --- Usage ------------------------------------------------------------------

// AppendUsage writes one (instance, minute) usage row and updates the
// per-(account, app, month) aggregate so UsageByMonth keeps returning the
// spec §10 shape without re-scanning the per-minute rows. Idempotent on
// (instance_id, minute) for mb_seconds / requests: a redelivered minute
// is a no-op (first write wins). Mirrors the production INSERT … ON
// CONFLICT (instance_id, minute) DO NOTHING semantics for mb_seconds
// in pgstore.go — see M7 hardening PR feat/m7-beta-hardening for the
// audit that surfaced this contract change.
//
// cpu_usec, tx_bytes, and net_tx_bytes are ADDITIVE on the same
// conflict key (issue #279 / PR-B for cpu_usec, ADR-046 for
// tx_bytes / net_tx_bytes): the schedd / meterd accumulators can
// each call AppendUsage many times within the same minute (250 ms
// cadence × ~240 ticks/minute), and the per-tick deltas need to be
// summed into the row. The pusher (meter → billing) deduplicates
// on a coarser window — the additive merge is safe end-to-end.
// See pkg/state/pgstore.go::AppendUsage,
// migrations/00055_usage_minutes_cpu.sql, and
// migrations/00065_usage_minutes_egress.sql for the production
// rationale.
func (m *MemStore) AppendUsage(_ context.Context, accountID, appID, instanceID string, minute time.Time, mbSeconds, requests, cpuUsec, txBytes, netTxBytes, netRxBytes int64, coldBootCount int32, tailSeconds int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := minute.UTC().Truncate(time.Minute)
	for i := range m.usage {
		if m.usage[i].InstanceID == instanceID && m.usage[i].Minute.Equal(key) {
			// Idempotent for mb_seconds / requests (first write wins,
			// so a restart-driven redelivery cannot inflate billing).
			// cpu_usec / tx_bytes / net_tx_bytes / net_rx_bytes /
			// cold_boot_count / tail_seconds are additive: the schedd
			// / meterd accumulators can call AppendUsage many times
			// per minute, and we sum the deltas. tail_seconds is
			// informational only — does not enter billing; pinned by
			// pkg/meter/pusher_shadow_test.go::TestPushHour_ExcludesTailSeconds.
			if m.usage[i].MBSeconds == 0 && m.usage[i].Requests == 0 {
				m.usage[i].MBSeconds = mbSeconds
				m.usage[i].Requests = requests
			}
			m.usage[i].CPUUsec += cpuUsec
			m.usage[i].TXBytes += txBytes
			m.usage[i].NetTxBytes += netTxBytes
			m.usage[i].NetRxBytes += netRxBytes
			m.usage[i].ColdBootCount += coldBootCount
			m.usage[i].TailSeconds += tailSeconds
			m.recomputeMonthLocked(accountID, appID, key)
			return nil
		}
	}
	m.usage = append(m.usage, usageMinute{
		AccountID: accountID, AppID: appID, InstanceID: instanceID,
		Minute: key, MBSeconds: mbSeconds, Requests: requests,
		CPUUsec: cpuUsec, TXBytes: txBytes, NetTxBytes: netTxBytes,
		NetRxBytes: netRxBytes, ColdBootCount: coldBootCount,
		TailSeconds: tailSeconds,
	})
	m.recomputeMonthLocked(accountID, appID, key)
	return nil
}

// AppendBuilderUsage mirrors pgstore's AppendBuilderUsage
// (ADR-048 §4). Idempotent on build_id — first write wins; a
// redelivered webhook is a no-op. The meterd rollup cron sums
// these into usage_daily.builder_seconds. seconds is wall-clock
// seconds from builds.started_at to finishedAt (matches the
// existing builderd build_duration_seconds histogram unit).
func (m *MemStore) AppendBuilderUsage(_ context.Context, accountID, appID, buildID string, finishedAt time.Time, kind string, seconds int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.builderUsage {
		if m.builderUsage[i].BuildID == buildID {
			// First write wins.
			return nil
		}
	}
	m.builderUsage = append(m.builderUsage, builderUsageRow{
		BuildID:    buildID,
		AccountID:  accountID,
		AppID:      appID,
		FinishedAt: finishedAt,
		Kind:       kind,
		Seconds:    seconds,
	})
	return nil
}

// UsageByMonth returns the per-app aggregates for one (account, month) —
// the read shape the dashboard and the meter aggregator rely on. The
// per-minute grain is internal; consumers see the rolled-up row.
func (m *MemStore) UsageByMonth(_ context.Context, accountID string, month time.Time) ([]Usage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.UTC)
	var out []Usage
	for _, u := range m.usageByMonth {
		if u.AccountID == accountID && u.Month.Equal(key) {
			out = append(out, u)
		}
	}
	return out, nil
}

// ListInvoicesForAccount mirrors pgstore's contract. Ordering is
// (period_end DESC, id DESC) — same as the SQL index. The month filter
// uses the same half-open UTC range as the SQL case. Empty when the
// account has no rows; nil and len-zero are both acceptable per the
// handler's `make([]api.Invoice, 0, len(rows))` guard.
func (m *MemStore) ListInvoicesForAccount(_ context.Context, accountID string, month *time.Time, before time.Time, limit int) ([]Invoice, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 25
	}
	var monthStart, monthEnd time.Time
	if month != nil {
		monthStart = time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.UTC)
		monthEnd = monthStart.AddDate(0, 1, 0)
	}
	var all []Invoice
	for _, inv := range m.invoices {
		if inv.AccountID != accountID {
			continue
		}
		if month != nil && (inv.PeriodEnd.Before(monthStart) || !inv.PeriodEnd.Before(monthEnd)) {
			continue
		}
		if !before.IsZero() && !inv.PeriodEnd.Before(before) {
			continue
		}
		all = append(all, inv)
	}
	sort.Slice(all, func(i, j int) bool {
		if !all[i].PeriodEnd.Equal(all[j].PeriodEnd) {
			return all[i].PeriodEnd.After(all[j].PeriodEnd)
		}
		return all[i].ID > all[j].ID
	})
	if limit > 0 && limit < len(all) {
		all = all[:limit]
	}
	return all, nil
}

// GetInvoiceByID resolves a single invoice by primary key. Mirrors
// pgstore.GetInvoiceByID: returns ErrNotFound when no row matches
// (the consumption reducer surfaces this to the apid handler as 404
// CodeNotFound). MemStore walks the in-memory map; an `invoices`
// index lookup is unnecessary at one-box scale.
func (m *MemStore) GetInvoiceByID(_ context.Context, id string) (Invoice, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, inv := range m.invoices {
		if inv.ID == id {
			return inv, nil
		}
	}
	return Invoice{}, ErrNotFound
}

// SeedInvoiceForTest is the test-only seam PR A's listInvoices
// handler tests use to plant invoice rows directly. PR B wires
// UpsertInvoice via the webhook path; until then, no production
// code calls this. Same `*_ForTest` naming as SetPastDueAtForTest
// / SetDeletionRequestedAtForTest — production-audit friendly and
// not on the Store interface, so ad-hoc writers cannot appear.
func (m *MemStore) SeedInvoiceForTest(inv Invoice) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if inv.ID == "" {
		inv.ID = "inv-" + inv.ProviderInvoiceID
	}
	if inv.Currency == "" {
		inv.Currency = "eur"
	}
	m.invoices[inv.ID] = inv
}

// --- credits (issue #279) ----------------------------------------------------

// CreateAccountCredit inserts a new operator-issued credit. Mirrors the
// pgstore body: DB assigns id (UUIDv4) and created_at; we stamp them
// in-memory so the handler can return the same shape over mock or real.
//
// MemStore does NOT validate the cents_remaining/reason CHECK constraints
// the migration enforces — that's a database concern. The handler
// validates client-side and a unit test on the pgstore body owns the
// round-trip.
func (m *MemStore) CreateAccountCredit(_ context.Context, c AccountCredit) (AccountCredit, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	m.accountCredits[c.ID] = c
	return c, nil
}

// ListAccountCredits returns the account's credit rows. onlyActive
// filters to (cents_remaining > 0) ∧ (expires_at IS NULL OR expires_at >
// now()), the active set the consumption reducer will use once it
// lands. The slice is empty (not nil) when no rows match — the
// handler's `make([]api.AccountCredit, 0, len(rows))` guard depends on
// this.
func (m *MemStore) ListAccountCredits(_ context.Context, accountID string, onlyActive bool) ([]AccountCredit, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	out := []AccountCredit{}
	for _, c := range m.accountCredits {
		if c.AccountID != accountID {
			continue
		}
		if onlyActive {
			if c.CentsRemaining <= 0 {
				continue
			}
			if c.ExpiresAt != nil && !c.ExpiresAt.After(now) {
				continue
			}
		}
		out = append(out, c)
	}
	// Stable order for deterministic tests: newest first.
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// CreateCreditLedgerEntry appends one audit row. Mirrors the pgstore
// body: DB assigns id (UUIDv4) and created_at; we stamp them in-memory
// so the returned… actually we don't return — the pgstore signature
// returns only error (the audit row is observational, not load-bearing
// for the issuance flow). MemStore matches the pgstore signature.
func (m *MemStore) CreateCreditLedgerEntry(_ context.Context, e CreditLedgerEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e.ID == "" {
		e.ID = uuid.NewString()
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	m.creditLedger = append(m.creditLedger, e)
	return nil
}

// ListActiveCreditsForConsumption returns the account's active credit
// rows ordered FIFO (created_at ASC) for the consumption reducer
// (issue #279 PR-C). Mirrors the (cents_remaining > 0) ∧ (expires_at
// IS NULL OR expires_at > now()) active-set predicate of
// ListAccountCredits(onlyActive=true) but sorts ASC because the
// reducer drains oldest credit first. The slice is empty (not nil)
// when no rows match — the handler's `make([]api.AccountCredit, 0,
// len(rows))` guard depends on this.
func (m *MemStore) ListActiveCreditsForConsumption(_ context.Context, accountID string) ([]AccountCredit, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	out := []AccountCredit{}
	for _, c := range m.accountCredits {
		if c.AccountID != accountID {
			continue
		}
		if c.CentsRemaining <= 0 {
			continue
		}
		if c.ExpiresAt != nil && !c.ExpiresAt.After(now) {
			continue
		}
		out = append(out, c)
	}
	// FIFO — oldest credit first. The reducer's whole correctness
	// story hangs on this sort, so it's documented here rather than
	// in the interface comment: a future "newest first" optimisation
	// must keep the reducer on ASC.
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// ConsumeAccountCredit performs an atomic FIFO decrement across the
// account's active credits, capped at TargetCents. The MemStore
// implementation holds m.mu across the loop so a concurrent
// operator issuance (which also takes m.mu) cannot interleave
// between the read and the decrement; a concurrent reducer call
// serialises behind us on the same mutex and observes the post-
// state.
//
// Idempotency mirrors the pgstore partial unique index
// (provider_invoice_id, credit_id) WHERE provider_invoice_id IS NOT
// NULL: a second call with the same ProviderInvoiceID and the same
// credit set sees every INSERT skipped (the seen map already has
// the (invoice, credit) pair) and returns AlreadyConsumedForInvoice
// = true with the original ConsumedCents re-derived from the existing
// ledger rows.
//
// "Atomic" here means: the per-credit check
// (cents_remaining >= amount) cannot return a row that would have
// driven cents_remaining negative — MemStore enforces this
// defensively (the migration's CHECK is the production floor).

// sumActive returns the sum of cents_remaining across all active
// (non-zero, non-expired) credits for the account. Caller must hold
// m.mu. Used to populate RemainingCreditsCents in the response and
// to seed the idempotency guard's idempotent ConsumedCents re-derive.
func (m *MemStore) sumActive(accountID string, now time.Time) int64 {
	var sum int64
	for _, c := range m.accountCredits {
		if c.AccountID != accountID {
			continue
		}
		if c.CentsRemaining <= 0 {
			continue
		}
		if c.ExpiresAt != nil && !c.ExpiresAt.After(now) {
			continue
		}
		sum += c.CentsRemaining
	}
	return sum
}

func (m *MemStore) ConsumeAccountCredit(_ context.Context, p ConsumeAccountCreditParams) (ConsumeAccountCreditResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if p.TargetCents == 0 {
		return ConsumeAccountCreditResult{}, nil
	}
	if p.ProviderInvoiceID == "" {
		return ConsumeAccountCreditResult{}, fmt.Errorf("ConsumeAccountCredit: ProviderInvoiceID required (the partial unique index needs a non-null dedupe key)")
	}

	// Idempotency guard: if any consumption ledger row already exists
	// for this invoice, the invoice was already drained in a prior
	// call. Return the same ConsumedCents and mark
	// AlreadyConsumedForInvoice=true so a webhook re-fire / admin
	// replay is a no-op. This is stronger than the per-(invoice,
	// credit) pair protection — it's a "one consumption event per
	// invoice" contract. Without this guard, a third credit issued
	// AFTER the first call would be drained on replay, double-
	// decrementing the active credit balance.
	priorCents := int64(0)
	priorCreditIDs := make(map[string]struct{})
	for _, le := range m.creditLedger {
		if le.ProviderInvoiceID != nil && *le.ProviderInvoiceID == p.ProviderInvoiceID {
			if le.DeltaCents < 0 {
				priorCents += -le.DeltaCents
			}
			priorCreditIDs[le.CreditID] = struct{}{}
		}
	}
	if priorCents > 0 {
		// Re-derive ConsumedCents from the prior rows so the operator
		// sees the same total across calls (PgStore mirrors this).
		remaining := m.sumActive(p.AccountID, time.Now().UTC())
		return ConsumeAccountCreditResult{
			ConsumedCents:             priorCents,
			RemainingCreditsCents:     remaining,
			AlreadyConsumedForInvoice: true,
		}, nil
	}

	now := time.Now().UTC()
	// Build FIFO list of active credits (mirror
	// ListActiveCreditsForConsumption — keep these two in lockstep so
	// the parity tests don't drift).
	active := []AccountCredit{}
	for _, c := range m.accountCredits {
		if c.AccountID != p.AccountID {
			continue
		}
		if c.CentsRemaining <= 0 {
			continue
		}
		if c.ExpiresAt != nil && !c.ExpiresAt.After(now) {
			continue
		}
		active = append(active, c)
	}
	sort.Slice(active, func(i, j int) bool { return active[i].CreatedAt.Before(active[j].CreatedAt) })

	// Idempotency map: (provider_invoice_id, credit_id) → first-seen
	// delta. Built up-front from existing ledger rows so a second
	// call for the same invoice recognises the prior consumption and
	// skips the decrement.
	seen := make(map[string]int64)
	for _, le := range m.creditLedger {
		if le.ProviderInvoiceID == nil || *le.ProviderInvoiceID != p.ProviderInvoiceID {
			continue
		}
		key := *le.ProviderInvoiceID + "\x00" + le.CreditID
		if _, ok := seen[key]; !ok {
			seen[key] = le.DeltaCents
		}
	}

	res := ConsumeAccountCreditResult{}
	remaining := p.TargetCents
	anyInserted := false
	for _, c := range active {
		if remaining == 0 {
			break
		}
		amount := c.CentsRemaining
		if amount > remaining {
			amount = remaining
		}

		key := p.ProviderInvoiceID + "\x00" + c.ID
		if _, alreadyConsumed := seen[key]; alreadyConsumed {
			// This (invoice, credit) pair was already drained in a
			// prior call — skip the decrement and the ledger insert.
			continue
		}

		newBalance := c.CentsRemaining - amount
		// Defensive parity check — the migration's
		// `cents_remaining >= 0` CHECK is the production floor, but
		// MemStore is reachable in tests that don't go through the
		// migration. Fail loud so the unit test surfaces the bug.
		if newBalance < 0 {
			return ConsumeAccountCreditResult{}, fmt.Errorf("ConsumeAccountCredit: race drove cents_remaining negative (credit=%s, amount=%d, prior=%d)", c.ID, amount, c.CentsRemaining)
		}

		// Apply the decrement. The conditional `if amount > 0` is
		// belt-and-suspenders: amount is always >= 1 here (the loop
		// break on `remaining == 0` happens first, and the newBalance
		// check rejects amount > CentsRemaining).
		if amount > 0 {
			c.CentsRemaining = newBalance
			m.accountCredits[c.ID] = c
			invID := p.ProviderInvoiceID
			m.creditLedger = append(m.creditLedger, CreditLedgerEntry{
				ID:                uuid.NewString(),
				AccountID:         p.AccountID,
				CreditID:          c.ID,
				DeltaCents:        -amount,
				Reason:            p.Reason,
				Actor:             p.Actor,
				CreatedAt:         now,
				ProviderInvoiceID: &invID,
			})
			seen[key] = -amount
			res.PerCredit = append(res.PerCredit, ConsumedCreditRow{
				CreditID:   c.ID,
				DeltaCents: -amount,
				NewBalance: newBalance,
			})
			res.ConsumedCents += amount
			remaining -= amount
			anyInserted = true
		}
	}

	if !anyInserted && p.TargetCents > 0 {
		// Every (invoice, credit) pair was already drained. The
		// original ConsumedCents for this invoice is the sum of the
		// seen entries — re-derive it so the operator sees the same
		// total regardless of which call they inspect.
		for _, delta := range seen {
			if delta < 0 {
				res.ConsumedCents += -delta
			}
		}
		res.AlreadyConsumedForInvoice = true
	}

	// Sum of remaining active credits after the call.
	res.RemainingCreditsCents = m.sumActive(p.AccountID, now)

	return res, nil
}

// GetAccountOverageCapCents returns (cents, ok, nil). ok=false means
// the column is NULL (no cap configured). 0 with ok=true means "no
// overage allowed"; >0 is the explicit monthly ceiling in cents.
// MemStore mirrors the pgstore signature: a hand-written read against
// the `accounts.overage_cap_cents` column populated by the migration.
func (m *MemStore) GetAccountOverageCapCents(_ context.Context, accountID string) (int64, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cents, ok := m.overageCapCents[accountID]
	return cents, ok, nil
}

// LoadAllOverageCapCents returns the bulk read used by meterd's
// quota tick. Mirrors PgStore.LoadAllOverageCapCents: cap-bearing
// accounts only, missing-cap accounts are dropped (the caller treats
// them as "no cap"). The returned map is a copy so the meterd loop
// can iterate without holding m.mu.
func (m *MemStore) LoadAllOverageCapCents(_ context.Context) (map[string]int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]int64, len(m.overageCapCents))
	for id, c := range m.overageCapCents {
		out[id] = c
	}
	return out, nil
}

// CurrentMonthOverageCents sums the account's usage_minutes.mb_seconds
// from the UTC month start and converts to integer cents. Formula:
// 1 GB-h = 3600 GB-seconds; at €0.01/GB-h → 1 GB-h = 100 cents.
// Integer math only — never float on money (CLAUDE.md).
//
// Mirrors pgstore.CurrentMonthOverageCents: the scan is O(rows-in-month)
// which on a one-box is bounded. The `now` argument is the caller's
// current time so a test can pin "month start" to a fixture.
func (m *MemStore) CurrentMonthOverageCents(_ context.Context, accountID string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.clock()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	var mbSeconds int64
	for _, u := range m.usage {
		if u.AccountID != accountID {
			continue
		}
		if u.Minute.Before(monthStart) {
			continue
		}
		mbSeconds += u.MBSeconds
	}
	return mbSeconds * 100 / 3600, nil
}

// UpdateAccountOverageCapCents writes accounts.overage_cap_cents for
// the given account. Pass nil → delete the map key (NULL round-trip
// in pgstore); pass *non-nil → store the cents. Issue #561's
// raiseOverageCap endpoint uses this for the customer self-service
// "set / clear cap" surface. Mirrors the pgstore UPDATE shape — the
// (cents=0, ok=true) case is preserved so the workload gate sees
// "cap = 0" as a refusal trigger, distinct from "no cap" (ok=false).
func (m *MemStore) UpdateAccountOverageCapCents(_ context.Context, accountID string, cents *int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cents == nil {
		delete(m.overageCapCents, accountID)
		return nil
	}
	m.overageCapCents[accountID] = *cents
	return nil
}

// SetOverageCapCentsForTest is the test-only seam that plants a cap
// value before the meterd tick runs. Not on the Store interface —
// production code never needs to write the cap; the column is set by
// a future operator surface (out of scope for issue #279 PR A).
func (m *MemStore) SetOverageCapCentsForTest(accountID string, cents int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.overageCapCents[accountID] = cents
}

// SetClockForTest replaces the wall-clock source
// CurrentMonthOverageCents uses to compute the UTC month-start
// cutoff. Tests that pin a fixture time for AppendUsage (so the
// derived overage is computable at a deterministic monthly bucket)
// MUST also pin the same fixture here, otherwise real wall-clock
// drifts past the fixture's month and the usage row is filtered as
// "before monthStart" (issue surfaced 2026-08-01 on
// TestRunQuotaOnce_OverageCapHonored + TestRunQuotaOnce_OverageCapAtCap).
// Not on the Store interface — production code never needs to
// override the clock; PgStore's date_trunc('month', now()) reads
// the SQL `now()`, which the test harness can't substitute cleanly
// without a clock-rewind transaction wrapper.
func (m *MemStore) SetClockForTest(clock func() time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	m.clock = clock
}

// AppendCreditForTest is the test-only seam that plants a credit row
// directly, mirroring SeedInvoiceForTest. Used by the meterd cap
// tests so the test fixture doesn't have to round-trip through
// CreateAccountCredit.
func (m *MemStore) AppendCreditForTest(c AccountCredit) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	m.accountCredits[c.ID] = c
}

// ListCreditLedgerForTest returns a snapshot of the in-memory
// credit_ledger rows for an account. Test-only seam — mirrors the
// SeedInvoiceForTest / AppendCreditForTest pattern. The Store
// interface deliberately does NOT expose a ledger read (the dashboard
// doesn't need it; the reducer writes but doesn't read its own rows).
// MemStore tests use this to assert post-consumption ledger state
// without standing up Postgres.
func (m *MemStore) ListCreditLedgerForTest(accountID string) []CreditLedgerEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]CreditLedgerEntry, 0)
	for _, le := range m.creditLedger {
		if le.AccountID == accountID {
			out = append(out, le)
		}
	}
	return out
}

// UsageByHour returns the per-app usage rows whose minute ∈ [start, end).
// The Stripe pusher calls this hourly; MemStore synthesizes the per-hour
// rollup from the per-minute rows on the fly — matches what PgStore would
// do in SQL.
func (m *MemStore) UsageByHour(_ context.Context, accountID string, start, end time.Time) ([]Usage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	type hourAgg struct {
		AccountID  string
		AppID      string
		MBSeconds  int64
		Requests   int64
		CPUUsec    int64
		TXBytes    int64
		NetTxBytes int64
	}
	bucket := map[appHourKey]hourAgg{}
	for _, u := range m.usage {
		if u.AccountID != accountID {
			continue
		}
		if u.Minute.Before(start) || !u.Minute.Before(end) {
			continue
		}
		k := appHourKey{AccountID: u.AccountID, AppID: u.AppID}
		a := bucket[k]
		a.AccountID = u.AccountID
		a.AppID = u.AppID
		a.MBSeconds += u.MBSeconds
		a.Requests += u.Requests
		a.CPUUsec += u.CPUUsec
		a.TXBytes += u.TXBytes
		a.NetTxBytes += u.NetTxBytes
		bucket[k] = a
	}
	out := make([]Usage, 0, len(bucket))
	for _, a := range bucket {
		out = append(out, Usage{
			AccountID: a.AccountID, AppID: a.AppID,
			Month: start, MBSeconds: a.MBSeconds, Requests: a.Requests,
			CPUUsec: a.CPUUsec, TXBytes: a.TXBytes, NetTxBytes: a.NetTxBytes,
		})
	}
	return out, nil
}

// UsageDaily mirrors pgstore.UsageDaily. The MemStore does not maintain
// the rollup table — usage_daily is a cron-populated rollup, and the
// in-process unit-test surface for the rollup loop uses stubExecer
// instead. Returns nil + nil so handlers do not crash on dev/host
// builds where Postgres is not wired.
func (m *MemStore) UsageDaily(_ context.Context, _ string, _ time.Time) ([]DailyUsage, error) {
	return nil, nil
}

// UsageSLOForApp + UsageSLOForAccount mirror pgstore for the
// customer-facing SLO surface (issue #696 / ADR-082). The
// MemStore does not maintain usage_minutes — pkg/meter
// wires directly to PgStore in production. Returns (0, 0, nil)
// so the handler treats the SLO panel as "empty" without
// turning the response degraded (Prometheus may still be
// reachable).
func (m *MemStore) UsageSLOForApp(_ context.Context, _, _ string, _, _ time.Time) (float64, float64, error) {
	return 0, 0, nil
}

func (m *MemStore) UsageSLOForAccount(_ context.Context, _ string, _, _ time.Time) (float64, float64, error) {
	return 0, 0, nil
}

// AppendSnapshotStorage + StorageUsage mirror pgstore for the storage
// rollup (ADR-049 §B.3). The MemStore does not maintain the rollup —
// pkg/meter/storage.go wires directly to PgStore in production. Returns
// nil so handlers do not crash on dev/host builds.
func (m *MemStore) AppendSnapshotStorage(_ context.Context, _, _ string, _ time.Time, _, _ int64) error {
	return nil
}

func (m *MemStore) StorageUsage(_ context.Context, _ string, _ time.Time) ([]StorageUsage, error) {
	return nil, nil
}

// LatestSnapshotBytes mirrors pgstore for the storage rollup
// (ADR-049 §B.3). The MemStore does not maintain a snapshot set;
// returns (0, 0, nil) so the rollup writes a zero-byte day rather
// than crashing. Tests that exercise the rollup's write path use
// PgStore against a real Postgres (migrations/00070_snapshot_storage_daily_test.go).
func (m *MemStore) LatestSnapshotBytes(_ context.Context, _ string) (int64, int64, error) {
	return 0, 0, nil
}

// HasStripePushHour + RecordStripePushHour implement the pkg/billing/stripe
// PushDedupe interface. The MemStore keeps a flat set keyed by
// (account, hour); PgStore keeps a dedicated table.
func (m *MemStore) HasStripePushHour(_ context.Context, accountID string, hour time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.stripePushHours[stripePushKey{accountID: accountID, hour: hour.UTC()}]
	return ok, nil
}

func (m *MemStore) RecordStripePushHour(_ context.Context, accountID string, hour time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stripePushHours == nil {
		m.stripePushHours = map[stripePushKey]struct{}{}
	}
	m.stripePushHours[stripePushKey{accountID: accountID, hour: hour.UTC()}] = struct{}{}
	return nil
}

// HasPaddleOverageMonth + RecordPaddleOverageMonth implement the
// pkg/billing/paddle PaddleOverageDedupe interface. Mirrors the Stripe
// pair one method above: flat set keyed by (account, month); UTC-
// normalized on every read/write so a future caller that forgets to
// normalize cannot create a phantom row. `month` is a calendar-month
// start (calendarMonthStart in pkg/billing/paddle/usage.go).
func (m *MemStore) HasPaddleOverageMonth(_ context.Context, accountID string, month time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.paddleOverageMonths[paddleOverageKey{accountID: accountID, month: month.UTC()}]
	return ok, nil
}

func (m *MemStore) RecordPaddleOverageMonth(_ context.Context, accountID string, month time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.paddleOverageMonths == nil {
		m.paddleOverageMonths = map[paddleOverageKey]struct{}{}
	}
	m.paddleOverageMonths[paddleOverageKey{accountID: accountID, month: month.UTC()}] = struct{}{}
	return nil
}

// PaddleOverageDedupeSchema reports the in-memory mirror of the
// paddle_overage_dedupe table. Mirrors the pgstore probe but does
// not query information_schema — the memstore can't lie about
// schema, only about contents. We report TableExists=true iff
// paddleOverageWindows is non-empty (an init memstore has
// nothing); on a fresh memstore this returns the "table missing"
// surface the pre-flight maps to the 00034-then-00041 hint.
//
// Notably the probe ignores paddleOverageMonths: that map is
// populated by the legacy monthly dedupe path (RecordPaddleOverageMonth),
// which is *not* what the B4 pre-flight is guarding. The pre-flight
// exists to certify the per-window pusher (ClaimPaddleOverageWindow /
// CompletePaddleOverageWindow) is wired to a table that has the
// 00041 columns. Months-only rows in a memstore would mean the
// window pusher has never been exercised — emitting a green "table
// exists, columns=ok" for that path would lie to the operator.
//
// The Has* flags are hardcoded to true once paddleOverageWindows has
// any rows — the in-process map tracks all four columns by
// construction, so any memstore that has window rows "has" the
// 00041 shape. This matches production semantics: a memstore
// without those columns cannot exist by definition.
func (m *MemStore) PaddleOverageDedupeSchema(_ context.Context) (PaddleOverageDedupeSchemaResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out PaddleOverageDedupeSchemaResult
	if len(m.paddleOverageWindows) > 0 {
		out.TableExists = true
		out.HasWindowStart = true
		out.HasState = true
		out.HasClaimedAt = true
		out.HasClaimedBy = true
	}
	for _, r := range m.paddleOverageWindows {
		if r.completed {
			out.CompletedRows++
		} else {
			out.PendingRows++
		}
	}
	return out, nil
}

// CheckWebhookReplay returns true when (provider, delivery_id) has a
// dedupe row whose received_at >= cutoff. The MemStore drops
// expired rows inline at read time (PgStore has the apid sweep
// goroutine for the equivalent background reaping); this keeps the
// in-memory map size bounded under long test runs. Returns false
// on an absent row OR on an expired row (caller's `cutoff` is
// computed as now()-TTL by pkg/webhookdedupe).
func (m *MemStore) CheckWebhookReplay(_ context.Context, provider, deliveryID string, cutoff time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.webhookDeliveries == nil {
		return false, nil
	}
	expiresAt, ok := m.webhookDeliveries[webhookDeliveryKey{provider: provider, deliveryID: deliveryID}]
	if !ok || expiresAt.UTC().Before(cutoff.UTC()) {
		return false, nil
	}
	return true, nil
}

// RecordWebhookDelivery inserts (or refreshes) the dedupe row. The
// value is the expires_at timestamp so CheckWebhookReplay can
// answer TTL-correctly. ON CONFLICT DO UPDATE in the PgStore is
// mirrored by an unconditional map write here.
func (m *MemStore) RecordWebhookDelivery(_ context.Context, provider, deliveryID string, expiresAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.webhookDeliveries == nil {
		m.webhookDeliveries = map[webhookDeliveryKey]time.Time{}
	}
	m.webhookDeliveries[webhookDeliveryKey{provider: provider, deliveryID: deliveryID}] = expiresAt.UTC()
	return nil
}

// SweepExpiredWebhookDeliveries drops any dedupe row whose
// expires_at is older than `now`. Returns the number of rows
// removed. Mirrors the apid sweep goroutine's Postgres DELETE.
func (m *MemStore) SweepExpiredWebhookDeliveries(_ context.Context, now time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.webhookDeliveries == nil {
		return 0, nil
	}
	var swept int64
	cutoff := now.UTC()
	for k, expiresAt := range m.webhookDeliveries {
		if expiresAt.UTC().Before(cutoff) {
			delete(m.webhookDeliveries, k)
			swept++
		}
	}
	return swept, nil
}

// ClaimPaddleOverageWindow mirrors PgStore.ClaimPaddleOverageWindow
// against the in-memory map. Three branches:
//
//   - row absent: insert a pending claim, return claimed=true.
//   - row pending + claimed_at within lease: another pod holds it;
//     return claimed=false without mutating state.
//   - row pending + claimed_at older than lease: steal the claim,
//     refresh claimed_at + claimed_by, return claimed=true.
//   - row completed: refresh the claim as a fresh pending row so
//     the caller can re-POST (the underlying SQL upsert in
//     PgStore handles this by re-INSERT with state='completed'
//     then UPDATE; in-memory we just flip to pending).
//
// The lock is held for the full check-then-mutate because the
// MemStore is single-process; PgStore relies on the SQL engine's
// atomic UPDATE for the same guarantee.
func (m *MemStore) ClaimPaddleOverageWindow(_ context.Context, accountID string, windowStart time.Time, claimedBy string, lease time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.paddleOverageWindows == nil {
		m.paddleOverageWindows = map[paddleOverageWindowKey]paddleOverageClaimState{}
	}
	key := paddleOverageWindowKey{accountID: accountID, windowStart: windowStart.UTC()}
	now := time.Now().UTC()
	state, ok := m.paddleOverageWindows[key]
	if !ok {
		m.paddleOverageWindows[key] = paddleOverageClaimState{
			claimedBy: claimedBy,
			claimedAt: now,
		}
		return true, nil
	}
	// Row exists. Either completed (re-claim path) or pending
	// (race path; only steal if stale).
	if !state.completed && now.Sub(state.claimedAt) < lease {
		// Fresh pending claim from another pod — skip.
		return false, nil
	}
	state.claimedBy = claimedBy
	state.claimedAt = now
	m.paddleOverageWindows[key] = state
	return true, nil
}

// CompletePaddleOverageWindow mirrors PgStore.CompletePaddleOverageWindow.
// A foreign caller (one whose lease expired and the row was
// reaped+re-claimed) sees no state change because the state is no
// longer in 'pending' under its claimed_by — but we don't error
// because the terminal state is already correct (someone else
// completed).
//
// Field-name note: `mbSecondsSum` here mirrors the production
// column `pushed_mb_seconds` (migration 00280). The Sum suffix
// predates the 00280 rename and is kept for backwards compatibility
// with the existing memstore test fixtures; the value is the
// last-completed window's integer stamp, not a cumulative sum.
func (m *MemStore) CompletePaddleOverageWindow(_ context.Context, accountID string, windowStart time.Time, mbSeconds int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.paddleOverageWindows == nil {
		return nil
	}
	key := paddleOverageWindowKey{accountID: accountID, windowStart: windowStart.UTC()}
	state, ok := m.paddleOverageWindows[key]
	if !ok {
		// No row → caller skipped Claim. No-op.
		return nil
	}
	now := time.Now().UTC()
	state.completed = true
	state.pushedAt = now
	state.mbSecondsSum = mbSeconds
	m.paddleOverageWindows[key] = state
	return nil
}

// ReapStalePaddleOverageClaims mirrors PgStore.ReapStalePaddleOverageClaims.
// Resets pending rows whose claimed_at is older than olderThan,
// returning them to the claimable pool. Returns the count reset
// (informational).
func (m *MemStore) ReapStalePaddleOverageClaims(_ context.Context, olderThan time.Duration) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.paddleOverageWindows == nil {
		return 0, nil
	}
	now := time.Now().UTC()
	n := 0
	for k, state := range m.paddleOverageWindows {
		if !state.completed && now.Sub(state.claimedAt) >= olderThan {
			delete(m.paddleOverageWindows, k)
			n++
		}
	}
	return n, nil
}

// SetPaddleOverageClaimForTest fabricates a claim row at the given
// state. Used by the reap/foreign-claim tests to plant rows that the
// production API alone cannot construct (a stale, foreign-owned claim
// with no recent activity). Not invoked by production code; documents
// the seam so a future refactor can rename the internal map without
// rewriting the test contract.
func (m *MemStore) SetPaddleOverageClaimForTest(accountID string, windowStart time.Time, claimedBy string, claimedAt time.Time, completed bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.paddleOverageWindows == nil {
		m.paddleOverageWindows = map[paddleOverageWindowKey]paddleOverageClaimState{}
	}
	m.paddleOverageWindows[paddleOverageWindowKey{accountID: accountID, windowStart: windowStart.UTC()}] = paddleOverageClaimState{
		claimedBy: claimedBy, claimedAt: claimedAt, completed: completed,
	}
}

type appHourKey struct {
	AccountID string
	AppID     string
}

// recomputeMonthLocked rebuilds the (account, app, month) aggregate from
// every per-minute row that falls in the calendar month. Called under m.mu
// from AppendUsage; the slice scan is O(rows-in-month) which on a one-box
// stays bounded (one minute × max_concurrency(plan) × apps).
func (m *MemStore) recomputeMonthLocked(accountID, appID string, minute time.Time) {
	month := time.Date(minute.Year(), minute.Month(), 1, 0, 0, 0, 0, time.UTC)
	var mbSec, req, cpuUsec, txBytes, netTxBytes, netRxBytes int64
	var coldBoots int32
	for _, u := range m.usage {
		if u.AccountID != accountID || u.AppID != appID {
			continue
		}
		if u.Minute.Year() != month.Year() || u.Minute.Month() != month.Month() {
			continue
		}
		mbSec += u.MBSeconds
		req += u.Requests
		cpuUsec += u.CPUUsec
		txBytes += u.TXBytes
		netTxBytes += u.NetTxBytes
		netRxBytes += u.NetRxBytes
		coldBoots += u.ColdBootCount
	}
	// Drop the existing row for this (account, app, month) if any, then append.
	for i := range m.usageByMonth {
		if m.usageByMonth[i].AccountID == accountID &&
			m.usageByMonth[i].AppID == appID &&
			m.usageByMonth[i].Month.Equal(month) {
			m.usageByMonth = append(m.usageByMonth[:i], m.usageByMonth[i+1:]...)
			break
		}
	}
	m.usageByMonth = append(m.usageByMonth, Usage{
		AccountID: accountID, AppID: appID, Month: month,
		MBSeconds: mbSec, Requests: req, CPUUsec: cpuUsec,
		TXBytes: txBytes, NetTxBytes: netTxBytes,
		NetRxBytes: netRxBytes, ColdBootCount: int64(coldBoots),
	})
}

// --- Idempotency ------------------------------------------------------------

func (m *MemStore) GetIdempotent(_ context.Context, accountID, key string) (int, []byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.idem[accountID+"\x00"+key]
	if !ok || time.Since(e.created) > 24*time.Hour {
		return 0, nil, ErrNotFound
	}
	return e.status, e.body, nil
}

func (m *MemStore) PutIdempotent(_ context.Context, accountID, key string, status int, body []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.idem[accountID+"\x00"+key] = idemEntry{status: status, body: append([]byte(nil), body...), created: time.Now()}
	return nil
}

// intOrZero dereferences a *int UpdateAppParams field, treating nil
// as 0. The nil sentinel means "don't touch the column" — the
// SetX bit (e.g. SetIdleTimeout) gates whether this value is even
// applied, so the 0 default only matters when the Set bit is on
// AND the caller passed nil (a misuse). Used by both pgstore (for
// SQL UPDATE args) and memstore (for in-memory App copy); kept
// here because memstore is the canonical home for the field-shape
// helpers (the unit-test surface).
func intOrZero(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// boolOrFalse is the *bool counterpart to intOrZero, used by both
// stores for the SetStreamingEnabled UpdateAppParams field. Same
// nil-means-zero-value contract: pgstore only consults this value
// when SetStreamingEnabled is on, so a nil pair there is harmless.
func boolOrFalse(p *bool) bool {
	if p == nil {
		return false
	}
	return *p
}

// IssueLoginToken stores a magic-link token hash → account_id mapping
// with the given expiry. The hash is the SHA-256 of the raw token
// (32-byte hex); see pkg/api.HashAPIKey for the canonical hash fn.
// Re-issue of the same hash is a no-op (the entry is overwritten).
func (m *MemStore) IssueLoginToken(_ context.Context, tokenHash []byte, accountID string, expiresAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.loginTokens == nil {
		m.loginTokens = map[string]LoginToken{}
	}
	m.loginTokens[string(tokenHash)] = LoginToken{
		TokenHash: append([]byte(nil), tokenHash...),
		AccountID: accountID,
		ExpiresAt: expiresAt,
	}
	return nil
}

// ConsumeLoginToken marks the token consumed in a single critical
// section and returns the bound account_id. A replay returns
// ErrNotFound. Expired tokens also return ErrNotFound (we don't leak
// whether the token was real-but-stale vs never-existed).
func (m *MemStore) ConsumeLoginToken(_ context.Context, tokenHash []byte) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tok, ok := m.loginTokens[string(tokenHash)]
	if !ok {
		return "", ErrNotFound
	}
	if tok.ConsumedAt != nil {
		delete(m.loginTokens, string(tokenHash))
		return "", ErrNotFound
	}
	if !tok.ExpiresAt.After(time.Now()) {
		delete(m.loginTokens, string(tokenHash))
		return "", ErrNotFound
	}
	now := time.Now()
	tok.ConsumedAt = &now
	m.loginTokens[string(tokenHash)] = tok
	return tok.AccountID, nil
}

// DeleteOldLoginTokens prunes tokens whose expires_at < before, even
// if they were never consumed. Returns the number removed. Used by
// the maintenance job (or a test cleanup hook).
func (m *MemStore) DeleteOldLoginTokens(_ context.Context, before time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var removed int64
	for k, tok := range m.loginTokens {
		if tok.ExpiresAt.Before(before) {
			delete(m.loginTokens, k)
			removed++
		}
	}
	return removed, nil
}

// DeleteOldEvents (ADR-075) prunes audit-log events whose `at` is
// older than the cutoff. Mirrors the PgStore shape so tests can
// drive the in-memory twin of the daily retention loop without
// spinning up Postgres. Returns the number removed.
//
// Allocates a fresh slice rather than reusing the backing array
// in place — concurrent AppendEvent callers would otherwise see
// a half-trimmed slice. The cost is one allocation per tick (once
// per day), so it's not on the hot path.
//
// Used by pkg/eventretention's daily loop; the maintenance floor
// is 90 days (SOC 2 CC6.2).
func (m *MemStore) DeleteOldEvents(_ context.Context, before time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := make([]Event, 0, len(m.events))
	var removed int64
	for _, e := range m.events {
		if e.At.Before(before) {
			removed++
			continue
		}
		kept = append(kept, e)
	}
	m.events = kept
	return removed, nil
}

// SetAccountPassword upserts the Argon2id PHC hash for an account.
// One row per account_id — the PK rejects a duplicate INSERT, so a
// racing concurrent SetAccountPassword against the same account
// would lose at the database floor; the MemStore upsert mirrors that
// by overwriting the prior row inside the lock.
//
// phc is the PHC wire format produced by pkg/auth.Encode. The MemStore
// stores it verbatim; pkg/auth.Verify parses the embedded m/t/p at
// verify time so a future parameter bump is transparent.
//
// UpdatedAt is stamped here so the "rotate hash on login" PR #2.5
// hardening has a stable reference. Defaulted to time.Now() when the
// caller passes a zero hash; the zero hash is otherwise a programming
// error and surfaces as a returned error.
func (m *MemStore) SetAccountPassword(_ context.Context, accountID, phc string) error {
	if accountID == "" {
		return ErrInvalidArgument
	}
	if phc == "" {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.accountPasswords == nil {
		m.accountPasswords = map[string]AccountPassword{}
	}
	m.accountPasswords[accountID] = AccountPassword{
		AccountID: accountID,
		Hash:      phc,
		UpdatedAt: time.Now(),
	}
	return nil
}

// AccountPasswordByAccountID returns the stored Argon2id PHC hash
// for an account, or ErrNotFound when no row exists. The postLogin
// handler uses ErrNotFound as the trigger for the anti-enumeration
// Argon2id pad (pkg/auth.DummyPHC) so the response time on unknown
// email matches the response time on known email + wrong password.
func (m *MemStore) AccountPasswordByAccountID(_ context.Context, accountID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.accountPasswords[accountID]
	if !ok {
		return "", ErrNotFound
	}
	return row.Hash, nil
}

// DeleteAccountPassword removes the Argon2id row for an account.
// Idempotent: deleting a row that doesn't exist is not an error
// (matches the pgx Exec semantics — a DELETE with zero affected
// rows returns nil). Used by the G6 hard-delete path's cleanup
// hooks and reserved for a future "switch to OAuth-only" opt-out.
func (m *MemStore) DeleteAccountPassword(_ context.Context, accountID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.accountPasswords, accountID)
	return nil
}

// UpsertOAuthLink writes the (provider, provider_subject) → account
// row. The §11 anti-takeover invariant is the database composite PK
// in Postgres; the MemStore mirrors it via the (provider+"\x00"+sub)
// map key — a second UpsertOAuthLink with the SAME provider/sub
// overwrites in place (same account re-binding, e.g. an email change
// refreshes the link's email field), and a second UpsertOAuthLink
// with a DIFFERENT account_id but the SAME provider/sub returns
// ErrConflict — that's the in-memory equivalent of Postgres' PK
// rejection. The OAuth callback (handlers_google.go / handlers_github.go)
// relies on this so a stolen email cannot bind to a second account.
func (m *MemStore) UpsertOAuthLink(_ context.Context, accountID, provider, providerSubject, email string, emailVerified bool) error {
	if accountID == "" || provider == "" || providerSubject == "" {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.oauthLinks == nil {
		m.oauthLinks = map[string]OAuthLink{}
	}
	key := provider + "\x00" + providerSubject
	if existing, ok := m.oauthLinks[key]; ok && existing.AccountID != accountID {
		return ErrConflict
	}
	now := time.Now()
	if existing, ok := m.oauthLinks[key]; ok {
		now = existing.CreatedAt // preserve CreatedAt on update
	}
	m.oauthLinks[key] = OAuthLink{
		Provider:        provider,
		ProviderSubject: providerSubject,
		AccountID:       accountID,
		Email:           email,
		EmailVerified:   emailVerified,
		CreatedAt:       now,
	}
	return nil
}

// OAuthLinkByProviderSubject returns the link for a (provider, sub)
// pair, or ErrNotFound when no row matches. The OAuth callback runs
// this on every handshake; the sub-first lookup is the §11
// anti-takeover closure (the first party to bind a sub owns the row).
func (m *MemStore) OAuthLinkByProviderSubject(_ context.Context, provider, providerSubject string) (OAuthLink, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.oauthLinks[provider+"\x00"+providerSubject]
	if !ok {
		return OAuthLink{}, ErrNotFound
	}
	return row, nil
}

// IssueCliAuthCode stores a freshly-minted code's SHA-256 hash with
// no account binding (AccountID empty). The hash key format matches
// loginTokens (the binary []byte hash used as a string key). A
// re-issue of the same hash is a no-op overwriting the entry, which
// matches the production on-conflict-do-nothing semantics.
func (m *MemStore) IssueCliAuthCode(_ context.Context, tokenHash []byte, expiresAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cliAuthCodes == nil {
		m.cliAuthCodes = map[string]CliAuthCode{}
	}
	m.cliAuthCodes[string(tokenHash)] = CliAuthCode{
		TokenHash: append([]byte(nil), tokenHash...),
		ExpiresAt: expiresAt,
	}
	return nil
}

// PeekCliAuthCode returns the row's status without mutating it. Used
// by the dashboard GET /cli-auth render to decide whether to show the
// email-input form or the "code unavailable" error page.
func (m *MemStore) PeekCliAuthCode(_ context.Context, tokenHash []byte) (api.CliAuthStatus, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.cliAuthCodes[string(tokenHash)]
	if !ok {
		return api.CliAuthStatusExpired, "", ErrNotFound
	}
	if !row.ExpiresAt.After(time.Now()) {
		return api.CliAuthStatusExpired, "", ErrNotFound
	}
	if row.ConsumedAt != nil {
		return api.CliAuthStatusConsumed, row.AccountID, nil
	}
	return api.CliAuthStatusPending, row.AccountID, nil
}

// ClaimCliAuthCode atomically transitions pending → consumed and
// binds account_id. Error shapes mirror PgStore (review finding F5):
//
//	ErrNotFound  — row missing OR expired (never minted or TTL passed)
//	ErrConflict  — row exists but already claimed by a prior call
//
// The CAS-equivalent for MemStore is the m.mu serializing all
// readers/writers; the second concurrent caller observes the
// first's write (AccountID != "") and returns ErrConflict.
//
// IMPORTANT: this MUST NOT touch ConsumedAt — that field is the
// exclusive mint-gate for ConsumeCliAuthCode. Pre-setting it here
// would short-circuit the CAS that the CLI's exchange relies on to
// mint exactly one API key per code (review finding F4).
func (m *MemStore) ClaimCliAuthCode(_ context.Context, tokenHash []byte, accountID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.cliAuthCodes[string(tokenHash)]
	if !ok {
		return ErrNotFound
	}
	if !row.ExpiresAt.After(time.Now()) {
		return ErrNotFound
	}
	if row.AccountID != "" {
		// A prior claim already bound the row to some account_id.
		// Dashboard POST never re-claims, so this is either a retry
		// (user double-clicked) or a parallel claim race. Either
		// way the row is no longer pending → ErrConflict so the
		// handler can render "Code already used".
		return ErrConflict
	}
	row.AccountID = accountID
	m.cliAuthCodes[string(tokenHash)] = row
	return nil
}

// ConsumeCliAuthCode is the CLI's poll-side read PLUS mint gate.
// Atomic CAS (mirrors ConsumeLoginToken): only mutates consumed_at
// on the FIRST call, returns the bound account_id exactly once.
// A buggy or replaying CLI cannot mint multiple keys for the same
// code (review finding F4).
//
// Filter: `account_id` must be non-empty (Claim must have run
// first) — without it the row is still pending and the CLI should
// keep polling, not see (Consumed,"") which would mint a key for an
// unbound account.
//
// Return contract:
//
//	pending (or empty account_id) → (Pending,  "",       nil)        keep polling
//	consumed (first call)        → (Consumed, acct_id,  nil)        mint API key
//	replay / expired / unknown    → (Expired, "",        ErrNotFound)
func (m *MemStore) ConsumeCliAuthCode(_ context.Context, tokenHash []byte) (api.CliAuthStatus, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.cliAuthCodes[string(tokenHash)]
	if !ok {
		return api.CliAuthStatusExpired, "", ErrNotFound
	}
	if !row.ExpiresAt.After(time.Now()) {
		return api.CliAuthStatusExpired, "", ErrNotFound
	}
	if row.AccountID == "" || row.ConsumedAt != nil {
		// Either still pending (dashboard hasn't claimed yet) or
		// already consumed (replay). The caller distinguishes via
		// the consumed_at nil check.
		if row.ConsumedAt != nil {
			return api.CliAuthStatusExpired, "", ErrNotFound
		}
		return api.CliAuthStatusPending, "", nil
	}
	now := time.Now()
	row.ConsumedAt = &now
	m.cliAuthCodes[string(tokenHash)] = row
	return api.CliAuthStatusConsumed, row.AccountID, nil
}

// AppendDeploymentLog records one line of build output. Returns the
// assigned seq (monotonic per deployment). MemStore mimics the
// Postgres bigserial cursor so cursor pagination (`seq < before`)
// works the same as production.
func (m *MemStore) AppendDeploymentLog(_ context.Context, deploymentID, stream, line string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deploymentSeq[deploymentID]++
	seq := m.deploymentSeq[deploymentID]
	if m.deploymentLogs == nil {
		m.deploymentLogs = map[string][]LogEntry{}
	}
	m.deploymentLogs[deploymentID] = append(m.deploymentLogs[deploymentID], LogEntry{
		DeploymentID: deploymentID,
		Seq:          seq,
		Stream:       stream,
		Line:         line,
		WrittenAt:    time.Now().UTC(),
	})
	return seq, nil
}

// ListDeploymentLogs returns the page of rows whose seq < beforeSeq
// (zero → all rows), in DESC seq order, capped at limit. hasMore is
// true when there are older rows still to fetch (rows == limit AND
// there's at least one more behind it).
func (m *MemStore) ListDeploymentLogs(_ context.Context, deploymentID string, beforeSeq int64, limit int) ([]LogEntry, bool, error) {
	if limit <= 0 {
		limit = 50
	}
	limit = clampLogLimit(limit)
	m.mu.Lock()
	defer m.mu.Unlock()
	all := m.deploymentLogs[deploymentID]
	if len(all) == 0 {
		return nil, false, nil
	}
	// Walk backwards (highest seq first) so the page is newest-first
	// regardless of insert order — matches production's ORDER BY seq DESC.
	out := make([]LogEntry, 0, limit)
	olderRemaining := false
	for i := len(all) - 1; i >= 0; i-- {
		e := all[i]
		if beforeSeq > 0 && e.Seq >= beforeSeq {
			continue
		}
		if len(out) >= limit {
			// Page is full. Stop only when we've also confirmed
			// there's at least one row behind us we'd otherwise
			// have included. Older rows survive any older iteration
			// (i > 0 and a row at i-1 with seq < beforeSeq).
			for j := i - 1; j >= 0; j-- {
				if beforeSeq == 0 || all[j].Seq < beforeSeq {
					olderRemaining = true
					break
				}
			}
			break
		}
		out = append(out, e)
	}
	return out, olderRemaining, nil
}

// --- customer secrets (spec §11/G2) -----------------------------------------
//
// Mirror of the PgStore implementations above. Plaintext VALUES never enter
// the MemStore — callers (apid handlers, schedd) pass ciphertext only. The
// MemStore's role is to model the (account_id, app_id, key) row shape + the
// ownership checks so unit tests can verify quota / list / delete logic
// without touching Postgres.

// UpsertAppSecret inserts or replaces the (account_id, app_id,
// scope='default', key) row (ADR-092 PR-A).
// updated_at is bumped on every call so schedd's wake staging
// observes a fresh mtime even when the ciphertext is identical
// (rotation flows re-seal with the same plaintext).
//
// ADR-089 PR-A: preserves the pre-PR-A wire shape (no kid stamp)
// for backward compatibility. New callers use
// UpsertAppSecretWithKid (or UpsertAppSecretWithKidInScope for
// non-default scope).
func (m *MemStore) UpsertAppSecret(ctx context.Context, accountID, appID, key string, ciphertext []byte) error {
	return m.UpsertAppSecretInScope(ctx, accountID, appID, DefaultEnvScope, key, ciphertext)
}

// UpsertAppSecretWithKid is the kid-stamping sibling of
// UpsertAppSecret (ADR-089 PR-A). Mirrors the pgstore impl — see
// pkg/state/pgstore.go::UpsertAppSecretWithKid for the rationale.
// Hardcodes scope='default' (PR-A); use
// UpsertAppSecretWithKidInScope for any other scope.
func (m *MemStore) UpsertAppSecretWithKid(ctx context.Context, accountID, appID, key, kid string, ciphertext []byte) error {
	return m.UpsertAppSecretWithKidInScope(ctx, accountID, appID, DefaultEnvScope, key, kid, ciphertext)
}

// UpsertAppSecretInScope is the scope-aware sibling of
// UpsertAppSecret (ADR-092 PR-A). Mirrors the pgstore impl's
// `ON CONFLICT (app_id, scope, key) DO UPDATE` semantics: an
// existing row at the (app_id, scope, key) tuple is replaced and
// updated_at bumped.
func (m *MemStore) UpsertAppSecretInScope(_ context.Context, accountID, appID, scope, key string, ciphertext []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := secretKey{AppID: appID, Scope: scope, Key: key}
	existing, ok := m.secrets[k]
	now := time.Now()
	if !ok {
		m.secrets[k] = AppSecret{
			AccountID: accountID, AppID: appID, Scope: scope, Key: key,
			Ciphertext: ciphertext, CreatedAt: now, UpdatedAt: now,
		}
		return nil
	}
	if existing.AccountID != accountID {
		return ErrNotFound
	}
	existing.Ciphertext = ciphertext
	existing.UpdatedAt = now
	m.secrets[k] = existing
	return nil
}

// UpsertAppSecretWithKidInScope is the kid-stamping scope-aware
// sibling (ADR-092 PR-A). Mirrors UpsertAppSecretInScope but
// stamps kid alongside ciphertext (see ADR-089 PR-A for the
// kid semantics).
func (m *MemStore) UpsertAppSecretWithKidInScope(_ context.Context, accountID, appID, scope, key, kid string, ciphertext []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := secretKey{AppID: appID, Scope: scope, Key: key}
	existing, ok := m.secrets[k]
	now := time.Now()
	if !ok {
		m.secrets[k] = AppSecret{
			AccountID: accountID, AppID: appID, Scope: scope, Key: key,
			Ciphertext: ciphertext, Kid: kid, CreatedAt: now, UpdatedAt: now,
		}
		return nil
	}
	if existing.AccountID != accountID {
		return ErrNotFound
	}
	existing.Ciphertext = ciphertext
	existing.Kid = kid
	existing.UpdatedAt = now
	m.secrets[k] = existing
	return nil
}

// UpsertAppSecretWithKidAndValueHashInScope is the value-hash
// scope-aware sibling (ADR-117 env-diff matrix, PR-C). Mirrors
// UpsertAppSecretWithKidInScope but stamps both kid and
// value_hash alongside ciphertext. Empty valueHash is stored as
// the zero-value (matches the SQL `NULLIF($7, ”)` for the
// pgstore sibling) so pre-PR-C callers preserve their prior
// behavior.
func (m *MemStore) UpsertAppSecretWithKidAndValueHashInScope(_ context.Context, accountID, appID, scope, key, kid, valueHash string, ciphertext []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := secretKey{AppID: appID, Scope: scope, Key: key}
	existing, ok := m.secrets[k]
	now := time.Now()
	if !ok {
		m.secrets[k] = AppSecret{
			AccountID: accountID, AppID: appID, Scope: scope, Key: key,
			Ciphertext: ciphertext, Kid: kid, ValueHash: valueHash,
			CreatedAt: now, UpdatedAt: now,
		}
		return nil
	}
	if existing.AccountID != accountID {
		return ErrNotFound
	}
	existing.Ciphertext = ciphertext
	existing.Kid = kid
	existing.ValueHash = valueHash
	existing.UpdatedAt = now
	m.secrets[k] = existing
	return nil
}

// GetAppSecret returns the (account_id, app_id,
// scope='default', key) row.
// Returns ErrNotFound when no row matches — same semantics as
// PgStore so the rotate handler (PR-B) renders 400
// CodeSecretNotFound for missing keys. Use GetAppSecretInScope for
// non-default scopes.
func (m *MemStore) GetAppSecret(ctx context.Context, accountID, appID, key string) (*AppSecret, error) {
	return m.GetAppSecretInScope(ctx, accountID, appID, DefaultEnvScope, key)
}

// DeleteAppSecret removes the (account_id, app_id,
// scope='default', key) row. Returns ErrNotFound when no row
// matches — same semantics as PgStore so the handler renders 400
// CodeSecretNotFound. Use DeleteAppSecretInScope for non-default
// scopes.
func (m *MemStore) DeleteAppSecret(ctx context.Context, accountID, appID, key string) error {
	return m.DeleteAppSecretInScope(ctx, accountID, appID, DefaultEnvScope, key)
}

// GetAppSecretInScope is the scope-aware sibling of GetAppSecret
// (ADR-092 PR-A). Returns the (app_id, scope, key) row scoped to
// accountID. Returns ErrNotFound when no row matches.
func (m *MemStore) GetAppSecretInScope(_ context.Context, accountID, appID, scope, key string) (*AppSecret, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := secretKey{AppID: appID, Scope: scope, Key: key}
	row, ok := m.secrets[k]
	if !ok || row.AccountID != accountID {
		return nil, ErrNotFound
	}
	cp := row
	return &cp, nil
}

// DeleteAppSecretInScope is the scope-aware sibling of
// DeleteAppSecret (ADR-092 PR-A).
func (m *MemStore) DeleteAppSecretInScope(_ context.Context, accountID, appID, scope, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := secretKey{AppID: appID, Scope: scope, Key: key}
	row, ok := m.secrets[k]
	if !ok || row.AccountID != accountID {
		return ErrNotFound
	}
	delete(m.secrets, k)
	return nil
}

// ListAppSecretsForRekey is the global paginated walk consumed by
// pkg/rekey.Replayer.Run (ADR-089 PR-A, widened to 4-tuple in
// ADR-092 PR-A). Order is (account_id ASC, app_id ASC, scope ASC,
// key ASC) so the cursor walk is monotonic across the
// (account_id, app_id, scope, key) primary-key order — see
// pkg/state/pgstore.go::ListAppSecretsForRekey for the SQL
// counterpart.
//
// CURSOR FORMAT: "<account_id>|<app_id>|<scope>|<key>" — the
// 4-tuple form. The 3-tuple "<account_id>|<app_id>|<key>" form
// (pre-PR) is still accepted on decode: a 2-pipe cursor is
// treated as scope="default" so an in-flight Replayer that
// persisted a pre-PR LastID continues to work after the rollout
// without operator intervention. Once the operator's first
// post-PR Run completes, RekeyProgress.LastID is upgraded to the
// 4-tuple form and the lazy fallback is no longer reached.
func (m *MemStore) ListAppSecretsForRekey(_ context.Context, limit int, cursor string) ([]AppSecret, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var curAcct, curApp, curScope, curKey string
	if cursor != "" {
		// Prefer 4-tuple; fall back to 3-tuple (pre-PR).
		parts := strings.SplitN(cursor, "|", 4)
		switch len(parts) {
		case 4:
			curAcct, curApp, curScope, curKey = parts[0], parts[1], parts[2], parts[3]
		case 3:
			curAcct, curApp, curKey = parts[0], parts[1], parts[2]
			curScope = DefaultEnvScope
		default:
			return nil, fmt.Errorf("memstore: malformed rekey cursor %q", cursor)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var all []AppSecret
	for _, s := range m.secrets {
		if curAcct != "" {
			cmp := strings.Compare(s.AccountID, curAcct)
			if cmp < 0 ||
				(cmp == 0 && (s.AppID < curApp ||
					(s.AppID == curApp && (s.Scope < curScope ||
						(s.Scope == curScope && s.Key <= curKey))))) {
				continue
			}
		}
		all = append(all, s)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].AccountID != all[j].AccountID {
			return all[i].AccountID < all[j].AccountID
		}
		if all[i].AppID != all[j].AppID {
			return all[i].AppID < all[j].AppID
		}
		if all[i].Scope != all[j].Scope {
			return all[i].Scope < all[j].Scope
		}
		return all[i].Key < all[j].Key
	})
	if len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}

// ListAppSecretsInScope is the scope-aware sibling of
// ListAppSecrets (ADR-092 PR-A). Returns every (key, ciphertext,
// kid, timestamps) row on the app where scope matches the
// caller-supplied value, scoped to accountID. Order: by scope
// ASC, key ASC for deterministic wake staging.
func (m *MemStore) ListAppSecretsInScope(_ context.Context, accountID, appID, scope string) ([]AppSecret, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AppSecret
	for _, s := range m.secrets {
		if s.AppID != appID || s.AccountID != accountID || s.Scope != scope {
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// ListAppSecrets returns every secret on the app where scope =
// 'default', scoped to accountID. Order: by key ASC for
// deterministic wake staging (matches PgStore ORDER BY).
// Returns nil slice (not error) when the app has no secrets —
// schedd treats that as "no env file to write". Use
// ListAppSecretsInScope for non-default scopes, or
// ListAllAppSecrets for the cross-scope enumeration the GET
// ?scope=__all__ handler renders.
func (m *MemStore) ListAppSecrets(ctx context.Context, accountID, appID string) ([]AppSecret, error) {
	return m.ListAppSecretsInScope(ctx, accountID, appID, DefaultEnvScope)
}

// ListAllAppSecrets is the cross-scope mirror of ListAppSecrets
// (ADR-092 PR-A). Returns every secret row on the app across all
// scopes, scoped to accountID. Order: by scope ASC, key ASC.
// Used by apid's GET /v1/apps/{slug}/secrets?scope=__all__ arm
// (PR-B) to render the nested secrets_by_scope response shape.
func (m *MemStore) ListAllAppSecrets(_ context.Context, accountID, appID string) ([]AppSecret, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AppSecret
	for _, s := range m.secrets {
		if s.AppID != appID || s.AccountID != accountID {
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Scope != out[j].Scope {
			return out[i].Scope < out[j].Scope
		}
		return out[i].Key < out[j].Key
	})
	return out, nil
}

// ListAppSecretsForAccount is the account-scoped mirror of
// ListAppSecrets (issue #393). The cursor is the (app_slug, key)
// pair — encoded as "<slug>|<key>" by the handler. Order is
// (app_slug ASC, key ASC) to match the SQL ORDER BY. Slugs are
// charset-validated upstream so the "|" separator is unambiguous.
func (m *MemStore) ListAppSecretsForAccount(_ context.Context, accountID string, limit int, before string) ([]AccountAppSecret, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	slugOf := make(map[string]string, len(m.apps))
	for _, a := range m.apps {
		slugOf[a.ID] = a.Slug
	}
	var beforeSlug, beforeKey string
	if before != "" {
		parts := strings.SplitN(before, "|", 2)
		beforeSlug, beforeKey = parts[0], parts[1]
	}
	var out []AccountAppSecret
	for _, s := range m.secrets {
		if s.AccountID != accountID {
			continue
		}
		slug, ok := slugOf[s.AppID]
		if !ok {
			continue
		}
		if before != "" {
			if slug < beforeSlug || (slug == beforeSlug && s.Key <= beforeKey) {
				continue
			}
		}
		out = append(out, AccountAppSecret{
			AccountID:  s.AccountID,
			AppID:      s.AppID,
			AppSlug:    slug,
			Key:        s.Key,
			Scope:      s.Scope,
			Ciphertext: s.Ciphertext,
			ValueHash:  s.ValueHash,
			CreatedAt:  s.CreatedAt,
			UpdatedAt:  s.UpdatedAt,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AppSlug != out[j].AppSlug {
			return out[i].AppSlug < out[j].AppSlug
		}
		return out[i].Key < out[j].Key
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// CountAppSecrets is the quota helper. Mirrors PgStore.CountAppSecrets.
func (m *MemStore) CountAppSecrets(_ context.Context, accountID, appID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, s := range m.secrets {
		if s.AppID == appID && s.AccountID == accountID {
			n++
		}
	}
	return n, nil
}

// --- per-app private-registry Basic Auth (issue #461 / ADR-062) -------------
//
// Mirror of the customer-secrets surface (lines 4479-4544) keyed by
// (app_id, registry) instead of (app_id, key). Same ownership
// semantics: row carries account_id for the cross-account ErrNotFound
// predicate in Get and the GDPR/G6 cascade in DeleteAccount.
//
// The ciphertext payload (password) is age-sealed at the handler
// layer; MemStore treats it as opaque bytes and never inspects,
// formats, or logs it.

// UpsertAppRegistryCredential inserts or replaces the
// (account_id, app_id, registry) row. updated_at is bumped on every
// call so imaged's MarkAppRegistryCredentialUsed check sees a fresh
// mtime; created_at is preserved across re-puts (same shape as
// UpsertAppSecret).
//
// Note: MemStore intentionally does NOT validate that appID belongs
// to accountID (the FK lives in SQL, not the in-memory check). The
// existing UpsertAppSecret follows the same posture; cross-account
// staging surfaces via Get/List/delete, which DO check ownership.
func (m *MemStore) UpsertAppRegistryCredential(_ context.Context, accountID, appID, registry, username string, passwordEncrypted []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := registryCredKey{AppID: appID, Registry: registry}
	now := time.Now().UTC()
	existing, ok := m.registryCreds[k]
	if !ok {
		m.registryCreds[k] = AppRegistryCredential{
			AccountID:         accountID,
			AppID:             appID,
			Registry:          registry,
			Username:          username,
			PasswordEncrypted: passwordEncrypted,
			CreatedAt:         now,
			UpdatedAt:         now,
		}
		return nil
	}
	existing.Username = username
	existing.PasswordEncrypted = passwordEncrypted
	existing.UpdatedAt = now
	m.registryCreds[k] = existing
	return nil
}

// GetAppRegistryCredential returns the row for (app_id, registry).
// Returns ErrNotFound if the row doesn't exist or the
// (accountID, appID) ownership doesn't match — defense in depth so a
// stale ID→slug mapping can't cross accounts.
func (m *MemStore) GetAppRegistryCredential(_ context.Context, accountID, appID, registry string) (AppRegistryCredential, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.registryCreds[registryCredKey{AppID: appID, Registry: registry}]
	if !ok || row.AccountID != accountID || row.AppID != appID {
		return AppRegistryCredential{}, ErrNotFound
	}
	return row, nil
}

// ListAppRegistryCredentials returns every (registry, username,
// ciphertext) row on the app, ordered by registry ASC for
// deterministic wire output. The handler renders registry +
// username + timestamps only — ciphertext stays server-side.
func (m *MemStore) ListAppRegistryCredentials(_ context.Context, accountID, appID string) ([]AppRegistryCredential, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]AppRegistryCredential, 0)
	for _, r := range m.registryCreds {
		if r.AccountID == accountID && r.AppID == appID {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Registry < out[j].Registry })
	return out, nil
}

// DeleteAppRegistryCredential removes the (app_id, registry) row.
// Returns ErrNotFound if the row doesn't exist or ownership doesn't
// match — mirrors DeleteAppSecret's 400-by-design posture.
func (m *MemStore) DeleteAppRegistryCredential(_ context.Context, accountID, appID, registry string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := registryCredKey{AppID: appID, Registry: registry}
	row, ok := m.registryCreds[k]
	if !ok || row.AccountID != accountID || row.AppID != appID {
		return ErrNotFound
	}
	delete(m.registryCreds, k)
	return nil
}

// CountAppRegistryCredentials is the quota check helper. Mirrors
// PgStore.CountAppRegistryCredentials.
func (m *MemStore) CountAppRegistryCredentials(_ context.Context, accountID, appID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, r := range m.registryCreds {
		if r.AppID == appID && r.AccountID == accountID {
			n++
		}
	}
	return n, nil
}

// RegistryCredentialQuotaCheck collapses the count + exists probe
// into one walk over the map. Mirrors
// PgStore.RegistryCredentialQuotaCheck — same (n, exists) shape.
func (m *MemStore) RegistryCredentialQuotaCheck(_ context.Context, accountID, appID, registry string) (int, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	exists := false
	for k, r := range m.registryCreds {
		if r.AppID != appID || r.AccountID != accountID {
			continue
		}
		n++
		if k.Registry == registry {
			exists = true
		}
	}
	return n, exists, nil
}

// MarkAppRegistryCredentialUsed updates last_used_at + updated_at to
// now(). Returns ErrNotFound if the row doesn't exist or ownership
// doesn't match — callers MUST treat ErrNotFound as non-fatal (the
// deployment already succeeded; missing-on-cascade is an expected
// race with account/app delete).
func (m *MemStore) MarkAppRegistryCredentialUsed(_ context.Context, accountID, appID, registry string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := registryCredKey{AppID: appID, Registry: registry}
	row, ok := m.registryCreds[k]
	if !ok || row.AccountID != accountID || row.AppID != appID {
		return ErrNotFound
	}
	now := time.Now().UTC()
	row.LastUsedAt = &now
	row.UpdatedAt = now
	m.registryCreds[k] = row
	return nil
}

// --- app env vars (issue #395 / ADR-045) -------------------------------------
//
// Mirror of the customer-secrets surface (lines 4479-4544) minus the
// ciphertext column. Plaintext values live only in this map; never
// logged by MemStore.

// UpsertAppEnv inserts or replaces the (account_id, app_id, scope=
// 'default', key) row. updated_at is bumped on every call so schedd's
// wake staging observes a fresh mtime on every write.
//
// ADR-090 PR-A: hardcodes scope='default' at the map key site. Use
// UpsertAppEnvInScope for non-default scopes.
func (m *MemStore) UpsertAppEnv(_ context.Context, accountID, appID, key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	scope := "default"
	k := envKey{AppID: appID, Scope: scope, Key: key}
	existing, ok := m.envs[k]
	now := time.Now()
	if !ok {
		m.envs[k] = AppEnv{AccountID: accountID, AppID: appID, Scope: scope, Key: key, Value: value, CreatedAt: now, UpdatedAt: now}
		return nil
	}
	if existing.AccountID != accountID {
		return ErrNotFound
	}
	existing.Value = value
	existing.UpdatedAt = now
	m.envs[k] = existing
	return nil
}

// DeleteAppEnv removes the (account_id, app_id, scope='default', key)
// row. Returns ErrNotFound when no row matches — same semantics as
// PgStore so the handler renders 400 CodeEnvVarNotFound.
//
// ADR-090 PR-A: hardcodes scope='default' (see UpsertAppEnv).
func (m *MemStore) DeleteAppEnv(_ context.Context, accountID, appID, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := envKey{AppID: appID, Scope: "default", Key: key}
	row, ok := m.envs[k]
	if !ok || row.AccountID != accountID {
		return ErrNotFound
	}
	delete(m.envs, k)
	return nil
}

// ListAppEnv returns every env row on the app where scope='default',
// scoped to accountID. Order: by scope ASC, key ASC (matches
// PgStore ORDER BY).
func (m *MemStore) ListAppEnv(_ context.Context, accountID, appID string) ([]AppEnv, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AppEnv
	for _, e := range m.envs {
		if e.AppID != appID || e.AccountID != accountID || e.Scope != "default" {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Scope != out[j].Scope {
			return out[i].Scope < out[j].Scope
		}
		return out[i].Key < out[j].Key
	})
	return out, nil
}

// CountAppEnv is the quota helper. Counts ALL scope values for the
// app per ADR-090 D6 (EnvVarsMax is per-app, not per-scope). Mirrors
// PgStore.CountAppEnv.
func (m *MemStore) CountAppEnv(_ context.Context, accountID, appID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, e := range m.envs {
		if e.AppID == appID && e.AccountID == accountID {
			n++
		}
	}
	return n, nil
}

// UpsertAppEnvInScope is the scope-aware sibling of UpsertAppEnv.
// Mirrors the shape of the pgstore implementation so handler tests
// exercise the same composite-key shape the production store
// enforces.
func (m *MemStore) UpsertAppEnvInScope(_ context.Context, accountID, appID, scope, key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := envKey{AppID: appID, Scope: scope, Key: key}
	existing, ok := m.envs[k]
	now := time.Now()
	if !ok {
		m.envs[k] = AppEnv{AccountID: accountID, AppID: appID, Scope: scope, Key: key, Value: value, CreatedAt: now, UpdatedAt: now}
		return nil
	}
	if existing.AccountID != accountID {
		return ErrNotFound
	}
	existing.Value = value
	existing.UpdatedAt = now
	m.envs[k] = existing
	return nil
}

// DeleteAppEnvInScope is the scope-aware sibling of DeleteAppEnv.
func (m *MemStore) DeleteAppEnvInScope(_ context.Context, accountID, appID, scope, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := envKey{AppID: appID, Scope: scope, Key: key}
	row, ok := m.envs[k]
	if !ok || row.AccountID != accountID {
		return ErrNotFound
	}
	delete(m.envs, k)
	return nil
}

// ListAppEnvInScope is the scope-aware sibling of ListAppEnv. Order:
// by scope ASC, key ASC for deterministic staging.
func (m *MemStore) ListAppEnvInScope(_ context.Context, accountID, appID, scope string) ([]AppEnv, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AppEnv
	for _, e := range m.envs {
		if e.AppID != appID || e.AccountID != accountID || e.Scope != scope {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Scope != out[j].Scope {
			return out[i].Scope < out[j].Scope
		}
		return out[i].Key < out[j].Key
	})
	return out, nil
}

// CountAppEnvInScope is the scope-aware sibling of CountAppEnv.
// Reserved for future per-scope caps (ADR-091 follow-up); PR-A does
// not call it.
func (m *MemStore) CountAppEnvInScope(_ context.Context, accountID, appID, scope string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, e := range m.envs {
		if e.AppID == appID && e.AccountID == accountID && e.Scope == scope {
			n++
		}
	}
	return n, nil
}

// ListAllAppEnv returns every env row on the app across all scopes,
// scoped to accountID. Order: by scope ASC, key ASC (mirrors the
// pgstore ORDER BY so handler tests see the same wire shape).
// Used by apid's GET /v1/apps/{slug}/envs?scope=__all__ arm
// (ADR-090 PR-B / D3) to render the nested `env_by_scope` response
// shape.
func (m *MemStore) ListAllAppEnv(_ context.Context, accountID, appID string) ([]AppEnv, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AppEnv
	for _, e := range m.envs {
		if e.AppID != appID || e.AccountID != accountID {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Scope != out[j].Scope {
			return out[i].Scope < out[j].Scope
		}
		return out[i].Key < out[j].Key
	})
	return out, nil
}

// --- app trusted cosign signers (issue #472 / ADR-054) -----------------------
//
// MemStore mirrors the PgStore trusted-signer CRUD so handler tests
// exercise the same shape the production store enforces. Same posture as
// the app_secrets/app_envs parity clusters above.

// UpsertAppTrustedSigner inserts or replaces the (app_id, signer_name)
// row. On conflict only cosign_public_key + added_by_account_id refresh;
// AddedAt stays at the original write so the audit trail distinguishes
// "created" from "rotated" (matches PgStore.on conflict do update).
//
// Returns (addedAt, rotated, err). Rotated=true means this was an
// update on an existing row (audit emits app.trusted_signer_rotated);
// rotated=false means this was a fresh insert. The addedAt returned
// is the original write timestamp, preserved across rotations.
// Mirrors PgStore's `(xmax = 0)` detection — there is no second
// SELECT race.
func (m *MemStore) UpsertAppTrustedSigner(_ context.Context, accountID, appID, signerName string, pubKey []byte, addedByAccountID string) (time.Time, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := trustedSignerKey{AppID: appID, SignerName: signerName}
	if existing, ok := m.trustedSigners[k]; ok {
		if existing.AccountID != accountID {
			return time.Time{}, false, ErrNotFound
		}
		existing.CosignPublicKey = pubKey
		existing.AddedByAccountID = addedByAccountID
		m.trustedSigners[k] = existing
		return existing.AddedAt, true, nil
	}
	now := time.Now()
	m.trustedSigners[k] = AppTrustedSigner{
		AccountID:        accountID,
		AppID:            appID,
		SignerName:       signerName,
		CosignPublicKey:  pubKey,
		AddedAt:          now,
		AddedByAccountID: addedByAccountID,
	}
	return now, false, nil
}

// DeleteAppTrustedSigner removes the (app_id, signer_name) row scoped to
// accountID. Returns ErrNotFound when no row matches — handler renders
// 404 CodeTrustedSignerNotFound.
func (m *MemStore) DeleteAppTrustedSigner(_ context.Context, accountID, appID, signerName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := trustedSignerKey{AppID: appID, SignerName: signerName}
	row, ok := m.trustedSigners[k]
	if !ok || row.AccountID != accountID {
		return ErrNotFound
	}
	delete(m.trustedSigners, k)
	return nil
}

// ListAppTrustedSigners returns every trusted-signer row on the app,
// scoped to accountID. Order: by signer_name ASC (matches PgStore
// ORDER BY). Returns nil slice when the app has no trusted signers.
func (m *MemStore) ListAppTrustedSigners(_ context.Context, accountID, appID string) ([]AppTrustedSigner, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AppTrustedSigner
	for _, r := range m.trustedSigners {
		if r.AppID != appID || r.AccountID != accountID {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SignerName < out[j].SignerName })
	return out, nil
}

// ListAppTrustedSignersForApp is the system-side sibling of
// ListAppTrustedSigners: takes only appID. Used by the on-disk
// mirror writer (cmd/apid/trusted_publisher_writer.go). Same
// order (signer_name ASC) and shape as the accountID-scoped
// sibling.
func (m *MemStore) ListAppTrustedSignersForApp(_ context.Context, appID string) ([]AppTrustedSigner, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AppTrustedSigner
	for _, r := range m.trustedSigners {
		if r.AppID != appID {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SignerName < out[j].SignerName })
	return out, nil
}

// CountAppTrustedSigners is the quota helper. Mirrors PgStore.CountAppTrustedSigners.
func (m *MemStore) CountAppTrustedSigners(_ context.Context, accountID, appID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, r := range m.trustedSigners {
		if r.AppID == appID && r.AccountID == accountID {
			n++
		}
	}
	return n, nil
}

// --- G6 account self-service (spec §17 G6, ADR-021) -------------------------
//
// MemStore mirrors PgStore for the G6 endpoints so handler tests
// exercise the same shape the production store enforces. The grace
// window lives in state.DeletionGraceDuration (the MemStore enforces
// the same constant the production timer uses).

// DeletionGraceDuration returns the customer-visible grace window the
// customer has to restore their account. MemStore and PgStore share
// the constant so handler tests don't drift from production behavior.
func DeletionGraceDuration() time.Duration { return 30 * 24 * time.Hour }

// DeleteAccount walks the FK graph in dependency order under a single
// m.mu lock. The dependency order matches the PgStore tx so a redelivered
// grace tick finds the same idempotent answer.
func (m *MemStore) DeleteAccount(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[id]
	if !ok {
		return ErrNotFound
	}
	// Conditional-delete mirrors the PgStore SQL `WHERE id=$1 AND
	// status='deleted_pending'`. We refuse to delete a row that's not
	// in deleted_pending so the restore→tick race closes identically
	// to the production path: if RestoreAccount flipped the row back
	// to active in between ListAllAccounts and DeleteAccount, the
	// grace timer gets ErrNotFound and swallows it.
	if a.Status != AccountDeletedPending {
		return ErrNotFound
	}
	// Drop children first so the parent's final delete is the sentinel.
	for k := range m.secrets {
		if m.secrets[k].AccountID == id {
			delete(m.secrets, k)
		}
	}
	for k := range m.registryCreds {
		if m.registryCreds[k].AccountID == id {
			delete(m.registryCreds, k)
		}
	}
	for k := range m.envs {
		if m.envs[k].AccountID == id {
			delete(m.envs, k)
		}
	}
	for domain, d := range m.domains {
		if app, ok := m.apps[d.AppID]; ok && app.AccountID == id {
			delete(m.domains, domain)
		}
	}
	for cid, c := range m.crons {
		if app, ok := m.apps[c.AppID]; ok && app.AccountID == id {
			delete(m.crons, cid)
		}
	}
	for iid, ins := range m.instances {
		if app, ok := m.apps[ins.AppID]; ok && app.AccountID == id {
			delete(m.instances, iid)
		}
	}
	// Snapshots + builds are keyed by deployment_id; resolve the
	// deployment set first.
	deletedDeployments := map[string]struct{}{}
	// consumer_keys (ADR-120 / issue #975 item #5). Mirror the
	// pgstore ON DELETE CASCADE — when an account is hard-deleted,
	// every consumer_keys row keyed to that account goes with it.
	// Mirrors the api_keys cascade pattern immediately below.
	for kid, k := range m.consumerKeys {
		if k.AccountID == id {
			delete(m.consumerKeys, kid)
		}
	}
	for did, d := range m.deployments {
		if app, ok := m.apps[d.AppID]; ok && app.AccountID == id {
			deletedDeployments[did] = struct{}{}
			delete(m.deployments, did)
		}
	}
	for i := len(m.snapshots) - 1; i >= 0; i-- {
		if _, ok := deletedDeployments[m.snapshots[i].DeploymentID]; ok {
			m.snapshots = append(m.snapshots[:i], m.snapshots[i+1:]...)
		}
	}
	for bid, b := range m.builds {
		if _, ok := deletedDeployments[b.DeploymentID]; ok {
			delete(m.builds, bid)
		}
	}
	for aid, a := range m.apps {
		if a.AccountID == id {
			delete(m.apps, aid)
			delete(m.githubBindings, aid)
		}
	}
	for kid, k := range m.keys {
		if k.AccountID == id {
			delete(m.keys, kid)
			delete(m.keyByHash, hex.EncodeToString(k.Hash))
		}
	}
	for k := range m.idem {
		// The MemStore idem key shape is "accountID\x00key" — strip
		// the prefix to find this account's bucket.
		if strings.HasPrefix(k, id+"\x00") {
			delete(m.idem, k)
		}
	}
	// usage_minutes + usageByMonth aggregates are keyed by accountID
	// (no separate owner column); filter and rewrite both slices.
	var kept []usageMinute
	for _, u := range m.usage {
		if u.AccountID != id {
			kept = append(kept, u)
		}
	}
	m.usage = kept
	var keptMonth []Usage
	for _, u := range m.usageByMonth {
		if u.AccountID != id {
			keptMonth = append(keptMonth, u)
		}
	}
	m.usageByMonth = keptMonth
	// Finally: clear stripeByCustomer reverse-index, then the parent.
	for sc, acid := range m.stripeByCustomer {
		if acid == id {
			delete(m.stripeByCustomer, sc)
		}
	}
	// Drop Paddle overage dedupe rows for this account so a redelivered
	// grace tick doesn't observe a stale (account, month) pair. Mirrors
	// the pgstore.go steps slice entry for paddle_overage_dedupe; the
	// MemStore has no FK so the explicit walk is the production
	// equivalent for tests.
	for k := range m.paddleOverageMonths {
		if k.accountID == id {
			delete(m.paddleOverageMonths, k)
		}
	}
	// Audit events (spec §17 G6 right-to-erasure). Drop events whose
	// subject is the account id or whose data->>account_id matches.
	// Mirrors the PgStore cascade; a non-JSON Data is left alone (the
	// parser below bails on the first byte).
	idUUID, _ := uuid.Parse(id)
	var keptEvents []Event
	for _, e := range m.events {
		if e.Subject != nil && idUUID != uuid.Nil && *e.Subject == idUUID {
			continue
		}
		if len(e.Data) > 0 {
			var payload map[string]any
			if err := json.Unmarshal(e.Data, &payload); err == nil {
				if v, ok := payload["account_id"].(string); ok && v == id {
					continue
				}
			}
		}
		keptEvents = append(keptEvents, e)
	}
	m.events = keptEvents

	// audit_log backfill (issue #755 / PR-6). Appends one
	// account.deleted row to the in-memory audit_log mirror so the
	// memstore surface mirrors the pgstore atomicity contract: the
	// audit row is recorded at the moment of deletion and outlives
	// the accounts row. Placement: AFTER the events cascade (the
	// events table is the per-event trail that gets erased under
	// GDPR) and BEFORE the parent delete(m.accounts, id).
	//
	// Uses appendAuditLogLocked directly (NOT InsertAuditLog) because
	// DeleteAccount already holds m.mu — sync.Mutex is non-reentrant,
	// so calling the locked InsertAuditLog would deadlock.
	auditPayload, mErr := json.Marshal(map[string]string{
		"source": "grace-sweep",
		"email":  a.Email,
		"actor":  "grace-sweep",
	})
	if mErr != nil {
		return fmt.Errorf("memstore: marshal audit_log payload for %s: %w", id, mErr)
	}
	var auditAccountID *uuid.UUID
	if idUUID != uuid.Nil {
		idCopy := idUUID
		auditAccountID = &idCopy
	}
	m.appendAuditLogLocked(AuditLog{
		ID:           uuid.New(),
		Kind:         AuditLogKindAccountDeleted,
		AccountID:    auditAccountID,
		AccountEmail: a.Email,
		Actor:        "grace-sweep",
		ReceivedAt:   time.Now().UTC(),
		Data:         auditPayload,
	})

	delete(m.accounts, id)
	return nil
}

// ListBuildsForAccount returns every build tied to the account.
func (m *MemStore) ListBuildsForAccount(_ context.Context, accountID string) ([]Build, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ownedDeployments := map[string]struct{}{}
	for _, d := range m.deployments {
		if app, ok := m.apps[d.AppID]; ok && app.AccountID == accountID {
			ownedDeployments[d.ID] = struct{}{}
		}
	}
	var out []Build
	for _, b := range m.builds {
		if _, ok := ownedDeployments[b.DeploymentID]; ok {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out, nil
}

// ListBuildsForAccountPaged returns one page of builds across the
// account's deployments, ordered started_at desc nulls last with
// id DESC as the tiebreaker (DEPLOY-PROV-6 follow-up / ADR-091,
// issue #741 close-out, post-review fix).
//
// statusFilter="" matches any status; appIDFilter="" matches any
// app. before.IsZero() = first page (beforeID ignored). limit is
// the page size (server-side handler clamps at 200). The result
// ordering + nulls-last + id-tiebreaker semantics mirror the
// PgStore impl + the builds_deployment_started_idx migration.
//
// The id tiebreaker is load-bearing for two failure modes the
// single-column cursor had:
//  1. queued (started_at NULL) rows past the first page boundary
//     were silently dropped — `started_at < before` excluded them
//     because NULL is never less than any value.
//  2. sub-second collisions on started_at — DB precision is
//     sub-second, wire format is RFC3339 (whole-second), so rows
//     whose DB sub-second value lands in the cursor's wall-clock
//     second were dropped.
//
// The (started_at, id) tuple makes both cases deterministic.
func (m *MemStore) ListBuildsForAccountPaged(
	_ context.Context, accountID, statusFilter, appIDFilter string,
	before time.Time, beforeID string, limit int,
) ([]Build, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ownedDeployments := map[string]struct{}{}
	for _, d := range m.deployments {
		if appIDFilter != "" && d.AppID != appIDFilter {
			continue
		}
		app, ok := m.apps[d.AppID]
		if !ok || app.AccountID != accountID {
			continue
		}
		ownedDeployments[d.ID] = struct{}{}
	}
	var out []Build
	for _, b := range m.builds {
		if _, ok := ownedDeployments[b.DeploymentID]; !ok {
			continue
		}
		if statusFilter != "" && string(b.Status) != statusFilter {
			continue
		}
		if !before.IsZero() || beforeID != "" {
			// Keyset applies when EITHER `before` is set
			// (non-zero started_at segment in the cursor) OR
			// `beforeID` is set (queued-tail cursor with empty
			// started_at segment). The four cases cover every
			// (started_at zone, cursor zone) combination under
			// the DESC NULLS LAST + id DESC ordering.
			//
			// Tuple ordering under nulls-last: zero started_at
			// sorts AFTER all non-zero started_at (DESC NULLS
			// LAST), so queued rows are at the bottom of every
			// page. The id tiebreaker breaks ties between
			// queued rows themselves.
			less := false
			switch {
			case b.StartedAt.IsZero() && !before.IsZero():
				// queued vs. non-queued cursor — queued sorts
				// AFTER (started_at desc nulls last) every
				// non-zero started_at, so ALL queued rows fall
				// into the strictly-less set when the cursor
				// is non-zero. The id DESC tiebreaker inside
				// the NULL zone then orders the queued tail
				// from newest-id to oldest-id. The page-2
				// cursor advances to the queued row with the
				// smallest id; subsequent pages walk back
				// through the queue via the third branch.
				less = true
			case b.StartedAt.IsZero() && before.IsZero() && beforeID != "":
				// Queued-tail cursor contract: the wire format
				// encodes "|id" when the cursor is a queued
				// row. before.IsZero() = "started_at segment
				// empty", beforeID != "" = "id anchor is set".
				// The page-2 keyset only considers rows in the
				// NULL zone (queued) with id < beforeID.
				less = b.ID < beforeID
			case b.StartedAt.IsZero() && before.IsZero() && beforeID == "":
				// First page + queued row: no cursor at all,
				// queued rows are valid candidates. less=false
				// keeps them in the candidate set.
				less = false
			default:
				// Both non-zero: standard tuple comparison.
				if b.StartedAt.Before(before) {
					less = true
				} else if b.StartedAt.Equal(before) && b.ID < beforeID {
					less = true
				}
			}
			if !less {
				continue
			}
		}
		out = append(out, b)
	}
	// Sort: started_at desc nulls last, then id desc as tiebreaker.
	sort.Slice(out, func(i, j int) bool {
		if out[i].StartedAt.IsZero() && !out[j].StartedAt.IsZero() {
			return false
		}
		if !out[i].StartedAt.IsZero() && out[j].StartedAt.IsZero() {
			return true
		}
		if out[i].StartedAt.IsZero() && out[j].StartedAt.IsZero() {
			return out[i].ID > out[j].ID
		}
		if !out[i].StartedAt.Equal(out[j].StartedAt) {
			return out[i].StartedAt.After(out[j].StartedAt)
		}
		return out[i].ID > out[j].ID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ListCronsForAccount returns every cron tied to the account.
func (m *MemStore) ListCronsForAccount(_ context.Context, accountID string) ([]Cron, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Cron
	for _, c := range m.crons {
		if app, ok := m.apps[c.AppID]; ok && app.AccountID == accountID {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// --- alert rules (issue #396, ADR-045) --------------------------------------
//
// MemStore mirrors the Postgres shape. The ClaimAlertFire semantics
// ride a single timestamp per rule (last_fired_at) so two ticks in
// the same bucket cannot both produce a delivery. UNIQUE(account_id,
// name) and the idempotency_key UNIQUE are enforced in Go because
// there are no SQL indexes backing them in the in-memory mirror.

// CreateAlertRule rejects on duplicate (account_id, name) before
// insert — same invariant the Postgres unique index holds. The
// returned row carries the DB-assigned id and created_at via the
// same field values the PgStore does (default gen_random_uuid +
// default now() are faked by MemStore with newID/time.Now).
func (m *MemStore) CreateAlertRule(_ context.Context, in AlertRule) (AlertRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.alertRules {
		if existing.AccountID == in.AccountID && existing.Name == in.Name {
			return AlertRule{}, ErrConflict
		}
	}
	if in.State == "" {
		in.State = AlertStateOk
	}
	if in.ID == "" {
		in.ID = newID()
	}
	if in.CreatedAt.IsZero() {
		in.CreatedAt = time.Now()
	}
	in.UpdatedAt = in.CreatedAt
	m.alertRules[in.ID] = in
	return in, nil
}

// CreateAlertRuleIfUnderQuota enforces the per-app + per-account caps
// with the same TOCTOU-defence shape as CreateCronIfUnderQuota:
// MemStore is single-process so a single critical section (m.mu)
// gates the count + insert. Account-wide rules (AppID == "") skip
// the per-app branch but still hit the per-account branch.
func (m *MemStore) CreateAlertRuleIfUnderQuota(_ context.Context, in AlertRule, limits api.Limits) (AlertRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.alertRules {
		if existing.AccountID == in.AccountID && existing.Name == in.Name {
			return AlertRule{}, ErrConflict
		}
	}

	if in.AppID != "" {
		app, ok := m.apps[in.AppID]
		if !ok || app.Status == AppDeleted {
			return AlertRule{}, ErrNotFound
		}
		// Per-app count: count every rule that pins this app,
		// regardless of account (a rule pinning an app belongs to
		// one account by definition; this loop is a fast double-
		// check).
		appCount := 0
		for _, r := range m.alertRules {
			if r.AppID == in.AppID {
				appCount++
			}
		}
		if appCount >= limits.AlertRuleLimitPerApp {
			return AlertRule{}, &AlertRuleQuotaError{
				Scope:    AlertRuleQuotaScopeApp,
				Limit:    limits.AlertRuleLimitPerApp,
				Observed: appCount,
			}
		}
	}

	// Per-account count excludes rules whose pinned app is soft-
	// deleted (matches the PgStore's EXISTS predicate).
	accountCount := 0
	for _, r := range m.alertRules {
		if r.AccountID != in.AccountID {
			continue
		}
		if r.AppID != "" {
			app, ok := m.apps[r.AppID]
			if !ok || app.Status == AppDeleted {
				continue
			}
		}
		accountCount++
	}
	if accountCount >= limits.AlertRuleLimitPerAccount {
		return AlertRule{}, &AlertRuleQuotaError{
			Scope:    AlertRuleQuotaScopeAccount,
			Limit:    limits.AlertRuleLimitPerAccount,
			Observed: accountCount,
		}
	}

	if in.ID == "" {
		in.ID = newID()
	}
	if in.CreatedAt.IsZero() {
		in.CreatedAt = time.Now()
	}
	if in.State == "" {
		in.State = AlertStateOk
	}
	in.UpdatedAt = in.CreatedAt
	m.alertRules[in.ID] = in
	return in, nil
}

func (m *MemStore) AlertRuleByID(_ context.Context, id string) (AlertRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.alertRules[id]
	if !ok {
		return AlertRule{}, ErrNotFound
	}
	return r, nil
}

// AlertRuleByAccountAppAndPresetName mirrors the pgstore
// implementation (issue #1233 / ADR-123 PR-C "Send test alert"
// button). The memstore scans both maps under a single lock:
// alert_presets for the catalog DisplayName, then alert_rules for
// the LIKE prefix. Memstore has no concurrent writer, but the
// outer mu.Lock matches the rest of the alert_rule accessors.
func (m *MemStore) AlertRuleByAccountAppAndPresetName(_ context.Context, accountID, appID, presetName string) (AlertRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	preset, ok := m.alertPresets[presetName]
	if !ok {
		return AlertRule{}, ErrNotFound
	}
	prefix := preset.DisplayName + " ("
	var matched []AlertRule
	for _, r := range m.alertRules {
		if r.AccountID == accountID && r.AppID == appID && strings.HasPrefix(r.Name, prefix) {
			matched = append(matched, r)
		}
	}
	switch len(matched) {
	case 0:
		return AlertRule{}, ErrNotFound
	case 1:
		return matched[0], nil
	default:
		// Same defensive ErrConflict as the pgstore — catalog
		// display_name uniqueness + the
		// (account_id, app_id, name) UNIQUE constraint make this
		// unreachable.
		return AlertRule{}, ErrConflict
	}
}

// UpdateAlertRule mirrors the PgStore's nil-skip semantics. The
// MemStore holds a deep-copy of the row so the caller's mutation
// after return doesn't bleed into the next read.
func (m *MemStore) UpdateAlertRule(_ context.Context, id string, p UpdateAlertRuleParams) (AlertRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.alertRules[id]
	if !ok {
		return AlertRule{}, ErrNotFound
	}
	if p.Name != nil {
		r.Name = *p.Name
	}
	if p.Enabled != nil {
		r.Enabled = *p.Enabled
	}
	if p.Metric != nil {
		r.Metric = *p.Metric
	}
	if p.Comparison != nil {
		r.Comparison = *p.Comparison
	}
	if p.Threshold != nil {
		r.Threshold = *p.Threshold
	}
	if p.WindowSpec != nil {
		r.WindowSpec = *p.WindowSpec
	}
	if p.WebhookURL != nil {
		r.WebhookURL = *p.WebhookURL
	}
	if p.WebhookSecretSealed != nil {
		// copy on store so the caller's slice can't mutate the
		// sealed bytes through aliasing.
		cp := make([]byte, len(*p.WebhookSecretSealed))
		copy(cp, *p.WebhookSecretSealed)
		r.WebhookSecretSealed = cp
	}
	if p.CooldownMinutes != nil {
		r.CooldownMinutes = *p.CooldownMinutes
	}
	// Action (issue #976 / ADR-122 / SAFE-RELEASES-B). Pointer PATCH
	// shape so a missing field leaves the row alone. Mirrors the
	// pgstore's coalesce($N, action) in the UPDATE statement.
	if p.Action != nil {
		r.Action = AlertAction(*p.Action)
	}
	r.UpdatedAt = time.Now()
	m.alertRules[id] = r
	return r, nil
}

func (m *MemStore) DeleteAlertRule(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.alertRules[id]; !ok {
		return ErrNotFound
	}
	delete(m.alertRules, id)
	return nil
}

func (m *MemStore) ListAlertRulesForAccount(_ context.Context, accountID string) ([]AlertRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AlertRule
	for _, r := range m.alertRules {
		if r.AccountID == accountID {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (m *MemStore) ListEnabledAlertRules(_ context.Context) ([]AlertRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AlertRule
	for _, r := range m.alertRules {
		if r.Enabled {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AccountID < out[j].AccountID })
	return out, nil
}

// ----------------------------------------------------------------------------
// Edge rules (ADR-089). MemStore mirrors the pgstore contract for
// the 8 methods. The single-process m.mu serialises the count +
// insert in CreateEdgeRuleIfUnderQuota, so the FOR UPDATE row lock
// in pgstore is unnecessary here. Action is round-tripped by value
// (jsonb-equivalent semantics) — the MemStore doesn't need
// encoding/json because the action struct is in-process.
//
// Account-wide rules do not exist for edge_rules (per-app scope
// only), so the quota branch is single-axis (per-app) — no per-
// account count.
// ----------------------------------------------------------------------------

func (m *MemStore) CreateEdgeRule(_ context.Context, in CreateEdgeRuleParams) (EdgeRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if in.MatchMethods == nil {
		in.MatchMethods = []string{}
	}
	now := time.Now()
	r := EdgeRule{
		ID:           newID(),
		AccountID:    in.AccountID,
		AppID:        in.AppID,
		MatchHost:    in.MatchHost,
		MatchPath:    in.MatchPath,
		MatchMethods: in.MatchMethods,
		Priority:     in.Priority,
		Enabled:      in.Enabled,
		Kind:         in.Kind,
		Action:       in.Action,
		// ValidateMode: empty string coerces to 'block' on
		// the pgstore side (col 00293 NOT NULL DEFAULT 'block')
		// and at the gateway handler (handler.go:2694). The
		// memstore keeps the verbatim value so the in-memory
		// mirror is byte-stable with the pgstore round-trip.
		ValidateMode: in.ValidateMode,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	m.edgeRules[r.ID] = r
	return r, nil
}

// CreateEdgeRuleIfUnderQuota — per-app cap only. AppID lookup
// mirrors the pgstore's `where id = $1 and status <> 'deleted'`
// predicate: a soft-deleted app is treated as missing so the
// customer can't smuggle rules into a dead app.
func (m *MemStore) CreateEdgeRuleIfUnderQuota(_ context.Context, in CreateEdgeRuleParams, limits api.Limits) (EdgeRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[in.AppID]
	if !ok || app.Status == AppDeleted {
		return EdgeRule{}, ErrNotFound
	}
	appCount := 0
	for _, r := range m.edgeRules {
		if r.AppID == in.AppID {
			appCount++
		}
	}
	if appCount >= limits.EdgeRulesPerApp {
		return EdgeRule{}, &EdgeRuleQuotaError{
			Limit:    limits.EdgeRulesPerApp,
			Observed: appCount,
		}
	}
	// Per-kind quota (ADR-091 D22). Same shape as the pgstore
	// branch. The memstore holds the entire edgeRules slice under
	// m.mu so the per-kind count is trivially race-free.
	if in.Kind == EdgeRuleKindGeo && limits.EdgeRulesGeoPerApp > 0 {
		kindCount := 0
		for _, r := range m.edgeRules {
			if r.AppID == in.AppID && r.Kind == EdgeRuleKindGeo {
				kindCount++
			}
		}
		if kindCount >= limits.EdgeRulesGeoPerApp {
			return EdgeRule{}, &EdgeRuleQuotaError{
				Limit:      limits.EdgeRulesGeoPerApp,
				Observed:   kindCount,
				Kind:       string(EdgeRuleKindGeo),
				PerAppOnly: true,
				PerKind:    true,
			}
		}
	}
	// kind='throttle' per-app quota (ADR-091 D20.5 amendment, issue
	// #881). Mirror of the pgstore branch: race-free under the
	// memstore's own m.mu (pgstore needs the explicit FOR UPDATE
	// on apps for race-freedom that the local slice doesn't).
	if in.Kind == EdgeRuleKindThrottle && limits.EdgeRulesThrottlePerApp > 0 {
		kindCount := 0
		for _, r := range m.edgeRules {
			if r.AppID == in.AppID && r.Kind == EdgeRuleKindThrottle {
				kindCount++
			}
		}
		if kindCount >= limits.EdgeRulesThrottlePerApp {
			return EdgeRule{}, &EdgeRuleQuotaError{
				Limit:      limits.EdgeRulesThrottlePerApp,
				Observed:   kindCount,
				Kind:       string(EdgeRuleKindThrottle),
				PerAppOnly: true,
				PerKind:    true,
			}
		}
	}
	// kind='cache' per-app quota (ADR-122 §Decision). Mirror of
	// the pgstore branch. Same rationale: tighter cap than
	// EdgeRulesPerApp because per (host, path, vary) cache rules
	// can pin the in-process store's byte ceiling. memstore is
	// race-free under m.mu; pgstore relies on the FOR UPDATE on
	// apps carried by the preceding quota branches.
	if in.Kind == EdgeRuleKindCache && limits.EdgeRulesCachePerApp > 0 {
		kindCount := 0
		for _, r := range m.edgeRules {
			if r.AppID == in.AppID && r.Kind == EdgeRuleKindCache {
				kindCount++
			}
		}
		if kindCount >= limits.EdgeRulesCachePerApp {
			return EdgeRule{}, &EdgeRuleQuotaError{
				Limit:      limits.EdgeRulesCachePerApp,
				Observed:   kindCount,
				Kind:       string(EdgeRuleKindCache),
				PerAppOnly: true,
				PerKind:    true,
			}
		}
	}
	if in.MatchMethods == nil {
		in.MatchMethods = []string{}
	}
	now := time.Now()
	r := EdgeRule{
		ID:           newID(),
		AccountID:    in.AccountID,
		AppID:        in.AppID,
		MatchHost:    in.MatchHost,
		MatchPath:    in.MatchPath,
		MatchMethods: in.MatchMethods,
		Priority:     in.Priority,
		Enabled:      in.Enabled,
		Kind:         in.Kind,
		Action:       in.Action,
		// ValidateMode: same shape as CreateEdgeRule — empty
		// string is preserved verbatim so the memstore mirror
		// matches the pgstore's column-NULL fallback (00293's
		// NOT NULL DEFAULT 'block' kicks in on the wire round-trip).
		ValidateMode: in.ValidateMode,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	m.edgeRules[r.ID] = r
	return r, nil
}

func (m *MemStore) ListEdgeRulesForAccount(_ context.Context, accountID string) ([]EdgeRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []EdgeRule
	for _, r := range m.edgeRules {
		if r.AccountID == accountID {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority < out[j].Priority
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

func (m *MemStore) ListEdgeRulesForApp(_ context.Context, appID string) ([]EdgeRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []EdgeRule
	for _, r := range m.edgeRules {
		if r.AppID == appID {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority < out[j].Priority
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

func (m *MemStore) GetEdgeRuleByID(_ context.Context, id string) (EdgeRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.edgeRules[id]
	if !ok {
		return EdgeRule{}, ErrNotFound
	}
	return r, nil
}

// ListCorsPresetsForAccount mirrors the pgstore query: every
// preset the account owns (both account-wide and app-scoped). The
// (app_id NULLS FIRST, name) order keeps the compile-side cache
// key deterministic — see cmd/gatewayd-internal/edge_rules.go.
func (m *MemStore) ListCorsPresetsForAccount(_ context.Context, accountID string) ([]CorsPreset, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []CorsPreset
	for _, p := range m.corsPresets {
		if p.AccountID == accountID {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		// AppID == "" means account-wide (NULL in pg). The
		// NULLS FIRST order puts account-wide presets before
		// app-scoped ones in the returned slice so a stable
		// "preset defined first wins" override rule has a
		// defined tiebreak.
		iWide := out[i].AppID == ""
		jWide := out[j].AppID == ""
		if iWide != jWide {
			return iWide
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// ListCorsPresetsForApp returns the app-scoped presets only,
// scoped to the caller's account. The compile path unions this
// with the account-wide result from ListCorsPresetsForAccount.
// accountID is defense-in-depth (the apps row is FK-scoped to
// one account, so the appID match alone is sufficient); the
// Store boundary enforces tenancy at the API surface so a future
// caller can't probe by appID without knowing the account.
// Empty appID is rejected so the strict-scope contract cannot
// be subverted by a caller passing "" (which would otherwise
// match the AppID="" of every account-wide preset — see the
// medium-code-review IDOR finding for the historical pg/memstore
// divergence).
func (m *MemStore) ListCorsPresetsForApp(_ context.Context, accountID, appID string) ([]CorsPreset, error) {
	if appID == "" {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []CorsPreset
	for _, p := range m.corsPresets {
		if p.AccountID == accountID && p.AppID == appID {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// GetCorsPresetByID returns the preset scoped to the caller's
// account or ErrNotFound. accountID is required so the Store
// boundary enforces tenancy — the pgstore equivalent pins
// account_id in the WHERE clause, this mirror keeps the two
// stores behaviorally aligned.
func (m *MemStore) GetCorsPresetByID(_ context.Context, accountID, id string) (CorsPreset, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.corsPresets[id]
	if !ok {
		return CorsPreset{}, ErrNotFound
	}
	if p.AccountID != accountID {
		// Cross-tenant probe: surface as ErrNotFound so the
		// wire-side message is stable (matches the pgstore
		// behavior where a WHERE on account_id returns no rows).
		return CorsPreset{}, ErrNotFound
	}
	return p, nil
}

// CreateCorsPresetIfUnderQuota — see Store interface. The memstore
// mirror uses the existing m.mu mutex for race-freedom; the
// per-app + per-account caps are enforced in the same critical
// section as the insert. UNIQUE collision on
// (account_id, COALESCE(app_id, ...), name) returns ErrConflict,
// matching pgstore's 23505-→-ErrConflict map.
func (m *MemStore) CreateCorsPresetIfUnderQuota(_ context.Context, p CorsPreset, limits api.Limits) (CorsPreset, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p.AppID != "" {
		app, ok := m.apps[p.AppID]
		if !ok || app.Status == "deleted" {
			return CorsPreset{}, ErrNotFound
		}
		var appCount int
		for _, q := range m.corsPresets {
			if q.AppID == p.AppID {
				appCount++
			}
		}
		if appCount >= limits.CorsPresetsPerApp {
			return CorsPreset{}, &CorsPresetQuotaError{
				Scope:    CorsPresetQuotaScopeApp,
				Limit:    limits.CorsPresetsPerApp,
				Observed: appCount,
			}
		}
	}
	// Per-account count excludes soft-deleted apps' preset rows.
	var accountCount int
	for _, q := range m.corsPresets {
		if q.AccountID != p.AccountID {
			continue
		}
		if q.AppID == "" {
			accountCount++
			continue
		}
		if app, ok := m.apps[q.AppID]; ok && app.Status != "deleted" {
			accountCount++
		}
	}
	if accountCount >= limits.CorsPresetsPerAccount {
		return CorsPreset{}, &CorsPresetQuotaError{
			Scope:    CorsPresetQuotaScopeAccount,
			Limit:    limits.CorsPresetsPerAccount,
			Observed: accountCount,
		}
	}
	// UNIQUE collision: same name on the same (account, app)
	// tuple. Account-wide presets collide on (account, "");
	// app-scoped presets collide on (account, appID).
	for _, q := range m.corsPresets {
		if q.AccountID == p.AccountID && q.AppID == p.AppID && q.Name == p.Name {
			return CorsPreset{}, ErrConflict
		}
	}
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	now := time.Now()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	m.corsPresets[p.ID] = p
	return p, nil
}

// UpdateCorsPreset replaces the entire row. The memstore mirror
// pins accountID so a cross-tenant UPDATE returns ErrNotFound,
// matching the pgstore WHERE clause. UNIQUE collisions
// (account_id, COALESCE(app_id, ...), name) return ErrConflict
// (the apid boundary maps to 409 "name already in use").
func (m *MemStore) UpdateCorsPreset(_ context.Context, accountID, id string, p CorsPreset) (CorsPreset, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	existing, ok := m.corsPresets[id]
	if !ok || existing.AccountID != accountID {
		return CorsPreset{}, ErrNotFound
	}
	// UNIQUE collision check on the post-update name+app_id
	// tuple. Excludes the row being updated so renaming a
	// preset to its own name is a no-op.
	for qid, q := range m.corsPresets {
		if qid == id {
			continue
		}
		if q.AccountID == accountID && q.AppID == p.AppID && q.Name == p.Name {
			return CorsPreset{}, ErrConflict
		}
	}
	p.ID = id
	p.AccountID = accountID
	p.CreatedAt = existing.CreatedAt
	p.UpdatedAt = time.Now()
	m.corsPresets[id] = p
	return p, nil
}

// DeleteCorsPreset removes a preset by id (scoped to the caller's
// account). The pgstore trigger fires pg_notify on every write;
// the memstore mirror has no pg-listen listener, so the gate is
// in-process (the gatewayd-internal compile path reads through
// the same Store interface, so a delete is visible on the next
// read).
func (m *MemStore) DeleteCorsPreset(_ context.Context, accountID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.corsPresets[id]
	if !ok || p.AccountID != accountID {
		return ErrNotFound
	}
	delete(m.corsPresets, id)
	return nil
}

// --- alert_presets (issue #1233, ADR-123) -----------------------------------

// ListAlertPresets mirrors the pgstore query: every catalog row
// ordered by (category, name). Catalog cardinality is bounded (8
// rows today) so no pagination is needed.
func (m *MemStore) ListAlertPresets(_ context.Context) ([]AlertPreset, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]AlertPreset, 0, len(m.alertPresets))
	for _, p := range m.alertPresets {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// SeedAlertPresetForTest inserts (or upserts) a single alert_presets
// row into the in-memory catalog. Mirrors the test-only Invoice
// seeder above; used by orchestrator tests that exercise the
// preset-then-instantiate flow (issue #1233 / ADR-123 PR-C
// commit 2 fix: end-to-end coverage for sendTestAlertPresetCore).
// Production callers must NOT use this — the catalog is owned by
// migrations/00418_alert_presets_seed.sql at deploy time.
func (m *MemStore) SeedAlertPresetForTest(p AlertPreset) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p.ID == "" {
		p.ID = newID()
	}
	m.alertPresets[p.Name] = p
}

// AlertPresetByName scans the map (8 rows). O(N) is acceptable
// for the catalog cardinality. Returns ErrNotFound on no match.
func (m *MemStore) AlertPresetByName(_ context.Context, name string) (AlertPreset, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.alertPresets {
		if p.Name == name {
			return p, nil
		}
	}
	return AlertPreset{}, ErrNotFound
}

// CountFailedDeploymentsSince mirrors the pgstore scan over the
// deployments table. Used by the alert evaluator's
// deployment_failed case. accountID is resolved via the apps
// lookup (deployments stores app_id only, mirroring the pgstore
// JOIN). appID "" means "any app on this account".
func (m *MemStore) CountFailedDeploymentsSince(_ context.Context, accountID, appID string, since time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, d := range m.deployments {
		// Resolve the deployment's account via the apps map
		// (deployments carries app_id only).
		app, ok := m.apps[d.AppID]
		if !ok || app.AccountID != accountID {
			continue
		}
		if appID != "" && d.AppID != appID {
			continue
		}
		if d.Status != DeployFailed {
			continue
		}
		if d.CreatedAt.Before(since) {
			continue
		}
		n++
	}
	return n, nil
}

// WasInvokedSuccessfullySince mirrors the pgstore EXISTS scan.
// Returns true iff at least one non-failed invocation exists in
// the window — the api_up signal.
func (m *MemStore) WasInvokedSuccessfullySince(_ context.Context, accountID, appID string, since time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, inv := range m.invocations {
		if inv.AccountID != accountID {
			continue
		}
		if appID != "" && inv.AppID != appID {
			continue
		}
		if inv.State == InvocationFailed {
			continue
		}
		if inv.CreatedAt.Before(since) {
			continue
		}
		return true, nil
	}
	return false, nil
}

// MTDSpendEurCents mirrors the pgstore SUM(eur_cents) over
// account_spend_snapshot. Returns 0 when no rows exist.
func (m *MemStore) MTDSpendEurCents(_ context.Context, accountID string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	monthStart := time.Now().UTC().Add(-time.Duration(time.Now().UTC().Day()-1) * 24 * time.Hour)
	// Snap to UTC midnight of day 1 — date_trunc equivalent.
	monthStart = time.Date(monthStart.Year(), monthStart.Month(), 1, 0, 0, 0, 0, time.UTC)
	var total int64
	for _, s := range m.accountSpendSnapshots {
		if s.AccountID != accountID {
			continue
		}
		if s.PeriodStart.Before(monthStart) {
			continue
		}
		total += s.EurCents
	}
	return total, nil
}

// UpsertAccountSpendSnapshot is the memstore mirror of the pg
// upsert. Idempotent on (account_id, source, period_end) — a
// double-fire (e.g. meterd restart mid-tick) overwrites the row
// with the latest gb_seconds / eur_cents.
func (m *MemStore) UpsertAccountSpendSnapshot(_ context.Context, accountID string, periodStart, periodEnd time.Time, gbSeconds float64, eurCents int64, source string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, s := range m.accountSpendSnapshots {
		if s.AccountID == accountID && s.Source == source && s.PeriodEnd.Equal(periodEnd) {
			s.PeriodStart = periodStart
			s.GBSeconds = gbSeconds
			s.EurCents = eurCents
			m.accountSpendSnapshots[id] = s
			return nil
		}
	}
	id := uuidOrSentinel()
	m.accountSpendSnapshots[id] = AccountSpendSnapshot{
		ID:          id,
		AccountID:   accountID,
		PeriodStart: periodStart,
		PeriodEnd:   periodEnd,
		GBSeconds:   gbSeconds,
		EurCents:    eurCents,
		Source:      source,
		CreatedAt:   time.Now().UTC(),
	}
	return nil
}

// uuidOrSentinel returns a v4-ish UUID for in-memory map keys.
// MemStore doesn't depend on the uuid package; a counter-based
// sentinel keeps tests deterministic.
var memStoreSnapshotCounter int64

func uuidOrSentinel() string {
	memStoreSnapshotCounter++
	return fmt.Sprintf("snapshot-%d", memStoreSnapshotCounter)
}

// MinCertExpiryForApp walks the meterd_tenant_surface_cert_expiry_state
// map and returns the smallest remaining seconds for the (account,
// app) — or -1 when no surface is in 'ok' state.
func (m *MemStore) MinCertExpiryForApp(_ context.Context, accountID, appID string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var minSec *int64
	for _, s := range m.tenantSurfaceCertExpiryStates {
		if s.AccountID != accountID || s.AppID != appID {
			continue
		}
		if s.LastWalkStatus != "ok" || s.LastObservedCertNotAfter == nil {
			continue
		}
		remaining := int64(time.Until(*s.LastObservedCertNotAfter).Seconds())
		if minSec == nil || remaining < *minSec {
			r := remaining
			minSec = &r
		}
	}
	if minSec == nil {
		return -1, nil
	}
	return *minSec, nil
}

// RefreshCertExpiryStates walks every tenant_surfaces row whose
// cert_state='issued', upserts the
// meterd_tenant_surface_cert_expiry_state mirror row, and stamps
// last_refreshed_at=now(). Returns the number of rows upserted.
// Mirrors pgstore.RefreshCertExpiryStates
// for tests. The hostname is the lexicographically-smallest
// verified hostname on the surface (matches the
// pkg/gateway.CertExpiry ordering at cert_expiry_surface.go:88-93)
// so the surface row's hostname is deterministic.
func (m *MemStore) RefreshCertExpiryStates(_ context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	now := time.Now()
	for _, ts := range m.tenantSurfaces {
		if ts.CertState != CertStateIssued {
			continue
		}
		var notAfter *time.Time
		status := "ok"
		if !ts.CertNotAfter.IsZero() {
			t := ts.CertNotAfter
			notAfter = &t
		} else {
			status = "cert_unissued"
		}
		// Pick the primary hostname — sort-by-hostname matches
		// pkg/gateway.CertExpiry so the on-disk cert the alert
		// evaluator reads is the same one the issuer wrote.
		hostname := ts.Name
		for _, h := range m.tenantHostnames {
			if h.SurfaceID != ts.ID || h.VerifiedAt.IsZero() {
				continue
			}
			if h.Hostname < hostname {
				hostname = h.Hostname
			}
		}
		m.tenantSurfaceCertExpiryStates[ts.ID] = TenantSurfaceCertExpiryState{
			TenantSurfaceID:          ts.ID,
			AccountID:                ts.AccountID,
			AppID:                    ts.AppID,
			Hostname:                 hostname,
			LastObservedCertNotAfter: notAfter,
			LastWalkStatus:           status,
			LastRefreshedAt:          now,
		}
		n++
	}
	return n, nil
}

// ListCertExpiryStateForWalker returns every row in
// meterd_tenant_surface_cert_expiry_state whose
// last_refreshed_at is fresher than (now - staleCutoff). Meterd's
// refresher uses this to stamp the
// apid_tenant_surface_cert_expiry_seconds gauge.
func (m *MemStore) ListCertExpiryStateForWalker(_ context.Context, staleCutoff time.Duration) ([]TenantSurfaceCertExpiryState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cutoff := time.Now().Add(-staleCutoff)
	var out []TenantSurfaceCertExpiryState
	for _, s := range m.tenantSurfaceCertExpiryStates {
		if s.LastRefreshedAt.Before(cutoff) {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

// UpdateEdgeRule mirrors the pgstore nil-skip semantics. Action
// replacement is whole-struct (no partial jsonb merge in MemStore).
func (m *MemStore) UpdateEdgeRule(_ context.Context, id string, p UpdateEdgeRuleParams) (EdgeRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.edgeRules[id]
	if !ok {
		return EdgeRule{}, ErrNotFound
	}
	if p.MatchHost != nil {
		r.MatchHost = *p.MatchHost
	}
	if p.MatchPath != nil {
		r.MatchPath = *p.MatchPath
	}
	if p.MatchMethods != nil {
		cp := make([]string, len(*p.MatchMethods))
		copy(cp, *p.MatchMethods)
		r.MatchMethods = cp
	}
	if p.Priority != nil {
		r.Priority = *p.Priority
	}
	if p.Enabled != nil {
		r.Enabled = *p.Enabled
	}
	if p.Action != nil {
		r.Action = *p.Action
	}
	// ValidateMode nil-skip mirrors the pgstore coalesce pattern.
	// The wire layer (cmd/apid/handlers_edge_rules.go) coerces
	// '' to 'block' before the request reaches here, so a
	// non-nil empty string in UpdateEdgeRuleParams is a
	// legitimate explicit-clear request that the pgstore collapses
	// to 'block' via coalesce(nullif('', ''), validate_mode).
	if p.ValidateMode != nil {
		r.ValidateMode = *p.ValidateMode
	}
	r.UpdatedAt = time.Now()
	m.edgeRules[id] = r
	return r, nil
}

func (m *MemStore) DeleteEdgeRule(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.edgeRules[id]; !ok {
		return ErrNotFound
	}
	delete(m.edgeRules, id)
	return nil
}

func (m *MemStore) CountEdgeRulesForApp(_ context.Context, appID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, r := range m.edgeRules {
		if r.AppID == appID {
			n++
		}
	}
	return n, nil
}

// CountEdgeRulesByKindForApp mirrors pgstore. The memstore doesn't
// enforce the per-kind cap in CreateEdgeRuleIfUnderQuota today
// (the FOR UPDATE lock pattern is strictly a Postgres race-defence
// — the test-only insert path on MemStore runs under the same
// m.mu lock, so a per-kind check is also straightforward if
// callers need it). The count is here so the Store interface
// stays single-shape and the apid handler's surface is identical
// across backends.
func (m *MemStore) CountEdgeRulesByKindForApp(_ context.Context, appID string, kind EdgeRuleKind) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, r := range m.edgeRules {
		if r.AppID == appID && r.Kind == kind {
			n++
		}
	}
	return n, nil
}

// MatchEdgeRulesForHost is the gateway hot-path read (same shape
// as pgstore). The host glob is re-checked in Go to mirror the
// Postgres LIKE; "*" matches every host; "*.<suffix>" matches any
// subdomain of suffix; exact hosts match themselves. Disabled rules
// are skipped (the gateway must never see a disabled rule on the
// hot path).
func (m *MemStore) MatchEdgeRulesForHost(_ context.Context, host string) ([]EdgeRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []EdgeRule
	for _, r := range m.edgeRules {
		if !r.Enabled {
			continue
		}
		if matchHostPattern(r.MatchHost, host) {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority < out[j].Priority
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

// matchHostPattern mirrors the pgstore LIKE: "*" → every host;
// "*.<suffix>" → any subdomain of suffix; exact hosts match
// themselves. The two stores MUST stay aligned — a drift here
// surfaces as "rule matches in test, fails in prod".
func matchHostPattern(pattern, host string) bool {
	switch pattern {
	case "*":
		return true
	case host:
		return true
	}
	if len(pattern) > 2 && pattern[:2] == "*." {
		suffix := pattern[1:] // ".example.com"
		return len(host) > len(suffix) && host[len(host)-len(suffix):] == suffix
	}
	return false
}

// ClaimAlertFire mirrors the Postgres CTE shape: a duplicate
// idempotency_key always loses regardless of `at` ordering, and a
// fresh key whose at advances past the rule's last_fired_at wins.
// MemStore keeps the dedupe set in alertClaimKeys (keyed by
// (ruleID, idempotency_key)) and ALSO inserts a delivery row into
// alertDeliveries — mirroring the PgStore contract (PR #409 fix to
// ensure both stores have identical dedupe + row-creation shapes;
// the previous "MemStore defers the insert to RecordAlertDelivery"
// comment was wrong about pg's contract and produced the
// alert_deliveries_idempotency_uniq 23505 every 2 s when the test
// ticks cycled through the same bucket).
//
// payload + observed are stamped onto the row at insert time so the
// test/dashboard can immediately observe the claimed delivery with
// its full envelope (parity with pgstore's
// alert_deliveries.payload + observed_value).
//
// On won=true the returned deliveryID is the new row's UUID; on
// won=false it is "".
func (m *MemStore) ClaimAlertFire(_ context.Context, ruleID, idempotencyKey string, payload []byte, observed float64, at time.Time) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.alertRules[ruleID]
	if !ok {
		return "", false, ErrNotFound
	}
	cacheKey := ruleID + "\x00" + idempotencyKey
	if _, dup := m.alertClaimKeys[cacheKey]; dup {
		return "", false, nil
	}
	if r.LastFiredAt.Before(at) || r.LastFiredAt.IsZero() {
		r.LastFiredAt = at.UTC()
		m.alertRules[ruleID] = r
		m.alertClaimKeys[cacheKey] = at.UTC()
		// Insert the pending delivery row, mirroring PgStore. The
		// m.alertDeliveries map is already keyed by idempotency_key
		// (RecordAlertDelivery's contract), so a duplicate here would
		// only occur if a parallel caller snuck a row in between our
		// dedupe-check and this write — but MemStore has no
		// concurrency (single goroutine owning the test) so that
		// race is impossible today. Empty payload normalises to "{}"
		// to match pgstore.
		if len(payload) == 0 {
			payload = []byte("{}")
		}
		id := newID()
		m.alertDeliveries[idempotencyKey] = AlertDelivery{
			ID:             id,
			RuleID:         r.ID,
			AccountID:      r.AccountID,
			AppID:          r.AppID,
			IdempotencyKey: idempotencyKey,
			Payload:        payload,
			Status:         AlertDeliveryPending,
			ObservedValue:  observed,
			FiredAt:        at.UTC(),
		}
		return id, true, nil
	}
	return "", false, nil
}

func (m *MemStore) SetAlertRuleState(_ context.Context, ruleID string, to AlertState, at time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.alertRules[ruleID]
	if !ok {
		return false, ErrNotFound
	}
	if r.State == to {
		return false, nil
	}
	r.State = to
	r.UpdatedAt = at.UTC()
	m.alertRules[ruleID] = r
	return true, nil
}

func (m *MemStore) SetAlertRuleLastEvaluated(_ context.Context, ruleID string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.alertRules[ruleID]
	if !ok {
		return ErrNotFound
	}
	r.LastEvaluatedAt = at.UTC()
	m.alertRules[ruleID] = r
	return nil
}

func (m *MemStore) RecordAlertDelivery(_ context.Context, in AlertDelivery) (AlertDelivery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Mirror the UNIQUE(idempotency_key) constraint in Postgres.
	if _, dup := m.alertDeliveries[in.IdempotencyKey]; dup {
		return AlertDelivery{}, ErrConflict
	}
	if in.ID == "" {
		in.ID = newID()
	}
	if in.FiredAt.IsZero() {
		in.FiredAt = time.Now()
	}
	if in.Status == "" {
		in.Status = AlertDeliveryPending
	}
	m.alertDeliveries[in.IdempotencyKey] = in
	return in, nil
}

func (m *MemStore) UpdateAlertDeliveryStatus(_ context.Context, id string, status AlertDeliveryStatus, attempt int, statusCode int, lastErr string, deliveredAt *time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, d := range m.alertDeliveries {
		if d.ID != id {
			continue
		}
		d.Status = status
		d.AttemptCount = attempt
		d.LastStatusCode = statusCode
		d.LastError = lastErr
		if deliveredAt != nil {
			d.DeliveredAt = deliveredAt.UTC()
		}
		m.alertDeliveries[k] = d
		return nil
	}
	return ErrNotFound
}

func (m *MemStore) ListAlertDeliveriesForRule(_ context.Context, ruleID string, limit int, includeTest bool) ([]AlertDelivery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	// Mirrors PgStore's two-path read: includeTest=false mirrors the
	// partial-index production read (test rows hidden), includeTest=true
	// surfaces Dispatcher.DispatchTest writes for the operator pane.
	// See migrations/00528_alert_deliveries_is_test.sql for the column
	// rationale and the partial-index shape.
	var out []AlertDelivery
	for _, d := range m.alertDeliveries {
		if d.RuleID != ruleID {
			continue
		}
		if !includeTest && d.IsTest {
			continue
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FiredAt.After(out[j].FiredAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// CountFailedInvocationsSince walks the in-memory invocations map.
// Mirrors the time-window predicate in PgStore — created_at is the
// grain (terminal rows have a created_at that anchors when they
// entered the queue).
func (m *MemStore) CountFailedInvocationsSince(_ context.Context, accountID, appID string, source InvocationSource, since time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, inv := range m.invocations {
		if inv.AccountID != accountID {
			continue
		}
		if inv.State != InvocationFailed {
			continue
		}
		if inv.CreatedAt.Before(since) {
			continue
		}
		if appID != "" && inv.AppID != appID {
			continue
		}
		if source != "" && inv.Source != source {
			continue
		}
		n++
	}
	return n, nil
}

// UsageByAccount returns the per-month roll-up. Mirrors the PgStore
// shape (per-app, per-month aggregated mb_seconds + requests + cpu_usec
// + tx_bytes + net_tx_bytes).
func (m *MemStore) UsageByAccount(_ context.Context, accountID string, since time.Time) ([]Usage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	type bucket struct {
		mb, req, cpu, tx, net int64
	}
	agg := map[string]*bucket{}
	for _, u := range m.usage {
		if u.AccountID != accountID {
			continue
		}
		if !since.IsZero() && u.Minute.Before(since) {
			continue
		}
		key := u.AppID + "\x00" + u.Minute.Format("2006-01")
		if _, ok := agg[key]; !ok {
			agg[key] = &bucket{}
		}
		agg[key].mb += u.MBSeconds
		agg[key].req += u.Requests
		agg[key].cpu += u.CPUUsec
		agg[key].tx += u.TXBytes
		agg[key].net += u.NetTxBytes
	}
	out := make([]Usage, 0, len(agg))
	for key, b := range agg {
		parts := strings.SplitN(key, "\x00", 2)
		month, _ := time.Parse("2006-01", parts[1])
		out = append(out, Usage{
			AccountID: accountID, AppID: parts[0], Month: month,
			MBSeconds: b.mb, Requests: b.req, CPUUsec: b.cpu,
			TXBytes: b.tx, NetTxBytes: b.net,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AppID != out[j].AppID {
			return out[i].AppID < out[j].AppID
		}
		return out[i].Month.Before(out[j].Month)
	})
	return out, nil
}

// MarkAccountDeletionPending flips the account into deleted_pending.
// Idempotent: if already pending, the original timestamp survives so
// the grace window's anchor stays at the customer's first ask.
func (m *MemStore) MarkAccountDeletionPending(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[id]
	if !ok {
		return ErrNotFound
	}
	if a.Status == AccountDeletedPending && a.DeletionRequestedAt != nil {
		return nil
	}
	a.Status = AccountDeletedPending
	now := time.Now().UTC()
	if a.DeletionRequestedAt == nil {
		a.DeletionRequestedAt = &now
	}
	m.accounts[id] = a
	return nil
}

// RestoreAccount flips status back to active and clears
// deletion_requested_at iff inside the 30-day grace window. Past
// grace → ErrConflict.
func (m *MemStore) RestoreAccount(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[id]
	if !ok {
		return ErrNotFound
	}
	if a.Status != AccountDeletedPending || a.DeletionRequestedAt == nil {
		return ErrConflict
	}
	if time.Since(*a.DeletionRequestedAt) > DeletionGraceDuration() {
		return ErrConflict
	}
	a.Status = AccountActive
	a.DeletionRequestedAt = nil
	m.accounts[id] = a
	return nil
}

// AppendGdprRequest records the action on the in-memory ledger. Mirrors
// PgStore: no auto-prune on DeleteAccount (the production table also
// outlives the account row), so a test can assert the audit row
// against the email + timestamp after the account row is gone.
func (m *MemStore) AppendGdprRequest(_ context.Context, r GdprRequest) error {
	if r.ID == "" {
		return fmt.Errorf("AppendGdprRequest: id is required")
	}
	if r.RequestedAt.IsZero() {
		r.RequestedAt = time.Now().UTC()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gdprRequests = append(m.gdprRequests, r)
	return nil
}

// ListGdprRequestsForAccount returns the rows in requested_at desc
// order, bounded by limit. limit <= 0 returns no rows (mirrors the
// PgStore guard).
func (m *MemStore) ListGdprRequestsForAccount(_ context.Context, accountID string, limit int) ([]GdprRequest, error) {
	if limit <= 0 {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]GdprRequest, 0)
	for i := len(m.gdprRequests) - 1; i >= 0 && len(out) < limit; i-- {
		if m.gdprRequests[i].AccountID == accountID {
			out = append(out, m.gdprRequests[i])
		}
	}
	return out, nil
}

// CompleteGdprRequest stamps completed_at on the most recent
// un-completed row of (account_id, action) in the in-memory ledger.
// Returns ErrNotFound when no matching row exists so callers can skip
// stale ticks without logging noise.
func (m *MemStore) CompleteGdprRequest(_ context.Context, accountID, action string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	for i := len(m.gdprRequests) - 1; i >= 0; i-- {
		r := &m.gdprRequests[i]
		if r.AccountID == accountID && r.Action == GdprAction(action) && r.CompletedAt.IsZero() {
			r.CompletedAt = now
			return nil
		}
	}
	return ErrNotFound
}

// CountGdprRequestsSince mirrors PgStore.CountGdprRequestsSince for
// the in-memory implementation. PR-5.1 / issue #755 — the rate-limit
// probe on GET /v1/account/export. Locks once, scans the slice
// backwards so the most-recent row is checked first (an account
// that has exported today will hit the limit faster than walking
// the slice in order).
func (m *MemStore) CountGdprRequestsSince(_ context.Context, accountID, action string, since time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sinceUTC := since.UTC()
	n := 0
	for i := len(m.gdprRequests) - 1; i >= 0; i-- {
		r := &m.gdprRequests[i]
		if r.AccountID != accountID || r.Action != GdprAction(action) {
			continue
		}
		if r.RequestedAt.Before(sinceUTC) {
			// Slice is requested_at-ascending (AppendGdprRequest
			// pushes to the tail), so anything older than since
			// can be skipped wholesale — early exit.
			break
		}
		n++
	}
	return n, nil
}

// FindGdprRequestByRequestID mirrors PgStore.FindGdprRequestByRequestID
// for the in-memory implementation. PR-5.2 / issue #755 — the
// idempotency probe for X-Request-Id retries on GET /v1/account/export.
// Walks the slice backwards so the most-recent row (the one we want to
// honor for a retry) is checked first. Empty requestID is a no-op
// that returns ErrNotFound, matching PgStore.
func (m *MemStore) FindGdprRequestByRequestID(_ context.Context, accountID, requestID string) (GdprRequest, error) {
	if requestID == "" {
		return GdprRequest{}, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(m.gdprRequests) - 1; i >= 0; i-- {
		r := &m.gdprRequests[i]
		if r.AccountID == accountID && r.RequestID == requestID {
			return *r, nil
		}
	}
	return GdprRequest{}, ErrNotFound
}

// BackdateGdprRequestsForTest rewrites the requested_at on every
// ledger row matching (account_id, action) to the supplied anchor.
// Test-only seam for the rate-limit roll-forward test
// (cmd/apid/handlers_account_test.go::TestExportAccount_RateLimit_AllowsAfter24h)
// — production code MUST NOT call this; the GDPR ledger is append-
// only and timestamping edits would defeat the audit-trail purpose.
// Lives on MemStore so the apid-side test can drive a 24h+ window
// in microseconds without sleeping.
func (m *MemStore) BackdateGdprRequestsForTest(_ context.Context, accountID string, action GdprAction, when time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	whenUTC := when.UTC()
	for i := range m.gdprRequests {
		r := &m.gdprRequests[i]
		if r.AccountID == accountID && r.Action == action {
			r.RequestedAt = whenUTC
		}
	}
}

// LoadAndStampLastQuotaWarning mirrors PgStore.LoadAndStampLastQuotaWarning
// for the in-memory implementation. Same contract:
//   - First call of the UTC day → (false, nil) and the row's stamp is
//     set to the supplied anchor's midnight.
//   - Same-day repeat → (true, nil) and the row's stamp stays put.
//   - Missing id → ErrNotFound.
func (m *MemStore) LoadAndStampLastQuotaWarning(_ context.Context, id string, day time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[id]
	if !ok {
		return false, ErrNotFound
	}
	dayStart := day.UTC().Truncate(24 * time.Hour)
	if a.LastQuotaWarningAt != nil && !a.LastQuotaWarningAt.Before(dayStart) {
		return true, nil
	}
	a.LastQuotaWarningAt = &dayStart
	m.accounts[id] = a
	return false, nil
}

// ClearQuotaWarning mirrors PgStore.ClearQuotaWarning. No-op when the
// row is gone or the stamp is already nil.
func (m *MemStore) ClearQuotaWarning(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[id]
	if !ok {
		return nil
	}
	a.LastQuotaWarningAt = nil
	m.accounts[id] = a
	return nil
}

// MarkDunningStep mirrors PgStore.MarkDunningStep. The MemStore enforces
// the same compare-and-flip semantics: the row's status must match
// `from` for the transition to land (else ErrNotFound — the
// redelivery-race guard), and past_due_at is stamped only when
// transitioning into past_due (coalesce preserves any pre-existing
// stamp). The from==to case is NOT short-circuited — it's the
// backfill-stamp path used by pkg/meter.Dunning to plant a stamp on
// a legacy row that entered past_due before the migration column
// existed.
func (m *MemStore) MarkDunningStep(_ context.Context, id string, from, to AccountStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[id]
	if !ok {
		return ErrNotFound
	}
	if a.Status != from {
		return ErrNotFound
	}
	a.Status = to
	if to == AccountPastDue && a.PastDueAt == nil {
		now := time.Now().UTC()
		a.PastDueAt = &now
	}
	m.accounts[id] = a
	return nil
}

// SetPastDueAtForTest is the test-only backdoor pkg/meter.Dunning tests
// use to plant a deterministic PastDueAt. Production never calls it —
// the only PastDueAt writer is MarkDunningStep (via the apid webhook
// path) which stamps time.Now(). The dunning timer compares against
// PastDueAt so the only way to exercise the 7d/21d thresholds in a
// sub-second test is to bypass MarkDunningStep's now()-stamp.
//
// Prefixed with "ForTest" so a `go vet -tests-only` or production
// audit can find it; not in pkg/state.Store (no public surface for
// ad-hoc PastDueAt writes).
func (m *MemStore) SetPastDueAtForTest(id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[id]
	if !ok {
		return ErrNotFound
	}
	stamp := at.UTC()
	a.PastDueAt = &stamp
	m.accounts[id] = a
	return nil
}

// SetDeletionRequestedAtForTest is the test-only backdoor the
// RestoreAccount-past-grace test uses to fast-forward the 30-day grace
// window without sleeping the test suite. Same `*_ForTest` naming
// convention as SetPastDueAtForTest (production audit-friendly); not
// part of the Store interface.
func (m *MemStore) SetDeletionRequestedAtForTest(id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[id]
	if !ok {
		return ErrNotFound
	}
	stamp := at.UTC()
	a.DeletionRequestedAt = &stamp
	m.accounts[id] = a
	return nil
}

// --- IAM-3 sessions (ADR-039, issue #187 + #244 merged) ---------------------
//
// One row per dashboard login, keyed by uuid. Revocation is
// RevokedAt != nil; LastSeenAt may update post-revocation. IDOR
// protection lives at the AccountID equality check (the SQL analog
// in pgstore uses an account_id predicate in the WHERE; in-memory
// we enforce the same predicate inline). Caller invariants:
//
//   - sid is a caller-generated uuid (the cookie envelope seal needs
//     the same value).
//   - issuedIP == "" means "RemoteAddr unparseable" — surfaces as an
//     empty string on reads (matches coalesce(host(issued_ip),'')
//     in pgstore).
//   - RevokeSession / RevokeAllSessions account-scope every write.
//
// MemStore drift from pgstore is caught by hand-parity tests in
// memstore_test.go (the IAM-3 block writes 5 cases: round-trip,
// touch, account-scoped revoke, list filters revoked, revoke-all
// excludes current).

func (m *MemStore) CreateSession(_ context.Context, id, accountID, issuedIP, issuedUA string) (Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.sessions[id]; exists {
		return Session{}, ErrConflict
	}
	if _, ok := m.accounts[accountID]; !ok {
		return Session{}, ErrNotFound
	}
	now := time.Now().UTC()
	s := Session{
		ID:        id,
		AccountID: accountID,
		IssuedIP:  issuedIP,
		IssuedUA:  issuedUA,
		IssuedAt:  now,
	}
	m.sessions[id] = s
	return s, nil
}

// CreateSessionWithBinding mirrors PgStore. The bindingHash
// parameter is the HMAC-SHA256 fingerprint of (ip, ua_family);
// empty string round-trips as the zero value on the struct
// (mirrors the pgstore NULL shape).
func (m *MemStore) CreateSessionWithBinding(_ context.Context, id, accountID, issuedIP, issuedUA, bindingHash string) (Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.sessions[id]; exists {
		return Session{}, ErrConflict
	}
	if _, ok := m.accounts[accountID]; !ok {
		return Session{}, ErrNotFound
	}
	now := time.Now().UTC()
	s := Session{
		ID:          id,
		AccountID:   accountID,
		IssuedIP:    issuedIP,
		IssuedUA:    issuedUA,
		IssuedAt:    now,
		BindingHash: bindingHash,
	}
	m.sessions[id] = s
	return s, nil
}

func (m *MemStore) GetSession(_ context.Context, id string) (Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return Session{}, ErrNotFound
	}
	return s, nil
}

// RevokeSession stamps revoked_at iff (a) the row exists, (b) it
// belongs to accountID, and (c) it is not already revoked. Returns
// true on real write, false on no-op (handler maps to 404).
func (m *MemStore) RevokeSession(_ context.Context, id, accountID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok || s.AccountID != accountID || s.RevokedAt != nil {
		return false, nil
	}
	now := time.Now().UTC()
	s.RevokedAt = &now
	m.sessions[id] = s
	return true, nil
}

// UpdateSessionBinding mirrors PgStore.UpdateSessionBinding.
// IDOR-safe via the (id, account_id) check; missing or
// cross-account or already-revoked rows return ErrNotFound so
// the handler maps them to 401 CodeSessionInvalid
// (byte-identical to a stolen-cookie 401).
func (m *MemStore) UpdateSessionBinding(_ context.Context, id, accountID, bindingHash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok || s.AccountID != accountID || s.RevokedAt != nil {
		return ErrNotFound
	}
	if bindingHash == "" {
		s.BindingHash = ""
	} else {
		s.BindingHash = bindingHash
	}
	m.sessions[id] = s
	return nil
}

func (m *MemStore) ListSessions(_ context.Context, accountID string) ([]Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Session
	for _, s := range m.sessions {
		if s.AccountID != accountID || s.RevokedAt != nil {
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].IssuedAt.After(out[j].IssuedAt)
	})
	return out, nil
}

// RevokeAllSessions revokes every active row for accountID except
// the supplied sid (the calling session). Returns the count.
func (m *MemStore) RevokeAllSessions(_ context.Context, accountID, exceptID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	now := time.Now().UTC()
	for id, s := range m.sessions {
		if s.AccountID != accountID || id == exceptID || s.RevokedAt != nil {
			continue
		}
		s.RevokedAt = &now
		m.sessions[id] = s
		n++
	}
	return n, nil
}

// TouchSessionLastSeen stamps last_seen_at = now(). Best-effort;
// allowed on revoked rows (operational signal — not authorization).
// Missing rows are silent no-ops: the calling handler is fire-and-
// forget and a GetSession that returned ErrNotFound followed by a
// Touch that races with a revoke must not panic.
func (m *MemStore) TouchSessionLastSeen(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return nil
	}
	now := time.Now().UTC()
	s.LastSeenAt = &now
	m.sessions[id] = s
	return nil
}

// Ping implements Store.Ping for in-memory testing.
func (m *MemStore) Ping(ctx context.Context) error {
	return nil
}

// compile-time check that MemStore satisfies Store.
var _ Store = (*MemStore)(nil)

// webhookSecretMeta is the per-tenant meta stamped alongside
// the secret (PR-D / ADR-012 §7 amendment). The shape mirrors
// github_webhook_secrets.upgraded_at + upgraded_by so the apid
// admin handler can echo the row back without a second
// round-trip.
type webhookSecretMeta struct {
	UpgradedAt time.Time
	UpgradedBy string
}

// BackdateForTest rewinds the row's started_at to the supplied
// absolute timestamp. Used by the §6.1 watchdog tests in pkg/sched
// to fabricate a stuck-WAKING/COLD_BOOTING row whose age exceeds
// the budget. Production wiring does not need this — Postgres
// timestamps are real.
func (m *MemStore) BackdateForTest(id string, startedAt time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ins, ok := m.instances[id]; ok {
		ins.StartedAt = startedAt
		m.instances[id] = ins
	}
}

// SetParkedAtForTest stamps the row's parked_at. Used by the
// watchdog tests to fabricate a stuck-SNAPSHOTTING row — the
// watchdog anchors SNAPSHOTTING age on parked_at, not started_at,
// because started_at is creation time.
func (m *MemStore) SetParkedAtForTest(id string, parkedAt time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ins, ok := m.instances[id]; ok {
		ins.ParkedAt = parkedAt
		m.instances[id] = ins
	}
}

// SetTerminalAtForTest stamps the row's terminal_at. Used by the §17
// retention sweep tests in pkg/sched to fabricate terminal rows
// (STOPPED / FAILED) whose age exceeds the configured retention
// window. Production wiring does not need this — Engine.transition
// stamps terminal_at atomically via UpdateInstanceStateToTerminal.
func (m *MemStore) SetTerminalAtForTest(id string, terminalAt time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ins, ok := m.instances[id]; ok {
		ts := terminalAt
		ins.TerminalAt = &ts
		m.instances[id] = ins
	}
}

// SetSnapshotStorageKeyForTest is the F-2 test seam: the engine's
// Wake fallback for empty StorageKey (engine.go:251-254) needs a
// pre-migration row shape. The MemStore's CreateSnapshot rejects
// empty values by contract (F-1), so the only way to fabricate the
// pre-migration shape is to mutate a stored row directly. Production
// wiring does not need this — the migration's backfill UPDATE plus
// the empty-key fallback in Wake cover the real transition.
func (m *MemStore) SetSnapshotStorageKeyForTest(deploymentID, storageKey string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, s := range m.snapshots {
		if s.DeploymentID == deploymentID {
			m.snapshots[i].StorageKey = storageKey
			return
		}
	}
}

// derefPrefixes is the []netip.Prefix sibling of intOrZero (ADR-031).
// Returns the underlying slice or nil so callers see a uniform shape
// for both branches of `SetEgressAllowlist`. Mirrors pgstore's
// copy; the duplication is intentional — pgstore dereferences for
// SQL params, memstore dereferences for in-memory copy.
func derefPrefixes(p *[]netip.Prefix) []netip.Prefix {
	if p == nil {
		return nil
	}
	return *p
}

// --- Organizations (ADR-061, IAM-6, PR 2) --------------------------------
//
// Mirrors the schema + sqlc surface 1:1 under m.mu. Errors returned here
// must match the PgStore sentinel set (ErrConflict, ErrNotFound,
// ErrOrgLastOwner, ErrOrgAlreadyMember, ErrOrgMemberCapExceeded,
// ErrOrgInvitationInvalid, ErrOrgInvitationExpired) so sister-file tests
// stay byte-comparable.

// orgAccountKey is the (org_id, account_id) composite mirror of the
// org_memberships PRIMARY KEY.
type orgAccountKey struct {
	OrgID     string
	AccountID string
}

// CreateOrg inserts an org row. Returns ErrConflict on slug collision
// (case-insensitive) or on a second personal org for the same account
// (the partial unique orgs_one_personal_per_account_uniq is enforced
// here in code; PgStore lets the SQL do it).
func (m *MemStore) CreateOrg(_ context.Context, o Org) (Org, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if o.ID == "" {
		o.ID = newID()
	}
	slugKey := strings.ToLower(o.Slug)
	if _, exists := m.orgsBySlug[slugKey]; exists {
		return Org{}, ErrConflict
	}
	// Defensive: Personal=true must carry PersonalOwnerAccountID. The SQL
	// CHECK orgs_personal_owner_link would reject the row on insert, but
	// the MemStore path dereferences the pointer below, so we guard
	// before the scan to keep MemStore panic-free.
	if o.Personal && o.PersonalOwnerAccountID == nil {
		return Org{}, ErrConflict
	}
	if o.Personal {
		for _, existing := range m.orgs {
			if existing.Personal && existing.PersonalOwnerAccountID != nil &&
				*existing.PersonalOwnerAccountID == *o.PersonalOwnerAccountID {
				return Org{}, ErrConflict
			}
		}
	}
	if o.CreatedAt.IsZero() {
		o.CreatedAt = time.Now().UTC()
	}
	if o.UpdatedAt.IsZero() {
		o.UpdatedAt = o.CreatedAt
	}
	if o.Plan == "" {
		o.Plan = api.PlanFree
	}
	if o.Status == "" {
		o.Status = OrgStatusActive
	}
	m.orgs[o.ID] = o
	m.orgsBySlug[slugKey] = o.ID
	return o, nil
}

// OrgByID is the canonical primary-key lookup.
func (m *MemStore) OrgByID(_ context.Context, id string) (Org, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.orgs[id]
	if !ok {
		return Org{}, ErrNotFound
	}
	return o, nil
}

// OrgBySlug case-folds the slug and returns the matching row.
func (m *MemStore) OrgBySlug(_ context.Context, slug string) (Org, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.orgsBySlug[strings.ToLower(slug)]
	if !ok {
		return Org{}, ErrNotFound
	}
	return m.orgs[id], nil
}

// OrgByPersonalAccount returns the unique personal-org row for an
// account. Returns ErrNotFound when no personal org exists yet
// (pre-PR-3 era).
func (m *MemStore) OrgByPersonalAccount(_ context.Context, accountID string) (Org, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, o := range m.orgs {
		if o.Personal && o.PersonalOwnerAccountID != nil && *o.PersonalOwnerAccountID == accountID {
			return o, nil
		}
	}
	return Org{}, ErrNotFound
}

// ListOrgsForAccount returns every org the account has an active
// membership in.
func (m *MemStore) ListOrgsForAccount(_ context.Context, accountID string) ([]Org, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := map[string]struct{}{}
	out := make([]Org, 0)
	for k, mem := range m.memberships {
		if k.AccountID != accountID || mem.RemovedAt != nil {
			continue
		}
		if _, dup := seen[k.OrgID]; dup {
			continue
		}
		seen[k.OrgID] = struct{}{}
		if o, ok := m.orgs[k.OrgID]; ok {
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

// UpdateOrgPlan / UpdateOrgName / UpdateOrgStatus / SoftDeleteOrg are
// mirror updates. Each stamps UpdatedAt so the wire shape is monotonic
// per row.
func (m *MemStore) UpdateOrgPlan(_ context.Context, id string, plan api.Plan) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.orgs[id]
	if !ok {
		return ErrNotFound
	}
	o.Plan = plan
	o.UpdatedAt = time.Now().UTC()
	m.orgs[id] = o
	return nil
}

// UpdateOrgName is the name half of PATCH /v1/orgs/{slug} (PR 5). The
// handler trims + bounds the name before reaching the Store; the
// MemStore is permissive but the validation contract is identical to
// the PgStore path.
func (m *MemStore) UpdateOrgName(_ context.Context, id, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.orgs[id]
	if !ok {
		return ErrNotFound
	}
	o.Name = name
	o.UpdatedAt = time.Now().UTC()
	m.orgs[id] = o
	return nil
}

func (m *MemStore) UpdateOrgStatus(_ context.Context, id string, status OrgStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.orgs[id]
	if !ok {
		return ErrNotFound
	}
	o.Status = status
	o.UpdatedAt = time.Now().UTC()
	m.orgs[id] = o
	return nil
}

func (m *MemStore) SoftDeleteOrg(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.orgs[id]
	if !ok {
		return ErrNotFound
	}
	o.DeletedPending = true
	o.Status = OrgStatusDeletedPending
	o.UpdatedAt = time.Now().UTC()
	m.orgs[id] = o
	return nil
}

// AddOrgMember inserts a membership row. Returns ErrConflict on
// duplicate (org_id, account_id), ErrOrgLastOwner when the partial
// unique would trip, and ErrNotFound on missing parent. The
// OrgMembersMax cap is enforced at consumeOrgInvitation (the only
// external-insert path) and at the wire helper
// `cmd/apid::enforceMemberCap`, NOT here — see pkg/state/pgstore.go
// ::AddOrgMember for the rationale.
func (m *MemStore) AddOrgMember(_ context.Context, orgID, accountID string, role OrgRole, invitedBy *string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.orgs[orgID]
	if !ok {
		return ErrNotFound
	}
	k := orgAccountKey{OrgID: orgID, AccountID: accountID}
	if _, exists := m.memberships[k]; exists {
		return ErrConflict
	}
	if role == OrgRoleOwner {
		for _, existing := range m.memberships {
			if existing.OrgID == orgID && existing.Role == OrgRoleOwner && existing.RemovedAt == nil {
				return ErrOrgLastOwner
			}
		}
	}
	now := time.Now().UTC()
	m.memberships[k] = OrgMembership{
		OrgID:              orgID,
		AccountID:          accountID,
		Role:               role,
		InvitedByAccountID: invitedBy,
		JoinedAt:           now,
		RemovedAt:          nil,
	}
	return nil
}

// RemoveOrgMember stamps removed_at and rejects removing the only
// active owner.
func (m *MemStore) RemoveOrgMember(_ context.Context, orgID, accountID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := orgAccountKey{OrgID: orgID, AccountID: accountID}
	mem, ok := m.memberships[k]
	if !ok {
		return ErrNotFound
	}
	if mem.Role == OrgRoleOwner && mem.RemovedAt == nil {
		return ErrOrgLastOwner
	}
	now := time.Now().UTC()
	mem.RemovedAt = &now
	m.memberships[k] = mem
	return nil
}

// UpdateOrgMemberRole updates the role and rejects demoting the only
// active owner.
func (m *MemStore) UpdateOrgMemberRole(_ context.Context, orgID, accountID string, role OrgRole) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := orgAccountKey{OrgID: orgID, AccountID: accountID}
	mem, ok := m.memberships[k]
	if !ok {
		return ErrNotFound
	}
	if mem.Role == OrgRoleOwner && role != OrgRoleOwner && mem.RemovedAt == nil {
		return ErrOrgLastOwner
	}
	mem.Role = role
	m.memberships[k] = mem
	return nil
}

// TransferOrgOwnership atomically promotes toAccountID to owner and
// demotes fromAccountID to admin under m.mu. Mirrors PgStore's
// sentinel-mapping (ErrNotFound / ErrOrgLastOwner) and demote-first
// ordering. No-op on fromAccountID == toAccountID returns
// ErrOrgLastOwner (a self-transfer would silently skip the swap and
// the wire-shape contract is to refuse).
func (m *MemStore) TransferOrgOwnership(_ context.Context, orgID, fromAccountID, toAccountID string) error {
	if fromAccountID == toAccountID {
		return ErrOrgLastOwner
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	fromKey := orgAccountKey{OrgID: orgID, AccountID: fromAccountID}
	fromMem, ok := m.memberships[fromKey]
	if !ok {
		return ErrNotFound
	}
	if fromMem.Role != OrgRoleOwner || fromMem.RemovedAt != nil {
		return ErrOrgLastOwner
	}
	toKey := orgAccountKey{OrgID: orgID, AccountID: toAccountID}
	toMem, ok := m.memberships[toKey]
	if !ok {
		return ErrNotFound
	}
	if toMem.RemovedAt != nil {
		return ErrNotFound
	}
	if toMem.Role == OrgRoleOwner {
		return ErrOrgLastOwner
	}
	// Demote-first mirrors PgStore's ordering. MemStore is single-
	// critical-section so the partial unique race PgStore guards
	// against can't occur here — but the ordering keeps the two
	// implementations byte-identical at the concurrency seam.
	fromMem.Role = OrgRoleAdmin
	m.memberships[fromKey] = fromMem
	toMem.Role = OrgRoleOwner
	m.memberships[toKey] = toMem
	return nil
}

// ListOrgMembers returns every membership row, ordered by JoinedAt.
func (m *MemStore) ListOrgMembers(_ context.Context, orgID string) ([]OrgMembership, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]OrgMembership, 0)
	for _, mem := range m.memberships {
		if mem.OrgID == orgID {
			out = append(out, mem)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].JoinedAt.Before(out[j].JoinedAt) })
	return out, nil
}

// CountActiveOrgMembers mirrors PgStore.CountActiveOrgMembers — counts
// memberships with RemovedAt == nil for the given org. Parity test
// `TestOrgMembersCountParity_PgMemstore` pins the agreement.
func (m *MemStore) CountActiveOrgMembers(_ context.Context, orgID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, mem := range m.memberships {
		if mem.OrgID == orgID && mem.RemovedAt == nil {
			n++
		}
	}
	return n, nil
}

// OrgMemberByAccount looks up the (org, account) row.
func (m *MemStore) OrgMemberByAccount(_ context.Context, orgID, accountID string) (OrgMembership, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	mem, ok := m.memberships[orgAccountKey{OrgID: orgID, AccountID: accountID}]
	if !ok {
		return OrgMembership{}, ErrNotFound
	}
	return mem, nil
}

// CreateOrgInvitation inserts a pending invitation.
func (m *MemStore) CreateOrgInvitation(_ context.Context, inv OrgInvitation) (OrgInvitation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if inv.ID == "" {
		inv.ID = newID()
	}
	if inv.CreatedAt.IsZero() {
		inv.CreatedAt = time.Now().UTC()
	}
	hashKey := hex.EncodeToString(inv.TokenHash)
	if _, exists := m.invitations[hashKey]; exists {
		return OrgInvitation{}, ErrConflict
	}
	m.invitations[hashKey] = inv
	return inv, nil
}

// OrgInvitationByTokenHash is the consume/revoke lookup.
func (m *MemStore) OrgInvitationByTokenHash(_ context.Context, hash []byte) (OrgInvitation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inv, ok := m.invitations[hex.EncodeToString(hash)]
	if !ok {
		return OrgInvitation{}, ErrNotFound
	}
	return inv, nil
}

// ConsumeOrgInvitation is the tx-heavy acceptance path. MemStore's
// m.mu covers the same atomicity boundary that the PgStore transaction
// does — every step below runs under the same lock so concurrent
// callers see a serialised outcome matching the SQL-side FOR UPDATE.
func (m *MemStore) ConsumeOrgInvitation(_ context.Context, hash []byte, acceptingAccount Account) (OrgMembership, OrgInvitation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	hashKey := hex.EncodeToString(hash)
	inv, ok := m.invitations[hashKey]
	if !ok {
		return OrgMembership{}, OrgInvitation{}, ErrOrgInvitationInvalid
	}
	if inv.ConsumedAt != nil || inv.RevokedAt != nil {
		return OrgMembership{}, OrgInvitation{}, ErrOrgInvitationInvalid
	}
	if !inv.ExpiresAt.IsZero() && inv.ExpiresAt.Before(time.Now().UTC()) {
		return OrgMembership{}, OrgInvitation{}, ErrOrgInvitationExpired
	}
	if !strings.EqualFold(inv.Email, acceptingAccount.Email) {
		return OrgMembership{}, OrgInvitation{}, ErrOrgInvitationInvalid
	}
	// IAM-6 / ADR-061 PR 2 cap check (load-bearing). Mirrors
	// PgStore.ConsumeOrgInvitation. Free + unknown plans read 0
	// (plan-policy fail-closed) so the `limit > 0` early-return keeps
	// them quiet — the abuse-floor tier cannot host shared orgs in
	// the first place, so reaching here is already a customer-flow
	// fault, not a cap-arithmetic decision.
	org, ok := m.orgs[inv.OrgID]
	if !ok {
		return OrgMembership{}, OrgInvitation{}, ErrNotFound
	}
	limits, _ := api.LimitsFor(org.Plan)
	limit := limits.OrgMembersMax
	if limit > 0 {
		active := 0
		for _, existing := range m.memberships {
			if existing.OrgID == inv.OrgID && existing.RemovedAt == nil {
				active++
			}
		}
		if active >= limit {
			return OrgMembership{}, OrgInvitation{}, ErrOrgMemberCapExceeded
		}
	}
	// Insert membership; surface ErrOrgAlreadyMember if a parallel
	// accept beat us.
	k := orgAccountKey{OrgID: inv.OrgID, AccountID: acceptingAccount.ID}
	if _, exists := m.memberships[k]; exists {
		return OrgMembership{}, OrgInvitation{}, ErrOrgAlreadyMember
	}
	now := time.Now().UTC()
	acceptingID := acceptingAccount.ID
	mem := OrgMembership{
		OrgID:              inv.OrgID,
		AccountID:          acceptingAccount.ID,
		Role:               inv.Role,
		InvitedByAccountID: inv.InvitedByAccountID,
		JoinedAt:           now,
		RemovedAt:          nil,
	}
	m.memberships[k] = mem
	inv.ConsumedAt = &now
	inv.AcceptingAccountID = &acceptingID
	m.invitations[hashKey] = inv
	return mem, inv, nil
}

// RevokeOrgInvitation stamps revoked_at on a still-pending row.
func (m *MemStore) RevokeOrgInvitation(_ context.Context, hash []byte, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	hashKey := hex.EncodeToString(hash)
	inv, ok := m.invitations[hashKey]
	if !ok {
		return ErrNotFound
	}
	if inv.ConsumedAt != nil || inv.RevokedAt != nil {
		return ErrOrgInvitationInvalid
	}
	now := time.Now().UTC()
	inv.RevokedAt = &now
	m.invitations[hashKey] = inv
	return nil
}

// ListOrgInvitationsForOrg returns every row ordered by created_at desc.
func (m *MemStore) ListOrgInvitationsForOrg(_ context.Context, orgID string) ([]OrgInvitation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]OrgInvitation, 0)
	for _, inv := range m.invitations {
		if inv.OrgID == orgID {
			out = append(out, inv)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// ListOrgInvitationsForOrgPage is the cursor-paginated mirror of
// ListOrgInvitationsForOrg (PR-8 acceptance; PR-9 cursor upgrade).
//
// PR-9: matched the PgStore swap to a compound (created_at, id)
// cursor (see pkg/cursor). The Memstore walk is unchanged —
// build the full sorted list, find the row whose (created_at, id)
// matches the cursor, skip past it to land on the next page's
// first row. The cursor's id alone is no longer the lookup key,
// which closes the v1 divergence between Memstore and PgStore
// (the v1 foot-note in the prior version of this comment spelled
// out the bug: PgStore's `id::text < $3` could skip a row whose
// id is lexically larger than the cursor's predecessor under
// random UUIDs).
//
// limit is clamped to [1, 100]; out-of-range resolves to 25.
// before "" means first page (no filter). Unknown cursor (id not
// in the org's rows) returns the same as the first page —
// defensive default; the customer emitted the cursor from a
// prior page so the call site knows the cursor is valid, and
// a stale cursor after a revoke just re-fetches the top of the
// org's queue.
func (m *MemStore) ListOrgInvitationsForOrgPage(_ context.Context, orgID string, limit int, before string) ([]OrgInvitation, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	all := make([]OrgInvitation, 0)
	for _, inv := range m.invitations {
		if inv.OrgID == orgID {
			all = append(all, inv)
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].CreatedAt.Equal(all[j].CreatedAt) {
			return all[i].ID > all[j].ID
		}
		return all[i].CreatedAt.After(all[j].CreatedAt)
	})
	// Find the cursor position. The cursor row is the last row of
	// the prior page; skip past it to land on the next page's
	// first row. PR-9 decodes the compound cursor here so the
	// (created_at, id) tuple — not just id — must match.
	skip := 0
	if before != "" {
		k, err := cursor.Decode(before)
		if err != nil {
			return nil, errors.Join(ErrInvalidCursor, err)
		}
		for i, row := range all {
			if row.CreatedAt.Equal(k.CreatedAt) && row.ID == k.ID {
				skip = i + 1
				break
			}
		}
	}
	if skip > len(all) {
		return []OrgInvitation{}, nil
	}
	out := all[skip:]
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// CountPendingOrgInvitations mirrors PgStore.CountPendingOrgInvitations
// — counts invitation rows that are not consumed, not revoked, and
// not past expires_at. Parity test
// `TestOrgPendingInvitationsCountParity_PgMemstore` pins the
// agreement.
func (m *MemStore) CountPendingOrgInvitations(_ context.Context, orgID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	n := 0
	for _, inv := range m.invitations {
		if inv.OrgID != orgID {
			continue
		}
		if inv.ConsumedAt != nil || inv.RevokedAt != nil {
			continue
		}
		if !inv.ExpiresAt.IsZero() && now.After(inv.ExpiresAt) {
			continue
		}
		n++
	}
	return n, nil
}

// ExpireOrgInvitations is the cleanup tick — stamps revoked_at on every
// pending + past-expires_at row.
func (m *MemStore) ExpireOrgInvitations(_ context.Context, now time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for k, inv := range m.invitations {
		if inv.ConsumedAt == nil && inv.RevokedAt == nil && inv.ExpiresAt.Before(now) {
			nowStamp := now
			inv.RevokedAt = &nowStamp
			m.invitations[k] = inv
			n++
		}
	}
	return n, nil
}

// TrafficAnomalyAggregate is the in-memory mirror of
// (PgStore).TrafficAnomalyAggregate. The MemStore backs the unit
// tests for the operator-obs handlers; production runs against the
// pgstore, so the memstore impl is intentionally minimal — it walks
// the in-memory usage_minutes map and applies the same scoring
// formula. Empty result on an empty store; full result when usage
// rows are seeded.
//
// See ADR-091 §3.6 / PR #2 for the scoring model.
func (m *MemStore) TrafficAnomalyAggregate(_ context.Context, arg sqlc.TrafficAnomalyAggregateParams) ([]sqlc.TrafficAnomalyAggregateRow, error) {
	if !arg.Minute.Valid || !arg.Minute_2.Valid || arg.Column3 <= 0 {
		return nil, fmt.Errorf("state: traffic_anomaly_aggregate: invalid params (since=%v baseline=%v limit=%d)", arg.Minute, arg.Minute_2, arg.Column3)
	}
	since := arg.Minute.Time
	baselineCutoff := arg.Minute_2.Time
	limit := int(arg.Column3)
	type bucket struct {
		sum, sumSq float64
		n          int
	}
	bl := map[string]*bucket{} // key = accountID|appID|hour
	key := func(a, b string, h int) string {
		return a + "|" + b + "|" + fmt.Sprint(h)
	}
	m.mu.Lock()
	for _, u := range m.usage {
		if u.Minute.Before(baselineCutoff) || !u.Minute.Before(since) {
			continue
		}
		if u.MBSeconds <= 0 {
			continue
		}
		k := key(u.AccountID, u.AppID, u.Minute.UTC().Hour())
		bk := bl[k]
		if bk == nil {
			bk = &bucket{}
			bl[k] = bk
		}
		bk.sum += float64(u.MBSeconds)
		bk.sumSq += float64(u.MBSeconds) * float64(u.MBSeconds)
		bk.n++
	}
	current := map[string]float64{} // (acct, app, minute RFC3339) -> sum
	for _, u := range m.usage {
		if u.Minute.Before(since) {
			continue
		}
		if u.MBSeconds <= 0 {
			continue
		}
		k := u.AccountID + "|" + u.AppID + "|" + u.Minute.UTC().Format(time.RFC3339)
		current[k] += float64(u.MBSeconds)
	}
	m.mu.Unlock()
	type scored struct {
		r sqlc.TrafficAnomalyAggregateRow
		z float64
	}
	out := make([]sqlc.TrafficAnomalyAggregateRow, 0)
	var scoredRows []scored
	for k, cur := range current {
		// Parse key back — three parts joined by "|", minute is
		// RFC 3339 which itself contains no "|".
		parts := strings.SplitN(k, "|", 3)
		if len(parts) != 3 {
			continue
		}
		accountID, appID, minuteRFC := parts[0], parts[1], parts[2]
		minute, _ := time.Parse(time.RFC3339, minuteRFC)
		bk := bl[key(accountID, appID, minute.UTC().Hour())]
		if bk == nil || bk.n < 3 {
			continue
		}
		mean := bk.sum / float64(bk.n)
		variance := (bk.sumSq / float64(bk.n)) - mean*mean
		if variance < 0 {
			variance = 0
		}
		stddev := sqrtFloat64(variance)
		var z float64
		var reason string
		switch {
		case stddev < 1.0 && cur >= 5.0*mean && mean > 0:
			z = (cur - mean) / 5.0
			reason = "raw_z"
		case stddev >= 1.0 && cur >= mean+3.0*stddev:
			z = (cur - mean) / stddev
			reason = "hour_of_day"
		default:
			continue
		}
		scoredRows = append(scoredRows, scored{
			r: sqlc.TrafficAnomalyAggregateRow{
				AccountID:        uuidToPgtype(accountID),
				AppID:            uuidToPgtype(appID),
				Minute:           pgtypeTimestamptzFromTime(minute),
				CurrentMbSeconds: cur,
				MeanMbSeconds:    mean,
				StddevMbSeconds:  stddev,
				SampleCount:      int32(bk.n),
				ZScore:           &z,
				Reason:           reason,
			},
			z: z,
		})
	}
	sort.Slice(scoredRows, func(i, j int) bool { return scoredRows[i].z > scoredRows[j].z })
	if len(scoredRows) > limit {
		scoredRows = scoredRows[:limit]
	}
	for _, s := range scoredRows {
		out = append(out, s.r)
	}
	return out, nil
}

// PerAccountRateLimitAggregate is the in-memory mirror of
// (PgStore).TrafficAnomalyAggregateByNode. Walks m.usage to build
// per-(account, app, node, hour) buckets; the memstore's
// usage entries don't carry node_id (they're per-instance), so
// the resolution joins to m.instances[instance_id].node_id.
// Computing a full per-node baseline is more expensive than the
// per-app path (one extra GROUP BY column), so the memstore
// shares the same baseline math via the existing bucket walker
// with an additional key dimension.
//
// Why mirror the per-app impl rather than just return empty: the
// obs handler tests for `?group_by=node` need to assert the wire
// shape from a memstore-backed store. An empty result would
// pass a "no rows" test but fail a "score threshold" test.
func (m *MemStore) TrafficAnomalyAggregateByNode(_ context.Context, arg sqlc.TrafficAnomalyAggregateByNodeParams) ([]sqlc.TrafficAnomalyAggregateByNodeRow, error) {
	if !arg.Minute.Valid || !arg.Minute_2.Valid || arg.Column3 <= 0 {
		return nil, fmt.Errorf("state: traffic_anomaly_aggregate_by_node: invalid params (since=%v baseline=%v limit=%d)", arg.Minute, arg.Minute_2, arg.Column3)
	}
	since := arg.Minute.Time
	baselineCutoff := arg.Minute_2.Time
	limit := int(arg.Column3)
	type bucket struct {
		sum, sumSq float64
		n          int
	}
	bl := map[string]*bucket{} // key = accountID|appID|nodeID|hour
	keyOf := func(a, b, n string, h int) string {
		return a + "|" + b + "|" + n + "|" + fmt.Sprint(h)
	}
	m.mu.Lock()
	for _, u := range m.usage {
		if u.Minute.Before(baselineCutoff) || !u.Minute.Before(since) {
			continue
		}
		if u.MBSeconds <= 0 {
			continue
		}
		nodeID := ""
		if inst, ok := m.instances[u.InstanceID]; ok && inst.NodeID != "" {
			nodeID = inst.NodeID
		} else {
			continue // no node resolution → skip (cannot credit a node)
		}
		k := keyOf(u.AccountID, u.AppID, nodeID, u.Minute.UTC().Hour())
		bk := bl[k]
		if bk == nil {
			bk = &bucket{}
			bl[k] = bk
		}
		bk.sum += float64(u.MBSeconds)
		bk.sumSq += float64(u.MBSeconds) * float64(u.MBSeconds)
		bk.n++
	}
	// current_pool: same shape, different time window.
	cur := map[string]map[int]float64{} // (account|app|node) → hour → sum
	curKey := func(a, b, n string) string {
		return a + "|" + b + "|" + n
	}
	for _, u := range m.usage {
		if u.Minute.Before(since) {
			continue
		}
		if u.MBSeconds <= 0 {
			continue
		}
		nodeID := ""
		if inst, ok := m.instances[u.InstanceID]; ok && inst.NodeID != "" {
			nodeID = inst.NodeID
		} else {
			continue
		}
		h := u.Minute.UTC().Hour()
		ck := curKey(u.AccountID, u.AppID, nodeID)
		cm := cur[ck]
		if cm == nil {
			cm = map[int]float64{}
			cur[ck] = cm
		}
		cm[h] += float64(u.MBSeconds)
	}
	m.mu.Unlock()
	type scored struct {
		accountID, appID, nodeID string
		minute                   time.Time
		current                  float64
		mean, stddev             float64
		n                        int
		z                        *float64
		reason                   string
	}
	out := []scored{}
	for ck, hours := range cur {
		// ck = "account|app|node"
		var accID, appID, nodeID string
		for i, c := range []byte(ck) {
			_ = i
			_ = c
		}
		// Split ck — three segments separated by '|'.
		// Avoid pulling strings.Split into hot path; use a tiny
		// manual scan.
		parts := [3]string{}
		pi := 0
		start := 0
		for i := 0; i < len(ck); i++ {
			if ck[i] == '|' && pi < 2 {
				parts[pi] = ck[start:i]
				pi++
				start = i + 1
			}
		}
		parts[pi] = ck[start:]
		accID, appID, nodeID = parts[0], parts[1], parts[2]
		for h, curSum := range hours {
			bk := bl[keyOf(accID, appID, nodeID, h)]
			if bk == nil {
				continue
			}
			mean := bk.sum / float64(bk.n)
			variance := (bk.sumSq / float64(bk.n)) - mean*mean
			if variance < 0 {
				variance = 0
			}
			stddev := math.Sqrt(variance)
			var z *float64
			var reason string
			if bk.n < 3 {
				continue
			}
			if stddev < 1.0 && curSum >= 5.0*mean && mean > 0 {
				zv := (curSum - mean) / 5.0
				z = &zv
				reason = "raw_z"
			} else if stddev >= 1.0 && curSum >= mean+3.0*stddev {
				zv := (curSum - mean) / stddev
				z = &zv
				reason = "hour_of_day"
			} else {
				continue
			}
			out = append(out, scored{
				accountID: accID, appID: appID, nodeID: nodeID,
				minute:  time.Date(since.Year(), since.Month(), since.Day(), h, 0, 0, 0, time.UTC),
				current: curSum, mean: mean, stddev: stddev, n: bk.n,
				z: z, reason: reason,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].z == nil || out[j].z == nil {
			return false
		}
		return *out[i].z > *out[j].z
	})
	if len(out) > limit {
		out = out[:limit]
	}
	rows := make([]sqlc.TrafficAnomalyAggregateByNodeRow, 0, len(out))
	for _, s := range out {
		var aid, bid, nid pgtype.UUID
		_ = aid.Scan(s.accountID)
		_ = bid.Scan(s.appID)
		_ = nid.Scan(s.nodeID)
		rows = append(rows, sqlc.TrafficAnomalyAggregateByNodeRow{
			AccountID:        aid,
			AppID:            bid,
			NodeID:           nid,
			Minute:           pgtypeTimestamptzFromTime(s.minute),
			CurrentMbSeconds: s.current,
			MeanMbSeconds:    s.mean,
			StddevMbSeconds:  s.stddev,
			SampleCount:      int32(s.n),
			ZScore:           s.z,
			Reason:           s.reason,
		})
	}
	return rows, nil
}

// PerAccountRateLimitAggregate is the in-memory mirror of
// (PgStore).PerAccountRateLimitAggregate. Counts events rows of
// kind='auth.rate_limited' over the since window, grouped by
// subject (uuid) — anonymous events (subject empty) collapse under
// the all-zeros UUID. See ADR-091 §3.5 / PR #2.
func (m *MemStore) PerAccountRateLimitAggregate(_ context.Context, arg sqlc.PerAccountRateLimitAggregateParams) ([]sqlc.PerAccountRateLimitAggregateRow, error) {
	if !arg.At.Valid || arg.Column2 <= 0 {
		return nil, fmt.Errorf("state: per_account_rate_limit_aggregate: invalid params (since=%v limit=%d)", arg.At, arg.Column2)
	}
	since := arg.At.Time
	type bucket struct {
		hits int
		last time.Time
	}
	bl := map[string]*bucket{} // key = subject UUID string (or zeros)
	m.mu.Lock()
	for _, ev := range m.events {
		if ev.Kind != "auth.rate_limited" {
			continue
		}
		if ev.At.Before(since) {
			continue
		}
		key := "00000000-0000-0000-0000-000000000000"
		if ev.Subject != nil && *ev.Subject != uuid.Nil {
			key = ev.Subject.String()
		}
		bk := bl[key]
		if bk == nil {
			bk = &bucket{}
			bl[key] = bk
		}
		bk.hits++
		if ev.At.After(bk.last) {
			bk.last = ev.At
		}
	}
	m.mu.Unlock()
	type rlrow struct {
		r sqlc.PerAccountRateLimitAggregateRow
	}
	var rows []rlrow
	limit := int(arg.Column2)
	for k, bk := range bl {
		rows = append(rows, rlrow{r: sqlc.PerAccountRateLimitAggregateRow{
			AccountID:   uuidToPgtype(k),
			Hits:        int32(bk.hits),
			LastEventAt: bk.last,
		}})
	}
	la := func(r sqlc.PerAccountRateLimitAggregateRow) time.Time {
		if t, ok := r.LastEventAt.(time.Time); ok {
			return t
		}
		return time.Time{}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].r.Hits != rows[j].r.Hits {
			return rows[i].r.Hits > rows[j].r.Hits
		}
		return la(rows[i].r).After(la(rows[j].r))
	})
	if len(rows) > limit {
		rows = rows[:limit]
	}
	out := make([]sqlc.PerAccountRateLimitAggregateRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.r)
	}
	return out, nil
}

// sqrtFloat64 is a small helper for the in-memory anomaly stddev
// calculation. MemStore lives in package state and avoids pulling
// in math just for sqrt; the inline implementation matches math.Sqrt
// within float64 precision for the inputs the anomaly aggregate
// sees (mb_seconds values, typically <1e9).
func sqrtFloat64(x float64) float64 {
	if x <= 0 {
		return 0
	}
	z := x
	for i := 0; i < 32; i++ {
		z = (z + x/z) / 2
	}
	return z
}

// uuidToPgtype parses a UUID string into a pgtype.UUID with
// Valid=true. Returns a zero pgtype.UUID with Valid=false on parse
// failure; the operator handlers never propagate the all-zeros
// sentinel to the wire (the SQL coalesce in the durable query
// already returns the literal all-zeros UUID for anonymous events).
func uuidToPgtype(s string) pgtype.UUID {
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		return pgtype.UUID{}
	}
	return u
}

// pgtypeTimestamptzFromTime builds a pgtype.Timestamptz with
// Valid=true from a Go time.Time. The MemStore anomaly aggregate
// only emits non-zero timestamps (it parses the RFC 3339 minute
// key); this helper exists so the Row.Minute field is wire-shape
// consistent with pgstore.
func pgtypeTimestamptzFromTime(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

// Consumer keys (ADR-120 / issue #975 item #5).
//
// Mirror PgStore semantics exactly: same IDOR-safe WHERE
// predicates (accountID pinned on every read), same idempotent
// revoke (revoke-after-revoke returns the same row), same empty-
// id error mapping. The memstore is a test fixture — if it
// diverges from pgstore, callers validated against memstore ship
// bugs when moved to PG. Symmetric strictness is the rule.

// consumerKeyAccountIDOf returns accountID + ok=false if the
// caller passed an empty keyID. The pg path's pgx.ErrNoRows →
// ErrNotFound collapse gives the same observable; we mirror it
// here so handlers don't have to special-case.
func consumerKeyAccountIDOf(ctx context.Context, accountID, keyID string) (string, bool) {
	_ = ctx
	if accountID == "" || keyID == "" {
		return "", false
	}
	return accountID, true
}

func (m *MemStore) CreateConsumerKey(_ context.Context, accountID, appID, name, prefix string, hash []byte, scopes []string, expiresAt *time.Time) (ConsumerKey, error) {
	if accountID == "" || appID == "" {
		return ConsumerKey{}, errors.New("memstore: CreateConsumerKey: empty account_id or app_id")
	}
	if len(hash) != 32 {
		return ConsumerKey{}, fmt.Errorf("memstore: CreateConsumerKey: hash must be 32 bytes, got %d", len(hash))
	}
	if len(scopes) == 0 {
		return ConsumerKey{}, errors.New("memstore: CreateConsumerKey: scopes cannot be empty (closed-set CHECK in 00329)")
	}
	// Closed-set vocab: pgstore's consumer_keys_scopes_vocab_chk rejects
	// anything outside {read, write, admin}. Memstore mirrored the empty
	// guard but NOT the vocab — a Store-level test that bypasses the
	// apid validator (PR #5-B) could seed an out-of-vocab scope and the
	// memstore would accept it, while the pg path silently rejects with
	// SQLSTATE 23514. Pin the surface here so pg ↔ memstore agree.
	for _, s := range scopes {
		switch s {
		case "read", "write", "admin":
			// ok
		default:
			return ConsumerKey{}, fmt.Errorf("memstore: CreateConsumerKey: scope %q is not in the closed-set {read, write, admin} (00329 vocab CHECK)", s)
		}
	}
	if expiresAt != nil && expiresAt.Before(time.Now()) {
		return ConsumerKey{}, errors.New("memstore: CreateConsumerKey: expires_at must be in the future (DB CHECK 00329)")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// Mirror the (account_id, app_id, name) UNIQUE from 00329.
	for _, k := range m.consumerKeys {
		if k.AccountID == accountID && k.AppID == appID && k.Name == name {
			return ConsumerKey{}, ErrConflict
		}
	}
	k := ConsumerKey{
		ID:        newID(),
		AccountID: accountID,
		AppID:     appID,
		Name:      name,
		Prefix:    prefix,
		Hash:      append([]byte(nil), hash...),
		Scopes:    append([]string(nil), scopes...),
		CreatedAt: time.Now().UTC(),
		ExpiresAt: expiresAt,
	}
	m.consumerKeys[k.ID] = k
	return k, nil
}

func (m *MemStore) GetConsumerKeyByID(ctx context.Context, accountID, keyID string) (ConsumerKey, error) {
	if _, ok := consumerKeyAccountIDOf(ctx, accountID, keyID); !ok {
		return ConsumerKey{}, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.consumerKeys[keyID]
	if !ok || k.AccountID != accountID {
		return ConsumerKey{}, ErrNotFound
	}
	return k, nil
}

func (m *MemStore) ListConsumerKeysForApp(ctx context.Context, accountID, appID string) ([]ConsumerKey, error) {
	if accountID == "" || appID == "" {
		return nil, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []ConsumerKey
	for _, k := range m.consumerKeys {
		if k.AccountID == accountID && k.AppID == appID {
			out = append(out, k)
		}
	}
	// Order by created_at desc to match the pg query. Stable tie-
	// break by ID so the test fixtures are deterministic.
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

// RevokeConsumerKey mirrors pgstore: idempotent (revoke-after-
// revoke returns the same row without error), ErrNotFound only if
// the key never existed for the account.
func (m *MemStore) RevokeConsumerKey(ctx context.Context, accountID, keyID string) (ConsumerKey, error) {
	if _, ok := consumerKeyAccountIDOf(ctx, accountID, keyID); !ok {
		return ConsumerKey{}, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.consumerKeys[keyID]
	if !ok || k.AccountID != accountID {
		return ConsumerKey{}, ErrNotFound
	}
	if k.RevokedAt == nil {
		now := time.Now().UTC()
		k.RevokedAt = &now
		m.consumerKeys[k.ID] = k
	}
	return k, nil
}

// TouchConsumerKeyLastUsed mirrors pgstore's WHERE filter on
// revoked_at IS NULL — best-effort observability, no failure if
// the key was just killed.
func (m *MemStore) TouchConsumerKeyLastUsed(ctx context.Context, keyID string) error {
	if keyID == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.consumerKeys[keyID]
	if !ok || k.RevokedAt != nil {
		return nil
	}
	now := time.Now().UTC()
	k.LastUsedAt = &now
	m.consumerKeys[k.ID] = k
	return nil
}

// ConsumerKeyByAppAndPrefix mirrors pgstore: walks the map,
// matches on (AccountID, AppID, Prefix), collapses misses to
// ErrNotFound. The (app_id, prefix) composite index is in-memory
// only — the memstore is a test fixture, not a production path.
func (m *MemStore) ConsumerKeyByAppAndPrefix(ctx context.Context, accountID, appID, prefix string) (ConsumerKey, error) {
	_ = ctx
	if accountID == "" || appID == "" || prefix == "" {
		return ConsumerKey{}, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range m.consumerKeys {
		if k.AccountID == accountID && k.AppID == appID && k.Prefix == prefix {
			return k, nil
		}
	}
	return ConsumerKey{}, ErrNotFound
}

// ProvisionedStaticEgressIPExists (ADR-119 redesign) is the
// apid-side gate. Mirrors the Postgres implementation
// (`pkg/state/pgstore.go::ProvisionedStaticEgressIPExists`).
// The memstore is a test fixture — the in-memory map is keyed
// by (accountID, customerIP) and the lookup is a single map
// probe.
func (m *MemStore) ProvisionedStaticEgressIPExists(_ context.Context, accountID string, ip netip.Addr) (bool, error) {
	if accountID == "" || !ip.Is4() {
		return false, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.provisionedStaticEgressIPs[accountID][ip.String()]
	return ok, nil
}

// ReplaceProvisionedStaticEgressIPs (ADR-119 redesign) is the
// vmmd-side write that mirrors the operator's TOML into the
// Postgres gate table. The memstore mirrors the same shape
// (clear-then-insert) so the test fixture's invariants match
// the production path's "either prior OR new, never partial"
// posture.
func (m *MemStore) ReplaceProvisionedStaticEgressIPs(_ context.Context, accountID string, ips []netip.Addr) error {
	if accountID == "" {
		return fmt.Errorf("state: MemStore.ReplaceProvisionedStaticEgressIPs: empty account_id")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.provisionedStaticEgressIPs == nil {
		m.provisionedStaticEgressIPs = map[string]map[string]netip.Addr{}
	}
	bucket := make(map[string]netip.Addr, len(ips))
	for _, ip := range ips {
		if !ip.Is4() {
			return fmt.Errorf("state: MemStore.ReplaceProvisionedStaticEgressIPs: rejecting non-v4 %s", ip)
		}
		bucket[ip.String()] = ip
	}
	m.provisionedStaticEgressIPs[accountID] = bucket
	return nil
}

// memNewUUID returns a freshly-minted 16-byte UUID for the
// memstore's trigger-stub helpers (commit #6). Real production
// code never sees this — the apid's MemStore tests do.
func memNewUUID() [16]byte {
	var b [16]byte
	b[0] = byte(time.Now().UnixNano() & 0xff)
	b[6] = (b[6] & 0x0f) | 0x40 // version 7
	b[8] = (b[8] & 0x3f) | 0x80 // variant RFC4122
	return b
}

// parseMemUUIDString decodes a hyphenated hex string UUID into a
// [16]byte for memstore internals.
func parseMemUUIDString(s string) [16]byte {
	var b [16]byte
	uid, err := uuid.Parse(s)
	if err != nil {
		return b
	}
	copy(b[:], uid[:])
	return b
}

// CreateMirrorRuleIfUnderQuota mirrors pgstore.CreateMirrorRuleIfUnderQuota
// (issue #72 / ADR-125). The MemStore version runs under m.mu so a
// concurrent CreateMirrorRuleIfUnderQuota call serialises behind us
// — same race-free contract the pgstore's FOR UPDATE provides.
//
// Range-check matches pgstore (handler already validates, this is
// defence in depth). Source/mirror distinctness check matches the
// SQL CHECK (migration 00348). Both-deployments-live + cross-app
// validation matches the pgstore's pre-insert queries.
func (m *MemStore) CreateMirrorRuleIfUnderQuota(_ context.Context, in CreateMirrorRuleParams, limits api.Limits) (MirrorRule, error) {
	if in.Percent < 0 || in.Percent > 100 {
		return MirrorRule{}, ErrInvalidMirrorPercent
	}
	if in.SourceDeploymentID == in.MirrorDeploymentID {
		return MirrorRule{}, ErrMirrorSourceTargetSame
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	app, ok := m.apps[in.AppID]
	if !ok || app.Status == AppDeleted {
		return MirrorRule{}, ErrNotFound
	}
	// Per-app count gate (Limits.MirrorTargetsPerApp; Free 0 /
	// Hobby 0 / Pro 1 / Scale 3). The plan gate (Free/Hobby lock)
	// is the handler's job — MirrorRuleAllowed returns false at
	// the Plan.MirrorRuleAllowed() boundary so we never see a
	// Free/Hobby request here.
	var appCount int
	for _, r := range m.mirrorRules {
		if r.AppID == in.AppID {
			appCount++
		}
	}
	if appCount >= limits.MirrorTargetsPerApp {
		return MirrorRule{}, &QuotaError{
			Kind:     QuotaErrorKindMirror,
			Limit:    limits.MirrorTargetsPerApp,
			Observed: appCount,
		}
	}
	// Validate source + mirror deployments. Both must be live
	// (operators mirror against live rows, same as traffic split)
	// AND belong to the same app (a single mirror_rule is
	// app-scoped; cross-app is ADR-125 §follow-on 4).
	for _, depID := range []string{in.SourceDeploymentID, in.MirrorDeploymentID} {
		d, ok := m.deployments[depID]
		if !ok || d.AppID != in.AppID || d.Status != DeployLive {
			return MirrorRule{}, ErrMirrorDeploymentNotLive
		}
	}
	now := time.Now().UTC()
	r := MirrorRule{
		ID:                 uuid.NewString(),
		AccountID:          in.AccountID,
		AppID:              in.AppID,
		SourceDeploymentID: in.SourceDeploymentID,
		MirrorDeploymentID: in.MirrorDeploymentID,
		Percent:            in.Percent,
		Enabled:            in.Enabled,
		IncludeBody:        in.IncludeBody,
		RedactHeaders:      in.RedactHeaders,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if r.RedactHeaders == nil {
		r.RedactHeaders = []string{}
	}
	m.mirrorRules[r.ID] = r
	return r, nil
}

// ListMirrorRules returns every mirror_rule for the app, ordered by
// created_at ASC. The gateway picker reads from this in the
// deployment_changed pg_notify refresh path; MemStore's in-memory
// mirror makes it testable without a DB.
func (m *MemStore) ListMirrorRules(_ context.Context, appID string) ([]MirrorRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []MirrorRule
	for _, r := range m.mirrorRules {
		if r.AppID == appID {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(a, b int) bool {
		return out[a].CreatedAt.Before(out[b].CreatedAt)
	})
	return out, nil
}

// GetMirrorRuleByID returns a single rule by id. IDOR safety is the
// caller's responsibility (apid's loadApp + AccountID check); this
// method scopes the read by id alone.
func (m *MemStore) GetMirrorRuleByID(_ context.Context, id string) (MirrorRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.mirrorRules[id]
	if !ok {
		return MirrorRule{}, ErrNotFound
	}
	return r, nil
}

// UpdateMirrorRule applies a partial update via MirrorRulePatch.
// Pointer fields let the caller distinguish "absent" from "zero"
// (Percent=0 disables the rule without removing it). Same m.mu
// discipline as CreateMirrorRuleIfUnderQuota so concurrent writers
// serialise.
func (m *MemStore) UpdateMirrorRule(_ context.Context, id string, patch MirrorRulePatch) (MirrorRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.mirrorRules[id]
	if !ok {
		return MirrorRule{}, ErrNotFound
	}
	if patch.Percent != nil {
		if *patch.Percent < 0 || *patch.Percent > 100 {
			return MirrorRule{}, ErrInvalidMirrorPercent
		}
		r.Percent = *patch.Percent
	}
	if patch.Enabled != nil {
		r.Enabled = *patch.Enabled
	}
	if patch.IncludeBody != nil {
		r.IncludeBody = *patch.IncludeBody
	}
	if patch.RedactHeaders != nil {
		r.RedactHeaders = *patch.RedactHeaders
	}
	r.UpdatedAt = time.Now().UTC()
	m.mirrorRules[id] = r
	return r, nil
}

// DeleteMirrorRule removes a rule. MemStore does not cascade to
// mirror_invocation_results (the in-process map keeps rows around
// until the next test teardown); the pgstore's ON DELETE CASCADE
// handles production cleanup.
func (m *MemStore) DeleteMirrorRule(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.mirrorRules[id]; !ok {
		return ErrNotFound
	}
	delete(m.mirrorRules, id)
	return nil
}

// InsertMirrorResult appends one row to the mirror ledger. Best-
// effort: the caller logs the error but doesn't roll back the
// customer-facing response. MemStore does NOT enforce the schema
// NOT NULLs (the caller must pass non-empty IDs / non-nil hashes
// where the SQL column requires them).
func (m *MemStore) InsertMirrorResult(_ context.Context, r MirrorInvocationResult) error {
	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	if r.CompletedAt.IsZero() {
		r.CompletedAt = time.Now().UTC()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mirrorResults[r.ID] = r
	return nil
}

// ListMirrorResults returns up to `limit` rows for a rule with
// completed_at >= `since`, ordered DESC. limit <= 0 means "no
// cap" (matches the same contract ListDeploymentsForApp uses).
func (m *MemStore) ListMirrorResults(_ context.Context, ruleID string, since time.Time, limit int) ([]MirrorInvocationResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []MirrorInvocationResult
	for _, r := range m.mirrorResults {
		if r.MirrorRuleID != ruleID {
			continue
		}
		// Boundary is INCLUSIVE (>=). Matches the PgStore SQL
		// `completed_at >= $2` clause and the docstring contract
		// on Store.ListMirrorResults. An operator polling with a
		// previous row's CompletedAt as the new `since` must see
		// that boundary row, not drop it.
		if !r.CompletedAt.Before(since) {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(a, b int) bool {
		return out[a].CompletedAt.After(out[b].CompletedAt)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// MirrorSummary aggregates the rows in the same window via Go-side
// iteration. The pgstore uses SQL aggregates (COUNT/SUM/AVG/
// p99_cont) — both stores return the same shape so the apid
// handler can render either.
func (m *MemStore) MirrorSummary(_ context.Context, ruleID string, since time.Time) (MirrorSummary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var s MirrorSummary
	var latencyDiffs []int
	for _, r := range m.mirrorResults {
		if r.MirrorRuleID != ruleID {
			continue
		}
		if r.CompletedAt.Before(since) {
			continue
		}
		s.TotalInvocations++
		if r.StatusDiff {
			s.StatusDiffCount++
		}
		if r.SchemaDiff {
			s.SchemaDiffCount++
		}
		if r.BodyDiff {
			s.BodyDiffCount++
		}
		if r.Crashed {
			s.CrashCount++
		}
		if r.LatencyMs > 0 && r.SourceLatencyMs > 0 {
			latencyDiffs = append(latencyDiffs, r.LatencyMs-r.SourceLatencyMs)
		}
	}
	if len(latencyDiffs) > 0 {
		var sum int
		for _, d := range latencyDiffs {
			sum += d
		}
		s.MeanLatencyDiffMs = sum / len(latencyDiffs)
		// p99 ≈ last element after sort (small N; for production
		// the pgstore uses percentile_cont).
		sort.Ints(latencyDiffs)
		p99Idx := len(latencyDiffs) * 99 / 100
		if p99Idx >= len(latencyDiffs) {
			p99Idx = len(latencyDiffs) - 1
		}
		s.P99LatencyDiffMs = latencyDiffs[p99Idx]
	}
	return s, nil
}

// --- ADR-127 PR-B — regression observation + dashboard reads (MemStore stubs) ---

// UpsertRegressionObservation is a no-op in MemStore. The regression
// cron runs only against the production PgStore; the MemStore
// implementation exists solely to satisfy the Store interface so
// unit tests that wire up a MemStore still compile.
func (m *MemStore) UpsertRegressionObservation(_ context.Context, _ sqlc.UpsertRegressionObservationParams) error {
	return nil
}

// ListActiveRegressionsByApp is a no-op in MemStore. Returns nil so
// dashboard tests can assert "no regressions surfaced" without a
// populated regression set.
func (m *MemStore) ListActiveRegressionsByApp(_ context.Context, _ sqlc.ListActiveRegressionsByAppParams) ([]sqlc.ListActiveRegressionsByAppRow, error) {
	return nil, nil
}

// ListDeploymentsForCompare is a no-op in MemStore. Returns nil so
// dashboard tests can render the compare panel empty.
func (m *MemStore) ListDeploymentsForCompare(_ context.Context, _ sqlc.ListDeploymentsForCompareParams) ([]sqlc.ListDeploymentsForCompareRow, error) {
	return nil, nil
}

// ListAppsWithRecentTelemetry is a no-op in MemStore. Returns nil so
// the regression cron's discovery loop is a no-op when wired against
// a MemStore (handy for unit tests that don't want to seed rows).
func (m *MemStore) ListAppsWithRecentTelemetry(_ context.Context, _ pgtype.Interval) ([]pgtype.UUID, error) {
	return nil, nil
}

// --- ADR-127 PR-D — spans_summary writer (MemStore stub) ---

// UpdateSpansSummary is a no-op in MemStore. The OTel spans writer
// runs only against the production PgStore (gatewayd-public flushes
// the spans_summary jsonb via apid's WriteSpansSummary gRPC RPC);
// the MemStore implementation exists solely to satisfy the Store
// interface so unit tests that wire up a MemStore still compile.
//
// PR-D code-review #1: accountID is ignored — MemStore doesn't
// enforce any predicate (it's an in-memory shim for unit tests
// that don't exercise cross-customer overwrite). The Store
// interface signature is uniform so the apid gRPC handler can
// pass accountID through unconditionally.
func (m *MemStore) UpdateSpansSummary(_ context.Context, _ string, _ uuid.UUID, _ []byte) error {
	return nil
}

// Issue #1182 §P1 packaging follow-up (PR-1 of 3): in-memory
// implementations of the upload-session Store methods. Production
// uses *PgStore against the upload_sessions table; these memstore
// impls back handler unit tests so the test author doesn't need a
// live DB to exercise the CAS / dedupe / cap logic. Minimal — just
// enough to satisfy the Store interface and pass the round-trip
// tests in cmd/apid/handlers_upload_session_test.go.

// CreateUploadSession inserts a fresh upload_sessions row. Mirrors
// pgstore + sqlc.CreateUploadSession but defaults created_at,
// last_patched_at, expires_at, and status to the same server-stamped
// values the live DB provides.
func (m *MemStore) CreateUploadSession(_ context.Context, in sqlc.CreateUploadSessionParams) (sqlc.UploadSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	row := sqlc.UploadSession{
		ID:            in.ID,
		AccountID:     in.AccountID,
		AppSlug:       in.AppSlug,
		TotalSize:     in.TotalSize,
		ReceivedBytes: 0,
		ChunkSize:     in.ChunkSize,
		Sha256Hex:     in.Sha256Hex,
		PartPath:      in.PartPath,
		Status:        "open",
		CreatedAt:     pgtype.Timestamptz{Time: now, Valid: true},
		LastPatchedAt: pgtype.Timestamptz{Time: now, Valid: true},
		ExpiresAt:     pgtype.Timestamptz{Time: now.Add(24 * time.Hour), Valid: true},
		DeploymentID:  pgtype.Text{String: "", Valid: false},
	}
	m.uploadSessions[row.ID] = row
	return row, nil
}

// GetUploadSession reads a single upload_sessions row by id.
// Returns ErrNotFound (the wire-stable sentinel; the handler maps
// it to api.ErrUploadSessionNotFound) when the id doesn't resolve.
func (m *MemStore) GetUploadSession(_ context.Context, id string) (sqlc.UploadSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.uploadSessions[id]
	if !ok {
		return sqlc.UploadSession{}, ErrNotFound
	}
	return row, nil
}

// AppendUploadBytes is the atomic CAS that backs PATCH /v1/uploads/{id}.
// The expectedOffset must equal the row's current received_bytes or
// the call returns ErrConflict (mapped by the handler to
// api.ErrUploadSessionOffsetConflict with the actual current
// received_bytes in the body).
func (m *MemStore) AppendUploadBytes(_ context.Context, in sqlc.AppendUploadBytesParams) (sqlc.AppendUploadBytesRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.uploadSessions[in.ID]
	if !ok {
		return sqlc.AppendUploadBytesRow{}, ErrNotFound
	}
	if row.Status != "open" || row.ReceivedBytes != in.ReceivedBytes_2 {
		return sqlc.AppendUploadBytesRow{
			ReceivedBytes: row.ReceivedBytes,
			TotalSize:     row.TotalSize,
		}, ErrConflict
	}
	row.ReceivedBytes = in.ReceivedBytes
	row.LastPatchedAt = pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
	m.uploadSessions[in.ID] = row
	return sqlc.AppendUploadBytesRow{
		ReceivedBytes: row.ReceivedBytes,
		TotalSize:     row.TotalSize,
	}, nil
}

// MarkUploadSessionCommitted transitions open → committed and stamps
// the deployment_id. Returns ErrConflict when the row is not open
// (terminal-state guard; the upload_commit_outcomes companion is the
// canonical dedupe).
func (m *MemStore) MarkUploadSessionCommitted(_ context.Context, in sqlc.MarkUploadSessionCommittedParams) (sqlc.MarkUploadSessionCommittedRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.uploadSessions[in.ID]
	if !ok {
		return sqlc.MarkUploadSessionCommittedRow{}, ErrNotFound
	}
	if row.Status != "open" {
		return sqlc.MarkUploadSessionCommittedRow{}, ErrConflict
	}
	row.Status = "committed"
	row.DeploymentID = in.DeploymentID
	m.uploadSessions[in.ID] = row
	return sqlc.MarkUploadSessionCommittedRow{
		ID:           row.ID,
		Status:       row.Status,
		DeploymentID: row.DeploymentID,
	}, nil
}

// CancelUploadSession transitions open → cancelled. The accountID
// predicate is a defense-in-depth check (the auth chain at
// cmd/apid/server.go already enforces account scoping; this makes
// accidental misuse crash-loud).
func (m *MemStore) CancelUploadSession(_ context.Context, in sqlc.CancelUploadSessionParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.uploadSessions[in.ID]
	if !ok {
		return ErrNotFound
	}
	if row.Status != "open" {
		return ErrConflict
	}
	if row.AccountID != in.Column2 {
		return ErrConflict
	}
	row.Status = "cancelled"
	m.uploadSessions[in.ID] = row
	return nil
}

// ReapExpiredUploadSessions scans up to 100 expired open sessions
// for the in-process reaper. Walks m.uploadSessions (the memstore
// is a test fixture, not the production hot path; no index
// discipline).
func (m *MemStore) ReapExpiredUploadSessions(_ context.Context) ([]sqlc.ReapExpiredUploadSessionsRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []sqlc.ReapExpiredUploadSessionsRow
	now := time.Now().UTC()
	for _, row := range m.uploadSessions {
		if row.Status == "open" && row.ExpiresAt.Valid && row.ExpiresAt.Time.Before(now) {
			out = append(out, sqlc.ReapExpiredUploadSessionsRow{
				ID:       row.ID,
				PartPath: row.PartPath,
			})
			if len(out) >= 100 {
				break
			}
		}
	}
	return out, nil
}

// ReapStaleUploadPartFiles returns terminal-row sessions whose
// last_patched_at < now() - 1h. Bounded by 100 rows. The
// 1h grace matches the production query — kept identical so
// the whitebox reaper tests exercise the same predicate.
func (m *MemStore) ReapStaleUploadPartFiles(_ context.Context) ([]sqlc.ReapStaleUploadPartFilesRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []sqlc.ReapStaleUploadPartFilesRow
	cutoff := time.Now().UTC().Add(-1 * time.Hour)
	for _, row := range m.uploadSessions {
		if row.Status != "committed" && row.Status != "cancelled" && row.Status != "expired" {
			continue
		}
		if row.LastPatchedAt.Valid && row.LastPatchedAt.Time.Before(cutoff) {
			out = append(out, sqlc.ReapStaleUploadPartFilesRow{
				ID:       row.ID,
				PartPath: row.PartPath,
			})
			if len(out) >= 100 {
				break
			}
		}
	}
	return out, nil
}

// ExpireUploadSession marks a single session expired. Idempotent —
// if the row is no longer open (committed / cancelled / already
// expired), returns nil and leaves the row untouched.
func (m *MemStore) ExpireUploadSession(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.uploadSessions[id]
	if !ok {
		return ErrNotFound
	}
	if row.Status != "open" {
		return nil
	}
	row.Status = "expired"
	m.uploadSessions[id] = row
	return nil
}

// RecordUploadCommitOutcome inserts a row into the
// upload_commit_outcomes companion table. Returns ErrConflict when
// the row already exists (ON CONFLICT DO NOTHING).
func (m *MemStore) RecordUploadCommitOutcome(_ context.Context, in sqlc.RecordUploadCommitOutcomeParams) (sqlc.UploadCommitOutcome, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.uploadCommitOutcomes[in.UploadID]; exists {
		return sqlc.UploadCommitOutcome{}, ErrConflict
	}
	row := sqlc.UploadCommitOutcome{
		UploadID:     in.UploadID,
		DeploymentID: in.DeploymentID,
		BuildID:      in.BuildID,
		FinalizedAt:  pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	}
	m.uploadCommitOutcomes[in.UploadID] = row
	return row, nil
}

// GetUploadCommitOutcome reads the dedupe row for a retry of POST
// /v1/uploads/{id}/commit.
func (m *MemStore) GetUploadCommitOutcome(_ context.Context, uploadID string) (sqlc.UploadCommitOutcome, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.uploadCommitOutcomes[uploadID]
	if !ok {
		return sqlc.UploadCommitOutcome{}, ErrNotFound
	}
	return row, nil
}

// CountOpenUploadSessionsByAccountApp backs the per-(account_id,
// app_slug) open-session cap check (5) at the top of POST /v1/uploads.
func (m *MemStore) CountOpenUploadSessionsByAccountApp(_ context.Context, in sqlc.CountOpenUploadSessionsByAccountAppParams) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var count int64
	for _, row := range m.uploadSessions {
		if row.Status == "open" && row.AccountID == in.Column1 && row.AppSlug == in.AppSlug {
			count++
		}
	}
	return count, nil
}

// SumOpenUploadSessionBytesByAccount backs the per-account open-
// spool budget check (4 × SourceTarballMaxMB) at the top of POST
// /v1/uploads.
func (m *MemStore) SumOpenUploadSessionBytesByAccount(_ context.Context, accountID pgtype.UUID) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var total int64
	for _, row := range m.uploadSessions {
		if row.Status == "open" && row.AccountID == accountID {
			total += row.TotalSize
		}
	}
	return total, nil
}
