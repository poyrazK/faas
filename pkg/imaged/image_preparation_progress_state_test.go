package imaged

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestImagePreparationProgressStoreContract(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			store, app, dep := imagePreparationFixture(t, backend)
			ctx := t.Context()
			reader := store.(state.ImagePreparationProgressObservationStore)
			images := store.(state.DeploymentImagePreparationStore)
			if got, err := reader.ImagePreparationProgressSnapshot(ctx, "node-a"); err != nil || got != (state.ImagePreparationProgressSnapshot{}) {
				t.Fatalf("empty=%+v err=%v", got, err)
			}
			first, err := images.BeginImagePreparation(ctx, dep.ID, "node-a")
			if err != nil || first.PhaseStartedAt.IsZero() {
				t.Fatalf("new preparation=%+v err=%v", first, err)
			}
			current := first
			for range 3 {
				retry, err := images.BeginImagePreparation(ctx, dep.ID, "node-a")
				if err != nil || !retry.PhaseStartedAt.Equal(first.PhaseStartedAt) || retry.ClaimToken == current.ClaimToken || retry.UpdatedAt.Before(current.UpdatedAt) {
					t.Fatalf("retry renewed progress: first=%+v retry=%+v err=%v", first, retry, err)
				}
				current = retry
			}
			if _, err := images.BeginImagePreparation(ctx, dep.ID, "node-b"); !errors.Is(err, state.ErrImagePreparationNotOwned) {
				t.Fatalf("foreign claim=%v", err)
			}
			if err := images.TransitionImagePreparation(ctx, dep.ID, current.ClaimToken, state.DeployImaging); err != nil {
				t.Fatal(err)
			}
			if err := images.PublishImagePreparationLayer(ctx, dep.ID, first.ClaimToken, "/stale", "stale", 1); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("stale publication=%v", err)
			}
			assertProgressPhase(t, reader, "node-a", "preparing", 1, first.PhaseStartedAt)
			if err := images.PublishImagePreparationLayer(ctx, dep.ID, current.ClaimToken, "/prepared", "prepared", 42); err != nil {
				t.Fatal(err)
			}
			published, err := images.BeginImagePreparation(ctx, dep.ID, "node-a")
			if err != nil || published.Phase != state.ImageLayerPublished || !published.PhaseStartedAt.After(first.PhaseStartedAt) {
				t.Fatalf("publication did not record progress: %+v %v", published, err)
			}
			if err := images.AdvanceImagePreparation(ctx, dep.ID, current.ClaimToken, state.ImageLayerPublished, state.ImageScanComplete); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("stale advance=%v", err)
			}
			assertProgressPhase(t, reader, "node-a", "layer_published", 1, published.PhaseStartedAt)
			if err := images.AdvanceImagePreparation(ctx, dep.ID, published.ClaimToken, state.ImageLayerPublished, state.ImageScanComplete); err != nil {
				t.Fatal(err)
			}
			scanned, err := images.BeginImagePreparation(ctx, dep.ID, "node-a")
			if err != nil || !scanned.PhaseStartedAt.After(published.PhaseStartedAt) {
				t.Fatalf("scan did not record progress: %+v %v", scanned, err)
			}
			assertProgressPhase(t, reader, "node-a", "scan_complete", 1, scanned.PhaseStartedAt)
			if err := images.TransitionImagePreparation(ctx, dep.ID, scanned.ClaimToken, state.DeploySnapshotting); err != nil {
				t.Fatal(err)
			}
			if err := images.AdvanceImagePreparation(ctx, dep.ID, scanned.ClaimToken, state.ImageScanComplete, state.ImageHandedOff); err != nil {
				t.Fatal(err)
			}
			if got, err := reader.ImagePreparationProgressSnapshot(ctx, "node-a"); err != nil || got != (state.ImagePreparationProgressSnapshot{}) {
				t.Fatalf("handed off remains pending: %+v %v", got, err)
			}

			// Independent apps avoid superseding older candidates. Exceed the
			// recovery batch limit and compare the observation to real recovery.
			for i := range 23 {
				candidate, err := store.CreateApp(ctx, state.App{AccountID: app.AccountID, Slug: fmt.Sprintf("progress-%d", i), Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
				if err != nil {
					t.Fatal(err)
				}
				d, err := store.CreateDeployment(ctx, state.Deployment{AppID: candidate.ID, Kind: state.DeploymentKindImage, ImageDigest: "example.test/app@sha256:abc"})
				if err != nil {
					t.Fatal(err)
				}
				node := "node-a"
				if i == 20 {
					node = "node-b"
				} else if i == 21 {
					node = ""
				}
				if _, err := images.BeginImagePreparation(ctx, d.ID, node); err != nil {
					t.Fatal(err)
				}
				if i == 22 {
					if err := store.UpdateDeploymentStatus(ctx, d.ID, state.DeployCancelled, ""); err != nil {
						t.Fatal(err)
					}
				}
			}
			for _, tc := range []struct {
				node  string
				count int64
			}{{" node-a ", 21}, {"node-b", 2}, {"", 22}, {"unknown-node", 1}} {
				got, err := reader.ImagePreparationProgressSnapshot(ctx, tc.node)
				if err != nil || got.Preparing.Pending != tc.count || got.Preparing.UnknownAge != 0 {
					t.Fatalf("scope %q: %+v %v", tc.node, got, err)
				}
				work, err := images.ListResumableImagePreparations(ctx, tc.node, 100)
				if err != nil || int64(len(work)) != tc.count {
					t.Fatalf("recovery mismatch: %d %v", len(work), err)
				}
				var oldest time.Time
				for _, item := range work {
					p, err := images.BeginImagePreparation(ctx, item.DeploymentID, item.NodeID)
					if err != nil {
						t.Fatal(err)
					}
					if oldest.IsZero() || p.PhaseStartedAt.Before(oldest) {
						oldest = p.PhaseStartedAt
					}
				}
				if !oldest.Equal(got.Preparing.OldestPhaseStartedAt) {
					t.Fatalf("oldest=%v want=%v", got.Preparing.OldestPhaseStartedAt, oldest)
				}
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := reader.ImagePreparationProgressSnapshot(canceled, "node-a"); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled read=%v", err)
			}
		})
	}
}

func assertProgressPhase(t *testing.T, reader state.ImagePreparationProgressObservationStore, node, phase string, pending int64, oldest time.Time) {
	t.Helper()
	s, err := reader.ImagePreparationProgressSnapshot(t.Context(), node)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range preparationPhases(s) {
		if p.name == phase {
			if p.value.Pending != pending || p.value.UnknownAge != 0 || !p.value.OldestPhaseStartedAt.Equal(oldest) {
				t.Fatalf("phase %s=%+v want pending=%d oldest=%v", phase, p.value, pending, oldest)
			}
		} else if p.value != (state.ImagePreparationPhaseSnapshot{}) {
			t.Fatalf("unexpected phase %s=%+v", p.name, p.value)
		}
	}
}
