package durableentity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

// Read restores one committed view. An entity without a transition starts at
// {} and version 0; missing/corrupt published snapshots are always errors.
func (m *Manager) Read(ctx context.Context, id ID) (View, error) {
	value, _, err := m.readManifest(ctx, id)
	if err != nil {
		return View{}, err
	}
	state, err := m.readSnapshot(ctx, value)
	if errors.Is(err, ErrNotFound) {
		// Cleanup may reclaim a snapshot after its manifest was read. A changed
		// authority is a retryable read conflict, not corrupt committed state.
		latest, _, readErr := m.readManifest(ctx, id)
		if readErr != nil {
			return View{}, readErr
		}
		if latest.SnapshotKey != value.SnapshotKey {
			return View{}, ErrConflict
		}
	}
	if err != nil {
		return View{}, err
	}
	return viewOf(state), nil
}

func viewOf(state snapshot) View {
	return View{Data: append(json.RawMessage(nil), state.Data...), Version: state.Version, AlarmAt: copyTime(state.AlarmAt)}
}

func (m *Manager) readSnapshot(ctx context.Context, value manifest) (snapshot, error) {
	if value.Version == 0 {
		return snapshot{Schema: 2, ID: value.ID, Data: json.RawMessage(`{}`)}, nil
	}
	body, _, err := m.store.Get(ctx, value.SnapshotKey, api.MaxDurableEntitySnapshotBytes)
	if err != nil {
		return snapshot{}, fmt.Errorf("restore entity snapshot: %w", errorsForRestore(err))
	}
	var state snapshot
	if len(body) > api.MaxDurableEntitySnapshotBytes || digest(body) != value.SnapshotHash || json.Unmarshal(body, &state) != nil || !validSnapshot(value, state) || value.StorageUsage != nil && value.StorageUsage.SnapshotBytes != int64(len(body)) {
		return snapshot{}, ErrCorrupt
	}
	return state, nil
}

func validSnapshot(value manifest, state snapshot) bool {
	if state.ID != value.ID || state.Version != value.Version || !json.Valid(state.Data) || !validAlarm(state.AlarmAt) {
		return false
	}
	if value.AlarmDelivery != nil && (state.AlarmAt == nil || !state.AlarmAt.Equal(value.AlarmDelivery.At)) {
		return false
	}
	if state.Schema == 1 {
		return state.ReceiptRoot == nil && state.LegacyReceipts == nil && validReceipts(state.Receipts, state.Version)
	}
	return state.Schema == 2 && state.ReceiptRoot != nil && len(state.Receipts) == 0 && validJournalRef(state.ID, state.ReceiptRoot, value.Generation) && validLegacyRef(state.ID, state.LegacyReceipts, value.Generation)
}
