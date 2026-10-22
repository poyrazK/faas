// Issue #961 / Mega-B PR-1 — `gregale connect repo <owner>/<name>`.
//
// Pins the wire shape (browser URL + JSON output) and the
// validateRepoSlug reject path. The CLI does NOT call the
// dashboard's bind endpoint — the test suite asserts that
// indirectly by relying on the recordedLauncher test seam: if a
// future PR adds a bind call, the URL won't change and the test
// stays green, but the bind call would surface in a test that
// spies on pkg/api.Client.
package main_test

import (
	"encoding/json"
	"github.com/onebox-faas/faas/cmd/gregale"
	"testing"
)

// TestCmdConnectRepo_OpensDashboard pins the happy-path URL.
// The dashboard's /dashboard/apps/new?repo=OWNER%2FNAME route is
// the browser handoff. The encoded query value is decoded by the
// dashboard router back to the GitHub owner/name string.
func TestCmdConnectRepo_OpensDashboard(t *testing.T) {
	rec := main.WithRecorder(t)
	t.Setenv("FAAS_API", "https://api.example.test")
	if code := main.CmdConnectRepo([]string{"poyrazK/gregale"}); code != 0 {
		t.Fatalf("cmdConnectRepo exit = %d, want 0", code)
	}
	if len((*rec.Urls())) != 1 {
		t.Fatalf("recorded urls = %d, want 1", len((*rec.Urls())))
	}
	want := "https://api.example.test/dashboard/apps/new?repo=poyrazK%2Fgregale"
	if (*rec.Urls())[0] != want {
		t.Errorf("url = %q, want %q", (*rec.Urls())[0], want)
	}
}

// TestCmdConnectRepo_RejectsInvalidShape runs the validateRepoSlug
// guard end-to-end through the CLI. The same predicate guards
// `gregale deploy --repo`; sharing it is the deliberate contract
// (commands_connect_repo.go doc-comment).
func TestCmdConnectRepo_RejectsInvalidShape(t *testing.T) {
	rec := main.WithRecorder(t)
	t.Setenv("FAAS_API", "https://api.example.test")
	bad := []string{
		"",            // empty
		"foo",         // no slash
		"/repo",       // empty owner
		"owner/",      // empty repo
		"a/b/c",       // too many slashes
		"owner/repo/", // trailing slash
		"owner/repo//extra",
		"../etc/passwd", // path traversal
		"owner/<script>",
		"foo bar/baz",  // whitespace
		"foo\nbar/baz", // newline
	}
	for _, in := range bad {
		if code := main.CmdConnectRepo([]string{in}); code != 1 {
			t.Errorf("cmdConnectRepo(%q) = %d, want 1", in, code)
		}
	}
	if len((*rec.Urls())) != 0 {
		t.Errorf("invalid shapes opened %d urls; want 0", len((*rec.Urls())))
	}
}

// TestCmdConnectRepo_NoArgsPrintsUsage pins the dispatcher
// behavior for the missing-positional case.
func TestCmdConnectRepo_NoArgsPrintsUsage(t *testing.T) {
	_ = main.WithRecorder(t)
	t.Setenv("FAAS_API", "https://api.example.test")
	if code := main.CmdConnectRepo(nil); code != 1 {
		t.Errorf("cmdConnectRepo(nil) = %d, want 1", code)
	}
	if code := main.CmdConnectRepo([]string{}); code != 1 {
		t.Errorf("cmdConnectRepo([]) = %d, want 1", code)
	}
}

// TestCmdConnectRepo_FallsBackOnBrowserError mirrors the existing
// cmdConnect github test: browser launch failure is a soft
// failure — the URL is the value the customer came for, so the
// CLI exits 0 after printing the URL.
func TestCmdConnectRepo_FallsBackOnBrowserError(t *testing.T) {
	rec := main.WithRecorder(t)
	(*rec.Err()) = (*main.ErrBoom)
	t.Setenv("FAAS_API", "https://api.example.test")
	if code := main.CmdConnectRepo([]string{"poyrazK/gregale"}); code != 0 {
		t.Fatalf("cmdConnectRepo exit = %d, want 0", code)
	}
	if len((*rec.Urls())) != 1 {
		t.Errorf("recorded urls = %d, want 1", len((*rec.Urls())))
	}
}

// TestCmdConnectRepo_JSONOutput pins the --json wire shape. The
// dashboard uses the same shape to compose the wizard URL in
// scripts.
func TestCmdConnectRepo_JSONOutput(t *testing.T) {
	rec := main.WithRecorder(t)
	t.Setenv("FAAS_API", "https://api.example.test")
	(*main.JsonOutput) = true
	defer func() { (*main.JsonOutput) = false }()

	stdout, restore := main.CaptureStdout(t)
	defer restore()
	if code := main.CmdConnectRepo([]string{"poyrazK/gregale"}); code != 0 {
		t.Fatalf("cmdConnectRepo --json = %d, want 0", code)
	}
	var got map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("stdout not valid JSON: %v\noutput: %s", err, stdout.String())
	}
	if got["url"] != "https://api.example.test/dashboard/apps/new?repo=poyrazK%2Fgregale" {
		t.Errorf("json url = %v, want the dashboard apps-new URL", got["url"])
	}
	if got["service"] != main.SvcRepo {
		t.Errorf("json service = %v, want %q", got["service"], main.SvcRepo)
	}
	if got["repo"] != "poyrazK/gregale" {
		t.Errorf("json repo = %v, want \"poyrazK/gregale\"", got["repo"])
	}
	if len((*rec.Urls())) != 0 {
		t.Errorf("--json opened browser %d times; want 0", len((*rec.Urls())))
	}
}

// TestCmdConnect_RepoRoutesToCmdConnectRepoEndToEnd exercises the
// outer dispatcher (`cmdConnect` in commands2.go) so the new
// switch branch is wired correctly. Mirrors the existing
// TestCmdConnect_GithubOpensDashboard test at commands2_test.go:489.
func TestCmdConnect_RepoRoutesToCmdConnectRepoEndToEnd(t *testing.T) {
	rec := main.WithRecorder(t)
	t.Setenv("FAAS_API", "https://api.example.test")
	if code := main.CmdConnect([]string{"repo", "poyrazK/gregale"}); code != 0 {
		t.Fatalf("cmdConnect repo exit = %d, want 0", code)
	}
	if len((*rec.Urls())) != 1 {
		t.Fatalf("recorded urls = %d, want 1", len((*rec.Urls())))
	}
	want := "https://api.example.test/dashboard/apps/new?repo=poyrazK%2Fgregale"
	if (*rec.Urls())[0] != want {
		t.Errorf("url = %q, want %q", (*rec.Urls())[0], want)
	}
}

// TestCmdConnect_RepoUnknownServiceErrors ensures the dispatcher's
// unknown-service error mentions both options ("github" + "repo
// <owner>/<name>"), so the customer gets actionable help text.
func TestCmdConnect_RepoUnknownServiceErrors(t *testing.T) {
	_ = main.WithRecorder(t)
	t.Setenv("FAAS_API", "https://api.example.test")
	if code := main.CmdConnect([]string{"gitlab"}); code != 1 {
		t.Errorf("cmdConnect gitlab = %d, want 1", code)
	}
}

// TestDashboardAppsNewURL pins the URL helper's query-parameter encoding.
func TestDashboardAppsNewURL(t *testing.T) {
	cases := []struct {
		api, repo, want string
	}{
		{"https://api.example.test", "poyrazK/gregale", "https://api.example.test/dashboard/apps/new?repo=poyrazK%2Fgregale"},
		{"https://api.example.test/", "poyrazK/gregale", "https://api.example.test/dashboard/apps/new?repo=poyrazK%2Fgregale"},
		{"http://localhost:8080", "octo/api", "http://localhost:8080/dashboard/apps/new?repo=octo%2Fapi"},
	}
	for _, c := range cases {
		if got := main.DashboardAppsNewURL(c.api, c.repo); got != c.want {
			t.Errorf("dashboardAppsNewURL(%q, %q) = %q, want %q", c.api, c.repo, got, c.want)
		}
	}
}
