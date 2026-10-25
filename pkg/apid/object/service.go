// Package object is apid's customer object storage surface: buckets, objects
// and versions, multipart uploads, signed URLs, upload routes, S3 credentials,
// compute bindings, encryption/object-lock/versioning/lifecycle settings,
// usage accounting and the recovery workers that finish provider operations.
// It was lifted out of cmd/apid by ADR-977.
//
// The package holds no state of its own. Everything it needs from apid goes
// through Host, which cmd/apid implements by reading its own fields at call
// time.
package object

import (
	"context"
	"log/slog"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// Host is what the object storage surface needs from apid.
type Host interface {
	httpkit.AppLoader
	Store() state.Store
	Logger() *slog.Logger
	EmitAudit(ctx context.Context, kind string, accountID *string, data map[string]any)
	Notify(ctx context.Context, channel, payload string) error
	// Ops is nil when apid runs without operational metrics.
	Ops() *wire.OpsMetrics
	// ObjectStorage is nil when no object storage backend is configured.
	ObjectStorage() *objectstorage.Registry
	BillingProvider() billing.Provider
	// S3RuntimeEnabled is the operator's runtime switch for the S3 surface.
	S3RuntimeEnabled() bool
	AdminAllows(acct state.Account) (bool, *api.Problem)
	// RequireScope wraps next in apid's scope check, for handlers that pick
	// the required scope from the request body.
	RequireScope(allowed ...string) func(httpkit.AccountHandler) httpkit.AccountHandler
	NotifyRuntimeConfigChange(ctx context.Context, channel string, acct state.Account, app state.App, kind, scope, key string)
	ClaimAppRestart(ctx context.Context, store state.Store, appID string) (bool, error)
	ReleaseAppRestartClaim(ctx context.Context, store state.Store, appID string) error
	CleanupDevSessionBucket(ctx context.Context, store state.ObjectBucketStore, bucket state.ObjectBucket) error
	// SecretRecipient, HostHMACKey and MFAIdentities return empty values when
	// the host secrets are not installed.
	SecretRecipient() *age.X25519Recipient
	// RuntimeSecretRecipient seals the app secrets a binding hands to the
	// workload, which vmmd opens (ADR 20261022111812).
	RuntimeSecretRecipient() *age.X25519Recipient
	// RuntimeSecretBinding is the account binding those secrets are sealed
	// with (ADR-20261023173624); zero while binding is off.
	RuntimeSecretBinding(accountID string) secretbox.Binding
	HostHMACKey() []byte
	MFAIdentities() []*age.X25519Identity
}

// Service is the object storage surface.
type Service struct {
	Host Host
	// InventoryWake, when set, lets bucket creation run the inventory sweep
	// now instead of at its next tick (H10-129). Buffered with capacity one.
	InventoryWake chan struct{}
	// health withdraws object storage from capabilities while a default
	// backend's provider refuses this installation's credentials.
	health providerHealth
}

// wakeObjectInventory asks the inventory sweep to run now. Admission needs a
// verified inventory of the target bucket, so a new bucket refused transfers
// until the minute-long sweep tick reached it (production-us hunt #10,
// H10-129). Never blocks.
func (s *Service) wakeObjectInventory() {
	select {
	case s.InventoryWake <- struct{}{}:
	default:
	}
}
