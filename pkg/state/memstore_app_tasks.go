package state

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
)

var _ AppTaskStore = (*MemStore)(nil)

func (m *MemStore) ensureAppTasksLocked() {
	if m.appTasks == nil {
		m.appTasks = make(map[string]AppTask)
	}
}

func (m *MemStore) CreateAppTask(_ context.Context, params CreateAppTaskParams) (AppTask, error) {
	resolved, err := resolveCreateAppTask(params)
	if err != nil {
		return AppTask{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureAppTasksLocked()

	app, ok := m.apps[resolved.AppID]
	if !ok || app.AccountID != resolved.AccountID || app.Status == AppDeleted {
		return AppTask{}, ErrAppTaskDeploymentUnavailable
	}
	deployment, ok := m.deployments[resolved.DeploymentID]
	if !ok || !validAppTaskDeployment(deployment, app.ID) {
		return AppTask{}, ErrAppTaskDeploymentUnavailable
	}
	if resolved.Kind == AppTaskKindRelease {
		for _, task := range m.appTasks {
			if task.DeploymentID == resolved.DeploymentID && task.Kind == AppTaskKindRelease {
				return AppTask{}, ErrConflict
			}
		}
	}

	now := resolved.CreatedAt
	task := AppTask{
		ID:              uuid.NewString(),
		AccountID:       resolved.AccountID,
		AppID:           resolved.AppID,
		DeploymentID:    resolved.DeploymentID,
		Kind:            resolved.Kind,
		Command:         append([]string(nil), resolved.Command...),
		CommandShell:    resolved.CommandShell,
		DeploymentScope: normalizedDeploymentScope(deployment.Scope),
		ArtifactKey:     deployment.RootfsKey,
		ImageDigest:     deployment.ImageDigest,
		Status:          AppTaskQueued,
		TimeoutSeconds:  resolved.TimeoutSeconds,
		MaxOutputBytes:  resolved.MaxOutputBytes,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	m.appTasks[task.ID] = task
	return cloneAppTask(task), nil
}

func (m *MemStore) AppTaskByID(_ context.Context, accountID, appID, taskID string) (AppTask, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	task, ok := m.appTasks[taskID]
	if !ok || task.AccountID != accountID || task.AppID != appID {
		return AppTask{}, ErrNotFound
	}
	return cloneAppTask(task), nil
}

func (m *MemStore) ListAppTasks(_ context.Context, accountID, appID string, limit, offset int) ([]AppTask, error) {
	limit, offset = normalizeAppTaskPage(limit, offset)
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := make([]AppTask, 0)
	for _, task := range m.appTasks {
		if task.AccountID == accountID && task.AppID == appID {
			rows = append(rows, cloneAppTask(task))
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			return rows[i].ID > rows[j].ID
		}
		return rows[i].CreatedAt.After(rows[j].CreatedAt)
	})
	if offset >= len(rows) {
		return []AppTask{}, nil
	}
	end := offset + limit
	if end > len(rows) {
		end = len(rows)
	}
	return rows[offset:end], nil
}

func (m *MemStore) ClaimNextAppTask(_ context.Context, owner string, claimedAt time.Time, leaseDuration time.Duration) (AppTask, error) {
	if owner == "" || leaseDuration <= 0 || claimedAt.IsZero() {
		return AppTask{}, ErrAppTaskInvalid
	}
	claimedAt = claimedAt.UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureAppTasksLocked()

	var selected *AppTask
	for id, candidate := range m.appTasks {
		if candidate.Status != AppTaskQueued || candidate.CancelRequested != nil || candidate.CreatedAt.After(claimedAt) {
			continue
		}
		if selected == nil || candidate.CreatedAt.Before(selected.CreatedAt) ||
			(candidate.CreatedAt.Equal(selected.CreatedAt) && candidate.ID < selected.ID) {
			copyCandidate := candidate
			copyCandidate.ID = id
			selected = &copyCandidate
		}
	}
	if selected == nil {
		return AppTask{}, ErrNotFound
	}
	token := uuid.NewString()
	ownerCopy := owner
	expires := claimedAt.Add(leaseDuration)
	selected.Status = AppTaskRestoring
	selected.LeaseToken = &token
	selected.LeaseOwner = &ownerCopy
	selected.LeaseExpiresAt = &expires
	selected.UpdatedAt = claimedAt
	m.appTasks[selected.ID] = *selected
	return cloneAppTask(*selected), nil
}

func (m *MemStore) MarkAppTaskRunning(_ context.Context, taskID, leaseToken string, startedAt time.Time) (AppTask, error) {
	if taskID == "" || leaseToken == "" || startedAt.IsZero() {
		return AppTask{}, ErrAppTaskInvalid
	}
	startedAt = startedAt.UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	task, ok := m.appTasks[taskID]
	if !ok || task.Status != AppTaskRestoring || task.LeaseToken == nil ||
		*task.LeaseToken != leaseToken || task.LeaseExpiresAt == nil || !task.LeaseExpiresAt.After(startedAt) {
		return AppTask{}, ErrAppTaskLeaseLost
	}
	if startedAt.Before(task.CreatedAt) {
		return AppTask{}, ErrAppTaskInvalid
	}
	task.Status = AppTaskRunning
	task.StartedAt = appTaskTimePtr(startedAt)
	task.UpdatedAt = startedAt
	m.appTasks[task.ID] = task
	return cloneAppTask(task), nil
}

func (m *MemStore) RequestAppTaskCancellation(_ context.Context, accountID, appID, taskID string, requestedAt time.Time) (AppTask, error) {
	if requestedAt.IsZero() {
		requestedAt = time.Now().UTC()
	} else {
		requestedAt = requestedAt.UTC()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	task, ok := m.appTasks[taskID]
	if !ok || task.AccountID != accountID || task.AppID != appID {
		return AppTask{}, ErrNotFound
	}
	if requestedAt.Before(task.CreatedAt) {
		return AppTask{}, ErrAppTaskInvalid
	}
	if task.Status.Terminal() {
		return cloneAppTask(task), nil
	}
	if task.Status == AppTaskQueued {
		task.Status = AppTaskCancelled
		task.FinishedAt = appTaskTimePtr(requestedAt)
	} else if task.CancelRequested == nil {
		task.CancelRequested = appTaskTimePtr(requestedAt)
	}
	task.UpdatedAt = requestedAt
	m.appTasks[task.ID] = task
	return cloneAppTask(task), nil
}

func (m *MemStore) CompleteAppTask(_ context.Context, params CompleteAppTaskParams) (AppTask, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	task, ok := m.appTasks[params.ID]
	if !ok || (task.Status != AppTaskRestoring && task.Status != AppTaskRunning) ||
		task.LeaseToken == nil || *task.LeaseToken != params.LeaseToken ||
		task.LeaseExpiresAt == nil || !task.LeaseExpiresAt.After(params.FinishedAt) {
		return AppTask{}, ErrAppTaskLeaseLost
	}
	if err := validateCompleteAppTask(params, task.MaxOutputBytes); err != nil {
		return AppTask{}, err
	}
	if params.Status == AppTaskSucceeded && task.Status != AppTaskRunning {
		return AppTask{}, fmt.Errorf("%w: a task must be running before it can succeed", ErrAppTaskInvalid)
	}
	finishedAt := params.FinishedAt.UTC()
	if finishedAt.Before(task.CreatedAt) {
		return AppTask{}, ErrAppTaskInvalid
	}
	task.Status = params.Status
	task.StdoutTail = params.StdoutTail
	task.StderrTail = params.StderrTail
	task.OutputTruncated = params.OutputTruncated
	task.ExitCode = cloneAppTaskIntPtr(params.ExitCode)
	task.FailureCode = cloneAppTaskStringPtr(params.FailureCode)
	task.FailureMessage = cloneAppTaskStringPtr(params.FailureMessage)
	task.FinishedAt = appTaskTimePtr(finishedAt)
	task.UpdatedAt = finishedAt
	task.LeaseToken = nil
	task.LeaseOwner = nil
	task.LeaseExpiresAt = nil
	m.appTasks[task.ID] = task
	return cloneAppTask(task), nil
}

func (m *MemStore) SweepExpiredAppTasks(_ context.Context, at time.Time) (AppTaskSweepResult, error) {
	if at.IsZero() {
		return AppTaskSweepResult{}, ErrAppTaskInvalid
	}
	at = at.UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	result := AppTaskSweepResult{}
	for id, task := range m.appTasks {
		if task.LeaseExpiresAt == nil || task.LeaseExpiresAt.After(at) {
			continue
		}
		switch task.Status {
		case AppTaskRestoring:
			if task.CancelRequested != nil {
				task.Status = AppTaskCancelled
				task.FinishedAt = appTaskTimePtr(at)
				result.Cancelled++
			} else {
				task.Status = AppTaskQueued
				result.RequeuedRestores++
			}
		case AppTaskRunning:
			if task.CancelRequested != nil {
				task.Status = AppTaskCancelled
				result.Cancelled++
			} else {
				code, message := "lease_expired", "the app task worker lease expired after dispatch; the command was not replayed"
				task.Status = AppTaskFailed
				task.FailureCode = &code
				task.FailureMessage = &message
				result.FailedRuns++
			}
			task.FinishedAt = appTaskTimePtr(at)
		default:
			continue
		}
		task.LeaseToken = nil
		task.LeaseOwner = nil
		task.LeaseExpiresAt = nil
		task.UpdatedAt = at
		m.appTasks[id] = task
	}
	return result, nil
}

// cancelAppTasksForAppLocked mirrors the PostgreSQL app-deletion cascade.
// Queued work never starts after deletion; an already restoring/running VM is
// asked to cancel and remains leased until its scheduler acknowledges teardown.
func (m *MemStore) cancelAppTasksForAppLocked(appID string, at time.Time) {
	for id, task := range m.appTasks {
		if task.AppID != appID || task.Status.Terminal() {
			continue
		}
		if task.Status == AppTaskQueued {
			task.Status = AppTaskCancelled
			task.FinishedAt = appTaskTimePtr(at)
		} else if task.CancelRequested == nil {
			task.CancelRequested = appTaskTimePtr(at)
		}
		task.UpdatedAt = at
		m.appTasks[id] = task
	}
}

func appTaskTimePtr(value time.Time) *time.Time {
	copyValue := value
	return &copyValue
}
