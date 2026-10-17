package ci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const JobsRunnerPath = "/usr/local/bin/gregale-ci-runner"
const JobsRunnerProtocol = "v1"

var ErrDispatchDisabled = errors.New("CI Jobs dispatch is disabled or unavailable")

// JobsExecution freezes the repository authority and materialized job images at
// fresh admission. It contains no installation token, URL, or inherited env.
type JobsExecution struct {
	Binding   GitHubBinding           `json:"binding"`
	Templates map[string]JobsTemplate `json:"templates"`
}

type JobsTemplate struct {
	ID         string `json:"id"`
	Image      string `json:"image"`
	Digest     string `json:"digest"`
	StorageKey string `json:"storage_key"`
	RAMMB      int    `json:"ram_mb"`
}

type JobsSubmitRequest struct {
	automation      *AutomationEvent
	automationHead  *automationHead
	automationPR    *automationPullRequest
	automationPaths *AutomationPathDecision
	rerun           *executionRerunRequest
	captureChecks   bool // set only by fresh, gated admission; never client JSON
	SubmitRequest
	ProfileID string            `json:"profile_id,omitempty"`
	BindingID string            `json:"binding_id"`
	Templates map[string]string `json:"templates"` // CI job name -> account-owned Jobs template UUID
}

// JobsStore is an apid intent boundary. The PostgreSQL implementation atomically
// fences the attempt and inserts/cancels the ordinary Jobs run. It never writes
// instances, claims tasks, invokes vmmd, or executes a customer command.
type JobsStore interface {
	ExecutionStore
	GitHubBindings(context.Context, string) ([]GitHubBinding, error)
	submitJobs(context.Context, string, string, JobsSubmitRequest, GitHubBinding) (Execution, bool, error)
	prepareJobsAttempt(context.Context, StartRequest) (jobsIntent, error)
	dispatchJobsAttempt(context.Context, AttemptKey, SourceArchive) error
	cancelJobsAttempt(context.Context, AttemptKey) error
	observeJobsAttempt(context.Context, AttemptKey) (jobsIntent, jobsTask, error)
	finishJobsOutput(context.Context, AttemptKey, Observation) (Observation, error)
}

// AdmitJobs admits a fresh Jobs execution. The internal HTTP service adds worker,
// storage and HTTPS gates; blocked receipts and qualification records stay separate.
func AdmitJobs(ctx context.Context, cfg Config, store JobsStore, verify GitHubVerifier, account, key string, req JobsSubmitRequest) (Execution, bool, error) {
	if !cfg.DispatchAllows(account) || store == nil || verify == nil {
		return Execution{}, false, ErrDispatchDisabled
	}
	if req.automation != nil && !cfg.AutomationAllows(account) {
		return Execution{}, false, ErrDispatchDisabled
	}
	req.captureChecks = cfg.ExecutionChecksEnabled && cfg.GitHubChecksEnabled
	ctx, cancel := context.WithTimeout(ctx, time.Duration(api.CIGitHubRPCSeconds)*time.Second)
	defer cancel()
	req, err := resolveProfileSubmission(ctx, store, account, req)
	if err != nil {
		return Execution{}, false, err
	}
	if err := validateSubmission(key, req.SubmitRequest); err != nil {
		return Execution{}, false, err
	}
	if !ValidID(req.BindingID) || len(req.Templates) == 0 || len(req.Templates) > api.CIMaxJobs {
		return Execution{}, false, ErrConflict
	}
	for name, id := range req.Templates {
		if !ValidName(name) || !ValidID(id) {
			return Execution{}, false, ErrConflict
		}
	}
	bindings, err := store.GitHubBindings(ctx, account)
	if err != nil {
		return Execution{}, false, err
	}
	for _, binding := range bindings {
		if binding.ID != req.BindingID || !binding.Enabled || binding.RevisionID != req.RevisionID {
			continue
		}
		if err := verify(ctx, account, binding.InstallationID, binding.RepositoryID, binding.Repository); err != nil {
			return Execution{}, false, err
		}
		return store.submitJobs(ctx, account, key, req, binding)
	}
	return Execution{}, false, ErrNotFound
}

// SourceArchive refers to a bounded, immutable, exact-SHA archive in the
// attempt's account prefix. GitHub credentials never cross this boundary.
type SourceArchive struct {
	Key    string `json:"key"`
	Digest string `json:"digest"`
	Bytes  int64  `json:"bytes"`
}

type JobsSourceRequest struct {
	Key        AttemptKey
	Binding    GitHubBinding
	Repository string
	CommitSHA  string
}

type JobsSource interface {
	Stage(context.Context, JobsSourceRequest) (SourceArchive, error)
}

// JobsOutput verifies the guest's independently published result and artifact
// bytes even on failure/cancellation. Jobs' success-only output_manifest is
// insufficient for this contract. No terminal output is requested before the
// scheduler confirms instance cleanup. Progress may be retained until exit.
type JobsOutput interface {
	Collect(context.Context, JobsSourceRequest, PlannedJob, string, *int) (Observation, error)
}

type JobsBackend struct {
	Config Config
	Store  JobsStore
	Source JobsSource
	Output JobsOutput
}

type jobsIntent struct {
	Request         StartRequest
	Binding         GitHubBinding
	Template        JobsTemplate
	RunID           string
	Cancelled       bool
	Output          *Observation
	Progress        *JobsProgress
	RuntimeUnsealed bool
	RuntimeSealed   bool
}

type jobsTask struct {
	Status      string
	ExitCode    *int
	Stopped     bool
	HadInstance bool
}

type JobsManifest struct {
	Inputs       []JobsInput   `json:"inputs,omitempty"`
	Version      int           `json:"version"`
	AccountID    string        `json:"account_id"`
	ExecutionID  string        `json:"execution_id"`
	AttemptID    string        `json:"attempt_id"`
	Repository   string        `json:"repository"`
	CommitSHA    string        `json:"commit_sha"`
	Deadline     time.Time     `json:"deadline"`
	Source       SourceArchive `json:"source"`
	Job          PlannedJob    `json:"job"`
	OutputPrefix string        `json:"output_prefix"`
}

func jobsManifest(req StartRequest, source SourceArchive, inputs ...JobsInput) ([]byte, error) {
	if req.Job.Matrix != nil {
		return nil, ErrBackendProtocol
	}
	manifest := JobsManifest{Version: jobsManifestVersion(req.Job.Job), AccountID: req.Key.AccountID, ExecutionID: req.Key.ExecutionID,
		AttemptID: req.Key.AttemptID, Repository: req.Repository, CommitSHA: req.CommitSHA,
		Deadline: req.Deadline, Source: source, Job: req.Job, OutputPrefix: jobsObjectPrefix(req.Key) + "output/"}
	manifest.Inputs = inputs
	// Report selectors belong to the controller; guests publish normal artifacts.
	manifest.Job.JUnit = nil
	raw, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	if len(raw) > api.CIJobsManifestMaxBytes {
		return nil, &LimitError{Resource: "jobs_manifest_bytes", Limit: api.CIJobsManifestMaxBytes, Observed: len(raw)}
	}
	return raw, nil
}

func (b JobsBackend) Start(ctx context.Context, req StartRequest) error {
	if !b.Config.DispatchAllows(req.Key.AccountID) || b.Store == nil || b.Source == nil || b.Output == nil {
		return ErrDispatchDisabled
	}
	intent, err := b.Store.prepareJobsAttempt(ctx, req)
	if err != nil || intent.Cancelled || intent.RunID != "" || intent.Output != nil {
		return err
	}
	source, err := b.Source.Stage(ctx, jobsSourceRequest(intent))
	if err != nil {
		// Provider details can contain request URLs. Keep them out of engine errors.
		return errors.New("CI source staging unavailable")
	}
	if err := validateSourceArchive(req.Key, source); err != nil {
		return err
	}
	return b.Store.dispatchJobsAttempt(ctx, req.Key, source)
}

func (b JobsBackend) Cancel(ctx context.Context, key AttemptKey) error {
	// Disabling new admission must never prevent cleanup of an existing attempt.
	if b.Store == nil {
		return ErrDispatchDisabled
	}
	return b.Store.cancelJobsAttempt(ctx, key)
}

func (b JobsBackend) Observe(ctx context.Context, req ObserveRequest) (Observation, error) {
	if b.Store == nil || req.LogOffset < 0 || req.LogOffset > api.CILogMaxBytesPerAttempt || req.LogLimit < 0 || req.LogLimit > api.CILogPageMaxBytes {
		return Observation{}, ErrLogCursor
	}
	intent, task, err := b.Store.observeJobsAttempt(ctx, req.Key)
	if err != nil {
		return Observation{}, err
	}
	if intent.Output != nil {
		return jobsLogPage(*intent.Output, req)
	}
	if intent.RunID == "" && intent.Cancelled {
		return Observation{Status: StatusCancelled, LogOffset: req.LogOffset}, nil
	}
	if !task.Stopped {
		if intent.Progress != nil {
			return progressPage(*intent.Progress, req)
		}
		steps := make([]StepResult, len(intent.Request.Job.Steps))
		for i, step := range intent.Request.Job.Steps {
			steps[i] = StepResult{Name: step.Name, Status: StatusQueued}
		}
		return Observation{Status: StatusRunning, Steps: steps, LogOffset: req.LogOffset}, nil
	}
	if task.Status == StatusCancelled && !task.HadInstance {
		// Native cleanup may already have cleared a former instance reference.
		// Retain any live prefix before freezing the cancellation receipt.
		result := Observation{Status: StatusCancelled}
		if intent.Progress != nil {
			result = incompleteProgressOutput(intent.Request.Job, task, intent.Progress, time.Now().UTC())
		}
		output, err := b.Store.finishJobsOutput(ctx, req.Key, result)
		if err != nil {
			return Observation{}, err
		}
		return jobsLogPage(output, req)
	}
	if _, required := b.Output.(interface{ runtimeSealRequired() }); required && !intent.RuntimeSealed {
		// Once native cleanup is certified, the guest can no longer seal.
		// Persist a platform failure instead of waiting forever or accepting
		// an unsealed success. Partial blobs remain available for recovery.
		output := incompleteProgressOutput(intent.Request.Job, task, intent.Progress, time.Now().UTC())
		output, err := b.Store.finishJobsOutput(ctx, req.Key, output)
		if err != nil {
			return Observation{}, err
		}
		return jobsLogPage(output, req)
	}
	if intent.RuntimeUnsealed {
		return Observation{}, ErrRuntimeAuthority
	}
	if b.Output == nil {
		return Observation{}, ErrDispatchDisabled
	}
	result, err := b.Output.Collect(ctx, jobsSourceRequest(intent), intent.Request.Job, task.Status, task.ExitCode)
	if err != nil {
		return Observation{}, errors.New("CI output collection unavailable")
	}
	if err := validateJobsOutput(intent.Request.Job, task, result); err != nil {
		return Observation{}, err
	}
	result, err = b.Store.finishJobsOutput(ctx, req.Key, result)
	if err != nil {
		return Observation{}, err
	}
	return jobsLogPage(result, req)
}

func incompleteRuntimeOutput(job PlannedJob, task jobsTask) Observation {
	code := 125
	output := Observation{Status: StatusFailed, ExitCode: &code,
		Logs: []byte("CI guest output was not sealed before native task cleanup; step results, logs and artifacts are incomplete.\n")}
	if task.Status == StatusCancelled {
		output.Status, output.ExitCode = StatusCancelled, task.ExitCode
	} else if task.ExitCode != nil && *task.ExitCode != 0 {
		output.ExitCode = task.ExitCode
	}
	for _, step := range job.Steps {
		output.Steps = append(output.Steps, StepResult{Name: step.Name, Status: StatusSkipped})
	}
	return output
}

func jobsSourceRequest(intent jobsIntent) JobsSourceRequest {
	return JobsSourceRequest{Key: intent.Request.Key, Binding: intent.Binding,
		Repository: intent.Request.Repository, CommitSHA: intent.Request.CommitSHA}
}

func jobsLogPage(result Observation, req ObserveRequest) (Observation, error) {
	if req.LogOffset > len(result.Logs) {
		return Observation{}, ErrLogCursor
	}
	end := min(len(result.Logs), req.LogOffset+req.LogLimit)
	result.MoreLogs = result.MoreLogs || end < len(result.Logs)
	result.Logs, result.LogOffset = append([]byte(nil), result.Logs[req.LogOffset:end]...), req.LogOffset
	return result, nil
}

func validateJobsOutput(plan PlannedJob, task jobsTask, result Observation) error {
	if !terminal(result.Status) || result.Status == StatusSkipped || result.LogOffset != 0 || len(result.Logs) > api.CILogMaxBytesPerAttempt ||
		(result.MoreLogs && len(result.Logs) != api.CILogMaxBytesPerAttempt) {
		return protocolError("invalid Jobs terminal output")
	}
	if (result.Status == StatusSucceeded) != (task.Status == "succeeded") ||
		(result.Status == StatusCancelled) != (task.Status == "cancelled") ||
		(task.ExitCode != nil && !sameExit(result.ExitCode, task.ExitCode)) {
		return protocolError("CI result disagrees with scheduler receipt")
	}
	previous := Attempt{Steps: make([]StepResult, len(plan.Steps))}
	for i, step := range plan.Steps {
		previous.Steps[i] = StepResult{Name: step.Name, Status: StatusQueued}
	}
	// Validate all output using the engine contract; log paging is checked separately.
	page := result
	page.Logs, page.MoreLogs = nil, false
	page = completeObservationReports(plan, page)
	return validateObservation(plan, previous, page)
}

func jobsAttemptFor(row Execution, key AttemptKey) (StartRequest, JobsTemplate, error) {
	if row.Mode != ExecutionJobs || row.Compute == nil || row.AccountID != key.AccountID || row.ID != key.ExecutionID || key.Fence < 1 {
		return StartRequest{}, JobsTemplate{}, ErrConflict
	}
	for i, job := range row.Jobs {
		if job.Name == key.JobName && job.Attempt != nil && job.Attempt.ID == key.AttemptID {
			template, ok := row.Compute.Templates[job.Name]
			if !ok {
				return StartRequest{}, JobsTemplate{}, ErrConflict
			}
			return StartRequest{Key: key, Repository: row.Plan.Repository, CommitSHA: row.CommitSHA,
				Job: clone(row.Plan.Jobs[i]), Deadline: job.Attempt.Deadline}, template, nil
		}
	}
	return StartRequest{}, JobsTemplate{}, ErrNotFound
}

func sameJobsRequest(expected, actual StartRequest) bool {
	return expected.Key == actual.Key && expected.Repository == actual.Repository && expected.CommitSHA == actual.CommitSHA &&
		expected.Deadline.Equal(actual.Deadline) && reflect.DeepEqual(expected.Job, actual.Job)
}

func jobsObjectPrefix(key AttemptKey) string {
	return fmt.Sprintf("ci/accounts/%s/executions/%s/attempts/%s/", key.AccountID, key.ExecutionID, key.AttemptID)
}
