package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCheckedRollbackRetainedRevisionStaysAvailable(t *testing.T) {
	t.Setenv("FAAS_API_CONTRACT_DIFF_ENABLED", "0")
	e, app, _, _ := apidserver.CheckedRollbackAPIFixture(t)
	store := *e.Store()
	account := *e.Acct()
	manifest := app.Manifest
	manifest.RevisionPinTTLSeconds = 3600
	var err error
	app, err = store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &manifest})
	if err != nil {
		t.Fatal(err)
	}
	target := createRollbackContractDeployment(t, store, app, "default", false)
	current := createRollbackContractDeployment(t, store, app, "default", false)
	zero := int64(0)
	policy, err := store.SetBindingReleasePolicy(t.Context(), account.ID, app.ID, "default", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero})
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/apps/" + app.Slug + "/rollback"
	response := e.Do(t, http.MethodPost, path, api.RollbackRequest{TargetDeploymentID: &target.ID, ExpectedCurrentDeploymentID: &current.ID}, nil)
	var receipt api.DeploymentResponse
	if response.Code != http.StatusAccepted || json.Unmarshal(response.Body.Bytes(), &receipt) != nil || receipt.RollbackOperation == nil || receipt.RollbackOperation.Status != "ready" {
		t.Fatalf("retained readiness: %d %s", response.Code, response.Body)
	}
	if _, err = store.ResolveRevisionPin(t.Context(), app.ID, "default", target.ID); err != nil {
		t.Fatalf("accepted rollback revoked existing pin: %v", err)
	}
	if err = (*e.S()).CheckedRollbackSweep(t.Context()); err != nil {
		t.Fatal(err)
	}
	blocked, err := store.GetCheckedRollback(t.Context(), account.ID, app.ID, receipt.RollbackOperation.ID)
	if err != nil || blocked.Status != "blocked" || len(blocked.Blockers) == 0 {
		t.Fatalf("retained target bypassed current bindings: %+v %v", blocked, err)
	}
	if _, err = store.SetBindingReleasePolicy(t.Context(), account.ID, app.ID, "default", api.SetBindingReleasePolicyRequest{Mode: "off", ExpectedRevision: &policy.Revision, Reason: "allow replacement to invalidate the pending rollback"}); err != nil {
		t.Fatal(err)
	}
	_ = createRollbackContractDeployment(t, store, app, "default", false)
	if err = (*e.S()).CheckedRollbackSweep(t.Context()); err != nil {
		t.Fatal(err)
	}
	status := e.Do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/rollbacks/"+blocked.ID, nil, nil)
	var failed api.RollbackOperation
	if status.Code != http.StatusOK || json.Unmarshal(status.Body.Bytes(), &failed) != nil || failed.Status != "failed" {
		t.Fatalf("stale rollback not retired: %d %s", status.Code, status.Body)
	}
	if _, err = store.ResolveRevisionPin(t.Context(), app.ID, "default", target.ID); err != nil {
		t.Fatalf("failed receipt revoked retained revision: %v", err)
	}
}
