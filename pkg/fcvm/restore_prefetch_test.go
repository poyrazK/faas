package fcvm

import (
	"fmt"
	"reflect"
	"testing"
)

// adr: 224 — every capture of one deployment shares a prefetch family, so a
// new capture's first wake reuses the previous capture's working set.
func TestSnapshotPrefetchFamily(t *testing.T) {
	for _, tc := range []struct{ key, want string }{
		{"snap/dep-1/captures/cap-a/v2/mem", "snap/dep-1"},
		{"snap/dep-1/captures/cap-b/v2/mem", "snap/dep-1"},
		{"snap/restore-bench/mem", "snap/restore-bench/mem"},
		{"/srv/fc/snap/legacy/mem", "/srv/fc/snap/legacy/mem"},
		{"", ""},
	} {
		if got := snapshotPrefetchFamily(tc.key); got != tc.want {
			t.Errorf("snapshotPrefetchFamily(%q) = %q, want %q", tc.key, got, tc.want)
		}
	}
}

// adr: 224 — a recorded set is sorted, merged across small gaps, bounded in
// range count by widening the gap, and truncated at the byte cap.
func TestCoalesceFileRanges(t *testing.T) {
	const page = 4096
	for _, tc := range []struct {
		name      string
		in        []fileRange
		gap       int64
		maxRanges int
		maxBytes  int64
		want      []fileRange
		wantBytes int64
	}{
		{
			name: "adjacent and overlapping merge, unsorted input",
			in:   []fileRange{{8 * page, page}, {0, page}, {page, page}, {page, 2 * page}},
			gap:  0, maxRanges: 16, maxBytes: 1 << 30,
			want:      []fileRange{{0, 3 * page}, {8 * page, page}},
			wantBytes: 4 * page,
		},
		{
			name: "gap below threshold merges",
			in:   []fileRange{{0, page}, {3 * page, page}},
			gap:  2 * page, maxRanges: 16, maxBytes: 1 << 30,
			want:      []fileRange{{0, 4 * page}},
			wantBytes: 4 * page,
		},
		{
			name: "range cap widens the gap",
			in:   []fileRange{{0, page}, {10 * page, page}, {20 * page, page}},
			gap:  page, maxRanges: 1, maxBytes: 1 << 30,
			want:      []fileRange{{0, 21 * page}},
			wantBytes: 21 * page,
		},
		{
			name: "byte cap truncates",
			in:   []fileRange{{0, 4 * page}, {100 * page, 4 * page}},
			gap:  0, maxRanges: 16, maxBytes: 5 * page,
			want:      []fileRange{{0, 4 * page}, {100 * page, page}},
			wantBytes: 5 * page,
		},
		{
			name: "empty and zero-length input",
			in:   []fileRange{{0, 0}},
			gap:  page, maxRanges: 16, maxBytes: 1 << 30,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := coalesceFileRanges(tc.in, tc.gap, tc.maxRanges, tc.maxBytes)
			if !reflect.DeepEqual(got.ranges, tc.want) || got.bytes != tc.wantBytes {
				t.Fatalf("got %v (%d bytes), want %v (%d bytes)", got.ranges, got.bytes, tc.want, tc.wantBytes)
			}
		})
	}
}

// adr: 224 — the store is bounded and evicts the oldest family first.
func TestRestorePrefetchStoreBounded(t *testing.T) {
	s := newRestorePrefetchStore()
	set := restorePrefetchSet{ranges: []fileRange{{0, 4096}}, bytes: 4096}
	for i := 0; i < restorePrefetchMaxFamilies+5; i++ {
		s.put(fmt.Sprintf("snap/dep-%d", i), set)
	}
	if len(s.sets) != restorePrefetchMaxFamilies {
		t.Fatalf("store holds %d families, want %d", len(s.sets), restorePrefetchMaxFamilies)
	}
	if _, ok := s.get("snap/dep-0"); ok {
		t.Fatal("oldest family was not evicted")
	}
	if _, ok := s.get(fmt.Sprintf("snap/dep-%d", restorePrefetchMaxFamilies+4)); !ok {
		t.Fatal("newest family missing")
	}
	s.put("snap/empty", restorePrefetchSet{})
	if _, ok := s.get("snap/empty"); ok {
		t.Fatal("an empty set must not be stored")
	}
}

// adr: 224 — prefetch is a no-op without a recorded set or when disabled.
func TestPrefetchRestoreNoopWithoutSet(t *testing.T) {
	v := NewJailerVMM(t.TempDir(), 0)
	if got := v.PrefetchRestore("snap/dep/captures/c/v2/mem"); got != 0 {
		t.Fatalf("PrefetchRestore without a set = %d, want 0", got)
	}
	v.WithRestorePrefetch(false)
	v.recordRestoreWorkingSet("inst", "snap/dep/captures/c/v2/mem", "/nonexistent")
	if got := v.PrefetchRestore("snap/dep/captures/c/v2/mem"); got != 0 {
		t.Fatalf("disabled PrefetchRestore = %d, want 0", got)
	}
}
