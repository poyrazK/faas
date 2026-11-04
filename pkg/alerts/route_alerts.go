package alerts

// ADR-20261028110044: route_monitor_incidents counts the app's production
// route monitor incidents open within the rule's window, and
// route_health_holds the canaries the route health guard holds now or
// aborted within it. A firing notification names them, so alert channels
// carry what app webhooks carried alone before.

import (
	"context"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (e *Evaluator) observeRouteAlert(ctx context.Context, rule state.AlertRule) (float64, bool, string) {
	total, _, err := e.readRouteAlert(ctx, rule, 1)
	if err != nil {
		e.log.Warn("alerts: read route alert", "rule", rule.ID, "metric", rule.Metric, "err", err)
		return 0, false, skipDegraded
	}
	if total < 0 {
		return 0, false, skipDegraded
	}
	value := float64(total)
	return value, compareFloat(value, rule.Comparison, rule.Threshold), ""
}

// routeAlertNotes describes the newest incidents or holds a firing route
// alert counted. A read failure leaves the notification without notes.
func (e *Evaluator) routeAlertNotes(ctx context.Context, rule state.AlertRule) []string {
	_, notes, err := e.readRouteAlert(ctx, rule, api.RouteAlertNotes)
	if err != nil {
		e.log.Warn("alerts: read route alert notes", "rule", rule.ID, "metric", rule.Metric, "err", err)
		return nil
	}
	return notes
}

// readRouteAlert returns the rule's count (-1 when the store cannot answer)
// and notes for up to limit of the newest matches.
func (e *Evaluator) readRouteAlert(ctx context.Context, rule state.AlertRule, limit int) (int64, []string, error) {
	store, ok := e.store.(state.RouteAlertStore)
	if !ok || rule.AppID == "" {
		return -1, nil, nil
	}
	since := e.windowStart(rule.WindowSpec, e.now())
	if rule.Metric == state.AlertMetricRouteHealthHolds {
		holds, total, err := store.RouteHealthHoldsSince(ctx, rule.AppID, since, limit)
		notes := make([]string, 0, len(holds))
		for _, h := range holds {
			notes = append(notes, routeAlertNote(routeHealthHoldNote(h)))
		}
		return total, notes, err
	}
	incidents, total, err := store.RouteMonitorIncidentsSince(ctx, rule.AppID, since, limit)
	notes := make([]string, 0, len(incidents))
	for _, i := range incidents {
		notes = append(notes, routeAlertNote(routeMonitorIncidentNote(i)))
	}
	return total, notes, err
}

func routeMonitorIncidentNote(i state.RouteMonitorIncidentAlert) string {
	note := fmt.Sprintf("Incident %s (%s, opened %s)", i.ID, i.Status, i.OpenedAt.Format("2006-01-02 15:04 UTC"))
	if i.Method != "" || i.Path != "" {
		note += ": " + strings.TrimSpace(i.Method+" "+i.Path)
		if i.Signal != "" {
			note += " " + i.Signal
		}
	}
	if i.Reason != "" {
		note += " — " + i.Reason
	}
	return note
}

func routeHealthHoldNote(h state.RouteHealthHoldAlert) string {
	verb := "held"
	if h.Status == "aborted" {
		verb = "aborted"
	}
	note := fmt.Sprintf("Canary %s %s by route health (%s)", h.DeploymentID, verb, h.UpdatedAt.Format("2006-01-02 15:04 UTC"))
	if h.Reason != "" {
		note += " — " + h.Reason
	}
	return note
}

// routeAlertNote bounds one note like a route insight message.
func routeAlertNote(note string) string {
	if len(note) > routeInsightNoteMaxBytes {
		return truncateUTF8(note, routeInsightNoteMaxBytes-len("…")) + "…"
	}
	return note
}
