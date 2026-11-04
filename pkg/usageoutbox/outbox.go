// Package usageoutbox keeps gateway usage facts on local disk until apid
// acknowledges their idempotent database transaction.
package usageoutbox

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	DefaultRoot            = "/var/lib/faas/consumer-usage"
	DefaultMaxBytes  int64 = 64 << 20
	compactAfter     int64 = 8 << 20
	groupCommitDelay       = time.Millisecond
)

var ErrFull = errors.New("consumer usage outbox full")

// Event carries financial facts and optional, opt-in observations. Discovered
// routes are normalized method/templates, never queries, headers, or bodies.
type Event struct {
	EventID                              string         `json:"event_id"`
	AccountID                            string         `json:"account_id"`
	AppID                                string         `json:"app_id"`
	ConsumerID                           string         `json:"consumer_id,omitempty"`
	PlatformTenantID                     string         `json:"platform_tenant_id,omitempty"`
	PlatformTenantSurfaceID              string         `json:"platform_tenant_surface_id,omitempty"`
	PlatformTenantJWTAuthorizationRuleID string         `json:"platform_tenant_jwt_authorization_rule_id,omitempty"`
	WindowStart                          time.Time      `json:"window_start"`
	RequestCount                         int64          `json:"request_count"`
	ErrorCount                           int64          `json:"error_count"`
	BillableUnits                        int64          `json:"billable_units"`
	Audit                                *AuditEvidence `json:"audit,omitempty"`
	DiscoveredRoute                      string         `json:"discovered_route,omitempty"`
	DiscoveredAtUnixMs                   int64          `json:"discovered_at_unix_ms,omitempty"`
	// BillingRoute is the bounded route label of consumer-attributed
	// traffic, kept as route-level billable minutes for weights (ADR-952).
	BillingRoute string `json:"billing_route,omitempty"`
	// ComputeMS and ResponseBytes measure a billable consumer request for
	// rate cards that price compute time or response size (ADR-1308).
	ComputeMS int64 `json:"compute_ms,omitempty"`
	// MeterUnits are the units the app reported for a billable consumer
	// request, by meter (ADR-1318).
	MeterUnits    map[string]int64 `json:"meter_units,omitempty"`
	ResponseBytes int64            `json:"response_bytes,omitempty"`
}

// AuditEvidence is opt-in request metadata attached to the same fsynced
// record and event ID as its financial usage fact. It never contains a query,
// payload, or unverified application-user ID. Source IP is populated only
// from the trusted public-gateway handoff. Undeclared route candidates can
// still retain literal path segments, so this is opt-in.
type AuditEvidence struct {
	RouteTemplate string    `json:"route_template"`
	Method        string    `json:"method"`
	HTTPStatus    int       `json:"http_status"`
	LatencyMS     int       `json:"latency_ms"`
	TraceID       string    `json:"trace_id,omitempty"`
	DeploymentID  string    `json:"deployment_id,omitempty"`
	CommitSHA     string    `json:"commit_sha,omitempty"`
	OccurredAt    time.Time `json:"occurred_at"`
	RequestID     string    `json:"request_id,omitempty"`
	SourceIP      string    `json:"source_ip,omitempty"`
}

type Item struct {
	Event Event
	start int64
	end   int64
}

type Stats struct {
	PendingRecords int64
	PendingBytes   int64
	CapacityBytes  int64
}

// Outbox has concurrent producers and one ordered acknowledger. The cursor is
// durable only after apid commits. Corrupt unacknowledged data fails startup.
type Outbox struct {
	mu         sync.Mutex
	cond       *sync.Cond
	root       string
	dataPath   string
	cursorPath string
	maxBytes   int64
	offset     int64
	end        int64
	durableEnd int64
	pending    int64
	flushing   bool
	broken     error
	// brokenAt is when broken was set; tornAt is the durable end to cut the
	// data file back to before recovering, or -1 when the failure did not
	// leave undurable lines behind (H103).
	brokenAt time.Time
	tornAt   int64
}

// recoverAfter is how long a broken outbox waits before it reloads itself
// from disk. A single failed append, fsync or cursor write used to latch the
// outbox broken until the process restarted, withholding every attributed
// response on the gateway meanwhile (bug hunt H103).
var recoverAfter = 5 * time.Second

// failLocked marks the outbox broken. torn says the failure may have left
// lines past durableEnd whose producers were told the write failed; recovery
// cuts them off so a withheld response is never billed.
func (q *Outbox) failLocked(err error, torn bool) error {
	if q.broken == nil {
		q.brokenAt = time.Now()
		if torn {
			q.tornAt = q.durableEnd
		}
	}
	q.broken = err
	return err
}

// recoverLocked reloads a broken outbox from disk once recoverAfter has
// passed, first cutting off lines that were never durable.
func (q *Outbox) recoverLocked() {
	if q.broken == nil || q.flushing || time.Since(q.brokenAt) < recoverAfter {
		return
	}
	if q.tornAt >= 0 {
		if err := truncateSync(q.dataPath, q.tornAt); err != nil {
			q.brokenAt = time.Now()
			return
		}
	}
	fresh := &Outbox{root: q.root, dataPath: q.dataPath, cursorPath: q.cursorPath, maxBytes: q.maxBytes, tornAt: -1}
	if err := fresh.load(); err != nil {
		q.broken, q.brokenAt = err, time.Now()
		return
	}
	q.offset, q.end, q.durableEnd, q.pending = fresh.offset, fresh.end, fresh.durableEnd, fresh.pending
	q.broken, q.tornAt = nil, -1
}

func truncateSync(path string, size int64) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0o640)
	if err != nil {
		return err
	}
	if err := f.Truncate(size); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func Open(root string, maxBytes int64) (*Outbox, error) {
	if root == "" {
		return nil, errors.New("consumer usage outbox root is empty")
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("create usage outbox: %w", err)
	}
	q := &Outbox{root: root, dataPath: filepath.Join(root, "events.jsonl"), cursorPath: filepath.Join(root, "cursor.json"), maxBytes: maxBytes, tornAt: -1}
	q.cond = sync.NewCond(&q.mu)
	if err := q.load(); err != nil {
		return nil, err
	}
	return q, nil
}

func (q *Outbox) load() error {
	old, err := os.Stat(q.dataPath + ".old")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat usage outbox backup: %w", err)
	}
	if old != nil {
		if _, err := os.Stat(q.dataPath); errors.Is(err, os.ErrNotExist) {
			if err := os.Rename(q.dataPath+".old", q.dataPath); err != nil {
				return err
			}
		} else if err == nil {
			// New compacted file was installed; its cursor starts at zero.
			if err := q.saveCursor(0); err != nil {
				return err
			}
			if err := os.Remove(q.dataPath + ".old"); err != nil {
				return err
			}
		} else {
			return err
		}
	}
	if err := os.Remove(q.dataPath + ".compact"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	b, err := os.ReadFile(q.cursorPath)
	if err == nil {
		if err := json.Unmarshal(b, &q.offset); err != nil || q.offset < 0 {
			return errors.New("corrupt usage outbox cursor")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.OpenFile(q.dataPath, os.O_CREATE|os.O_RDWR, 0o640)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := syncDir(q.root); err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if q.offset > info.Size() {
		if info.Size() != 0 {
			return errors.New("usage outbox cursor past data")
		}
		q.offset = 0
		if err := q.saveCursor(0); err != nil {
			return err
		}
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	reader := bufio.NewReader(f)
	var pos int64
	boundary := q.offset == 0
	for {
		line, readErr := reader.ReadBytes('\n')
		if errors.Is(readErr, io.EOF) {
			if len(line) > 0 {
				if err := f.Truncate(pos); err != nil {
					return err
				}
				if err := f.Sync(); err != nil {
					return err
				}
			}
			break
		}
		if readErr != nil {
			return readErr
		}
		pos += int64(len(line))
		if pos == q.offset {
			boundary = true
		}
		if pos > q.offset {
			var event Event
			if err := json.Unmarshal(line, &event); err != nil || event.EventID == "" {
				return errors.New("corrupt unacknowledged usage event")
			}
			q.pending++
		}
	}
	if !boundary || q.offset > pos {
		return errors.New("usage outbox cursor is not a record boundary")
	}
	q.end = pos
	q.durableEnd = pos
	return nil
}

// Enqueue fsyncs a single fact before returning. A full or unhealthy spool
// returns an error rather than silently discarding financially relevant usage.
func (q *Outbox) Enqueue(event Event) error {
	if event.EventID == "" || event.AccountID == "" || event.AppID == "" || event.RequestCount < 1 {
		return errors.New("invalid usage event")
	}
	b, err := json.Marshal(event)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	q.mu.Lock()
	defer q.mu.Unlock()
	q.recoverLocked()
	if q.broken != nil {
		return q.broken
	}
	if q.end-q.offset+int64(len(b)) > q.maxBytes {
		return ErrFull
	}
	f, err := os.OpenFile(q.dataPath, os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return q.failLocked(err, true)
	}
	_, err = f.Write(b)
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return q.failLocked(fmt.Errorf("append usage event: %w", err), true)
	}
	q.end += int64(len(b))
	q.pending++
	return q.waitDurableLocked(q.end)
}

// waitDurableLocked elects one producer to fsync all events appended during a
// short group-commit window. Every caller waits until its own end offset is
// durable; only the election leader sleeps, and no background goroutine leaks.
func (q *Outbox) waitDurableLocked(target int64) error {
	for q.durableEnd < target {
		if q.broken != nil {
			return q.broken
		}
		if q.flushing {
			q.cond.Wait()
			continue
		}
		q.flushing = true
		q.mu.Unlock()
		time.Sleep(groupCommitDelay)
		q.mu.Lock()
		if q.broken == nil {
			f, err := os.OpenFile(q.dataPath, os.O_RDWR, 0o640)
			if err == nil {
				err = f.Sync()
				closeErr := f.Close()
				if err == nil {
					err = closeErr
				}
			}
			if err != nil {
				_ = q.failLocked(fmt.Errorf("sync usage outbox: %w", err), true)
			} else {
				q.durableEnd = q.end
			}
		}
		q.flushing = false
		q.cond.Broadcast()
	}
	return q.broken
}

func (q *Outbox) Next() (Item, bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.recoverLocked()
	if q.broken != nil {
		return Item{}, false, q.broken
	}
	if q.pending == 0 || q.offset >= q.durableEnd {
		return Item{}, false, nil
	}
	f, err := os.Open(q.dataPath) //nolint:forbidigo // server-owned outbox path, never a customer-supplied file
	if err != nil {
		return Item{}, false, err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Seek(q.offset, io.SeekStart); err != nil {
		return Item{}, false, err
	}
	line, err := bufio.NewReader(f).ReadBytes('\n')
	if err != nil {
		return Item{}, false, err
	}
	var event Event
	if err := json.Unmarshal(line, &event); err != nil {
		return Item{}, false, err
	}
	return Item{Event: event, start: q.offset, end: q.offset + int64(len(line))}, true, nil
}

// Ack is called only after a positive apid receipt. A crash between receipt
// and cursor fsync replays the same event ID, which the database deduplicates.
func (q *Outbox) Ack(item Item) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.broken != nil {
		return q.broken
	}
	if item.start != q.offset || item.end > q.durableEnd || item.end <= item.start {
		return errors.New("stale usage outbox acknowledgement")
	}
	if err := q.saveCursor(item.end); err != nil {
		return q.failLocked(err, false)
	}
	q.offset = item.end
	q.pending--
	if q.pending == 0 {
		if err := os.Truncate(q.dataPath, 0); err != nil {
			return q.failLocked(err, false)
		}
		q.end = 0
		q.durableEnd = 0
		if err := q.saveCursor(0); err != nil {
			return q.failLocked(err, false)
		}
		q.offset = 0
	} else if q.durableEnd == q.end && !q.flushing && q.offset >= compactAfter && q.offset >= q.end/2 {
		if err := q.compact(); err != nil {
			return q.failLocked(err, false)
		}
	}
	return nil
}

func (q *Outbox) compact() error {
	in, err := os.Open(q.dataPath) //nolint:forbidigo // server-owned outbox path, never a customer-supplied file
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	if _, err := in.Seek(q.offset, io.SeekStart); err != nil {
		return err
	}
	out, err := os.OpenFile(q.dataPath+".compact", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	n, copyErr := io.Copy(out, in)
	if copyErr == nil {
		copyErr = out.Sync()
	}
	closeErr := out.Close()
	if copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return copyErr
	}
	if err := os.Rename(q.dataPath, q.dataPath+".old"); err != nil {
		return err
	}
	if err := os.Rename(q.dataPath+".compact", q.dataPath); err != nil {
		return err
	}
	if err := syncDir(q.root); err != nil {
		return err
	}
	if err := q.saveCursor(0); err != nil {
		return err
	}
	q.offset = 0
	q.end = n
	q.durableEnd = n
	if err := os.Remove(q.dataPath + ".old"); err != nil {
		return err
	}
	return syncDir(q.root)
}

// Absorb appends every unacknowledged event of the outbox at dir to q in
// one fsynced write, then empties dir. A hand-off's old generation leaves its
// outbox in its own directory (ADR-1736); the generation that adopts it
// delivers those events. A crash between the two steps replays event IDs,
// which apid deduplicates. ErrFull leaves dir untouched.
func (q *Outbox) Absorb(dir string) (int64, error) {
	src, err := Open(dir, DefaultMaxBytes)
	if err != nil {
		return 0, fmt.Errorf("open usage outbox %s: %w", dir, err)
	}
	if src.pending > 0 {
		if err := q.appendFrom(src); err != nil {
			return 0, err
		}
	}
	for _, name := range []string{src.dataPath, src.cursorPath} {
		if err := os.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			return src.pending, err
		}
	}
	return src.pending, syncDir(dir)
}

func (q *Outbox) appendFrom(src *Outbox) error {
	in, err := os.Open(src.dataPath) //nolint:forbidigo // server-owned outbox path, never a customer-supplied file
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	b := make([]byte, src.durableEnd-src.offset)
	if _, err := in.ReadAt(b, src.offset); err != nil {
		return fmt.Errorf("read usage outbox %s: %w", src.root, err)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.recoverLocked()
	if q.broken != nil {
		return q.broken
	}
	if q.end-q.offset+int64(len(b)) > q.maxBytes {
		return ErrFull
	}
	f, err := os.OpenFile(q.dataPath, os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return q.failLocked(err, true)
	}
	_, err = f.Write(b)
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return q.failLocked(fmt.Errorf("append adopted usage events: %w", err), true)
	}
	q.end += int64(len(b))
	q.pending += src.pending
	return q.waitDurableLocked(q.end)
}

// Quarantine appends item's event, with the receiver's reason, to
// quarantine.jsonl beside the outbox and acknowledges it, so an event the
// receiver will never accept stops blocking every later one. The file keeps
// the event for reconciliation (bug hunt 2026-10-28 H38).
func (q *Outbox) Quarantine(item Item, reason string) error {
	line, err := json.Marshal(struct {
		Reason        string    `json:"reason"`
		QuarantinedAt time.Time `json:"quarantined_at"`
		Event         Event     `json:"event"`
	}{reason, time.Now().UTC(), item.Event})
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(q.root, "quarantine.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return q.Ack(item)
}

func (q *Outbox) Stats() Stats {
	q.mu.Lock()
	defer q.mu.Unlock()
	return Stats{PendingRecords: q.pending, PendingBytes: q.end - q.offset, CapacityBytes: q.maxBytes}
}

func (q *Outbox) Health() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.recoverLocked()
	return q.broken
}

func (q *Outbox) saveCursor(offset int64) error {
	b, err := json.Marshal(offset)
	if err != nil {
		return err
	}
	tmp := q.cursorPath + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, q.cursorPath); err != nil {
		return err
	}
	return syncDir(q.root)
}

func syncDir(path string) error {
	d, err := os.Open(path) //nolint:forbidigo // server-owned outbox directory opened only for fsync
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}
