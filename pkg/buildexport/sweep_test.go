package buildexport

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeExport(t *testing.T, root, id string, bytes int, mtime time.Time) string {
	t.Helper()
	artifact := filepath.Join(root, id, "build", "out", "image.tar")
	if err := os.MkdirAll(filepath.Dir(artifact), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact, make([]byte, bytes), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(artifact, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(root, id), mtime, mtime); err != nil {
		t.Fatal(err)
	}
	return artifact
}

func TestAcquireArtifactRecognizesOnlyCanonicalExport(t *testing.T) {
	root := t.TempDir()
	artifact := writeExport(t, root, "build-1", 1, time.Now())
	lease, ok, err := AcquireArtifact(artifact)
	if err != nil || !ok || lease == nil {
		t.Fatalf("AcquireArtifact = (%v, %v, %v), want lease", lease, ok, err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if lease, ok, err := AcquireArtifact(filepath.Join(root, "customer.tar")); err != nil || ok || lease != nil {
		t.Fatalf("non-export = (%v, %v, %v), want ignored", lease, ok, err)
	}
}

func TestSweepReleasedOutcomesAndRestartRecovery(t *testing.T) {
	now := time.Now()
	root := t.TempDir()
	for _, id := range []string{"succeeded", "failed", "cancelled"} {
		writeExport(t, root, id, 32, now.Add(-time.Minute))
	}
	resolve := func(_ context.Context, _, _ string) (ReferenceState, error) {
		return ReferenceReleased, nil
	}
	result, err := Sweep(context.Background(), SweepOptions{Root: root, Now: now, MaxAge: 24 * time.Hour, MaxBytes: 1024, Resolve: resolve})
	if err != nil {
		t.Fatal(err)
	}
	if result.Removed != 3 || result.CurrentBytes != 0 || result.RemovedByReason["released"] != 3 {
		t.Fatalf("first sweep = %+v, want three released removals", result)
	}
	// A restarted daemon's startup pass is idempotent after the previous
	// process removed the durable obligations.
	result, err = Sweep(context.Background(), SweepOptions{Root: root, Now: now, MaxAge: 24 * time.Hour, Resolve: resolve})
	if err != nil || result.Removed != 0 || result.CurrentBytes != 0 {
		t.Fatalf("restart sweep = (%+v, %v), want empty", result, err)
	}
}

func TestSweepCannotRaceConsumerAndRetrySucceeds(t *testing.T) {
	now := time.Now()
	root := t.TempDir()
	artifact := writeExport(t, root, "handoff", 64, now.Add(-time.Minute))
	reader, ok, err := AcquireArtifact(artifact)
	if err != nil || !ok {
		t.Fatal(err)
	}
	resolve := func(_ context.Context, _, _ string) (ReferenceState, error) {
		return ReferenceReleased, nil
	}
	result, err := Sweep(context.Background(), SweepOptions{Root: root, Now: now, MaxAge: 24 * time.Hour, Resolve: resolve})
	if err != nil {
		t.Fatal(err)
	}
	if result.Removed != 0 || result.SkippedActive != 1 {
		t.Fatalf("leased sweep = %+v, want active skip", result)
	}
	if _, err := os.Stat(artifact); err != nil {
		t.Fatalf("active artifact removed: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	// The next periodic/restart pass is the retry after the reader releases.
	result, err = Sweep(context.Background(), SweepOptions{Root: root, Now: now, MaxAge: 24 * time.Hour, Resolve: resolve})
	if err != nil || result.Removed != 1 {
		t.Fatalf("retry sweep = (%+v, %v), want removal", result, err)
	}
}

func TestSweepPreservesCrashHandoffAndBoundsLegacyTree(t *testing.T) {
	now := time.Now()
	root := t.TempDir()
	active := writeExport(t, root, "active", 80, now.Add(-2*time.Hour))
	writeExport(t, root, "legacy-old", 70, now.Add(-2*time.Hour))
	writeExport(t, root, "legacy-fresh", 60, now.Add(-10*time.Minute))
	resolve := func(_ context.Context, id, _ string) (ReferenceState, error) {
		if id == "active" {
			return ReferenceActive, nil
		}
		return ReferenceUnknown, nil
	}
	result, err := Sweep(context.Background(), SweepOptions{
		Root: root, Now: now, MaxAge: 24 * time.Hour, OrphanMinAge: time.Hour,
		MaxBytes: 140, Resolve: resolve,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RemovedByReason["pressure"] != 1 || result.CurrentBytes != 140 {
		t.Fatalf("pressure sweep = %+v, want old orphan reclaimed to cap", result)
	}
	// A builderd crash between DB handoff and imaged publication leaves the
	// durable active reference intact. A restarted imaged can still lease it.
	lease, ok, err := AcquireArtifact(active)
	if err != nil || !ok || lease == nil {
		t.Fatalf("restart acquire = (%v, %v, %v)", lease, ok, err)
	}
	_ = lease.Close()
}

func TestSweepKeepsEntriesOnResolverFailure(t *testing.T) {
	root := t.TempDir()
	artifact := writeExport(t, root, "db-down", 8, time.Now().Add(-48*time.Hour))
	result, err := Sweep(context.Background(), SweepOptions{
		Root: root, Now: time.Now(), MaxAge: time.Hour,
		Resolve: func(context.Context, string, string) (ReferenceState, error) {
			return ReferenceUnknown, errors.New("postgres unavailable")
		},
	})
	if err != nil || result.Errors != 1 || result.Removed != 0 {
		t.Fatalf("resolver failure = (%+v, %v), want fail-safe retention", result, err)
	}
	if _, err := os.Stat(artifact); err != nil {
		t.Fatalf("artifact removed on resolver failure: %v", err)
	}
}
