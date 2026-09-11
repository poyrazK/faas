package logdrain

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// The outbox is intentionally local to gatewayd-internal. Runtime logs are
// already produced on the node and a local fsync keeps the stream producer
// independent from Postgres and from the customer's endpoint.
const (
	DefaultSpoolRoot          = "/var/lib/faas/log-drains"
	DefaultSpoolMaxBytes      = 64 * 1024 * 1024
	DefaultDeadLetterMaxBytes = 8 * 1024 * 1024
	maxQueueIDLen             = 128
)

var (
	// ErrQueueFull means the durable outbox has reached its byte budget.
	ErrQueueFull = errors.New("log drain: durable queue is full")
	// ErrQueueData means the cursor or an outbox record is not recoverable.
	ErrQueueData = errors.New("log drain: durable queue data is corrupt")
)

// QueueConfig controls one drain's durable outbox.
type QueueConfig struct {
	Root               string
	DrainID            string
	MaxBytes           int64
	DeadLetterMaxBytes int64
}

// QueueStats is a point-in-time view of the durable outbox. PendingBytes is
// the number of bytes that have not been acknowledged. The oldest timestamp
// is zero when the outbox is empty.
type QueueStats struct {
	PendingRecords  int
	PendingBytes    int64
	CapacityBytes   int64
	DeadLetterTotal int64
	OldestPendingAt time.Time
}

// QueueItem is the head of a Queue. Items must be acknowledged in order.
// Keeping the byte offsets private to the package prevents callers from
// accidentally advancing the cursor past an unprocessed record.
type QueueItem struct {
	Record     Record
	EnqueuedAt time.Time
	Attempts   int

	offset     int64
	nextOffset int64
}

type persistedRecord struct {
	Record     Record    `json:"record"`
	EnqueuedAt time.Time `json:"enqueued_at"`
}

type deadLetter struct {
	Record     Record    `json:"record"`
	EnqueuedAt time.Time `json:"enqueued_at"`
	FailedAt   time.Time `json:"failed_at"`
	Attempts   int       `json:"attempts"`
	Error      string    `json:"error"`
}

type queueState struct {
	Offset        int64             `json:"offset"`
	Attempts      int               `json:"attempts"`
	LastSequences map[string]uint64 `json:"last_sequences,omitempty"`
}

// Queue is a single-consumer, multi-producer durable outbox. records.jsonl
// is append-only and cursor.json advances only after a successful delivery.
// A process restart therefore resumes at the first unacknowledged record.
type Queue struct {
	mu sync.Mutex

	recordsPath string
	cursorPath  string
	deadPath    string
	maxBytes    int64
	deadMax     int64

	state          queueState
	pendingRecords int
	pendingBytes   int64
	oldestPending  time.Time
	deadLetters    int64
	deadBytes      int64
	pendingKeys    map[queueKey]struct{}
}

type queueKey struct {
	instance string
	sequence uint64
}

// NewQueue opens or creates one durable outbox for DrainID.
func NewQueue(cfg QueueConfig) (*Queue, error) {
	if cfg.Root == "" {
		return nil, errors.New("log drain: durable queue root is empty")
	}
	if cfg.DrainID == "" || len(cfg.DrainID) > maxQueueIDLen || strings.ContainsAny(cfg.DrainID, `/\\\x00`) {
		return nil, errors.New("log drain: durable queue id is invalid")
	}
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = DefaultSpoolMaxBytes
	}
	if cfg.DeadLetterMaxBytes <= 0 {
		cfg.DeadLetterMaxBytes = DefaultDeadLetterMaxBytes
	}
	dir := filepath.Join(cfg.Root, cfg.DrainID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("log drain: create durable queue: %w", err)
	}
	q := &Queue{
		recordsPath: filepath.Join(dir, "records.jsonl"),
		cursorPath:  filepath.Join(dir, "cursor.json"),
		deadPath:    filepath.Join(dir, "dead-letters.jsonl"),
		maxBytes:    cfg.MaxBytes,
		deadMax:     cfg.DeadLetterMaxBytes,
		pendingKeys: make(map[queueKey]struct{}),
		state:       queueState{LastSequences: make(map[string]uint64)},
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := q.loadStateLocked(); err != nil {
		return nil, err
	}
	if err := q.recoverCompactionLocked(); err != nil {
		return nil, err
	}
	if err := q.rebuildStatsLocked(); err != nil {
		return nil, err
	}
	return q, nil
}

func (q *Queue) recoverCompactionLocked() error {
	oldPath := q.recordsPath + ".old"
	compactPath := q.recordsPath + ".compact"
	_, oldErr := os.Stat(oldPath)
	currentInfo, currentErr := os.Stat(q.recordsPath)
	if oldErr == nil {
		if currentErr != nil && errors.Is(currentErr, os.ErrNotExist) {
			if err := os.Rename(oldPath, q.recordsPath); err != nil {
				return fmt.Errorf("log drain: recover durable queue: %w", err)
			}
		} else if currentErr == nil {
			if err := os.Remove(oldPath); err != nil {
				return fmt.Errorf("log drain: remove compacted queue backup: %w", err)
			}
			if q.state.Offset > currentInfo.Size() {
				q.state.Offset = 0
				q.state.Attempts = 0
				if err := q.writeStateLocked(); err != nil {
					return err
				}
			}
		} else {
			return fmt.Errorf("log drain: inspect durable queue recovery: %w", currentErr)
		}
	} else if !errors.Is(oldErr, os.ErrNotExist) {
		return fmt.Errorf("log drain: inspect durable queue backup: %w", oldErr)
	}
	if err := os.Remove(compactPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("log drain: remove interrupted queue compaction: %w", err)
	}
	return nil
}

func (q *Queue) loadStateLocked() error {
	b, err := os.ReadFile(q.cursorPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("log drain: read durable queue cursor: %w", err)
	}
	if err := json.Unmarshal(b, &q.state); err != nil || q.state.Offset < 0 || q.state.Attempts < 0 {
		return fmt.Errorf("log drain: read durable queue cursor: %w", ErrQueueData)
	}
	if q.state.LastSequences == nil {
		q.state.LastSequences = make(map[string]uint64)
	}
	return nil
}

// rebuildStatsLocked also repairs a torn final append. Every complete record
// is newline-terminated; a partial final line can only be an interrupted
// write and is safe to discard because it was never durably enqueued.
func (q *Queue) rebuildStatsLocked() error {
	info, err := os.Stat(q.recordsPath)
	if errors.Is(err, os.ErrNotExist) {
		q.state = queueState{LastSequences: q.state.LastSequences}
		return q.writeStateLocked()
	}
	if err != nil {
		return fmt.Errorf("log drain: stat durable queue: %w", err)
	}
	if q.state.Offset > info.Size() {
		if info.Size() != 0 {
			return fmt.Errorf("log drain: cursor exceeds durable queue: %w", ErrQueueData)
		}
		q.state = queueState{LastSequences: q.state.LastSequences}
	}
	if q.state.Offset == info.Size() && info.Size() > 0 {
		if err := os.Truncate(q.recordsPath, 0); err != nil {
			return fmt.Errorf("log drain: compact durable queue: %w", err)
		}
		q.state = queueState{LastSequences: q.state.LastSequences}
		if err := q.writeStateLocked(); err != nil {
			return err
		}
		info = nil
	}
	if info == nil {
		q.pendingRecords, q.pendingBytes, q.oldestPending = 0, 0, time.Time{}
		q.pendingKeys = make(map[queueKey]struct{})
	} else {
		if err := q.scanRecordsLocked(); err != nil {
			return err
		}
	}
	deadInfo, err := os.Stat(q.deadPath)
	if errors.Is(err, os.ErrNotExist) {
		q.deadLetters, q.deadBytes = 0, 0
	} else if err != nil {
		return fmt.Errorf("log drain: stat dead-letter queue: %w", err)
	} else {
		q.deadBytes = deadInfo.Size()
		q.deadLetters, err = countLines(q.deadPath)
		if err != nil {
			return fmt.Errorf("log drain: count dead-letter queue: %w", err)
		}
	}
	return nil
}

func (q *Queue) scanRecordsLocked() error {
	file, err := os.Open(q.recordsPath) //nolint:forbidigo // recordsPath is a server-owned queue file under the validated per-drain spool root, never a customer path.
	if err != nil {
		return fmt.Errorf("log drain: open durable queue: %w", err)
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Seek(q.state.Offset, io.SeekStart); err != nil {
		return fmt.Errorf("log drain: seek durable queue: %w", err)
	}
	reader := bufio.NewReader(file)
	offset := q.state.Offset
	for {
		line, readErr := reader.ReadString('\n')
		if len(line) > 0 {
			if readErr == io.EOF {
				if err := os.Truncate(q.recordsPath, offset); err != nil {
					return fmt.Errorf("log drain: repair durable queue: %w", err)
				}
				break
			}
			var record persistedRecord
			if err := json.Unmarshal([]byte(strings.TrimSuffix(line, "\n")), &record); err != nil {
				return fmt.Errorf("log drain: decode durable queue: %w", ErrQueueData)
			}
			if q.pendingRecords == 0 {
				q.oldestPending = record.EnqueuedAt
			}
			q.pendingRecords++
			q.pendingBytes += int64(len(line))
			q.addPendingKey(record.Record)
			offset += int64(len(line))
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("log drain: scan durable queue: %w", readErr)
		}
	}
	return nil
}

// Enqueue durably records one log line before returning. It is safe for
// concurrent producers. ErrQueueFull is the only expected capacity error.
func (q *Queue) Enqueue(record Record, enqueuedAt time.Time) error {
	if enqueuedAt.IsZero() {
		enqueuedAt = time.Now().UTC()
	}
	payload, err := json.Marshal(persistedRecord{Record: record, EnqueuedAt: enqueuedAt.UTC()})
	if err != nil {
		return fmt.Errorf("log drain: encode durable queue record: %w", err)
	}
	payload = append(payload, '\n')

	q.mu.Lock()
	defer q.mu.Unlock()
	if record.InstanceID != "" && record.Sequence > 0 {
		key := queueKey{instance: record.InstanceID, sequence: record.Sequence}
		if record.Sequence <= q.state.LastSequences[record.InstanceID] {
			return nil
		}
		if _, exists := q.pendingKeys[key]; exists {
			return nil
		}
	}
	if q.pendingBytes+int64(len(payload)) > q.maxBytes {
		return ErrQueueFull
	}
	file, err := os.OpenFile(q.recordsPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("log drain: open durable queue for append: %w", err)
	}
	if err := writeAll(file, payload); err != nil {
		_ = file.Close()
		return fmt.Errorf("log drain: append durable queue: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("log drain: sync durable queue: %w", err)
	}
	closeErr := file.Close()
	if closeErr != nil {
		return fmt.Errorf("log drain: close durable queue: %w", closeErr)
	}
	if q.pendingRecords == 0 {
		q.oldestPending = enqueuedAt.UTC()
	}
	q.pendingRecords++
	q.pendingBytes += int64(len(payload))
	q.addPendingKey(record)
	return nil
}

// Next returns the first unacknowledged record without changing the cursor.
func (q *Queue) Next() (QueueItem, bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.pendingRecords == 0 {
		return QueueItem{}, false, nil
	}
	file, err := os.Open(q.recordsPath) //nolint:forbidigo // recordsPath is a server-owned queue file under the validated per-drain spool root, never a customer path.
	if err != nil {
		return QueueItem{}, false, fmt.Errorf("log drain: open durable queue head: %w", err)
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Seek(q.state.Offset, io.SeekStart); err != nil {
		return QueueItem{}, false, fmt.Errorf("log drain: seek durable queue head: %w", err)
	}
	line, err := bufio.NewReader(file).ReadString('\n')
	if err != nil {
		return QueueItem{}, false, fmt.Errorf("log drain: read durable queue head: %w", ErrQueueData)
	}
	var record persistedRecord
	if err := json.Unmarshal([]byte(strings.TrimSuffix(line, "\n")), &record); err != nil {
		return QueueItem{}, false, fmt.Errorf("log drain: decode durable queue head: %w", ErrQueueData)
	}
	return QueueItem{
		Record: record.Record, EnqueuedAt: record.EnqueuedAt,
		Attempts: q.state.Attempts, offset: q.state.Offset,
		nextOffset: q.state.Offset + int64(len(line)),
	}, true, nil
}

// MarkAttempt persists the attempt number for the current head. If the
// daemon restarts after a failed request, retries continue from this count.
func (q *Queue) MarkAttempt(item QueueItem, attempts int) error {
	if attempts <= 0 {
		return errors.New("log drain: attempt must be positive")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if item.offset != q.state.Offset {
		return fmt.Errorf("log drain: mark attempt for stale item: %w", ErrQueueData)
	}
	q.state.Attempts = attempts
	return q.writeStateLocked()
}

// Ack advances the cursor only after the endpoint has returned a 2xx status.
func (q *Queue) Ack(item QueueItem) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if item.offset != q.state.Offset || item.nextOffset <= item.offset {
		return fmt.Errorf("log drain: acknowledge stale item: %w", ErrQueueData)
	}
	q.state.Offset = item.nextOffset
	q.state.Attempts = 0
	if item.Record.InstanceID != "" && item.Record.Sequence > q.state.LastSequences[item.Record.InstanceID] {
		q.state.LastSequences[item.Record.InstanceID] = item.Record.Sequence
	}
	if err := q.writeStateLocked(); err != nil {
		return err
	}
	q.pendingRecords--
	q.pendingBytes -= item.nextOffset - item.offset
	q.deletePendingKey(item.Record)
	if q.pendingRecords == 0 {
		if err := os.Truncate(q.recordsPath, 0); err != nil {
			return fmt.Errorf("log drain: compact acknowledged queue: %w", err)
		}
		q.state = queueState{LastSequences: q.state.LastSequences}
		q.pendingBytes, q.oldestPending = 0, time.Time{}
		return q.writeStateLocked()
	}
	q.oldestPending = q.nextEnqueuedAtLocked()
	return q.compactIfNeededLocked()
}

// DeadLetter durably preserves a record after retry exhaustion, then removes
// it from the active outbox. The dead-letter file is bounded by retaining the
// newest entries when its byte budget is reached.
func (q *Queue) DeadLetter(item QueueItem, attempts int, deliveryErr error) error {
	if attempts <= 0 {
		attempts = item.Attempts
	}
	message := "delivery failed"
	if deliveryErr != nil {
		message = deliveryErr.Error()
	}
	payload, err := json.Marshal(deadLetter{
		Record: item.Record, EnqueuedAt: item.EnqueuedAt, FailedAt: time.Now().UTC(),
		Attempts: attempts, Error: message,
	})
	if err != nil {
		return fmt.Errorf("log drain: encode dead letter: %w", err)
	}
	payload = append(payload, '\n')

	q.mu.Lock()
	defer q.mu.Unlock()
	if item.offset != q.state.Offset {
		return fmt.Errorf("log drain: dead-letter stale item: %w", ErrQueueData)
	}
	if q.deadBytes > 0 && q.deadBytes+int64(len(payload)) > q.deadMax {
		if err := os.Truncate(q.deadPath, 0); err != nil {
			return fmt.Errorf("log drain: rotate dead-letter queue: %w", err)
		}
		q.deadBytes, q.deadLetters = 0, 0
	}
	file, err := os.OpenFile(q.deadPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("log drain: open dead-letter queue: %w", err)
	}
	if err := writeAll(file, payload); err != nil {
		_ = file.Close()
		return fmt.Errorf("log drain: append dead-letter queue: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("log drain: sync dead-letter queue: %w", err)
	}
	closeErr := file.Close()
	if closeErr != nil {
		return fmt.Errorf("log drain: close dead-letter queue: %w", closeErr)
	}
	q.deadBytes += int64(len(payload))
	q.deadLetters++

	q.state.Offset = item.nextOffset
	q.state.Attempts = 0
	if item.Record.InstanceID != "" && item.Record.Sequence > q.state.LastSequences[item.Record.InstanceID] {
		q.state.LastSequences[item.Record.InstanceID] = item.Record.Sequence
	}
	if err := q.writeStateLocked(); err != nil {
		return err
	}
	q.pendingRecords--
	q.pendingBytes -= item.nextOffset - item.offset
	q.deletePendingKey(item.Record)
	if q.pendingRecords == 0 {
		if err := os.Truncate(q.recordsPath, 0); err != nil {
			return fmt.Errorf("log drain: compact dead-lettered queue: %w", err)
		}
		q.state = queueState{LastSequences: q.state.LastSequences}
		q.pendingBytes, q.oldestPending = 0, time.Time{}
		return q.writeStateLocked()
	}
	q.oldestPending = q.nextEnqueuedAtLocked()
	return q.compactIfNeededLocked()
}

const queueCompactOffset = 1 << 20

func (q *Queue) compactIfNeededLocked() error {
	if q.state.Offset < queueCompactOffset {
		return nil
	}
	info, err := os.Stat(q.recordsPath)
	if err != nil {
		return fmt.Errorf("log drain: stat queue before compaction: %w", err)
	}
	if q.state.Offset*2 < info.Size() {
		return nil
	}
	compactPath := q.recordsPath + ".compact"
	oldPath := q.recordsPath + ".old"
	src, err := os.Open(q.recordsPath) //nolint:forbidigo // recordsPath is a server-owned queue file under the validated per-drain spool root, never a customer path.
	if err != nil {
		return fmt.Errorf("log drain: open queue for compaction: %w", err)
	}
	if _, err := src.Seek(q.state.Offset, io.SeekStart); err != nil {
		_ = src.Close()
		return fmt.Errorf("log drain: seek queue for compaction: %w", err)
	}
	dst, err := os.OpenFile(compactPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		_ = src.Close()
		return fmt.Errorf("log drain: create compacted queue: %w", err)
	}
	_, copyErr := io.Copy(dst, src)
	if copyErr == nil {
		copyErr = dst.Sync()
	}
	closeDstErr := dst.Close()
	closeSrcErr := src.Close()
	if copyErr != nil {
		return fmt.Errorf("log drain: copy queue for compaction: %w", copyErr)
	}
	if closeDstErr != nil {
		return fmt.Errorf("log drain: close compacted queue: %w", closeDstErr)
	}
	if closeSrcErr != nil {
		return fmt.Errorf("log drain: close queue after compaction: %w", closeSrcErr)
	}
	if err := os.Rename(q.recordsPath, oldPath); err != nil {
		return fmt.Errorf("log drain: stage queue for compaction: %w", err)
	}
	if err := os.Rename(compactPath, q.recordsPath); err != nil {
		_ = os.Rename(oldPath, q.recordsPath)
		return fmt.Errorf("log drain: install compacted queue: %w", err)
	}
	q.state.Offset = 0
	q.state.Attempts = 0
	if err := q.writeStateLocked(); err != nil {
		return err
	}
	if err := os.Remove(oldPath); err != nil {
		return fmt.Errorf("log drain: remove queue compaction backup: %w", err)
	}
	return nil
}

// Stats returns durable backlog and dead-letter counters for health reporting.
func (q *Queue) Stats() QueueStats {
	q.mu.Lock()
	defer q.mu.Unlock()
	return QueueStats{
		PendingRecords: q.pendingRecords, PendingBytes: q.pendingBytes,
		CapacityBytes: q.maxBytes, DeadLetterTotal: q.deadLetters,
		OldestPendingAt: q.oldestPending,
	}
}

// LastSequences returns the highest acknowledged or terminal sequence per
// runtime instance. The stream consumer uses it to avoid replaying the whole
// ring after a gateway restart; pending records are deduplicated by Enqueue.
func (q *Queue) LastSequences() map[string]int64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	sequences := make(map[string]int64, len(q.state.LastSequences))
	for instance, sequence := range q.state.LastSequences {
		sequences[instance] = int64(sequence)
	}
	return sequences
}

func (q *Queue) addPendingKey(record Record) {
	if record.InstanceID != "" && record.Sequence > 0 {
		q.pendingKeys[queueKey{instance: record.InstanceID, sequence: record.Sequence}] = struct{}{}
	}
}

func (q *Queue) deletePendingKey(record Record) {
	if record.InstanceID != "" && record.Sequence > 0 {
		delete(q.pendingKeys, queueKey{instance: record.InstanceID, sequence: record.Sequence})
	}
}

func (q *Queue) nextEnqueuedAtLocked() time.Time {
	item, ok, err := q.nextLocked()
	if err != nil || !ok {
		return time.Time{}
	}
	return item.EnqueuedAt
}

func (q *Queue) nextLocked() (QueueItem, bool, error) {
	file, err := os.Open(q.recordsPath) //nolint:forbidigo // recordsPath is a server-owned queue file under the validated per-drain spool root, never a customer path.
	if err != nil {
		return QueueItem{}, false, err
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Seek(q.state.Offset, io.SeekStart); err != nil {
		return QueueItem{}, false, err
	}
	line, err := bufio.NewReader(file).ReadString('\n')
	if errors.Is(err, io.EOF) {
		return QueueItem{}, false, nil
	}
	if err != nil {
		return QueueItem{}, false, err
	}
	var record persistedRecord
	if err := json.Unmarshal([]byte(strings.TrimSuffix(line, "\n")), &record); err != nil {
		return QueueItem{}, false, err
	}
	return QueueItem{Record: record.Record, EnqueuedAt: record.EnqueuedAt, offset: q.state.Offset, nextOffset: q.state.Offset + int64(len(line))}, true, nil
}

func (q *Queue) writeStateLocked() error {
	payload, err := json.Marshal(q.state)
	if err != nil {
		return fmt.Errorf("log drain: encode durable queue cursor: %w", err)
	}
	tmp := q.cursorPath + ".tmp"
	file, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("log drain: open durable queue cursor: %w", err)
	}
	if err := writeAll(file, payload); err != nil {
		_ = file.Close()
		return fmt.Errorf("log drain: write durable queue cursor: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("log drain: sync durable queue cursor: %w", err)
	}
	closeErr := file.Close()
	if closeErr != nil {
		return fmt.Errorf("log drain: close durable queue cursor: %w", closeErr)
	}
	if err := os.Rename(tmp, q.cursorPath); err != nil {
		return fmt.Errorf("log drain: install durable queue cursor: %w", err)
	}
	return nil
}

func writeAll(file *os.File, payload []byte) error {
	for len(payload) > 0 {
		n, err := file.Write(payload)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		payload = payload[n:]
	}
	return nil
}

func countLines(path string) (int64, error) {
	file, err := os.Open(path) //nolint:forbidigo // path is a server-owned dead-letter queue under the validated per-drain spool root, never a customer path.
	if err != nil {
		return 0, err
	}
	defer func() { _ = file.Close() }()
	var count int64
	reader := bufio.NewReader(file)
	for {
		_, err := reader.ReadString('\n')
		if err == nil {
			count++
			continue
		}
		if errors.Is(err, io.EOF) {
			return count, nil
		}
		return 0, err
	}
}
