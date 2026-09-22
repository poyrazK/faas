//go:build linux

// Stateless-runtime advisory (spec §17 G13, ADR-047). Closes the
// runtime half of the Wave 0 stateless-only contract — PR-A gates
// persistence at deploy-accept (422 stateless_only_violation); PR-C
// observes persistence at runtime and ships an audit row.
//
// Wire path: kernel fanotify → in-process debounce → AF_VSOCK DGRAM
// (port=1025, msg_type=2, host CID=2) → vmmd → /run/faas/apid.sock
// gRPC → apid pkg/audit.Auditor.Emit("stateless.advisory", ...).
//
// Advisory only, never blocking. A dropped advisory is silent
// (ADR-035: audit rows are observation, not source of truth). EROFS
// / read-only remount is explicitly out of scope for Wave 0.

package main

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Wire constants. Distinct from the resume listener (port 1024,
// msg_type 1) so the host parser can route by prefix.
const (
	// VsockStatelessAdvisoryPort differs from VsockResumePort=1024 so
	// the resume listener and the advisory listener can run side-by-
	// side. vmmd's wire receiver (pkg/fcvm/vmm.go::SendStatelessAdvisory)
	// listens for this port.
	VsockStatelessAdvisoryPort uint32 = 1025

	// VsockStatelessAdvisoryMsgType is the wire-format message-type
	// prefix (uint32 big-endian, first 4 bytes of the datagram). Differs
	// from VsockResumeMsgType=1 so a prefix collision is impossible.
	VsockStatelessAdvisoryMsgType uint32 = 2
)

// Sizing + tuning.
const (
	// advisoryBatchCap caps a single DGRAM payload at 64 events. A noisy
	// app (writes /data on every request) is debounced to ~1/s in the
	// guest before this cap ever fires.
	advisoryBatchCap = 64

	// advisoryMaxBody is the largest JSON body we'll serialise per DGRAM
	// (8 KiB). At ~120 bytes/event, 64 events fit comfortably.
	advisoryMaxBody = 8 * 1024

	// advisoryDeDupWindow collapses repeat events on the same path into
	// a single advisory row within this window. ADR-035 — observation,
	// not source of truth — so a one-second de-dup is enough to keep
	// the events table quiet without hiding distinct state shapes.
	advisoryDeDupWindow = 1 * time.Second

	// advisoryFanotifyEventBuf is the read buffer for the fanotify fd.
	// 4 KiB is plenty — each event is ~24 bytes plus the path tail.
	advisoryFanotifyEventBuf = 4 * 1024
)

// statelessRuntimePaths is the closed set of guest-side paths the
// fanotify mark observes. Mirrors cmd/apid/deploy_inputs.go's
// statefulTopLevelDirs (/data, /db) and extends to well-known daemon
// data directories so a customer who runs `apt install postgresql`
// without a VOLUME directive still trips the audit row.
//
// Wave 1 follow-up: dynamic discovery via a small admin path-list if
// customer telemetry shows the closed set is missing real workloads.
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

// advisoryEvent is one observed write. Wire-formatted as a single
// entry in the events array of the DGRAM payload (see advisoryBatch).
type advisoryEvent struct {
	Path   string   `json:"path"`
	Masks  []string `json:"mask"` // canonical verb names
	PID    int      `json:"pid"`
	TsUnix int64    `json:"ts_unix_ms"`
}

// advisoryBatch is the JSON body of one DGRAM datagram. The app_id
// comes from /etc/faas/app.json (guest-init already opened it in
// boot() to read the manifest; we re-read to keep this file standalone).
type advisoryBatch struct {
	AppID  string          `json:"app_id"`
	Events []advisoryEvent `json:"events"`
}

// maskNames returns the canonical verb names for a fanotify event
// mask. Mirrors the kernel's FAN_CREATE/FAN_MODIFY/FAN_MOVE/FAN_DELETE
// flag set; anything we don't recognise is bucketed into "other".
func maskNames(mask uint64) []string {
	var out []string
	if mask&unix.FAN_CREATE != 0 {
		out = append(out, "create")
	}
	if mask&unix.FAN_MODIFY != 0 {
		out = append(out, "modify")
	}
	if mask&unix.FAN_MOVE != 0 {
		out = append(out, "move")
	}
	if mask&unix.FAN_ACCESS != 0 {
		out = append(out, "access")
	}
	if mask&unix.FAN_DELETE != 0 {
		out = append(out, "delete")
	}
	if len(out) == 0 {
		out = []string{"other"}
	}
	return out
}

// runStatelessAdvisory starts the fanotify mark + vsock DGRAM shipper.
// Returns are tolerated (warn-log only) so a guest built without
// CONFIG_FANOTIFY=y, or on a kernel where AF_VSOCK is missing, still
// boots — the platform contract is "no signal" not "won't boot".
//
// Lifecycle: caller (boot()) invokes this once, after pivotInto and
// after listenResumeHook, before the supervisor starts the app.
func runStatelessAdvisory(log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}

	// 1. fanotify_init. FAN_CLASS_NOTIF means we observe events, never
	// consume them (the alternative — FAN_CLASS_CONTENT or FAN_CLASS_PRE
	// CONTENT — would require a response from us and is out of scope).
	fd, err := unix.FanotifyInit(
		unix.FAN_CLOEXEC|unix.FAN_NONBLOCK,
		unix.O_RDONLY|unix.O_LARGEFILE|unix.O_CLOEXEC,
	)
	if err != nil {
		return fmt.Errorf("fanotify_init: %w", err)
	}

	// 2. FAN_MARK_MOUNT on each closed-set path. Errors are warn-logged
	// not fatal — a guest without /var/lib/cockroach still gets a mark
	// on /data and /db.
	markMask := uint64(unix.FAN_CREATE | unix.FAN_MODIFY | unix.FAN_MOVE | unix.FAN_EVENT_ON_CHILD)
	for _, p := range statelessRuntimePaths {
		if err := unix.FanotifyMark(
			fd,
			unix.FAN_MARK_ADD|unix.FAN_MARK_MOUNT,
			markMask,
			unix.AT_FDCWD,
			p,
		); err != nil {
			log.Warn("stateless advisory fanotify_mark failed (non-fatal)", "path", p, "err", err)
		}
	}

	// 3. AF_VSOCK DGRAM. Port-bind, not connect — DGRAM is fire-and-
	// forget; vmmd listens on the host CID (VMADDR_CID_HOST) at this
	// port.
	sock, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		_ = unix.Close(fd)
		return fmt.Errorf("vsock socket: %w", err)
	}
	if err := unix.Bind(sock, &unix.SockaddrVM{
		CID:  unix.VMADDR_CID_ANY,
		Port: VsockStatelessAdvisoryPort,
	}); err != nil {
		_ = unix.Close(sock)
		_ = unix.Close(fd)
		return fmt.Errorf("vsock bind: %w", err)
	}

	// 4. Read the app_id once at boot — the manifest is guest-static
	// (built by imaged before the microVM starts) and won't change.
	appID, err := readAppIDFromManifest()
	if err != nil {
		// Tolerate a missing manifest — we can still ship advisories
		// with app_id="" and the host will look up by app_id from the
		// vsock port context (future: vmmd stamps instance+app on the
		// datagram envelope; today we fall back to the manifest).
		log.Warn("stateless advisory: app.json missing or unreadable", "err", err)
	}

	// 5. Two goroutines: readFanotify drains the fd, advisoryShipper
	// batches + ships over vsock. A sync.Mutex protects the pending
	// batch; a sync.Cond wakes the shipper when something arrives or
	// the debounce window elapses.
	pipe := newAdvisoryPipe(log)

	go func() {
		defer func() { _ = unix.Close(fd) }()
		readFanotify(fd, pipe, log)
	}()
	go func() {
		defer func() { _ = unix.Close(sock) }()
		advisoryShipper(sock, appID, pipe, log)
	}()

	log.Info("stateless advisory started", "paths", len(statelessRuntimePaths), "vsock_port", VsockStatelessAdvisoryPort)
	return nil
}

// readAppIDFromManifest reads /etc/faas/app.json and pulls the
// app_id field. The manifest shape is defined in pkg/api (api.AppManifest).
// We tolerate any parse error so a missing app.json doesn't kill the
// advisory goroutine.
func readAppIDFromManifest() (string, error) {
	//nolint:forbidigo // Vetted-id path: /etc/faas/app.json is injected
	// by imaged at guest-init injection time (see pkg/imaged/inject.go)
	// and is the only path the guest reads at runtime. No customer
	// input ever reaches this line; the openCustomerFile guard is for
	// code paths that handle customer-controlled paths.
	f, err := os.Open("/etc/faas/app.json")
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	var m struct {
		AppID string `json:"app_id"`
	}
	if err := json.NewDecoder(f).Decode(&m); err != nil {
		return "", err
	}
	if m.AppID == "" {
		return "", errors.New("app.json: app_id empty")
	}
	return m.AppID, nil
}

// fanotifyEventMetadata matches the kernel's fanotify_event_metadata
// struct (linux/fanotify.h). We don't decode the optional
// fanotify_event_info records (FID, file name, etc.) — for Wave 0 the
// path attached to the event is sufficient to bucket the write.
type fanotifyEventMetadata struct {
	Vers        uint16
	Reserved    uint16
	MetadataLen uint32
	Events      uint16
	Flags       uint8
	Padding     uint8 // alignment on amd64
	Fd          int32
}

// readFanotify drains the fanotify fd, parses each event, and feeds
// advisoryPipe.add(). Non-blocking I/O — the kernel only sets events
// we marked for. We close any fd the kernel hands us (events with
// FAN_FD_AVAILABLE); for advisory-only we don't actually need the
// fd, but the kernel requires we close it or it leaks.
func readFanotify(fd int, pipe *advisoryPipe, log *slog.Logger) {
	buf := make([]byte, advisoryFanotifyEventBuf)
	for {
		n, err := unix.Read(fd, buf)
		if err != nil {
			if errors.Is(err, unix.EAGAIN) {
				// FAN_NONBLOCK + nothing queued — sleep a hair so we
				// don't burn CPU on a tight loop. The kernel will wake
				// us on the next event anyway, but select/poll would be
				// the "right" shape — for Wave 0 this is fine.
				time.Sleep(50 * time.Millisecond)
				continue
			}
			log.Warn("stateless advisory fanotify read failed; exiting", "err", err)
			return
		}
		if n < int(unsafe.Sizeof(fanotifyEventMetadata{})) {
			continue
		}
		// Parse one or more events out of the buffer.
		off := 0
		for off+int(unsafe.Sizeof(fanotifyEventMetadata{})) <= n {
			//nolint:gosec // fanotify wire format: the kernel writes
			// a packed fanotify_event_metadata header at buf[off]. The
			// bounds check above (off + sizeof(header) <= n) prevents
			// out-of-bounds reads; the cast is unavoidable to decode
			// the syscall payload.
			meta := (*fanotifyEventMetadata)(unsafe.Pointer(&buf[off]))
			ev := advisoryEvent{
				Masks:  maskNames(uint64(meta.Events)),
				PID:    pidFromFD(int(meta.Fd), log),
				TsUnix: time.Now().UnixMilli(),
			}
			// Resolve the path from the fd (kernel attaches the marked
			// path's dentry). FAN_FD_AVAILABLE may not be set in
			// advisory-only mode; if readlink fails, use the marked
			// path's basename as a coarse fallback.
			if path, perr := os.Readlink(fmt.Sprintf("/proc/self/fd/%d", meta.Fd)); perr == nil {
				ev.Path = path
			} else {
				ev.Path = markedPathFallback(int(meta.Fd))
			}
			_ = unix.Close(int(meta.Fd)) // kernel requires we close
			pipe.add(ev)
			off += int(meta.MetadataLen)
		}
	}
}

// pidFromFD pulls the pid out of /proc/self/fdinfo/<fd> for an
// event whose FAN_FD_AVAILABLE bit was set. Most advisory-only events
// don't have it, so a readlink failure is non-fatal and we report 0.
func pidFromFD(fd int, log *slog.Logger) int {
	data, err := os.ReadFile(fmt.Sprintf("/proc/self/fdinfo/%d", fd))
	if err != nil {
		return 0
	}
	for _, line := range splitLines(data) {
		if hasPrefix(line, "pid:") {
			var pid int
			_, _ = fmt.Sscanf(line, "pid:%d", &pid)
			return pid
		}
	}
	return 0
}

// markedPathFallback returns the marked path that produced the most
// recent event. Used only when /proc/self/fd/<n> resolves to nothing
// (FAN_FD_AVAILABLE not set in advisory mode). The fallback is coarse
// — the audit row shows the marked dir, not the file — which is
// enough for the audit-table signal.
func markedPathFallback(fd int) string {
	// /proc/self/fd/<n> may be a deleted anon_inode:[fanotify]; fall
	// back to the first marked path which is /data. Better than
	// silently dropping the event.
	_ = fd
	if len(statelessRuntimePaths) > 0 {
		return statelessRuntimePaths[0]
	}
	return "/data"
}

// splitLines + hasPrefix are tiny stdlib-ish helpers to avoid an
// extra import in a binary that ships inside the microVM rootfs.
func splitLines(b []byte) []string {
	var out []string
	start := 0
	for i, c := range b {
		if c == '\n' {
			out = append(out, string(b[start:i]))
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, string(b[start:]))
	}
	return out
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// advisoryPipe owns the pending batch + the de-dup window. Producers
// (readFanotify) call add(); consumers (advisoryShipper) call drain().
type advisoryPipe struct {
	mu     sync.Mutex
	cond   *sync.Cond
	closed bool
	// pending keyed by (path, mask-set-hash). A repeat within the
	// debounce window overwrites the existing entry — we keep the
	// freshest pid/ts.
	pending map[string]*advisoryEvent
	// lastSeen tracks when each path was last added — used by the
	// shipper to decide when to flush (window elapsed since last add
	// on this path OR cap reached).
	lastSeen map[string]time.Time
	log      *slog.Logger
}

func newAdvisoryPipe(log *slog.Logger) *advisoryPipe {
	p := &advisoryPipe{
		pending:  make(map[string]*advisoryEvent),
		lastSeen: make(map[string]time.Time),
		log:      log,
	}
	p.cond = sync.NewCond(&p.mu)
	return p
}

func (p *advisoryPipe) add(ev advisoryEvent) {
	key := ev.Path + "|" + joinMasks(ev.Masks)
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.pending[key] = &ev
	p.lastSeen[key] = time.Now()
	shouldFlush := len(p.pending) >= advisoryBatchCap
	p.mu.Unlock()
	if shouldFlush {
		p.cond.Broadcast()
	}
}

// drain returns the pending batch and resets state. Returns nil if
// nothing is pending and the window has not elapsed since lastFlush.
//
// We hold the lock across the slice copy so a concurrent add can't
// half-overwrite our view; advisoryBatchCap keeps the copy bounded.
func (p *advisoryPipe) drain(window time.Duration) []advisoryEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.pending) == 0 {
		return nil
	}
	now := time.Now()
	// Only flush if the newest pending entry is at least window old.
	// This is the debounce — a burst within the window collapses into
	// one row.
	var newest time.Time
	for _, t := range p.lastSeen {
		if t.After(newest) {
			newest = t
		}
	}
	if now.Sub(newest) < window && len(p.pending) < advisoryBatchCap {
		return nil
	}
	out := make([]advisoryEvent, 0, len(p.pending))
	for _, ev := range p.pending {
		out = append(out, *ev)
	}
	// Bound the output — drop the tail if the map exceeded the cap
	// before the window elapsed (rare; only on a fanotify storm).
	if len(out) > advisoryBatchCap {
		out = out[:advisoryBatchCap]
	}
	p.pending = make(map[string]*advisoryEvent)
	p.lastSeen = make(map[string]time.Time)
	return out
}

// joinMasks is a stable string for the mask-set so the dedupe key is
// path|maskSet. "create|modify" != "create" so a path that gets both
// create and modify stays distinct from one that gets only create.
func joinMasks(masks []string) string {
	out := ""
	for _, m := range masks {
		out += m + ","
	}
	return out
}

// advisoryShipper serialises drain() output as JSON, prepends the
// msg_type prefix, and sends one DGRAM per batch to host CID.
//
// Host-side parser lives at pkg/fcvm/vmm.go::SendStatelessAdvisory
// and reads: [uint32 BE msg_type][uint32 BE body_len][body bytes].
func advisoryShipper(sock int, appID string, pipe *advisoryPipe, log *slog.Logger) {
	ticker := time.NewTicker(advisoryDeDupWindow)
	defer ticker.Stop()
	for {
		// Wait for either: a signal (cap reached) or the ticker.
		pipe.mu.Lock()
		for len(pipe.pending) == 0 && !pipe.closed {
			pipe.cond.Wait()
		}
		if pipe.closed {
			pipe.mu.Unlock()
			return
		}
		pipe.mu.Unlock()

		// We have at least one pending entry. Sleep at most
		// advisoryDeDupWindow before draining so a burst collapses.
		select {
		case <-ticker.C:
		default:
			time.Sleep(advisoryDeDupWindow)
		}

		batch := pipe.drain(advisoryDeDupWindow)
		if len(batch) == 0 {
			continue
		}
		body, sent, err := marshalAdvisoryBatchWithinLimit(appID, batch, advisoryMaxBody)
		if err != nil {
			log.Warn("stateless advisory marshal failed", "err", err)
			continue
		}
		if len(body) == 0 {
			log.Warn("stateless advisory event exceeds the frame limit; dropping batch",
				"events", len(batch), "limit", advisoryMaxBody)
			continue
		}
		if sent < len(batch) {
			log.Warn("stateless advisory batch too large; dropped trailing events",
				"sent", sent, "dropped", len(batch)-sent, "limit", advisoryMaxBody)
		}

		// Frame: msg_type (4 BE) + body_len (4 BE) + body.
		var framed [8]byte
		binary.BigEndian.PutUint32(framed[0:4], VsockStatelessAdvisoryMsgType)
		binary.BigEndian.PutUint32(framed[4:8], uint32(len(body)))
		payload := append(framed[:], body...)

		dst := &unix.SockaddrVM{
			CID:  unix.VMADDR_CID_HOST,
			Port: VsockStatelessAdvisoryPort,
		}
		// SendmsgN is the cross-arch form (SendtoN is amd64-only on
		// golang.org/x/sys). DGRAM semantics are identical: kernel
		// takes the buffer atomically, no scatter/gather needed.
		if _, err := unix.SendmsgN(sock, payload, nil, dst, 0); err != nil {
			// Drop on EAGAIN (DGRAM queue full — host can't keep up)
			// or any other error. ADR-035 — silent on drop.
			log.Warn("stateless advisory vsock send failed", "err", err, "events", len(batch))
		}
	}
}

// marshalAdvisoryBatchWithinLimit encodes the largest prefix of events whose
// JSON document fits inside limit bytes, returning the document and the
// number of events it carries.
//
// The previous form marshalled the whole batch and then byte-sliced the
// result down to the limit. That yields a truncated JSON document: the host
// receiver cannot unmarshal it, so the "dropping tail" the log claimed was in
// fact dropping the entire batch, silently, every time an app was noisy
// enough to exceed 8 KiB in one de-dup window. Dropping whole events instead
// keeps the document parseable and makes the loss honest in the log line.
//
// A zero-length result means even a single event exceeds the limit; the
// caller drops the batch rather than emitting an unparseable frame.
func marshalAdvisoryBatchWithinLimit(appID string, events []advisoryEvent, limit int) ([]byte, int, error) {
	if len(events) == 0 {
		return nil, 0, nil
	}
	// Binary search the largest prefix that fits. Encoded size is monotonic
	// in the prefix length, so the predicate is well-behaved.
	var best []byte
	bestN := 0
	lo, hi := 1, len(events)
	for lo <= hi {
		mid := (lo + hi) / 2
		body, err := json.Marshal(advisoryBatch{AppID: appID, Events: events[:mid]})
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
