package livequery

import "github.com/onebox-faas/faas/pkg/livequery/liveproto"

// Snapshot is a parsed pg_snapshot; the protocol package owns it because
// clients evaluate snapshots too.
type Snapshot = liveproto.Snapshot

// ParseSnapshot parses the text form of pg_current_snapshot().
func ParseSnapshot(s string) (Snapshot, error) { return liveproto.ParseSnapshot(s) }

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
