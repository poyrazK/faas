package state

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ OrgActivityAppLifecycleMutationStore = (*MemStore)(nil)

func (m *MemStore) CreateAppIfUnderQuotaWithActivity(_ context.Context, app App, limits api.Limits, entry OrgActivity) (App, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	created, err := m.createAppIfUnderQuotaLocked(app, limits)
	if err != nil {
		return App{}, 0, err
	}
	if created.OrgID == "" && entry.OrgID == uuid.Nil {
		// Legacy MemStore fixtures can create accounts without a personal
		// org. Keep app creation compatible for those non-organization-owned
		// rows; production accounts always have a personal org, and callers
		// with an explicit org continue through the atomic activity path.
		return created, 0, nil
	}
	entry, err = bindOrgActivityToApp(entry, created)
	if err != nil {
		delete(m.apps, created.ID)
		delete(m.applicationStandardEnrollments, created.ID)
		m.eraseStandardAppExceptionsLocked(created.ID)
		return App{}, 0, err
	}
	return created, m.enqueueOrgActivityOutboxLocked(entry), nil
}

func (m *MemStore) ScheduleAppDeletionWithActivity(_ context.Context, id string, graceUntil time.Time, entry OrgActivity) (App, int64, error) {
	return m.scheduleAppDeletion(id, graceUntil, &entry)
}

func (m *MemStore) scheduleAppDeletion(id string, graceUntil time.Time, entry *OrgActivity) (App, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.apps[id]
	if !ok {
		return App{}, 0, ErrNotFound
	}
	for _, b := range m.objectBuckets {
		if b.AppID == id && b.State != "deleted" {
			return App{}, 0, ErrConflict
		}
	}
	wasDeleted := a.Status == AppDeleted
	var normalized OrgActivity
	if entry != nil && !wasDeleted {
		var err error
		normalized, err = bindOrgActivityToApp(*entry, a)
		if err != nil {
			return App{}, 0, err
		}
	}
	if graceUntil.IsZero() {
		graceUntil = time.Now().UTC().Add(AppDeleteGraceDuration())
	}
	now := time.Now().UTC()
	if a.DeletedAt == nil {
		a.DeletedAt = &now
	}
	if a.DeleteGraceUntil == nil {
		deadline := graceUntil.UTC()
		a.DeleteGraceUntil = &deadline
	}
	a.Status = AppDeleted
	m.apps[id] = a
	m.cancelAppTasksForAppLocked(id, now)
	if !wasDeleted {
		delete(m.appDeletionClaims, id)
	}
	for cronID, cron := range m.crons {
		if cron.AppID == id {
			cron.SuspendedReason = CronSuspendedAppDeleted
			m.crons[cronID] = cron
		}
	}
	for i := range m.snapshots {
		deployment, ok := m.deployments[m.snapshots[i].DeploymentID]
		if ok && deployment.AppID == id {
			m.deleteSnapshotReplicasLocked(m.snapshots[i].ID)
		}
	}
	var outboxID int64
	if entry != nil && !wasDeleted {
		outboxID = m.enqueueOrgActivityOutboxLocked(normalized)
	}
	return a, outboxID, nil
}

func (m *MemStore) RestoreAppWithActivity(_ context.Context, id string, limits api.Limits, entry OrgActivity) (App, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.apps[id]
	if !ok {
		return App{}, 0, ErrNotFound
	}
	entry, err := bindOrgActivityToApp(entry, a)
	if err != nil {
		return App{}, 0, err
	}
	restored, err := m.restoreAppLocked(id, limits)
	if err != nil {
		return App{}, 0, err
	}
	return restored, m.enqueueOrgActivityOutboxLocked(entry), nil
}
