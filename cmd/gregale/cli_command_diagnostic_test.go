package main_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

type commandDiagnosticView struct {
	Kind              string     `json:"kind"`
	SuggestedCommands [][]string `json:"suggested_commands"`
	HelpArgv          []string   `json:"help_argv"`
}

// adr: 20261115100510 — corrections are advisory command paths, never replayed argv.
func TestCLICommandCorrectionsJSON(t *testing.T) {
	setupJSONHelp(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests.Add(1); w.WriteHeader(500) }))
	t.Cleanup(server.Close)
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_command-corrections-private")
	for _, tc := range []struct {
		name, code, kind string
		args, path       []string
		correction       []string
	}{
		{"root", "invalid_request", "unknown_command", []string{"deplyo", "private-target", "--secret=private-value"}, []string{"deploy"}, []string{"gregale", "deploy"}},
		{"nested", "validation_failed", "unknown_command", []string{"invocation", "lsit", "private-target"}, []string{"invocation", "list"}, []string{"gregale", "invocation", "list"}},
		{"legacy alias", "validation_failed", "unknown_command", []string{"invocations", "lsit"}, []string{"invocation", "list"}, []string{"gregale", "invocation", "list"}},
		{"shared nested renderer", "validation_failed", "unknown_command", []string{"config", "lsit"}, []string{"config", "list"}, []string{"gregale", "config", "list"}},
		{"deep shared renderer", "validation_failed", "unknown_command", []string{"account", "sessions", "lsit"}, []string{"account", "sessions", "list"}, []string{"gregale", "account", "sessions", "list"}},
		{"help root", "invalid_request", "unknown_help_topic", []string{"help", "invocaton"}, []string{"invocation"}, []string{"gregale", "--json", "help", "invocation"}},
		{"help nested", "invalid_request", "unknown_help_topic", []string{"help", "invocation", "lsit"}, []string{"invocation", "list"}, []string{"gregale", "--json", "help", "invocation", "list"}},
		{"help alias", "invalid_request", "unknown_help_topic", []string{"help", "invocations", "lsit"}, []string{"invocation", "list"}, []string{"gregale", "--json", "help", "invocation", "list"}},
		{"ambiguous", "invalid_request", "unknown_command", []string{"lo"}, nil, nil},
		{"unrelated", "invalid_request", "unknown_command", []string{"zzzzzzzzzzzz"}, nil, nil},
		{"hidden", "invalid_request", "unknown_command", []string{"eventt"}, nil, nil},
		{"unknown nested", "validation_failed", "unknown_command", []string{"invocation", "zzzzzzzzzzzz"}, []string{"invocation"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, environment := range []bool{false, true} {
				args := append([]string(nil), tc.args...)
				t.Setenv("FAAS_JSON", "0")
				if environment {
					t.Setenv("FAAS_JSON", "1")
				} else {
					args = append([]string{"--json"}, args...)
				}
				out, stderr, restore := main.SwapIO(t)
				code := main.Run(args)
				text := stderr()
				restore()
				var problem struct {
					Code, Category string
					Status         int
					ExitCode       int `json:"exit_code"`
					CLI            *commandDiagnosticView
				}
				if code != 1 || out.Len() != 0 || json.Unmarshal([]byte(text), &problem) != nil || problem.Code != tc.code || problem.Category != "invalid_request" || problem.Status != 400 || problem.ExitCode != 1 || problem.CLI == nil {
					t.Fatalf("exit=%d stdout=%s stderr=%s", code, out, text)
				}
				wantHelp := append([]string{"gregale", "--json", "help"}, tc.path...)
				var wantCorrections [][]string
				if tc.correction != nil {
					wantCorrections = [][]string{tc.correction}
				}
				if problem.CLI.Kind != tc.kind || !reflect.DeepEqual(problem.CLI.SuggestedCommands, wantCorrections) || !reflect.DeepEqual(problem.CLI.HelpArgv, wantHelp) {
					t.Fatalf("advice=%+v want kind=%s commands=%q help=%q", problem.CLI, tc.kind, wantCorrections, wantHelp)
				}
				metadata, err := json.Marshal(problem.CLI)
				if err != nil {
					t.Fatal(err)
				}
				for _, private := range []string{"private-target", "private-value", "fp_test_command-corrections-private", server.URL} {
					if strings.Contains(string(metadata), private) {
						t.Fatalf("advice contains invocation input: %s", metadata)
					}
				}
				readJSONHelp(t, problem.CLI.HelpArgv[1:]...)
			}
		})
	}
	if requests.Load() != 0 {
		t.Fatalf("correction or discovery made %d requests", requests.Load())
	}
}

func TestCLICommandCorrectionsKeepHumanOutput(t *testing.T) {
	setupJSONHelp(t)
	previousTTY := *main.TestOnlyTTY
	off := false
	*main.TestOnlyTTY = &off
	t.Cleanup(func() { *main.TestOnlyTTY = previousTTY })
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"deplyo"}, "Unknown command\n  gregale: unknown command \"deplyo\"; did you mean 'gregale deploy'?\n"},
		{[]string{"invocation", "lsit"}, "unknown invocations subcommand \"lsit\"\n  Did you mean \"list\"?\n"},
		{[]string{"help", "invocaton"}, "unknown help topic: invocaton; did you mean 'gregale help invocation'?\n  Docs: https://gregale.dev/docs/cli\n"},
	} {
		out, stderr, restore := main.SwapIO(t)
		code := main.Run(tc.args)
		text := stderr()
		restore()
		if code != 1 || out.Len() != 0 || text != tc.want {
			t.Fatalf("human output changed: exit=%d stdout=%s stderr=%q want=%q", code, out, text, tc.want)
		}
	}
}

func TestCLICommandCorrectionsOmitAPIErrors(t *testing.T) {
	setupJSONHelp(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.Header().Set("X-Faas-Request-Id", errorCorrelationID)
		w.WriteHeader(503)
		_, _ = io.WriteString(w, `{"status":503,"code":"service_unavailable","title":"Try later"}`)
	}))
	t.Cleanup(server.Close)
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_command-corrections-private")
	out, stderr, restore := main.SwapIO(t)
	code := main.Run([]string{"--json", "invocation", "list"})
	text := stderr()
	restore()
	var problem map[string]json.RawMessage
	if code != 3 || out.Len() != 0 || json.Unmarshal([]byte(text), &problem) != nil || problem["cli"] != nil || string(problem["code"]) != `"service_unavailable"` || string(problem["request_id"]) != `"`+errorCorrelationID+`"` {
		t.Fatalf("API contract changed: exit=%d stdout=%s stderr=%s", code, out, text)
	}
}
