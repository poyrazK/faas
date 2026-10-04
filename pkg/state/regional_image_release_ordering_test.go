package state

// adr: 479

import (
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func runRegionalImageReleaseOrdering(t *testing.T, store Store, releases RegionalImageReleaseStore, cellID string, account Account) {
	t.Helper()
	t.Run("delayed-older-and-equal-generation", func(t *testing.T) {
		app := releaseTestApp(t, store, account)
		intent, dep := releaseTestIntent(cellID, app)
		intent.GlobalAppID, intent.Generation = uuid.NewString(), 20
		accepted, err := releases.CreateRegionalImageRelease(t.Context(), intent, dep, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, generation := range []int64{1, 19, 20} {
			stale := intent
			stale.ReleaseID, stale.Generation = uuid.NewString(), generation
			if _, err := releases.CreateRegionalImageRelease(t.Context(), stale, dep, releaseTestActivity(app)); !errors.Is(err, ErrRegionalImageReleaseOrder) {
				t.Fatalf("delayed generation %d accepted: %v", generation, err)
			}
			if _, err := releases.RegionalImageReleaseReceipt(t.Context(), cellID, app.ID, stale.ReleaseID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("rejected release persisted: %v", err)
			}
		}
		current, err := store.DeploymentByID(t.Context(), accepted.Receipt.DeploymentID)
		if err != nil || current.Status != DeployPending {
			t.Fatalf("newer deployment changed: %+v %v", current, err)
		}
		rows, err := store.ListDeploymentsForApp(t.Context(), app.ID, 0, 0)
		if err != nil || len(rows) != 1 {
			t.Fatalf("rejected generations created work: %d %v", len(rows), err)
		}
	})
	t.Run("old-receipt-replays-before-fence", func(t *testing.T) {
		app := releaseTestApp(t, store, account)
		intent, dep := releaseTestIntent(cellID, app)
		intent.GlobalAppID, intent.Generation = uuid.NewString(), 1
		first, err := releases.CreateRegionalImageRelease(t.Context(), intent, dep, nil)
		if err != nil {
			t.Fatal(err)
		}
		newer := intent
		newer.ReleaseID, newer.Generation = uuid.NewString(), 2
		second, err := releases.CreateRegionalImageRelease(t.Context(), newer, dep, nil)
		if err != nil {
			t.Fatal(err)
		}
		retry, err := releases.CreateRegionalImageRelease(t.Context(), intent, dep, nil)
		if err != nil || !retry.Replayed || retry.Receipt.DeploymentID != first.Receipt.DeploymentID || retry.Receipt.Generation != 1 {
			t.Fatalf("matching older receipt: %+v %v", retry, err)
		}
		current, err := store.DeploymentByID(t.Context(), second.Receipt.DeploymentID)
		if err != nil || current.Status != DeployPending {
			t.Fatalf("replay changed newer deployment: %+v %v", current, err)
		}
		for _, change := range []string{"generation", "global-app", "omit"} {
			changed := intent
			switch change {
			case "generation":
				changed.Generation = 3
			case "global-app":
				changed.GlobalAppID = uuid.NewString()
			case "omit":
				changed.GlobalAppID, changed.Generation = "", 0
			}
			if _, err := releases.CreateRegionalImageRelease(t.Context(), changed, dep, nil); !errors.Is(err, ErrRegionalImageReleaseConflict) {
				t.Fatalf("changed %s: %v", change, err)
			}
		}
		foreign := newer
		foreign.ReleaseID, foreign.GlobalAppID, foreign.Generation = uuid.NewString(), uuid.NewString(), 3
		if _, err := releases.CreateRegionalImageRelease(t.Context(), foreign, dep, nil); !errors.Is(err, ErrRegionalImageReleaseOrder) {
			t.Fatalf("stream rebound: %v", err)
		}
	})
	t.Run("concurrent-generation-order", func(t *testing.T) {
		app := releaseTestApp(t, store, account)
		base, dep := releaseTestIntent(cellID, app)
		base.GlobalAppID = uuid.NewString()
		const count = 16
		lastReleaseID := uuid.NewString()
		errorsCh := make(chan error, count)
		var wg sync.WaitGroup
		for generation := range count {
			wg.Go(func() {
				intent := base
				intent.ReleaseID, intent.Generation = uuid.NewString(), int64(generation+1)
				if generation == count-1 {
					intent.ReleaseID = lastReleaseID
				}
				_, err := releases.CreateRegionalImageRelease(t.Context(), intent, dep, nil)
				if err != nil && !errors.Is(err, ErrRegionalImageReleaseOrder) {
					errorsCh <- err
				}
			})
		}
		wg.Wait()
		close(errorsCh)
		for err := range errorsCh {
			t.Fatal(err)
		}
		rows, err := store.ListDeploymentsForApp(t.Context(), app.ID, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		pending := 0
		for _, d := range rows {
			if d.Status == DeployPending {
				pending++
			}
		}
		if pending != 1 {
			t.Fatalf("pending deployments=%d", pending)
		}
		highest, err := releases.RegionalImageReleaseReceipt(t.Context(), cellID, app.ID, lastReleaseID)
		if err != nil || highest.Generation != count || highest.DeploymentStatus != string(DeployPending) {
			t.Fatalf("highest generation is not pending: %+v %v", highest, err)
		}
		probe := base
		probe.ReleaseID, probe.Generation = uuid.NewString(), count
		if _, err := releases.CreateRegionalImageRelease(t.Context(), probe, dep, nil); !errors.Is(err, ErrRegionalImageReleaseOrder) {
			t.Fatalf("highest generation not retained: %v", err)
		}
	})
	t.Run("invalid-ordering", func(t *testing.T) {
		app := releaseTestApp(t, store, account)
		base, dep := releaseTestIntent(cellID, app)
		for _, ordering := range []struct {
			id         string
			generation int64
		}{{"", 1}, {uuid.Nil.String(), 1}, {uuid.NewString(), 0}, {uuid.NewString(), -1}, {uuid.NewString(), api.GlobalImageReleaseGenerationMax + 1}, {"bad", 1}} {
			intent := base
			intent.GlobalAppID, intent.Generation = ordering.id, ordering.generation
			if _, err := releases.CreateRegionalImageRelease(t.Context(), intent, dep, nil); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("invalid ordering %+v: %v", ordering, err)
			}
		}
		base.GlobalAppID, base.Generation = uuid.NewString(), api.GlobalImageReleaseGenerationMax
		if _, err := releases.CreateRegionalImageRelease(t.Context(), base, dep, nil); err != nil {
			t.Fatalf("maximum exact JSON generation: %v", err)
		}
	})
}

func TestMemRegionalImageReleaseOrdering(t *testing.T) {
	store := NewMemStore()
	account, err := store.CreateAccount(t.Context(), "order@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	runRegionalImageReleaseOrdering(t, store, store, uuid.NewString(), account)
}
