package apidserver_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/platformflags"
)

// adr: 20261027090010 — the agent API belongs to the internal agents
// capability (ADR-20261023115741): every agent route, any method, answers
// like an unregistered route to an account the capability is not released
// to. A released account reaches the routes.
func TestAgentsHiddenUntilReleased(t *testing.T) {
	e := entityAPISetup(t)
	if err := e.s.InstallCapabilityGate(); err != nil {
		t.Fatal(err)
	}
	e.s.ConfigureAgentsForTest()
	const run = "/v1/agent-runs/00000000-0000-4000-8000-0000000000aa"
	requests := func() []*httptest.ResponseRecorder {
		return []*httptest.ResponseRecorder{
			e.do(t, http.MethodPost, "/v1/agents", nil, nil),
			e.do(t, http.MethodGet, "/v1/agents", nil, nil),
			e.do(t, http.MethodGet, "/v1/agents/triage", nil, nil),
			e.do(t, http.MethodDelete, "/v1/agents/triage", nil, nil),
			e.do(t, http.MethodPost, "/v1/agents/triage/versions", nil, nil),
			e.do(t, http.MethodGet, "/v1/agents/triage/versions/1", nil, nil),
			e.do(t, http.MethodPost, "/v1/agents/triage/runs", nil, nil),
			e.do(t, http.MethodGet, "/v1/agent-runs", nil, nil),
			e.do(t, http.MethodGet, run, nil, nil),
			e.do(t, http.MethodDelete, run, nil, nil),
			e.do(t, http.MethodGet, run+"/outputs", nil, nil),
			e.do(t, http.MethodGet, run+"/outputs/files/report.md", nil, nil),
			e.do(t, http.MethodPut, "/v1/agents/triage", nil, nil),
		}
	}
	// The internal tier default: nobody.
	for _, res := range requests() {
		assertUnregisteredRoute(t, res)
	}

	// Released to another account: still hidden from this one.
	releaseAgentsTo(t, e.s, "00000000-0000-4000-8000-000000000001")
	for _, res := range requests() {
		assertUnregisteredRoute(t, res)
	}

	// Released to this account: the routes answer (unavailable without
	// Postgres here), and a wrong method is still hidden.
	releaseAgentsTo(t, e.s, "00000000-0000-4000-8000-000000000001", e.acct.ID)
	res := e.do(t, http.MethodGet, "/v1/agents", nil, nil)
	if res.Code == http.StatusNotFound || !strings.Contains(res.Body.String(), "capacity_unavailable") {
		t.Fatalf("released account = %d %s", res.Code, res.Body.String())
	}
	assertUnregisteredRoute(t, e.do(t, http.MethodPut, "/v1/agents/triage", nil, nil))

	// The kill switch hides the routes before authentication, from
	// everyone, released accounts included.
	e.s.SeedPlatformFlag(capabilityFlag(t, "agents"), platformflags.Definition{Enabled: false})
	for _, res := range append(requests(), anonymousEntityRequest(e, "/v1/agents")) {
		assertUnregisteredRoute(t, res)
	}
}

// installAgentGate installs the capability gate with the given capabilities
// released to every account; agents keeps its internal default, hidden.
func installAgentGate(t *testing.T, srv *apidserver.Server, released ...string) {
	t.Helper()
	if err := srv.InstallCapabilityGate(); err != nil {
		t.Fatal(err)
	}
	for _, capability := range released {
		srv.SeedPlatformFlag(capabilityFlag(t, capability), platformflags.Definition{Enabled: true, Default: true})
	}
}

// releaseAgentsTo releases the agents capability to accounts, as an
// operator's account rule does.
func releaseAgentsTo(t *testing.T, srv *apidserver.Server, accounts ...string) {
	t.Helper()
	allow := true
	srv.SeedPlatformFlag(capabilityFlag(t, "agents"), platformflags.Definition{Enabled: true,
		Rules: []platformflags.Rule{{ID: "agents-test", Accounts: accounts, Value: &allow}}})
}
