// adr: 20261126164847
package api

import (
	"net/http"
	"testing"
)

// githubd's source-ref problems cross gRPC carrying only their code; the
// status apid answers with comes from StatusForCode, and must match the
// constructor's.
func TestStatusForCodeSourceRefProblems(t *testing.T) {
	for _, p := range []*Problem{
		ErrInvalidRef("main"),
		ErrSourceRefUnavailable("down"),
		ErrGitHubInstallNotFound(),
		NewProblem(http.StatusForbidden, CodeGitHubRepoNotAccessible, "Repository is not accessible", "private"),
	} {
		if got := StatusForCode(p.Code); got != p.Status {
			t.Errorf("StatusForCode(%q) = %d, want the constructor's %d", p.Code, got, p.Status)
		}
	}
}
