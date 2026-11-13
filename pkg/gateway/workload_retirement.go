package gateway

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"github.com/onebox-faas/faas/pkg/state"
)

type workloadAdmissionScope struct{ appID, scope string }
type workloadAdmissionEpoch struct{ app, global uint64 }
type workloadAdmissionCache struct {
	mu     sync.Mutex
	epochs map[string]uint64
	global uint64
	values map[workloadAdmissionScope]bool
}

func (c *workloadAdmissionCache) read(key workloadAdmissionScope) (bool, bool, workloadAdmissionEpoch) {
	c.mu.Lock()
	defer c.mu.Unlock()
	value, ok := c.values[key]
	return value, ok, workloadAdmissionEpoch{app: c.epochs[key.appID], global: c.global}
}
func (c *workloadAdmissionCache) publish(key workloadAdmissionScope, value bool, epoch workloadAdmissionEpoch) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.global != epoch.global || c.epochs[key.appID] != epoch.app {
		return false
	}
	if c.values == nil {
		c.values = map[workloadAdmissionScope]bool{}
	}
	c.values[key] = value
	return true
}
func (c *workloadAdmissionCache) invalidate(appID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.epochs == nil {
		c.epochs = map[string]uint64{}
	}
	c.epochs[appID]++
	for key := range c.values {
		if key.appID == appID {
			delete(c.values, key)
		}
	}
}
func (c *workloadAdmissionCache) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.global++
	c.values = nil
	c.epochs = nil
}

// InvalidateWorkloadAdmission runs before a withdrawal generation is
// acknowledged. Epochs prevent an older in-flight storage read repopulating
// the cache after invalidation. Drain starts only after the whole roster acks.
func (b *PGBackend) InvalidateWorkloadAdmission(appID string) { b.workloadAdmissions.invalidate(appID) }

// Known admission decisions keep the existing cache-only serving behavior.
// Cold and invalidated decisions fail closed if storage is unavailable.
func (b *PGBackend) CheckWorkloadAdmission(ctx context.Context, appID, scope string) error {
	store, ok := b.store.(state.EnvironmentWorkloadRetirementAdmissionStore)
	if !ok {
		return nil
	}
	if scope == "" {
		scope = state.DefaultEnvScope
	}
	key := workloadAdmissionScope{appID, scope}
	for range 3 {
		blocked, cached, epoch := b.workloadAdmissions.read(key)
		if !cached {
			var err error
			blocked, err = store.EnvironmentWorkloadAdmissionRetired(ctx, appID, scope)
			if err != nil {
				return err
			}
			if !b.workloadAdmissions.publish(key, blocked, epoch) {
				continue
			}
		}
		if blocked {
			return state.ErrEnvironmentWorkloadRetired
		}
		return nil
	}
	return state.ErrConflict
}
func (h *Handler) workloadAdmissionOpen(w http.ResponseWriter, r *http.Request, appID, scope string) bool {
	admission, ok := h.backend.(interface {
		CheckWorkloadAdmission(context.Context, string, string) error
	})
	if !ok {
		return true
	}
	// The check can read storage after a route change invalidated its cache.
	// Measure it as policy time and name the refusal: logged as an unmeasured
	// "body_response", the 5-14 s refusals during a database stall pointed at
	// the request body (prod 2026-11-09).
	stop := measureTrafficPhase(r.Context(), trafficPolicy)
	err := admission.CheckWorkloadAdmission(r.Context(), appID, scope)
	stop()
	if err != nil {
		recordTrafficRefusal(r.Context(), "policy_unavailable")
		if errors.Is(err, state.ErrEnvironmentWorkloadRetired) {
			recordTrafficPolicyStage(r.Context(), "workload_retired")
		} else {
			recordTrafficPolicyStage(r.Context(), "workload_admission_unavailable")
			// Storage, not the workload, is unavailable: invite a retry.
			w.Header().Set("Retry-After", "2")
		}
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "Workload unavailable in this environment", http.StatusServiceUnavailable)
		return false
	}
	return true
}
