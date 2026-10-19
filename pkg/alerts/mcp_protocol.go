package alerts

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/state"
)

// mcpProtocolExpressions shares exactly the same population between a metric
// and its traffic gate. Latency uses measured executions, not all RPC outcomes.
func mcpProtocolExpressions(rule state.AlertRule) (string, string) {
	selector := fmt.Sprintf(`{app_id=%q}`, rule.AppID)
	window := string(rule.WindowSpec)
	if string(rule.Metric) == api.MetricMCPProtocolP95 {
		count := `sum(increase(vmmd_mcp_protocol_execution_seconds_count` + selector + `[` + window + `]))`
		value := `1000 * histogram_quantile(0.95,sum by (le) (increase(vmmd_mcp_protocol_execution_seconds_bucket` + selector + `[` + window + `])))`
		return value, count
	}
	count := `sum(increase(vmmd_mcp_protocol_requests_total` + selector + `[` + window + `]))`
	errors := fmt.Sprintf(`sum(increase(vmmd_mcp_protocol_requests_total{app_id=%q,outcome=~"rpc_error|tool_error|authorization_error|internal_error|timeout"}[%s]))`, rule.AppID, window)
	return `100 * (` + errors + `) / (` + count + `)`, count
}
func mcpProtocolSustainedQuery(rule state.AlertRule, value, count string) string {
	operator := ">"
	if rule.Comparison == state.AlertGte {
		operator = ">="
	}
	verdict := `((` + value + `) ` + operator + ` bool ` + strconv.FormatFloat(rule.Threshold, 'g', -1, 64) + `) and on() ((` + count + `) >= ` + strconv.Itoa(rule.MCPMinCalls) + `)`
	// Subqueries include both endpoints when evaluation is step-aligned.
	// Require the exact expected sample count for aligned and unaligned clocks,
	// so missing observations cannot bridge a breach.
	history := `(` + verdict + `)[` + strconv.Itoa(rule.MCPForMinutes) + `m:30s]`
	return `min_over_time(` + history + `) == 1 and on() (count_over_time(` + history + `) >= ` + strconv.Itoa(rule.MCPForMinutes*2) + ` + (time() % 30 == bool 0))`
}
func (e *Evaluator) observeMCPProtocol(ctx context.Context, rule state.AlertRule) (float64, bool, string) {
	if api.ValidateMCPProtocolAlert(string(rule.Metric), string(rule.Comparison), string(rule.WindowSpec), rule.Threshold, rule.MCPMinCalls, rule.MCPForMinutes) != nil || !api.AlertRuleActionAllowedForMetric(string(rule.Metric), string(rule.Action)) || e.promQL == nil {
		return 0, false, skipDegraded
	}
	if _, err := uuid.Parse(rule.AppID); err != nil {
		return 0, false, skipNoIdentity
	}
	app, err := e.store.AppByID(ctx, rule.AppID)
	if err != nil || app.AccountID != rule.AccountID || app.Status == state.AppDeleted {
		return 0, false, skipNoIdentity
	}
	readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	valueExpr, countExpr := mcpProtocolExpressions(rule)
	count, err := e.promQL.QueryScalar(readCtx, countExpr)
	if errors.Is(err, promql.ErrNoData) {
		return 0, false, skipInsufficient
	}
	if err != nil || math.IsNaN(count) || math.IsInf(count, 0) || count < 0 {
		return 0, false, skipDegraded
	}
	if count < float64(rule.MCPMinCalls) {
		return 0, false, skipInsufficient
	}
	observed, err := e.promQL.QueryScalar(readCtx, `(`+valueExpr+`) and on() ((`+countExpr+`) >= `+strconv.Itoa(rule.MCPMinCalls)+`)`)
	if errors.Is(err, promql.ErrNoData) {
		return 0, false, skipInsufficient
	}
	if err != nil || !api.IsFiniteFloat(observed) || observed < 0 {
		return 0, false, skipDegraded
	}
	if !compareFloat(observed, rule.Comparison, rule.Threshold) {
		return observed, false, ""
	}
	if !rule.CreatedAt.IsZero() && e.now().Sub(rule.CreatedAt) < time.Duration(rule.MCPForMinutes)*time.Minute {
		return observed, false, skipInsufficient
	}
	sustained, err := e.promQL.QueryScalar(readCtx, mcpProtocolSustainedQuery(rule, valueExpr, countExpr))
	if errors.Is(err, promql.ErrNoData) {
		return observed, false, skipInsufficient
	}
	if err != nil || !api.IsFiniteFloat(sustained) {
		return 0, false, skipDegraded
	}
	if sustained != 1 {
		return observed, false, skipInsufficient
	}
	return observed, true, ""
}
