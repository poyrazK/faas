package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// App-task concurrency: how many of an account's own commands may be queued
// or running at once (`app exec`, interactive sessions, `app cp`,
// `app port-forward`). Each holds a VM of the app's size, so without a cap one
// account could queue VMs without bound against node capacity that serving
// apps need. Release commands, command crons and the platform's own probes
// (binding verification, Data API inspection) are not counted: the platform
// bounds those itself.

// CodePlanLimitAppTasks refuses a task while the account already has its
// plan's number of tasks queued or running.
const CodePlanLimitAppTasks = "plan_limit_app_tasks"

// AppTaskConcurrencyBound is the largest value an operator override may set.
const AppTaskConcurrencyBound = 1000

// AppTaskConcurrencyDefault is a plan's cap before any operator override,
// from the plan's Limits.AppTaskConcurrency. An unknown plan gets the Free
// cap.
func AppTaskConcurrencyDefault(p Plan) int {
	if limits, ok := LimitsFor(p); ok {
		return limits.AppTaskConcurrency
	}
	limits, _ := LimitsFor(PlanFree)
	return limits.AppTaskConcurrency
}

func knownPlan(p Plan) bool {
	for _, plan := range Plans {
		if plan == p {
			return true
		}
	}
	return false
}

// AppTaskConcurrencyDefaultsJSON is the runtime-config default: every plan's
// built-in cap.
func AppTaskConcurrencyDefaultsJSON() json.RawMessage {
	out := make(map[string]int, len(Plans))
	for _, plan := range Plans {
		out[string(plan)] = AppTaskConcurrencyDefault(plan)
	}
	raw, _ := json.Marshal(out)
	return raw
}

// ParseAppTaskConcurrency reads the operator override, an object of plan to
// cap such as {"free": 1, "pro": 20}. A plan left out keeps its default; 0
// refuses every new task on that plan.
func ParseAppTaskConcurrency(raw json.RawMessage) (map[Plan]int, error) {
	var values map[string]int
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, fmt.Errorf("app task concurrency must be an object of plan to count, such as {\"free\": 2}")
	}
	out := make(map[Plan]int, len(values))
	for name, limit := range values {
		plan := Plan(name)
		if !knownPlan(plan) {
			return nil, fmt.Errorf("unknown plan %q; use %s", name, joinPlans())
		}
		if limit < 0 || limit > AppTaskConcurrencyBound {
			return nil, fmt.Errorf("plan %q: count must be from 0 to %d", name, AppTaskConcurrencyBound)
		}
		out[plan] = limit
	}
	return out, nil
}

// AppTaskConcurrencyFor is a plan's cap with the operator override applied.
func AppTaskConcurrencyFor(p Plan, overrides map[Plan]int) int {
	if limit, ok := overrides[p]; ok {
		return limit
	}
	return AppTaskConcurrencyDefault(p)
}

// AppTaskCountsTowardConcurrency reports whether a manual task's command is
// the account's own work. Platform probe and inspection commands use the
// reserved __gregale_ prefix and are exempt; copy-out, port-forward and debug
// sessions use it too but are the account's own sessions, so they count.
func AppTaskCountsTowardConcurrency(command []string) bool {
	if len(command) == 0 || !strings.HasPrefix(command[0], "__gregale_") {
		return true
	}
	switch command[0] {
	case AppTaskCopyOutCommand, AppTaskPortForwardCommand, AppTaskDebugCommand:
		return true
	}
	return false
}

// ErrPlanLimitAppTasks refuses a task at the plan's concurrency cap.
func ErrPlanLimitAppTasks(plan Plan, limit, active int, slug string) *Problem {
	detail := fmt.Sprintf("%s plan allows %d app task(s) queued or running at once; this account has %d. "+
		"Wait for one to finish, or stop one: list them with `gregale app tasks %s` and cancel with `gregale app tasks cancel %s <task-id>`.",
		plan, limit, active, slug, slug)
	if limit == 0 {
		detail = fmt.Sprintf("App tasks are paused for the %s plan on this installation; try again later or contact support.", plan)
	}
	return NewProblem(http.StatusTooManyRequests, CodePlanLimitAppTasks, "App task limit reached", detail).
		WithLimit(int64(limit), int64(active)).
		WithDocs(docsBase + "/apps#task-history")
}

func joinPlans() string {
	names := make([]string, len(Plans))
	for i, plan := range Plans {
		names[i] = string(plan)
	}
	return strings.Join(names, ", ")
}
