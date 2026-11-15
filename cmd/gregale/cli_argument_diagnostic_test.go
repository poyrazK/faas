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

type argumentCountView struct {
	Labels        []string `json:"labels"`
	ExpectedMin   int      `json:"expected_min"`
	ExpectedMax   int      `json:"expected_max"`
	ReceivedCount int      `json:"received_count"`
}

// adr: 20261115103154 — explicit parser arity, never supplied argument values.
func TestCLIArgumentCountDiagnostics(t *testing.T) {
	setupJSONHelp(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests.Add(1); w.WriteHeader(500) }))
	t.Cleanup(server.Close)
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_argument-private")
	for _, tc := range []struct {
		name       string
		args, help []string
		want       argumentCountView
	}{
		{"missing get", []string{"invocation", "get"}, []string{"invocation", "get"}, argumentCountView{[]string{"ID"}, 1, 1, 0}},
		{"missing wait", []string{"invocation", "wait"}, []string{"invocation", "wait"}, argumentCountView{[]string{"ID"}, 1, 1, 0}},
		{"flag values are not positionals", []string{"invocation", "wait", "--timeout", "10s", "--interval", "1s"}, []string{"invocation", "wait"}, argumentCountView{[]string{"ID"}, 1, 1, 0}},
		{"extra get", []string{"invocation", "get", "private-one", "private-two"}, []string{"invocation", "get"}, argumentCountView{[]string{"ID"}, 1, 1, 2}},
		{"extra wait", []string{"invocation", "wait", "private-one", "private-two", "--timeout", "10s"}, []string{"invocation", "wait"}, argumentCountView{[]string{"ID"}, 1, 1, 2}},
		{"literal get", []string{"invocation", "get", "--", "--private-value", "private-two"}, []string{"invocation", "get"}, argumentCountView{[]string{"ID"}, 1, 1, 2}},
		{"literal wait", []string{"invocation", "wait", "--", "--private-value", "private-two"}, []string{"invocation", "wait"}, argumentCountView{[]string{"ID"}, 1, 1, 2}},
		{"compatibility alias", []string{"invocations", "wait"}, []string{"invocation", "wait"}, argumentCountView{[]string{"ID"}, 1, 1, 0}},
		{"zero arguments", []string{"account", "sessions", "list", "--", "private-one", "private-two"}, []string{"account", "sessions", "list"}, argumentCountView{[]string{}, 0, 0, 2}},
		{"flag-only leaf", []string{"account", "update", "--name", "private-name", "--", "private-value"}, []string{"account", "update"}, argumentCountView{[]string{}, 0, 0, 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, fromEnvironment := range []bool{false, true} {
				t.Setenv("FAAS_JSON", "0")
				args := append([]string(nil), tc.args...)
				if fromEnvironment {
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
					CLI            *struct {
						Kind      string
						Arguments *argumentCountView
						HelpArgv  []string `json:"help_argv"`
					}
				}
				if code != 1 || out.Len() != 0 || json.Unmarshal([]byte(text), &problem) != nil || problem.Code != "validation_failed" || problem.Category != "invalid_request" || problem.Status != 400 || problem.ExitCode != 1 || problem.CLI == nil || problem.CLI.Arguments == nil {
					t.Fatalf("exit=%d stdout=%s stderr=%s", code, out, text)
				}
				kind := "unexpected_argument"
				if tc.want.ReceivedCount < tc.want.ExpectedMin {
					kind = "missing_argument"
				}
				wantHelp := append([]string{"gregale", "--json", "help"}, tc.help...)
				if problem.CLI.Kind != kind || !reflect.DeepEqual(*problem.CLI.Arguments, tc.want) || !reflect.DeepEqual(problem.CLI.HelpArgv, wantHelp) {
					t.Fatalf("advice=%+v counts=%+v want=%+v help=%q", problem.CLI, problem.CLI.Arguments, tc.want, wantHelp)
				}
				metadata, err := json.Marshal(problem.CLI)
				if err != nil {
					t.Fatal(err)
				}
				for _, private := range []string{"private-", "fp_test_argument-private", server.URL} {
					if strings.Contains(string(metadata), private) {
						t.Fatalf("argument metadata retained supplied input: %s", metadata)
					}
				}
				readJSONHelp(t, problem.CLI.HelpArgv[1:]...)
			}
		})
	}
	if requests.Load() != 0 {
		t.Fatalf("invalid counts/discovery made %d requests", requests.Load())
	}
}

func TestCLIArgumentCountsKeepHumanUsage(t *testing.T) {
	setupJSONHelp(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"invocation", "get"}, "usage: gregale invocation get [--json] [--replay|--replay-keyed] <id>\n  Docs: https://gregale.dev/docs/cli\n"},
		{[]string{"invocation", "wait"}, "usage: gregale invocation wait [--json] [--timeout D] [--interval D] <id>\n  Docs: https://gregale.dev/docs/cli\n"},
		{[]string{"account", "sessions", "list", "extra"}, "usage: gregale account sessions list\n  Docs: https://gregale.dev/docs/cli\n"},
		{[]string{"account", "update", "--name", "Demo", "extra"}, "usage: gregale account update [flags]; unexpected positional argument(s): extra\n  Docs: https://gregale.dev/docs/cli\n"},
	} {
		out, stderr, restore := main.SwapIO(t)
		code := main.Run(tc.args)
		text := stderr()
		restore()
		if code != 1 || out.Len() != 0 || text != tc.want {
			t.Fatalf("human usage changed: exit=%d stdout=%s stderr=%q want=%q", code, out, text, tc.want)
		}
	}
}

func TestCLIArgumentCountsPreserveOtherValidation(t *testing.T) {
	setupJSONHelp(t)
	for _, args := range [][]string{
		{"invocation", "get", "--replay", "--replay-keyed", "private-id"},
		{"invocation", "wait", "--timeout=-1s", "private-id"},
		{"invocation", "get", "--unknown-flag"},
	} {
		out, stderr, restore := main.SwapIO(t)
		code := main.Run(append([]string{"--json"}, args...))
		text := stderr()
		restore()
		var problem struct {
			CLI *struct{ Arguments json.RawMessage }
		}
		if code != 1 || out.Len() != 0 || json.Unmarshal([]byte(text), &problem) != nil || (problem.CLI != nil && problem.CLI.Arguments != nil) {
			t.Fatalf("non-arity validation acquired counts: exit=%d stdout=%s stderr=%s", code, out, text)
		}
	}
}

func TestCLIValidArgumentCountsReachAPI(t *testing.T) {
	setupJSONHelp(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/problem+json")
		w.Header().Set("X-Faas-Request-Id", errorCorrelationID)
		w.WriteHeader(503)
		_, _ = io.WriteString(w, `{"status":503,"code":"service_unavailable","title":"Try later"}`)
	}))
	t.Cleanup(server.Close)
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_argument-private")
	for _, args := range [][]string{
		{"invocation", "get", "--", "00000000-0000-4000-8000-000000000001"},
		{"invocation", "wait", "--timeout", "1s", "--", "00000000-0000-4000-8000-000000000001"},
		{"account", "sessions", "list"},
	} {
		before := requests.Load()
		out, stderr, restore := main.SwapIO(t)
		code := main.Run(append([]string{"--json"}, args...))
		text := stderr()
		restore()
		var problem map[string]json.RawMessage
		if code != 3 || out.Len() != 0 || requests.Load() != before+1 || json.Unmarshal([]byte(text), &problem) != nil || problem["cli"] != nil || string(problem["code"]) != `"service_unavailable"` || string(problem["request_id"]) != `"`+errorCorrelationID+`"` {
			t.Fatalf("valid arity changed API contract: exit=%d requests=%d stdout=%s stderr=%s", code, requests.Load()-before, out, text)
		}
	}
}
