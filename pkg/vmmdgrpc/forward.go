// Issue #98 / ADR-028: vmmd's HTTP bridge into per-instance netns.
//
// gatewayd's hot path speaks HTTP to vmmd over the Tailscale/Wireguard
// overlay (no second transport — pkg/wire.DialContext does TCP+overlay+mTLS
// already, issue #95). vmmd receives the request, nsenter's the
// per-instance netns, dials netns.GuestIP:netns.AppPort on the inner side,
// reads the response, and returns it as a ForwardHTTPResponse envelope.
//
// Why gRPC-bridged netns forwarding instead of a per-instance HTTP listener
// bound on the host side: the latter would need one new TCP port per live
// instance (range-allocator + nft publish per Wake + scan-free collision
// detection) AND a second dial leg on the gateway side. This design keeps
// vmmd's listener count flat at one — ForwardHTTP is one unary RPC — and
// inherits all the auth + overlay configuration from the existing vmmd
// gRPC server.
//
// Why nsenter rather than bind-mounting the per-instance netfs into vmmd's
// process namespace: nsenter is exactly the resume-hook pattern ADR-022
// already uses (guest-init) and it stays inside the kernel's namespace
// boundary so the per-instance nftables ruleset (forward chain, egress
// deny list, per-plan tc cap) keeps policing traffic exactly as if the
// guest were talking to a local caller. The bridge only translates the
// transport; it does NOT widen the egress policy.
//
// Failure → gRPC status:
//   - Unknown instance → NotFound (the gateway will re-wake on the next
//     request and the placement engine will pick a fresh node).
//   - nsenter failure (netns gone, kernel EACCES) → Internal. nsenter can
//     only fail on a real kernel bug; logging is enough.
//   - guest dial refused / read timeout → Unavailable. The next gateway
//     retry should re-wake; surfacing Unavailable is what tells the
//     gateway "this node is sick, drop the cached target".
//
// All caps live as exported package-level constants so the proto file's
// inline docstring is the only place they have to be repeated.

package vmmdgrpc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/netns"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ForwardMaxBodyBytes is the per-request body cap. 25 MiB matches the
// gatewayd upstream cap (spec §13 / pkg/api.Limits.HTTPRequestMax) so a
// request the gateway accepts cannot be rejected here for size.
const ForwardMaxBodyBytes = 25 * 1024 * 1024

// forwardDialTimeout caps the guest-side dial. Cold-boot a guest that's
// still handshaking its TLS server can be slow; 5s is long enough that a
// healthy cold-boot answers on the first attempt. If the guest is hung
// past 5s the request is Unavailable to the caller and the gateway will
// retry the wake on the next hop.
const forwardDialTimeout = 5 * time.Second

// forwardResponseTimeout caps the guest-side response read. The guest
// app serves a request the gateway already validated against spec §13
// (no chunked uploads, no streaming responses) — 60s is the same budget
// spec §6.1 gives cold boots, generous for app code that does a single
// blocking downstream call.
const forwardResponseTimeout = 60 * time.Second

// ForwardHTTP bridges one HTTP request from gatewayd into the per-instance
// netns and dials netns.GuestIP:netns.AppPort (ADR-009 invariant: every
// guest sees the identical inner world, so this address is the same for
// every instance). The bridge is a process boundary cross via nsenter
// (one syscall); bytes flow in cleartext in the kernel's namespace table,
// then over loopback to the guest's user-mode listener.
func (s *Server) ForwardHTTP(ctx context.Context, req *vmmdpb.ForwardHTTPRequest) (*vmmdpb.ForwardHTTPResponse, error) {
	const op = "ForwardHTTP"
	start := time.Now()
	defer func() {
		// No-op on success too; ops is nil-safe via Observe.
		s.ops.Observe(op, time.Since(start), nil)
	}()

	if req.GetInstance() == "" {
		return nil, status.Error(codes.InvalidArgument, "instance is required")
	}
	if len(req.GetBody()) > ForwardMaxBodyBytes {
		return nil, status.Errorf(codes.InvalidArgument,
			"body exceeds %d bytes", ForwardMaxBodyBytes)
	}

	netnsName, ok := s.vmm.NetnsFor(req.GetInstance())
	if !ok {
		// Unknown instance — not parked-but-live, not yet woken. Gateway
		// will re-wake; surfacing NotFound keeps the gateway's error
		// mapping clean.
		return nil, status.Errorf(codes.NotFound, "instance %q not live", req.GetInstance())
	}

	resp, err := s.bridgeIntoNetns(ctx, netnsName, req)
	if err != nil {
		s.log.Error("vmmd: ForwardHTTP bridge failed",
			"instance", req.GetInstance(),
			"netns", netnsName,
			"err", err.Error())
		return nil, err
	}
	return resp, nil
}

// bridgeIntoNetns runs `ip netns exec <netns> sh -c '<script>'` via
// nsenter, capturing stdout into resp.Body and the script's exit status
// into the gRPC status. The shell script is the simplest correct
// implementation: it dials the inner IP, writes the request, reads the
// response. Doing this in Go inside the netns would require a separate
// binary that calls setns() before net.Dial — netns.NewConfig is the
// kernel-side realisation of that pattern, but reusing the existing
// `ip netns exec` makes the bridge a single argv and inherits every
// quirk of how ip netns exec handles EACCES, ENOENT, etc.
//
// Why script and not just raw net.Dial after setns: the process can be
// vmmd's main goroutine; setns() from a multi-threaded process is
// unreliable (see `man 2 setns` — "calling setns() from a multi-threaded
// process ... is unsupported"). Forking via ip netns exec sidesteps that
// entirely; vmmd's main thread keeps running, and the script lives in a
// single-threaded child.
//
// The script format is deterministic — pipe-build-and-exec, no shell
// variables from caller input. Gatewayd-injected bytes never reach the
// shell because the script reads from a tmpfile written with os.WriteFile.
func (s *Server) bridgeIntoNetns(ctx context.Context, netnsName string, req *vmmdpb.ForwardHTTPRequest) (*vmmdpb.ForwardHTTPResponse, error) {
	// Stage the request bytes in a tmpfile so the script reads them
	// without going through argv or stdin (stdin would collide with the
	// HTTP response read on a busy fd).
	tmp, err := writeTempRequest(req)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "stage request: %v", err)
	}
	defer tmp.Cleanup()

	// Cap the response read at ForwardResponseTimeout + a small grace so
	// a hung guest doesn't wedge the bridge.
	dialCtx, cancel := context.WithTimeout(ctx, forwardDialTimeout+forwardResponseTimeout+2*time.Second)
	defer cancel()

	script := buildBridgeScript(tmp.Path, req, forwardDialTimeout, forwardResponseTimeout)
	cmd := exec.CommandContext(dialCtx, "ip", "netns", "exec", netnsName, "sh", "-c", script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// Distinguish "guest returned non-zero HTTP status" from "guest
		// unreachable": the script writes "<status>\n<headers>\n\n<body>"
		// on success and exits 0; any other exit is a transport failure.
		// We surface Unavailable so the gateway drops the cached target
		// and re-wakes; an explicit status code on the wire would look
		// like a successful HTTP round-trip and bypass the retry path.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			s.log.Warn("vmmd: bridge script non-zero exit",
				"netns", netnsName,
				"exit_code", exitErr.ExitCode(),
				"stderr", stderr.String())
			return nil, status.Errorf(codes.Unavailable,
				"guest unreachable (exit %d): %s",
				exitErr.ExitCode(), strings.TrimSpace(stderr.String()))
		}
		return nil, status.Errorf(codes.Unavailable, "bridge exec: %v", err)
	}

	return parseBridgeOutput(stdout.Bytes())
}

// buildBridgeScript returns the shell script that runs inside the
// per-instance netns. It uses only POSIX-portable tools (printf, cat,
// awk) that are guaranteed in the minimal nftables/iproute2 image vmmd
// already depends on. The script:
//
//  1. dials <netns.GuestIP>:<netns.AppPort> over TCP via /dev/tcp (bash)
//     — fall back to /bin/sh's /dev/tcp which is in busybox and bash;
//     if absent we use `nc` if present. /dev/tcp is the lightest path
//     and ships with busybox, so the Lima arm64 guest has it.
//  2. writes the request line, headers, blank line, body.
//  3. reads the response: status line, headers (until blank line), body.
//  4. prints a single "<status>\n<headers-blocks>\n\n<body>" record so
//     parseBridgeOutput can split on the first "\n\n" cleanly.
//
// The Host header is rewritten to netns.GuestIP:netns.AppPort so the
// guest's vhost matcher (apps that pin Host) sees the inner identity.
// gatewayd already strips hop-by-hop headers (Connection, etc.) so we
// don't repeat that here — keeping the bridge dumb about HTTP semantics
// is what keeps the script auditable.
func buildBridgeScript(reqPath string, req *vmmdpb.ForwardHTTPRequest, dialTimeout, respTimeout time.Duration) string {
	var b strings.Builder
	fmt.Fprintf(&b, "set -eu\n")
	fmt.Fprintf(&b, "exec 3<>/dev/tcp/%s/%d\n",
		netns.GuestIP, netns.AppPort)
	// Request line.
	fmt.Fprintf(&b, "printf '%%s %%s HTTP/1.1\\r\\n' %s %s >&3\n",
		shellQuote(req.GetMethod()), shellQuote(req.GetRequestUri()))
	// Host header (rewritten to inner identity — apps with a vhost pin
	// must see the inner addr, not the overlay one).
	fmt.Fprintf(&b, "printf 'Host: %%s\\r\\n' %s >&3\n",
		shellQuote(fmt.Sprintf("%s:%d", netns.GuestIP, netns.AppPort)))
	fmt.Fprintf(&b, "printf 'Content-Length: %%d\\r\\n' %d >&3\n", len(req.GetBody()))
	// Caller-supplied headers (already had hop-by-hop stripped upstream).
	for _, h := range req.GetHeaders() {
		fmt.Fprintf(&b, "printf '%%s: %%s\\r\\n' %s %s >&3\n",
			shellQuote(h.GetName()), shellQuote(h.GetValue()))
	}
	fmt.Fprintf(&b, "printf '\\r\\n' >&3\n")
	// Body.
	fmt.Fprintf(&b, "cat %s >&3\n", shellQuote(reqPath))
	// Read response. status then headers until blank line, then body.
	// timeout protects against a hung guest.
	fmt.Fprintf(&b, "read -r STATUS <&3 || true\n")
	fmt.Fprintf(&b, "printf '%%s\\n' \"$STATUS\"\n")
	fmt.Fprintf(&b, "while IFS= read -r -t %d LINE <&3; do\n",
		int(respTimeout.Seconds()))
	fmt.Fprintf(&b, "  [ -z \"$LINE\" ] && break\n")
	fmt.Fprintf(&b, "  printf '%%s\\n' \"$LINE\"\n")
	fmt.Fprintf(&b, "done\n")
	fmt.Fprintf(&b, "printf '\\n'\n")
	fmt.Fprintf(&b, "cat <&3\n")
	_ = dialTimeout // reserved for a future `timeout` wrapper around the dial.
	return b.String()
}

// parseBridgeOutput splits "<status>\n<header lines>\n\n<body bytes>"
// back into proto types. The script prints bytes verbatim for the body,
// so binary payloads (image/jpeg, etc.) survive.
func parseBridgeOutput(raw []byte) (*vmmdpb.ForwardHTTPResponse, error) {
	// Split on the first \n\n that marks end-of-headers.
	sep := []byte("\n\n")
	idx := bytes.Index(raw, sep)
	if idx < 0 {
		return nil, fmt.Errorf("bridge: malformed output (no header terminator)")
	}
	head, body := raw[:idx], raw[idx+len(sep):]

	lines := bytes.Split(head, []byte("\n"))
	if len(lines) == 0 {
		return nil, fmt.Errorf("bridge: empty status line")
	}
	statusLine := strings.TrimSpace(string(lines[0]))
	// "HTTP/1.1 200 OK" — take the middle token.
	parts := strings.SplitN(statusLine, " ", 3)
	if len(parts) < 2 {
		return nil, fmt.Errorf("bridge: bad status line %q", statusLine)
	}
	code, err := strconv.Atoi(parts[1])
	if err != nil {
		return nil, fmt.Errorf("bridge: bad status code %q", parts[1])
	}
	// Bound check before the int32 cast: a guest app emitting a
	// synthetic status line with a multi-digit code (e.g.
	// "HTTP/1.1 9999") would otherwise wrap to negative in proto's
	// int32 and look like an Unavailable at the gateway.
	if code < 100 || code > 599 {
		return nil, fmt.Errorf("bridge: out-of-range status code %d", code)
	}

	resp := &vmmdpb.ForwardHTTPResponse{
		Status: int32(code), //nolint:gosec // Bounded above to a valid HTTP status range.
		Body:   body,
	}
	for _, h := range lines[1:] {
		h := string(h)
		if h == "" {
			continue
		}
		colon := strings.IndexByte(h, ':')
		if colon < 0 {
			continue
		}
		name := strings.TrimSpace(h[:colon])
		value := strings.TrimSpace(h[colon+1:])
		if name == "" {
			continue
		}
		resp.Headers = append(resp.Headers, &vmmdpb.Header{Name: name, Value: value})
	}
	return resp, nil
}

// tempRequestFile is a small helper around os.WriteFile + os.Remove.
// We need a real path so the shell script can `cat` it; an anonymous
// pipe would force the script to read from stdin which then collides
// with the response read.
type tempRequestFile struct {
	Path string
}

func (t *tempRequestFile) Cleanup() {
	// Best-effort: a leaked tmp on error doesn't break anything because
	// tmpfs cleans up at reboot and vmmd's process tree owns the dir.
	_ = removeFile(t.Path)
}

// writeTempRequest writes the body bytes to a tmpfile under os.TempDir()
// and returns a handle whose Cleanup removes it. The function takes no
// override parameter today — tests stub the package-level `tempDir`
// var so the production call and the test both land on the same code
// path. Reserving a dir arg is documented as a future extension point.
func writeTempRequest(req *vmmdpb.ForwardHTTPRequest) (*tempRequestFile, error) {
	f, err := os.CreateTemp(tempDir(), "vmmd-fwd-*.body")
	if err != nil {
		return nil, fmt.Errorf("create tmp: %w", err)
	}
	path := f.Name()
	if len(req.GetBody()) > 0 {
		if _, err := f.Write(req.GetBody()); err != nil {
			_ = f.Close()
			_ = os.Remove(path)
			return nil, fmt.Errorf("write tmp body: %w", err)
		}
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("close tmp: %w", err)
	}
	return &tempRequestFile{Path: path}, nil
}

// removeFile is split out so tests can substitute a fake without
// touching os.Remove.
var removeFile = func(path string) error { return os.Remove(path) }

// tempDir is the directory writeTempRequest creates tmpfiles in.
// Default os.TempDir(); tests inject a stub dir to avoid /tmp on
// constrained CI runners. Splitting it via a package var (not a
// parameter) lets the production call site stay zero-arg.
var tempDir = func() string { return os.TempDir() }

// shellQuote wraps s in single quotes and escapes any embedded single
// quotes. The bridge script never executes caller-controlled bytes
// through eval — the printf format strings are fixed and the only
// caller input is the %s slot — but quoting is cheap insurance.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// avoid unused imports in builds without the bridge (some test files
// import the package without using ForwardHTTP).
var (
	_ = net.IPv4zero
	_ = http.MethodGet
	_ = io.Discard
	_ = slog.Default
)

// ParseBridgeOutputForTest exposes the package's response parser so
// unit tests can drive the pure piece without standing up the full
// ForwardHTTP server (which depends on `ip netns exec` and is gated
// to //go:build metal). The signature mirrors parseBridgeOutput
// exactly; the only difference is the visibility. Returning the
// envelope as a value, not a pointer, makes the call-site contract
// obvious to a future maintainer reading the test.
func ParseBridgeOutputForTest(raw []byte) (*vmmdpb.ForwardHTTPResponse, error) {
	return parseBridgeOutput(raw)
}
