package apidserver_test

// adr: 1525

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

func TestGetOperatorIntentUnknownParkEvidence(t *testing.T) {
	store := state.NewMemStore()
	account, err := store.CreateAccount(t.Context(), "park-ops@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "park-api", RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
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
	_, err = store.InsertOperatorIntent(t.Context(), state.OperatorIntentKindForcePark, instance.ID, &account.ID, account.ID, "recover", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := store.ClaimPendingOperatorIntent(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	target := state.OperatorParkTarget{OperatorRestartTarget: state.OperatorRestartTarget{InstanceID: instance.ID, AppID: app.ID, DeploymentID: dep.ID, NodeID: node.ID, WakeID: instance.WakeID, Generation: "physical-vm-generation"}, InitialState: instance.State, Mode: instance.Mode, StartedAt: instance.StartedAt, LastRequestAt: instance.LastRequestAt, RequestCount: instance.RequestCount, Action: state.OperatorParkStop}
	if _, err := store.PrepareOperatorPark(t.Context(), intent.ID, intent.ClaimToken, target); err != nil {
		t.Fatal(err)
	}
	execution, _, err := store.StartOperatorPark(t.Context(), intent.ID, intent.ClaimToken, node.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CompleteOperatorPark(t.Context(), state.OperatorParkCompletion{IntentID: intent.ID, ClaimToken: intent.ClaimToken, Status: state.OperatorIntentOutcomeUnknown, Error: "VM teardown acknowledgement unavailable"})
	if err != nil {
		t.Fatal(err)
	}
	server := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{}).WithAdminAllowlist(account.Email)
	cookie := operatorSessionCookie(t, store, server.Sessions(), account.ID)
	request := httptest.NewRequest(http.MethodGet, "/v1/admin/operator-intents/"+intent.ID, nil)
	request.AddCookie(cookie)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response api.OperatorIntentResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Status != "outcome_unknown" || response.ParkExecution == nil || response.ParkExecution.Phase != state.ParkOutcomeUnknown || response.ParkExecution.Generation != target.Generation || response.ParkExecution.StoppedAt != nil {
		t.Fatalf("missing recovery evidence: %+v", response)
	}
	if strings.Contains(recorder.Body.String(), intent.ClaimToken) || strings.Contains(recorder.Body.String(), execution.EffectToken) {
		t.Fatal("polling response exposed an execution ownership token")
	}
}
