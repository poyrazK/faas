package auditanchor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// fakeStore keeps audit rows and anchors in slices a test can rewrite, as
// the schema owner could.
type fakeStore struct {
	rows    []state.AuditLog
	anchors []state.AuditLogAnchor
}

func (f *fakeStore) LatestAuditLogAnchor(context.Context) (state.AuditLogAnchor, bool, error) {
	if len(f.anchors) == 0 {
		return state.AuditLogAnchor{}, false, nil
	}
	return f.anchors[len(f.anchors)-1], true, nil
}

func (f *fakeStore) ListAuditLogAnchors(_ context.Context, fromSeq int64, limit int) ([]state.AuditLogAnchor, error) {
	var out []state.AuditLogAnchor
	for _, a := range f.anchors {
		if a.Seq >= fromSeq && len(out) < limit {
			out = append(out, a)
		}
	}
	return out, nil
}

func (f *fakeStore) FirstAuditLogAnchorEndingAfter(_ context.Context, after time.Time) (int64, bool, error) {
	for _, a := range f.anchors {
		if a.WindowEnd.After(after) {
			return a.Seq, true, nil
		}
	}
	return 0, false, nil
}

func (f *fakeStore) InsertAuditLogAnchor(_ context.Context, a state.AuditLogAnchor) (bool, error) {
	for _, have := range f.anchors {
		if have.Seq == a.Seq {
			return false, nil
		}
	}
	f.anchors = append(f.anchors, a)
	slices.SortFunc(f.anchors, func(x, y state.AuditLogAnchor) int { return int(x.Seq - y.Seq) })
	return true, nil
}

func (f *fakeStore) AuditLogWindowPage(_ context.Context, start, end time.Time, after *state.AuditLog, limit int) ([]state.AuditLog, error) {
	order := func(a, b state.AuditLog) int {
		if c := a.ReceivedAt.Compare(b.ReceivedAt); c != 0 {
			return c
		}
		return bytes.Compare(a.ID[:], b.ID[:])
	}
	var out []state.AuditLog
	for _, row := range f.rows {
		if row.ReceivedAt.Before(start) || !row.ReceivedAt.Before(end) || (after != nil && order(row, *after) <= 0) {
			continue
		}
		out = append(out, row)
	}
	slices.SortFunc(out, order)
	return out[:min(limit, len(out))], nil
}

func (f *fakeStore) add(at time.Time, kind string) {
	f.rows = append(f.rows, state.AuditLog{ID: uuid.New(), Kind: kind, Actor: "apid", ReceivedAt: at.UTC(),
		Data: json.RawMessage(fmt.Sprintf(`{"n":%d}`, len(f.rows)))})
}

var (
	testKey = mustKey(bytes.Repeat([]byte{7}, 32))
	t0      = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
)

func mustKey(auditKey []byte) []byte {
	key, err := DeriveKey(auditKey)
	if err != nil {
		panic(err)
	}
	return key
}

// sealedStore holds rows over two sealed windows (anchors 1 and 2) and one
// row too recent to seal.
func sealedStore(t *testing.T) *fakeStore {
	t.Helper()
	f := &fakeStore{}
	for i := range 5 {
		f.add(t0.Add(time.Duration(i)*time.Minute), "account.login")
	}
	seal(t, f, t0.Add(30*time.Minute))
	for i := range 3 {
		f.add(t0.Add(40*time.Minute+time.Duration(i)*time.Second), "org.member.removed")
	}
	seal(t, f, t0.Add(time.Hour))
	f.add(t0.Add(55*time.Minute), "account.login")
	return f
}

func seal(t *testing.T, f *fakeStore, now time.Time) state.AuditLogAnchor {
	t.Helper()
	a, ok, err := Seal(t.Context(), f, testKey, now, SealOptions{})
	if err != nil || !ok {
		t.Fatalf("seal at %s = %v, %v", now, ok, err)
	}
	return a
}

// adr: 20261026163015 — anchors chain from Genesis, each sealing the rows
// received up to now less the lag, and a clean chain verifies.
func TestSealChainsWindowsAndVerifies(t *testing.T) {
	f := sealedStore(t)
	first, second := f.anchors[0], f.anchors[1]
	if first.Seq != 1 || !first.WindowStart.Equal(Genesis) || !first.WindowEnd.Equal(t0.Add(20*time.Minute)) || first.RowCount != 5 || first.PrevMAC != "" {
		t.Fatalf("first anchor = %+v", first)
	}
	if second.Seq != 2 || !second.WindowStart.Equal(first.WindowEnd) || !second.WindowEnd.Equal(t0.Add(50*time.Minute)) ||
		second.RowCount != 3 || second.PrevMAC != first.MAC || second.KeyID != KeyID(testKey) {
		t.Fatalf("second anchor = %+v", second)
	}
	if _, ok, err := Seal(t.Context(), f, testKey, t0.Add(time.Hour), SealOptions{}); ok || err != nil {
		t.Fatalf("sealing an empty window = %v, %v; want nothing sealed", ok, err)
	}
	report, err := Verify(t.Context(), f, testKey, 0)
	if err != nil || len(report.Findings) != 0 || report.Anchors != 2 || report.Rows != 8 || report.LastSeq != 2 || report.LastMAC != second.MAC {
		t.Fatalf("verify = %+v, %v", report, err)
	}
	// The row too recent for anchor 2 is sealed by the next anchor.
	if third := seal(t, f, t0.Add(2*time.Hour)); third.RowCount != 1 {
		t.Fatalf("third anchor sealed %d rows, want the late one", third.RowCount)
	}
}

func TestVerifyFindsTampering(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tamper func(t *testing.T, f *fakeStore)
		seq    int64
		kinds  []string
	}{
		{"row rewritten", func(_ *testing.T, f *fakeStore) { f.rows[1].Data = json.RawMessage(`{"n":"forged"}`) }, 1, []string{FindingRowsChanged}},
		{"row actor rewritten", func(_ *testing.T, f *fakeStore) { f.rows[6].Actor = "someone-else" }, 2, []string{FindingRowsChanged}},
		{"row erased", func(_ *testing.T, f *fakeStore) { f.rows = slices.Delete(f.rows, 2, 3) }, 1, []string{FindingRowsChanged}},
		{"row added to a sealed window", func(_ *testing.T, f *fakeStore) { f.add(t0.Add(3*time.Minute), "account.login") }, 1, []string{FindingRowsChanged}},
		{"row moved out of its window", func(_ *testing.T, f *fakeStore) { f.rows[0].ReceivedAt = t0.Add(-time.Hour) }, 1, []string{FindingRowsChanged}},
		{"anchor count rewritten", func(_ *testing.T, f *fakeStore) { f.anchors[0].RowCount = 4 }, 1, []string{FindingMACMismatch, FindingRowsChanged}},
		{"anchor and rows rewritten together", func(_ *testing.T, f *fakeStore) {
			f.rows = slices.Delete(f.rows, 0, 1)
			f.anchors[0].RowCount, f.anchors[0].RowsDigest = recount(f, f.anchors[0])
		}, 1, []string{FindingMACMismatch}},
		{"middle anchor erased", func(t *testing.T, f *fakeStore) {
			seal(t, f, t0.Add(2*time.Hour))
			f.anchors = slices.Delete(f.anchors, 1, 2)
		}, 3, []string{FindingMissingAnchor, FindingChainBroken, FindingWindowGap}},
		{"window narrowed", func(_ *testing.T, f *fakeStore) { f.anchors[1].WindowStart = f.anchors[1].WindowStart.Add(time.Minute) }, 2, []string{FindingWindowGap, FindingMACMismatch}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := sealedStore(t)
			tc.tamper(t, f)
			report, err := Verify(t.Context(), f, testKey, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, kind := range tc.kinds {
				if !slices.ContainsFunc(report.Findings, func(x Finding) bool { return x.Seq == tc.seq && x.Kind == kind }) {
					t.Errorf("findings = %+v, want %s on anchor %d", report.Findings, kind, tc.seq)
				}
			}
		})
	}
}

func recount(f *fakeStore, a state.AuditLogAnchor) (int64, string) {
	count, digest, err := DigestWindow(context.Background(), f, a.WindowStart, a.WindowEnd)
	if err != nil {
		panic(err)
	}
	return count, digest
}

// Sealing never extends a chain whose head does not verify, and a lost
// key needs an explicit acceptance.
func TestSealRefusesAForgedHeadOrAnotherKey(t *testing.T) {
	f := sealedStore(t)
	f.anchors[1].RowCount++
	if _, _, err := Seal(t.Context(), f, testKey, t0.Add(2*time.Hour), SealOptions{}); !errors.Is(err, ErrHeadTampered) {
		t.Fatalf("seal on a forged head = %v, want ErrHeadTampered", err)
	}
	f = sealedStore(t)
	other := mustKey(bytes.Repeat([]byte{9}, 32))
	if _, _, err := Seal(t.Context(), f, other, t0.Add(2*time.Hour), SealOptions{}); !errors.Is(err, ErrKeyChanged) {
		t.Fatalf("seal under another key = %v, want ErrKeyChanged", err)
	}
	third, ok, err := Seal(t.Context(), f, other, t0.Add(2*time.Hour), SealOptions{AcceptKeyChange: true})
	if err != nil || !ok || third.PrevMAC != f.anchors[1].MAC {
		t.Fatalf("seal accepting the key change = %+v, %v, %v", third, ok, err)
	}
	report, err := Verify(t.Context(), f, other, 3)
	if err != nil || len(report.Findings) != 0 || report.Anchors != 1 {
		t.Fatalf("verify from the key change = %+v, %v", report, err)
	}
	report, err = Verify(t.Context(), f, other, 0)
	if err != nil || len(report.Findings) != 2 || report.Findings[0].Kind != FindingKeyUnknown {
		t.Fatalf("verify across the key change = %+v, %v; want the old anchors reported as key_unknown", report, err)
	}
}

// Windows larger than a page digest every row once, in order.
func TestDigestWindowPages(t *testing.T) {
	f := &fakeStore{}
	for i := range 2*pageSize + 500 {
		f.add(t0.Add(time.Duration(i)*time.Millisecond), "account.login")
	}
	a := seal(t, f, t0.Add(time.Hour))
	if a.RowCount != int64(len(f.rows)) {
		t.Fatalf("sealed %d rows, want %d", a.RowCount, len(f.rows))
	}
	f.rows[2*pageSize+100].Kind = "forged"
	report, err := Verify(t.Context(), f, testKey, 0)
	if err != nil || len(report.Findings) != 1 || report.Findings[0].Kind != FindingRowsChanged {
		t.Fatalf("verify after rewriting a row on the third page = %+v, %v", report, err)
	}
}

func TestDeriveKey(t *testing.T) {
	if _, err := DeriveKey(make([]byte, 16)); err == nil {
		t.Fatal("a 16-byte audit key was accepted")
	}
	a, b := mustKey(bytes.Repeat([]byte{1}, 32)), mustKey(bytes.Repeat([]byte{2}, 32))
	if bytes.Equal(a, b) || bytes.Equal(a, bytes.Repeat([]byte{1}, 32)) || len(KeyID(a)) != 16 || KeyID(a) == KeyID(b) {
		t.Fatalf("derived keys are not distinct from each other and the audit key")
	}
}
