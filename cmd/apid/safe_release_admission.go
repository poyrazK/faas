package main

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// admitCanaryDeployment checks the durable meterd lease before any deployment
// row is created. An absent lease also covers disabled workers and startup.
func (s *server) admitCanaryDeployment(w http.ResponseWriter, r *http.Request, dep state.Deployment) bool {
	if dep.CanaryTotalSteps == 0 {
		return true
	}
	leaseStore, ok := s.store.(state.SafeReleaseWorkerLeaseStore)
	if !ok {
		api.WriteProblem(w, api.ErrSafeReleaseUnavailable())
		return false
	}
	ready, err := leaseStore.SafeReleaseWorkerLeaseReady(r.Context())
	if err != nil {
		s.log.Warn("safe release lease read failed", "error", err)
	}
	if err != nil || !ready {
		api.WriteProblem(w, api.ErrSafeReleaseUnavailable())
		return false
	}
	return true
}
