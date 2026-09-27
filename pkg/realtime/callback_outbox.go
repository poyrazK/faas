package realtime

import (
	"container/heap"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
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
	DefaultCallbackOutboxRoot                   = "/var/lib/faas/realtime-callbacks"
	LegacyCallbackOutboxRoot                    = "/run/faas/realtime-callbacks"
	DefaultCallbackOutboxMaxBytes         int64 = 64 << 20
	DefaultCallbackDeadLetterMaxBytes     int64 = 64 << 20
	DefaultCallbackOutboxMaxAttempts            = 10
	DefaultCallbackOutboxReplayWorkers          = 8
	MaxCallbackOutboxReplayWorkers              = 32
	DefaultCallbackOutboxMaxRetryInterval       = time.Minute
	MaxCallbackOutboxMaxRetryInterval           = time.Hour
	DefaultCallbackDeadLetterPageSize           = 100
	MaxCallbackDeadLetterPageSize               = 100
	DefaultCallbackOutboxRetryInterval          = time.Second
)

var (
	ErrCallbackOutboxFull         = errors.New("realtime: callback outbox is full")
	ErrCallbackOutboxAdmission    = errors.New("realtime: callback outbox admission failed")
	ErrCallbackOutboxItem         = errors.New("realtime: callback outbox item is not claimable")
	ErrCallbackOutboxUnavailable  = errors.New("realtime: callback outbox is unavailable")
	ErrCallbackDeadLetterNotFound = errors.New("realtime: callback dead letter not found")
	ErrCallbackDeadLetterConflict = errors.New("realtime: callback dead letter replay conflicts with active delivery")
	ErrCallbackDeadLetterCorrupt  = errors.New("realtime: callback dead letter is corrupt")
)

// CallbackOutboxConfig controls the node-local callback spool.
type CallbackOutboxConfig struct {
	Root               string
	MaxBytes           int64
	DeadLetterMaxBytes int64
	MaxAttempts        int
	ReplayWorkers      int
	// RetryInterval is both the replay poll interval and the base delay for a
	// failed callback. Consecutive failures use capped exponential backoff.
	RetryInterval time.Duration
	// MaxRetryInterval caps persisted callback retry delays and Retry-After hints.
	MaxRetryInterval time.Duration
}

// CallbackOutboxStats is a point-in-time view of the pending callback backlog,
// replay progress, and retained dead letters. The queue is intentionally
// node-local; aggregate its counters across realtimed nodes for fleet-level
// metering.
type CallbackOutboxStats struct {
	Pending                    int     `json:"pending"`
	PendingBytes               int64   `json:"pending_bytes"`
	CapacityBytes              int64   `json:"capacity_bytes"`
	ReplayDeliveries           uint64  `json:"replay_deliveries"`
	OldestPendingAgeSeconds    float64 `json:"oldest_pending_age_seconds"`
	DeadLetterTotal            int64   `json:"dead_letter_total"`
	DeadLetterBytes            int64   `json:"dead_letter_bytes"`
	DeadLetterCapacityBytes    int64   `json:"dead_letter_capacity_bytes"`
	DeadLetterEvictions        uint64  `json:"dead_letter_evictions"`
	DeadLetterLastEvictionUnix int64   `json:"dead_letter_last_eviction_unix"`
}

// CallbackDeadLetter contains operator-safe metadata for a retained callback.
// Payload bytes, callback URLs, and callback credentials are deliberately
// excluded from this type so they cannot escape through the management API.
type CallbackDeadLetter struct {
	ID             string    `json:"id"`
	Type           EventType `json:"type"`
	EndpointID     string    `json:"endpoint_id"`
	ConnectionID   string    `json:"connection_id"`
	Sequence       uint64    `json:"sequence"`
	OccurredAt     time.Time `json:"occurred_at"`
	EnqueuedAt     time.Time `json:"enqueued_at"`
	DeadLetteredAt time.Time `json:"dead_lettered_at"`
	Attempts       int       `json:"attempts"`
	SizeBytes      int64     `json:"size_bytes"`
}

// CallbackDeadLetterPage is one stable, ID-ordered page of retained callback
// metadata. Pass NextCursor as the next request's after value to continue.
type CallbackDeadLetterPage struct {
	Items      []CallbackDeadLetter `json:"items"`
	NextCursor string               `json:"next_cursor,omitempty"`
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

type callbackPendingAgeHeap []*callbackOutboxItem

func (h callbackPendingAgeHeap) Len() int { return len(h) }
func (h callbackPendingAgeHeap) Less(i, j int) bool {
	if !h[i].enqueuedAt.Equal(h[j].enqueuedAt) {
		return h[i].enqueuedAt.Before(h[j].enqueuedAt)
	}
	return h[i].record.Event.ID < h[j].record.Event.ID
}
func (h callbackPendingAgeHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].pendingAgeIndex = i
	h[j].pendingAgeIndex = j
}
func (h *callbackPendingAgeHeap) Push(value any) {
	item := value.(*callbackOutboxItem)
	item.pendingAgeIndex = len(*h)
	*h = append(*h, item)
}
func (h *callbackPendingAgeHeap) Pop() any {
	last := len(*h) - 1
	value := (*h)[last]
	(*h)[last] = nil
	value.pendingAgeIndex = -1
	*h = (*h)[:last]
	return value
}

type callbackOutboxRecord struct {
	Event             Event     `json:"event"`
	CallbackURL       string    `json:"callback_url"`
	CallbackPath      string    `json:"callback_path"`
	CallbackAuthToken string    `json:"callback_auth_token,omitempty"`
	EnqueuedAt        time.Time `json:"enqueued_at,omitempty"`
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
	record          callbackOutboxRecord
	path            string
	size            int64
	enqueuedAt      time.Time
	pendingAgeIndex int
}

// CallbackOutbox is a multi-producer durable spool for message and disconnect
// callbacks. Its bounded worker pool preserves event order per connection
// while allowing independent connections to deliver concurrently.
// Each event is written and fsynced before its first HTTP attempt; an
// unacknowledged file is replayed after restart. Delivery is at-least-once, so
// callback handlers should deduplicate by the event ID carried in the request
// header and JSON body.
type CallbackOutbox struct {
	mu                   sync.Mutex
	root                 string
	deadRoot             string
	maxBytes             int64
	deadMaxBytes         int64
	maxAttempts          int
	replayWorkers        int
	retryInterval        time.Duration
	maxRetryInterval     time.Duration
	items                map[string]*callbackOutboxItem
	inFlight             map[string]struct{}
	bytes                int64
	replayDeliveries     uint64
	deadBytes            int64
	deadEvictions        uint64
	deadLastEvictionUnix int64
	dead                 callbackDeadLetterHeap
	pendingAge           callbackPendingAgeHeap
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
	if cfg.ReplayWorkers <= 0 {
		cfg.ReplayWorkers = DefaultCallbackOutboxReplayWorkers
	}
	if cfg.ReplayWorkers > MaxCallbackOutboxReplayWorkers {
		cfg.ReplayWorkers = MaxCallbackOutboxReplayWorkers
	}
	if cfg.RetryInterval <= 0 {
		cfg.RetryInterval = DefaultCallbackOutboxRetryInterval
	}
	if cfg.RetryInterval > MaxCallbackOutboxMaxRetryInterval {
		cfg.RetryInterval = MaxCallbackOutboxMaxRetryInterval
	}
	if cfg.MaxRetryInterval <= 0 {
		cfg.MaxRetryInterval = DefaultCallbackOutboxMaxRetryInterval
	}
	if cfg.MaxRetryInterval > MaxCallbackOutboxMaxRetryInterval {
		cfg.MaxRetryInterval = MaxCallbackOutboxMaxRetryInterval
	}
	if cfg.MaxRetryInterval < cfg.RetryInterval {
		cfg.MaxRetryInterval = cfg.RetryInterval
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
		replayWorkers:      cfg.ReplayWorkers,
		retryInterval:      cfg.RetryInterval,
		maxRetryInterval:   cfg.MaxRetryInterval,
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
		enqueuedAt := record.EnqueuedAt
		if enqueuedAt.IsZero() {
			enqueuedAt = record.Event.At
		}
		if enqueuedAt.IsZero() {
			enqueuedAt = info.ModTime().UTC()
		}
		item := &callbackOutboxItem{record: record, path: path, size: info.Size(), enqueuedAt: enqueuedAt}
		q.items[id] = item
		heap.Push(&q.pendingAge, item)
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
		EnqueuedAt:        time.Now().UTC(),
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
	item := &callbackOutboxItem{record: record, path: path, size: int64(len(payload)), enqueuedAt: record.EnqueuedAt}
	q.items[event.ID] = item
	heap.Push(&q.pendingAge, item)
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
	return q.ack(id, false)
}

func (q *CallbackOutbox) ackReplay(id string) error {
	return q.ack(id, true)
}

func (q *CallbackOutbox) ack(id string, replay bool) error {
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
	if item.pendingAgeIndex >= 0 {
		heap.Remove(&q.pendingAge, item.pendingAgeIndex)
	}
	q.bytes -= item.size
	delete(q.items, id)
	delete(q.inFlight, id)
	if replay {
		q.replayDeliveries++
	}
	return nil
}

// Fail records a failed delivery using exponential backoff without a
// Retry-After hint. After the bounded retry budget it moves the event to the
// dead-letter directory.
func (q *CallbackOutbox) Fail(id string) error {
	return q.FailWithRetryAfter(id, 0)
}

// FailWithRetryAfter records a failed delivery. A positive retryAfter is used
// as the minimum delay before the next attempt, up to the configured cap.
func (q *CallbackOutbox) FailWithRetryAfter(id string, retryAfter time.Duration) error {
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
	delay := callbackOutboxRetryDelay(q.retryInterval, q.maxRetryInterval, item.record.Attempts, retryAfter)
	item.record.NextAttemptAt = time.Now().UTC().Add(delay)
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
		if item.pendingAgeIndex >= 0 {
			heap.Remove(&q.pendingAge, item.pendingAgeIndex)
		}
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

func callbackOutboxRetryDelay(base, maximum time.Duration, attempt int, retryAfter time.Duration) time.Duration {
	if base <= 0 {
		base = DefaultCallbackOutboxRetryInterval
	}
	if maximum < base {
		maximum = base
	}
	if attempt < 1 {
		attempt = 1
	}
	delay := base
	for i := 1; i < attempt && delay < maximum; i++ {
		if delay > maximum/2 {
			delay = maximum
			break
		}
		delay *= 2
	}
	if delay > maximum {
		delay = maximum
	}
	if retryAfter <= 0 && delay > 1 {
		// Full jitter between half and all of the exponential delay spreads
		// simultaneous failures across the same callback receiver.
		delay -= time.Duration(rand.Int63n(int64(delay / 2)))
	}
	if retryAfter > delay {
		delay = retryAfter
	}
	if delay > maximum {
		delay = maximum
	}
	return delay
}

// ListDeadLetters returns retained callback metadata in event-ID order. The
// cursor is exclusive so a caller can safely resume a bounded listing.
func (q *CallbackOutbox) ListDeadLetters(after string, limit int) (CallbackDeadLetterPage, error) {
	if q == nil {
		return CallbackDeadLetterPage{}, ErrCallbackOutboxUnavailable
	}
	if (after != "" && !validCallbackOutboxID(after)) || limit < 1 || limit > MaxCallbackDeadLetterPageSize {
		return CallbackDeadLetterPage{}, ErrCallbackOutboxItem
	}
	q.mu.Lock()
	defer q.mu.Unlock()

	deadLetters := append(callbackDeadLetterHeap(nil), q.dead...)
	sort.Slice(deadLetters, func(i, j int) bool { return deadLetters[i].id < deadLetters[j].id })
	page := CallbackDeadLetterPage{Items: make([]CallbackDeadLetter, 0, limit)}
	for _, dead := range deadLetters {
		if dead.id <= after {
			continue
		}
		if len(page.Items) == limit {
			page.NextCursor = page.Items[len(page.Items)-1].ID
			break
		}
		payload, err := os.ReadFile(filepath.Join(q.deadRoot, dead.id+".json"))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				q.removeDeadLetter(dead.id)
				continue
			}
			return CallbackDeadLetterPage{}, fmt.Errorf("realtime: read callback dead letter %q: %w", dead.id, err)
		}
		var record callbackOutboxRecord
		if err := json.Unmarshal(payload, &record); err != nil || record.Event.ID != dead.id ||
			(record.Event.Type != EventMessage && record.Event.Type != EventDisconnect) {
			return CallbackDeadLetterPage{}, fmt.Errorf("realtime: decode callback dead letter %q: %w", dead.id, ErrCallbackDeadLetterCorrupt)
		}
		enqueuedAt := record.EnqueuedAt
		if enqueuedAt.IsZero() {
			enqueuedAt = record.Event.At
		}
		if enqueuedAt.IsZero() {
			enqueuedAt = dead.modified
		}
		page.Items = append(page.Items, CallbackDeadLetter{
			ID:             record.Event.ID,
			Type:           record.Event.Type,
			EndpointID:     record.Event.EndpointID,
			ConnectionID:   record.Event.ConnectionID,
			Sequence:       record.Event.Sequence,
			OccurredAt:     record.Event.At,
			EnqueuedAt:     enqueuedAt,
			DeadLetteredAt: dead.modified,
			Attempts:       record.Attempts,
			SizeBytes:      dead.size,
		})
	}
	return page, nil
}

// ReplayDeadLetter returns a retained event to the pending queue. Repeating a
// request for an ID already pending is safe and does not create a duplicate.
// The event ID and enqueue timestamp are preserved for application deduplication
// and queue age reporting; only its bounded retry state is reset.
func (q *CallbackOutbox) ReplayDeadLetter(id string) error {
	if q == nil {
		return ErrCallbackOutboxUnavailable
	}
	if !validCallbackOutboxID(id) {
		return ErrCallbackOutboxItem
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, pending := q.items[id]; pending {
		return nil
	}
	if _, dead := q.deadIDs[id]; !dead {
		return ErrCallbackDeadLetterNotFound
	}

	deadPath := filepath.Join(q.deadRoot, id+".json")
	payload, err := os.ReadFile(deadPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			q.removeDeadLetter(id)
			return ErrCallbackDeadLetterNotFound
		}
		return fmt.Errorf("realtime: read callback dead letter %q for replay: %w", id, err)
	}
	var record callbackOutboxRecord
	if err := json.Unmarshal(payload, &record); err != nil || record.Event.ID != id ||
		(record.Event.Type != EventMessage && record.Event.Type != EventDisconnect) {
		return fmt.Errorf("realtime: decode callback dead letter %q for replay: %w", id, ErrCallbackDeadLetterCorrupt)
	}
	for inFlightID := range q.inFlight {
		pending := q.items[inFlightID]
		if pending != nil && pending.record.Event.ConnectionID == record.Event.ConnectionID &&
			callbackEventBefore(record.Event, pending.record.Event) {
			return ErrCallbackDeadLetterConflict
		}
	}

	pendingPath := filepath.Join(q.root, id+".json")
	if _, err := os.Lstat(pendingPath); err == nil {
		return ErrCallbackDeadLetterConflict
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("realtime: inspect callback replay destination %q: %w", id, err)
	}
	enqueuedAt := record.EnqueuedAt
	if enqueuedAt.IsZero() {
		enqueuedAt = record.Event.At
	}
	if enqueuedAt.IsZero() {
		enqueuedAt = time.Now().UTC()
	}
	record.EnqueuedAt = enqueuedAt
	record.Attempts = 0
	record.NextAttemptAt = time.Time{}
	payload, err = json.Marshal(record)
	if err != nil {
		return fmt.Errorf("realtime: encode callback dead-letter replay %q: %w", id, err)
	}
	if q.bytes+int64(len(payload)) > q.maxBytes {
		return ErrCallbackOutboxFull
	}
	if err := writeCallbackOutboxFile(deadPath, payload); err != nil {
		q.refreshDeadLetterInfo(id)
		return fmt.Errorf("realtime: persist callback dead-letter replay %q: %w", id, err)
	}
	if err := os.Rename(deadPath, pendingPath); err != nil {
		q.refreshDeadLetterInfo(id)
		return fmt.Errorf("realtime: move callback dead-letter replay %q: %w", id, err)
	}
	q.removeDeadLetter(id)
	item := &callbackOutboxItem{record: record, path: pendingPath, size: int64(len(payload)), enqueuedAt: enqueuedAt}
	q.items[id] = item
	heap.Push(&q.pendingAge, item)
	q.bytes += item.size
	var syncErr error
	if err := syncCallbackOutboxDir(q.root); err != nil {
		syncErr = errors.Join(syncErr, fmt.Errorf("realtime: sync callback outbox after replay: %w", err))
	}
	if err := syncCallbackOutboxDir(q.deadRoot); err != nil {
		syncErr = errors.Join(syncErr, fmt.Errorf("realtime: sync callback dead letters after replay: %w", err))
	}
	if syncErr != nil {
		return syncErr
	}
	return nil
}

// removeDeadLetter updates retained-dead-letter indexes after a successful
// move. It is called with q.mu held.
func (q *CallbackOutbox) removeDeadLetter(id string) {
	for index, dead := range q.dead {
		if dead.id == id {
			heap.Remove(&q.dead, index)
			delete(q.deadIDs, id)
			q.deadBytes -= dead.size
			return
		}
	}
	delete(q.deadIDs, id)
}

// refreshDeadLetterInfo repairs the in-memory size and modification time after
// a dead-letter rewrite that did not complete its move to the pending folder.
// It is called with q.mu held.
func (q *CallbackOutbox) refreshDeadLetterInfo(id string) {
	info, err := os.Stat(filepath.Join(q.deadRoot, id+".json"))
	if err != nil {
		return
	}
	for index := range q.dead {
		if q.dead[index].id == id {
			q.deadBytes += info.Size() - q.dead[index].size
			q.dead[index].size = info.Size()
			q.dead[index].modified = info.ModTime()
			heap.Init(&q.dead)
			return
		}
	}
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
	oldestPendingAge := float64(0)
	if q.pendingAge.Len() > 0 {
		oldestPendingAge = time.Since(q.pendingAge[0].enqueuedAt).Seconds()
		if oldestPendingAge < 0 {
			oldestPendingAge = 0
		}
	}
	return CallbackOutboxStats{
		Pending:                    len(q.items),
		PendingBytes:               q.bytes,
		CapacityBytes:              q.maxBytes,
		ReplayDeliveries:           q.replayDeliveries,
		OldestPendingAgeSeconds:    oldestPendingAge,
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
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var workers sync.WaitGroup
	var errMu sync.Mutex
	var firstErr error
	workers.Add(q.replayWorkers)
	for range q.replayWorkers {
		go func() {
			defer workers.Done()
			if err := q.drainWorker(workerCtx, deliver); err != nil && !errors.Is(err, context.Canceled) {
				errMu.Lock()
				if firstErr == nil {
					firstErr = err
					cancel()
				}
				errMu.Unlock()
			}
		}()
	}
	workers.Wait()

	errMu.Lock()
	defer errMu.Unlock()
	if firstErr != nil {
		return firstErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func (q *CallbackOutbox) drainWorker(ctx context.Context, deliver func(context.Context, Event) error) error {
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
		if ctx.Err() != nil {
			q.Release(event.ID)
			return ctx.Err()
		}
		err = deliver(ctx, event)
		if err == nil {
			if ackErr := q.ackReplay(event.ID); ackErr != nil {
				q.Release(event.ID)
				return ackErr
			}
			continue
		}
		if ctx.Err() != nil {
			q.Release(event.ID)
			return ctx.Err()
		}
		if failErr := q.FailWithRetryAfter(event.ID, callbackRetryAfter(err)); failErr != nil {
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
