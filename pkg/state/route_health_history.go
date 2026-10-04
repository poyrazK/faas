package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routehealth"
)

type RouteHealthHistoryStore interface {
	ListRouteHealthHistory(context.Context, string, string, string, int, string) (api.RouteHealthHistoryPage, error)
	GetRouteHealthHistoryEntry(context.Context, string, string, string, string) (api.RouteHealthHistoryEntry, error)
}

type routeHealthStoredDecision struct {
	Key       string
	Body      []byte
	CheckedAt time.Time
	ID        string
}

func buildRouteHealthHistory(report api.RouteHealthReport, d Deployment, params CanaryAdvanceParams) (api.RouteHealthHistoryEntry, routeHealthStoredDecision, error) {
	if len(report.Routes) == 0 {
		return api.RouteHealthHistoryEntry{}, routeHealthStoredDecision{}, nil
	}
	source, requested := "manual", params.TrafficPercent
	if params.RequireSafeReleaseLease {
		source = "worker"
	}
	if params.ExpectedStep+1 >= d.CanaryTotalSteps-1 {
		requested = 100
	}
	entry := api.RouteHealthHistoryEntry{Version: api.RouteHealthHistoryVersion, CheckedAt: report.CheckedAt, Source: source,
		TrafficPercent: d.TrafficPercent, RequestedTrafficPercent: requested, Policy: routehealth.HistoryPolicy(), Decision: routehealth.Decision(report), Report: report}
	return encodeRouteHealthHistory(entry)
}

func buildRouteHealthAbortHistory(report api.RouteHealthReport, d Deployment) (api.RouteHealthHistoryEntry, routeHealthStoredDecision, error) {
	entry := api.RouteHealthHistoryEntry{Version: api.RouteHealthHistoryVersion, Purpose: "abort", CheckedAt: report.CheckedAt, Source: "worker",
		TrafficPercent: d.TrafficPercent, RequestedTrafficPercent: 0, Policy: routehealth.HistoryPolicy(), Decision: routehealth.AbortDecision(report), Report: report}
	return encodeRouteHealthHistory(entry)
}

func encodeRouteHealthHistory(entry api.RouteHealthHistoryEntry) (api.RouteHealthHistoryEntry, routeHealthStoredDecision, error) {
	// Preserve every evidence field in the key, except the wall clock of the
	// retry. Window bounds, anchors, intent, counts and percentiles remain pinned.
	keyEntry := entry
	keyEntry.CheckedAt, keyEntry.Decision.CheckedAt, keyEntry.Report.CheckedAt = time.Time{}, time.Time{}, time.Time{}
	canonical, err := json.Marshal(keyEntry)
	if err != nil {
		return entry, routeHealthStoredDecision{}, fmt.Errorf("encode route health decision key: %w", err)
	}
	digest := sha256.Sum256(canonical)
	entry.ID = uuid.NewString()
	entry.Decision.HistoryID = entry.ID
	body, err := json.Marshal(entry)
	if err != nil {
		return entry, routeHealthStoredDecision{}, fmt.Errorf("encode route health decision evidence: %w", err)
	}
	if len(body) > api.RouteHealthHistoryEntryMaxBytes {
		return entry, routeHealthStoredDecision{}, fmt.Errorf("route health evidence exceeds history limit: %w", ErrInvalidArgument)
	}
	return entry, routeHealthStoredDecision{Key: hex.EncodeToString(digest[:]), Body: body, CheckedAt: entry.CheckedAt, ID: entry.ID}, nil
}

func decodeRouteHealthHistory(body []byte, appID, deploymentID string) (api.RouteHealthHistoryEntry, error) {
	var entry api.RouteHealthHistoryEntry
	if err := json.Unmarshal(body, &entry); err != nil {
		return entry, fmt.Errorf("decode route health history: %w", err)
	}
	id, err := uuid.Parse(entry.ID)
	if err != nil || id.String() != entry.ID || entry.Version != api.RouteHealthHistoryVersion || entry.Policy.Version < 1 ||
		entry.Report.AppID != appID || entry.Report.DeploymentID != deploymentID || entry.Decision.DeploymentID != deploymentID ||
		entry.Decision.HistoryID != entry.ID || entry.CheckedAt.IsZero() || !entry.CheckedAt.Equal(entry.Report.CheckedAt) || !entry.CheckedAt.Equal(entry.Decision.CheckedAt) ||
		entry.Source != "manual" && entry.Source != "worker" {
		return entry, fmt.Errorf("invalid route health history identity: %w", ErrInvalidArgument)
	}
	return entry, nil
}

func validateRouteHealthHistoryPage(limit int, before string) error {
	if limit < 1 || limit > api.RouteHealthHistoryMaxPage {
		return ErrInvalidArgument
	}
	if before != "" {
		if id, err := uuid.Parse(before); err != nil || id.String() != before {
			return ErrInvalidArgument
		}
	}
	return nil
}
