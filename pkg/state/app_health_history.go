package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type AppHealthClaim struct {
	AppID, AccountID, Token string
	StartedAt, LeaseUntil   time.Time
}

type AppHealthHistoryStore interface {
	ClaimAppHealth(context.Context, string, time.Time) (AppHealthClaim, error)
	FinishAppHealth(context.Context, AppHealthClaim, api.AppHealthResponse, time.Time) error
	ListAppHealthHistory(context.Context, string, string, int, string, time.Time) (api.AppHealthHistoryPage, error)
	PruneExpiredAppHealthHistory(context.Context, time.Time) (int64, error)
}

type appHealthRecord struct {
	Claim        AppHealthClaim
	NextCheckAt  time.Time
	Latest       *api.AppHealthResponse
	Key          string
	Entries      []api.AppHealthHistoryEntry
	Notification appHealthNotificationState
}

func appHealthEligible(app App) bool {
	mode := app.Manifest.ExecutionMode
	return app.Status != AppDeleted && app.DeletedAt == nil && (mode == "" || mode == "request" || mode == "service")
}

// Ignore moving counters, prose and evidence timestamps. Retain changes to
// assessment meaning, targets and policy; ordering never creates an event.
func appHealthKey(a api.AppHealthResponse) (string, error) {
	parts := []string{a.Status, a.Phase, a.Scope, a.LatestDeploymentID}
	parts = append(parts, fmt.Sprintf("capacity:%t:%d", a.Capacity.Known, a.Capacity.Required))
	ids := slices.Clone(a.ServingDeploymentIDs)
	slices.Sort(ids)
	parts = append(parts, ids...)
	var checks []string
	for _, c := range a.Checks {
		checks = append(checks, fmt.Sprintf("%s:%s:%s:%s:%t", c.Code, c.Status, c.Reason, c.DeploymentID, c.FindingsTruncated))
		for _, f := range c.Findings {
			checks = append(checks, fmt.Sprintf("%s:%s:%s:%s:%s:%s", c.Code, f.Status, f.Reason, f.DeploymentID, f.InstanceID, f.Source))
		}
	}
	slices.Sort(checks)
	parts = append(parts, checks...)
	policy := api.AppHealthRequestPolicy{}
	if a.Requests != nil {
		policy = a.Requests.Policy
	}
	body, err := json.Marshal(struct {
		Parts  []string
		Policy api.AppHealthRequestPolicy
	}{parts, policy})
	if err != nil {
		return "", fmt.Errorf("encode app health fingerprint: %w", err)
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}

func prepareAppHealth(claim AppHealthClaim, a api.AppHealthResponse, now time.Time) (string, []byte, error) {
	at, err := time.Parse(time.RFC3339Nano, a.EvaluatedAt)
	if err != nil || a.AppID != claim.AppID || a.Scope != "default" || at.Before(claim.StartedAt) || at.After(now) || a.ValidForSeconds < 1 || a.ValidForSeconds > int(api.AppHealthEvidenceMaxAge/time.Second) {
		return "", nil, ErrInvalidArgument
	}
	if !slices.Contains([]string{"healthy", "degraded", "unhealthy", "unknown"}, a.Status) {
		return "", nil, ErrInvalidArgument
	}
	body, err := json.Marshal(a)
	if err != nil {
		return "", nil, fmt.Errorf("encode app health assessment: %w", err)
	}
	entryBody, err := json.Marshal(api.AppHealthHistoryEntry{ID: uuid.NewString(), Kind: "transition", ObservedAt: a.EvaluatedAt, PreviousStatus: "unhealthy", Assessment: a})
	if err != nil {
		return "", nil, fmt.Errorf("encode app health entry bound: %w", err)
	}
	if len(entryBody) > api.AppHealthHistoryEntryMaxBytes {
		return "", nil, ErrInvalidArgument
	}
	key, err := appHealthKey(a)
	return key, body, err
}

func appHealthEntries(previous *api.AppHealthResponse, previousKey, key string, a api.AppHealthResponse) []api.AppHealthHistoryEntry {
	entries := []api.AppHealthHistoryEntry{}
	previousStatus, kind := "", "baseline"
	if previous != nil {
		previousStatus, kind = previous.Status, "transition"
		at, _ := time.Parse(time.RFC3339Nano, previous.EvaluatedAt)
		nextAt, _ := time.Parse(time.RFC3339Nano, a.EvaluatedAt)
		expires := at.Add(time.Duration(previous.ValidForSeconds) * time.Second)
		if nextAt.After(expires) {
			gap := api.AppHealthResponse{AppID: a.AppID, Scope: "default", Status: "unknown", Phase: "unknown",
				Summary:     "The previous collection expired before the next observation; health during this gap is unconfirmed.",
				EvaluatedAt: expires.UTC().Format(time.RFC3339Nano), ValidForSeconds: int(api.AppHealthEvidenceMaxAge / time.Second),
				ServingDeploymentIDs: []string{}, Checks: []api.AppHealthCheck{{Code: "collection", Status: "unknown", Reason: "collection_gap", Detail: "No fresh background assessment was recorded for this interval."}}}
			entries = append(entries, api.AppHealthHistoryEntry{ID: uuid.NewString(), Kind: "gap", ObservedAt: gap.EvaluatedAt, PreviousStatus: previousStatus, Assessment: gap})
			previousStatus = "unknown"
		} else if previousKey == key {
			return entries
		}
	}
	return append(entries, api.AppHealthHistoryEntry{ID: uuid.NewString(), Kind: kind, ObservedAt: a.EvaluatedAt, PreviousStatus: previousStatus, Assessment: a})
}

func appHealthPage(appID string, latest *api.AppHealthResponse, now time.Time) api.AppHealthHistoryPage {
	page := api.AppHealthHistoryPage{AppID: appID, Scope: "default", Entries: []api.AppHealthHistoryEntry{}, Latest: latest, IntervalSeconds: int(api.AppHealthCollectorInterval / time.Second)}
	if latest != nil {
		at, err := time.Parse(time.RFC3339Nano, latest.EvaluatedAt)
		page.CollectorFresh = err == nil && !now.Before(at) && now.Sub(at) <= time.Duration(latest.ValidForSeconds)*time.Second
	}
	return page
}

func validateAppHealthPage(limit int, before string) error {
	if limit < 1 || limit > api.AppHealthHistoryMaxPage {
		return ErrInvalidArgument
	}
	if before != "" {
		if id, err := uuid.Parse(before); err != nil || id.String() != before {
			return ErrInvalidArgument
		}
	}
	return nil
}

func cloneAppHealth[T any](value T) T {
	// All persisted values have already passed JSON encoding validation.
	body, _ := json.Marshal(value)
	var out T
	_ = json.Unmarshal(body, &out)
	return out
}
