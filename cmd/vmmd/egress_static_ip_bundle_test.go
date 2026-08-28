// ADR-119: unit tests for the static egress IP bundle loader.
// Mirrors egress_bundle_test.go's table-driven shape but for
// the per-app tuple list. The loader rejects reserved-range
// IPs, IPv6 (v6 deferred), malformed rows, and dedup's on
// app_id (last-wins) — the same posture the apid handler
// enforces upstream.
package main

import (
	"context"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// silentStaticIPLogger discards all log output so the bundle
// tests don't pollute the test binary's stderr. Errors are
// surfaced by the function's error return, not by slog.Warn.
func silentStaticIPLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestLoadStaticEgressIPBundle_Valid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "static_egress_ips.toml")
	content := `# ADR-119 — operator static egress IP bundle.
[[entries]]
account_id = "11111111-1111-1111-1111-111111111111"
app_id = "shop"
ip = "203.0.113.42"

[[entries]]
account_id = "11111111-1111-1111-1111-111111111111"
app_id = "api"
ip = "198.51.100.7"
`
	if err := os.WriteFile(path, []byte(content), 0o400); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	bundle, err := LoadStaticEgressIPBundle(path, silentStaticIPLogger())
	if err != nil {
		t.Fatalf("LoadStaticEgressIPBundle: %v", err)
	}
	if len(bundle.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(bundle.Entries))
	}
	if got := bundle.Entries[0].AppID; got != "api" {
		t.Errorf("entries[0].AppID = %q, want api", got)
	}
	if got := bundle.Entries[0].IP.String(); got != "198.51.100.7" {
		t.Errorf("entries[0].IP = %q, want 198.51.100.7", got)
	}
	if got := bundle.Entries[1].AppID; got != "shop" {
		t.Errorf("entries[1].AppID = %q, want shop", got)
	}
}

// TestLoadStaticEgressIPBundle_MissingFile — a missing file is
// not an error; the loader returns the zero-value bundle so the
// SIGHUP-driven reload path can treat "no file" as "remove all
// aliases" rather than refusing to start.
func TestLoadStaticEgressIPBundle_MissingFile(t *testing.T) {
	bundle, err := LoadStaticEgressIPBundle("/nonexistent/static_egress_ips.toml", silentStaticIPLogger())
	if err != nil {
		t.Fatalf("missing file: err = %v, want nil", err)
	}
	if len(bundle.Entries) != 0 {
		t.Errorf("missing file: entries = %d, want 0", len(bundle.Entries))
	}
}

// TestLoadStaticEgressIPBundle_DropsReservedRanges pins the
// deny set: RFC1918, CGN, link-local, multicast, loopback. The
// same deny set apid enforces at the API layer is mirrored
// here so an operator typo can't pin a reserved IP.
func TestLoadStaticEgressIPBundle_DropsReservedRanges(t *testing.T) {
	cases := []struct {
		name string
		ip   string
		want bool
	}{
		{"RFC1918", "10.1.2.3", false},
		{"CGN", "100.64.0.1", false},
		{"loopback", "127.0.0.1", false},
		{"link-local", "169.254.1.1", false},
		{"multicast", "224.0.0.1", false},
		{"TEST-NET-1", "192.0.2.5", true},
		{"public", "203.0.113.42", true},
		{"public-alt", "198.51.100.7", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "static_egress_ips.toml")
			content := "[[entries]]\naccount_id = \"11111111-1111-1111-1111-111111111111\"\napp_id = \"x\"\nip = \"" + tc.ip + "\"\n"
			if err := os.WriteFile(path, []byte(content), 0o400); err != nil {
				t.Fatalf("write: %v", err)
			}
			bundle, err := LoadStaticEgressIPBundle(path, silentStaticIPLogger())
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			found := false
			for _, e := range bundle.Entries {
				if e.IP.String() == tc.ip {
					found = true
				}
			}
			if found != tc.want {
				t.Errorf("ip %q in entries = %v, want %v", tc.ip, found, tc.want)
			}
		})
	}
}

// TestLoadStaticEgressIPBundle_DropsIPv6 — IPv6 deferred per
// ADR-119.
func TestLoadStaticEgressIPBundle_DropsIPv6(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "static_egress_ips.toml")
	content := "[[entries]]\naccount_id = \"11111111-1111-1111-1111-111111111111\"\napp_id = \"x\"\nip = \"2001:db8::1\"\n"
	if err := os.WriteFile(path, []byte(content), 0o400); err != nil {
		t.Fatalf("write: %v", err)
	}
	bundle, err := LoadStaticEgressIPBundle(path, silentStaticIPLogger())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(bundle.Entries) != 0 {
		t.Errorf("v6 entry must be dropped, got %d entries", len(bundle.Entries))
	}
}

// TestLoadStaticEgressIPBundle_DropsMalformed — malformed IP
// rows are dropped without poisoning the rest of the file.
func TestLoadStaticEgressIPBundle_DropsMalformed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "static_egress_ips.toml")
	content := `[[entries]]
account_id = "11111111-1111-1111-1111-111111111111"
app_id = "good"
ip = "203.0.113.42"

[[entries]]
account_id = "11111111-1111-1111-1111-111111111111"
app_id = "bad"
ip = "not-an-ip"
`
	if err := os.WriteFile(path, []byte(content), 0o400); err != nil {
		t.Fatalf("write: %v", err)
	}
	bundle, err := LoadStaticEgressIPBundle(path, silentStaticIPLogger())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(bundle.Entries) != 1 {
		t.Fatalf("entries = %d, want 1 (the bad row must be dropped, not poison)", len(bundle.Entries))
	}
	if bundle.Entries[0].AppID != "good" {
		t.Errorf("entries[0].AppID = %q, want good", bundle.Entries[0].AppID)
	}
}

// TestLoadStaticEgressIPBundle_LastWinsPerApp — two rows for
// the same app collapse to the last one.
func TestLoadStaticEgressIPBundle_LastWinsPerApp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "static_egress_ips.toml")
	content := `[[entries]]
account_id = "11111111-1111-1111-1111-111111111111"
app_id = "shop"
ip = "203.0.113.42"

[[entries]]
account_id = "11111111-1111-1111-1111-111111111111"
app_id = "shop"
ip = "198.51.100.7"
`
	if err := os.WriteFile(path, []byte(content), 0o400); err != nil {
		t.Fatalf("write: %v", err)
	}
	bundle, err := LoadStaticEgressIPBundle(path, silentStaticIPLogger())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(bundle.Entries) != 1 {
		t.Fatalf("entries = %d, want 1 (last-wins dedup)", len(bundle.Entries))
	}
	if bundle.Entries[0].IP.String() != "198.51.100.7" {
		t.Errorf("entries[0].IP = %q, want 198.51.100.7 (last wins)", bundle.Entries[0].IP.String())
	}
}

// TestLoadStaticEgressIPBundle_DropsEmptyRows — empty app_id
// or empty ip is rejected.
func TestLoadStaticEgressIPBundle_DropsEmptyRows(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "static_egress_ips.toml")
	content := `[[entries]]
account_id = "11111111-1111-1111-1111-111111111111"
app_id = ""
ip = "203.0.113.42"

[[entries]]
account_id = "11111111-1111-1111-1111-111111111111"
app_id = "shop"
ip = ""
`
	if err := os.WriteFile(path, []byte(content), 0o400); err != nil {
		t.Fatalf("write: %v", err)
	}
	bundle, err := LoadStaticEgressIPBundle(path, silentStaticIPLogger())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(bundle.Entries) != 0 {
		t.Errorf("empty rows must be dropped, got %d entries", len(bundle.Entries))
	}
}

// TestLoadStaticEgressIPBundle_MalformedToml — TOML parse
// errors are fail-loud.
func TestLoadStaticEgressIPBundle_MalformedToml(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "static_egress_ips.toml")
	content := "this is not = = = valid toml [[[\n"
	if err := os.WriteFile(path, []byte(content), 0o400); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadStaticEgressIPBundle(path, silentStaticIPLogger())
	if err == nil {
		t.Fatal("expected error for malformed TOML")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Errorf("err = %v, want substring `parse`", err)
	}
}

// staticEgressIPRecording is a staticEgressIPTarget stub that
// records every SetStaticEgressIPAliases invocation.
type staticEgressIPRecording struct {
	mu      sync.Mutex
	calls   int
	entries []StaticEgressIPEntry
}

func (r *staticEgressIPRecording) SetStaticEgressIPAliases(entries []StaticEgressIPEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	r.entries = append(r.entries, entries...)
}

// TestWatchStaticEgressIPBundleReload_StartupLoad — the
// watcher fires the startup load before the SIGHUP loop.
func TestWatchStaticEgressIPBundleReload_StartupLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "static_egress_ips.toml")
	content := "[[entries]]\naccount_id = \"11111111-1111-1111-1111-111111111111\"\napp_id = \"shop\"\nip = \"203.0.113.42\"\n"
	if err := os.WriteFile(path, []byte(content), 0o400); err != nil {
		t.Fatalf("write: %v", err)
	}
	target := &staticEgressIPRecording{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	hupCh := make(chan os.Signal, 1)
	watchStaticEgressIPBundleReload(ctx, target, state.NewMemStore(), path, "test-node", silentStaticIPLogger(), hupCh)
	if target.calls != 1 {
		t.Errorf("startup calls = %d, want 1", target.calls)
	}
	if len(target.entries) != 1 || target.entries[0].AppID != "shop" {
		t.Errorf("startup entries = %+v, want one entry for `shop`", target.entries)
	}
}

// TestWatchStaticEgressIPBundleReload_EmptyPathSkipsWatch —
// empty path disables the watcher entirely.
func TestWatchStaticEgressIPBundleReload_EmptyPathSkipsWatch(t *testing.T) {
	target := &staticEgressIPRecording{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	hupCh := make(chan os.Signal, 1)
	watchStaticEgressIPBundleReload(ctx, target, state.NewMemStore(), "", "test-node", silentStaticIPLogger(), hupCh)
	if target.calls != 0 {
		t.Errorf("empty path: target.calls = %d, want 0", target.calls)
	}
	hupCh <- nil
}

// mustIP returns a parsed netip.Addr or fails the test.
func mustIP(t *testing.T, s string) netip.Addr {
	t.Helper()
	a, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return a
}

// keep imports alive across test rewrites.
var _ = mustIP
var _ = netip.Addr{}
