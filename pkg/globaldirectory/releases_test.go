package globaldirectory

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const releaseTestImage = "registry.example.com/shop@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func releaseSetup(t *testing.T, store Store, owner string) (App, []Target) {
	t.Helper()
	app, err := store.CreateApp(t.Context(), owner, "release-shop", 3)
	if err != nil {
		t.Fatal(err)
	}
	var targets []Target
	for _, region := range []string{"eu-fsn", "eu-hel"} {
		cell, err := store.PutCell(t.Context(), Cell{ID: uuid.NewString(), Region: region, APIBase: "https://api." + region + ".example.com", Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		target, err := store.AttachTarget(t.Context(), owner, app.Slug, Binding{CellID: cell.ID, Region: region, CellGeneration: cell.Generation, RegionalAccountID: uuid.NewString(), RegionalAppID: uuid.NewString(), RegionalAppSlug: "regional-shop"})
		if err != nil {
			t.Fatal(err)
		}
		targets = append(targets, target)
	}
	return app, targets
}

func releaseContract(t *testing.T, store Store, owner string) {
	t.Helper()
	ctx := t.Context()
	app, targets := releaseSetup(t, store, owner)
	releaseID := uuid.NewString()
	first, err := store.PlanRelease(ctx, owner, app.Slug, releaseID, releaseTestImage)
	if err != nil || first.Generation != 1 || first.Status() != ReleasePending || len(first.Targets) != 2 {
		t.Fatalf("plan: %+v %v", first, err)
	}
	if _, err := store.GetRelease(ctx, uuid.NewString(), app.Slug, releaseID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("owner: %v", err)
	}
	if _, err := store.PlanRelease(ctx, owner, app.Slug, releaseID, releaseTestImage+"a"); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed input: %v", err)
	}
	first.Targets[0].APIBase = "https://attacker.example.com"
	again, err := store.GetRelease(ctx, owner, app.Slug, releaseID)
	if err != nil || again.Targets[0].APIBase != targets[0].APIBase {
		t.Fatalf("caller mutated durable snapshot: %+v %v", again, err)
	}
	accepted := ReleaseResult{Outcome: ReleaseAccepted, DeploymentID: uuid.NewString()}
	if err := store.RecordReleaseResult(ctx, owner, app.Slug, releaseID, targets[0].Region, accepted); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordReleaseResult(ctx, owner, app.Slug, releaseID, targets[0].Region, ReleaseResult{Outcome: ReleasePending, Diagnostic: DiagnosticUnavailable}); err != nil {
		t.Fatal(err)
	}
	if err := store.DetachTarget(ctx, owner, app.Slug, targets[1].Region); err != nil {
		t.Fatal(err)
	}
	if err := store.CheckReleaseTarget(ctx, owner, app.Slug, releaseID, targets[1].Region); !errors.Is(err, ErrStaleCell) {
		t.Fatal(err)
	}
	replay, err := store.PlanRelease(ctx, owner, app.Slug, releaseID, releaseTestImage)
	if err != nil || replay.Generation != 1 || len(replay.Targets) != 2 || replay.Targets[0].Outcome != ReleaseAccepted || replay.Targets[0].DeploymentID != accepted.DeploymentID {
		t.Fatalf("snapshot retry: %+v %v", replay, err)
	}
	if err := store.RecordReleaseResult(ctx, owner, app.Slug, releaseID, targets[1].Region, ReleaseResult{Outcome: ReleaseBlocked, Diagnostic: DiagnosticIdentity}); err != nil {
		t.Fatal(err)
	}
	blocked, err := store.GetRelease(ctx, owner, app.Slug, releaseID)
	if err != nil || blocked.Status() != ReleaseBlocked {
		t.Fatalf("detached: %+v %v", blocked, err)
	}
	second, err := store.PlanRelease(ctx, owner, app.Slug, uuid.NewString(), releaseTestImage)
	if err != nil || second.Generation != 2 || len(second.Targets) != 1 {
		t.Fatalf("new target set: %+v %v", second, err)
	}
	old, err := store.GetRelease(ctx, owner, app.Slug, releaseID)
	if err != nil || old.Status() != ReleaseSuperseded {
		t.Fatalf("supersession: %+v %v", old, err)
	}
	if err := store.RecordReleaseResult(ctx, owner, app.Slug, releaseID, targets[0].Region, accepted); !errors.Is(err, ErrSuperseded) {
		t.Fatalf("late publication: %v", err)
	}
	cell := Cell{ID: targets[0].CellID, Region: targets[0].Region, APIBase: "https://changed.example.com", Enabled: true}
	if _, err := store.PutCell(ctx, cell); err != nil {
		t.Fatal(err)
	}
	rejectedID := uuid.NewString()
	if _, err := store.PlanRelease(ctx, owner, app.Slug, rejectedID, releaseTestImage); !errors.Is(err, ErrStaleCell) {
		t.Fatalf("stale allocation: %v", err)
	}
	if _, err := store.GetRelease(ctx, owner, app.Slug, rejectedID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("partial allocation: %v", err)
	}
	head, err := store.GetRelease(ctx, owner, app.Slug, second.ReleaseID)
	if err != nil || head.DesiredGeneration != 2 {
		t.Fatalf("stale allocation advanced head: %+v %v", head, err)
	}
}

func releaseRace(t *testing.T, store Store, owner string) {
	t.Helper()
	app, _ := releaseSetup(t, store, owner)
	var wg sync.WaitGroup
	rows := make(chan Release, 16)
	errs := make(chan error, 16)
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := store.PlanRelease(t.Context(), owner, app.Slug, uuid.NewString(), releaseTestImage)
			if err != nil {
				errs <- err
			} else {
				rows <- r
			}
		}()
	}
	wg.Wait()
	close(rows)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	gens := map[int64]bool{}
	var last Release
	for r := range rows {
		if gens[r.Generation] {
			t.Fatal("duplicate generation")
		}
		gens[r.Generation] = true
		if r.Generation > last.Generation {
			last = r
		}
	}
	if len(gens) != 16 || last.Generation != 16 {
		t.Fatalf("generations: %v", gens)
	}
	r, err := store.GetRelease(t.Context(), owner, app.Slug, last.ReleaseID)
	if err != nil || r.DesiredGeneration != 16 {
		t.Fatalf("head: %+v %v", r, err)
	}
}

func TestGlobalReleaseMemoryContract(t *testing.T) {
	releaseContract(t, NewMemoryStore(), uuid.NewString())
}
func TestGlobalReleaseMemoryRace(t *testing.T) { releaseRace(t, NewMemoryStore(), uuid.NewString()) }
func TestGlobalReleaseNoTargets(t *testing.T) {
	store := NewMemoryStore()
	owner := uuid.NewString()
	app, err := store.CreateApp(t.Context(), owner, "empty-release", 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PlanRelease(t.Context(), owner, app.Slug, uuid.NewString(), releaseTestImage); !errors.Is(err, ErrNoTargets) {
		t.Fatal(err)
	}
	for i, result := range []ReleaseResult{{Outcome: "bad"}, {Outcome: ReleaseAccepted}, {Outcome: ReleasePending, Diagnostic: "secret"}, {Outcome: ReleaseBlocked, DeploymentID: uuid.NewString()}} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			if result.valid() {
				t.Fatal("invalid result accepted")
			}
		})
	}
}

func TestGlobalReleaseTargetBound(t *testing.T) {
	store := NewMemoryStore()
	owner := uuid.NewString()
	releaseTargetBound(t, store, owner)
}

func releaseTargetBound(t *testing.T, store Store, owner string) {
	t.Helper()
	app, err := store.CreateApp(t.Context(), owner, "target-bound", 1)
	if err != nil {
		t.Fatal(err)
	}
	for i := range api.GlobalImageReleaseTargetsMax + 1 {
		region := fmt.Sprintf("region-%d", i)
		cell, err := store.PutCell(t.Context(), Cell{ID: uuid.NewString(), Region: region, APIBase: "https://" + region + ".example.com", Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		_, err = store.AttachTarget(t.Context(), owner, app.Slug, Binding{CellID: cell.ID, Region: region, CellGeneration: 1, RegionalAccountID: uuid.NewString(), RegionalAppID: uuid.NewString(), RegionalAppSlug: "regional-shop"})
		if err != nil {
			t.Fatal(err)
		}
	}
	id := uuid.NewString()
	_, err = store.PlanRelease(t.Context(), owner, app.Slug, id, releaseTestImage)
	assertReleaseLimit(t, err, "targets", api.GlobalImageReleaseTargetsMax, api.GlobalImageReleaseTargetsMax+1)
	if _, err := store.GetRelease(t.Context(), owner, app.Slug, id); !errors.Is(err, ErrNotFound) {
		t.Fatal("partial bounded allocation")
	}
}

func assertReleaseLimit(t *testing.T, err error, kind string, limit, observed int64) {
	t.Helper()
	var bound *ReleaseLimitError
	if !errors.Is(err, ErrReleaseLimit) || !errors.As(err, &bound) || bound.Kind != kind || bound.Limit != limit || bound.Observed != observed {
		t.Fatalf("release limit: %v, %+v", err, bound)
	}
}

func TestGlobalReleaseGenerationBound(t *testing.T) {
	store := NewMemoryStore()
	owner := uuid.NewString()
	app, _ := releaseSetup(t, store, owner)
	store.releaseHeads[app.ID] = api.GlobalImageReleaseGenerationMax
	id := uuid.NewString()
	_, err := store.PlanRelease(t.Context(), owner, app.Slug, id, releaseTestImage)
	assertReleaseLimit(t, err, "generation", api.GlobalImageReleaseGenerationMax, api.GlobalImageReleaseGenerationMax+1)
	if _, err := store.GetRelease(t.Context(), owner, app.Slug, id); !errors.Is(err, ErrNotFound) {
		t.Fatal("partial allocation", err)
	}
}

func TestGlobalReleaseLimitDetailValidation(t *testing.T) {
	for _, detail := range []string{"secret", "targets:32:0", "targets:64:65", "unknown:32:33", "targets:32:33:secret", "targets:32:9223372036854775808"} {
		if err := releaseLimitFromDetail(detail); !errors.Is(err, ErrConflict) || strings.Contains(err.Error(), detail) {
			t.Fatalf("unsafe detail %q: %v", detail, err)
		}
	}
}
