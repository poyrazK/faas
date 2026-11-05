package managedrealtime

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// fakeHost is a Host for unit tests that never route requests or seal secrets.
type fakeHost struct{ store state.Store }

var _ Host = fakeHost{}

func (fakeHost) LoadApp(http.ResponseWriter, *http.Request, state.Account, string) (state.App, bool) {
	panic("fakeHost.LoadApp: unit tests do not route requests")
}

func (h fakeHost) Store() state.Store { return h.store }

func (fakeHost) Logger() *slog.Logger { return slog.New(slog.DiscardHandler) }

func (fakeHost) EmitAudit(context.Context, string, *string, map[string]any) {}

func (fakeHost) Ops() *wire.OpsMetrics { return nil }

func (fakeHost) SecretRecipients() ([]*age.X25519Recipient, error) {
	return nil, errors.New("no recipient")
}

func (fakeHost) HostIdentitiesForUnseal(context.Context) []*age.X25519Identity { return nil }

func (fakeHost) ResolveAndCheckEgress(context.Context, string) *api.Problem { return nil }
