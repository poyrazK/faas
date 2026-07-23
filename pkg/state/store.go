package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ErrNotFound is returned by Store reads when a row does not exist.
var ErrNotFound = errors.New("state: not found")

// ErrQuotaExceeded is returned by CreateAppIfUnderQuota when the
// account already holds limits.DeployedApps live apps. The error wraps
// the observed count so apid can include it in the 403 envelope via
// api.ErrPlanLimitApps without re-running the count.
type QuotaError struct {
	Limit    int // limits.DeployedApps at the time of the call
	Observed int // count(*) of live apps observed inside the same critical section
}

func (e *QuotaError) Error() string {
	return fmt.Sprintf("state: deployed-app quota exceeded (limit=%d, observed=%d)", e.Limit, e.Observed)
}

// Is allows errors.Is(err, ErrQuotaExceeded) to match any *QuotaError.
// Behaviour parity with ErrNotFound / ErrConflict.
func (e *QuotaError) Is(target error) bool {
	return target == ErrQuotaExceeded
}

// ErrQuotaExceeded is the sentinel callers compare against via errors.Is.
// Concrete instances are *QuotaError so handlers can read limit/observed.
var ErrQuotaExceeded = errors.New("state: deployed-app quota exceeded")

// MaxDeploymentLogPage caps the per-call row count for
// ListDeploymentLogs. Both implementations clamp the caller's
// `limit` to this value before allocating — defense in depth so a
// caller that forgets to validate a query-string `limit` can't
// trigger an oversized allocation (CodeQL go/allocation-size).
const MaxDeploymentLogPage = 500

// clampLogLimit sanitizes the caller-supplied `limit` argument to
// ListDeploymentLogs so the slice allocation in the store
// implementations is provably bounded. CodeQL's
// go/allocation-size rule recognizes the result of this helper
// (small pure function returning a constant-bounded value) as a
// sanitizer; an inline `if limit > X { limit = X }` branch is not
// tracked.
func clampLogLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > MaxDeploymentLogPage {
		return MaxDeploymentLogPage
	}
	return limit
}

// Store is the persistence boundary apid and schedd depend on (spec §6, ADR-006).
// The production implementation is Postgres via the embedded SQL queries in
// pkg/state/queries.sql; MemStore backs unit tests. Keeping this interface
// narrow keeps the ownership rules enforceable — apid only touches
// customer-intent tables through the methods it is given.
type Store interface {
	// Accounts & auth.
	CreateAccount(ctx context.Context, email string, plan api.Plan) (Account, error)
	AccountByID(ctx context.Context, id string) (Account, error)
	AccountByEmail(ctx context.Context, email string) (Account, error)
	AccountByKeyHash(ctx context.Context, hash []byte) (Account, error)
	UpdateAccountPlan(ctx context.Context, id string, plan api.Plan) error
	UpdateAccountStatus(ctx context.Context, id string, status AccountStatus) error
	// UpdateAccountStripeCustomerID records the Stripe `cus_…` ID on the
	// account row so the webhook + push paths can join. Idempotent — a
	// repeat call with the same value is a no-op (ADR-010, Slice 2).
	UpdateAccountStripeCustomerID(ctx context.Context, id, stripeCustomerID string) error
	// UpdateAccountStripeSubscriptionItem records the Stripe metered
	// subscription item ID (si_…) so meterd's hourly push knows
	// where to POST UsageRecord (issue #52, M7). Empty until the
	// customer's first subscription.created webhook lands.
	UpdateAccountStripeSubscriptionItem(ctx context.Context, id, subItem string) error
	// AccountByStripeCustomerID resolves an account from the Stripe customer
	// ID. The webhook is the only caller; backed by an index in production
	// (deferred). Returns ErrNotFound for unknown customers.
	AccountByStripeCustomerID(ctx context.Context, stripeCustomerID string) (Account, error)
	// ListAllAccounts returns every account. meterd walks it on every
	// quota tick and every Stripe push; on a one-box that's bounded
	// (Free + Hobby + Pro + Scale test accounts + a handful of paid).
	ListAllAccounts(ctx context.Context) ([]Account, error)

	// Account-scoped deletion (spec §17 G6, ADR-021). The customer's
	// DELETE /v1/account schedules a 30-day grace window; pkg/grace in
	// apid sweeps on a 60s timer and calls DeleteAccount once the
	// window lapses. RestoreAccount flips the row back to active iff
	// called inside the grace window — past that the only honest
	// answer is ErrConflict and the handler returns 409.
	//
	// DeleteAccount is a single transaction that walks the FK graph in
	// dependency order (app_secrets → custom_domains → crons → instances
	// → snapshots → builds → deployments → apps → api_keys →
	// idempotency_keys → usage_minutes → accounts). Returns ErrNotFound
	// when the final accounts row is already gone, so a redelivered
	// grace tick is idempotent.
	//
	// MarkAccountDeletionPending is idempotent: a repeat call leaves
	// deletion_requested_at untouched (it carries the original grace
	// deadline). RestoreAccount zeroes deletion_requested_at.
	DeleteAccount(ctx context.Context, id string) error
	// AppendGdprRequest records a single GDPR self-service action
	// (export, delete, restore) against the account email at the
	// moment it lands. Append-only by contract; the gdpr_requests
	// table outlives DeleteAccount so a customer (or a DPO) can be
	// shown the proof of erasure against an email + timestamp. The
	// Completed field is set when the action has run to its end
	// (export = on insert, delete = after pg/grace hard-delete fires,
	// restore = on insert since restore is itself the endpoint).
	AppendGdprRequest(ctx context.Context, req GdprRequest) error
	// ListGdprRequestsForAccount returns the ledger rows for an
	// account in requested_at desc order, bounded by limit. Used by
	// the GDPR export bundle's audit slice so the customer sees their
	// own actions reflected in the same JSON.
	ListGdprRequestsForAccount(ctx context.Context, accountID string, limit int) ([]GdprRequest, error)
	// CompleteGdprRequest stamps completed_at on the most recent
	// un-completed row of (account_id, action). Called by pkg/grace
	// after DeleteAccount succeeds so the delete row in the ledger
	// carries the actual hard-delete timestamp.
	CompleteGdprRequest(ctx context.Context, accountID, action string) error
	ListBuildsForAccount(ctx context.Context, accountID string) ([]Build, error)
	ListCronsForAccount(ctx context.Context, accountID string) ([]Cron, error)
	// UsageByAccount aggregates every per-minute usage_minutes row that
	// landed in [since, now]. MemStore synthesizes the per-minute
	// rollup; PgStore runs a SELECT … WHERE account_id = $1 AND minute >= $2.
	// Empty since means "every row" — used by the GDPR export bundle.
	UsageByAccount(ctx context.Context, accountID string, since time.Time) ([]Usage, error)
	MarkAccountDeletionPending(ctx context.Context, id string) error
	RestoreAccount(ctx context.Context, id string) error

	// Dunning + quota warning (spec §4.7). These three are the load-
	// bearing primitives pkg/meter.Dunning + pkg/meter.EnforceQuota's
	// "warn" branch depend on.
	//
	// LoadAndStampLastQuotaWarning atomically stamps last_quota_warning_at
	// to the supplied UTC-day anchor AND reports whether the row already
	// carried a stamp for that day. Returns (true, nil) on a same-day
	// repeat call (the quota gate suppresses the second notify), (false,
	// nil) on a first-today call (caller emits the notify), and
	// ErrNotFound when the account row is gone.
	LoadAndStampLastQuotaWarning(ctx context.Context, accountID string, day time.Time) (alreadyWarned bool, err error)
	// ClearQuotaWarning nulls last_quota_warning_at so a customer who
	// paid an invoice (or any other path that resets the overage
	// counter) sees the next quota_warning on the *next* UTC day rather
	// than being skipped because of a stamp from days ago.
	ClearQuotaWarning(ctx context.Context, accountID string) error
	// MarkDunningStep atomically advances a row from `from` to `to`
	// (e.g. past_due → suspended), stamping past_due_at only when the
	// destination is past_due. Returns ErrNotFound when the row is
	// missing OR its status didn't match `from` (the latter is the
	// redelivery race: two ticks firing close together on the same
	// overdue row, the second must not double-transition).
	MarkDunningStep(ctx context.Context, accountID string, from, to AccountStatus) error

	// API keys.
	CreateAPIKey(ctx context.Context, accountID string, hash []byte, label string) (APIKey, error)
	DeleteAPIKey(ctx context.Context, accountID, keyID string) error
	ListAPIKeys(ctx context.Context, accountID string) ([]APIKey, error)
	TouchKeyLastUsed(ctx context.Context, keyID string) error

	// Login tokens (M7.5 magic-link, spec §14 + ADR-011).
	//
	// IssueLoginToken persists a freshly-minted token's SHA-256 hash
	// with an expiry; the raw token is returned to the caller to
	// embed in the email. ConsumeLoginToken marks the token consumed
	// AND returns the bound account_id in a single statement so a
	// replay returns ErrNotFound (or sql.ErrNoRows) — never a stale
	// account. The DeleteOldLoginTokens helper is a maintenance call
	// (the dashboard backend or a daily cron can prune).
	IssueLoginToken(ctx context.Context, tokenHash []byte, accountID string, expiresAt time.Time) error
	ConsumeLoginToken(ctx context.Context, tokenHash []byte) (string, error)
	DeleteOldLoginTokens(ctx context.Context, before time.Time) (int64, error)

	// CLI auth codes (spec §2.2 device-code flow). The mint + peek +
	// claim + consume cycle mirrors the magic-link primitives but with
	// a nullable account_id — the binding to a customer happens at
	// claim time (dashboard POST /cli-auth), not at mint time
	// (anonymous POST /v1/cli-auth/code).
	//
	// IssueCliAuthCode persists a freshly-minted code's SHA-256 hash
	// with no account (account_id NULL). PeekCliAuthCode returns the
	// row's status without mutating it (the dashboard render uses
	// this). ClaimCliAuthCode atomically transitions pending →
	// consumed and binds account_id in one statement; a racing second
	// claim returns ErrConflict. ConsumeCliAuthCode is the CLI's poll
	// path: returns (status, account_id, err) so the CLI can mint the
	// API key once it sees "consumed".
	IssueCliAuthCode(ctx context.Context, tokenHash []byte, expiresAt time.Time) error
	PeekCliAuthCode(ctx context.Context, tokenHash []byte) (api.CliAuthStatus, string, error)
	ClaimCliAuthCode(ctx context.Context, tokenHash []byte, accountID string) error
	ConsumeCliAuthCode(ctx context.Context, tokenHash []byte) (api.CliAuthStatus, string, error)

	// Apps (apid is the only writer, spec §Component ownership).
	CreateApp(ctx context.Context, app App) (App, error)
	// CreateAppIfUnderQuota inserts app iff the account currently holds
	// fewer than limits.DeployedApps live apps (active + evicted_cold).
	// The count + insert happen under a single critical section — PgStore
	// opens a transaction that SELECT … FOR UPDATE locks the parent
	// accounts row, MemStore holds m.mu — so two concurrent createApp
	// calls on a Free account cannot both pass the cap check (spec §4.2,
	// PR fix for the TOCTOU in handlers.go::createApp).
	//
	// Returns:
	//   - (App, nil) on success
	//   - (App{}, *QuotaError) when the cap is reached — handlers map
	//     this to 403 CodePlanLimitApps with limit + observed
	//   - (App{}, ErrConflict) when app.Slug is already taken
	//   - (App{}, ErrNotFound) when the account row is gone
	//   - (App{}, other) on transport / SQL errors
	//
	// limits.DeployedApps is the per-plan cap (api.MustLimitsFor(plan)).
	// Implementations enforce it authoritatively; callers MUST NOT also
	// call CountDeployedApps before this method (that's the bug).
	CreateAppIfUnderQuota(ctx context.Context, app App, limits api.Limits) (App, error)
	AppByID(ctx context.Context, id string) (App, error)
	AppBySlug(ctx context.Context, slug string) (App, error)
	ListApps(ctx context.Context, accountID string) ([]App, error)
	// ListAllApps returns every non-deleted app on the box. schedd's reaper and
	// cron loops walk this (one-box scale, spec §4.3); apid never calls it.
	ListAllApps(ctx context.Context) ([]App, error)
	// CountDeployedApps counts apps that occupy a deploy slot (active or
	// evicted_cold) for quota enforcement (spec §4.2).
	CountDeployedApps(ctx context.Context, accountID string) (int, error)
	UpdateApp(ctx context.Context, id string, p UpdateAppParams) (App, error)
	// RenameApp changes an app's slug atomically (issue #63). Returns
	// ErrNotFound if oldSlug doesn't belong to accountID; ErrConflict if
	// newSlug is already taken by another live app. MemStore holds the
	// same unique-slug invariant the Postgres `apps.slug` index enforces.
	RenameApp(ctx context.Context, accountID, oldSlug, newSlug string) (App, error)
	// SetAppMinInstances stamps the per-app floor (ux_spec §6.5) the
	// reaper honors when parking idle instances. 0 => scale to zero.
	// Plan-tier gating is the apid handler's job; the store writes the
	// column unconditionally. Updates an existing row's min_instances
	// column in place; returns ErrNotFound when the app is gone.
	SetAppMinInstances(ctx context.Context, appID string, min int) error
	DeleteApp(ctx context.Context, id string) error
	// RecordGitHubBinding persists the (app → installation_id, repo,
	// branch) tuple after the /oauth/callback handler verified the
	// installation against api.github.com. Idempotent: re-binding the
	// same app overwrites the previous values. Two apps cannot claim
	// the same (install_id, repo) pair — the migration enforces a
	// unique partial index for the §11 least-privilege audit.
	RecordGitHubBinding(ctx context.Context, appID string, installID int64, repoFullName, productionBranch string) error
	// GitHubBindingForApp returns the persisted binding for an app.
	// Returns ErrNotFound if the app has never been GitHub-connected
	// (the zero-value binding with installID==0 is also a miss; callers
	// that need to distinguish "bound to install 0" — impossible per
	// the migration check — from "not bound" should check err).
	GitHubBindingForApp(ctx context.Context, appID string) (GitHubBinding, error)
	// InstallationIDForRepo is the reverse-lookup that closes the
	// review-finding #1+#2 §11 least-privilege regression: githubd's
	// checks.go needs to mint the right per-install access token for
	// the repo's push, not the hardcoded installation_id=1 placeholder
	// that shipped with M7.5. Returns ErrNotFound if no app is bound
	// to (repo). When two apps are bound to the same (install_id,
	// repo) — impossible per the migration unique index — the first
	// hit wins; apid is the canonical owner of bindings so this is
	// not a contention point in practice.
	InstallationIDForRepo(ctx context.Context, repoFullName string) (int64, error)

	// Deployments.
	CreateDeployment(ctx context.Context, d Deployment) (Deployment, error)
	DeploymentByID(ctx context.Context, id string) (Deployment, error)
	LatestDeployment(ctx context.Context, appID string) (Deployment, error)
	// LiveDeployment returns the app's current live deployment (status='live').
	// schedd's wake path boots from this; ErrNotFound if the app has never had a
	// successful deploy (an app always has a live snapshot OR a cold-bootable
	// rootfs — never neither, invariant §6.2-3).
	LiveDeployment(ctx context.Context, appID string) (Deployment, error)
	LatestSupersededDeployment(ctx context.Context, appID string) (Deployment, error)
	// ListDeploymentsForApp returns deployments for an app, ordered DESC by
	// created_at. limit <= 0 means "no row cap" (return every remaining row
	// after offset). MemStore and PgStore both honour this contract — F-10
	// closed the prior silent asymmetry where Postgres' `LIMIT 0` returned
	// zero rows and MemStore returned all rows. NaN `offset` (= negative
	// value) is treated as 0 by both backends.
	ListDeploymentsForApp(ctx context.Context, appID string, limit, offset int) ([]Deployment, error)
	// SetDeploymentFailed is the failure-specific helper ADR-021 introduced
	// alongside the deployments.error_code column. Status is pinned to
	// 'failed'; code is the RFC 7807 code pkg/api.SentinelToCode lifted
	// from the wrapping error (empty when the failure did not map to a
	// sentinel); message is the free-text debug string. Returns the
	// refreshed row. Idempotent — a redeploy after a fix overwrites
	// both columns.
	SetDeploymentFailed(ctx context.Context, id, code, message string) (Deployment, error)
	// ListDeploymentsForAccount returns deployments across every app the
	// account owns, cursor-paginated by created_at DESC. before is the
	// inclusive upper bound — pass the previous response's NextBefore to
	// page backwards. limit is the page cap (caller validates a sane upper
	// bound). MemStore sorts in memory; PgStore uses a LIMIT/OFFSET or
	// keyset pagination (deferred — LIMIT/OFFSET is fine at one-box scale).
	ListDeploymentsForAccount(ctx context.Context, accountID string, before time.Time, limit int) ([]Deployment, error)

	// Deployment logs (M7.5 slice 5).
	//
	// AppendDeploymentLog inserts one row of build output. Builderd is
	// the writer in production; tests write directly. Returns the seq
	// Postgres assigned (or the MemStore-picked seq). The seq is what
	// the SSE endpoint returns as the cursor — the client pages by
	// `(deployment_id, seq < before_seq) ORDER BY seq DESC`.
	//
	// ListDeploymentLogs returns the page of rows whose seq is < before
	// (zero → newest page first), ordered DESC. Returns the rows +
	// hasMore so the caller knows there's another page without an
	// extra round-trip (rows == limit + 1 sentinel keeps the impl
	// cheap).
	AppendDeploymentLog(ctx context.Context, deploymentID, stream, line string) (seq int64, err error)
	ListDeploymentLogs(ctx context.Context, deploymentID string, beforeSeq int64, limit int) (rows []LogEntry, hasMore bool, err error)
	UpdateDeploymentStatus(ctx context.Context, id string, status DeploymentStatus, errMsg string) error
	MarkDeploymentSuperseded(ctx context.Context, id string) error
	MarkDeploymentLive(ctx context.Context, id string) error
	// SetDeploymentRootfs records the on-disk path + size + StorageBackend
	// key of the per-app ext4 layer imaged produced for this deployment
	// (spec §4.6, drive1). The snapshot-prime handshake reads this when
	// staging the cold boot so schedd can attach drive1 from the right
	// path / key (ADR-018, issue #96 / ADR-025 axis 2 — PR #116). Only
	// imaged writes it. rootfsKey is the canonical StorageBackend key
	// (e.g. "apps/<slug>/<depID>.ext4"); schedd carries it on the wake
	// wire and vmmd resolves it via Storage.Get before staging the chroot.
	SetDeploymentRootfs(ctx context.Context, id, path, key string, bytes int64) error

	// Builds (apid creates the queued row; builderd writes status, spec §9).
	CreateBuild(ctx context.Context, deploymentID string, kind DeploymentKind, sourceBytes int64, logPath string) (Build, error)
	BuildByID(ctx context.Context, id string) (Build, error)
	BuildByDeployment(ctx context.Context, deploymentID string) (Build, error)
	// ClaimQueuedBuild atomically transitions queued → running and returns
	// the row. Returns ErrNotFound when the row is missing OR when its
	// status is no longer BuildQueued — callers (builderd's ProcessOne)
	// use the second case to drop duplicate notifications from the apid
	// write path and the imaged reaper (PR-A). started_at is set to now.
	ClaimQueuedBuild(ctx context.Context, id string) (Build, error)
	UpdateBuildStatus(ctx context.Context, id string, status BuildStatus, fc FailureClass, started, finished bool) error
	// ListStaleQueuedBuilds returns builds still in BuildQueued whose
	// enqueued_at is older than threshold. The imaged reaper (PR-A)
	// walks this set on a tick and re-emits db.NotifyBuildQueued for
	// each, recovering from a missed pg_notify at the apid write path
	// (e.g. transient Postgres blip between INSERT and NOTIFY).
	// Builds is bounded by spec §9 (shallow queue), so a full scan is
	// cheap; if pressure emerges, add a partial index on
	// (status, enqueued_at) WHERE status='queued'.
	ListStaleQueuedBuilds(ctx context.Context, threshold time.Duration) ([]Build, error)

	// Custom domains (apid is sole writer).
	CreateCustomDomain(ctx context.Context, domain, appID, token string) (CustomDomain, error)
	DomainByName(ctx context.Context, domain string) (CustomDomain, error)
	ListDomainsForApp(ctx context.Context, appID string) ([]CustomDomain, error)
	ListDomainsForAccount(ctx context.Context, accountID string) ([]CustomDomain, error)
	MarkDomainVerified(ctx context.Context, domain string) error
	DeleteCustomDomain(ctx context.Context, domain string) error

	// Crons (apid CRUDs; schedd fires).
	CreateCron(ctx context.Context, appID, schedule, path string, enabled bool) (Cron, error)
	CronByID(ctx context.Context, id string) (Cron, error)
	// UpdateCron mutates the optional fields of a cron row. nil pointers
	// leave the field untouched. createdAt is supported because schedd's
	// dispatch loop reads the boundary off CreatedAt (first-fire guard);
	// backfilling this field is the only honest way to rewind a test or
	// restore an imported schedule.
	UpdateCron(ctx context.Context, id string, schedule, path *string, enabled *bool, createdAt *time.Time) (Cron, error)
	DeleteCron(ctx context.Context, id, appID string) error
	ListCronsForApp(ctx context.Context, appID string) ([]Cron, error)
	ListEnabledCrons(ctx context.Context) ([]Cron, error)
	// MarkCronFired stamps the last_fired_at column. The schedd cron
	// dispatch loop calls this after a synthetic request has been
	// dispatched through gatewayd (spec §4.4, M7). MemStore keeps a
	// lastFiredAt map; PgStore uses a column added in migration 00003.
	MarkCronFired(ctx context.Context, cronID string, at time.Time) error

	// Instances (schedd is sole writer, spec §6). apid reads only.
	//
	// nodeID is the compute_node the instance lives on (issue #97 /
	// ADR-025 axis 3). schedd's Wake flow resolves it via
	// sched.ChoosePlacement at instance creation; tests that don't
	// exercise routing may pass DefaultLocalNodeName (or the id
	// resolved via ComputeNodeByName) — the engine never accepts an
	// empty node_id once CreateInstance is reached, so the legacy
	// single-box path always passes DefaultLocalNodeName at minimum.
	//
	// wakeID is the per-wake-attempt correlation handle (gaps analysis
	// 2026-07-23); schedd mints this UUIDv7 in Engine.Wake Phase 2 right
	// before the INSERT so every row created by Wake carries a unique
	// wake_id. An empty wakeID triggers the column default
	// (gen_random_uuid()) which is the safe behavior for any caller that
	// hasn't been updated yet (test fixtures, ad-hoc backfill scripts).
	// Migration 00027 enforces NOT NULL going forward, so passing empty
	// is fine — the row still has a non-NULL wake_id after the write.
	CreateInstance(ctx context.Context, appID, deploymentID, state string, ramMB int, nodeID, wakeID string) (Instance, error)
	InstanceByID(ctx context.Context, id string) (Instance, error)
	ListInstancesForApp(ctx context.Context, appID string) ([]Instance, error)
	// ListLatestInstancesForApp returns up to `limit` instance rows for
	// appID ordered by started_at DESC. The dashboard's app-detail
	// "Recent wakes" table uses this to bound the per-render scan at
	// the SQL layer instead of fetching every row (including parked
	// history) and sorting in Go. limit must be > 0; a value ≤ 0
	// returns an empty slice so the caller fails closed rather than
	// rendering an unbounded table. The supporting partial index
	// `instances_wake_id_app_idx` (migration 00027) covers live
	// states but not parked; the SQL still scans parked rows in the
	// sort phase, so a future index on (app_id, started_at DESC)
	// WHERE state = 'parked' is the right optimization if a single
	// app accumulates enough parked history to make this slow.
	ListLatestInstancesForApp(ctx context.Context, appID string, limit int) ([]Instance, error)
	// ListLatestInstancePerApp returns the most-recently-started instance
	// for each app belonging to the account. Empty map when no instance
	// rows exist yet (a fresh deploy never woken). Used by the dashboard
	// to populate the cold-wake state badge in one round-trip instead of
	// N per-app ListInstancesForApp calls (PR #48 follow-up). Result is
	// keyed by app ID; callers must handle the "no row" case explicitly.
	ListLatestInstancePerApp(ctx context.Context, accountID string) (map[string]Instance, error)
	// ListAllInstances returns every instance on the box, ordered newest
	// first. schedd's G7 reaper warm-passes this slice to the conntrack
	// reader (pkg/sched/flowcount) once per tick — a single bulk read is
	// cheaper than a per-app loop and lets the reader index by host_ip
	// up front. Scoped to RUNNING/WAKING/COLD_BOOTING/SNAPSHOTTING because
	// parked/stopped/failed instances have no veth and no flows by
	// construction (invariant §6.2-4). The partial index
	// `instances_reaper_state_idx` (migration 00009) covers this query.
	ListAllInstances(ctx context.Context) ([]Instance, error)
	// ListInstancesForAccount returns every live instance belonging to an
	// account. Used by the meterd quota loop to park everything when a Free
	// account crosses 100 % (spec §4.7). Per-account scan is O(instances);
	// on a one-box that's bounded by max_concurrency(plan) × apps, fine to
	// run on the minute boundary.
	ListInstancesForAccount(ctx context.Context, accountID string) ([]Instance, error)
	UpdateInstanceState(ctx context.Context, id, state string) error
	// UpdateInstanceStateWithTimestamp is the same write but stamps
	// parked_at to the supplied time on the same statement. Used by
	// schedd's snapshotAndPark (commit 3) when transitioning into
	// SNAPSHOTTING — the §6.1 watchdog reads parked_at for
	// SNAPSHOTTING rows (started_at means "row creation", not "time
	// entered current state"), so the engine must stamp it on entry.
	// Non-SNAPSHOTTING transitions should still use UpdateInstanceState.
	UpdateInstanceStateWithTimestamp(ctx context.Context, id, state string, parkedAt time.Time) error
	// UpdateInstanceStateToTerminal writes state AND stamps terminal_at
	// on the same UPDATE (PR #74, spec §17 follow-up). terminal_at is
	// the dedicated retention anchor the daily sweep (pkg/sched.Retention)
	// reads; started_at means "row creation" and parked_at is overloaded
	// (also means "entered PARKED"), so neither is correct for a STOPPED
	// row whose vmmd boot succeeded days earlier. Engine.transition
	// routes here when the target state is STOPPED or FAILED; every other
	// transition still uses UpdateInstanceState / UpdateInstanceStateWithTimestamp.
	UpdateInstanceStateToTerminal(ctx context.Context, id, state string, terminalAt time.Time) error
	// ListInstancesByStatesOlderThan is the §6.1 watchdog's lookup.
	// Returns rows currently in any of the given states whose
	// "age timestamp" is strictly older than threshold. The age
	// column is state-aware: started_at for WAKING/COLD_BOOTING
	// (stamped on creation by migration 00015), parked_at for
	// SNAPSHOTTING (stamped on entry into that state by
	// UpdateInstanceStateWithTimestamp). Implementations must NOT
	// coalesce the two columns — pre-migration 00015 rows have
	// NULL started_at, and coalesce would silently use the stale
	// parked_at. PgStore relies on migration 00016's partial index
	// for the state predicate.
	ListInstancesByStatesOlderThan(ctx context.Context, states []State, threshold time.Time) ([]Instance, error)
	// ListInstancesInTerminalStatesOlderThan is the §17 retention sweep's
	// lookup (PR #74). Returns rows currently in any of the given states
	// (today: {STOPPED, FAILED}) whose terminal_at is strictly older than
	// threshold. Order is implementation-defined. Reads the dedicated
	// terminal_at column — distinct from ListInstancesByStatesOlderThan,
	// which uses the state-aware started_at/parked_at comparison and is
	// the wrong tool for retention aging (a STOPPED row that booted
	// successfully has a stale started_at). PgStore relies on migration
	// 00017's partial index for the state predicate.
	ListInstancesInTerminalStatesOlderThan(ctx context.Context, states []State, threshold time.Time) ([]Instance, error)
	// DeleteInstance removes a single instance row unconditionally
	// (PR #74). Returns ErrNotFound when the row is already gone — the
	// retention sweep swallows that case for redelivery. There are NO
	// foreign-key cascades: events.subject and usage_minutes.instance_id
	// carry no FK to instances today (audit log is append-only by spec
	// §6.1; usage is reconciled by account hard-delete). Adding a FK
	// in a future migration would silently break this sweep — review
	// PR-#74's readme when touching either table.
	DeleteInstance(ctx context.Context, id string) error
	// SetInstanceRuntime records the per-instance identity vmmd allocated on
	// wake (netns, routable host IP, jail uid) and stamps started_at=now. schedd
	// calls this between a successful vmmd boot and the RUNNING transition so the
	// gateway can route to host_ip:8080 (spec §7).
	SetInstanceRuntime(ctx context.Context, id, netns, hostIP string, guestUID int) error
	// RunningInstanceForApp returns the newest RUNNING instance for an app, or
	// ErrNotFound when none is live. schedd uses it to make Wake idempotent and
	// the gateway to seed its route target on startup.
	RunningInstanceForApp(ctx context.Context, appID string) (Instance, error)
	// TouchInstancesLastSeen batches last_request_at updates the gateway flushes
	// every 15 s (spec §4.1). schedd is the sole writer to instances, so the
	// gateway hands it the batch (ADR-018). Returns the number of rows updated.
	TouchInstancesLastSeen(ctx context.Context, touches []InstanceTouch) (int, error)

	// Snapshots (imaged is sole writer; schedd reads latest non-stale and marks
	// stale on a failed restore, ADR-005).
	CreateSnapshot(ctx context.Context, snap Snapshot) (Snapshot, error)
	LatestSnapshot(ctx context.Context, deploymentID string) (Snapshot, error)
	MarkSnapshotStale(ctx context.Context, snapshotID string) error

	// Snapshot GC (imaged nightly + on FC upgrade, spec §4.6 + §4.4).
	//
	// ListSnapshotsForGC returns every non-stale snapshot joined with its
	// deployment + app + account. Soft-deleted apps (status='deleted') are
	// excluded; their snapshots have no in-flight wake target.
	ListSnapshotsForGC(ctx context.Context) ([]SnapshotForGC, error)
	// DeleteSnapshotsByID bulk-removes the named snapshot rows (no cascade).
	// Returns the number of rows deleted; a second call with the same ids
	// returns 0 and no error.
	DeleteSnapshotsByID(ctx context.Context, ids []string) (int64, error)
	// MarkAllSnapshotsStaleByFCVersion flips every non-stale row whose
	// fc_version != currentVersion stale (ADR-005: snapshots are pinned to
	// the Firecracker version that made them). Returns the number of rows
	// affected. Idempotent.
	MarkAllSnapshotsStaleByFCVersion(ctx context.Context, currentVersion string) (int64, error)
	// MarkOldSnapshotsStale marks the given snapshot IDs stale (per-app
	// "current + previous" enforcement, run before DeleteSnapshotsByID).
	MarkOldSnapshotsStale(ctx context.Context, beforeSnapshotIDs []string) (int64, error)
	// DeleteSnapshotsStaleOlderThan removes rows where stale=true AND
	// created_at < now()-retention. Used by imaged's F2 startup sweep
	// after the mark-stale step: old snapshots stay restorable for a
	// retention window (typically 7 days per api.SnapshotStaleRetention)
	// so a firecracker downgrade or operator rollback doesn't pay an
	// extra cold boot. After the window they go away. Returns the row
	// count. Idempotent.
	DeleteSnapshotsStaleOlderThan(ctx context.Context, retention time.Duration) (int64, error)

	// Compute nodes (issue #97 / ADR-025 axis 3). schedd's Wake flow
	// is the sole reader of these methods (single-leader CP, no
	// consensus); apid writes via CreateComputeNode on
	// POST /v1/compute-nodes. The synthetic 'default-local' row is
	// seeded by migrations/00024_compute_nodes.sql so production
	// callers never have to insert it themselves.
	//
	// ActiveComputeNodes returns every active compute_node for
	// placement (Wake asks "which node has headroom?"; the partial
	// compute_nodes_active_idx keeps inactive rows out of the read
	// path). Order is by name (placement sorts in memory after
	// computing used_mb per node).
	ActiveComputeNodes(ctx context.Context) ([]ComputeNode, error)
	// ComputeNodeByID resolves a row by primary key. Wake calls this
	// after placement to fetch the target URL for the dial step.
	// Returns ErrNotFound when the id has no row.
	ComputeNodeByID(ctx context.Context, id string) (ComputeNode, error)
	// ComputeNodeByName resolves a row by its unique name. The
	// engine's startup path uses this once to cache the
	// default-local UUID (DefaultLocalNodeName → id) so subsequent
	// Wake flows don't repeat the SELECT. Returns ErrNotFound when
	// the name has no row — a config / migration drift that the
	// boot path surfaces as a loud failure (don't paper over it with
	// a default).
	ComputeNodeByName(ctx context.Context, name string) (ComputeNode, error)
	// ComputeNodeUsedMB returns the Σ(ram_mb + PerVMOverheadMB) for
	// live instances on the given node. Single SQL aggregate, no
	// client loop. Live = state IN ('waking','cold_booting',
	// 'running') per spec §6.2-2 re-stated per-node. Atomic with
	// the ledger; the ledger is the cache, this is the source of
	// truth after a schedd restart. PerVMOverheadMB is the 8 MB
	// fixed cost (spec §4.7 / billing model) added per live instance.
	ComputeNodeUsedMB(ctx context.Context, nodeID string) (int64, error)
	// HeartbeatComputeNode stamps last_heartbeat_at to now(). The
	// schedd watchdog goroutine calls this every HeartbeatInterval
	// (default 30s, env-overridable) for each registered node whose
	// dial succeeded. Idempotent — repeated calls just bump the
	// timestamp. A future gate will flip active=false when the
	// timestamp ages past the staleness threshold (2× the heartbeat
	// cadence); that policy lives in the watchdog, not here.
	HeartbeatComputeNode(ctx context.Context, nodeID string) error
	// CreateComputeNode inserts a new compute_node row on
	// POST /v1/compute-nodes (operator-only admin endpoint). The id
	// is gen_random_uuid() (column default). Returns the inserted
	// row with its assigned id and created_at. ErrConflict when
	// the name is already taken (UNIQUE constraint on name).
	CreateComputeNode(ctx context.Context, node ComputeNode) (ComputeNode, error)
	// MarkComputeNodeInactive flips a row's active flag to false
	// (issue #97 / ADR-025 axis 3, PR #114). schedd's heartbeat
	// loop calls this when VMRouter.Ping fails — placement's
	// ActiveComputeNodes filter then excludes the dead node so
	// future wakes don't dial an unreachable target. Idempotent:
	// flipping an already-inactive row is a no-op UPDATE. A
	// future staleness gate (last_heartbeat_at > 2 × interval)
	// will reuse this method; today only the heartbeat path
	// calls it. The row is preserved (no DELETE) so an operator
	// can flip it back via a future admin endpoint without
	// re-provisioning the cert/target_url.
	MarkComputeNodeInactive(ctx context.Context, nodeID string) error
	// UpsertComputeNode inserts or updates a row by name. The
	// vmmd self-registration path calls this on startup
	// (issue #98 / ADR-028): a node rebooting should bring itself
	// back without operator intervention. ON CONFLICT (name) DO
	// UPDATE SET target_url, vpcpus, mem_mb, max_concurrency,
	// admission_ceiling_mb, active=true — re-applies operator
	// config and re-activates a previously drained row in one
	// round-trip. Returns the row (id, timestamps refreshed).
	// ErrConflict is reserved for a future partial-cluster failure;
	// the upsert path doesn't currently fail.
	UpsertComputeNode(ctx context.Context, node ComputeNode) (ComputeNode, error)
	// SetComputeNodeActive flips the active flag on a row by id.
	// The schedd heartbeat staleness gate (issue #98) calls this
	// to mark a node active=false when last_heartbeat_at ages past
	// 90s, and again active=true when a heartbeat succeeds for a
	// previously-drained node. Emits compute_node_changed via the
	// pg_notify listener (pkg/db/notify.NotifyComputeNodeChanged) so
	// gatewayd can add or drop its per-node client without a
	// restart. ErrNotFound when the id has no row.
	SetComputeNodeActive(ctx context.Context, id string, active bool) error
	// ListComputeNodes returns every compute_node in name order.
	// includeInactive=false (default) returns only active rows
	// (placement-equivalent); apid's GET /v1/compute-nodes handler
	// passes true so operators can drain visibility. Backed by the
	// existing compute_nodes_active_idx partial index.
	ListComputeNodes(ctx context.Context, includeInactive bool) ([]ComputeNode, error)
	// DeleteComputeNode hard-deletes a row by id. apid's
	// DELETE /v1/compute-nodes/{name}?hard=1 is the only caller;
	// soft-delete via SetComputeNodeActive(false) is the default
	// for the routine operator workflow. Returns ErrNotFound if
	// the id is unknown.
	DeleteComputeNode(ctx context.Context, id string) error

	// Audit (append-only, spec §6.1).
	AppendEvent(ctx context.Context, actor, kind string, subject *string, data []byte) error
	ListEvents(ctx context.Context, subject string, limit int) ([]Event, error)

	// Usage (apid reads for GET /v1/usage; meterd writes in production).
	// AppendUsage is idempotent on (instance_id, minute): the first write
	// wins, a redelivered minute is a no-op. This prevents silent
	// double-billing under any meterd restart (M7 hardening).
	AppendUsage(ctx context.Context, accountID, appID, instanceID string, minute time.Time, mbSeconds, requests int64) error
	UsageByMonth(ctx context.Context, accountID string, month time.Time) ([]Usage, error)
	// UsageByHour returns the per-app usage rows whose minute ∈ [start,
	// end). The Stripe pusher calls this hourly to compute the billable
	// GB-RAM-hours for the past hour (spec §4.7, ADR-010). MemStore scans
	// in memory; PgStore runs a SELECT … WHERE minute >= $2 AND minute < $3.
	UsageByHour(ctx context.Context, accountID string, start, end time.Time) ([]Usage, error)

	// StripePushDedup is the dedupe table for hourly usage pushes. The
	// PushDedupe interface in pkg/stripex is satisfied by both stores.
	HasStripePushHour(ctx context.Context, accountID string, hour time.Time) (bool, error)
	RecordStripePushHour(ctx context.Context, accountID string, hour time.Time) error

	// Idempotency (spec §4.2: Idempotency-Key stored 24 h).
	GetIdempotent(ctx context.Context, accountID, key string) (status int, body []byte, err error)
	PutIdempotent(ctx context.Context, accountID, key string, status int, body []byte) error

	// Customer secrets (spec §11/G2). apid is the only writer; schedd reads
	// ciphertext rows at wake time to hand to vmmd. Ciphertext is age-sealed
	// (pkg/secretbox); the plaintext VALUE is never stored.
	//
	// UpsertAppSecret writes-or-replaces the (app_id, key) row. accountID is
	// passed for ownership verification (the handler must own the app before
	// it can set a secret on it); the row also stores account_id for audit
	// and for the account-scoped delete path.
	UpsertAppSecret(ctx context.Context, accountID, appID, key string, ciphertext []byte) error
	// DeleteAppSecret removes the (app_id, key) row. Returns ErrNotFound if
	// the row doesn't exist — handlers render 400 CodeSecretNotFound (not a
	// 404) because the URL resource IS the secret name, by design.
	DeleteAppSecret(ctx context.Context, accountID, appID, key string) error
	// ListAppSecrets returns every secret on the app (key + ciphertext). The
	// handler renders KEYS only; ciphertext flows to vmmd. Returns nil slice
	// (not error) when the app has no secrets.
	ListAppSecrets(ctx context.Context, accountID, appID string) ([]AppSecret, error)
	// CountAppSecrets is the quota check helper. apid calls it before
	// UpsertAppSecret to enforce Limits.SecretCountMax.
	CountAppSecrets(ctx context.Context, accountID, appID string) (int, error)
}
