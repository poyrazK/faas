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
	"fmt"
	"os"
	"strings"
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

// scanListeningFile parses one /proc/net/tcp{,6} for the first
// LISTEN entry (state 0A) whose inode is in `owned`. Returns
// (port, "ip:port", true) on hit. /proc/net/tcp format:
//
//	sl  local_address:port  rem_address  st  tx_queue:rx_queue  ...
//	0:  0100007F:1F90 00000000:0000 0A ...
//
// Each hex digit pair = one address byte.
//
// Lives here (no build tag) so the parser is unit-tested on every
// platform with a temp-file fixture — the linux-only thing is the
// path passed in.
// procNetInodeColumn is the inode's position in a /proc/net/tcp{,6} row:
// sl local_address rem_address st tx:rx tr:tm->when retrnsmt uid timeout inode.
const procNetInodeColumn = 9

func scanListeningFile(path string, owned map[uint64]struct{}) (int, string, bool) {
	//nolint:forbidigo // /proc/net/tcp{,6} is a vetted kernel path inside the
	// guest; the customer-path guard (openCustomerFile) is for host daemons
	// reading customer bytes — this reads in-guest kernel state only.
	f, err := os.Open(path)
	if err != nil {
		return 0, "", false
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}
		if fields[3] != "0A" { // not LISTEN
			continue
		}
		// fields[1] = local_address:port (hex)
		localHex := fields[1]
		colon := strings.IndexByte(localHex, ':')
		if colon < 0 {
			continue
		}
		ipHex := localHex[:colon]
		portHex := localHex[colon+1:]
		var port uint16
		if _, sErr := fmt.Sscanf(portHex, "%x", &port); sErr != nil {
			continue
		}
		// inode is the LAST field (kernel writes it after all the
		// rx/tx queues + uid/tgid columns). Trim trailing text
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
		ip := hexIPToString(ipHex)
		addr := fmt.Sprintf("%s:%d", ip, port)
		return int(port), addr, true
	}
	return 0, "", false
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

// hexIPToString flips 8 hex chars (4 bytes for tcp, 16 hex for tcp6)
// into dotted-decimal (v4) or colon-hex (v6). Kept conservative;
// the host re-derives the class anyway so a malformed address here
// is logged but doesn't fail the boot.
func hexIPToString(h string) string {
	// Parse a hex-by-hex reverse byte string ("0100007F" → "127.0.0.1").
	if len(h) == 8 {
		var b [4]byte
		for i := 0; i < 4; i++ {
			var v uint8
			_, err := fmt.Sscanf(h[2*i:2*i+2], "%x", &v)
			if err != nil {
				return "127.0.0.1"
			}
			b[i] = v
		}
		// Stored little-endian in /proc/net/tcp — reverse.
		return fmt.Sprintf("%d.%d.%d.%d", b[3], b[2], b[1], b[0])
	}
	// IPv6 kept terse — full expansion is verbose and the host only
	// really needs the first listener.
	return "::1"
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
func probeListening(pid int) (int, string, bool) {
	if pid <= 0 {
		return 0, "", false
	}
	return probeListeningLinux(pid)
}
