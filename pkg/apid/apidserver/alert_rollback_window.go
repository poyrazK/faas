package apidserver

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func alertRollbackWindowFrom(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// validateAlertRollbackWindow checks a rule's post-deploy rollback window
// (ADR-604). A window needs action rollback and a rule the deployment
// evidence can judge: an error_rate_pct rule with gt or gte. Any other rule
// would fire and then fail with alert_rollback_metric_unsupported.
func validateAlertRollbackWindow(seconds int, action, metric, comparison, windowSpec string, threshold float64) *api.Problem {
	if seconds < 0 || seconds > api.AlertRollbackMaxWindowSeconds || seconds > 0 && action != "rollback" {
		return api.ErrAlertRuleInvalid(fmt.Sprintf("post_deploy_rollback_window_seconds must be 0 (off) or 1..%d with action=rollback", api.AlertRollbackMaxWindowSeconds))
	}
	if seconds > 0 && !state.HistoricalAlertRollbackSupported(metric, comparison, windowSpec, threshold) {
		return api.ErrAlertRuleInvalid("post_deploy_rollback_window_seconds needs an error_rate_pct rule compared with gt or gte " +
			"(rolling a completed release back is judged on that release's own 5xx rate); set it to 0 on any other rule")
	}
	return nil
}
