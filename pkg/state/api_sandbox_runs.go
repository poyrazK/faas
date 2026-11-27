package state

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type APISandboxRun struct {
	api.APISandboxRunReport
	AppID              string
	LeaseToken         string
	LeaseUntil         *time.Time
	Interrupted        bool
	PartnerInterrupted bool
	PartnerClaim       string
	PartnerClaimUntil  *time.Time
	ShareHash          []byte
	ShareExpiresAt     *time.Time
}

type APISandboxRunStore interface {
	GetAPISandboxRun(ctx context.Context, id string) (APISandboxRun, error)
	ListAPISandboxRuns(ctx context.Context, sourceID string) ([]api.APISandboxRunReport, error)
	ClaimAPISandboxRun(ctx context.Context, at time.Time, lease time.Duration) (APISandboxRun, error)
	OpenAPISandboxPartnerRun(ctx context.Context, run APISandboxRun, at time.Time) error
	AppendAPISandboxRunStep(ctx context.Context, run APISandboxRun, step api.APISandboxRunStep, at time.Time) error
	FinishAPISandboxRun(ctx context.Context, run APISandboxRun, at time.Time) error
	ShareAPISandboxRun(ctx context.Context, id string, hash []byte, expiresAt time.Time) error
}

func apiSandboxRunFromRow(row sqlc.ApiSandboxRun, err error) (APISandboxRun, error) {
	if err != nil {
		return APISandboxRun{}, mapErr(err)
	}
	report := api.APISandboxRunReport{
		ID: pgUUIDString(row.ID), SourceDeploymentID: pgUUIDString(row.SourceDeploymentID),
		SandboxDeploymentID: pgUUIDString(row.SandboxDeploymentID), ScenarioRevision: pgUUIDString(row.ScenarioRevision),
		ScenarioName: row.ScenarioName, Status: row.Status, FailureCode: row.FailureCode,
		PartnerStepCount: int(row.PartnerStepCount), PartnerWebhook: row.PartnerWebhook, PartnerOutcomeUntil: optionalPgTime(row.PartnerOutcomeUntil),
		CreatedAt: row.CreatedAt.Time.UTC(), StartedAt: optionalPgTime(row.StartedAt), FinishedAt: optionalPgTime(row.FinishedAt),
	}
	if err := json.Unmarshal(row.Steps, &report.Steps); err != nil {
		return APISandboxRun{}, fmt.Errorf("state: decode sandbox run steps: %w", err)
	}
	report = publicAPISandboxRunReport(report)
	return APISandboxRun{
		APISandboxRunReport: report, AppID: pgUUIDString(row.SandboxAppID),
		LeaseToken: pgUUIDString(row.LeaseToken), LeaseUntil: optionalPgTime(row.LeaseUntil),
		ShareHash: row.ShareHash, ShareExpiresAt: optionalPgTime(row.ShareExpiresAt),
	}, nil
}

func (s *PgStore) GetAPISandboxRun(ctx context.Context, id string) (APISandboxRun, error) {
	return apiSandboxRunFromRow(sqlc.New().GetAPISandboxRun(ctx, s.pool, mustPgUUID(id)))
}

func (s *PgStore) ListAPISandboxRuns(ctx context.Context, sourceID string) ([]api.APISandboxRunReport, error) {
	rows, err := sqlc.New().ListAPISandboxRuns(ctx, s.pool, mustPgUUID(sourceID))
	if err != nil {
		return nil, mapErr(err)
	}
	runs := make([]api.APISandboxRunReport, 0, len(rows))
	for _, row := range rows {
		run, err := apiSandboxRunFromRow(row, nil)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run.APISandboxRunReport)
	}
	return runs, nil
}

func (s *PgStore) ClaimAPISandboxRun(ctx context.Context, at time.Time, lease time.Duration) (APISandboxRun, error) {
	row, err := sqlc.New().ClaimAPISandboxRun(ctx, s.pool, sqlc.ClaimAPISandboxRunParams{
		AtTime: pgTimestamptzUTC(at), LeaseToken: mustPgUUID(uuid.NewString()), LeaseUntil: pgTimestamptzUTC(at.Add(lease)),
	})
	if err != nil {
		return APISandboxRun{}, mapErr(err)
	}
	run, err := apiSandboxRunFromRow(sqlc.ApiSandboxRun{
		ID: row.ID, SourceDeploymentID: row.SourceDeploymentID, SandboxAppID: row.SandboxAppID,
		SandboxDeploymentID: row.SandboxDeploymentID, ScenarioRevision: row.ScenarioRevision, ScenarioName: row.ScenarioName,
		Status: row.Status, Steps: row.Steps, FailureCode: row.FailureCode, CreatedAt: row.CreatedAt, StartedAt: row.StartedAt,
		FinishedAt: row.FinishedAt, LeaseToken: row.LeaseToken, LeaseUntil: row.LeaseUntil,
		ShareHash: row.ShareHash, ShareExpiresAt: row.ShareExpiresAt, PartnerStepCount: row.PartnerStepCount,
		PartnerWebhook: row.PartnerWebhook, PartnerOutcomeUntil: row.PartnerOutcomeUntil,
	}, nil)
	run.Interrupted, run.PartnerInterrupted = row.Interrupted, row.PartnerInterrupted
	return run, err
}

func (s *PgStore) AppendAPISandboxRunStep(ctx context.Context, run APISandboxRun, step api.APISandboxRunStep, at time.Time) error {
	data, err := json.Marshal([]api.APISandboxRunStep{step})
	if err != nil {
		return fmt.Errorf("state: encode sandbox run step: %w", err)
	}
	n, err := sqlc.New().AppendAPISandboxRunStep(ctx, s.pool, sqlc.AppendAPISandboxRunStepParams{
		RunID: mustPgUUID(run.ID), LeaseToken: mustPgUUID(run.LeaseToken), AtTime: pgTimestamptzUTC(at),
		DeploymentID: mustPgUUID(run.SandboxDeploymentID), Step: data,
	})
	if err == nil && n == 0 {
		return ErrConflict
	}
	return mapErr(err)
}

func (s *PgStore) FinishAPISandboxRun(ctx context.Context, run APISandboxRun, at time.Time) error {
	updated, err := sqlc.New().FinishAPISandboxRun(ctx, s.pool, sqlc.FinishAPISandboxRunParams{
		RunID: mustPgUUID(run.ID), LeaseToken: mustPgUUID(run.LeaseToken), AtTime: pgTimestamptzUTC(at),
		Outcome: run.Status, FailureCode: run.FailureCode,
	})
	if err == nil && !updated {
		return ErrConflict
	}
	return mapErr(err)
}

func (s *PgStore) ShareAPISandboxRun(ctx context.Context, id string, hash []byte, expiresAt time.Time) error {
	n, err := sqlc.New().ShareAPISandboxRun(ctx, s.pool, sqlc.ShareAPISandboxRunParams{
		ID: mustPgUUID(id), ShareHash: hash, ShareExpiresAt: pgTimestamptzUTC(expiresAt),
	})
	if err == nil && n == 0 {
		return ErrConflict
	}
	return mapErr(err)
}

func (m *MemStore) GetAPISandboxRun(_ context.Context, id string) (APISandboxRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.apiSandboxRuns[id]
	if !ok {
		return APISandboxRun{}, ErrNotFound
	}
	run.Steps = slices.Clone(run.Steps)
	run.ShareHash = slices.Clone(run.ShareHash)
	run.APISandboxRunReport = publicAPISandboxRunReport(run.APISandboxRunReport)
	return run, nil
}

func (m *MemStore) ListAPISandboxRuns(_ context.Context, sourceID string) ([]api.APISandboxRunReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	runs := make([]api.APISandboxRunReport, 0)
	for _, run := range m.apiSandboxRuns {
		if run.SourceDeploymentID == sourceID {
			run.Steps = slices.Clone(run.Steps)
			runs = append(runs, publicAPISandboxRunReport(run.APISandboxRunReport))
		}
	}
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].CreatedAt.Equal(runs[j].CreatedAt) {
			return runs[i].ID > runs[j].ID
		}
		return runs[i].CreatedAt.After(runs[j].CreatedAt)
	})
	return runs[:min(len(runs), 25)], nil
}

func (m *MemStore) ClaimAPISandboxRun(_ context.Context, at time.Time, lease time.Duration) (APISandboxRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var next APISandboxRun
	for _, run := range m.apiSandboxRuns {
		op := m.apiSandboxOperations[run.AppID]
		ready := run.Status == api.APISandboxRunQueued && (!APISandboxAcceptsRequests(m.apps[run.AppID], at) ||
			(run.SandboxDeploymentID == "" && (op.Phase == APISandboxLive || op.Phase == APISandboxFailed)) ||
			(run.PartnerStepCount > 0 && len(run.Steps) == run.PartnerStepCount) ||
			(run.PartnerClaimUntil != nil && !run.PartnerClaimUntil.After(at)) ||
			(run.PartnerOutcomeUntil != nil && (!run.PartnerOutcomeUntil.After(at) || m.apiSandboxWebhookPartners[run.ID].OutcomeAt != nil)))
		stale := run.Status == api.APISandboxRunRunning && run.LeaseUntil != nil && !run.LeaseUntil.After(at)
		if (ready || stale) && (next.ID == "" || run.CreatedAt.Before(next.CreatedAt) ||
			(run.CreatedAt.Equal(next.CreatedAt) && run.ID < next.ID)) {
			next = run
		}
	}
	if next.ID == "" {
		return APISandboxRun{}, ErrNotFound
	}
	next.Interrupted = next.Status == api.APISandboxRunRunning
	next.PartnerInterrupted = next.PartnerClaim != ""
	next.PartnerClaim, next.PartnerClaimUntil = "", nil
	next.Status = api.APISandboxRunRunning
	if next.StartedAt == nil {
		next.StartedAt = &at
	}
	until := at.Add(lease)
	next.LeaseToken, next.LeaseUntil = uuid.NewString(), &until
	m.apiSandboxRuns[next.ID] = next
	next.Steps = slices.Clone(next.Steps)
	next.APISandboxRunReport = publicAPISandboxRunReport(next.APISandboxRunReport)
	return next, nil
}

func (m *MemStore) AppendAPISandboxRunStep(_ context.Context, run APISandboxRun, step api.APISandboxRunStep, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.apiSandboxRuns[run.ID]
	if !current.ownsLease(run.LeaseToken, at) {
		return ErrConflict
	}
	current.Steps = append(current.Steps, step)
	current.SandboxDeploymentID = run.SandboxDeploymentID
	m.apiSandboxRuns[run.ID] = current
	return nil
}

func (m *MemStore) FinishAPISandboxRun(_ context.Context, run APISandboxRun, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.apiSandboxRuns[run.ID]
	if !current.ownsLease(run.LeaseToken, at) || !run.Terminal() {
		return ErrConflict
	}
	current.Status, current.FailureCode, current.FinishedAt = run.Status, run.FailureCode, &at
	current.LeaseToken, current.LeaseUntil = "", nil
	m.apiSandboxRuns[run.ID] = current
	m.closeSandboxRunLocked(current.AppID)
	return nil
}

func (m *MemStore) ShareAPISandboxRun(_ context.Context, id string, hash []byte, expiresAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	run := m.apiSandboxRuns[id]
	if !run.Terminal() {
		return ErrConflict
	}
	run.ShareHash, run.ShareExpiresAt = slices.Clone(hash), &expiresAt
	m.apiSandboxRuns[id] = run
	return nil
}

func (r APISandboxRun) ownsLease(token string, at time.Time) bool {
	return token != "" && r.LeaseToken == token && r.LeaseUntil != nil && r.LeaseUntil.After(at)
}
