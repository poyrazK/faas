package toolregistry_test

import (
	"bytes"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/toolgrant/granttest"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func TestToolGrantsMemory(t *testing.T) { exerciseToolGrants(t, state.NewMemStore()) }
func TestToolGrantsPostgres(t *testing.T) {
	exerciseToolGrants(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}

func exerciseToolGrants(t *testing.T, store state.Store) {
	t.Helper()
	ctx := t.Context()
	f := granttest.New(t, store, "")
	in := api.ToolGrantIntent{KeyID: f.AgentKey.ID, Version: f.PublicationA.ID, Name: "visible", ExpiresAt: time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond), AllowInvocation: true}
	id := uuid.NewString()
	page, err := f.Agent.SearchToolRegistry(ctx, "", "", 20)
	if err != nil || len(page.Versions) != 0 {
		t.Fatalf("ungranted registry: %+v %v", page, err)
	}
	_, err = f.Agent.GetRegistryTool(ctx, in.Version, in.Name)
	requireGrantStatus(t, err, http.StatusNotFound)
	for _, client := range []*api.Client{f.Agent, f.Other} {
		_, err = client.GetToolPublication(ctx, in.Version)
		requireGrantStatus(t, err, http.StatusForbidden)
		_, err = client.PutToolGrant(ctx, id, in)
		requireGrantStatus(t, err, http.StatusForbidden)
		_, err = client.ListToolGrants(ctx, "", 20)
		requireGrantStatus(t, err, http.StatusForbidden)
	}
	created, err := f.Owner.PutToolGrant(ctx, id, in)
	if err != nil || created.AppID != f.AppA.ID || created.ProjectID != f.ProjectA.ID {
		t.Fatalf("grant create: %+v %v", created, err)
	}
	page, err = f.Agent.SearchToolRegistry(ctx, "", "", 20)
	if err != nil || len(page.Versions) != 1 || !reflect.DeepEqual(page.Versions[0].Tools, []string{"visible"}) || page.Versions[0].AppID != f.AppA.ID {
		t.Fatalf("granted registry: %+v %v", page, err)
	}
	page, err = f.Agent.SearchToolRegistry(ctx, "hidden", "", 20)
	if err != nil || len(page.Versions) != 0 {
		t.Fatal("search disclosed an ungranted name")
	}
	_, err = f.Agent.GetRegistryTool(ctx, in.Version, "hidden")
	requireGrantStatus(t, err, http.StatusNotFound)
	description, err := f.Agent.GetRegistryTool(ctx, in.Version, "visible")
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := mcphosting.ReadContract(bytes.NewReader(description.Catalog))
	if err != nil || len(catalog.Tools) != 1 || catalog.Tools[0].Name != "visible" || strings.Contains(string(description.Catalog), "hidden") || description.CatalogContractSHA256 != toolpublication.Digest(description.Catalog) || description.PublishedCatalogContractSHA256 != toolpublication.Digest(f.PublicationA.Outcome.Catalog) || description.CatalogContractSHA256 == description.PublishedCatalogContractSHA256 {
		t.Fatal("descriptor failed single-tool isolation or digest binding")
	}
	page, err = f.Other.SearchToolRegistry(ctx, "", "", 20)
	if err != nil || len(page.Versions) != 0 {
		t.Fatal("another connection inherited the grant")
	}
	_, err = f.Agent.GetRegistryTool(ctx, f.PublicationB.ID, "visible")
	requireGrantStatus(t, err, http.StatusNotFound)
	wrongProject := in
	wrongProject.Version = f.PublicationB.ID
	_, err = f.Owner.PutToolGrant(ctx, uuid.NewString(), wrongProject)
	requireGrantStatus(t, err, http.StatusNotFound)
	// Simulate retained authority from before project access was narrowed. Reads
	// must intersect the grant with current key limits, not trust stored intent.
	outside := created
	outside.ID, outside.App, outside.AppID, outside.ProjectID = uuid.NewString(), f.AppB.Slug, f.AppB.ID, f.ProjectB.ID
	outside.Intent.Version = f.PublicationB.ID
	if _, err = store.(state.ToolGrantStore).PutToolGrant(ctx, outside); err != nil {
		t.Fatal(err)
	}
	_, err = f.Agent.GetRegistryTool(ctx, f.PublicationB.ID, "visible")
	requireGrantStatus(t, err, http.StatusNotFound)
	page, err = f.Agent.SearchToolRegistry(ctx, "beta", "", 20)
	if err != nil || len(page.Versions) != 0 {
		t.Fatal("stored grant widened current project access")
	}
	// Recording an invocation ceiling never opens the existing local executor.
	_, err = f.Agent.GetToolInvocation(ctx, uuid.NewString())
	requireGrantStatus(t, err, http.StatusForbidden)
	_, err = f.Agent.BeginToolInvocation(ctx, uuid.NewString(), api.ToolInvocationIntent{})
	requireGrantStatus(t, err, http.StatusForbidden)
	changed := in
	changed.AllowInvocation = false
	_, err = f.Owner.PutToolGrant(ctx, id, changed)
	requireGrantStatus(t, err, http.StatusConflict)
	for _, expires := range []time.Time{time.Now().Add(-time.Second), time.Now().Add(api.ToolGrantMaxLifetime + time.Hour)} {
		bad := in
		bad.ExpiresAt = expires
		_, err = f.Owner.PutToolGrant(ctx, uuid.NewString(), bad)
		requireGrantStatus(t, err, http.StatusBadRequest)
	}
	revoked, err := f.Owner.RevokeToolGrant(ctx, id)
	if err != nil || revoked.RevokedAt == nil {
		t.Fatal("grant revoke failed", err)
	}
	again, err := f.Owner.PutToolGrant(ctx, id, in)
	if err != nil || again.RevokedAt == nil || !again.RevokedAt.Equal(*revoked.RevokedAt) {
		t.Fatal("put reopened revoked authority", err)
	}
	_, err = f.Agent.GetRegistryTool(ctx, in.Version, in.Name)
	requireGrantStatus(t, err, http.StatusNotFound)
	page, err = f.Agent.SearchToolRegistry(ctx, "", "", 20)
	if err != nil || len(page.Versions) != 0 {
		t.Fatal("revocation did not apply on next read")
	}
	// Seed a historical expired intent without a wall-clock sleep.
	expired := created
	expired.ID = uuid.NewString()
	expired.CreatedAt = time.Now().UTC().Add(-2 * time.Hour)
	expired.Intent.ExpiresAt = time.Now().UTC().Add(-time.Hour)
	if _, err = store.(state.ToolGrantStore).PutToolGrant(ctx, expired); err != nil {
		t.Fatal(err)
	}
	_, err = f.Agent.GetRegistryTool(ctx, in.Version, in.Name)
	requireGrantStatus(t, err, http.StatusNotFound)
	// Concurrent creation retains one immutable record, including its timestamp.
	concurrentID := uuid.NewString()
	var wg sync.WaitGroup
	results := make(chan api.ToolGrantRecord, 8)
	failures := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			g, createErr := f.Owner.PutToolGrant(ctx, concurrentID, in)
			if createErr != nil {
				failures <- createErr
			} else {
				results <- g
			}
		})
	}
	wg.Wait()
	close(results)
	close(failures)
	for failure := range failures {
		t.Fatal(failure)
	}
	var first *api.ToolGrantRecord
	for g := range results {
		if first == nil {
			copy := g
			first = &copy
		} else if !first.CreatedAt.Equal(g.CreatedAt) {
			t.Fatal("concurrent duplicate created another record")
		}
	}
	// A second kind of agent connection can receive its own explicit grant.
	other := in
	other.KeyID = f.OtherKey.ID
	if _, err = f.Owner.PutToolGrant(ctx, uuid.NewString(), other); err != nil {
		t.Fatal(err)
	}
	page, err = f.Other.SearchToolRegistry(ctx, "", "", 20)
	if err != nil || len(page.Versions) != 1 {
		t.Fatal("hosted sign-in grant unavailable", err)
	}
	// Quotas count retained identities, including revoked and expired grants.
	for i := 4; i < api.ToolGrantsPerKey; i++ {
		if _, err = f.Owner.PutToolGrant(ctx, uuid.NewString(), in); err != nil {
			t.Fatalf("quota %d: %v", i, err)
		}
	}
	_, err = f.Owner.PutToolGrant(ctx, uuid.NewString(), in)
	requireGrantStatus(t, err, http.StatusTooManyRequests)
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) || apiErr.Problem.Code != "tool_grant_limit" {
		t.Fatal("grant quota has no typed problem")
	}
	ownerPage, err := f.Owner.ListToolGrants(ctx, "", 1)
	if err != nil || len(ownerPage.Grants) != 1 || ownerPage.NextCursor == "" {
		t.Fatal("owner grant pagination failed", err)
	}
	ownerNext, err := f.Owner.ListToolGrants(ctx, ownerPage.NextCursor, 1)
	if err != nil || len(ownerNext.Grants) != 1 || ownerNext.Grants[0].ID <= ownerPage.Grants[0].ID {
		t.Fatal("grant cursor repeated a record", err)
	}
	if err = store.DeleteAPIKey(ctx, f.Account.ID, f.AgentKey.ID); err != nil {
		t.Fatal(err)
	}
	// The fixture still authenticates its cached key copy. Authorization must
	// reload the revoked key rather than trust that stale authenticated copy.
	_, err = f.Agent.GetRegistryTool(ctx, in.Version, in.Name)
	requireGrantStatus(t, err, http.StatusNotFound)
}

func requireGrantStatus(t *testing.T, err error, want int) {
	t.Helper()
	var e *api.APIError
	if !errors.As(err, &e) || e.Problem.Status != want {
		t.Fatalf("status: %v; want %d", err, want)
	}
}
