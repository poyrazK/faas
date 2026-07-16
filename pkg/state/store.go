package state

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ErrNotFound is returned by Store reads when a row does not exist.
var ErrNotFound = errors.New("state: not found")

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

	// API keys.
	CreateAPIKey(ctx context.Context, accountID string, hash []byte, label string) (APIKey, error)
	DeleteAPIKey(ctx context.Context, accountID, keyID string) error
	ListAPIKeys(ctx context.Context, accountID string) ([]APIKey, error)
	TouchKeyLastUsed(ctx context.Context, keyID string) error

	// Apps (apid is the only writer, spec §Component ownership).
	CreateApp(ctx context.Context, app App) (App, error)
	AppByID(ctx context.Context, id string) (App, error)
	AppBySlug(ctx context.Context, slug string) (App, error)
	ListApps(ctx context.Context, accountID string) ([]App, error)
	// CountDeployedApps counts apps that occupy a deploy slot (active or
	// evicted_cold) for quota enforcement (spec §4.2).
	CountDeployedApps(ctx context.Context, accountID string) (int, error)
	UpdateApp(ctx context.Context, id string, p UpdateAppParams) (App, error)
	DeleteApp(ctx context.Context, id string) error

	// Deployments.
	CreateDeployment(ctx context.Context, d Deployment) (Deployment, error)
	DeploymentByID(ctx context.Context, id string) (Deployment, error)
	LatestDeployment(ctx context.Context, appID string) (Deployment, error)
	LatestSupersededDeployment(ctx context.Context, appID string) (Deployment, error)
	ListDeploymentsForApp(ctx context.Context, appID string, limit, offset int) ([]Deployment, error)
	UpdateDeploymentStatus(ctx context.Context, id string, status DeploymentStatus, errMsg string) error
	MarkDeploymentSuperseded(ctx context.Context, id string) error
	MarkDeploymentLive(ctx context.Context, id string) error

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
	UpdateCron(ctx context.Context, id string, schedule, path *string, enabled *bool) (Cron, error)
	DeleteCron(ctx context.Context, id, appID string) error
	ListCronsForApp(ctx context.Context, appID string) ([]Cron, error)
	ListEnabledCrons(ctx context.Context) ([]Cron, error)

	// Instances (schedd is sole writer, spec §6). apid reads only.
	CreateInstance(ctx context.Context, appID, deploymentID, state string, ramMB int) (Instance, error)
	InstanceByID(ctx context.Context, id string) (Instance, error)
	ListInstancesForApp(ctx context.Context, appID string) ([]Instance, error)
	UpdateInstanceState(ctx context.Context, id, state string) error

	// Snapshots (imaged is sole writer; schedd reads latest non-stale).
	CreateSnapshot(ctx context.Context, snap Snapshot) (Snapshot, error)
	LatestSnapshot(ctx context.Context, deploymentID string) (Snapshot, error)

	// Audit (append-only, spec §6.1).
	AppendEvent(ctx context.Context, actor, kind string, subject *string, data []byte) error
	ListEvents(ctx context.Context, subject string, limit int) ([]Event, error)

	// Usage (apid reads for GET /v1/usage; meterd writes in production).
	AppendUsage(ctx context.Context, accountID, appID, instanceID string, minute time.Time, mbSeconds, requests int64) error
	UsageByMonth(ctx context.Context, accountID string, month time.Time) ([]Usage, error)

	// Idempotency (spec §4.2: Idempotency-Key stored 24 h).
	GetIdempotent(ctx context.Context, accountID, key string) (status int, body []byte, err error)
	PutIdempotent(ctx context.Context, accountID, key string, status int, body []byte) error
}
