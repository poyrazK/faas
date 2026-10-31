package canary

import (
	"fmt"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261020085706 — a release waiting for approval is a skip, not a
// worker error, whether apid's client wraps the problem or not.
func TestProgressionSkipsReleasesWaitingForApproval(t *testing.T) {
	now := time.Now()
	waiting := api.NewProblem(http.StatusConflict, api.CodeReleaseApprovalRequired, "Release waiting for approval", "needs 1 more approval")
	for name, advanceErr := range map[string]error{
		"problem":             waiting,
		"api error":           &api.APIError{Problem: *waiting},
		"wrapped api error":   fmt.Errorf("advance: %w", &api.APIError{Problem: *waiting}),
		"rejected":            &api.APIError{Problem: *api.NewProblem(http.StatusConflict, api.CodeReleaseApprovalRejected, "Release rejected", "")},
		"route gate via http": &api.APIError{Problem: *api.NewProblem(http.StatusConflict, api.CodeRouteGateBlocked, "Route gate blocked", "")},
	} {
		t.Run(name, func(t *testing.T) {
			store := &stubStore{rows: []CanaryRow{mirrorCleanRow(t, now)}, mirrorSummary: MirrorSummary{TotalInvocations: 2}}
			prog := NewProgression(store, &stubAPID{err: advanceErr}, nil, slog.Default())
			prog.Now = func() time.Time { return now }
			stats, err := prog.Once(t.Context())
			if err != nil || stats.Errors != 0 || stats.Advanced != 0 || stats.SkippedAwaitingApproval+stats.SkippedRouteGate != 1 {
				t.Fatalf("stats = %+v, err = %v", stats, err)
			}
		})
	}
}
