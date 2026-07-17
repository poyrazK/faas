package state

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ErrNotFound is returned by Store reads when a row does not exist.
var ErrNotFound = errors.New("state: not found")

// MaxDeploymentLogPage caps the per-call row count for
// ListDeploymentLogs. Both implementations clamp the caller's
// `limit` to this value before allocating — defense in depth so a
// caller that forgets to validate a query-string `limit` can't
// trigger an oversized allocation (CodeQL go/allocation-size).
const MaxDeploymentLogPage = 500

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
	// AccountByStripeCustomerID resolves an account from the Stripe customer
	// ID. The webhook is the only caller; backed by an index in production
	// (deferred). Returns ErrNotFound for unknown customers.
	AccountByStripeCustomerID(ctx context.Context, stripeCustomerID string) (Account, error)
	// ListAllAccounts returns every account. meterd walks it on every
	// quota tick and every Stripe push; on a one-box that's bounded
	// (Free + Hobby + Pro + Scale test accounts + a handful of paid).
	ListAllAccounts(ctx context.Context) ([]Account, error)

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
	DeleteApp(ctx context.Context, id string) error

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
}
