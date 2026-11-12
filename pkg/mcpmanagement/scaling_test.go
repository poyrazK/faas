// adr: 20261104104521
package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (f *operationFixture) serveScaling(w http.ResponseWriter, r *http.Request) bool {
	const path = "/v1/apps/alpha/scaling"
	if r.URL.Path != path && !strings.HasPrefix(r.URL.Path, path+"/requests/") {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.base.mu.Lock()
	defer f.base.mu.Unlock()
	if f.scalingRevision == 0 {
		f.scalingRevision = 1
	}
	if f.denyScalingReads && r.Method == http.MethodGet {
		api.WriteProblem(w, api.NewProblem(403, "forbidden", "Access denied", "scaling access revoked"))
		return true
	}
	snapshot := func() api.AppScalingSnapshot {
		app := f.base.app
		return api.AppScalingSnapshot{AppID: app.ID, Scope: "default", Revision: f.scalingRevision, Settings: api.AppScalingSettings{RAMMB: app.RAMMB, CPUMillicores: app.CPUMillicores, MaxConcurrency: app.MaxConcurrency, MinInstances: app.MinInstances}}
	}
	if r.Method == http.MethodGet {
		if r.URL.Path == path {
			_ = json.NewEncoder(w).Encode(snapshot())
			return true
		}
		receipt, ok := f.scalingReceipts[strings.TrimPrefix(r.URL.Path, path+"/requests/")]
		if !ok {
			api.WriteProblem(w, api.NewProblem(404, "not_found", "Not found", "no scaling receipt"))
			return true
		}
		_ = json.NewEncoder(w).Encode(receipt)
		return true
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return true
	}
	var req api.ApplyAppScalingRequest
	if json.NewDecoder(r.Body).Decode(&req) != nil || req.RequestID == "" || req.ExpectedRevision == nil || req.ExpectedAppID != f.base.app.ID {
		w.WriteHeader(400)
		return true
	}
	if receipt, ok := f.scalingReceipts[req.RequestID]; ok {
		_ = json.NewEncoder(w).Encode(receipt)
		return true
	}
	if f.failScalingBeforeCommit {
		f.failScalingBeforeCommit = false
		api.WriteProblem(w, api.ErrCapacity("request interrupted before commit"))
		return true
	}
	if f.concurrentScalingRAM != nil {
		f.base.app.RAMMB = *f.concurrentScalingRAM
		f.scalingRevision++
		f.concurrentScalingRAM = nil
	}
	if *req.ExpectedRevision != f.scalingRevision {
		api.WriteProblem(w, api.NewProblem(409, "scaling_revision_conflict", "Scaling changed", "review current settings"))
		return true
	}
	f.patches++
	if req.RAMMB != nil {
		f.base.app.RAMMB = *req.RAMMB
	}
	if req.CPUMillicores != nil {
		f.base.app.CPUMillicores = *req.CPUMillicores
	}
	if req.MaxConcurrency != nil {
		f.base.app.MaxConcurrency = *req.MaxConcurrency
	}
	if req.MinInstances != nil {
		f.base.app.MinInstances = *req.MinInstances
	}
	f.scalingRevision++
	receipt := api.AppScalingReceipt{AppScalingSnapshot: snapshot(), RequestID: req.RequestID, PreviousRevision: *req.ExpectedRevision, CommittedAt: time.Now().UTC()}
	if f.scalingReceipts == nil {
		f.scalingReceipts = map[string]api.AppScalingReceipt{}
	}
	f.scalingReceipts[req.RequestID] = receipt
	if f.failScalingAfterCommit {
		f.failScalingAfterCommit = false
		api.WriteProblem(w, api.ErrCapacity("response interrupted after commit"))
		return true
	}
	_ = json.NewEncoder(w).Encode(receipt)
	return true
}

func TestScalingRecoversJournaledRequestAfterLostResponseAndLaterWrite(t *testing.T) {
	m, f := newOperationFixture(t)
	store, path, key := testStateStore(t)
	m.opts.planStore, m.opts.planNamespace = store, "scaling-recovery"
	ram := 512
	plan, err := m.planScale(t.Context(), scalePlanArgs{App: "alpha", Reason: "increase memory", RAMMB: &ram})
	if err != nil {
		t.Fatal(err)
	}
	id := plan.(releasePlan).ID
	f.mu.Lock()
	f.failScalingAfterCommit = true
	f.mu.Unlock()
	if _, err := m.applyOperation(t.Context(), applyArgs{PlanID: id}); err == nil {
		t.Fatal("lost response reported as success")
	}
	f.mu.Lock()
	f.base.mu.Lock()
	f.base.app.RAMMB = 768
	f.scalingRevision++
	f.base.mu.Unlock()
	f.mu.Unlock()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := OpenStateStore(path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	opts := m.opts
	opts.planStore = next
	_, restored, err := newManagementServer(api.NewClient(opts.APIBase, testToken), opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restored.applyOperation(t.Context(), applyArgs{PlanID: id}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	writes := f.patches
	f.mu.Unlock()
	f.base.mu.Lock()
	current := f.base.app.RAMMB
	f.base.mu.Unlock()
	if writes != 1 || current != 768 {
		t.Fatalf("recovery overwrote later settings: %d writes, RAM %d", writes, current)
	}
	verified, err := restored.verifyPlan(t.Context(), planLookupArgs{PlanID: id})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(verified)
	if !strings.Contains(string(raw), `"status":"blocked"`) || !strings.Contains(string(raw), "committed_settings_superseded") {
		t.Fatalf("superseded commit verified: %s", raw)
	}
}

func TestScalingResubmitsUncommittedRequestAndVerifiesConfiguration(t *testing.T) {
	m, f := newOperationFixture(t)
	ram := 512
	plan, err := m.planScale(t.Context(), scalePlanArgs{App: "alpha", Reason: "increase memory", RAMMB: &ram})
	if err != nil {
		t.Fatal(err)
	}
	id := plan.(releasePlan).ID
	f.mu.Lock()
	f.failScalingBeforeCommit = true
	f.mu.Unlock()
	if _, err := m.applyOperation(t.Context(), applyArgs{PlanID: id}); err == nil {
		t.Fatal("interruption reported as success")
	}
	if _, err := m.applyOperation(t.Context(), applyArgs{PlanID: id}); err != nil {
		t.Fatal(err)
	}
	verified, err := m.verifyPlan(t.Context(), planLookupArgs{PlanID: id})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(verified)
	if !strings.Contains(string(raw), `"goal":"scaling_configuration"`) || !strings.Contains(string(raw), `"status":"verified"`) || !strings.Contains(string(raw), "runtime_convergence_not_assessed") {
		t.Fatalf("incorrect verification: %s", raw)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.patches != 1 {
		t.Fatalf("wrote %d times", f.patches)
	}
}

func TestScalingNativeGuardRefusesWriteAfterReviewRead(t *testing.T) {
	m, f := newOperationFixture(t)
	ram, concurrent := 512, 768
	plan, err := m.planScale(t.Context(), scalePlanArgs{App: "alpha", Reason: "increase memory", RAMMB: &ram})
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.concurrentScalingRAM = &concurrent
	f.mu.Unlock()
	if _, err := m.applyOperation(t.Context(), applyArgs{PlanID: plan.(releasePlan).ID}); err == nil {
		t.Fatal("concurrent native update overwritten")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.patches != 0 {
		t.Fatal("stale plan wrote")
	}
}

func TestScalingRetryRequiresNewReviewAfterNativeRevisionConflict(t *testing.T) {
	m, f := newOperationFixture(t)
	ram := 512
	plan, err := m.planScale(t.Context(), scalePlanArgs{App: "alpha", Reason: "increase memory", RAMMB: &ram})
	if err != nil {
		t.Fatal(err)
	}
	id := plan.(releasePlan).ID
	f.mu.Lock()
	f.failScalingBeforeCommit = true
	f.mu.Unlock()
	if _, err := m.applyOperation(t.Context(), applyArgs{PlanID: id}); err == nil {
		t.Fatal("interruption reported as success")
	}
	f.mu.Lock()
	f.scalingRevision++
	f.mu.Unlock()
	if _, err := m.applyOperation(t.Context(), applyArgs{PlanID: id}); err == nil {
		t.Fatal("stale retry applied")
	}
	if failure := m.plans[id].failure; failure == nil || failure.Status != "refused" {
		t.Fatalf("stale retry must require a new review: %+v", failure)
	}
}
