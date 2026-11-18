package apidserver_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// A source deploy's manifest protection key turns protection on or off
// (ADR-20261116120000 §5); an absent key changes nothing, and with the
// capability off the key is ignored.
func TestSourceRefManifestProtection(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	srv, store, acct := *e.S(), *e.Store(), *e.Acct()
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "manifest-protection", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	apply := func(want string) {
		t.Helper()
		m := &gregalemanifest.Manifest{Protection: want}
		if _, problem := srv.ApplySourceRefManifest(t.Context(), acct, app, m, state.DefaultEnvScope, false); problem != nil {
			t.Fatalf("apply protection %q: %+v", want, problem)
		}
	}
	stateOf := func() (string, bool) {
		t.Helper()
		p, found, err := store.GetAppProtection(t.Context(), app.ID)
		if err != nil {
			t.Fatal(err)
		}
		return p.State, found
	}

	apply("off")
	if _, found := stateOf(); found {
		t.Fatal("protection key applied with the capability off")
	}

	srv.EnablePlatformFlagForAll(platformflags.CapabilityFlagKey("protection-baseline"))
	apply("off")
	if got, _ := stateOf(); got != api.AppProtectionStateOff {
		t.Fatalf("after protection: off, state = %q", got)
	}
	apply("")
	if got, _ := stateOf(); got != api.AppProtectionStateOff {
		t.Fatalf("an absent key changed the state to %q", got)
	}
	apply("on")
	if got, _ := stateOf(); got != api.AppProtectionStateOn {
		t.Fatalf("after protection: on, state = %q", got)
	}
}
