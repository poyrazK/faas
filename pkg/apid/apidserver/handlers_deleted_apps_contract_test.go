package apidserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	apidserver "github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

func TestDeletedAppsEmptyAndMissingLifecycleMetadata(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	r := e.Do(t, http.MethodGet, "/v1/deleted-apps", nil, nil)
	if r.Code != 200 {
		t.Fatalf("empty inventory = %d: %s", r.Code, r.Body)
	}
	var page struct {
		Apps []json.RawMessage `json:"apps"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &page); err != nil || page.Apps == nil || len(page.Apps) != 0 {
		t.Fatalf("empty page = %+v, %v", page, err)
	}
	app, err := (*e.Store()).CreateApp(context.Background(), state.App{AccountID: e.Acct().ID, Slug: "legacy-deleted", Status: state.AppDeleted})
	if err != nil {
		t.Fatal(err)
	}
	r = e.Do(t, http.MethodGet, "/v1/deleted-apps/"+app.ID, nil, nil)
	var summary struct {
		DeletedAt *time.Time `json:"deleted_at"`
		Deadline  *time.Time `json:"delete_grace_until"`
		Eligible  bool       `json:"restore_eligible"`
		Blocker   string     `json:"restore_blocker"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &summary); err != nil || r.Code != 200 || summary.DeletedAt != nil || summary.Deadline != nil || summary.Eligible || summary.Blocker != "metadata_unavailable" {
		t.Fatalf("legacy metadata = %d: %+v, %v", r.Code, summary, err)
	}
}

func TestDeletedAppsRejectsUnqualifiedScopeAndDuplicatePagination(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	id := apidserver.MustSeedApp(t, e, "scoped-tombstone")
	_, _ = (*e.Store()).ScheduleAppDeletion(context.Background(), id, time.Now().Add(time.Hour))
	for _, path := range []string{
		"/v1/deleted-apps?limit=1&limit=2",
		"/v1/deleted-apps?cursor=a&cursor=b",
		"/v1/deleted-apps?environment=staging",
		"/v1/deleted-apps/" + id + "/restore?environment=staging",
	} {
		method := http.MethodGet
		if path == "/v1/deleted-apps/"+id+"/restore?environment=staging" {
			method = http.MethodPost
		}
		if r := e.Do(t, method, path, nil, nil); r.Code != 400 {
			t.Errorf("unexpected scope/query accepted: %s = %d", path, r.Code)
		}
	}
	still, _ := (*e.Store()).AppBySlugIncludingDeleted(context.Background(), "scoped-tombstone")
	if still.Status != state.AppDeleted {
		t.Fatal("unqualified environment restored production app")
	}
}

type replacedDeletedSlugStore struct {
	MemStore    *state.MemStore
	replacement state.App
}

func (s replacedDeletedSlugStore) AppByID(context.Context, string) (state.App, error) {
	return s.replacement, nil
}

func TestRestoreDeletedAppRejectsReplacementSlugBeforeMutation(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	id := apidserver.MustSeedApp(t, e, "original-deleted")
	replacementID := apidserver.MustSeedApp(t, e, "replacement-deleted")
	for _, appID := range []string{id, replacementID} {
		_, _ = (*e.Store()).ScheduleAppDeletion(context.Background(), appID, time.Now().Add(time.Hour))
	}
	replacement, _ := (*e.Store()).AppBySlugIncludingDeleted(context.Background(), "replacement-deleted")
	replacement.Slug = "original-deleted"
	*(*e.S()).Store() = statetest.NewMem(replacedDeletedSlugStore{MemStore: (*e.Store()), replacement: replacement})
	r := e.Do(t, http.MethodPost, "/v1/deleted-apps/"+id+"/restore", nil, nil)
	if r.Code != 409 {
		t.Fatalf("replaced slug = %d: %s", r.Code, r.Body)
	}
	for _, slug := range []string{"original-deleted", "replacement-deleted"} {
		app, _ := (*e.Store()).AppBySlugIncludingDeleted(context.Background(), slug)
		if app.Status != state.AppDeleted {
			t.Errorf("mutated %s instead of rejecting changed identity", slug)
		}
	}
}

func TestRestoreDeletedAppRejectsAnActiveSlugCollision(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	id := apidserver.MustSeedApp(t, e, "reused-slug")
	_, _ = (*e.Store()).ScheduleAppDeletion(context.Background(), id, time.Now().Add(time.Hour))
	replacement, err := (*e.Store()).CreateApp(context.Background(), state.App{AccountID: e.Acct().ID, Slug: "reused-slug", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	r := e.Do(t, http.MethodPost, "/v1/deleted-apps/"+id+"/restore", nil, nil)
	if r.Code != 409 {
		t.Fatalf("slug collision = %d: %s", r.Code, r.Body)
	}
	old, _ := (*e.Store()).AppByID(context.Background(), id)
	current, _ := (*e.Store()).AppByID(context.Background(), replacement.ID)
	if old.Status != state.AppDeleted || current.Status != state.AppActive {
		t.Fatalf("changed collision records: old=%s, replacement=%s", old.Status, current.Status)
	}
}

func TestRestoreDeletedAppDoesNotBypassMissingLifecycleMetadata(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	deadline := time.Now().Add(time.Hour)
	app, err := (*e.Store()).CreateApp(context.Background(), state.App{
		AccountID: e.Acct().ID, Slug: "missing-deletion-time", Status: state.AppDeleted, DeleteGraceUntil: &deadline,
	})
	if err != nil {
		t.Fatal(err)
	}
	r := e.Do(t, http.MethodPost, "/v1/deleted-apps/"+app.ID+"/restore", nil, nil)
	if r.Code != 409 {
		t.Fatalf("missing metadata restore = %d: %s", r.Code, r.Body)
	}
	still, _ := (*e.Store()).AppByID(context.Background(), app.ID)
	if still.Status != state.AppDeleted {
		t.Fatal("restored despite unavailable lifecycle metadata")
	}
}

func TestDeletedAppsReportsTheAuthoritativeAccountContext(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	id := apidserver.MustSeedApp(t, e, "owned-context")
	_, _ = (*e.Store()).ScheduleAppDeletion(context.Background(), id, time.Now().Add(time.Hour))
	for _, path := range []string{"/v1/deleted-apps", "/v1/deleted-apps/" + id} {
		r := e.Do(t, http.MethodGet, path, nil, nil)
		var body struct {
			AccountID string `json:"account_id"`
		}
		if err := json.Unmarshal(r.Body.Bytes(), &body); err != nil || r.Code != 200 || body.AccountID != e.Acct().ID {
			t.Fatalf("account context for %s = %d: %+v, %v", path, r.Code, body, err)
		}
	}
}

type failedDeletedRestoreStore struct {
	MemStore *state.MemStore
	failure  error
}

func (s failedDeletedRestoreStore) RestoreApp(context.Context, string, api.Limits) (state.App, error) {
	return state.App{}, s.failure
}

func (s failedDeletedRestoreStore) RestoreAppWithActivity(ctx context.Context, id string, limits api.Limits, _ state.OrgActivity) (state.App, int64, error) {
	app, err := s.RestoreApp(ctx, id, limits)
	return app, 0, err
}

func TestRestoreDeletedAppPreservesQuotaAndCapacityFailures(t *testing.T) {
	for _, tc := range []struct {
		name    string
		failure error
		status  int
		code    string
	}{
		{"quota", &state.QuotaError{Kind: state.QuotaErrorKindApps, Limit: 1, Observed: 1}, 403, api.CodePlanLimitApps},
		{"capacity", errors.New("fixture capacity unavailable"), 503, api.CodeCapacity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := apidserver.Setup(t, api.PlanPro)
			id := apidserver.MustSeedApp(t, e, "recoverable-restore")
			_, _ = (*e.Store()).ScheduleAppDeletion(context.Background(), id, time.Now().Add(time.Hour))
			*(*e.S()).Store() = statetest.NewMem(failedDeletedRestoreStore{MemStore: (*e.Store()), failure: tc.failure})
			apidserver.AssertProblem(t, e.Do(t, http.MethodPost, "/v1/deleted-apps/"+id+"/restore", nil, nil), tc.status, tc.code)
			still, _ := (*e.Store()).AppByID(context.Background(), id)
			if still.Status != state.AppDeleted {
				t.Fatal("failed restore changed the app")
			}
			*(*e.S()).Store() = (*e.Store())
			if r := e.Do(t, http.MethodPost, "/v1/deleted-apps/"+id+"/restore", nil, nil); r.Code != 200 {
				t.Fatalf("explicit corrected restore = %d: %s", r.Code, r.Body)
			}
		})
	}
}

func TestRestoreDeletedAppRequiresTrustedCookieOriginAndMFA(t *testing.T) {
	for _, pending := range []bool{false, true} {
		e := apidserver.SetupWithMFA(t, api.PlanPro, pending, false)
		app, err := (*e.Store()).CreateApp(context.Background(), state.App{AccountID: e.Acct().ID, Slug: "session-restore", Status: state.AppActive})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = (*e.Store()).ScheduleAppDeletion(context.Background(), app.ID, time.Now().Add(time.Hour))
		request := func(origin string) *httptest.ResponseRecorder {
			r := httptest.NewRequest(http.MethodPost, "/v1/deleted-apps/"+app.ID+"/restore", nil)
			r.AddCookie(*e.Cookie())
			r.Header.Set("Origin", origin)
			out := httptest.NewRecorder()
			(*e.H()).ServeHTTP(out, r)
			return out
		}
		if r := request("https://untrusted.invalid"); r.Code != 403 {
			t.Fatalf("untrusted origin = %d: %s", r.Code, r.Body)
		}
		r := request("https://example.com")
		want := 200
		if pending {
			want = 403
		}
		if r.Code != want {
			t.Fatalf("trusted origin with pending=%v = %d: %s", pending, r.Code, r.Body)
		}
	}
}
