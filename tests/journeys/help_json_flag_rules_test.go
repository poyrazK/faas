package journeys

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type discoveredFlagCondition struct {
	Flag, Test string
	Value      bool
}
type discoveredFlagRule struct {
	ID, Kind       string
	When, Requires []discoveredFlagCondition
}

// GA row 156: discover a dependency offline, diagnose it before login, and
// satisfy the registered requirement before issuing one read-only request.
func TestJSONHelpFlagRulesIsolatedCLI(t *testing.T) {
	cli := os.Getenv("GREGALE_JOURNEY_TEST_CLI")
	if tgt != nil {
		cli = tgt.cli
	}
	if cli == "" {
		t.Skip("set GREGALE_JOURNEY_TEST_CLI or configure a journey target")
	}
	for _, kind := range []string{"build", "invocation"} {
		t.Run(kind, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				key, want := "items", "/v1/builds"
				if kind == "invocation" {
					key, want = "invocations", "/v1/invocations"
				}
				if r.Method != http.MethodGet || r.URL.Path != want {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
					http.NotFound(w, r)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{key: []any{}})
			}))
			t.Cleanup(server.Close)
			tg := &target{api: server.URL, cli: cli, home: t.TempDir(), appsDomain: "apps.example.test"}
			help := tg.exec(t, "", tg.home, time.Minute, "--profile", "missing-rule-profile", "help", kind, "list")
			var document struct {
				Schema int                  `json:"schema_version"`
				Rules  []discoveredFlagRule `json:"flag_rules"`
			}
			if help.exit != 0 || len(help.stderr) != 0 || json.Unmarshal(help.stdout, &document) != nil || document.Schema != 1 || len(document.Rules) != 3 || calls.Load() != 0 {
				t.Fatalf("offline help: %d %s %s calls=%d", help.exit, help.stdout, help.stderr, calls.Load())
			}
			for _, rule := range document.Rules {
				t.Run(rule.ID, func(t *testing.T) {
					path := filepath.Join(t.TempDir(), "private-rule-rows.jsonl")
					args := []string{kind, "list"}
					file := false
					progress := false
					for _, condition := range rule.When {
						if condition.Flag == "output" && condition.Test == "present" && condition.Value {
							args = append(args, "--output", path)
							file = true
						} else if condition.Flag == "progress" && condition.Test == "active" && condition.Value {
							args = append(args, "--progress", "ndjson")
							progress = true
						} else if condition.Test == "enabled" && condition.Value {
							args = append(args, "--"+condition.Flag)
						} else if condition.Flag != "output" || condition.Value {
							t.Fatalf("unrecognized condition: %+v", condition)
						}
					}
					before := calls.Load()
					res := tg.exec(t, "", tg.home, time.Minute, args...)
					var problem struct {
						Code, Category string
						Exit           int `json:"exit_code"`
						CLI            *struct {
							Kind     string
							Rule     discoveredFlagRule
							Missing  []discoveredFlagCondition
							HelpArgv []string `json:"help_argv"`
						}
					}
					if res.exit != 1 || len(res.stdout) != 0 || json.Unmarshal(res.stderr, &problem) != nil || problem.Exit != 1 || problem.Category != "invalid_request" || problem.CLI == nil || problem.CLI.Kind != "invalid_flag_combination" || !reflect.DeepEqual(problem.CLI.Rule, rule) || calls.Load() != before {
						t.Fatalf("dependency: %d %s %s calls=%d", res.exit, res.stdout, res.stderr, calls.Load())
					}
					wantCode := "validation_failed"
					if file || progress {
						wantCode = "invalid_request"
					}
					if problem.Code != wantCode {
						t.Fatal(problem.Code)
					}
					if strings.Contains(string(res.stderr), path) || !reflect.DeepEqual(problem.CLI.HelpArgv, []string{"gregale", "--json", "help", kind, "list"}) {
						t.Fatalf("unsafe help: %s", res.stderr)
					}
					wantMissing := []discoveredFlagCondition{{Flag: "all", Test: "enabled", Value: true}}
					if progress {
						wantMissing = append([]discoveredFlagCondition{{Flag: "output", Test: "present", Value: true}}, wantMissing...)
					}
					if !reflect.DeepEqual(problem.CLI.Missing, wantMissing) {
						t.Fatal(problem.CLI.Missing)
					}
					if _, err := os.Lstat(path); !os.IsNotExist(err) {
						t.Fatalf("invalid command published: %v", err)
					}
					advice := tg.exec(t, "", tg.home, time.Minute, problem.CLI.HelpArgv[1:]...)
					if advice.exit != 0 || len(advice.stderr) != 0 || !json.Valid(advice.stdout) || calls.Load() != before {
						t.Fatalf("offline advice: %d %s %s", advice.exit, advice.stdout, advice.stderr)
					}
					for _, requirement := range rule.Requires {
						if requirement.Flag == "output" && requirement.Test == "present" && requirement.Value {
							args = append(args, "--output", path)
							file = true
						} else if requirement.Test == "enabled" && requirement.Value {
							args = append(args, "--"+requirement.Flag)
						} else if requirement.Flag != "json" || requirement.Test != "effective" || !requirement.Value {
							t.Fatalf("unrecognized requirement: %+v", requirement)
						}
					}
					fixed := tg.exec(t, "fp_test_flag-rule-journey", tg.home, time.Minute, args...)
					if fixed.exit != 0 || (!progress && len(fixed.stderr) != 0) || calls.Load() != before+1 {
						t.Fatalf("repaired combination: %d %s %s", fixed.exit, fixed.stdout, fixed.stderr)
					}
					if progress {
						decoder := json.NewDecoder(bytes.NewReader(fixed.stderr))
						count := 0
						var last struct {
							Type, Event string
							Published   bool
						}
						for {
							var event struct {
								Type, Event string
								Published   bool
							}
							err := decoder.Decode(&event)
							if err == io.EOF {
								break
							}
							if err != nil || event.Type != "list_export_progress" {
								t.Fatalf("invalid progress: %s", fixed.stderr)
							}
							last = event
							count++
						}
						if count < 2 || last.Event != "published" || !last.Published {
							t.Fatalf("publication progress: %s", fixed.stderr)
						}
					}
					if file {
						var receipt struct {
							Published bool
							Format    string
						}
						data, err := os.ReadFile(path)
						if json.Unmarshal(fixed.stdout, &receipt) != nil || !receipt.Published || receipt.Format != "jsonl" || err != nil || len(data) != 0 {
							t.Fatalf("completed export: %s %q %v", fixed.stdout, data, err)
						}
					} else if len(fixed.stdout) != 0 {
						t.Fatalf("empty stream: %q", fixed.stdout)
					}
				})
			}
			before := calls.Load()
			disabled := tg.exec(t, "", tg.home, time.Minute, kind, "list", "--stream=false")
			var auth struct {
				CLI  json.RawMessage
				Code string
			}
			if disabled.exit != 2 || len(disabled.stdout) != 0 || json.Unmarshal(disabled.stderr, &auth) != nil || auth.CLI != nil || auth.Code != "unauthorized" || calls.Load() != before {
				t.Fatalf("disabled flag did not reach authentication: %d %s", disabled.exit, disabled.stderr)
			}
			if bytes.Contains(help.stdout, []byte(server.URL)) || bytes.Contains(help.stdout, []byte("missing-rule-profile")) {
				t.Fatal("help exposed connection values")
			}
		})
	}
}
