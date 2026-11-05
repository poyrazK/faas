package alerts

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// observeLogSearch evaluates log_search_matches (ADR-20261018083536): the
// summed occurrences of the app's log events matching the rule's stored
// filters within the window, read with the same bounded facet query a log
// search uses. A query that fails or times out leaves the rule unknown
// rather than reading as zero matches.
func (e *Evaluator) observeLogSearch(ctx context.Context, rule state.AlertRule) (float64, bool, string) {
	ledger, ok := e.store.(state.LogEventStore)
	if !ok || rule.AppID == "" {
		return 0, false, skipDegraded
	}
	_, filter, err := state.CanonicalLogQuery(rule.LogQuery)
	if err != nil {
		e.log.Warn("alerts: invalid log_query", "rule", rule.ID, "err", err)
		return 0, false, skipDegraded
	}
	now := e.now()
	filter.AccountID, filter.AppID = rule.AccountID, rule.AppID
	filter.Since, filter.Until, filter.Timeout = e.windowStart(rule.WindowSpec, now), now, api.LogSearchStatementTimeout
	facets, err := ledger.FacetLogEvents(ctx, filter, 1)
	if err != nil {
		e.log.Warn("alerts: log search count", "rule", rule.ID, "err", err)
		return 0, false, skipDegraded
	}
	observed := float64(facets.Total)
	return observed, compareFloat(observed, rule.Comparison, rule.Threshold), ""
}
