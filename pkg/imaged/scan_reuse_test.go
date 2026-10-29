package imaged

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// deployScanIdentity is the installed scanner the identity probe reports.
type deployScanIdentity struct {
	id  scannerIdentity
	err error
}

func (p *deployScanIdentity) probe(context.Context) (scannerIdentity, error) { return p.id, p.err }

// A redeploy of an unchanged artifact reuses the previous scan instead of
// running Grype again (#5709), and only when that scan came from the scanner
// and database installed now (ADR-20261028102540).
func TestDeployScanReusesAnUnchangedArtifactsResult(t *testing.T) {
	cases := []struct {
		name      string
		policy    api.AppSecurityPolicy
		secondArt string
		// dbStatus is what Grype reports for its database; empty means valid.
		dbStatus string
		noProbe  bool
		// before runs between the two deploys. It can change what the probe
		// reports, as a database refresh does.
		before    func(h *Handler, installed *deployScanIdentity)
		wantGrype int
		wantReuse bool
	}{
		{name: "unchanged artifact", secondArt: "layer", wantGrype: 1, wantReuse: true},
		{name: "changed artifact", secondArt: "layer v2", wantGrype: 2},
		{name: "enforce scans every deployment", policy: api.AppSecurityPolicyEnforce, secondArt: "layer", wantGrype: 2},
		{name: "database refreshed", secondArt: "layer", wantGrype: 2, before: func(h *Handler, installed *deployScanIdentity) {
			installed.id.DBBuiltAt = "2026-10-29T00:00:00Z"
			h.refreshScannerDB(context.Background())
		}},
		{name: "scanner identity unknown", secondArt: "layer", wantGrype: 2, before: func(h *Handler, installed *deployScanIdentity) {
			installed.err = context.DeadlineExceeded
			h.refreshScannerDB(context.Background())
		}},
		{name: "no identity probe", noProbe: true, secondArt: "layer", wantGrype: 2},
		{name: "database not reported valid", dbStatus: "stale", secondArt: "layer", wantGrype: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			appsRoot := t.TempDir()
			grypeRuns := 0
			dbStatus := tc.dbStatus
			if dbStatus == "" {
				dbStatus = "valid"
			}
			stubGrype := func(context.Context, string) (*ScanResult, error) {
				grypeRuns++
				return &ScanResult{
					ScannerVersion: "0.116.0", ScannerDBStatus: dbStatus,
					ScannerDBVersion: "v6.1.2", ScannerDBBuiltAt: "2026-10-28T00:00:00Z",
					SeverityCounts:  SeverityCounts{Medium: 1},
					Vulnerabilities: []Vulnerability{{ID: "CVE-2026-0001", Severity: SeverityMedium}},
				}, nil
			}
			backend, err := storage.NewLocalStorageBackend(appsRoot)
			if err != nil {
				t.Fatal(err)
			}
			h := New(state.NewMemStore(), &fakeNotifier{}, fakePuller{}, &fakeBuilder{},
				"./init", appsRoot, slog.New(slog.DiscardHandler)).
				WithStorage(backend).
				WithGrypeRun(stubGrype)
			installed := &deployScanIdentity{id: scannerIdentity{Version: "0.116.0", DBVersion: "v6.1.2", DBBuiltAt: "2026-10-28T00:00:00Z"}}
			if !tc.noProbe {
				h.WithScannerIdentity(installed.probe).WithScannerDBUpdater(func(context.Context) error { return nil })
			}
			ctx := context.Background()
			acct, err := h.store.CreateAccount(ctx, "reuse@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := h.store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "reuse", RAMMB: 512, IdleTimeoutS: 60, MaxConcurrency: 5, SecurityPolicy: tc.policy})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(appsRoot+"/reuse", 0o755); err != nil {
				t.Fatal(err)
			}
			deploy := func(artifact string) state.Deployment {
				t.Helper()
				dep, err := h.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:abc", Kind: state.DeploymentKindImage})
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(appsRoot+"/reuse/"+dep.ID+".ext4", []byte(artifact), 0o600); err != nil {
					t.Fatal(err)
				}
				_ = h.runDeployScan(ctx, app, dep)
				dep, err = h.store.DeploymentByID(ctx, dep.ID)
				if err != nil {
					t.Fatal(err)
				}
				return dep
			}
			first := deploy("layer")
			if tc.before != nil {
				tc.before(h, installed)
			}
			second := deploy(tc.secondArt)

			if grypeRuns != tc.wantGrype {
				t.Fatalf("grype ran %d times, want %d", grypeRuns, tc.wantGrype)
			}
			got := decodeScanEvidence(second.ScanResult)
			if got == nil || second.ScanStatus != "complete" {
				t.Fatalf("second deployment scan = %s %s, want a complete result", second.ScanStatus, second.ScanResult)
			}
			if reused := got.ReusedFrom == first.ID; reused != tc.wantReuse {
				t.Fatalf("reused_from = %q, want reuse %v of %s", got.ReusedFrom, tc.wantReuse, first.ID)
			}
			if tc.wantReuse {
				prior := decodeScanEvidence(first.ScanResult)
				if got.ScannedAt != prior.ScannedAt || got.ArtifactDigest != prior.ArtifactDigest ||
					got.SeverityCounts != prior.SeverityCounts || len(got.Vulnerabilities) != 1 {
					t.Fatalf("reused result %+v does not repeat %+v", got, prior)
				}
			}
		})
	}
}
