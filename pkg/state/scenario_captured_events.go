// adr: 20261020090000
package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ScenarioEventCapture is one event an enrolled app published, after the same
// envelope normalization and schema validation as an ordinary publish. ID is
// the envelope ID: a second capture of the same source and ID is the same
// event, as in production (ADR-20261021120000).
type ScenarioEventCapture struct {
	ID            string
	AccountID     string
	Source        string
	Type          string
	SchemaVersion string
	Data          json.RawMessage
}

// ScenarioEventCaptureStore redirects an enrolled app's published events into
// its run's evidence instead of the account's event bus. captured is false
// only for an app with no rehearsal policy, which then publishes normally.
// When captured is true the event must never be published, even with an error.
type ScenarioEventCaptureStore interface {
	CaptureScenarioEvent(ctx context.Context, appID, installedRunID string, event ScenarioEventCapture) (captured bool, err error)
}

// ErrScenarioActionsSealed refuses an effect that arrives after its run's
// action window closed. It is never published either.
var ErrScenarioActionsSealed = errors.New("state: rehearsal run actions are sealed")

var (
	// ErrScenarioEventDuplicate reports a capture of an event the run already
	// holds with the same source, ID and content. Like a duplicate production
	// publish it succeeds without a second event.
	ErrScenarioEventDuplicate = errors.New("state: rehearsal event already captured")
	// ErrScenarioEventIdentityConflict reports the same source and ID with a
	// different type, schema version or data. It is an ErrConflict.
	ErrScenarioEventIdentityConflict = fmt.Errorf("%w: rehearsal event source and id hold different content", ErrConflict)
)

// ScenarioRehearsalNetworkPolicy names a contained app's run and account.
type ScenarioRehearsalNetworkPolicy struct {
	RunID, AccountID string
}

// ScenarioRehearsalNetworkPolicyStore reads a contained app's policy; ok is
// false for any other app.
type ScenarioRehearsalNetworkPolicyStore interface {
	ScenarioRehearsalNetworkPolicy(ctx context.Context, appID string) (policy ScenarioRehearsalNetworkPolicy, ok bool, err error)
}

func (s *PgStore) ScenarioRehearsalNetworkPolicy(ctx context.Context, appID string) (ScenarioRehearsalNetworkPolicy, bool, error) {
	row, err := sqlc.New().ScenarioRehearsalNetworkPolicy(ctx, s.pool, appID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ScenarioRehearsalNetworkPolicy{}, false, nil
	}
	if err != nil {
		return ScenarioRehearsalNetworkPolicy{}, false, fmt.Errorf("read rehearsal network policy: %w", err)
	}
	return ScenarioRehearsalNetworkPolicy{RunID: row.RunID, AccountID: row.AccountID}, true, nil
}

func (m *MemStore) ScenarioRehearsalNetworkPolicy(ctx context.Context, appID string) (ScenarioRehearsalNetworkPolicy, bool, error) {
	if err := ctx.Err(); err != nil {
		return ScenarioRehearsalNetworkPolicy{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	policy, ok := m.scenarioActionPolicyLocked(appID)
	if !ok || policy.Member.NetworkMode != ScenarioNetworkContained {
		return ScenarioRehearsalNetworkPolicy{}, false, nil
	}
	return ScenarioRehearsalNetworkPolicy{RunID: policy.Member.RunID, AccountID: policy.Member.AccountID}, true, nil
}

type memScenarioCapturedEvent struct {
	id, accountID, runID, appID string
	event                       ScenarioEventCapture
	oversized                   bool
	createdAt                   time.Time
}

func sameScenarioAccount(a, b string) bool {
	x, errA := uuid.Parse(a)
	y, errB := uuid.Parse(b)
	return errA == nil && errB == nil && x == y
}

func (s *PgStore) CaptureScenarioEvent(ctx context.Context, appID, installedRunID string, event ScenarioEventCapture) (bool, error) {
	q := sqlc.New()
	policy, err := q.ScenarioEventCapturePolicy(ctx, s.pool, appID)
	if errors.Is(err, pgx.ErrNoRows) {
		if installedRunID != "" {
			return true, ErrConflict // a contained instance never publishes without its policy
		}
		return false, nil
	}
	if err != nil {
		return true, fmt.Errorf("read rehearsal event capture policy: %w", err)
	}
	if !sameScenarioAccount(policy.AccountID, event.AccountID) || installedRunID != "" && installedRunID != policy.RunID {
		return true, ErrConflict
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return true, fmt.Errorf("begin rehearsal event capture: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// The seal takes the same run lock, so an event is either in the sealed
	// snapshot or refused as late; it can never commit unseen.
	if err := q.LockScenarioActionRun(ctx, tx, sqlc.LockScenarioActionRunParams{AccountID: policy.AccountID, RunID: policy.RunID}); err != nil {
		return true, fmt.Errorf("lock rehearsal event capture: %w", err)
	}
	st, err := q.ScenarioEventCaptureState(ctx, tx, sqlc.ScenarioEventCaptureStateParams{AccountID: policy.AccountID, RunID: policy.RunID})
	if err != nil {
		return true, fmt.Errorf("read rehearsal event capture state: %w", err)
	}
	if st.Sealed {
		return true, ErrScenarioActionsSealed
	}
	oversized := len(event.Data) > api.MaxScenarioCapturedEventDataBytes
	data := []byte(event.Data)
	if oversized {
		data = nil
	}
	if event.ID != "" {
		same, err := q.ScenarioCapturedEventIdentity(ctx, tx, sqlc.ScenarioCapturedEventIdentityParams{AccountID: policy.AccountID, RunID: policy.RunID,
			Source: event.Source, EventID: event.ID, Type: event.Type, SchemaVersion: event.SchemaVersion, Data: data, Oversized: oversized})
		switch {
		case err == nil && same:
			return true, ErrScenarioEventDuplicate
		case err == nil:
			return true, ErrScenarioEventIdentityConflict
		case !errors.Is(err, pgx.ErrNoRows):
			return true, fmt.Errorf("read rehearsal event identity: %w", err)
		}
	}
	if st.Captured > api.MaxScenarioCapturedEvents {
		return true, nil // the extra row already marks the inventory incomplete
	}
	if err := q.InsertScenarioCapturedEvent(ctx, tx, sqlc.InsertScenarioCapturedEventParams{AccountID: policy.AccountID, RunID: policy.RunID, AppID: appID,
		Source: event.Source, Type: event.Type, SchemaVersion: event.SchemaVersion, Data: data, Oversized: oversized, EventID: event.ID}); err != nil {
		return true, fmt.Errorf("persist rehearsal event capture: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return true, fmt.Errorf("commit rehearsal event capture: %w", err)
	}
	return true, nil
}

func (m *MemStore) CaptureScenarioEvent(ctx context.Context, appID, installedRunID string, event ScenarioEventCapture) (bool, error) {
	if err := ctx.Err(); err != nil {
		return true, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	policy, enrolled := m.scenarioActionPolicyLocked(appID)
	if !enrolled {
		if installedRunID != "" {
			return true, ErrConflict
		}
		return false, nil
	}
	member := policy.Member
	if !sameScenarioAccount(member.AccountID, event.AccountID) || installedRunID != "" && installedRunID != member.RunID {
		return true, ErrConflict
	}
	captured := 0
	for _, p := range m.scenarioActionPolicies {
		if p.Member.AccountID == member.AccountID && p.Member.RunID == member.RunID && !p.SealedAt.IsZero() {
			return true, ErrScenarioActionsSealed
		}
	}
	oversized := len(event.Data) > api.MaxScenarioCapturedEventDataBytes
	for _, e := range m.scenarioCapturedEvents {
		if e.accountID != member.AccountID || e.runID != member.RunID {
			continue
		}
		captured++
		if event.ID != "" && e.event.ID == event.ID && e.event.Source == event.Source {
			if e.event.Type == event.Type && e.event.SchemaVersion == event.SchemaVersion && e.oversized == oversized && (oversized || sameScenarioEventData(e.event.Data, event.Data)) {
				return true, ErrScenarioEventDuplicate
			}
			return true, ErrScenarioEventIdentityConflict
		}
	}
	if captured > api.MaxScenarioCapturedEvents {
		return true, nil
	}
	row := memScenarioCapturedEvent{id: uuid.NewString(), accountID: member.AccountID, runID: member.RunID, appID: member.AppID, event: event,
		oversized: oversized, createdAt: time.Now().UTC()}
	row.event.Data = append(json.RawMessage(nil), event.Data...)
	if row.oversized {
		row.event.Data = nil
	}
	m.scenarioCapturedEvents = append(m.scenarioCapturedEvents, row)
	return true, nil
}

// sameScenarioEventData mirrors jsonb equality for the memory store.
func sameScenarioEventData(a, b json.RawMessage) bool {
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}

// applyScenarioEventSnapshot records the run's captured events and suppressed
// ordinary webhook events. Oversized or overflowing inventory is never
// presented as complete.
func applyScenarioEventSnapshot(snapshot *api.ScenarioActionSnapshot, events []api.ScenarioCapturedEvent, oversized int64, overflow bool, suppressed []api.ScenarioSuppressedWebhookEvents, suppressedOverflow bool) {
	snapshot.EventCaptureVersion = api.ScenarioEventCaptureVersion
	snapshot.IncompleteCapturedEvents = oversized
	if overflow || suppressedOverflow {
		snapshot.IncompleteCapturedEvents++
	}
	sort.Slice(events, func(i, j int) bool {
		a, b := events[i], events[j]
		if a.Workload != b.Workload {
			return a.Workload < b.Workload
		}
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		return a.ID < b.ID
	})
	sort.Slice(suppressed, func(i, j int) bool {
		if suppressed[i].Workload != suppressed[j].Workload {
			return suppressed[i].Workload < suppressed[j].Workload
		}
		return suppressed[i].Event < suppressed[j].Event
	})
	snapshot.PublishedEvents, snapshot.SuppressedWebhookEvents = events, suppressed
}

func addScenarioEventSnapshot(ctx context.Context, tx sqlc.DBTX, snapshot *api.ScenarioActionSnapshot, accountID, runID string) error {
	q := sqlc.New()
	rows, err := q.ScenarioCapturedEvents(ctx, tx, sqlc.ScenarioCapturedEventsParams{AccountID: accountID, RunID: runID})
	if err != nil {
		return fmt.Errorf("read captured rehearsal events: %w", err)
	}
	var events []api.ScenarioCapturedEvent
	var oversized int64
	for i, row := range rows {
		if i >= api.MaxScenarioCapturedEvents {
			break
		}
		if row.Oversized {
			oversized++
			continue
		}
		events = append(events, api.ScenarioCapturedEvent{ID: row.ID, Workload: row.Workload, Source: row.Source, Type: row.Type, SchemaVersion: row.SchemaVersion, Data: row.Data})
	}
	groups, err := q.ScenarioSuppressedWebhookEvents(ctx, tx, sqlc.ScenarioSuppressedWebhookEventsParams{AccountID: accountID, RunID: runID})
	if err != nil {
		return fmt.Errorf("read suppressed rehearsal webhook events: %w", err)
	}
	var suppressed []api.ScenarioSuppressedWebhookEvents
	for i, group := range groups {
		if i >= api.MaxScenarioCapturedEvents {
			break
		}
		suppressed = append(suppressed, api.ScenarioSuppressedWebhookEvents{Workload: group.Workload, Event: group.Event, Count: group.Count})
	}
	applyScenarioEventSnapshot(snapshot, events, oversized, len(rows) > api.MaxScenarioCapturedEvents, suppressed, len(groups) > api.MaxScenarioCapturedEvents)
	return nil
}

func (m *MemStore) addScenarioEventSnapshotLocked(snapshot *api.ScenarioActionSnapshot, accountID, runID string, members map[string]ScenarioTestMember) {
	var events []api.ScenarioCapturedEvent
	var oversized int64
	total := 0
	for _, row := range m.scenarioCapturedEvents {
		if row.accountID != accountID || row.runID != runID {
			continue
		}
		total++
		if total > api.MaxScenarioCapturedEvents {
			continue
		}
		if row.oversized {
			oversized++
			continue
		}
		member := members[canonicalMemUUID(row.appID)]
		events = append(events, api.ScenarioCapturedEvent{ID: row.id, Workload: member.Workload, Source: row.event.Source, Type: row.event.Type,
			SchemaVersion: row.event.SchemaVersion, Data: append(json.RawMessage(nil), row.event.Data...)})
	}
	counts := map[[2]string]int64{}
	for _, delivery := range m.appWebhookDeliveries {
		if delivery.LastError != ScenarioWebhookSuppressed || delivery.Event == OperationEffectEvent {
			continue
		}
		member, byApp := members[canonicalMemUUID(delivery.AppID)]
		if !byApp {
			hook := m.appWebhooks[delivery.WebhookID]
			if hook.ID == "" {
				for id, candidate := range m.appWebhooks {
					if canonicalMemUUID(id) == canonicalMemUUID(delivery.WebhookID) {
						hook = candidate
						break
					}
				}
			}
			var byReceiver bool
			if member, byReceiver = members[canonicalMemUUID(hook.AppID)]; !byReceiver {
				continue
			}
		}
		if !sameScenarioAccount(delivery.AccountID, member.AccountID) {
			continue
		}
		counts[[2]string{member.Workload, string(delivery.Event)}]++
	}
	var suppressed []api.ScenarioSuppressedWebhookEvents
	for key, count := range counts {
		suppressed = append(suppressed, api.ScenarioSuppressedWebhookEvents{Workload: key[0], Event: key[1], Count: count})
	}
	overflow := len(suppressed) > api.MaxScenarioCapturedEvents
	applyScenarioEventSnapshot(snapshot, events, oversized, total > api.MaxScenarioCapturedEvents, suppressed, overflow)
	if overflow {
		snapshot.SuppressedWebhookEvents = snapshot.SuppressedWebhookEvents[:api.MaxScenarioCapturedEvents]
	}
}
