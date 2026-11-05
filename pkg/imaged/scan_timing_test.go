package imaged

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// TestDeployScanLogsPhaseTimings is the Hunt 14 H14-12 instrumentation: the
// per-deploy scan took p50 48 s on production-us and logged one line with no
// timings. The stamped line now carries each phase, including the ones the
// grype runner marks.
func TestDeployScanLogsPhaseTimings(t *testing.T) {
	appsRoot := t.TempDir()
	const slug = "timed"
	stubGrype := func(ctx context.Context, _ string) (*ScanResult, error) {
		markScanPhase(ctx, "extract")
		markScanPhase(ctx, "grype")
		return &ScanResult{Vulnerabilities: []Vulnerability{}}, nil
	}
	backend, err := storage.NewLocalStorageBackend(appsRoot)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	h := New(state.NewMemStore(), &fakeNotifier{}, fakePuller{}, &fakeBuilder{},
		"./init", appsRoot, slog.New(slog.NewJSONHandler(&logs, nil))).
		WithStorage(backend).
		WithGrypeRun(stubGrype)
	ctx := context.Background()
	acct, err := h.store.CreateAccount(ctx, "timing@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := h.store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: slug, RAMMB: 512, IdleTimeoutS: 60, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := h.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:abc", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(appsRoot+"/"+slug, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(appsRoot+"/"+slug+"/"+dep.ID+".ext4", []byte("layer"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = h.runDeployScan(ctx, app, dep)

	var stamped map[string]any
	for _, line := range bytes.Split(logs.Bytes(), []byte("\n")) {
		var entry map[string]any
		if json.Unmarshal(line, &entry) == nil && entry["msg"] == "imaged: per-deploy scan stamped" {
			stamped = entry
		}
	}
	if stamped == nil {
		t.Fatalf("no scan stamped line in:\n%s", logs.String())
	}
	for _, key := range []string{"stage_ms", "digest_ms", "extract_ms", "grype_ms", "record_ms", "total_ms"} {
		if _, ok := stamped[key]; !ok {
			t.Errorf("scan stamped line lacks %s: %v", key, stamped)
		}
	}
}

func TestMarkScanPhaseWithoutRecorderIsANoOp(t *testing.T) {
	markScanPhase(context.Background(), "stage")
	markPreparationPhase(context.Background(), "layer")
	ctx, timing := withScanTiming(context.Background())
	markPreparationPhase(ctx, "layer") // another timeline's phase is not recorded here
	markScanPhase(ctx, "stage")
	markScanPhase(ctx, "grype")
	attrs := timing.attrs()
	if len(attrs) != 6 || attrs[0] != "stage_ms" || attrs[2] != "grype_ms" || attrs[4] != "total_ms" {
		t.Fatalf("attrs = %v, want stage_ms, grype_ms and total_ms in order", attrs)
	}
}
