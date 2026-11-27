package state

import (
	"context"
	"fmt"
	"maps"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type APISandboxComparisonStore interface {
	CreateAPISandboxComparison(ctx context.Context, id string, baseline, candidate App, limits api.Limits) ([2]App, error)
	GetAPISandboxComparison(ctx context.Context, id string) (api.APISandboxComparisonReport, error)
	ListAPISandboxComparisons(ctx context.Context, candidateID string) ([]api.APISandboxComparisonReport, error)
	ListAPISandboxComparisonBaselines(ctx context.Context, candidate Deployment) ([]api.APISandboxComparisonBaseline, error)
}

type apiSandboxComparison struct {
	id, baselineRunID, candidateRunID string
	createdAt                         time.Time
	approvalPins                      APISandboxApprovalPins
}

func validateAPISandboxComparisonApps(baseline, candidate App) error {
	if !IsAPISandbox(baseline) || !IsAPISandbox(candidate) ||
		!baseline.Manifest.APISandboxRun || !candidate.Manifest.APISandboxRun ||
		baseline.Manifest.APISandboxPartner || candidate.Manifest.APISandboxPartner ||
		baseline.APISandboxRunPartnerWebhook || candidate.APISandboxRunPartnerWebhook ||
		baseline.AccountID != candidate.AccountID || baseline.OrgID != candidate.OrgID ||
		baseline.PreviewOfSlug != candidate.PreviewOfSlug ||
		baseline.Manifest.APISandboxSourceDeploymentID == candidate.Manifest.APISandboxSourceDeploymentID ||
		baseline.APISandboxRunID == "" || candidate.APISandboxRunID == "" || baseline.APISandboxRunID == candidate.APISandboxRunID ||
		baseline.APISandboxScenario == nil || baseline.APISandboxScenario != candidate.APISandboxScenario {
		return ErrInvalidArgument
	}
	return nil
}

func (s *PgStore) CreateAPISandboxComparison(ctx context.Context, id string, baseline, candidate App, limits api.Limits) ([2]App, error) {
	return s.createAPISandboxComparison(ctx, id, baseline, candidate, limits, APISandboxApprovalPins{})
}

func (s *PgStore) createAPISandboxComparison(ctx context.Context, id string, baseline, candidate App, limits api.Limits, pins APISandboxApprovalPins) ([2]App, error) {
	if err := validateAPISandboxComparisonApps(baseline, candidate); err != nil {
		return [2]App{}, err
	}
	return retryTrafficSerialization(ctx, func() ([2]App, error) {
		tx, err := s.beginTrafficPolicyMutation(ctx, uuidToPgtype(candidate.AccountID))
		if err != nil {
			return [2]App{}, fmt.Errorf("state: begin sandbox comparison: %w", err)
		}
		defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
		var created [2]App
		for i, app := range [2]App{baseline, candidate} {
			created[i], err = createAppIfUnderQuotaTx(ctx, tx, app, limits)
			if err != nil {
				return [2]App{}, err
			}
		}
		if err := sqlc.New().CreateAPISandboxComparison(ctx, tx, sqlc.CreateAPISandboxComparisonParams{
			ID: mustPgUUID(id), BaselineRunID: mustPgUUID(baseline.APISandboxRunID), CandidateRunID: mustPgUUID(candidate.APISandboxRunID),
		}); err != nil {
			return [2]App{}, mapErr(err)
		}
		if pins.Candidate != "" {
			if err := sqlc.New().CreateAPISandboxApprovalCheck(ctx, tx, sqlc.CreateAPISandboxApprovalCheckParams{
				ComparisonID: mustPgUUID(id), BaselineArtifactPin: pins.Baseline, CandidateArtifactPin: pins.Candidate,
			}); err != nil {
				return [2]App{}, mapErr(err)
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return [2]App{}, fmt.Errorf("state: commit sandbox comparison: %w", err)
		}
		return created, nil
	})
}

func sandboxComparisonFromRows(id string, at time.Time, baselineRow, candidateRow sqlc.ApiSandboxRun) (api.APISandboxComparisonReport, error) {
	baseline, err := apiSandboxRunFromRow(baselineRow, nil)
	if err != nil {
		return api.APISandboxComparisonReport{}, err
	}
	candidate, err := apiSandboxRunFromRow(candidateRow, nil)
	if err != nil {
		return api.APISandboxComparisonReport{}, err
	}
	return api.CompareAPISandboxRuns(id, at, baseline.APISandboxRunReport, candidate.APISandboxRunReport), nil
}

func (s *PgStore) GetAPISandboxComparison(ctx context.Context, id string) (api.APISandboxComparisonReport, error) {
	row, err := sqlc.New().GetAPISandboxComparison(ctx, s.pool, mustPgUUID(id))
	if err != nil {
		return api.APISandboxComparisonReport{}, mapErr(err)
	}
	return sandboxComparisonFromRows(id, row.CreatedAt.Time.UTC(), row.ApiSandboxRun, row.ApiSandboxRun_2)
}

func (s *PgStore) ListAPISandboxComparisons(ctx context.Context, candidateID string) ([]api.APISandboxComparisonReport, error) {
	rows, err := sqlc.New().ListAPISandboxComparisons(ctx, s.pool, mustPgUUID(candidateID))
	if err != nil {
		return nil, mapErr(err)
	}
	comparisons := make([]api.APISandboxComparisonReport, 0, len(rows))
	for _, row := range rows {
		report, err := sandboxComparisonFromRows(pgUUIDString(row.ID), row.CreatedAt.Time.UTC(), row.ApiSandboxRun, row.ApiSandboxRun_2)
		if err != nil {
			return nil, err
		}
		comparisons = append(comparisons, report)
	}
	return comparisons, nil
}

func (s *PgStore) ListAPISandboxComparisonBaselines(ctx context.Context, candidate Deployment) ([]api.APISandboxComparisonBaseline, error) {
	rows, err := sqlc.New().ListAPISandboxComparisonBaselines(ctx, s.pool, sqlc.ListAPISandboxComparisonBaselinesParams{
		AppID: mustPgUUID(candidate.AppID), CandidateID: mustPgUUID(candidate.ID), CandidateCreatedAt: pgTimestamptzUTC(candidate.CreatedAt),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	baselines := make([]api.APISandboxComparisonBaseline, 0, len(rows))
	for _, row := range rows {
		baselines = append(baselines, api.APISandboxComparisonBaseline{
			DeploymentID: pgUUIDString(row.ID), CommitSHA: row.CommitSha, CreatedAt: row.CreatedAt.Time.UTC(),
		})
	}
	return baselines, nil
}

func (m *MemStore) CreateAPISandboxComparison(ctx context.Context, id string, baseline, candidate App, limits api.Limits) ([2]App, error) {
	return m.createAPISandboxComparison(ctx, id, baseline, candidate, limits, APISandboxApprovalPins{})
}

func (m *MemStore) createAPISandboxComparison(ctx context.Context, id string, baseline, candidate App, limits api.Limits, pins APISandboxApprovalPins) ([2]App, error) {
	if err := validateAPISandboxComparisonApps(baseline, candidate); err != nil {
		return [2]App{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.apiSandboxComparisons[id]; ok {
		return [2]App{}, ErrConflict
	}

	staged := maps.Clone(m.apps)
	var created [2]App
	for i, app := range [2]App{baseline, candidate} {
		if _, ok := m.apiSandboxRuns[app.APISandboxRunID]; ok {
			return [2]App{}, ErrConflict
		}
		row, err := m.prepareAppIfUnderQuotaLocked(app, limits, staged)
		if err != nil {
			return [2]App{}, err
		}
		staged[row.ID], created[i] = row, row
	}
	if err := appTrafficBindingError(m.checkMemTrafficBindingLocked(ctx, candidate.AccountID, nil, "", memTrafficPolicyChange{Apps: staged})); err != nil {
		return [2]App{}, err
	}
	if err := m.enrollNewAppsLocked(created[:]); err != nil {
		return [2]App{}, err
	}

	m.apps = staged
	for i, app := range created {
		m.ensureServiceAddressIndexLocked(&app)
		app = m.recordAPISandboxAppLocked(app)
		m.apps[app.ID], created[i] = app, app
	}
	if m.apiSandboxComparisons == nil {
		m.apiSandboxComparisons = make(map[string]apiSandboxComparison)
	}
	m.apiSandboxComparisons[id] = apiSandboxComparison{
		id: id, baselineRunID: baseline.APISandboxRunID, candidateRunID: candidate.APISandboxRunID,
		createdAt: time.Now().UTC(), approvalPins: pins,
	}
	return created, nil
}

func (m *MemStore) sandboxComparisonLocked(comparison apiSandboxComparison) (api.APISandboxComparisonReport, bool) {
	baseline, haveBaseline := m.apiSandboxRuns[comparison.baselineRunID]
	candidate, haveCandidate := m.apiSandboxRuns[comparison.candidateRunID]
	if !haveBaseline || !haveCandidate {
		return api.APISandboxComparisonReport{}, false
	}
	b, c := publicAPISandboxRunReport(baseline.APISandboxRunReport), publicAPISandboxRunReport(candidate.APISandboxRunReport)
	b.Steps = append([]api.APISandboxRunStep{}, b.Steps...)
	c.Steps = append([]api.APISandboxRunStep{}, c.Steps...)
	return api.CompareAPISandboxRuns(comparison.id, comparison.createdAt, b, c), true
}

func (m *MemStore) GetAPISandboxComparison(_ context.Context, id string) (api.APISandboxComparisonReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	stored := m.apiSandboxComparisons[id]
	report, ok := m.sandboxComparisonLocked(stored)
	if !ok || stored.approvalPins.Candidate != "" {
		return api.APISandboxComparisonReport{}, ErrNotFound
	}
	return report, nil
}

func (m *MemStore) ListAPISandboxComparisons(_ context.Context, candidateID string) ([]api.APISandboxComparisonReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	comparisons := make([]api.APISandboxComparisonReport, 0)
	for _, comparison := range m.apiSandboxComparisons {
		if comparison.approvalPins.Candidate == "" && m.apiSandboxRuns[comparison.candidateRunID].SourceDeploymentID == candidateID {
			if report, ok := m.sandboxComparisonLocked(comparison); ok {
				comparisons = append(comparisons, report)
			}
		}
	}
	sort.Slice(comparisons, func(i, j int) bool {
		return sandboxComparisonBefore(comparisons[j].CreatedAt, comparisons[j].ID, comparisons[i].CreatedAt, comparisons[i].ID)
	})
	return comparisons[:min(len(comparisons), 25)], nil
}

func (m *MemStore) ListAPISandboxComparisonBaselines(_ context.Context, candidate Deployment) ([]api.APISandboxComparisonBaseline, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	baselines := make([]api.APISandboxComparisonBaseline, 0)
	for _, d := range m.deployments {
		if d.AppID == candidate.AppID && sandboxComparisonBefore(d.CreatedAt, d.ID, candidate.CreatedAt, candidate.ID) && APISandboxSourceSupported(d) {
			baselines = append(baselines, api.APISandboxComparisonBaseline{DeploymentID: d.ID, CommitSHA: d.CommitSHA, CreatedAt: d.CreatedAt})
		}
	}
	sort.Slice(baselines, func(i, j int) bool {
		return sandboxComparisonBefore(baselines[j].CreatedAt, baselines[j].DeploymentID, baselines[i].CreatedAt, baselines[i].DeploymentID)
	})
	return baselines[:min(len(baselines), 25)], nil
}

func sandboxComparisonBefore(a time.Time, aID string, b time.Time, bID string) bool {
	return a.Before(b) || (a.Equal(b) && aID < bID)
}
