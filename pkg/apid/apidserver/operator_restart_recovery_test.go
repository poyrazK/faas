package apidserver_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestGetOperatorIntentUnknownRestartEvidence(t *testing.T) {
	store := state.NewMemStore()
	account, err := store.CreateAccount(t.Context(), "restart-ops@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	key, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(t.Context(), account.ID, hash, "restart", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "restart-api", RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	node, err := store.ComputeNodeByName(t.Context(), state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := store.CreateInstance(t.Context(), app.ID, dep.ID, string(state.StateRunning), 512, node.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.InsertOperatorIntent(t.Context(), state.OperatorIntentKindForceRestart, instance.ID, &account.ID, account.ID, "recover", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := store.ClaimPendingOperatorIntent(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	target := state.OperatorRestartTarget{InstanceID: instance.ID, AppID: app.ID, DeploymentID: dep.ID, NodeID: node.ID, WakeID: instance.WakeID, Generation: "physical-vm-generation"}
	if _, err := store.PrepareOperatorRestart(t.Context(), intent.ID, intent.ClaimToken, target); err != nil {
		t.Fatal(err)
	}
	execution, _, err := store.StartOperatorRestartDestroy(t.Context(), intent.ID, intent.ClaimToken, node.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CompleteOperatorRestart(t.Context(), state.OperatorRestartCompletion{IntentID: intent.ID, ClaimToken: intent.ClaimToken, Status: state.OperatorIntentOutcomeUnknown, Error: "VM teardown acknowledgement unavailable"})
	if err != nil {
		t.Fatal(err)
	}
	server := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{}).WithAdminAllowlist(account.Email)
	request := httptest.NewRequest(http.MethodGet, "/v1/admin/operator-intents/"+intent.ID, nil)
	request.Header.Set("Authorization", "Bearer "+key)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response api.OperatorIntentResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Status != "outcome_unknown" || response.RestartExecution == nil || response.RestartExecution.Phase != state.RestartOutcomeUnknown || response.RestartExecution.Generation != target.Generation || response.RestartExecution.DestroyedAt != nil {
		t.Fatalf("missing recovery evidence: %+v", response)
	}
	if strings.Contains(recorder.Body.String(), intent.ClaimToken) || strings.Contains(recorder.Body.String(), execution.DestroyToken) {
		t.Fatal("polling response exposed an execution ownership token")
	}
}
