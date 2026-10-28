package alerts

import (
	"context"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// Per-account evaluation (ADR-20261028101615). One tick used to walk every
// account's rules in a single goroutine, so one account's slow reads or
// unresponsive webhook delayed every account's alerts. A tick now evaluates
// up to `workers` accounts at once, each account's rules in order, and gives
// each account AlertAccountTickBudget: once spent, its remaining rules wait
// for the next tick, which starts with them.

// RuleRelease decides, for one account's tick, whether meterd evaluates a rule
// and whether its notifications go to notification channels. meterd builds it
// from the account's plan and capability flags.
type RuleRelease func(ctx context.Context, rule state.AlertRule) (evaluate, channels bool)

// SetRuleRelease installs forAccount, called once per account per tick. nil,
// or a nil RuleRelease, evaluates every rule and delivers to every channel. A
// nil evaluator ignores it, as SetChannels does.
func (e *Evaluator) SetRuleRelease(forAccount func(ctx context.Context, accountID string) RuleRelease) {
	if e == nil {
		return
	}
	e.release = forAccount
}

// SetRuleOutcome installs a counter of the rules a tick did not evaluate:
// reason "deferred" (the account spent its tick budget) or "not_released"
// (its plan or capability flags no longer cover the rule). nil records
// nothing; a nil evaluator ignores it.
func (e *Evaluator) SetRuleOutcome(count func(reason string, n int)) {
	if e == nil {
		return
	}
	e.ruleOutcome = count
}

// recordRuleOutcomes reports a tick's skipped rules to the installed counter.
func (e *Evaluator) recordRuleOutcomes(stats Stats) {
	if e.ruleOutcome == nil {
		return
	}
	e.ruleOutcome("deferred", stats.Deferred)
	e.ruleOutcome("not_released", stats.SkippedNotReleased)
}

// groupByAccount splits the enabled rules by account, keeping list order.
func groupByAccount(rules []state.AlertRule) [][]state.AlertRule {
	var out [][]state.AlertRule
	index := map[string]int{}
	for _, rule := range rules {
		i, ok := index[rule.AccountID]
		if !ok {
			i = len(out)
			index[rule.AccountID] = i
			out = append(out, nil)
		}
		out[i] = append(out[i], rule)
	}
	return out
}

// evalAccounts evaluates every account's rules, e.workers accounts at a time,
// and returns each account's counters.
func (e *Evaluator) evalAccounts(ctx context.Context, accounts [][]state.AlertRule, now time.Time) []Stats {
	results := make([]Stats, len(accounts))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range min(max(e.workers, 1), len(accounts)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				results[i] = e.evalAccount(ctx, accounts[i], now)
			}
		}()
	}
feed:
	for i := range accounts {
		select {
		case jobs <- i:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
	return results
}

// evalAccount evaluates one account's rules in order within its tick budget.
// A rule already started always finishes; the budget is checked between rules.
func (e *Evaluator) evalAccount(ctx context.Context, rules []state.AlertRule, now time.Time) Stats {
	var stats Stats
	if len(rules) == 0 {
		return stats
	}
	accountID := rules[0].AccountID
	rules = e.resumeOrder(accountID, rules)
	var release RuleRelease
	if e.release != nil {
		release = e.release(ctx, accountID)
	}
	start := time.Now()
	for k, rule := range rules {
		if ctx.Err() != nil {
			return stats
		}
		if k > 0 && time.Since(start) > e.accountBudget {
			stats.Deferred += len(rules) - k
			e.setResume(accountID, rule.ID)
			e.log.Warn("alerts: account spent its tick budget; deferring its remaining rules",
				"account_id", accountID, "deferred", len(rules)-k, "budget", e.accountBudget)
			return stats
		}
		rctx := ctx
		if release != nil {
			evaluate, channels := release(ctx, rule)
			if !evaluate {
				stats.SkippedNotReleased++
				continue
			}
			if !channels {
				rctx = withoutChannels(ctx)
			}
		}
		e.evalRule(rctx, rule, now, &stats)
	}
	e.setResume(accountID, "")
	return stats
}

// resumeOrder starts an account's rules with the one its last tick deferred,
// so a budget overrun never starves the same tail of rules.
func (e *Evaluator) resumeOrder(accountID string, rules []state.AlertRule) []state.AlertRule {
	e.resumeMu.Lock()
	first := e.resume[accountID]
	e.resumeMu.Unlock()
	if first == "" {
		return rules
	}
	for i, rule := range rules {
		if rule.ID == first && i > 0 {
			return append(append(make([]state.AlertRule, 0, len(rules)), rules[i:]...), rules[:i]...)
		}
	}
	return rules
}

func (e *Evaluator) setResume(accountID, ruleID string) {
	e.resumeMu.Lock()
	defer e.resumeMu.Unlock()
	if ruleID == "" {
		delete(e.resume, accountID)
		return
	}
	if e.resume == nil {
		e.resume = map[string]string{}
	}
	e.resume[accountID] = ruleID
}

type channelsOffKey struct{}

// withoutChannels marks a rule's evaluation as one whose notifications must
// not go to notification channels (the capability is off for the account).
func withoutChannels(ctx context.Context) context.Context {
	return context.WithValue(ctx, channelsOffKey{}, true)
}

func channelsOff(ctx context.Context) bool {
	off, _ := ctx.Value(channelsOffKey{}).(bool)
	return off
}
