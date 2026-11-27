package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type APISandboxApprovalPins struct {
	Baseline  string
	Candidate string
}

type APISandboxApprovalEvidence struct {
	Comparison api.APISandboxComparisonReport
	Pins       APISandboxApprovalPins
}

type APISandboxApprovalCheckStore interface {
	CreateAPISandboxApprovalCheck(context.Context, string, App, App, api.Limits, APISandboxApprovalPins) ([2]App, error)
	GetAPISandboxApprovalCheck(context.Context, string) (APISandboxApprovalEvidence, error)
	ListAPISandboxApprovalChecks(context.Context, string) ([]APISandboxApprovalEvidence, error)
}

// APISandboxArtifactPin fingerprints the deployment inputs copied into the
// sandbox. It is internal and excludes production environment overrides.
func APISandboxArtifactPin(source Deployment) string {
	raw, err := json.Marshal(apiSandboxCloneArtifact(source))
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func APISandboxApprovalCandidateSupported(source Deployment) bool {
	return source.Status == DeploySnapshotting && IsProductionScope(source.Scope) &&
		!source.EnvironmentWorkloadHeld() && !source.EnvironmentWorkloadManaged() && apiSandboxArtifactAvailable(source)
}

func validSandboxApprovalPins(pins APISandboxApprovalPins) bool {
	return len(pins.Baseline) == sha256.Size*2 && len(pins.Candidate) == sha256.Size*2
}

func (s *PgStore) CreateAPISandboxApprovalCheck(ctx context.Context, id string, baseline, candidate App, limits api.Limits, pins APISandboxApprovalPins) ([2]App, error) {
	if !validSandboxApprovalPins(pins) {
		return [2]App{}, ErrInvalidArgument
	}
	return s.createAPISandboxComparison(ctx, id, baseline, candidate, limits, pins)
}

func sandboxApprovalEvidenceFromRows(id string, at time.Time, baseline, candidate sqlc.ApiSandboxRun, baselinePin, candidatePin string) (APISandboxApprovalEvidence, error) {
	comparison, err := sandboxComparisonFromRows(id, at, baseline, candidate)
	return APISandboxApprovalEvidence{Comparison: comparison, Pins: APISandboxApprovalPins{Baseline: baselinePin, Candidate: candidatePin}}, err
}

func (s *PgStore) GetAPISandboxApprovalCheck(ctx context.Context, id string) (APISandboxApprovalEvidence, error) {
	row, err := sqlc.New().GetAPISandboxApprovalCheck(ctx, s.pool, mustPgUUID(id))
	if err != nil {
		return APISandboxApprovalEvidence{}, mapErr(err)
	}
	return sandboxApprovalEvidenceFromRows(id, row.CreatedAt.Time.UTC(), row.ApiSandboxRun, row.ApiSandboxRun_2, row.BaselineArtifactPin, row.CandidateArtifactPin)
}

func (s *PgStore) ListAPISandboxApprovalChecks(ctx context.Context, candidateID string) ([]APISandboxApprovalEvidence, error) {
	rows, err := sqlc.New().ListAPISandboxApprovalChecks(ctx, s.pool, mustPgUUID(candidateID))
	if err != nil {
		return nil, mapErr(err)
	}
	evidence := make([]APISandboxApprovalEvidence, 0, len(rows))
	for _, row := range rows {
		entry, err := sandboxApprovalEvidenceFromRows(pgUUIDString(row.ID), row.CreatedAt.Time.UTC(), row.ApiSandboxRun, row.ApiSandboxRun_2, row.BaselineArtifactPin, row.CandidateArtifactPin)
		if err != nil {
			return nil, err
		}
		evidence = append(evidence, entry)
	}
	return evidence, nil
}

func (m *MemStore) CreateAPISandboxApprovalCheck(ctx context.Context, id string, baseline, candidate App, limits api.Limits, pins APISandboxApprovalPins) ([2]App, error) {
	if !validSandboxApprovalPins(pins) {
		return [2]App{}, ErrInvalidArgument
	}
	return m.createAPISandboxComparison(ctx, id, baseline, candidate, limits, pins)
}

func (m *MemStore) GetAPISandboxApprovalCheck(_ context.Context, id string) (APISandboxApprovalEvidence, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row := m.apiSandboxComparisons[id]
	comparison, ok := m.sandboxComparisonLocked(row)
	if !ok || row.approvalPins.Candidate == "" {
		return APISandboxApprovalEvidence{}, ErrNotFound
	}
	return APISandboxApprovalEvidence{Comparison: comparison, Pins: row.approvalPins}, nil
}

func (m *MemStore) ListAPISandboxApprovalChecks(_ context.Context, candidateID string) ([]APISandboxApprovalEvidence, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	evidence := make([]APISandboxApprovalEvidence, 0)
	suiteChecks := make(map[string]bool)
	for id, run := range m.apiSandboxSuiteRuns {
		if run.CandidateDeploymentID == candidateID {
			for _, item := range m.apiSandboxSuiteScenarios[id] {
				suiteChecks[item.ComparisonID] = true
			}
		}
	}
	for _, row := range m.apiSandboxComparisons {
		if suiteChecks[row.id] || row.approvalPins.Candidate == "" || m.apiSandboxRuns[row.candidateRunID].SourceDeploymentID != candidateID {
			continue
		}
		if comparison, ok := m.sandboxComparisonLocked(row); ok {
			evidence = append(evidence, APISandboxApprovalEvidence{Comparison: comparison, Pins: row.approvalPins})
		}
	}
	sort.Slice(evidence, func(i, j int) bool {
		a, b := evidence[i].Comparison, evidence[j].Comparison
		return sandboxComparisonBefore(b.CreatedAt, b.ID, a.CreatedAt, a.ID)
	})
	return evidence[:min(len(evidence), 25)], nil
}

func sandboxCloneSourceEligible(source Deployment, pin string, candidate bool) bool {
	if pin != "" && APISandboxArtifactPin(source) != pin {
		return false
	}
	if candidate {
		return pin != "" && APISandboxApprovalCandidateSupported(source)
	}
	return APISandboxSourceSupported(source)
}

func validateSandboxCloneSourceTx(ctx context.Context, tx pgx.Tx, appID string, source Deployment) error {
	q := sqlc.New()
	pin, err := q.GetAPISandboxApprovalPinForApp(ctx, tx, mustPgUUID(appID))
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return mapErr(err)
	}
	if !sandboxCloneSourceEligible(source, pin.ArtifactPin, pin.Candidate) {
		return ErrConflict
	}
	if pin.Candidate {
		hasTask, err := q.APISandboxSourceHasReleaseTask(ctx, tx, mustPgUUID(source.ID))
		if err != nil {
			return mapErr(err)
		}
		if hasTask {
			return ErrConflict
		}
	}
	return nil
}

func (m *MemStore) validateSandboxCloneSourceLocked(appID string, source Deployment) error {
	var pin string
	var candidate bool
	for _, row := range m.apiSandboxComparisons {
		if row.approvalPins.Candidate == "" {
			continue
		}
		if m.apiSandboxRuns[row.candidateRunID].AppID == appID {
			pin, candidate = row.approvalPins.Candidate, true
			break
		}
		if m.apiSandboxRuns[row.baselineRunID].AppID == appID {
			pin = row.approvalPins.Baseline
			break
		}
	}
	if !sandboxCloneSourceEligible(source, pin, candidate) {
		return ErrConflict
	}
	if candidate {
		for _, task := range m.appTasks {
			if task.DeploymentID == source.ID && task.Kind == AppTaskKindRelease {
				return ErrConflict
			}
		}
	}
	return nil
}
