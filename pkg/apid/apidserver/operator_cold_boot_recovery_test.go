package apidserver_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestGetOperatorIntentColdBootRecoveryGeneration(t *testing.T) {
	store := state.NewMemStore()
	account, err := store.CreateAccount(t.Context(), "cold-boot-ops@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	key, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(t.Context(), account.ID, hash, "cold-boot", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "cold-boot-api", RAMMB: 512, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.InsertOperatorIntent(t.Context(), state.OperatorIntentKindForceColdBoot, dep.ID, &account.ID, account.ID, "recover", nil, nil); err != nil {
		t.Fatal(err)
	}
	i, err := store.ClaimPendingOperatorIntent(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.ExecuteOperatorColdBoot(t.Context(), i.ID, i.ClaimToken)
	if err != nil {
		t.Fatal(err)
	}
	server := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{}).WithAdminAllowlist(account.Email)
	for range 2 {
		request := httptest.NewRequest(http.MethodGet, "/v1/admin/operator-intents/"+i.ID, nil)
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
		if response.Status != "succeeded" || response.ColdBootGeneration != result.Generation || response.ColdBootGeneration != 1 || response.FinishedAt == nil {
			t.Fatalf("missing committed recovery result: %+v", response)
		}
		if strings.Contains(recorder.Body.String(), i.ClaimToken) {
			t.Fatal("polling exposed internal claim token")
		}
	}
}
