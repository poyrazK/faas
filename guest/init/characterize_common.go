// Pure helpers for the characterize probe (ADR-051 Phase 4).
// Lives in a build-tag-free file so the unit tests can exercise
// these on every platform — the linux-only parts of the probe
// (AF_VSOCK, /proc/net/tcp{,6}, /proc/<pid>/fd) are in
// characterize_linux.go, gated `//go:build linux` for the guest
// VM target.
//
// The boundary is intentional: anything that can be tested without
// AF_VSOCK or /proc is here. Anything that needs a real guest VM
// lives in the //go:build metal suite.
package main

import (
	"bufio"
	"context"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// containsNat reports whether the byte slice lists "nat" as a line.
// Avoids a strings.Contains that could match e.g. "nat1234"; the
// file format is one table per line — exact match on the trimmed line.
func containsNat(data []byte) bool {
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "nat" {
			return true
		}
	}
	return false
}

// procNetInodeColumn is the inode's position in a /proc/net/tcp{,6} row:
// sl local_address rem_address st tx:rx tr:tm->when retrnsmt uid timeout inode.
const procNetInodeColumn = 9

// scanListeningFile parses one /proc/net/tcp{,6} for the first
// LISTEN entry (state 0A) whose inode is in `owned`. Returns
// (port, "ip:port", true) on hit. /proc/net/tcp format:
//
//	sl  local_address:port  rem_address  st  tx_queue:rx_queue  ...
//	0:  0100007F:1F90 00000000:0000 0A ...
//
// The address is printed in host byte order (hexIPToString).
//
// Lives here (no build tag) so the parser is unit-tested on every
// platform with a temp-file fixture — the linux-only thing is the
// path passed in.
func scanListeningFile(path string, owned map[uint64]struct{}) (int, string, bool) {
	port, addr, ok, _ := scanListeningFileFor(path, owned, 0)
	return port, addr, ok
}

// scanListeningFileFor returns the owned LISTEN entry on port want when there
// is one, else the first owned LISTEN entry; exact reports the former. An app
// with a second listener (a raw TCP or metrics port) used to be characterized
// on whichever socket /proc listed first, and portnorm then redirected the
// expected HTTP port to that listener (H10-56).
func scanListeningFileFor(path string, owned map[uint64]struct{}, want int) (port int, addr string, ok, exact bool) {
	sockets := scanListenSockets(path, owned)
	if len(sockets) == 0 {
		return 0, "", false, false
	}
	pick := sockets[0]
	for _, socket := range sockets {
		if want > 0 && socket.Port == want {
			pick, exact = socket, true
			break
		}
	}
	// JoinHostPort brackets IPv6 ("[::1]:5173"), which the host's
	// loopback check (pkg/fcvm loopbackBindProblem) can parse.
	return pick.Port, net.JoinHostPort(pick.IP.String(), strconv.Itoa(pick.Port)), true, exact
}

// listenSocket is one LISTEN socket of the app's process tree.
type listenSocket struct {
	IP   net.IP
	Port int
}

// scanListenSockets returns every LISTEN entry (state 0A) of one
// /proc/net/tcp{,6} whose inode is in owned, in file order. The ADR-741
// debugger listener is never the app's, so it is left out.
func scanListenSockets(path string, owned map[uint64]struct{}) []listenSocket {
	//nolint:forbidigo // /proc/net/tcp{,6} is a vetted kernel path inside the
	// guest; the customer-path guard (openCustomerFile) is for host daemons
	// reading customer bytes — this reads in-guest kernel state only.
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	var out []listenSocket
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 10 || fields[3] != "0A" { // not LISTEN
			continue
		}
		// fields[1] = local_address:port (hex)
		ipHex, portHex, found := strings.Cut(fields[1], ":")
		if !found {
			continue
		}
		var port uint16
		if _, sErr := fmt.Sscanf(portHex, "%x", &port); sErr != nil {
			continue
		}
		// The inode is column 9 (sl local rem st tx:rx tr:tm retrnsmt uid
		// timeout INODE ...). It is NOT the last field: a real row carries ref,
		// pointer and timer columns after it, so reading the last field matched a
		// counter — never a socket — and every app on every real kernel
		// characterized as bind_timeout. Only the canned fixtures, whose rows
		// ended at the inode, agreed with the old code.
		var inode uint64
		if _, sErr := fmt.Sscanf(fields[procNetInodeColumn], "%d", &inode); sErr != nil {
			continue
		}
		if _, isOwned := owned[inode]; !isOwned {
			continue
		}
		// ADR-741: the debugger listener is never the app's port.
		if devDebugActive.Load() && int(port) == api.DevDebugNodePort {
			continue
		}
		out = append(out, listenSocket{IP: net.ParseIP(hexIPToString(ipHex)), Port: int(port)})
	}
	return out
}

// scanEstablishedFile parses one /proc/net/tcp{,6} for the count of
// ESTABLISHED entries (state 01) whose inode is in `owned`.
// Mirrors scanListeningFile's parser shape but only returns the
// count — the host's worker-vs-job classification only needs the
// magnitude, not the per-connection address (ADR-051 §"Consequences"
// rules table: ≥1 outbound + still running → `worker`). Excludes
// LISTEN (state 0A) so it doesn't double-count the bind we already
// observed.
//
// Lives here (no build tag) so the parser is unit-tested on every
// platform with a temp-file fixture — the linux-only thing is the
// path passed in.
func scanEstablishedFile(path string, owned map[uint64]struct{}) int {
	//nolint:forbidigo // /proc/net/tcp{,6} is a vetted kernel path inside the
	// guest; the customer-path guard (openCustomerFile) is for host daemons
	// reading customer bytes — this reads in-guest kernel state only.
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	n := 0
	for sc.Scan() {
		line := sc.Text()
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}
		if fields[3] != "01" { // not ESTABLISHED (skip LISTEN, TIME_WAIT, etc.)
			continue
		}
		var inode uint64
		if _, sErr := fmt.Sscanf(fields[procNetInodeColumn], "%d", &inode); sErr != nil {
			continue
		}
		if _, isOwned := owned[inode]; !isOwned {
			continue
		}
		n++
	}
	return n
}

// hexIPToString decodes a /proc/net/tcp{,6} address: 8 hex chars for IPv4,
// 32 for IPv6. The kernel prints each 32-bit word in host byte order
// (little-endian on the x86_64 guest), so "0100007F" is 127.0.0.1 and
// "00000000000000000000000001000000" is ::1. Anything malformed falls back
// to 127.0.0.1; the host re-derives the class, so this never fails a boot.
func hexIPToString(h string) string {
	raw, err := hex.DecodeString(h)
	if err != nil || (len(raw) != net.IPv4len && len(raw) != net.IPv6len) {
		return "127.0.0.1"
	}
	ip := make(net.IP, len(raw))
	for w := 0; w < len(raw); w += 4 {
		ip[w], ip[w+1], ip[w+2], ip[w+3] = raw[w+3], raw[w+2], raw[w+1], raw[w]
	}
	return ip.String()
}

// truncateLog returns at most n bytes of s. Used by the log_tail
// field on the wire — a 64 KiB cap keeps the JSON body within the
// VsockCharacterizationMaxBody budget after JSON overhead.
//
// n <= 0 returns s unchanged: the caller might pass a zero budget
// (a test stub with no buffer, an old wire field) and the safer
// behavior is "log everything" rather than panic. Truncation to a
// negative n is meaningless; a tiny positive n is a no-op.
func truncateLog(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// choosePortNormMode lives in portnorm_common.go (it depends on the
// PortNormMode type + ladder constants, which are platform-agnostic).

// probeListening is the cross-platform entry point. The no-child
// early-out is here (testable on every platform); the /proc walk
// is delegated to probeListeningLinux (gated `//go:build linux`)
// on linux. On non-linux builds, pid > 0 always returns
// (0, "", false) — there is no /proc to walk, and the caller
// polls until deadline.
func probeListening(pid, want int) (int, string, bool) {
	if pid <= 0 {
		return 0, "", false
	}
	return probeListeningLinux(pid, want)
}

// probeResult is the inner return shape of every L7 probe.
// Class is the workload-class hint (empty = inconclusive).
// OpenAPIDoc + OpenAPIDocTruncated are populated by probeHTTP
// only; the other probes leave them at the zero value. HTTP1 reports
// that the listener answered the probe with an HTTP/1.x response of any
// status.
type probeResult struct {
	Class               string
	OpenAPIDoc          []byte
	OpenAPIDocTruncated bool
	HTTP1               bool
	// Stalled: the listener accepted the request but sent nothing before the
	// probe's timeout. An HTTP/1 app still importing its code does that on a
	// cold boot; a prior-knowledge gRPC server rejects HTTP/1.1 at once.
	Stalled bool
}

// collectL7Probes runs the HTTP/1.1 probes concurrently and returns the
// first non-empty class hint, keeping the OpenAPI document from whichever
// probe captured one. h2Probe, which writes the HTTP/2 connection preface,
// runs only when no HTTP/1.1 probe got an HTTP/1.x answer: an HTTP/1 server
// cannot be a prior-knowledge gRPC server, and the preface would only put a
// protocol error in the customer's logs (Hunt 11 H11-5). ctx bounds the
// whole collection; on expiry it returns whatever document it has, with no
// class hint (the engine re-derives from runtime shape).
func collectL7Probes(ctx context.Context, http1Probes []func() probeResult, h2Probe func() probeResult) (string, []byte, bool) {
	resCh := make(chan probeResult, len(http1Probes))
	for _, fn := range http1Probes {
		go func() { resCh <- fn() }()
	}
	var openAPIDoc []byte
	var truncated bool
	spokeHTTP1 := false
	for collected := 0; collected < len(http1Probes); collected++ {
		select {
		case <-ctx.Done():
			return "", openAPIDoc, truncated
		case r := <-resCh:
			if r.OpenAPIDoc != nil && openAPIDoc == nil {
				openAPIDoc, truncated = r.OpenAPIDoc, r.OpenAPIDocTruncated
			}
			if r.Class != "" {
				return r.Class, openAPIDoc, truncated
			}
			// A stalled listener is not a gRPC server either: the preface
			// reached gunicorn while its workers were still booting (Hunt
			// 14 H14-21).
			spokeHTTP1 = spokeHTTP1 || r.HTTP1 || r.Stalled
		}
	}
	if spokeHTTP1 || h2Probe == nil {
		// All probes ran without a class hint. Surface the captured
		// openapi_doc (if any) but DO NOT default to classHTTP — "no
		// probe matched" is itself a signal (ADR-051 §"Class hint
		// fallback").
		return "", openAPIDoc, truncated
	}
	h2Ch := make(chan probeResult, 1)
	go func() { h2Ch <- h2Probe() }()
	select {
	case <-ctx.Done():
		return "", openAPIDoc, truncated
	case r := <-h2Ch:
		return r.Class, openAPIDoc, truncated
	}
}
