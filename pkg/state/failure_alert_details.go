package state

// ADR-045 amendment 1: a failure rule can watch one cron, and a firing
// failure rule's notification names the failures it counted.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// FailedBoot is one failed VM boot a boot_failures rule counted.
type FailedBoot struct {
	At                           time.Time
	DeploymentID, NodeID, Method string
}

// FailedJobRun is one failed or dead-lettered job run a job_failures rule
// counted. FirstError is the lowest-indexed failed task's error message or
// class; empty when no task recorded one.
type FailedJobRun struct {
	ID, JobName, Status string
	Tasks, TasksFailed  int
	DeadLetters         int
	FinishedAt          time.Time
	FirstError          string
}

// FailedInvocation is one failed or dead-lettered invocation a
// failed_invocations rule counted.
type FailedInvocation struct {
	ID, Source, Path, CronID string
	Attempts                 int
	LastError                string
	FailedAt                 time.Time
}

// FailureAlertDetailer serves the one-cron count and the notification
// details of the failure alert metrics. It is optional: the evaluator
// degrades a cron-filtered rule on a store without it and sends
// notifications without details.
type FailureAlertDetailer interface {
	// CountFailedCronInvocations counts one cron's failed and
	// dead-lettered invocations since the time, in production or in the
	// stage environmentID names.
	CountFailedCronInvocations(ctx context.Context, accountID, appID, environmentID, cronID string, since time.Time) (int64, error)
	// ListAppBootFailures returns up to limit of the newest failed boots
	// CountAppBootFailures counts.
	ListAppBootFailures(ctx context.Context, appID string, since, until time.Time, limit int) ([]FailedBoot, error)
	// ListAccountFailedJobRuns returns up to limit of the newest runs
	// CountAccountFailedJobRuns counts.
	ListAccountFailedJobRuns(ctx context.Context, accountID, jobName string, since, until time.Time, limit int) ([]FailedJobRun, error)
	// ListFailedInvocations returns up to limit of the newest failed
	// invocations of one app, optionally of one source and one cron.
	ListFailedInvocations(ctx context.Context, accountID, appID, environmentID string, source AlertFailureSource, cronID string, since time.Time, limit int) ([]FailedInvocation, error)
}

var _ FailureAlertDetailer = (*PgStore)(nil)

func failureDetailUUID(id string) (pgtype.UUID, error) {
	if id == "" {
		return pgtype.UUID{}, nil
	}
	parsed, err := uuid.Parse(id)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("%w: %q is not a uuid", ErrInvalidArgument, id)
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}, nil
}

func failureDetailUUIDs(ids ...string) ([]pgtype.UUID, error) {
	out := make([]pgtype.UUID, len(ids))
	for i, id := range ids {
		v, err := failureDetailUUID(id)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

func failureDetailLimit(limit int) int32 {
	if limit < 1 {
		return 1
	}
	if limit > 50 {
		return 50
	}
	return int32(limit)
}

// CountFailedCronInvocations runs the sqlc CountFailedCronInvocations query.
func (s *PgStore) CountFailedCronInvocations(ctx context.Context, accountID, appID, environmentID, cronID string, since time.Time) (int64, error) {
	ids, err := failureDetailUUIDs(accountID, appID, environmentID, cronID)
	if err != nil {
		return 0, fmt.Errorf("state: count failed cron invocations: %w", err)
	}
	n, err := sqlc.New().CountFailedCronInvocations(ctx, s.pool, sqlc.CountFailedCronInvocationsParams{
		AccountID: ids[0], AppID: ids[1], EnvironmentID: ids[2], CronID: ids[3],
		Since: pgtype.Timestamptz{Time: since, Valid: true},
	})
	if err != nil {
		return 0, fmt.Errorf("state: count failed cron invocations: %w", err)
	}
	return n, nil
}

// ListAppBootFailures runs the sqlc ListAppBootFailures query.
func (s *PgStore) ListAppBootFailures(ctx context.Context, appID string, since, until time.Time, limit int) ([]FailedBoot, error) {
	rows, err := sqlc.New().ListAppBootFailures(ctx, s.pool, sqlc.ListAppBootFailuresParams{
		AppID: appID, Since: pgtype.Timestamptz{Time: since, Valid: true}, Until: pgtype.Timestamptz{Time: until, Valid: true},
		RowLimit: failureDetailLimit(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("state: list app boot failures: %w", err)
	}
	out := make([]FailedBoot, 0, len(rows))
	for _, r := range rows {
		out = append(out, FailedBoot{At: r.At.Time.UTC(), DeploymentID: r.DeploymentID, NodeID: r.NodeID, Method: r.Method})
	}
	return out, nil
}

// ListAccountFailedJobRuns runs the sqlc ListAccountFailedJobRuns query.
func (s *PgStore) ListAccountFailedJobRuns(ctx context.Context, accountID, jobName string, since, until time.Time, limit int) ([]FailedJobRun, error) {
	account, err := failureDetailUUID(accountID)
	if err != nil {
		return nil, fmt.Errorf("state: list account failed job runs: %w", err)
	}
	rows, err := sqlc.New().ListAccountFailedJobRuns(ctx, s.pool, sqlc.ListAccountFailedJobRunsParams{
		AccountID: account, JobName: pgtype.Text{String: jobName, Valid: jobName != ""},
		Since: pgtype.Timestamptz{Time: since, Valid: true}, Until: pgtype.Timestamptz{Time: until, Valid: true},
		RowLimit: failureDetailLimit(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("state: list account failed job runs: %w", err)
	}
	out := make([]FailedJobRun, 0, len(rows))
	for _, r := range rows {
		out = append(out, FailedJobRun{ID: uuid.UUID(r.ID.Bytes).String(), JobName: r.JobName, Status: r.AggregateStatus,
			Tasks: int(r.Tasks), TasksFailed: int(r.TasksFailed), DeadLetters: int(r.DeadLetterCount),
			FinishedAt: r.FinishedAt.Time.UTC(), FirstError: r.FirstError})
	}
	return out, nil
}

// ListFailedInvocations runs the sqlc ListFailedInvocations query.
func (s *PgStore) ListFailedInvocations(ctx context.Context, accountID, appID, environmentID string, source AlertFailureSource, cronID string, since time.Time, limit int) ([]FailedInvocation, error) {
	ids, err := failureDetailUUIDs(accountID, appID, environmentID, cronID)
	if err != nil {
		return nil, fmt.Errorf("state: list failed invocations: %w", err)
	}
	src := pgtype.Text{String: string(source), Valid: source != "" && source != AlertFailureAny}
	rows, err := sqlc.New().ListFailedInvocations(ctx, s.pool, sqlc.ListFailedInvocationsParams{
		AccountID: ids[0], AppID: ids[1], EnvironmentID: ids[2], Source: src, CronID: ids[3],
		Since: pgtype.Timestamptz{Time: since, Valid: true}, RowLimit: failureDetailLimit(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("state: list failed invocations: %w", err)
	}
	out := make([]FailedInvocation, 0, len(rows))
	for _, r := range rows {
		inv := FailedInvocation{ID: uuid.UUID(r.ID.Bytes).String(), Source: r.Source, Path: r.Path, Attempts: int(r.Attempts),
			LastError: r.LastError, FailedAt: r.FailedAt.Time.UTC()}
		if r.CronID.Valid {
			inv.CronID = uuid.UUID(r.CronID.Bytes).String()
		}
		out = append(out, inv)
	}
	return out, nil
}

var _ FailureAlertDetailer = (*MemStore)(nil)

// CountFailedCronInvocations is the in-memory equivalent of the PgStore query.
func (m *MemStore) CountFailedCronInvocations(_ context.Context, accountID, appID, environmentID, cronID string, since time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return int64(m.countFailedInvocationsLocked(accountID, appID, since, func(inv Invocation) bool {
		return inv.EnvironmentID == environmentID && inv.Source == InvocationCron && inv.CronID != nil && *inv.CronID == cronID
	})), nil
}

// ListAppBootFailures is the in-memory equivalent of the PgStore query.
func (m *MemStore) ListAppBootFailures(_ context.Context, appID string, since, until time.Time, limit int) ([]FailedBoot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []FailedBoot
	for _, e := range m.events {
		if e.Kind != wakeBootFailedKind || e.At.Before(since) || e.At.After(until) {
			continue
		}
		var data struct {
			AppID        string `json:"app_id"`
			Reason       string `json:"reason"`
			Mirror       *bool  `json:"mirror"`
			DeploymentID string `json:"deployment_id"`
			NodeID       string `json:"node_id"`
			Method       string `json:"method"`
		}
		if json.Unmarshal(e.Data, &data) == nil && data.AppID == appID && data.Reason == vmmBootFailedReason && data.Mirror == nil {
			out = append(out, FailedBoot{At: e.At.UTC(), DeploymentID: data.DeploymentID, NodeID: data.NodeID, Method: data.Method})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out[:min(len(out), int(failureDetailLimit(limit)))], nil
}

// ListAccountFailedJobRuns is the in-memory equivalent of the PgStore query.
func (m *MemStore) ListAccountFailedJobRuns(_ context.Context, accountID, jobName string, since, until time.Time, limit int) ([]FailedJobRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []FailedJobRun
	for _, r := range m.jobRuns {
		if r.AccountID != accountID || r.FinishedAt == nil || r.FinishedAt.Before(since) || r.FinishedAt.After(until) ||
			r.AggregateStatus != "failed" && r.AggregateStatus != "dead_letter" {
			continue
		}
		job := m.jobs[r.JobID]
		if jobName != "" && job.Name != jobName {
			continue
		}
		out = append(out, FailedJobRun{ID: r.ID, JobName: job.Name, Status: r.AggregateStatus, Tasks: r.Tasks, TasksFailed: r.TasksFailed,
			DeadLetters: r.DeadLetterCount, FinishedAt: r.FinishedAt.UTC(), FirstError: m.firstJobTaskErrorLocked(r.ID)})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].FinishedAt.Equal(out[j].FinishedAt) {
			return out[i].FinishedAt.After(out[j].FinishedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out[:min(len(out), int(failureDetailLimit(limit)))], nil
}

// firstJobTaskErrorLocked is the lowest-indexed unsucceeded task's error
// message or class. The caller holds m.mu.
func (m *MemStore) firstJobTaskErrorLocked(runID string) string {
	best, msg := -1, ""
	for index, t := range m.jobTasks[runID] {
		if t.Status == "succeeded" || best >= 0 && index > best {
			continue
		}
		switch {
		case t.ErrorMessage != nil:
			best, msg = index, *t.ErrorMessage
		case t.ErrorClass != nil:
			best, msg = index, *t.ErrorClass
		}
	}
	return msg
}

// ListFailedInvocations is the in-memory equivalent of the PgStore query.
func (m *MemStore) ListFailedInvocations(_ context.Context, accountID, appID, environmentID string, source AlertFailureSource, cronID string, since time.Time, limit int) ([]FailedInvocation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []FailedInvocation
	for _, inv := range m.invocations {
		if inv.AccountID != accountID || inv.AppID != appID || inv.EnvironmentID != environmentID ||
			inv.State != InvocationFailed && inv.State != InvocationDeadLetter {
			continue
		}
		if source != "" && source != AlertFailureAny && string(inv.Source) != string(source) ||
			cronID != "" && (inv.CronID == nil || *inv.CronID != cronID) {
			continue
		}
		failedAt := inv.CreatedAt
		if inv.CompletedAt != nil {
			failedAt = *inv.CompletedAt
		}
		if failedAt.Before(since) {
			continue
		}
		f := FailedInvocation{ID: inv.ID, Source: string(inv.Source), Path: inv.Path, Attempts: inv.Attempts, LastError: inv.LastError, FailedAt: failedAt.UTC()}
		if inv.CronID != nil {
			f.CronID = *inv.CronID
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].FailedAt.Equal(out[j].FailedAt) {
			return out[i].FailedAt.After(out[j].FailedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out[:min(len(out), int(failureDetailLimit(limit)))], nil
}
