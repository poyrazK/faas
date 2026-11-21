// Package insights answers product analytics queries — event series,
// funnels, retention and requests by flag value — from the bounded
// primitives of state.AnalyticsStore (ADR 20261117084505 §3).
package insights

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/state"
)

// Store is the analytics store the queries read.
type Store = state.AnalyticsStore

// ErrInvalid wraps every validation error; its message is safe to show.
var ErrInvalid = errors.New("invalid insights query")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

var propertyKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// ValidPropertyKey reports whether key names an event property.
func ValidPropertyKey(key string) bool { return propertyKeyPattern.MatchString(key) }

// Breakdown is a parsed "flag:KEY" or "property:KEY".
type Breakdown struct{ Kind, Key string }

// ParseBreakdown parses a breakdown spec; empty is no breakdown.
func ParseBreakdown(spec string) (Breakdown, error) {
	if spec == "" {
		return Breakdown{}, nil
	}
	kind, key, ok := strings.Cut(spec, ":")
	switch {
	case ok && kind == "property" && ValidPropertyKey(key):
		return Breakdown{Kind: kind, Key: key}, nil
	case ok && kind == "flag" && flags.ValidKey(key):
		return Breakdown{Kind: kind, Key: key}, nil
	}
	return Breakdown{}, invalid("by is flag:KEY or property:KEY")
}

// Unit validates a unit kind; empty is customer.
func Unit(unit string) (string, error) {
	switch unit {
	case "", state.AnalyticsUnitCustomer:
		return state.AnalyticsUnitCustomer, nil
	case state.AnalyticsUnitSubject:
		return unit, nil
	}
	return "", invalid("unit is customer or subject")
}

// Range bounds a query's [from, until) to the retention and to
// AnalyticsQueryMaxDays; unset bounds default to the last defaultDays days.
type Range struct{ From, Until time.Time }

// ResolveRange checks a requested range against now and a retention.
func ResolveRange(from, until *time.Time, now time.Time, retentionDays, defaultDays int) (Range, error) {
	if retentionDays <= 0 {
		return Range{}, invalid("the plan keeps no events for this query")
	}
	r := Range{Until: now.UTC()}
	if until != nil {
		r.Until = until.UTC()
	}
	if r.Until.After(now) {
		r.Until = now.UTC()
	}
	r.From = r.Until.AddDate(0, 0, -min(defaultDays, retentionDays))
	if from != nil {
		r.From = from.UTC()
	}
	oldest := now.UTC().AddDate(0, 0, -retentionDays)
	if r.From.Before(oldest) {
		r.From = oldest
	}
	switch {
	case !r.From.Before(r.Until):
		return Range{}, invalid("from must be before until")
	case r.Until.Sub(r.From) > time.Duration(api.AnalyticsQueryMaxDays)*24*time.Hour:
		return Range{}, invalid("a query covers at most %d days", api.AnalyticsQueryMaxDays)
	}
	return r, nil
}

func validStep(s api.InsightStepRequest) error {
	if !flags.ValidEventName(s.Event) {
		return invalid("event %q is not an event name (^[a-z][a-z0-9_.-]{0,63}$)", s.Event)
	}
	if len(s.Where) > api.AnalyticsEventMaxProperties {
		return invalid("where holds at most %d properties", api.AnalyticsEventMaxProperties)
	}
	for k, v := range s.Where {
		if !ValidPropertyKey(k) || len(v) > api.AnalyticsPropertyValueMaxBytes {
			return invalid("where.%s is not a property key with a value of at most %d bytes", k, api.AnalyticsPropertyValueMaxBytes)
		}
	}
	return nil
}

// topValues orders breakdown values by weight and keeps the first
// AnalyticsMaxBreakdownValues; the rest map to InsightsOther.
func topValues(weight map[string]int64) map[string]string {
	values := make([]string, 0, len(weight))
	for v := range weight {
		values = append(values, v)
	}
	slices.SortFunc(values, func(a, b string) int {
		if weight[a] != weight[b] {
			if weight[a] > weight[b] {
				return -1
			}
			return 1
		}
		return strings.Compare(a, b)
	})
	out := make(map[string]string, len(values))
	for i, v := range values {
		if i < api.AnalyticsMaxBreakdownValues {
			out[v] = v
		} else {
			out[v] = api.InsightsOther
		}
	}
	return out
}

// unitBreakdowns returns each entry's breakdown value: the property or flag
// of its anchor event, or for a flag the unit's earliest event in the range
// that carries it, else InsightsNone.
func unitBreakdowns(ctx context.Context, store Store, env, unit string, by Breakdown, entries []state.AnalyticsEntry, r Range) (map[string]string, error) {
	out := make(map[string]string, len(entries))
	if by.Kind == "" {
		for _, e := range entries {
			out[e.Unit.Key()] = ""
		}
		return out, nil
	}
	var missing []state.AnalyticsUnitRef
	for _, e := range entries {
		src := e.Properties
		if by.Kind == "flag" {
			src = e.Flags
		}
		if v, ok := src[by.Key]; ok {
			out[e.Unit.Key()] = v
			continue
		}
		out[e.Unit.Key()] = api.InsightsNone
		if by.Kind == "flag" {
			missing = append(missing, e.Unit)
		}
	}
	if len(missing) == 0 {
		return out, nil
	}
	found, err := store.AnalyticsUnitFlags(ctx, env, unit, by.Key, missing, r.From, r.Until)
	if err != nil {
		return nil, err
	}
	for k, v := range found {
		out[k] = v
	}
	return out, nil
}

func byString(by Breakdown) string {
	if by.Kind == "" {
		return ""
	}
	return by.Kind + ":" + by.Key
}

// SeriesQuery is an event series request.
type SeriesQuery struct {
	EnvironmentID string
	Event         string
	Where         map[string]string
	Interval      string
	Unit          string
	By            string
	Range         Range
}

// Series returns an event's counts per day or hour.
func Series(ctx context.Context, store Store, q SeriesQuery) (api.InsightEventSeries, error) {
	if err := validStep(api.InsightStepRequest{Event: q.Event, Where: q.Where}); err != nil {
		return api.InsightEventSeries{}, err
	}
	unit, err := Unit(q.Unit)
	if err != nil {
		return api.InsightEventSeries{}, err
	}
	by, err := ParseBreakdown(q.By)
	if err != nil {
		return api.InsightEventSeries{}, err
	}
	bucket := 24 * time.Hour
	switch q.Interval {
	case "", "day":
		q.Interval = "day"
	case "hour":
		bucket = time.Hour
		if q.Range.Until.Sub(q.Range.From) > 7*24*time.Hour {
			return api.InsightEventSeries{}, invalid("an hourly series covers at most 7 days")
		}
	default:
		return api.InsightEventSeries{}, invalid("interval is day or hour")
	}
	rows, err := store.AnalyticsEventSeries(ctx, state.AnalyticsSeriesQuery{
		AnalyticsMatch: state.AnalyticsMatch{EnvironmentID: q.EnvironmentID, Name: q.Event, From: q.Range.From, Until: q.Range.Until, Properties: q.Where},
		Bucket:         bucket, ByKind: by.Kind, ByKey: by.Key, Unit: unit})
	if err != nil {
		return api.InsightEventSeries{}, err
	}
	weight := map[string]int64{}
	for _, r := range rows {
		weight[r.Breakdown] += r.Events
	}
	top := topValues(weight)
	type key struct {
		start     time.Time
		breakdown string
	}
	merged := map[key]*api.InsightEventPoint{}
	var order []key
	for _, r := range rows {
		k := key{r.BucketStart, top[r.Breakdown]}
		p := merged[k]
		if p == nil {
			p = &api.InsightEventPoint{Start: r.BucketStart, Breakdown: k.breakdown}
			merged[k] = p
			order = append(order, k)
		}
		p.Events += r.Events
		p.Units += r.Units
		p.Value += r.ValueSum
	}
	out := api.InsightEventSeries{Event: q.Event, Unit: unit, Interval: q.Interval, By: byString(by), From: q.Range.From, Until: q.Range.Until,
		Points: make([]api.InsightEventPoint, 0, len(order))}
	for _, k := range order {
		out.Points = append(out.Points, *merged[k])
	}
	slices.SortStableFunc(out.Points, func(a, b api.InsightEventPoint) int {
		if c := a.Start.Compare(b.Start); c != 0 {
			return c
		}
		return strings.Compare(a.Breakdown, b.Breakdown)
	})
	return out, nil
}
