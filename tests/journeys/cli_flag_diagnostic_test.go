package journeys

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

// GA row156: reject a typo, consume structured corrections, and discover the
// corrected flag without sending any request or submitting a deployment.
func TestCLIFlagCorrectionsIsolatedCLI(t *testing.T) {
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
	result := tg.exec(t, "fp_test_flag-corrections-journey", tg.home, time.Minute, "deploy", "--naem", "demo")
	var problem struct {
		Code     string
		ExitCode int `json:"exit_code"`
		CLI      struct {
			SuggestedFlags []string `json:"suggested_flags"`
			HelpArgv       []string `json:"help_argv"`
		}
	}
	if result.exit != 1 || len(result.stdout) != 0 || json.Unmarshal(result.stderr, &problem) != nil || problem.Code != "validation_failed" || problem.ExitCode != 1 || !slices.Equal(problem.CLI.SuggestedFlags, []string{"--name"}) || !slices.Equal(problem.CLI.HelpArgv, []string{"gregale", "--json", "help", "deploy"}) {
		t.Fatalf("typo correction: exit=%d stdout=%s stderr=%s", result.exit, result.stdout, result.stderr)
	}
	help := tg.exec(t, "fp_test_flag-corrections-journey", t.TempDir(), time.Minute, problem.CLI.HelpArgv[1:]...)
	var document struct {
		Flags []struct{ Spellings []string }
	}
	if help.exit != 0 || len(help.stderr) != 0 || json.Unmarshal(help.stdout, &document) != nil || !slices.ContainsFunc(document.Flags, func(f struct{ Spellings []string }) bool {
		return slices.Contains(f.Spellings, problem.CLI.SuggestedFlags[0])
	}) {
		t.Fatalf("correction cannot be discovered: exit=%d stdout=%s stderr=%s", help.exit, help.stdout, help.stderr)
	}
	if requests.Load() != 0 {
		t.Fatalf("correction/discovery made %d requests", requests.Load())
	}
}
