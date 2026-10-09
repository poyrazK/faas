package profiling

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"math"

	"github.com/google/pprof/profile"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/profileproto"
)

type routeRequestReportKey struct{}
type routeRequestMetadata struct {
	From     int64            `json:"f"`
	Until    int64            `json:"u"`
	Complete bool             `json:"c"`
	Counts   map[string]int64 `json:"r"`
}

func routeRequestHash(route string) string {
	hash := sha256.Sum256([]byte(route))
	return hex.EncodeToString(hash[:16])
}

// Guest counters remain claims. Only host-admitted static labels are encoded;
// rejected values never reach backend metadata.
func admitRouteRequestReport(report *profileproto.RouteRequestReport, principal Principal, p *profile.Profile) *routeRequestMetadata {
	if report == nil || len(report.Routes) > api.ProfileRouteMaxLabels || report.FromUnixNano < principal.StartedAt.UnixNano() || report.FromUnixNano < p.TimeNanos-int64(api.ProfileRouteRequestTimestampTolerance) || report.UntilUnixNano <= report.FromUnixNano || report.UntilUnixNano-report.FromUnixNano > int64(api.ProfileMaxCaptureDuration) || report.UntilUnixNano > p.TimeNanos+p.DurationNanos+int64(api.ProfileRouteRequestTimestampTolerance) {
		return nil
	}
	out := &routeRequestMetadata{From: report.FromUnixNano, Until: report.UntilUnixNano, Complete: report.Complete, Counts: map[string]int64{}}
	allowed := map[string]bool{}
	for _, route := range principal.Routes {
		allowed[route] = true
	}
	for route, count := range report.Routes {
		if !allowed[route] || count < 0 || count > api.ProfileRouteMaxLabeledRequests {
			out.Complete = false
			continue
		}
		out.Counts[routeRequestHash(route)] = count
	}
	data, err := json.Marshal(out)
	if err != nil || len(data) > api.ProfileRouteRequestMetadataMaxBytes {
		return nil
	}
	return out
}

func appendRouteRequestMetadata(name string, report *routeRequestMetadata) string {
	if report == nil {
		return name
	}
	data, err := json.Marshal(report)
	if err != nil || len(data) > api.ProfileRouteRequestMetadataMaxBytes {
		return name
	}
	return name + ":route_requests_v1:" + base64.RawURLEncoding.EncodeToString(data)
}

func readRouteRequestMetadata(parts []string, multiplicity int64, start, end int64, q api.ProfileQuery, out *api.ProfileCoverage) {
	if len(parts) != 13 || parts[11] != "route_requests_v1" || len(parts[12]) > base64.RawURLEncoding.EncodedLen(api.ProfileRouteRequestMetadataMaxBytes) {
		out.LabelCountsComplete = false
		return
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[12])
	var report routeRequestMetadata
	if err != nil || json.Unmarshal(data, &report) != nil || len(report.Counts) > api.ProfileRouteMaxLabels || report.From < start-int64(api.ProfileRouteRequestTimestampTolerance) || report.Until <= report.From || report.Until-report.From > int64(api.ProfileMaxCaptureDuration) || report.Until > end+int64(api.ProfileRouteRequestTimestampTolerance) || !report.Complete {
		out.LabelCountsComplete = false
		return
	}
	// Never apportion request counts from captures crossing a query boundary.
	if report.From < q.Start.UnixNano() || report.Until > q.End.UnixNano() {
		out.LabelCountBoundaryProfiles += multiplicity
		return
	}
	for hash, count := range report.Counts {
		raw, err := hex.DecodeString(hash)
		if err != nil || len(raw) != 16 || count < 0 || count > api.ProfileRouteMaxLabeledRequests || count > math.MaxInt64/multiplicity || out.LabelCounts[hash] > math.MaxInt64-count*multiplicity {
			out.LabelCountsComplete = false
			return
		}
	}
	for hash, count := range report.Counts {
		out.LabelCounts[hash] += count * multiplicity
	}
	out.LabelCountProfiles += multiplicity
}
