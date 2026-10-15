package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type EventAcknowledgmentWriterMember struct {
	Role string `json:"role"`
	Slot string `json:"slot"`
}
type EventAcknowledgmentWriterRegistration struct {
	SessionID, Role, Slot string
	CapabilityVersion     int32
	Inventory             []EventAcknowledgmentWriterMember
}
type EventAcknowledgmentWriterStore interface {
	HeartbeatEventAcknowledgmentWriter(context.Context, EventAcknowledgmentWriterRegistration) error
}
type eventAcknowledgmentWriter struct {
	registration                  EventAcknowledgmentWriterRegistration
	revision                      string
	started, seen, since, expires time.Time
}
type eventAcknowledgmentCoverageHead struct {
	revision       string
	since, through *time.Time
	updated        time.Time
}

func writerSlotValid(role, slot string) bool {
	if (role != "apid" && role != "schedd") || len(slot) == 0 || len(slot) > api.EventAcknowledgmentWriterSlotMaxBytes {
		return false
	}
	for _, c := range []byte(slot) {
		if (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' && c != '.' && c != ':' && c != '-' {
			return false
		}
	}
	return true
}
func acknowledgmentWriterInventory(members []EventAcknowledgmentWriterMember) ([]EventAcknowledgmentWriterMember, []byte, string, error) {
	if len(members) > api.EventAcknowledgmentWriterRosterMax {
		return nil, nil, "", ErrInvalidArgument
	}
	normalized := append([]EventAcknowledgmentWriterMember{}, members...)
	sort.Slice(normalized, func(i, j int) bool {
		if normalized[i].Role != normalized[j].Role {
			return normalized[i].Role < normalized[j].Role
		}
		return normalized[i].Slot < normalized[j].Slot
	})
	roles := map[string]bool{}
	for i, m := range normalized {
		if !writerSlotValid(m.Role, m.Slot) || (i > 0 && m == normalized[i-1]) {
			return nil, nil, "", ErrInvalidArgument
		}
		roles[m.Role] = true
	}
	if len(members) > 0 && (!roles["apid"] || !roles["schedd"]) {
		return nil, nil, "", ErrInvalidArgument
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return nil, nil, "", err
	}
	if len(raw) > api.EventAcknowledgmentWriterRosterMaxBytes {
		return nil, nil, "", ErrInvalidArgument
	}
	if len(members) == 0 {
		return normalized, raw, "", nil
	}
	digest := sha256.Sum256(raw)
	return normalized, raw, hex.EncodeToString(digest[:]), nil
}
func ValidateEventAcknowledgmentWriterRegistration(r EventAcknowledgmentWriterRegistration) error {
	if id, err := operationUUID(r.SessionID); err != nil || id.Bytes == [16]byte{} {
		return ErrInvalidArgument
	}
	if !writerSlotValid(r.Role, r.Slot) || r.CapabilityVersion < 0 {
		return ErrInvalidArgument
	}
	_, _, _, err := acknowledgmentWriterInventory(r.Inventory)
	return err
}
func assessAcknowledgmentWriters(rows []eventAcknowledgmentWriter, now time.Time) api.EventAcknowledgmentRecordingCoverage {
	c := api.EventAcknowledgmentRecordingCoverage{ObservedAt: now, ActiveWriters: len(rows), CapabilityVersion: api.EventAcknowledgmentWriterCapabilityVersion, HeartbeatLeaseSeconds: int(api.EventAcknowledgmentWriterLease / time.Second), Reason: "no_live_writers"}
	if len(rows) == 0 {
		return c
	}
	if len(rows) > api.EventAcknowledgmentWriterLiveMax {
		c.Reason = "writer_limit_exceeded"
		return c
	}
	members, _, revision, err := acknowledgmentWriterInventory(rows[0].registration.Inventory)
	if err != nil || revision == "" {
		c.Reason = "inventory_unconfigured"
		return c
	}
	c.ExpectedWriters = len(members)
	c.InventoryRevision = revision
	expected := map[EventAcknowledgmentWriterMember]int{}
	for _, m := range members {
		expected[m] = 0
	}
	var since, through time.Time
	for _, row := range rows {
		r := row.registration
		if row.revision != revision {
			c.Reason = "inventory_mismatch"
			return c
		}
		if r.CapabilityVersion != api.EventAcknowledgmentWriterCapabilityVersion {
			c.Reason = "unsupported_writer"
			return c
		}
		key := EventAcknowledgmentWriterMember{r.Role, r.Slot}
		n, ok := expected[key]
		if !ok {
			c.Reason = "unexpected_writer"
			return c
		}
		if n != 0 {
			c.Reason = "duplicate_writer"
			return c
		}
		expected[key] = n + 1
		if row.started.IsZero() || row.seen.Before(row.started) || row.since.Before(row.started) || row.since.After(row.seen) || row.seen.After(now) || !row.expires.After(now) || !row.expires.Equal(row.seen.Add(api.EventAcknowledgmentWriterLease)) {
			c.Reason = "invalid_writer_evidence"
			return c
		}
		if row.since.After(since) {
			since = row.since
		}
		if through.IsZero() || row.seen.Before(through) {
			through = row.seen
		}
	}
	for _, n := range expected {
		if n != 1 {
			c.Reason = "missing_writer"
			return c
		}
	}
	c.ContinuousSince = &since
	c.VerifiedThrough = &through
	c.Verified = true
	c.Reason = "verified"
	return c
}
func bindAcknowledgmentCoverage(c api.EventAcknowledgmentRecordingCoverage, head eventAcknowledgmentCoverageHead, heartbeat bool) api.EventAcknowledgmentRecordingCoverage {
	if !c.Verified {
		return c
	}
	if heartbeat {
		if head.since == nil || head.revision != c.InventoryRevision {
			since := c.ObservedAt
			c.ContinuousSince = &since
		} else if head.since.After(*c.ContinuousSince) {
			c.ContinuousSince = cloneEventReceiptTime(head.since)
		}
	} else {
		if head.since == nil || head.through == nil || head.revision != c.InventoryRevision || head.updated.After(c.ObservedAt) {
			c.Verified = false
			c.Reason = "awaiting_heartbeat"
			c.ContinuousSince = nil
			c.VerifiedThrough = nil
			return c
		}
		if head.since.After(*c.ContinuousSince) {
			c.ContinuousSince = cloneEventReceiptTime(head.since)
		}
		if head.through.Before(*c.VerifiedThrough) {
			c.VerifiedThrough = cloneEventReceiptTime(head.through)
		}
	}
	if c.VerifiedThrough.Before(*c.ContinuousSince) {
		c.Verified = false
		c.Reason = "warming_up"
	}
	return c
}
func acknowledgmentWriterRows(rows []sqlc.EventAcknowledgmentWriter) []eventAcknowledgmentWriter {
	out := make([]eventAcknowledgmentWriter, 0, len(rows))
	for _, r := range rows {
		var inventory []EventAcknowledgmentWriterMember
		// Corrupt inventory is invalid evidence, never positive coverage.
		if json.Unmarshal(r.Inventory, &inventory) != nil {
			inventory = nil
		}
		_, _, revision, err := acknowledgmentWriterInventory(inventory)
		if err != nil || revision != r.InventoryRevision {
			revision = "invalid"
		}
		out = append(out, eventAcknowledgmentWriter{registration: EventAcknowledgmentWriterRegistration{SessionID: uuidString(r.SessionID), Role: r.Role, Slot: r.Slot, CapabilityVersion: r.CapabilityVersion, Inventory: inventory}, revision: revision, started: timeFromPgtype(r.StartedAt), seen: timeFromPgtype(r.SeenAt), since: timeFromPgtype(r.ContinuousSince), expires: timeFromPgtype(r.ExpiresAt)})
	}
	return out
}
func writerCoverageHead(r sqlc.EventAcknowledgmentRecordingCoverage) eventAcknowledgmentCoverageHead {
	return eventAcknowledgmentCoverageHead{revision: r.InventoryRevision, since: timestamptzToTimePtr(r.ContinuousSince), through: timestamptzToTimePtr(r.VerifiedThrough), updated: timeFromPgtype(r.UpdatedAt)}
}
func (s *PgStore) HeartbeatEventAcknowledgmentWriter(ctx context.Context, r EventAcknowledgmentWriterRegistration) error {
	if err := ValidateEventAcknowledgmentWriterRegistration(r); err != nil {
		return err
	}
	_, inventory, revision, err := acknowledgmentWriterInventory(r.Inventory)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if err = q.EventAckWriterCoverageEnsure(ctx, tx); err != nil {
		return err
	}
	head, err := q.EventAckWriterCoverageLock(ctx, tx)
	if err != nil {
		return err
	}
	if err = q.EventAckWritersPrune(ctx, tx, sqlc.EventAckWritersPruneParams{RetentionSeconds: int32(api.EventAcknowledgmentWriterRetention / time.Second), BatchLimit: int32(api.EventAcknowledgmentSweepBatch)}); err != nil {
		return err
	}
	n, err := q.EventAckWriterBeat(ctx, tx, sqlc.EventAckWriterBeatParams{SessionID: mustPgUUID(r.SessionID), Role: r.Role, Slot: r.Slot, CapabilityVersion: r.CapabilityVersion, Inventory: inventory, InventoryRevision: revision, LeaseSeconds: int32(api.EventAcknowledgmentWriterLease / time.Second)})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	clock, err := q.EventAckNow(ctx, tx)
	if err != nil {
		return err
	}
	rows, err := q.EventAckWritersLive(ctx, tx, sqlc.EventAckWritersLiveParams{NowAt: clock, RowLimit: int32(api.EventAcknowledgmentWriterLiveMax + 1)})
	if err != nil {
		return err
	}
	c := bindAcknowledgmentCoverage(assessAcknowledgmentWriters(acknowledgmentWriterRows(rows), timeFromPgtype(clock)), writerCoverageHead(head), true)
	if err = q.EventAckWriterCoverageSet(ctx, tx, sqlc.EventAckWriterCoverageSetParams{InventoryRevision: c.InventoryRevision, ContinuousSince: nullableTimestamptzPtr(c.ContinuousSince), VerifiedThrough: nullableTimestamptzPtr(c.VerifiedThrough)}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *PgStore) acknowledgmentRecordingCoverage(ctx context.Context, tx pgx.Tx) (api.EventAcknowledgmentRecordingCoverage, error) {
	q := sqlc.New()
	clock, err := q.EventAckNow(ctx, tx)
	if err != nil {
		return api.EventAcknowledgmentRecordingCoverage{}, err
	}
	rows, err := q.EventAckWritersLive(ctx, tx, sqlc.EventAckWritersLiveParams{NowAt: clock, RowLimit: int32(api.EventAcknowledgmentWriterLiveMax + 1)})
	if err != nil {
		return api.EventAcknowledgmentRecordingCoverage{}, err
	}
	head, err := q.EventAckWriterCoverageGet(ctx, tx)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return api.EventAcknowledgmentRecordingCoverage{}, err
	}
	c := assessAcknowledgmentWriters(acknowledgmentWriterRows(rows), timeFromPgtype(clock))
	return bindAcknowledgmentCoverage(c, writerCoverageHead(head), false), nil
}
func (m *MemStore) HeartbeatEventAcknowledgmentWriter(ctx context.Context, r EventAcknowledgmentWriterRegistration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := ValidateEventAcknowledgmentWriterRegistration(r); err != nil {
		return err
	}
	members, _, revision, err := acknowledgmentWriterInventory(r.Inventory)
	if err != nil {
		return err
	}
	r.Inventory = members
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	if m.eventAcknowledgmentWriters == nil {
		m.eventAcknowledgmentWriters = map[string]eventAcknowledgmentWriter{}
	}
	r.SessionID = canonicalMemUUID(r.SessionID)
	old, found := m.eventAcknowledgmentWriters[r.SessionID]
	if found && (old.registration.Role != r.Role || old.registration.Slot != r.Slot) {
		return ErrConflict
	}
	row := eventAcknowledgmentWriter{registration: r, revision: revision, started: now, seen: now, since: now, expires: now.Add(api.EventAcknowledgmentWriterLease)}
	if found {
		row.started = old.started
		if old.expires.After(now) && old.revision == revision && old.registration.CapabilityVersion == r.CapabilityVersion {
			row.since = old.since
		}
	}
	m.eventAcknowledgmentWriters[r.SessionID] = row
	count := 0
	for key, item := range m.eventAcknowledgmentWriters {
		if item.seen.Before(now.Add(-api.EventAcknowledgmentWriterRetention)) {
			delete(m.eventAcknowledgmentWriters, key)
			count++
			if count >= api.EventAcknowledgmentSweepBatch {
				break
			}
		}
	}
	c := bindAcknowledgmentCoverage(assessAcknowledgmentWriters(m.liveAcknowledgmentWritersLocked(now), now), m.eventAcknowledgmentCoverageHead, true)
	m.eventAcknowledgmentCoverageHead = eventAcknowledgmentCoverageHead{revision: c.InventoryRevision, since: cloneEventReceiptTime(c.ContinuousSince), through: cloneEventReceiptTime(c.VerifiedThrough), updated: now}
	return nil
}
func (m *MemStore) liveAcknowledgmentWritersLocked(now time.Time) []eventAcknowledgmentWriter {
	rows := []eventAcknowledgmentWriter{}
	for _, r := range m.eventAcknowledgmentWriters {
		if r.expires.After(now) {
			rows = append(rows, r)
			if len(rows) > api.EventAcknowledgmentWriterLiveMax {
				break
			}
		}
	}
	return rows
}
