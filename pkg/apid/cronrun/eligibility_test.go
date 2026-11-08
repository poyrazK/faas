package cronrun_test

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/cronrun"
	"github.com/onebox-faas/faas/pkg/state"
)

// fakeStore holds one account's apps and crons and ranks them the way
// state.CronAccountQuotaStore does: oldest first, per-app cap, then the
// per-account cap.
type fakeStore struct {
	plan         api.Plan
	suspended    bool
	crons        []state.Cron
	eligibleErr  error
	eligibleAsks int
}

func (f *fakeStore) AccountByID(_ context.Context, id string) (state.Account, error) {
	status := state.AccountActive
	if f.suspended {
		status = state.AccountSuspended
	}
	return state.Account{ID: id, Plan: f.plan, Status: status}, nil
}

func (f *fakeStore) OrgByID(context.Context, string) (state.Org, error) {
	return state.Org{}, state.ErrNotFound
}

func (f *fakeStore) BillingSubjectEligibleCronIDs(_ context.Context, _ string, perApp, perAccount int) ([]string, error) {
	f.eligibleAsks++
	if f.eligibleErr != nil {
		return nil, f.eligibleErr
	}
	live := f.live("")
	perAppSeen := map[string]int{}
	var ids []string
	for _, c := range live {
		perAppSeen[c.AppID]++
		if perAppSeen[c.AppID] > perApp || len(ids) == perAccount {
			continue
		}
		ids = append(ids, c.ID)
	}
	return ids, nil
}

func (f *fakeStore) ListCronsForApp(_ context.Context, appID string) ([]state.Cron, error) {
	var out []state.Cron
	for _, c := range f.crons {
		if c.AppID == appID {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *fakeStore) live(appID string) []state.Cron {
	var out []state.Cron
	for _, c := range f.crons {
		if c.Enabled && c.SuspendedReason == "" && (appID == "" || c.AppID == appID) {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// crons returns n enabled crons of app, created a minute apart from start.
func crons(app string, n int, start time.Time) []state.Cron {
	out := make([]state.Cron, n)
	for i := range out {
		out[i] = state.Cron{ID: fmt.Sprintf("%s-%d", app, i+1), AppID: app, Enabled: true,
			CreatedAt: start.Add(time.Duration(i) * time.Minute)}
	}
	return out
}

func TestOverPlan(t *testing.T) {
	ctx := context.Background()
	t0 := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	// Hobby: 5 per app, 10 per account. App a has 7 crons, the oldest;
	// apps b and c have 5 each, so b's are ranked 6-10 and c's 11-15 overall.
	all := append(append(crons("a", 7, t0), crons("b", 5, t0.Add(time.Hour))...), crons("c", 5, t0.Add(2*time.Hour))...)
	disabled := state.Cron{ID: "a-off", AppID: "a", CreatedAt: t0.Add(3 * time.Hour)}

	cases := []struct {
		name          string
		plan          api.Plan
		cron          string
		eligibleErr   error
		wantScope     string // "" = allowed
		wantLimit     int64
		wantObserved  int64
		wantNoRanking bool
	}{
		{name: "oldest cron of the app runs", plan: api.PlanHobby, cron: "a-1"},
		{name: "a cron within both caps runs", plan: api.PlanHobby, cron: "b-5"},
		{name: "over the per-app cap", plan: api.PlanHobby, cron: "a-6", wantScope: "app", wantLimit: 5, wantObserved: 7},
		{name: "over the per-account cap", plan: api.PlanHobby, cron: "c-1", wantScope: "account", wantLimit: 10, wantObserved: 15},
		{name: "a plan that allows them all", plan: api.PlanPro, cron: "c-5"},
		{name: "a ranking error admits", plan: api.PlanHobby, cron: "c-1", eligibleErr: errors.New("db down")},
		{name: "a disabled cron is left to the scheduler", plan: api.PlanHobby, cron: "a-off", wantNoRanking: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{plan: tc.plan, crons: append(append([]state.Cron(nil), all...), disabled), eligibleErr: tc.eligibleErr}
			var c state.Cron
			for _, x := range store.crons {
				if x.ID == tc.cron {
					c = x
				}
			}
			prob := cronrun.OverPlan(ctx, store, state.App{ID: c.AppID, AccountID: "acct"}, c)
			if tc.wantNoRanking && store.eligibleAsks != 0 {
				t.Fatalf("ranked crons %d times for a disabled cron", store.eligibleAsks)
			}
			if tc.wantScope == "" {
				if prob != nil {
					t.Fatalf("refused: %s", prob.Detail)
				}
				return
			}
			if prob == nil {
				t.Fatal("allowed a cron the plan does not let fire")
			}
			if prob.Code != api.CodePlanCronQuota || prob.Status != 403 {
				t.Fatalf("problem %s %d, want %s 403", prob.Code, prob.Status, api.CodePlanCronQuota)
			}
			if prob.Limit == nil || *prob.Limit != tc.wantLimit || prob.Observed == nil || *prob.Observed != tc.wantObserved {
				t.Fatalf("limit/observed = %v/%v, want %d/%d", prob.Limit, prob.Observed, tc.wantLimit, tc.wantObserved)
			}
		})
	}
}

// Paused names why an enabled, unsuspended cron does not fire, for status.
func TestPaused(t *testing.T) {
	ctx := context.Background()
	t0 := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	all := crons("a", 7, t0)
	app := state.App{ID: "a", AccountID: "acct"}
	for _, tc := range []struct {
		name      string
		suspended bool
		cron      state.Cron
		want      string
	}{
		{"within the limits", false, all[0], ""},
		{"over the per-app limit", false, all[6], cronrun.PausedPlanLimit},
		{"owner suspended", true, all[0], cronrun.PausedAccountSuspended},
		{"disabled is reported elsewhere", false, state.Cron{ID: "off", AppID: "a"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{plan: api.PlanHobby, suspended: tc.suspended, crons: all}
			if got := cronrun.Paused(ctx, store, app, tc.cron); got != tc.want {
				t.Fatalf("Paused = %q, want %q", got, tc.want)
			}
		})
	}
}
