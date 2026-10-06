//go:build !no_pg

package state_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPgRouteCustomersHistoricalIdentityWeightedCountsAndIsolation(t *testing.T) {
	f := newTelemetryFixture(t)
	account, app := uuid.UUID(f.account.Bytes).String(), uuid.UUID(f.app.Bytes).String()
	oldTenant, _, err := f.s.CreatePlatformTenant(f.ctx, account, "old", "Old tenant", 10)
	if err != nil {
		t.Fatal(err)
	}
	newTenant, _, err := f.s.CreatePlatformTenant(f.ctx, account, "new", "New tenant", 10)
	if err != nil {
		t.Fatal(err)
	}
	a, err := f.s.CreateAPIConsumer(f.ctx, account, app, "a", "A")
	if err != nil {
		t.Fatal(err)
	}
	b, err := f.s.CreateAPIConsumer(f.ctx, account, app, "b", "B")
	if err != nil {
		t.Fatal(err)
	}
	// Today's link and revocation must not rewrite yesterday's attribution.
	if _, err := f.s.LinkPlatformTenantConsumer(f.ctx, account, newTenant.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.RevokeAPIConsumer(f.ctx, account, b.ID); err != nil {
		t.Fatal(err)
	}
	foreignAccount, foreignApp, foreignDep := seedLiveDeploy(t, f.s, f.ctx, "foreign", "foreign")
	foreign, err := f.s.CreateAPIConsumer(f.ctx, foreignAccount, foreignApp, "foreign", "Foreign")
	if err != nil {
		t.Fatal(err)
	}
	other, err := f.s.CreateDeployment(f.ctx, state.Deployment{AppID: app, Kind: state.DeploymentKindImage, ImageDigest: "sha256:other"})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Hour)
	row := func(count int32, consumer, tenant string, minute int) telemetryRow {
		return telemetryRow{at: base.Add(time.Duration(minute) * time.Minute), route: "GET /orders/{id}", method: "GET", status: 200, count: count, consumer: consumer, tenant: tenant}
	}
	f.insert(t, row(7, a.ID, oldTenant.ID, 1), row(3, a.ID, "", 2), row(2, b.ID, oldTenant.ID, 3), row(4, "", oldTenant.ID, 4), row(5, "", "", 8), row(6, foreign.ID, "", 9), row(99, a.ID, oldTenant.ID, -1), row(99, a.ID, oldTenant.ID, 60))
	differentDep := row(99, a.ID, oldTenant.ID, 5)
	differentDep.deployment = other.ID
	f.insert(t, differentDep, telemetryRow{at: base.Add(5 * time.Minute), route: "POST /orders/{id}", method: "POST", status: 200, count: 8, consumer: a.ID})
	foreignFixture := telemetryFixture{s: f.s, ctx: f.ctx, account: mustPgUUID(t, foreignAccount), app: mustPgUUID(t, foreignApp), dep: mustPgUUID(t, foreignDep)}
	foreignFixture.insert(t, row(999, foreign.ID, "", 5))
	query := sqlc.RequestTelemetryRouteCustomersParams{AppID: f.app, AccountID: f.account, DeploymentID: f.dep, SinceAt: ts(base), UntilAt: ts(base.Add(time.Hour)), RouteLimit: 1, CustomerLimit: 1}
	got, err := f.s.RequestTelemetryRouteCustomers(f.ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("rows: %+v", got)
	}
	g := got[0]
	if g.Requests != 27 || g.IdentifiedRequests != 16 || g.AnonymousRequests != 5 || g.UnresolvedIdentityRequests != 6 || g.ConsumerCount != 2 || g.PlatformTenantCount != 1 || g.MatchedRoutes != 2 || g.CustomerGroups != 4 || g.OtherCustomerRequests != 9 {
		t.Fatalf("weighted bounded exposure: %+v", g)
	}
	if g.ConsumerID != a.ID || g.PlatformTenantID != oldTenant.ID || g.CustomerRequests != 7 || !g.CustomerLastObservedAt.Time.Equal(base.Add(time.Minute)) || !g.LastObservedAt.Time.Equal(base.Add(9*time.Minute)) {
		t.Fatalf("historical identity: %+v", g)
	}
	query.CustomerLimit, query.RouteLimit = 20, 200
	got, err = f.s.RequestTelemetryRouteCustomers(f.ctx, query)
	if err != nil || len(got) != 5 {
		t.Fatalf("full observations %+v: %v", got, err)
	}
	foundBeforeLink, foundRevoked := false, false
	for _, g := range got {
		if g.Route == "GET /orders/{id}" && g.ConsumerID == a.ID && g.PlatformTenantID == "" {
			foundBeforeLink = true
		}
		if g.ConsumerID == b.ID {
			foundRevoked = true
		}
		if g.PlatformTenantID == newTenant.ID || g.ConsumerID == foreign.ID {
			t.Fatalf("inferred or foreign identity: %+v", g)
		}
	}
	if !foundBeforeLink || !foundRevoked {
		t.Fatalf("historical groups omitted: %+v", got)
	}
	query.AccountID = mustPgUUID(t, foreignAccount)
	got, err = f.s.RequestTelemetryRouteCustomers(f.ctx, query)
	if err != nil || len(got) != 0 {
		t.Fatalf("cross-account read: %+v %v", got, err)
	}
}

func TestPgRouteCustomersDetailCapDoesNotUndercountConsumers(t *testing.T) {
	f := newTelemetryFixture(t)
	base := time.Now().UTC().Truncate(time.Hour)
	for i := 0; i < 23; i++ {
		c, err := f.s.CreateAPIConsumer(f.ctx, uuid.UUID(f.account.Bytes).String(), uuid.UUID(f.app.Bytes).String(), fmt.Sprintf("c-%02d", i), "Customer")
		if err != nil {
			t.Fatal(err)
		}
		f.insert(t, telemetryRow{at: base, route: "GET /many", method: "GET", status: 200, count: 2, consumer: c.ID})
	}
	f.insert(t, telemetryRow{at: base, route: "GET /anonymous", method: "GET", status: 200, count: 3})
	query := sqlc.RequestTelemetryRouteCustomersParams{AppID: f.app, AccountID: f.account, DeploymentID: f.dep, SinceAt: ts(base), UntilAt: ts(base.Add(time.Hour)), RouteLimit: 200, CustomerLimit: 20}
	got, err := f.s.RequestTelemetryRouteCustomers(f.ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 21 || got[0].Requests != 46 || got[0].ConsumerCount != 23 || got[0].CustomerGroups != 23 || got[0].OtherCustomerRequests != 6 {
		t.Fatalf("detail cap lost consumers: %+v", got)
	}
	for i := 1; i < 20; i++ {
		if got[i-1].ConsumerID >= got[i].ConsumerID {
			t.Fatal("unstable customer tie ordering")
		}
	}
	last := got[20]
	if last.AnonymousRequests != 3 || last.IdentifiedRequests != 0 || last.ConsumerID != "" || last.CustomerGroups != 0 {
		t.Fatalf("anonymous route: %+v", last)
	}
}
