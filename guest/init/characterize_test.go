// Pure-helper tests for characterize_linux.go. The wire / vsock /
// /proc paths are not exercised here — they need real AF_VSOCK + a
// customer app, which lives in the //go:build metal suite. This
// file pins the local glue: address parsing, log truncation,
// ladder-mode selection, /proc/net/tcp parsing.
//
// Run on every platform: characterize_linux.go is gated `//go:build linux`
// (it references AF_VSOCK + /proc, which don't exist on darwin), so
// the test file would be unreachable under that gate. The pure
// helpers it exercises don't touch linux-only APIs.
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestHexIPToString_IPv4(t *testing.T) {
	// /proc/net/tcp stores the address in reverse byte order.
	cases := []struct {
		in, want string
	}{
		{"0100007F", "127.0.0.1"}, // localhost
		{"00000000", "0.0.0.0"},   // any
		{"0101A8C0", "192.168.1.1"},
	}
	for _, tc := range cases {
		if got := hexIPToString(tc.in); got != tc.want {
			t.Errorf("hexIPToString(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestHexIPToString_IPv6(t *testing.T) {
	// IPv6 (16 hex chars) collapses to "::1" in the conservative
	// implementation — we only need the first listener, the host
	// re-derives anyway. Pin the literal so the host-side parser
	// stays in sync if we ever expand this.
	if got := hexIPToString("00000000000000000000000000000001"); got != "::1" {
		t.Errorf("hexIPToString(ipv6-loopback) = %q, want %q", got, "::1")
	}
}

func TestHexIPToString_MalformedFallsBack(t *testing.T) {
	// Anything we can't parse lands on 127.0.0.1; the host re-derives
	// the class anyway, so this is just a never-panic contract.
	if got := hexIPToString("ZZZZ"); got == "" {
		t.Errorf("hexIPToString(malformed) = empty, want fallback")
	}
}

func TestTruncateLog(t *testing.T) {
	cases := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{"shorter than n is unchanged", "hello", 100, "hello"},
		{"exactly n is unchanged", "hello", 5, "hello"},
		{"longer returns the last n bytes", "abcdefghij", 3, "hij"},
		{"n <= 0 returns input unchanged", "abc", 0, "abc"},
		{"n negative returns input unchanged", "abc", -5, "abc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := truncateLog(tc.in, tc.n); got != tc.want {
				t.Errorf("truncateLog(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
			}
		})
	}
}

func TestChoosePortNormMode(t *testing.T) {
	// manifest.EffectivePort() falls back to DefaultAppPort (8080)
	// when Port==0; matching the observed bind yields mode=none
	// (rung 1 of the ladder satisfied the customer at build time).
	cases := []struct {
		name     string
		wantPort int
		manifest api.AppManifest
		observed int
		want     PortNormMode
	}{
		{
			name:     "manifest 8080, observed 8080 → none",
			manifest: api.AppManifest{Port: 8080},
			observed: 8080,
			want:     PortNormNone,
		},
		{
			name:     "manifest zero (default 8080), observed 8080 → none",
			manifest: api.AppManifest{},
			observed: 8080,
			want:     PortNormNone,
		},
		{
			name:     "manifest 3000, observed 3000 → none",
			manifest: api.AppManifest{Port: 3000},
			observed: 3000,
			want:     PortNormNone,
		},
		{
			name:     "manifest 8080, observed 5000 → dnat",
			manifest: api.AppManifest{Port: 8080},
			observed: 5000,
			want:     PortNormDNAT,
		},
		{
			name:     "manifest zero, observed 8001 → dnat",
			manifest: api.AppManifest{},
			observed: 8001,
			want:     PortNormDNAT,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := choosePortNormMode(tc.manifest, tc.observed); got != tc.want {
				t.Errorf("choosePortNormMode(%+v, %d) = %q, want %q",
					tc.manifest, tc.observed, got, tc.want)
			}
		})
	}
}

func TestScanListeningFile_MatchOwnedInode(t *testing.T) {
	// Write a fake /proc/net/tcp: two LISTEN entries (one not
	// owned, one owned). Pin the wire format — any drift here
	// breaks the probe silently because the format is documented
	// in scanListeningFile's doc comment.
	dir := t.TempDir()
	path := filepath.Join(dir, "net_tcp")
	content := strings.Join([]string{
		"  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode                                                      ",
		" 0: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12345 1 0000000000000000 100 0 0 10 0",
		" 1: 00000000:0050 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 67890 1 0000000000000000 100 0 0 10 0",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fake proc/net/tcp: %v", err)
	}

	owned := map[uint64]struct{}{67890: {}}
	port, addr, ok := scanListeningFile(path, owned)
	if !ok {
		t.Fatalf("scanListeningFile: expected match, got none")
	}
	if port != 80 {
		t.Errorf("port = %d, want 80", port)
	}
	if !strings.Contains(addr, ":80") {
		t.Errorf("addr = %q, want contains :80", addr)
	}
}

func TestScanListeningFile_IgnoresUnowned(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "net_tcp")
	content := strings.Join([]string{
		"  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode                                                      ",
		" 0: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12345 1 0000000000000000 100 0 0 10 0",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fake proc/net/tcp: %v", err)
	}

	// No owned inodes → no match.
	if _, _, ok := scanListeningFile(path, map[uint64]struct{}{99999: {}}); ok {
		t.Errorf("expected no match for unowned inode, got one")
	}
}

func TestScanListeningFile_IgnoresNonListen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "net_tcp")
	content := strings.Join([]string{
		"  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode                                                      ",
		// state 01 = ESTABLISHED — not LISTEN (0A), should be skipped.
		" 0: 0100007F:1F90 00000000:0000 01 00000000:00000000 00:00000000 00000000     0        0 12345 1 0000000000000000 100 0 0 10 0",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fake proc/net/tcp: %v", err)
	}

	if _, _, ok := scanListeningFile(path, map[uint64]struct{}{12345: {}}); ok {
		t.Errorf("expected non-LISTEN state to be ignored, got a match")
	}
}

func TestScanEstablishedFile_MatchOwnedInode(t *testing.T) {
	// One ESTABLISHED entry, owned by the app. Mirrors the LISTEN
	// counterpart's shape — same /proc/net/tcp wire format, only the
	// state filter flips (01 ESTABLISHED vs 0A LISTEN).
	dir := t.TempDir()
	path := filepath.Join(dir, "net_tcp")
	content := strings.Join([]string{
		"  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode                                                      ",
		" 0: 0100007F:1F90 0100007F:0050 01 00000000:00000000 00:00000000 00000000     0        0 12345 1 0000000000000000 100 0 0 10 0",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fake proc/net/tcp: %v", err)
	}
	if got := scanEstablishedFile(path, map[uint64]struct{}{12345: {}}); got != 1 {
		t.Errorf("scanEstablishedFile = %d, want 1", got)
	}
}

func TestScanEstablishedFile_IgnoresListen(t *testing.T) {
	// LISTEN (0A) entries must not be counted as outbound — the bind
	// observation already saw them via scanListeningFile. Otherwise
	// a server-class app would always classify as worker.
	dir := t.TempDir()
	path := filepath.Join(dir, "net_tcp")
	content := strings.Join([]string{
		"  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode                                                      ",
		" 0: 00000000:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12345 1 0000000000000000 100 0 0 10 0",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fake proc/net/tcp: %v", err)
	}
	if got := scanEstablishedFile(path, map[uint64]struct{}{12345: {}}); got != 0 {
		t.Errorf("scanEstablishedFile counted LISTEN entry: got %d, want 0", got)
	}
}

func TestScanEstablishedFile_IgnoresUnowned(t *testing.T) {
	// ESTABLISHED but inode not in the owned set (e.g. a
	// guest-init–internal socket, an unrelated process). The
	// owned-inode filter matches scanListeningFile's contract.
	dir := t.TempDir()
	path := filepath.Join(dir, "net_tcp")
	content := strings.Join([]string{
		"  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode                                                      ",
		" 0: 0100007F:1F90 0100007F:0050 01 00000000:00000000 00:00000000 00000000     0        0 12345 1 0000000000000000 100 0 0 10 0",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fake proc/net/tcp: %v", err)
	}
	if got := scanEstablishedFile(path, map[uint64]struct{}{99999: {}}); got != 0 {
		t.Errorf("scanEstablishedFile counted unowned inode: got %d, want 0", got)
	}
}

func TestScanEstablishedFile_CountsMultiple(t *testing.T) {
	// Three ESTABLISHED entries, two owned — count must be 2, not 3.
	// Pins that the function is a true count, not "first match wins".
	dir := t.TempDir()
	path := filepath.Join(dir, "net_tcp")
	content := strings.Join([]string{
		"  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode                                                      ",
		" 0: 0100007F:1F90 0100007F:0050 01 00000000:00000000 00:00000000 00000000     0        0 11111 1 0000000000000000 100 0 0 10 0",
		" 1: 0100007F:1F91 0100007F:0050 01 00000000:00000000 00:00000000 00000000     0        0 22222 1 0000000000000000 100 0 0 10 0",
		" 2: 0100007F:1F92 0100007F:0050 01 00000000:00000000 00:00000000 00000000     0        0 33333 1 0000000000000000 100 0 0 10 0",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fake proc/net/tcp: %v", err)
	}
	if got := scanEstablishedFile(path, map[uint64]struct{}{11111: {}, 33333: {}}); got != 2 {
		t.Errorf("scanEstablishedFile = %d, want 2", got)
	}
}

func TestScanEstablishedFile_MissingFileIsZero(t *testing.T) {
	// Defensive contract: a missing /proc path returns 0, not an
	// error. The caller treats 0 as a legitimate "no outbound"
	// signal (a job that never connects out, or a guest kernel
	// without /proc/net/tcp compiled in).
	if got := scanEstablishedFile("/does/not/exist", map[uint64]struct{}{1: {}}); got != 0 {
		t.Errorf("scanEstablishedFile(missing) = %d, want 0", got)
	}
}

func TestProbeListening_NoChildEarlyOut(t *testing.T) {
	// pid <= 0 short-circuits — caller polls again. Pin the contract
	// so a regression to "scan all inodes anyway" doesn't silently
	// observe an init-script-spawned daemon and misclassify.
	for _, pid := range []int{-1, 0} {
		if port, _, ok := probeListening(pid); ok || port != 0 {
			t.Errorf("probeListening(%d) = (%d, _, %v), want (0, _, false)", pid, port, ok)
		}
	}
}

func TestContainsNat(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"nat\nfilter\n", true},
		{"\nfilter\nraw\n", false},
		{"", false},
		{"nat1234\n", false}, // exact-line match, not prefix
		{"nat\n", true},
	}
	for _, tc := range cases {
		if got := containsNat([]byte(tc.in)); got != tc.want {
			t.Errorf("containsNat(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestPortNormMode_StringValues(t *testing.T) {
	// The metric label maps 1:1 to the string. Lock the values so a
	// refactor doesn't silently rename a metric series and break
	// dashboards.
	cases := map[PortNormMode]string{
		PortNormNone:    "none",
		PortNormDNAT:    "dnat",
		PortNormForward: "forward",
	}
	for mode, want := range cases {
		if string(mode) != want {
			t.Errorf("PortNormMode(%v) = %q, want %q", mode, string(mode), want)
		}
	}
}

func TestShipReportRetriesBoundedByConstant(t *testing.T) {
	// shipReport's loop is `attempts := VsockCharacterizationRetries + 1`.
	// Pin the constant: a change here affects boot-time tail latency
	// and is the kind of thing a well-meaning refactor would silently
	// raise. 4 = 1 initial + 3 retries; total budget ~1.85s with the
	// backoff table (100+250+500 + 3*1500ms per-attempt ack wait).
	//
	// The wire constants live in characterize_linux.go (`//go:build linux`)
	// because the wire-format framing references AF_VSOCK + msg_type. We
	// can't pin them on every platform — the test belongs in the
	// //go:build metal suite next to shipOnce.
	t.Skip("wire constants pinned in //go:build metal suite; see characterize_linux.go")
}

func TestTruncateLog_VsockBodyCap(t *testing.T) {
	// The wire-format body cap is VsockCharacterizationMaxBody (32 KiB);
	// truncateLog is the actual boundary function. Pin the contract
	// so the LogTail field never overflows the JSON body after
	// framing overhead.
	const cap = 32 * 1024
	big := bytes.Repeat([]byte{'x'}, cap*2)
	out := truncateLog(string(big), cap)
	if len(out) != cap {
		t.Errorf("truncateLog len = %d, want %d", len(out), cap)
	}
}
