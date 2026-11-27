// Package dbqueryinsights computes bounded changes in native PostgreSQL
// statement counters. Reports identify statements by query ID.
package dbqueryinsights

import (
	"math"
	"strconv"
	"time"
)

const (
	MaxEntries = 256
	Interval   = 2 * time.Minute
	Retention  = 24 * time.Hour
)

type Counter struct {
	QueryID     string  `json:"query_id"`
	UserID      uint32  `json:"user_id"`
	Calls       int64   `json:"calls"`
	ExecutionMS float64 `json:"execution_ms"`
}

type Sample struct {
	At        time.Time `json:"at"`
	BootAt    time.Time `json:"boot_at"`
	ResetAt   time.Time `json:"reset_at"`
	Evictions int64     `json:"evictions"`
	Counters  []Counter `json:"counters"`
}

type counterKey struct {
	query string
	user  uint32
}

func (s Sample) Valid() bool {
	if s.At.IsZero() || s.BootAt.IsZero() || s.BootAt.After(s.At) || s.ResetAt.After(s.At) ||
		s.Evictions < 0 || len(s.Counters) > MaxEntries {
		return false
	}
	keys := make(map[counterKey]bool, len(s.Counters))
	for _, c := range s.Counters {
		if id, err := strconv.ParseInt(c.QueryID, 10, 64); err != nil || id == 0 || strconv.FormatInt(id, 10) != c.QueryID ||
			c.UserID == 0 || c.Calls < 0 || c.ExecutionMS < 0 || math.IsNaN(c.ExecutionMS) || math.IsInf(c.ExecutionMS, 0) {
			return false
		}
		key := counterKey{c.QueryID, c.UserID}
		if keys[key] {
			return false
		}
		keys[key] = true
	}
	return true
}

// Changes returns only increments within a continuous observation window.
// Eviction can recreate a key with larger counters and hide a reset, so a
// change in the eviction count invalidates that window.
func Changes(previous, current Sample) ([]Counter, bool) {
	gap := current.At.Sub(previous.At)
	if gap <= 0 || gap > 3*Interval || !previous.BootAt.Equal(current.BootAt) ||
		!previous.ResetAt.Equal(current.ResetAt) || previous.Evictions != current.Evictions {
		return nil, false
	}
	prior := make(map[counterKey]Counter, len(previous.Counters))
	for _, row := range previous.Counters {
		prior[counterKey{row.QueryID, row.UserID}] = row
	}
	out := make([]Counter, 0, len(current.Counters))
	for _, row := range current.Counters {
		before := prior[counterKey{row.QueryID, row.UserID}]
		if row.Calls < before.Calls || row.ExecutionMS < before.ExecutionMS {
			return nil, false
		}
		row.Calls -= before.Calls
		row.ExecutionMS -= before.ExecutionMS
		if row.Calls > 0 {
			out = append(out, row)
		}
	}
	return out, true
}
