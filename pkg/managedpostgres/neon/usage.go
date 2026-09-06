package neon

import (
	"context"
	"math"
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

const consumptionMetrics = "compute_unit_seconds,root_branch_bytes_month,child_branch_bytes_month,instant_restore_bytes_month,snapshot_storage_bytes_month,public_network_transfer_bytes,private_network_transfer_bytes"

type consumptionMetric struct {
	Name  string `json:"metric_name"`
	Value int64  `json:"value"`
}

type consumptionTimeframe struct {
	Metrics []consumptionMetric `json:"metrics"`
}

type consumptionPeriod struct {
	Consumption []consumptionTimeframe `json:"consumption"`
}

type projectConsumption struct {
	ProjectID string              `json:"project_id"`
	Periods   []consumptionPeriod `json:"periods"`
}

type consumptionResponse struct {
	Projects   []projectConsumption `json:"projects"`
	Pagination struct {
		Cursor string `json:"cursor"`
	} `json:"pagination"`
}

func (p *Provider) Usage(ctx context.Context, providerResourceID string, window managedpostgres.UsageWindow) (managedpostgres.Usage, error) {
	ref, err := parseResourceRef(providerResourceID)
	if err != nil || window.From.IsZero() || !window.To.After(window.From) {
		return managedpostgres.Usage{}, managedpostgres.ErrInvalid
	}
	// Neon exposes consumption at project scope. A restored target is a
	// branch inside its source project, so returning the project total here
	// would double-count it alongside the source database. Keep the target
	// unmetered until Neon exposes an allocatable branch-level breakdown.
	if ref.branchID != "" {
		return managedpostgres.Usage{}, managedpostgres.ErrUnsupported
	}
	granularity, err := usageGranularity(window)
	if err != nil {
		return managedpostgres.Usage{}, err
	}
	query := url.Values{
		"project_ids": {ref.projectID},
		"from":        {window.From.UTC().Format(time.RFC3339)},
		"to":          {window.To.UTC().Format(time.RFC3339)},
		"granularity": {granularity},
		"org_id":      {p.organizationID},
		"metrics":     {consumptionMetrics},
		"limit":       {"1"},
	}
	var response consumptionResponse
	if err := p.doJSON(ctx, http.MethodGet, "/consumption_history/v2/projects", query, nil, &response, http.StatusOK); err != nil {
		return managedpostgres.Usage{}, err
	}
	if response.Pagination.Cursor != "" || len(response.Projects) != 1 || response.Projects[0].ProjectID != ref.projectID {
		if len(response.Projects) == 0 {
			return managedpostgres.Usage{}, managedpostgres.ErrNotFound
		}
		return managedpostgres.Usage{}, managedpostgres.ErrUnavailable
	}
	compute, storage, history, egress, err := sumConsumption(response.Projects[0])
	if err != nil {
		return managedpostgres.Usage{}, err
	}
	usage := managedpostgres.Usage{
		Window: window,
		Readings: []managedpostgres.MeterReading{
			{Meter: managedpostgres.MeterComputeUnitSeconds, Quantity: compute},
			{Meter: managedpostgres.MeterStorageByteSeconds, Quantity: storage},
			{Meter: managedpostgres.MeterHistoryByteSeconds, Quantity: history},
			{Meter: managedpostgres.MeterEgressBytes, Quantity: egress},
		},
	}
	if err := usage.Validate(); err != nil {
		return managedpostgres.Usage{}, managedpostgres.ErrUnavailable
	}
	return usage, nil
}

func usageGranularity(window managedpostgres.UsageWindow) (string, error) {
	from, to := window.From.UTC(), window.To.UTC()
	duration := to.Sub(from)
	if duration <= 168*time.Hour && from.Equal(from.Truncate(time.Hour)) && to.Equal(to.Truncate(time.Hour)) {
		return "hourly", nil
	}
	if duration <= 60*24*time.Hour && atUTCMidnight(from) && atUTCMidnight(to) {
		return "daily", nil
	}
	if duration <= 366*24*time.Hour && firstOfUTCMonth(from) && firstOfUTCMonth(to) {
		return "monthly", nil
	}
	return "", managedpostgres.ErrUnsupported
}

func atUTCMidnight(value time.Time) bool {
	return value.Hour() == 0 && value.Minute() == 0 && value.Second() == 0 && value.Nanosecond() == 0
}

func firstOfUTCMonth(value time.Time) bool {
	return atUTCMidnight(value) && value.Day() == 1
}

func sumConsumption(project projectConsumption) (int64, int64, int64, int64, error) {
	var compute int64
	var storageByteHours int64
	var historyByteHours int64
	var egress int64
	for _, period := range project.Periods {
		for _, timeframe := range period.Consumption {
			for _, metric := range timeframe.Metrics {
				if metric.Value < 0 {
					return 0, 0, 0, 0, managedpostgres.ErrUnavailable
				}
				var err error
				switch metric.Name {
				case "compute_unit_seconds":
					compute, err = addQuantity(compute, metric.Value)
				case "root_branch_bytes_month", "child_branch_bytes_month":
					storageByteHours, err = addQuantity(storageByteHours, metric.Value)
				case "instant_restore_bytes_month", "snapshot_storage_bytes_month":
					historyByteHours, err = addQuantity(historyByteHours, metric.Value)
				case "public_network_transfer_bytes", "private_network_transfer_bytes":
					egress, err = addQuantity(egress, metric.Value)
				}
				if err != nil {
					return 0, 0, 0, 0, err
				}
			}
		}
	}
	storage, err := byteHoursToSeconds(storageByteHours)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	history, err := byteHoursToSeconds(historyByteHours)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	return compute, storage, history, egress, nil
}

// Neon reports storage line items in byte-hours even though the metric names
// use a billing-oriented *_bytes_month suffix. Gregale's canonical meter is
// byte-seconds, so convert only after summing all provider timeframes and
// reject overflow instead of recording a wrapped quantity.
func byteHoursToSeconds(value int64) (int64, error) {
	if value < 0 || value > math.MaxInt64/int64(time.Hour/time.Second) {
		return 0, managedpostgres.ErrUnavailable
	}
	return value * int64(time.Hour/time.Second), nil
}

func addQuantity(total, value int64) (int64, error) {
	if value > math.MaxInt64-total {
		return 0, managedpostgres.ErrUnavailable
	}
	return total + value, nil
}
