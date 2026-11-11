package apidserver

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/imagerun"
	"github.com/onebox-faas/faas/pkg/state"
)

// createImageRun mounts ADR-1161's POST /v1/image-runs. Dependencies are read
// per request so tests that swap the store or notifier see the change.
func (s *server) createImageRun(w http.ResponseWriter, r *http.Request, acct state.Account) {
	h := s.imageRunHandler()
	h.Create(w, r, acct)
}

// ensureSessionImage caches a seller image for a sandbox session in the
// image-run cache (ADR-20261027090013).
func (s *server) ensureSessionImage(ctx context.Context, acct state.Account, image string) (string, string, *api.Problem) {
	if !s.imageRunsEnabled {
		return "", "", api.ErrSandboxUnsupported("image", "seller images need image runs, which are off on this control-plane host; name a preset")
	}
	h := s.imageRunHandler()
	if h.Store == nil {
		return "", "", api.ErrCapacity("image runs are unavailable on this control-plane host")
	}
	job, ref, prob := h.EnsureSessionImage(ctx, acct, image)
	return job.ID, ref, prob
}

func (s *server) imageRunHandler() imagerun.Handler {
	store, _ := s.store.(imagerun.Store)
	return imagerun.Handler{
		Enabled:            s.imageRunsEnabled,
		InteractiveEnabled: s.interactiveImageRunsEnabled,
		Store:              store,
		Audit:              s.audit.Emit,
		Notify: func(ctx context.Context, channel, payload string) error {
			if s.notif == nil {
				return nil
			}
			return s.notif.Notify(ctx, channel, payload)
		},
		Log: s.log,
		SetRegistryCredential: func(ctx context.Context, acct state.Account, job state.Job, cred api.PutJobRegistryCredentialRequest) *api.Problem {
			_, prob := s.storeJobRegistryCredential(ctx, acct, job, cred)
			return prob
		},
	}
}
