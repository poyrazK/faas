package journeys

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

// GA row 156: discover the installed CLI and resolve a compatibility path
// without authentication or requests, even when a connection is selected.
func TestJSONHelpIsolatedCLI(t *testing.T) {
	cli := os.Getenv("GREGALE_JOURNEY_TEST_CLI")
	if tgt != nil {
		cli = tgt.cli
	}
	if cli == "" {
		t.Skip("set GREGALE_JOURNEY_TEST_CLI or configure a journey target")
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	tg := &target{api: server.URL, cli: cli, home: t.TempDir(), appsDomain: "apps.example.test"}
	type document struct {
		Version     int    `json:"schema_version"`
		Command     string `json:"command"`
		Requested   string `json:"requested_command"`
		Aliases     []string
		Positionals []string
		Flags       []struct {
			Name       string
			TakesValue bool `json:"takes_value"`
		}
		Subcommands []struct{ Command string }
	}
	read := func(args ...string) document {
		t.Helper()
		result := tg.exec(t, "fp_test_help-private", tg.home, time.Minute, args...)
		var d document
		if result.exit != 0 || len(result.stderr) != 0 || json.Unmarshal(result.stdout, &d) != nil || d.Version != 1 {
			t.Fatalf("help %q: exit=%d stdout=%s stderr=%s", args, result.exit, result.stdout, result.stderr)
		}
		return d
	}
	root := read("--non-interactive", "help")
	if root.Command != "gregale" || !slices.ContainsFunc(root.Subcommands, func(c struct{ Command string }) bool { return c.Command == "gregale invocation" }) {
		t.Fatalf("root lacks canonical invocation family: %+v", root)
	}
	family := read("help", "invocation")
	if !slices.ContainsFunc(family.Subcommands, func(c struct{ Command string }) bool { return c.Command == "gregale invocation run" }) {
		t.Fatal("root discovery cannot reach invocation submission")
	}
	leaf := read("--profile", "missing", "--debug-http", "help", "invocation", "run")
	alias := read("help", "invoke")
	if leaf.Command != "gregale invocation run" || alias.Command != leaf.Command || alias.Requested != "gregale invoke" || !slices.Contains(leaf.Aliases, "gregale invoke") || len(leaf.Positionals) == 0 {
		t.Fatalf("compatibility discovery lost canonical metadata: leaf=%+v alias=%+v", leaf, alias)
	}
	if !slices.ContainsFunc(leaf.Flags, func(f struct {
		Name       string
		TakesValue bool `json:"takes_value"`
	}) bool {
		return f.Name == "payload" && f.TakesValue
	}) {
		t.Fatal("agent cannot discover how to supply an invocation payload")
	}
	invalid := tg.exec(t, "fp_test_help-private", tg.home, time.Minute, "help", "fp_test_help-private")
	var problem struct {
		Code string `json:"code"`
		Exit int    `json:"exit_code"`
	}
	if invalid.exit != 1 || len(invalid.stdout) != 0 || json.Unmarshal(invalid.stderr, &problem) != nil || problem.Code != "invalid_request" || problem.Exit != 1 || bytes.Contains(invalid.stderr, []byte("fp_test_help-private")) {
		t.Fatalf("invalid help failed its JSON contract: exit=%d stderr=%s", invalid.exit, invalid.stderr)
	}
	if requests.Load() != 0 {
		t.Fatalf("help made %d API requests", requests.Load())
	}
}
