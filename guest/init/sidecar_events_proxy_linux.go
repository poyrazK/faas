//go:build linux

// Sidecar events proxy (issue #463 / ADR-069 / ADR-071 / PR-C §3,§4).
//
// guest-init's runWorkloads orchestrator emits three sidecar-class
// events to the host so the platform can audit init failures and
// observe restart rates:
//
//   - sidecar_init_exit  (type=0x02 on port 1027) — fired when
//     an init sidecar (type=="init" workload) exits, regardless
//     of whether the exit was clean. status="init_ok" or
//     "init_failed"; the exit code and elapsed millis travel
//     alongside. vmmd translates the frame into a
//     pkg/events.SidecarInitExit event AND, on init_failed,
//     stamps the deployments-side audit row with
//     failure_class: user_error (AC #1).
//
//   - sidecar_restart    (type=0x03 on port 1027) — fired when
//     a long-running sidecar supervisor exhausts a Restart
//     attempt cycle (i.e. a fresh fork). vmmd increments
//     vmmd_sidecar_restart_total{app,sidecar} and emits
//     pkg/events.SidecarRestart (AC #3).
//
//   - sidecar_health     (type=0x08 on port 1027) — fired for the
//     lifecycle transitions of a long-running sidecar: starting,
//     healthy, unhealthy, restarting, or failed. vmmd translates
//     the frame into pkg/events.SidecarHealth and increments the
//     transition counter.
//
// Wire (guest-init → vsock STREAM, port 1027):
//
//	[1B type=0x02 | 0x03 | 0x08][json envelope bytes (UTF-8)]
//
// The envelope shape (json):
//
//	{
//	  "sidecar":      "<name>",     // required
//	  "status":       "init_ok" | "init_failed",   // 0x02 only
//	  "exit_code":    <int>,        // 0x02 only
//	  "duration_ms":  <int>,        // 0x02 only
//	  "attempt":      <int>         // 0x03 only
//	  "reason":       "<bounded text>" // 0x08 only
//	}
//
// This piggybacks on the same vsock channel PR #470 carved for
// framework_ready (port 1027, type=0x01). The host receiver
// (cmd/vmmd/framework_ready_recv.go) dispatches on the leading
// type byte and routes 0x02/0x03/0x08 to the sidecar events emitter.
// A future PR can split into per-event-class sockets for cleaner
// backpressure; the closed enum + bounded payload keeps the
// single-socket design safe in PR-C.
//
// Each event uses a fresh stream. EOF is the frame boundary at the
// per-instance host listener, so a broken connection cannot poison a later
// lifecycle event.
//
// Lifecycle: outbox (sender only). startSidecarEventsProxy is
// called once from boot() before the supervisor starts, so the
// first init-exit can't race the proxy coming up. Returns are
// tolerated — send failures log at Warn and do not stop the workload.
package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// VsockSidecarEventsPort mirrors cmd/vmmd/framework_ready_recv.go
// ::VsockFrameworkReadyHostPort = 1027 (issue #463 / ADR-069 /
// ADR-071 / PR-C). The guest-init proxy and the host receiver
// share the same port; the leading type byte disambiguates the
// event class (0x01 = framework_ready, 0x02 = sidecar_init_exit,
// 0x03 = sidecar_restart, 0x08 = sidecar_health). Duplicated by design — guest-init and
// cmd/vmmd cannot share compile-time symbols.
const VsockSidecarEventsPort uint32 = 1027

// VsockSidecarEventsTypeInitExit is the discriminator byte for
// the "init_ok"/"init_failed" envelope. Future-compatibility: the
// host drops unknown types at Warn.
const VsockSidecarEventsTypeInitExit byte = 0x02

// VsockSidecarEventsTypeRestart is the discriminator byte for the
// restart-counter envelope.
const VsockSidecarEventsTypeRestart byte = 0x03

// VsockSidecarEventsTypeHealth is the discriminator byte for the
// bounded sidecar lifecycle envelope.
const VsockSidecarEventsTypeHealth byte = 0x08

// VsockTailEventType (issue #667 / ADR-078) is the discriminator
// byte for the waitUntil(post-response tail) terminal-event
// envelope. Same STREAM port 1027 channel as the framework_ready /
// sidecar_init_exit / sidecar_restart events; the host's recv
// loop (cmd/vmmd/framework_ready_recv.go) dispatches on the
// leading type byte. The 1-byte outcome + 8-byte elapsed_ms BE
// payload follows the type byte.
//
// Wire layout (16 bytes total, fixed-size):
//
//	[1B type=0x04][1B outcome][6B reserved][8B elapsed_ms BE uint64]
//
// Reserved bytes stay 0x00 in PR 3 — the host resolves instance
// identity from the STREAM peer CID (same join the framework_ready
// / sidecar paths use). The reserved space gives a follow-up PR
// a wire-incompatible-free upgrade path (e.g. an explicit
// instance_id if the peer CID join ever needs to be relaxed for
// a future deployment topology).
const VsockTailEventType byte = 0x04

// tailEventMaxDatagram is the upper bound on the tail-event
// body the guest-init proxy will emit. 16 bytes is the
// fixed-size envelope (1+1+6+8); the host reads up to
// frameworkReadyMaxDatagram=1024 for ALL types so this is
// well within budget.
const tailEventMaxDatagram = 16

// tailEventOutcomeCompleted mirrors pkg/fcvm.TailOutcomeCompleted.
// Duplicated here because guest/init cannot import pkg/fcvm
// (the two packages are built into separate binaries at
// different stages of the boot chain). A drift between the
// two closed sets is a wire-incompatible bug — the
// sidecar_events_proxy_linux_test.go pins the numeric
// equality between this byte and the host's parseFWReadyKindTail
// decoder.
//
// Reason: same as VsockFrameworkReadyPort in
// framework_ready_proxy_linux.go — guest/init and pkg/fcvm
// cannot share compile-time symbols. The numeric values are
// pinned at both ends by parse/emit unit tests so a drift
// surfaces as a failing test rather than a silent wire
// mismatch.
const (
	tailEventOutcomeCompleted byte = 0x01
	tailEventOutcomeFailed    byte = 0x02
	tailEventOutcomeTimeout   byte = 0x03
)

// sidecarInitExitEnvelope is the JSON payload the proxy sends
// for type=0x02. The field tags mirror the json wire exactly —
// the host parses the same set, so a rename here requires a
// parallel rename in cmd/vmmd/framework_ready_recv.go and the
// unit test in guest/init/sidecar_events_proxy_linux_test.go.
type sidecarInitExitEnvelope struct {
	Sidecar    string `json:"sidecar"`
	Status     string `json:"status"` // "init_ok" | "init_failed"
	ExitCode   int    `json:"exit_code"`
	DurationMs int64  `json:"duration_ms"`
}

// sidecarRestartEnvelope is the JSON payload the proxy sends
// for type=0x03. "attempt" is the 1-indexed restart number
// (1 = first restart after the initial fork).
type sidecarRestartEnvelope struct {
	Sidecar string `json:"sidecar"`
	Attempt int    `json:"attempt"`
}

// sidecarHealthEnvelope is the JSON payload the proxy sends for type=0x08.
// Status is a closed set shared with cmd/vmmd; reason is diagnostic text only
// and is bounded before the frame is written.
type sidecarHealthEnvelope struct {
	Sidecar string `json:"sidecar"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
}

// sidecarMaxDatagram caps the JSON envelope. sidecar names are
// bounded by api.SidecarCapMax=2 + a reasonable length bound
// (~32 chars); the JSON envelope settles well under 256 bytes
// for any realistic payload. 512 is a generous future-proof
// margin below the host receiver's 1024-byte frame cap.
const sidecarMaxDatagram = 512

// startSidecarEventsProxy installs the outbound sender used by runWorkloads
// and the supervisor's OnCrash hook. A socket is opened per send.
func startSidecarEventsProxy(log *slog.Logger) (*sidecarEventsProxy, error) {
	if log == nil {
		log = slog.Default()
	}
	p := &sidecarEventsProxy{log: log}
	log.Info("sidecar events proxy started", "vsock_port", VsockSidecarEventsPort, "transport", "stream")
	return p, nil
}

// sidecarEventsProxy is the outbound-only sender for the three
// sidecar event classes.
type sidecarEventsProxy struct {
	log *slog.Logger
}

// SendInitExit frames one sidecarInitExitEnvelope and ships it
// to VMADDR_CID_HOST:VsockSidecarEventsPort. Returns the
// underlying encode error so the caller (runWorkloads) can log
// + return; a nil return means the vsock write was accepted by
// the kernel. The guest-init call site does not treat a vsock
// write error as fatal — we never silently fail a customer's
// deploy because the audit signal didn't make it home; the
// supervisor's restart policy remains the source of truth for
// "did the deploy actually succeed".
func (p *sidecarEventsProxy) SendInitExit(sidecar, status string, exitCode int, durationMs int64) error {
	if p == nil {
		return nil // proxy never came up; see boot() no-signal contract
	}
	env := sidecarInitExitEnvelope{
		Sidecar: sidecar, Status: status, ExitCode: exitCode, DurationMs: durationMs,
	}
	body, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("sidecar init_exit marshal: %w", err)
	}
	return p.send(VsockSidecarEventsTypeInitExit, body)
}

// SendRestart frames one sidecarRestartEnvelope and ships it
// to VMADDR_CID_HOST:VsockSidecarEventsPort. Called from the
// supervisor's OnCrash hook (workload_linux.go), which fires
// after each restart attempt cycle. A send error is logged at
// Warn; the orchestrator's restart policy continues
// unaffected.
func (p *sidecarEventsProxy) SendRestart(sidecar string, attempt int) error {
	if p == nil {
		return nil
	}
	env := sidecarRestartEnvelope{Sidecar: sidecar, Attempt: attempt}
	body, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("sidecar restart marshal: %w", err)
	}
	return p.send(VsockSidecarEventsTypeRestart, body)
}

// SendHealth ships one bounded sidecar lifecycle transition to the host.
// The send is best-effort at the caller, just like the existing restart and
// init-exit signals: losing an observation must not change workload behavior.
func (p *sidecarEventsProxy) SendHealth(sidecar, status, reason string) error {
	if p == nil {
		return nil
	}
	reason = clampSidecarHealthReason(reason)
	env := sidecarHealthEnvelope{Sidecar: sidecar, Status: status, Reason: reason}
	body, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("sidecar health marshal: %w", err)
	}
	return p.send(VsockSidecarEventsTypeHealth, body)
}

func clampSidecarHealthReason(reason string) string {
	if len(reason) <= 256 {
		return reason
	}
	runes := []rune(reason)
	if len(runes) > 256 {
		runes = runes[:256]
	}
	for len(runes) > 0 && len(string(runes)) > 256 {
		runes = runes[:len(runes)-1]
	}
	return string(runes)
}

// SendTailEvent (issue #667 / ADR-078) ships one waitUntil
// terminal-event envelope to the host. Called from the runner's
// tail host once per waitUntil(promise) task that reaches a
// terminal state (completed / failed / timeout). The runner's
// per-task context.WithTimeout enforces the per-plan timeout
// (Free 5s / Hobby 15s / Pro 30s / Scale 60s) so this is the
// only place a TailOutcomeTimeout byte is emitted.
//
// Wire layout (16 bytes, fixed-size):
//
//	[1B type=0x04][1B outcome][6B reserved][8B elapsed_ms BE uint64]
//
// Reserved bytes are 0x00 in PR 3 — the host resolves
// instance identity from the STREAM peer CID. elapsedMs is
// the wall-clock duration from waitUntil registration to
// terminal (in milliseconds); the host reads it for the
// telemetry histogram (PR 5). A send error is logged at the
// caller's Warn (the runner-side log shim — see
// guest/runners/internal/runnerparity) and the runner keeps
// draining — a lost receipt is bounded by the 5s
// snapshotAndPark watchdog.
func (p *sidecarEventsProxy) SendTailEvent(outcome byte, elapsedMs int64) error {
	if p == nil {
		return nil
	}
	return p.sendTail(outcome, elapsedMs)
}

// sendTail is the fixed-size-encode variant of send(); the
// 16-byte layout doesn't need json.Marshal. Mirrors the size
// cap pattern (early-return on > tailEventMaxDatagram) so
// the make() allocation is bounded by a compile-time constant.
func (p *sidecarEventsProxy) sendTail(outcome byte, elapsedMs int64) error {
	// Closed-set outcome guard — anything outside the 3-byte
	// enum is a wiring bug; surface loud rather than silently
	// shipping garbage to the host.
	switch outcome {
	case tailEventOutcomeCompleted, tailEventOutcomeFailed, tailEventOutcomeTimeout:
	default:
		return fmt.Errorf("tail event outcome byte 0x%02x outside closed set {completed, failed, timeout}", outcome)
	}
	buf := make([]byte, tailEventMaxDatagram)
	buf[0] = VsockTailEventType
	buf[1] = outcome
	// buf[2:8] reserved, already zero from make().
	binary.BigEndian.PutUint64(buf[8:16], uint64(elapsedMs))
	if err := sendGuestEventFrame(buf, time.Second); err != nil {
		return fmt.Errorf("tail event vsock send: %w", err)
	}
	return nil
}

// codeql[go/allocation-size-overflow] false-positive: send()'s early-return guard rejects any payload whose len(body)+1 exceeds sidecarMaxDatagram (512); the make() below allocates a fixed-size scratch buffer and slices it, so the taint can't reach the make() size argument at all.
func (p *sidecarEventsProxy) send(t byte, body []byte) error {
	bodyLen := len(body)
	if bodyLen+1 > sidecarMaxDatagram {
		return fmt.Errorf("sidecar event datagram %d bytes exceeds limit %d", bodyLen+1, sidecarMaxDatagram)
	}
	// codeql[go/allocation-size-overflow] false-positive: the early-return above rejects any payload whose bodyLen+1 exceeds sidecarMaxDatagram (512); buf below is allocated with a compile-time constant and sliced — no tainted size ever reaches make().
	buf := make([]byte, sidecarMaxDatagram)[:1+bodyLen]
	buf[0] = t
	copy(buf[1:], body)
	if err := sendGuestEventFrame(buf, time.Second); err != nil {
		return fmt.Errorf("sidecar event vsock send: %w", err)
	}
	return nil
}
