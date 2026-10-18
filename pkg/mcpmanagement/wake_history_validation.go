package mcpmanagement

import (
	"math"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *management) validateWakeHistory(slug, appID string, opts api.AppWakeTimelineOptions, n api.AppWakeTimelineResponse) (int, error) {
	fail := invalid("invalid_response", "native wake history has inconsistent identity, counters or source metadata")
	_, asOfOK := wakeHistoryTime(n.AsOf)
	if n.App.AppID != appID || n.App.Slug != slug || !asOfOK || len(n.Rows) > api.MCPWakeHistoryNativeCandidatesMax || n.WakeCount24h != len(n.Rows) || n.WakeCountWithMeta < 0 || n.WakeCountWithMeta > len(n.Rows) || n.AtCapacityCount < 0 || n.AtCapacityCount > n.WakeCountWithMeta || !performanceRate(n.AtCapacityPct) {
		return 0, fail
	}
	known, atCapacity, provedMeta := 0, 0, 0
	var previous time.Time
	for _, row := range n.Rows {
		at, err := m.validateWakeHistoryRow(opts, row, n.Page != nil)
		if err != nil {
			return 0, err
		}
		if !at.IsZero() {
			if !previous.IsZero() && at.After(previous) {
				return 0, invalid("invalid_response", "native wake history is not newest-first at its timestamp precision")
			}
			previous = at
		}
		if row.AtCapacityPresent {
			known++
			if row.AtCapacity {
				atCapacity++
			}
		}
		if row.AtCapacityPresent || row.AtCapacity || row.Trigger != "" || row.TriggerClass != "" || row.Method != "" || row.Tier != "" || row.QueuedCount > 0 || row.ConcurrencyAtAdmit > 0 || row.ReadyInMS > 0 {
			provedMeta++
		}
	}
	if known > n.WakeCountWithMeta || provedMeta > n.WakeCountWithMeta || atCapacity != n.AtCapacityCount {
		return 0, fail
	}
	expected := 0.0
	if n.WakeCountWithMeta > 0 {
		expected = float64(atCapacity) / float64(n.WakeCountWithMeta) * 100
	}
	if math.Abs(n.AtCapacityPct-expected) > 1e-9 {
		return 0, fail
	}
	return known, nil
}

func (m *management) validateWakeHistoryRow(opts api.AppWakeTimelineOptions, row api.WakeTimelineJSONRow, modern bool) (time.Time, error) {
	fail := invalid("invalid_response", "native wake row has an unsafe identity, incompatible timestamp or invalid measurement")
	if row.Kind != "wake.boot_started" || row.QueuedCount < 0 || row.ConcurrencyAtAdmit < 0 || row.ReadyInMS < -1 {
		return time.Time{}, fail
	}
	if row.WakeID != "" && (validateSegment(row.WakeID, "wake_id") != nil || len(row.WakeID) > api.MCPRequestIdentifierMaxBytes || m.clean(row.WakeID) != row.WakeID) {
		return time.Time{}, fail
	}
	if row.At == "" {
		if modern {
			return time.Time{}, fail
		}
		return time.Time{}, nil
	}
	at, ok := wakeHistoryTime(row.At)
	from, _ := wakeHistoryTime(opts.Since)
	until, _ := wakeHistoryTime(opts.Until)
	// Native rows format started_at to RFC3339 whole seconds after filtering
	// at source precision. Only overlapping timestamp buckets are verifiable.
	if modern {
		if !ok || at.IsZero() || !at.Equal(at.Truncate(time.Microsecond)) || at.Before(from) || at.After(until) {
			return time.Time{}, fail
		}
		return at, nil
	}
	if !ok || !at.Equal(at.Truncate(time.Second)) || at.After(until) || !at.Add(time.Second).After(from) {
		return time.Time{}, fail
	}
	return at, nil
}
