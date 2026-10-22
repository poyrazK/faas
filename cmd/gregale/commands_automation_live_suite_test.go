// adr: 1295
package main_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

const liveSuiteAppID = "00000000-0000-4000-8000-000000000003"
const liveSuiteAPIPath = "/v1/apps/billing/automations/report/live-verifications"

type liveSuiteAdmission struct{ key, body, name string }

type liveSuiteFixture struct {
	t             *testing.T
	mu            sync.Mutex
	mode          string
	path          string
	calls         []string
	starts        []liveSuiteAdmission
	runs          map[string]api.AutomationLiveVerification
	keys          map[string]string
	onStart       func()
	onRead        func()
	sharedSaves   int
	sharedID      string
	sharedRequest api.SaveAutomationLiveSuiteRequest
}

func newLiveSuiteFixture(t *testing.T, mode string) *liveSuiteFixture {
	t.Helper()
	main.ResetJSONOut(t)
	(*main.JsonOutput) = true
	f := &liveSuiteFixture{t: t, mode: mode, path: filepath.Join(t.TempDir(), "suite.json"), runs: map[string]api.AutomationLiveVerification{}, keys: map[string]string{}}
	server := main.AuthedFakeAPI(t, "", 200)
	(*server.Srv()).Config.Handler = http.HandlerFunc(f.serveHTTP)
	return f
}

func (f *liveSuiteFixture) serveHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	w.Header().Set("Content-Type", "application/json")
	if r.Header.Get("Authorization") != "Bearer test-token" {
		f.t.Error("suite request omitted authentication")
	}
	var response any
	switch {
	case r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/v1/apps/billing/automations/report/live-suites/"):
		f.sharedSaves++
		var request api.SaveAutomationLiveSuiteRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if decoder.Decode(&request) != nil || request.ExpectedVersion != 7 || request.ExpectedPublishedVersion != 4 || request.ExpectedDeploymentID != main.LiveCLIDeployment {
			f.t.Error("shared history changed the frozen target or sent non-metadata fields")
		}
		if f.mode == "share-fails-before" || f.mode == "share-fails-after" && len(f.keys) > 0 {
			w.WriteHeader(503)
			_, _ = w.Write([]byte(`{"detail":"PRIVATE sharing failure"}`))
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/v1/apps/billing/automations/report/live-suites/")
		if f.sharedID != "" && f.sharedID != id {
			f.t.Error("resume changed shared suite identity")
		}
		f.sharedID, f.sharedRequest = id, request
		view := api.AutomationLiveSuite{AutomationLiveSuiteSummary: api.AutomationLiveSuiteSummary{ID: id, Name: "report", Version: 7, PublishedVersion: 4, DeploymentID: main.LiveCLIDeployment}}
		for _, member := range request.Cases {
			view.Cases = append(view.Cases, api.AutomationLiveSuiteCase{Name: member.Name, RunID: member.RunID})
		}
		if f.mode == "share-wrong-target" {
			view.Version++
		}
		response = view
	case r.Method == "GET" && r.URL.Path == "/v1/apps/billing":
		id := liveSuiteAppID
		if f.mode == "app-changed" {
			id = main.LiveCLIRun
		}
		response = map[string]string{"id": id, "slug": "billing"}
	case r.Method == "GET" && r.URL.Path == "/v1/apps/billing/automations/report":
		def := api.WorkflowSpec{Name: "report", Steps: []api.WorkflowStepSpec{{Name: "collect", Path: "/collect"}}}
		suite := &api.AutomationTestSuite{}
		for _, name := range []string{"first", "second"} {
			live := &api.AutomationLiveTest{Expectations: []api.AutomationLiveExpectation{{Step: "collect", State: "succeeded"}}}
			if f.mode == "expected-failure" {
				live.ExpectedRunStatus = "failed"
			}
			suite.Cases = append(suite.Cases, api.AutomationTestCase{Name: name, Simulation: api.AutomationTestInputs{Input: json.RawMessage(`{"PRIVATE":"input"}`)}, Live: live})
		}
		version := int64(7)
		if f.mode == "version-changed" {
			version++
		}
		if f.mode == "case-missing" {
			suite.Cases = suite.Cases[:1]
		}
		response = api.AutomationResponse{Name: "report", Version: version, PublishedVersion: 4, Published: &def, TestSuite: suite}
	case r.Method == "GET" && r.URL.Path == liveSuiteAPIPath:
		deployment := main.LiveCLIDeployment
		if f.mode == "deployment-changed" {
			deployment = main.LiveCLIRun
		}
		response = api.ListAutomationLiveVerificationsResponse{DeploymentID: deployment, PublishedVersion: 4}
	case r.Method == "POST" && r.URL.Path == liveSuiteAPIPath:
		body, _ := io.ReadAll(r.Body)
		var target api.StartAutomationLiveVerificationRequest
		decoder := json.NewDecoder(strings.NewReader(string(body)))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&target) != nil || target.ExpectedVersion != 7 || target.ExpectedPublishedVersion != 4 || target.ExpectedDeploymentID != main.LiveCLIDeployment || !target.ConfirmRealActions {
			f.t.Errorf("suite changed its original start fences: %s", body)
		}
		key := r.Header.Get("Idempotency-Key")
		if !main.ValidLiveSuiteHash(key) {
			f.t.Error("suite omitted its derived stable case key")
		}
		f.starts = append(f.starts, liveSuiteAdmission{key, string(body), target.CaseName})
		if target.CaseName == "second" {
			for _, previous := range f.runs {
				if !previous.ReportCurrent || previous.Report == nil || !previous.Report.Passed {
					f.t.Error("admitted the next case before a passing verdict")
				}
			}
		}
		if f.mode == "conflict-second" && target.CaseName == "second" {
			w.WriteHeader(409)
			_, _ = w.Write([]byte(`{"detail":"PRIVATE target changed"}`))
			return
		}
		id := f.keys[key]
		if id == "" {
			id = fmt.Sprintf("00000000-0000-4000-8000-%012d", len(f.keys)+10)
			view := main.LiveCLIView("succeeded")
			view.RunID, view.CaseName = id, target.CaseName
			if f.mode == "pending" {
				view.RunStatus = "running"
			}
			if f.mode == "expected-failure" {
				view.RunStatus = "failed"
			}
			f.keys[key], f.runs[id] = id, view
		}
		if f.onStart != nil {
			f.onStart()
		}
		if f.mode == "lost-ack" && len(f.starts) == 1 {
			w.WriteHeader(503)
			_, _ = w.Write([]byte(`{"detail":"PRIVATE lost acknowledgement"}`))
			return
		}
		view := f.runs[id]
		if f.mode == "wrong-admission" {
			view.CaseName = "other"
		}
		response = view
	case strings.HasPrefix(r.URL.Path, liveSuiteAPIPath+"/"):
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, liveSuiteAPIPath+"/"), "/check")
		view, exists := f.runs[id]
		if !exists || f.mode == "record-missing" {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"detail":"PRIVATE missing run"}`))
			return
		}
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/check") {
			var body map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 0 {
				f.t.Error("check submitted execution data")
			}
			view.ReportCurrent = true
			view.Report = &api.AutomationLiveReport{Passed: f.mode != "failed", RunStatus: view.RunStatus, CheckedAt: time.Now().UTC(), Steps: []api.AutomationTestStepResult{{Step: "collect", State: "succeeded", Passed: f.mode != "failed"}}}
			f.runs[id] = view
		} else if r.Method == "GET" {
			if f.onRead != nil {
				f.onRead()
			}
			if len(f.keys) == 2 && view.CaseName == "first" && f.mode == "final-resumed" {
				view.RunStatus, view.ReportCurrent = "running", false
			}
			if f.mode == "wrong-run" {
				view.RunID = main.LiveCLIRun
			}
		} else {
			f.t.Errorf("unexpected execution mutation: %s %s", r.Method, r.URL.Path)
		}
		response = view
	default:
		f.t.Errorf("unexpected suite request: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(500)
		return
	}
	data, _ := json.Marshal(response)
	var fields map[string]any
	_ = json.Unmarshal(data, &fields)
	fields["raw_error"] = "PRIVATE backend payload"
	_ = json.NewEncoder(w).Encode(fields)
}

func liveSuiteArgs(path string) []string {
	return []string{"automations", "live", "suite", "--app", "billing", "--name", "report", "--case", "first", "--case", "second", "--expected-version", "7", "--expected-published-version", "4", "--expected-deployment-id", main.LiveCLIDeployment, "--idempotency-key", "suite-ci-key", "--journal", path, "--confirm-real-actions"}
}

type liveSuiteTestResult struct {
	SchemaVersion int                                  `json:"schema_version"`
	Outcome       string                               `json:"outcome"`
	ExitCode      int                                  `json:"exit_code"`
	Summary       map[string]int                       `json:"summary"`
	Cases         []main.AutomationLiveSuiteCaseResult `json:"cases"`
	Resume        string                               `json:"resume"`
}

func runLiveSuiteTest(t *testing.T, args []string, want int) liveSuiteTestResult {
	t.Helper()
	out := main.CaptureAutomationStdout(t)
	code, stderr := main.RunWithStderr(t, func() int { return main.Run(args) })
	var result liveSuiteTestResult
	if code != want || json.Unmarshal(out.Bytes(), &result) != nil || result.SchemaVersion != 1 || result.ExitCode != want {
		t.Fatalf("exit=%d want=%d stdout=%s stderr=%s", code, want, out, stderr)
	}
	for _, secret := range []string{"PRIVATE", "suite-ci-key", "idempotency_key", `"key"`} {
		if strings.Contains(out.String()+stderr, secret) {
			t.Fatalf("suite output exposed private data: %s", out)
		}
	}
	return result
}

func resumeLiveSuiteArgs(path string) []string {
	return []string{"automations", "live", "suite", "--resume", path, "--confirm-real-actions"}
}

func TestAutomationLiveSuiteSequentialResults(t *testing.T) {
	for _, mode := range []string{"passed", "expected-failure", "failed", "case-missing", "conflict-second", "wrong-admission", "final-resumed"} {
		t.Run(mode, func(t *testing.T) {
			f := newLiveSuiteFixture(t, mode)
			want := map[string]int{"failed": 1, "case-missing": 1, "conflict-second": 5, "wrong-admission": 3, "final-resumed": 1}[mode]
			result := runLiveSuiteTest(t, liveSuiteArgs(f.path), want)
			j, err := main.ReadAutomationLiveSuite(f.path)
			if err != nil {
				t.Fatal(err)
			}
			if result.Summary["selected"] != 2 || !strings.Contains(result.Resume, "--resume ") || !strings.Contains(result.Resume, "--confirm-real-actions") {
				t.Fatal("missing combined result or explicit resume command", result)
			}
			info, err := os.Stat(f.path)
			if err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
				t.Fatal("journal was not private", err)
			}
			raw, _ := os.ReadFile(f.path)
			if strings.Contains(string(raw), "PRIVATE") {
				t.Fatal("journal retained inputs or raw backend data")
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			switch mode {
			case "passed", "expected-failure":
				if result.Outcome != "passed" || result.Summary["passed"] != 2 || len(f.starts) != 2 || result.Cases[0].ObservedAt == nil {
					t.Fatal("incomplete passing suite", result)
				}
			case "failed", "wrong-admission":
				if len(f.starts) != 1 || result.Summary["not_started"] != 1 {
					t.Fatal("suite admitted work after a nonpassing case", result)
				}
			case "case-missing":
				if len(f.starts) != 0 || result.Summary["not_started"] != 2 {
					t.Fatal("suite did not validate all selected cases before execution")
				}
			case "final-resumed":
				if result.Cases[0].Outcome != "pending" || result.Outcome == "passed" {
					t.Fatal("suite reused a passing report after external continuation", result)
				}
			}
			for i, start := range f.starts {
				if start.key != j.Cases[i].Key || start.name != j.Cases[i].Name {
					t.Fatal("journal did not retain original case admission identity")
				}
			}
		})
	}
}

func TestAutomationLiveSuiteResumeLostAdmission(t *testing.T) {
	f := newLiveSuiteFixture(t, "lost-ack")
	first := runLiveSuiteTest(t, liveSuiteArgs(f.path), 3)
	if first.Cases[0].RunID != "" || first.Summary["not_started"] != 1 {
		t.Fatal("uncertain admission not retained", first)
	}
	j, err := main.ReadAutomationLiveSuite(f.path)
	if err != nil || j.Cases[0].AttemptedAt == nil {
		t.Fatal("intent was not durable before admission", err)
	}
	second := runLiveSuiteTest(t, resumeLiveSuiteArgs(f.path), 0)
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.starts) != 3 || len(f.keys) != 2 || f.starts[0] != f.starts[1] || second.Summary["passed"] != 2 {
		t.Fatal("resume replaced the original request or admitted duplicate work", f.starts, second)
	}
}

func TestAutomationLiveSuiteResumeKnownRun(t *testing.T) {
	for _, mode := range []string{"passed", "record-missing", "wrong-run", "version-changed", "deployment-changed", "app-changed", "failed"} {
		t.Run(mode, func(t *testing.T) {
			f := newLiveSuiteFixture(t, "pending")
			o, err := main.ParseAutomationLiveSuite(liveSuiteArgs(f.path)[3:])
			if err != nil {
				t.Fatal(err)
			}
			client, err := main.AuthedClient()
			if err != nil {
				t.Fatal(err)
			}
			journal, err := main.PrepareAutomationLiveSuite(t.Context(), client, o)
			if err != nil {
				t.Fatal(err)
			}
			// Establish accepted, persisted work before expiring the invocation.
			// A short wall-clock budget can expire before admission on busy CI.
			if _, err := main.AdmitOrReadAutomationLiveSuiteCase(t.Context(), client, (*o.Path()), &journal, 0); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
			defer cancel()
			out := main.CaptureAutomationStdout(t)
			code := main.ExecuteAutomationLiveSuite(ctx, client, o, &journal)
			var first liveSuiteTestResult
			if code != 124 || json.Unmarshal(out.Bytes(), &first) != nil || first.ExitCode != 124 {
				t.Fatal("deadline did not stop the suite", code, out)
			}
			if first.Cases[0].RunID == "" || first.Outcome != "timed_out" || first.Summary["not_started"] != 1 {
				t.Fatal("deadline lost accepted work", first)
			}
			f.mu.Lock()
			f.mode = mode
			for id, view := range f.runs {
				view.RunStatus = "succeeded"
				f.runs[id] = view
			}
			f.mu.Unlock()
			want := map[string]int{"record-missing": 4, "wrong-run": 3, "version-changed": 5, "deployment-changed": 5, "app-changed": 5, "failed": 1}[mode]
			second := runLiveSuiteTest(t, resumeLiveSuiteArgs(f.path), want)
			f.mu.Lock()
			defer f.mu.Unlock()
			wantStarts := 1
			if mode == "passed" {
				wantStarts = 2
			}
			if len(f.starts) != wantStarts || second.Cases[0].RunID != first.Cases[0].RunID {
				t.Fatal("resume replaced an accepted run or admitted after failure", f.starts, second)
			}
		})
	}
}

func TestAutomationLiveSuiteInterruptAndResume(t *testing.T) {
	f := newLiveSuiteFixture(t, "pending")
	o, err := main.ParseAutomationLiveSuite(liveSuiteArgs(f.path)[3:])
	if err != nil {
		t.Fatal(err)
	}
	c, err := main.AuthedClient()
	if err != nil {
		t.Fatal(err)
	}
	j, err := main.PrepareAutomationLiveSuite(context.Background(), c, o)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.onRead = cancel
	(*o.Interval()) = time.Millisecond
	out := main.CaptureAutomationStdout(t)
	code := main.ExecuteAutomationLiveSuite(ctx, c, o, &j)
	var result liveSuiteTestResult
	if code != 130 || json.Unmarshal(out.Bytes(), &result) != nil || result.Outcome != "interrupted" {
		t.Fatal("interruption lost suite progress", code, out)
	}
	f.mu.Lock()
	f.onRead, f.mode = nil, "passed"
	for id, view := range f.runs {
		view.RunStatus = "succeeded"
		f.runs[id] = view
	}
	f.mu.Unlock()
	runLiveSuiteTest(t, resumeLiveSuiteArgs(f.path), 0)
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.starts) != 2 {
		t.Fatal("interrupted suite admitted a replacement run")
	}
}

func TestAutomationLiveSuitePersistenceFailureStopsAdmissions(t *testing.T) {
	f := newLiveSuiteFixture(t, "passed")
	f.onStart = func() {
		j, err := main.ReadAutomationLiveSuite(f.path)
		if err != nil {
			f.t.Error(err)
			return
		}
		// A competing writer ignores the advisory lock. Compare-and-swap must
		// stop this process, retaining the accepted identity in its CI report.
		if err := main.SaveAutomationLiveSuite(f.path, &j, false); err != nil {
			f.t.Error(err)
		}
	}
	result := runLiveSuiteTest(t, liveSuiteArgs(f.path), 3)
	if result.Cases[0].RunID == "" || result.Summary["not_started"] != 1 {
		t.Fatal("persistence failure lost an acknowledged run", result)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.starts) != 1 {
		t.Fatal("admitted further work after persistence failed")
	}
}

func TestAutomationLiveSuiteExpiredUncertainAdmission(t *testing.T) {
	f := newLiveSuiteFixture(t, "lost-ack")
	runLiveSuiteTest(t, liveSuiteArgs(f.path), 3)
	j, err := main.ReadAutomationLiveSuite(f.path)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-25 * time.Hour)
	j.Plan.CreatedAt, j.Cases[0].AttemptedAt = old, &old
	j.Fingerprint = main.LiveSuiteHash(j.Plan)
	raw, _ := json.Marshal(j)
	if err := os.WriteFile(f.path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	runLiveSuiteTest(t, resumeLiveSuiteArgs(f.path), 3)
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.starts) != 1 {
		t.Fatal("replayed an expired uncertain admission")
	}
}

func TestAutomationLiveSuiteRejectsUnsafeLocalProgress(t *testing.T) {
	for _, mode := range []string{"locked", "existing", "truncated", "trailing", "unknown", "oversized", "fingerprint", "nonprivate", "symlink", "wrong-endpoint"} {
		t.Run(mode, func(t *testing.T) {
			f := newLiveSuiteFixture(t, "lost-ack")
			runLiveSuiteTest(t, liveSuiteArgs(f.path), 3)
			args := resumeLiveSuiteArgs(f.path)
			switch mode {
			case "locked":
				lock, err := main.LockAutomationLiveSuite(f.path)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = lock.Close() }()
			case "existing":
				args = liveSuiteArgs(f.path)
			case "oversized":
				if err := os.Truncate(f.path, api.AutomationLiveSuiteJournalMaxBytes+1); err != nil {
					t.Fatal(err)
				}
			case "fingerprint":
				j, err := main.ReadAutomationLiveSuite(f.path)
				if err != nil {
					t.Fatal(err)
				}
				j.Plan.Cases[0] = "unreviewed"
				raw, _ := json.Marshal(j)
				if err := os.WriteFile(f.path, raw, 0600); err != nil {
					t.Fatal(err)
				}
			case "truncated", "trailing", "unknown":
				raw, _ := os.ReadFile(f.path)
				if mode == "truncated" {
					raw = raw[:len(raw)/2]
				} else if mode == "trailing" {
					raw = append(raw, []byte(`{}`)...)
				} else {
					raw = append([]byte(`{"unknown":true,`), raw[1:]...)
				}
				if err := os.WriteFile(f.path, raw, 0600); err != nil {
					t.Fatal(err)
				}
			case "nonprivate":
				if runtime.GOOS == "windows" {
					t.Skip("Unix permission semantics")
				}
				if err := os.Chmod(f.path, 0644); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				alias := f.path + ".alias"
				if err := os.Symlink(f.path, alias); err != nil {
					t.Skip(err)
				}
				args = resumeLiveSuiteArgs(alias)
			case "wrong-endpoint":
				t.Setenv("FAAS_API", "http://127.0.0.1:1")
			}
			f.mu.Lock()
			before := len(f.calls)
			f.mu.Unlock()
			code, _ := main.RunWithStderr(t, func() int { return main.Run(args) })
			f.mu.Lock()
			defer f.mu.Unlock()
			if code == 0 || len(f.calls) != before {
				t.Fatal("invalid local progress reached the API", code, f.calls[before:])
			}
		})
	}
}

func TestAutomationLiveSuiteSelectionAndStableKeys(t *testing.T) {
	args := liveSuiteArgs(filepath.Join(t.TempDir(), "suite.json"))[3:]
	o, err := main.ParseAutomationLiveSuite(args)
	if err != nil {
		t.Fatal(err)
	}
	p := (*o.Plan())
	p.AppID, p.EndpointHash, p.CreatedAt = liveSuiteAppID, main.LiveSuiteHash("http://fixture"), time.Now().UTC()
	key := main.LiveSuiteCaseKey(p, "first")
	p.CreatedAt = p.CreatedAt.Add(time.Hour)
	p.Cases = []string{"second", "first"}
	if main.LiveSuiteCaseKey(p, "first") != key || main.LiveSuiteCaseKey(p, "second") == key {
		t.Fatal("journal creation time or selection order changed a stable case key")
	}
	p.Target.ExpectedVersion++
	if main.LiveSuiteCaseKey(p, "first") == key {
		t.Fatal("case key did not bind its frozen target")
	}
	for _, extra := range [][]string{{"--case", "first"}, {"--confirm-real-actions=false"}, {"--timeout", "0"}, {"--poll-interval", "1ms"}, {"--resume", "other.json"}, {"--case", "\n"}} {
		if _, err := main.ParseAutomationLiveSuite(append(append([]string(nil), args...), extra...)); err == nil {
			t.Fatal("accepted invalid suite options", extra)
		}
	}
	tooMany := append([]string(nil), args...)
	for i := 0; i < api.AutomationLiveSuiteMaxCases; i++ {
		tooMany = append(tooMany, "--case", fmt.Sprint(i))
	}
	if _, err := main.ParseAutomationLiveSuite(tooMany); err == nil {
		t.Fatal("accepted an unbounded suite")
	}
	resume := resumeLiveSuiteArgs("suite.json")[3:]
	if _, err := main.ParseAutomationLiveSuite(append(resume, "--app", "")); err == nil {
		t.Fatal("resume accepted conflicting selection flags")
	}
	if !reflect.DeepEqual((*o.Plan()).Cases, []string{"first", "second"}) {
		t.Fatal("explicit selection order changed")
	}
}

func TestAutomationLiveSuiteManifestHelp(t *testing.T) {
	f := newLiveSuiteFixture(t, "passed")
	output := main.CaptureAutomationStdout(t)
	code := main.Run([]string{"automations", "live", "suite", "--help"})
	if code != 0 {
		t.Fatal(code, output)
	}
	for _, flag := range []string{"--resume", "--journal", "--case", "--confirm-real-actions"} {
		if !strings.Contains(output.String(), flag) {
			t.Fatal("missing suite help flag", flag, output)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) != 0 {
		t.Fatal("suite help contacted the API")
	}
}
