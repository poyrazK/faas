package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestParseAppTaskConcurrency(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		want      map[Plan]int
		wantErr   string
	}{
		{name: "defaults object", raw: string(AppTaskConcurrencyDefaultsJSON()), want: map[Plan]int{PlanFree: 2, PlanHobby: 4, PlanPro: 10, PlanScale: 25}},
		{name: "partial", raw: `{"free": 1}`, want: map[Plan]int{PlanFree: 1}},
		{name: "pause a plan", raw: `{"hobby": 0}`, want: map[Plan]int{PlanHobby: 0}},
		{name: "empty", raw: `{}`, want: map[Plan]int{}},
		{name: "unknown plan", raw: `{"enterprise": 3}`, wantErr: "unknown plan"},
		{name: "negative", raw: `{"pro": -1}`, wantErr: "from 0 to"},
		{name: "above bound", raw: `{"pro": 1001}`, wantErr: "from 0 to"},
		{name: "not an object", raw: `5`, wantErr: "object of plan"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseAppTaskConcurrency(json.RawMessage(tc.raw))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for plan, limit := range tc.want {
				if got[plan] != limit {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestAppTaskConcurrencyFor(t *testing.T) {
	overrides := map[Plan]int{PlanPro: 0}
	if got := AppTaskConcurrencyFor(PlanPro, overrides); got != 0 {
		t.Fatalf("overridden pro = %d, want 0", got)
	}
	if got := AppTaskConcurrencyFor(PlanScale, overrides); got != 25 {
		t.Fatalf("default scale = %d, want 25", got)
	}
	if got := AppTaskConcurrencyFor(Plan("legacy"), nil); got != AppTaskConcurrencyDefault(PlanFree) {
		t.Fatalf("unknown plan = %d, want the free cap", got)
	}
}

func TestAppTaskCountsTowardConcurrency(t *testing.T) {
	for _, tc := range []struct {
		command []string
		want    bool
	}{
		{[]string{"bin/migrate"}, true},
		{nil, true},
		{[]string{AppTaskCopyOutCommand, "/data"}, true},
		{[]string{AppTaskPortForwardCommand, "5432"}, true},
		{[]string{AppTaskDebugCommand}, true},
		{[]string{AppTaskServiceBindingProbeCommand, "svc"}, false},
		{[]string{AppTaskMigrationDependenciesCommand}, false},
	} {
		if got := AppTaskCountsTowardConcurrency(tc.command); got != tc.want {
			t.Errorf("%v: got %v, want %v", tc.command, got, tc.want)
		}
	}
}

func TestErrPlanLimitAppTasks(t *testing.T) {
	p := ErrPlanLimitAppTasks(PlanHobby, 4, 4, "web")
	if p.Status != http.StatusTooManyRequests || p.Code != CodePlanLimitAppTasks {
		t.Fatalf("problem = %d %s", p.Status, p.Code)
	}
	if !strings.Contains(p.Detail, "gregale app tasks cancel web <task-id>") {
		t.Fatalf("detail does not name the fix: %s", p.Detail)
	}
	paused := ErrPlanLimitAppTasks(PlanFree, 0, 0, "web")
	if !strings.Contains(paused.Detail, "paused") {
		t.Fatalf("a 0 cap should read as paused: %s", paused.Detail)
	}
}
