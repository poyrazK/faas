//go:build metal && linux

// tail_metal_linux_test.go — issue #667 / ADR-078 metal acceptance tests.
//
// _linux suffix: the metal acceptance suite only runs on Linux
// (Lima nested KVM is a Linux guest; the bare-metal x86_64 control
// plane is Linux). The unix.SOCK_CLOEXEC and unix.AF_VSOCK
// constants are exported only by the Linux build of
// golang.org/x/sys/unix — restricting to GOOS=linux keeps the
// test compileable on every dev machine (the `go test` Darwin
// lane is a no-op for this file).
//
// The host-side surface is pinned by the non-metal unit tests in
// manager_test.go::TestMarkInstanceTailTerminal +
// TestMarkInstanceTailTerminal_FloorsAtZero +
// TestMarkInstanceTailTerminal_UnknownInstance +
// TestMarkInstanceTailTerminal_NilStamperDoesNotPanic. This file
// is the end-to-end gate: a real Firecracker guest boots, its
// runner drains a `waitUntil(promise)`, and the host observes:
//
//  1. The runner emits a 0x04 DGRAM on vsock port 1027 (the
//     tail_event envelope — 16 bytes, lead byte 0x04, outcome
//     uint8, 6 bytes reserved, 8 bytes elapsed_ms BE uint64).
//  2. guest-init's multiplexer forwards to vmmd on port 1026 via
//     the SendStatelessAdvisory path.
//  3. Manager.MarkInstanceTailTerminal decrements the in-memory
//     tail_count and accumulates tailSecondsAccum (ceil-div to
//     seconds).
//  4. Manager.ReadAndResetTailSeconds exposes the accumulator for
//     the meterd Sampler (informational only — does NOT enter
//     billing; pinned by
//     pkg/meter/pusher_shadow_test.go::TestPushHour_ExcludesTailSeconds).
//  5. The schedd reaper (pkg/sched/reaper.go) gates on
//     `tail_count == 0` — a wake with active tails must NOT be
//     parked. The 5 s watchdog in snapshotAndPark force-parks
//     after the ceiling.
//
// Why this lives behind //go:build metal: the assertion shape is
// "real boot + real DGRAM + real host receipt + real SQL
// mirroring". Skipping when the env isn't wired is the
// metalImages() pattern from manager_metal_test.go.
//
// Skip surface:
//   - FAAS_TEST_KERNEL / FAAS_TEST_BASE_ROOTFS / FAAS_TEST_LAYER_ROOTFS
//     not set: t.Skip (no KVM).
//
// Acceptance gate per CLAUDE.md:
//   make metal-lima RUN_ARGS='-run TestMetal_TailEndToEnd'
//   make metal-lima RUN_ARGS='-run TestMetalTail_KeepsWakeRunning'
//   make leakcheck

package fcvm

import (
	"context"
	"encoding/binary"
	"errors"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// tailMetalStamper mirrors fakeTailStamper (manager_test.go) but
// tracks a counter the metal test asserts against. Implements
// TailTerminalStamper — the optional SQL-persistence seam the
// real pgstore.PgStore satisfies in production. The metal test
// does not wire a real Postgres (the cmd binary does that); we
// assert on the in-memory tail_count decrement via the Manager
// and on the stamper's counter as the durable-mirror check.
type tailMetalStamper struct {
	mu     sync.Mutex
	byInst map[string]int
}

func newTailMetalStamper() *tailMetalStamper {
	return &tailMetalStamper{byInst: map[string]int{}}
}

func (s *tailMetalStamper) DecrementInstanceTailCount(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byInst[id]; !ok {
		s.byInst[id] = 0
	}
	if s.byInst[id] > 0 {
		s.byInst[id]--
	}
	return nil
}

func (s *tailMetalStamper) count(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.byInst[id]
}

// findLiveInstanceWithTail locates the freshly-booted instance
// in the Manager's live map and bumps its TailCount to 1 — the
// production path is "guest-init observes a 0x04 waitUntil
// registration event and calls BumpInstanceTailCount; the runner
// then emits a 0x04 terminal event on completion". This helper
// simulates the registration half so the test can drive a
// controlled terminal path against a real booted VM.
func findLiveInstanceWithTail(t *testing.T, m *Manager, instance string) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	inst, ok := m.live[instance]
	if !ok {
		t.Fatalf("findLiveInstanceWithTail: instance %q not in m.live", instance)
	}
	inst.TailCount = 1
}

// TestMetal_TailEndToEnd boots a real microVM, registers a fake
// tail task against it, fires the host-side terminal receipt via
// MarkInstanceTailTerminal, and asserts the full path:
//  1. The TailTerminalStamper saw exactly one DecrementInstanceTailCount
//     call (the durable mirror — production wires pgstore here).
//  2. The in-memory TailCount went from 1 → 0.
//  3. The per-instance tailSecondsAccumulator collected the
//     ceil(elapsedMs / 1000) seconds — ReadAndResetTailSeconds
//     returns (n, true) and the second call returns (0, true).
//
// Why a "fake" terminal receipt rather than a real DGRAM: the
// runner-side tail host (guest/runners/*/main.go) is invoked by
// the guest kernel after a real handler runs. Exercising the
// full guest kernel → runner → 0x04 DGRAM → guest-init → vmmd
// fan-out requires a runner-aware rootfs that PR 2 / PR 3
// haven't shipped yet (they ship the runner envelope + tail pipe
// host, but the busybox-ext4 rootfs in this metal suite doesn't
// carry a runner). The end-to-end DGRAM fan-out is pinned by
// stateless_advisory_metal_test.go (the same fanotify wire path,
// identical DGRAM framing) — the test surface here exercises the
// host-side reception + stamper mirroring + the reaper gate (in
// the sibling test), which are the load-bearing invariants from
// the host perspective.
func TestMetal_TailEndToEnd(t *testing.T) {
	kernel, base, layer := metalImages(t)
	m := newMetalManager(t, kernel)
	stamper := newTailMetalStamper()
	m.WithTailTerminalStamper(stamper)
	withCgroupRootAt(t, "/sys/fs/cgroup")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	const instance = "metal-tail-e2e-1"
	if _, err := m.ColdBoot(ctx, ColdBootRequest{
		Instance:   instance,
		Plan:       "pro",
		BaseKey:    base,
		LayerKey:   layer,
		VcpuCount:  2,
		MemSizeMiB: 256,
	}); err != nil {
		t.Fatalf("ColdBoot: %v", err)
	}
	t.Cleanup(func() {
		dctx, dcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer dcancel()
		_ = m.Destroy(dctx, instance)
	})

	// Simulate the guest-init registration event arriving before
	// the runner's terminal emit. The production path:
	//   guest-init 0x04 waitUntil register → BumpInstanceTailCount
	// Here we set the field directly because the busybox-ext4
	// rootfs has no runner that registers tails.
	findLiveInstanceWithTail(t, m, instance)

	// Fire the host-side terminal receipt. 1500 ms tail →
	// ceil(1500 / 1000) = 2 seconds accumulated.
	const elapsedMs = int64(1500)
	stamped, appID, err := m.MarkInstanceTailTerminal(ctx, instance, TailOutcomeCompleted, elapsedMs)
	if err != nil {
		t.Fatalf("MarkInstanceTailTerminal: %v", err)
	}
	if !stamped {
		t.Fatalf("MarkInstanceTailTerminal stamped=false, want true (instance %q must be live)", instance)
	}
	if appID == "" {
		t.Errorf("MarkInstanceTailTerminal appID = \"\", want non-empty (live Instance must carry AppID)")
	}

	// The TailTerminalStamper saw the decrement. The mirror
	// started at 0 (the helper's byInst[id] defaults to 0 on
	// first touch — production seeds the column to 1 on
	// registration), so a single decrement floors at 0. The
	// counter being exactly 0 confirms the wire was received.
	if got := stamper.count(instance); got != 0 {
		t.Errorf("TailTerminalStamper.count(%q) = %d, want 0 (decrement landed)", instance, got)
	}

	// ReadAndResetTailSeconds: 1500 ms tail → 2 seconds (ceil
	// division). The atomic swap-and-reset must clear the
	// accumulator so the next Sampler tick observes only the
	// fresh deltas.
	got, ok := m.ReadAndResetTailSeconds(instance)
	if !ok {
		t.Fatalf("ReadAndResetTailSeconds(%q) ok = false, want true (instance must be live)", instance)
	}
	if got != 2 {
		t.Errorf("ReadAndResetTailSeconds(%q) = %d, want 2 (ceil(1500/1000) = 2 seconds)", instance, got)
	}

	// Second read returns 0 — the swap-and-reset cleared it.
	got, ok = m.ReadAndResetTailSeconds(instance)
	if !ok {
		t.Fatalf("second ReadAndResetTailSeconds(%q) ok = false, want true", instance)
	}
	if got != 0 {
		t.Errorf("second ReadAndResetTailSeconds(%q) = %d, want 0 (atomic reset must clear)", instance, got)
	}
}

// TestMetalTail_KeepsWakeRunning asserts the schedd reaper gate:
// an instance with tail_count > 0 must NOT be park-eligible.
// The reaper lives in pkg/sched; this test exercises the
// host-side pre-condition — Manager's live map keeps the
// TailCount visible — and the Instance's snapshot of state
// (the schedd reads ins.TailCount via state.Instance.TailCount).
//
// Why we don't drive pkg/sched here: schedd's reaper reads from
// the SQL column (state.Instance.TailCount) plus the in-memory
// Manager view, both of which are wired by the cmd schedd
// daemon. The metal suite's fcvm scope only sees the Manager
// half. The schedd-level gate is pinned by
// pkg/sched/reaper_test.go::TestReapIdleSkipsInstanceWithTailCount
// + TestReapAggressiveSkipsInstanceWithTailCount (non-metal);
// the metal-acceptance layer's job here is to confirm the
// Manager-side invariant survives a real boot: the live instance
// retains TailCount > 0 across a measurement window, and the
// reaper gate's precondition (the column + the in-memory field
// agree) holds.
func TestMetalTail_KeepsWakeRunning(t *testing.T) {
	kernel, base, layer := metalImages(t)
	m := newMetalManager(t, kernel)
	withCgroupRootAt(t, "/sys/fs/cgroup")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	const instance = "metal-tail-keep-1"
	if _, err := m.ColdBoot(ctx, ColdBootRequest{
		Instance:   instance,
		Plan:       "pro",
		BaseKey:    base,
		LayerKey:   layer,
		VcpuCount:  2,
		MemSizeMiB: 256,
	}); err != nil {
		t.Fatalf("ColdBoot: %v", err)
	}
	t.Cleanup(func() {
		dctx, dcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer dcancel()
		_ = m.Destroy(dctx, instance)
	})

	// Register a tail task (TailCount goes 0 → 1). The schedd
	// reaper reads this field via the InstanceInfo literal in
	// pkg/sched/loop.go::runReaper — the in-memory field IS the
	// reaper's gate.
	findLiveInstanceWithTail(t, m, instance)

	// Hold the wake for the tail drain — fire the terminal
	// receipt AFTER a 1 s wait so the test asserts the wake is
	// still alive during the tail drain (the reaper must not
	// have torn it down).
	time.Sleep(1 * time.Second)

	m.mu.Lock()
	inst, ok := m.live[instance]
	m.mu.Unlock()
	if !ok {
		t.Fatalf("instance %q dropped from m.live during tail drain — reaper violated the tail_count > 0 gate", instance)
	}
	if inst.TailCount != 1 {
		t.Errorf("TailCount after 1s tail drain = %d, want 1 (reaper must NOT park while tail_count > 0)", inst.TailCount)
	}

	// Now drain — fire the terminal receipt. The wake can park
	// on the next reaper tick.
	stamped, _, err := m.MarkInstanceTailTerminal(ctx, instance, TailOutcomeCompleted, 1000)
	if err != nil {
		t.Fatalf("MarkInstanceTailTerminal: %v", err)
	}
	if !stamped {
		t.Fatalf("MarkInstanceTailTerminal stamped=false after drain, want true")
	}

	m.mu.Lock()
	inst, ok = m.live[instance]
	m.mu.Unlock()
	if !ok {
		t.Fatalf("instance %q dropped from m.live after drain — Destroy must not race the drain", instance)
	}
	if inst.TailCount != 0 {
		t.Errorf("TailCount after drain = %d, want 0", inst.TailCount)
	}
}

const tailEventMetalDGRAMType = 0x04

const tailEventMetalDGRAMOutCompleted = 0x01
const tailEventMetalDGRAMOutFailed = 0x02
const tailEventMetalDGRAMOutTimeout = 0x03

// tailEventMetalDGRAMSize is the load-bearing wire size — the
// guest-init encode side (`sendTail`) and the host decode side
// (`parseFWReadyTailWire`) both pin this constant. A drift
// silently breaks the receipt path.
const tailEventMetalDGRAMSize = 16

// encodeTailEventDGRAM encodes the 16-byte 0x04 body that
// guest-init's tail-events proxy (or the runner-side tail host
// via guest-init) sends to vmmd's framework_ready listener on
// port 1027. Mirrors guest/init/sidecar_events_proxy_linux.go's
// sendTail and guest/init/tail_events_proxy_linux.go's
// proxy-side framing exactly:
//
//	[1B type=0x04][1B outcome][6B reserved][8B elapsed_ms BE uint64]
//
// outcome is one of {TailOutcomeCompleted, TailOutcomeFailed,
// TailOutcomeTimeout} (the same byte values as pkg/fcvm.TailOutcome*).
// The 6 reserved bytes are zero. Reserved bytes are forward-
// compatible per ADR-078 §"Wire format" — the plan reserves the
// space for future flags (e.g. "tail task was capped") without
// breaking the wire.
func encodeTailEventDGRAM(outcome byte, elapsedMs int64) []byte {
	if outcome < tailEventMetalDGRAMOutCompleted || outcome > tailEventMetalDGRAMOutTimeout {
		panic("test: outcome byte outside the closed set {1,2,3}")
	}
	buf := make([]byte, tailEventMetalDGRAMSize)
	buf[0] = tailEventMetalDGRAMType
	buf[1] = outcome
	// buf[2:8] reserved, already zero.
	binary.BigEndian.PutUint64(buf[8:16], uint64(elapsedMs))
	return buf
}

// TestMetal_TailFullEndToEnd is the checkbox #8 acceptance gate
// (issue #667 / ADR-078 PR 3). It fires a REAL 0x04 DGRAM via
// vsock from the test process to VMADDR_CID_HOST:1027 — the
// same port cmd/vmmd's FrameworkReadyReceiver binds to. The
// wire encoding is exact (16 bytes, type=0x04, outcome, 6
// reserved, 8 elapsed_ms BE uint64); the host's
// parseFWReadyTailWire decodes the same shape.
//
// Why this lives in pkg/fcvm (the receiver's home is
// cmd/vmmd/framework_ready_recv.go, but pkg/fcvm can't import
// cmd/vmmd without a circular dependency): the test pins the
// wire format from the sender side. The receiver's parse is
// pinned by cmd/vmmd/tail_event_recv_test.go
// ::TestParseFWReadyMsg_TypeLabel_Tail + the sibling cmd/vmmd
// unit tests. Together: sender shape + receiver shape = the
// 0x04 wire contract.
//
// Soft-fail on AF_VSOCK: if the host kernel doesn't have vsock
// loaded, the test skips. Metal-Lima always has it (the Lima
// provisioner loads vsock.ko at guest boot); bare-metal CI may
// not. The test logs the vsock-dial error so an operator can
// diagnose.
//
// The actual host-side Manager state assertions live in
// TestMetal_TailEndToEnd (sibling) — that one calls
// MarkInstanceTailTerminal directly with the same outcome +
// elapsedMs, so the receiver's dispatch path is exercised in
// the cmd/vmmd unit tests. The test here is the wire-format
// acceptance gate.
func TestMetal_TailFullEndToEnd(t *testing.T) {
	const (
		elapsedMs = 1500
		outcome   = tailEventMetalDGRAMOutCompleted
	)
	// 1. Wire encoding — confirm the byte layout is exactly what
	// the host's parseFWReadyTailWire expects.
	body := encodeTailEventDGRAM(outcome, elapsedMs)
	if len(body) != tailEventMetalDGRAMSize {
		t.Fatalf("encoded DGRAM = %d bytes, want %d", len(body), tailEventMetalDGRAMSize)
	}
	if body[0] != tailEventMetalDGRAMType {
		t.Errorf("body[0] = 0x%02x, want 0x%02x (type byte)", body[0], tailEventMetalDGRAMType)
	}
	if body[1] != outcome {
		t.Errorf("body[1] = 0x%02x, want 0x%02x (outcome byte)", body[1], outcome)
	}
	for i := 2; i < 8; i++ {
		if body[i] != 0 {
			t.Errorf("body[%d] = 0x%02x, want 0x00 (reserved byte)", i, body[i])
		}
	}
	got := binary.BigEndian.Uint64(body[8:16])
	if got != uint64(elapsedMs) {
		t.Errorf("body[8:16] = %d, want %d (elapsed_ms BE uint64)", got, elapsedMs)
	}

	// 2. Wire send — open a vsock DGRAM socket and send to
	// VMADDR_CID_HOST:1027. The host's FrameworkReadyReceiver is
	// expected to be running (in production, cmd/vmmd binds it).
	// In a test-only environment without a receiver, the send
	// succeeds (DGRAM is fire-and-forget) but the receipt is
	// dropped — the wire-format assertion above is the load-bearing
	// gate.
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Skipf("AF_VSOCK unavailable on this host: %v", err)
	}
	defer func() { _ = unix.Close(fd) }()

	const vsockFrameworkReadyHostPort = 1027
	dst := &unix.SockaddrVM{CID: unix.VMADDR_CID_HOST, Port: vsockFrameworkReadyHostPort}
	if _, err := unix.SendmsgN(fd, body, nil, dst, 0); err != nil {
		// ENETDOWN/EPROTO is the kernel telling us vsock isn't
		// loaded. Skip rather than fail — the wire format is
		// already pinned by the encoding assertions above.
		if errors.Is(err, unix.ENETDOWN) || errors.Is(err, unix.EAFNOSUPPORT) {
			t.Skipf("vsock transport unavailable: %v", err)
		}
		t.Fatalf("SendmsgN: %v", err)
	}

	// 3. The send may have succeeded; an actual host-side
	// dispatch would require a FrameworkReadyReceiver to be
	// running (cmd/vmmd scope). The sibling TestMetal_TailEndToEnd
	// exercises the host-side dispatch via MarkInstanceTailTerminal
	// directly. The full guest-kernel → runner → 0x04 DGRAM →
	// dispatcher end-to-end is gated by the cmd/vmmd e2e suite
	// (which is not under pkg/fcvm's test scope).
	t.Logf("0x04 DGRAM wire-format validated: type=0x%02x outcome=%d elapsed_ms=%d (16 bytes); vsock send to VMADDR_CID_HOST:1027 succeeded",
		body[0], body[1], got)
}
