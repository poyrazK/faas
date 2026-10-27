package apidserver

import (
	"log/slog"
	"net/http"

	"github.com/onebox-faas/faas/pkg/apid/profilecapture"
	"github.com/onebox-faas/faas/pkg/profiling"
	"github.com/onebox-faas/faas/pkg/state"
)

// profileCaptureHost adapts *server to profilecapture.Host (ADR-1155). It
// reads server fields at call time, so tests that swap s.store or s.notif
// after construction see the new values.
type profileCaptureHost struct{ s *server }

var _ profilecapture.Host = profileCaptureHost{}

func (h profileCaptureHost) LoadApp(w http.ResponseWriter, r *http.Request, acct state.Account, slug string) (state.App, bool) {
	return h.s.loadApp(w, r, acct, slug)
}

func (h profileCaptureHost) Store() state.Store { return h.s.store }

func (h profileCaptureHost) Logger() *slog.Logger { return h.s.log }

func (h profileCaptureHost) Notifier() profilecapture.Notifier { return h.s.notif }

func (h profileCaptureHost) SourceProvenance(app state.App, dep state.Deployment) profiling.SourceProvenance {
	return profileSourceProvenance(app, dep)
}
