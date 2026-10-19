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
	store, _ := s.store.(imagerun.Store)
	h := imagerun.Handler{
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
	h.Create(w, r, acct)
}
