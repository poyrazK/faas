// adr: 968 — owner Data API tools preserve app-task and refresh boundaries.
package apidserver_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/dataapi"
	"github.com/onebox-faas/faas/pkg/state"
)

func dataAPIDashboardPost(e apidserver.TestEnv, session, csrf *http.Cookie, path, token string) *httptest.ResponseRecorder {
	form := url.Values{"csrf_token": {token}, "command": {"arbitrary SQL must not execute"}}
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(session)
	if csrf != nil {
		r.AddCookie(csrf)
	}
	w := httptest.NewRecorder()
	(*e.H()).ServeHTTP(w, r)
	return w
}

func finishDataAPIDashboardTask(t *testing.T, e apidserver.TestEnv, task state.AppTask, content string, truncated bool) {
	t.Helper()
	now := time.Now().UTC()
	claimed, err := (*e.Store()).ClaimNextAppTask(t.Context(), "data-api-dashboard-test", now, time.Minute)
	if err != nil || claimed.ID != task.ID {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	if _, err := (*e.Store()).MarkAppTaskRunning(t.Context(), task.ID, *claimed.LeaseToken, now); err != nil {
		t.Fatal(err)
	}
	exit := 0
	if _, err := (*e.Store()).CompleteAppTask(t.Context(), state.CompleteAppTaskParams{ID: task.ID, LeaseToken: *claimed.LeaseToken, Status: state.AppTaskSucceeded, ExitCode: &exit, StdoutTail: content, OutputTruncated: truncated, FinishedAt: now}); err != nil {
		t.Fatal(err)
	}
}

func TestDashboardDataAPIOperationsAreOwnerScopedAndCSRFBound(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.EnableAppTaskAPIForTest(&e)
	app, _ := apidserver.SeedAppTaskDeployment(t, e, "data-tools")
	cookie := apidserver.RestartProgressDashboardCookie(t, e, (*e.Acct()).ID, false)
	path := "/dashboard/apps/" + app.Slug + "/data-api"
	page := apidserver.RestartProgressDashboardGET(e, cookie, path)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "Inspect schema") {
		t.Fatalf("page = %d %s", page.Code, page.Body)
	}
	rows, _ := (*e.Store()).ListAppTasks(t.Context(), (*e.Acct()).ID, app.ID, 10, 0)
	if len(rows) != 0 {
		t.Fatal("GET admitted a task")
	}
	token := apidserver.DashboardInputToken(page.Body.String())
	csrf := apidserver.FindDashboardCookie(page.Result().Cookies(), apidserver.DashboardDataAPICookie)
	if rec := dataAPIDashboardPost(e, cookie, nil, path+"/schema", token); rec.Code != 400 {
		t.Fatalf("missing CSRF = %d", rec.Code)
	}
	if rec := dataAPIDashboardPost(e, cookie, csrf, path+"/shell", token); rec.Code != 404 {
		t.Fatalf("arbitrary operation = %d", rec.Code)
	}
	other, _ := (*e.Store()).CreateAccount(t.Context(), "other-data-api@example.com", api.PlanPro)
	otherCookie := apidserver.RestartProgressDashboardCookie(t, e, other.ID, false)
	if rec := apidserver.RestartProgressDashboardGET(e, otherCookie, path); rec.Code != 404 {
		t.Fatalf("cross-account page = %d", rec.Code)
	}
	for _, auth := range []*http.Cookie{nil, apidserver.RestartProgressDashboardCookie(t, e, (*e.Acct()).ID, true)} {
		if rec := apidserver.RestartProgressDashboardGET(e, auth, path); rec.Code == 200 {
			t.Fatal("anonymous or pending MFA read allowed")
		}
	}
	post := dataAPIDashboardPost(e, cookie, csrf, path+"/schema", token)
	if post.Code != 303 {
		t.Fatalf("inspect = %d %s", post.Code, post.Body)
	}
	rows, _ = (*e.Store()).ListAppTasks(t.Context(), (*e.Acct()).ID, app.ID, 10, 0)
	if len(rows) != 1 || apidserver.DataAPITaskKind(rows[0]) != "schema" || rows[0].CommandShell {
		t.Fatalf("unexpected task: %+v", rows)
	}
	progress := apidserver.RestartProgressDashboardGET(e, cookie, post.Header().Get("Location"))
	if !strings.Contains(progress.Body.String(), `http-equiv="refresh"`) {
		t.Fatal("queued operation did not refresh")
	}
	snapshot, err := json.Marshal(dataapi.Snapshot{Version: 3, Schemas: []string{"api"}, Enums: map[string][]string{},
		Tables: []dataapi.Table{{Schema: "api", Name: "orders", Columns: []dataapi.Column{{Name: "id", Type: "number"}}, Relationships: json.RawMessage(`[]`)}},
		Functions: []dataapi.Function{{Schema: "api", Name: "latest_order", Args: []dataapi.Argument{{Name: "source_row", Type: `Database["api"]["Tables"]["orders"]['Row']`}},
			Returns: `Database["api"]["Tables"]["orders"]['Row'][]`, Setof: &dataapi.Setof{From: "orders", To: "orders", OneToOne: true}}},
		Composites: []dataapi.Composite{{Schema: "api", Name: "location", Fields: []dataapi.CompositeField{{Name: "x", Type: "number", Nullable: true}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	contract := fmt.Sprintf(`{"version":1,"fingerprint":"%x","snapshot":%s}`, sha256.Sum256([]byte(snapshot)), snapshot)
	finishDataAPIDashboardTask(t, e, rows[0], contract, false)
	result := apidserver.RestartProgressDashboardGET(e, cookie, post.Header().Get("Location"))
	if !strings.Contains(result.Body.String(), "Schema fingerprint:") || !strings.Contains(result.Body.String(), "Composite api.location") ||
		!strings.Contains(result.Body.String(), "RPC api.latest_order") || !strings.Contains(result.Body.String(), "Relationship from <code>orders</code>: one row or null") || strings.Contains(result.Body.String(), `http-equiv="refresh"`) {
		t.Fatalf("completed result = %s", result.Body)
	}
}

func TestDashboardDataAPITypesRejectStaleTruncatedAndCrossAppOutput(t *testing.T) {
	for _, truncated := range []bool{false, true} {
		t.Run(fmt.Sprint(truncated), func(t *testing.T) {
			e := apidserver.Setup(t, api.PlanPro)
			apidserver.EnableAppTaskAPIForTest(&e)
			app, _ := apidserver.SeedAppTaskDeployment(t, e, "types-download")
			otherApp, _ := apidserver.SeedAppTaskDeployment(t, e, "other-types")
			cookie := apidserver.RestartProgressDashboardCookie(t, e, (*e.Acct()).ID, false)
			response := apidserver.CreateAppTaskForTest(t, e, app.Slug, api.CreateAppTaskRequest{Command: apidserver.DataAPITaskCommand("types")})
			task, _ := (*e.Store()).AppTaskByID(t.Context(), (*e.Acct()).ID, app.ID, response.ID)
			content := "// Generated by gregale data-api types. Do not edit.\n// Schema fingerprint: " + strings.Repeat("a", 64) + "\nexport type Database = {}\n"
			finishDataAPIDashboardTask(t, e, task, content, truncated)
			path := "/dashboard/apps/" + app.Slug + "/data-api/types/" + task.ID
			result := apidserver.RestartProgressDashboardGET(e, cookie, path)
			if truncated {
				if result.Code != 404 {
					t.Fatal("truncated output accepted")
				}
				return
			}
			if result.Code != 200 || result.Body.String() != content || result.Header().Get("Cache-Control") != "no-store" || result.Header().Get("Content-Disposition") == "" {
				t.Fatalf("download = %d %s", result.Code, result.Body)
			}
			if rec := apidserver.RestartProgressDashboardGET(e, cookie, "/dashboard/apps/"+otherApp.Slug+"/data-api/types/"+task.ID); rec.Code != 404 {
				t.Fatal("cross-app output accepted")
			}
			if _, err := (*e.Store()).CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Status: state.DeployLive, CreatedAt: time.Now().UTC().Add(time.Minute)}); err != nil {
				t.Fatal(err)
			}
			if rec := apidserver.RestartProgressDashboardGET(e, cookie, path); rec.Code != 404 {
				t.Fatal("stale output accepted")
			}
		})
	}
}

func TestDashboardDataAPIRefreshUsesExistingAdmission(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	app, _ := apidserver.SeedAppTaskDeployment(t, e, "data-refresh")
	cookie := apidserver.RestartProgressDashboardCookie(t, e, (*e.Acct()).ID, false)
	path := "/dashboard/apps/" + app.Slug + "/data-api"
	page := apidserver.RestartProgressDashboardGET(e, cookie, path)
	csrf := apidserver.FindDashboardCookie(page.Result().Cookies(), apidserver.DashboardDataAPICookie)
	token := apidserver.DashboardInputToken(page.Body.String())
	if post := dataAPIDashboardPost(e, cookie, nil, path+"/refresh", token); post.Code != 400 {
		t.Fatal("refresh without CSRF")
	}
	post := dataAPIDashboardPost(e, cookie, csrf, path+"/refresh", token)
	if post.Code != 303 || !strings.HasPrefix(post.Header().Get("Location"), "/dashboard/apps/"+app.Slug+"/restarts/") {
		t.Fatalf("refresh = %d %s", post.Code, post.Body)
	}
}
