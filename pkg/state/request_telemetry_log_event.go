package state

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// RequestTelemetryLogStore commits an HTTP telemetry row and its customer log
// projection together. The gateway's stable event ID is the replay key for
// both rows; a replay must not increment telemetry or append a second log.
type RequestTelemetryLogStore interface {
	InsertRequestTelemetryWithLogEvent(context.Context, sqlc.InsertRequestTelemetryParams, string) error
}

func requestTelemetryLogEvent(arg sqlc.InsertRequestTelemetryParams, eventID string) (LogEvent, error) {
	parsedEventID, err := uuid.Parse(eventID)
	if err != nil {
		return LogEvent{}, fmt.Errorf("state: HTTP log event id must be a UUID: %w", err)
	}
	if !arg.AccountID.Valid || !arg.AppID.Valid || !arg.DeploymentID.Valid ||
		!arg.ReceivedAt.Valid || arg.ReceivedAt.Time.IsZero() || arg.Count < 1 {
		return LogEvent{}, fmt.Errorf("state: HTTP log event requires tenant, deployment, timestamp, and positive count")
	}
	accountID := uuid.UUID(arg.AccountID.Bytes).String()
	appID := uuid.UUID(arg.AppID.Bytes).String()
	deploymentID := uuid.UUID(arg.DeploymentID.Bytes).String()
	route := strings.TrimPrefix(arg.Route, arg.Method+" ")
	level := "info"
	if arg.Status >= 500 {
		level = "error"
	} else if arg.Status >= 400 {
		level = "warn"
	}
	latency := int(arg.LatencyMs)
	traceID := ""
	if arg.TraceID.Valid {
		traceID = arg.TraceID.String
	}
	instanceID := ""
	if arg.InstanceID.Valid {
		instanceID = arg.InstanceID.String
	}
	event := LogEvent{
		OccurredAt:    arg.ReceivedAt.Time,
		AccountID:     accountID,
		AppID:         appID,
		DeploymentID:  deploymentID,
		InstanceID:    instanceID,
		Source:        LogEventSourceHTTP,
		SourceEventID: parsedEventID.String(),
		RequestID:     traceID,
		TraceID:       traceID,
		Route:         route,
		Method:        arg.Method,
		Status:        int(arg.Status),
		Level:         level,
		Stream:        "access",
		Message:       fmt.Sprintf("%s %s returned %d in %dms", arg.Method, route, arg.Status, arg.LatencyMs),
		LatencyMS:     &latency,
		Occurrences:   int(arg.Count),
		ColdBoot:      arg.ColdBoot,
	}
	return normalizeLogEventForInsert(event, time.Now())
}
