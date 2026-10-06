// adr: 487
package state_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"testing/quick"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/exclusivework"
	"github.com/onebox-faas/faas/pkg/state"
)

func routedCommitFixture(t *testing.T) (*state.PgStore, *pgxpool.Pool, state.CommitSource, state.Invocation, []string) {
	t.Helper()
	store, pool, legacy, inv := commitManagedFixture(t)
	ctx := t.Context()
	_, err := store.UpsertExclusiveWorkPolicy(ctx, legacy.AccountID, exclusivework.Policy{
		Name: "customer-orders", Scope: "platform_tenant", Contention: "queue", MemberAppIDs: []string{legacy.AppID}, LeaseSeconds: 15, MaxAttemptSeconds: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE apps SET platform_tenant_required=true WHERE id=$1::uuid`, legacy.AppID); err != nil {
		t.Fatal(err)
	}
	src, err := store.CreateCommitSource(ctx, state.CommitSource{AccountID: legacy.AccountID, AppID: legacy.AppID, Name: "customer-orders", OperationPolicy: "customer-orders", ContractVersion: 2, AllowTenantSelection: true})
	if err != nil {
		t.Fatal(err)
	}
	var tenants []string
	for _, name := range []string{"customer-a", "customer-b"} {
		tenant, _, err := store.CreatePlatformTenant(ctx, src.AccountID, name, name, 250)
		if err != nil {
			t.Fatal(err)
		}
		surface, err := store.CreateTenantSurfaceIfUnderQuota(ctx, pgTenantSurfaceParams(src.AccountID, src.AppID, name), api.MustLimitsFor(api.PlanPro))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.LinkPlatformTenantSurface(ctx, src.AccountID, tenant.ID, surface.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE tenant_surfaces SET status='active' WHERE id=$1::uuid`, surface.ID); err != nil {
			t.Fatal(err)
		}
		tenants = append(tenants, tenant.ID)
	}
	return store, pool, src, inv, tenants
}

func TestPgCommitCustomerRoutingIsolationAndReplay(t *testing.T) {
	store, pool, src, inv, tenants := routedCommitFixture(t)
	ctx := t.Context()
	event := uuid.NewString()
	route := &api.CommitRouting{Version: 2, PlatformTenantID: tenants[0], Key: json.RawMessage(`1`)}
	receipts := make(chan state.CommitReceipt, 8)
	failures := make(chan error, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := store.AcceptCommitOperation(ctx, src.AccountID, src.ID, event, "order.created", inv.Payload, inv, route)
			receipts <- r
			failures <- err
		}()
	}
	wg.Wait()
	close(receipts)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	var first state.CommitReceipt
	for r := range receipts {
		if first.ID == "" {
			first = r
		}
		if r.ID != first.ID || r.OperationID != first.OperationID {
			t.Fatalf("concurrent admission reopened event: %+v %+v", first, r)
		}
	}
	original, err := store.ExclusiveOperationByID(ctx, src.AccountID, first.OperationID)
	if err != nil || original.PlatformTenantID != tenants[0] {
		t.Fatalf("customer ownership: %+v %v", original, err)
	}
	for _, tc := range []struct {
		name, tenant, key string
		sameLane          bool
	}{
		{"same-customer-number-normalization", tenants[0], `1.0`, true},
		{"different-customer", tenants[1], `1`, false},
		{"different-key", tenants[0], `2`, false},
		{"typed-string-key", tenants[0], `"1"`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := store.AcceptCommitOperation(ctx, src.AccountID, src.ID, uuid.NewString(), "order.created", inv.Payload, inv, &api.CommitRouting{Version: 2, PlatformTenantID: tc.tenant, Key: json.RawMessage(tc.key)})
			if err != nil {
				t.Fatal(err)
			}
			op, err := store.ExclusiveOperationByID(ctx, src.AccountID, r.OperationID)
			if err != nil || (op.KeyID == original.KeyID) != tc.sameLane || op.PlatformTenantID != tc.tenant {
				t.Fatalf("lane isolation: %+v %v", op, err)
			}
		})
	}
	// A second source shares a business lane, but cannot collide on event UUID.
	second := src
	second.Name = "second-source"
	second.ID = ""
	second, err = store.CreateCommitSource(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	r, err := store.AcceptCommitOperation(ctx, src.AccountID, second.ID, event, "order.created", inv.Payload, inv, route)
	if err != nil || r.OperationID == first.OperationID {
		t.Fatalf("source identity collision: %+v %v", r, err)
	}
	op, err := store.ExclusiveOperationByID(ctx, src.AccountID, r.OperationID)
	if err != nil || op.KeyID != original.KeyID {
		t.Fatalf("sources did not share business lane: %+v %v", op, err)
	}
	for _, changed := range []*api.CommitRouting{
		{Version: 2, PlatformTenantID: tenants[1], Key: json.RawMessage(`1`)},
		{Version: 2, PlatformTenantID: tenants[0], Key: json.RawMessage(`2`)},
		nil,
	} {
		if _, err := store.AcceptCommitOperation(ctx, src.AccountID, src.ID, event, "order.created", inv.Payload, state.Invocation{}, changed); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("routing identity changed: %v", err)
		}
	}
	if _, err := store.SetCommitSourceEnabled(ctx, src.AccountID, src.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetPlatformTenantStatus(ctx, src.AccountID, tenants[0], state.PlatformTenantSuspended); err != nil {
		t.Fatal(err)
	}
	// Normalized routing is retained across migration replay and lifecycle changes.
	if _, err := pool.Exec(ctx, `DELETE FROM goose_db_version WHERE version_id=20261003113000001`); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	canonical := &api.CommitRouting{Version: 2, PlatformTenantID: strings.ToUpper(tenants[0]), Key: json.RawMessage(`1e0`)}
	replay, err := store.AcceptCommitOperation(ctx, src.AccountID, src.ID, event, "order.created", inv.Payload, state.Invocation{}, canonical)
	if err != nil || replay.ID != first.ID || replay.OperationID != first.OperationID {
		t.Fatalf("paused suspended replay: %+v %v", replay, err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM commit_receipts WHERE source_id=$1::uuid AND event_id=$2::uuid`, src.ID, event).Scan(&count); err != nil || count != 1 {
		t.Fatalf("receipt count=%d %v", count, err)
	}
}

func TestPgCommitCustomerRoutingRejectsUnauthorizedScope(t *testing.T) {
	store, pool, src, inv, tenants := routedCommitFixture(t)
	ctx := t.Context()
	unlinked, _, err := store.CreatePlatformTenant(ctx, src.AccountID, "unlinked", "unlinked", 250)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := store.CreateAccount(ctx, "foreign-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	foreignTenant, _, err := store.CreatePlatformTenant(ctx, foreign.ID, "foreign", "foreign", 250)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetPlatformTenantStatus(ctx, src.AccountID, tenants[1], state.PlatformTenantSuspended); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		route *api.CommitRouting
		want  error
	}{
		{"missing-routing", nil, state.ErrInvalidArgument},
		{"missing-customer", &api.CommitRouting{Version: 2, Key: json.RawMessage(`"order"`)}, state.ErrInvalidArgument},
		{"foreign-customer", &api.CommitRouting{Version: 2, PlatformTenantID: foreignTenant.ID, Key: json.RawMessage(`"order"`)}, state.ErrNotFound},
		{"unlinked-customer", &api.CommitRouting{Version: 2, PlatformTenantID: unlinked.ID, Key: json.RawMessage(`"order"`)}, state.ErrNotFound},
		{"suspended-customer", &api.CommitRouting{Version: 2, PlatformTenantID: tenants[1], Key: json.RawMessage(`"order"`)}, state.ErrPlatformTenantSuspended},
		{"invalid-key", &api.CommitRouting{Version: 2, PlatformTenantID: tenants[0], Key: json.RawMessage(`{}`)}, state.ErrInvalidArgument},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := store.AcceptCommitOperation(ctx, src.AccountID, src.ID, uuid.NewString(), "order.created", inv.Payload, inv, tc.route)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
	if _, err := pool.Exec(ctx, `UPDATE tenant_surfaces SET status='suspended' WHERE platform_tenant_id=$1::uuid`, tenants[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AcceptCommitOperation(ctx, src.AccountID, src.ID, uuid.NewString(), "order.created", inv.Payload, inv, &api.CommitRouting{Version: 2, PlatformTenantID: tenants[0], Key: json.RawMessage(`1`)}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("inactive app link: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM exclusive_work_operations`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rejected admission leaked owners: %d %v", count, err)
	}
	changed := src
	changed.AllowTenantSelection = false
	changed.OperationPolicy = "orders"
	if _, err := store.CreateCommitSource(ctx, changed); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("changed source grant: %v", err)
	}
	policy := exclusivework.Policy{Name: "customer-orders", Scope: "account", Contention: "queue", MemberAppIDs: []string{src.AppID}, LeaseSeconds: 15, MaxAttemptSeconds: 60}
	if _, err := store.UpsertExclusiveWorkPolicy(ctx, src.AccountID, policy); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("enabled source policy authority changed: %v", err)
	}
}

func TestPgCommitRoutingVersionCompatibility(t *testing.T) {
	store, _, legacy, inv := commitManagedFixture(t)
	ctx := t.Context()
	route := &api.CommitRouting{Version: 2, Key: json.RawMessage(`"order-1"`)}
	if _, err := store.AcceptCommitOperation(ctx, legacy.AccountID, legacy.ID, uuid.NewString(), "order.created", inv.Payload, inv, route); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("legacy accepted routing: %v", err)
	}
	modern := legacy
	modern.ID = ""
	modern.Name = "modern"
	modern.ContractVersion = 2
	modern, err := store.CreateCommitSource(ctx, modern)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AcceptCommitOperation(ctx, modern.AccountID, modern.ID, uuid.NewString(), "order.created", inv.Payload, inv); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("v2 accepted missing routing: %v", err)
	}
	if _, err := store.AcceptCommitOperation(ctx, modern.AccountID, modern.ID, uuid.NewString(), "order.created", inv.Payload, inv, route); err != nil {
		t.Fatal(err)
	}
	changed := legacy
	changed.ContractVersion = 2
	if _, err := store.CreateCommitSource(ctx, changed); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("source version changed: %v", err)
	}
	for _, src := range []state.CommitSource{
		{AccountID: legacy.AccountID, AppID: legacy.AppID, Name: "bad-version", OperationPolicy: "orders", ContractVersion: 3},
		{AccountID: legacy.AccountID, AppID: legacy.AppID, Name: "bad-grant", OperationPolicy: "orders", ContractVersion: 1, AllowTenantSelection: true},
		{AccountID: legacy.AccountID, AppID: legacy.AppID, Name: "bad-scope", OperationPolicy: "orders", ContractVersion: 2, AllowTenantSelection: true},
	} {
		if _, err := store.CreateCommitSource(ctx, src); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("invalid contract %+v: %v", src, err)
		}
	}
}

// Generate numeric keys to verify replay normalization and customer lane
// separation together, rather than depending on one chosen example key.
func TestPgCommitCustomerRoutingProperties(t *testing.T) {
	store, _, src, inv, tenants := routedCommitFixture(t)
	ctx := t.Context()
	property := func(value uint16) bool {
		event := uuid.NewString()
		key := json.RawMessage(fmt.Sprintf("%d", value))
		route := &api.CommitRouting{Version: 2, PlatformTenantID: tenants[0], Key: key}
		first, err := store.AcceptCommitOperation(ctx, src.AccountID, src.ID, event, "order.created", inv.Payload, inv, route)
		if err != nil {
			t.Log(err)
			return false
		}
		equivalent := &api.CommitRouting{Version: 2, PlatformTenantID: tenants[0], Key: json.RawMessage(fmt.Sprintf("%d.0", value))}
		replay, err := store.AcceptCommitOperation(ctx, src.AccountID, src.ID, event, "order.created", inv.Payload, state.Invocation{}, equivalent)
		if err != nil || replay.ID != first.ID || replay.OperationID != first.OperationID {
			t.Logf("replay=%+v err=%v", replay, err)
			return false
		}
		other, err := store.AcceptCommitOperation(ctx, src.AccountID, src.ID, uuid.NewString(), "order.created", inv.Payload, inv, &api.CommitRouting{Version: 2, PlatformTenantID: tenants[1], Key: key})
		if err != nil {
			t.Log(err)
			return false
		}
		a, err := store.ExclusiveOperationByID(ctx, src.AccountID, first.OperationID)
		if err != nil {
			t.Log(err)
			return false
		}
		b, err := store.ExclusiveOperationByID(ctx, src.AccountID, other.OperationID)
		if err != nil {
			t.Log(err)
			return false
		}
		return a.KeyID != b.KeyID && a.PlatformTenantID == tenants[0] && b.PlatformTenantID == tenants[1]
	}
	if err := quick.Check(property, &quick.Config{MaxCount: 12, Rand: rand.New(rand.NewSource(459))}); err != nil {
		t.Fatal(err)
	}
}
