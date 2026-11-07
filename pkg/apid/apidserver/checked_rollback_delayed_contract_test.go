package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCheckedRollbackRechecksContractAfterReadiness(t *testing.T) {
	t.Setenv("FAAS_API_CONTRACT_DIFF_ENABLED", "0")
	e := apidserver.Setup(t, api.PlanPro)
	s := *e.Store()
	app, err := s.CreateApp(t.Context(), state.App{AccountID: (*e.Acct()).ID, Slug: "delayed-contract", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	target := createRollbackContractDeployment(t, s, app, "default", false)
	current := createRollbackContractDeployment(t, s, app, "default", false)
	writeRollbackContractSnapshot(t, s, target, rollbackContractOld)
	writeRollbackContractSnapshot(t, s, current, rollbackContractNew)
	response := e.Do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/rollback", api.RollbackRequest{TargetDeploymentID: &target.ID, ExpectedCurrentDeploymentID: &current.ID}, nil)
	var receipt api.DeploymentResponse
	if response.Code != 202 || json.Unmarshal(response.Body.Bytes(), &receipt) != nil || receipt.RollbackOperation == nil {
		t.Fatalf("admission: %d %s", response.Code, response.Body)
	}
	if err = s.MarkDeploymentLive(t.Context(), target.ID); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_API_CONTRACT_DIFF_ENABLED", "1")
	if err = (*e.S()).CheckedRollbackSweep(t.Context()); err != nil {
		t.Fatal(err)
	}
	op, err := s.GetCheckedRollback(t.Context(), (*e.Acct()).ID, app.ID, receipt.RollbackOperation.ID)
	if err != nil || op.Status != "failed" || op.Code != api.CodeAPIContractBreakingChange {
		t.Fatalf("delayed contract gate: %+v %v", op, err)
	}
	serving, err := s.DeploymentByID(t.Context(), current.ID)
	if err != nil || serving.TrafficPercent != 100 {
		t.Fatalf("contract failure changed serving traffic: %+v %v", serving, err)
	}
}

func TestCheckedRollbackAllowsCompatibleCapturedContract(t *testing.T) {
	t.Setenv("FAAS_API_CONTRACT_DIFF_ENABLED", "1")
	e := apidserver.Setup(t, api.PlanPro)
	s := *e.Store()
	app, err := s.CreateApp(t.Context(), state.App{AccountID: (*e.Acct()).ID, Slug: "compatible-contract", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	target := createRollbackContractDeployment(t, s, app, "default", false)
	current := createRollbackContractDeployment(t, s, app, "default", false)
	writeRollbackContractSnapshot(t, s, target, rollbackContractNew)
	writeRollbackContractSnapshot(t, s, current, rollbackContractOld)
	response := e.Do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/rollback", api.RollbackRequest{TargetDeploymentID: &target.ID, ExpectedCurrentDeploymentID: &current.ID}, nil)
	var receipt api.DeploymentResponse
	if response.Code != 202 || json.Unmarshal(response.Body.Bytes(), &receipt) != nil || receipt.RollbackOperation == nil {
		t.Fatalf("compatible admission: %d %s", response.Code, response.Body)
	}
	if err = s.MarkDeploymentLive(t.Context(), target.ID); err != nil {
		t.Fatal(err)
	}
	if err = (*e.S()).CheckedRollbackSweep(t.Context()); err != nil {
		t.Fatal(err)
	}
	op, err := s.GetCheckedRollback(t.Context(), (*e.Acct()).ID, app.ID, receipt.RollbackOperation.ID)
	if err != nil || op.Status != "complete" {
		t.Fatalf("compatible rollback did not complete: %+v %v", op, err)
	}
}
