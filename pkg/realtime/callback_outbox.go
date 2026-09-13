package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultCallbackOutboxRoot is node-local durable storage. /run/faas is
	// writable by the realtimed unit and survives a process restart; operators
	// that need reboot survival can override it with a persistent mount.
	DefaultCallbackOutboxRoot                = "/run/faas/realtime-callbacks"
	DefaultCallbackOutboxMaxBytes      int64 = 64 << 20
	DefaultCallbackOutboxMaxAttempts         = 10
	DefaultCallbackOutboxRetryInterval       = time.Second
)

var (
	ErrCallbackOutboxFull = errors.New("realtime: callback outbox is full")
	ErrCallbackOutboxItem = errors.New("realtime: callback outbox item is not claimable")
)

// CallbackOutboxConfig controls the node-local callback spool.
type CallbackOutboxConfig struct {
	Root          string
	MaxBytes      int64
	MaxAttempts   int
	RetryInterval time.Duration
}

// CallbackOutboxStats is a point-in-time view of pending and dead-lettered
// callback events. The queue is intentionally node-local; aggregate these
// counters across realtimed nodes for fleet-level metering.
type CallbackOutboxStats struct {
	Pending         int   `json:"pending"`
	PendingBytes    int64 `json:"pending_bytes"`
	CapacityBytes   int64 `json:"capacity_bytes"`
	DeadLetterTotal int64 `json:"dead_letter_total"`
}

type callbackOutboxRecord struct {
	Event             Event     `json:"event"`
	CallbackURL       string    `json:"callback_url"`
	CallbackPath      string    `json:"callback_path"`
	CallbackAuthToken string    `json:"callback_auth_token,omitempty"`
	Attempts          int       `json:"attempts"`
	NextAttemptAt     time.Time `json:"next_attempt_at,omitempty"`
}

func (r callbackOutboxRecord) event() Event {
	event := r.Event
	event.CallbackURL = r.CallbackURL
	event.CallbackPath = r.CallbackPath
	event.CallbackAuthToken = r.CallbackAuthToken
	return event
}

type callbackOutboxItem struct {
	record callbackOutboxRecord
	path   string
	size   int64
}

// CallbackOutbox is a single-consumer, multi-producer durable spool for
// message and disconnect callbacks. Each event is written and fsynced before
// its first HTTP attempt; an unacknowledged file is replayed after restart.
// Delivery is at-least-once, so callback handlers should deduplicate by the
// event ID carried in the request header and JSON body.
type CallbackOutbox struct {
	mu            sync.Mutex
	root          string
	deadRoot      string
	maxBytes      int64
	maxAttempts   int
	retryInterval time.Duration
	items         map[string]*callbackOutboxItem
	inFlight      map[string]struct{}
	bytes         int64
	deadLetters   int64
}

// NewCallbackOutbox opens or creates a node-local callback spool.
func NewCallbackOutbox(cfg CallbackOutboxConfig) (*CallbackOutbox, error) {
	if strings.TrimSpace(cfg.Root) == "" {
		return nil, errors.New("realtime: callback outbox root is empty")
	}
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = DefaultCallbackOutboxMaxBytes
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = DefaultCallbackOutboxMaxAttempts
	}
	if cfg.RetryInterval <= 0 {
		cfg.RetryInterval = DefaultCallbackOutboxRetryInterval
	}
	deadRoot := filepath.Join(cfg.Root, "dead")
	if err := os.MkdirAll(deadRoot, 0o750); err != nil {
		return nil, fmt.Errorf("realtime: create callback outbox: %w", err)
	}
	q := &CallbackOutbox{
		root:          cfg.Root,
		deadRoot:      deadRoot,
		maxBytes:      cfg.MaxBytes,
		maxAttempts:   cfg.MaxAttempts,
		retryInterval: cfg.RetryInterval,
		items:         make(map[string]*callbackOutboxItem),
		inFlight:      make(map[string]struct{}),
	}
	if err := q.load(); err != nil {
		return nil, err
	}
	return q, nil
}

func (q *CallbackOutbox) load() error {
	entries, err := os.ReadDir(q.root)
	if err != nil {
		return fmt.Errorf("realtime: read callback outbox: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("realtime: callback outbox entry %q is not a regular file", entry.Name())
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if !validCallbackOutboxID(id) {
			return fmt.Errorf("realtime: callback outbox entry %q has invalid id", entry.Name())
		}
		path := filepath.Join(q.root, entry.Name())
		payload, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("realtime: read callback outbox item %q: %w", id, err)
		}
		var record callbackOutboxRecord
		if err := json.Unmarshal(payload, &record); err != nil || record.Event.ID != id {
			return fmt.Errorf("realtime: decode callback outbox item %q: %w", id, ErrCallbackOutboxItem)
		}
		if record.Event.Type != EventMessage && record.Event.Type != EventDisconnect {
			return fmt.Errorf("realtime: callback outbox item %q has unsupported event type %q", id, record.Event.Type)
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("realtime: stat callback outbox item %q: %w", id, err)
		}
		q.items[id] = &callbackOutboxItem{record: record, path: path, size: info.Size()}
		q.bytes += info.Size()
	}
	deadEntries, err := os.ReadDir(q.deadRoot)
	if err != nil {
		return fmt.Errorf("realtime: read callback dead letters: %w", err)
	}
	for _, entry := range deadEntries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			q.deadLetters++
		}
	}
	return nil
}

func validCallbackOutboxID(id string) bool {
	return id != "" && len(id) <= 256 && !strings.ContainsAny(id, `/\\`) && !strings.ContainsRune(id, '\x00')
}

// EnqueueAndClaim durably records event and reserves it for an immediate
// delivery attempt. A false result means the same event ID is already being
// delivered by the background replay loop; event IDs are unique, so callers
// can treat that as accepted.
func (q *CallbackOutbox) EnqueueAndClaim(event Event) (bool, error) {
	if q == nil || !validCallbackOutboxID(event.ID) {
		return false, ErrCallbackOutboxItem
	}
	if event.Type != EventMessage && event.Type != EventDisconnect {
		return false, ErrCallbackOutboxItem
	}
	record := callbackOutboxRecord{
		Event:             event,
		CallbackURL:       event.CallbackURL,
		CallbackPath:      event.CallbackPath,
		CallbackAuthToken: event.CallbackAuthToken,
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return false, fmt.Errorf("realtime: encode callback outbox item: %w", err)
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	if _, exists := q.items[event.ID]; exists {
		if _, inFlight := q.inFlight[event.ID]; inFlight {
			return false, nil
		}
		q.inFlight[event.ID] = struct{}{}
		return true, nil
	}
	if q.bytes+int64(len(payload)) > q.maxBytes {
		return false, ErrCallbackOutboxFull
	}
	path := filepath.Join(q.root, event.ID+".json")
	if err := writeCallbackOutboxFile(path, payload); err != nil {
		return false, fmt.Errorf("realtime: persist callback outbox item: %w", err)
	}
	q.items[event.ID] = &callbackOutboxItem{record: record, path: path, size: int64(len(payload))}
	q.bytes += int64(len(payload))
	q.inFlight[event.ID] = struct{}{}
	return true, nil
}

// ClaimNext returns the oldest eligible pending event and reserves it for the
// caller. Ordering is deterministic by event ID; retry timing is persisted so
// one failed callback cannot be retried in a tight loop.
func (q *CallbackOutbox) ClaimNext() (Event, bool, error) {
	if q == nil {
		return Event{}, false, ErrCallbackOutboxItem
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	ids := make([]string, 0, len(q.items))
	for id := range q.items {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	now := time.Now().UTC()
	for _, id := range ids {
		if _, inFlight := q.inFlight[id]; inFlight {
			continue
		}
		item := q.items[id]
		if item.record.NextAttemptAt.After(now) {
			continue
		}
		q.inFlight[id] = struct{}{}
		event := item.record.event()
		event.Data = append([]byte(nil), event.Data...)
		return event, true, nil
	}
	return Event{}, false, nil
}

// Ack removes a successfully delivered event from the durable spool.
func (q *CallbackOutbox) Ack(id string) error {
	if q == nil {
		return ErrCallbackOutboxItem
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	item, ok := q.items[id]
	if !ok {
		return ErrCallbackOutboxItem
	}
	if _, claimed := q.inFlight[id]; !claimed {
		return ErrCallbackOutboxItem
	}
	if err := os.Remove(item.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("realtime: acknowledge callback outbox item: %w", err)
	}
	q.bytes -= item.size
	delete(q.items, id)
	delete(q.inFlight, id)
	return nil
}

// Fail records a failed delivery. After the bounded retry budget it moves the
// event to the dead-letter directory, preserving it for operator inspection
// without allowing a poison callback to consume the active outbox forever.
func (q *CallbackOutbox) Fail(id string) error {
	if q == nil {
		return ErrCallbackOutboxItem
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	item, ok := q.items[id]
	if !ok {
		return ErrCallbackOutboxItem
	}
	if _, claimed := q.inFlight[id]; !claimed {
		return ErrCallbackOutboxItem
	}
	item.record.Attempts++
	item.record.NextAttemptAt = time.Now().UTC().Add(q.retryInterval)
	payload, err := json.Marshal(item.record)
	if err != nil {
		delete(q.inFlight, id)
		return fmt.Errorf("realtime: encode failed callback outbox item: %w", err)
	}
	if item.record.Attempts >= q.maxAttempts {
		if err := writeCallbackOutboxFile(item.path, payload); err != nil {
			delete(q.inFlight, id)
			return fmt.Errorf("realtime: persist callback dead letter: %w", err)
		}
		deadPath := filepath.Join(q.deadRoot, id+".json")
		if err := os.Rename(item.path, deadPath); err != nil {
			delete(q.inFlight, id)
			return fmt.Errorf("realtime: move callback dead letter: %w", err)
		}
		q.bytes -= item.size
		q.deadLetters++
		delete(q.items, id)
		delete(q.inFlight, id)
		return nil
	}
	if err := writeCallbackOutboxFile(item.path, payload); err != nil {
		delete(q.inFlight, id)
		return fmt.Errorf("realtime: persist callback retry: %w", err)
	}
	q.bytes += int64(len(payload)) - item.size
	item.size = int64(len(payload))
	delete(q.inFlight, id)
	return nil
}

// Release abandons an in-flight claim without changing its retry budget. It
// is used when shutdown cancels an HTTP attempt before it can be classified.
func (q *CallbackOutbox) Release(id string) {
	if q == nil {
		return
	}
	q.mu.Lock()
	delete(q.inFlight, id)
	q.mu.Unlock()
}

// Stats returns the current durable queue counters.
func (q *CallbackOutbox) Stats() CallbackOutboxStats {
	if q == nil {
		return CallbackOutboxStats{}
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return CallbackOutboxStats{
		Pending:         len(q.items),
		PendingBytes:    q.bytes,
		CapacityBytes:   q.maxBytes,
		DeadLetterTotal: q.deadLetters,
	}
}

// Run replays pending events until ctx is canceled. The deliver function must
// return nil only after a 2xx callback response; failures remain durable and
// are retried after the configured interval.
func (q *CallbackOutbox) Run(ctx context.Context, deliver func(context.Context, Event) error) error {
	if q == nil || deliver == nil {
		return ErrCallbackOutboxItem
	}
	ticker := time.NewTicker(q.retryInterval)
	defer ticker.Stop()
	for {
		if err := q.drain(ctx, deliver); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (q *CallbackOutbox) drain(ctx context.Context, deliver func(context.Context, Event) error) error {
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		event, ok, err := q.ClaimNext()
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		err = deliver(ctx, event)
		if err == nil {
			if ackErr := q.Ack(event.ID); ackErr != nil {
				return ackErr
			}
			continue
		}
		if ctx.Err() != nil {
			q.Release(event.ID)
			return ctx.Err()
		}
		if failErr := q.Fail(event.ID); failErr != nil {
			return failErr
		}
	}
}

func writeCallbackOutboxFile(path string, payload []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".callback-tmp-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(payload); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	return nil
}
