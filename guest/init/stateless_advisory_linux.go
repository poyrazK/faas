//go:build linux

// Stateless-runtime advisory (spec §17 G13, ADR-047 as amended 2026-11-12).
// Closes the runtime half of the Wave 0 stateless-only contract: PR-A gates
// persistence at deploy-accept (422 stateless_only_violation); this observes
// writes at runtime so the customer sees an audit row before the data is
// lost on the next park.
//
// Wire path: inotify on the watched directories → per-path de-dup → one
// guest-initiated AF_VSOCK STREAM per batch (host CID 2, port 1025,
// msg_type 2) → Firecracker's <vsock.sock>_1025 → vmmd → stateless.advisory
// audit row.
//
// The ADR-047 original used fanotify mount marks and vsock DGRAM. Neither
// worked: FAN_CREATE/FAN_MOVE need an FID-reporting group and mount marks
// refuse them (fanotify_mark answered EINVAL for every path in every guest),
// and Firecracker's vsock carries only streams. inotify watches only the
// directories that matter, so writes elsewhere cost nothing.
//
// Advisory only, never blocking. A dropped advisory is silent (ADR-035:
// audit rows are observation, not source of truth).

package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// Wire constants. pkg/fcvm VsockStatelessAdvisoryHostPort and
// StatelessAdvisoryMsgType mirror them on the host.
const (
	VsockStatelessAdvisoryPort    uint32 = 1025
	VsockStatelessAdvisoryMsgType uint32 = 2
)

// Sizing + tuning.
const (
	// advisoryBatchCap caps one frame at 64 events.
	advisoryBatchCap = 64

	// advisoryMaxBody is the largest JSON body per frame (8 KiB); the host
	// refuses anything larger.
	advisoryMaxBody = 8 * 1024

	// advisoryDeDupWindow is how long the shipper collects after the first
	// pending write, so a burst becomes one frame.
	advisoryDeDupWindow = 1 * time.Second

	// advisoryReportedPathCap bounds the distinct paths one guest reports in
	// its lifetime. Each path is reported once: the audit row says the data
	// will not survive, and repeating it on every write would only flood
	// the audit table.
	advisoryReportedPathCap = 256

	// advisoryWatchCap bounds inotify watches (one per directory).
	advisoryWatchCap = 1024

	// advisoryReadBuf holds many inotify events (16 bytes plus a name each).
	advisoryReadBuf = 64 * 1024

	// advisorySendTimeout bounds one frame's connect and write.
	advisorySendTimeout = time.Second

	// advisoryMaxPath is the longest path the host accepts.
	advisoryMaxPath = 1024
)

// inotify masks. Tree directories report writes; parent directories only
// notice a watched root appearing (or being recreated) so it can be armed.
const (
	advisoryTreeMask   = unix.IN_CREATE | unix.IN_MODIFY | unix.IN_MOVED_TO | unix.IN_ONLYDIR
	advisoryParentMask = unix.IN_CREATE | unix.IN_MOVED_TO | unix.IN_ONLYDIR
	// advisoryQuietTreeMask drops IN_MODIFY once a directory has reported:
	// a database file rewritten on every request would otherwise wake
	// guest-init on every write after its one report.
	advisoryQuietTreeMask = unix.IN_CREATE | unix.IN_MOVED_TO | unix.IN_ONLYDIR
)

// statelessRuntimePaths is the closed set of absolute state-shaped
// directories. Mirrors the deploy-accept check's top-level data/ and db/
// and extends to well-known daemon data directories so a customer who runs
// `apt install postgresql` without a VOLUME directive still trips the audit
// row. advisoryWatchRoots adds the app's own data/ and db/.
var statelessRuntimePaths = []string{
	"/data",
	"/db",
	"/var/lib/postgresql",
	"/var/lib/redis",
	"/var/lib/mysql",
	"/var/lib/mongodb",
	"/var/lib/mongo",
	"/var/lib/cockroach",
	"/var/lib/cassandra",
	"/var/lib/clickhouse",
}

// advisoryWatchRoots is the closed set plus data/ and db/ under the app's
// working directory: the source-root directories the deploy-accept scan
// inspects, and the ones a buildpack app (running as an unprivileged user
// that cannot create /data) can actually write (Hunt 34 H34-3).
func advisoryWatchRoots(workingDir string) []string {
	if workingDir == "" || !filepath.IsAbs(workingDir) {
		workingDir = "/"
	}
	workingDir = filepath.Clean(workingDir)
	seen := make(map[string]bool)
	var out []string
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, p := range statelessRuntimePaths {
		add(p)
	}
	add(filepath.Join(workingDir, "data"))
	add(filepath.Join(workingDir, "db"))
	return out
}

// advisoryEvent is one observed write; one entry in the frame's events.
type advisoryEvent struct {
	Path   string   `json:"path"`
	Masks  []string `json:"mask"` // canonical verbs: create, modify, move, other
	TsUnix int64    `json:"ts_unix_ms"`
}

// advisoryBatch is the JSON body of one frame. The host takes the app from
// the vsock listener, never from the frame.
type advisoryBatch struct {
	Events []advisoryEvent `json:"events"`
}

// inotifyMaskNames maps an inotify event mask to the canonical verbs.
func inotifyMaskNames(mask uint32) []string {
	var out []string
	if mask&unix.IN_CREATE != 0 {
		out = append(out, "create")
	}
	if mask&unix.IN_MODIFY != 0 {
		out = append(out, "modify")
	}
	if mask&unix.IN_MOVED_TO != 0 {
		out = append(out, "move")
	}
	if len(out) == 0 {
		out = []string{"other"}
	}
	return out
}

// runStatelessAdvisory arms the watches and starts the reader and the
// shipper. An error is warn-logged by the caller: a guest without inotify
// or vsock still boots — the contract is "no signal", not "won't boot".
//
// Lifecycle: boot() calls this once, after reading the app manifest and
// before the supervisor starts the app, so the app's first write is seen.
func runStatelessAdvisory(log *slog.Logger, workingDir string) error {
	if log == nil {
		log = slog.Default()
	}
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC)
	if err != nil {
		return fmt.Errorf("inotify_init1: %w", err)
	}
	w := newAdvisoryWatcher(fd, log)
	roots := advisoryWatchRoots(workingDir)
	for _, root := range roots {
		w.watchRoot(root)
	}
	pipe := newAdvisoryPipe()
	go w.run(pipe)
	go advisoryShipper(pipe, sendAdvisoryFrame, log)
	log.Info("stateless advisory started", "roots", len(roots), "watches", len(w.dirs), "vsock_port", VsockStatelessAdvisoryPort)
	return nil
}

// advisoryWatchDir is what one inotify watch descriptor stands for.
type advisoryWatchDir struct {
	path string
	// tree marks a directory inside a watched root: writes in it report.
	tree bool
	// quiet is set once the directory has reported and IN_MODIFY is off.
	quiet bool
	// roots maps a child name to the watched root it would become.
	roots map[string]string
}

// advisoryWatcher owns the inotify fd. Only the reader goroutine touches it
// after runStatelessAdvisory returns.
type advisoryWatcher struct {
	fd       int
	log      *slog.Logger
	dirs     map[int32]*advisoryWatchDir
	capped   bool
	overflow bool
}

func newAdvisoryWatcher(fd int, log *slog.Logger) *advisoryWatcher {
	return &advisoryWatcher{fd: fd, log: log, dirs: make(map[int32]*advisoryWatchDir)}
}

// watch adds mask to path's watch and returns its record, or nil when the
// path is not a directory, is gone, or the watch cap is reached.
func (w *advisoryWatcher) watch(path string, mask uint32) *advisoryWatchDir {
	if len(w.dirs) >= advisoryWatchCap {
		if !w.capped {
			w.capped = true
			w.log.Warn("stateless advisory: watch cap reached; deeper directories are not watched", "cap", advisoryWatchCap)
		}
		return nil
	}
	wd, err := unix.InotifyAddWatch(w.fd, path, mask|unix.IN_MASK_ADD)
	if err != nil {
		return nil
	}
	dir := w.dirs[int32(wd)]
	if dir == nil {
		dir = &advisoryWatchDir{path: path}
		w.dirs[int32(wd)] = dir
	}
	return dir
}

// watchRoot watches root's parent, so a root created (or recreated) later is
// armed when it appears, and every directory already under root.
func (w *advisoryWatcher) watchRoot(root string) {
	if parent := w.watch(filepath.Dir(root), advisoryParentMask); parent != nil {
		if parent.roots == nil {
			parent.roots = make(map[string]string)
		}
		parent.roots[filepath.Base(root)] = root
	}
	w.watchTree(root)
}

// watchTree watches root and the directories below it. Symlinks below the
// root are not followed; a symlinked root itself is (inotify follows it).
func (w *advisoryWatcher) watchTree(root string) {
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == root {
				return err
			}
			return nil
		}
		if p != root && !d.IsDir() {
			return nil
		}
		dir := w.watch(p, advisoryTreeMask)
		switch {
		case dir != nil:
			dir.tree = true
			return nil
		case w.capped:
			return fs.SkipAll
		case p != root:
			return fs.SkipDir
		default:
			return nil
		}
	})
}

// run reads events until the fd fails. The read blocks; guest-init has no
// other use for this goroutine.
func (w *advisoryWatcher) run(pipe *advisoryPipe) {
	buf := make([]byte, advisoryReadBuf)
	for {
		n, err := unix.Read(w.fd, buf)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			w.log.Warn("stateless advisory: inotify read failed; stopping", "err", err)
			return
		}
		w.handle(buf[:n], time.Now(), pipe.add)
	}
}

// handle decodes a read's worth of inotify_event records.
func (w *advisoryWatcher) handle(buf []byte, now time.Time, emit func(advisoryEvent)) {
	for off := 0; off+unix.SizeofInotifyEvent <= len(buf); {
		wd := int32(binary.NativeEndian.Uint32(buf[off:]))
		mask := binary.NativeEndian.Uint32(buf[off+4:])
		nameLen := int(binary.NativeEndian.Uint32(buf[off+12:]))
		end := off + unix.SizeofInotifyEvent + nameLen
		if end > len(buf) {
			return
		}
		name := buf[off+unix.SizeofInotifyEvent : end]
		if i := bytes.IndexByte(name, 0); i >= 0 {
			name = name[:i]
		}
		off = end
		w.handleOne(wd, mask, string(name), now, emit)
	}
}

func (w *advisoryWatcher) handleOne(wd int32, mask uint32, name string, now time.Time, emit func(advisoryEvent)) {
	if mask&unix.IN_Q_OVERFLOW != 0 {
		if !w.overflow {
			w.overflow = true
			w.log.Warn("stateless advisory: inotify queue overflowed; some writes were not observed")
		}
		return
	}
	if mask&unix.IN_IGNORED != 0 {
		// The directory is gone; its parent watch re-arms a root that
		// comes back.
		delete(w.dirs, wd)
		w.capped = false
		return
	}
	dir := w.dirs[wd]
	if dir == nil || name == "" {
		return
	}
	path := filepath.Join(dir.path, name)
	if mask&unix.IN_ISDIR != 0 {
		if mask&(unix.IN_CREATE|unix.IN_MOVED_TO) == 0 {
			return
		}
		if root, ok := dir.roots[name]; ok {
			w.watchTree(root)
		}
		if dir.tree {
			w.watchTree(path)
		}
		return
	}
	if !dir.tree {
		return
	}
	emit(advisoryEvent{Path: path, Masks: inotifyMaskNames(mask), TsUnix: now.UnixMilli()})
	if !dir.quiet {
		dir.quiet = true
		w.quiet(wd, dir)
	}
}

// quiet replaces the directory's mask with one without IN_MODIFY. A
// directory that is also a root's parent keeps its parent bits, which the
// quiet mask includes.
func (w *advisoryWatcher) quiet(wd int32, dir *advisoryWatchDir) {
	got, err := unix.InotifyAddWatch(w.fd, dir.path, advisoryQuietTreeMask)
	if err == nil && int32(got) != wd {
		// The path now names another directory; leave it alone.
		_, _ = unix.InotifyRmWatch(w.fd, uint32(got))
	}
}

// advisoryPipe collects pending writes for the shipper and remembers which
// paths were already reported. The reader calls add; the shipper drains.
type advisoryPipe struct {
	mu       sync.Mutex
	cond     *sync.Cond
	closed   bool
	pending  map[string]*advisoryEvent
	reported map[string]struct{}
}

func newAdvisoryPipe() *advisoryPipe {
	p := &advisoryPipe{
		pending:  make(map[string]*advisoryEvent),
		reported: make(map[string]struct{}),
	}
	p.cond = sync.NewCond(&p.mu)
	return p
}

// add records one write. A path already reported, or one past the lifetime
// cap, is dropped; repeat writes to a pending path merge their verbs.
func (p *advisoryPipe) add(ev advisoryEvent) {
	// The host refuses a frame with an overlong or non-UTF-8 path, which
	// would lose the whole batch with it.
	if len(ev.Path) > advisoryMaxPath {
		return
	}
	ev.Path = strings.ToValidUTF8(ev.Path, "\uFFFD")
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	if _, done := p.reported[ev.Path]; done {
		return
	}
	if cur := p.pending[ev.Path]; cur != nil {
		cur.Masks = mergeAdvisoryVerbs(cur.Masks, ev.Masks)
		return
	}
	if len(p.pending)+len(p.reported) >= advisoryReportedPathCap {
		return
	}
	p.pending[ev.Path] = &ev
	p.cond.Broadcast()
}

// drain returns every pending write, sorted by path, and marks the paths
// reported.
func (p *advisoryPipe) drain() []advisoryEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]advisoryEvent, 0, len(p.pending))
	for path, ev := range p.pending {
		out = append(out, *ev)
		p.reported[path] = struct{}{}
	}
	p.pending = make(map[string]*advisoryEvent)
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// waitPending blocks until something is pending; false once closed.
func (p *advisoryPipe) waitPending() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for len(p.pending) == 0 && !p.closed {
		p.cond.Wait()
	}
	return !p.closed
}

func (p *advisoryPipe) close() {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	p.cond.Broadcast()
}

func mergeAdvisoryVerbs(have, more []string) []string {
	for _, m := range more {
		found := false
		for _, h := range have {
			if h == m {
				found = true
				break
			}
		}
		if !found {
			have = append(have, m)
		}
	}
	return have
}

// advisoryShipper waits for a write, collects for advisoryDeDupWindow, then
// ships everything pending in frames of at most advisoryBatchCap events.
func advisoryShipper(pipe *advisoryPipe, send func([]byte) error, log *slog.Logger) {
	for pipe.waitPending() {
		time.Sleep(advisoryDeDupWindow)
		events := pipe.drain()
		for len(events) > 0 {
			batch := events[:min(len(events), advisoryBatchCap)]
			body, sent, err := marshalAdvisoryBatchWithinLimit(batch, advisoryMaxBody)
			switch {
			case err != nil:
				log.Warn("stateless advisory marshal failed; dropping batch", "err", err, "events", len(batch))
				sent = len(batch)
			case sent == 0:
				log.Warn("stateless advisory event exceeds the frame limit; dropping", "limit", advisoryMaxBody)
				sent = 1
			default:
				if err := send(encodeAdvisoryFrame(body)); err != nil {
					// No host receiver (an older vmmd) or a full queue.
					// ADR-035: dropped, silently to the app.
					log.Warn("stateless advisory send failed", "err", err, "events", sent)
				}
			}
			events = events[sent:]
		}
	}
}

// encodeAdvisoryFrame is msg_type (uint32 BE), body length (uint32 BE), body.
func encodeAdvisoryFrame(body []byte) []byte {
	frame := make([]byte, 8+len(body))
	binary.BigEndian.PutUint32(frame[0:4], VsockStatelessAdvisoryMsgType)
	binary.BigEndian.PutUint32(frame[4:8], uint32(len(body)))
	copy(frame[8:], body)
	return frame
}

// sendAdvisoryFrame opens one guest-initiated stream per frame, as the other
// guest→host channels do; Firecracker forwards it to <vsock.sock>_1025.
func sendAdvisoryFrame(frame []byte) error {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("stateless advisory socket: %w", err)
	}
	defer func() { _ = unix.Close(fd) }()
	tv := unix.NsecToTimeval(advisorySendTimeout.Nanoseconds())
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_SNDTIMEO, &tv); err != nil {
		return fmt.Errorf("stateless advisory send timeout: %w", err)
	}
	if err := unix.Connect(fd, &unix.SockaddrVM{CID: unix.VMADDR_CID_HOST, Port: VsockStatelessAdvisoryPort}); err != nil {
		return fmt.Errorf("stateless advisory connect: %w", err)
	}
	if err := writeAllFD(fd, frame); err != nil {
		return fmt.Errorf("stateless advisory write: %w", err)
	}
	return nil
}

// marshalAdvisoryBatchWithinLimit encodes the largest prefix of events whose
// JSON document fits inside limit bytes, returning the document and the
// number of events it carries. Byte-slicing a larger document would leave
// unparseable JSON that the host drops whole. A zero-length result means even
// the first event exceeds the limit.
func marshalAdvisoryBatchWithinLimit(events []advisoryEvent, limit int) ([]byte, int, error) {
	if len(events) == 0 {
		return nil, 0, nil
	}
	// Encoded size is monotonic in the prefix length, so binary search.
	var best []byte
	bestN := 0
	lo, hi := 1, len(events)
	for lo <= hi {
		mid := (lo + hi) / 2
		body, err := json.Marshal(advisoryBatch{Events: events[:mid]})
		if err != nil {
			return nil, 0, err
		}
		if len(body) <= limit {
			best, bestN = body, mid
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	return best, bestN, nil
}
