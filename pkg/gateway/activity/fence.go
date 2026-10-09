package activity

import (
	"fmt"
	"slices"
	"sync"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// Fence is process-local forwarding admission, not scheduler or VM authority.
// ID changes whenever the routing revision or reviewed binding changes.
type Fence struct {
	ID, AppID, DeploymentID, RoutingRevision, Binding string
}

func NewWithFences(sessionID string) (*Tracker, error) {
	t, err := New(sessionID)
	if err != nil {
		return nil, err
	}
	t.fencing, t.fences = true, make(map[string]Fence)
	return t, nil
}

func validFenceRevision(revision string) bool {
	if len(revision) != 64 {
		return false
	}
	for _, c := range revision {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// TryBegin atomically checks the fence and starts counting at actual dispatch.
// Private fencing rejects unidentified/untrackable forwards rather than letting
// activity appear behind a previously published closed zero observation.
func (t *Tracker) TryBegin(appID, deploymentID string) (func(), bool) {
	if t == nil || !t.fencing {
		return t.Begin(appID, deploymentID), true
	}
	k := key{appID, deploymentID}
	t.mu.Lock()
	if reason := t.untrackable(k); reason != "" {
		t.markUnknown(reason)
		t.mu.Unlock()
		return func() {}, false
	}
	if f, exists := t.fences[appID]; exists && f.DeploymentID == deploymentID {
		t.mu.Unlock()
		return func() {}, false
	}
	t.advance()
	t.active[k]++
	t.total++
	t.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { t.finish(k, true) }) }, true
}

// InstallFence closes admission without interrupting existing forwards. Exact
// retries preserve ID. Replacing a binding is atomic and never briefly reopens.
func (t *Tracker) InstallFence(appID, deploymentID, revision, binding string) (Fence, error) {
	if t == nil || !t.fencing || !canonicalIdentity(appID) || !canonicalIdentity(deploymentID) ||
		!validFenceRevision(revision) || len(binding) == 0 || len(binding) > api.RuntimeUpgradeActivityFenceBindingMaxBytes {
		return Fence{}, fmt.Errorf("activity: invalid forwarding fence")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if old, ok := t.fences[appID]; ok && old.DeploymentID == deploymentID && old.RoutingRevision == revision && old.Binding == binding {
		return old, nil
	}
	if _, exists := t.fences[appID]; !exists && len(t.fences) >= api.RuntimeUpgradeActivityFenceLimit {
		return Fence{}, fmt.Errorf("activity: forwarding fence capacity exhausted")
	}
	f := Fence{ID: uuid.NewString(), AppID: appID, DeploymentID: deploymentID, RoutingRevision: revision, Binding: binding}
	t.fences[appID] = f
	t.advance()
	return f, nil
}

// ReconcileRouting releases only after an authoritative DIFFERENT routing
// revision with no applicable upgrade plan. Read failure, missing membership,
// timeout or expired receipt must never silently reopen the same revision.
func (t *Tracker) ReconcileRouting(appID, revision string) {
	if t == nil || !t.fencing || !validFenceRevision(revision) {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if f, ok := t.fences[appID]; ok && f.RoutingRevision != revision {
		delete(t.fences, appID)
		t.advance()
	}
}

func (t *Tracker) ObserveFence(appID string) (Fence, Observation, bool) {
	if t == nil {
		return Fence{}, Observation{}, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	f, ok := t.fences[appID]
	if !ok {
		return Fence{}, Observation{}, false
	}
	return f, Observation{SessionID: t.session, ActivityVersion: t.version, CoverageKnown: t.known, CoverageUnknownReason: t.unknownReason,
		ActiveForwards: t.active[key{appID, f.DeploymentID}], TotalActiveForwards: t.total}, true
}

// FenceApps retains repair coverage after the cutover leaves the recent scan.
func (t *Tracker) FenceApps() []string {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, 0, len(t.fences))
	for id := range t.fences {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}
