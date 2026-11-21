package journeys

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

// GA row 156: discover a command correction without replaying its input.
func TestCLICommandCorrectionsIsolatedCLI(t *testing.T) {
	cli := os.Getenv("GREGALE_JOURNEY_TEST_CLI")
	if tgt != nil {
		cli = tgt.cli
	}
	if cli == "" {
		t.Skip("set GREGALE_JOURNEY_TEST_CLI or configure a journey target")
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests.Add(1); w.WriteHeader(500) }))
	t.Cleanup(server.Close)
	tg := &target{api: server.URL, cli: cli, home: t.TempDir(), appsDomain: "apps.example.test"}
	for _, args := range [][]string{{"invocation", "lsit", "private-target"}, {"help", "invocation", "lsit"}} {
		result := tg.exec(t, "fp_test_command-corrections-journey", tg.home, time.Minute, args...)
		var problem struct {
			CLI struct {
				Kind              string
				SuggestedCommands [][]string `json:"suggested_commands"`
				HelpArgv          []string   `json:"help_argv"`
			}
		}
		if result.exit != 1 || len(result.stdout) != 0 || json.Unmarshal(result.stderr, &problem) != nil || len(problem.CLI.SuggestedCommands) != 1 || !reflect.DeepEqual(problem.CLI.HelpArgv, []string{"gregale", "--json", "help", "invocation", "list"}) {
			t.Fatalf("correction: exit=%d stdout=%s stderr=%s", result.exit, result.stdout, result.stderr)
		}
		want := []string{"gregale", "invocation", "list"}
		if args[0] == "help" {
			want = problem.CLI.HelpArgv
		}
		if !reflect.DeepEqual(problem.CLI.SuggestedCommands[0], want) {
			t.Fatalf("correction retained or invented input: %q", problem.CLI.SuggestedCommands)
		}
		help := tg.exec(t, "fp_test_command-corrections-journey", tg.home, time.Minute, problem.CLI.HelpArgv[1:]...)
		var document struct{ Command string }
		if help.exit != 0 || len(help.stderr) != 0 || json.Unmarshal(help.stdout, &document) != nil || document.Command != "gregale invocation list" {
			t.Fatalf("suggested discovery: exit=%d stdout=%s stderr=%s", help.exit, help.stdout, help.stderr)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("correction/discovery made %d requests", requests.Load())
	}
}
