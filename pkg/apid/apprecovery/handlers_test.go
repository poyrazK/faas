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

func purgeRequest(app state.App) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/v1/deleted-apps/"+app.ID+"/purge", nil)
	request.SetPathValue("id", app.ID)
	return request
}

// The owner's purge closes the restore window and purges the exact app at
// once, reporting the slug free.
func TestPurgeClosesTheWindowAndPurgesNow(t *testing.T) {
	handler, account, app, calls := fixture(t)
	handler.OnPurgeRequested = func(context.Context, *http.Request, state.Account, state.App) {
		*calls = append(*calls, "audit")
	}
	handler.PurgeNow = func(ctx context.Context, id, actor string) error {
		retained, err := handler.Store.AppByID(ctx, id)
		if err != nil || id != app.ID || actor != "customer:"+account.ID || retained.DeleteGraceUntil.After(time.Now()) {
			t.Fatalf("purge %s by %s with window %v: %v", id, actor, retained.DeleteGraceUntil, err)
		}
		*calls = append(*calls, "purge")
		return nil
	}
	response := httptest.NewRecorder()
	handler.Purge(response, purgeRequest(app), account)
	var out api.DeletedAppPurgeResponse
	if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil || response.Code != 200 || !out.Purged || out.Slug != app.Slug || out.ID != app.ID {
		t.Fatalf("purge=%d %+v %v", response.Code, out, err)
	}
	if strings.Join(*calls, ",") != "audit,purge" {
		t.Fatalf("callbacks=%v", *calls)
	}
}

// A purge something still holds answers 202 with the reason; the window
// stays closed so the sweep finishes it.
func TestPurgeBlockedAfterTheWindowClosedIsPending(t *testing.T) {
	handler, account, app, _ := fixture(t)
	handler.PurgeNow = func(context.Context, string, string) error {
		return &state.AppPurgeBlockedError{Reason: state.AppPurgeBlockedExclusiveWork}
	}
	response := httptest.NewRecorder()
	handler.Purge(response, purgeRequest(app), account)
	var out api.DeletedAppPurgeResponse
	if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil || response.Code != 202 || out.Purged || out.PendingReason != state.AppPurgeBlockedExclusiveWork {
		t.Fatalf("purge=%d %+v %v", response.Code, out, err)
	}
	if _, err := handler.Store.RestoreApp(t.Context(), app.ID, api.MustLimitsFor(api.PlanPro)); err == nil {
		t.Fatal("restore succeeded after the purge closed the window")
	}
}

type bucketHeldStore struct{ *state.MemStore }

func (bucketHeldStore) EndAppDeleteGrace(context.Context, string) (state.App, error) {
	return state.App{}, state.ErrConflict
}

// A live bucket refuses the purge before the window closes, so the app stays
// restorable and the owner can delete the bucket first.
func TestPurgeRefusedByABucketLeavesTheAppRestorable(t *testing.T) {
	handler, account, app, calls := fixture(t)
	handler.Store = bucketHeldStore{handler.Store.(*state.MemStore)}
	handler.PurgeNow = func(context.Context, string, string) error {
		*calls = append(*calls, "purge")
		return nil
	}
	response := httptest.NewRecorder()
	handler.Purge(response, purgeRequest(app), account)
	if response.Code != 409 || !strings.Contains(response.Body.String(), api.CodeAppNotPurgeable) || len(*calls) != 0 {
		t.Fatalf("purge=%d %s hooks=%v", response.Code, response.Body.String(), *calls)
	}
}

func TestForeignAccountPurgeIsMissingAndKeepsTheWindow(t *testing.T) {
	handler, account, app, calls := fixture(t)
	account.ID = "foreign-account"
	handler.PurgeNow = func(context.Context, string, string) error {
		*calls = append(*calls, "purge")
		return nil
	}
	response := httptest.NewRecorder()
	handler.Purge(response, purgeRequest(app), account)
	if response.Code != 404 || len(*calls) != 0 {
		t.Fatalf("foreign purge=%d hooks=%v", response.Code, *calls)
	}
	retained, err := handler.Store.AppByID(t.Context(), app.ID)
	if err != nil || !retained.DeleteGraceUntil.After(time.Now()) {
		t.Fatalf("foreign purge closed the window: %v %v", retained.DeleteGraceUntil, err)
	}
}
