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
	EventID            string         `json:"event_id"`
	AccountID          string         `json:"account_id"`
	AppID              string         `json:"app_id"`
	ConsumerID         string         `json:"consumer_id,omitempty"`
	PlatformTenantID   string         `json:"platform_tenant_id,omitempty"`
	WindowStart        time.Time      `json:"window_start"`
	RequestCount       int64          `json:"request_count"`
	ErrorCount         int64          `json:"error_count"`
	BillableUnits      int64          `json:"billable_units"`
	Audit              *AuditEvidence `json:"audit,omitempty"`
	DiscoveredRoute    string         `json:"discovered_route,omitempty"`
	DiscoveredAtUnixMs int64          `json:"discovered_at_unix_ms,omitempty"`
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
	q := &Outbox{root: root, dataPath: filepath.Join(root, "events.jsonl"), cursorPath: filepath.Join(root, "cursor.json"), maxBytes: maxBytes}
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
	if q.broken != nil {
		return q.broken
	}
	if q.end-q.offset+int64(len(b)) > q.maxBytes {
		return ErrFull
	}
	f, err := os.OpenFile(q.dataPath, os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		q.broken = err
		return err
	}
	_, err = f.Write(b)
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		q.broken = fmt.Errorf("append usage event: %w", err)
		return q.broken
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
				q.broken = fmt.Errorf("sync usage outbox: %w", err)
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
		q.broken = err
		return err
	}
	q.offset = item.end
	q.pending--
	if q.pending == 0 {
		if err := os.Truncate(q.dataPath, 0); err != nil {
			q.broken = err
			return err
		}
		q.end = 0
		q.durableEnd = 0
		if err := q.saveCursor(0); err != nil {
			q.broken = err
			return err
		}
		q.offset = 0
	} else if q.durableEnd == q.end && !q.flushing && q.offset >= compactAfter && q.offset >= q.end/2 {
		if err := q.compact(); err != nil {
			q.broken = err
			return err
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

func (q *Outbox) Stats() Stats {
	q.mu.Lock()
	defer q.mu.Unlock()
	return Stats{PendingRecords: q.pending, PendingBytes: q.end - q.offset, CapacityBytes: q.maxBytes}
}

func (q *Outbox) Health() error { q.mu.Lock(); defer q.mu.Unlock(); return q.broken }

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
