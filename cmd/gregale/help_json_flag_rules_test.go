package main_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

func discoverListFlagRules(t *testing.T, topic ...string) []main.CLIFlagRule {
	t.Helper()
	out, stderr, restore := main.SwapIO(t)
	code := main.Run(append([]string{"--json", "help"}, topic...))
	text := stderr()
	restore()
	var document struct {
		SchemaVersion int                `json:"schema_version"`
		Rules         []main.CLIFlagRule `json:"flag_rules"`
	}
	if code != 0 || text != "" || json.Unmarshal(out.Bytes(), &document) != nil || document.SchemaVersion != 1 || len(document.Rules) != 2 {
		t.Fatalf("discovery: %d %s %s", code, out, text)
	}
	return document.Rules
}

func TestJSONHelpListFlagRulesOffline(t *testing.T) {
	setupJSONHelp(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	t.Cleanup(server.Close)
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_private-flag-rule")
	build := discoverListFlagRules(t, "build", "list")
	invocation := discoverListFlagRules(t, "invocation", "list")
	compat := discoverListFlagRules(t, "invocations", "list")
	if !reflect.DeepEqual(build, invocation) || !reflect.DeepEqual(invocation, compat) {
		t.Fatal("canonical/compatibility rules drifted")
	}
	data, _ := json.Marshal(build)
	registered, _ := json.Marshal(main.ListFlagRules())
	if !bytes.Equal(data, registered) {
		t.Fatalf("validation registry drift: %s != %s", data, registered)
	}
	out, stderr, restore := main.SwapIO(t)
	code := main.Run([]string{"--profile", "missing-private-rule-profile", "--debug-http", "--json", "help", "build", "list"})
	text := stderr()
	restore()
	if code != 0 || text != "" || !json.Valid(out.Bytes()) {
		t.Fatalf("offline: %d %s %s", code, out, text)
	}
	for _, private := range []string{server.URL, "fp_test_private-flag-rule", "missing-private-rule-profile"} {
		if strings.Contains(out.String(), private) {
			t.Fatal("discovery exposed configuration")
		}
	}
	for _, topic := range [][]string{nil, {"build"}, {"invocation"}, {"account"}} {
		out, stderr, restore := main.SwapIO(t)
		code := main.Run(append([]string{"--json", "help"}, topic...))
		text := stderr()
		restore()
		var doc map[string]json.RawMessage
		if code != 0 || text != "" || json.Unmarshal(out.Bytes(), &doc) != nil || doc["flag_rules"] != nil {
			t.Fatalf("unregistered rules: %q %d %s %s", topic, code, out, text)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("discovery made %d API requests", calls.Load())
	}
}

func TestListFlagRuleProblemsBeforeAuthentication(t *testing.T) {
	for _, kind := range []string{"build", "invocation"} {
		t.Run(kind, func(t *testing.T) {
			setupListStream(t)
			t.Setenv("FAAS_TOKEN", "")
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(500) }))
			t.Cleanup(server.Close)
			t.Setenv("FAAS_API", server.URL)
			for _, test := range []struct {
				name               string
				args               []string
				rule, code, detail string
				missing            int
			}{
				{"output", []string{"--output", "private-path-token"}, main.ListOutputRequiresAll, "invalid_request", "--output requires --all", 1},
				{"empty-presence", []string{"--output="}, main.ListOutputRequiresAll, "invalid_request", "--output requires --all", 1},
				{"false-path", []string{"--output=false"}, main.ListOutputRequiresAll, "invalid_request", "--output requires --all", 1},
				{"literal-flag-value", []string{"--output", "--all"}, main.ListOutputRequiresAll, "invalid_request", "--output requires --all", 1},
				{"stream", []string{"--stream"}, main.ListStreamRequiresAllJSON, "validation_failed", "--stream requires --json and --all", 1},
				{"last-false", []string{"--stream", "--all", "--all=false"}, main.ListStreamRequiresAllJSON, "validation_failed", "--stream requires --json and --all", 1},
				{"output-precedence", []string{"--stream", "--output", "private-path-token"}, main.ListOutputRequiresAll, "invalid_request", "--output requires --all", 1},
			} {
				t.Run(test.name, func(t *testing.T) {
					args := append([]string{"--json", kind, "list"}, test.args...)
					out, stderr, restore := main.SwapIO(t)
					code := main.Run(args)
					text := stderr()
					restore()
					var problem struct {
						Code, Category, Detail string
						Exit                   int                         `json:"exit_code"`
						CLI                    *main.CLIFlagRuleDiagnostic `json:"cli"`
						Pagination             json.RawMessage
					}
					decoder := json.NewDecoder(strings.NewReader(text))
					if code != 1 || out.Len() != 0 || decoder.Decode(&problem) != nil || decoder.Decode(new(any)) != io.EOF || problem.Code != test.code || problem.Category != "invalid_request" || problem.Exit != 1 || problem.Detail != test.detail || problem.CLI == nil || problem.CLI.Kind != "invalid_flag_combination" || problem.CLI.Rule.ID != test.rule || len(problem.CLI.Missing) != test.missing || problem.Pagination != nil {
						t.Fatalf("Problem: %d %s %s", code, out, text)
					}
					if strings.Contains(text, "private-path-token") || !reflect.DeepEqual(problem.CLI.HelpArgv, []string{"gregale", "--json", "help", kind, "list"}) {
						t.Fatalf("unsafe/wrong metadata: %s", text)
					}
					if problem.CLI.Missing[0].Flag != "all" || problem.CLI.Missing[0].Test != "enabled" || !problem.CLI.Missing[0].Value {
						t.Fatal(problem.CLI.Missing)
					}
					out, stderr, restore = main.SwapIO(t)
					helpCode := main.Run(problem.CLI.HelpArgv[1:])
					helpErr := stderr()
					restore()
					if helpCode != 0 || helpErr != "" || !json.Valid(out.Bytes()) {
						t.Fatalf("help advice: %d %s %s", helpCode, out, helpErr)
					}
				})
			}
			if calls.Load() != 0 {
				t.Fatalf("invalid combinations made %d requests", calls.Load())
			}
		})
	}
}

func TestListFlagRuleHumanOutputAndPrecedence(t *testing.T) {
	for _, kind := range []string{"build", "invocation"} {
		for _, output := range []bool{false, true} {
			t.Run(fmt.Sprint(kind, "/", output), func(t *testing.T) {
				setupListStream(t)
				t.Setenv("FAAS_TOKEN", "")
				var expected string
				_, stderr, restore := main.SwapIO(t)
				if output {
					main.PrintErr("Invalid list output", errors.New("--output requires --all"))
				} else {
					main.PrintUsage(*main.OsStderr, "--stream requires --json and --all", "cli")
				}
				expected = stderr()
				restore()
				args := []string{kind, "list", "--stream"}
				if output {
					args = append(args, "--output", "private-path-token")
				}
				out, stderr, restore := main.SwapIO(t)
				code := main.Run(args)
				text := stderr()
				restore()
				if code != 1 || out.Len() != 0 || text != expected {
					t.Fatalf("human output changed: %d %q want %q", code, text, expected)
				}
			})
		}
	}
}

func TestListFlagRuleValidCombinationsAndOtherErrors(t *testing.T) {
	for _, kind := range []string{"build", "invocation"} {
		t.Run(kind, func(t *testing.T) {
			setupListStream(t)
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != "GET" {
					t.Error(r.Method)
				}
				w.Header().Set("Content-Type", "application/problem+json")
				w.Header().Set("X-Faas-Request-Id", errorCorrelationID)
				w.WriteHeader(503)
				_, _ = io.WriteString(w, `{"status":503,"code":"service_unavailable","title":"Try later"}`)
			}))
			t.Cleanup(server.Close)
			t.Setenv("FAAS_API", server.URL)
			for _, test := range []struct {
				name        string
				flags       []string
				environment bool
			}{
				{"disabled", []string{"--stream=false"}, false},
				{"stream", []string{"--stream", "--all=false", "--all"}, false},
				{"environment", []string{"--stream", "--all"}, true},
				{"file", []string{"--all", "--output", "OUTPUT"}, false},
				{"redundant-stream", []string{"--all", "--stream", "--output", "OUTPUT"}, false},
			} {
				t.Run(test.name, func(t *testing.T) {
					before := calls.Load()
					path := filepath.Join(t.TempDir(), "rows.jsonl")
					args := []string{"--json", kind, "list"}
					if test.environment {
						t.Setenv("FAAS_JSON", "1")
						args = args[1:]
					}
					for _, flag := range test.flags {
						if flag == "OUTPUT" {
							flag = path
						}
						args = append(args, flag)
					}
					out, stderr, restore := main.SwapIO(t)
					code := main.Run(args)
					text := stderr()
					restore()
					var problem map[string]json.RawMessage
					if code != 3 || out.Len() != 0 || json.Unmarshal([]byte(text), &problem) != nil || problem["cli"] != nil || string(problem["code"]) != `"service_unavailable"` || string(problem["request_id"]) != `"`+errorCorrelationID+`"` || calls.Load() != before+1 {
						t.Fatalf("valid flags changed API routing: %d %s %s", code, out, text)
					}
					if _, err := os.Lstat(path); !os.IsNotExist(err) {
						t.Fatalf("failed read published: %v", err)
					}
				})
			}
			for _, flags := range [][]string{{"--all", "--output="}, {"--all", "--output", "-"}, {"--limit", "0", "--stream"}, {"--unknown-private-option"}, {"--stream", "--", "literal-private-target"}} {
				before := calls.Load()
				out, stderr, restore := main.SwapIO(t)
				code := main.Run(append([]string{"--json", kind, "list"}, flags...))
				text := stderr()
				restore()
				var problem struct{ CLI *struct{ Kind string } }
				if code != 1 || out.Len() != 0 || json.Unmarshal([]byte(text), &problem) != nil || (problem.CLI != nil && problem.CLI.Kind == "invalid_flag_combination") || calls.Load() != before {
					t.Fatalf("other validation acquired rule: %q %d %s", flags, code, text)
				}
			}
		})
	}
}

func TestListFlagRuleWrappedErrorsAndOfflineHelpFallback(t *testing.T) {
	setupListStream(t)
	previous := *main.JsonOutput
	*main.JsonOutput = true
	t.Cleanup(func() { *main.JsonOutput = previous })
	for _, command := range []string{"build list", "private-unknown-command"} {
		violation := main.ListFlagRuleViolation(main.ListOutputRequiresAll, main.ListFlagRuleState{Output: true}, command)
		out, stderr, restore := main.SwapIO(t)
		code := main.PrintErr("Invalid list output", fmt.Errorf("wrapped: %w", violation))
		text := stderr()
		restore()
		var problem struct{ CLI *main.CLIFlagRuleDiagnostic }
		want := []string{"gregale", "--json", "help"}
		if command == "build list" {
			want = append(want, "build", "list")
		}
		if code != 1 || out.Len() != 0 || json.Unmarshal([]byte(text), &problem) != nil || problem.CLI == nil || !reflect.DeepEqual(problem.CLI.HelpArgv, want) || strings.Contains(text, "private-unknown-command") {
			t.Fatalf("wrapped rule: %d %s %s", code, out, text)
		}
	}
}

func TestListFlagRuleHumanFileStillAcceptsStream(t *testing.T) {
	for _, kind := range []string{"build", "invocation"} {
		t.Run(kind, func(t *testing.T) {
			setupListStream(t)
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				key, want := "items", "/v1/builds"
				if kind == "invocation" {
					key, want = "invocations", "/v1/invocations"
				}
				if r.Method != "GET" || r.URL.Path != want {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{key: []any{}})
			}))
			t.Cleanup(server.Close)
			t.Setenv("FAAS_API", server.URL)
			path := filepath.Join(t.TempDir(), "empty.jsonl")
			out, stderr, restore := main.SwapIO(t)
			code := main.Run([]string{kind, "list", "--all", "--stream", "--output", path})
			text := stderr()
			restore()
			data, err := os.ReadFile(path)
			if code != 0 || text != "" || err != nil || len(data) != 0 || json.Valid(out.Bytes()) || !strings.Contains(out.String(), "Exported 0 items") || calls.Load() != 1 {
				t.Fatalf("human file combination changed: %d %s %s file=%q err=%v calls=%d", code, out, text, data, err, calls.Load())
			}
		})
	}
}
