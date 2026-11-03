package builderd

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func seedCappedBuild(t *testing.T, store *state.MemStore) (state.Deployment, state.Build) {
	t.Helper()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "capped-logs@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "capped-build-logs", RAMMB: 256, IdleTimeoutS: 60, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, LogPath: filepath.Join(t.TempDir(), "build.log")})
	if err != nil {
		t.Fatal(err)
	}
	build, err := store.CreateBuild(ctx, dep.ID, state.DeploymentKindTarball, 1, dep.LogPath)
	if err != nil {
		t.Fatal(err)
	}
	return dep, build
}

func newCappedBuilderd(t *testing.T, store state.Store, notif Notifier, maxBytes int64) *Builderd {
	t.Helper()
	return New(store, notif, nil, NewCache(t.TempDir()), NewDetector(), nil,
		Config{SourceSpoolDir: t.TempDir(), BuildLogMaxBytes: maxBytes}, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func storedBuildLog(t *testing.T, store *state.MemStore, deploymentID string) []state.LogEntry {
	t.Helper()
	rows, _, err := store.ListDeploymentLogs(context.Background(), deploymentID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	return rows
}

// adr: 20261102182856
// The database copy of a build log stops at Config.BuildLogMaxBytes: the line
// that would cross it becomes one marker row and later lines are neither
// stored nor fanned out.
func TestEmitBuildLogStopsAtTheCapWithOneMarker(t *testing.T) {
	store := state.NewMemStore()
	dep, build := seedCappedBuild(t, store)
	notif := &fakeNotifier{}
	b := newCappedBuilderd(t, store, notif, 64)
	ctx := context.Background()
	line := strings.Repeat("a", 29) + "\n" // 30 bytes
	b.emitBuildLog(ctx, build.ID, line)
	b.emitBuildLog(ctx, build.ID, line)
	b.emitBuildLog(ctx, build.ID, "crosses the cap\n")
	b.emitBuildLog(ctx, build.ID, "x\n")

	rows := storedBuildLog(t, store, dep.ID)
	if len(rows) != 3 || rows[0].Line != line || rows[1].Line != line || rows[0].Stream != "build" {
		t.Fatalf("stored rows = %#v", rows)
	}
	marker := rows[2]
	if marker.Stream != state.DeploymentLogStreamSystem || marker.Line != buildLogTruncatedMarker(64) || !strings.Contains(marker.Line, "64 bytes") {
		t.Fatalf("marker row = %#v", marker)
	}
	if len(notif.calls) != 3 || !strings.Contains(notif.calls[2].payload, "build log truncated") {
		t.Fatalf("notifications = %#v", notif.calls)
	}
	usage, err := store.DeploymentLogUsage(ctx, dep.ID)
	if err != nil || !usage.Truncated || usage.Bytes != 60+int64(len(marker.Line)) {
		t.Fatalf("usage = %+v, %v", usage, err)
	}
}

// A restarted builderd (or another builder node) continues the stored count
// and never writes a second marker.
func TestEmitBuildLogCapSurvivesRestart(t *testing.T) {
	store := state.NewMemStore()
	dep, build := seedCappedBuild(t, store)
	ctx := context.Background()
	line := strings.Repeat("b", 39) + "\n" // 40 bytes
	newCappedBuilderd(t, store, &fakeNotifier{}, 100).emitBuildLog(ctx, build.ID, line)
	restarted := newCappedBuilderd(t, store, &fakeNotifier{}, 100)
	restarted.emitBuildLog(ctx, build.ID, line)
	restarted.emitBuildLog(ctx, build.ID, line) // 120 > 100: marker
	newCappedBuilderd(t, store, &fakeNotifier{}, 100).emitBuildLog(ctx, build.ID, "y\n")

	rows := storedBuildLog(t, store, dep.ID)
	markers := 0
	for _, row := range rows {
		if row.Stream == state.DeploymentLogStreamSystem {
			markers++
		}
	}
	if len(rows) != 3 || markers != 1 || rows[2].Stream != state.DeploymentLogStreamSystem {
		t.Fatalf("rows after restarts = %#v", rows)
	}
}

func TestBuildLogBudgetRereadsAfterAnIdleGap(t *testing.T) {
	store := state.NewMemStore()
	dep, _ := seedCappedBuild(t, store)
	ctx := context.Background()
	var budget buildLogBudget
	const maxBytes = 31
	line := "0123456789" // 10 bytes
	// admit stores what it keeps, as persistBuildLog does.
	admit := func(line string, at time.Time) buildLogVerdict {
		t.Helper()
		v, err := budget.admit(ctx, store, dep.ID, line, maxBytes, at)
		if err != nil {
			t.Fatal(err)
		}
		if v == buildLogKeep {
			if _, err := store.AppendDeploymentLog(ctx, dep.ID, "build", line); err != nil {
				t.Fatal(err)
			}
		}
		return v
	}
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	if v := admit(line, now); v != buildLogKeep {
		t.Fatalf("first line: %v", v)
	}
	// Another builder stores 12 bytes meanwhile (22 stored).
	if _, err := store.AppendDeploymentLog(ctx, dep.ID, "build", "elsewhere 12"); err != nil {
		t.Fatal(err)
	}
	// Inside the reload window the in-memory count is trusted: 20 <= 31.
	if v := admit(line, now.Add(time.Second)); v != buildLogKeep {
		t.Fatalf("second line inside the window: %v", v)
	}
	// After the gap the stored 32 bytes are read back, so 10 more cross the
	// cap; the in-memory 20 would have let the line through.
	if v := admit(line, now.Add(time.Second+buildLogUsageReloadAfter)); v != buildLogTruncate {
		t.Fatalf("line after the idle gap: %v, want truncate", v)
	}
	if v := admit("z", now.Add(2*time.Second+buildLogUsageReloadAfter)); v != buildLogDrop {
		t.Fatalf("line after truncation: %v, want drop", v)
	}
}

type failingBuildLogUsage struct{ calls int }

func (f *failingBuildLogUsage) DeploymentLogUsage(context.Context, string) (state.DeploymentLogUsage, error) {
	f.calls++
	return state.DeploymentLogUsage{}, errors.New("database unavailable")
}

// An unknown count keeps the line and is not cached.
func TestBuildLogBudgetKeepsLinesWhenUsageIsUnknown(t *testing.T) {
	var budget buildLogBudget
	reader := &failingBuildLogUsage{}
	now := time.Now()
	for i := 0; i < 2; i++ {
		if v, err := budget.admit(context.Background(), reader, "dep", "line\n", 1, now); err == nil || v != buildLogKeep {
			t.Fatalf("admit with failing usage = %v, %v", v, err)
		}
	}
	if reader.calls != 2 {
		t.Fatalf("usage read %d times, want 2", reader.calls)
	}
}

func TestBuildLogTruncatedMarkerNamesTheCap(t *testing.T) {
	if got := buildLogTruncatedMarker(DefaultBuildLogMaxBytes); !strings.Contains(got, "8 MiB") || !strings.HasSuffix(got, "\n") {
		t.Fatalf("default marker = %q", got)
	}
	if got := buildLogTruncatedMarker(0); !strings.Contains(got, "8 MiB") {
		t.Fatalf("unset cap marker = %q", got)
	}
}
