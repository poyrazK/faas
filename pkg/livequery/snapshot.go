package livequery

import (
	"fmt"
	"strconv"
	"strings"
)

// Snapshot is a parsed pg_snapshot ("xmin:xmax:xip,..."), in full 64-bit
// transaction IDs.
type Snapshot struct {
	Xmin uint64
	Xmax uint64
	Xip  map[uint64]struct{}
	text string
}

// ParseSnapshot parses the text form of pg_current_snapshot().
func ParseSnapshot(s string) (Snapshot, error) {
	parts := strings.Split(s, ":")
	if len(parts) != 3 {
		return Snapshot{}, fmt.Errorf("livequery: parse snapshot %q: want xmin:xmax:xip", s)
	}
	xmin, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return Snapshot{}, fmt.Errorf("livequery: parse snapshot xmin %q: %w", s, err)
	}
	xmax, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		return Snapshot{}, fmt.Errorf("livequery: parse snapshot xmax %q: %w", s, err)
	}
	snap := Snapshot{Xmin: xmin, Xmax: xmax, Xip: map[uint64]struct{}{}, text: s}
	if parts[2] != "" {
		for _, raw := range strings.Split(parts[2], ",") {
			xid, err := strconv.ParseUint(raw, 10, 64)
			if err != nil {
				return Snapshot{}, fmt.Errorf("livequery: parse snapshot xip %q: %w", s, err)
			}
			snap.Xip[xid] = struct{}{}
		}
	}
	return snap, nil
}

// String returns the snapshot in pg_snapshot text form.
func (s Snapshot) String() string { return s.text }

// Visible reports whether a committed transaction is visible in the snapshot,
// following pg_visible_in_snapshot.
func (s Snapshot) Visible(xid uint64) bool {
	if xid < s.Xmin {
		return true
	}
	if xid >= s.Xmax {
		return false
	}
	_, inProgress := s.Xip[xid]
	return !inProgress
}

// FullXid extends a 32-bit xid from the replication stream to a 64-bit xid8,
// given a reference xid8 that is not older than the transaction. The xid is
// assumed to lie within 2^31 transactions before the reference, which
// PostgreSQL's wraparound protection guarantees for live transactions.
func FullXid(xid uint32, reference uint64) uint64 {
	full := (reference &^ 0xffffffff) | uint64(xid)
	if full > reference && full >= 1<<32 {
		full -= 1 << 32
	}
	return full
}
