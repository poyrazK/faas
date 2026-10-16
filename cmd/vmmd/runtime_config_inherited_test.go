// adr: 1136
package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// The live runtime configuration serves shared project values with the app's
// own, as at boot, and a change to either moves the revision.
func TestRuntimeConfigServesInheritedProjectValues(t *testing.T) {
	snapshot := runtimeEnvTestSnapshot("project-dep", "production", "app-own")
	snapshot.Inherited = []state.AppEnv{{AccountID: "acct-1", AppID: "app-1", Scope: "production", Key: "SHARED", Value: "one"}}
	read := func() runtimeConfigResponse {
		t.Helper()
		response, err := loadRuntimeConfig(t.Context(), runtimeConfigStoreStub{snapshot: snapshot}, snapshot.DeploymentID, "app-1", "acct-1")
		if err != nil {
			t.Fatalf("loadRuntimeConfig: %v", err)
		}
		return response
	}
	first := read()
	if len(first.Env) != 2 || first.Env["MODE"] != "app-own" || first.Env["SHARED"] != "one" {
		t.Fatalf("env = %v, want the app's MODE and the inherited SHARED", first.Env)
	}
	snapshot.Inherited[0].Value = "two"
	if second := read(); second.Revision == first.Revision || second.Env["SHARED"] != "two" {
		t.Fatalf("an inherited change kept revision %s / env %v", second.Revision, second.Env)
	}
	// The store never returns an inherited key the app also sets; if it did,
	// the duplicate is refused rather than silently resolved.
	snapshot.Inherited[0].Key = "MODE"
	if _, err := loadRuntimeConfig(t.Context(), runtimeConfigStoreStub{snapshot: snapshot}, snapshot.DeploymentID, "app-1", "acct-1"); err == nil {
		t.Fatal("a duplicate inherited key was accepted")
	}
}
