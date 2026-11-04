package logbuf

import (
	"strings"
	"testing"
)

// Rings that share a budget each keep about their share of it, so the node
// total stays bounded however many instances log steadily; a lone ring keeps
// its own cap, and a closed ring releases what it held (bug hunt H47).
func TestRingsShareTheNodeBudget(t *testing.T) {
	const total, floor = 64 << 10, 4 << 10
	b := NewBudget(total, floor)
	line := strings.Repeat("x", 99) + "\n"
	rings := make([]*Ring, 8)
	for i := range rings {
		rings[i] = New(1 << 20)
		rings[i].SetBudget(b)
	}
	for range 2000 {
		for _, r := range rings {
			if _, err := r.Write([]byte(line)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if used := b.Used(); used > total+int64(len(rings))*100 {
		t.Fatalf("rings hold %d bytes, want at most about %d", used, total)
	}
	share := int64(total / len(rings))
	for i, r := range rings {
		r.mu.Lock()
		held := r.totalBytes
		r.mu.Unlock()
		if int64(held) > share || int64(held) < share-200 {
			t.Fatalf("ring %d holds %d bytes, want its share %d", i, held, share)
		}
	}
	for _, r := range rings[1:] {
		_ = r.Close()
	}
	if b.Rings() != 1 {
		t.Fatalf("live rings = %d, want 1", b.Rings())
	}
	// Alone again, the remaining ring may grow back toward the whole budget.
	for range 2000 {
		_, _ = rings[0].Write([]byte(line))
	}
	if used := b.Used(); used <= share || used > total {
		t.Fatalf("lone ring holds %d bytes, want more than its old share %d and at most %d", used, share, total)
	}
	_ = rings[0].Close()
	if b.Used() != 0 || b.Rings() != 0 {
		t.Fatalf("after closing every ring: used=%d rings=%d", b.Used(), b.Rings())
	}
}

// The floor holds however many rings share the budget.
func TestRingBudgetFloor(t *testing.T) {
	b := NewBudget(1<<10, 2<<10)
	for range 10 {
		New(1 << 20).SetBudget(b)
	}
	if got := b.share(); got != 2<<10 {
		t.Fatalf("share = %d, want the floor %d", got, 2<<10)
	}
}
