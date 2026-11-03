package apidserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestIssueTicketPendingReturnsConflict(t *testing.T) {
	w := httptest.NewRecorder()
	writeIssueError(w, state.ErrIssueTicketPending)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "issue_ticket_pending") {
		t.Fatalf("pending ticket response: %d %s", w.Code, w.Body.String())
	}
}
