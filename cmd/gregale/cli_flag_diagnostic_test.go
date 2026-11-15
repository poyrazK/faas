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

type cliDiagnosticView struct {
	Kind           string   `json:"kind"`
	Flag           string   `json:"flag"`
	SuggestedFlags []string `json:"suggested_flags"`
	ExpectedType   string   `json:"expected_type"`
	ValueMarker    string   `json:"value_marker"`
	AllowedValues  []string `json:"allowed_values"`
	Placement      string   `json:"placement"`
	HelpArgv       []string `json:"help_argv"`
}

// adr: 20261115082128 — parser corrections are structured, local and advisory.
func TestCLIFlagDiagnosticsJSONContracts(t *testing.T) {
	setupJSONHelp(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(500) }))
	t.Cleanup(server.Close)
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_flag-corrections-private")
	for _, tc := range []struct {
		name string
		args []string
		want cliDiagnosticView
	}{
		{"typo", []string{"deploy", "--naem", "private-value"}, cliDiagnosticView{Kind: "unknown_flag", SuggestedFlags: []string{"--name"}, HelpArgv: []string{"gregale", "--json", "help", "deploy"}}},
		{"integer", []string{"invocation", "list", "--limit", "private-value"}, cliDiagnosticView{Kind: "invalid_value", Flag: "--limit", ExpectedType: "integer", ValueMarker: "N", HelpArgv: []string{"gregale", "--json", "help", "invocation", "list"}}},
		{"missing", []string{"invocation", "list", "--limit"}, cliDiagnosticView{Kind: "missing_value", Flag: "--limit", ExpectedType: "integer", ValueMarker: "N", HelpArgv: []string{"gregale", "--json", "help", "invocation", "list"}}},
		{"duration", []string{"invocation", "wait", "--timeout", "private-value"}, cliDiagnosticView{Kind: "invalid_value", Flag: "--timeout", ExpectedType: "duration", ValueMarker: "D", HelpArgv: []string{"gregale", "--json", "help", "invocation", "wait"}}},
		{"boolean", []string{"logs", "--follow=private-value"}, cliDiagnosticView{Kind: "invalid_value", Flag: "--follow", ExpectedType: "boolean", HelpArgv: []string{"gregale", "--json", "help", "logs"}}},
		{"choices", []string{"app", "update", "--visibility"}, cliDiagnosticView{Kind: "missing_value", Flag: "--visibility", ExpectedType: "enum", ValueMarker: "public|internal", AllowedValues: []string{"public", "internal"}, HelpArgv: []string{"gregale", "--json", "help", "app", "update"}}},
		{"placement", []string{"deploy", "--non-interactive"}, cliDiagnosticView{Kind: "misplaced_global_flag", Flag: "--non-interactive", Placement: "before_command", HelpArgv: []string{"gregale", "--json", "help", "deploy"}}},
		{"org-placement", []string{"logs", "--org", "private-value"}, cliDiagnosticView{Kind: "misplaced_global_flag", Flag: "--org", Placement: "before_command", HelpArgv: []string{"gregale", "--json", "help", "logs"}}},
		{"local-profile", []string{"deploy", "--profile"}, cliDiagnosticView{Kind: "missing_value", Flag: "--profile", ExpectedType: "value", ValueMarker: "SIZE", HelpArgv: []string{"gregale", "--json", "help", "deploy"}}},
		{"unknown-private", []string{"deploy", "--fp_test_flag-corrections-private"}, cliDiagnosticView{Kind: "unknown_flag", HelpArgv: []string{"gregale", "--json", "help", "deploy"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, stderr, restore := main.SwapIO(t)
			code := main.Run(append([]string{"--json"}, tc.args...))
			text := stderr()
			restore()
			var problem struct {
				Code     string
				ExitCode int                `json:"exit_code"`
				CLI      *cliDiagnosticView `json:"cli"`
			}
			decoder := json.NewDecoder(strings.NewReader(text))
			if code != 1 || out.Len() != 0 || decoder.Decode(&problem) != nil || decoder.Decode(new(any)) != io.EOF || problem.Code != "invalid_request" || problem.ExitCode != 1 || problem.CLI == nil {
				t.Fatalf("parser contract: exit=%d stdout=%s stderr=%s", code, out, text)
			}
			if !reflect.DeepEqual(*problem.CLI, tc.want) {
				t.Fatalf("diagnostic got %+v want %+v; stderr=%s", *problem.CLI, tc.want, text)
			}
			metadata, err := json.Marshal(problem.CLI)
			if err != nil {
				t.Fatal(err)
			}
			for _, private := range []string{"private-value", "fp_test_flag-corrections-private", server.URL} {
				if strings.Contains(string(metadata), private) {
					t.Fatal("correction metadata exposed invocation input")
				}
			}
			// Follow the returned argv verbatim; help must remain local and valid.
			readJSONHelp(t, problem.CLI.HelpArgv[1:]...)
		})
	}
	if requests.Load() != 0 {
		t.Fatalf("invalid flags or suggested help made %d requests", requests.Load())
	}
}

func TestCLIFlagDiagnosticsKeepHumanOutput(t *testing.T) {
	setupJSONHelp(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"deploy", "--naem", "demo"}, "Invalid command flags\n  unknown flag --naem\n  Did you mean --name?\n  run 'gregale deploy --help' for usage\n"},
		{[]string{"logs", "--profile", "staging"}, "Invalid command flags\n  unknown flag --profile\n  --profile is a global flag: put it before the command, as in 'gregale --profile NAME logs'\n  run 'gregale logs --help' for usage\n"},
	} {
		out, stderr, restore := main.SwapIO(t)
		code := main.Run(tc.args)
		text := stderr()
		restore()
		if code != 1 || out.Len() != 0 || text != tc.want {
			t.Fatalf("human diagnostics changed: exit=%d stdout=%s stderr=%q want=%q", code, out, text, tc.want)
		}
	}
}

func TestCLIFlagDiagnosticsStayOutOfAPIErrors(t *testing.T) {
	setupJSONHelp(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"status":503,"code":"service_unavailable","title":"Try later"}`)
	}))
	t.Cleanup(server.Close)
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_flag-corrections-private")
	out, stderr, restore := main.SwapIO(t)
	code := main.Run([]string{"--json", "invocation", "list"})
	text := stderr()
	restore()
	var p map[string]json.RawMessage
	if code != 3 || out.Len() != 0 || json.Unmarshal([]byte(text), &p) != nil || p["cli"] != nil || string(p["code"]) != `"service_unavailable"` {
		t.Fatalf("API Problem changed: exit=%d stdout=%s stderr=%s", code, out, text)
	}
}
