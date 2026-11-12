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

// WithTestRollbackArtifacts lets rollbacks on the cli-auth test server find
// their artifacts.
func WithTestRollbackArtifacts(t *testing.T, srv http.Handler) func() {
	t.Helper()
	h, ok := srv.(*cliAuthTestServer)
	if !ok {
		t.Fatal("WithTestRollbackArtifacts: unexpected handler type")
	}
	previous := h.apid.rollbackArtifactVerifier
	h.apid.WithRollbackArtifactVerifier(stubRollbackArtifactVerifier{})
	return func() { h.apid.rollbackArtifactVerifier = previous }
}
