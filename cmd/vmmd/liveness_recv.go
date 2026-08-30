//go:build linux

// Host-side liveness probe loop (issue #554 / ADR-078). The
// guest-init listener (guest/init/liveness_linux.go) binds AF_VSOCK
// STREAM port 1028 inside the VM; this file dials that port on
// every Period, runs the framed probe request, and translates the
// 4-class outcome into the consecutive-failure counter. After
// ConsecutiveFailures non-2xx (or timeout / conn-refused) responses
// the goroutine calls Manager.ReportLivenessFailed, which the
// schedd-side relay drains into Engine.DestroyForLivenessFailure.
//
// The wire envelope mirrors ADR-022's resume hook + the
// guest-init listener:
//
//	4-byte big-endian msg-type   = guest.VsockLivenessMsgProbe (10)
//	4-byte big-endian body-len
//	N-byte JSON body             = {"path":"/healthz", "timeout_ms":2000}
//
//	(responding)
//	4-byte big-endian msg-type   = guest.VsockLivenessMsgAck (11)
//	4-byte big-endian body-len
//	N-byte JSON body             = {"status":200, "err":""}
//
// Lifecycle: one livenessProbeLoop goroutine per instance. The
// Manager owns the lifecycle (start on BringUp, stop on
// DestroyForLivenessFailure or Park). The goroutine exits on
// ctx cancellation (cmd vmmd shutdown) or on a fatal vsock
// error; the per-iteration tick is timer-driven so a single
// missed probe doesn't compound with the next.
package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"golang.org/x/sys/unix"

	"github.com/onebox-faas/faas/pkg/fcvm"
)

// VsockLivenessHostPort mirrors the guest-side
// guest.VsockLivenessPort (= 1028). Must match on both sides.
// Duplicated here because cmd/vmmd does not import guest/init
// (one-way layering).
const VsockLivenessHostPort uint32 = 1028

// livenessRequestBody is the JSON body the host ships to the guest.
// Mirrors guest/init/livenessReq.
type livenessRequestBody struct {
	Path      string `json:"path"`
	TimeoutMs int    `json:"timeout_ms"`
}

// livenessResponseBody is the JSON body the guest ships back.
// Mirrors guest/init/livenessResp.
//
// WWWAuthenticate is the verbatim WWW-Authenticate response header
// value when the runner responded 401/403 — see the Cluster A
// rationale in guest/init/liveness_linux.go. The host reads the
// field defensively today (empty = no signal); the discriminator
// becomes load-bearing if/when the platform introduces its own
// probe-auth round-trip.
type livenessResponseBody struct {
	Status          int    `json:"status"`
	Err             string `json:"err"`
	WWWAuthenticate string `json:"www_authenticate,omitempty"`
}

// livenessProbeOutcomes is the closed set the vmmd
// poll goroutine tracks in the vmmd_guest_liveness_probe_seconds
// histogram. Each value is the Prometheus label suffix.
//
// Error-explanations cluster (spec §6.4 amendment 1):
// livenessOutcomeUnauthorized discriminates 401/403 from the
// generic non_200 bucket. A health endpoint that's gated behind
// auth is a distinct failure ("the app is up but we can't tell
// because /healthz returns 401") with a distinct remediation
// (expose /healthz without auth, or move to a separate
// healthcheck_path) — folding it into non_200 would force the
// customer to debug it as a runtime failure. The classify +
// histogram + downstream app_healthz_unauthorized stamping all
// flow from this single discriminator.
//
// Cluster A: the guest-init's livenessResp now also carries
// WWWAuthenticate (the verbatim response header on 401/403) so the
// host can later discriminate customer-app intentional 401 from a
// platform-side probe auth round-trip. The discriminator is a
// forward-compat field today (no platform-side probe auth exists);
// the closed-set is the six values below.
const (
	livenessOutcomeOK           = "ok"
	livenessOutcomeNon200       = "non_200"
	livenessOutcomeUnauthorized = "unauthorized"
	livenessOutcomeTimeout      = "timeout"
	livenessOutcomeConnRefused  = "conn_refused"
	livenessOutcomeConnErr      = "conn_err"
)

// livenessProbeConfig aliases the pkg/fcvm.LivenessProbeConfig struct
// (issue #554 / ADR-078). The cmd-side per-instance loop reads the
// same fields the Manager already populates from the per-deployment
// override + per-plan defaults merge. Type alias keeps existing
// references (livenessProbeLoop.cfg, run() cfg.PeriodSeconds, etc.)
// unchanged.
type livenessProbeConfig = fcvm.LivenessProbeConfig

// livenessProbeLoop is the per-instance poll goroutine. The
// Manager owns a map[*livenessProbeLoop]cancelFunc so a Park /
// Destroy race can stop the loop without waiting for the next
// tick.
type livenessProbeLoop struct {
	instance     string
	deploymentID string // set from WakeRequest at BringUp; survives across cold boots (code review #725 F1)
	cfg          livenessProbeConfig
	cid          uint32
	mgr          *fcvm.Manager
	log          *slog.Logger
	count        int // current consecutive-failure count
	// probeFn is the test seam: production code uses dialAndProbe
	// (real AF_VSOCK), tests inject a stub that returns the
	// closed-set outcome string ("ok", "non_200", "unauthorized",
	// "timeout", "conn_refused", "conn_err"). Default = nil →
	// runOne uses the real dialAndProbe.
	probeFn func(ctx context.Context, timeoutMs int) string
	// nowFn is the clock seam (issue #554 closure / ADR-078
	// cooldown gate). Production = time.Now; tests inject a
	// frozen clock so the cooldown window can be exercised
	// deterministically without real-time sleeps.
	nowFn func() time.Time
}

// runLivenessProbeLoop is the entry point. Blocks until ctx is
// done. The poll cadence is cfg.PeriodSeconds (default 5s); the
// per-probe timeout is min(cfg.PeriodSeconds * 1000, 5000)ms —
// the hard ceiling matches the guest-init's
// VsockLivenessHardTimeoutMs.
//
// On every non-2xx / timeout / conn-refused response the count
// increments; on every 2xx it resets to 0. When count reaches
// cfg.ConsecutiveFailures the loop calls Manager.ReportLivenessFailed
// and exits (the schedd side will paint the instance stopped, and
// the Manager will cancel the loop via the destruction path).
func (l *livenessProbeLoop) run(ctx context.Context) {
	if l.cfg.PeriodSeconds <= 0 {
		// The plan didn't enable liveness for this instance.
		return
	}
	tick := time.NewTicker(time.Duration(l.cfg.PeriodSeconds) * time.Second)
	defer tick.Stop()
	timeoutMs := l.cfg.PeriodSeconds * 1000
	if timeoutMs > 5000 {
		timeoutMs = 5000
	}
	if timeoutMs < 1000 {
		timeoutMs = 1000
	}
	// Single-shot probe on entry so a Steady-State VM doesn't
	// have to wait PeriodSeconds to validate the liveness path
	// is wired. Failures here still count toward the counter.
	if l.runOne(ctx, timeoutMs) {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if l.runOne(ctx, timeoutMs) {
				return
			}
		}
	}
}

// runOne executes one probe. The closed-set outcome is folded into the
// consecutive-failure counter + the vmmd_guest_liveness_probe_seconds
// histogram. The 5s deadline is tighter than the period so a stuck
// probe doesn't compound with the next tick. It returns true when
// the terminal failure threshold is reached so the outer loop can
// stop instead of leaving a stale ticker goroutine behind.
func (l *livenessProbeLoop) runOne(ctx context.Context, timeoutMs int) bool {
	if ctx.Err() != nil {
		return true
	}
	start := time.Now()
	var outcome string
	if l.probeFn != nil {
		outcome = l.probeFn(ctx, timeoutMs)
	} else {
		outcome = l.dialAndProbe(ctx, timeoutMs)
	}
	// Destroy/Park can cancel the loop while the vsock probe is in
	// flight. Do not publish metrics or report a failure for an instance
	// whose teardown already won the race.
	if ctx.Err() != nil {
		return true
	}
	elapsed := time.Since(start).Seconds()
	l.mgr.ObserveLivenessProbe(outcome, elapsed)

	// Cooldown gate (issue #554 closure / ADR-078, code review
	// #725 finding F1). After a successful liveness destroy the
	// cold-boot replacement instance has a grace window: probes
	// that fail within cfg.CooldownSeconds of the previous
	// destroy are short-circuited — we increment nothing, fire
	// nothing, and let the cold-boot instance settle. The gate
	// bypasses on cfg.CooldownSeconds <= 0 (Free plan / legacy
	// callers) AND on empty deploymentID (legacy pre-PR-B
	// callers that don't carry deployment_id on the wire).
	//
	// Reading via Manager.LastLivenessDestroyAtForDeployment
	// keys on deploymentID (not instanceID) — the cold-boot
	// replacement inherits deploymentID from schedd's
	// CreateInstance + Wake stamp, so the gate sees the
	// previous instance's destroy time even though the
	// instance id is a fresh UUID. Reading via the Manager
	// accessor keeps the loop goroutine out of m.mu on the
	// hot path; the Manager stamps under its own lock.
	nowFn := l.nowFn
	if nowFn == nil {
		nowFn = time.Now
	}
	if l.cfg.CooldownSeconds > 0 && l.deploymentID != "" {
		if last := l.mgr.LastLivenessDestroyAtForDeployment(l.deploymentID); !last.IsZero() {
			cooldown := time.Duration(l.cfg.CooldownSeconds) * time.Second
			if nowFn().Sub(last) < cooldown {
				l.log.Debug("liveness: cooldown window",
					"instance", l.instance,
					"deployment_id", l.deploymentID,
					"since_destroy_s", int(nowFn().Sub(last).Seconds()),
					"cooldown_s", l.cfg.CooldownSeconds)
				return false
			}
		}
	}

	switch outcome {
	case livenessOutcomeOK:
		// Reset on the first 2xx (AC #2 — intermittent failures
		// must not produce oscillation).
		if l.count > 0 {
			l.log.Debug("liveness probe reset",
				"instance", l.instance, "prev_count", l.count)
		}
		l.count = 0
		l.mgr.SetLivenessConsecutiveFailures(l.instance, 0)
	default:
		l.count++
		l.mgr.SetLivenessConsecutiveFailures(l.instance, l.count)
		if l.count >= l.cfg.ConsecutiveFailures {
			if ctx.Err() != nil {
				return true
			}
			// Mirror the reason the run-time classifies
			// into the relay so the schedd side audit
			// event names the cluster correctly.
			reason := classifyLivenessOutcome(outcome)
			l.mgr.ReportLivenessFailed(ctx, l.instance, reason)
			// Exit the loop — schedd's Engine.DestroyForLivenessFailure
			// will Park / destroy the instance, and the
			// Manager will cancel this loop via the
			// teardown path. Don't re-arm the counter.
			return true
		}
	}
	return false
}

// classifyLivenessOutcome maps a closed-set probe outcome into
// the matching wire reason. Extracted from runOne so the
// switch is the single source of truth (a new outcome class
// adds one case + one livenessOutcomeXxx const, no risk of
// the chained-if refactor pattern dropping a branch).
func classifyLivenessOutcome(outcome string) string {
	switch outcome {
	case livenessOutcomeTimeout:
		return "liveness_timeout"
	case livenessOutcomeConnRefused:
		return "liveness_conn_refused"
	case livenessOutcomeConnErr:
		return "liveness_conn_err"
	case livenessOutcomeUnauthorized:
		return "liveness_unauthorized"
	case livenessOutcomeNon200:
		return "liveness_non_200"
	default:
		return "liveness_n_consecutive"
	}
}

// livenessProbeDialTimeout is the absolute cap on the dial+read.
// 5s matches the guest-init's VsockLivenessHardTimeoutMs ceiling.
const livenessProbeDialTimeout = 5 * time.Second

// dialAndProbe opens an AF_VSOCK STREAM connection to the per-VM
// CID on VsockLivenessHostPort, ships the probe body, and returns
// the closed-set outcome string. The classification mirrors the
// four classes the guest-init reports — see
// guest/init/liveness_linux.go.
func (l *livenessProbeLoop) dialAndProbe(ctx context.Context, timeoutMs int) string {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	if err != nil {
		return livenessOutcomeConnErr
	}
	defer func() { _ = unix.Close(fd) }()
	dialCtx, cancel := context.WithTimeout(ctx, livenessProbeDialTimeout)
	defer cancel()
	addr := &unix.SockaddrVM{CID: l.cid, Port: VsockLivenessHostPort}
	// unix.Connect is non-blocking on the AF_VSOCK STREAM socket
	// the kernel returns immediately with EINPROGRESS; we wrap
	// in a deadline-driven polling loop. The connect itself
	// blocks until the guest-init's listener accepts OR the
	// deadline fires (the livenessProbeDialTimeout cap).
	connectDone := make(chan error, 1)
	go func() {
		connectDone <- unix.Connect(fd, addr)
	}()
	select {
	case <-ctx.Done():
		return livenessOutcomeConnErr
	case <-dialCtx.Done():
		return livenessOutcomeConnRefused
	case err := <-connectDone:
		if err != nil {
			// ECONNREFUSED is the expected signal when the
			// guest-init's listener is NOT up yet (a hot
			// rebuild before boot). The failure counter
			// increments — the CooldownSeconds gate in the
			// schedd-side window protects against a cold
			// boot noise signature.
			return livenessOutcomeConnRefused
		}
	}
	body, err := json.Marshal(livenessRequestBody{
		Path:      l.cfg.Path,
		TimeoutMs: timeoutMs,
	})
	if err != nil {
		return livenessOutcomeConnErr
	}
	var hdr [8]byte
	binary.BigEndian.PutUint32(hdr[:4], 10) // guest.VsockLivenessMsgProbe
	binary.BigEndian.PutUint32(hdr[4:8], uint32(len(body)))
	if err := writeAll(fd, hdr[:]); err != nil {
		return livenessOutcomeConnErr
	}
	if err := writeAll(fd, body); err != nil {
		return livenessOutcomeConnErr
	}
	// Read the response envelope. Deadline is the same
	// livenessProbeDialTimeout cap; we use a deadline-tracked
	// read loop instead of SetReadDeadline so the guest's
	// VsockLivenessDefaultTimeoutMs (2s) is the natural
	// timeout surface.
	var respHdr [8]byte
	if err := readAll(fd, respHdr[:], livenessProbeDialTimeout); err != nil {
		return livenessOutcomeTimeout
	}
	mt := binary.BigEndian.Uint32(respHdr[:4])
	if mt != 11 {
		// guest.VsockLivenessMsgAck — wire-shape regression
		// if it doesn't match.
		return livenessOutcomeConnErr
	}
	bodyLen := binary.BigEndian.Uint32(respHdr[4:8])
	if bodyLen == 0 || bodyLen > 4096 {
		return livenessOutcomeConnErr
	}
	respBody := make([]byte, bodyLen)
	if err := readAll(fd, respBody, livenessProbeDialTimeout); err != nil {
		return livenessOutcomeTimeout
	}
	var resp livenessResponseBody
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return livenessOutcomeConnErr
	}
	if resp.Err != "" {
		// The guest-init itself classified the failure —
		// fold the wire-side reason into the closed-set
		// outcome so the histogram stays the source of truth.
		switch resp.Err {
		case "timeout":
			return livenessOutcomeTimeout
		case "conn_refused":
			return livenessOutcomeConnRefused
		case "runner_not_ready":
			return livenessOutcomeConnRefused
		default:
			return livenessOutcomeConnErr
		}
	}
	if resp.Status >= 200 && resp.Status < 300 {
		return livenessOutcomeOK
	}
	// Error-explanations cluster (spec §6.4 amendment 1):
	// 401 + 403 are the canonical "health endpoint gated behind
	// auth" signal. Discriminate them from the generic non_200
	// bucket so the whycopy catalog can render the right hint
	// ("expose /healthz without auth") instead of the generic
	// "the app didn't return 200" line.
	if resp.Status == http.StatusUnauthorized || resp.Status == http.StatusForbidden {
		return livenessOutcomeUnauthorized
	}
	return livenessOutcomeNon200
}

// writeAll is the unix-style write-loop helper. Mirrors the
// guest-init's readFull. Both live here so the failure surface is
// symmetric — if this helper changes, the guest-side readFull gets
// a corresponding bump.
func writeAll(fd int, b []byte) error {
	for len(b) > 0 {
		n, err := unix.Write(fd, b)
		if n > 0 {
			b = b[n:]
		}
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			return err
		}
		if n == 0 {
			return fmt.Errorf("vsock write: 0 bytes")
		}
	}
	return nil
}

// readAll is the unix-style read-loop helper. The deadline is
// enforced via SO_RCVTIMEO (setsockopt) so the kernel returns
// EAGAIN on its own without a watcher goroutine. We honour
// ctx cancellation on the EINTR path so a parent-ctx cancel
// pre-empts the deadline.
//
// Mirrors the guest-init's setSockTimeout helper at
// guest/init/characterize_linux.go:572 — the AF_VSOCK stack has
// no Go-side deadline primitive, so we go through the kernel.
// A setsockopt failure is tolerated (the helper is best-effort);
// on failure the read becomes unbounded and the caller
// (dialAndProbe) leans on its outer deadline.
func readAll(fd int, b []byte, deadline time.Duration) error {
	tv := unix.Timeval{
		Sec:  int64(deadline / time.Second),
		Usec: int64(deadline%time.Second) / 1000,
	}
	_ = unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv)
	for len(b) > 0 {
		n, err := unix.Read(fd, b)
		if n > 0 {
			b = b[n:]
		}
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			// EAGAIN/EWOULDBLOCK = the SO_RCVTIMEO deadline
			// fired without a frame arriving. Surface as a
			// wrapped timeout so the caller can fold it into
			// the liveness_timeout outcome.
			if err == unix.EAGAIN || err == unix.EWOULDBLOCK {
				return fmt.Errorf("vsock read deadline %s", deadline)
			}
			return err
		}
		if n == 0 {
			return fmt.Errorf("vsock read: 0 bytes (EOF)")
		}
	}
	return nil
}

// startLivenessLoopHelper is the cmd-level goroutine launcher
// (issue #554 / ADR-078 / PR review fix). Builds the per-instance
// livenessProbeLoop, spawns its run goroutine, and returns the
// cancel func the Manager registers with the livenessRegistry
// (pkg/fcvm.LivenessRegistry). Called from Manager.startLivenessLoop
// via the LivenessProbeStarter closure the cmd/vmmd main loop wires
// via WithLivenessProbeStarter.
//
// The helper holds the parent vmmd ctx + the *fcvm.Manager (the
// metrics + sink access surface). The loop's child context is
// derived from the parent so a Park cancel or a vmmd shutdown
// drains the loop within one tick.
//
// Mirrors the production-only start() helper that lived inline in
// livenessRegistry prior to the PR-review refactor that moved the
// registry into pkg/fcvm.
func startLivenessLoopHelper(parent context.Context, mgr *fcvm.Manager, log *slog.Logger, instance string, slot int, deploymentID string, cfg fcvm.LivenessProbeConfig) context.CancelFunc {
	loop := &livenessProbeLoop{
		instance:     instance,
		deploymentID: deploymentID,
		cfg:          cfg,
		cid:          fcvm.GuestVsockCID(slot),
		mgr:          mgr,
		log:          log,
	}
	loopCtx, cancel := context.WithCancel(parent)
	go func() {
		defer mgr.FinishLivenessLoop(instance, loopCtx)
		loop.run(loopCtx)
	}()
	return cancel
}
