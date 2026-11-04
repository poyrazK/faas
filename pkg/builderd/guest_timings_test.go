package builderd

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func coldNodeBuildTimings() *api.BuildTimings {
	return &api.BuildTimings{
		GuestStartMS: 1234,
		Stages:       []api.BuildStageTiming{{Stage: "fuse", AtMS: 800}, {Stage: "buildkit-ready", AtMS: 9100}, {Stage: "build-command", AtMS: 9300}},
		BuildMS:      250_400, ImageExportMS: 12_500, CacheExportMS: 115_000,
		SlowestSteps: []api.BuildStepTiming{
			{Name: "exporting cache to client directory", MS: 115_000},
			{Name: "[builder 1/1] FROM ghcr.io/railwayapp/railpack-builder:mise-2026.8.13", MS: 60_200},
			{Name: "npm ci\x1b[31m\r", MS: 25_500},
		},
	}
}

func TestGuestTimingsLogSaysWhereTheVMTimeWent(t *testing.T) {
	want := "[build] builder VM: build started 1.2s after boot, BuildKit ready at 9.1s, build command 4m10.4s (image export 12.5s, dependency cache export 1m55s)\n" +
		"[build] slowest build steps:\n" +
		"     1m55s  exporting cache to client directory\n" +
		"    1m0.2s  [builder 1/1] FROM ghcr.io/railwayapp/railpack-builder:mise-2026.8.13\n" +
		"     25.5s  npm ci[31m\n"
	if got := guestTimingsLog(coldNodeBuildTimings()); got != want {
		t.Fatalf("guestTimingsLog =\n%s\nwant\n%s", got, want)
	}
	if got := guestTimingsLog(nil); got != "" {
		t.Fatalf("an older guest produced %q", got)
	}
	// The guest is untrusted: its step list and names are bounded again.
	flood := &api.BuildTimings{}
	for range api.BuildTimingMaxSteps + 5 {
		flood.SlowestSteps = append(flood.SlowestSteps, api.BuildStepTiming{Name: strings.Repeat("x", 4*api.BuildTimingStepNameMaxBytes), MS: 1000})
	}
	got := guestTimingsLog(flood)
	if lines := strings.Count(got, "\n"); lines != api.BuildTimingMaxSteps+1 {
		t.Fatalf("flooded steps printed %d lines", lines)
	}
	for _, line := range strings.Split(strings.TrimSpace(got), "\n")[1:] {
		if len(line) > api.BuildTimingStepNameMaxBytes+16 {
			t.Fatalf("step name not cut: %d bytes", len(line))
		}
	}
}

func TestGuestTimingAttrsCarryNoStepNames(t *testing.T) {
	attrs := guestTimingAttrs(coldNodeBuildTimings())
	got := map[string]any{}
	for i := 0; i+1 < len(attrs); i += 2 {
		got[attrs[i].(string)] = attrs[i+1]
	}
	want := map[string]int64{"guest_start_ms": 1234, "buildkit_ready_ms": 9100, "build_command_ms": 250_400, "image_export_ms": 12_500, "cache_export_ms": 115_000}
	if len(got) != len(want) {
		t.Fatalf("attrs = %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %d", k, got[k], v)
		}
	}
	if guestTimingAttrs(nil) != nil {
		t.Fatal("an older guest produced attributes")
	}
}

func TestProcessOneWritesGuestTimingsToTheBuildLog(t *testing.T) {
	store := state.NewMemStore()
	src := filepath.Join(t.TempDir(), "src.tar.gz")
	makeTarballWithName(t, src, []string{"package.json", "index.js"})
	buildID, depID, _ := seedDeployment(t, store, src)
	image := filepath.Join(t.TempDir(), "image.tar")
	if err := os.WriteFile(image, []byte("produced layer"), 0o644); err != nil {
		t.Fatal(err)
	}
	vm := &fakeVM{out: BuildOutcome{OCIImage: image, Timings: coldNodeBuildTimings()}}
	b := New(store, &fakeNotifier{}, vm, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := b.ProcessOne(context.Background(), buildID); err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}
	rows, _, err := store.ListDeploymentLogs(context.Background(), depID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var log strings.Builder
	for _, row := range rows {
		log.WriteString(row.Line)
	}
	for _, want := range []string{"[build] builder VM: build started 1.2s after boot", "[build] slowest build steps:", "railpack-builder:mise-2026.8.13"} {
		if !strings.Contains(log.String(), want) {
			t.Fatalf("build log missing %q:\n%s", want, log.String())
		}
	}
}
