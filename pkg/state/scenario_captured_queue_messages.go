// adr: 20261021090000
package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ScenarioQueueCapture is one queue send or delayed task an enrolled app
// requested through the run-local platform API. Slug is the {slug} the request
// addressed. Schedule is delay:<seconds> or absolute for a delayed task and
// empty for a queue send. Message is nil when Oversized.
type ScenarioQueueCapture struct {
	Kind      string
	Slug      string
	QueueName string
	Schedule  string
	Message   json.RawMessage
	Oversized bool
}

// ScenarioQueueCaptureStore records an enrolled app's queue work as run
// evidence instead of enqueueing it. captured is false only for an app with no
// rehearsal policy, whose request is then not handled at all. ErrNotFound
// reports a target that is neither a run member nor an app of the account, as
// a real send would.
type ScenarioQueueCaptureStore interface {
	CaptureScenarioQueueMessage(ctx context.Context, appID string, msg ScenarioQueueCapture) (id string, captured bool, err error)
}

type memScenarioCapturedQueueMessage struct {
	id, accountID, runID, appID, target string
	msg                                 ScenarioQueueCapture
}

func validScenarioQueueCapture(msg ScenarioQueueCapture) bool {
	if msg.Kind != api.ScenarioQueueKindQueue && msg.Kind != api.ScenarioQueueKindDelayedTask || len(msg.QueueName) > 256 || msg.Oversized != (msg.Message == nil) {
		return false
	}
	return msg.Oversized || (msg.Kind == api.ScenarioQueueKindDelayedTask) == (msg.Schedule != "")
}

func (s *PgStore) CaptureScenarioQueueMessage(ctx context.Context, appID string, msg ScenarioQueueCapture) (string, bool, error) {
	q := sqlc.New()
	policy, err := q.ScenarioEventCapturePolicy(ctx, s.pool, appID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", true, fmt.Errorf("read rehearsal queue capture policy: %w", err)
	}
	if !validScenarioQueueCapture(msg) {
		return "", true, ErrConflict
	}
	if !api.ValidAppSlug(msg.Slug) {
		return "", true, ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", true, fmt.Errorf("begin rehearsal queue capture: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// The seal takes the same run lock, so a message is either in the sealed
	// snapshot or refused as late; it can never commit unseen.
	if err := q.LockScenarioActionRun(ctx, tx, sqlc.LockScenarioActionRunParams{AccountID: policy.AccountID, RunID: policy.RunID}); err != nil {
		return "", true, fmt.Errorf("lock rehearsal queue capture: %w", err)
	}
	st, err := q.ScenarioQueueCaptureState(ctx, tx, sqlc.ScenarioQueueCaptureStateParams{AccountID: policy.AccountID, RunID: policy.RunID})
	if err != nil {
		return "", true, fmt.Errorf("read rehearsal queue capture state: %w", err)
	}
	if st.Sealed {
		return "", true, ErrScenarioActionsSealed
	}
	target, err := q.ScenarioQueueCaptureTarget(ctx, tx, sqlc.ScenarioQueueCaptureTargetParams{AccountID: policy.AccountID, RunID: policy.RunID, Slug: msg.Slug})
	if err != nil {
		return "", true, fmt.Errorf("resolve rehearsal queue target: %w", err)
	}
	if target == "" {
		return "", true, ErrNotFound
	}
	if st.Captured > api.MaxScenarioCapturedQueueMessages {
		return uuid.NewString(), true, nil // the extra row already marks the inventory incomplete
	}
	id, err := q.InsertScenarioCapturedQueueMessage(ctx, tx, sqlc.InsertScenarioCapturedQueueMessageParams{AccountID: policy.AccountID, RunID: policy.RunID, AppID: appID,
		Kind: msg.Kind, Target: target, QueueName: msg.QueueName, Schedule: msg.Schedule, Message: []byte(msg.Message), Oversized: msg.Oversized})
	if err != nil {
		return "", true, fmt.Errorf("persist rehearsal queue capture: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", true, fmt.Errorf("commit rehearsal queue capture: %w", err)
	}
	return id, true, nil
}

func (m *MemStore) CaptureScenarioQueueMessage(ctx context.Context, appID string, msg ScenarioQueueCapture) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", true, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	policy, enrolled := m.scenarioActionPolicyLocked(appID)
	if !enrolled {
		return "", false, nil
	}
	if !validScenarioQueueCapture(msg) {
		return "", true, ErrConflict
	}
	if !api.ValidAppSlug(msg.Slug) {
		return "", true, ErrNotFound
	}
	member := policy.Member
	for _, p := range m.scenarioActionPolicies {
		if p.Member.AccountID == member.AccountID && p.Member.RunID == member.RunID && !p.SealedAt.IsZero() {
			return "", true, ErrScenarioActionsSealed
		}
	}
	target := m.scenarioQueueTargetLocked(member, msg.Slug)
	if target == "" {
		return "", true, ErrNotFound
	}
	captured := 0
	for _, row := range m.scenarioQueueCaptures {
		if row.accountID == member.AccountID && row.runID == member.RunID {
			captured++
		}
	}
	if captured > api.MaxScenarioCapturedQueueMessages {
		return uuid.NewString(), true, nil
	}
	row := memScenarioCapturedQueueMessage{id: uuid.NewString(), accountID: member.AccountID, runID: member.RunID, appID: member.AppID, target: target, msg: msg}
	row.msg.Message = append(json.RawMessage(nil), msg.Message...)
	if msg.Oversized {
		row.msg.Message = nil
	}
	m.scenarioQueueCaptures = append(m.scenarioQueueCaptures, row)
	return row.id, true, nil
}

// scenarioQueueTargetLocked mirrors ScenarioQueueCaptureTarget.
func (m *MemStore) scenarioQueueTargetLocked(member ScenarioTestMember, slug string) string {
	var workloads []string
	for _, p := range m.scenarioActionPolicies {
		w := p.Member
		if w.AccountID != member.AccountID || w.RunID != member.RunID || w.Workload == "" {
			continue
		}
		if w.Workload == slug || m.apps[w.AppID].Slug == slug {
			workloads = append(workloads, w.Workload)
		}
	}
	if len(workloads) > 0 {
		sort.Strings(workloads)
		return "workload:" + workloads[0]
	}
	for _, app := range m.apps {
		if app.Slug == slug && sameScenarioAccount(app.AccountID, member.AccountID) {
			return "app:" + slug
		}
	}
	return ""
}

// applyScenarioQueueSnapshot records the run's captured queue work. Oversized
// or overflowing inventory is never presented as complete.
func applyScenarioQueueSnapshot(snapshot *api.ScenarioActionSnapshot, messages []api.ScenarioCapturedQueueMessage, oversized int64, overflow bool) {
	snapshot.QueueCaptureVersion = api.ScenarioQueueCaptureVersion
	snapshot.IncompleteQueueMessages = oversized
	if overflow {
		snapshot.IncompleteQueueMessages++
	}
	sort.Slice(messages, func(i, j int) bool {
		a, b := messages[i], messages[j]
		if a.Workload != b.Workload {
			return a.Workload < b.Workload
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Target != b.Target {
			return a.Target < b.Target
		}
		if a.QueueName != b.QueueName {
			return a.QueueName < b.QueueName
		}
		return a.ID < b.ID
	})
	snapshot.QueueMessages = messages
}

func addScenarioQueueSnapshot(ctx context.Context, tx sqlc.DBTX, snapshot *api.ScenarioActionSnapshot, accountID, runID string) error {
	rows, err := sqlc.New().ScenarioCapturedQueueMessages(ctx, tx, sqlc.ScenarioCapturedQueueMessagesParams{AccountID: accountID, RunID: runID})
	if err != nil {
		return fmt.Errorf("read captured rehearsal queue messages: %w", err)
	}
	var messages []api.ScenarioCapturedQueueMessage
	var oversized int64
	for i, row := range rows {
		if i >= api.MaxScenarioCapturedQueueMessages {
			break
		}
		if row.Oversized {
			oversized++
			continue
		}
		messages = append(messages, api.ScenarioCapturedQueueMessage{ID: row.ID, Workload: row.Workload, Kind: row.Kind, Target: row.Target,
			QueueName: row.QueueName, Schedule: row.Schedule, Message: row.Message})
	}
	applyScenarioQueueSnapshot(snapshot, messages, oversized, len(rows) > api.MaxScenarioCapturedQueueMessages)
	return nil
}

func (m *MemStore) addScenarioQueueSnapshotLocked(snapshot *api.ScenarioActionSnapshot, accountID, runID string, members map[string]ScenarioTestMember) {
	var messages []api.ScenarioCapturedQueueMessage
	var oversized int64
	total := 0
	for _, row := range m.scenarioQueueCaptures {
		if row.accountID != accountID || row.runID != runID {
			continue
		}
		total++
		if total > api.MaxScenarioCapturedQueueMessages {
			continue
		}
		if row.msg.Oversized {
			oversized++
			continue
		}
		member := members[canonicalMemUUID(row.appID)]
		messages = append(messages, api.ScenarioCapturedQueueMessage{ID: row.id, Workload: member.Workload, Kind: row.msg.Kind, Target: row.target,
			QueueName: row.msg.QueueName, Schedule: row.msg.Schedule, Message: append(json.RawMessage(nil), row.msg.Message...)})
	}
	applyScenarioQueueSnapshot(snapshot, messages, oversized, total > api.MaxScenarioCapturedQueueMessages)
}
