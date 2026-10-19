package genslot_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/genslot"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// moveQueue stands in for a queue's merge: it appends from's queue file to
// into's and removes it.
func moveQueue(t *testing.T, from, into string) error {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(from, "queue"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(into, "queue"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Remove(filepath.Join(from, "queue"))
}

func TestOverlappingGenerationsGetSeparateSlots(t *testing.T) {
	root := t.TempDir()
	first, err := genslot.Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := genslot.Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	if first.Index() != 0 || first.Dir() != root {
		t.Fatalf("first generation = slot %d at %s, want slot 0 at the root", first.Index(), first.Dir())
	}
	if second.Index() != 1 || second.Dir() == root {
		t.Fatalf("second generation = slot %d at %s, want its own slot", second.Index(), second.Dir())
	}
	if info, err := os.Stat(second.Dir()); err != nil || !info.IsDir() {
		t.Fatalf("slot directory not created: %v", err)
	}
	var held []*genslot.Slot
	for range genslot.MaxGenerations - 2 {
		s, err := genslot.Acquire(root)
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, s)
	}
	if _, err := genslot.Acquire(root); err == nil {
		t.Fatal("acquired a slot beyond MaxGenerations")
	}
	_ = first.Release()
	again, err := genslot.Acquire(root)
	if err != nil || again.Index() != 0 {
		t.Fatalf("after slot 0 was released got %v, %v; want slot 0 again", again, err)
	}
	for _, s := range append(held, second, again) {
		_ = s.Release()
	}
}

func TestAdoptOrphansTakesOnlyExitedGenerations(t *testing.T) {
	root := t.TempDir()
	old, err := genslot.Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	next, err := genslot.Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = next.Release() }()
	write(t, filepath.Join(old.Dir(), "queue"), "old\n")

	var seen []string
	absorb := func(dir string) error {
		seen = append(seen, dir)
		return moveQueue(t, dir, next.Dir())
	}
	if err := next.AdoptOrphans(absorb); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 0 {
		t.Fatalf("adopted %v while the old generation still runs", seen)
	}
	_ = old.Release() // the old generation exits
	if err := next.AdoptOrphans(absorb); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(seen, []string{root}) {
		t.Fatalf("adopted %v, want the root slot", seen)
	}
	if got := read(t, filepath.Join(next.Dir(), "queue")); got != "old\n" {
		t.Fatalf("adopted queue = %q", got)
	}
	// The root slot is free again for the next generation.
	s, err := genslot.Acquire(root)
	if err != nil || s.Index() != 0 {
		t.Fatalf("got %v, %v; want slot 0", s, err)
	}
	_ = s.Release()
}

func TestAdoptOrphansRemovesAnAdoptedSlotDirectory(t *testing.T) {
	root := t.TempDir()
	home, _ := genslot.Acquire(root)
	defer func() { _ = home.Release() }()
	crashed, _ := genslot.Acquire(root)
	write(t, filepath.Join(crashed.Dir(), "queue"), "crashed\n")
	_ = crashed.Release()
	if err := home.AdoptOrphans(func(dir string) error { return moveQueue(t, dir, home.Dir()) }); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(root, "queue")); got != "crashed\n" {
		t.Fatalf("queue = %q", got)
	}
	if _, err := os.Stat(crashed.Dir()); !os.IsNotExist(err) {
		t.Fatalf("adopted slot directory still exists: %v", err)
	}
}

func TestReturnHomeMovesRecordsIntoTheRootSlot(t *testing.T) {
	root := t.TempDir()
	old, _ := genslot.Acquire(root)
	next, _ := genslot.Acquire(root)
	write(t, filepath.Join(next.Dir(), "queue"), "next\n")
	merge := func(from, home string) error { return moveQueue(t, from, home) }

	// The old generation still holds slot 0: the records stay put.
	if err := next.ReturnHome(150*time.Millisecond, merge); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(next.Dir(), "queue")); got != "next\n" {
		t.Fatalf("records moved while slot 0 was held: %q", got)
	}
	// A later generation adopts them once slot 0 is free.
	_ = old.Release()
	later, _ := genslot.Acquire(root)
	if later.Index() != 0 {
		t.Fatalf("later generation in slot %d", later.Index())
	}
	_ = later.Release()

	again, _ := genslot.Acquire(root) // slot 0
	stray, _ := genslot.Acquire(root) // slot 1 holds "next"
	if stray.Index() != 1 {
		t.Fatalf("stray in slot %d", stray.Index())
	}
	_ = again.Release()
	if err := stray.ReturnHome(time.Second, merge); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(root, "queue")); got != "next\n" {
		t.Fatalf("home queue = %q", got)
	}
	if _, err := os.Stat(stray.Dir()); !os.IsNotExist(err) {
		t.Fatalf("slot directory left after returning home: %v", err)
	}
}
