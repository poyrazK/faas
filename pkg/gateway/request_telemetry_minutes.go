// adr: 20261101085552
package gateway

import (
	"time"

	"github.com/google/uuid"
)

// requestTelemetryMinutes holds collapsed request telemetry until its minute
// closes, so a busy bucket ships once per minute instead of once per drained
// batch. The per-batch collapse wrote the same minute about once a second at
// 290 req/s (ADR-20261101085552). Only the publisher goroutine touches it.
type requestTelemetryMinutes struct {
	keys  []string
	rows  []RequestTelemetryRow
	index map[string]int
	// ready holds closed and exact rows in shipping order.
	ready []RequestTelemetryRow
	// exact counts the exact samples each open bucket has shipped
	// (ADR-127 amendment 2026-10-31); a bucket's count goes when its minute
	// closes.
	exact map[string]exactSamples
}

type exactSamples struct {
	n      int
	minute time.Time
}

func newRequestTelemetryMinutes() *requestTelemetryMinutes {
	return &requestTelemetryMinutes{index: make(map[string]int), exact: make(map[string]exactSamples)}
}

// add folds row into its open bucket. An exact row never folds: it is ready
// at once, as it is on the per-batch path. So are the first
// requestTelemetryExactPerBucket exact samples of a bucket's minute.
func (m *requestTelemetryMinutes) add(original RequestTelemetryRow) {
	row, key := requestTelemetryBucketed(original)
	if key == "" {
		m.ready = append(m.ready, row)
		return
	}
	if sampled := m.exact[key]; original.exactSample && sampled.n < requestTelemetryExactPerBucket {
		m.exact[key] = exactSamples{n: sampled.n + 1, minute: row.ReceivedAt}
		m.ready = append(m.ready, exactTelemetryRow(original, row))
		return
	}
	if idx, ok := m.index[key]; ok {
		mergeRequestTelemetry(&m.rows[idx], row)
		return
	}
	m.index[key] = len(m.rows)
	m.keys = append(m.keys, key)
	m.rows = append(m.rows, row)
}

// close readies every bucket whose minute ended at least grace before now.
// Past maxOpen buckets it readies the oldest open minutes as well.
func (m *requestTelemetryMinutes) close(now time.Time, grace time.Duration, maxOpen int) {
	m.closeBefore(now.Add(-grace).Truncate(time.Minute))
	for len(m.rows) > maxOpen {
		oldest := m.rows[0].ReceivedAt
		for _, row := range m.rows[1:] {
			if row.ReceivedAt.Before(oldest) {
				oldest = row.ReceivedAt
			}
		}
		m.closeBefore(oldest.Add(time.Minute))
	}
}

// closeAll readies every open bucket, for the final flush.
func (m *requestTelemetryMinutes) closeAll() {
	m.ready = append(m.ready, m.rows...)
	m.keys, m.rows = nil, nil
	clear(m.index)
	clear(m.exact)
}

func (m *requestTelemetryMinutes) closeBefore(cutoff time.Time) {
	for key, sampled := range m.exact {
		if sampled.minute.Before(cutoff) {
			delete(m.exact, key)
		}
	}
	keys, rows := m.keys[:0], m.rows[:0]
	clear(m.index)
	for i, row := range m.rows {
		if row.ReceivedAt.Before(cutoff) {
			m.ready = append(m.ready, row)
			continue
		}
		m.index[m.keys[i]] = len(rows)
		keys = append(keys, m.keys[i])
		rows = append(rows, row)
	}
	m.keys, m.rows = keys, rows
}

// takeReady removes and returns up to n ready rows, oldest first.
func (m *requestTelemetryMinutes) takeReady(n int) []RequestTelemetryRow {
	if n > len(m.ready) {
		n = len(m.ready)
	}
	if n == 0 {
		return nil
	}
	out := append([]RequestTelemetryRow(nil), m.ready[:n]...)
	m.ready = m.ready[n:]
	if len(m.ready) == 0 {
		m.ready = nil // release the drained backing array
	}
	return out
}

// trimReady removes the oldest ready rows past max and returns them, so a
// long delivery outage cannot grow the gateway's memory without bound.
func (m *requestTelemetryMinutes) trimReady(max int) []RequestTelemetryRow {
	if len(m.ready) <= max {
		return nil
	}
	excess := len(m.ready) - max
	dropped := append([]RequestTelemetryRow(nil), m.ready[:excess]...)
	m.ready = m.ready[excess:]
	return dropped
}

// empty reports whether nothing is open or waiting to ship.
func (m *requestTelemetryMinutes) empty() bool {
	return len(m.rows) == 0 && len(m.ready) == 0
}

// takeAll removes and returns every open and ready row, for loss accounting
// on shutdown.
func (m *requestTelemetryMinutes) takeAll() []RequestTelemetryRow {
	m.closeAll()
	return m.takeReady(len(m.ready))
}

// undeliveredByApp counts the requests in closed minutes that have not
// shipped yet, per app. Requests still folding into a minute that has not
// passed its grace are not pending: no consumer reads that minute yet (route
// evidence waits api.RouteHealthIngestionLag, route removal two minutes), and
// counting them kept every app with traffic permanently "pending", which
// blocked route removal for good (ADR-20261028110044 amendment 4).
func (m *requestTelemetryMinutes) undeliveredByApp() map[uuid.UUID]int {
	return m.undeliveredWhere(func(RequestTelemetryRow) bool { return true })
}

// undeliveredWhere is undeliveredByApp over the rows keep accepts.
func (m *requestTelemetryMinutes) undeliveredWhere(keep func(RequestTelemetryRow) bool) map[uuid.UUID]int {
	counts := make(map[uuid.UUID]int)
	for _, row := range m.ready {
		if keep(row) {
			counts[row.AppID] += normalizedRequestTelemetryCount(row.Count)
		}
	}
	return counts
}
