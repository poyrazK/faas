// adr: 531
package gateway_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/gateway"
)

func TestPGBackendPreparingEnvironmentDoesNotPoisonProductionAppCache(t *testing.T) {
	const stageHost, productionHost = "staging.example.test", "production.example.test"
	r := &fakeRouter{byID: map[string]gateway.App{
		stageHost:      {ID: "app-1", EnvironmentNotReady: true},
		productionHost: {ID: "app-1"},
	}}
	b := gateway.NewPGBackend(r, gateway.NewFakeScheduler(""), nil)
	for i := 0; i < 2; i++ {
		if app, ok := b.Lookup(context.Background(), stageHost); !ok || !app.EnvironmentNotReady {
			t.Fatalf("stage lookup = %+v, ok=%v", app, ok)
		}
		if app, ok := b.Lookup(context.Background(), productionHost); !ok || app.EnvironmentNotReady {
			t.Fatalf("production lookup = %+v, ok=%v", app, ok)
		}
	}
	if calls := r.resolveCalls(); calls != 3 {
		t.Fatalf("lookups = %d, want two fresh stage reads and one cached production read", calls)
	}
}
