package apidserver_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A dashboard app page that compares the app's creator with the signed-in
// account answers "no such app" to every other member of the company, whose
// access the dashboard gate already decided (bug hunt 2026-10-28 B36). App
// pages use dashboardAppOwner. Only the account-level views below keep the
// creator check: the gate does not cover them, because their URL names no
// app or names another one.
func TestDashboardAppPagesDoNotCheckOnlyTheCreator(t *testing.T) {
	allowed := map[string]int{
		"handlers_dashboard_async_invocations.go": 1, // an invocation's detail, by its ID
		"handlers_dashboard_jobs.go":              1, // the account's jobs page
		"handlers_dashboard_slo.go":               1, // SLO badges across the account's apps
		"handlers_dashboard_preview_destroy.go":   1, // the preview, not the gated parent app
	}
	files, err := filepath.Glob("handlers_dashboard*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Count(string(src), "app.AccountID != acct.ID"); got > allowed[file] {
			t.Errorf("%s compares the app's creator with the session account %d time(s); use dashboardAppOwner", file, got)
		}
	}
}
