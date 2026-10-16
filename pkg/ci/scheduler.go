package ci

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const RuntimeURLEnv = "FAAS_CI_RUNTIME_URL"

// JobsSchedulerStore is host-only. Ownership lookup must work with every gate
// off so the ordinary Jobs dispatcher cannot bypass CI authorization.
type JobsSchedulerStore interface {
	JobsRetryBlocked(context.Context, string) (bool, error)
	ValidateJobsLaunch(context.Context, Config, string, string, int) (bool, time.Time, error)
	IssueJobsRuntime(context.Context, Config, RuntimeHostProof) (RuntimeProof, error)
	AuthorizeJobsRuntime(context.Context, Config, RuntimeProof, string) (RuntimeView, error)
}

func ValidRuntimeOrigin(origin string) bool {
	u, err := url.Parse(origin)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil &&
		(origin == "https://"+u.Host || origin == "https://"+u.Host+"/")
}

// ValidateJobsLaunch runs before native capacity or a task lease is acquired.
// Issuance and the held-start release repeat the durable policy check.
func (s *PostgresStore) ValidateJobsLaunch(ctx context.Context, cfg Config, account, runID string, index int) (bool, time.Time, error) {
	owned, err := s.JobsRetryBlocked(ctx, runID)
	if err != nil || !owned {
		return owned, time.Time{}, err
	}
	state, err := s.runtimeState(ctx, runID)
	if err != nil {
		return true, time.Time{}, err
	}
	defer func() { _ = state.tx.Rollback(ctx) }()
	if index != 0 || state.execution.AccountID != account || !cfg.DispatchAllows(account) || state.task.Status != "queued" ||
		state.task.Attempt != 1 || !s.runtimePolicy(ctx, state) {
		return true, time.Time{}, ErrRuntimeAuthority
	}
	return true, state.manifest.Deadline, storeError(state.tx.Commit(ctx))
}

func (s *PostgresStore) runtimeRunMatches(ctx context.Context, state runtimeState) bool {
	run, err := s.q.CIJobsLaunchSnapshot(ctx, state.tx, dbUUID(state.manifest.AttemptID))
	if err != nil {
		return false
	}
	template := state.execution.Compute.Templates[state.attempt.JobName]
	raw, err := json.Marshal(state.manifest)
	if err != nil {
		return false
	}
	return uuidString(run.AccountID) == state.manifest.AccountID && uuidString(run.JobID) == template.ID && run.Tasks == 1 && run.Parallelism == 1 &&
		run.RetryMax.Valid && run.RetryMax.Int32 == 0 && run.TaskTimeoutS.Valid && int(run.TaskTimeoutS.Int32) == state.manifest.Job.TimeoutSeconds &&
		run.ImageRefSnapshot.String == template.Image && run.ImageResolvedDigestSnapshot.String == template.Digest && run.ImageStorageKeySnapshot.String == template.StorageKey &&
		run.RamMbSnapshot.Valid && run.RamMbSnapshot.Int32 == api.CIJobsRAMMB && run.StartDeadlineAt.Valid && run.StartDeadlineAt.Time.Equal(state.manifest.Deadline.Truncate(time.Microsecond)) &&
		slices.Equal(run.Command, []string{JobsRunnerPath, JobsRunnerProtocol, base64.StdEncoding.EncodeToString(raw)}) &&
		emptyRuntimeEnv(run.EffectiveEnvSnapshot) && emptyRuntimeEnv(run.EnvOverrides) && len(run.FailureRules) == 0 && run.ExecutionClass == "standard"
}

func emptyRuntimeEnv(raw []byte) bool {
	if len(raw) == 0 {
		return true
	}
	var env map[string]string
	return json.Unmarshal(raw, &env) == nil && len(env) == 0
}
