package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// Issue #3362: `crons info|update|rm|runs` take a rule ID, so the human
// list must print it.
func TestCmdCronsListPrintsRuleID(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")
	const id = "50785d48-6bbf-4757-a07e-f209153f2c94"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]api.CronResponse{{ID: id, Schedule: "* * * * *", Path: "/cron", Enabled: true}})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	var out bytes.Buffer
	old := osStdout
	osStdout = &out
	defer func() { osStdout = old }()

	if code := cmdCrons([]string{"list", "--app", "my-app"}); code != 0 {
		t.Fatalf("crons list = %d, want 0", code)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "ID") || !strings.HasPrefix(lines[1], id) {
		t.Fatalf("crons list output = %q, want a header and a row starting with the rule ID", out.String())
	}
}
