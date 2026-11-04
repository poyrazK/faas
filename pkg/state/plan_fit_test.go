package state_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// A plan that shrinks keeps the subject's oldest deployed apps and refuses
// the rest; developer and preview apps have their own limits and deleted
// apps none; an app above the plan's RAM is over whatever its rank.
// adr: 20261103131742
func TestAppsOverPlan(t *testing.T) {
	at := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	app := func(id string, age int, ram int, status state.AppStatus) state.App {
		return state.App{ID: id, RAMMB: ram, Status: status, CreatedAt: at.Add(time.Duration(age) * time.Hour)}
	}
	preview := app("preview", 0, 128, state.AppActive)
	preview.PreviewOfSlug, preview.PreviewPrNumber = "api", 7
	apps := []state.App{
		app("newest", 3, 128, state.AppActive),
		app("parked-second", 1, 128, state.AppEvictedCold),
		app("oldest", 0, 128, state.AppActive),
		app("deleted", 0, 4096, state.AppDeleted),
		app("big-third", 2, 1024, state.AppActive),
		preview,
	}
	hobby := api.MustLimitsFor(api.PlanHobby)
	if over := state.AppsOverPlan(apps, hobby); len(over) != 1 || over["big-third"].RAMMB != 1024 {
		t.Fatalf("Hobby: over = %+v, want only big-third's RAM", over)
	}
	free := api.MustLimitsFor(api.PlanFree)
	over := state.AppsOverPlan(apps, free)
	want := map[string]state.PlanOverage{
		"parked-second": {DeployedApps: 4},
		"big-third":     {DeployedApps: 4, RAMMB: 1024},
		"newest":        {DeployedApps: 4},
	}
	if len(over) != len(want) {
		t.Fatalf("Free: over = %+v, want %+v", over, want)
	}
	for id, w := range want {
		if over[id] != w {
			t.Fatalf("Free: over[%s] = %+v, want %+v", id, over[id], w)
		}
	}
	if p := over["big-third"].Problem(free); p.Code != api.CodePlanLimitRAM {
		t.Fatalf("RAM and rank over: code %s, want %s", p.Code, api.CodePlanLimitRAM)
	}
	if p := over["newest"].Problem(free); p.Code != api.CodePlanLimitApps || p.Limit == nil {
		t.Fatalf("rank over: problem %+v, want %s with its limit", p, api.CodePlanLimitApps)
	}
}
