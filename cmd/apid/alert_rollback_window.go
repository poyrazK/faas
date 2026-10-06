package main

import (
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
)

func alertRollbackWindowFrom(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}
func validateAlertRollbackWindow(seconds int, action string) *api.Problem {
	if seconds < 0 || seconds > api.AlertRollbackMaxWindowSeconds || seconds > 0 && action != "rollback" {
		return api.ErrAlertRuleInvalid(fmt.Sprintf("post_deploy_rollback_window_seconds must be 0 (off) or 1..%d with action=rollback", api.AlertRollbackMaxWindowSeconds))
	}
	return nil
}
