package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type routeHealthNotificationState struct {
	ContextKey        string
	Status            string
	BlockedDecisionID string
}

type routeHealthNotification struct {
	State   routeHealthNotificationState
	Event   AppWebhookEvent
	Payload []byte
}

// A single state per deployment outlives bounded history pruning. Retries and
// moving telemetry windows do not reopen a hold. A configuration/stage/stable
// change resets the comparison; unknown never clears a confirmed regression.
func routeHealthTransition(previous routeHealthNotificationState, entry api.RouteHealthHistoryEntry, slug string) (routeHealthNotification, error) {
	key, err := routeHealthNotificationContext(entry)
	if err != nil {
		return routeHealthNotification{}, err
	}
	state := previous
	if state.ContextKey != key {
		state = routeHealthNotificationState{ContextKey: key, Status: "clear"}
	}
	next := routeHealthNotification{State: state}
	if state.Status == "aborted" {
		return next, nil
	}
	if entry.Decision.Mode != "enforce" {
		next.State.Status, next.State.BlockedDecisionID = "clear", ""
		return next, nil
	}
	switch entry.Decision.Status {
	case "aborted":
		next.State.Status, next.State.BlockedDecisionID = "aborted", ""
		next.Event = AppWebhookEventRouteHealthAborted
	case "blocked":
		if state.Status == "clear" || state.Status == "blocked_unknown" && entry.Report.Status == "regressed" {
			next.State.Status = "blocked_unknown"
			if entry.Report.Status == "regressed" {
				next.State.Status = "blocked_regressed"
			}
			next.State.BlockedDecisionID = entry.ID
			next.Event = AppWebhookEventRouteHealthBlocked
		}
	case "allowed":
		if entry.Report.Status != "healthy" {
			return next, nil
		}
		next.State.Status, next.State.BlockedDecisionID = "clear", ""
		if state.Status != "clear" {
			next.Event = AppWebhookEventRouteHealthResumed
		}
	}
	if next.Event == "" {
		return next, nil
	}
	status := "blocked"
	blockedID := ""
	if next.Event == AppWebhookEventRouteHealthResumed {
		status, blockedID = "resumed", state.BlockedDecisionID
	}
	if next.Event == AppWebhookEventRouteHealthAborted {
		status, blockedID = "aborted", state.BlockedDecisionID
	}
	payload := api.RouteHealthTransitionWebhookPayload{Version: api.RouteHealthTransitionVersion, AppID: entry.Report.AppID,
		DeploymentID: entry.Report.DeploymentID, StableDeploymentID: entry.Report.StableDeploymentID, DecisionID: entry.ID,
		BlockedDecisionID: blockedID, Status: status, HealthStatus: entry.Report.Status, Reason: entry.Decision.Reason, Source: entry.Source,
		CanaryStep: entry.Report.CanaryStep, Revision: entry.Report.Revision, ObservationAnchor: entry.Report.ObservationAnchor,
		CheckedAt: entry.CheckedAt, PreviousTrafficPercent: entry.TrafficPercent, RequestedTrafficPercent: entry.RequestedTrafficPercent,
		HistoryPath: "/v1/apps/" + url.PathEscape(slug) + "/route-health/deployments/" + entry.Report.DeploymentID + "/history/" + entry.ID}
	next.Payload, err = json.Marshal(payload)
	if err != nil {
		return next, fmt.Errorf("encode route health notification: %w", err)
	}
	return next, nil
}

func routeHealthNotificationContext(entry api.RouteHealthHistoryEntry) (string, error) {
	context := struct {
		DeploymentID string                          `json:"deployment_id"`
		StableID     string                          `json:"stable_id"`
		Mode         string                          `json:"mode"`
		Step         int                             `json:"step"`
		Revision     int64                           `json:"revision"`
		Anchor       *time.Time                      `json:"anchor"`
		Policy       api.RouteHealthEvaluationPolicy `json:"policy"`
	}{entry.Report.DeploymentID, entry.Report.StableDeploymentID, entry.Report.Mode, entry.Report.CanaryStep, entry.Report.Revision, entry.Report.ObservationAnchor, entry.Policy}
	body, err := json.Marshal(context)
	if err != nil {
		return "", fmt.Errorf("encode route health notification context: %w", err)
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}
