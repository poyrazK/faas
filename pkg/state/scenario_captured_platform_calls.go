// adr: 20261021150000
package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ScenarioPlatformCallCapture is one workflow or realtime call an enrolled app
// requested through the run-local platform API. Address is what the request
// named: the {slug} of a workflow start or realtime call, or the run ID of a
// workflow event. Endpoint is a realtime call's endpoint ID. Name is the
// workflow, event or channel name. Message is nil when Oversized.
type ScenarioPlatformCallCapture struct {
	Kind           string
	Address        string
	Endpoint       string
	Name           string
	IdempotencyKey string
	Message        json.RawMessage
	Oversized      bool
}

// ScenarioPlatformCallResult identifies the capture. Replayed is true when an
// idempotent retry returned an earlier capture instead of a new one.
type ScenarioPlatformCallResult struct {
	ID       string
	Replayed bool
}

// ScenarioPlatformCaptureStore records an enrolled app's workflow and realtime
// calls as run evidence instead of performing them. captured is false only for
// an app with no rehearsal policy. ErrNotFound reports an address that is
// neither part of the run nor of the caller's account, as a real call would.
type ScenarioPlatformCaptureStore interface {
	CaptureScenarioPlatformCall(ctx context.Context, appID string, call ScenarioPlatformCallCapture) (result ScenarioPlatformCallResult, captured bool, err error)
}

// ErrScenarioIdempotencyConflict reports an Idempotency-Key reused for a
// different request. It is an ErrConflict.
var ErrScenarioIdempotencyConflict = fmt.Errorf("%w: rehearsal idempotency key reused for a different request", ErrConflict)

type memScenarioCapturedPlatformCall struct {
	id, accountID, runID, appID, target string
	call                                ScenarioPlatformCallCapture
	createdAt                           time.Time
}

func scenarioRealtimeKind(kind string) bool {
	return kind == api.ScenarioPlatformKindRealtimePublish || kind == api.ScenarioPlatformKindRealtimeSignal
}

func scenarioExecutionKind(kind string) bool {
	return kind == api.ScenarioPlatformKindExecution || kind == api.ScenarioPlatformKindExecutionCancel
}

func validScenarioPlatformCall(call ScenarioPlatformCallCapture) bool {
	if call.Kind != api.ScenarioPlatformKindWorkflowRun && call.Kind != api.ScenarioPlatformKindWorkflowEvent && !scenarioRealtimeKind(call.Kind) && !scenarioExecutionKind(call.Kind) ||
		scenarioRealtimeKind(call.Kind) != (call.Endpoint != "") {
		return false
	}
	return call.Name != "" && len(call.Name) <= 256 && len(call.IdempotencyKey) <= WorkflowRunIdempotencyKeyMaxBytes && call.Oversized == (call.Message == nil)
}

// scenarioPlatformReplay applies production's idempotency rule to earlier
// captures under the same key: a workflow start replays only the same input
// to the same workflow; a workflow event replays the same event name whatever
// its payload, and refuses another name. A Run start or cancellation replays
// the first response under its key whatever the request, as apid's
// account-wide Idempotency-Key does.
func scenarioPlatformReplay(kind, name string, prior []sqlc.ScenarioPlatformCallsByIdempotencyKeyRow) (string, error) {
	for _, row := range prior {
		switch {
		case scenarioExecutionKind(kind):
			return row.ID, nil
		case kind == api.ScenarioPlatformKindWorkflowEvent && row.Name == name:
			return row.ID, nil
		case kind == api.ScenarioPlatformKindWorkflowEvent:
			return "", ErrScenarioIdempotencyConflict
		case row.Name == name && row.Same:
			return row.ID, nil
		case row.Name == name:
			return "", ErrScenarioIdempotencyConflict
		}
	}
	return "", nil
}

func (s *PgStore) CaptureScenarioPlatformCall(ctx context.Context, appID string, call ScenarioPlatformCallCapture) (ScenarioPlatformCallResult, bool, error) {
	var empty ScenarioPlatformCallResult
	q := sqlc.New()
	policy, err := q.ScenarioEventCapturePolicy(ctx, s.pool, appID)
	if errors.Is(err, pgx.ErrNoRows) {
		return empty, false, nil
	}
	if err != nil {
		return empty, true, fmt.Errorf("read rehearsal platform capture policy: %w", err)
	}
	if !validScenarioPlatformCall(call) {
		return empty, true, ErrConflict
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return empty, true, fmt.Errorf("begin rehearsal platform capture: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// The seal takes the same run lock, so a call is either in the sealed
	// snapshot or refused as late; it can never commit unseen.
	if err := q.LockScenarioActionRun(ctx, tx, sqlc.LockScenarioActionRunParams{AccountID: policy.AccountID, RunID: policy.RunID}); err != nil {
		return empty, true, fmt.Errorf("lock rehearsal platform capture: %w", err)
	}
	st, err := q.ScenarioPlatformCaptureState(ctx, tx, sqlc.ScenarioPlatformCaptureStateParams{AccountID: policy.AccountID, RunID: policy.RunID})
	if err != nil {
		return empty, true, fmt.Errorf("read rehearsal platform capture state: %w", err)
	}
	if st.Sealed {
		return empty, true, ErrScenarioActionsSealed
	}
	target, err := s.scenarioPlatformTarget(ctx, tx, policy.AccountID, policy.RunID, call)
	if err != nil {
		return empty, true, err
	}
	if call.IdempotencyKey != "" {
		prior, err := q.ScenarioPlatformCallsByIdempotencyKey(ctx, tx, sqlc.ScenarioPlatformCallsByIdempotencyKeyParams{AccountID: policy.AccountID, RunID: policy.RunID,
			AppID: appID, Kind: call.Kind, Target: target, IdempotencyKey: call.IdempotencyKey, Message: []byte(call.Message), Oversized: call.Oversized})
		if err != nil {
			return empty, true, fmt.Errorf("read rehearsal idempotency key: %w", err)
		}
		if id, err := scenarioPlatformReplay(call.Kind, call.Name, prior); id != "" || err != nil {
			return ScenarioPlatformCallResult{ID: id, Replayed: id != ""}, true, err
		}
	}
	if st.Captured > api.MaxScenarioCapturedPlatformCalls {
		return ScenarioPlatformCallResult{ID: uuid.NewString()}, true, nil // the extra row already marks the inventory incomplete
	}
	id, err := q.InsertScenarioCapturedPlatformCall(ctx, tx, sqlc.InsertScenarioCapturedPlatformCallParams{AccountID: policy.AccountID, RunID: policy.RunID, AppID: appID,
		Kind: call.Kind, Target: target, Name: call.Name, IdempotencyKey: call.IdempotencyKey, Message: []byte(call.Message), Oversized: call.Oversized})
	if err != nil {
		return empty, true, fmt.Errorf("persist rehearsal platform capture: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return empty, true, fmt.Errorf("commit rehearsal platform capture: %w", err)
	}
	return ScenarioPlatformCallResult{ID: id}, true, nil
}

// scenarioPlatformTarget names a call's addressee: a workflow start's app as
// workload:<name> or app:<slug>, and a workflow event's run either by its
// captured start or, for a run outside the rehearsal, by run:<id>.
func (s *PgStore) scenarioPlatformTarget(ctx context.Context, tx pgx.Tx, accountID, runID string, call ScenarioPlatformCallCapture) (string, error) {
	q := sqlc.New()
	if scenarioRealtimeKind(call.Kind) {
		endpoint, err := uuid.Parse(call.Endpoint)
		if err != nil || call.Address != "" && !api.ValidAppSlug(call.Address) {
			return "", ErrNotFound
		}
		target, err := q.ScenarioRealtimeEndpointTarget(ctx, tx, sqlc.ScenarioRealtimeEndpointTargetParams{AccountID: accountID, RunID: runID, Slug: call.Address, EndpointID: endpoint.String()})
		if err != nil {
			return "", fmt.Errorf("resolve rehearsal realtime endpoint: %w", err)
		}
		if target == "" {
			return "", ErrNotFound
		}
		return target, nil
	}
	switch call.Kind {
	case api.ScenarioPlatformKindExecution:
		return api.ScenarioExecutionTarget, nil
	case api.ScenarioPlatformKindExecutionCancel:
		// Only the run's own captured starts can be cancelled; a production
		// Run is outside the run, as for a read of it.
		id, err := uuid.Parse(call.Address)
		if err != nil {
			return "", ErrNotFound
		}
		reference, err := q.ScenarioCapturedExecutionReference(ctx, tx, sqlc.ScenarioCapturedExecutionReferenceParams{ID: id.String(), AccountID: accountID, RunID: runID})
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		if err != nil {
			return "", fmt.Errorf("resolve captured execution: %w", err)
		}
		return reference, nil
	}
	if call.Kind == api.ScenarioPlatformKindWorkflowRun {
		if !api.ValidAppSlug(call.Address) {
			return "", ErrNotFound
		}
		target, err := q.ScenarioQueueCaptureTarget(ctx, tx, sqlc.ScenarioQueueCaptureTargetParams{AccountID: accountID, RunID: runID, Slug: call.Address})
		if err != nil {
			return "", fmt.Errorf("resolve rehearsal workflow target: %w", err)
		}
		if target == "" {
			return "", ErrNotFound
		}
		return target, nil
	}
	id, err := uuid.Parse(call.Address)
	if err != nil {
		return "", ErrNotFound
	}
	reference, err := q.ScenarioCapturedWorkflowRunReference(ctx, tx, sqlc.ScenarioCapturedWorkflowRunReferenceParams{ID: id.String(), AccountID: accountID, RunID: runID})
	if err == nil {
		return reference, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("resolve captured workflow run: %w", err)
	}
	present, err := q.ScenarioExternalWorkflowRunInAccount(ctx, tx, sqlc.ScenarioExternalWorkflowRunInAccountParams{ID: id.String(), AccountID: accountID})
	if err != nil {
		return "", fmt.Errorf("resolve workflow run: %w", err)
	}
	if !present {
		return "", ErrNotFound
	}
	return "run:" + id.String(), nil
}

func (m *MemStore) CaptureScenarioPlatformCall(ctx context.Context, appID string, call ScenarioPlatformCallCapture) (ScenarioPlatformCallResult, bool, error) {
	var empty ScenarioPlatformCallResult
	if err := ctx.Err(); err != nil {
		return empty, true, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	policy, enrolled := m.scenarioActionPolicyLocked(appID)
	if !enrolled {
		return empty, false, nil
	}
	if !validScenarioPlatformCall(call) {
		return empty, true, ErrConflict
	}
	member := policy.Member
	for _, p := range m.scenarioActionPolicies {
		if p.Member.AccountID == member.AccountID && p.Member.RunID == member.RunID && !p.SealedAt.IsZero() {
			return empty, true, ErrScenarioActionsSealed
		}
	}
	target := m.scenarioPlatformTargetLocked(member, call)
	if target == "" {
		return empty, true, ErrNotFound
	}
	captured := 0
	var prior []sqlc.ScenarioPlatformCallsByIdempotencyKeyRow
	for _, row := range m.scenarioPlatformCalls {
		if row.accountID != member.AccountID || row.runID != member.RunID {
			continue
		}
		captured++
		if call.IdempotencyKey != "" && row.appID == member.AppID && row.call.Kind == call.Kind && row.target == target && row.call.IdempotencyKey == call.IdempotencyKey {
			same := row.call.Oversized == call.Oversized && (call.Oversized || sameScenarioEventData(row.call.Message, call.Message))
			prior = append(prior, sqlc.ScenarioPlatformCallsByIdempotencyKeyRow{ID: row.id, Name: row.call.Name, Same: same})
		}
	}
	if id, err := scenarioPlatformReplay(call.Kind, call.Name, prior); id != "" || err != nil {
		return ScenarioPlatformCallResult{ID: id, Replayed: id != ""}, true, err
	}
	if captured > api.MaxScenarioCapturedPlatformCalls {
		return ScenarioPlatformCallResult{ID: uuid.NewString()}, true, nil
	}
	row := memScenarioCapturedPlatformCall{id: uuid.NewString(), accountID: member.AccountID, runID: member.RunID, appID: member.AppID, target: target, call: call,
		createdAt: time.Now().UTC()}
	row.call.Message = append(json.RawMessage(nil), call.Message...)
	if call.Oversized {
		row.call.Message = nil
	}
	m.scenarioPlatformCalls = append(m.scenarioPlatformCalls, row)
	return ScenarioPlatformCallResult{ID: row.id}, true, nil
}

// scenarioPlatformTargetLocked mirrors PgStore.scenarioPlatformTarget.
func (m *MemStore) scenarioPlatformTargetLocked(member ScenarioTestMember, call ScenarioPlatformCallCapture) string {
	if scenarioRealtimeKind(call.Kind) {
		return m.scenarioRealtimeTargetLocked(member, call)
	}
	switch call.Kind {
	case api.ScenarioPlatformKindExecution:
		return api.ScenarioExecutionTarget
	case api.ScenarioPlatformKindExecutionCancel:
		return m.scenarioExecutionReferenceLocked(member.AccountID, member.RunID, call.Address)
	}
	if call.Kind == api.ScenarioPlatformKindWorkflowRun {
		if !api.ValidAppSlug(call.Address) {
			return ""
		}
		return m.scenarioQueueTargetLocked(member, call.Address)
	}
	id, err := uuid.Parse(call.Address)
	if err != nil {
		return ""
	}
	for i, row := range m.scenarioPlatformCalls {
		if row.accountID != member.AccountID || row.runID != member.RunID || row.call.Kind != api.ScenarioPlatformKindWorkflowRun || canonicalMemUUID(row.id) != id.String() {
			continue
		}
		ordinal := 0
		for _, earlier := range m.scenarioPlatformCalls[:i+1] {
			if earlier.accountID == row.accountID && earlier.runID == row.runID && earlier.appID == row.appID &&
				earlier.call.Kind == api.ScenarioPlatformKindWorkflowRun && earlier.target == row.target && earlier.call.Name == row.call.Name {
				ordinal++
			}
		}
		starter, _ := m.scenarioActionPolicyLocked(row.appID)
		return "run:" + starter.Member.Workload + "/" + row.target + "/" + row.call.Name + "#" + strconv.Itoa(ordinal)
	}
	for runID, run := range m.workflowRuns {
		if canonicalMemUUID(runID) == id.String() && sameScenarioAccount(m.apps[run.AppID].AccountID, member.AccountID) {
			return "run:" + id.String()
		}
	}
	return ""
}

// scenarioExecutionReferenceLocked mirrors ScenarioCapturedExecutionReference:
// the run's captured start id as execution:<starter>/<runtime>#<ordinal>, or
// "" for anything else.
func (m *MemStore) scenarioExecutionReferenceLocked(accountID, runID, id string) string {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return ""
	}
	for i, row := range m.scenarioPlatformCalls {
		if !sameScenarioAccount(row.accountID, accountID) || row.runID != runID || row.call.Kind != api.ScenarioPlatformKindExecution || canonicalMemUUID(row.id) != parsed.String() {
			continue
		}
		ordinal := 0
		for _, earlier := range m.scenarioPlatformCalls[:i+1] {
			if earlier.accountID == row.accountID && earlier.runID == row.runID && earlier.appID == row.appID &&
				earlier.call.Kind == api.ScenarioPlatformKindExecution && earlier.call.Name == row.call.Name {
				ordinal++
			}
		}
		starter, _ := m.scenarioActionPolicyLocked(row.appID)
		return "execution:" + starter.Member.Workload + "/" + row.call.Name + "#" + strconv.Itoa(ordinal)
	}
	return ""
}

// scenarioRealtimeTargetLocked mirrors ScenarioRealtimeEndpointTarget.
func (m *MemStore) scenarioRealtimeTargetLocked(member ScenarioTestMember, call ScenarioPlatformCallCapture) string {
	id, err := uuid.Parse(call.Endpoint)
	if err != nil || call.Address != "" && !api.ValidAppSlug(call.Address) {
		return ""
	}
	for key, endpoint := range m.managedRealtimeEndpoints {
		if canonicalMemUUID(key) != id.String() || !sameScenarioAccount(endpoint.AccountID, member.AccountID) {
			continue
		}
		app := m.apps[endpoint.AppID]
		owner, enrolled := m.scenarioActionPolicyLocked(endpoint.AppID)
		inRun := enrolled && owner.Member.RunID == member.RunID && owner.Member.AccountID == member.AccountID && owner.Member.Workload != ""
		if call.Address != "" && app.Slug != call.Address && (!inRun || owner.Member.Workload != call.Address) {
			return ""
		}
		if !inRun {
			return "app:" + app.Slug + "/endpoint:" + id.String()
		}
		ordinal := 0
		for _, other := range m.managedRealtimeEndpoints {
			if other.AppID == endpoint.AppID && (other.CreatedAt.Before(endpoint.CreatedAt) || other.CreatedAt.Equal(endpoint.CreatedAt) && canonicalMemUUID(other.ID) <= id.String()) {
				ordinal++
			}
		}
		return "workload:" + owner.Member.Workload + "/endpoint#" + strconv.Itoa(ordinal)
	}
	return ""
}

// applyScenarioPlatformSnapshot records the run's captured platform calls.
// Oversized or overflowing inventory is never presented as complete.
func applyScenarioPlatformSnapshot(snapshot *api.ScenarioActionSnapshot, calls []api.ScenarioCapturedPlatformCall, oversized int64, overflow bool) {
	snapshot.PlatformCaptureVersion = api.ScenarioPlatformCaptureVersion
	snapshot.IncompletePlatformCalls = oversized
	if overflow {
		snapshot.IncompletePlatformCalls++
	}
	sort.Slice(calls, func(i, j int) bool {
		a, b := calls[i], calls[j]
		if a.Workload != b.Workload {
			return a.Workload < b.Workload
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Target != b.Target {
			return a.Target < b.Target
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.ID < b.ID
	})
	snapshot.PlatformCalls = calls
}

func addScenarioPlatformSnapshot(ctx context.Context, tx sqlc.DBTX, snapshot *api.ScenarioActionSnapshot, accountID, runID string) error {
	rows, err := sqlc.New().ScenarioCapturedPlatformCalls(ctx, tx, sqlc.ScenarioCapturedPlatformCallsParams{AccountID: accountID, RunID: runID})
	if err != nil {
		return fmt.Errorf("read captured rehearsal platform calls: %w", err)
	}
	var calls []api.ScenarioCapturedPlatformCall
	var oversized int64
	for i, row := range rows {
		if i >= api.MaxScenarioCapturedPlatformCalls {
			break
		}
		if row.Oversized {
			oversized++
			continue
		}
		calls = append(calls, api.ScenarioCapturedPlatformCall{ID: row.ID, Workload: row.Workload, Kind: row.Kind, Target: row.Target, Name: row.Name, Message: row.Message})
	}
	applyScenarioPlatformSnapshot(snapshot, calls, oversized, len(rows) > api.MaxScenarioCapturedPlatformCalls)
	return nil
}

func (m *MemStore) addScenarioPlatformSnapshotLocked(snapshot *api.ScenarioActionSnapshot, accountID, runID string, members map[string]ScenarioTestMember) {
	var calls []api.ScenarioCapturedPlatformCall
	var oversized int64
	total := 0
	for _, row := range m.scenarioPlatformCalls {
		if row.accountID != accountID || row.runID != runID {
			continue
		}
		total++
		if total > api.MaxScenarioCapturedPlatformCalls {
			continue
		}
		if row.call.Oversized {
			oversized++
			continue
		}
		member := members[canonicalMemUUID(row.appID)]
		calls = append(calls, api.ScenarioCapturedPlatformCall{ID: row.id, Workload: member.Workload, Kind: row.call.Kind, Target: row.target, Name: row.call.Name,
			Message: append(json.RawMessage(nil), row.call.Message...)})
	}
	applyScenarioPlatformSnapshot(snapshot, calls, oversized, total > api.MaxScenarioCapturedPlatformCalls)
}
