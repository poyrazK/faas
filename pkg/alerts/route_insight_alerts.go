package alerts

// ADR-20261020161310: route_insights_detected counts the app's route insights
// that became active within the rule's window, and a firing notification
// carries their messages, so an email says what was found.

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// routeInsightNoteMaxBytes bounds one insight message in a notification.
const routeInsightNoteMaxBytes = 500

func (e *Evaluator) observeRouteInsights(ctx context.Context, rule state.AlertRule) (float64, bool, string) {
	store, ok := e.store.(state.RouteInsightAlertStore)
	if !ok || rule.AppID == "" {
		return 0, false, skipDegraded
	}
	_, total, err := store.RouteInsightsDetectedSince(ctx, rule.AppID, e.windowStart(rule.WindowSpec, e.now()), 1)
	if err != nil {
		e.log.Warn("alerts: read route insights", "rule", rule.ID, "err", err)
		return 0, false, skipDegraded
	}
	value := float64(total)
	return value, compareFloat(value, rule.Comparison, rule.Threshold), ""
}

// routeInsightNotes returns the messages of the newest insights a firing
// route_insights_detected rule counted. A read failure leaves the
// notification without notes rather than failing it.
func (e *Evaluator) routeInsightNotes(ctx context.Context, rule state.AlertRule) []string {
	store, ok := e.store.(state.RouteInsightAlertStore)
	if !ok || rule.AppID == "" {
		return nil
	}
	found, _, err := store.RouteInsightsDetectedSince(ctx, rule.AppID, e.windowStart(rule.WindowSpec, e.now()), api.RouteInsightAlertNotes)
	if err != nil {
		e.log.Warn("alerts: read route insight notes", "rule", rule.ID, "err", err)
		return nil
	}
	notes := make([]string, 0, len(found))
	for _, f := range found {
		note := f.Message
		if len(note) > routeInsightNoteMaxBytes {
			note = truncateUTF8(note, routeInsightNoteMaxBytes-len("…")) + "…"
		}
		notes = append(notes, note)
	}
	return notes
}

// truncateUTF8 cuts s to at most n bytes without splitting a rune.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && (s[n]&0xC0) == 0x80 {
		n--
	}
	return s[:n]
}
