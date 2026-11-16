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

// TestGetAppProtectionReportsBaselineItems pins ADR-20261116120000: an app
// created while the flag is on is enrolled and reports every baseline item
// pending; a skipped record reports its reason; turning protection off is
// reported.
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
		AppID: app.ID, Item: state.AppProtectionEmailChannel, Status: state.AppProtectionObjectSkipped, Reason: api.AppProtectionSkipEmailUnverified,
	}); err != nil {
		t.Fatal(err)
	}
	got = getProtection(t, e, "shop")
	if got.Items[3].Item != "email_channel" || got.Items[3].State != api.AppProtectionItemSkipped || got.Items[3].Reason != api.AppProtectionSkipEmailUnverified {
		t.Fatalf("skipped channel = %+v", got.Items[3])
	}

	if _, err := e.store.SetAppProtection(context.Background(), app.ID, e.acct.ID, state.AppProtectionOff); err != nil {
		t.Fatal(err)
	}
	if got = getProtection(t, e, "shop"); got.State != api.AppProtectionStateOff || got.Protected {
		t.Fatalf("turned off = %+v; want off, unprotected", got)
	}
}

// TestAppProtectionResponseProtected: an enrolled app is protected only when
// every item is active; a removed item, or protection off, is not
// protected.
func TestAppProtectionResponseProtected(t *testing.T) {
	on := state.AppProtection{State: state.AppProtectionOn}
	var all []state.AppProtectionObject
	for _, item := range state.AppProtectionItems {
		all = append(all, state.AppProtectionObject{Item: item, Status: state.AppProtectionObjectCreated, ObjectID: "id-" + string(item)})
	}
	if got := appProtectionResponse(on, true, all, nil); !got.Protected || got.Items[0].ObjectID != "id-availability_slo" {
		t.Fatalf("all active = %+v; want protected", got)
	}
	got := appProtectionResponse(on, true, all, map[state.AppProtectionItem]bool{state.AppProtectionHealthCheck: true})
	if got.Protected || got.Items[2].State != api.AppProtectionItemRemoved || got.Items[2].ObjectID != "id-health_check" {
		t.Fatalf("one removed = %+v; want unprotected, health check removed", got)
	}
	if got := appProtectionResponse(state.AppProtection{State: state.AppProtectionOff}, true, all, nil); got.Protected {
		t.Fatalf("off = %+v; want unprotected", got)
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
