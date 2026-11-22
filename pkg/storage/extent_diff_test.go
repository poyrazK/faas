package storage

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// adr: 20261122132055 §4 — two clones' extent maps give the ranges a backup
// must read again: blocks moved, written or freed since the last backup.
func TestDiffExtents(t *testing.T) {
	x := func(logical, physical, length int64) mappedExtent { return mappedExtent{logical, physical, length} }
	for _, tc := range []struct {
		name      string
		prev, cur []mappedExtent
		want      []Range
	}{
		{"unchanged", []mappedExtent{x(0, 1000, 100)}, []mappedExtent{x(0, 1000, 100)}, nil},
		{"split the same way differently", []mappedExtent{x(0, 1000, 100)}, []mappedExtent{x(0, 1000, 40), x(40, 1040, 60)}, nil},
		{"rewritten middle", []mappedExtent{x(0, 1000, 100)}, []mappedExtent{x(0, 1000, 40), x(40, 9000, 20), x(60, 1060, 40)}, []Range{{40, 60}}},
		{"new data in a hole", []mappedExtent{x(0, 1000, 10)}, []mappedExtent{x(0, 1000, 10), x(50, 5000, 10)}, []Range{{50, 60}}},
		{"freed data", []mappedExtent{x(0, 1000, 10), x(50, 5000, 10)}, []mappedExtent{x(0, 1000, 10)}, []Range{{50, 60}}},
		{"unknown placement", []mappedExtent{x(0, 1000, 10)}, []mappedExtent{x(0, -1, 10)}, []Range{{0, 10}}},
		{"adjacent changes merge", []mappedExtent{x(0, 1000, 20)}, []mappedExtent{x(0, 7000, 10), x(10, 8000, 10)}, []Range{{0, 20}}},
		{"no previous backup", nil, []mappedExtent{x(10, 1000, 10), x(40, 2000, 5)}, []Range{{10, 20}, {40, 45}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := diffExtents(tc.prev, tc.cur); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("diffExtents = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCloneFileKeepsContentAndSize(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	data := bytes.Repeat([]byte("v"), 4096)
	f, err := os.Create(src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt(data, 1<<20); err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(4 << 20); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	dst := filepath.Join(dir, "dst")
	if err := os.WriteFile(dst, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := CloneFile(dst, src); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	want, _ := os.ReadFile(src)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("clone differs (%v)", err)
	}
	if _, err := ChangedRanges(src, dst); err != nil && !errors.Is(err, ErrExtentsUnsupported) {
		t.Fatalf("ChangedRanges: %v", err)
	}
}
