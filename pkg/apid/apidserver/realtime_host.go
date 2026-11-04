package apidserver

import (
	"context"
	"log/slog"
	"net/http"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/managedrealtime"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// realtimeHost adapts *server to managedrealtime.Host (ADR-977). It reads
// server fields and package test hooks at call time, so tests that swap
// s.store or hostIdentitiesForUnseal behave exactly as before the lift.
type realtimeHost struct{ s *server }

var _ managedrealtime.Host = realtimeHost{}

func (h realtimeHost) LoadApp(w http.ResponseWriter, r *http.Request, acct state.Account, slug string) (state.App, bool) {
	return h.s.loadApp(w, r, acct, slug)
}

func (h realtimeHost) Store() state.Store { return h.s.store }

func (h realtimeHost) Logger() *slog.Logger { return h.s.log }

func (h realtimeHost) EmitAudit(ctx context.Context, kind string, accountID *string, data map[string]any) {
	h.s.audit.Emit(ctx, kind, accountID, data)
}

func (h realtimeHost) Ops() *wire.OpsMetrics { return h.s.ops }

// SecretRecipients seal realtime credentials, account values (ADR
// 20261022111812), with the fleet recipient as their legacy recipient.
func (h realtimeHost) SecretRecipients() ([]*age.X25519Recipient, error) {
	return accountRecipients(hostSecretRecipient())
}

// HostIdentitiesForUnseal opens realtime credentials: the account domain
// first, then the host identities they were opened with before it.
func (h realtimeHost) HostIdentitiesForUnseal(ctx context.Context) []*age.X25519Identity {
	var legacy []*age.X25519Identity
	if hostIdentitiesForUnseal != nil {
		legacy = hostIdentitiesForUnseal(ctx)
	}
	return accountIdentitiesWith(legacy)
}

func (h realtimeHost) ResolveAndCheckEgress(ctx context.Context, rawURL string) *api.Problem {
	return resolveAndCheckEgress(ctx, rawURL)
}
