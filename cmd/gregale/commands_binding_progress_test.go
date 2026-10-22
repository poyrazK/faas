package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bindingrun"
)

func TestBindingProgressCLI(t *testing.T) {
	raw, err := os.ReadFile("../../sdk/binding-runner-tests/creation.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, graph := range []bool{false, true} {
		for _, mode := range []string{"auto", "plain", "ndjson", "off"} {
			for _, stop := range []bool{false, true} {
				t.Run(mode+map[bool]string{true: "/graph", false: "/app"}[graph]+map[bool]string{true: "/stop", false: "/pass"}[stop], func(t *testing.T) {
					var f struct {
						App   api.BindingVerificationRun        `json:"app_run"`
						Graph api.ProjectBindingVerificationRun `json:"graph_run"`
					}
					if err := json.Unmarshal(raw, &f); err != nil {
						t.Fatal(err)
					}
					created := time.Now().UTC().Add(-time.Second)
					expiry := created.Add(time.Minute)
					update := func(r api.BindingVerificationRun) api.BindingVerificationRun {
						r.CreatedAt = created
						r.ExpiresAt = expiry
						r.Status = "pending"
						for i := range r.Steps {
							r.Steps[i].Plan.Action = "observe"
							r.Steps[i].ResourceID = ""
							r.Steps[i].ResourceKind = ""
							r.Steps[i].Owned = false
							r.Steps[i].Status = "planned"
						}
						return r
					}
					f.App = update(f.App)
					f.Graph.CreatedAt = created
					f.Graph.ExpiresAt = expiry
					f.Graph.Status = "pending"
					for n, m := range f.Graph.Members {
						f.Graph.Members[n] = update(m)
					}
					id, app := f.App.ID, f.App.App
					if graph {
						id = f.Graph.ID
						for n := range f.Graph.Members {
							app = n
							break
						}
					}
					completes, reads, writes := 0, 0, 0
					q := &api.BindingVerificationQualification{Conditions: []api.BindingVerificationQualificationCondition{{App: app, Code: "runtime_updating", Scope: "production"}}}
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						if strings.HasSuffix(r.URL.Path, "/diagnostics") {
							w.WriteHeader(404)
							return
						}
						if r.Method == http.MethodDelete {
							writes++
							w.WriteHeader(500)
							return
						}
						switch {
						case strings.HasSuffix(r.URL.Path, "/resume"):
						case strings.HasSuffix(r.URL.Path, "/complete"):
							completes++
							if completes == 1 {
								q.CheckedAt = time.Now().UTC()
								f.App.Qualification = q
								f.Graph.Qualification = q
								w.WriteHeader(202)
							} else {
								if stop {
									w.WriteHeader(409)
									_ = json.NewEncoder(w).Encode(api.Problem{Status: 409, Code: "conflict"})
									return
								}
								f.App.Status = "passed"
								f.Graph.Status = "passed"
								for n, m := range f.Graph.Members {
									m.Status = "passed"
									f.Graph.Members[n] = m
								}
								w.WriteHeader(503)
								_ = json.NewEncoder(w).Encode(api.Problem{Status: 503})
								return
							}
						case r.Method == http.MethodGet:
							reads++
							f.App.Qualification = nil
							f.Graph.Qualification = nil
							if reads == 1 {
								w.WriteHeader(503)
								_ = json.NewEncoder(w).Encode(api.Problem{Status: 503})
								return
							}
						default:
							writes++
							t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
							w.WriteHeader(500)
							return
						}
						var data []byte
						if graph {
							data, _ = json.Marshal(f.Graph)
						} else {
							data, _ = json.Marshal(f.App)
						}
						var body map[string]json.RawMessage
						_ = json.Unmarshal(data, &body)
						body["private_output"] = json.RawMessage(`"hidden-progress-secret"`)
						_ = json.NewEncoder(w).Encode(body)
					}))
					defer server.Close()
					args := []string{"bindings", "verification-runs", "resume", f.App.App, id}
					if graph {
						args = []string{"projects", "environments", "release-sets", "verification-runs", "resume", f.Graph.Project, f.Graph.Environment, id}
					}
					args = append(args, "--json", "--progress", mode, "--status-read-retries", "1", "--poll-interval", "1ms")
					code, out, stderr := main.CaptureBindingCLI(t, server.URL, args...)
					want := 0
					if stop {
						want = 1
					}
					if code != want || completes != 2 || writes != 0 {
						t.Fatalf("code=%d completes=%d writes=%d %s %s", code, completes, writes, out, stderr)
					}
					decoder := json.NewDecoder(strings.NewReader(out))
					var result map[string]json.RawMessage
					if err := decoder.Decode(&result); err != nil {
						t.Fatalf("stdout: %v %s", err, out)
					}
					var extra any
					if !errors.Is(decoder.Decode(&extra), io.EOF) {
						t.Fatal("stdout was not one final JSON result")
					}
					if strings.Contains(out+stderr, "hidden-progress-secret") {
						t.Fatal("unknown private field leaked")
					}
					if mode == "off" || mode == "auto" {
						if stderr != "" {
							t.Fatalf("disabled progress: %s", stderr)
						}
					}
					if mode == "plain" && !strings.Contains(stderr, "waiting_for_qualification") {
						t.Fatalf("missing waiting progress: %s", stderr)
					}
					if mode == "ndjson" {
						statuses := map[string]bool{}
						for _, line := range strings.Split(strings.TrimSpace(stderr), "\n") {
							var event struct {
								Type, Status, Operation, Project, Environment string
								RunID                                         string `json:"run_id"`
							}
							if err := json.Unmarshal([]byte(line), &event); err != nil {
								t.Fatalf("non-JSON stderr: %s", line)
							}
							if event.Type != "binding_verification_progress" || event.RunID != id {
								t.Fatalf("unexpected event %s", line)
							}
							statuses[event.Status] = true
							if graph && (event.Project != f.Graph.Project || event.Environment != f.Graph.Environment) {
								t.Fatalf("lost graph selection: %s", line)
							}
							if event.Status == "reconciling" && event.Operation != "completion" {
								t.Fatal("lost reconciliation operation")
							}
						}
						for _, status := range []string{"waiting_for_qualification", "retrying"} {
							if !statuses[status] {
								t.Fatalf("missing %s: %s", status, stderr)
							}
						}
						if !stop && (!statuses["reconciling"] || !statuses["passed"]) {
							t.Fatalf("missing recovery progress: %s", stderr)
						}
					}
					if stop {
						var recovery main.BindingRecoverySummary
						if err := json.Unmarshal(result["recovery"], &recovery); err != nil {
							t.Fatal(err)
						}
						if recovery.RunID != id || recovery.LastQualification == nil || len(recovery.LastQualification.Conditions) != 1 || recovery.LastQualification.Conditions[0].Code != "runtime_updating" || !strings.Contains(recovery.StatusCommand, id) || !strings.Contains(recovery.DiagnosticsCommand, id) {
							t.Fatalf("missing stop context: %+v", recovery)
						}
					} else if result["recovery"] != nil {
						t.Fatal("success carried a stop summary")
					}
				})
			}
		}
	}
}

func TestBindingProgressDeduplicatesAndRetainsObservations(t *testing.T) {
	oldErr, oldJSON := (*main.OsStderr), (*main.JsonOutput)
	defer func() { (*main.OsStderr), (*main.JsonOutput) = oldErr, oldJSON }()
	var output bytes.Buffer
	(*main.OsStderr) = &output
	(*main.JsonOutput) = false
	view := main.NewBindingProgress(context.Background(), "receipt", "api", "", "")
	q := &api.BindingVerificationQualification{CheckedAt: time.Now(), Conditions: []api.BindingVerificationQualificationCondition{{App: "api", Code: "runtime_updating", Name: "escape\n\x1b", Scope: "production"}}}
	p := bindingrun.Progress{RunID: "receipt", Step: -1, Status: "waiting_for_qualification", Qualification: q}
	_ = view.Observe(p)
	first := output.String()
	q.CheckedAt = q.CheckedAt.Add(time.Second)
	_ = view.Observe(p)
	if output.String() != first {
		t.Fatal("observation clock alone repeated progress")
	}
	if strings.Contains(first, "\x1b") || !strings.Contains(first, `escape\n\x1b`) {
		t.Fatalf("unsafe selector: %q", first)
	}
	q.Conditions[0].Code = "runtime_stale"
	_ = view.Observe(p)
	if output.String() == first {
		t.Fatal("changed condition was suppressed")
	}
	_ = view.Observe(bindingrun.Progress{RunID: "receipt", Step: -1, Status: "pending"})
	q.Conditions[0].Code = "mutated"
	if view.Recovery().LastQualification.Conditions[0].Code != "runtime_stale" {
		t.Fatal("last waiting observation was not detached")
	}
}

func TestBindingProgressFlagsRequireExecution(t *testing.T) {
	for _, args := range [][]string{
		{"bindings", "verify", "api", "--release-policy", "--progress", "plain"},
		{"bindings", "verify", "api", "--release-policy", "--durable", "--dry-run", "--progress", "plain"},
		{"bindings", "verification-runs", "status", "api", "11111111-1111-4111-8111-111111111111", "--progress", "off"},
		{"projects", "environments", "release-sets", "verification-runs", "cancel", "project", "production", "11111111-1111-4111-8111-111111111111", "--progress", "plain"},
		{"projects", "environments", "release-sets", "verify", "project", "production", "--file", "missing.json", "--progress", "ndjson"},
		{"bindings", "verification-runs", "resume", "api", "11111111-1111-4111-8111-111111111111", "--progress", "invalid"},
	} {
		code, _, _ := main.CaptureBindingCLI(t, "http://127.0.0.1:1", args...)
		if code != 1 {
			t.Fatalf("invalid progress flags accepted: %v (%d)", args, code)
		}
	}
}

type brokenBindingProgressWriter struct{}

func (brokenBindingProgressWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func TestBindingProgressOutputIsBestEffort(t *testing.T) {
	oldErr, oldJSON := (*main.OsStderr), (*main.JsonOutput)
	defer func() { (*main.OsStderr), (*main.JsonOutput) = oldErr, oldJSON }()
	(*main.OsStderr) = brokenBindingProgressWriter{}
	(*main.JsonOutput) = false
	for _, mode := range []string{"plain", "ndjson", "off"} {
		view := main.NewBindingProgress(main.WithBindingProgress(t.Context(), mode), "receipt", "api", "", "")
		if err := view.Observe(bindingrun.Progress{RunID: "receipt", Step: -1, Status: "reconciling", Operation: "completion"}); err != nil {
			t.Fatal(err)
		}
		_ = view.Observe(bindingrun.Progress{RunID: "receipt", Step: -1, Status: "retrying", RetryAttempt: 1})
		if view.Recovery().LastOperation != "completion" {
			t.Fatal("retry lost the operation being reconciled")
		}
	}
}
func TestBindingProgressHumanDefaultsAndStructuredStop(t *testing.T) {
	oldErr, oldOut, oldJSON := (*main.OsStderr), (*main.OsStdout), (*main.JsonOutput)
	defer func() { (*main.OsStderr), (*main.OsStdout), (*main.JsonOutput) = oldErr, oldOut, oldJSON }()
	var output, stderr bytes.Buffer
	(*main.OsStdout), (*main.OsStderr) = &output, &stderr
	(*main.JsonOutput) = false
	auto := main.NewBindingProgress(t.Context(), "receipt", "api", "", "")
	_ = auto.Observe(bindingrun.Progress{RunID: "receipt", Step: -1, Status: "pending"})
	if !strings.Contains(stderr.String(), "Verification run receipt: pending") {
		t.Fatal("human default was silent")
	}
	stderr.Reset()
	view := main.NewBindingProgress(main.WithBindingProgress(t.Context(), "ndjson"), "receipt", "api", "", "")
	report := main.BindingPolicyVerificationReport{RunID: "receipt", App: "api", Status: "blocked", Error: "hidden raw transport payload", ErrorCode: "verification_timeout", Recovery: view.Recovery()}
	view.RenderReport(report)
	if strings.Contains(stderr.String(), "hidden") {
		t.Fatal("raw error entered progress stream")
	}
	var event map[string]any
	if err := json.Unmarshal(stderr.Bytes(), &event); err != nil {
		t.Fatal(err)
	}
	if event["type"] != "binding_verification_notice" || event["code"] != "verification_timeout" {
		t.Fatal(event)
	}
	if !strings.Contains(output.String(), "Binding release verification") {
		t.Fatal("human stdout result missing")
	}
}

func TestBindingProgressTimeoutRetainsWaitAndCleanup(t *testing.T) {
	raw, err := os.ReadFile("../../sdk/binding-runner-tests/creation.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, graph := range []bool{false, true} {
		t.Run(map[bool]string{false: "app", true: "graph"}[graph], func(t *testing.T) {
			var f struct {
				App   api.BindingVerificationRun        `json:"app_run"`
				Graph api.ProjectBindingVerificationRun `json:"graph_run"`
			}
			if err := json.Unmarshal(raw, &f); err != nil {
				t.Fatal(err)
			}
			at := time.Now().UTC().Add(-time.Second)
			expiry := at.Add(time.Minute)
			update := func(r api.BindingVerificationRun) api.BindingVerificationRun {
				r.CreatedAt = at
				r.ExpiresAt = expiry
				for i := range r.Steps {
					r.Steps[i].Plan.Action = "observe"
					r.Steps[i].Owned = false
					r.Steps[i].ResourceID = ""
					r.Steps[i].ResourceKind = ""
					r.Steps[i].Status = "planned"
				}
				return r
			}
			f.App = update(f.App)
			f.Graph.CreatedAt = at
			f.Graph.ExpiresAt = expiry
			for n, m := range f.Graph.Members {
				f.Graph.Members[n] = update(m)
			}
			id, app := f.App.ID, f.App.App
			if graph {
				id = f.Graph.ID
				for n := range f.Graph.Members {
					app = n
					break
				}
			}
			cancels, completes := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodDelete:
					cancels++
					w.WriteHeader(503)
					_ = json.NewEncoder(w).Encode(api.Problem{Status: 503, Detail: "hidden-cleanup-secret"})
					return
				case strings.HasSuffix(r.URL.Path, "/resume"):
				case strings.HasSuffix(r.URL.Path, "/complete"):
					completes++
					q := &api.BindingVerificationQualification{CheckedAt: time.Now().UTC(), Conditions: []api.BindingVerificationQualificationCondition{{App: app, Code: "runtime_updating", Scope: "production"}}}
					f.App.Qualification = q
					f.Graph.Qualification = q
					w.WriteHeader(202)
				case r.Method == http.MethodGet: // Recovery read stays pending until the caller deadline.
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				if graph {
					_ = json.NewEncoder(w).Encode(f.Graph)
				} else {
					_ = json.NewEncoder(w).Encode(f.App)
				}
			}))
			defer server.Close()
			args := []string{"bindings", "verification-runs", "resume", f.App.App, id}
			if graph {
				args = []string{"projects", "environments", "release-sets", "verification-runs", "resume", f.Graph.Project, f.Graph.Environment, id}
			}
			args = append(args, "--json", "--progress", "ndjson", "--poll-interval", "20ms", "--wait-timeout", "120ms")
			code, out, stderr := main.CaptureBindingCLI(t, server.URL, args...)
			if code != 1 || cancels != 1 || completes < 1 {
				t.Fatalf("code=%d cancels=%d completes=%d %s %s", code, cancels, completes, out, stderr)
			}
			var result struct {
				Recovery *main.BindingRecoverySummary `json:"recovery"`
			}
			if err := json.Unmarshal([]byte(out), &result); err != nil {
				t.Fatal(err)
			}
			if result.Recovery == nil || result.Recovery.LastQualification == nil || result.Recovery.RunID != id {
				t.Fatalf("lost timeout wait: %s", out)
			}
			notice := false
			waits := 0
			for _, line := range strings.Split(strings.TrimSpace(stderr), "\n") {
				var event struct{ Type, Code, Status string }
				if err := json.Unmarshal([]byte(line), &event); err != nil {
					t.Fatalf("non-NDJSON stderr: %s", line)
				}
				if event.Code == "cleanup_failed" {
					notice = true
				}
				if event.Status == "waiting_for_qualification" {
					waits++
				}
			}
			if !notice || waits != 1 {
				t.Fatalf("cleanup notice=%v repeated waits=%d: %s", notice, waits, stderr)
			}
			if strings.Contains(stderr, "hidden-cleanup-secret") {
				t.Fatal("cleanup payload entered progress")
			}
		})
	}
}

func TestBindingProgressCreationAlwaysAnnouncesRecoveryIdentity(t *testing.T) {
	oldErr, oldJSON := (*main.OsStderr), (*main.JsonOutput)
	defer func() { (*main.OsStderr), (*main.JsonOutput) = oldErr, oldJSON }()
	(*main.JsonOutput) = true
	for _, mode := range []string{"auto", "off", "ndjson"} {
		var out bytes.Buffer
		(*main.OsStderr) = &out
		main.AnnounceBindingRun(main.WithBindingProgress(t.Context(), mode), "receipt", "api", "", "")
		if !strings.Contains(out.String(), "receipt") {
			t.Fatal("lost pre-create recovery identity")
		}
		if mode == "ndjson" {
			var event struct{ Status string }
			if err := json.Unmarshal(out.Bytes(), &event); err != nil || event.Status != "creating" {
				t.Fatalf("invalid creation progress: %s", out.String())
			}
		}
	}
}
