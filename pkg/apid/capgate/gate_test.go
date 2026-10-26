package capgate

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/productcap"
)

const account = "11111111-1111-4111-8111-111111111111"

func testCatalog() productcap.Catalog {
	return productcap.Catalog{Version: 1, Capabilities: []productcap.Capability{
		{ID: "apps", Maturity: productcap.MaturityGA, Core: true, Routes: []string{"=/v1/apps/{}", "/v1/apps/{}/rename"}},
		{ID: "realtime", Maturity: productcap.MaturityPreview, Routes: []string{"/v1/apps/{}/realtime", "/v1/realtime"}},
		{ID: "route-insights", Maturity: productcap.MaturityInternal, Routes: []string{"/v1/apps/{}/routes"}},
		{ID: "source-deploy", Maturity: productcap.MaturityGA, Routes: []string{"/v1/deployments"}},
		{ID: "progressive-rollouts", Maturity: productcap.MaturityBeta, Routes: []string{"/v1/deployments/{}/traffic"}},
	}}
}

func TestCapabilityOwnsLongestEntry(t *testing.T) {
	g := New(testCatalog(), nil)
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"GET /v1/apps/{slug}", "apps", true},
		{"/v1/apps/my-api", "apps", true},
		{"POST /v1/apps/{slug}/rename", "apps", true},
		{"GET /v1/apps/{slug}/realtime/endpoints/{id}", "realtime", true},
		{"/v1/apps/my-api/realtime/endpoints/e1/connections", "realtime", true},
		{"GET /v1/realtime", "realtime", true},
		{"GET /v1/apps/{slug}/routes", "route-insights", true},
		{"GET /v1/deployments/{id}", "source-deploy", true},
		{"PATCH /v1/deployments/{id}/traffic", "progressive-rollouts", true},
		{"GET /v1/deployments/", "source-deploy", true},
		// =/v1/apps/{} owns only the exact path, so an unassigned child is unmapped.
		{"GET /v1/apps/{slug}/unassigned", "", false},
		{"GET /v1/admin/accounts", "", false},
		{"GET /healthz", "", false},
		{"GET /v1/nothing", "", false},
	}
	for _, tc := range tests {
		got, ok := g.Capability(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("Capability(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestTierDefaultsWithoutSnapshot(t *testing.T) {
	g := New(testCatalog(), nil)
	subject := platformflags.Subject{AccountID: account, Plan: "pro"}
	for pattern, want := range map[string]bool{
		"GET /v1/apps/{slug}":                  true,
		"PATCH /v1/deployments/{id}/traffic":   true,
		"GET /v1/apps/{slug}/realtime":         false,
		"GET /v1/apps/{slug}/routes":           false,
		"GET /v1/admin/accounts":               true,
		"GET /v1/apps/{slug}/unassigned-route": true,
	} {
		if got := g.Allowed(pattern, subject); got != want {
			t.Errorf("Allowed(%q) = %v, want %v", pattern, got, want)
		}
	}
	// Preview and internal keep their kill switch on so an operator can allow accounts.
	if !g.Released("GET /v1/apps/{slug}/realtime") || !g.Released("GET /v1/apps/{slug}/routes") {
		t.Fatal("preview and internal routes must pass the pre-auth kill switch by default")
	}
}

func TestFlagsDecidePerAccountAndKillSwitch(t *testing.T) {
	realtimeKey, _ := platformflags.CapabilityFlag("realtime")
	appsKey, _ := platformflags.CapabilityFlag("apps")
	other := "22222222-2222-4222-8222-222222222222"
	allow := true
	set := platformflags.NewSet(map[string]platformflags.Definition{
		realtimeKey: {Enabled: true, Default: false, Rules: []platformflags.Rule{{ID: "design-partner", Accounts: []string{account}, Value: &allow}}},
		appsKey:     {Enabled: false},
	})
	g := New(testCatalog(), set.Snapshot)

	if !g.Allowed("GET /v1/realtime", platformflags.Subject{AccountID: account}) {
		t.Fatal("an account rule must release a preview to that account")
	}
	if g.Allowed("GET /v1/realtime", platformflags.Subject{AccountID: other}) {
		t.Fatal("a preview must stay hidden from accounts the flag does not include")
	}
	if g.Released("GET /v1/apps/{slug}") {
		t.Fatal("a kill switch that is off must hide the route before authentication")
	}
	if got := testutil.ToFloat64(g.hidden.WithLabelValues("apps", "kill_switch")); got != 1 {
		t.Fatalf("hidden kill_switch counter = %v, want 1", got)
	}
}

func TestUnmappedRoutesAreReleasedAndCounted(t *testing.T) {
	g := New(testCatalog(), nil)
	if !g.Released("GET /v1/apps/{slug}/unassigned") {
		t.Fatal("an unowned route must stay reachable")
	}
	if !g.Released("GET /v1/admin/accounts") {
		t.Fatal("operator routes must stay reachable")
	}
	if got := testutil.ToFloat64(g.unmapped); got != 1 {
		t.Fatalf("unmapped counter = %v, want 1 (operator routes are not a gap)", got)
	}
}

func TestEmbeddedCatalogBuildsAGate(t *testing.T) {
	catalog, err := productcap.Load()
	if err != nil {
		t.Fatal(err)
	}
	g := New(catalog, nil)
	subject := platformflags.Subject{AccountID: account, Plan: "pro"}
	for pattern, want := range map[string]bool{
		"POST /v1/apps":                              true,
		"GET /v1/apps/{slug}":                        true,
		"POST /v1/apps/{slug}/deployments":           true,
		"GET /v1/apps/{slug}/logs":                   true,
		"POST /v1/auth/login":                        true,
		"GET /v1/account/export":                     true,
		"PATCH /v1/deployments/{id}/traffic":         true,
		"GET /v1/apps/{slug}/realtime/endpoints":     false,
		"GET /v1/postgres/databases":                 false,
		"GET /v1/apps/{slug}/route-requirements":     false,
		"GET /v1/account/platform-tenants":           false,
		"GET /dashboard/apps/{slug}/secrets":         true,
		"GET /dashboard/apps/{slug}/tenant-surfaces": false,
	} {
		if got := g.Allowed(pattern, subject); got != want {
			t.Errorf("default Allowed(%q) = %v, want %v", pattern, got, want)
		}
	}
}

func TestSubFeatureNeedsEveryAncestor(t *testing.T) {
	catalog := productcap.Catalog{Version: 1, Capabilities: []productcap.Capability{
		{ID: "events", Maturity: productcap.MaturityBeta, Routes: []string{"/v1/events"}},
		{ID: "event-replays", Maturity: productcap.MaturityBeta, Parent: "events", Routes: []string{"/v1/events/replays"}},
		{ID: "replay-previews", Maturity: productcap.MaturityBeta, Parent: "event-replays", Routes: []string{"/v1/events/replays/preview"}},
		{ID: "isolation", Maturity: productcap.MaturityGA, AlwaysOn: true},
	}}
	subject := platformflags.Subject{AccountID: account}
	g := New(catalog, nil)
	if !g.Allowed("GET /v1/events/replays/preview", subject) {
		t.Fatal("a beta grandchild of beta parents must be released by default")
	}
	if !g.Enabled("isolation", subject) {
		t.Fatal("an always-on capability must count as released")
	}

	eventsKey := platformflags.CapabilityFlagKey("events")
	replaysKey := platformflags.CapabilityFlagKey("event-replays")
	// The test catalog's ids are not real flags, so decide from a snapshot that defines them.
	off := map[string]platformflags.Definition{eventsKey: {Enabled: true, Default: false}, replaysKey: {Enabled: true, Default: true}}
	set := platformflags.Compile(off, map[string]int64{eventsKey: 1, replaysKey: 1}, nil, 0)
	g = New(catalog, func() *platformflags.Snapshot { return set })
	if g.Allowed("GET /v1/events/replays", subject) {
		t.Fatal("a sub-feature must stay hidden while its parent is off")
	}
	if g.Allowed("GET /v1/events/replays/preview", subject) {
		t.Fatal("a grandchild must stay hidden while any ancestor is off")
	}

	killed := map[string]platformflags.Definition{eventsKey: {Enabled: false}, replaysKey: {Enabled: true, Default: true}}
	set = platformflags.Compile(killed, map[string]int64{eventsKey: 1, replaysKey: 1}, nil, 0)
	if g.Released("GET /v1/events/replays") {
		t.Fatal("a parent's kill switch must hide its sub-features before authentication")
	}
}

// Every step of the CLI's source upload (start, append, discover, commit,
// cancel) belongs to the GA source-deploy capability. rc.295 put the commit
// under the internal gregale-commit capability, so every CLI source deploy
// on production answered 404 "no such API route" at commit (2026-10-26).
func TestSourceUploadRoutesAreSourceDeploy(t *testing.T) {
	catalog, err := productcap.Load()
	if err != nil {
		t.Fatal(err)
	}
	g := New(catalog, nil)
	subject := platformflags.Subject{AccountID: account, Plan: "free"}
	for _, pattern := range []string{
		"POST /v1/uploads",
		"GET /v1/uploads/{id}",
		"PATCH /v1/uploads/{id}",
		"DELETE /v1/uploads/{id}",
		"POST /v1/uploads/{id}/commit",
	} {
		if capability, _ := g.Capability(pattern); capability != "source-deploy" {
			t.Errorf("Capability(%q) = %q, want source-deploy", pattern, capability)
		}
		if !g.Released(pattern) || !g.Allowed(pattern, subject) {
			t.Errorf("%q is hidden by default; a GA deploy step must not be", pattern)
		}
	}
}
