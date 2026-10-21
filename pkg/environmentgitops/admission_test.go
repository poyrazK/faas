// adr: 20261021080847 — enforced sources execute only for admitted accounts.
package environmentgitops_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/environmentgitops"
)

func TestWorkerHoldsEnforcedSourceOfUnadmittedAccount(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mode     string
		admitted bool
		asked    int
		status   string
		code     string
		applied  int
	}{
		{name: "withheld", mode: "enforce", status: "blocked", code: environmentgitops.EnforcementUnavailableCode, asked: 1},
		{name: "admitted", mode: "enforce", admitted: true, asked: 1, applied: 1},
		{name: "report ignores admission", mode: "report", status: "drifted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, source, _, b, worker := setup(t, tc.mode)
			b.observation.State.Fields[1].Value = json.RawMessage(`"console-edit"`)
			asked := 0
			worker.Mode = tc.mode
			worker.Admit = func(_ context.Context, accountID string) bool {
				asked++
				if accountID != source.AccountID {
					t.Errorf("admission asked for %q, want %q", accountID, source.AccountID)
				}
				return tc.admitted
			}
			if worked, err := worker.RunOnce(t.Context()); err != nil || !worked {
				t.Fatalf("attempt: %v %v", worked, err)
			}
			run := lastRun(t, store, source)
			if asked != tc.asked || b.applied != tc.applied {
				t.Fatalf("asked=%d applied=%d, want %d/%d", asked, b.applied, tc.asked, tc.applied)
			}
			if tc.status != "" && (run.Status != tc.status || run.ErrorCode != tc.code) {
				t.Fatalf("run = %s/%s, want %s/%s", run.Status, run.ErrorCode, tc.status, tc.code)
			}
			if tc.code != "" && b.observed != 0 {
				t.Fatalf("a held source was observed %d times", b.observed)
			}
		})
	}
}
