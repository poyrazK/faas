package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Version 1 rehearses local Git commits using one trusted, fixture-based recipe.
// It delegates execution, lifecycle evidence and resource cleanup to Gregale Test.
type testRehearsalRevision struct {
	Ref           string `json:"ref"`
	Commit        string `json:"commit"`
	Tree          string `json:"tree"`
	ArchiveSHA256 string `json:"archive_sha256"`
	SourceSHA256  string `json:"source_sha256"`
}

type testRehearsalIdentity struct {
	Version        int                   `json:"version"`
	ID             string                `json:"id"`
	Status         string                `json:"status"`
	Scenario       string                `json:"scenario"`
	ManifestSHA256 string                `json:"manifest_sha256"`
	RecipeSHA256   string                `json:"recipe_sha256"`
	TrustedFiles   []testBehaviorFile    `json:"trusted_files"`
	Baseline       testRehearsalRevision `json:"baseline"`
	Candidate      testRehearsalRevision `json:"candidate"`
	CleanupError   string                `json:"cleanup_error,omitempty"`
	Error          string                `json:"error,omitempty"`
}

type testRehearsalReport struct {
	testRehearsalIdentity
	StartedAt     time.Time            `json:"started_at"`
	FinishedAt    time.Time            `json:"finished_at"`
	BaselineRuns  []testRunReceipt     `json:"baseline_runs"`
	CandidateRuns []testRunReceipt     `json:"candidate_runs"`
	Comparison    testReportComparison `json:"comparison"`
}

type testRehearsalOptions struct {
	Repo, Base, Candidate, Manifest, Scenario string
	Engine, Profile                           string
	Repeat, MaxWorkloadMinutes                int
	Validate                                  bool
	Report, HTML, Markdown                    string
}

type testRehearsalSession struct {
	root, repo, manifestDir, trusted string
	scenario                         testScenario
	files                            []testBehaviorFile
	archives                         [2]string
	identity                         testRehearsalIdentity
}

func cmdTestRehearse(args []string) int {
	var o testRehearsalOptions
	fs := newFlagSet("test rehearse", flag.ContinueOnError)
	fs.StringVar(&o.Repo, "repo", ".", "local Git checkout containing both commits and trusted inputs")
	fs.StringVar(&o.Base, "base", "", "baseline Git revision (resolved once to a commit)")
	fs.StringVar(&o.Candidate, "candidate", "", "candidate Git revision (resolved once to a commit)")
	fs.StringVar(&o.Manifest, "manifest", "gregale-test.yaml", "trusted scenario manifest from the invoking checkout")
	fs.StringVar(&o.Scenario, "scenario", "", "scenario with declared behavior observations")
	fs.StringVar(&o.Engine, "engine", "real-vm", "real-vm, local, or simulated")
	fs.StringVar(&o.Profile, "profile", "warm", "real-vm lifecycle profile: warm, cold, restored, or all")
	fs.IntVar(&o.Repeat, "repeat", 1, "fresh runs per revision and profile (1..20)")
	fs.IntVar(&o.MaxWorkloadMinutes, "max-workload-minutes", 0, "required total real-vm workload-minute limit across both revisions")
	fs.BoolVar(&o.Validate, "validate", false, "materialize and validate both revisions without executing code")
	fs.StringVar(&o.Report, "report", "", "combined JSON receipt; output must be a new file")
	fs.StringVar(&o.HTML, "html", "", "standalone comparison HTML; output must be a new file")
	fs.StringVar(&o.Markdown, "markdown", "", "comparison Markdown; output must be a new file")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) || fs.NArg() != 0 {
		return printErr("Invalid rehearsal", errors.New("rehearse accepts flags only; pass --base, --candidate, and --scenario"))
	}
	profileSet := false
	fs.Visit(func(f *flag.Flag) { profileSet = profileSet || f.Name == "profile" })
	if err := validateTestRehearsalOptions(o, profileSet); err != nil {
		return printErr("Invalid rehearsal", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), testTerminationSignals()...)
	defer stop()
	session, err := prepareTestRehearsal(ctx, o)
	if err != nil {
		return printErr("Could not prepare rehearsal", err)
	}
	defer func() { _ = os.RemoveAll(session.root) }()
	if o.Validate {
		if jsonOutput {
			return rehearsalJSONResult(session.identity)
		}
		_, _ = fmt.Fprintf(osStdout, "Rehearsal validated: %s → %s (%s)\n", session.identity.Baseline.Commit, session.identity.Candidate.Commit, o.Scenario)
		return 0
	}
	var client *Client
	if o.Engine == "real-vm" {
		client, err = authedClient()
		if err != nil {
			return printErr("Not logged in", err)
		}
	}
	if err := reserveTestRehearsalOutputs(o.Report, o.HTML, o.Markdown); err != nil {
		return printErr("Invalid rehearsal output", err)
	}
	if local := testRehearsalLocalExecution(session.scenario, o.Engine); local != "" {
		_, _ = fmt.Fprintf(osStderr, "Warning: %s, inside each checked-out revision and with your environment (FAAS_TOKEN removed). Rehearse only revisions you trust.\n", local)
	}
	report := runTestRehearsal(ctx, client, session, o)
	if err := saveTestRehearsalReport(o, report); err != nil {
		return printErr("Could not save rehearsal", err)
	}
	if jsonOutput {
		if code := rehearsalJSONResult(report); code != 0 {
			return code
		}
	} else {
		printTestReportComparison(osStdout, report.Comparison)
		_, _ = fmt.Fprintf(osStdout, "Rehearsal: %s · combined receipt: %s\n", report.Status, o.Report)
	}
	if report.Status != "passed" {
		return 1
	}
	return 0
}

func rehearsalJSONResult(value any) int {
	if err := writeJSON(value); err != nil {
		return printErr("Could not print rehearsal", err)
	}
	return 0
}

func validateTestRehearsalOptions(o testRehearsalOptions, profileSet bool) error {
	if o.Base == "" || o.Candidate == "" || o.Scenario == "" {
		return errors.New("--base, --candidate, and --scenario are required")
	}
	if o.Repeat < 1 || o.Repeat > 20 {
		return errors.New("--repeat must be between 1 and 20")
	}
	if o.Engine != "real-vm" && o.Engine != "local" && o.Engine != "simulated" {
		return errors.New("--engine must be real-vm, local, or simulated")
	}
	if o.Engine != "real-vm" && (profileSet || o.MaxWorkloadMinutes != 0) {
		return errors.New("--profile and --max-workload-minutes apply only to real-vm rehearsals")
	}
	if _, err := selectedTestProfiles(o.Profile); err != nil {
		return err
	}
	if o.Engine == "real-vm" && o.MaxWorkloadMinutes <= 0 {
		return errors.New("real-vm rehearsals require --max-workload-minutes for both revisions together")
	}
	if o.Validate && (o.Report != "" || o.HTML != "" || o.Markdown != "") {
		return errors.New("--validate does not execute scenarios or write reports")
	}
	if !o.Validate && o.Report == "" {
		return errors.New("execution requires --report for the combined receipt")
	}
	return validateDistinctTestReportPaths(o.Report, o.HTML, o.Markdown)
}

// Output reservations fail before executing any application and never replace
// existing files, including source files, symlinks and previous receipts.
func reserveTestRehearsalOutputs(paths ...string) error {
	var created []string
	for _, path := range paths {
		if path == "" {
			continue
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			err = file.Close()
			created = append(created, path)
		}
		if err != nil {
			for _, name := range created {
				_ = os.Remove(name)
			}
			return fmt.Errorf("reserve new output %q: %w", path, err)
		}
	}
	return nil
}

// testRehearsalLocalExecution says what a rehearsal runs on the invoking
// machine, or "" when nothing does. Recipe commands run inside each extracted
// revision with the caller's environment, so a candidate's scripts run here
// even when its app runs in microVMs.
func testRehearsalLocalExecution(scenario testScenario, engine string) string {
	if engine != "real-vm" {
		return "both revisions and the scenario's commands run on this machine"
	}
	local := len(scenario.Simulation) > 0 || scenario.CommandExecution == nil &&
		(len(scenario.Setup) > 0 || len(scenario.Trigger) > 0 || len(scenario.Command) > 0 || len(scenario.Cleanup) > 0)
	for _, step := range scenario.Steps {
		local = local || len(step.Command) > 0
	}
	if !local {
		return ""
	}
	return "the scenario's setup, trigger, step and cleanup commands run on this machine"
}

func runTestRehearsal(ctx context.Context, client *Client, session *testRehearsalSession, o testRehearsalOptions) testRehearsalReport {
	report := testRehearsalReport{testRehearsalIdentity: session.identity, StartedAt: time.Now().UTC()}
	profiles := []string{o.Engine}
	if o.Engine == "real-vm" {
		profiles, _ = selectedTestProfiles(o.Profile)
	}
	// A capability the platform has not enabled refuses every slot the same
	// way, so the first refusal skips the rest instead of creating and
	// destroying sessions for each.
	ctx, refuse := context.WithCancelCause(ctx)
	defer refuse(nil)
	run := func(side int, profile string, attempt int) testRunReceipt {
		receipt := session.run(ctx, client, o, side, profile, attempt)
		if strings.Contains(receipt.Error, api.CodeRehearsalNotEnabled) {
			refuse(errors.New("the platform has not enabled a rehearsal capability this scenario needs"))
		}
		return receipt
	}
	for attempt := 1; attempt <= o.Repeat; attempt++ {
		for _, profile := range profiles {
			report.BaselineRuns = append(report.BaselineRuns, run(0, profile, attempt))
			report.CandidateRuns = append(report.CandidateRuns, run(1, profile, attempt))
		}
	}
	// Remove all extracted application and trusted input files before deciding
	// success. Hosted resources have already gone through each runner's cleanup.
	if err := os.RemoveAll(session.root); err != nil {
		report.CleanupError = err.Error()
	}
	report.FinishedAt = time.Now().UTC()
	var err error
	report.Comparison, err = compareTestReports(report.Baseline.Commit, report.Candidate.Commit, report.BaselineRuns, report.CandidateRuns)
	if err != nil {
		report.Error = err.Error()
	}
	report.Status = report.Comparison.BehaviorStatus
	if report.Comparison.ActionStatus == "inconclusive" {
		report.Status = "inconclusive"
	} else if report.Comparison.ActionStatus == "failed" && report.Status == "passed" {
		report.Status = "failed"
	}
	if report.Comparison.ObjectStatus == "inconclusive" {
		report.Status = "inconclusive"
	} else if report.Comparison.ObjectStatus == "failed" && report.Status == "passed" {
		report.Status = "failed"
	}
	if report.Comparison.PostgresStatus == "inconclusive" {
		report.Status = "inconclusive"
	} else if report.Comparison.PostgresStatus == "failed" && report.Status == "passed" {
		report.Status = "failed"
	}
	if report.Status == "" || report.CleanupError != "" || err != nil {
		report.Status = "inconclusive"
	}
	report.Comparison.Rehearsal = &report.testRehearsalIdentity
	return report
}

func (s *testRehearsalSession) run(ctx context.Context, client *Client, o testRehearsalOptions, side int, profile string, attempt int) (receipt testRunReceipt) {
	receipt = testRunReceipt{
		Scenario: o.Scenario, Engine: o.Engine, Profile: profile, Attempt: attempt,
		Status: "failed", StartedAt: time.Now().UTC(), Behavior: newTestBehaviorEvidence(s.scenario.Behavior),
	}
	defer func() {
		receipt.Attempt = attempt
		if receipt.FinishedAt.IsZero() {
			receipt.FinishedAt = time.Now().UTC()
			receipt.DurationMS = receipt.FinishedAt.Sub(receipt.StartedAt).Milliseconds()
		}
		printTestRunResult(receipt, o.Repeat)
	}()
	if ctx.Err() != nil {
		receipt.Status, receipt.SkipReason = "skipped", "rehearsal interrupted: "+context.Cause(ctx).Error()
		return receipt
	}
	if !jsonOutput {
		label := "baseline"
		if side == 1 {
			label = "candidate"
		}
		_, _ = fmt.Fprintf(osStdout, "Rehearsing %s (%s, attempt %d/%d)\n", label, profile, attempt, o.Repeat)
	}
	dir, err := os.MkdirTemp(s.root, "run-")
	if err != nil {
		receipt.Error = fmt.Sprintf("create revision run directory: %v", err)
		return receipt
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			receipt.addCleanupError("remove revision source: " + err.Error())
		}
	}()
	manifestDir, digest, err := s.materialize(side, dir)
	if err != nil {
		receipt.Error = fmt.Sprintf("materialize pinned revision: %v", err)
		return receipt
	}
	want := s.identity.Baseline.SourceSHA256
	if side == 1 {
		want = s.identity.Candidate.SourceSHA256
	}
	if digest != want {
		receipt.Error = "materialized revision no longer matches its pinned source digest"
		return receipt
	}
	p := &testPreparedScenario{Name: o.Scenario, Scenario: s.scenario, ManifestDir: manifestDir, Engine: o.Engine}
	receipt = executeTestRunPlan(ctx, client, testRunPlan{Prepared: p, Profile: profile, Attempt: attempt})
	return receipt
}

func saveTestRehearsalReport(o testRehearsalOptions, report testRehearsalReport) error {
	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(o.Report, append(body, '\n'), 0o600); err != nil {
		return err
	}
	if o.HTML != "" {
		if err := writeTestComparisonHTML(o.HTML, report.Comparison); err != nil {
			return err
		}
	}
	if o.Markdown != "" {
		return writeTestComparisonMarkdown(o.Markdown, report.Comparison)
	}
	return nil
}

func prepareTestRehearsal(ctx context.Context, o testRehearsalOptions) (*testRehearsalSession, error) {
	root, err := os.MkdirTemp("", "gregale-rehearsal-")
	if err != nil {
		return nil, err
	}
	// rehearsalRelativePath compares symlink-resolved paths, so the session
	// root must be resolved too: macOS's default TMPDIR is below the /var
	// symlink, which made every extracted source look outside the checkout.
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		_ = os.RemoveAll(root)
		return nil, err
	}
	root = resolved
	s := &testRehearsalSession{root: root, trusted: filepath.Join(root, "trusted")}
	if err := s.prepare(ctx, o); err != nil {
		_ = os.RemoveAll(root)
		return nil, err
	}
	s.identity.ID, s.identity.Version, s.identity.Status = rand.Text(), 1, "validated"
	return s, nil
}
