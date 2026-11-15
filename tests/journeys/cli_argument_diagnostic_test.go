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

// GA row 156: discover missing and extra arguments without sending work.
func TestCLIArgumentCountsIsolatedCLI(t *testing.T) {
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
	for _, tc := range []struct {
		args, labels, help []string
		kind               string
		expected, received int
	}{
		{[]string{"invocation", "wait", "--timeout", "1s"}, []string{"ID"}, []string{"invocation", "wait"}, "missing_argument", 1, 0},
		{[]string{"account", "update", "--name", "private-name", "--", "private-value"}, []string{}, []string{"account", "update"}, "unexpected_argument", 0, 1},
	} {
		result := tg.exec(t, "fp_test_argument-counts-journey", tg.home, time.Minute, tc.args...)
		var problem struct {
			Code     string
			ExitCode int `json:"exit_code"`
			CLI      struct {
				Kind      string
				Arguments struct {
					Labels        []string
					ExpectedMin   int `json:"expected_min"`
					ExpectedMax   int `json:"expected_max"`
					ReceivedCount int `json:"received_count"`
				}
				HelpArgv []string `json:"help_argv"`
			}
		}
		if result.exit != 1 || len(result.stdout) != 0 || json.Unmarshal(result.stderr, &problem) != nil || problem.Code != "validation_failed" || problem.ExitCode != 1 || problem.CLI.Kind != tc.kind || !reflect.DeepEqual(problem.CLI.Arguments.Labels, tc.labels) || problem.CLI.Arguments.ExpectedMin != tc.expected || problem.CLI.Arguments.ExpectedMax != tc.expected || problem.CLI.Arguments.ReceivedCount != tc.received || !reflect.DeepEqual(problem.CLI.HelpArgv, append([]string{"gregale", "--json", "help"}, tc.help...)) {
			t.Fatalf("argument advice: exit=%d stdout=%s stderr=%s", result.exit, result.stdout, result.stderr)
		}
		help := tg.exec(t, "fp_test_argument-counts-journey", tg.home, time.Minute, problem.CLI.HelpArgv[1:]...)
		var document struct{ Command string }
		if help.exit != 0 || len(help.stderr) != 0 || json.Unmarshal(help.stdout, &document) != nil || document.Command == "" {
			t.Fatalf("help: exit=%d stdout=%s stderr=%s", help.exit, help.stdout, help.stderr)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("invalid arguments/help made %d requests", requests.Load())
	}
}
