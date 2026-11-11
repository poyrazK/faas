package apidsource

// adr: 20261104132753 — a source build is refused once the paying account
// used its plan's monthly build minutes; a failed read admits the build.

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

var _ BuildMinutesStore = (*state.PgStore)(nil)

type fakeBuildMinutes struct {
	plan    api.Plan
	seconds int64
	err     error
}

func (f fakeBuildMinutes) BuildSecondsThisMonth(context.Context, string, time.Time) (api.Plan, int64, error) {
	return f.plan, f.seconds, f.err
}

func TestBuildMinutesProblem(t *testing.T) {
	hobby := api.MustLimitsFor(api.PlanHobby).BuildMinutesPerMonth
	cases := []struct {
		name   string
		reader BuildMinutesStore
		refuse bool
	}{
		{"no reader", nil, false},
		{"under the quota", fakeBuildMinutes{plan: api.PlanHobby, seconds: int64(hobby-1) * 60}, false},
		{"at the quota", fakeBuildMinutes{plan: api.PlanHobby, seconds: int64(hobby) * 60}, true},
		{"read error admits", fakeBuildMinutes{err: errors.New("db down")}, false},
		{"unknown plan admits", fakeBuildMinutes{plan: "", seconds: 1 << 40}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			problem := buildMinutesProblem(context.Background(), tc.reader, EnqueueParams{AppID: "app-1"})
			if (problem != nil) != tc.refuse {
				t.Fatalf("problem = %v, want refused=%v", problem, tc.refuse)
			}
			if problem == nil {
				return
			}
			if problem.Status != http.StatusForbidden || problem.Code != api.CodePlanLimitBuildMinutes {
				t.Fatalf("problem = %+v, want 403 %s", problem, api.CodePlanLimitBuildMinutes)
			}
			if problem.Limit == nil || *problem.Limit != int64(hobby) {
				t.Fatalf("limit = %v, want %d", problem.Limit, hobby)
			}
		})
	}
}
