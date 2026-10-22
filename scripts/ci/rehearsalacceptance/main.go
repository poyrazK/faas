// Recompute the running-import journey verdict from real CLI receipts.
// adr: 20261018000100
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"syscall"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/actioncompare"
	"github.com/onebox-faas/faas/pkg/behaviorcompare"
	"github.com/onebox-faas/faas/pkg/rehearsalobj"
	"github.com/onebox-faas/faas/pkg/rehearsalpg"
	"github.com/onebox-faas/faas/pkg/rehearsalresume"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
)

const csv = "external_id,state\nimport-001,complete\n"
const notificationBody = `{"from":"imports@example.test","to":["customer@example.test"],"subject":"Import complete","text":"import-001"}`

var digest = regexp.MustCompile(`^[0-9a-f]{64}$`)
var commit = regexp.MustCompile(`^[0-9a-f]{40}$`)

// A projection of the CLI report: evidence uses the public production types.
type revision struct {
	Commit        string `json:"commit"`
	Tree          string `json:"tree"`
	ArchiveSHA256 string `json:"archive_sha256"`
	SourceSHA256  string `json:"source_sha256"`
}

type report struct {
	Version        int             `json:"version"`
	ID             string          `json:"id"`
	Status         string          `json:"status"`
	Scenario       string          `json:"scenario"`
	ManifestSHA256 string          `json:"manifest_sha256"`
	RecipeSHA256   string          `json:"recipe_sha256"`
	TrustedFiles   json.RawMessage `json:"trusted_files"`
	Error          string          `json:"error"`
	CleanupError   string          `json:"cleanup_error"`
	Baseline       revision        `json:"baseline"`
	Candidate      revision        `json:"candidate"`
	BaselineRuns   []run           `json:"baseline_runs"`
	CandidateRuns  []run           `json:"candidate_runs"`
	Comparison     struct {
		BehaviorStatus string `json:"behavior_status"`
		ActionStatus   string `json:"action_status"`
		ObjectStatus   string `json:"object_status"`
		PostgresStatus string `json:"postgres_status"`
	} `json:"comparison"`
}

type commandReceipt struct {
	Stage           string `json:"stage"`
	TaskID          string `json:"task_id"`
	DeploymentID    string `json:"deployment_id"`
	SpecSHA256      string `json:"spec_sha256"`
	ReceiptSHA256   string `json:"receipt_sha256"`
	Status          string `json:"status"`
	ExitCode        *int   `json:"exit_code"`
	OutputTruncated bool   `json:"output_truncated"`
}

type run struct {
	Scenario              string                    `json:"scenario"`
	Engine                string                    `json:"engine"`
	Profile               string                    `json:"profile"`
	Status                string                    `json:"status"`
	Attempt               int                       `json:"attempt"`
	RunID                 string                    `json:"run_id"`
	AppSlug               string                    `json:"app_slug"`
	DeploymentID          string                    `json:"deployment_id"`
	SourceSHA256          string                    `json:"source_sha256"`
	Error                 string                    `json:"error"`
	CleanupError          string                    `json:"cleanup_error"`
	SkipReason            string                    `json:"skip_reason"`
	Services              map[string]string         `json:"services"`
	WorkloadAppIDs        map[string]string         `json:"workload_app_ids"`
	WorkloadDeploymentIDs map[string]string         `json:"workload_deployment_ids"`
	Commands              []commandReceipt          `json:"commands"`
	SuspendResume         *rehearsalresume.Evidence `json:"suspend_resume"`
	Behavior              *behaviorcompare.Evidence `json:"behavior"`
	Postgres              *rehearsalpg.Evidence     `json:"postgres"`
	Objects               *rehearsalobj.Evidence    `json:"objects"`
	Actions               *struct {
		PolicyVersion       int                     `json:"policy_version"`
		Status              string                  `json:"status"`
		Network             string                  `json:"network"`
		Webhooks            string                  `json:"webhooks"`
		SimulationSHA256    string                  `json:"simulation_sha256"`
		ManagedWorkSHA256   string                  `json:"managed_work_sha256"`
		SuspendResumeSHA256 string                  `json:"suspend_resume_sha256"`
		PostgresFaultSHA256 string                  `json:"postgres_fault_sha256"`
		Evidence            *actioncompare.Evidence `json:"evidence"`
	} `json:"actions"`
	Invocations []struct {
		ID       string `json:"id"`
		Service  string `json:"service"`
		State    string `json:"state"`
		Attempts int    `json:"attempts"`
	} `json:"invocations"`
}

func validID(s string) bool { u, err := uuid.Parse(s); return err == nil && u != uuid.Nil }

func readReport(path string) (report, error) {
	var r report
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return r, err
	}
	defer func() { _ = root.Close() }()
	f, err := root.OpenFile(filepath.Base(path), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return r, err
	}
	defer func() { _ = f.Close() }()
	metadata, err := f.Stat()
	if err != nil || !metadata.Mode().IsRegular() {
		return r, errors.New("report must be a regular file")
	}
	const max = 16 << 20
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil || len(b) > max {
		return r, errors.New("report unreadable or exceeds 16 MiB")
	}
	if b = bytes.TrimSpace(b); len(b) == 0 || b[0] != '{' {
		return r, errors.New("report must be a JSON object")
	}
	// Also reject duplicate keys and trailing documents before projecting fields.
	if _, err = behaviorcompare.CanonicalJSON(b); err != nil {
		return r, fmt.Errorf("ambiguous report JSON: %w", err)
	}
	err = json.Unmarshal(b, &r)
	return r, err
}

func validateRunJourney(r run, account string, notifications int, journey string) error {
	// The deployment source SHA hashes the upload, while revision.SourceSHA256
	// hashes the full materialized Git tree. These are distinct contracts.
	if r.Scenario != "running-import" || r.Engine != "real-vm" || r.Profile != "warm" || r.Attempt != 1 || r.Status != "passed" || r.Error != "" || r.CleanupError != "" || r.SkipReason != "" || !digest.MatchString(r.SourceSHA256) || r.AppSlug == "" || len(r.Services) != 1 || r.Services["worker"] == "" || len(r.WorkloadAppIDs) != 2 || len(r.WorkloadDeploymentIDs) != 2 {
		return errors.New("run is not one complete warm native journey with confirmed cleanup")
	}
	for _, name := range []string{"import-api", "worker"} {
		if !validID(r.WorkloadAppIDs[name]) || !validID(r.WorkloadDeploymentIDs[name]) {
			return errors.New("missing workload identity")
		}
	}
	if r.DeploymentID != r.WorkloadDeploymentIDs["import-api"] {
		return errors.New("primary deployment differs")
	}
	if r.Actions == nil || r.Actions.Status != "applied" || r.Actions.PolicyVersion != 1 || r.Actions.Network != "contained" || r.Actions.Webhooks != "capture" || r.Actions.Evidence == nil || actioncompare.Compare(r.Actions.Evidence, r.Actions.Evidence).Status != "passed" {
		return errors.New("incomplete contained action evidence")
	}
	s := r.Actions.Evidence.Snapshot
	if s.RunID != r.RunID || s.ManagedWork == nil || s.ManagedWork.Validate() != nil || s.ManagedWork.RunID != r.RunID || r.Actions.ManagedWorkSHA256 != s.ManagedWork.PlanSHA256 || r.Actions.SimulationSHA256 != s.SimulationSHA256 || len(s.Intents) != 0 || s.SuppressedDeliveries != 0 || s.RecorderSessions < int64(len(r.Commands)+len(s.Workloads)) {
		return errors.New("sealed checkpoint or managed-work journal differs")
	}
	p, err := journeyCheckpoint(r, account, journey)
	if err != nil {
		return err
	}
	work := s.ManagedWork
	if work.Plan.Primary != "import-api" || work.Plan.WorkerTermination != nil || len(work.Jobs) != 1 || len(work.Bindings) != 2 {
		return errors.New("unsupported worker inventory")
	}
	j := work.Jobs[0]
	if j.ID != p.InvocationID || j.Method != "POST" || j.Path != "/import" || j.PayloadSHA256 != rehearsalwork.PayloadHash([]byte(`{"external_id":"import-001"}`)) || len(j.Attempts) != 2 {
		return errors.New("fixture did not execute exactly one import with a real retry")
	}
	for _, a := range j.Attempts {
		if a.InstanceID != p.InstanceID {
			return errors.New("retry replaced the resumed VM")
		}
	}
	if len(r.Invocations) != 1 || r.Invocations[0].ID != j.ID || r.Invocations[0].Service != "worker" || r.Invocations[0].State != "completed" || r.Invocations[0].Attempts != 2 {
		return errors.New("CLI invocation observation differs from the independent journal")
	}
	if r.Postgres == nil || r.Objects == nil || r.Postgres.Validate() != nil || r.Objects.Validate() != nil || !r.Postgres.LockBudgetPassed {
		return errors.New("independent resource collection or cleanup is incomplete")
	}
	for _, b := range work.Bindings {
		if b.AppID != r.WorkloadAppIDs[b.Workload] || b.DeploymentID != r.WorkloadDeploymentIDs[b.Workload] || b.PostgresResourceID != r.Postgres.ResourceID || b.ObjectsResourceID != r.Objects.ResourceID {
			return errors.New("resource or deployment binding differs")
		}
	}
	if r.Postgres.RunID != r.RunID || r.Objects.RunID != r.RunID || r.Postgres.AppID != r.WorkloadAppIDs["import-api"] || r.Objects.AppID != r.WorkloadAppIDs["import-api"] || r.Postgres.DeploymentID != r.DeploymentID || r.Objects.DeploymentID != r.DeploymentID {
		return errors.New("resource collector belongs to another run")
	}
	if len(r.Postgres.Before) != 1 || len(r.Postgres.After) != 1 || r.Postgres.Before[0].Table != "public.imports" || r.Postgres.After[0].Table != "public.imports" || len(r.Postgres.Before[0].Rows) != 1 || len(r.Postgres.After[0].Rows) != 1 || !sameJSON(r.Postgres.Before[0].Rows[0], []byte(`{"external_id":"import-001","state":"pending"}`)) || !sameJSON(r.Postgres.After[0].Rows[0], []byte(`{"external_id":"import-001","state":"complete"}`)) {
		return errors.New("unexpected initial or final committed database rows")
	}
	if len(r.Objects.Before) != 0 || len(r.Objects.After) != 1 || len(r.Objects.Mutations) != 1 {
		return errors.New("expected exactly one independently observed object upload")
	}
	o, m := r.Objects.After[0], r.Objects.Mutations[0]
	if o.Key != "exports/import-001.csv" || o.Size != int64(len(csv)) || o.SHA256 != rehearsalobj.HashBytes([]byte(csv)) || o.ContentType != "text/csv" || m.Method != "PUT" || m.Status != 200 || m.Workload != "worker" {
		return errors.New("generated object bytes or mutation actor differs")
	}
	checkpoint, notification := 0, 0
	ordinals := map[int]bool{}
	for _, a := range s.SimulatedAttempts {
		if a.Workload != "worker" || a.Status != "completed" || a.ResponseStatus != 200 || a.Fault != "" {
			return errors.New("simulation has an unexpected outcome")
		}
		switch a.Fixture {
		case "after-commit":
			checkpoint++
			if a.ID != p.CheckpointAttemptID || a.Integration != "checkpoint" || a.Adapter != "http" || a.Method != "POST" || a.Path != "/after-commit" || a.Attempt != 1 || a.RequestSHA256 != rehearsalwork.PayloadHash([]byte(`{}`)) {
				return errors.New("held response differs from original checkpoint attempt")
			}
		case "notification":
			notification++
			if a.Integration != "email" || a.Adapter != "resend" || a.Method != "POST" || a.Path != "/emails" || a.IdempotencySHA256 != rehearsalwork.Hash([]byte("import-001:notification")) || a.RequestSHA256 != rehearsalwork.PayloadHash([]byte(notificationBody)) || !sameJSON(a.Payload, []byte(`{"subject":"Import complete","to":["customer@example.test"]}`)) || a.Attempt < 1 || a.Attempt > notifications || ordinals[a.Attempt] {
				return errors.New("unexpected notification destination or idempotency key")
			}
			ordinals[a.Attempt] = true
		default:
			return errors.New("unexpected simulated external action")
		}
	}
	if checkpoint != 1 || notification != notifications {
		return errors.New("checkpoint or notification multiplicity differs")
	}
	if r.Behavior == nil || behaviorcompare.Compare(r.Behavior, r.Behavior).Status != "passed" || len(r.Behavior.Observations) != 1 || r.Behavior.Observations[0].Key != "api.ready" || !sameJSON(r.Behavior.Observations[0].Value, []byte(`{"ready":true}`)) {
		return errors.New("missing application readiness observation")
	}
	if len(r.Commands) != 3 {
		return errors.New("contained setup, trigger and observer receipts required")
	}
	for i, stage := range []string{"setup", "trigger", "assertion"} {
		c := r.Commands[i]
		if c.Stage != stage || !validID(c.TaskID) || c.DeploymentID != r.DeploymentID || c.Status != "succeeded" || c.ExitCode == nil || *c.ExitCode != 0 || c.OutputTruncated || !digest.MatchString(c.SpecSHA256) || !digest.MatchString(c.ReceiptSHA256) {
			return errors.New("contained command did not finish with an intact receipt")
		}
	}
	return nil
}

func sameJSON(a, b []byte) bool {
	x, e1 := behaviorcompare.CanonicalJSON(a)
	y, e2 := behaviorcompare.CanonicalJSON(b)
	return e1 == nil && e2 == nil && string(x) == string(y)
}

func trustedInputs(raw json.RawMessage) (map[string]string, error) {
	var files []struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	}
	expected := map[string]bool{"commands.mjs": true, "input.json": true, "fixture.sql": true}
	if json.Unmarshal(raw, &files) != nil || len(files) != len(expected) {
		return nil, errors.New("missing trusted fixture or observer inventory")
	}
	hashes := map[string]string{}
	for _, f := range files {
		if !expected[f.Path] || !digest.MatchString(f.SHA256) || hashes[f.Path] != "" {
			return nil, errors.New("invalid trusted input path or hash")
		}
		hashes[f.Path] = f.SHA256
	}
	return hashes, nil
}

func verifyJourney(control, regression report, baseline, candidate, account, journey string) error {
	if !commit.MatchString(baseline) || !commit.MatchString(candidate) || baseline == candidate || !validID(account) {
		return errors.New("distinct pinned fixture commits and an acceptance account are required")
	}
	seen := map[string]bool{}
	for index, r := range []report{control, regression} {
		inputs, err := trustedInputs(r.TrustedFiles)
		if err != nil {
			return err
		}
		want, actions, revisionSHA := "passed", "passed", baseline
		if index == 1 {
			want, actions, revisionSHA = "failed", "failed", candidate
		}
		if r.Version != 1 || r.ID == "" || r.Status != want || r.Error != "" || r.CleanupError != "" || r.Scenario != "running-import" || len(r.BaselineRuns) != 1 || len(r.CandidateRuns) != 1 || r.Baseline.Commit != baseline || r.Candidate.Commit != revisionSHA || !digest.MatchString(r.ManifestSHA256) || !digest.MatchString(r.RecipeSHA256) || string(r.TrustedFiles) == "null" || len(r.TrustedFiles) == 0 {
			return errors.New("rehearsal identity, pins, status or cleanup differs")
		}
		for side, source := range []revision{r.Baseline, r.Candidate} {
			if !commit.MatchString(source.Tree) || !digest.MatchString(source.ArchiveSHA256) || !digest.MatchString(source.SourceSHA256) {
				return errors.New("missing source/archive provenance")
			}
			run := r.BaselineRuns[0]
			notifications := 1
			if side == 1 {
				run = r.CandidateRuns[0]
				notifications += index
			}
			if err := validateRunJourney(run, account, notifications, journey); err != nil {
				return fmt.Errorf("report %d side %d: %w", index, side, err)
			}
			if run.Postgres.FixtureSHA256 != inputs["fixture.sql"] {
				return errors.New("database fixture differs from the pinned SQL input")
			}
			p, _ := journeyCheckpoint(run, account, journey)
			ids := []string{run.RunID, run.AppSlug, run.Services["worker"], run.Postgres.ResourceID, run.Objects.ResourceID, p.ID, p.InstanceID, p.RecorderSessionID, p.InvocationID, p.CheckpointAttemptID}
			for _, c := range run.Commands {
				ids = append(ids, c.TaskID)
			}
			for _, id := range ids {
				if seen[id] {
					return errors.New("an execution or fixture identity was reused across runs")
				}
				seen[id] = true
			}
		}
		a, b := r.BaselineRuns[0], r.CandidateRuns[0]
		comparison := actioncompare.Compare(a.Actions.Evidence, b.Actions.Evidence)
		if comparison.Status != actions || behaviorcompare.Compare(a.Behavior, b.Behavior).Status != "passed" || rehearsalpg.Compare(a.Postgres, b.Postgres).Status != "passed" || rehearsalobj.Compare(a.Objects, b.Objects).Status != "passed" || r.Comparison.ActionStatus != actions || r.Comparison.BehaviorStatus != "passed" || r.Comparison.PostgresStatus != "passed" || r.Comparison.ObjectStatus != "passed" {
			return errors.New("recomputed comparison does not isolate the expected notification regression")
		}
		for _, c := range comparison.Changes {
			if c.Status != "unchanged" && (index != 1 || c.Workload != "worker" || c.Source != "outbound" || c.Name != "notification" || len(c.Before) != 1 || len(c.After) != 2) {
				return errors.New("unexpected behavioral difference beyond notification multiplicity")
			}
		}
	}
	if control.ManifestSHA256 != regression.ManifestSHA256 || control.RecipeSHA256 != regression.RecipeSHA256 || !sameJSON(control.TrustedFiles, regression.TrustedFiles) || !reflect.DeepEqual(control.Baseline, control.Candidate) || !reflect.DeepEqual(control.Baseline, regression.Baseline) || control.Baseline.SourceSHA256 == regression.Candidate.SourceSHA256 {
		return errors.New("control and regression do not share pinned inputs and baseline")
	}
	return nil
}

func main() {
	controlPath := flag.String("control", "", "baseline versus baseline report")
	regressionPath := flag.String("regression", "", "baseline versus duplicate notification report")
	baseline := flag.String("baseline", "", "pinned fixture baseline commit")
	candidate := flag.String("candidate", "", "pinned fixture candidate commit")
	account := flag.String("account", "", "dedicated acceptance account UUID")
	journey := flag.String("journey", "running-job", "running-job or postgres-recovery")
	flag.Parse()
	control, err := readReport(*controlPath)
	if err == nil {
		var regression report
		regression, err = readReport(*regressionPath)
		if err == nil {
			err = verifyJourney(control, regression, *baseline, *candidate, *account, *journey)
		}
	}
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "rehearsal journey evidence rejected:", err)
		os.Exit(1)
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"version": 1, "status": "passed", "scope": *journey + "-journey-evidence"})
}
