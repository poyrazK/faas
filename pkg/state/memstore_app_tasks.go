package state

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/exclusivework"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

var _ AppTaskStore = (*MemStore)(nil)

func (m *MemStore) ensureAppTasksLocked() {
	if m.appTasks == nil {
		m.appTasks = make(map[string]AppTask)
	}
}

func (m *MemStore) appTaskExclusiveCurrentLocked(task AppTask, at time.Time) bool {
	if task.ExclusiveOperationID == "" {
		return task.ExclusiveGeneration == 0
	}
	operation, ok := m.exclusiveOperations[task.ExclusiveOperationID]
	if !ok || operation.AccountID != task.AccountID || operation.AppID != task.AppID ||
		operation.State != "running" || operation.Generation != task.ExclusiveGeneration ||
		operation.LeaseExpiresAt == nil || operation.AttemptDeadline == nil {
		return false
	}
	return operation.LeaseExpiresAt.After(at) && operation.AttemptDeadline.After(at)
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
	if !ok || !validAppTaskDeployment(deployment, app.ID) || resolved.RequireLiveDeployment && deployment.Status != DeployLive {
		return AppTask{}, ErrAppTaskDeploymentUnavailable
	}
	if resolved.ExclusiveOperationID != "" {
		probe := AppTask{ID: "", AccountID: resolved.AccountID, AppID: resolved.AppID,
			ExclusiveOperationID: resolved.ExclusiveOperationID, ExclusiveGeneration: resolved.ExclusiveGeneration}
		if !m.appTaskExclusiveCurrentLocked(probe, resolved.CreatedAt) {
			return AppTask{}, ErrNotFound
		}
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
		BindingVerification:  cloneBindingVerificationPin(resolved.BindingVerification),
		FailureRules:         workpolicy.Clone(resolved.FailureRules),
		OccurrenceID:         resolved.OccurrenceID,
		StartDeadlineAt:      cloneAppTaskTimePtr(resolved.StartDeadlineAt),
		ID:                   uuid.NewString(),
		AccountID:            resolved.AccountID,
		AppID:                resolved.AppID,
		ExclusiveOperationID: resolved.ExclusiveOperationID,
		ExclusiveGeneration:  resolved.ExclusiveGeneration,
		DeploymentID:         resolved.DeploymentID,
		CronID:               resolved.CronID,
		ScheduledFor:         cloneAppTaskTimePtr(resolved.ScheduledFor),
		Kind:                 resolved.Kind,
		Command:              append([]string(nil), resolved.Command...),
		CommandShell:         resolved.CommandShell,
		DeploymentScope:      normalizedDeploymentScope(deployment.Scope),
		ArtifactKey:          deployment.RootfsKey,
		ImageDigest:          deployment.ImageDigest,
		Status:               AppTaskQueued,
		TimeoutSeconds:       resolved.TimeoutSeconds,
		MaxOutputBytes:       resolved.MaxOutputBytes,
		RetryMax:             resolved.RetryMax,
		RetryBackoffSeconds:  resolved.RetryBackoffSeconds,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	m.appTasks[task.ID] = task
	if resolved.Interactive != nil {
		if m.appTaskAttach == nil {
			m.appTaskAttach = make(map[string]AppTaskAttach)
		}
		m.appTaskAttach[task.ID] = AppTaskAttach{
			TaskID: task.ID, TTY: resolved.Interactive.TTY,
			TokenSHA256: append([]byte(nil), resolved.Interactive.TokenSHA256...), CreatedAt: now,
		}
	}
	return cloneAppTask(task), nil
}

var _ AppTaskAttachStore = (*MemStore)(nil)

func (m *MemStore) AppTaskAttachByTask(_ context.Context, taskID string) (AppTaskAttach, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	record, ok := m.appTaskAttach[taskID]
	if !ok {
		return AppTaskAttach{}, ErrNotFound
	}
	return cloneAppTaskAttach(record), nil
}

func (m *MemStore) RecordAppTaskAttachNode(_ context.Context, taskID, leaseToken, nodeID string, recordedAt time.Time) error {
	if taskID == "" || leaseToken == "" || !validAppTaskAttachNodeID(nodeID) || recordedAt.IsZero() {
		return ErrAppTaskInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	record, ok := m.appTaskAttach[taskID]
	task, taskOK := m.appTasks[taskID]
	if !ok || !taskOK || task.Status != AppTaskRunning || task.LeaseToken == nil || *task.LeaseToken != leaseToken {
		return ErrAppTaskLeaseLost
	}
	recordedAt = recordedAt.UTC()
	record.NodeID = nodeID
	record.NodeRecordedAt = &recordedAt
	m.appTaskAttach[taskID] = record
	return nil
}

func (m *MemStore) AppTaskAttachTarget(_ context.Context, accountID, appID, taskID string) (AppTaskAttachTarget, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	record, ok := m.appTaskAttach[taskID]
	task, taskOK := m.appTasks[taskID]
	if !ok || !taskOK || task.AccountID != accountID || task.AppID != appID {
		return AppTaskAttachTarget{}, ErrNotFound
	}
	return AppTaskAttachTarget{TaskID: task.ID, TaskStatus: task.Status, TTY: record.TTY, NodeID: record.NodeID}, nil
}

func (m *MemStore) ListAppTasksByExclusiveOperation(_ context.Context, accountID, operationID string) ([]AppTask, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := make([]AppTask, 0)
	for _, task := range m.appTasks {
		if task.AccountID == accountID && task.ExclusiveOperationID == operationID {
			rows = append(rows, cloneAppTask(task))
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].ExclusiveGeneration == rows[j].ExclusiveGeneration {
			return rows[i].CreatedAt.Before(rows[j].CreatedAt)
		}
		return rows[i].ExclusiveGeneration < rows[j].ExclusiveGeneration
	})
	return rows, nil
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
	var deadline *time.Time
	if cron.SchedulePolicy != nil {
		deadline = cron.SchedulePolicy.Deadline(firedAt)
	}
	task := AppTask{
		FailureRules: workpolicy.Clone(cron.FailureRules), StartDeadlineAt: deadline,
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

// CreateScheduledCronAppTaskOccurrence atomically records the policy decision,
// advances the cron cursor, and queues the command task when the occurrence is
// admitted. A replace disposition waits until the previous task is confirmed
// stopped; it never advances the cursor while that task remains active.
func (m *MemStore) CreateScheduledCronAppTaskOccurrence(_ context.Context, cronID string, expectedLastFiredAt *time.Time, evaluatedAt time.Time, options CronScheduledOccurrenceOptions) (AppTask, ScheduleOccurrence, bool, error) {
	if cronID == "" || evaluatedAt.IsZero() {
		return AppTask{}, ScheduleOccurrence{}, false, ErrAppTaskInvalid
	}
	evaluatedAt = evaluatedAt.UTC()
	scheduledFor := options.ScheduledFor.UTC()
	if options.ScheduledFor.IsZero() {
		scheduledFor = evaluatedAt
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureAppTasksLocked()
	cron, ok := m.crons[cronID]
	if !ok || !cron.Enabled || cron.SuspendedReason != "" || len(cron.Command) == 0 ||
		!sameTimePointer(nonZeroTimePtr(cron.LastFiredAt), expectedLastFiredAt) ||
		(options.ScheduleRevision > 0 && cron.ScheduleRevision != options.ScheduleRevision) {
		return AppTask{}, ScheduleOccurrence{}, false, nil
	}
	app, ok := m.apps[cron.AppID]
	if !ok || app.Status == AppDeleted {
		return AppTask{}, ScheduleOccurrence{}, false, ErrAppTaskDeploymentUnavailable
	}
	if expectedLastFiredAt != nil && !scheduledFor.After(*expectedLastFiredAt) {
		return AppTask{}, ScheduleOccurrence{}, false, nil
	}
	policy := effectiveCronSchedulePolicy(cron)
	deadline := policy.Deadline(scheduledFor)
	status, reason, blocker := "queued", "", ""
	if options.Disposition != "" {
		if options.Disposition != "coalesced" && options.Disposition != "missed_deadline" {
			return AppTask{}, ScheduleOccurrence{}, false, ErrInvalidArgument
		}
		status, reason = options.Disposition, options.Reason
	} else if workpolicy.DeadlineMissed(deadline, evaluatedAt) {
		status, reason = "missed_deadline", "start deadline expired before the scheduler could dispatch the occurrence"
	}
	if status == "queued" && (policy.Overlap != "allow" || (options.ExclusiveAdmission != nil && cron.SkipIfRunning)) {
		for _, active := range m.appTasks {
			if active.CronID != cronID || active.Status.Terminal() {
				continue
			}
			if options.ExclusiveAdmission != nil && active.ExclusiveOperationID != "" {
				continue
			}
			blocker = active.OccurrenceID
			if policy.Overlap == "replace" && options.ExclusiveAdmission == nil {
				return AppTask{}, ScheduleOccurrence{}, false, nil
			}
			status, reason = "skipped_overlap", "an earlier task for this cron is still active"
			break
		}
	}
	var deployment Deployment
	if status == "queued" {
		for _, candidate := range m.deployments {
			if candidate.AppID != app.ID || candidate.Status != DeployLive || candidate.RootfsKey == "" || candidate.ImageDigest == "" {
				continue
			}
			if deployment.ID == "" || (candidate.TrafficPercent > 0 && deployment.TrafficPercent == 0) ||
				((candidate.TrafficPercent > 0) == (deployment.TrafficPercent > 0) && candidate.CreatedAt.After(deployment.CreatedAt)) {
				deployment = candidate
			}
		}
		if deployment.ID == "" {
			return AppTask{}, ScheduleOccurrence{}, false, ErrAppTaskDeploymentUnavailable
		}
	}
	var exclusiveOperationID string
	var exclusiveTx *exclusiveMemoryTx
	if status == "queued" && options.ExclusiveAdmission != nil {
		admission := *options.ExclusiveAdmission
		if err := validateExclusiveCommandCronAdmission(admission, app.AccountID, cron.AppID, cronID); err != nil {
			return AppTask{}, ScheduleOccurrence{}, false, ErrInvalidArgument
		}
		exclusiveTx = m.exclusiveMemoryTxLocked()
		operation, joined, admitErr := admitExclusiveTransaction(exclusiveTx, admission)
		if errors.Is(admitErr, exclusivework.ErrBusy) {
			status, reason = "skipped_overlap", "managed operation lane is busy under the configured contention policy"
			exclusiveTx = nil
		} else if admitErr != nil {
			return AppTask{}, ScheduleOccurrence{}, false, fmt.Errorf("state: admit scheduled command cron operation: %w", admitErr)
		} else {
			exclusiveOperationID = operation.ID
			if joined {
				status, reason = "coalesced", "joined an equivalent active managed operation"
			} else {
				status = "pending"
			}
		}
	}
	cron.LastFiredAt = scheduledFor
	m.crons[cronID] = cron
	occurrence := ScheduleOccurrence{
		ID: newUUIDString(), AccountID: app.AccountID, CronID: cronID,
		ScheduleRevision: cron.ScheduleRevision, ScheduledFor: scheduledFor,
		StartDeadlineAt: deadline, SchedulePolicy: *workpolicy.Clone(policy),
		Status: status, Reason: reason, BlockingOccurrenceID: blocker,
		ExclusiveOperationID: exclusiveOperationID,
		CreatedAt:            evaluatedAt, UpdatedAt: evaluatedAt,
	}
	if status != "queued" {
		m.scheduleOccurrences[occurrence.ID] = occurrence
		if exclusiveTx != nil {
			m.commitExclusiveMemoryTxLocked(exclusiveTx)
		}
		return AppTask{}, cloneScheduleOccurrence(occurrence), exclusiveOperationID != "" && status == "pending", nil
	}
	if exclusiveOperationID != "" {
		m.scheduleOccurrences[occurrence.ID] = occurrence
		m.commitExclusiveMemoryTxLocked(exclusiveTx)
		return AppTask{}, cloneScheduleOccurrence(occurrence), true, nil
	}
	task := AppTask{
		FailureRules: workpolicy.Clone(cron.FailureRules), OccurrenceID: occurrence.ID,
		StartDeadlineAt: cloneAppTaskTimePtr(deadline),
		ID:              newUUIDString(), AccountID: app.AccountID, AppID: app.ID, DeploymentID: deployment.ID,
		CronID: cronID, ScheduledFor: cloneAppTaskTimePtr(&scheduledFor), Kind: AppTaskKindCron,
		Command: append([]string(nil), cron.Command...), CommandShell: cron.CommandShell,
		DeploymentScope: normalizedDeploymentScope(deployment.Scope), ArtifactKey: deployment.RootfsKey,
		ImageDigest: deployment.ImageDigest, Status: AppTaskQueued,
		TimeoutSeconds: cron.CommandTimeoutSeconds, MaxOutputBytes: cron.CommandMaxOutputBytes,
		RetryMax: cron.RetryMax, RetryBackoffSeconds: cron.RetryBackoffSeconds,
		CreatedAt: evaluatedAt, UpdatedAt: evaluatedAt,
	}
	occurrence.AppTaskID = task.ID
	m.scheduleOccurrences[occurrence.ID] = occurrence
	m.appTasks[task.ID] = task
	return cloneAppTask(task), cloneScheduleOccurrence(occurrence), true, nil
}

// CreateExclusiveCommandCronAppTask materializes the saved command only while
// the supplied operation generation is current. The occurrence or fire-now
// receipt is linked to the task under the same MemStore lock.
func (m *MemStore) CreateExclusiveCommandCronAppTask(_ context.Context, accountID, appID, operationID string, generation int64, expectedCronID string, createdAt time.Time) (AppTask, error) {
	if accountID == "" || appID == "" || operationID == "" || expectedCronID == "" || generation <= 0 || createdAt.IsZero() {
		return AppTask{}, ErrAppTaskInvalid
	}
	createdAt = createdAt.UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureAppTasksLocked()
	probe := AppTask{AccountID: accountID, AppID: appID, ExclusiveOperationID: operationID, ExclusiveGeneration: generation}
	if !m.appTaskExclusiveCurrentLocked(probe, createdAt) {
		return AppTask{}, exclusivework.ErrStaleOwner
	}
	for _, existing := range m.appTasks {
		if existing.ExclusiveOperationID == operationID && existing.ExclusiveGeneration == generation {
			return cloneAppTask(existing), nil
		}
	}

	var occurrence *ScheduleOccurrence
	for id, candidate := range m.scheduleOccurrences {
		if candidate.ExclusiveOperationID != operationID || candidate.Status == "coalesced" {
			continue
		}
		if occurrence == nil || candidate.CreatedAt.Before(occurrence.CreatedAt) ||
			(candidate.CreatedAt.Equal(occurrence.CreatedAt) && id < occurrence.ID) {
			copyOccurrence := candidate
			occurrence = &copyOccurrence
		}
	}
	cronID := ""
	if occurrence != nil {
		cronID = occurrence.CronID
	} else {
		for _, request := range m.fireNowRequests {
			if request.OperationID != nil && *request.OperationID == operationID {
				cronID = request.CronID
				break
			}
		}
	}
	if cronID == "" || cronID != expectedCronID {
		return AppTask{}, ErrNotFound
	}
	cron, ok := m.crons[cronID]
	if !ok || cron.AppID != appID {
		return AppTask{}, ErrNotFound
	}
	if !cron.Enabled {
		return AppTask{}, ErrAppTaskCronDisabled
	}
	if cron.SuspendedReason != "" {
		return AppTask{}, ErrAppTaskCronSuspended
	}
	if len(cron.Command) == 0 {
		return AppTask{}, ErrAppTaskInvalid
	}
	if cron.SkipIfRunning {
		for _, active := range m.appTasks {
			if active.CronID == cron.ID && active.ExclusiveOperationID == "" &&
				(active.Status == AppTaskQueued || active.Status == AppTaskRestoring || active.Status == AppTaskRunning) {
				return AppTask{}, ErrAppTaskCronOverlap
			}
		}
	}
	app, ok := m.apps[appID]
	if !ok || app.AccountID != accountID || app.Status == AppDeleted {
		return AppTask{}, ErrNotFound
	}
	var deployment Deployment
	for _, candidate := range m.deployments {
		if candidate.AppID != appID || candidate.Status != DeployLive || candidate.RootfsKey == "" || candidate.ImageDigest == "" {
			continue
		}
		if deployment.ID == "" || (candidate.TrafficPercent > 0 && deployment.TrafficPercent == 0) ||
			((candidate.TrafficPercent > 0) == (deployment.TrafficPercent > 0) && candidate.CreatedAt.After(deployment.CreatedAt)) {
			deployment = candidate
		}
	}
	if deployment.ID == "" {
		return AppTask{}, ErrAppTaskDeploymentUnavailable
	}
	task := AppTask{
		ID: uuid.NewString(), AccountID: accountID, AppID: appID, DeploymentID: deployment.ID,
		ExclusiveOperationID: operationID, ExclusiveGeneration: generation,
		CronID: cronID, Kind: AppTaskKindCron, Command: append([]string(nil), cron.Command...),
		CommandShell: cron.CommandShell, DeploymentScope: normalizedDeploymentScope(deployment.Scope),
		ArtifactKey: deployment.RootfsKey, ImageDigest: deployment.ImageDigest, Status: AppTaskQueued,
		TimeoutSeconds: cron.CommandTimeoutSeconds, MaxOutputBytes: cron.CommandMaxOutputBytes,
		RetryMax: cron.RetryMax, RetryBackoffSeconds: cron.RetryBackoffSeconds,
		CreatedAt: createdAt, UpdatedAt: createdAt,
	}
	if occurrence != nil {
		task.OccurrenceID = occurrence.ID
		task.ScheduledFor = cloneAppTaskTimePtr(&occurrence.ScheduledFor)
		task.StartDeadlineAt = cloneAppTaskTimePtr(occurrence.StartDeadlineAt)
		task.FailureRules = workpolicy.Clone(cron.FailureRules)
		occurrence.AppTaskID = task.ID
		occurrence.Status = "queued"
		occurrence.Reason = ""
		occurrence.FinishedAt = nil
		occurrence.UpdatedAt = createdAt
		m.scheduleOccurrences[occurrence.ID] = *occurrence
	}
	m.appTasks[task.ID] = task
	for id, request := range m.fireNowRequests {
		if request.OperationID != nil && *request.OperationID == operationID {
			request.TaskID = &task.ID
			m.fireNowRequests[id] = request
		}
	}
	return cloneAppTask(task), nil
}

// AdmitExclusiveCommandCronFireNow couples operation acceptance to the
// fire-now request receipt so a retry cannot enqueue a second command run.
func (m *MemStore) AdmitExclusiveCommandCronFireNow(_ context.Context, requestID string, firedAt time.Time, admission ExclusiveAdmission) (ExclusiveOperation, bool, error) {
	if requestID == "" || firedAt.IsZero() {
		return ExclusiveOperation{}, false, ErrAppTaskInvalid
	}
	firedAt = firedAt.UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	request, ok := m.fireNowRequests[requestID]
	if !ok || request.Status != FireNowStatusRunning {
		return ExclusiveOperation{}, false, ErrFireNowRequestNotFound
	}
	cron, ok := m.crons[request.CronID]
	if !ok {
		return ExclusiveOperation{}, false, ErrNotFound
	}
	if !cron.Enabled {
		return ExclusiveOperation{}, false, ErrAppTaskCronDisabled
	}
	if cron.SuspendedReason != "" {
		return ExclusiveOperation{}, false, ErrAppTaskCronSuspended
	}
	if len(cron.Command) == 0 || validateExclusiveCommandCronAdmission(admission, request.AccountID, cron.AppID, cron.ID) != nil {
		return ExclusiveOperation{}, false, ErrAppTaskInvalid
	}
	app, ok := m.apps[cron.AppID]
	if !ok || app.AccountID != request.AccountID || app.Status == AppDeleted {
		return ExclusiveOperation{}, false, ErrNotFound
	}
	deploymentAvailable := false
	for _, deployment := range m.deployments {
		if deployment.AppID == app.ID && deployment.Status == DeployLive && deployment.RootfsKey != "" && deployment.ImageDigest != "" {
			deploymentAvailable = true
			break
		}
	}
	if !deploymentAvailable {
		return ExclusiveOperation{}, false, ErrAppTaskDeploymentUnavailable
	}
	ownerTx := m.exclusiveMemoryTxLocked()
	operation, joined, err := admitExclusiveTransaction(ownerTx, admission)
	if err != nil {
		return ExclusiveOperation{}, false, err
	}
	if cron.SkipIfRunning {
		for _, task := range m.appTasks {
			if task.CronID == cron.ID && task.ExclusiveOperationID == "" &&
				(task.Status == AppTaskQueued || task.Status == AppTaskRestoring || task.Status == AppTaskRunning) {
				return ExclusiveOperation{}, false, ErrAppTaskCronOverlap
			}
		}
	}
	finishedAt := firedAt
	if ownerNow, nowErr := ownerTx.now(); nowErr == nil {
		finishedAt = ownerNow
	}
	request.Status = FireNowStatusSucceeded
	request.OperationID = &operation.ID
	request.FinishedAt = &finishedAt
	m.fireNowRequests[requestID] = request
	m.commitExclusiveMemoryTxLocked(ownerTx)
	return cloneExclusiveOperation(operation), joined, nil
}

// CreateManualCronAppTaskForFireNow queues a command cron without changing
// its scheduled cursor. The fire-now request and task are updated together
// under the store lock so a replay cannot enqueue a second task.
func (m *MemStore) CreateManualCronAppTaskForFireNow(_ context.Context, requestID string, firedAt time.Time) (AppTask, error) {
	if requestID == "" || firedAt.IsZero() {
		return AppTask{}, ErrAppTaskInvalid
	}
	firedAt = firedAt.UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureAppTasksLocked()

	request, ok := m.fireNowRequests[requestID]
	if !ok {
		return AppTask{}, ErrFireNowRequestNotFound
	}
	if request.TaskID != nil {
		if task, found := m.appTasks[*request.TaskID]; found {
			return cloneAppTask(task), nil
		}
	}
	if request.Status != FireNowStatusRunning {
		return AppTask{}, ErrFireNowRequestNotFound
	}
	cron, ok := m.crons[request.CronID]
	if !ok {
		return AppTask{}, ErrNotFound
	}
	if !cron.Enabled {
		return AppTask{}, ErrAppTaskCronDisabled
	}
	if cron.SuspendedReason != "" {
		return AppTask{}, ErrAppTaskCronSuspended
	}
	if len(cron.Command) == 0 {
		return AppTask{}, ErrAppTaskInvalid
	}
	app, ok := m.apps[cron.AppID]
	if !ok || app.AccountID != request.AccountID || app.Status == AppDeleted {
		return AppTask{}, ErrNotFound
	}
	if cron.SkipIfRunning {
		for _, active := range m.appTasks {
			if active.CronID == cron.ID && (active.Status == AppTaskQueued || active.Status == AppTaskRestoring || active.Status == AppTaskRunning) {
				return AppTask{}, ErrAppTaskCronOverlap
			}
		}
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
		return AppTask{}, ErrAppTaskDeploymentUnavailable
	}

	task := AppTask{
		ID: uuid.NewString(), AccountID: app.AccountID, AppID: app.ID, DeploymentID: deployment.ID,
		CronID: cron.ID, Kind: AppTaskKindCron,
		Command: append([]string(nil), cron.Command...), CommandShell: cron.CommandShell,
		DeploymentScope: normalizedDeploymentScope(deployment.Scope), ArtifactKey: deployment.RootfsKey,
		ImageDigest: deployment.ImageDigest, Status: AppTaskQueued,
		TimeoutSeconds: cron.CommandTimeoutSeconds, MaxOutputBytes: cron.CommandMaxOutputBytes,
		RetryMax: cron.RetryMax, RetryBackoffSeconds: cron.RetryBackoffSeconds,
		CreatedAt: firedAt, UpdatedAt: firedAt,
	}
	m.appTasks[task.ID] = task
	request.Status = FireNowStatusSucceeded
	request.TaskID = &task.ID
	finishedAt := time.Now().UTC()
	request.FinishedAt = &finishedAt
	m.fireNowRequests[requestID] = request
	return cloneAppTask(task), nil
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
			(candidate.RetryAt != nil && candidate.RetryAt.After(claimedAt)) ||
			!m.appTaskExclusiveCurrentLocked(candidate, claimedAt) {
			continue
		}
		if workpolicy.DeadlineMissed(candidate.StartDeadlineAt, claimedAt) && !m.appTaskOccurrenceStartedLocked(candidate) {
			candidate.Status = AppTaskCancelled
			candidate.FinishedAt = appTaskTimePtr(claimedAt)
			candidate.RetryAt = nil
			candidate.UpdatedAt = claimedAt
			m.appTasks[id] = candidate
			m.syncAppTaskOccurrenceLocked(candidate, claimedAt)
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
	m.syncAppTaskOccurrenceLocked(*selected, claimedAt)
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
		*task.LeaseToken != leaseToken || task.CancelRequested != nil || task.LeaseExpiresAt == nil || !task.LeaseExpiresAt.After(startedAt) ||
		!m.appTaskExclusiveCurrentLocked(task, startedAt) {
		return AppTask{}, ErrAppTaskLeaseLost
	}
	if startedAt.Before(task.CreatedAt) {
		return AppTask{}, ErrAppTaskInvalid
	}
	if workpolicy.DeadlineMissed(task.StartDeadlineAt, startedAt) && !m.appTaskOccurrenceStartedLocked(task) {
		task.Status = AppTaskCancelled
		task.FinishedAt = appTaskTimePtr(startedAt)
		task.RetryAt = nil
		task.LeaseToken = nil
		task.LeaseOwner = nil
		task.LeaseExpiresAt = nil
		task.UpdatedAt = startedAt
		m.appTasks[task.ID] = task
		m.syncAppTaskOccurrenceLocked(task, startedAt)
		return AppTask{}, ErrAppTaskLeaseLost
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
	m.syncAppTaskOccurrenceLocked(task, startedAt)
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
		task.LeaseExpiresAt == nil || !task.LeaseExpiresAt.After(renewedAt) ||
		!m.appTaskExclusiveCurrentLocked(task, renewedAt) {
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
	m.syncAppTaskOccurrenceLocked(task, requestedAt)
	return cloneAppTask(task), nil
}

func (m *MemStore) CompleteAppTask(_ context.Context, params CompleteAppTaskParams) (AppTask, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	task, ok := m.appTasks[params.ID]
	if !ok || (task.Status != AppTaskRestoring && task.Status != AppTaskRunning) ||
		task.LeaseToken == nil || *task.LeaseToken != params.LeaseToken ||
		task.LeaseExpiresAt == nil || !task.LeaseExpiresAt.After(params.FinishedAt) ||
		!m.appTaskExclusiveCurrentLocked(task, params.FinishedAt) {
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
	decision := workpolicy.Evaluate(task.FailureRules, workpolicy.Evidence{
		Succeeded: params.Status == AppTaskSucceeded, Cancelled: params.Status == AppTaskCancelled,
		Infra: task.Status == AppTaskRestoring, ExitCode: params.ExitCode, OutcomeCode: params.OutcomeCode,
	})
	if decision.Reason == "outcome_code_matched" && params.Status == AppTaskSucceeded {
		params.Status = AppTaskFailed
		code, message := "classified_outcome", "command reported an application outcome classified by policy"
		params.FailureCode, params.FailureMessage = &code, &message
	}
	finishedAt := params.FinishedAt.UTC()
	if finishedAt.Before(task.CreatedAt) {
		return AppTask{}, ErrAppTaskInvalid
	}
	task.WorkDecision, task.OutcomeCode = &decision, params.OutcomeCode
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
	task.WorkDecision = &decision
	task.OutcomeCode = params.OutcomeCode
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
	m.syncAppTaskOccurrenceLocked(task, finishedAt)
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
				var decision *workpolicy.Decision
				if task.FailureRules != nil {
					evaluated := workpolicy.Evaluate(task.FailureRules, workpolicy.Evidence{Uncertain: true})
					decision = &evaluated
					message = "completion receipt missing; the command outcome is uncertain"
				}
				task.Status = AppTaskFailed
				task.FailureCode = &code
				task.FailureMessage = &message
				task.WorkDecision = workpolicy.Clone(decision)
				if decision != nil && decision.Action == "retry" {
					retryAt := cronAppTaskRetryAt(task, AppTaskRunning, AppTaskFailed, at)
					if retryAt != nil {
						task.Status = AppTaskQueued
						task.RetryAt = retryAt
						task.FinishedAt = nil
						task.StartedAt = nil
					}
				}
				if task.Status == AppTaskFailed {
					result.FailedRuns++
				}
			}
			if task.Status == AppTaskFailed || task.Status == AppTaskCancelled {
				task.FinishedAt = appTaskTimePtr(at)
			}
		default:
			continue
		}
		task.LeaseToken = nil
		task.LeaseOwner = nil
		task.LeaseExpiresAt = nil
		task.UpdatedAt = at
		m.appTasks[id] = task
		m.syncAppTaskOccurrenceLocked(task, at)
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
		m.syncAppTaskOccurrenceLocked(task, at)
	}
}

func (m *MemStore) appTaskOccurrenceStartedLocked(task AppTask) bool {
	if task.OccurrenceID == "" {
		return task.StartedAt != nil
	}
	occurrence, ok := m.scheduleOccurrences[task.OccurrenceID]
	return ok && occurrence.StartedAt != nil
}

func (m *MemStore) syncAppTaskOccurrenceLocked(task AppTask, now time.Time) {
	if task.OccurrenceID == "" {
		return
	}
	occurrence, ok := m.scheduleOccurrences[task.OccurrenceID]
	if !ok {
		return
	}
	switch task.Status {
	case AppTaskQueued:
		if occurrence.StartedAt != nil {
			occurrence.Status = "running"
		} else {
			occurrence.Status = "queued"
		}
	case AppTaskRestoring, AppTaskRunning:
		occurrence.Status = "running"
	case AppTaskSucceeded:
		occurrence.Status = "succeeded"
	case AppTaskFailed, AppTaskTimedOut:
		occurrence.Status = "failed"
	case AppTaskCancelled:
		occurrence.Status = "cancelled"
	default:
		return
	}
	if task.StartedAt != nil && occurrence.StartedAt == nil {
		occurrence.StartedAt = cloneTimePtr(task.StartedAt)
	}
	terminal := task.Status.Terminal()
	if terminal && occurrence.StartedAt == nil && workpolicy.DeadlineMissed(occurrence.StartDeadlineAt, now) {
		occurrence.Status = "missed_deadline"
		occurrence.Reason = "command task did not start before the occurrence start deadline"
	}
	if terminal || occurrence.Status == "missed_deadline" {
		finished := now.UTC()
		occurrence.FinishedAt = &finished
	} else {
		occurrence.FinishedAt = nil
	}
	occurrence.UpdatedAt = now.UTC()
	m.scheduleOccurrences[occurrence.ID] = occurrence
}

func appTaskTimePtr(value time.Time) *time.Time {
	copyValue := value
	return &copyValue
}
