package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
)

func restartProgressDashboardCookie(t *testing.T, e testEnv, accountID string, pending bool) *http.Cookie {
	t.Helper()
	sid := uuid.NewString()
	if _, err := e.store.CreateSession(t.Context(), sid, accountID, "192.0.2.10", "restart-progress-test"); err != nil {
		t.Fatal(err)
	}
	token, err := e.s.sessions.IssueWithSession(sid, accountID, pending)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: sessionCookie, Value: token}
}

func restartProgressDashboardGET(e testEnv, cookie *http.Cookie, path string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w
}

func TestDashboardRestartProgressUsesAPIStatusAndNeverMutates(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := seedAppForTimeline(t, e, "restart-progress")
	wakeID := uuid.NewString()
	now := time.Now().UTC()
	reader := &runtimeConfigRestartStatusNotifier{status: db.RuntimeConfigRestartStatus{
		State: "pending", Attempts: 3, RequestedAt: now.Add(-time.Minute), LastError: "PRIVATE_DETAILS reason=requests_active",
	}}
	e.s.notif = reader
	cookie := restartProgressDashboardCookie(t, e, e.acct.ID, false)
	path := "/dashboard/apps/" + app.Slug + "/restarts/" + wakeID
	for _, state := range []string{"pending", "processing", "delivered", "dead_letter"} {
		t.Run(state, func(t *testing.T) {
			reader.status.State = state
			reader.status.CompletedAt = nil
			if state == "delivered" {
				reader.status.CompletedAt = &now
			}
			before, err := e.store.AppByID(t.Context(), app.ID)
			if err != nil {
				t.Fatal(err)
			}
			apiResponse := e.do(t, "GET", "/v1/apps/"+app.Slug+"/runtime-config-restarts/"+wakeID, nil, nil)
			var status api.RuntimeConfigRestartStatusResponse
			if apiResponse.Code != 200 || json.Unmarshal(apiResponse.Body.Bytes(), &status) != nil {
				t.Fatalf("API status: %d %s", apiResponse.Code, apiResponse.Body)
			}
			page := restartProgressDashboardGET(e, cookie, path)
			body := page.Body.String()
			if page.Code != 200 || !strings.Contains(body, status.ProgressMessage()) || !strings.Contains(body, status.ProgressNextStep()) || !strings.Contains(body, wakeID) {
				t.Fatalf("dashboard differs from API: %d %s", page.Code, body)
			}
			pending := state == "pending" || state == "processing"
			if strings.Contains(body, `http-equiv="refresh"`) != pending {
				t.Fatalf("refresh did not stop at terminal state: %s", body)
			}
			if strings.Contains(body, "PRIVATE_DETAILS") {
				t.Fatal("dashboard exposed internal errors")
			}
			if state == "delivered" && !strings.Contains(body, "Application health has not been verified") {
				t.Fatal("restart processing completion established health")
			}
			after, err := e.store.AppByID(t.Context(), app.ID)
			if err != nil || after.Status != before.Status {
				t.Fatalf("status read mutated app: %+v %v", after, err)
			}
			if instances, err := e.store.ListInstancesForApp(t.Context(), app.ID); err != nil || len(instances) != 0 {
				t.Fatalf("status read woke an instance: %+v %v", instances, err)
			}
		})
	}
}

func TestDashboardRestartProgressUnavailableAndAccess(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := seedAppForTimeline(t, e, "restart-access")
	wakeID := uuid.NewString()
	path := "/dashboard/apps/" + app.Slug + "/restarts/" + wakeID
	reader := &runtimeConfigRestartStatusNotifier{err: errors.New("PRIVATE_STORE_ERROR")}
	e.s.notif = reader
	cookie := restartProgressDashboardCookie(t, e, e.acct.ID, false)
	page := restartProgressDashboardGET(e, cookie, path)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "may still be processing") || strings.Contains(page.Body.String(), "PRIVATE_STORE_ERROR") || strings.Contains(page.Body.String(), `http-equiv="refresh"`) {
		t.Fatalf("unavailable status: %d %s", page.Code, page.Body)
	}
	reader.err = db.ErrRuntimeConfigRestartNotFound
	if page := restartProgressDashboardGET(e, cookie, path); page.Code != 404 {
		t.Fatalf("missing request: %d", page.Code)
	}
	other, err := e.store.CreateAccount(t.Context(), "other-restart@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	reader.appID = ""
	otherCookie := restartProgressDashboardCookie(t, e, other.ID, false)
	if page := restartProgressDashboardGET(e, otherCookie, path); page.Code != 404 || reader.appID != "" {
		t.Fatalf("foreign app status disclosed: %d lookup=%s", page.Code, reader.appID)
	}
	if page := restartProgressDashboardGET(e, nil, path); page.Code == 200 {
		t.Fatal("anonymous restart status permitted")
	}
	if err := e.store.MarkMFAEnrolled(t.Context(), e.acct.ID); err != nil {
		t.Fatal(err)
	}
	pendingCookie := restartProgressDashboardCookie(t, e, e.acct.ID, true)
	if page := restartProgressDashboardGET(e, pendingCookie, path); page.Code == 200 {
		t.Fatal("pending MFA restart status permitted")
	}
}

func TestDashboardRestartProgressRejectsUnconfirmedCompletion(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := seedAppForTimeline(t, e, "restart-incomplete")
	wakeID := uuid.NewString()
	e.s.notif = &runtimeConfigRestartStatusNotifier{status: db.RuntimeConfigRestartStatus{State: "delivered", RequestedAt: time.Now().UTC()}}
	cookie := restartProgressDashboardCookie(t, e, e.acct.ID, false)
	page := restartProgressDashboardGET(e, cookie, "/dashboard/apps/"+app.Slug+"/restarts/"+wakeID)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "status could not be read") || strings.Contains(page.Body.String(), "processing is complete") {
		t.Fatalf("missing completion receipt treated as complete: %d %s", page.Code, page.Body)
	}
}

func TestParseAppRestartProgressPath(t *testing.T) {
	id := uuid.NewString()
	for _, path := range []string{"demo/restarts/" + id, "demo/restarts/" + id + "/"} {
		slug, got, ok := parseAppRestartProgressPath(path)
		if !ok || slug != "demo" || got != id {
			t.Fatalf("path rejected: %s", path)
		}
	}
	for _, path := range []string{"demo/restarts/bad", "demo/restarts/" + uuid.Nil.String(), "demo/restarts/" + id + "/extra", "other/demo/restarts/" + id} {
		if _, _, ok := parseAppRestartProgressPath(path); ok {
			t.Fatalf("invalid path accepted: %s", path)
		}
	}
}
