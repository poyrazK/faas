package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const rehearsalObserver = `set -eu
test ! -e ran.txt
printf ran > ran.txt
test "$(cat fixture.txt)" = trusted-fixture
printf '%s\n' "$PWD" >> "$REHEARSAL_TEST_TRACE"
value=$(sh action.sh)
printf '{"version":1,"observations":[{"key":"notification.successes","value":%s}]}' "$value" > "$GREGALE_TEST_BEHAVIOR_OUTPUT"
test ! -e fail.txt
`

func rehearsalTestGit(t *testing.T, repo string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Rehearsal Test", "GIT_AUTHOR_EMAIL=rehearsal@example.invalid", "GIT_COMMITTER_NAME=Rehearsal Test", "GIT_COMMITTER_EMAIL=rehearsal@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", args[0], err, out)
	}
	return strings.TrimSpace(string(out))
}

func rehearsalTestRepo(t *testing.T) (testRehearsalOptions, string) {
	t.Helper()
	repo := t.TempDir()
	inputs := filepath.Join(repo, "checks")
	if err := os.Mkdir(inputs, 0o700); err != nil {
		t.Fatal(err)
	}
	writeSuiteTestFile(t, filepath.Join(inputs, "gregale-test.yaml"), `version: 1
scenarios:
  notifications:
    project: rehearsal-app
    source: .
    command: [sh, observe.sh]
    simulation: [sh, observe.sh]
    timeout: 5s
    behavior:
      version: 1
      fixtures: [fixture.txt]
      observers: [observe.sh]
      observations: [{key: notification.successes, kind: delivery}]
`)
	writeSuiteTestFile(t, filepath.Join(inputs, "fixture.txt"), "trusted-fixture")
	writeSuiteTestFile(t, filepath.Join(inputs, "observe.sh"), rehearsalObserver)
	writeSuiteTestFile(t, filepath.Join(inputs, "action.sh"), "printf 1\n")
	rehearsalTestGit(t, repo, "init", "-q")
	rehearsalTestGit(t, repo, "add", ".")
	rehearsalTestGit(t, repo, "commit", "-qm", "baseline")
	base := rehearsalTestGit(t, repo, "rev-parse", "HEAD")
	writeSuiteTestFile(t, filepath.Join(inputs, "action.sh"), "printf 2\n")
	// Neither the candidate's changed observer nor its changed fixture may
	// weaken the input contract supplied by the invoking checkout.
	writeSuiteTestFile(t, filepath.Join(inputs, "observe.sh"), "exit 73\n")
	writeSuiteTestFile(t, filepath.Join(inputs, "fixture.txt"), "candidate-fixture")
	rehearsalTestGit(t, repo, "add", ".")
	rehearsalTestGit(t, repo, "commit", "-qm", "duplicate notification")
	candidate := rehearsalTestGit(t, repo, "rev-parse", "HEAD")
	writeSuiteTestFile(t, filepath.Join(inputs, "observe.sh"), rehearsalObserver)
	writeSuiteTestFile(t, filepath.Join(inputs, "fixture.txt"), "trusted-fixture")
	// This dirty application code must never be substituted for either commit.
	writeSuiteTestFile(t, filepath.Join(inputs, "action.sh"), "printf 999\n")
	trace := filepath.Join(t.TempDir(), "trace.txt")
	t.Setenv("REHEARSAL_TEST_TRACE", trace)
	return testRehearsalOptions{Repo: repo, Manifest: filepath.Join(inputs, "gregale-test.yaml"), Scenario: "notifications", Base: base, Candidate: candidate, Engine: "simulated", Profile: "warm", Repeat: 2}, trace
}

func TestRehearsalTwoCommitsUseTrustedInputsAndFreshSources(t *testing.T) {
	captureSuiteTestOutput(t)
	o, trace := rehearsalTestRepo(t)
	session, err := prepareTestRehearsal(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(session.root) })
	// Resolve both refs before execution and use only captured input bytes.
	writeSuiteTestFile(t, filepath.Join(o.Repo, "checks", "observe.sh"), "exit 88\n")
	writeSuiteTestFile(t, filepath.Join(o.Repo, "checks", "fixture.txt"), "changed-after-preparation")
	report := runTestRehearsal(context.Background(), nil, session, o)
	if report.Status != "failed" || report.Comparison.BehaviorStatus != "failed" || report.CleanupError != "" {
		t.Fatalf("incorrect rehearsal gate: %+v", report)
	}
	if len(report.BaselineRuns) != 2 || len(report.CandidateRuns) != 2 || report.Baseline.SourceSHA256 == report.Candidate.SourceSHA256 || len(report.TrustedFiles) != 2 {
		t.Fatalf("missing source or execution provenance: %+v", report.testRehearsalIdentity)
	}
	for i := range report.BaselineRuns {
		before, after := report.BaselineRuns[i], report.CandidateRuns[i]
		if before.Status != "passed" || after.Status != "passed" || before.Attempt != i+1 || after.Attempt != i+1 {
			t.Fatalf("underlying runs did not pass: %+v / %+v", before, after)
		}
		if before.Behavior.FixtureSHA256 != after.Behavior.FixtureSHA256 || before.Behavior.ContractSHA256 != after.Behavior.ContractSHA256 || string(before.Behavior.Observations[0].Value) != "1" || string(after.Behavior.Observations[0].Value) != "2" {
			t.Fatal("pinned code and trusted input identities were lost")
		}
	}
	paths, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, path := range strings.Fields(string(paths)) {
		if seen[path] {
			t.Fatalf("source directory reused across attempts: %s", path)
		}
		seen[path] = true
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("revision source leaked: %s (%v)", path, err)
		}
	}
	if len(seen) != 4 {
		t.Fatalf("expected four isolated runs, got %d", len(seen))
	}
	if got := rehearsalTestGit(t, o.Repo, "rev-parse", "HEAD"); got != o.Candidate {
		t.Fatal("rehearsal changed the user's checkout")
	}
	o.Report, o.HTML, o.Markdown = filepath.Join(t.TempDir(), "rehearsal.json"), filepath.Join(t.TempDir(), "rehearsal.html"), filepath.Join(t.TempDir(), "rehearsal.md")
	if err := saveTestRehearsalReport(o, report); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{o.Report, o.HTML, o.Markdown} {
		body, err := os.ReadFile(path)
		if err != nil || !bytes.Contains(body, []byte(o.Base)) || !bytes.Contains(body, []byte(o.Candidate)) || !bytes.Contains(body, []byte(report.ManifestSHA256)) {
			t.Fatalf("missing rehearsal identity in %s: %v", path, err)
		}
		if bytes.Contains(body, []byte("trusted-fixture")) || bytes.Contains(body, []byte("changed-after-preparation")) {
			t.Fatalf("fixture contents leaked in %s", path)
		}
	}
}

// macOS's default TMPDIR is below the /var -> /private/var symlink. The
// session root must resolve like the checkout does, or every extracted source
// appears to escape it (ADR-20261019120000).
func TestRehearsalPreparesBelowSymlinkedTempDir(t *testing.T) {
	captureSuiteTestOutput(t)
	o, _ := rehearsalTestRepo(t)
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "tmp-link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", link)
	o.Validate = true
	session, err := prepareTestRehearsal(context.Background(), o)
	if err != nil {
		t.Fatalf("prepare below symlinked TMPDIR: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(session.root) })
	resolvedReal, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(session.root, resolvedReal+string(filepath.Separator)) {
		t.Fatalf("session root %s is not resolved below %s", session.root, resolvedReal)
	}
}

func TestRehearsalEquivalentRevisionsAndInterruptedRuns(t *testing.T) {
	captureSuiteTestOutput(t)
	for _, interrupt := range []bool{false, true} {
		t.Run(map[bool]string{false: "equivalent", true: "interrupted"}[interrupt], func(t *testing.T) {
			o, trace := rehearsalTestRepo(t)
			o.Candidate, o.Repeat = o.Base, 1
			session, err := prepareTestRehearsal(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(session.root) })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if interrupt {
				cancel()
			}
			report := runTestRehearsal(ctx, nil, session, o)
			want := "passed"
			if interrupt {
				want = "inconclusive"
				if _, err := os.Stat(trace); !os.IsNotExist(err) {
					t.Fatal("interrupted rehearsal still executed a command")
				}
			}
			if report.Status != want || report.Baseline.SourceSHA256 != report.Candidate.SourceSHA256 {
				t.Fatalf("rehearsal status %s, want %s", report.Status, want)
			}
			if _, err := os.Stat(session.root); !os.IsNotExist(err) {
				t.Fatalf("session leaked after %s: %v", want, err)
			}
		})
	}
}

func TestRehearsalCLIValidationGatesAndOutputProtection(t *testing.T) {
	out, _ := captureSuiteTestOutput(t)
	o, trace := rehearsalTestRepo(t)
	args := []string{"rehearse", "--repo", o.Repo, "--manifest", o.Manifest, "--scenario", o.Scenario, "--base", o.Base, "--candidate", o.Candidate, "--engine", "simulated"}
	if code := cmdTest(append(args, "--validate")); code != 0 {
		t.Fatalf("validate failed: %d", code)
	}
	if _, err := os.Stat(trace); !os.IsNotExist(err) {
		t.Fatal("validation executed application code")
	}
	path := filepath.Join(t.TempDir(), "report.json")
	writeSuiteTestFile(t, path, "existing")
	if code := cmdTest(append(args, "--report", path)); code == 0 {
		t.Fatal("existing output was accepted")
	}
	body, _ := os.ReadFile(path)
	if string(body) != "existing" {
		t.Fatal("existing file was overwritten")
	}
	if _, err := os.Stat(trace); !os.IsNotExist(err) {
		t.Fatal("output validation happened after execution")
	}
	newPath := filepath.Join(t.TempDir(), "report.json")
	if code := cmdTest(append(args, "--report", newPath)); code != 1 {
		t.Fatalf("changed behavior exit code = %d; output %s", code, out)
	}
	body, _ = os.ReadFile(newPath)
	var report testRehearsalReport
	if err := json.Unmarshal(body, &report); err != nil || report.Status != "failed" || report.Comparison.Rehearsal == nil {
		t.Fatalf("combined report not saved on regression: %v", err)
	}
}

func TestRehearsalRejectsUnsupportedSourcesAndRevisions(t *testing.T) {
	captureSuiteTestOutput(t)
	for _, mode := range []string{"missing-ref", "option-ref", "symlink", "submodule", "outside-manifest", "outside-source", "absolute-service", "failed-assertion"} {
		t.Run(mode, func(t *testing.T) {
			o, trace := rehearsalTestRepo(t)
			switch mode {
			case "missing-ref":
				o.Candidate = "no-such-commit"
			case "option-ref":
				o.Candidate = "--all"
			case "symlink":
				if err := os.Symlink("action.sh", filepath.Join(o.Repo, "checks", "link.sh")); err != nil {
					t.Fatal(err)
				}
				rehearsalTestGit(t, o.Repo, "add", "checks/link.sh")
			case "submodule":
				rehearsalTestGit(t, o.Repo, "update-index", "--add", "--cacheinfo", "160000,"+o.Base+",module")
			case "outside-manifest":
				o.Repo = t.TempDir()
				rehearsalTestGit(t, o.Repo, "init", "-q")
			case "outside-source":
				body, _ := os.ReadFile(o.Manifest)
				writeSuiteTestFile(t, o.Manifest, strings.Replace(string(body), "source: .", "source: /tmp", 1))
			case "absolute-service":
				body, _ := os.ReadFile(o.Manifest)
				writeSuiteTestFile(t, o.Manifest, strings.Replace(string(body), "source: .", "source: .\n    services: {other: {source: /tmp}}", 1))
			case "failed-assertion":
				writeSuiteTestFile(t, filepath.Join(o.Repo, "checks", "fail.txt"), "fail after valid observations")
				rehearsalTestGit(t, o.Repo, "add", "checks/fail.txt")
			}
			if mode == "symlink" || mode == "submodule" || mode == "failed-assertion" {
				rehearsalTestGit(t, o.Repo, "commit", "-qm", mode)
				o.Candidate = rehearsalTestGit(t, o.Repo, "rev-parse", "HEAD")
			}
			session, err := prepareTestRehearsal(context.Background(), o)
			if mode == "failed-assertion" {
				if err != nil {
					t.Fatal(err)
				}
				report := runTestRehearsal(context.Background(), nil, session, o)
				if report.Status != "inconclusive" || report.CandidateRuns[0].Status != "failed" || report.CandidateRuns[0].Behavior.Status != "complete" {
					t.Fatalf("assertion failure looked equivalent: %+v", report)
				}
				return
			}
			if session != nil {
				_ = os.RemoveAll(session.root)
			}
			if err == nil {
				t.Fatal("unsupported source was accepted")
			}
			if _, err := os.Stat(trace); !os.IsNotExist(err) {
				t.Fatal("invalid revision executed application code")
			}
		})
	}
}

func TestRehearsalOptionAndResourceGuards(t *testing.T) {
	valid := testRehearsalOptions{Base: "main", Candidate: "HEAD", Scenario: "notifications", Engine: "simulated", Profile: "warm", Repeat: 1, Report: "new.json"}
	for _, mode := range []string{"missing-base", "missing-candidate", "missing-report", "engine", "profile", "repeat", "real-vm-budget", "validate-output", "output-alias"} {
		t.Run(mode, func(t *testing.T) {
			o := valid
			switch mode {
			case "missing-base":
				o.Base = ""
			case "missing-candidate":
				o.Candidate = ""
			case "missing-report":
				o.Report = ""
			case "engine":
				o.Engine = "unknown"
			case "profile":
				o.Profile = "other"
			case "repeat":
				o.Repeat = 21
			case "real-vm-budget":
				o.Engine = "real-vm"
			case "validate-output":
				o.Validate = true
			case "output-alias":
				o.HTML = o.Report
			}
			if err := validateTestRehearsalOptions(o, false); err == nil {
				t.Fatal("invalid options were accepted")
			}
		})
	}
	if err := validateTestRehearsalOptions(valid, true); err == nil {
		t.Fatal("simulated lifecycle profile accepted")
	}
	var out bytes.Buffer
	w := &testRehearsalLimitWriter{writer: &out, remaining: 2}
	if _, err := w.Write([]byte("oversized")); err == nil || out.Len() != 0 {
		t.Fatal("bounded snapshot writer exceeded its budget")
	}
}

func TestRehearsalActiveInterruptionPreservesFailureAndRemovesSources(t *testing.T) {
	captureSuiteTestOutput(t)
	o, trace := rehearsalTestRepo(t)
	writeSuiteTestFile(t, filepath.Join(o.Repo, "checks", "action.sh"), "sleep 30\nprintf 1\n")
	rehearsalTestGit(t, o.Repo, "add", "checks/action.sh")
	rehearsalTestGit(t, o.Repo, "commit", "-qm", "slow operation")
	o.Base = rehearsalTestGit(t, o.Repo, "rev-parse", "HEAD")
	o.Candidate, o.Repeat = o.Base, 1
	session, err := prepareTestRehearsal(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(session.root) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{})
	go func() {
		defer close(ready)
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(trace); err == nil {
				cancel()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
	}()
	started := time.Now()
	report := runTestRehearsal(ctx, nil, session, o)
	<-ready
	if time.Since(started) > 4*time.Second || report.Status != "inconclusive" || report.BaselineRuns[0].Status != "failed" || report.CandidateRuns[0].Status != "skipped" {
		t.Fatalf("active interruption did not stop the rehearsal: %+v", report)
	}
	if _, err := os.Stat(trace); err != nil {
		t.Fatal("test did not interrupt an executing scenario")
	}
	if _, err := os.Stat(session.root); !os.IsNotExist(err) {
		t.Fatal("interrupted revision directories leaked")
	}
}

func TestRehearsalExportExampleComparesTwoGitCommits(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node.js is required for the export acceptance fixture")
	}
	_, stderr := captureSuiteTestOutput(t)
	t.Setenv("GREGALE_BEHAVIOR_EXAMPLE_VARIANT", "")
	repo := t.TempDir()
	fixture := "tests/scenario-acceptance/behavior/"
	for _, path := range []string{
		fixture + "gregale-test.yaml", fixture + "fixture.json", fixture + "observe.mjs", fixture + "run.mjs", fixture + "implementation.mjs",
		"tests/scenario-acceptance/export-api/server.js", "tests/scenario-acceptance/export-api/package.json",
		"tests/scenario-acceptance/export-worker/server.js", "tests/scenario-acceptance/export-worker/package.json",
		"cmd/gregale/scenario_fixtures/delivery-sink/server.js", "cmd/gregale/scenario_fixtures/delivery-sink/package.json",
	} {
		if err := copyTestRehearsalFile(filepath.Join("..", "..", filepath.FromSlash(path)), filepath.Join(repo, filepath.FromSlash(path)), testBehaviorFileLimit); err != nil {
			t.Fatal(err)
		}
	}
	rehearsalTestGit(t, repo, "init", "-q")
	rehearsalTestGit(t, repo, "add", ".")
	rehearsalTestGit(t, repo, "commit", "-qm", "single notification")
	base := rehearsalTestGit(t, repo, "rev-parse", "HEAD")
	writeSuiteTestFile(t, filepath.Join(repo, filepath.FromSlash(fixture), "implementation.mjs"), `export const implementationVariant = "duplicate";`)
	rehearsalTestGit(t, repo, "add", ".")
	rehearsalTestGit(t, repo, "commit", "-qm", "duplicate successful notification")
	candidate := rehearsalTestGit(t, repo, "rev-parse", "HEAD")
	outputs := t.TempDir()
	path := filepath.Join(outputs, "rehearsal.json")
	if code := cmdTest([]string{"rehearse", "--repo", repo, "--manifest", filepath.Join(repo, filepath.FromSlash(fixture), "gregale-test.yaml"), "--base", base, "--candidate", candidate, "--scenario", "customer-export", "--engine", "simulated", "--report", path, "--html", filepath.Join(outputs, "rehearsal.html"), "--markdown", filepath.Join(outputs, "rehearsal.md")}); code != 1 {
		t.Fatalf("expected a behavior regression, got exit %d: %s", code, stderr)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report testRehearsalReport
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatal(err)
	}
	if report.Status != "failed" || report.Baseline.Commit != base || report.Candidate.Commit != candidate || len(report.BaselineRuns) != 1 || len(report.CandidateRuns) != 1 {
		t.Fatalf("missing real revision comparison: %+v", report)
	}
	if report.BaselineRuns[0].Status != "passed" || report.CandidateRuns[0].Status != "passed" {
		t.Fatalf("scenario executions failed: %s", stderr)
	}
	changes := report.Comparison.Runs[0].Behavior.Changes
	if len(changes) != 3 || changes[0].Status != "unchanged" || changes[1].Status != "unchanged" || changes[2].Key != "notification.successful_deliveries" || string(changes[2].Before) != "1" || string(changes[2].After) != "2" {
		t.Fatalf("unexpected behavioral evidence: %+v", changes)
	}
}

func TestRehearsalIgnoresReplacementObjectsAndMovingRefs(t *testing.T) {
	captureSuiteTestOutput(t)
	o, _ := rehearsalTestRepo(t)
	rehearsalTestGit(t, o.Repo, "branch", "reviewed-base", o.Base)
	rehearsalTestGit(t, o.Repo, "branch", "reviewed-candidate", o.Candidate)
	rehearsalTestGit(t, o.Repo, "replace", o.Base, o.Candidate)
	base, candidate := o.Base, o.Candidate
	o.Base, o.Candidate, o.Repeat = "reviewed-base", "reviewed-candidate", 1
	session, err := prepareTestRehearsal(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(session.root) })
	rehearsalTestGit(t, o.Repo, "branch", "-f", "reviewed-base", candidate)
	rehearsalTestGit(t, o.Repo, "branch", "-f", "reviewed-candidate", base)
	report := runTestRehearsal(context.Background(), nil, session, o)
	if report.Status != "failed" || report.Baseline.Commit != base || report.Candidate.Commit != candidate || string(report.BaselineRuns[0].Behavior.Observations[0].Value) != "1" {
		t.Fatalf("replacement objects or moving refs changed pinned execution: %+v", report)
	}
}

func TestRehearsalRealVMFailuresRetainDistinctNamespacesAndCleanup(t *testing.T) {
	captureSuiteTestOutput(t)
	o, _ := rehearsalTestRepo(t)
	writeSuiteTestFile(t, filepath.Join(o.Repo, "checks", "package.json"), `{"name":"rehearsal","scripts":{"start":"node server.js"}}`)
	writeSuiteTestFile(t, filepath.Join(o.Repo, "checks", "server.js"), "require('http').createServer((r,s)=>s.end('ok')).listen(8080);\n")
	rehearsalTestGit(t, o.Repo, "add", "checks/package.json", "checks/server.js")
	rehearsalTestGit(t, o.Repo, "commit", "-qm", "hosted app source")
	o.Base = rehearsalTestGit(t, o.Repo, "rev-parse", "HEAD")
	o.Candidate, o.Repeat, o.Engine, o.MaxWorkloadMinutes = o.Base, 1, "real-vm", 30
	session, err := prepareTestRehearsal(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(session.root) })
	workspaces, namespaces, destroyed, released := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "PUT" && r.URL.Path == "/v1/dev/sessions/rehearsal-app":
			var request struct {
				WorkspaceID string `json:"workspace_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.WorkspaceID == "" || workspaces[request.WorkspaceID] {
				t.Errorf("invalid or reused workspace: %+v (%v)", request, err)
			}
			workspaces[request.WorkspaceID] = true
			_ = json.NewEncoder(w).Encode(api.DevSessionResponse{App: api.AppResponse{Slug: "rehearsal-" + request.WorkspaceID[:8]}})
		case strings.HasPrefix(r.URL.Path, "/v1/dev/test-runs/"):
			id := strings.TrimPrefix(r.URL.Path, "/v1/dev/test-runs/")
			if r.Method == "PUT" {
				namespaces[id] = true
			} else if r.Method == "DELETE" {
				released[id] = true
			}
			w.WriteHeader(204)
		case r.Method == "PATCH" && strings.HasPrefix(r.URL.Path, "/v1/apps/"):
			w.WriteHeader(500) // Fail before building; both sides still own cleanup.
		case r.Method == "DELETE" && r.URL.Path == "/v1/dev/sessions/rehearsal-app":
			destroyed[r.URL.Query().Get("workspace_id")] = true
			w.WriteHeader(204)
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer server.Close()
	report := runTestRehearsal(context.Background(), NewClient(server.URL, "synthetic-key"), session, o)
	if report.Status != "inconclusive" || len(workspaces) != 2 || len(namespaces) != 2 || len(destroyed) != 2 || len(released) != 2 {
		t.Fatalf("isolated cleanup was lost: status=%s workspaces=%v namespaces=%v destroyed=%v released=%v", report.Status, workspaces, namespaces, destroyed, released)
	}
	for id := range workspaces {
		if !namespaces[id] || !destroyed[id] || !released[id] {
			t.Fatalf("namespace cleanup did not match workspace %s", id)
		}
	}
}
