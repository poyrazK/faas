package realtime

import (
	"container/heap"
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
	// DefaultCallbackOutboxRoot is node-local persistent storage. The runtime
	// directory is retained only as a migration source for older installs.
	DefaultCallbackOutboxRoot                = "/var/lib/faas/realtime-callbacks"
	LegacyCallbackOutboxRoot                 = "/run/faas/realtime-callbacks"
	DefaultCallbackOutboxMaxBytes      int64 = 64 << 20
	DefaultCallbackDeadLetterMaxBytes  int64 = 64 << 20
	DefaultCallbackOutboxMaxAttempts         = 10
	DefaultCallbackOutboxRetryInterval       = time.Second
)

var (
	ErrCallbackOutboxFull = errors.New("realtime: callback outbox is full")
	ErrCallbackOutboxItem = errors.New("realtime: callback outbox item is not claimable")
)

// CallbackOutboxConfig controls the node-local callback spool.
type CallbackOutboxConfig struct {
	Root               string
	MaxBytes           int64
	DeadLetterMaxBytes int64
	MaxAttempts        int
	RetryInterval      time.Duration
}

// CallbackOutboxStats is a point-in-time view of pending and dead-lettered
// callback events. The queue is intentionally node-local; aggregate these
// counters across realtimed nodes for fleet-level metering.
type CallbackOutboxStats struct {
	Pending                    int    `json:"pending"`
	PendingBytes               int64  `json:"pending_bytes"`
	CapacityBytes              int64  `json:"capacity_bytes"`
	DeadLetterTotal            int64  `json:"dead_letter_total"`
	DeadLetterBytes            int64  `json:"dead_letter_bytes"`
	DeadLetterCapacityBytes    int64  `json:"dead_letter_capacity_bytes"`
	DeadLetterEvictions        uint64 `json:"dead_letter_evictions"`
	DeadLetterLastEvictionUnix int64  `json:"dead_letter_last_eviction_unix"`
}

type callbackDeadLetter struct {
	id       string
	size     int64
	modified time.Time
}

type callbackDeadLetterHeap []callbackDeadLetter

func (h callbackDeadLetterHeap) Len() int { return len(h) }
func (h callbackDeadLetterHeap) Less(i, j int) bool {
	if !h[i].modified.Equal(h[j].modified) {
		return h[i].modified.Before(h[j].modified)
	}
	return h[i].id < h[j].id
}
func (h callbackDeadLetterHeap) Swap(i, j int)   { h[i], h[j] = h[j], h[i] }
func (h *callbackDeadLetterHeap) Push(value any) { *h = append(*h, value.(callbackDeadLetter)) }
func (h *callbackDeadLetterHeap) Pop() any {
	last := len(*h) - 1
	value := (*h)[last]
	*h = (*h)[:last]
	return value
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
	mu                   sync.Mutex
	root                 string
	deadRoot             string
	maxBytes             int64
	deadMaxBytes         int64
	maxAttempts          int
	retryInterval        time.Duration
	items                map[string]*callbackOutboxItem
	inFlight             map[string]struct{}
	bytes                int64
	deadBytes            int64
	deadEvictions        uint64
	deadLastEvictionUnix int64
	dead                 callbackDeadLetterHeap
	deadIDs              map[string]struct{}
	callbackAuthTokens   map[string]string
}

// NewCallbackOutbox opens or creates a node-local callback spool.
func NewCallbackOutbox(cfg CallbackOutboxConfig) (*CallbackOutbox, error) {
	if strings.TrimSpace(cfg.Root) == "" {
		return nil, errors.New("realtime: callback outbox root is empty")
	}
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = DefaultCallbackOutboxMaxBytes
	}
	if cfg.DeadLetterMaxBytes <= 0 {
		cfg.DeadLetterMaxBytes = DefaultCallbackDeadLetterMaxBytes
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = DefaultCallbackOutboxMaxAttempts
	}
	if cfg.RetryInterval <= 0 {
		cfg.RetryInterval = DefaultCallbackOutboxRetryInterval
	}
	deadRoot := filepath.Join(cfg.Root, "dead")
	if err := os.MkdirAll(deadRoot, 0o700); err != nil {
		return nil, fmt.Errorf("realtime: create callback outbox: %w", err)
	}
	q := &CallbackOutbox{
		root:               cfg.Root,
		deadRoot:           deadRoot,
		maxBytes:           cfg.MaxBytes,
		deadMaxBytes:       cfg.DeadLetterMaxBytes,
		maxAttempts:        cfg.MaxAttempts,
		retryInterval:      cfg.RetryInterval,
		items:              make(map[string]*callbackOutboxItem),
		inFlight:           make(map[string]struct{}),
		deadIDs:            make(map[string]struct{}),
		callbackAuthTokens: make(map[string]string),
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
		if !strings.HasSuffix(entry.Name(), ".json") || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if !validCallbackOutboxID(id) || !entry.Type().IsRegular() {
			return fmt.Errorf("realtime: invalid callback dead letter %q", entry.Name())
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("realtime: stat callback dead letter %q: %w", id, err)
		}
		q.dead = append(q.dead, callbackDeadLetter{id: id, size: info.Size(), modified: info.ModTime()})
		q.deadIDs[id] = struct{}{}
		q.deadBytes += info.Size()
	}
	heap.Init(&q.dead)
	return q.pruneDeadLetters()
}

// pruneDeadLetters is called with q.mu held. Oldest files are removed first;
// the ID breaks ties so startup and live retention choose the same victims.
func (q *CallbackOutbox) pruneDeadLetters() (err error) {
	if q.deadBytes <= q.deadMaxBytes {
		return nil
	}
	removed := false
	defer func() {
		if removed {
			if syncErr := syncCallbackOutboxDir(q.deadRoot); syncErr != nil {
				err = errors.Join(err, fmt.Errorf("realtime: sync callback dead-letter eviction: %w", syncErr))
			}
		}
	}()
	for q.deadBytes > q.deadMaxBytes {
		oldest := heap.Pop(&q.dead).(callbackDeadLetter)
		removeErr := os.Remove(filepath.Join(q.deadRoot, oldest.id+".json"))
		if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			heap.Push(&q.dead, oldest)
			return fmt.Errorf("realtime: evict callback dead letter %q: %w", oldest.id, removeErr)
		}
		q.deadBytes -= oldest.size
		delete(q.deadIDs, oldest.id)
		if removeErr == nil {
			q.deadEvictions++
			q.deadLastEvictionUnix = time.Now().Unix()
			removed = true
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
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, exists := q.deadIDs[event.ID]; exists {
		return false, ErrCallbackOutboxItem
	}
	if _, exists := q.items[event.ID]; exists {
		if _, inFlight := q.inFlight[event.ID]; inFlight {
			return false, nil
		}
		if q.hasPriorEvent(event) {
			return false, nil
		}
		q.inFlight[event.ID] = struct{}{}
		return true, nil
	}
	if token, known := q.callbackAuthTokens[event.EndpointID]; known {
		event.CallbackAuthToken = token
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
	if q.bytes+int64(len(payload)) > q.maxBytes {
		return false, ErrCallbackOutboxFull
	}
	path := filepath.Join(q.root, event.ID+".json")
	if err := writeCallbackOutboxFile(path, payload); err != nil {
		return false, fmt.Errorf("realtime: persist callback outbox item: %w", err)
	}
	q.items[event.ID] = &callbackOutboxItem{record: record, path: path, size: int64(len(payload))}
	q.bytes += int64(len(payload))
	if q.hasPriorEvent(event) {
		// The replay loop will deliver this after earlier events for the
		// connection are acknowledged or dead-lettered.
		return false, nil
	}
	q.inFlight[event.ID] = struct{}{}
	return true, nil
}

func (q *CallbackOutbox) claimedEvent(id string) (Event, error) {
	if q == nil {
		return Event{}, ErrCallbackOutboxItem
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	item, ok := q.items[id]
	if !ok {
		return Event{}, ErrCallbackOutboxItem
	}
	if _, claimed := q.inFlight[id]; !claimed {
		return Event{}, ErrCallbackOutboxItem
	}
	event := item.record.event()
	event.Data = append([]byte(nil), event.Data...)
	return event, nil
}

// updateCallbackAuthToken records the credential used by future enqueues.
// Pending files keep the credential snapshot captured when they were queued,
// so applications can accept both tokens until their old callbacks drain.
func (q *CallbackOutbox) updateCallbackAuthToken(endpointID, token string) error {
	if q == nil || endpointID == "" {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.callbackAuthTokens[endpointID] = token
	return nil
}

func (q *CallbackOutbox) removeCallbackAuthToken(endpointID string) {
	if q == nil {
		return
	}
	q.mu.Lock()
	delete(q.callbackAuthTokens, endpointID)
	q.mu.Unlock()
}

// callbackEventBefore orders callbacks for one connection by their WebSocket
// receive sequence. A disconnect shares the last message's sequence and must
// follow that message. Event ID is only a final deterministic tie-breaker.
func callbackEventBefore(a, b Event) bool {
	if a.Sequence != b.Sequence {
		return a.Sequence < b.Sequence
	}
	if a.Type != b.Type {
		return a.Type == EventMessage
	}
	if !a.At.Equal(b.At) {
		return a.At.Before(b.At)
	}
	return a.ID < b.ID
}

// hasPriorEvent is called with q.mu held.
func (q *CallbackOutbox) hasPriorEvent(event Event) bool {
	for id, item := range q.items {
		if id != event.ID && item.record.Event.ConnectionID == event.ConnectionID &&
			callbackEventBefore(item.record.Event, event) {
			return true
		}
	}
	return false
}

// ClaimNext reserves the oldest eligible event while preserving each
// connection's sequence. A failed earlier callback blocks later callbacks for
// that connection until it is acknowledged or dead-lettered.
func (q *CallbackOutbox) ClaimNext() (Event, bool, error) {
	if q == nil {
		return Event{}, false, ErrCallbackOutboxItem
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	firstByConnection := make(map[string]string)
	for id, item := range q.items {
		key := item.record.Event.ConnectionID
		if key == "" {
			key = id
		}
		if prior, ok := firstByConnection[key]; !ok ||
			callbackEventBefore(item.record.Event, q.items[prior].record.Event) {
			firstByConnection[key] = id
		}
	}
	ids := make([]string, 0, len(firstByConnection))
	for _, id := range firstByConnection {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := q.items[ids[i]].record.Event, q.items[ids[j]].record.Event
		if !a.At.Equal(b.At) {
			return a.At.Before(b.At)
		}
		return ids[i] < ids[j]
	})
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
		modified := time.Now().UTC()
		if info, err := os.Stat(item.path); err == nil {
			modified = info.ModTime()
		}
		deadPath := filepath.Join(q.deadRoot, id+".json")
		if err := os.Rename(item.path, deadPath); err != nil {
			delete(q.inFlight, id)
			return fmt.Errorf("realtime: move callback dead letter: %w", err)
		}
		q.bytes -= item.size
		q.deadBytes += int64(len(payload))
		heap.Push(&q.dead, callbackDeadLetter{id: id, size: int64(len(payload)), modified: modified})
		q.deadIDs[id] = struct{}{}
		delete(q.items, id)
		delete(q.inFlight, id)
		if err := syncCallbackOutboxDir(q.deadRoot); err != nil {
			return fmt.Errorf("realtime: sync callback dead letter: %w", err)
		}
		if err := syncCallbackOutboxDir(q.root); err != nil {
			return fmt.Errorf("realtime: sync callback outbox after dead-letter move: %w", err)
		}
		return q.pruneDeadLetters()
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
		Pending:                    len(q.items),
		PendingBytes:               q.bytes,
		CapacityBytes:              q.maxBytes,
		DeadLetterTotal:            int64(len(q.dead)),
		DeadLetterBytes:            q.deadBytes,
		DeadLetterCapacityBytes:    q.deadMaxBytes,
		DeadLetterEvictions:        q.deadEvictions,
		DeadLetterLastEvictionUnix: q.deadLastEvictionUnix,
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
	return syncCallbackOutboxDir(filepath.Dir(path))
}

func syncCallbackOutboxDir(path string) error {
	dir, err := os.Open(path) //nolint:forbidigo // path is the operator-owned callback spool directory, never a customer path
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}
