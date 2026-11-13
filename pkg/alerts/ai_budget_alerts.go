package alerts

// ADR 20261027132936 amendment 2: ai_budget_alerts counts the AI budget
// threshold crossings of the integrations bound to the app within the rule's
// window. A firing notification names them, so alert channels carry what the
// outbound.ai_budget.threshold app webhook carried alone before.

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (e *Evaluator) observeAIBudgetAlerts(ctx context.Context, rule state.AlertRule) (float64, bool, string) {
	total, _, err := e.readAIBudgetAlerts(ctx, rule, 1)
	if err != nil {
		e.log.Warn("alerts: read AI budget alerts", "rule", rule.ID, "err", err)
		return 0, false, skipDegraded
	}
	if total < 0 {
		return 0, false, skipDegraded
	}
	value := float64(total)
	return value, compareFloat(value, rule.Comparison, rule.Threshold), ""
}

// aiBudgetAlertNotes describes the newest crossings a firing rule counted.
// A read failure leaves the notification without notes.
func (e *Evaluator) aiBudgetAlertNotes(ctx context.Context, rule state.AlertRule) []string {
	_, notes, err := e.readAIBudgetAlerts(ctx, rule, api.AIBudgetAlertNotes)
	if err != nil {
		e.log.Warn("alerts: read AI budget alert notes", "rule", rule.ID, "err", err)
		return nil
	}
	return notes
}

// readAIBudgetAlerts returns the rule's count (-1 when the store cannot
// answer) and notes for up to limit of the newest crossings.
func (e *Evaluator) readAIBudgetAlerts(ctx context.Context, rule state.AlertRule, limit int) (int64, []string, error) {
	store, ok := e.store.(state.AIBudgetAlertStore)
	if !ok || rule.AppID == "" {
		return -1, nil, nil
	}
	since := e.windowStart(rule.WindowSpec, e.now())
	crossings, total, err := store.AIBudgetAlertsSince(ctx, rule.AccountID, rule.AppID, since, limit)
	notes := make([]string, 0, len(crossings))
	for _, c := range crossings {
		notes = append(notes, routeAlertNote(aiBudgetAlertNote(c)))
	}
	return total, notes, err
}

// aiBudgetAlertNote names one crossing: the integration, the budget and
// whose it is, and how much of it was used. Costs are millicents of USD.
func aiBudgetAlertNote(c state.AIBudgetAlert) string {
	note := fmt.Sprintf("%s %s budget at %d%% (%s of %s, %s)", c.IntegrationName, c.Budget, c.ThresholdPercent,
		aiBudgetAmount(c.Budget, c.Used), aiBudgetAmount(c.Budget, c.Limit), c.CrossedAt.Format("2006-01-02 15:04 UTC"))
	switch {
	case c.EndUser != "":
		note += " for end user " + c.EndUser
	case c.Tag != "":
		note += " for tag " + c.Tag
	}
	return note
}

// aiBudgetAmount formats a budget value: USD for cost budgets, tokens
// otherwise.
func aiBudgetAmount(budget string, value int64) string {
	switch budget {
	case "daily_cost", "monthly_cost", "user_daily_cost", "tag_daily_cost":
		return fmt.Sprintf("USD %d.%05d", value/100_000, value%100_000)
	}
	return fmt.Sprintf("%d tokens", value)
}
