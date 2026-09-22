package buildexport

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

// ADR-145: canceling the cleanup worker must preserve pending image handoffs.
func TestSweepCancellationPreservesExports(t *testing.T) {
	for _, when := range []string{"before inventory", "during resolution", "resolver returns cancellation"} {
		t.Run(when, func(t *testing.T) {
			now := time.Now()
			root := t.TempDir()
			artifacts := []string{
				writeExport(t, root, "a", 16, now.Add(-48*time.Hour)),
				writeExport(t, root, "b", 16, now.Add(-48*time.Hour)),
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			opts := SweepOptions{Root: root, Now: now, MaxAge: time.Hour, MaxBytes: 1}
			if when == "before inventory" {
				cancel()
			} else {
				opts.Resolve = func(_ context.Context, id, _ string) (ReferenceState, error) {
					if id == "b" {
						cancel()
						if when == "resolver returns cancellation" {
							return ReferenceUnknown, ctx.Err()
						}
					}
					return ReferenceReleased, nil
				}
			}
			result, err := Sweep(ctx, opts)
			if !errors.Is(err, context.Canceled) || result.Removed != 0 || result.Errors != 0 {
				t.Errorf("canceled sweep = (%+v, %v), want cancellation without cleanup", result, err)
			}
			for _, artifact := range artifacts {
				if _, err := os.Stat(artifact); err != nil {
					t.Errorf("canceled sweep removed %s: %v", artifact, err)
				}
			}
		})
	}
}

func TestSweepCountsLeasedExportOnce(t *testing.T) {
	for _, state := range []ReferenceState{ReferenceUnknown, ReferenceActive, ReferenceReleased} {
		root := t.TempDir()
		artifact := writeExport(t, root, "leased", 16, time.Now().Add(-48*time.Hour))
		lease, _, err := AcquireArtifact(artifact)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = lease.Close() })
		result, err := Sweep(context.Background(), SweepOptions{
			Root: root, MaxAge: time.Hour, MaxBytes: 1,
			Resolve: func(context.Context, string, string) (ReferenceState, error) { return state, nil },
		})
		if err != nil || result.SkippedActive != 1 || result.Removed != 0 || result.CurrentBytes != 16 {
			t.Errorf("state %d: sweep = (%+v, %v), want one leased export", state, result, err)
		}
	}
}

func TestCanceledRemovalReleasesLeaseAndPreservesExport(t *testing.T) {
	root := t.TempDir()
	artifact := writeExport(t, root, "handoff", 16, time.Now())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := SweepResult{CurrentBytes: 16, RemovedByReason: map[string]int{}}
	item := &entry{path: root, artifact: artifact, bytes: 16}
	if err := removeEntry(ctx, item, "released", &result); !errors.Is(err, context.Canceled) {
		t.Fatalf("removeEntry = %v, want cancellation", err)
	}
	if result.Removed != 0 || result.CurrentBytes != 16 {
		t.Fatalf("canceled removal changed accounting: %+v", result)
	}
	lease, ok, err := AcquireArtifact(artifact)
	if err != nil || !ok || lease == nil {
		t.Fatalf("canceled removal lost the artifact or leaked its lock: (%v, %v, %v)", lease, ok, err)
	}
	_ = lease.Close()
	if _, _, err := treeUsage(ctx, root); !errors.Is(err, context.Canceled) {
		t.Fatalf("treeUsage = %v, want cancellation", err)
	}
}
