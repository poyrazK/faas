package globaldirectory

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestGlobalDirectoryMemoryContract(t *testing.T) {
	directoryContract(t, NewMemoryStore(), uuid.NewString(), uuid.NewString())
}

func directoryContract(t *testing.T, store Store, owner, other string) {
	t.Helper()
	ctx := t.Context()
	if err := store.CheckScope(ctx); err != nil {
		t.Fatal(err)
	}
	fsn, err := store.PutCell(ctx, Cell{ID: uuid.NewString(), Region: "eu-fsn", APIBase: "https://api.fsn.example.com", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	hel, err := store.PutCell(ctx, Cell{ID: uuid.NewString(), Region: "eu-hel", APIBase: "https://api.hel.example.com", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, owner, "global-shop", 2)
	if err != nil || app.RoutingPolicy != AllSelectedRegions {
		t.Fatalf("create: %+v %v", app, err)
	}
	again, err := store.CreateApp(ctx, owner, app.Slug, 1)
	if err != nil || again.ID != app.ID {
		t.Fatalf("idempotent create: %+v %v", again, err)
	}
	second, err := store.CreateApp(ctx, owner, "other-shop", 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateApp(ctx, owner, "over-limit", 2); !errors.Is(err, ErrLimit) {
		t.Fatalf("quota: %v", err)
	}
	if _, err := store.CreateApp(ctx, other, app.Slug, 2); !errors.Is(err, ErrConflict) {
		t.Fatalf("slug owner: %v", err)
	}
	if _, err := store.AppBySlug(ctx, other, app.Slug); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign owner: %v", err)
	}
	foreign, err := store.CreateApp(ctx, other, "foreign-shop", 2)
	if err != nil {
		t.Fatal(err)
	}
	fsnBinding := Binding{CellID: fsn.ID, Region: fsn.Region, CellGeneration: fsn.Generation, RegionalAccountID: uuid.NewString(), RegionalAppID: uuid.NewString(), RegionalAppSlug: "shop-fsn"}
	helBinding := Binding{CellID: hel.ID, Region: hel.Region, CellGeneration: hel.Generation, RegionalAccountID: uuid.NewString(), RegionalAppID: uuid.NewString(), RegionalAppSlug: "shop-hel"}
	first, err := store.AttachTarget(ctx, owner, app.Slug, fsnBinding)
	if err != nil || first.GlobalAppID != app.ID || !first.Resolvable() {
		t.Fatalf("attach: %+v %v", first, err)
	}
	if _, err := store.AttachTarget(ctx, owner, app.Slug, helBinding); err != nil {
		t.Fatal(err)
	}
	refreshed, err := store.AttachTarget(ctx, owner, app.Slug, fsnBinding)
	if err != nil || refreshed.VerifiedAt.Before(first.VerifiedAt) {
		t.Fatalf("refresh: %+v %v", refreshed, err)
	}
	if _, err := store.AttachTarget(ctx, owner, second.Slug, fsnBinding); !errors.Is(err, ErrConflict) {
		t.Fatalf("regional app duplicate: %v", err)
	}
	if _, err := store.AttachTarget(ctx, other, foreign.Slug, fsnBinding); !errors.Is(err, ErrConflict) {
		t.Fatalf("regional account duplicate: %v", err)
	}
	changed := fsnBinding
	changed.RegionalAppID = uuid.NewString()
	if _, err := store.AttachTarget(ctx, owner, app.Slug, changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("binding replacement: %v", err)
	}
	changed.RegionalAccountID = uuid.NewString()
	if _, err := store.AttachTarget(ctx, owner, second.Slug, changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("account replacement: %v", err)
	}
	targets, err := store.ListTargets(ctx, owner, app.Slug)
	if err != nil || len(targets) != 2 || targets[0].Region != fsn.Region || targets[1].Region != hel.Region {
		t.Fatalf("targets: %+v %v", targets, err)
	}
	if _, err := store.ListTargets(ctx, other, app.Slug); !errors.Is(err, ErrNotFound) {
		t.Fatalf("target IDOR: %v", err)
	}
	if _, err := Resolve(ctx, store, owner, app.Slug, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("implicit resolution: %v", err)
	}
	resolved, err := Resolve(ctx, store, owner, app.Slug, fsn.Region)
	if err != nil || resolved.RegionalAppID != fsnBinding.RegionalAppID {
		t.Fatalf("resolve: %+v %v", resolved, err)
	}
	fsn.Enabled = false
	fsn, err = store.PutCell(ctx, fsn)
	if err != nil || fsn.Generation != 2 {
		t.Fatalf("disable: %+v %v", fsn, err)
	}
	if _, err := Resolve(ctx, store, owner, app.Slug, fsn.Region); !errors.Is(err, ErrStaleCell) {
		t.Fatalf("disabled resolve: %v", err)
	}
	if _, err := store.AttachTarget(ctx, owner, app.Slug, fsnBinding); !errors.Is(err, ErrStaleCell) {
		t.Fatalf("disabled attach: %v", err)
	}
	fsn.Enabled, fsn.APIBase = true, "https://new.fsn.example.com"
	fsn, err = store.PutCell(ctx, fsn)
	if err != nil || fsn.Generation != 3 {
		t.Fatalf("endpoint: %+v %v", fsn, err)
	}
	if _, err := Resolve(ctx, store, owner, app.Slug, fsn.Region); !errors.Is(err, ErrStaleCell) {
		t.Fatalf("unverified endpoint: %v", err)
	}
	if _, err := store.AttachTarget(ctx, owner, app.Slug, fsnBinding); !errors.Is(err, ErrStaleCell) {
		t.Fatalf("stale proof: %v", err)
	}
	fsnBinding.CellGeneration = fsn.Generation
	if _, err := store.AttachTarget(ctx, owner, app.Slug, fsnBinding); err != nil {
		t.Fatal(err)
	}
	resolved, err = Resolve(ctx, store, owner, app.Slug, fsn.Region)
	if err != nil || resolved.APIBase != fsn.APIBase {
		t.Fatalf("reverified: %+v %v", resolved, err)
	}
	unchanged, err := store.PutCell(ctx, fsn)
	if err != nil || unchanged.Generation != fsn.Generation {
		t.Fatalf("registry retry: %+v %v", unchanged, err)
	}
	fsn.Region = "eu-paris"
	if _, err := store.PutCell(ctx, fsn); !errors.Is(err, ErrConflict) {
		t.Fatalf("region replacement: %v", err)
	}
	fsn.ID, fsn.Region = uuid.NewString(), "eu-fsn"
	if _, err := store.PutCell(ctx, fsn); !errors.Is(err, ErrConflict) {
		t.Fatalf("cell replacement: %v", err)
	}
	if err := store.DetachTarget(ctx, other, app.Slug, "eu-fsn"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("detach IDOR: %v", err)
	}
	if err := store.DetachTarget(ctx, owner, app.Slug, "eu-fsn"); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(ctx, store, owner, app.Slug, "eu-fsn"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("detached resolve: %v", err)
	}
}

func quotaRace(t *testing.T, store Store, accountID string) {
	t.Helper()
	ctx := t.Context()
	var wg sync.WaitGroup
	results := make(chan error, 12)
	for i := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.CreateApp(ctx, accountID, fmt.Sprintf("racing-app-%d", i), 3)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	won := 0
	for err := range results {
		if err == nil {
			won++
		} else if !errors.Is(err, ErrLimit) {
			t.Fatal(err)
		}
	}
	if won != 3 {
		t.Fatalf("quota admitted %d apps", won)
	}
	apps, err := store.ListApps(ctx, accountID)
	if err != nil || len(apps) != 3 {
		t.Fatalf("durable quota: %d %v", len(apps), err)
	}
}

func TestGlobalDirectoryMemoryQuotaRace(t *testing.T) {
	quotaRace(t, NewMemoryStore(), uuid.NewString())
}

func TestGlobalDirectoryAPIOrigins(t *testing.T) {
	for _, origin := range []string{"https://api.fsn.example.com", "https://127.0.0.1:443", "https://[::1]:8443"} {
		if !ValidAPIBase(origin) {
			t.Errorf("rejected %q", origin)
		}
	}
	for _, origin := range []string{"http://api.example.com", "https://USER:password@api.example.com", "https://API.example.com", "https://api.example.com/", "https://api.example.com/v1", "https://api.example.com?", "https://api.example.com:0", "https://api.example.com:65536", "https://api..example.com", "https://[bad]", "https://api.example.com#secret", "https://-invalid.example.com", "https://foo::443", "https://2001:db8::1", "https://api.example.com:00443"} {
		if ValidAPIBase(origin) {
			t.Errorf("accepted %q", origin)
		}
	}
}

func TestGlobalDirectoryMissingApp(t *testing.T) {
	store := NewMemoryStore()
	if _, err := store.ListTargets(context.Background(), uuid.NewString(), "absent"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}
