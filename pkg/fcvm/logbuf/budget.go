package logbuf

import "sync"

// Budget bounds the line bytes every ring of one vmmd retains together. The
// rings live in vmmd's heap, charged to the shared control-plane slice, not
// to the tenants' RAM: 360 live instances that log steadily held about 4 GiB
// in the root daemon with only the per-ring cap (bug hunt H47). Each ring
// keeps at most its share, the budget split across live rings but never
// below the floor, and evicts its own oldest lines (to the archive spool) to
// stay under it. A ring that is not written keeps what it has until its next
// line or its close.
type Budget struct {
	max, floor int64

	mu    sync.Mutex
	rings int
	used  int64
}

// NewBudget returns a budget of max bytes across rings, each allowed at
// least floor.
func NewBudget(max, floor int64) *Budget {
	return &Budget{max: max, floor: floor}
}

// share is one ring's current byte allowance.
func (b *Budget) share() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.rings <= 1 {
		return max(b.max, b.floor)
	}
	return max(b.max/int64(b.rings), b.floor)
}

func (b *Budget) join() {
	b.mu.Lock()
	b.rings++
	b.mu.Unlock()
}

// leave releases a closed ring and the bytes it still held.
func (b *Budget) leave(bytes int) {
	b.mu.Lock()
	b.rings--
	b.used -= int64(bytes)
	b.mu.Unlock()
}

func (b *Budget) charge(delta int) {
	b.mu.Lock()
	b.used += int64(delta)
	b.mu.Unlock()
}

// Used is the line bytes all rings hold now, for the vmmd_log_ring_bytes
// gauge.
func (b *Budget) Used() int64 {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.used
}

// Rings is the number of live rings sharing the budget.
func (b *Budget) Rings() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.rings
}
