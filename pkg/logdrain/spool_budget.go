package logdrain

import "sync/atomic"

// DefaultNodeSpoolBudgetBytes bounds the pending bytes of every drain queue
// one manager holds open. Each queue also keeps its own MaxBytes, but a
// node with many drains (one Scale account may hold 100) could otherwise
// fill its disk at 64 MiB per drain.
const DefaultNodeSpoolBudgetBytes = 2 << 30

// SpoolBudget is a byte budget shared by the queues of one node. It is a
// soft bound: concurrent enqueues into different queues may overshoot it by
// the size of the lines in flight.
type SpoolBudget struct {
	max  int64
	used atomic.Int64
}

// NewSpoolBudget returns a budget of max bytes; max <= 0 uses the default.
func NewSpoolBudget(max int64) *SpoolBudget {
	if max <= 0 {
		max = DefaultNodeSpoolBudgetBytes
	}
	return &SpoolBudget{max: max}
}

// Used reports the pending bytes charged to the budget.
func (b *SpoolBudget) Used() int64 { return b.used.Load() }

func (b *SpoolBudget) fits(n int64) bool { return b.used.Load()+n <= b.max }

func (b *SpoolBudget) add(n int64) { b.used.Add(n) }
