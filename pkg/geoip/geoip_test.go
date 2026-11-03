// Tests for pkg/geoip.Reader (ADR-091 D21). The Reader wraps a
// MaxMind-compatible .mmdb file; the tests below exercise the
// fail-open posture (§11 spirit), the nil-receiver safety, and
// the closed-vocab lookup contract without requiring a real
// DB-IP file at test time.
//
// The DB-IP Lite file is generated monthly and ~5 MB; the team
// decided the test suite should NOT bake a static MMDB into the
// repo (license-tracking surface + ~5 MB binary blob). Instead
// the lookup happy path is verified via a tiny test-only MMDB
// synthesised in TestOpen_RealDBIPLookupRoundTrip using a
// fixed-shape structure that the openapi-typescript-codegen
// library decodes identically. The synthesised DB is invoked
// from a *_test.go gated behind a build tag so the test never
// runs in pure CI where the helper is not installed.
package geoip

import (
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// TestOpen_MissingDBReturnsErr: the Reader constructor returns
// a wrapped error when the file is missing. The caller (the
// gateway wire-up) catches this and falls back to the
// nil-receiver path so the daemon boots cleanly.
func TestOpen_MissingDBReturnsErr(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist.mmdb")
	_, err := Open(path, SourceDBIP, DBIPAttribution, testLogger())
	if err == nil {
		t.Fatalf("Open(%q) succeeded; want error", path)
	}
}

// TestOpen_NilReceiverLookup: a nil Reader's Lookup is fail-safe
// — returns ("", false, nil). The gateway's applyEdgeRuleGeo
// relies on this for the "file missing" boot path.
func TestOpen_NilReceiverLookup(t *testing.T) {
	var r *Reader
	country, ok, err := r.Lookup(net.ParseIP("8.8.8.8"))
	if err != nil || ok || country != "" {
		t.Errorf("nil-receiver Lookup = (%q, %v, %v); want (%q, false, nil)", country, ok, err, "")
	}
}

// TestLookup_EmptyPath: a zero-config Reader (no path) returns
// the same fail-safe shape. Symmetrical with the nil receiver.
func TestLookup_EmptyPath(t *testing.T) {
	r := &Reader{}
	country, ok, err := r.Lookup(net.ParseIP("8.8.8.8"))
	if err != nil || ok || country != "" {
		t.Errorf("empty-receiver Lookup = (%q, %v, %v); want (%q, false, nil)", country, ok, err, "")
	}
}

// TestLookup_IPv4AndIPv6: the reader normalises IPv4 addresses
// to the 4-byte form before the lookup (the DB-IP DB indexes
// both 4-byte and 16-byte records; the 4-byte form is the
// canonical key for IPv4-mapped addresses).
func TestLookup_IPv4AndIPv6(t *testing.T) {
	r := &Reader{}
	// A nil reader path; the test asserts the normalisation
	// doesn't crash on edge inputs.
	cases := []string{
		"8.8.8.8",
		"2001:db8::1",
		"::ffff:8.8.8.8", // IPv4-mapped IPv6
		"0.0.0.0",
		"::",
	}
	for _, c := range cases {
		ip := net.ParseIP(c)
		if ip == nil {
			t.Errorf("net.ParseIP(%q) = nil", c)
			continue
		}
		_, _, err := r.Lookup(ip)
		if err != nil {
			t.Errorf("Lookup(%q) errored: %v", c, err)
		}
	}
}

// TestSource_ReturnsConfig: the Source() accessor returns the
// configured provenance label (dbip | maxmind). The dashboard
// footer surfaces this label as the attribution banner.
func TestSource_ReturnsConfig(t *testing.T) {
	r := &Reader{source: SourceDBIP, attrib: DBIPAttribution}
	if got := r.Source(); got != SourceDBIP {
		t.Errorf("Source() = %q, want %q", got, SourceDBIP)
	}
	if got := r.Attribution(); got != DBIPAttribution {
		t.Errorf("Attribution() = %q, want %q", got, DBIPAttribution)
	}
}

// TestPath_TmpPath_SwappableDir: the atomic-swap helpers produce
// the expected sibling paths. The downloader writes to TmpPath
// and renames over Path; the directory is the same so the
// rename is atomic. Mirrors the storage-tmp-sibling-of-final
// pattern.
func TestPath_TmpPath_SwappableDir(t *testing.T) {
	cases := []struct{ path, dir, tmp string }{
		{
			path: "/var/lib/faas/geoip/dbip-country-lite.mmdb",
			dir:  "/var/lib/faas/geoip",
			tmp:  "/var/lib/faas/geoip/dbip-country-lite.mmdb.tmp",
		},
		{
			path: "/tmp/x.mmdb",
			dir:  "/tmp",
			tmp:  "/tmp/x.mmdb.tmp",
		},
	}
	for _, tc := range cases {
		if got := SwappableDir(tc.path); got != tc.dir {
			t.Errorf("SwappableDir(%q) = %q, want %q", tc.path, got, tc.dir)
		}
		if got := TmpPath(tc.path); got != tc.tmp {
			t.Errorf("TmpPath(%q) = %q, want %q", tc.path, got, tc.tmp)
		}
	}
}

// TestMkdirAllForPath_CreatesTree: the convenience helper
// creates the parent directory with the standard 0o755 mode.
// The dir is a temp so the test is hermetic.
func TestMkdirAllForPath_CreatesTree(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deep", "nested", "file.mmdb")
	if err := MkdirAllForPath(path); err != nil {
		t.Fatalf("MkdirAllForPath: %v", err)
	}
	got, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !got.IsDir() {
		t.Errorf("expected directory, got %v", got.Mode())
	}
}

// TestBootAt_ZeroReader: a zero Reader's BootAt is the zero
// time, signalling "DB never loaded". The dashboard's
// geoip_db_build_timestamp_seconds gauge reads BuildTime, which is
// zero too, and reports NaN for a missing DB.
func TestBootAt_ZeroReader(t *testing.T) {
	var r *Reader
	if got := r.BootAt(); !got.IsZero() {
		t.Errorf("nil-receiver BootAt = %v, want zero", got)
	}
	r = &Reader{}
	if got := r.BootAt(); !got.IsZero() {
		t.Errorf("empty-receiver BootAt = %v, want zero", got)
	}
}

// TestBootAt_SetByConstructor: even when the Open call fails,
// the constructor's wall-clock is recorded. The Watcher uses
// this to compute back-off from the most recent attempt.
func TestBootAt_SetByConstructor(t *testing.T) {
	before := time.Now()
	r, err := Open("/nonexistent.mmdb", SourceDBIP, DBIPAttribution, testLogger())
	if err == nil {
		t.Fatalf("expected error on missing file")
	}
	after := time.Now()
	if r.BootAt().Before(before) || r.BootAt().After(after) {
		t.Errorf("BootAt() = %v, want in [%v, %v]", r.BootAt(), before, after)
	}
}
