package main_test

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// ownerFixtureServer answers one delayed task and one edge rule owned by app
// "web" (id app-web); app "other" exists with id app-other.
func ownerFixtureServer(t *testing.T, taskID, ruleID string, deleted *int) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/delayed-tasks/"+taskID:
			main.WriteJSONTest(w, api.DelayedTaskResponse{ID: taskID, AppID: "app-web", State: "completed"})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/edge-rules/"+ruleID:
			main.WriteJSONTest(w, api.EdgeRuleResponse{ID: ruleID, AppID: "app-web"})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/edge-rules/"+ruleID:
			*deleted++
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/web":
			main.WriteJSONTest(w, api.AppResponse{ID: "app-web", Slug: "web"})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/other":
			main.WriteJSONTest(w, api.AppResponse{ID: "app-other", Slug: "other"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
}

// TestOwnerScopedGetAndRm pins H10-124 and H10-131: `delayed-task get <id>
// --app <slug>` and `edge-rules rm <id> --app <slug>` were refused outright;
// --app now checks the owner.
func TestOwnerScopedGetAndRm(t *testing.T) {
	main.ResetJSONOut(t)
	const taskID = "3081e8fe-e18a-44ed-b7c4-e38dd7d822e7"
	const ruleID = "37f7d80e-4471-46c1-b4cf-0e7fb765adb6"
	deleted := 0
	ownerFixtureServer(t, taskID, ruleID, &deleted)
	_, restore := main.CaptureStdout(t)
	defer restore()
	if code := main.CmdDelayedTaskGet([]string{taskID, "--app", "web"}); code != 0 {
		t.Fatalf("delayed-task get --app web exit = %d", code)
	}
	if code := main.CmdDelayedTaskGet([]string{taskID, "--app", "other"}); code == 0 {
		t.Fatal("delayed-task get accepted a task owned by another app")
	}
	if code := main.CmdEdgeRulesRm([]string{ruleID, "--app", "other", "--yes"}); code == 0 || deleted != 0 {
		t.Fatalf("edge-rules rm deleted another app's rule: exit=%d deleted=%d", code, deleted)
	}
	if code := main.CmdEdgeRulesRm([]string{ruleID, "--app", "web", "--yes"}); code != 0 || deleted != 1 {
		t.Fatalf("edge-rules rm --app web: exit=%d deleted=%d", code, deleted)
	}
}

// TestWithoutConfirmationFlags pins H10-130: version-delete counted --yes as
// a fifth argument.
func TestWithoutConfirmationFlags(t *testing.T) {
	got := main.WithoutConfirmationFlags([]string{"--yes", "app", "-q", "bucket", "key", "--quiet", "version"})
	if want := []string{"app", "bucket", "key", "version"}; !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}
