package apidserver

import (
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/receivers"
)

// AgentRequestsForTest is what apid answers gateways' agent approval and
// activity calls with (ADR-20261111150000).
func AgentRequestsForTest(t *testing.T, srv http.Handler) receivers.AgentRequests {
	t.Helper()
	h, ok := srv.(*cliAuthTestServer)
	if !ok {
		t.Fatal("AgentRequestsForTest: unexpected handler type")
	}
	agents := h.apid.agentRequestsForGateways()
	if agents == nil {
		t.Fatal("AgentRequestsForTest: no auth middleware")
	}
	return agents
}
