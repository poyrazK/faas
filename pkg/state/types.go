package state

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Domain types mirroring the schema (spec §5). These are the rows apid and
// schedd read and write; the Store abstracts the actual Postgres access (sqlc in
// production, the in-memory store in tests).

// AccountStatus tracks billing/dunning state (spec §4.7).
type AccountStatus string

const (
	AccountActive         AccountStatus = "active"
	AccountPastDue        AccountStatus = "past_due"
	AccountSuspended      AccountStatus = "suspended"
	AccountDeletedPending AccountStatus = "deleted_pending"
)

// AppType distinguishes a plain App from a Function (spec §2, ADR-003).
type AppType string

const (
	AppTypeApp      AppType = "app"
	AppTypeFunction AppType = "function"
)

// AppStatus is the app's lifecycle (distinct from an instance's State).
type AppStatus string

const (
	AppActive      AppStatus = "active"
	AppEvictedCold AppStatus = "evicted_cold"
	AppDeleted     AppStatus = "deleted"
)

// DeploymentStatus tracks a deployment through the pipeline (spec §5, §9).
type DeploymentStatus string

const (
	DeployPending      DeploymentStatus = "pending"
	DeployBuilding     DeploymentStatus = "building"
	DeployImaging      DeploymentStatus = "imaging"
	DeploySnapshotting DeploymentStatus = "snapshotting"
	DeployLive         DeploymentStatus = "live"
	DeployFailed       DeploymentStatus = "failed"
	DeploySuperseded   DeploymentStatus = "superseded"
)

// Account is a customer account.
type Account struct {
	ID               string
	Email            string
	Plan             api.Plan
	Status           AccountStatus
	StripeCustomerID string
	CreatedAt        time.Time
}

// Active reports whether the account may deploy (not suspended/deleted).
func (a Account) Active() bool { return a.Status == AccountActive || a.Status == AccountPastDue }

// APIKey is a hashed, account-scoped credential.
type APIKey struct {
	ID         string
	AccountID  string
	Hash       []byte
	Label      string
	LastUsedAt time.Time
	CreatedAt  time.Time
}

// App is a deployed application (or function).
type App struct {
	ID             string
	AccountID      string
	Slug           string
	Type           AppType
	Runtime        string // node22|python312 for functions
	RAMMB          int
	IdleTimeoutS   int // 0 => plan default
	MaxConcurrency int
	Status         AppStatus
	CreatedAt      time.Time
}

// Deployment is one attempt to ship a version of an app.
type Deployment struct {
	ID          string
	AppID       string
	BuildID     string // empty for image: deploys
	ImageDigest string
	RootfsPath  string
	Status      DeploymentStatus
	Error       string
	CreatedAt   time.Time
}
