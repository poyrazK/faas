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
		ID:                  uuid.NewString(),
		AccountID:           resolved.AccountID,
		AppID:               resolved.AppID,
		DeploymentID:        resolved.DeploymentID,
		CronID:              resolved.CronID,
		ScheduledFor:        cloneAppTaskTimePtr(resolved.ScheduledFor),
		Kind:                resolved.Kind,
		Command:             append([]string(nil), resolved.Command...),
		CommandShell:        resolved.CommandShell,
		DeploymentScope:     normalizedDeploymentScope(deployment.Scope),
		ArtifactKey:         deployment.RootfsKey,
		ImageDigest:         deployment.ImageDigest,
		Status:              AppTaskQueued,
		TimeoutSeconds:      resolved.TimeoutSeconds,
		MaxOutputBytes:      resolved.MaxOutputBytes,
		RetryMax:            resolved.RetryMax,
		RetryBackoffSeconds: resolved.RetryBackoffSeconds,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	m.appTasks[task.ID] = task
	return cloneAppTask(task), nil
}

func (m *MemStore) CreateScheduledCronAppTask(_ context.Context, cronID string, expectedLastFiredAt *time.Time, firedAt time.Time) (AppTask, bool, error) {
	if cronID == "" || firedAt.IsZero() {
		return AppTask{}, false, ErrAppTaskInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureAppTasksLocked()
	cron, ok := m.crons[cronID]
	if !ok || !cron.Enabled || cron.SuspendedReason != "" || len(cron.Command) == 0 ||
		!sameTimePointer(nonZeroTimePtr(cron.LastFiredAt), expectedLastFiredAt) {
		return AppTask{}, false, nil
	}
	firedAt = firedAt.UTC()
	if expectedLastFiredAt != nil && !firedAt.After(*expectedLastFiredAt) {
		return AppTask{}, false, nil
	}
	app, ok := m.apps[cron.AppID]
	if !ok || app.Status == AppDeleted {
		return AppTask{}, false, ErrAppTaskDeploymentUnavailable
	}
	var deployment Deployment
	for _, candidate := range m.deployments {
		if candidate.AppID != app.ID || candidate.Status != DeployLive ||
			candidate.RootfsKey == "" || candidate.ImageDigest == "" {
			continue
		}
		if deployment.ID == "" || (candidate.TrafficPercent > 0 && deployment.TrafficPercent == 0) ||
			((candidate.TrafficPercent > 0) == (deployment.TrafficPercent > 0) && candidate.CreatedAt.After(deployment.CreatedAt)) {
			deployment = candidate
		}
	}
	if deployment.ID == "" {
		return AppTask{}, false, ErrAppTaskDeploymentUnavailable
	}
	cron.LastFiredAt = firedAt
	m.crons[cronID] = cron
	task := AppTask{
		ID: uuid.NewString(), AccountID: app.AccountID, AppID: app.ID, DeploymentID: deployment.ID,
		CronID: cronID, ScheduledFor: cloneAppTaskTimePtr(&firedAt), Kind: AppTaskKindCron,
		Command: append([]string(nil), cron.Command...), CommandShell: cron.CommandShell,
		DeploymentScope: normalizedDeploymentScope(deployment.Scope), ArtifactKey: deployment.RootfsKey,
		ImageDigest: deployment.ImageDigest, Status: AppTaskQueued,
		TimeoutSeconds: cron.CommandTimeoutSeconds, MaxOutputBytes: cron.CommandMaxOutputBytes,
		RetryMax: cron.RetryMax, RetryBackoffSeconds: cron.RetryBackoffSeconds,
		CreatedAt: firedAt, UpdatedAt: firedAt,
	}
	m.appTasks[task.ID] = task
	return cloneAppTask(task), true, nil
}

func (m *MemStore) CountActiveCronAppTasks(_ context.Context, cronID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, task := range m.appTasks {
		if task.CronID == cronID && (task.Status == AppTaskQueued || task.Status == AppTaskRestoring || task.Status == AppTaskRunning) {
			count++
		}
	}
	return count, nil
}

func (m *MemStore) ListCronAppTaskRuns(_ context.Context, cronID string, limit int, before string) ([]AppTask, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 10
	}
	var cursor *AppTask
	if before != "" {
		if task, ok := m.appTasks[before]; ok && task.CronID == cronID {
			copyTask := task
			cursor = &copyTask
		}
	}
	matched := make([]AppTask, 0)
	for _, task := range m.appTasks {
		if task.CronID != cronID {
			continue
		}
		if cursor != nil && (!task.CreatedAt.Before(cursor.CreatedAt) &&
			(!task.CreatedAt.Equal(cursor.CreatedAt) || task.ID >= cursor.ID)) {
			continue
		}
		matched = append(matched, cloneAppTask(task))
	}
	sort.Slice(matched, func(i, j int) bool {
		if matched[i].CreatedAt.Equal(matched[j].CreatedAt) {
			return matched[i].ID > matched[j].ID
		}
		return matched[i].CreatedAt.After(matched[j].CreatedAt)
	})
	if len(matched) > limit {
		matched = matched[:limit]
	}
	return matched, nil
}

func nonZeroTimePtr(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
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

func (m *MemStore) ReleaseAppTaskByDeployment(_ context.Context, deploymentID string) (AppTask, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, task := range m.appTasks {
		if task.DeploymentID == deploymentID && task.Kind == AppTaskKindRelease {
			return cloneAppTask(task), nil
		}
	}
	return AppTask{}, ErrNotFound
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
	var selectedPriority time.Time
	for id, candidate := range m.appTasks {
		if candidate.Status != AppTaskQueued || candidate.CancelRequested != nil || candidate.CreatedAt.After(claimedAt) ||
			(candidate.RetryAt != nil && candidate.RetryAt.After(claimedAt)) {
			continue
		}
		priority := candidate.CreatedAt
		if candidate.RetryAt != nil {
			priority = *candidate.RetryAt
		}
		if selected == nil || priority.Before(selectedPriority) ||
			(priority.Equal(selectedPriority) && (candidate.CreatedAt.Before(selected.CreatedAt) ||
				(candidate.CreatedAt.Equal(selected.CreatedAt) && candidate.ID < selected.ID))) {
			copyCandidate := candidate
			copyCandidate.ID = id
			selected = &copyCandidate
			selectedPriority = priority
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
	selected.RetryAt = nil
	selected.StdoutTail = ""
	selected.StderrTail = ""
	selected.OutputTruncated = false
	selected.ExitCode = nil
	selected.FailureCode = nil
	selected.FailureMessage = nil
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
		*task.LeaseToken != leaseToken || task.CancelRequested != nil || task.LeaseExpiresAt == nil || !task.LeaseExpiresAt.After(startedAt) {
		return AppTask{}, ErrAppTaskLeaseLost
	}
	if startedAt.Before(task.CreatedAt) {
		return AppTask{}, ErrAppTaskInvalid
	}
	task.Status = AppTaskRunning
	task.StartedAt = appTaskTimePtr(startedAt)
	task.AttemptCount++
	task.RetryAt = nil
	task.StdoutTail = ""
	task.StderrTail = ""
	task.OutputTruncated = false
	task.ExitCode = nil
	task.FailureCode = nil
	task.FailureMessage = nil
	task.UpdatedAt = startedAt
	m.appTasks[task.ID] = task
	return cloneAppTask(task), nil
}

func (m *MemStore) RenewAppTaskLease(_ context.Context, taskID, leaseToken string, renewedAt time.Time, leaseDuration time.Duration) error {
	if taskID == "" || leaseToken == "" || renewedAt.IsZero() || leaseDuration <= 0 {
		return ErrAppTaskInvalid
	}
	renewedAt = renewedAt.UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	task, ok := m.appTasks[taskID]
	if !ok || (task.Status != AppTaskRestoring && task.Status != AppTaskRunning) ||
		task.LeaseToken == nil || *task.LeaseToken != leaseToken || task.CancelRequested != nil ||
		task.LeaseExpiresAt == nil || !task.LeaseExpiresAt.After(renewedAt) {
		return ErrAppTaskLeaseLost
	}
	expiresAt := renewedAt.Add(leaseDuration).UTC()
	task.LeaseExpiresAt = &expiresAt
	task.UpdatedAt = renewedAt
	m.appTasks[task.ID] = task
	return nil
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
		task.RetryAt = nil
		task.FailureCode = nil
		task.FailureMessage = nil
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
	if task.CancelRequested != nil && params.Status != AppTaskCancelled {
		return AppTask{}, ErrAppTaskCancellationPending
	}
	if params.Status == AppTaskSucceeded && task.Status != AppTaskRunning {
		return AppTask{}, fmt.Errorf("%w: a task must be running before it can succeed", ErrAppTaskInvalid)
	}
	finishedAt := params.FinishedAt.UTC()
	if finishedAt.Before(task.CreatedAt) {
		return AppTask{}, ErrAppTaskInvalid
	}
	retryAt := cronAppTaskRetryAt(task, task.Status, params.Status, finishedAt)
	task.Status = params.Status
	if retryAt != nil {
		task.Status = AppTaskQueued
		task.RetryAt = retryAt
	}
	task.StdoutTail = params.StdoutTail
	task.StderrTail = params.StderrTail
	task.OutputTruncated = params.OutputTruncated
	task.ExitCode = cloneAppTaskIntPtr(params.ExitCode)
	task.FailureCode = cloneAppTaskStringPtr(params.FailureCode)
	task.FailureMessage = cloneAppTaskStringPtr(params.FailureMessage)
	if retryAt == nil {
		task.FinishedAt = appTaskTimePtr(finishedAt)
	} else {
		task.FinishedAt = nil
		task.StartedAt = nil
	}
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
