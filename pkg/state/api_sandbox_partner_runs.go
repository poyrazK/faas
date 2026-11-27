package state

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

const apiSandboxPartnerStepLease = 2 * time.Minute

func apiSandboxPartnerStepCount(app App) int {
	if app.Manifest.APISandboxPartner && app.APISandboxScenario != nil {
		return len(app.APISandboxScenario.Requests) - 1
	}
	return 0
}

func publicAPISandboxRunReport(report api.APISandboxRunReport) api.APISandboxRunReport {
	report.Mode = api.APISandboxRunModeAutomated
	if report.Status == api.APISandboxRunQueued && report.PartnerOutcomeUntil != nil {
		report.Status = api.APISandboxRunAwaitingOutcome
	}
	if report.PartnerStepCount == 0 {
		return report
	}
	report.Mode = api.APISandboxRunModePartner
	if report.Status == api.APISandboxRunQueued && report.SandboxDeploymentID != "" {
		report.Status = api.APISandboxRunAwaitingPartner
		if len(report.Steps) == report.PartnerStepCount {
			report.Status = api.APISandboxRunCheckingState
		}
	}
	return report
}

func (s *PgStore) OpenAPISandboxPartnerRun(ctx context.Context, run APISandboxRun, at time.Time) error {
	n, err := sqlc.New().OpenAPISandboxPartnerRun(ctx, s.pool, sqlc.OpenAPISandboxPartnerRunParams{
		RunID: mustPgUUID(run.ID), LeaseToken: mustPgUUID(run.LeaseToken),
		DeploymentID: mustPgUUID(run.SandboxDeploymentID), AtTime: pgTimestamptzUTC(at),
	})
	if err == nil && n != 1 {
		return ErrConflict
	}
	return mapErr(err)
}

func (s *PgStore) ClaimAPISandboxPartnerStep(ctx context.Context, appID, deploymentID, keyID, method, target string, at time.Time) (api.APISandboxPartnerStep, error) {
	token := uuid.NewString()
	row, err := sqlc.New().ClaimAPISandboxPartnerStep(ctx, s.pool, sqlc.ClaimAPISandboxPartnerStepParams{
		AppID: mustPgUUID(appID), DeploymentID: mustPgUUID(deploymentID), KeyID: mustPgUUID(keyID),
		Method: method, Target: target, ClaimToken: mustPgUUID(token), AtTime: pgTimestamptzUTC(at),
		ClaimUntil: pgTimestamptzUTC(at.Add(apiSandboxPartnerStepLease)),
	})
	if err != nil {
		return api.APISandboxPartnerStep{}, mapErr(err)
	}
	var example api.APISandboxExampleRequest
	if err := json.Unmarshal(row.Example, &example); err != nil {
		return api.APISandboxPartnerStep{}, fmt.Errorf("state: decode sandbox partner step: %w", err)
	}
	example.Path = target
	return newAPISandboxPartnerStep(pgUUIDString(row.ID), token, int(row.StepIndex), example), nil
}

func (s *PgStore) CompleteAPISandboxPartnerStep(ctx context.Context, permit api.APISandboxPartnerStep, at time.Time) error {
	data, err := json.Marshal([]api.APISandboxRunStep{permit.Step})
	if err != nil {
		return err
	}
	updated, err := sqlc.New().CompleteAPISandboxPartnerStep(ctx, s.pool, sqlc.CompleteAPISandboxPartnerStepParams{
		RunID: mustPgUUID(permit.RunID), ClaimToken: mustPgUUID(permit.Token), Step: data,
		AtTime: pgTimestamptzUTC(at), Failed: permit.Step.FailureCode != "", FailureCode: permit.Step.FailureCode,
	})
	if err == nil && !updated {
		return ErrConflict
	}
	return mapErr(err)
}

func (m *MemStore) OpenAPISandboxPartnerRun(_ context.Context, run APISandboxRun, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.apiSandboxRuns[run.ID]
	if !current.ownsLease(run.LeaseToken, at) || current.PartnerStepCount == 0 || current.SandboxDeploymentID != "" {
		return ErrConflict
	}
	current.Status, current.SandboxDeploymentID = api.APISandboxRunQueued, run.SandboxDeploymentID
	current.LeaseToken, current.LeaseUntil = "", nil
	m.apiSandboxRuns[run.ID] = current
	return nil
}

func (m *MemStore) ClaimAPISandboxPartnerStep(_ context.Context, appID, deploymentID, keyID, method, target string, at time.Time) (api.APISandboxPartnerStep, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	op := m.apiSandboxOperations[appID]
	if !APISandboxAcceptsRequests(m.apps[appID], at) || !m.apiSandboxPartners[appID].ActiveKey(keyID) ||
		op.Phase != APISandboxLive || op.DeploymentID != deploymentID || op.Scenario == nil {
		return api.APISandboxPartnerStep{}, ErrNotFound
	}
	for id, run := range m.apiSandboxRuns {
		index := len(run.Steps)
		if run.AppID != appID || run.Status != api.APISandboxRunQueued || run.SandboxDeploymentID != deploymentID ||
			deploymentID == "" || run.PartnerClaim != "" || index >= run.PartnerStepCount || index >= len(op.Scenario.Requests) {
			continue
		}
		example := op.Scenario.Requests[index]
		if example.Method != method || example.Path != target {
			break
		}
		until := at.Add(apiSandboxPartnerStepLease)
		run.PartnerClaim, run.PartnerClaimUntil = uuid.NewString(), &until
		m.apiSandboxRuns[id] = run
		return newAPISandboxPartnerStep(id, run.PartnerClaim, index, example), nil
	}
	return api.APISandboxPartnerStep{}, ErrNotFound
}

func (m *MemStore) CompleteAPISandboxPartnerStep(_ context.Context, permit api.APISandboxPartnerStep, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	run := m.apiSandboxRuns[permit.RunID]
	if run.Status != api.APISandboxRunQueued || run.PartnerClaim != permit.Token || run.PartnerClaimUntil == nil || !run.PartnerClaimUntil.After(at) {
		return ErrConflict
	}
	run.PartnerClaim, run.PartnerClaimUntil = "", nil
	run.Steps = append(run.Steps, permit.Step)
	if permit.Step.FailureCode != "" {
		run.Status, run.FailureCode, run.FinishedAt = api.APISandboxRunFailed, permit.Step.FailureCode, &at
		m.closeSandboxRunLocked(run.AppID)
	}
	m.apiSandboxRuns[run.ID] = run
	return nil
}

func newAPISandboxPartnerStep(runID, token string, index int, example api.APISandboxExampleRequest) api.APISandboxPartnerStep {
	path, _, _ := strings.Cut(example.Path, "?")
	return api.APISandboxPartnerStep{
		RunID: runID, Token: token,
		Step: api.APISandboxRunStep{Index: index + 1, Title: example.Title, Method: example.Method, Path: path,
			ExpectedStatus: example.ExpectedStatus, PartnerRequest: true},
	}
}

func (m *MemStore) closeSandboxRunLocked(appID string) {
	if app := m.apps[appID]; app.Status == AppActive && app.PreviewPrState == PreviewPrStateOpen {
		app.PreviewPrState = PreviewPrStateTearingDown
		m.apps[app.ID] = app
	}
}
