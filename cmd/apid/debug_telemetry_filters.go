package main

// debug_telemetry_filters.go keeps request-list filter parsing and cursor
// matching in one place.  The list endpoint is intentionally strict: a
// cursor is only valid when every filter that shaped the original page is
// supplied again, so a page walk cannot silently change its result set.

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const debugTelemetryAnonymousConsumer = "__anonymous__"

// debugTelemetryFilters is the normalized, server-side representation of
// request-list filters.  Zero values mean "not filtered" except for
// cold_boot, which uses a pointer so false remains distinguishable from an
// omitted parameter.
type debugTelemetryFilters struct {
	DeploymentID string
	Status       int
	ColdBoot     *bool
	ConsumerID   string
	MinLatencyMS int
}

type debugTelemetryCursorFilters struct {
	DeploymentID string
	Status       int
	ColdBoot     *bool
	ConsumerID   string
	MinLatencyMS int
}

func parseDebugTelemetryFilters(values url.Values) (debugTelemetryFilters, error) {
	filters := debugTelemetryFilters{}

	if raw := strings.TrimSpace(values.Get("deployment_id")); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return filters, fmt.Errorf("deployment_id must be a UUID")
		}
		filters.DeploymentID = id.String()
	}

	if raw := strings.TrimSpace(values.Get("status")); raw != "" {
		status, err := strconv.Atoi(raw)
		if err != nil || status < 100 || status > 599 {
			return filters, fmt.Errorf("status must be an integer between 100 and 599")
		}
		filters.Status = status
	}

	if raw := strings.TrimSpace(values.Get("cold_boot")); raw != "" {
		coldBoot, err := strconv.ParseBool(raw)
		if err != nil {
			return filters, fmt.Errorf("cold_boot must be true or false")
		}
		filters.ColdBoot = &coldBoot
	}

	if raw := strings.TrimSpace(values.Get("consumer_id")); raw != "" {
		if raw == debugTelemetryAnonymousConsumer {
			filters.ConsumerID = raw
		} else {
			id, err := uuid.Parse(raw)
			if err != nil {
				return filters, fmt.Errorf("consumer_id must be a UUID or %q", debugTelemetryAnonymousConsumer)
			}
			filters.ConsumerID = id.String()
		}
	}

	if raw := strings.TrimSpace(values.Get("min_latency_ms")); raw != "" {
		latency, err := strconv.Atoi(raw)
		if err != nil || latency < 0 || latency > 86_400_000 {
			return filters, fmt.Errorf("min_latency_ms must be an integer between 0 and 86400000")
		}
		filters.MinLatencyMS = latency
	}

	return filters, nil
}

func (f debugTelemetryFilters) same(other debugTelemetryCursorFilters) bool {
	if f.DeploymentID != other.DeploymentID || f.Status != other.Status ||
		f.ConsumerID != other.ConsumerID || f.MinLatencyMS != other.MinLatencyMS {
		return false
	}
	if f.ColdBoot == nil || other.ColdBoot == nil {
		return f.ColdBoot == nil && other.ColdBoot == nil
	}
	return *f.ColdBoot == *other.ColdBoot
}

func (f debugTelemetryFilters) cursor() debugTelemetryCursorFilters {
	return debugTelemetryCursorFilters(f)
}

func (f debugTelemetryFilters) response() api.DebugTelemetryListFilters {
	return api.DebugTelemetryListFilters{
		DeploymentID: f.DeploymentID,
		Status:       f.Status,
		ColdBoot:     f.ColdBoot,
		ConsumerID:   f.ConsumerID,
		MinLatencyMS: f.MinLatencyMS,
	}
}

// sqlConsumerID returns the UUID value used by the SQL query. Anonymous
// traffic is represented by consumer_id IS NULL instead of a sentinel UUID.
func (f debugTelemetryFilters) sqlConsumerID() string {
	if f.ConsumerID == debugTelemetryAnonymousConsumer {
		return ""
	}
	return f.ConsumerID
}

func (f debugTelemetryFilters) sqlConsumerAnonymous() bool {
	return f.ConsumerID == debugTelemetryAnonymousConsumer
}

func (f debugTelemetryFilters) sqlColdBootFilter() int32 {
	if f.ColdBoot == nil {
		return -1
	}
	if *f.ColdBoot {
		return 1
	}
	return 0
}
