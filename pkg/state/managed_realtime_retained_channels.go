package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
)

// Retained channel limits (ADR-1323, amendment of 2026-10-27). A channel's
// head row outlives its messages so sequences never restart, so the limits
// count channels that hold something now: retained messages, reducer state or
// a snapshot. An endpoint holds at most ManagedRealtimeHistoryMaxChannels of
// them and an account its plan's RetainedChannelsPerAccount. Expired messages
// are pruned by PruneExpiredManagedRealtimeChannelMessages, which frees their
// channel.

// ManagedRealtimeRetainedChannelLimitError reports an account at its plan's
// retained channel allowance.
type ManagedRealtimeRetainedChannelLimitError struct{ Limit, Observed int }

func (e *ManagedRealtimeRetainedChannelLimitError) Error() string {
	return fmt.Sprintf("state: account holds %d of %d retained realtime channels", e.Observed, e.Limit)
}
func (e *ManagedRealtimeRetainedChannelLimitError) Unwrap() error {
	return ErrManagedRealtimeHistoryLimit
}

// heldRealtimeChannel is true for a head h that holds retained messages,
// reducer state or a snapshot.
const heldRealtimeChannel = `(h.oldest_sequence < h.next_sequence
	or exists(select 1 from managed_realtime_channel_reducers r where r.endpoint_id = h.endpoint_id and r.channel = h.channel)
	or exists(select 1 from managed_realtime_channel_snapshots s where s.endpoint_id = h.endpoint_id and s.channel = h.channel))`

// heldRealtimeChannels lists the (endpoint, channel) pairs held on the
// endpoints that endpoints selects (a subquery over e). Each branch reads one
// index, so dormant heads are never scanned: heads through
// managed_realtime_channel_heads_held_idx, reducers and snapshots through
// their primary keys.
func heldRealtimeChannels(endpoints string) string {
	return `select h.endpoint_id, h.channel from managed_realtime_channel_heads h
		where h.endpoint_id in (` + endpoints + `) and h.oldest_sequence < h.next_sequence
	union select r.endpoint_id, r.channel from managed_realtime_channel_reducers r where r.endpoint_id in (` + endpoints + `)
	union select s.endpoint_id, s.channel from managed_realtime_channel_snapshots s where s.endpoint_id in (` + endpoints + `)`
}

// checkRetainedChannelPG admits retaining channel on endpointID. The caller
// holds the endpoint's row lock, so the endpoint count is exact; the account
// count may run over by the publishes racing on the account's other endpoints.
func checkRetainedChannelPG(ctx context.Context, tx pgx.Tx, endpointID, channel string) error {
	var held bool
	err := tx.QueryRow(ctx, `select `+heldRealtimeChannel+` from managed_realtime_channel_heads h
		where h.endpoint_id = $1 and h.channel = $2`, endpointID, channel).Scan(&held)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("state: check retained realtime channel: %w", err)
	}
	if held {
		return nil
	}
	var channels int
	if err := tx.QueryRow(ctx, `select count(*) from (`+heldRealtimeChannels(`select $1::uuid`)+`) held`, endpointID).Scan(&channels); err != nil {
		return fmt.Errorf("state: count retained realtime channels: %w", err)
	}
	if channels >= ManagedRealtimeHistoryMaxChannels {
		return ErrManagedRealtimeHistoryLimit
	}
	var account string
	var plan api.Plan
	if err := tx.QueryRow(ctx, `select e.account_id, a.plan from managed_realtime_endpoints e
		join accounts a on a.id = e.account_id where e.id = $1`, endpointID).Scan(&account, &plan); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("state: read realtime endpoint plan: %w", err)
	}
	limits, _ := api.RealtimeLimitsFor(plan)
	if limits.RetainedChannelsPerAccount <= 0 {
		return nil
	}
	if err := tx.QueryRow(ctx, `select count(*) from (`+heldRealtimeChannels(`select e.id from managed_realtime_endpoints e where e.account_id = $1`)+`) held`, account).Scan(&channels); err != nil {
		return fmt.Errorf("state: count account retained realtime channels: %w", err)
	}
	if channels >= limits.RetainedChannelsPerAccount {
		return &ManagedRealtimeRetainedChannelLimitError{Limit: limits.RetainedChannelsPerAccount, Observed: channels}
	}
	return nil
}

// checkRetainedChannelMem is checkRetainedChannelPG for the MemStore. The
// caller holds the store's lock.
func (m *MemStore) checkRetainedChannelMem(endpointID, channel string) error {
	key := managedRealtimeHistoryKey{endpointID: endpointID, channel: channel}
	if state := m.managedRealtimeHistory[key]; state != nil && state.oldest < state.next {
		return nil
	}
	if _, ok := m.managedRealtimeReducers[key]; ok {
		return nil
	}
	if _, ok := m.managedRealtimeSnapshots[key]; ok {
		return nil
	}
	held := m.heldRealtimeChannelsMem(func(id string) bool { return id == endpointID })
	if len(held) >= ManagedRealtimeHistoryMaxChannels {
		return ErrManagedRealtimeHistoryLimit
	}
	endpoint, ok := m.managedRealtimeEndpoints[endpointID]
	if !ok {
		return nil
	}
	limits, _ := api.RealtimeLimitsFor(m.accounts[endpoint.AccountID].Plan)
	if limits.RetainedChannelsPerAccount <= 0 {
		return nil
	}
	accountHeld := m.heldRealtimeChannelsMem(func(id string) bool {
		return m.managedRealtimeEndpoints[id].AccountID == endpoint.AccountID
	})
	if len(accountHeld) >= limits.RetainedChannelsPerAccount {
		return &ManagedRealtimeRetainedChannelLimitError{Limit: limits.RetainedChannelsPerAccount, Observed: len(accountHeld)}
	}
	return nil
}

// heldRealtimeChannelsMem is the set of held channels on the endpoints that
// include selects.
func (m *MemStore) heldRealtimeChannelsMem(include func(endpointID string) bool) map[managedRealtimeHistoryKey]bool {
	held := map[managedRealtimeHistoryKey]bool{}
	for key, state := range m.managedRealtimeHistory {
		if state != nil && state.oldest < state.next && include(key.endpointID) {
			held[key] = true
		}
	}
	for key := range m.managedRealtimeReducers {
		if include(key.endpointID) {
			held[key] = true
		}
	}
	for key := range m.managedRealtimeSnapshots {
		if include(key.endpointID) {
			held[key] = true
		}
	}
	return held
}
