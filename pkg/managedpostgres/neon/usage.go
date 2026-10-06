package neon

import (
	"context"
	"math"
	"net/http"
	"net/url"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

const consumptionMetrics = "compute_unit_seconds,root_branch_bytes_month,child_branch_bytes_month,instant_restore_bytes_month,snapshot_storage_bytes_month,public_network_transfer_bytes,private_network_transfer_bytes"

type consumptionMetric struct {
	Name  string `json:"metric_name"`
	Value *int64 `json:"value"`
}

type consumptionTimeframe struct {
	From    time.Time           `json:"timeframe_start"`
	To      time.Time           `json:"timeframe_end"`
	Metrics []consumptionMetric `json:"metrics"`
}

type consumptionPeriod struct {
	ID          string                 `json:"period_id"`
	From        time.Time              `json:"period_start"`
	To          time.Time              `json:"period_end"`
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
	// Neon exposes consumption at project scope, including root and child
	// branch meters. The control plane records that aggregate once against the
	// source database and skips restore descendants; direct branch reads remain
	// unsupported so callers cannot mistake a project total for branch-only
	// consumption.
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
	// Pagination is over projects, not their timeframes. Neon can return the
	// last project's ID as a cursor even for a single-project filter.
	if len(response.Projects) != 1 || response.Projects[0].ProjectID != ref.projectID {
		if len(response.Projects) == 0 {
			return managedpostgres.Usage{}, managedpostgres.ErrNotFound
		}
		return managedpostgres.Usage{}, managedpostgres.ErrUnavailable
	}
	if err := validateConsumptionCoverage(response.Projects[0], window); err != nil {
		return managedpostgres.Usage{}, err
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

// A zero-valued metric may be omitted by Neon, but absent time coverage does
// not prove zero consumption. Require contiguous, nonduplicated coverage
// before the collector advances its durable checkpoint.
func validateConsumptionCoverage(project projectConsumption, window managedpostgres.UsageWindow) error {
	type interval struct{ from, to time.Time }
	var intervals []interval
	periods := make(map[string]bool, len(project.Periods))
	for _, period := range project.Periods {
		if period.ID == "" || periods[period.ID] || period.From.IsZero() || (!period.To.IsZero() && !period.To.After(period.From)) {
			return managedpostgres.ErrUnavailable
		}
		periods[period.ID] = true
		for _, frame := range period.Consumption {
			if frame.From.IsZero() || !frame.To.After(frame.From) || frame.From.Before(window.From) || frame.To.After(window.To) {
				return managedpostgres.ErrUnavailable
			}
			// A billing-plan change can split a bucket across two periods.
			// Each period's quantities apply only to its share of that bucket.
			from, to := frame.From, frame.To
			if from.Before(period.From) {
				from = period.From
			}
			if !period.To.IsZero() && to.After(period.To) {
				to = period.To
			}
			if !to.After(from) {
				return managedpostgres.ErrUnavailable
			}
			intervals = append(intervals, interval{from, to})
		}
	}
	sort.Slice(intervals, func(i, j int) bool { return intervals[i].from.Before(intervals[j].from) })
	until := window.From
	for _, interval := range intervals {
		if !interval.from.Equal(until) {
			return managedpostgres.ErrUnavailable
		}
		until = interval.to
	}
	if !until.Equal(window.To) {
		return managedpostgres.ErrUnavailable
	}
	return nil
}

func sumConsumption(project projectConsumption) (int64, int64, int64, int64, error) {
	var compute int64
	var storageByteMonths int64
	var historyByteMonths int64
	var egress int64
	for _, period := range project.Periods {
		for _, timeframe := range period.Consumption {
			seen := make(map[string]bool, len(timeframe.Metrics))
			for _, metric := range timeframe.Metrics {
				if metric.Name == "" || seen[metric.Name] || metric.Value == nil || *metric.Value < 0 {
					return 0, 0, 0, 0, managedpostgres.ErrUnavailable
				}
				seen[metric.Name] = true
				value := *metric.Value
				var err error
				switch metric.Name {
				case "compute_unit_seconds":
					compute, err = addQuantity(compute, value)
				case "root_branch_bytes_month", "child_branch_bytes_month":
					storageByteMonths, err = addQuantity(storageByteMonths, value)
				case "instant_restore_bytes_month", "snapshot_storage_bytes_month":
					historyByteMonths, err = addQuantity(historyByteMonths, value)
				case "public_network_transfer_bytes", "private_network_transfer_bytes":
					egress, err = addQuantity(egress, value)
				default:
					return 0, 0, 0, 0, managedpostgres.ErrUnavailable
				}
				if err != nil {
					return 0, 0, 0, 0, err
				}
			}
		}
	}
	storage, err := byteMonthsToSeconds(storageByteMonths)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	history, err := byteMonthsToSeconds(historyByteMonths)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	return compute, storage, history, egress, nil
}

// Neon v2 reports byte-months, normalized to a fixed 744-hour billing month.
// Gregale records byte-seconds; calendar month length does not change this
// provider conversion. Legacy data_storage_bytes_hour uses a different unit.
func byteMonthsToSeconds(value int64) (int64, error) {
	const secondsPerBillingMonth = int64(744 * time.Hour / time.Second)
	if value < 0 || value > math.MaxInt64/secondsPerBillingMonth {
		return 0, managedpostgres.ErrUnavailable
	}
	return value * secondsPerBillingMonth, nil
}

func addQuantity(total, value int64) (int64, error) {
	if value > math.MaxInt64-total {
		return 0, managedpostgres.ErrUnavailable
	}
	return total + value, nil
}
