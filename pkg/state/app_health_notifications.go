package state

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type appHealthNotificationState struct {
	BaselineStatus string                        `json:"baseline_status"`
	LastNotifiedAt *time.Time                    `json:"last_notified_at,omitempty"`
	Pending        *appHealthPendingNotification `json:"pending,omitempty"`
}

type appHealthPendingNotification struct {
	EntryID    string            `json:"entry_id"`
	ObservedAt string            `json:"observed_at"`
	Recipients map[string]string `json:"recipients"`
	Coalesced  bool              `json:"coalesced"`
}

type appHealthNotification struct {
	State      appHealthNotificationState
	StateJSON  []byte
	SourceID   string
	Payload    []byte
	Recipients []string
}

// Keep a comparison independently of history retention. Cooldown changes are
// coalesced to the latest observed status; returning to baseline cancels them.
// Recipients are captured at the change, so a late subscription cannot receive
// a historical pending transition. The caller commits all outputs atomically.
func prepareAppHealthNotification(state appHealthNotificationState, previous *api.AppHealthResponse, a api.AppHealthResponse, entries []api.AppHealthHistoryEntry, recipients map[string]string, slug string, now time.Time) (appHealthNotification, error) {
	next := appHealthNotification{State: cloneAppHealth(state)}
	if len(recipients) > api.AppHealthNotificationRecipients {
		return next, ErrInvalidArgument
	}
	if previous == nil || state.BaselineStatus == "" || slices.ContainsFunc(entries, func(e api.AppHealthHistoryEntry) bool { return e.Kind == "gap" }) {
		next.State.BaselineStatus, next.State.Pending = a.Status, nil
		return encodeAppHealthNotification(next)
	}
	if previous.Status != a.Status {
		if len(recipients) == 0 || a.Status == state.BaselineStatus {
			next.State.BaselineStatus, next.State.Pending = a.Status, nil
			return encodeAppHealthNotification(next)
		}
		if len(entries) == 0 {
			return next, ErrInvalidArgument
		}
		e := entries[len(entries)-1]
		next.State.Pending = &appHealthPendingNotification{EntryID: e.ID, ObservedAt: e.ObservedAt, Recipients: maps.Clone(recipients), Coalesced: state.Pending != nil}
	}
	pending := next.State.Pending
	if pending == nil {
		return encodeAppHealthNotification(next)
	}
	// Respect opt-out before a delayed event is committed; never add recipients.
	for id, revision := range pending.Recipients {
		if current, ok := recipients[id]; ok && current == revision {
			next.Recipients = append(next.Recipients, id)
		}
	}
	slices.Sort(next.Recipients)
	if len(next.Recipients) == 0 {
		next.State.BaselineStatus, next.State.Pending = a.Status, nil
		return encodeAppHealthNotification(next)
	}
	if state.LastNotifiedAt != nil && now.Before(state.LastNotifiedAt.Add(api.AppHealthNotificationCooldown)) {
		pending.Coalesced = true
		next.Recipients = nil
		return encodeAppHealthNotification(next)
	}
	payload := api.AppHealthChangedWebhookPayload{Version: 1, AppID: a.AppID, Scope: a.Scope, TransitionID: pending.EntryID,
		TransitionObservedAt: pending.ObservedAt, PreviousStatus: state.BaselineStatus, Status: a.Status,
		Change: appHealthChange(state.BaselineStatus, a.Status), Phase: a.Phase, EvaluatedAt: a.EvaluatedAt,
		QueuedAt: now.UTC().Format(time.RFC3339Nano), Coalesced: pending.Coalesced, CooldownSeconds: int(api.AppHealthNotificationCooldown / time.Second),
		LatestDeploymentID: a.LatestDeploymentID, ServingDeploymentIDs: slices.Clone(a.ServingDeploymentIDs),
		HistoryPath: "/v1/apps/" + url.PathEscape(slug) + "/health/history"}
	if payload.ServingDeploymentIDs == nil {
		payload.ServingDeploymentIDs = []string{}
	}
	var err error
	next.Payload, err = json.Marshal(payload)
	if err != nil {
		return next, fmt.Errorf("encode app health notification: %w", err)
	}
	next.SourceID = pending.EntryID
	next.State.BaselineStatus, next.State.LastNotifiedAt, next.State.Pending = a.Status, &now, nil
	return encodeAppHealthNotification(next)
}

func encodeAppHealthNotification(next appHealthNotification) (appHealthNotification, error) {
	body, err := json.Marshal(next.State)
	if err != nil {
		return next, fmt.Errorf("encode app health notification state: %w", err)
	}
	if len(body) > api.AppHealthNotificationStateBytes {
		return next, ErrInvalidArgument
	}
	next.StateJSON = body
	return next, nil
}

func appHealthChange(previous, current string) string {
	if current == "unknown" {
		return "unconfirmed"
	}
	if previous == "unknown" {
		return "confirmed"
	}
	rank := map[string]int{"healthy": 0, "degraded": 1, "unhealthy": 2}
	if rank[current] > rank[previous] {
		return "worsened"
	}
	return "improved"
}
