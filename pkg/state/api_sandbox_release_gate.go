package state

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var (
	ErrPassingSandboxSuiteRequired  = errors.New("state: release requires a current passing sandbox suite")
	ErrReleaseApprovalPolicyChanged = errors.New("state: release approval policy changed before command admission")
)

type APISandboxReleaseGateReader interface {
	APISandboxReleaseGate(context.Context, string, string, bool) (*api.APISandboxReleaseGate, error)
}

// APISandboxApprovalBaseline selects the retained release serving all traffic.
// Callers also verify the production graph resolves to this same release.
func APISandboxApprovalBaseline(live []Deployment, candidateID string) (Deployment, error) {
	var baseline Deployment
	for _, current := range ProductionRoutingDeployments(live) {
		if current.TrafficPercent <= 0 {
			continue
		}
		if baseline.ID != "" || current.TrafficPercent != 100 || current.ID == candidateID ||
			!APISandboxSourceSupported(current) || APISandboxArtifactPin(current) == "" {
			return Deployment{}, ErrNotFound
		}
		baseline = current
	}
	if baseline.ID == "" {
		return Deployment{}, ErrNotFound
	}
	return baseline, nil
}

func (s *PgStore) APISandboxReleaseGate(ctx context.Context, appID, candidateID string, atApproval bool) (*api.APISandboxReleaseGate, error) {
	q := sqlc.New()
	runID, err := q.GetAPISandboxReleaseAdmission(ctx, s.pool, mustPgUUID(candidateID))
	switch {
	case err == nil:
		return &api.APISandboxReleaseGate{Status: "admitted", SuiteRunID: pgUUIDString(runID)}, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return nil, mapErr(err)
	case !atApproval:
		return nil, nil
	}
	policy, err := q.GetAppReleaseApprovalPolicy(ctx, s.pool, mustPgUUID(appID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil || !policy.RequirePassingSandboxSuite {
		return nil, mapErr(err)
	}
	return readSandboxReleaseGate(ctx, s.pool, appID, candidateID, time.Duration(policy.TimeoutMinutes)*time.Minute)
}

func (s *PgStore) createAppTaskWithSandboxSuite(ctx context.Context, params CreateAppTaskParams) (AppTask, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return AppTask{}, err
	}
	defer rollbackOrgAccess(ctx, tx)
	q, app := sqlc.New(), mustPgUUID(params.AppID)
	// Project publication takes the project lock before the app. The app lock
	// orders go-live, suite changes, library edits and new suite runs.
	if _, err := q.LockAPISandboxReleaseProject(ctx, tx, app); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return AppTask{}, mapErr(err)
	}
	if _, err := q.LockAPISandboxScenarioLibrary(ctx, tx, app); err != nil {
		return AppTask{}, mapErr(err)
	}
	policy, err := q.LockAPISandboxReleaseApprovalPolicy(ctx, tx, app)
	if errors.Is(err, pgx.ErrNoRows) {
		return AppTask{}, ErrReleaseApprovalPolicyChanged
	}
	if err != nil {
		return AppTask{}, mapErr(err)
	}
	if !policy.UpdatedAt.Time.Equal(*params.ReleaseApprovalPolicyUpdatedAt) {
		return AppTask{}, ErrReleaseApprovalPolicyChanged
	}
	var gate *api.APISandboxReleaseGate
	if policy.RequirePassingSandboxSuite {
		if _, err := q.LockAPISandboxReleaseDeployments(ctx, tx, sqlc.LockAPISandboxReleaseDeploymentsParams{
			AppID: app, ID: mustPgUUID(params.DeploymentID),
		}); err != nil {
			return AppTask{}, mapErr(err)
		}
		gate, err = readSandboxReleaseGate(ctx, tx, params.AppID, params.DeploymentID, time.Duration(policy.TimeoutMinutes)*time.Minute)
		if err != nil {
			return AppTask{}, err
		}
		if gate.Status != "passed" {
			return AppTask{}, ErrPassingSandboxSuiteRequired
		}
	}
	task, err := createAppTaskOnDB(ctx, tx, params)
	if err != nil {
		return AppTask{}, err
	}
	if gate != nil {
		if err := q.RecordAPISandboxReleaseAdmission(ctx, tx, sqlc.RecordAPISandboxReleaseAdmissionParams{
			DeploymentID: mustPgUUID(params.DeploymentID), TaskID: mustPgUUID(task.ID), SuiteRunID: mustPgUUID(gate.SuiteRunID),
		}); err != nil {
			return AppTask{}, mapErr(err)
		}
	}
	return task, mapErr(tx.Commit(ctx))
}

func readSandboxReleaseGate(ctx context.Context, db sqlc.DBTX, appID, candidateID string, timeout time.Duration) (*api.APISandboxReleaseGate, error) {
	q, app, target := sqlc.New(), mustPgUUID(appID), mustPgUUID(candidateID)
	gate := &api.APISandboxReleaseGate{Status: "unavailable"}
	suite, err := q.GetAPISandboxSuite(ctx, db, app)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && len(suite.ScenarioIds) == 0) {
		gate.Status = "no_suite"
		return gate, nil
	}
	if err != nil {
		return nil, mapErr(err)
	}
	candidate, err := deploymentByIDDB(ctx, db, candidateID)
	if err != nil {
		return nil, err
	}
	if candidate.AppID != appID || !APISandboxApprovalCandidateSupported(candidate) {
		return gate, nil
	}
	live, err := readLiveDeployments(ctx, db, appID)
	if err != nil {
		return nil, err
	}
	baseline, err := APISandboxApprovalBaseline(live, candidateID)
	if errors.Is(err, ErrNotFound) {
		return gate, nil
	}
	if err != nil {
		return nil, err
	}
	graph, err := q.ResolvePublicProjectRelease(ctx, db, sqlc.ResolvePublicProjectReleaseParams{
		AppID: app, Scope: "production",
	})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, mapErr(err)
	}
	if err == nil && graph.DeploymentID != baseline.ID {
		return gate, nil
	}
	return readSandboxReleaseEvidence(ctx, db, gate, suite, app, target, baseline, candidate, ReleasePhaseStartedAt(candidate).Add(timeout))
}

func readSandboxReleaseEvidence(ctx context.Context, db sqlc.DBTX, gate *api.APISandboxReleaseGate, suite sqlc.GetAPISandboxSuiteRow,
	app, target pgtype.UUID, baseline, candidate Deployment, deadline time.Time) (*api.APISandboxReleaseGate, error) {
	q := sqlc.New()
	row, err := q.GetLatestAPISandboxSuiteRun(ctx, db, target)
	if errors.Is(err, pgx.ErrNoRows) {
		gate.Status = "waiting"
		return gate, nil
	}
	if err != nil {
		return nil, mapErr(err)
	}
	run := pgSandboxSuiteRun(row)
	revisions, err := q.APISandboxSuiteLibraryEntries(ctx, db, sqlc.APISandboxSuiteLibraryEntriesParams{AppID: app, ScenarioIds: suite.ScenarioIds})
	if err != nil {
		return nil, mapErr(err)
	}
	items, err := q.ListAPISandboxSuiteScenarios(ctx, db, row.ID)
	if err != nil {
		return nil, mapErr(err)
	}
	current := len(revisions) == len(suite.ScenarioIds) && len(items) == len(revisions) && len(items) == run.ScenarioCount
	for i := range revisions {
		current = current && items[i].ScenarioID == revisions[i].ID && items[i].ScenarioRevision == revisions[i].Revision
	}
	gate.SuiteRunID = run.ID
	gate.Status = sandboxSuiteGateStatus(run, pgUUIDString(suite.Revision), baseline, candidate, deadline, current)
	if gate.Status == "passed" {
		passed, err := q.APISandboxSuiteRunPassed(ctx, db, sqlc.APISandboxSuiteRunPassedParams{
			RunID: row.ID, ScenarioCount: int64(run.ScenarioCount), BaselineID: row.BaselineDeploymentID,
			CandidateID: row.CandidateDeploymentID, BaselinePin: run.Pins.Baseline, CandidatePin: run.Pins.Candidate,
			ApprovalDeadline: pgTimestamptzUTC(deadline),
		})
		if err != nil {
			return nil, mapErr(err)
		}
		if !passed.Valid || !passed.Bool {
			gate.Status = "failed"
		}
	}
	return gate, nil
}

func sandboxSuiteGateStatus(run APISandboxSuiteRun, revision string, baseline, candidate Deployment, deadline time.Time, current bool) string {
	if !current || run.SuiteRevision != revision || run.AppID != candidate.AppID ||
		run.CandidateDeploymentID != candidate.ID || run.BaselineDeploymentID != baseline.ID || run.Pins.Baseline != APISandboxArtifactPin(baseline) ||
		run.Pins.Candidate != APISandboxArtifactPin(candidate) {
		return "stale"
	}
	if run.FinishedAt == nil {
		return "waiting"
	}
	if !run.FinishedAt.Before(deadline) || run.FailureCode != "" || run.CompletedCount != run.ScenarioCount || run.ScenarioCount == 0 {
		return "failed"
	}
	return "passed"
}

func (m *MemStore) APISandboxReleaseGate(_ context.Context, appID, candidateID string, atApproval bool) (*api.APISandboxReleaseGate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if runID := m.apiSandboxReleaseAdmissions[candidateID]; runID != "" {
		return &api.APISandboxReleaseGate{Status: "admitted", SuiteRunID: runID}, nil
	}
	if !atApproval {
		return nil, nil
	}
	return m.sandboxReleaseGateLocked(appID, candidateID), nil
}

func (m *MemStore) sandboxReleaseGateLocked(appID, candidateID string) *api.APISandboxReleaseGate {
	policy := m.releaseApprovalPolicies[appID]
	if !policy.RequiresSandboxSuite() {
		return nil
	}
	suite := m.apiSandboxSuites[appID]
	if len(suite.ScenarioIDs) == 0 {
		return &api.APISandboxReleaseGate{Status: "no_suite"}
	}
	gate := &api.APISandboxReleaseGate{Status: "unavailable"}
	candidate := m.deployments[candidateID]
	if candidate.AppID != appID || !APISandboxApprovalCandidateSupported(candidate) {
		return gate
	}
	var live []Deployment
	for _, dep := range m.deployments {
		if dep.AppID == appID && dep.Status == DeployLive {
			live = append(live, dep)
		}
	}
	baseline, err := APISandboxApprovalBaseline(live, candidateID)
	if err != nil {
		return gate
	}
	if _, routed, err := m.resolveProjectReleaseLocked(appID, "production", ""); err != nil || (routed != "" && routed != baseline.ID) {
		return gate
	}
	var latest APISandboxSuiteRun
	for _, run := range m.apiSandboxSuiteRuns {
		if run.CandidateDeploymentID == candidateID && (latest.ID == "" ||
			sandboxComparisonBefore(latest.CreatedAt, latest.ID, run.CreatedAt, run.ID)) {
			latest = run
		}
	}
	if latest.ID == "" {
		gate.Status = "waiting"
		return gate
	}
	items := m.apiSandboxSuiteScenarios[latest.ID]
	gate.SuiteRunID = latest.ID
	deadline := ReleasePhaseStartedAt(candidate).Add(time.Duration(policy.TimeoutMinutes) * time.Minute)
	gate.Status = sandboxSuiteGateStatus(latest, suite.Revision, baseline, candidate, deadline,
		len(items) == latest.ScenarioCount && slices.EqualFunc(items, suite.ScenarioIDs, func(item APISandboxSuiteScenario, id string) bool {
			return item.ScenarioID == id && m.apiSandboxScenarioLibrary[appID][id].revision == item.ScenarioRevision
		}))
	if gate.Status == "passed" {
		for _, item := range items {
			stored := m.apiSandboxComparisons[item.ComparisonID]
			comparison, ok := m.sandboxComparisonLocked(stored)
			if !ok || stored.approvalPins != latest.Pins || comparison.Verdict != api.APISandboxComparisonPassed ||
				comparison.BaselineDeploymentID != baseline.ID || comparison.CandidateDeploymentID != candidateID ||
				comparison.Baseline.FinishedAt == nil || !comparison.Baseline.FinishedAt.Before(deadline) ||
				comparison.Candidate.FinishedAt == nil || !comparison.Candidate.FinishedAt.Before(deadline) ||
				comparison.Baseline.ScenarioRevision != item.ScenarioRevision || comparison.Candidate.ScenarioRevision != item.ScenarioRevision {
				gate.Status = "failed"
				break
			}
		}
	}
	return gate
}
