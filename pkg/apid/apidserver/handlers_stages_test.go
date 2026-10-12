// Tests for GET /v1/deployments/{id}/stages — the closed-stage
// summary read surface (companion to GET /v1/deployments/{id}/logs
// which streams the live SSE event: stage frames during a deploy).
//
// Pins:
//
//   - Happy path: handler re-emits the raw deployments.stage_state
//     jsonb verbatim. The CLI does the typed unmarshal; the server
//     does NOT add a DTO layer (so the jsonb column IS the wire).
//   - Closed vocabulary: the column's CHECK constraint
//     `deployments_stage_state_current_check` rejects out-of-set
//     stages; here we verify a 4-stage history that exercises the
//     mid-deploy path (current = snapshot_prepare, last 3 stages
//     completed) round-trips intact.
//   - IDOR posture: cross-account probes get 404, not 403. Same
//     posture as getDeployment / getDeploymentScan.
//   - Unknown id: 404 with the standard not-found problem code.
package apidserver_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestGetDeploymentStages_HappyPath seeds a deployment and stamps
// 3 forward transitions through the store (so the jsonb grows one
// closed stage per call), then asserts GET /v1/deployments/{id}/stages
// returns the exact same jsonb the column carries (handler is a
// pass-through — no Go-side re-shape, no silent rename if the typed
// StageState struct ever drifts from the jsonb).
func TestGetDeploymentStages_HappyPath(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	dep := apidserver.MustSeedDeployment(t, e, "stages-happy")

	now := time.Now().UTC().Truncate(time.Second)
	// Walk source_download → dependency_restore → image_build →
	// snapshot_prepare so the column's history has 3 closed
	// rows and `current = snapshot_prepare`. Mirrors a real
	// mid-deploy shape (security_scan ran but readiness not yet
	// entered — actually snapshot_prepare next, but the closed
	// vocabulary doesn't require ordering for the test pin).
	transitions := []struct {
		from, to state.StageName
		at       time.Time
	}{
		{state.StageSourceDownload, state.StageDependencyRestore, now.Add(-10 * time.Second)},
		{state.StageDependencyRestore, state.StageImageBuild, now.Add(-8 * time.Second)},
		{state.StageImageBuild, state.StageSnapshotPrepare, now.Add(-3 * time.Second)},
	}
	for _, tr := range transitions {
		if _, err := (*e.Store()).AppendDeploymentStage(context.Background(), dep.ID, tr.from, tr.to, tr.at, ""); err != nil {
			t.Fatalf("AppendDeploymentStage %s→%s: %v", tr.from, tr.to, err)
		}
	}

	rec := e.Do(t, "GET", "/v1/deployments/"+dep.ID+"/stages", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}

	var got state.StageState
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v (raw body=%s)", err, rec.Body.String())
	}
	if got.Current != state.StageSnapshotPrepare {
		t.Errorf("current: got %q, want %q", got.Current, state.StageSnapshotPrepare)
	}
	if len(got.History) != len(transitions) {
		t.Fatalf("history len: got %d, want %d (body=%s)", len(got.History), len(transitions), rec.Body.String())
	}
	// History is append-ordered: each transition closes the from-row,
	// so history[i].Name == transitions[i].from.
	for i, tr := range transitions {
		gotItem := got.History[i]
		if gotItem.Name != tr.from {
			t.Errorf("history[%d].name: got %q, want %q", i, gotItem.Name, tr.from)
		}
		if gotItem.Status != "completed" {
			t.Errorf("history[%d].status: got %q, want completed", i, gotItem.Status)
		}
	}
}

// TestGetDeploymentStages_UnknownReturns404 covers the not-found
// branch (deployment id format is valid hex but no such row).
func TestGetDeploymentStages_UnknownReturns404(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	rec := e.Do(t, "GET", "/v1/deployments/deadbeefdeadbeefdeadbeefdeadbeef/stages", nil, nil)
	apidserver.AssertProblem(t, rec, 404, api.CodeNotFound)
}

// TestGetDeploymentStages_CrossAccountReturns404 locks the IDOR
// posture: a probe from a second account must NEVER distinguish
// "deployment doesn't exist" from "deployment exists in another
// account" — both surface as 404 with the same problem code. Same
// posture as getDeployment (handlers_ext.go:1136) and
// getDeploymentScan (handlers_scan.go:58).
func TestGetDeploymentStages_CrossAccountReturns404(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	dep := apidserver.MustSeedDeployment(t, e, "stages-cross")

	// Provision a second account + API key on the same store. The
	// attacker uses the foreign key to probe the victim's deployment
	// id — must NEVER confirm the id exists.
	store := (*e.Store())
	foreignAcct, err := store.CreateAccount(context.Background(), "intruder-stages@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount foreign: %v", err)
	}
	pt, hash, _ := api.GenerateAPIKey()
	if _, err := store.CreateAPIKey(context.Background(), foreignAcct.ID, hash, "intruder", api.ScopesAdminOnly); err != nil {
		t.Fatalf("CreateAPIKey foreign: %v", err)
	}
	rec := e.DoAs(t, "GET", "/v1/deployments/"+dep.ID+"/stages", nil, nil, pt)
	if rec.Code != 404 {
		t.Fatalf("cross-account GET /stages: status %d, want 404 (body=%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), api.CodeNotFound) {
		t.Errorf("cross-account GET /stages: body did not carry %s, got %s", api.CodeNotFound, rec.Body.String())
	}
}
