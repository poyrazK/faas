package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	apidserver "github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDeletedAppsInventoryIsBoundedAndAccountOwned(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	ctx := context.Background()
	other, _ := (*e.Store()).CreateAccount(ctx, "foreign-tombstone@example.com", api.PlanPro)
	deadline := time.Now().Add(time.Hour)
	for _, slug := range []string{"owned-tombstone-a", "owned-tombstone-b"} {
		id := apidserver.MustSeedApp(t, e, slug)
		if _, err := (*e.Store()).ScheduleAppDeletion(ctx, id, deadline); err != nil {
			t.Fatal(err)
		}
	}
	foreign, _ := (*e.Store()).CreateApp(ctx, state.App{AccountID: other.ID, Slug: "foreign-tombstone", Status: state.AppActive})
	_, _ = (*e.Store()).ScheduleAppDeletion(ctx, foreign.ID, deadline)
	rec := e.Do(t, http.MethodGet, "/v1/deleted-apps?limit=1", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("inventory = %d: %s", rec.Code, rec.Body)
	}
	var page struct {
		Apps []struct {
			ID       string    `json:"id"`
			Slug     string    `json:"slug"`
			Eligible bool      `json:"restore_eligible"`
			Until    time.Time `json:"delete_grace_until"`
		} `json:"apps"`
		Next      string    `json:"next_cursor"`
		Evaluated time.Time `json:"evaluated_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Apps) != 1 || !page.Apps[0].Eligible || !page.Apps[0].Until.Equal(deadline) || page.Evaluated.IsZero() || page.Next == "" {
		t.Fatalf("page = %+v", page)
	}
	next := e.Do(t, http.MethodGet, "/v1/deleted-apps?limit=1&cursor="+page.Next, nil, nil)
	if next.Code != 200 {
		t.Fatalf("next = %d: %s", next.Code, next.Body)
	}
	var second struct {
		Apps []struct {
			ID string `json:"id"`
		} `json:"apps"`
		Next string `json:"next_cursor"`
	}
	_ = json.Unmarshal(next.Body.Bytes(), &second)
	if len(second.Apps) != 1 || second.Apps[0].ID == page.Apps[0].ID || second.Next != "" {
		t.Fatalf("second = %+v", second)
	}
	apidserver.AssertProblem(t, e.Do(t, http.MethodGet, "/v1/deleted-apps/"+foreign.ID, nil, nil), 404, api.CodeNotFound)
	// A cursor generated for the original account is not accepted by another account.
	pt, hash, _ := api.GenerateAPIKey()
	_, _ = (*e.Store()).CreateAPIKey(ctx, other.ID, hash, "other", api.ScopesAdminOnly)
	otherEnv := e
	*otherEnv.Key() = pt
	if out := otherEnv.Do(t, http.MethodGet, "/v1/deleted-apps?cursor="+page.Next, nil, nil); out.Code != 400 {
		t.Fatalf("foreign cursor = %d: %s", out.Code, out.Body)
	}
	for _, query := range []string{"limit=0", "limit=51", "limit=no", "cursor=not-a-cursor"} {
		if out := e.Do(t, http.MethodGet, "/v1/deleted-apps?"+query, nil, nil); out.Code != 400 {
			t.Fatalf("%s = %d: %s", query, out.Code, out.Body)
		}
	}
}

func TestDeletedAppsInventoryReportsLifecycleBlockersAndRequiresAuth(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	id := apidserver.MustSeedApp(t, e, "expired-tombstone")
	_, _ = (*e.Store()).ScheduleAppDeletion(context.Background(), id, time.Now().Add(-time.Hour))
	read := func(want string) {
		t.Helper()
		r := e.Do(t, http.MethodGet, "/v1/deleted-apps/"+id, nil, nil)
		var out struct {
			Eligible bool   `json:"restore_eligible"`
			Blocker  string `json:"restore_blocker"`
		}
		if r.Code != 200 {
			t.Fatalf("detail = %d: %s", r.Code, r.Body)
		}
		_ = json.Unmarshal(r.Body.Bytes(), &out)
		if out.Eligible || out.Blocker != want {
			t.Fatalf("detail = %+v; want %s", out, want)
		}
	}
	read("grace_expired")
	if err := (*e.Store()).ClaimAppDeletion(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	read("purge_started")
	rec := httptest.NewRecorder()
	(*e.H()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/deleted-apps", nil))
	if rec.Code != 401 {
		t.Fatalf("unauthenticated = %d", rec.Code)
	}
}

func TestDeletedAppsInventoryRequiresReadScopeAndPreservesDeletedSlug(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	id := apidserver.MustSeedApp(t, e, "deleted")
	rec := e.Do(t, http.MethodGet, "/v1/apps/deleted", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("existing slug read = %d: %s", rec.Code, rec.Body)
	}
	var app api.AppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &app); err != nil || app.ID != id {
		t.Fatalf("existing slug identity = %+v, %v", app, err)
	}
	pt, hash, _ := api.GenerateAPIKey()
	if _, err := (*e.Store()).CreateAPIKey(context.Background(), e.Acct().ID, hash, "write-only", []string{api.ScopeDeployWrite}); err != nil {
		t.Fatal(err)
	}
	*e.Key() = pt
	if rec := e.Do(t, http.MethodGet, "/v1/deleted-apps", nil, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("write-only inventory = %d: %s", rec.Code, rec.Body)
	}
}

func TestDeletedAppsInventoryRequiresMFA(t *testing.T) {
	e := apidserver.SetupWithMFA(t, api.PlanPro, true, false)
	request := httptest.NewRequest(http.MethodGet, "/v1/deleted-apps", nil)
	request.AddCookie(*e.Cookie())
	rec := httptest.NewRecorder()
	(*e.H()).ServeHTTP(rec, request)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("pending MFA inventory = %d: %s", rec.Code, rec.Body)
	}
}

func TestRestoreAppRejectsChangedInventoryIdentity(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	id := apidserver.MustSeedApp(t, e, "replacement-tombstone")
	_, _ = (*e.Store()).ScheduleAppDeletion(context.Background(), id, time.Now().Add(time.Hour))
	r := e.Do(t, http.MethodPost, "/v1/apps/replacement-tombstone/restore", map[string]string{"expected_app_id": uuid.NewString()}, nil)
	if r.Code != 409 {
		t.Fatalf("stale restore = %d: %s", r.Code, r.Body)
	}
	still, err := (*e.Store()).AppBySlugIncludingDeleted(context.Background(), "replacement-tombstone")
	if err != nil || still.Status != state.AppDeleted {
		t.Fatalf("replacement was changed: %+v, %v", still, err)
	}
	for _, body := range []map[string]any{{"expected_app_id": "bad"}, {"expected_app_id": nil}, {"expected_app_id": ""}, {"unexpected": "field"}} {
		if r := e.Do(t, http.MethodPost, "/v1/apps/replacement-tombstone/restore", body, nil); r.Code != 400 {
			t.Fatalf("bad precondition = %d: %s", r.Code, r.Body)
		}
	}
	r = e.Do(t, http.MethodPost, "/v1/apps/replacement-tombstone/restore", map[string]string{"expected_app_id": id}, nil)
	if r.Code != 200 {
		t.Fatalf("identified restore = %d: %s", r.Code, r.Body)
	}
	apidserver.AssertProblem(t, e.Do(t, http.MethodGet, "/v1/deleted-apps/"+id, nil, nil), 404, api.CodeNotFound)
}

func TestRestoreDeletedAppDedicatedIdentityRoute(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	id := apidserver.MustSeedApp(t, e, "identified-tombstone")
	_, _ = (*e.Store()).ScheduleAppDeletion(context.Background(), id, time.Now().Add(time.Hour))
	if r := e.Do(t, http.MethodPost, "/v1/deleted-apps/"+id+"/restore", map[string]string{"dry_run": "true"}, nil); r.Code != 400 {
		t.Fatalf("unknown restore intent = %d: %s", r.Code, r.Body)
	}
	r := e.Do(t, http.MethodPost, "/v1/deleted-apps/"+id+"/restore", nil, nil)
	if r.Code != 200 {
		t.Fatalf("identified route = %d: %s", r.Code, r.Body)
	}
	var restored api.AppResponse
	if err := json.Unmarshal(r.Body.Bytes(), &restored); err != nil || restored.ID != id {
		t.Fatalf("restore identity = %+v, %v", restored, err)
	}
	if r := e.Do(t, http.MethodPost, "/v1/deleted-apps/"+id+"/restore", nil, nil); r.Code != 404 {
		t.Fatalf("active app is no longer in deleted collection = %d", r.Code)
	}
	other, _ := (*e.Store()).CreateAccount(context.Background(), "foreign-restore@example.com", api.PlanPro)
	foreign, _ := (*e.Store()).CreateApp(context.Background(), state.App{AccountID: other.ID, Slug: "foreign-restore", Status: state.AppActive})
	_, _ = (*e.Store()).ScheduleAppDeletion(context.Background(), foreign.ID, time.Now().Add(time.Hour))
	apidserver.AssertProblem(t, e.Do(t, http.MethodPost, "/v1/deleted-apps/"+foreign.ID+"/restore", nil, nil), 404, api.CodeNotFound)
}

func TestRestoreDeletedAppDedicatedRouteRechecksExpiryAndScope(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	id := apidserver.MustSeedApp(t, e, "expired-restore")
	_, _ = (*e.Store()).ScheduleAppDeletion(context.Background(), id, time.Now().Add(-time.Hour))
	apidserver.AssertProblem(t, e.Do(t, http.MethodPost, "/v1/deleted-apps/"+id+"/restore", nil, nil), 409, api.CodeAppNotRestorable)
	pt, hash, _ := api.GenerateAPIKey()
	_, _ = (*e.Store()).CreateAPIKey(context.Background(), e.Acct().ID, hash, "read-only", []string{api.ScopeAppsRead})
	*e.Key() = pt
	if rec := e.Do(t, http.MethodPost, "/v1/deleted-apps/"+id+"/restore", nil, nil); rec.Code != 403 {
		t.Fatalf("read-only restore = %d: %s", rec.Code, rec.Body)
	}
}
