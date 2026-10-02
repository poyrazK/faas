// Package failureoutbox persists vmmd failure reports until schedd acknowledges
// their application. It never changes instance state or releases VM resources.
// adr: 397
package failureoutbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	Liveness       = "liveness"
	WorkloadOOM    = "workload_oom"
	workers        = 4
	maxRecordBytes = 4096
)

// Report binds an instance ID to its observing node instead of app routing.
// Live migration preserves IDs, so schedd must also recheck the source node.
// The first observation of each kind wins; redelivery preserves its metadata.
type Report struct {
	InstanceID   string `json:"instance_id"`
	SourceNodeID string `json:"source_node_id,omitempty"`
	Kind         string `json:"kind"`
	Reason       string `json:"reason,omitempty"`
	PeakMB       uint32 `json:"peak_mb,omitempty"`
	PlanMB       uint32 `json:"plan_mb,omitempty"`
	// Recovered is delivery metadata, never persisted or sent over the wire.
	// A restarted daemon must reconcile guest ownership before redelivery.
	Recovered bool `json:"-"`
}

func (r Report) validate() error {
	if r.InstanceID == "" || len(r.InstanceID) > 256 || len(r.SourceNodeID) > 256 || len(r.Reason) > 128 {
		return errors.New("failureoutbox: invalid instance ID or reason length")
	}
	if r.Kind != Liveness && r.Kind != WorkloadOOM {
		return errors.New("failureoutbox: unknown report kind")
	}
	return nil
}

func (r Report) key() string {
	sum := sha256.Sum256([]byte(r.Kind + "\x00" + r.InstanceID))
	return hex.EncodeToString(sum[:])
}

type record struct {
	Version    int       `json:"version"`
	Report     Report    `json:"report"`
	ObservedAt time.Time `json:"observed_at"`
}

type entry struct {
	record
	durable  bool
	inflight bool
	attempt  int
	next     time.Time
}

// Sender returns nil only for an affirmative application acknowledgement.
// All failures, including NotFound and a negative acknowledgement, are retried.
type Sender func(context.Context, Report) error

type Outbox struct {
	mu      sync.Mutex
	root    string
	lock    *os.File
	pending map[string]*entry
	send    Sender
	log     *slog.Logger
	wake    chan struct{}
	cancel  context.CancelFunc
	done    chan struct{}
	closed  bool
	started bool
	// Fixed production defaults; shortened only by package tests.
	retryBase      time.Duration
	retryMax       time.Duration
	attemptTimeout time.Duration
}

// Open locks the private persistent directory and loads every committed record.
// Corruption or an unusable spool fails startup instead of silently losing work.
func Open(root string, send Sender, log *slog.Logger) (*Outbox, error) {
	if root == "" || send == nil {
		return nil, errors.New("failureoutbox: root and sender required")
	}
	if log == nil {
		log = slog.Default()
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("failureoutbox: mkdir: %w", err)
	}
	info, err := os.Lstat(root)
	if err != nil {
		return nil, fmt.Errorf("failureoutbox: stat: %w", err)
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("failureoutbox: root must be a private directory (0700)")
	}
	lock, err := os.OpenFile(filepath.Join(root, ".lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("failureoutbox: open lock: %w", err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("failureoutbox: acquire exclusive lock: %w", err)
	}
	o := &Outbox{root: root, lock: lock, pending: make(map[string]*entry), send: send, log: log,
		wake: make(chan struct{}, 1), retryBase: time.Second, retryMax: time.Minute, attemptTimeout: 20 * time.Second}
	if err := o.load(); err != nil {
		_ = o.Close()
		return nil, err
	}
	if err := o.syncDir(); err != nil {
		_ = o.Close()
		return nil, err
	}
	// Persist the spool directory's own entry if Open just created it. Its
	// parent hierarchy is operator/systemd-provisioned persistent storage.
	if err := syncDirectory(filepath.Dir(root)); err != nil {
		_ = o.Close()
		return nil, err
	}
	return o, nil
}

func (o *Outbox) load() error {
	files, err := os.ReadDir(o.root)
	if err != nil {
		return fmt.Errorf("failureoutbox: list: %w", err)
	}
	for _, file := range files {
		name := file.Name()
		if strings.HasPrefix(name, ".pending-") {
			if err := os.Remove(filepath.Join(o.root, name)); err != nil {
				return fmt.Errorf("failureoutbox: remove incomplete write: %w", err)
			}
			continue
		}
		if name == ".lock" {
			continue
		}
		if !strings.HasSuffix(name, ".json") || !file.Type().IsRegular() {
			return fmt.Errorf("failureoutbox: unexpected spool entry %q", name)
		}
		if err := o.loadRecord(name); err != nil {
			return err
		}
	}
	return nil
}

func (o *Outbox) loadRecord(name string) error {
	f, err := os.Open(filepath.Join(o.root, name)) //nolint:forbidigo // daemon-owned private spool; ReadDir rejected symlinks/nonregular entries and the hash identity is checked below.
	if err != nil {
		return fmt.Errorf("failureoutbox: read record: %w", err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("failureoutbox: stat record: %w", err)
	}
	if info.Size() > maxRecordBytes || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("failureoutbox: oversized or nonprivate record %s", name)
	}
	dec := json.NewDecoder(io.LimitReader(f, maxRecordBytes+1))
	dec.DisallowUnknownFields()
	var r record
	if err := dec.Decode(&r); err != nil {
		return fmt.Errorf("failureoutbox: decode %s: %w", name, err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("failureoutbox: trailing data in %s", name)
	}
	if err := r.Report.validate(); err != nil {
		return err
	}
	if r.Version != 1 || r.ObservedAt.IsZero() || name != r.Report.key()+".json" {
		return fmt.Errorf("failureoutbox: invalid record identity/version %s", name)
	}
	r.Report.Recovered = true
	o.pending[r.Report.key()] = &entry{record: r, durable: true}
	return nil
}

// Enqueue synchronously commits before delivery. On a storage error the entry
// also remains in memory for persistence retries; crash durability then requires
// storage to recover before vmmd exits. It never starts an unbounded goroutine.
func (o *Outbox) Enqueue(r Report) error {
	if err := r.validate(); err != nil {
		return err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return errors.New("failureoutbox: closed")
	}
	key := r.key()
	e := o.pending[key]
	if e == nil {
		e = &entry{record: record{Version: 1, Report: r, ObservedAt: time.Now().UTC()}}
		o.pending[key] = e
	}
	var err error
	if !e.durable {
		err = o.persist(e.record)
		e.durable = err == nil
	}
	select {
	case o.wake <- struct{}{}:
	default:
	}
	return err
}

func (o *Outbox) persist(r record) error {
	body, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("failureoutbox: encode: %w", err)
	}
	f, err := os.CreateTemp(o.root, ".pending-")
	if err != nil {
		return fmt.Errorf("failureoutbox: create record: %w", err)
	}
	defer func() { _ = f.Close(); _ = os.Remove(f.Name()) }()
	if _, err := f.Write(body); err != nil {
		return fmt.Errorf("failureoutbox: write record: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("failureoutbox: sync record: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("failureoutbox: close record: %w", err)
	}
	if err := os.Rename(f.Name(), filepath.Join(o.root, r.Report.key()+".json")); err != nil {
		return fmt.Errorf("failureoutbox: commit record: %w", err)
	}
	return o.syncDir()
}

func (o *Outbox) syncDir() error {
	return syncDirectory(o.root)
}

func syncDirectory(path string) error {
	f, err := os.Open(path) //nolint:forbidigo // fsync a validated daemon spool directory or its operator-provisioned parent; no customer file is read.
	if err != nil {
		return fmt.Errorf("failureoutbox: open directory: %w", err)
	}
	defer func() { _ = f.Close() }()
	if err := f.Sync(); err != nil {
		return fmt.Errorf("failureoutbox: sync directory: %w", err)
	}
	return nil
}

// Start roots delivery in the daemon lifecycle, independently of guest probes.
func (o *Outbox) Start(ctx context.Context) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.started || o.closed {
		return
	}
	o.started = true
	ctx, o.cancel = context.WithCancel(ctx)
	o.done = make(chan struct{})
	go func() { defer close(o.done); o.run(ctx) }()
}

type result struct {
	key string
	err error
}

func (o *Outbox) run(ctx context.Context) {
	jobs := make(chan record, workers)
	results := make(chan result, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for {
				select {
				case <-ctx.Done():
					return
				case r := <-jobs:
					attemptCtx, cancel := context.WithTimeout(ctx, o.attemptTimeout)
					err := o.send(attemptCtx, r.Report)
					cancel()
					select {
					case results <- result{r.Report.key(), err}:
					case <-ctx.Done():
						return
					}
				}
			}
		})
	}
	defer wg.Wait()
	tick := time.NewTicker(o.retryBase / 2)
	defer tick.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		o.dispatch(jobs)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-o.wake:
		case r := <-results:
			o.complete(r)
		}
	}
}

func (o *Outbox) dispatch(jobs chan<- record) {
	o.mu.Lock()
	defer o.mu.Unlock()
	var due []*entry
	active := 0
	now := time.Now()
	for _, e := range o.pending {
		if e.inflight {
			active++
			continue
		}
		if !e.next.After(now) {
			due = append(due, e)
		}
	}
	sort.Slice(due, func(i, j int) bool { return due[i].next.Before(due[j].next) })
	for _, e := range due {
		if active >= workers {
			break
		}
		if !e.durable {
			if err := o.persist(e.record); err != nil {
				o.retry(e, err)
				continue
			}
			e.durable = true
		}
		e.inflight = true
		jobs <- e.record
		active++
	}
}

func (o *Outbox) complete(r result) {
	o.mu.Lock()
	defer o.mu.Unlock()
	e := o.pending[r.key]
	e.inflight = false
	if r.err == nil {
		r.err = os.Remove(filepath.Join(o.root, r.key+".json"))
		if r.err == nil || errors.Is(r.err, os.ErrNotExist) {
			r.err = o.syncDir()
			if r.err != nil {
				e.durable = false
			}
		}
	}
	if r.err != nil {
		o.retry(e, r.err)
		return
	}
	delete(o.pending, r.key)
	o.log.Info("vmmd: failure report acknowledged", "instance_id", e.Report.InstanceID, "kind", e.Report.Kind)
}

func (o *Outbox) retry(e *entry, err error) {
	e.attempt++
	delay := o.retryBase
	for n := 1; n < e.attempt && delay < o.retryMax; n++ {
		delay = min(delay*2, o.retryMax)
	}
	// Stable per-instance jitter spreads fleet retries without a shared RNG.
	sum := sha256.Sum256([]byte(e.Report.key()))
	delay = min(delay+time.Duration(sum[0])*delay/1024, o.retryMax)
	e.next = time.Now().Add(delay)
	o.log.Warn("vmmd: failure report pending", "instance_id", e.Report.InstanceID,
		"kind", e.Report.Kind, "attempt", e.attempt, "retry_delay", delay, "err", err)
}

func (o *Outbox) Pending() int { o.mu.Lock(); defer o.mu.Unlock(); return len(o.pending) }

// Close cancels and joins bounded RPC workers before releasing the spool lock.
// Pending committed files deliberately survive shutdown.
func (o *Outbox) Close() error {
	o.mu.Lock()
	o.closed = true
	if o.cancel != nil {
		o.cancel()
	}
	done := o.done
	o.mu.Unlock()
	if done != nil {
		<-done
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.lock == nil {
		return nil
	}
	err := o.lock.Close()
	o.lock = nil
	return err
}
