package state

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
)

// ErrNotFound is returned by Store reads when a row does not exist.
var ErrNotFound = errors.New("state: not found")

// Store is the persistence boundary apid and schedd depend on (spec §6, ADR-006).
// The production implementation is Postgres via sqlc; MemStore backs unit tests.
// Keeping this interface narrow keeps the ownership rules enforceable — apid only
// touches customer-intent tables through the methods it is given.
type Store interface {
	// Accounts & auth.
	CreateAccount(ctx context.Context, email string, plan api.Plan) (Account, error)
	AccountByEmail(ctx context.Context, email string) (Account, error)
	AccountByKeyHash(ctx context.Context, hash []byte) (Account, error)

	// API keys.
	CreateAPIKey(ctx context.Context, accountID string, hash []byte, label string) (APIKey, error)
	DeleteAPIKey(ctx context.Context, accountID, keyID string) error

	// Apps (apid is the only writer, spec §Component ownership).
	CreateApp(ctx context.Context, app App) (App, error)
	AppByID(ctx context.Context, id string) (App, error)
	AppBySlug(ctx context.Context, slug string) (App, error)
	ListApps(ctx context.Context, accountID string) ([]App, error)
	// CountDeployedApps counts apps that occupy a deploy slot (active or
	// evicted_cold) for quota enforcement (spec §4.2).
	CountDeployedApps(ctx context.Context, accountID string) (int, error)
	DeleteApp(ctx context.Context, id string) error

	// Deployments.
	CreateDeployment(ctx context.Context, d Deployment) (Deployment, error)
	LatestDeployment(ctx context.Context, appID string) (Deployment, error)

	// Idempotency (spec §4.2: Idempotency-Key stored 24 h).
	GetIdempotent(ctx context.Context, accountID, key string) (status int, body []byte, err error)
	PutIdempotent(ctx context.Context, accountID, key string, status int, body []byte) error
}
