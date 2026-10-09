package profiling

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/pprof/profile"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const coverageProfileType = "gregale_profile_coverage:events:count:events:count"

// CoverageBackend is optional so old installations and test backends can
// return CPU data while reporting coverage as unavailable.
type CoverageBackend interface {
	QueryCoverage(context.Context, string, string, string, api.ProfileQuery) (api.ProfileCoverage, error)
}

type failureBackend interface {
	RecordFailure(context.Context, Principal, time.Time) error
}

func (s *Service) recordFailure(ctx context.Context, principal Principal) {
	backend, ok := s.Backend.(failureBackend)
	if !ok || ctx.Err() != nil {
		return
	}
	now := s.clock()
	s.mu.Lock()
	for key, at := range s.failures {
		if now.Sub(at) > api.ProfileRetryCacheTTL {
			delete(s.failures, key)
		}
	}
	last, exists := s.failures[principal.AccountID]
	if (exists && now.Sub(last) < api.ProfileFailureRecordInterval) || (!exists && len(s.failures) >= api.ProfileMaxTrackedAccounts) {
		s.mu.Unlock()
		return
	}
	s.failures[principal.AccountID] = now
	s.mu.Unlock()
	_ = backend.RecordFailure(ctx, principal, now)
}

func coverageProfile(name string, at int64) *profile.Profile {
	f := &profile.Function{ID: 1, Name: name}
	l := &profile.Location{ID: 1, Line: []profile.Line{{Function: f}}}
	return &profile.Profile{
		SampleType: []*profile.ValueType{{Type: "events", Unit: "count"}},
		PeriodType: &profile.ValueType{Type: "events", Unit: "count"}, Period: 1,
		TimeNanos: at, DurationNanos: int64(time.Nanosecond),
		Function: []*profile.Function{f}, Location: []*profile.Location{l},
		Sample: []*profile.Sample{{Location: []*profile.Location{l}, Value: []int64{1}}},
	}
}

func receivedProfile(p *profile.Profile, identity sampleIdentity) *profile.Profile {
	name := fmt.Sprintf("received:%s:%d:%d:%d", identity.Collector, p.TimeNanos, p.DurationNanos, identity.ReceivedAt.UnixNano())
	return coverageProfile(name, p.TimeNanos)
}

func (b *Pyroscope) RecordFailure(ctx context.Context, principal Principal, at time.Time) error {
	identity := sampleIdentity{
		ID:        uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("%s:%d:failure", principal.AccountID, at.UnixNano()))).String(),
		Collector: uuid.NewSHA1(uuid.NameSpaceOID, []byte(principal.InstanceID+":"+principal.Generation+":failure")).String(),
	}
	p := coverageProfile("failed", at.UnixNano())
	series, err := encodeSeries(principal, identity, "gregale_profile_coverage", p)
	if err != nil {
		return err
	}
	return b.pushSeries(ctx, principal.AccountID, protoBytes(nil, 1, series))
}

func (b *Pyroscope) QueryCoverage(ctx context.Context, tenant, appID, scope string, q api.ProfileQuery) (api.ProfileCoverage, error) {
	p, err := b.queryProfile(ctx, tenant, appID, scope, q, coverageProfileType, api.ProfileMaxCoverageEntries)
	if err != nil {
		return api.ProfileCoverage{}, err
	}
	return coverageView(p, q)
}

type capturedInterval struct{ start, end time.Time }

func coverageView(p *profile.Profile, q api.ProfileQuery) (api.ProfileCoverage, error) {
	out := api.ProfileCoverage{Available: true, LabelCountsComplete: true, LabelCounts: map[string]int64{}, WindowSeconds: q.End.Sub(q.Start).Seconds()}
	if out.WindowSeconds <= 0 {
		return api.ProfileCoverage{}, fmt.Errorf("invalid coverage window")
	}
	var intervals []capturedInterval
	collectors := map[string]bool{}
	diagnostics := map[string]float64{}
	if p != nil {
		if len(p.SampleType) != 1 || p.SampleType[0].Type != "events" || p.SampleType[0].Unit != "count" {
			return api.ProfileCoverage{}, fmt.Errorf("invalid coverage units")
		}
		if len(p.Sample) > api.ProfileMaxCoverageEntries {
			return api.ProfileCoverage{}, fmt.Errorf("coverage exceeds query bounds")
		}
		for _, s := range p.Sample {
			if len(s.Value) != 1 || s.Value[0] <= 0 || len(s.Location) != 1 || len(s.Location[0].Line) != 1 || s.Location[0].Line[0].Function == nil {
				return api.ProfileCoverage{}, fmt.Errorf("coverage is incomplete")
			}
			name := s.Location[0].Line[0].Function.Name
			if name == "failed" {
				if s.Value[0] > math.MaxInt64-out.RecordedFailedUploads {
					return api.ProfileCoverage{}, fmt.Errorf("coverage count overflows")
				}
				out.RecordedFailedUploads += s.Value[0]
				continue
			}
			parts := strings.Split(name, ":")
			if (len(parts) != 5 && len(parts) != 6+len(attributionReasons) && len(parts) != 13) || parts[0] != "received" {
				// Includes backend-pruned synthetic frames: no exact counts
				// or coverage are reported from a truncated result.
				return api.ProfileCoverage{}, fmt.Errorf("coverage is incomplete")
			}
			if _, err := uuid.Parse(parts[1]); err != nil {
				return api.ProfileCoverage{}, fmt.Errorf("invalid collector identity")
			}
			start, err1 := strconv.ParseInt(parts[2], 10, 64)
			duration, err2 := strconv.ParseInt(parts[3], 10, 64)
			received, err3 := strconv.ParseInt(parts[4], 10, 64)
			if err1 != nil || err2 != nil || err3 != nil || start <= 0 || duration <= 0 || duration > int64(api.ProfileMaxCaptureDuration) || received <= 0 {
				return api.ProfileCoverage{}, fmt.Errorf("invalid coverage interval")
			}
			a, z := time.Unix(0, start), time.Unix(0, start).Add(time.Duration(duration))
			if a.Before(q.Start) {
				a = q.Start
			}
			if z.After(q.End) {
				z = q.End
			}
			if !z.After(a) {
				continue
			}
			if s.Value[0] > math.MaxInt64-out.ReceivedProfiles {
				return api.ProfileCoverage{}, fmt.Errorf("coverage count overflows")
			}
			if err := parseAttributionCounters(parts, s.Value[0], diagnostics); err != nil {
				return api.ProfileCoverage{}, err
			}
			readRouteRequestMetadata(parts, s.Value[0], start, start+duration, q, &out)
			out.ReceivedProfiles += s.Value[0]
			collectors[parts[1]] = true
			at := time.Unix(0, received).UTC()
			if out.LastReceivedAt == nil || at.After(*out.LastReceivedAt) {
				out.LastReceivedAt = &at
			}
			intervals = append(intervals, capturedInterval{a, z})
		}
	}
	sort.Slice(intervals, func(i, j int) bool { return intervals[i].start.Before(intervals[j].start) })
	var end time.Time
	for _, interval := range intervals {
		start := interval.start
		if start.Before(end) {
			start = end
		}
		if interval.end.After(start) {
			out.CoveredSeconds += interval.end.Sub(start).Seconds()
			end = interval.end
		}
	}
	out.AttributionReasons = attributionReasonRows(diagnostics)
	out.ContributingCollectors = len(collectors)
	out.GapSeconds = max(0, out.WindowSeconds-out.CoveredSeconds)
	return out, nil
}
