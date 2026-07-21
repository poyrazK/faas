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
	Limit  int // limits.DeployedApps at the time of the call
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
	ListDeploymentsForApp(ctx context.Context, appID string, limit, offset int) ([]Deployment, error)
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
	// SetDeploymentRootfs records the on-disk path + size of the per-app ext4
	// layer imaged produced for this deployment (spec §4.6, drive1). The
	// snapshot-prime handshake reads this when staging the cold boot so schedd
	// can attach drive1 from the right path (ADR-018). Only imaged writes it.
	SetDeploymentRootfs(ctx context.Context, id, path string, bytes int64) error

	// Builds (apid creates the queued row; builderd writes status, spec §9).
	CreateBuild(ctx context.Context, deploymentID string, kind DeploymentKind, sourceBytes int64, logPath string) (Build, error)
	BuildByID(ctx context.Context, id string) (Build, error)
	BuildByDeployment(ctx context.Context, deploymentID string) (Build, error)
	UpdateBuildStatus(ctx context.Context, id string, status BuildStatus, fc FailureClass, started, finished bool) error

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
	CreateInstance(ctx context.Context, appID, deploymentID, state string, ramMB int) (Instance, error)
	InstanceByID(ctx context.Context, id string) (Instance, error)
	ListInstancesForApp(ctx context.Context, appID string) ([]Instance, error)
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

	// Audit (append-only, spec §6.1).
	AppendEvent(ctx context.Context, actor, kind string, subject *string, data []byte) error
	ListEvents(ctx context.Context, subject string, limit int) ([]Event, error)

	// Usage (apid reads for GET /v1/usage; meterd writes in production).
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
