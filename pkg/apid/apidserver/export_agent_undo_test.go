package apidserver

import (
	"net/http"
	"sort"
	"testing"
)

// AgentUndoHandlerPatternsForTest lists the routes undo reads and writes
// through (ADR-20261112080601).
func AgentUndoHandlerPatternsForTest(t *testing.T, srv http.Handler) []string {
	t.Helper()
	h, ok := srv.(*cliAuthTestServer)
	if !ok {
		t.Fatal("AgentUndoHandlerPatternsForTest: unexpected handler type")
	}
	var out []string
	for pattern := range h.apid.agentUndoHandlers() {
		out = append(out, pattern)
	}
	sort.Strings(out)
	return out
}
