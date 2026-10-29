package imaged

// adr: 20261028102540

import (
	"context"
	"encoding/json"
	"errors"
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
		want    scannerIdentity
		wantErr bool
	}{
		{name: "valid", version: version, status: `{"schemaVersion":"v6.1.10","built":"2026-10-28T06:23:30Z","valid":true}`,
			want: scannerIdentity{Version: "0.116.0", DBVersion: "v6.1.10", DBBuiltAt: "2026-10-28T06:23:30Z"}},
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
	if (scannerIdentity{}).matches("0.116.0", "v6.1.10", "2026-10-28T06:23:30Z") {
		t.Fatal("an empty identity matched")
	}
}

func TestCurrentScannerIdentityCachesAndFailsClosed(t *testing.T) {
	h := &Handler{}
	if _, err := h.currentScannerIdentity(t.Context()); !errors.Is(err, errScannerIdentityUnavailable) {
		t.Fatalf("no probe should mean no identity: %v", err)
	}
	calls := 0
	want := scannerIdentity{Version: "0.116.0", DBVersion: "v6.1.10", DBBuiltAt: "2026-10-28T06:23:30Z"}
	h.WithScannerIdentity(func(context.Context) (scannerIdentity, error) {
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
	if _, ok := h.scannerIdentityCache.get(time.Now().Add(api.ScannerIdentityCacheTTL)); ok {
		t.Fatal("cache served an identity past its TTL")
	}
	// A real scan that reports a newer database replaces the cached identity.
	h.scannerIdentityCache.observe("0.116.0", "v6.1.10", "2026-10-29T06:00:00Z", time.Now())
	if got, _ := h.currentScannerIdentity(t.Context()); got.DBBuiltAt != "2026-10-29T06:00:00Z" || calls != 1 {
		t.Fatalf("scan-observed identity not used: %+v after %d probes", got, calls)
	}
	// Unparsable scan metadata clears the cache rather than keeping a stale one.
	h.scannerIdentityCache.observe("", "", "", time.Now())
	if _, ok := h.scannerIdentityCache.get(time.Now()); ok {
		t.Fatal("unparsable scan metadata kept the cached identity")
	}
	failing := (&Handler{}).WithScannerIdentity(func(context.Context) (scannerIdentity, error) {
		return scannerIdentity{}, errors.New("grype missing")
	})
	if _, err := failing.currentScannerIdentity(t.Context()); err == nil {
		t.Fatal("probe error became an identity")
	}
}

func TestScanEvidenceCurrent(t *testing.T) {
	built := "2026-10-28T06:23:30Z"
	h := (&Handler{}).WithScannerIdentity(func(context.Context) (scannerIdentity, error) {
		return scannerIdentity{Version: "0.116.0", DBVersion: "v6.1.10", DBBuiltAt: built}, nil
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
		h.scannerIdentityCache.set(scannerIdentity{Version: "0.116.0", DBVersion: "v6.1.10", DBBuiltAt: "2026-10-28T06:23:30Z"}, time.Now())
		h.refreshScannerDB(t.Context())
		if updates != 1 {
			t.Fatalf("updater ran %d times, want 1", updates)
		}
		if _, ok := h.scannerIdentityCache.get(time.Now()); ok {
			t.Fatalf("identity survived a database update (err %v)", updateErr)
		}
	}
}
