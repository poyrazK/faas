// adr:567
package copycontents

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

func rowsDigest(t *testing.T, fields, memory int, parts ...string) string {
	t.Helper()
	var key [32]byte
	key[0] = 19
	dir := t.TempDir()
	s, e := newDigestSorter(t.Context(), dir, memory, 1<<20, key, []byte("private-relation"))
	if e != nil {
		t.Fatal(e)
	}
	w := newRowWriter(s, fields, 1<<20, key, []byte("private-relation"))
	for _, part := range parts {
		if _, e = w.Write([]byte(part)); e != nil {
			t.Fatal(e)
		}
	}
	d, e := w.Finish()
	s.Close()
	if e != nil {
		t.Fatal(e)
	}
	entries, e := os.ReadDir(dir)
	if e != nil || len(entries) != 0 {
		t.Fatal("digest spools leaked", e)
	}
	return d
}
func TestContentsSortedRowsPreserveDuplicatesAndIgnoreOrder(t *testing.T) {
	if a, b := rowsDigest(t, 2, 32, "a\t1\nb\t2\na\t1\n"), rowsDigest(t, 2, 128, "b\t2\na\t1\na\t1\n"); a != b {
		t.Fatal("multiset depends on order/merge layout")
	}
	if a, b := rowsDigest(t, 1, 32, "a\na\nb\n"), rowsDigest(t, 1, 32, "a\nb\nb\n"); a == b {
		t.Fatal("duplicate multiplicity lost")
	}
	if a, b := rowsDigest(t, 1, 32, "a\na\n"), rowsDigest(t, 1, 32, ""); a == b {
		t.Fatal("duplicates cancel")
	}
	if rowsDigest(t, 0, 32, "\n\n") == rowsDigest(t, 0, 32, "\n") {
		t.Fatal("zero-column row count lost")
	}
}
func TestContentsEscapedRowsAreUnambiguousAcrossEveryChunkBoundary(t *testing.T) {
	raw := "\\N\t\t\\\\N\nsecret\\nline\\tvalue\té\\\\quoted\tend\n"
	expected := rowsDigest(t, 3, 64, raw)
	for i := 0; i <= len(raw); i++ {
		if actual := rowsDigest(t, 3, 32, raw[:i], raw[i:]); actual != expected {
			t.Fatal("chunk framing changed data", i)
		}
	}
	if rowsDigest(t, 1, 32, "\\N\n") == rowsDigest(t, 1, 32, "\n") {
		t.Fatal("NULL equals empty")
	}
	if rowsDigest(t, 1, 32, "\\\\N\n") == rowsDigest(t, 1, 32, "\\N\n") {
		t.Fatal("literal NULL marker equals NULL")
	}
	if rowsDigest(t, 2, 32, "ab\tc\n") == rowsDigest(t, 2, 32, "a\tbc\n") {
		t.Fatal("field boundaries ambiguous")
	}
}
func TestContentsRowsRejectIncompleteMalformedAndOverBudgetStreams(t *testing.T) {
	for _, raw := range []string{"unfinished", "bad\\", "bad\\xescape\n", "bad\rline\n", "too\tmany\n"} {
		var key [32]byte
		key[0] = 7
		s, e := newDigestSorter(t.Context(), t.TempDir(), 32, 1<<20, key, nil)
		if e != nil {
			t.Fatal(e)
		}
		w := newRowWriter(s, 1, 1024, key, nil)
		_, e = w.Write([]byte(raw))
		if e == nil {
			_, e = w.Finish()
		}
		s.Close()
		if !errors.Is(e, pgerrors.ErrConflict) {
			t.Fatal("malformed COPY accepted", e)
		}
	}
	var key [32]byte
	key[0] = 7
	s, _ := newDigestSorter(t.Context(), t.TempDir(), 32, 1<<20, key, nil)
	defer s.Close()
	w := newRowWriter(s, 1, 1, key, nil)
	if _, e := w.Write([]byte("a\n")); !errors.Is(e, pgerrors.ErrQuotaExceeded) {
		t.Fatal("byte budget ignored", e)
	}
}
func TestContentsSortBoundsAndCancellationLeaveNoSpools(t *testing.T) {
	for _, mode := range []string{"disk", "cancelled", "bad_parent"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			dir := t.TempDir()
			parent := dir
			disk := int64(64)
			if mode == "bad_parent" {
				parent = dir + "/missing"
			}
			var key [32]byte
			key[0] = 19
			s, e := newDigestSorter(ctx, parent, 32, disk, key, nil)
			if mode == "bad_parent" {
				if !errors.Is(e, pgerrors.ErrUnavailable) {
					t.Fatal(e)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			if mode == "cancelled" {
				cancel()
			}
			for i := 0; i < 10 && e == nil; i++ {
				e = s.Add(sha256.Sum256([]byte(strings.Repeat("private-payload", i))))
			}
			s.Close()
			want := pgerrors.ErrQuotaExceeded
			if mode == "cancelled" {
				want = context.Canceled
			}
			if !errors.Is(e, want) {
				t.Fatal("sort bound ignored", e)
			}
			entries, e := os.ReadDir(dir)
			if e != nil || len(entries) != 0 {
				t.Fatal("failed sort leaked spools", e)
			}
		})
	}
}
