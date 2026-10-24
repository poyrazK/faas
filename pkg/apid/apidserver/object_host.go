package apidserver

import (
	"context"
	"log/slog"
	"net/http"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/apid/object"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// WithObjectStorage installs the object storage backend registry.
func (s *server) WithObjectStorage(registry *objectstorage.Registry) *server {
	s.objectStorage = registry
	return s
}

// objectHost adapts *server to object.Host (ADR-977). It reads server fields
// at call time, so tests that swap s.store or the registry after construction
// behave exactly as they did before the lift.
type objectHost struct{ s *server }

var _ object.Host = objectHost{}

func (h objectHost) LoadApp(w http.ResponseWriter, r *http.Request, acct state.Account, slug string) (state.App, bool) {
	return h.s.loadApp(w, r, acct, slug)
}

func (h objectHost) Store() state.Store { return h.s.store }

func (h objectHost) Logger() *slog.Logger { return h.s.log }

func (h objectHost) EmitAudit(ctx context.Context, kind string, accountID *string, data map[string]any) {
	h.s.audit.Emit(ctx, kind, accountID, data)
}

func (h objectHost) Notify(ctx context.Context, channel, payload string) error {
	return h.s.notif.Notify(ctx, channel, payload)
}

func (h objectHost) Ops() *wire.OpsMetrics { return h.s.ops }

func (h objectHost) ObjectStorage() *objectstorage.Registry { return h.s.objectStorage }

func (h objectHost) BillingProvider() billing.Provider { return h.s.billingProvider }

func (h objectHost) S3RuntimeEnabled() bool { return h.s.runtimeBool(runtimeConfigS3, false) }

func (h objectHost) AdminAllows(acct state.Account) (bool, *api.Problem) {
	return h.s.adminAllows(acct)
}

func (h objectHost) RequireScope(allowed ...string) func(httpkit.AccountHandler) httpkit.AccountHandler {
	return h.s.requireScope(allowed...)
}

func (h objectHost) NotifyRuntimeConfigChange(ctx context.Context, channel string, acct state.Account, app state.App, kind, scope, key string) {
	h.s.notifyRuntimeConfigChange(ctx, channel, acct, app, kind, scope, key)
}

func (h objectHost) ClaimAppRestart(ctx context.Context, store state.Store, appID string) (bool, error) {
	return claimAppRestart(ctx, store, appID)
}

func (h objectHost) ReleaseAppRestartClaim(ctx context.Context, store state.Store, appID string) error {
	return releaseAppRestartClaim(ctx, store, appID)
}

func (h objectHost) CleanupDevSessionBucket(ctx context.Context, store state.ObjectBucketStore, bucket state.ObjectBucket) error {
	return h.s.cleanupDevSessionBucket(ctx, store, bucket)
}

func (h objectHost) SecretRecipient() *age.X25519Recipient {
	if setSecretRecipient == nil {
		return nil
	}
	return hostSecretRecipient()
}

func (h objectHost) RuntimeSecretRecipient() *age.X25519Recipient {
	return runtimeSecretRecipient()
}

func (h objectHost) RuntimeSecretBinding(accountID string) secretbox.Binding {
	return runtimeSecretBinding(accountID)
}

func (h objectHost) HostHMACKey() []byte {
	if hostHMACKey == nil {
		return nil
	}
	return hostHMACKeyBytes()
}

// MFAIdentities prefers the rotation set and falls back to the single
// identity, matching how object storage secrets were sealed before the lift.
func (h objectHost) MFAIdentities() []*age.X25519Identity {
	var idents []*age.X25519Identity
	if mfaIdentities != nil {
		idents = mfaIdentities()
	}
	if len(idents) == 0 && mfaIdentity != nil {
		if id := mfaIdentity(); id != nil {
			idents = []*age.X25519Identity{id}
		}
	}
	return idents
}
