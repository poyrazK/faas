// Package auditanchor seals audit_log into a chain of anchors and verifies
// it (ADR-20261026163015). Daemons may only insert audit rows, but the
// schema owner can rewrite or erase them; an anchor records how many rows
// one window of received_at holds and a digest over them, and chains to
// the previous anchor by an HMAC whose key lives on the control plane host,
// never in the database. Rewriting a sealed row, erasing one, or adding one
// to a sealed window changes the window's digest; forging an anchor to
// match needs the key. Each anchor is also logged off-host, which covers
// the newest anchors being erased with their rows.
package auditanchor

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

const (
	// DefaultLag keeps a window open until rows stamped before it by a
	// writer that is slow to commit have landed.
	DefaultLag = 10 * time.Minute

	keyLabel    = "gregale-audit-anchor-key/v1"
	macLabel    = "gregale-audit-anchor/v1"
	rowsLabel   = "gregale-audit-rows/v1"
	pageSize    = 2000
	anchorsPage = 500
)

// Genesis is where the first anchor's window starts: before every row.
var Genesis = time.Unix(0, 0).UTC()

var (
	// ErrHeadTampered means the newest anchor does not verify under the
	// key, so sealing would extend a forged chain.
	ErrHeadTampered = errors.New("auditanchor: the newest anchor does not verify under this key")
	// ErrKeyChanged means the newest anchor was sealed under another key.
	ErrKeyChanged = errors.New("auditanchor: the newest anchor was sealed under another key")
	// ErrRaced means another anchorer sealed the same seq first.
	ErrRaced = errors.New("auditanchor: another anchorer sealed this seq first")
)

// DeriveKey derives the anchor MAC key from the apid audit HMAC key, so the
// key needs no backup or standby copy of its own.
func DeriveKey(auditHMACKey []byte) ([]byte, error) {
	if len(auditHMACKey) < 32 {
		return nil, fmt.Errorf("auditanchor: audit HMAC key is %d bytes, want 32", len(auditHMACKey))
	}
	m := hmac.New(sha256.New, auditHMACKey)
	m.Write([]byte(keyLabel))
	return m.Sum(nil), nil
}

// KeyID names a key without revealing it.
func KeyID(key []byte) string {
	sum := sha256.Sum256(key)
	return hex.EncodeToString(sum[:8])
}

// MAC is an anchor's MAC under key: every field but the MAC itself and the
// creation time, including the previous anchor's MAC.
func MAC(key []byte, a state.AuditLogAnchor) string {
	m := hmac.New(sha256.New, key)
	writeString(m, macLabel)
	writeInt(m, a.Seq)
	writeInt(m, a.WindowStart.UnixMicro())
	writeInt(m, a.WindowEnd.UnixMicro())
	writeInt(m, a.RowCount)
	writeString(m, a.RowsDigest)
	writeString(m, a.PrevMAC)
	writeString(m, a.KeyID)
	return hex.EncodeToString(m.Sum(nil))
}

// Store is what sealing and verifying read and write.
type Store interface {
	state.AuditLogAnchorStore
}

// DigestWindow counts and digests the audit rows received in [start, end),
// in (received_at, id) order. Each field is length-prefixed, received_at is
// taken to the microsecond PostgreSQL keeps, and data is the stored text.
func DigestWindow(ctx context.Context, store Store, start, end time.Time) (int64, string, error) {
	h := sha256.New()
	writeString(h, rowsLabel)
	var count int64
	var after *state.AuditLog
	for {
		page, err := store.AuditLogWindowPage(ctx, start, end, after, pageSize)
		if err != nil {
			return 0, "", err
		}
		for _, row := range page {
			writeRow(h, row)
		}
		count += int64(len(page))
		if len(page) < pageSize {
			return count, hex.EncodeToString(h.Sum(nil)), nil
		}
		last := page[len(page)-1]
		after = &last
	}
}

// SealOptions tune Seal.
type SealOptions struct {
	// Lag is how far behind now a window ends (DefaultLag when zero).
	Lag time.Duration
	// AcceptKeyChange extends a chain whose newest anchor was sealed under
	// another key, after the audit HMAC key was lost. Verification cannot
	// check the anchors sealed under the old key.
	AcceptKeyChange bool
}

// Seal appends the next anchor, covering the rows received from the end of
// the newest anchor (Genesis before the first) up to now less the lag,
// truncated to the minute. It returns false when that window is empty. It
// refuses to extend a chain whose newest anchor does not verify.
func Seal(ctx context.Context, store Store, key []byte, now time.Time, opts SealOptions) (state.AuditLogAnchor, bool, error) {
	lag := opts.Lag
	if lag <= 0 {
		lag = DefaultLag
	}
	keyID := KeyID(key)
	next := state.AuditLogAnchor{Seq: 1, WindowStart: Genesis, KeyID: keyID}
	head, ok, err := store.LatestAuditLogAnchor(ctx)
	if err != nil {
		return state.AuditLogAnchor{}, false, err
	}
	if ok {
		switch {
		case head.KeyID != keyID && !opts.AcceptKeyChange:
			return state.AuditLogAnchor{}, false, fmt.Errorf("%w: anchor %d has key %s, this key is %s", ErrKeyChanged, head.Seq, head.KeyID, keyID)
		case head.KeyID == keyID && !hmac.Equal([]byte(MAC(key, head)), []byte(head.MAC)):
			return state.AuditLogAnchor{}, false, fmt.Errorf("%w: anchor %d", ErrHeadTampered, head.Seq)
		}
		next.Seq, next.WindowStart, next.PrevMAC = head.Seq+1, head.WindowEnd, head.MAC
	}
	next.WindowEnd = now.Add(-lag).Truncate(time.Minute).UTC()
	if !next.WindowEnd.After(next.WindowStart) {
		return state.AuditLogAnchor{}, false, nil
	}
	if next.RowCount, next.RowsDigest, err = DigestWindow(ctx, store, next.WindowStart, next.WindowEnd); err != nil {
		return state.AuditLogAnchor{}, false, err
	}
	next.MAC = MAC(key, next)
	inserted, err := store.InsertAuditLogAnchor(ctx, next)
	if err != nil {
		return state.AuditLogAnchor{}, false, err
	}
	if !inserted {
		return state.AuditLogAnchor{}, false, fmt.Errorf("%w: seq %d", ErrRaced, next.Seq)
	}
	return next, true, nil
}

// Finding kinds.
const (
	// FindingRowsChanged: the window's rows no longer match the anchor (a
	// row rewritten, erased, or added with a received_at inside it).
	FindingRowsChanged = "rows_changed"
	// FindingMACMismatch: the anchor itself was altered or forged.
	FindingMACMismatch = "mac_mismatch"
	// FindingChainBroken: the anchor does not chain to the one before it.
	FindingChainBroken = "chain_broken"
	// FindingMissingAnchor: anchors are missing between two seqs.
	FindingMissingAnchor = "missing_anchor"
	// FindingWindowGap: the anchor's window does not start where the
	// previous one ended, so rows in between are unsealed.
	FindingWindowGap = "window_gap"
	// FindingKeyUnknown: the anchor was sealed under another key; its MAC
	// cannot be checked here.
	FindingKeyUnknown = "key_unknown"
)

// Finding is one anchor that does not verify.
type Finding struct {
	Seq    int64  `json:"seq"`
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

// Report is the outcome of Verify. LastSeq and LastMAC are what the
// off-host log should show as the newest anchor.
type Report struct {
	FromSeq  int64     `json:"from_seq"`
	LastSeq  int64     `json:"last_seq"`
	LastMAC  string    `json:"last_mac"`
	Anchors  int       `json:"anchors"`
	Rows     int64     `json:"rows"`
	Findings []Finding `json:"findings"`
}

// Verify recomputes every anchor from seq fromSeq on (from the first when
// fromSeq < 1): its MAC, its link to the anchor before it, and its window's
// rows.
func Verify(ctx context.Context, store Store, key []byte, fromSeq int64) (Report, error) {
	fromSeq = max(fromSeq, 1)
	report := Report{FromSeq: fromSeq, Findings: []Finding{}}
	keyID := KeyID(key)
	var prev *state.AuditLogAnchor
	if fromSeq > 1 {
		before, err := store.ListAuditLogAnchors(ctx, fromSeq-1, 1)
		if err != nil {
			return report, err
		}
		if len(before) == 1 && before[0].Seq == fromSeq-1 {
			prev = &before[0]
		}
	}
	for cursor := fromSeq; ; {
		page, err := store.ListAuditLogAnchors(ctx, cursor, anchorsPage)
		if err != nil {
			return report, err
		}
		for _, a := range page {
			report.Findings = append(report.Findings, checkLink(prev, a)...)
			if a.KeyID != keyID {
				report.Findings = append(report.Findings, Finding{a.Seq, FindingKeyUnknown, "sealed under key " + a.KeyID + ", this key is " + keyID})
			} else if !hmac.Equal([]byte(MAC(key, a)), []byte(a.MAC)) {
				report.Findings = append(report.Findings, Finding{a.Seq, FindingMACMismatch, "the anchor's fields do not match its MAC"})
			}
			count, digest, err := DigestWindow(ctx, store, a.WindowStart, a.WindowEnd)
			if err != nil {
				return report, err
			}
			if count != a.RowCount || digest != a.RowsDigest {
				report.Findings = append(report.Findings, Finding{a.Seq, FindingRowsChanged,
					fmt.Sprintf("window %s to %s sealed %d rows, now holds %d with another digest",
						a.WindowStart.Format(time.RFC3339), a.WindowEnd.Format(time.RFC3339), a.RowCount, count)})
			}
			report.Anchors++
			report.Rows += count
			report.LastSeq, report.LastMAC = a.Seq, a.MAC
			anchor := a
			prev = &anchor
		}
		if len(page) < anchorsPage {
			return report, nil
		}
		cursor = page[len(page)-1].Seq + 1
	}
}

func checkLink(prev *state.AuditLogAnchor, a state.AuditLogAnchor) []Finding {
	if prev == nil {
		if a.Seq == 1 && !a.WindowStart.Equal(Genesis) {
			return []Finding{{a.Seq, FindingWindowGap, "the first window does not start at " + Genesis.Format(time.RFC3339)}}
		}
		return nil
	}
	var out []Finding
	if a.Seq != prev.Seq+1 {
		out = append(out, Finding{a.Seq, FindingMissingAnchor, fmt.Sprintf("anchors %d to %d are missing", prev.Seq+1, a.Seq-1)})
	}
	if a.PrevMAC != prev.MAC {
		out = append(out, Finding{a.Seq, FindingChainBroken, fmt.Sprintf("does not chain to anchor %d", prev.Seq)})
	}
	if !a.WindowStart.Equal(prev.WindowEnd) {
		out = append(out, Finding{a.Seq, FindingWindowGap, fmt.Sprintf("starts at %s, anchor %d ended at %s",
			a.WindowStart.Format(time.RFC3339), prev.Seq, prev.WindowEnd.Format(time.RFC3339))})
	}
	return out
}

func writeRow(h hash.Hash, row state.AuditLog) {
	h.Write(row.ID[:])
	writeString(h, row.Kind)
	account := ""
	if row.AccountID != nil {
		account = row.AccountID.String()
	}
	writeString(h, account)
	writeString(h, row.AccountEmail)
	writeString(h, row.Actor)
	writeInt(h, row.ReceivedAt.UnixMicro())
	writeString(h, string(row.Data))
}

func writeString(h hash.Hash, s string) {
	writeInt(h, int64(len(s)))
	h.Write([]byte(s))
}

func writeInt(h hash.Hash, v int64) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(v))
	h.Write(buf[:])
}
