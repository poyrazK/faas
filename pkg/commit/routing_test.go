// adr: 487
package commit

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestNormalizeRouting(t *testing.T) {
	for _, tc := range []struct {
		name  string
		route *api.CommitRouting
		valid bool
	}{
		{"legacy", nil, true},
		{"string", &api.CommitRouting{Version: 2, Key: json.RawMessage(`"order-1"`)}, true},
		{"number", &api.CommitRouting{Version: 2, Key: json.RawMessage(`1e0`)}, true},
		{"boolean", &api.CommitRouting{Version: 2, Key: json.RawMessage(`false`)}, true},
		{"version", &api.CommitRouting{Version: 1, Key: json.RawMessage(`1`)}, false},
		{"missing", &api.CommitRouting{Version: 2}, false},
		{"null", &api.CommitRouting{Version: 2, Key: json.RawMessage(`null`)}, false},
		{"object", &api.CommitRouting{Version: 2, Key: json.RawMessage(`{}`)}, false},
		{"array", &api.CommitRouting{Version: 2, Key: json.RawMessage(`[]`)}, false},
		{"empty", &api.CommitRouting{Version: 2, Key: json.RawMessage(`""`)}, false},
		{"oversize", &api.CommitRouting{Version: 2, Key: json.RawMessage(`"` + strings.Repeat("a", 255) + `"`)}, false},
		{"invalid-customer", &api.CommitRouting{Version: 2, PlatformTenantID: "not-uuid", Key: json.RawMessage(`1`)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NormalizeRouting(tc.route)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
	tenant := uuid.NewString()
	original := &api.CommitRouting{Version: 2, PlatformTenantID: strings.ToUpper(tenant), Key: json.RawMessage(`"order"`)}
	raw, err := NormalizeRouting(original)
	if err != nil {
		t.Fatal(err)
	}
	var normalized api.CommitRouting
	if err := json.Unmarshal(raw, &normalized); err != nil || normalized.PlatformTenantID != tenant || original.PlatformTenantID != strings.ToUpper(tenant) {
		t.Fatalf("normalization mutates caller: %+v %+v %v", original, normalized, err)
	}
}

func TestPostgresCommitRoutingUpgradeAndLegacyRelay(t *testing.T) {
	pool := pgtest.OpenDatabase(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, Schema); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE public.gregale_outbox DROP COLUMN routing`); err != nil {
		t.Fatal(err)
	}
	if err := QualifySchemaVersion(ctx, pool, 1); err != nil {
		t.Fatal(err)
	}
	if err := QualifySchemaVersion(ctx, pool, 2); err == nil {
		t.Fatal("v2 qualified without routing column")
	}
	legacy := Event{ID: uuid.NewString(), Type: "legacy", Data: json.RawMessage(`{}`)}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := Insert(ctx, tx, legacy); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	relay := Relay{Pool: pool, Acceptor: acceptFunc(func(_ context.Context, e Event) (Receipt, error) {
		if e.Routing != nil {
			t.Fatal("legacy gained routing")
		}
		return Receipt{ID: uuid.NewString(), OperationID: uuid.NewString()}, nil
	})}
	if n, err := relay.Tick(ctx); err != nil || n != 1 {
		t.Fatalf("old-schema relay: %d %v", n, err)
	}
	for range 2 {
		if _, err := pool.Exec(ctx, RoutingUpgradeSchema); err != nil {
			t.Fatal(err)
		}
	}
	if err := QualifySchemaVersion(ctx, pool, 2); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{`{}`, `{"version":1,"key":1}`, `{"version":2,"key":null}`, `{"version":2,"key":1,"platform_tenant_id":3}`} {
		if _, err := pool.Exec(ctx, `INSERT INTO public.gregale_outbox(event_id,event_type,payload,routing) VALUES($1,'invalid','{}',$2::jsonb)`, uuid.NewString(), invalid); err == nil {
			t.Fatalf("invalid routing passed CHECK: %s", invalid)
		}
	}
	route := &api.CommitRouting{Version: 2, PlatformTenantID: uuid.NewString(), Key: json.RawMessage(`"order-1"`)}
	event := Event{ID: uuid.NewString(), Type: "customer-order", Data: json.RawMessage(`{}`), Routing: route}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := Insert(ctx, tx, event); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	relay.Acceptor = acceptFunc(func(_ context.Context, e Event) (Receipt, error) {
		if e.Routing == nil || e.Routing.PlatformTenantID != route.PlatformTenantID || string(e.Routing.Key) != string(route.Key) {
			t.Fatalf("lost routing: %+v", e)
		}
		return Receipt{ID: uuid.NewString(), OperationID: uuid.NewString()}, nil
	})
	if n, err := relay.Tick(ctx); err != nil || n != 1 {
		t.Fatalf("routed relay: %d %v", n, err)
	}
}

func TestPostgresCommitMalformedRoutingDoesNotBlockNeighbors(t *testing.T) {
	pool := pgtest.OpenDatabase(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, Schema); err != nil {
		t.Fatal(err)
	}
	// Simulate drift in a customer-owned schema: relay validation stays strict.
	if _, err := pool.Exec(ctx, `ALTER TABLE public.gregale_outbox DROP CONSTRAINT gregale_outbox_routing_check`); err != nil {
		t.Fatal(err)
	}
	bad, good := uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO public.gregale_outbox(event_id,event_type,payload,routing) VALUES($1,'bad','{}','{"version":2,"key":1,"app_id":"wrong"}'),($2,'good','{}','{"version":2,"key":1}')`, bad, good); err != nil {
		t.Fatal(err)
	}
	calls := 0
	relay := Relay{Pool: pool, Acceptor: acceptFunc(func(_ context.Context, e Event) (Receipt, error) {
		calls++
		if e.ID != good {
			t.Fatal("malformed route escaped validation")
		}
		return Receipt{ID: uuid.NewString(), OperationID: uuid.NewString()}, nil
	})}
	if n, err := relay.Tick(ctx); n != 1 || err == nil || calls != 1 {
		t.Fatalf("neighbor progress: accepted=%d calls=%d err=%v", n, calls, err)
	}
	var code string
	if err := pool.QueryRow(ctx, `SELECT blocked_code FROM public.gregale_outbox WHERE event_id=$1::uuid`, bad).Scan(&code); err != nil || code != "invalid_routing" {
		t.Fatalf("bad route not blocked: %s %v", code, err)
	}
}
