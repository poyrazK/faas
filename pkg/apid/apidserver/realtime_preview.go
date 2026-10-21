package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/apid/managedrealtime"
	"github.com/onebox-faas/faas/pkg/apid/realtimepreview"
)

// configureRealtimePreview reads FAAS_REALTIME_PREVIEW_ACCOUNTS, the accounts
// that see realtime preview features (ADR-20261019102657). Empty: nobody.
func (s *server) configureRealtimePreview(getenv func(string) string) error {
	rollout, err := managedrealtime.ParsePreviewRollout(getenv("FAAS_REALTIME_PREVIEW_ACCOUNTS"))
	if err != nil {
		return err
	}
	s.realtime.Preview = rollout
	return nil
}

// WithRealtimePreview sets who sees realtime preview features.
func (s *server) WithRealtimePreview(gate managedrealtime.PreviewGate) *server {
	s.realtime.Preview = gate
	return s
}

// realtimePreviewPublished reports whether every account sees the realtime
// preview, so the served OpenAPI document may list it.
func (s *server) realtimePreviewPublished() bool {
	all, ok := s.realtime.Preview.(interface{ All() bool })
	return ok && all.All()
}

// realtimePreviewProbe reports whether a request that matched no route
// probes a realtime preview path with another method. Until every account
// sees the preview it answers like an unregistered route, not 405, so the
// Allow header cannot reveal the preview.
func (s *server) realtimePreviewProbe(r *http.Request) bool {
	return !s.realtimePreviewPublished() && realtimepreview.Path(r.URL.Path)
}

// realtimePreviewRoute answers as an unregistered route, before
// authentication, while no account sees realtime previews; per-account checks
// run in the handlers after authentication.
func (s *server) realtimePreviewRoute(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		gate := s.realtime.Preview
		if off, ok := gate.(interface{ Off() bool }); gate == nil || ok && off.Off() {
			httpkit.NoSuchRoute(w)
			return
		}
		next(w, r)
	}
}
