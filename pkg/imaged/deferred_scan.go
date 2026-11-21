package imaged

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// deferredScans runs the per-deploy scan after the snapshot handoff for a
// deployment whose app does not gate on it (ADR-075 amendment 2026-11-17).
// Under security_policy off or warn the result is only shown to the
// customer, yet the deploy used to wait for it before snapshot preparation
// began: about 18 s at p50 and 42 s at p90 in production. A scan queued
// here that never runs, because the queue was full or imaged stopped, is
// picked up by the live-deployment rescan sweep, which treats a deployment
// with no scan as due.
type deferredScans struct {
	slots    chan struct{}
	mu       sync.Mutex
	inFlight map[string]struct{}
	wg       sync.WaitGroup
}

func newDeferredScans() *deferredScans {
	return &deferredScans{slots: make(chan struct{}, api.ImagedDeferredScanWorkers), inFlight: make(map[string]struct{})}
}

// claim reserves the deployment's scan. It refuses one already queued or
// running, and any once the queue is full.
func (d *deferredScans) claim(id string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.inFlight[id]; ok || len(d.inFlight) >= api.ImagedDeferredScanQueue {
		return false
	}
	d.inFlight[id] = struct{}{}
	return true
}

func (d *deferredScans) release(id string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.inFlight, id)
}

// running reports whether the deployment's scan is queued or running here,
// so the rescan sweep does not scan it a second time.
func (d *deferredScans) running(id string) bool {
	if d == nil {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.inFlight[id]
	return ok
}

// wait blocks until every queued scan has finished.
func (d *deferredScans) wait() {
	if d != nil {
		d.wg.Wait()
	}
}

type deferredScanKey struct{}

func deferredScan(ctx context.Context) bool {
	on, _ := ctx.Value(deferredScanKey{}).(bool)
	return on
}

// WithDeferredDeployScans runs the deploy scan of an app that does not gate
// on it after the snapshot handoff (ADR-075 amendment 2026-11-17). Without
// it every deploy waits for its scan, as unit tests built on New expect.
func (h *Handler) WithDeferredDeployScans() *Handler {
	h.deferredScans = newDeferredScans()
	return h
}

// deployScanGatesHandoff reports whether the deploy must wait for its scan
// before the snapshot handoff. Enforce mode promotes only on the scan's
// evidence, and the producer-evidence route (ADR-435) requires complete
// evidence in every mode, as does a signed image deployed without a
// producer; any doubt keeps the scan in front.
//
// An organization's app captures the deployment's scan mirrors at
// admission (ADR-595). The admission checks ignore those mirrors (ADR
// 20261031190030), so a scan landing during the snapshot prime leaves the
// prime alone, except under a standard that requires native runtime
// admission: native captures compare the mirrors, and such an app keeps its
// scan in front (ADR-075 amendment 2026-11-19).
func (h *Handler) deployScanGatesHandoff(ctx context.Context, app state.App, dep state.Deployment) bool {
	if h.deferredScans == nil || h.store == nil || h.log == nil ||
		app.SecurityPolicy == api.AppSecurityPolicyEnforce || app.RequireSigned {
		return true
	}
	if app.OrgID != "" {
		if native, err := standardRequiresNativeRuntime(ctx, h.store, app); native || err != nil {
			return true
		}
	}
	present, err := producedRuntimePresent(ctx, h.store, app, dep)
	return present || err != nil
}

// standardRequiresNativeRuntime reports whether an organization's app runs
// under a standard that admits its instances natively. An app with no
// enrollment row has none.
func standardRequiresNativeRuntime(ctx context.Context, store state.Store, app state.App) (bool, error) {
	reader, ok := store.(interface {
		GetApplicationStandardEnrollment(context.Context, string, string) (state.ApplicationStandardEnrollment, error)
	})
	if !ok {
		return false, errors.New("imaged: application standard enrollment reader unavailable")
	}
	enrollment, err := reader.GetApplicationStandardEnrollment(ctx, app.OrgID, app.ID)
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("imaged: read application standard enrollment: %w", err)
	}
	return state.ApplicationStandardEnrollmentRequiresNative(enrollment), nil
}

// startDeferredDeployScan queues the deployment's scan. It outlives the
// snapshot_boot delivery that handed the deployment off, so it does not
// hold the deployment's activation lock while it runs.
func (h *Handler) startDeferredDeployScan(ctx context.Context, app state.App, dep state.Deployment) {
	d := h.deferredScans
	if !d.claim(dep.ID) {
		h.log.Info("imaged: deferred deploy scan not queued; the rescan sweep covers it once live", "deployment", dep.ID, "app", app.Slug)
		return
	}
	scanCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ImagedDeferredScanTimeout)
	scanCtx = context.WithValue(scanCtx, deferredScanKey{}, true)
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		defer d.release(dep.ID)
		defer cancel()
		select {
		case d.slots <- struct{}{}:
		case <-scanCtx.Done():
			return
		}
		defer func() { <-d.slots }()
		if err := h.runDeployScan(scanCtx, app, dep); err != nil {
			h.log.Warn("imaged: deferred deploy scan", "deployment", dep.ID, "app", app.Slug, "err", err)
		}
	}()
}
