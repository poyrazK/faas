package apidserver_test

import (
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

func edgeCheckoutConfig(rewrite string, killed bool) flags.Config {
	return flags.Config{Flags: []flags.Flag{{
		Key: "pricing", Type: "variant", Enabled: true, Killed: killed, Default: "a",
		Variants: []flags.FlagVariant{{Key: "a", Weight: 5000}, {Key: "b", Weight: 5000}},
		Edge:     &flags.Edge{Routes: []flags.EdgeRoute{{Path: "/pricing", Variants: map[string]flags.EdgeAction{"b": {Rewrite: rewrite}}}}},
	}}}
}

// ADR-20261124212701: edge routes publish only while edge_flags is on for
// the account; once it is off, edits that leave the routes alone (a kill)
// still publish, and changed routes are refused.
func TestFeatureFlagEdgeRoutesFollowThePlatformFlag(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	(*e.S()).EnablePlatformFlagForAll(platformflags.CustomerFlags)
	if _, err := (*e.Store()).CreateProject(t.Context(), state.Project{AccountID: (*e.Acct()).ID, Slug: "site", ProductionBranch: "main", ScanSource: state.ProjectScanSourceCompose}); err != nil {
		t.Fatal(err)
	}
	path := "/v1/projects/site/environments/production/flags"
	put := func(version int64, cfg flags.Config) int {
		t.Helper()
		res := e.Do(t, http.MethodPut, path, apidserver.UpdateFeatureFlagsRequest{ExpectedVersion: &version, Config: cfg}, nil)
		return res.Code
	}
	if code := put(0, edgeCheckoutConfig("/pricing-b", false)); code != http.StatusForbidden {
		t.Fatalf("edge routes without edge_flags = %d; want 403", code)
	}
	(*e.S()).EnablePlatformFlagForAll(platformflags.EdgeFlags)
	if code := put(0, edgeCheckoutConfig("/pricing-b", false)); code != http.StatusOK {
		t.Fatalf("edge routes with edge_flags = %d; want 200", code)
	}
	(*e.S()).SeedPlatformFlag(platformflags.EdgeFlags, platformflags.FromAccountList(false, false, nil))
	if code := put(1, edgeCheckoutConfig("/pricing-b", true)); code != http.StatusOK {
		t.Fatalf("a kill that keeps the routes after edge_flags is off = %d; want 200", code)
	}
	if code := put(2, edgeCheckoutConfig("/pricing-c", true)); code != http.StatusForbidden {
		t.Fatalf("changed routes after edge_flags is off = %d; want 403", code)
	}
	if code := put(2, flags.Config{Flags: []flags.Flag{{Key: "pricing", Type: "variant", Enabled: true, Default: "a",
		Variants: []flags.FlagVariant{{Key: "a", Weight: 5000}, {Key: "b", Weight: 5000}}}}}); code != http.StatusOK {
		t.Fatalf("removing the routes after edge_flags is off = %d; want 200", code)
	}
}

// SDKs never receive edge routes: the gateway applies them.
func TestRuntimeBundleLeavesEdgeRoutesOut(t *testing.T) {
	b := flags.Bundle{Version: 3, Config: edgeCheckoutConfig("/pricing-b", false)}
	got := apidserver.RuntimeBundleForSDK(b, api.FlagSDKKillCapability)
	if flags.HasEdge(got.Config) || len(got.Flags) != 1 || !flags.HasEdge(b.Config) {
		t.Fatalf("runtime bundle = %+v; want the flag without its edge routes, the stored bundle untouched", got.Flags)
	}
}
