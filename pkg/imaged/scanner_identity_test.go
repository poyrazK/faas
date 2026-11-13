package imaged

// adr: 20261028102540

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestParseGrypeIdentity(t *testing.T) {
	version := []byte(`{"application":"grype","version":"0.116.0"}`)
	for _, tc := range []struct {
		name    string
		version []byte
		status  string
		want    installedScanner
		wantErr bool
	}{
		{name: "valid", version: version, status: `{"schemaVersion":"v6.1.10","built":"2026-10-28T06:23:30Z","valid":true}`,
			want: installedScanner{Version: "0.116.0", DBVersion: "v6.1.10", DBBuiltAt: "2026-10-28T06:23:30Z"}},
		{name: "invalid database", version: version, status: `{"schemaVersion":"v6.1.10","built":"2026-10-28T06:23:30Z","valid":false}`, wantErr: true},
		{name: "validity missing", version: version, status: `{"schemaVersion":"v6.1.10","built":"2026-10-28T06:23:30Z"}`, wantErr: true},
		{name: "no build time", version: version, status: `{"schemaVersion":"v6.1.10","valid":true}`, wantErr: true},
		{name: "no version", version: []byte(`{}`), status: `{"schemaVersion":"v6.1.10","built":"2026-10-28T06:23:30Z","valid":true}`, wantErr: true},
		{name: "not JSON", version: []byte("grype 0.116.0"), status: `{}`, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseGrypeIdentity(tc.version, []byte(tc.status))
			if (err != nil) != tc.wantErr {
				t.Fatalf("parseGrypeIdentity error = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("parseGrypeIdentity = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestScannerIdentityMatches(t *testing.T) {
	id, ok := newScannerIdentity("0.116.0", "v6.1.10", "2026-10-28T06:23:30Z")
	if !ok {
		t.Fatal("valid identity rejected")
	}
	for _, tc := range []struct {
		name                        string
		version, dbVersion, dbBuilt string
		want                        bool
	}{
		{"same", "0.116.0", "v6.1.10", "2026-10-28T06:23:30Z", true},
		{"same instant, other zone", "0.116.0", "v6.1.10", "2026-10-28T08:23:30+02:00", true},
		{"newer database", "0.116.0", "v6.1.10", "2026-10-29T06:23:30Z", false},
		{"other schema", "0.116.0", "v6.1.11", "2026-10-28T06:23:30Z", false},
		{"other scanner", "0.117.0", "v6.1.10", "2026-10-28T06:23:30Z", false},
		{"missing build time", "0.116.0", "v6.1.10", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := id.matches(tc.version, tc.dbVersion, tc.dbBuilt); got != tc.want {
				t.Fatalf("matches = %v, want %v", got, tc.want)
			}
		})
	}
	if (installedScanner{}).matches("0.116.0", "v6.1.10", "2026-10-28T06:23:30Z") {
		t.Fatal("an empty identity matched")
	}
}

func TestCurrentScannerIdentityCachesAndFailsClosed(t *testing.T) {
	h := &Handler{}
	if _, err := h.currentScannerIdentity(t.Context()); !errors.Is(err, errScannerIdentityUnavailable) {
		t.Fatalf("no probe should mean no identity: %v", err)
	}
	calls := 0
	want := installedScanner{Version: "0.116.0", DBVersion: "v6.1.10", DBBuiltAt: "2026-10-28T06:23:30Z"}
	h.WithScannerIdentity(func(context.Context) (installedScanner, error) {
		calls++
		return want, nil
	})
	for range 3 {
		got, err := h.currentScannerIdentity(t.Context())
		if err != nil || got != want {
			t.Fatalf("currentScannerIdentity = %+v, %v", got, err)
		}
	}
	if calls != 1 {
		t.Fatalf("probe ran %d times within the cache TTL, want 1", calls)
	}
	if _, ok := h.scannerIdentityCache.get(time.Now().Add(api.ScannerIdentityCacheTTL), ""); ok {
		t.Fatal("cache served an identity past its TTL")
	}
	// A real scan that reports a newer database replaces the cached identity.
	h.scannerIdentityCache.observe("0.116.0", "v6.1.10", "2026-10-29T06:00:00Z", time.Now())
	if got, _ := h.currentScannerIdentity(t.Context()); got.DBBuiltAt != "2026-10-29T06:00:00Z" || calls != 1 {
		t.Fatalf("scan-observed identity not used: %+v after %d probes", got, calls)
	}
	// Unparsable scan metadata clears the cache rather than keeping a stale one.
	h.scannerIdentityCache.observe("", "", "", time.Now())
	if _, ok := h.scannerIdentityCache.get(time.Now(), ""); ok {
		t.Fatal("unparsable scan metadata kept the cached identity")
	}
	failing := (&Handler{}).WithScannerIdentity(func(context.Context) (installedScanner, error) {
		return installedScanner{}, errors.New("grype missing")
	})
	if _, err := failing.currentScannerIdentity(t.Context()); err == nil {
		t.Fatal("probe error became an identity")
	}
}

func TestScanEvidenceCurrent(t *testing.T) {
	built := "2026-10-28T06:23:30Z"
	h := (&Handler{}).WithScannerIdentity(func(context.Context) (installedScanner, error) {
		return installedScanner{Version: "0.116.0", DBVersion: "v6.1.10", DBBuiltAt: built}, nil
	})
	evidence := func(t *testing.T, mutate func(*ScanResult)) []byte {
		t.Helper()
		result := ScanResult{ScannerVersion: "0.116.0", ScannerDBStatus: "valid", ScannerDBVersion: "v6.1.10", ScannerDBBuiltAt: built, SeverityCounts: SeverityCounts{Low: 2}}
		if mutate != nil {
			mutate(&result)
		}
		raw, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	for _, tc := range []struct {
		name   string
		status string
		raw    []byte
		want   bool
	}{
		{"same scanner and database", "complete", evidence(t, nil), true},
		{"newer database installed since", "complete", evidence(t, func(r *ScanResult) { r.ScannerDBBuiltAt = "2026-10-27T06:00:00Z" }), false},
		{"high findings rescan", "complete", evidence(t, func(r *ScanResult) { r.SeverityCounts.High = 1 }), false},
		{"failed scan rescans", "failed", evidence(t, nil), false},
		{"scan error rescans", "complete", evidence(t, func(r *ScanResult) { r.Error = "scanner_unavailable" }), false},
		{"no evidence", "complete", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dep := state.Deployment{ID: "dep-1", ScanStatus: tc.status, ScanResult: tc.raw}
			if got := h.scanEvidenceCurrent(t.Context(), dep); got != tc.want {
				t.Fatalf("scanEvidenceCurrent = %v, want %v", got, tc.want)
			}
		})
	}
	if (&Handler{}).scanEvidenceCurrent(t.Context(), state.Deployment{ScanStatus: "complete", ScanResult: evidence(t, nil)}) {
		t.Fatal("evidence counted as current without a scanner identity")
	}
}

func TestRefreshScannerDBUpdatesAndForgetsTheIdentity(t *testing.T) {
	(&Handler{}).refreshScannerDB(t.Context()) // no updater: a no-op
	for _, updateErr := range []error{nil, errors.New("registry unreachable")} {
		h := &Handler{}
		updates := 0
		h.WithScannerDBUpdater(func(ctx context.Context) error {
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("database update has no deadline")
			}
			updates++
			return updateErr
		})
		h.scannerIdentityCache.set(installedScanner{Version: "0.116.0", DBVersion: "v6.1.10", DBBuiltAt: "2026-10-28T06:23:30Z"}, time.Now())
		h.refreshScannerDB(t.Context())
		if updates != 1 {
			t.Fatalf("updater ran %d times, want 1", updates)
		}
		if _, ok := h.scannerIdentityCache.get(time.Now(), ""); ok {
			t.Fatalf("identity survived a database update (err %v)", updateErr)
		}
	}
}

func TestScannerIdentityKeptWhileTheInstallStampHolds(t *testing.T) {
	id := installedScanner{Version: "0.116.0", DBVersion: "v6.1.10", DBBuiltAt: "2026-10-28T06:23:30Z"}
	probed := time.Now()
	past := probed.Add(api.ScannerIdentityCacheTTL + time.Minute)
	cases := []struct {
		name  string
		at    time.Time
		stamp string
		want  bool
	}{
		{name: "within the TTL, any stamp", at: probed.Add(time.Second), stamp: "", want: true},
		{name: "past the TTL, same stamp", at: past, stamp: "s1", want: true},
		{name: "past the TTL, changed stamp", at: past, stamp: "s2", want: false},
		{name: "past the TTL, unknown stamp", at: past, stamp: "", want: false},
		{name: "past the unchanged max age", at: probed.Add(api.ScannerIdentityUnchangedMaxAge), stamp: "s1", want: false},
		{name: "clock moved back", at: probed.Add(-time.Second), stamp: "s1", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var c scannerIdentityCache
			c.setProbed(id, probed, "s1")
			if _, ok := c.get(tc.at, tc.stamp); ok != tc.want {
				t.Fatalf("get = %v, want %v", ok, tc.want)
			}
		})
	}

	// A real scan reporting the same identity keeps the stamp; a different
	// one may have updated the database, so it drops it.
	var c scannerIdentityCache
	c.setProbed(id, probed, "s1")
	c.observe(id.Version, id.DBVersion, id.DBBuiltAt, probed)
	if _, ok := c.get(past, "s1"); !ok {
		t.Fatal("a scan reporting the cached identity dropped the install stamp")
	}
	c.observe(id.Version, id.DBVersion, "2026-10-29T06:00:00Z", probed)
	if _, ok := c.get(past, "s1"); ok {
		t.Fatal("a scan reporting a newer database kept the old install stamp")
	}
}

func TestCurrentScannerIdentityProbesOnlyWhenTheInstallChanges(t *testing.T) {
	want := installedScanner{Version: "0.116.0", DBVersion: "v6.1.10", DBBuiltAt: "2026-10-28T06:23:30Z"}
	calls := 0
	stamp := "s1"
	h := (&Handler{}).WithScannerIdentity(func(context.Context) (installedScanner, error) {
		calls++
		return want, nil
	}).WithScannerInstallStamp(func() string { return stamp })
	// An identity probed under s1 two minutes ago is past the TTL.
	h.scannerIdentityCache.setProbed(want, time.Now().Add(-api.ScannerIdentityCacheTTL-time.Minute), "s1")
	if got, err := h.currentScannerIdentity(t.Context()); err != nil || got != want || calls != 0 {
		t.Fatalf("unchanged install: got %+v, %v after %d probes, want the cached identity and no probe", got, err, calls)
	}
	stamp = "s2"
	h.scannerIdentityCache.setProbed(want, time.Now().Add(-api.ScannerIdentityCacheTTL-time.Minute), "s1")
	if _, err := h.currentScannerIdentity(t.Context()); err != nil || calls != 1 {
		t.Fatalf("changed install: %v after %d probes, want one probe", err, calls)
	}
}

func TestGrypeInstallStampFollowsTheBinaryAndDatabase(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "grype")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(dir, "db")
	if err := os.MkdirAll(filepath.Join(db, "6"), 0o755); err != nil {
		t.Fatal(err)
	}
	dbFile := filepath.Join(db, "6", "vulnerability.db")
	if err := os.WriteFile(dbFile, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	stamp := grypeInstallStamp(bin, db)
	first := stamp()
	if first == "" || stamp() != first {
		t.Fatalf("stamp of an unchanged install is %q then %q, want one stable non-empty value", first, stamp())
	}
	seen := map[string]string{"initial": first}
	change := func(name string, do func() error) {
		t.Helper()
		if err := do(); err != nil {
			t.Fatal(err)
		}
		got := stamp()
		if got == "" {
			t.Fatalf("stamp after %s is empty", name)
		}
		for prior, v := range seen {
			if v == got {
				t.Fatalf("stamp after %s equals the one after %s", name, prior)
			}
		}
		seen[name] = got
	}
	later := time.Now().Add(time.Hour)
	change("database rewritten", func() error {
		if err := os.WriteFile(dbFile, []byte("v2-longer"), 0o644); err != nil {
			return err
		}
		return os.Chtimes(dbFile, later, later)
	})
	change("database file added", func() error { return os.WriteFile(filepath.Join(db, "6", "import.json"), []byte("{}"), 0o644) })
	change("binary replaced", func() error {
		next := bin + ".new"
		if err := os.WriteFile(next, []byte("#!/bin/sh\n# v2\n"), 0o755); err != nil {
			return err
		}
		return os.Rename(next, bin)
	})

	if got := grypeInstallStamp(bin, filepath.Join(dir, "missing"))(); got != "" {
		t.Fatalf("stamp of a missing database directory = %q, want empty", got)
	}
	if got := grypeInstallStamp(filepath.Join(dir, "no-such-grype"), db)(); got != "" {
		t.Fatalf("stamp of a missing binary = %q, want empty", got)
	}
}
