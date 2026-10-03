package canary

import (
	"errors"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestProgressionWaitsForRouteGateWithoutWorkerError(t *testing.T) {
	now := time.Now()
	for _, code := range []string{api.CodeRouteGateBlocked, api.CodeRouteHealthBlocked, ""} {
		routeBlocked := code != ""
		store := &stubStore{rows: []CanaryRow{mirrorCleanRow(t, now)}, mirrorSummary: MirrorSummary{TotalInvocations: 2}}
		var advanceErr error = errors.New("unexpected transport failure")
		if routeBlocked {
			advanceErr = api.NewProblem(http.StatusConflict, code, "Route gate blocked", "check_stale")
		}
		apid := &stubAPID{err: advanceErr}
		prog := NewProgression(store, apid, nil, slog.Default())
		prog.Now = func() time.Time { return now }
		stats, err := prog.Once(t.Context())
		if err != nil || stats.Advanced != 0 {
			t.Fatalf("advance: %+v %v", stats, err)
		}
		if routeBlocked && (stats.SkippedRouteGate != 1 || stats.Errors != 0) {
			t.Fatalf("expected gate counted as worker failure: %+v", stats)
		}
		if !routeBlocked && stats.Errors != 1 {
			t.Fatalf("unexpected failure suppressed: %+v", stats)
		}
	}
}
