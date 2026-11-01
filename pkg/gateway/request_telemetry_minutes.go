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
}

func newRequestTelemetryMinutes() *requestTelemetryMinutes {
	return &requestTelemetryMinutes{index: make(map[string]int)}
}

// add folds row into its open bucket. An exact row never folds: it is ready
// at once, as it is on the per-batch path.
func (m *requestTelemetryMinutes) add(row RequestTelemetryRow) {
	row, key := requestTelemetryBucketed(row)
	if key == "" {
		m.ready = append(m.ready, row)
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
}

func (m *requestTelemetryMinutes) closeBefore(cutoff time.Time) {
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

// pendingByApp counts the requests held open or waiting to ship, per app.
func (m *requestTelemetryMinutes) pendingByApp() map[uuid.UUID]int {
	counts := make(map[uuid.UUID]int)
	for _, rows := range [][]RequestTelemetryRow{m.rows, m.ready} {
		for _, row := range rows {
			counts[row.AppID] += normalizedRequestTelemetryCount(row.Count)
		}
	}
	return counts
}
