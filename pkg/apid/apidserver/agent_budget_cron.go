package apidserver

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/cronexpr"
	"github.com/onebox-faas/faas/pkg/state"
)

// cronPricingWindow is how far ahead a cron's firings are walked to price
// it; a week covers every daily and weekly pattern.
const cronPricingWindow = 7 * 24 * time.Hour

// cronPricingMaxFirings bounds the walk: a cron firing every minute fires
// 10,080 times a week.
const cronPricingMaxFirings = 20_000

// cronResidentSecondsPerMonth is how long a cron keeps a VM running in a
// month: each firing holds it for hold seconds, or until the next firing
// when that comes sooner. A schedule that does not parse prices nothing; it
// was validated before this is called.
func cronResidentSecondsPerMonth(schedule, timezone string, hold time.Duration, now time.Time) int64 {
	if hold <= 0 {
		return 0
	}
	sched, err := cronexpr.Parse(schedule, timezone)
	if err != nil {
		return 0
	}
	end := now.Add(cronPricingWindow)
	var resident time.Duration
	at := sched.Next(now)
	for i := 0; i < cronPricingMaxFirings && !at.IsZero() && at.Before(end); i++ {
		next := sched.Next(at)
		held := hold
		if !next.IsZero() && next.Sub(at) < held {
			held = next.Sub(at)
		}
		resident += held
		at = next
	}
	month := resident * time.Duration(api.AgentBudgetHoursPerMonth) / (cronPricingWindow / time.Hour)
	return int64(month / time.Second)
}

// cronStandingMillicents prices a cron for an agent's budget (cost-control
// review 2026-11-19, N3). An HTTP cron wakes the app, which stays up for its
// idle timeout; an app that already keeps a warm instance adds nothing. A
// command cron boots its own task VM, priced up to the command's timeout.
// A disabled cron costs nothing.
func cronStandingMillicents(app state.App, plan api.Plan, schedule, timezone string, enabled bool, command []string, commandTimeoutS int, now time.Time) int64 {
	if !enabled {
		return 0
	}
	var hold time.Duration
	if len(command) > 0 {
		hold = time.Duration(commandTimeoutS) * time.Second
	} else {
		if appFloor(app) > 0 {
			return 0
		}
		hold = time.Duration(appIdleTimeoutS(app, plan)) * time.Second
	}
	return api.AgentPrewarmMillicents(1, appRAM(app.RAMMB, plan), cronResidentSecondsPerMonth(schedule, timezone, hold, now))
}

// chargeCronUpdate charges an agent for what a cron patch adds: the cron
// priced as it will be less as it is. Turning a cron off or slowing it down
// never asks.
func (s *server) chargeCronUpdate(w http.ResponseWriter, r *http.Request, plan api.Plan, app state.App, c state.Cron,
	req api.UpdateCronRequest, timezone *string) (func(), bool) {
	now := time.Now().UTC()
	before := cronStandingMillicents(app, plan, c.Schedule, c.Timezone, c.Enabled, c.Command, c.CommandTimeoutSeconds, now)
	schedule, zone, enabled := c.Schedule, c.Timezone, c.Enabled
	if req.Schedule != nil {
		schedule = *req.Schedule
	}
	if timezone != nil {
		zone = *timezone
	}
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	after := cronStandingMillicents(app, plan, schedule, zone, enabled, c.Command, c.CommandTimeoutSeconds, now)
	return s.chargeAgentBudget(w, r, plan, before, after)
}

// appIdleTimeoutS is how long the app stays up after a request: its own
// idle timeout, else its plan's.
func appIdleTimeoutS(app state.App, plan api.Plan) int {
	if app.IdleTimeoutS > 0 {
		return app.IdleTimeoutS
	}
	if limits, ok := api.LimitsFor(plan); ok {
		return limits.IdleTimeoutS
	}
	return 0
}
