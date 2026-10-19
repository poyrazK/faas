package state

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalcommand"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ScenarioActionSnapshotStore seals supported effects before the runner cleans
// up its fixtures. The interface is optional; unsupported stores fail closed.
type ScenarioActionSnapshotStore interface {
	SealScenarioTestActions(context.Context, string, string) (api.ScenarioActionSnapshot, error)
}

type scenarioActionSummary struct {
	captured, payloadBytes, pending, incomplete, suppressed int64
}

func newScenarioActionSnapshot(runID string, workloads []string, s scenarioActionSummary) api.ScenarioActionSnapshot {
	sort.Strings(workloads)
	return api.ScenarioActionSnapshot{Version: api.ScenarioActionSnapshotVersion, Scope: api.ScenarioActionScope,
		RunID: runID, Status: "complete", Workloads: workloads, Intents: []api.ScenarioCapturedAction{},
		CapturedCount: s.captured, PendingManagedWork: s.pending, IncompleteManagedEffects: s.incomplete, SuppressedDeliveries: s.suppressed}
}

func finalizeScenarioActionSnapshot(snapshot *api.ScenarioActionSnapshot, summary scenarioActionSummary, intents []api.ScenarioCapturedAction) []byte {
	combinedBytes := 0
	for _, intent := range intents {
		combinedBytes += len(intent.Payload)
	}
	for _, attempt := range snapshot.SimulatedAttempts {
		combinedBytes += len(attempt.Payload)
	}
	if combinedBytes > api.MaxScenarioActionPayloadBytes {
		snapshot.IncompleteSimulationAttempts++
		snapshot.Truncated = true
	}
	switch {
	case summary.pending > 0 || snapshot.PendingSimulationAttempts > 0 || snapshot.PendingRecorderSessions > 0 || snapshot.ManagedWork != nil && snapshot.ManagedWork.Status == "pending":
		snapshot.Status, snapshot.Reason = "pending", "managed work, simulated requests or recorder closing acknowledgements are still pending"
		return nil
	case snapshot.SuspendResume != nil && snapshot.SuspendResume.Status != "complete":
		snapshot.Status, snapshot.Reason = "incomplete", "suspend/resume evidence is incomplete"
	case snapshot.ColdStart != nil && snapshot.ColdStart.Status != "complete":
		snapshot.Status, snapshot.Reason = "incomplete", "cold-start retirement or boot proof is missing"
	case snapshot.ManagedWork != nil && snapshot.ManagedWork.Status != "complete":
		snapshot.Status, snapshot.Reason = "incomplete", snapshot.ManagedWork.Reason
	case snapshot.IncompleteRecorderSessions > 0:
		snapshot.Status, snapshot.Reason = "incomplete", "recorder sessions are missing, failed or were not acknowledged by their execution owner"
	case snapshot.IncompleteSimulationAttempts > 0:
		snapshot.Status, snapshot.Reason = "incomplete", "simulation requests were unsupported, interrupted, oversized or missing durable evidence"
	case summary.incomplete > 0:
		snapshot.Status, snapshot.Reason = "incomplete", "managed effects are missing capture or receiver identity; start a new run with upgraded completion workers"
	case summary.captured > api.MaxScenarioActionIntents || summary.payloadBytes > api.MaxScenarioActionPayloadBytes:
		snapshot.Status, snapshot.Truncated, snapshot.Reason = "incomplete", true, "captured intent exceeds the evidence format bounds"
	case int64(len(intents)) != summary.captured:
		snapshot.Status, snapshot.Reason = "incomplete", "captured intent inventory is incomplete"
	default:
		snapshot.Intents = intents
	}
	if snapshot.Intents == nil {
		snapshot.Intents = []api.ScenarioCapturedAction{}
	}
	snapshot.SealedAt = time.Now().UTC().Format(time.RFC3339Nano)
	body, err := json.Marshal(snapshot)
	if err != nil || len(body) > api.MaxScenarioActionSnapshotBytes {
		snapshot.Status, snapshot.Truncated, snapshot.Reason = "incomplete", true, "captured intent cannot be encoded within the evidence format bounds"
		snapshot.Intents = []api.ScenarioCapturedAction{}
		snapshot.SimulatedAttempts = nil
		body, _ = json.Marshal(snapshot) // All remaining fields are bounded metadata.
	}
	return body
}

func (s *PgStore) SealScenarioTestActions(ctx context.Context, accountID, runID string) (api.ScenarioActionSnapshot, error) {
	var empty api.ScenarioActionSnapshot
	if accountID == "" || !scenarioTestRunPattern.MatchString(runID) {
		return empty, ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return empty, fmt.Errorf("begin action snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if err := q.LockScenarioActionRun(ctx, tx, sqlc.LockScenarioActionRunParams{AccountID: accountID, RunID: runID}); err != nil {
		return empty, fmt.Errorf("lock action snapshot run: %w", err)
	}
	policies, err := q.ScenarioActionPoliciesForRun(ctx, tx, sqlc.ScenarioActionPoliciesForRunParams{AccountID: accountID, RunID: runID})
	if err != nil {
		return empty, fmt.Errorf("load action snapshot policies: %w", err)
	}
	if len(policies) == 0 || len(policies) > 16 {
		return empty, ErrNotFound
	}
	workloads := make([]string, 0, len(policies))
	for _, policy := range policies {
		if len(policy.SealedSnapshot) > 0 {
			var snapshot api.ScenarioActionSnapshot
			if err := json.Unmarshal(policy.SealedSnapshot, &snapshot); err != nil {
				return empty, fmt.Errorf("decode sealed action snapshot: %w", err)
			}
			return snapshot, nil
		}
	}
	for _, policy := range policies {
		if policy.SealedAt.Valid || !scenarioTestWorkloadPattern.MatchString(policy.WorkloadName) {
			return empty, ErrConflict
		}
		eligible, err := q.LockScenarioActionApp(ctx, tx, sqlc.LockScenarioActionAppParams{AccountID: accountID, AppID: policy.AppID})
		if err != nil {
			return empty, fmt.Errorf("lock action snapshot app: %w", err)
		}
		if !eligible.Valid || !eligible.Bool {
			return empty, ErrConflict
		}
		workloads = append(workloads, policy.WorkloadName)
	}
	matches, err := q.ScenarioActionRunMembersMatch(ctx, tx, sqlc.ScenarioActionRunMembersMatchParams{AccountID: accountID, RunID: runID})
	if err != nil {
		return empty, fmt.Errorf("check action snapshot membership: %w", err)
	}
	if !matches {
		return empty, ErrConflict
	}
	if err := q.CloseScenarioManagedWork(ctx, tx, sqlc.CloseScenarioManagedWorkParams{AccountID: accountID, RunID: runID}); err != nil {
		return empty, err
	}
	counts, err := q.ScenarioActionSnapshotSummary(ctx, tx, sqlc.ScenarioActionSnapshotSummaryParams{AccountID: accountID, RunID: runID})
	if err != nil {
		return empty, fmt.Errorf("count action snapshot: %w", err)
	}
	summary := scenarioActionSummary{counts.CapturedCount, counts.PayloadBytes, counts.PendingManagedWork, counts.IncompleteManagedEffects, counts.SuppressedDeliveries}
	snapshot := newScenarioActionSnapshot(runID, workloads, summary)
	var intents []api.ScenarioCapturedAction
	if summary.pending == 0 && summary.incomplete == 0 && summary.captured <= api.MaxScenarioActionIntents && summary.payloadBytes <= api.MaxScenarioActionPayloadBytes {
		rows, err := q.ScenarioCapturedActions(ctx, tx, sqlc.ScenarioCapturedActionsParams{AccountID: accountID, RunID: runID})
		if err != nil {
			return empty, fmt.Errorf("read action snapshot: %w", err)
		}
		for _, row := range rows {
			intents = append(intents, api.ScenarioCapturedAction{ID: row.ID, Workload: row.Workload, Source: row.Source, OperationID: row.OperationID,
				Workflow: row.Workflow, Step: row.Step, Name: row.Name, WebhookID: row.WebhookID, ReceiverSHA256: row.ReceiverSha256, Type: row.Type, Payload: row.Payload})
		}
	}
	if err := s.addScenarioSimulationSnapshot(ctx, tx, &snapshot, accountID, policies); err != nil {
		return empty, err
	}
	if err := addScenarioRecorderSnapshot(ctx, tx, &snapshot, accountID, summary.pending == 0); err != nil {
		return empty, err
	}
	if err := addScenarioManagedWorkSnapshot(ctx, tx, &snapshot, accountID, policies); err != nil {
		return empty, err
	}
	if err := addScenarioSuspendSnapshot(ctx, tx, &snapshot, accountID, policies); err != nil {
		return empty, err
	}
	if err := addScenarioColdSnapshot(ctx, tx, &snapshot, policies); err != nil {
		return empty, err
	}
	body := finalizeScenarioActionSnapshot(&snapshot, summary, intents)
	if snapshot.Status == "pending" {
		return snapshot, tx.Commit(ctx)
	}
	sealedAt, _ := time.Parse(time.RFC3339Nano, snapshot.SealedAt)
	updated, err := q.SealScenarioActionPolicies(ctx, tx, sqlc.SealScenarioActionPoliciesParams{AccountID: accountID, RunID: runID, AnchorAppID: policies[0].AppID,
		SealedAt: pgtype.Timestamptz{Time: sealedAt, Valid: true}, Snapshot: body})
	if err != nil {
		return empty, fmt.Errorf("persist sealed action snapshot: %w", err)
	}
	if updated != int64(len(policies)) {
		return empty, ErrConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return empty, fmt.Errorf("commit action snapshot: %w", err)
	}
	return snapshot, nil
}

func (m *MemStore) SealScenarioTestActions(ctx context.Context, accountID, runID string) (api.ScenarioActionSnapshot, error) {
	var empty api.ScenarioActionSnapshot
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if accountID == "" || !scenarioTestRunPattern.MatchString(runID) {
		return empty, ErrNotFound
	}
	members := make(map[string]ScenarioTestMember)
	var appIDs, workloads []string
	for id, policy := range m.scenarioActionPolicies {
		member := policy.Member
		if member.AccountID != accountID || member.RunID != runID {
			continue
		}
		if len(policy.Snapshot) > 0 {
			var snapshot api.ScenarioActionSnapshot
			if err := json.Unmarshal(policy.Snapshot, &snapshot); err != nil {
				return empty, fmt.Errorf("decode sealed action snapshot: %w", err)
			}
			return snapshot, nil
		}
		members[canonicalMemUUID(id)] = member
		appIDs, workloads = append(appIDs, id), append(workloads, member.Workload)
	}
	if len(appIDs) == 0 || len(appIDs) > 16 {
		return empty, ErrNotFound
	}
	for _, id := range appIDs {
		p := m.scenarioActionPolicies[id]
		registered, exists := m.scenarioTestMembers[id]
		if !p.SealedAt.IsZero() || !exists || registered != p.Member || !eligibleScenarioTestApp(m.apps[id], accountID) {
			return empty, ErrConflict
		}
	}
	for id, member := range m.scenarioTestMembers {
		if member.AccountID == accountID && member.RunID == runID {
			if _, found := members[canonicalMemUUID(id)]; !found {
				return empty, ErrConflict
			}
		}
	}
	summary, intents := m.scenarioActionInventoryLocked(runID, members)
	snapshot := newScenarioActionSnapshot(runID, workloads, summary)
	planJSON := ""
	failed := false
	var attempts []api.ScenarioSimulationAttempt
	for _, id := range appIDs {
		p := m.scenarioActionPolicies[id]
		if p.Member.SimulationJSON != "" {
			planJSON = p.Member.SimulationJSON
		}
		failed = failed || p.SimulationFailed
		attempts = append(attempts, p.SimulationAttempts...)
	}
	applyScenarioSimulationSnapshot(&snapshot, planJSON, failed, attempts)
	m.addScenarioManagedWorkSnapshotLocked(&snapshot, appIDs)
	m.addScenarioRecorderSnapshotLocked(&snapshot, appIDs, summary.pending == 0)
	m.addScenarioColdSnapshotLocked(&snapshot, appIDs)
	m.addScenarioSuspendSnapshotLocked(&snapshot, appIDs)
	body := finalizeScenarioActionSnapshot(&snapshot, summary, intents)
	if snapshot.Status == "pending" {
		return snapshot, nil
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	sort.Strings(appIDs)
	sealedAt, _ := time.Parse(time.RFC3339Nano, snapshot.SealedAt)
	for i, id := range appIDs {
		policy := m.scenarioActionPolicies[id]
		policy.SealedAt = sealedAt
		if i == 0 {
			policy.Snapshot = body
		}
		m.scenarioActionPolicies[id] = policy
	}
	return snapshot, nil
}

func (m *MemStore) scenarioActionInventoryLocked(runID string, members map[string]ScenarioTestMember) (scenarioActionSummary, []api.ScenarioCapturedAction) {
	var summary scenarioActionSummary
	var intents []api.ScenarioCapturedAction
	knownEffects := make(map[string]bool)
	add := func(action api.ScenarioCapturedAction, capturedRun string) {
		knownEffects[canonicalMemUUID(action.ID)] = true
		if capturedRun != runID || action.ReceiverSHA256 == "" {
			summary.incomplete++
		}
		if capturedRun != runID {
			return
		}
		summary.captured++
		summary.payloadBytes += int64(len(action.Payload))
		if len(intents) <= api.MaxScenarioActionIntents && summary.payloadBytes <= api.MaxScenarioActionPayloadBytes {
			action.Payload = append(json.RawMessage(nil), action.Payload...)
			intents = append(intents, action)
		}
	}
	for _, task := range m.appTasks {
		if _, included := members[canonicalMemUUID(task.AppID)]; included && rehearsalcommand.IsCommand(task.Command) && !task.Status.Terminal() {
			summary.pending++
		}
	}
	for id, operation := range m.exclusiveOperations {
		member, included := members[canonicalMemUUID(operation.AppID)]
		if !included || canonicalMemUUID(operation.AccountID) != canonicalMemUUID(member.AccountID) {
			continue
		}
		if operation.State == "pending" || operation.State == "running" {
			summary.pending++
		}
		for _, effect := range m.exclusiveEffects[id] {
			if effect.Record.WebhookID == "" {
				continue
			}
			add(api.ScenarioCapturedAction{ID: effect.Record.ID, Workload: member.Workload, Source: "operation", OperationID: id, Name: effect.Record.Name,
				WebhookID: effect.Record.WebhookID, ReceiverSHA256: effect.ReceiverSHA256, Type: effect.Record.Type, Payload: effect.Effect.Payload}, effect.Record.RehearsalRunID)
		}
	}
	for _, run := range m.workflowRuns {
		if _, included := members[canonicalMemUUID(run.AppID)]; included && (run.Status == WorkflowRunStatusPending || run.Status == WorkflowRunStatusRunning || run.Status == WorkflowRunStatusAwaitingEvent) {
			summary.pending++
		}
	}
	for key, effects := range m.workflowOperationEffects {
		run := m.workflowRuns[key.runID]
		member, included := members[canonicalMemUUID(run.AppID)]
		if !included {
			continue
		}
		for _, effect := range effects {
			add(api.ScenarioCapturedAction{ID: effect.Record.ID, Workload: member.Workload, Source: "workflow", OperationID: effect.OperationID, Workflow: run.WorkflowName, Step: key.stepName,
				Name: effect.Record.Name, WebhookID: effect.Record.WebhookID, ReceiverSHA256: effect.ReceiverSHA256, Type: effect.Record.Type, Payload: effect.Effect.Payload}, effect.Record.RehearsalRunID)
		}
	}
	for _, invocation := range m.invocations {
		if _, included := members[canonicalMemUUID(invocation.AppID)]; included && (invocation.State == InvocationPending || invocation.State == InvocationDispatching) {
			summary.pending++
		}
	}
	for _, delivery := range m.appWebhookDeliveries {
		member, byApp := members[canonicalMemUUID(delivery.AppID)]
		hook := m.appWebhooks[delivery.WebhookID]
		if hook.ID == "" {
			for id, candidate := range m.appWebhooks {
				if canonicalMemUUID(id) == canonicalMemUUID(delivery.WebhookID) {
					hook = candidate
					break
				}
			}
		}
		receiverMember, byReceiver := members[canonicalMemUUID(hook.AppID)]
		if !byApp && !byReceiver {
			continue
		}
		if !byApp {
			member = receiverMember
		}
		if canonicalMemUUID(delivery.AccountID) != canonicalMemUUID(member.AccountID) {
			continue
		}
		if delivery.LastError == ScenarioWebhookSuppressed {
			summary.suppressed++
		}
		if delivery.Event == OperationEffectEvent && !knownEffects[canonicalMemUUID(delivery.ID)] {
			summary.incomplete++
		}
	}
	sort.Slice(intents, func(i, j int) bool {
		a, b := intents[i], intents[j]
		if a.Workload != b.Workload {
			return a.Workload < b.Workload
		}
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		return a.ID < b.ID
	})
	return summary, intents
}
