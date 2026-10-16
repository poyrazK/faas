package state_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func scenarioConsumerIntentContract(t *testing.T, store state.Store) {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "scenario-intent@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	expiry := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "intent-app", RAMMB: 128, Status: state.AppActive, PreviewOfSlug: "intent-app", PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expiry})
	if err != nil {
		t.Fatal(err)
	}
	run := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	req := api.ScenarioTestConsumerRequest{AppID: app.ID, ExpiresAt: expiry}
	if _, err := store.EnsureScenarioTestConsumer(ctx, account.ID, run, "customer", req); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("unregistered app", err)
	}
	if err := store.RegisterScenarioTestMembers(ctx, account.ID, run, []state.ScenarioTestMember{{Workload: "api", AppID: app.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LookupScenarioTestConsumer(ctx, account.ID, run, "customer", req); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("lookup created a consumer", err)
	}
	var wg sync.WaitGroup
	ids := make(chan string, 12)
	failures := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := store.EnsureScenarioTestConsumer(ctx, account.ID, run, "customer", req)
			if err != nil {
				failures <- err
			} else {
				ids <- c.ID
			}
		}()
	}
	wg.Wait()
	close(ids)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	var id string
	for next := range ids {
		if id != "" && id != next {
			t.Fatal("retry created another consumer")
		}
		id = next
	}
	if id == "" {
		t.Fatal("no consumer")
	}
	rows, err := store.ListAPIConsumersForApp(ctx, account.ID, app.ID)
	if err != nil || len(rows) != 1 {
		t.Fatal("duplicate consumers", len(rows), err)
	}
	changed := req
	changed.ExpiresAt = changed.ExpiresAt.Add(time.Hour)
	if _, err := store.EnsureScenarioTestConsumer(ctx, account.ID, run, "customer", changed); !errors.Is(err, state.ErrConflict) {
		t.Fatal("changed intent accepted", err)
	}
	if _, err := store.LookupScenarioTestConsumer(ctx, account.ID, run, "customer", changed); !errors.Is(err, state.ErrConflict) {
		t.Fatal("changed lookup accepted", err)
	}
	other, err := store.CreateAccount(ctx, "scenario-other@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.LookupScenarioTestConsumer(ctx, other.ID, run, "customer", req); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("cross-account receipt", err)
	}
	if _, err := store.EnsureScenarioTestConsumer(ctx, other.ID, run, "customer", req); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("cross-account creation", err)
	}
	if _, err := store.CreateAPIConsumer(ctx, account.ID, app.ID, "gregale-test-"+run+"-unowned", "Scenario unowned"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnsureScenarioTestConsumer(ctx, account.ID, run, "unowned", req); !errors.Is(err, state.ErrConflict) {
		t.Fatal("adopted an unreceipted consumer", err)
	}
	linked, err := store.EnsureScenarioTestConsumer(ctx, account.ID, run, "linked", req)
	if err != nil {
		t.Fatal(err)
	}
	tenants := store.(state.PlatformTenantStore)
	tenant, _, err := tenants.CreatePlatformTenant(ctx, account.ID, linked.ExternalRef, "Customer", 250)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tenants.LinkPlatformTenantConsumer(ctx, account.ID, tenant.ID, linked.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LookupScenarioTestConsumer(ctx, account.ID, run, "linked", req); !errors.Is(err, state.ErrConflict) {
		t.Fatal("changed consumer ownership accepted", err)
	}
	if _, err := store.RevokeScenarioTestConsumer(ctx, account.ID, run, "linked", req, linked.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("changed ownership revoked", err)
	}
	if _, err := store.SoftDeleteAppCascade(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteScenarioTestMembers(ctx, account.ID, run); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RevokeScenarioTestConsumer(ctx, account.ID, run, "customer", req, linked.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("replacement ID revoked", err)
	}
	for i := 0; i < 2; i++ {
		c, err := store.RevokeScenarioTestConsumer(ctx, account.ID, run, "customer", req, id)
		if err != nil || c.ID != id || c.Status != state.APIConsumerStatusRevoked {
			t.Fatal("post-teardown revocation", c, err)
		}
	}
	c, err := store.LookupScenarioTestConsumer(ctx, account.ID, run, "customer", req)
	if err != nil || c.ID != id || c.Status != state.APIConsumerStatusRevoked {
		t.Fatal("receipt lost after cleanup", c, err)
	}
	c, err = store.EnsureScenarioTestConsumer(ctx, account.ID, run, "customer", req)
	if err != nil || c.ID != id || c.Status != state.APIConsumerStatusRevoked {
		t.Fatal("retry resurrected consumer", c, err)
	}
	if _, err := store.EnsureScenarioTestConsumer(ctx, account.ID, run, "another", req); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("created consumer after teardown", err)
	}
}

func TestScenarioTestConsumerIntentMemory(t *testing.T) {
	// adr: 984 — atomic intent receipts survive retries and forbid replacement ownership.
	scenarioConsumerIntentContract(t, state.NewMemStore())
}
