package apprecovery

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func fixture(t *testing.T) (Handler, state.Account, state.App, *[]string) {
	t.Helper()
	store := state.NewMemStore()
	account, err := store.CreateAccount(t.Context(), "recovery@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "retained-app", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	app, err = store.ScheduleAppDeletion(t.Context(), app.ID, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	calls := []string{}
	handler := Handler{Store: store,
		RestoreWithActivity: func(ctx context.Context, _ *http.Request, a state.Account, reviewed state.App, limits api.Limits) (state.App, error) {
			if a.ID != account.ID || reviewed.ID != app.ID {
				t.Fatal("changed restore identity")
			}
			calls = append(calls, "restore")
			return store.RestoreApp(ctx, reviewed.ID, limits)
		},
		OnRestored: func(_ context.Context, _ *http.Request, _ state.Account, _ state.App) {
			calls = append(calls, "notify")
		},
		Response: func(_ context.Context, a state.App, _ api.Plan) api.AppResponse {
			calls = append(calls, "response")
			return api.AppResponse{ID: a.ID, Slug: a.Slug, Status: string(a.Status)}
		},
		ParkedRef: func(_ context.Context, r api.AppResponse, _ state.App) api.AppResponse {
			calls = append(calls, "parked")
			return r
		},
	}
	return handler, account, app, &calls
}

func TestExactRestorePreservesLifecycleCallbacksAndUndeployedEvidence(t *testing.T) {
	handler, account, app, calls := fixture(t)
	request := httptest.NewRequest(http.MethodPost, "/v1/deleted-apps/"+app.ID+"/restore", nil)
	request.SetPathValue("id", app.ID)
	response := httptest.NewRecorder()
	handler.Restore(response, request, account)
	var result api.AppResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || response.Code != 200 || result.ID != app.ID || result.Status != api.AppStatusUndeployed {
		t.Fatalf("restore=%d %+v %v", response.Code, result, err)
	}
	if strings.Join(*calls, ",") != "restore,notify,response,parked" {
		t.Fatalf("callbacks=%v", *calls)
	}
}

func TestUnknownIntentNeverRunsLifecycleHooks(t *testing.T) {
	for _, body := range []string{"null", `{"dry_run":true}`, `{"expected_app_id":"wrong"}`} {
		handler, account, app, calls := fixture(t)
		request := httptest.NewRequest(http.MethodPost, "/v1/deleted-apps/"+app.ID+"/restore", strings.NewReader(body))
		request.SetPathValue("id", app.ID)
		response := httptest.NewRecorder()
		handler.Restore(response, request, account)
		if response.Code != 400 || len(*calls) != 0 {
			t.Fatalf("intent %s=%d hooks=%v", body, response.Code, *calls)
		}
		retained, err := handler.Store.AppByID(t.Context(), app.ID)
		if err != nil || retained.Status != state.AppDeleted {
			t.Fatal("unexpected restore", err)
		}
	}
}

func TestForeignAccountReadAndRestoreAreMissingWithoutCallbacks(t *testing.T) {
	handler, account, app, calls := fixture(t)
	account.ID = "foreign-account"
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		request := httptest.NewRequest(method, "/v1/deleted-apps/"+app.ID, nil)
		request.SetPathValue("id", app.ID)
		response := httptest.NewRecorder()
		if method == http.MethodGet {
			handler.Get(response, request, account)
		} else {
			handler.Restore(response, request, account)
		}
		if response.Code != 404 || len(*calls) != 0 {
			t.Fatalf("foreign %s=%d hooks=%v", method, response.Code, *calls)
		}
	}
}
