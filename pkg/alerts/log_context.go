package alerts

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// SetLogContext turns on the ADR-20261022172052 log context for firing app
// alerts of the accounts released reports true for: meterd passes the
// log-search capability flag (ADR-20261024222102). nil turns it off. A nil
// evaluator (alerting not configured) ignores it, as SetChannels does.
func (e *Evaluator) SetLogContext(released func(ctx context.Context, accountID, appID string) bool) {
	if e == nil {
		return
	}
	e.logContextReleased = released
}

// logContext is what the app's log ledger shows around a firing alert.
type logContext struct {
	Since, Until    time.Time
	Total           int64
	Errors          int64 // error and fatal
	Warnings        int64
	ServerErrors    int64 // HTTP 5xx
	ErrorRoute      string
	ErrorRouteCount int64
	Deploys         []state.LogEvent
	SearchPath      string
}

// readLogContext reads the ledger for the rule window (capped) and the
// deploys of the hour before it. Any failure omits the context: an alert
// never waits on or fails for it.
func (e *Evaluator) readLogContext(ctx context.Context, rule state.AlertRule, slug string, now time.Time) (logContext, bool) {
	if e.logContextReleased == nil || rule.AppID == "" || slug == "" || !e.logContextReleased(ctx, rule.AccountID, rule.AppID) {
		return logContext{}, false
	}
	ledger, ok := e.store.(state.LogEventStore)
	if !ok {
		return logContext{}, false
	}
	since := e.windowStart(rule.WindowSpec, now)
	if now.Sub(since) > api.AlertLogContextMaxWindow {
		since = now.Add(-api.AlertLogContextMaxWindow)
	}
	base := state.LogEventFilter{AccountID: rule.AccountID, AppID: rule.AppID, Since: since, Until: now, Timeout: api.LogSearchStatementTimeout}
	all, err := ledger.FacetLogEvents(ctx, base, 1)
	if err != nil {
		e.log.Debug("alerts: log context counts", "rule", rule.ID, "err", err)
		return logContext{}, false
	}
	lc := logContext{Since: since, Until: now, Total: all.Total}
	for _, level := range all.Levels {
		switch level.Value {
		case "error", "fatal":
			lc.Errors += level.Count
		case "warn":
			lc.Warnings += level.Count
		}
	}
	for _, class := range all.StatusClasses {
		if class.Value == "5xx" {
			lc.ServerErrors = class.Count
		}
	}
	if lc.Errors > 0 {
		errorsOnly := base
		errorsOnly.Levels = []string{"error", "fatal"}
		if errs, err := ledger.FacetLogEvents(ctx, errorsOnly, 1); err == nil && len(errs.Routes) > 0 {
			lc.ErrorRoute, lc.ErrorRouteCount = errs.Routes[0].Value, errs.Routes[0].Count
		}
	}
	deploys := state.LogEventFilter{
		AccountID: rule.AccountID, AppID: rule.AppID, Source: state.LogEventSourceDeploy,
		Since: since.Add(-api.AlertLogContextDeployLookback), Until: now, Limit: api.AlertLogContextDeploysMax,
		Timeout: api.LogSearchStatementTimeout,
	}
	if events, _, err := ledger.ListLogEvents(ctx, deploys); err == nil {
		lc.Deploys = events
	}
	q := url.Values{"since": {since.UTC().Format(time.RFC3339)}, "until": {now.UTC().Format(time.RFC3339)}}
	if lc.Errors > 0 {
		q.Set("level", "error,fatal")
	}
	lc.SearchPath = "/dashboard/apps/" + url.PathEscape(slug) + "/log-search?" + q.Encode()
	return lc, true
}

// payload is the webhook's log_context object.
func (lc logContext) payload() map[string]any {
	deploys := make([]map[string]any, 0, len(lc.Deploys))
	for _, d := range lc.Deploys {
		deploys = append(deploys, map[string]any{"at": d.OccurredAt.UTC().Format(time.RFC3339), "level": d.Level, "message": d.Message, "deployment_id": d.DeploymentID})
	}
	m := map[string]any{
		"since": lc.Since.UTC().Format(time.RFC3339), "until": lc.Until.UTC().Format(time.RFC3339),
		"total": lc.Total, "errors": lc.Errors, "warnings": lc.Warnings, "http_5xx": lc.ServerErrors,
		"recent_deploys": deploys, "search_path": lc.SearchPath,
	}
	if lc.ErrorRoute != "" {
		m["busiest_error_route"] = map[string]any{"route": lc.ErrorRoute, "count": lc.ErrorRouteCount}
	}
	return m
}

// notes are the notification lines; baseURL turns the search path into a
// link when the dashboard URL is configured.
func (lc logContext) notes(baseURL string) []string {
	line := fmt.Sprintf("Logs in the window: %d errors, %d warnings, %d HTTP 5xx", lc.Errors, lc.Warnings, lc.ServerErrors)
	if lc.ErrorRoute != "" {
		line += fmt.Sprintf(" (most errors on %s: %d)", lc.ErrorRoute, lc.ErrorRouteCount)
	}
	notes := []string{line}
	for _, d := range lc.Deploys {
		notes = append(notes, fmt.Sprintf("%s at %s", strings.TrimSpace(d.Message), d.OccurredAt.UTC().Format("15:04 UTC")))
	}
	if baseURL != "" {
		notes = append(notes, "Search the logs: "+baseURL+lc.SearchPath)
	}
	return notes
}
