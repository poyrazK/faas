package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/apid/managedrealtime"
	"github.com/onebox-faas/faas/pkg/apid/realtimepreview"
	"github.com/onebox-faas/faas/pkg/platformflags"
)

// configureRealtimePreview reads FAAS_REALTIME_PREVIEW_ACCOUNTS, the accounts
// that see realtime preview features (ADR-20261019102657). Empty: nobody. The
// list also seeds the realtime_preview platform flag (ADR-1700), which
// decides instead once an operator writes it.
func (s *server) configureRealtimePreview(getenv func(string) string) error {
	rollout, err := managedrealtime.ParsePreviewRollout(getenv("FAAS_REALTIME_PREVIEW_ACCOUNTS"))
	if err != nil {
		return err
	}
	all, accounts := rollout.Accounts()
	s.seedPlatformFlag(platformflags.RealtimePreview, platformflags.FromAccountList(!rollout.Off(), all, accounts))
	s.WithRealtimePreview(rollout)
	return nil
}

// WithRealtimePreview sets who sees realtime preview features until an
// operator writes the realtime_preview platform flag.
func (s *server) WithRealtimePreview(fallback managedrealtime.PreviewGate) *server {
	s.realtimeRollout = fallback
	s.realtime.Preview = realtimePreviewGate{s: s}
	return s
}

// realtimePreviewGate decides per call, so an operator's change to the
// realtime_preview platform flag applies without a restart.
type realtimePreviewGate struct{ s *server }

func (g realtimePreviewGate) current() managedrealtime.PreviewGate {
	snapshot := g.s.platformFlags().Snapshot()
	if def, version, ok := snapshot.Definition(platformflags.RealtimePreview); ok && version > 0 {
		return realtimeFlagGate{snapshot: snapshot, everyone: def.Enabled && def.Default && len(def.Rules) == 0}
	}
	return g.s.realtimeRollout
}

func (g realtimePreviewGate) Allows(accountID string) bool {
	gate := g.current()
	return gate != nil && gate.Allows(accountID)
}

// Off reports that no account sees the preview.
func (g realtimePreviewGate) Off() bool {
	gate := g.current()
	off, ok := gate.(interface{ Off() bool })
	return gate == nil || ok && off.Off()
}

// All reports that every account sees the preview.
func (g realtimePreviewGate) All() bool {
	all, ok := g.current().(interface{ All() bool })
	return ok && all.All()
}

// realtimeFlagGate adapts a platform-flag snapshot to the preview gate. Only
// the account is known on the realtime path, so plan rules never match.
type realtimeFlagGate struct {
	snapshot *platformflags.Snapshot
	everyone bool
}

func (g realtimeFlagGate) Allows(accountID string) bool {
	return g.snapshot.Enabled(platformflags.RealtimePreview, platformflags.Subject{AccountID: accountID})
}

func (g realtimeFlagGate) Off() bool {
	return !g.snapshot.GloballyEnabled(platformflags.RealtimePreview)
}

func (g realtimeFlagGate) All() bool { return g.everyone }

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
