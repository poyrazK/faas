//go:build linux

// Liveness probe — guest-init vsock 1028 STREAM listener
// (issue #554 / ADR-078). The host (cmd/vmmd) dials this port on every
// `Period`; the guest-init watches the runner's existing :8080 listener
// (no runner changes — `guest/runners/node22/main.go:65-68`,
// `guest/runners/python312/main.go:65-68` already register /healthz
// returning 200) and ships a 2xx-only response back to the host. After
// N consecutive failures (per-plan defaults: Hobby/Pro/Scale → 3), the
// host declares the VM wedged and triggers DestroyForLivenessFailure,
// which cold-boots from rootfs per ADR-005.
//
// WIRE FORMAT (mirrors ADR-022's resume hook at
// pkg/fcvm/vmm.go::resumeHookMsgResume for consistency):
//
//	4-byte big-endian msg-type   = VsockLivenessMsgProbe (10)
//	4-byte big-endian body-len
//	N-byte JSON body             = {"path":"/healthz", "timeout_ms":2000}
//
//	(responding)
//	4-byte big-endian msg-type   = VsockLivenessMsgAck (11)
//	4-byte big-endian body-len
//	N-byte JSON body             = {"status":200, "err":""}
//
// Why STREAM (not DGRAM like framework_ready on 1027):
//   - DGRAM is guest→host only (the runner can't speak vsock natively,
//     guest-init dials VMADDR_CID_HOST after a UDS proxy). Liveness is
//     host→guest (vmmd dials the per-VM CID via FC's vsock proxy), so
//     the wire is the same direction as the resume hook (port 1024).
//   - STREAM gives us a length-prefixed boundary so a slow probe
//     doesn't frame-collide with the next probe on a hot VM.
//
// Reuse: the runner's `:8080` health endpoint is the canonical liveness
// target. Issue #554 §4 explicitly chose the runner's existing
// `:8080/healthz` endpoint to avoid a runner shim change — the runners
// already bind :8080 and register /healthz ahead of the user's HTTP
// handler so the probe targets the runtime surface, not the customer's
// app.
package main

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const (
	// VsockLivenessPort is the AF_VSOCK STREAM port the liveness
	// probe listener binds inside the guest. The host's vmmd
	// goroutine dials this port via FC's vsock proxy against the
	// per-slot guest CID (mirrors vmm.go::resumeHookGuestPort =
	// 1024 for the resume hook). Must match on both sides —
	// changing it here requires bumping the host-side constant in
	// cmd/vmmd/liveness_recv.go and the spec §6.4 ADR-078 table.
	VsockLivenessPort = 1028
	// VsockLivenessMsgProbe is the wire-format discriminator the
	// guest accepts on inbound connections. 10 = probe.
	//
	// No collision risk with the existing framework-ready envelope
	// (VsockFrameworkReadyPort = 1027 DGRAM, single-byte
	// discriminator 0x01/0x02/0x03) — the two envelopes live on
	// different ports (1028 STREAM vs 1027 DGRAM) AND use different
	// framings (length-prefixed big-endian vs single-byte). The
	// probe discriminator is also distinct from the resume-hook
	// envelope discriminator (vmm.go::VsockCharacterizationMsgReport)
	// which is on a separate port and uses a JSON body.
	VsockLivenessMsgProbe uint32 = 10
	// VsockLivenessMsgAck is the wire-format discriminator the
	// guest writes back on the response. 11 = ack.
	VsockLivenessMsgAck uint32 = 11
	// VsockLivenessMaxBody is the upper bound on the inbound JSON
	// body. The request is a small struct (path + timeout_ms); 4
	// KiB is generous against accidental growth and pins the
	// per-connection read budget.
	VsockLivenessMaxBody = 4 * 1024
	// VsockLivenessDefaultTimeoutMs is the default per-probe HTTP
	// timeout the guest-init uses when the host's request omits
	// `timeout_ms` (or passes 0). 2 s is the longest "still
	// responsive" probe that doesn't burn the period budget on a
	// single check (the 5 s default period has 3 s of headroom).
	VsockLivenessDefaultTimeoutMs = 2000
	// VsockLivenessHardTimeoutMs caps the host-supplied timeout_ms
	// to protect the guest from a 24 h sleep. 5 s matches the
	// per-plan override ceiling in pkg/api/dto.go::Validate.
	VsockLivenessHardTimeoutMs = 5000
)

// livenessReq is the inbound JSON body from the host's dial.
type livenessReq struct {
	Path      string `json:"path"`
	Port      int    `json:"port,omitempty"`
	TimeoutMs int    `json:"timeout_ms"`
}

// livenessResp is the outbound JSON body the guest-init writes
// after the HTTP probe completes.
//
// Status is the HTTP status code from the runner's :8080 listener
// (0 = "no response received"; the host's failure counter treats
// 0 as timeout / conn-refused). Err is the wire-side error string
// ("timeout", "conn_refused", "runner_not_ready"); empty on a
// clean 2xx. The host maps (status, err) to the four outcome
// classes it tracks in the vmmd_guest_liveness_probe_seconds
// histogram (ok / non_200 / timeout / conn_refused).
//
// WWWAuthenticate is the WWW-Authenticate response header value
// (verbatim, comma-separated list) when the runner responds with
// 401 or 403. Cluster A (error-explanations, spec §6.4 amendment 1):
// forwarded for forward-compat so the host can later discriminate
// "customer app intentionally gated its /healthz" (realm="customer")
// from "platform probe auth round-trip" without another wire-shape
// bump. Empty on any other status. The host reads the field
// defensively — empty = no signal.
type livenessResp struct {
	Status          int    `json:"status"`
	Err             string `json:"err"`
	WWWAuthenticate string `json:"www_authenticate,omitempty"`
}

// listenLivenessHook opens an AF_VSOCK socket bound to VsockLivenessPort
// on VsockResumeBindCID (VMADDR_CID_ANY — same constant as the resume
// hook because vsock ports are independent) and spawns
// acceptLivenessConns in the background. Called from boot() before
// the supervisor starts the app, so the dial from vmmd never races the
// listener coming up.
//
// Binding on VMADDR_CID_ANY accepts inbound on whatever CID Firecracker
// assigned this instance — the slot-derived guest_cid from
// pkg/fcvm/GuestVsockCID. Mirrors listenResumeHook's setup.
//
// The listener is multi-shot: each connection runs one probe. On a
// syscall error (typically the VM exiting) it logs at Debug and
// returns. The boot() caller does not wait on this goroutine.
func listenLivenessHook(log *slog.Logger) error {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	if err != nil {
		return fmt.Errorf("vsock socket: %w", err)
	}
	addr := &unix.SockaddrVM{CID: VsockResumeBindCID, Port: VsockLivenessPort}
	if err := unix.Bind(fd, addr); err != nil {
		_ = unix.Close(fd)
		return fmt.Errorf("vsock bind (cid=VMADDR_CID_ANY) %d: %w", VsockLivenessPort, err)
	}
	if err := unix.Listen(fd, 8); err != nil {
		// backlog 8 — vmmd fires one probe every Period; in
		// the burst signature (10+ wakers landing in the same
		// 5 s window at scale) we want the kernel to queue 8
		// SYN-equivalents without drops. Listen(1) was a
		// resume-specific tuning; the probe needs more.
		_ = unix.Close(fd)
		return fmt.Errorf("vsock listen: %w", err)
	}
	go acceptLivenessConns(fd, log)
	return nil
}

// acceptLivenessConns accepts connections on fd and dispatches each to
// a goroutine running handleLivenessConn. Sequential accepts; each
// handle runs in its own goroutine so a slow HTTP probe (>2 s) does
// not back up the listener. Mirrors acceptResumeConns.
func acceptLivenessConns(fd int, log *slog.Logger) {
	for {
		raw, _, err := unix.Accept(fd)
		if err != nil {
			log.Debug("vsock liveness accept ended", "err", err)
			return
		}
		f := os.NewFile(uintptr(raw), "vsock-liveness")
		go handleLivenessConn(f, log)
	}
}

// handleLivenessConn reads the liveness probe request, runs an HTTP GET
// on the runner's :8080<path>, and writes the {status, err} response.
// Closes the file on return regardless of error.
//
// Fail-closed: on ANY wire error (unknown msg type, body too long, JSON
// parse failure, HTTP error) the guest writes a 0/conn_err ack back so
// the host's failure counter increments. A silent drop would hide a
// wedged guest inside the host's nil-ack timeout — and that timeout
// is the exact signature the host uses to NACK cold-boot decisions.
//
// Wire format (mirrors ADR-022's resume hook at handleResumeConn):
//
//	4-byte big-endian msg-type   = VsockLivenessMsgProbe (10)
//	4-byte big-endian body-len
//	N-byte JSON body             = {"path":"/healthz", "timeout_ms":2000}
//
//	(responding)
//	4-byte big-endian msg-type   = VsockLivenessMsgAck (11)
//	4-byte big-endian body-len
//	N-byte JSON body             = {"status":200, "err":""}
func handleLivenessConn(f *os.File, log *slog.Logger) {
	defer func() { _ = f.Close() }()

	var hdr [8]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		log.Warn("vsock liveness read header", "err", err)
		writeLivenessResp(f, livenessResp{Status: 0, Err: "conn_err"})
		return
	}
	msgType := binary.BigEndian.Uint32(hdr[:4])
	if msgType != VsockLivenessMsgProbe {
		log.Warn("vsock liveness unknown msg type", "type", msgType)
		// Fail-closed: a wrong msg-type (the host-side message
		// version drifted) is a wire-shape regression that the
		// host's metrics + the warn log will surface; we still
		// NACK the probe so the consecutive-failure counter
		// does not silently stall.
		writeLivenessResp(f, livenessResp{Status: 0, Err: "conn_err"})
		return
	}
	bodyLen := binary.BigEndian.Uint32(hdr[4:8])
	if bodyLen == 0 || bodyLen > uint32(VsockLivenessMaxBody) {
		log.Warn("vsock liveness body length out of range", "len", bodyLen, "max", VsockLivenessMaxBody)
		writeLivenessResp(f, livenessResp{Status: 0, Err: "conn_err"})
		return
	}
	body := make([]byte, bodyLen)
	if _, err := io.ReadFull(f, body); err != nil {
		log.Warn("vsock liveness read body", "err", err)
		writeLivenessResp(f, livenessResp{Status: 0, Err: "conn_err"})
		return
	}
	var req livenessReq
	if err := json.Unmarshal(body, &req); err != nil {
		log.Warn("vsock liveness body parse", "err", err)
		writeLivenessResp(f, livenessResp{Status: 0, Err: "conn_err"})
		return
	}
	// Path must start with "/". The host-side Validate enforces
	// this; we re-check here as a second-line defence against a
	// host-side regression that ships an unsanitised path into
	// the guest.
	if !strings.HasPrefix(req.Path, "/") {
		log.Warn("vsock liveness path must start with /", "path", req.Path)
		writeLivenessResp(f, livenessResp{Status: 0, Err: "conn_err"})
		return
	}
	// Apply the timeout floor / ceiling. 0 means "host didn't
	// pass one" — fall back to the default. Hard ceiling is the
	// per-plan override ceiling in pkg/api/dto.go::Validate.
	timeoutMs := req.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = VsockLivenessDefaultTimeoutMs
	}
	if timeoutMs > VsockLivenessHardTimeoutMs {
		timeoutMs = VsockLivenessHardTimeoutMs
	}
	status, errStr, wwwAuth := runLivenessProbe(req.Path, timeoutMs, req.Port)
	writeLivenessResp(f, livenessResp{Status: status, Err: errStr, WWWAuthenticate: wwwAuth})
}

// runLivenessProbe hits the configured runtime port and returns the
// status + err classification + the WWW-Authenticate response header
// (verbatim). Port 0 is the backward-compatible legacy :8080 target.
//
// Returns:
//   - status: the HTTP status code (0 = no response / conn_refused /
//     timeout).
//   - errStr: one of {"timeout", "conn_refused", "runner_not_ready",
//     ""} (empty = clean 2xx; the host's failure counter treats
//     status>=400 or errStr!="" as a probe failure).
//   - wwwAuth: the verbatim WWW-Authenticate header value, only set
//     when status is 401 or 403 (the only two statuses that
//     conventionally carry the header per RFC 7235). Empty
//     otherwise. Cluster A forward-compat — the host reads the
//     field defensively.
func runLivenessProbe(path string, timeoutMs, port int) (int, string, string) {
	// Build the URL. The runner is on loopback; the path is
	// already validated to start with "/". We use the http
	// package's Get with a per-call Client so the timeout is
	// honored atomically — http.Client.Timeout covers the DNS
	// resolve + connect + transfer budget (loopback resolves in
	// 0 ms, no DNS budget burn).
	if port < 1 || port > 65535 {
		port = 8080
	}
	url := "http://127.0.0.1:" + strconv.Itoa(port) + path
	client := &http.Client{Timeout: time.Duration(timeoutMs) * time.Millisecond}
	resp, err := client.Get(url)
	if err != nil {
		// Classify the error. The http package returns
		// *url.Error wrapping the concrete failure mode;
		// we read .Timeout() for the timeout shape and the
		// string match for the conn-refused shape (which
		// Go exposes as a network error without a typed
		// sentinel). Anything else is conn_err.
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return 0, "timeout", ""
		}
		if strings.Contains(err.Error(), "connection refused") {
			return 0, "conn_refused", ""
		}
		return 0, "conn_err", ""
	}
	defer func() { _ = resp.Body.Close() }()
	// Capture WWW-Authenticate ONLY for 401/403 (the only statuses
	// that conventionally carry it per RFC 7235 §4.1). Reading on
	// any other status would burn cycles on the happy path AND
	// could leak a customer's auth scheme to the wire envelope
	// when the runner returns 2xx (none should set the header,
	// but defensive read is cheaper than an invariant test).
	var wwwAuth string
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		wwwAuth = resp.Header.Get("WWW-Authenticate")
	}
	// Drain + discard the body so the connection can be reused.
	// The runner's /healthz returns < 100 B; the cap is the
	// io.DiscardMax.
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, "", wwwAuth
}

// writeLivenessResp serialises the response and writes the wire
// envelope (4B msg-type + 4B body-len + JSON body). On a write
// error the guest logs at Debug — the host has already received
// the probe failure via the response body, so a partial write
// is just a missed ack and the host's deadline will catch it.
func writeLivenessResp(f *os.File, resp livenessResp) {
	body, err := json.Marshal(resp)
	if err != nil {
		// Marshal of a 2-field struct with primitive types — a
		// failure here is a wire-shape regression caught by the
		// test fixture, not a runtime panic. We log + bail.
		slog.Default().Warn("vsock liveness marshal resp", "err", err)
		return
	}
	var hdr [8]byte
	binary.BigEndian.PutUint32(hdr[:4], VsockLivenessMsgAck)
	binary.BigEndian.PutUint32(hdr[4:8], uint32(len(body)))
	if _, err := f.Write(hdr[:]); err != nil {
		slog.Default().Debug("vsock liveness write hdr", "err", err)
		return
	}
	if _, err := f.Write(body); err != nil {
		slog.Default().Debug("vsock liveness write body", "err", err)
	}
}
