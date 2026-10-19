package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/apid/managedrealtime"
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
