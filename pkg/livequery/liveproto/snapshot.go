package liveproto

import (
	"fmt"
	"strconv"
	"strings"
)

// Snapshot is a parsed pg_snapshot ("xmin:xmax:xip,..."), in full 64-bit
// transaction IDs. Every live result is the query's result at one snapshot.
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
		return Snapshot{}, fmt.Errorf("liveproto: parse snapshot %q: want xmin:xmax:xip", s)
	}
	xmin, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return Snapshot{}, fmt.Errorf("liveproto: parse snapshot xmin %q: %w", s, err)
	}
	xmax, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		return Snapshot{}, fmt.Errorf("liveproto: parse snapshot xmax %q: %w", s, err)
	}
	if xmin > xmax {
		return Snapshot{}, fmt.Errorf("liveproto: parse snapshot %q: xmin after xmax", s)
	}
	snap := Snapshot{Xmin: xmin, Xmax: xmax, Xip: map[uint64]struct{}{}, text: s}
	if parts[2] != "" {
		for _, raw := range strings.Split(parts[2], ",") {
			xid, err := strconv.ParseUint(raw, 10, 64)
			if err != nil {
				return Snapshot{}, fmt.Errorf("liveproto: parse snapshot xip %q: %w", s, err)
			}
			if xid < xmin || xid >= xmax {
				return Snapshot{}, fmt.Errorf("liveproto: parse snapshot %q: xip %d outside [xmin, xmax)", s, xid)
			}
			snap.Xip[xid] = struct{}{}
		}
	}
	return snap, nil
}

// String returns the snapshot in pg_snapshot text form.
func (s Snapshot) String() string { return s.text }

// Visible reports whether a committed transaction is visible in the snapshot,
// following pg_visible_in_snapshot. A client's awaitTxId resolves once its
// result's snapshot makes the transaction visible.
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

// Covers reports whether every transaction visible in o is visible in s, so
// that s is not older than o. Results only move forward.
func (s Snapshot) Covers(o Snapshot) bool {
	for xid := range s.Xip {
		if o.Visible(xid) {
			return false
		}
	}
	if s.Xmax >= o.Xmax {
		return true
	}
	// s sees nothing in [s.Xmax, o.Xmax), so o must have all of it in
	// progress; that needs at least as many xip entries as the gap is wide.
	if o.Xmax-s.Xmax > uint64(len(o.Xip)) {
		return false
	}
	for xid := s.Xmax; xid < o.Xmax; xid++ {
		if o.Visible(xid) {
			return false
		}
	}
	return true
}
