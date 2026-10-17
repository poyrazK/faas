package apidserver

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/durable"
	"github.com/onebox-faas/faas/pkg/state"
)

// durableHost adapts *server to durable.Host (ADR-977). It reads server fields
// at call time, so tests that swap s.store or toggle executionAPIEnabled after
// construction behave exactly as they did before the lift.
type durableHost struct{ s *server }

var _ durable.Host = durableHost{}

func (h durableHost) LoadApp(w http.ResponseWriter, r *http.Request, acct state.Account, slug string) (state.App, bool) {
	return h.s.loadApp(w, r, acct, slug)
}

func (h durableHost) Store() state.Store { return h.s.store }

func (h durableHost) Logger() *slog.Logger { return h.s.log }

func (h durableHost) EmitAudit(ctx context.Context, kind string, accountID *string, data map[string]any) {
	h.s.audit.Emit(ctx, kind, accountID, data)
}

func (h durableHost) ResolveQueueEnvironment(ctx context.Context, acct state.Account, app state.App, requested string) (string, *state.ProjectEnvironment, *api.Problem) {
	return h.s.resolveQueueEnvironment(ctx, acct, app, requested)
}

func (h durableHost) EnqueuePreparedInvocation(ctx context.Context, inv state.Invocation, capacityDetail string) (state.Invocation, *api.Problem) {
	return h.s.enqueuePreparedInvocation(ctx, inv, capacityDetail)
}

func (h durableHost) ResultRetention(acct state.Account) *time.Time {
	return retentionForRequest(nil, acct)
}

func (h durableHost) SecretRecipient() *age.X25519Recipient {
	if setSecretRecipient == nil {
		return nil
	}
	return hostSecretRecipient()
}

func (h durableHost) ExecutionAPIEnabled() bool { return h.s.executionAPIEnabled }

// configureDurableEntities reads the durable entity settings against the
// object storage registry installed by WithObjectStorage.
func (s *server) configureDurableEntities(ctx context.Context, getenv func(string) string) error {
	return s.durable.Configure(ctx, getenv, s.objectStorage)
}
