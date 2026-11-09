// adr: 20261108090000
package apidserver

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// x-gregale-allow-callers naming no service and no app is reported; a
// project service or an account app slug is not.
func TestAllowCallersNotesNameUnknownCallers(t *testing.T) {
	callers := []string{"web", "ledger", "partner-app"}
	selected := []reposcan.Workload{{Name: "billing", AllowedServiceCallers: &callers}}
	scanned := append([]reposcan.Workload{{Name: "web"}}, selected...)
	notes := allowCallersNotes(selected, scanned, nil, []state.App{{Slug: "partner-app"}})
	if len(notes) != 1 || !strings.Contains(notes[0], `"ledger"`) {
		t.Fatalf("notes = %v, want only ledger reported", notes)
	}
}
