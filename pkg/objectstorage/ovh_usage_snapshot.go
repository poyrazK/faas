package objectstorage

import (
	"context"
	"time"
)

func (c *OVHUsageClient) usageSnapshot(ctx context.Context, from, through time.Time) (OVHUsageSnapshot, error) {
	var detail ovhUsageHistoryDetail
	var err error
	current := from.Equal(utcMonth(time.Now()))
	if current {
		err = c.getJSON(ctx, "/cloud/project/"+c.ServiceName+"/usage/current", nil, &detail)
	} else {
		detail, err = c.closedUsage(ctx, from, through)
	}
	if err != nil {
		return OVHUsageSnapshot{}, err
	}
	end := from.AddDate(0, 1, 0)
	if !detail.Period.From.Equal(from) || current && detail.Period.To.Before(detail.LastUpdate) || detail.Period.To.After(end) || !detail.Period.To.After(from) || detail.LastUpdate.IsZero() || detail.LastUpdate.Before(from) || detail.LastUpdate.After(time.Now()) {
		return OVHUsageSnapshot{}, ErrOVHUsageInvalid
	}
	if detail.TotalPrice == nil || detail.TotalPrice.CurrencyCode != "EUR" || detail.TotalPrice.PriceInUcents == nil || *detail.TotalPrice.PriceInUcents < 0 || detail.HourlyUsage == nil || detail.HourlyUsage.Storage == nil {
		return OVHUsageSnapshot{}, ErrOVHUsageInvalid
	}
	observed := detail.LastUpdate
	if !current {
		observed = end.Add(-time.Microsecond)
	}
	if observed.After(through) || !utcMonth(observed).Equal(from) {
		return OVHUsageSnapshot{}, ErrOVHUsageInvalid
	}
	usage := make(map[string]OVHBucketUsage)
	for _, storage := range detail.HourlyUsage.Storage {
		if storage.BucketName == "" {
			return OVHUsageSnapshot{}, ErrOVHUsageInvalid
		}
		row, err := normalizeOVHStorage(storage)
		if err != nil {
			return OVHUsageSnapshot{}, err
		}
		old := usage[storage.BucketName]
		old.StoredByteHours, err = addUsageValue(old.StoredByteHours, row.StoredByteHours)
		if err == nil {
			old.EgressBytes, err = addUsageValue(old.EgressBytes, row.EgressBytes)
		}
		if err == nil {
			old.CostMillicents, err = addUsageValue(old.CostMillicents, row.CostMillicents)
		}
		if err != nil {
			return OVHUsageSnapshot{}, err
		}
		usage[storage.BucketName] = old
	}
	return OVHUsageSnapshot{PeriodStart: from, ObservedAt: observed, Buckets: usage}, nil
}

// Historical documents describe complete periods, not independent hourly
// deltas. Never sum overlapping snapshots or prorate a month from partial data.
func (c *OVHUsageClient) closedUsage(ctx context.Context, from, through time.Time) (ovhUsageHistoryDetail, error) {
	end := from.AddDate(0, 1, 0)
	if through.Before(end.Add(-time.Microsecond)) {
		return ovhUsageHistoryDetail{}, ErrOVHUsageInvalid
	}
	history, err := c.history(ctx, from.Add(-time.Second), end)
	if err != nil {
		return ovhUsageHistoryDetail{}, err
	}
	var selected *ovhUsageHistory
	seen := map[string]bool{}
	for i := range history {
		entry := &history[i]
		if !validOVHPathPart(entry.ID) || seen[entry.ID] {
			return ovhUsageHistoryDetail{}, ErrOVHUsageInvalid
		}
		seen[entry.ID] = true
		if !entry.Period.From.Equal(from) || !entry.Period.To.Equal(end) {
			continue
		}
		if selected != nil || entry.LastUpdate.Before(end) || entry.LastUpdate.After(time.Now()) {
			return ovhUsageHistoryDetail{}, ErrOVHUsageInvalid
		}
		selected = entry
	}
	if selected == nil {
		return ovhUsageHistoryDetail{}, ErrOVHUsageInvalid
	}
	detail, err := c.historyDetail(ctx, selected.ID)
	if err != nil {
		return detail, err
	}
	if detail.ID != selected.ID || !detail.Period.From.Equal(from) || !detail.Period.To.Equal(end) || detail.LastUpdate.Before(end) || detail.LastUpdate.Before(selected.LastUpdate) {
		return ovhUsageHistoryDetail{}, ErrOVHUsageInvalid
	}
	return detail, nil
}
