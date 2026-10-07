package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appstandards"
	"github.com/onebox-faas/faas/pkg/state"
)

type standardTestStore interface {
	state.ApplicationStandardStore
	CreateAccount(context.Context, string, api.Plan) (state.Account, error)
	CreateOrg(context.Context, state.Org) (state.Org, error)
}

func TestMemApplicationStandardVersions(t *testing.T) { standardStoreLifecycle(t, state.NewMemStore()) }

func standardStoreLifecycle(t *testing.T, store standardTestStore) {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "standards@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	org, err := store.CreateOrg(ctx, state.Org{Slug: "standard-test-org", Name: "Standard test org", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	request := state.ApplicationStandardPublish{OrgID: org.ID, ActorID: account.ID, Slug: "production-baseline", CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{Description: "Production logging and security", Definition: json.RawMessage(`{"require_signed":{"mode":"mandatory","value":true}}`)}}
	first, err := store.PublishApplicationStandardVersion(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if first.Version != 1 || first.StandardID == "" || len(first.DefinitionHash) != 64 || first.CreatedAt.IsZero() {
		t.Fatalf("invalid publication %+v", first)
	}
	if _, err := store.PublishApplicationStandardVersion(ctx, request); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale create: %v", err)
	}
	first.Definition[appstandards.RequireSigned] = appstandards.Rule{Mode: appstandards.Default, Value: json.RawMessage("false")}
	stored, err := store.GetApplicationStandardVersion(ctx, org.ID, request.Slug, 1)
	if err != nil || string(stored.Definition[appstandards.RequireSigned].Value) != "true" {
		t.Fatalf("read alias changed immutable version: %+v %v", stored, err)
	}
	request.ExpectedVersion = 1
	request.Description = "Updated description"
	var successes atomic.Int32
	var group sync.WaitGroup
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			if _, err := store.PublishApplicationStandardVersion(ctx, request); err == nil {
				successes.Add(1)
			} else if !errors.Is(err, state.ErrConflict) {
				t.Errorf("concurrent publication: %v", err)
			}
		}()
	}
	group.Wait()
	if successes.Load() != 1 {
		t.Fatalf("concurrent writers accepted: %d", successes.Load())
	}
	latest, err := store.GetApplicationStandardVersion(ctx, org.ID, request.Slug, 0)
	if err != nil || latest.Version != 2 || latest.StandardID != stored.StandardID || latest.DefinitionHash != stored.DefinitionHash {
		t.Fatalf("latest: %+v %v", latest, err)
	}
	old, err := store.GetApplicationStandardVersion(ctx, org.ID, request.Slug, 1)
	if err != nil || old.Description == latest.Description {
		t.Fatalf("old version mutated %+v %v", old, err)
	}
	otherOrg, err := store.CreateOrg(ctx, state.Org{Slug: "standard-other-org", Name: "Other standard org", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetApplicationStandardVersion(ctx, otherOrg.ID, request.Slug, 1); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-org read: %v", err)
	}
	if _, err := store.GetApplicationStandardVersion(ctx, org.ID, request.Slug, 3); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing version: %v", err)
	}
	request.ExpectedVersion = 0
	for _, slug := range []string{"aaa-baseline", "zzz-baseline"} {
		request.Slug = slug
		if _, err := store.PublishApplicationStandardVersion(ctx, request); err != nil {
			t.Fatal(err)
		}
	}
	page, err := store.ListApplicationStandards(ctx, org.ID, "", 2)
	if err != nil || len(page) != 2 || page[0].Slug != "aaa-baseline" || page[1].Version != 2 {
		t.Fatalf("page: %+v %v", page, err)
	}
	next, err := store.ListApplicationStandards(ctx, org.ID, page[1].Slug, 2)
	if err != nil || len(next) != 1 || next[0].Slug != "zzz-baseline" {
		t.Fatalf("next page: %+v %v", next, err)
	}
	request.Slug = "bad/new"
	if _, err := store.PublishApplicationStandardVersion(ctx, request); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("invalid slug: %v", err)
	}
}
