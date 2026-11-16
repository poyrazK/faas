package apidserver

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

func enableProtectionBaseline(e testEnv) {
	e.s.seedPlatformFlag(platformflags.CapabilityFlagKey(protectionBaselineCapability), platformflags.FromAccountList(true, true, nil))
}

func getProtection(t *testing.T, e testEnv, slug string) api.AppProtectionResponse {
	t.Helper()
	rec := e.do(t, http.MethodGet, "/v1/apps/"+slug+"/protection", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET protection = %d %s", rec.Code, rec.Body)
	}
	var out api.AppProtectionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestGetAppProtectionReportsBaselineItems pins ADR-20261116120000 slice 1:
// an app created while the flag is on is enrolled and reports every
// baseline item pending; recorded objects turn active, and all of them
// together make the app protected.
func TestGetAppProtectionReportsBaselineItems(t *testing.T) {
	e := setup(t, api.PlanHobby)
	enableProtectionBaseline(e)
	app := createApp(t, e, "shop")

	got := getProtection(t, e, "shop")
	if got.State != api.AppProtectionStateOn || got.Protected || len(got.Items) != len(api.ProtectionBaselineItems) {
		t.Fatalf("new app = %+v; want enrolled, unprotected, every item", got)
	}
	for i, item := range got.Items {
		want := api.ProtectionBaselineItems[i]
		if item.Item != want.Item || item.Kind != want.Kind || item.Name != want.Name || item.State != api.AppProtectionItemPending || item.ObjectID != "" {
			t.Fatalf("items[%d] = %+v; want pending %+v", i, item, want)
		}
	}

	if err := e.store.RecordAppProtectionObject(context.Background(), state.AppProtectionObject{
		AppID: app.ID, Item: state.AppProtectionHealthCheck, ObjectID: "check-1",
	}); err != nil {
		t.Fatal(err)
	}
	got = getProtection(t, e, "shop")
	if got.Items[2].Item != "health_check" || got.Items[2].State != api.AppProtectionItemActive || got.Items[2].ObjectID != "check-1" || got.Protected {
		t.Fatalf("after one object = %+v", got)
	}

	for _, item := range state.AppProtectionItems {
		if err := e.store.RecordAppProtectionObject(context.Background(), state.AppProtectionObject{
			AppID: app.ID, Item: item, ObjectID: "id-" + string(item),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if got = getProtection(t, e, "shop"); !got.Protected {
		t.Fatalf("all objects = %+v; want protected", got)
	}
	if _, err := e.store.SetAppProtection(context.Background(), app.ID, e.acct.ID, state.AppProtectionOff); err != nil {
		t.Fatal(err)
	}
	if got = getProtection(t, e, "shop"); got.State != api.AppProtectionStateOff || got.Protected {
		t.Fatalf("turned off = %+v; want off, unprotected", got)
	}
}

// TestAppProtectionNotEnrolledWithoutFlag: an app created while the flag is
// off is never enrolled, even after the flag turns on (no backfill). Hiding
// the route while the flag is off is capgate's job and tested there.
func TestAppProtectionNotEnrolledWithoutFlag(t *testing.T) {
	e := setup(t, api.PlanHobby)
	createApp(t, e, "old")
	enableProtectionBaseline(e)
	got := getProtection(t, e, "old")
	if got.State != api.AppProtectionStateNone || got.Protected || got.Items == nil || len(got.Items) != 0 {
		t.Fatalf("pre-flag app = %+v; want none with an empty item list", got)
	}
	if rec := e.do(t, http.MethodGet, "/v1/apps/missing/protection", nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown app = %d; want 404", rec.Code)
	}
}
