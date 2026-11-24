package insights

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/state"
)

// Saved insights (ADR-20261122190000): a named events, funnel or retention
// query over the last RangeDays days, run on demand and by meterd for
// digests and threshold alerts.

// Default ranges of a saved insight, the same as the ad hoc queries'.
const (
	savedDefaultDays          = 7
	savedRetentionDefaultDays = 28
)

var savedComparisons = []string{"gt", "gte", "lt", "lte"}

// NormalizeSaved fills a saved insight's defaults and validates it against
// the plan's analytics retention. It returns the stored query.
func NormalizeSaved(req *api.SavedInsightRequest, retentionDays int) (json.RawMessage, error) {
	if !flags.ValidKey(req.Key) {
		return nil, invalid("key %q is not a key (^[a-z][a-z0-9_-]{0,63}$)", req.Key)
	}
	if req.Name == "" {
		req.Name = req.Key
	}
	if len(req.Name) > api.SavedInsightNameMaxBytes {
		return nil, invalid("name is at most %d bytes", api.SavedInsightNameMaxBytes)
	}
	query, defaultDays, err := savedQuery(req)
	if err != nil {
		return nil, err
	}
	if req.RangeDays == 0 {
		req.RangeDays = min(defaultDays, retentionDays)
	}
	if req.RangeDays < 1 || req.RangeDays > min(api.AnalyticsQueryMaxDays, retentionDays) {
		return nil, invalid("range_days is 1 to %d on this plan", min(api.AnalyticsQueryMaxDays, retentionDays))
	}
	if req.Kind == api.SavedInsightKindEvents && req.Events.Interval == "hour" && req.RangeDays > 7 {
		return nil, invalid("an hourly series covers at most 7 days")
	}
	switch req.Digest {
	case "", api.SavedInsightDigestDaily, api.SavedInsightDigestWeekly:
	default:
		return nil, invalid("digest is daily or weekly")
	}
	if req.Alert != nil && !slices.Contains(savedComparisons, req.Alert.Comparison) {
		return nil, invalid("alert comparison is gt, gte, lt or lte")
	}
	if len(req.ChannelIDs) > api.SavedInsightMaxChannels {
		return nil, invalid("a saved insight sends to at most %d channels", api.SavedInsightMaxChannels)
	}
	for _, id := range req.ChannelIDs {
		if _, err := uuid.Parse(id); err != nil {
			return nil, invalid("channel %q is not a channel id", id)
		}
	}
	notifies := req.Digest != "" || req.Alert != nil
	switch {
	case notifies && len(req.ChannelIDs) == 0:
		return nil, invalid("a digest or alert needs at least one channel_ids entry")
	case !notifies && len(req.ChannelIDs) > 0:
		return nil, invalid("channel_ids needs a digest or an alert")
	}
	return query, nil
}

// savedQuery validates the query of req's kind and returns it encoded with
// its default range.
func savedQuery(req *api.SavedInsightRequest) (json.RawMessage, int, error) {
	set := 0
	for _, ok := range []bool{req.Events != nil, req.Funnel != nil, req.Retention != nil} {
		if ok {
			set++
		}
	}
	if set != 1 {
		return nil, 0, invalid("set exactly one of events, funnel and retention")
	}
	var (
		v           any
		defaultDays = savedDefaultDays
		err         error
	)
	switch req.Kind {
	case api.SavedInsightKindEvents:
		if req.Events == nil {
			return nil, 0, invalid("an events insight sets events")
		}
		v, err = req.Events, validEvents(req.Events)
	case api.SavedInsightKindFunnel:
		if req.Funnel == nil {
			return nil, 0, invalid("a funnel insight sets funnel")
		}
		v, err = req.Funnel, validFunnel(req.Funnel)
	case api.SavedInsightKindRetention:
		if req.Retention == nil {
			return nil, 0, invalid("a retention insight sets retention")
		}
		v, err, defaultDays = req.Retention, validRetention(req.Retention), savedRetentionDefaultDays
	default:
		return nil, 0, invalid("kind is events, funnel or retention")
	}
	if err != nil {
		return nil, 0, err
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, 0, fmt.Errorf("saved insight query: %w", err)
	}
	return raw, defaultDays, nil
}

func validEvents(q *api.InsightEventsQuery) error {
	if err := validStep(api.InsightStepRequest{Event: q.Event, Where: q.Where}); err != nil {
		return err
	}
	if _, err := Unit(q.Unit); err != nil {
		return err
	}
	if _, err := ParseBreakdown(q.By); err != nil {
		return err
	}
	switch q.Interval {
	case "", "day", "hour":
	default:
		return invalid("interval is day or hour")
	}
	switch q.Measure {
	case "", "events", "value":
	default:
		return invalid("measure is events or value")
	}
	return nil
}

func validFunnel(q *api.InsightFunnelRequest) error {
	if q.From != nil || q.Until != nil {
		return invalid("a saved insight sets range_days, not from or until")
	}
	if n := len(q.Steps); n < api.AnalyticsFunnelMinSteps || n > api.AnalyticsFunnelMaxSteps {
		return invalid("a funnel has %d to %d steps", api.AnalyticsFunnelMinSteps, api.AnalyticsFunnelMaxSteps)
	}
	for _, s := range q.Steps {
		if err := validStep(s); err != nil {
			return err
		}
	}
	if _, err := funnelWindow(q.Window); err != nil {
		return err
	}
	if _, err := Unit(q.Unit); err != nil {
		return err
	}
	_, err := ParseBreakdown(q.By)
	return err
}

func validRetention(q *api.InsightRetentionRequest) error {
	if q.From != nil || q.Until != nil {
		return invalid("a saved insight sets range_days, not from or until")
	}
	for _, s := range []api.InsightStepRequest{q.Start, q.Return} {
		if err := validStep(s); err != nil {
			return err
		}
	}
	if _, err := Unit(q.Unit); err != nil {
		return err
	}
	if _, err := ParseBreakdown(q.By); err != nil {
		return err
	}
	switch q.Period {
	case "", "day", "week":
	default:
		return invalid("period is day or week")
	}
	if q.Periods < 0 || q.Periods > api.AnalyticsRetentionMaxPeriods {
		return invalid("periods is 1 to %d", api.AnalyticsRetentionMaxPeriods)
	}
	return nil
}

// SavedRequest decodes a stored saved insight back into its request.
func SavedRequest(s state.SavedInsight) api.SavedInsightRequest {
	req := api.SavedInsightRequest{Key: s.Key, Name: s.Name, Kind: s.Kind, RangeDays: s.RangeDays, Digest: s.Digest,
		ChannelIDs: slices.Clone(s.ChannelIDs)}
	switch s.Kind {
	case api.SavedInsightKindEvents:
		req.Events = &api.InsightEventsQuery{}
		_ = json.Unmarshal(s.Query, req.Events)
	case api.SavedInsightKindFunnel:
		req.Funnel = &api.InsightFunnelRequest{}
		_ = json.Unmarshal(s.Query, req.Funnel)
	case api.SavedInsightKindRetention:
		req.Retention = &api.InsightRetentionRequest{}
		_ = json.Unmarshal(s.Query, req.Retention)
	}
	if s.AlertComparison != "" {
		req.Alert = &api.SavedInsightAlert{Comparison: s.AlertComparison, Threshold: s.AlertThreshold}
	}
	return req
}

// RunSaved runs a saved insight over its last RangeDays days, at most the
// plan's analytics retention.
func RunSaved(ctx context.Context, store Store, s state.SavedInsight, retentionDays int, now time.Time) (api.SavedInsightResult, error) {
	req := SavedRequest(s)
	rng, err := ResolveRange(nil, nil, now, retentionDays, req.RangeDays)
	if err != nil {
		return api.SavedInsightResult{}, err
	}
	out := api.SavedInsightResult{Key: s.Key, Kind: s.Kind, From: rng.From, Until: rng.Until}
	switch s.Kind {
	case api.SavedInsightKindEvents:
		q := req.Events
		series, err := Series(ctx, store, SeriesQuery{EnvironmentID: s.EnvironmentID, Event: q.Event, Where: q.Where,
			Interval: q.Interval, Unit: q.Unit, By: q.By, Range: rng})
		if err != nil {
			return api.SavedInsightResult{}, err
		}
		out.Events = &series
		total := 0.0
		for _, p := range series.Points {
			if q.Measure == "value" {
				total += float64(p.Value)
			} else {
				total += float64(p.Events)
			}
		}
		// No events is a real zero.
		out.Value = &total
	case api.SavedInsightKindFunnel:
		f, err := Funnel(ctx, store, FunnelQuery{EnvironmentID: s.EnvironmentID, Request: *req.Funnel, Range: rng})
		if err != nil {
			return api.SavedInsightResult{}, err
		}
		out.Funnel = &f
		// Without entrants there is no conversion rate.
		if n := len(f.Steps); n > 0 && f.Steps[0].Units > 0 {
			v := f.Steps[n-1].ConversionFromFirst
			out.Value = &v
		}
	case api.SavedInsightKindRetention:
		r, err := Retention(ctx, store, RetentionQuery{EnvironmentID: s.EnvironmentID, Request: *req.Retention, Range: rng, Now: now})
		if err != nil {
			return api.SavedInsightResult{}, err
		}
		out.Retention = &r
		// Rates[k-1] is period k's rate; with nobody eligible for period 1
		// (no period has ended) there is no rate yet.
		if o := r.Overall; len(o.Rates) > 0 && len(o.Eligible) > 0 && o.Eligible[0] > 0 {
			v := o.Rates[0]
			out.Value = &v
		}
	default:
		return api.SavedInsightResult{}, invalid("kind is events, funnel or retention")
	}
	return out, nil
}

// Schedule returns when a saved insight's next digest goes out and when
// meterd next looks at it; both are nil when it sends nothing.
func Schedule(digest string, alert bool, now time.Time) (nextDigest, nextCheck *time.Time) {
	now = now.UTC()
	if digest != "" {
		day := time.Date(now.Year(), now.Month(), now.Day(), api.SavedInsightDigestHourUTC, 0, 0, 0, time.UTC)
		for !day.After(now) || (digest == api.SavedInsightDigestWeekly && day.Weekday() != time.Monday) {
			day = day.AddDate(0, 0, 1)
		}
		nextDigest = &day
	}
	if alert {
		t := now.UTC().Add(api.SavedInsightAlertInterval)
		nextCheck = &t
	}
	if nextDigest != nil && (nextCheck == nil || nextDigest.Before(*nextCheck)) {
		t := *nextDigest
		nextCheck = &t
	}
	return nextDigest, nextCheck
}

// Compare reports whether value breaches the alert.
func Compare(value float64, comparison string, threshold float64) bool {
	switch comparison {
	case "gt":
		return value > threshold
	case "gte":
		return value >= threshold
	case "lt":
		return value < threshold
	case "lte":
		return value <= threshold
	}
	return false
}
