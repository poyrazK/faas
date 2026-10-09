package durableentity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// Execute serializes local handlers and commits state/result/receipt/alarm/outbox in
// one publication. A second process using the same claim can lose CAS and must
// retry the SAME request. A replay never invokes the callback again.
func (m *Manager) Execute(ctx context.Context, claim Claim, request Request, handler func(context.Context, View) (Transition, error)) (Result, error) {
	return m.execute(ctx, claim, request, handler, false)
}

func (m *Manager) execute(ctx context.Context, claim Claim, request Request, handler func(context.Context, View) (Transition, error), preserveDelivery bool) (Result, error) {
	if !claim.ID.valid() || !validIdentity(request.ID) || !json.Valid(request.Payload) || handler == nil {
		return Result{}, ErrInvalid
	}
	if len(request.Payload) > api.MaxDurableEntitySnapshotBytes {
		return Result{}, exceeded("payload_bytes", api.MaxDurableEntitySnapshotBytes, len(request.Payload))
	}
	// Compute identity before the caller's callback can reuse its payload buffer.
	fingerprint := digest(request.Payload)
	unlock, err := m.lock(ctx, claim.ID.prefix())
	if err != nil {
		return Result{}, err
	}
	defer unlock()
	value, _, err := m.owned(ctx, claim)
	if err != nil {
		return Result{}, err
	}
	state, err := m.readSnapshot(ctx, value)
	if err != nil {
		return Result{}, m.restoreFailure(ctx, claim, value, err)
	}
	saved, exists, err := m.findReceipt(ctx, state, request.ID)
	if err != nil {
		return Result{}, m.restoreFailure(ctx, claim, value, err)
	}
	if exists {
		if saved.Fingerprint != fingerprint {
			return Result{}, ErrRequestConflict
		}
		return Result{Value: append(json.RawMessage(nil), saved.Result...), Version: saved.Version, Replayed: true}, nil
	}
	if value.StorageLimitBytes > 0 && value.StorageUsage == nil {
		return Result{}, ErrInventoryPending
	}
	if state.Version == ^uint64(0) {
		return Result{}, ErrLimit
	}
	transition, err := handler(ctx, viewOf(state))
	if err != nil {
		return Result{}, fmt.Errorf("entity handler: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	return m.commitMode(ctx, claim, value, state, request.ID, fingerprint, transition, preserveDelivery)
}

func (m *Manager) commit(ctx context.Context, claim Claim, base manifest, state snapshot, requestID, fingerprint string, transition Transition) (Result, error) {
	return m.commitMode(ctx, claim, base, state, requestID, fingerprint, transition, false)
}

func (m *Manager) commitMode(ctx context.Context, claim Claim, base manifest, state snapshot, requestID, fingerprint string, transition Transition, preserveDelivery bool) (Result, error) {
	if !json.Valid(transition.Data) || !json.Valid(transition.Result) || !validAlarm(transition.AlarmAt) {
		return Result{}, ErrInvalid
	}
	if len(transition.Data)+len(transition.Result) > api.MaxDurableEntitySnapshotBytes {
		return Result{}, exceeded("snapshot_bytes", api.MaxDurableEntitySnapshotBytes, len(transition.Data)+len(transition.Result))
	}
	state.Version++
	state.Data = append(json.RawMessage(nil), transition.Data...)
	state.AlarmAt = copyTime(transition.AlarmAt)
	if err := appendOutbox(&state, transition.Outbox, m.now()); err != nil {
		return Result{}, err
	}
	saved := receipt{Fingerprint: fingerprint, Result: append(json.RawMessage(nil), transition.Result...), Version: state.Version}
	plan := &commitPlan{ObjectStore: m.store}
	planner := &Manager{store: plan, now: m.now, lease: m.lease}
	delta, err := planner.journalTransition(ctx, base, &state, requestID, saved)
	if err != nil {
		return Result{}, m.restoreFailure(ctx, claim, base, err)
	}
	if len(state.Outbox) > 0 {
		state.Schema = 3
	}
	body, err := json.Marshal(state)
	if err != nil {
		return Result{}, fmt.Errorf("encode entity snapshot: %w", err)
	}
	if len(body) > api.MaxDurableEntitySnapshotBytes {
		return Result{}, exceeded("snapshot_bytes", api.MaxDurableEntitySnapshotBytes, len(body))
	}
	if _, err := projectedUsage(base, delta, int64(len(body))); err != nil {
		return Result{}, err
	}
	key := fmt.Sprintf("%ssnapshots/%d/%s.json", claim.ID.prefix(), base.Generation, uuid.NewString())
	plan.objects = append(plan.objects, plannedObject{key: key, body: body})
	if err := plan.upload(ctx); err != nil {
		return Result{}, fmt.Errorf("upload uncommitted entity snapshot: %w", err)
	}
	// Reload AFTER the callback/upload: renewal may have changed only authority
	// metadata. Takeover or another commit must never be overwritten.
	latest, etag, err := m.owned(ctx, claim)
	if err != nil {
		return Result{}, err
	}
	if latest.Version != base.Version || latest.SnapshotKey != base.SnapshotKey || latest.Generation != base.Generation {
		return Result{}, ErrConflict
	}
	usage, err := projectedUsage(latest, delta, int64(len(body)))
	if err != nil {
		return Result{}, err
	}
	latest.Version, latest.SnapshotKey, latest.SnapshotHash = state.Version, key, digest(body)
	latest.StorageUsage = usage
	if !preserveDelivery {
		latest.AlarmDelivery = nil
	}
	if err := m.putManifest(ctx, latest, etag); err != nil {
		return Result{}, err
	}
	// Index hints are repairable. Failure after the authoritative publication
	// cannot turn an acknowledged transition into a failure.
	m.publishAlarmHint(ctx, latest, state)
	m.publishOutboxHint(ctx, latest, state)
	// Return the encoded receipt representation so first delivery and replay
	// agree even when a handler supplied whitespace in its JSON result.
	encoded, err := json.Marshal(saved.Result)
	if err != nil {
		return Result{}, ErrUncertain
	}
	return Result{Value: encoded, Version: state.Version}, nil
}

func (m *Manager) restoreFailure(ctx context.Context, claim Claim, base manifest, restoreErr error) error {
	if !errors.Is(restoreErr, ErrNotFound) {
		return restoreErr
	}
	latest, _, err := m.owned(ctx, claim)
	if err != nil {
		return err
	}
	if latest.SnapshotKey != base.SnapshotKey || latest.Generation != base.Generation {
		return ErrConflict
	}
	return restoreErr
}

func copyTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := value.UTC()
	return &copy
}

func validAlarm(value *time.Time) bool {
	return value == nil || !value.IsZero() && value.Year() >= 1 && value.Year() <= 9999
}

func errorsForRestore(err error) error {
	if errors.Is(err, ErrNotFound) {
		return errors.Join(ErrCorrupt, err)
	}
	return err
}
