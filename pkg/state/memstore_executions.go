package state

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ ExecutionStore = (*MemStore)(nil)

type executionPayload struct {
	sealed    []byte
	kid       string
	createdAt time.Time
}

type executionUsageLedgerRow struct {
	ExecutionID  string
	AccountID    string
	Runtime      api.ExecutionRuntime
	Status       api.ExecutionStatus
	WallTimeMS   int64
	CPUTimeMS    int64
	PeakMemoryMB int64
	OutputBytes  int64
	StartedAt    *time.Time
	FinishedAt   time.Time
	CreatedAt    time.Time
}

func cloneExecution(row Execution) Execution {
	row.Result = append([]byte(nil), row.Result...)
	if row.LeaseToken != nil {
		value := *row.LeaseToken
		row.LeaseToken = &value
	}
	if row.LeaseOwner != nil {
		value := *row.LeaseOwner
		row.LeaseOwner = &value
	}
	if row.LeaseExpiresAt != nil {
		value := *row.LeaseExpiresAt
		row.LeaseExpiresAt = &value
	}
	if row.CancelRequested != nil {
		value := *row.CancelRequested
		row.CancelRequested = &value
	}
	if row.ExitCode != nil {
		value := *row.ExitCode
		row.ExitCode = &value
	}
	if row.FailureCode != nil {
		value := *row.FailureCode
		row.FailureCode = &value
	}
	if row.FailureMessage != nil {
		value := *row.FailureMessage
		row.FailureMessage = &value
	}
	if row.StartedAt != nil {
		value := *row.StartedAt
		row.StartedAt = &value
	}
	if row.FinishedAt != nil {
		value := *row.FinishedAt
		row.FinishedAt = &value
	}
	return row
}

func (m *MemStore) CreateExecution(_ context.Context, params CreateExecutionParams) (Execution, error) {
	if err := validateCreateExecution(params); err != nil {
		return Execution{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	account, ok := m.accounts[params.AccountID]
	if !ok {
		return Execution{}, ErrNotFound
	}
	planLimits, planOK := account.Plan.ExecutionLimits()
	if !planOK {
		return Execution{}, ErrExecutionsNotAllowed
	}
	if err := validateExecutionPlan(params, planLimits); err != nil {
		return Execution{}, err
	}
	active := 0
	for _, row := range m.executions {
		if row.AccountID == params.AccountID && !row.Status.Terminal() {
			active++
		}
	}
	if active >= planLimits.MaxConcurrent {
		return Execution{}, &ExecutionQuotaError{Limit: planLimits.MaxConcurrent, Observed: active + 1}
	}
	row := Execution{
		ID:          uuid.NewString(),
		AccountID:   params.AccountID,
		Runtime:     params.Request.Runtime,
		Status:      api.ExecutionStatusQueued,
		NetworkMode: params.Request.Network.Mode,
		Limits:      params.Request.Limits,
		SourceBytes: params.SourceBytes,
		InputBytes:  params.InputBytes,
		DeadlineAt:  params.DeadlineAt.UTC(),
		CreatedAt:   params.AdmittedAt.UTC(),
		UpdatedAt:   params.AdmittedAt.UTC(),
	}
	m.executions[row.ID] = row
	m.executionPayloads[row.ID] = executionPayload{
		sealed: append([]byte(nil), params.SealedPayload...), kid: params.PayloadKID,
		createdAt: params.AdmittedAt.UTC(),
	}
	m.appendExecutionEventLocked(row.AccountID, row.ID, ExecutionEventStatus, executionStatusPayload(row.Status), params.AdmittedAt)
	return cloneExecution(row), nil
}

func (m *MemStore) ExecutionByID(_ context.Context, accountID, executionID string) (Execution, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.executions[executionID]
	if !ok || row.AccountID != accountID {
		return Execution{}, ErrNotFound
	}
	return cloneExecution(row), nil
}

func (m *MemStore) ListExecutions(_ context.Context, accountID string, limit, offset int) ([]Execution, error) {
	return m.listExecutions(accountID, "", limit, offset)
}

func (m *MemStore) ListExecutionsByStatus(_ context.Context, accountID string, status api.ExecutionStatus, limit, offset int) ([]Execution, error) {
	return m.listExecutions(accountID, status, limit, offset)
}

func (m *MemStore) listExecutions(accountID string, status api.ExecutionStatus, limit, offset int) ([]Execution, error) {
	limit, offset = normalizeExecutionPage(limit, offset)
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := make([]Execution, 0)
	for _, row := range m.executions {
		if row.AccountID == accountID && (status == "" || row.Status == status) {
			rows = append(rows, cloneExecution(row))
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			return rows[i].ID > rows[j].ID
		}
		return rows[i].CreatedAt.After(rows[j].CreatedAt)
	})
	if offset >= len(rows) {
		return nil, nil
	}
	rows = rows[offset:]
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

func (m *MemStore) ExecutionQueueStats(_ context.Context, at time.Time) (ExecutionQueueStats, error) {
	if at.IsZero() {
		return ExecutionQueueStats{}, ErrExecutionInvalid
	}
	at = at.UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	stats := ExecutionQueueStats{}
	for _, row := range m.executions {
		if row.Status != api.ExecutionStatusQueued || row.CancelRequested != nil ||
			row.CreatedAt.After(at) || !row.DeadlineAt.After(at) {
			continue
		}
		stats.Queued++
		if stats.OldestCreatedAt == nil || row.CreatedAt.Before(*stats.OldestCreatedAt) {
			created := row.CreatedAt
			stats.OldestCreatedAt = &created
		}
	}
	return stats, nil
}

func (m *MemStore) ListExecutionQueueAccounts(_ context.Context, at time.Time, limit int) ([]ExecutionQueueAccount, error) {
	if at.IsZero() {
		return nil, ErrExecutionInvalid
	}
	if limit <= 0 {
		limit = 64
	}
	if limit > 1000 {
		limit = 1000
	}
	at = at.UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	byAccount := make(map[string]ExecutionQueueAccount)
	for _, row := range m.executions {
		if row.Status != api.ExecutionStatusQueued || row.CancelRequested != nil ||
			row.CreatedAt.After(at) || !row.DeadlineAt.After(at) {
			continue
		}
		entry := byAccount[row.AccountID]
		entry.AccountID = row.AccountID
		entry.Queued++
		if entry.OldestCreatedAt.IsZero() || row.CreatedAt.Before(entry.OldestCreatedAt) {
			entry.OldestCreatedAt = row.CreatedAt
		}
		byAccount[row.AccountID] = entry
	}
	accounts := make([]ExecutionQueueAccount, 0, len(byAccount))
	for _, account := range byAccount {
		accounts = append(accounts, account)
	}
	sort.Slice(accounts, func(i, j int) bool {
		return accounts[i].AccountID < accounts[j].AccountID
	})
	if len(accounts) > limit {
		accounts = accounts[:limit]
	}
	return accounts, nil
}

func (m *MemStore) ClaimExecution(_ context.Context, owner string, claimedAt time.Time, leaseDuration time.Duration) (ExecutionClaim, error) {
	return m.claimExecution(owner, "", claimedAt, leaseDuration)
}

// ClaimExecutionForAccount is the fair-queue variant used by the production
// coordinator. The account filter is applied while holding the same store lock
// as the claim, so a selected queue head cannot be displaced by another worker.
func (m *MemStore) ClaimExecutionForAccount(_ context.Context, accountID, owner string, claimedAt time.Time, leaseDuration time.Duration) (ExecutionClaim, error) {
	return m.claimExecution(owner, strings.TrimSpace(accountID), claimedAt, leaseDuration)
}

func (m *MemStore) claimExecution(owner, accountID string, claimedAt time.Time, leaseDuration time.Duration) (ExecutionClaim, error) {
	owner = strings.TrimSpace(owner)
	if owner == "" || claimedAt.IsZero() || leaseDuration <= 0 {
		return ExecutionClaim{}, fmt.Errorf("%w: claim owner, time, and positive lease are required", ErrExecutionInvalid)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var candidate *Execution
	for _, stored := range m.executions {
		row := stored
		if row.Status != api.ExecutionStatusQueued || row.CancelRequested != nil ||
			(accountID != "" && row.AccountID != accountID) ||
			row.CreatedAt.After(claimedAt) || !row.DeadlineAt.After(claimedAt) {
			continue
		}
		if candidate == nil || row.CreatedAt.Before(candidate.CreatedAt) ||
			(row.CreatedAt.Equal(candidate.CreatedAt) && row.ID < candidate.ID) {
			candidate = &row
		}
	}
	if candidate == nil {
		return ExecutionClaim{}, ErrNotFound
	}
	payload, ok := m.executionPayloads[candidate.ID]
	if !ok {
		return ExecutionClaim{}, ErrNotFound
	}
	token := uuid.NewString()
	expiresAt := claimedAt.Add(leaseDuration).UTC()
	if expiresAt.After(candidate.DeadlineAt) {
		expiresAt = candidate.DeadlineAt
	}
	claimedAt = claimedAt.UTC()
	candidate.Status = api.ExecutionStatusRestoring
	candidate.LeaseToken = &token
	candidate.LeaseOwner = &owner
	candidate.LeaseExpiresAt = &expiresAt
	candidate.UpdatedAt = claimedAt
	m.executions[candidate.ID] = *candidate
	m.appendExecutionEventLocked(candidate.AccountID, candidate.ID, ExecutionEventStatus, executionStatusPayload(candidate.Status), claimedAt)
	return ExecutionClaim{
		Execution: cloneExecution(*candidate), SealedPayload: append([]byte(nil), payload.sealed...), PayloadKID: payload.kid,
	}, nil
}

func (m *MemStore) MarkExecutionRunning(_ context.Context, executionID, leaseToken string, startedAt time.Time) (Execution, error) {
	if executionID == "" || leaseToken == "" || startedAt.IsZero() {
		return Execution{}, ErrExecutionInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.executions[executionID]
	if !ok || row.Status != api.ExecutionStatusRestoring || row.LeaseToken == nil ||
		*row.LeaseToken != leaseToken || row.CancelRequested != nil || row.LeaseExpiresAt == nil ||
		!row.LeaseExpiresAt.After(startedAt) || !row.DeadlineAt.After(startedAt) {
		return Execution{}, ErrExecutionLeaseLost
	}
	startedAt = startedAt.UTC()
	row.Status = api.ExecutionStatusRunning
	row.StartedAt = &startedAt
	row.UpdatedAt = startedAt
	m.executions[row.ID] = row
	m.appendExecutionEventLocked(row.AccountID, row.ID, ExecutionEventStatus, executionStatusPayload(row.Status), startedAt)
	return cloneExecution(row), nil
}

func (m *MemStore) RenewExecutionLease(_ context.Context, executionID, leaseToken string, renewedAt time.Time, leaseDuration time.Duration) error {
	if executionID == "" || leaseToken == "" || renewedAt.IsZero() || leaseDuration <= 0 {
		return ErrExecutionInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.executions[executionID]
	if !ok || (row.Status != api.ExecutionStatusRestoring && row.Status != api.ExecutionStatusRunning) ||
		row.LeaseToken == nil || *row.LeaseToken != leaseToken || row.CancelRequested != nil ||
		row.LeaseExpiresAt == nil || !row.LeaseExpiresAt.After(renewedAt) || !row.DeadlineAt.After(renewedAt) {
		return ErrExecutionLeaseLost
	}
	renewedAt = renewedAt.UTC()
	expiresAt := renewedAt.Add(leaseDuration).UTC()
	if expiresAt.After(row.DeadlineAt) {
		expiresAt = row.DeadlineAt
	}
	row.LeaseExpiresAt = &expiresAt
	row.UpdatedAt = renewedAt
	m.executions[row.ID] = row
	return nil
}

func (m *MemStore) CompleteExecution(_ context.Context, params CompleteExecutionParams) (Execution, error) {
	if params.ID == "" || params.LeaseToken == "" {
		return Execution{}, ErrExecutionInvalidTerminal
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.executions[params.ID]
	if !ok || (row.Status != api.ExecutionStatusRestoring && row.Status != api.ExecutionStatusRunning) ||
		row.LeaseToken == nil || *row.LeaseToken != params.LeaseToken {
		return Execution{}, ErrExecutionLeaseLost
	}
	if err := validateCompletion(params, row.Limits.MaxOutputBytes); err != nil {
		return Execution{}, err
	}
	if row.LeaseExpiresAt == nil || !params.FinishedAt.Before(*row.LeaseExpiresAt) {
		return Execution{}, ErrExecutionLeaseLost
	}
	if row.CancelRequested != nil && params.Status != api.ExecutionStatusCancelled {
		return Execution{}, fmt.Errorf("%w: cancellation was already requested", ErrExecutionInvalidTerminal)
	}
	if !row.DeadlineAt.After(params.FinishedAt) && params.Status != api.ExecutionStatusTimedOut &&
		params.Status != api.ExecutionStatusCancelled {
		return Execution{}, fmt.Errorf("%w: execution cannot succeed after its deadline", ErrExecutionInvalidTerminal)
	}
	if params.Status == api.ExecutionStatusSucceeded && row.Status != api.ExecutionStatusRunning {
		return Execution{}, ErrExecutionInvalidTerminal
	}
	if params.FinishedAt.Before(row.CreatedAt) || (row.StartedAt != nil && params.FinishedAt.Before(*row.StartedAt)) {
		return Execution{}, fmt.Errorf("%w: finish time predates execution lifecycle", ErrExecutionInvalidTerminal)
	}
	finishedAt := params.FinishedAt.UTC()
	row.Status = params.Status
	row.LeaseToken = nil
	row.LeaseOwner = nil
	row.LeaseExpiresAt = nil
	row.Result = append([]byte(nil), params.Result...)
	row.Stdout = params.Stdout
	row.Stderr = params.Stderr
	row.OutputTruncated = params.OutputTruncated
	row.ExitCode = copyInt(params.ExitCode)
	row.FailureCode = copyString(params.FailureCode)
	row.FailureMessage = copyString(params.FailureMessage)
	row.Usage = params.Usage
	row.FinishedAt = &finishedAt
	row.UpdatedAt = finishedAt
	m.executions[row.ID] = row
	m.recordExecutionUsageLocked(row)
	if !params.OutputEventsPersisted {
		appendExecutionOutputEventsLocked(m, row, finishedAt)
	}
	m.appendExecutionEventLocked(row.AccountID, row.ID, ExecutionEventTerminal, executionTerminalPayload(row), finishedAt)
	delete(m.executionPayloads, row.ID)
	return cloneExecution(row), nil
}

// recordExecutionUsageLocked is the MemStore mirror of the Postgres
// execution_usage_ledger insert. The execution ID is the idempotency key;
// repeated terminalization/recovery attempts are no-ops.
func (m *MemStore) recordExecutionUsageLocked(row Execution) {
	if !row.Status.Terminal() {
		return
	}
	if _, exists := m.executionUsageLedger[row.ID]; exists {
		return
	}
	startedAt := cloneTimePtr(row.StartedAt)
	m.executionUsageLedger[row.ID] = executionUsageLedgerRow{
		ExecutionID:  row.ID,
		AccountID:    row.AccountID,
		Runtime:      row.Runtime,
		Status:       row.Status,
		WallTimeMS:   row.Usage.WallTimeMS,
		CPUTimeMS:    row.Usage.CPUTimeMS,
		PeakMemoryMB: int64(row.Usage.PeakMemoryMB),
		OutputBytes:  int64(len(row.Result) + len(row.Stdout) + len(row.Stderr)),
		StartedAt:    startedAt,
		FinishedAt:   derefTime(row.FinishedAt),
		CreatedAt:    row.CreatedAt,
	}
}

// ExecutionUsageByAccount returns a month-scoped aggregate over the durable
// ledger. The month is interpreted as a UTC calendar month and must be the
// first day of that month (or it is normalized defensively).
func (m *MemStore) ExecutionUsageByAccount(_ context.Context, accountID string, month time.Time) (ExecutionUsageSummary, error) {
	if strings.TrimSpace(accountID) == "" || month.IsZero() {
		return ExecutionUsageSummary{}, ErrExecutionInvalid
	}
	month = month.UTC()
	start := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	m.mu.Lock()
	defer m.mu.Unlock()
	summary := ExecutionUsageSummary{AccountID: accountID, Month: start}
	for _, row := range m.executionUsageLedger {
		if row.AccountID != accountID || row.FinishedAt.Before(start) || !row.FinishedAt.Before(end) {
			continue
		}
		summary.Runs++
		summary.WallTimeMS += row.WallTimeMS
		summary.CPUTimeMS += row.CPUTimeMS
		if row.PeakMemoryMB > summary.PeakMemoryMB {
			summary.PeakMemoryMB = row.PeakMemoryMB
		}
		summary.OutputBytes += row.OutputBytes
		switch row.Status {
		case api.ExecutionStatusSucceeded:
			summary.Succeeded++
		case api.ExecutionStatusFailed:
			summary.Failed++
		case api.ExecutionStatusTimedOut:
			summary.TimedOut++
		case api.ExecutionStatusOutOfMemory:
			summary.OutOfMemory++
		case api.ExecutionStatusCancelled:
			summary.Cancelled++
		}
	}
	return summary, nil
}

func copyInt(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func copyString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func (m *MemStore) RequestExecutionCancellation(_ context.Context, accountID, executionID string, requestedAt time.Time) (Execution, error) {
	if accountID == "" || executionID == "" || requestedAt.IsZero() {
		return Execution{}, ErrExecutionInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.executions[executionID]
	if !ok || row.AccountID != accountID {
		return Execution{}, ErrNotFound
	}
	if row.Status.Terminal() {
		m.recordExecutionUsageLocked(row)
		delete(m.executionPayloads, row.ID)
		return cloneExecution(row), nil
	}
	if requestedAt.Before(row.CreatedAt) {
		return Execution{}, fmt.Errorf("%w: cancellation predates admission", ErrExecutionInvalid)
	}
	requestedAt = requestedAt.UTC()
	row.CancelRequested = &requestedAt
	row.UpdatedAt = requestedAt
	if row.Status == api.ExecutionStatusQueued {
		row.Status = api.ExecutionStatusCancelled
		row.FinishedAt = &requestedAt
		m.recordExecutionUsageLocked(row)
		m.appendExecutionEventLocked(row.AccountID, row.ID, ExecutionEventTerminal, executionTerminalPayload(row), requestedAt)
		delete(m.executionPayloads, row.ID)
	}
	m.executions[row.ID] = row
	return cloneExecution(row), nil
}

type executionSweepCandidate struct {
	id string
	at time.Time
}

func executionCandidateIDs(rows map[string]Execution, limit int, match func(Execution) (time.Time, bool)) []string {
	candidates := make([]executionSweepCandidate, 0)
	for id, row := range rows {
		at, ok := match(row)
		if ok {
			candidates = append(candidates, executionSweepCandidate{id: id, at: at})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].at.Equal(candidates[j].at) {
			return candidates[i].id < candidates[j].id
		}
		return candidates[i].at.Before(candidates[j].at)
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	ids := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		ids = append(ids, candidate.id)
	}
	return ids
}

func (m *MemStore) SweepExecutions(_ context.Context, at time.Time, limit int) (ExecutionSweepResult, error) {
	if at.IsZero() {
		return ExecutionSweepResult{}, ErrExecutionInvalid
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	at = at.UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	result := ExecutionSweepResult{}

	expiredQueued := executionCandidateIDs(m.executions, limit, func(row Execution) (time.Time, bool) {
		return row.DeadlineAt, row.Status == api.ExecutionStatusQueued && !row.DeadlineAt.After(at)
	})
	for _, id := range expiredQueued {
		row := m.executions[id]
		finishExecutionForSweep(&row, api.ExecutionStatusTimedOut, at, "deadline_exceeded", "execution deadline elapsed before dispatch")
		m.executions[id] = row
		m.recordExecutionUsageLocked(row)
		m.appendExecutionEventLocked(row.AccountID, row.ID, ExecutionEventTerminal, executionTerminalPayload(row), at)
		if _, ok := m.executionPayloads[id]; ok {
			delete(m.executionPayloads, id)
			result.PayloadsDeleted++
		}
		result.ExpiredQueued++
	}

	finishedRestores := executionCandidateIDs(m.executions, limit, func(row Execution) (time.Time, bool) {
		if row.Status != api.ExecutionStatusRestoring || row.LeaseExpiresAt == nil || row.LeaseExpiresAt.After(at) {
			return time.Time{}, false
		}
		return *row.LeaseExpiresAt, !row.DeadlineAt.After(at) || row.CancelRequested != nil
	})
	for _, id := range finishedRestores {
		row := m.executions[id]
		status, code, message := sweepTerminal(row, at, "execution deadline elapsed during restore")
		finishExecutionForSweep(&row, status, at, code, message)
		m.executions[id] = row
		m.recordExecutionUsageLocked(row)
		m.appendExecutionEventLocked(row.AccountID, row.ID, ExecutionEventTerminal, executionTerminalPayload(row), at)
		if _, ok := m.executionPayloads[id]; ok {
			delete(m.executionPayloads, id)
			result.PayloadsDeleted++
		}
		result.FinishedRestores++
	}

	requeuedRestores := executionCandidateIDs(m.executions, limit, func(row Execution) (time.Time, bool) {
		if row.Status != api.ExecutionStatusRestoring || row.LeaseExpiresAt == nil {
			return time.Time{}, false
		}
		return *row.LeaseExpiresAt, !row.LeaseExpiresAt.After(at) && row.DeadlineAt.After(at) && row.CancelRequested == nil
	})
	for _, id := range requeuedRestores {
		row := m.executions[id]
		row.Status = api.ExecutionStatusQueued
		row.LeaseToken = nil
		row.LeaseOwner = nil
		row.LeaseExpiresAt = nil
		row.UpdatedAt = at
		m.executions[id] = row
		result.RequeuedRestores++
	}

	finishedRuns := executionCandidateIDs(m.executions, limit, func(row Execution) (time.Time, bool) {
		if row.Status != api.ExecutionStatusRunning || row.LeaseExpiresAt == nil {
			return time.Time{}, false
		}
		return *row.LeaseExpiresAt, !row.LeaseExpiresAt.After(at)
	})
	for _, id := range finishedRuns {
		row := m.executions[id]
		status, code, message := sweepTerminal(row, at, "execution deadline elapsed while running")
		if status == api.ExecutionStatusFailed {
			code, message = "lease_expired", "scheduler lease expired after execution dispatch"
		}
		finishExecutionForSweep(&row, status, at, code, message)
		m.executions[id] = row
		m.recordExecutionUsageLocked(row)
		m.appendExecutionEventLocked(row.AccountID, row.ID, ExecutionEventTerminal, executionTerminalPayload(row), at)
		if _, ok := m.executionPayloads[id]; ok {
			delete(m.executionPayloads, id)
			result.PayloadsDeleted++
		}
		result.FinishedRuns++
	}

	orphanPayloads := make([]executionSweepCandidate, 0)
	for id, payload := range m.executionPayloads {
		if row, ok := m.executions[id]; ok && row.Status.Terminal() {
			orphanPayloads = append(orphanPayloads, executionSweepCandidate{id: id, at: payload.createdAt})
		}
	}
	sort.Slice(orphanPayloads, func(i, j int) bool {
		if orphanPayloads[i].at.Equal(orphanPayloads[j].at) {
			return orphanPayloads[i].id < orphanPayloads[j].id
		}
		return orphanPayloads[i].at.Before(orphanPayloads[j].at)
	})
	if len(orphanPayloads) > limit {
		orphanPayloads = orphanPayloads[:limit]
	}
	for _, payload := range orphanPayloads {
		delete(m.executionPayloads, payload.id)
		result.PayloadsDeleted++
	}
	return result, nil
}

func sweepTerminal(row Execution, at time.Time, timeoutMessage string) (api.ExecutionStatus, string, string) {
	if row.CancelRequested != nil {
		return api.ExecutionStatusCancelled, "", ""
	}
	if !row.DeadlineAt.After(at) {
		return api.ExecutionStatusTimedOut, "deadline_exceeded", timeoutMessage
	}
	return api.ExecutionStatusFailed, "lease_expired", "scheduler lease expired"
}

func finishExecutionForSweep(row *Execution, status api.ExecutionStatus, at time.Time, code, message string) {
	row.Status = status
	row.LeaseToken = nil
	row.LeaseOwner = nil
	row.LeaseExpiresAt = nil
	row.FinishedAt = &at
	row.UpdatedAt = at
	if code == "" {
		row.FailureCode = nil
		row.FailureMessage = nil
	} else {
		row.FailureCode = &code
		row.FailureMessage = &message
	}
}
