package apidserver

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestSetAppProtectionOffAndOn pins ADR-20261116120000 §5: off deletes the
// baseline objects the customer has not changed and keeps a changed one,
// what it still watches, and the shared email channel; the reconciler
// creates nothing while protection is off; on recreates what off removed;
// an app created before protection existed can opt in.
func TestSetAppProtectionOffAndOn(t *testing.T) {
	e, app := protectionEnv(t, api.PlanHobby, true)
	ctx := context.Background()
	goLive(t, e, app)
	reconcileNow(t, e, app)
	before := protectionItems(t, e, "shop")
	threshold := 9.0
	if _, err := e.store.UpdateAlertRule(ctx, before["latency_burn_alert"].ObjectID, state.UpdateAlertRuleParams{Threshold: &threshold}); err != nil {
		t.Fatal(err)
	}

	var off api.AppProtectionResponse
	rec := e.do(t, http.MethodPut, "/v1/apps/shop/protection", api.SetAppProtectionRequest{State: "off"}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT off = %d %s", rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &off); err != nil {
		t.Fatal(err)
	}
	got := map[string]api.AppProtectionItemStatus{}
	for _, item := range off.Items {
		got[item.Item] = item
	}
	if off.State != api.AppProtectionStateOff || off.Protected {
		t.Fatalf("off = %+v", off)
	}
	for _, name := range []string{"availability_slo", "health_check", "availability_burn_alert", "health_alert", "availability_gate"} {
		if got[name].State != api.AppProtectionItemPending {
			t.Fatalf("after off, %s = %+v; want removed and forgotten (pending)", name, got[name])
		}
	}
	for _, name := range []string{"latency_burn_alert", "latency_slo", "email_channel"} {
		if got[name].State != api.AppProtectionItemActive {
			t.Fatalf("after off, %s = %+v; want kept", name, got[name])
		}
	}
	if slos, _ := e.store.ListSLOs(ctx, app.ID); len(slos) != 1 || slos[0].Name != api.ProtectionBaselineLatencySLOName {
		t.Fatalf("SLOs after off = %+v; want only the latency SLO", slos)
	}
	if gates, _ := e.store.ListReleaseGates(ctx, app.ID); len(gates) != 0 {
		t.Fatalf("gates after off = %+v", gates)
	}
	reconcileNow(t, e, app)
	if slos, _ := e.store.ListSLOs(ctx, app.ID); len(slos) != 1 {
		t.Fatal("the reconciler recreated objects while protection is off")
	}

	if rec := e.do(t, http.MethodPut, "/v1/apps/shop/protection", api.SetAppProtectionRequest{State: "on"}, nil); rec.Code != http.StatusOK {
		t.Fatalf("PUT on = %d %s", rec.Code, rec.Body)
	}
	reconcileNow(t, e, app)
	if after := getProtection(t, e, "shop"); !after.Protected {
		t.Fatalf("after on = %+v; want every item active again", after)
	}

	old := setup(t, api.PlanHobby)
	createApp(t, old, "old")
	enableProtectionBaseline(old)
	if got := getProtection(t, old, "old"); got.State != api.AppProtectionStateNone {
		t.Fatalf("pre-flag app = %+v", got)
	}
	if rec := old.do(t, http.MethodPut, "/v1/apps/old/protection", api.SetAppProtectionRequest{State: "on"}, nil); rec.Code != http.StatusOK {
		t.Fatalf("opt in = %d %s", rec.Code, rec.Body)
	}
	if got := getProtection(t, old, "old"); got.State != api.AppProtectionStateOn {
		t.Fatalf("after opt in = %+v; want on", got)
	}
	if rec := old.do(t, http.MethodPut, "/v1/apps/old/protection", api.SetAppProtectionRequest{State: "maybe"}, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad state = %d; want 400", rec.Code)
	}
}

// A source deploy's manifest protection key turns protection on or off
// (ADR-20261116120000 §5); an absent key or the current state changes
// nothing.
func TestSourceRefManifestProtection(t *testing.T) {
	e, app := protectionEnv(t, api.PlanHobby, true)
	ctx := context.Background()
	apply := func(want string) {
		t.Helper()
		m := &gregalemanifest.Manifest{Protection: want}
		if _, problem := e.s.applySourceRefManifest(ctx, e.acct, app, m, state.DefaultEnvScope, false); problem != nil {
			t.Fatalf("apply protection %q: %+v", want, problem)
		}
	}
	stateOf := func() string {
		t.Helper()
		p, found, err := e.store.GetAppProtection(ctx, app.ID)
		if err != nil || !found {
			t.Fatalf("GetAppProtection = %+v, %v, %v", p, found, err)
		}
		return p.State
	}
	apply("off")
	if got := stateOf(); got != api.AppProtectionStateOff {
		t.Fatalf("after protection: off, state = %q", got)
	}
	apply("")
	if got := stateOf(); got != api.AppProtectionStateOff {
		t.Fatalf("an absent key changed the state to %q", got)
	}
	apply("on")
	if got := stateOf(); got != api.AppProtectionStateOn {
		t.Fatalf("after protection: on, state = %q", got)
	}
}
