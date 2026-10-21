package state

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ProjectReleaseRemovalsStore records the services an atomic release retires
// (ADR-20261021120000). A retirement uses the service addition record: it
// serves until the release publishes, then stays dormant (omitted, not
// deployable outside a release) until rollback restores it or the app is deleted.
type ProjectReleaseRemovalsStore interface {
	StageProjectReleaseRemovals(ctx context.Context, accountID, attemptID, projectID string, safe *api.ProjectSafeRelease) error
}

func validateRemovalPlan(safe *api.ProjectSafeRelease) error {
	if safe == nil || !safe.RemoveServices || safe.AddServices || len(safe.Removals) == 0 || safe.Worker != nil || safe.ReleaseCommand != nil || safe.Configuration != nil || safe.Verification != nil && safe.Verification.Progressive {
		return ErrInvalidArgument
	}
	seen := map[string]bool{}
	for _, removal := range safe.Removals {
		if removal.AppID == "" || seen[removal.AppID] {
			return ErrInvalidArgument
		}
		seen[removal.AppID] = true
	}
	return nil
}

// checkRemovalCandidate accepts a baseline member whose settings still match
// the plan and whose record, if any, is serving and owned by a settled attempt.
func checkRemovalCandidate(app App, projectID string, inBaseline bool, removal api.ProjectReleaseRemoval, record projectReleaseAdditionRecord, recorded bool, ownerAdvancing bool, attemptID string) error {
	if app.ID != removal.AppID || app.ProjectID != projectID || app.Status == AppDeleted || app.PreviewOfSlug != "" || !inBaseline || ProjectReleaseAdditionFingerprint(app) != removal.Fingerprint {
		return ErrConflict
	}
	if recorded && (record.Dormant || record.AttemptID != attemptID && ownerAdvancing) {
		return ErrConflict
	}
	return nil
}

func (m *MemStore) StageProjectReleaseRemovals(_ context.Context, accountID, attemptID, projectID string, safe *api.ProjectSafeRelease) error {
	if err := validateRemovalPlan(safe); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	project, found := m.projects[projectID]
	entry, reserved := m.projectSubmissions[accountID+"\x00"+attemptID]
	active := m.activeProjectReleaseSets[releaseKey(projectID, "production")]
	if !found || project.AccountID != accountID || !reserved || entry.Status != "processing" || active != safe.PreviousReleaseID {
		return ErrConflict
	}
	members := map[string]bool{}
	for _, member := range m.projectReleaseSets[active].Members {
		members[member.AppID] = true
	}
	for _, removal := range safe.Removals {
		record, recorded := m.projectReleaseAdditions[removal.AppID]
		advancing := recorded && projectSubmissionAdvancing(m.projectSubmissions[accountID+"\x00"+record.AttemptID].Payload)
		if err := checkRemovalCandidate(m.apps[removal.AppID], projectID, members[removal.AppID], removal, record, recorded, advancing, attemptID); err != nil {
			return err
		}
	}
	if m.projectReleaseAdditions == nil {
		m.projectReleaseAdditions = map[string]projectReleaseAdditionRecord{}
	}
	for _, removal := range safe.Removals {
		m.projectReleaseAdditions[removal.AppID] = projectReleaseAdditionRecord{removal.AppID, attemptID, removal.Fingerprint, false}
	}
	return nil
}

func (s *PgStore) StageProjectReleaseRemovals(ctx context.Context, accountID, attemptID, projectID string, safe *api.ProjectSafeRelease) error {
	if err := validateRemovalPlan(safe); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	entry, err := q.LockSafeProjectSubmission(ctx, tx, sqlc.LockSafeProjectSubmissionParams{AccountID: accountID, AttemptID: attemptID})
	if err != nil {
		return mapErr(err)
	}
	if entry.Status != "processing" {
		return ErrConflict
	}
	if _, err := q.LockReleaseAdditionProject(ctx, tx, sqlc.LockReleaseAdditionProjectParams{ProjectID: projectID, AccountID: accountID}); err != nil {
		return mapErr(err)
	}
	baseline, err := readProjectReleaseSetTx(ctx, tx, accountID, projectID, "production", safe.PreviousReleaseID)
	if err != nil {
		return err
	}
	if active, err := activeProjectReleaseSetIDTx(ctx, tx, projectID, "production"); err != nil || active != safe.PreviousReleaseID {
		return ErrConflict
	}
	members := map[string]bool{}
	for _, member := range baseline.Members {
		members[member.AppID] = true
	}
	records, err := readReleaseAdditionsDB(ctx, tx, projectID)
	if err != nil {
		return err
	}
	for _, removal := range safe.Removals {
		app, err := releaseAdditionAppDB(ctx, tx, removal.AppID)
		if err != nil {
			return err
		}
		record, recorded := records[removal.AppID]
		advancing := false
		if recorded && record.AttemptID != attemptID {
			owner, err := q.ReadProjectSubmission(ctx, tx, sqlc.ReadProjectSubmissionParams{AccountID: accountID, AttemptID: record.AttemptID})
			if err != nil && !errors.Is(mapErr(err), ErrNotFound) {
				return err
			}
			advancing = err == nil && projectSubmissionAdvancing(owner.Payload)
		}
		if err := checkRemovalCandidate(app, projectID, members[removal.AppID], removal, record, recorded, advancing, attemptID); err != nil {
			return err
		}
		n, err := q.StageProjectReleaseRemoval(ctx, tx, sqlc.StageProjectReleaseRemovalParams{AppID: removal.AppID, AccountID: accountID, ProjectID: projectID, AttemptID: attemptID, Fingerprint: removal.Fingerprint})
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrConflict
		}
	}
	return tx.Commit(ctx)
}
