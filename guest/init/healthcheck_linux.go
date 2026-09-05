//go:build linux

package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"golang.org/x/sys/unix"
)

// Healthcheck DGRAM wire format (ADR-139 §Decision 1):
//
//	+------+----------+----------+----------+--------------+
//	|  4B  |  4B seq  | 1B status|  4B olen  |  olen bytes  |
//	+------+----------+----------+----------+--------------+
//
// status enum: 0x00=starting, 0x01=pass, 0x02=fail. The olen
// field is the byte length of the captured stdout/stderr (capped
// at VsockHealthcheckMaxOutput = 4096 per ADR-139 §Decision 1).
//
// We deliberately use a binary frame rather than JSON for the
// guest→host leg: the host's vmmd receives N reports per
// minute, parsing JSON per packet is wasted work, and the wire
// is already 4-byte-aligned on the framing boundary (seq +
// status + olen = 9 bytes; the host pads to 12 for alignment).
// The status enum is intentionally small (1 byte) so the
// future "skip" / "disabled" states land without breaking the
// host decoder.
//
// VsockHealthcheckPort mirrors cmd/vmmd/healthcheck_recv.go's
// host-side constant (1029). The guest-init and vmmd packages
// cannot share compile-time symbols — they're separate binaries
// at different stages of the boot chain — so the literal must
// be hand-synced. A drift here is a silent "no healthcheck
// reports" failure mode: the host filter ignores the DGRAM,
// the engine times out and uses the fallback :8080 TCP-accept
// probe. Reviewers: please grep the PR for "1029" — both sides
// must agree.
const VsockHealthcheckPort uint32 = 1029

// VsockHealthcheckMaxOutput is the per-report output byte cap.
// 4096 matches ADR-139 §Decision 1 — large enough for a useful
// diagnostic tail from a failing /bin/check, small enough that
// the host's receive buffer can drain 16 buffered reports in
// ~64 KiB without back-pressure.
const VsockHealthcheckMaxOutput = 4096

// healthcheckStatus is the wire-format status enum. Numeric
// values are stable across versions — the host decoder reads
// them verbatim. Renumbering requires a vmmdpb wire bump.
const (
	healthcheckStatusStarting byte = 0x00
	healthcheckStatusPass     byte = 0x01
	healthcheckStatusFail     byte = 0x02
)

// HealthcheckReport is the typed view of one DGRAM frame. The
// host's vmmd decodes the same struct (mirrored in
// pkg/fcvm/healthcheck.go). Keeping the struct here makes the
// test surface obvious — the unit test for the poll goroutine
// decodes a frame, asserts each field, and pins the wire
// shape.
type HealthcheckReport struct {
	Seq      uint32 // monotonic; 0-based
	Status   byte   // healthcheckStatus*
	Output   []byte // ≤ VsockHealthcheckMaxOutput bytes
	TsUnixMs int64  // guest-side stamp
	// StartPeriodS is the StartPeriodS value from the manifest,
	// used by the host to decide whether this fail counts as a
	// "starting" or a real failure. Mirrored per ADR-139
	// §Decision 1 ("StartPeriodS is the startup grace where
	// fail is downgraded to starting").
	StartPeriodS int
}

// parseHealthcheckTest picks the argv slice to exec from the
// AppManifest.Healthcheck.Test field, plus the dispatch kind.
//
//   - ["CMD", a, b, c] → (exec.Command("a", "b", "c"), false)
//   - ["CMD-SHELL", "x"] → (/bin/sh -c "x", true)
//   - ["NONE"] → (nil, false)  [no goroutine]
//   - empty/nil → (nil, false) [no goroutine, the NONE shape]
//
// The leading keyword is mandatory when Test is non-empty:
// Docker rejects manifests without it, and OCI inherits that
// behaviour. Returning (nil, false) on a malformed Test is
// fail-open: the engine's TCP-accept readiness probe (§13)
// continues to gate readiness, so the customer doesn't lose
// the boot. A warn log fires so a manifest bug surfaces in
// the boot log.
func parseHealthcheckTest(test []string) (argv []string, isShell bool) {
	if len(test) == 0 {
		return nil, false
	}
	switch test[0] {
	case "NONE":
		return nil, false
	case "CMD-SHELL":
		if len(test) < 2 {
			return nil, false
		}
		return []string{"/bin/sh", "-c", test[1]}, true
	case "CMD":
		if len(test) < 2 {
			return nil, false
		}
		return test[1:], false
	default:
		// Treat as CMD — match Docker's permissive parser
		// (a leading-keyword typo would surface as "exit 127"
		// from the first poll, not a hard manifest rejection).
		return test, false
	}
}

// healthcheckDefaults applies Docker's documented defaults when
// a manifest leaves a field at 0. Per ADR-139 §Decision 1, the
// defaults match Docker: 30s interval, 30s timeout, 3 retries,
// 0s start period. The function takes the AppManifest.Healthcheck
// pointer so a nil Healthcheck (the NONE shape) propagates the
// "don't run a poll goroutine" intent.
func healthcheckDefaults(hc *api.AppManifestHealthcheck) (interval, timeout, startPeriod time.Duration, retries int) {
	if hc == nil {
		return 0, 0, 0, 0
	}
	interval = time.Duration(hc.IntervalS) * time.Second
	if interval == 0 {
		interval = 30 * time.Second
	}
	timeout = time.Duration(hc.TimeoutS) * time.Second
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	startPeriod = time.Duration(hc.StartPeriodS) * time.Second
	if hc.Retries == 0 {
		retries = 3
	} else {
		retries = hc.Retries
	}
	return interval, timeout, startPeriod, retries
}

// runStartupHealthcheck performs the first bounded probe for a workload that
// has a declared OCI HEALTHCHECK. A workload without a check is considered
// started-and-ready for dependency purposes; the host's existing readiness
// probe remains authoritative for admitting traffic to the main workload.
func runStartupHealthcheck(manifest api.AppManifest, env []string, dir, root string, uid int, procAttr *syscall.SysProcAttr, log *slog.Logger) error {
	if manifest.Healthcheck == nil {
		return nil
	}
	argv, _ := parseHealthcheckTest(manifest.Healthcheck.Test)
	if len(argv) == 0 {
		return nil
	}
	if root != "" {
		argv[0] = resolveSidecarCommandPath(root, argv[0], env)
	}
	_, timeout, _, _ := healthcheckDefaults(manifest.Healthcheck)
	report := execHealthcheckWithOptions(context.Background(), argv, timeout, uid, env, dir, procAttr, log)
	if report.Status != healthcheckStatusPass {
		return fmt.Errorf("startup healthcheck failed for %q", argv[0])
	}
	return nil
}

// runHealthcheckPoll (M-2 / ADR-139 §Decision 1) is the in-guest
// HEALTHCHECK executor. It opens an AF_VSOCK DGRAM socket on
// VsockHealthcheckPort, spawns a poll goroutine, and ships a
// HealthcheckReport on every attempt's completion. Returns when
// ctx is cancelled (the supervisor's Stop hook cancels the boot
// context — see runSignalHandlers).
//
// Errors from exec or socket I/O are logged at Warn; the
// goroutine continues so a transient /bin/check crash doesn't
// pin the VM forever. The host treats "absence of reports for
// IntervalS × Retries" as fail anyway, so a wedged poll is
// observable as a liveness failure rather than a hang.
//
// argv is the parsed command; if nil (NONE / empty Test), the
// function returns nil immediately — the caller wires a no-op.
//
// uid/gid are the customer's credentials (looked up from
// AppManifest.EffectiveUser at boot); running the check as
// root would let a hostile image read secrets.env and ship
// them via the Output field.
func runHealthcheckPoll(ctx context.Context, manifest api.AppManifest, log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	// A nil Healthcheck is the normal OCI shape for images that do not
	// declare HEALTHCHECK. Treat it exactly like an empty Test field: the
	// legacy TCP readiness probe remains the readiness gate. Do this check
	// before dereferencing the optional pointer below; otherwise every
	// healthcheck-free application crashes guest-init (PID 1) during boot.
	if manifest.Healthcheck == nil {
		log.Info("healthcheck: NONE shape; no poll goroutine")
		return nil
	}
	argv, _ := parseHealthcheckTest(manifest.Healthcheck.Test)
	if len(argv) == 0 {
		log.Info("healthcheck: NONE shape; no poll goroutine")
		return nil
	}

	interval, timeout, startPeriod, retries := healthcheckDefaults(manifest.Healthcheck)

	sock, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("healthcheck: vsock socket: %w", err)
	}
	if err := unix.Bind(sock, &unix.SockaddrVM{
		CID:  unix.VMADDR_CID_ANY,
		Port: VsockHealthcheckPort,
	}); err != nil {
		_ = unix.Close(sock)
		return fmt.Errorf("healthcheck: vsock bind %d: %w", VsockHealthcheckPort, err)
	}

	go func() {
		defer func() { _ = unix.Close(sock) }()
		runHealthcheckPollLoop(ctx, sock, argv, manifest, interval, timeout, startPeriod, retries, log)
	}()
	return nil
}

// runHealthcheckPollLoop is the poll goroutine. Pulled out so
// the unit test can drive it with a buffer-backed DGRAM fake.
// Each iteration:
//
//  1. Wait IntervalS (or 1s during StartPeriod — fast first-poll).
//  2. Exec argv[0] with timeout, capturing stdout/stderr.
//  3. Decide pass/fail/starting (fail during StartPeriod → starting).
//  4. Build HealthcheckReport, send via DGRAM.
//
// The loop exits on ctx.Done() — the boot context the supervisor
// Stop hook cancels.
func runHealthcheckPollLoop(ctx context.Context, sock int, argv []string, manifest api.AppManifest, interval, timeout, startPeriod time.Duration, retries int, log *slog.Logger) {
	uid := lookupUID(manifest.EffectiveUser())
	var seq uint32
	bootAt := time.Now()
	for {
		// Pick the next poll delay: 1s during startPeriod so the
		// first pass lands quickly; IntervalS thereafter. Faster
		// polls during startPeriod let the customer ship a
		// "pass" before the engine times out the boot.
		nextDelay := interval
		if time.Since(bootAt) < startPeriod {
			nextDelay = 1 * time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(nextDelay):
		}

		report := execHealthcheck(ctx, argv, timeout, uid, log)
		report.Seq = seq
		report.TsUnixMs = time.Now().UnixMilli()
		report.StartPeriodS = int(startPeriod / time.Second)

		// ADR-139 §Decision 1: fail during StartPeriodS is
		// downgraded to "starting" so it doesn't count toward
		// Retries. Without this, a slow-starting container
		// (e.g. JVM warmup, database migration) would trip
		// liveness before the workload was actually broken.
		if report.Status == healthcheckStatusFail && time.Since(bootAt) < startPeriod {
			report.Status = healthcheckStatusStarting
		}

		if err := sendHealthcheckReport(sock, report); err != nil {
			log.Warn("healthcheck: send report failed",
				"err", err, "seq", seq)
		}
		seq++
	}
}

// execHealthcheck runs argv[0] once with the timeout, captures
// stdout+stderr, and decides pass/fail based on exit code.
// Pulled out so the unit test exercises the decision tree
// without spawning a goroutine.
func execHealthcheck(ctx context.Context, argv []string, timeout time.Duration, uid int, log *slog.Logger) HealthcheckReport {
	return execHealthcheckWithOptions(ctx, argv, timeout, uid, nil, "", nil, log)
}

// execHealthcheckWithOptions is the startup-gate variant used for sidecar
// dependencies. It preserves the regular healthcheck environment and working
// directory, and can enter a full-rootfs sidecar through SysProcAttr.
func execHealthcheckWithOptions(ctx context.Context, argv []string, timeout time.Duration, uid int, env []string, dir string, procAttr *syscall.SysProcAttr, log *slog.Logger) HealthcheckReport {
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, argv[0], argv[1:]...)
	if env != nil {
		cmd.Env = env
	}
	if dir != "" {
		cmd.Dir = dir
	}
	if procAttr != nil {
		attr := *procAttr
		if attr.Credential == nil && uid > 0 {
			attr.Credential = &syscall.Credential{Uid: uint32(uid), Gid: uint32(uid)}
		}
		cmd.SysProcAttr = &attr
	} else {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
		if uid > 0 {
			cmd.SysProcAttr.Credential = &syscall.Credential{Uid: uint32(uid), Gid: uint32(uid)}
		}
	}
	out, err := cmd.CombinedOutput()
	status := healthcheckStatusPass
	if err != nil {
		status = healthcheckStatusFail
		// DeadlineExceeded is the timeout path; surface as
		// a separate log line so operators can distinguish
		// "binary crashed" from "binary hung past timeout".
		if errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
			if log != nil {
				log.Warn("healthcheck: probe timed out",
					"argv0", argv[0], "timeout", timeout.String())
			}
		}
	}
	// Truncate output to VsockHealthcheckMaxOutput bytes. We
	// keep the LAST bytes (most-recent output is the most
	// useful for diagnostics). A naive "first N bytes" would
	// drop the error message in a script that prints
	// progress before failing.
	if len(out) > VsockHealthcheckMaxOutput {
		out = out[len(out)-VsockHealthcheckMaxOutput:]
	}
	return HealthcheckReport{
		Status: status,
		Output: out,
	}
}

// sendHealthcheckReport frames a HealthcheckReport into the
// binary wire shape and writes it via sendto. Returns the
// error from sendto; the caller logs and continues (a single
// dropped report doesn't fail the guest).
func sendHealthcheckReport(sock int, r HealthcheckReport) error {
	buf := make([]byte, 0, 16+len(r.Output))
	hdr := make([]byte, 13) // 4B msg-type discriminator + 4B seq + 1B status + 4B olen
	// ADR-139: msg-type discriminator = 0x05 — distinct from
	// framework_ready (0x04), sidecar_init_exit (0x02), and
	// characterization (0x03). Same port (1027 multiplexed,
	// 1029 unmuxed — healthcheck is its own port, so the
	// discriminator is mostly defensive).
	binary.BigEndian.PutUint32(hdr[0:4], 0x05)
	binary.BigEndian.PutUint32(hdr[4:8], r.Seq)
	hdr[8] = r.Status
	binary.BigEndian.PutUint32(hdr[9:13], uint32(len(r.Output)))
	buf = append(buf, hdr...)
	buf = append(buf, r.Output...)
	return unix.Sendto(sock, buf, 0, &unix.SockaddrVM{
		CID:  unix.VMADDR_CID_HOST,
		Port: VsockHealthcheckPort,
	})
}

// encodeHealthcheckReport is the test-visible wrapper around
// sendHealthcheckReport. Returns the framed bytes (no I/O)
// so unit tests can assert the wire shape without standing
// up an AF_VSOCK socket on a non-Linux host.
func encodeHealthcheckReport(r HealthcheckReport) []byte {
	buf := make([]byte, 0, 16+len(r.Output))
	hdr := make([]byte, 13)
	binary.BigEndian.PutUint32(hdr[0:4], 0x05)
	binary.BigEndian.PutUint32(hdr[4:8], r.Seq)
	hdr[8] = r.Status
	binary.BigEndian.PutUint32(hdr[9:13], uint32(len(r.Output)))
	buf = append(buf, hdr...)
	buf = append(buf, r.Output...)
	return buf
}

// decodeHealthcheckReport is the matching parser. Returns
// (report, remaining, err). The host side (pkg/fcvm/healthcheck.go)
// uses the same decoder; the unit tests here pin the symmetry.
func decodeHealthcheckReport(b []byte) (HealthcheckReport, error) {
	if len(b) < 13 {
		return HealthcheckReport{}, fmt.Errorf("healthcheck: short frame len=%d", len(b))
	}
	olen := binary.BigEndian.Uint32(b[9:13])
	if uint32(len(b)-13) < olen {
		return HealthcheckReport{}, fmt.Errorf("healthcheck: truncated output frame=%d olen=%d", len(b), olen)
	}
	return HealthcheckReport{
		Seq:    binary.BigEndian.Uint32(b[4:8]),
		Status: b[8],
		Output: append([]byte(nil), b[13:13+olen]...),
	}, nil
}

// healthcheckReportJSON is a debug-only helper for slog fields.
// Kept so a future boot-log upgrade can dump the JSON form
// without re-importing encoding/json in main_linux.go.
var _ = json.Marshal
