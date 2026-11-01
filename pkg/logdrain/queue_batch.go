package logdrain

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"strings"
	"time"
)

// lastSequenceTTL is how long a drain remembers an instance's delivered
// cursor after its last delivered line. Instance IDs change on every wake,
// so without pruning the cursor file grew by one entry per instance forever
// and was rewritten in full on every acknowledgement.
const lastSequenceTTL = 30 * 24 * time.Hour

// NextBatch returns up to max consecutive unacknowledged records from the
// head without moving the cursor. It stops before the records would pass
// maxBytes, but always returns at least the head record. The first item
// carries the head's persisted attempt count.
func (q *Queue) NextBatch(max int, maxBytes int64) ([]QueueItem, error) {
	if max <= 0 {
		max = 1
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.pendingRecords == 0 {
		return nil, nil
	}
	file, err := os.Open(q.recordsPath) //nolint:forbidigo // recordsPath is a server-owned queue file under the validated per-drain spool root, never a customer path.
	if err != nil {
		return nil, fmt.Errorf("log drain: open durable queue head: %w", err)
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Seek(q.state.Offset, io.SeekStart); err != nil {
		return nil, fmt.Errorf("log drain: seek durable queue head: %w", err)
	}
	reader := bufio.NewReader(file)
	offset := q.state.Offset
	var items []QueueItem
	var size int64
	for len(items) < max {
		line, err := reader.ReadString('\n')
		if err != nil {
			// A record still being appended has no newline yet; it is the
			// next batch's head.
			if len(items) == 0 {
				return nil, fmt.Errorf("log drain: read durable queue head: %w", ErrQueueData)
			}
			break
		}
		if len(items) > 0 && size+int64(len(line)) > maxBytes {
			break
		}
		var record persistedRecord
		if err := json.Unmarshal([]byte(strings.TrimSuffix(line, "\n")), &record); err != nil {
			return nil, fmt.Errorf("log drain: decode durable queue record: %w", ErrQueueData)
		}
		item := QueueItem{Record: record.Record, EnqueuedAt: record.EnqueuedAt, offset: offset, nextOffset: offset + int64(len(line))}
		if len(items) == 0 {
			item.Attempts = q.state.Attempts
		}
		items = append(items, item)
		size += int64(len(line))
		offset = item.nextOffset
	}
	return items, nil
}

// AckBatch advances the cursor past items, which must be the consecutive
// head records NextBatch returned, with one cursor write for the batch.
func (q *Queue) AckBatch(items []QueueItem) error {
	if len(items) == 0 {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.advancePastLocked(items, time.Now())
}

// advancePastLocked commits the cursor past items before changing the
// in-memory head or backlog. A failed disk write leaves them retryable.
func (q *Queue) advancePastLocked(items []QueueItem, now time.Time) error {
	if items[0].offset != q.state.Offset {
		return fmt.Errorf("log drain: acknowledge stale item: %w", ErrQueueData)
	}
	for i, item := range items {
		if item.nextOffset <= item.offset || (i > 0 && item.offset != items[i-1].nextOffset) {
			return fmt.Errorf("log drain: acknowledge non-consecutive items: %w", ErrQueueData)
		}
	}
	last := items[len(items)-1]
	next := queueState{Offset: last.nextOffset, LastSequences: maps.Clone(q.state.LastSequences), LastSeen: maps.Clone(q.state.LastSeen)}
	if next.LastSequences == nil {
		next.LastSequences = make(map[string]uint64)
	}
	if next.LastSeen == nil {
		next.LastSeen = make(map[string]int64)
	}
	for _, item := range items {
		instance := item.Record.InstanceID
		if instance == "" {
			continue
		}
		if item.Record.Sequence > next.LastSequences[instance] {
			next.LastSequences[instance] = item.Record.Sequence
		}
		next.LastSeen[instance] = now.Unix()
	}
	pruneLastSequences(&next, now)
	if err := q.writeStateLocked(next); err != nil {
		return err
	}
	q.pendingRecords -= len(items)
	q.pendingBytes -= last.nextOffset - items[0].offset
	for _, item := range items {
		q.deletePendingKey(item.Record)
	}
	if q.pendingRecords <= 0 {
		q.pendingRecords, q.pendingBytes, q.oldestPending = 0, 0, time.Time{}
	} else {
		q.oldestPending = q.nextEnqueuedAtLocked()
	}
	return q.compactIfNeededLocked()
}

// pruneLastSequences drops the cursors of instances that delivered nothing
// for lastSequenceTTL. A cursor from a state file written before LastSeen
// existed counts as seen now, so it ages out like any other.
func pruneLastSequences(state *queueState, now time.Time) {
	cutoff := now.Add(-lastSequenceTTL).Unix()
	for instance := range state.LastSequences {
		seen, ok := state.LastSeen[instance]
		if !ok {
			state.LastSeen[instance] = now.Unix()
			continue
		}
		if seen < cutoff {
			delete(state.LastSequences, instance)
			delete(state.LastSeen, instance)
		}
	}
	for instance := range state.LastSeen {
		if _, ok := state.LastSequences[instance]; !ok {
			delete(state.LastSeen, instance)
		}
	}
}
