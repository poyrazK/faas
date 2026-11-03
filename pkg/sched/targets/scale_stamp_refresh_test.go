// adr: 20261103094355 — a reused app list can carry an older scale-out stamp.

package targets

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// stampStore is a list whose scale-out stamps lag the rows, as schedd's app
// snapshot's may, with the rows' stamps readable one app at a time.
type stampStore struct {
	fakeStore
	rowStamps map[string]*time.Time
	reads     int
}

func (s *stampStore) AppScaleOutStamp(_ context.Context, appID string) (*time.Time, error) {
	s.reads++
	return s.rowStamps[appID], nil
}

// The list says the app never scaled out, so the cooldown looks clear; the
// row was stamped a second ago, which holds it. The trigger reads the row's
// stamp before admitting.
func TestTriggerReadsTheRowScaleOutStampBeforeAdmitting(t *testing.T) {
	stamp := time.Now().Add(-time.Second)
	store := &stampStore{rowStamps: map[string]*time.Time{"app1": &stamp}}
	store.apps = []state.App{{ID: "app1", MaxConcurrency: 5, ScalingPolicy: scaleUpPolicy(1.0, 60)}}
	ledger := &fakeLedger{conc: map[string]int{"app1": 2}}
	instats := &fakeInstats{byApp: map[string]int64{"app1": 10}} // hot
	engine := &fakeEngine{}
	tr := New(store, instats, engine, ledger, Options{Metrics: wire.NewOpsMetrics("schedd")})
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if store.reads != 1 || len(engine.admitCalls) != 0 {
		t.Fatalf("stamp reads %d, admissions %v; want the row's stamp read and the cooldown held", store.reads, engine.admitCalls)
	}

	old := time.Now().Add(-2 * time.Minute)
	store.rowStamps["app1"] = &old
	if err := tr.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(engine.admitCalls) == 0 {
		t.Fatal("no admission once the row's cooldown had passed")
	}
}
