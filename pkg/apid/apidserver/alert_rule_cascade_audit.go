package apidserver

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state"
)

// rulesRemovedWith lists the account's alert rules a delete takes with it:
// deleting an SLO or a synthetic check cascades its rules (ADR-747, ADR-748).
// A failed read only costs the audit trail, never the delete.
func (s *server) rulesRemovedWith(ctx context.Context, acct state.Account, removed func(state.AlertRule) bool) []state.AlertRule {
	rules, err := s.store.ListAlertRulesForAccount(ctx, acct.ID)
	if err != nil {
		s.log.Warn("list alert rules a delete removes", "account", acct.ID, "err", err)
		return nil
	}
	var out []state.AlertRule
	for _, rule := range rules {
		if removed(rule) {
			out = append(out, rule)
		}
	}
	return out
}

// auditRulesRemovedWith records each rule a delete removed, as a direct rule
// delete would, naming what took it (ADR-20261030214500).
func (s *server) auditRulesRemovedWith(ctx context.Context, acct state.Account, rules []state.AlertRule, cause, causeID string) {
	for _, rule := range rules {
		s.audit.Emit(ctx, "alert_rule.deleted", &acct.ID, map[string]any{
			"rule_id":  rule.ID,
			"app_id":   rule.AppID,
			"name":     rule.Name,
			"cause":    cause,
			"cause_id": causeID,
		})
	}
}
