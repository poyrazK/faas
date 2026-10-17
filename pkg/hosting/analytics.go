package hosting

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"golang.org/x/sync/singleflight"
)

var ErrTrafficUnavailable = errors.New("hosting traffic observations unavailable")
var datasetName = regexp.MustCompile(`^gregale_internal_[a-z0-9_]+$`)

type TrafficReader interface {
	Read(context.Context, string, string, time.Time) (api.HostingTraffic, error)
}
type trafficCache struct {
	value api.HostingTraffic
	until time.Time
}
type Analytics struct {
	endpoint, dataset, token string
	client                   *http.Client
	slots                    chan struct{}
	mu                       sync.Mutex
	cache                    map[string]trafficCache
	group                    singleflight.Group
}

func NewAnalytics(account, dataset, token string) (*Analytics, error) {
	if !hexID(account) || !datasetName.MatchString(dataset) || len(dataset) > api.HostingAnalyticsDatasetMaxBytes || token == "" {
		return nil, errors.New("hosting analytics requires an isolated dataset, account and scoped read token")
	}
	return &Analytics{endpoint: "https://api.cloudflare.com/client/v4/accounts/" + account + "/analytics_engine/sql", dataset: dataset, token: token,
		client: &http.Client{Timeout: api.HostingTrafficQueryTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, slots: make(chan struct{}, api.HostingTrafficQueryConcurrency), cache: make(map[string]trafficCache)}, nil
}

func cloneTraffic(value api.HostingTraffic) api.HostingTraffic {
	value.Buckets = append([]api.HostingTrafficBucket{}, value.Buckets...)
	value.Failures = append([]api.HostingTrafficFailure{}, value.Failures...)
	return value
}
func (a *Analytics) cached(key string, now time.Time) (api.HostingTraffic, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	entry, ok := a.cache[key]
	return cloneTraffic(entry.value), ok && entry.until.After(now)
}
func (a *Analytics) remember(key string, value api.HostingTraffic, now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.cache) >= api.HostingTrafficCacheEntries {
		oldest := ""
		var until time.Time
		for k, entry := range a.cache {
			if oldest == "" || entry.until.Before(until) {
				oldest, until = k, entry.until
			}
		}
		delete(a.cache, oldest)
	}
	a.cache[key] = trafficCache{value: cloneTraffic(value), until: now.Add(api.HostingTrafficCacheTTL)}
}
func (a *Analytics) Read(ctx context.Context, site, origin string, now time.Time) (api.HostingTraffic, error) {
	if uuid.Validate(site) != nil || !ValidDigest(origin) || now.IsZero() {
		return api.HostingTraffic{}, ErrTrafficUnavailable
	}
	key := site + origin
	if value, ok := a.cached(key, now); ok {
		return value, nil
	}
	value, err, _ := a.group.Do(key, func() (any, error) {
		if value, ok := a.cached(key, now); ok {
			return value, nil
		}
		select {
		case a.slots <- struct{}{}:
			defer func() { <-a.slots }()
		default:
			return nil, ErrTrafficUnavailable
		}
		report, err := a.query(ctx, site, origin, now)
		if err == nil {
			a.remember(key, report, now)
		}
		return report, err
	})
	if err != nil {
		return api.HostingTraffic{}, ErrTrafficUnavailable
	}
	return cloneTraffic(value.(api.HostingTraffic)), nil
}

// Analytics Engine has no SQL parameter API. Interpolation is limited to the
// validated immutable dataset, UUID/digest and server-generated timestamps.
const trafficSQL = `SELECT toStartOfHour(timestamp) AS bucket, blob3 AS code, blob4 AS cache, double3 AS status,
 SUM(_sample_interval * double1) AS requests, SUM(_sample_interval * double2) AS payload_bytes,
 MAX(_sample_interval) AS sample_interval FROM %s
 WHERE index1 = '%s' AND blob1 = '%s' AND blob5 = 'v1'
 AND timestamp >= toDateTime('%s') AND timestamp < toDateTime('%s')
 GROUP BY bucket, code, cache, status ORDER BY bucket DESC LIMIT %d FORMAT JSON`

type trafficRow struct {
	Bucket         string          `json:"bucket"`
	Code           string          `json:"code"`
	Cache          string          `json:"cache"`
	Status         json.RawMessage `json:"status"`
	Requests       json.RawMessage `json:"requests"`
	PayloadBytes   json.RawMessage `json:"payload_bytes"`
	SampleInterval json.RawMessage `json:"sample_interval"`
}

func (a *Analytics) query(ctx context.Context, site, origin string, now time.Time) (api.HostingTraffic, error) {
	end := now.UTC().Truncate(time.Minute)
	start := end.Add(-api.HostingTrafficWindow)
	query := fmt.Sprintf(trafficSQL, a.dataset, site, origin, start.Format("2006-01-02 15:04:05"), end.Format("2006-01-02 15:04:05"), api.HostingTrafficRowsMax)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint, bytes.NewBufferString(query))
	if err != nil {
		return api.HostingTraffic{}, ErrTrafficUnavailable
	}
	request.Header.Set("Authorization", "Bearer "+a.token)
	request.Header.Set("Content-Type", "text/plain")
	response, err := a.client.Do(request)
	if err != nil {
		return api.HostingTraffic{}, ErrTrafficUnavailable
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return api.HostingTraffic{}, ErrTrafficUnavailable
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, api.HostingTrafficResponseMaxBytes+1))
	var decoded struct {
		Data []trafficRow `json:"data"`
	}
	if err != nil || len(body) > api.HostingTrafficResponseMaxBytes || json.Unmarshal(body, &decoded) != nil || decoded.Data == nil || len(decoded.Data) >= api.HostingTrafficRowsMax {
		return api.HostingTraffic{}, ErrTrafficUnavailable
	}
	report := api.HostingTraffic{SiteID: site, Source: "cloudflare_analytics_engine", Approximate: true, PeriodStart: start, PeriodEnd: end, ObservedAt: now.UTC(), Buckets: []api.HostingTrafficBucket{}, Failures: []api.HostingTrafficFailure{}}
	if err := aggregateTraffic(&report, decoded.Data); err != nil {
		return api.HostingTraffic{}, ErrTrafficUnavailable
	}
	return report, nil
}

func trafficInteger(raw json.RawMessage) (int64, error) {
	value := string(raw)
	if len(raw) > 0 && raw[0] == '"' {
		if json.Unmarshal(raw, &value) != nil {
			return 0, ErrTrafficUnavailable
		}
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || number < 0 || number > float64(api.HostingTrafficCounterMax) || math.Trunc(number) != number {
		return 0, ErrTrafficUnavailable
	}
	return int64(number), nil
}
func trafficCodeStatus(code string) int64 {
	switch code {
	case "ok":
		return 200
	case "not_modified":
		return 304
	case "method_not_allowed":
		return 405
	case "hosting_file_not_found":
		return 404
	case "hosting_route_unavailable", "hosting_unavailable", "hosting_artifact_unavailable":
		return 503
	default:
		return 0
	}
}
func addTraffic(target *api.HostingTrafficCounters, requests, payload, status int64, cache string) error {
	if requests > api.HostingTrafficCounterMax-target.Requests || payload > api.HostingTrafficCounterMax-target.PayloadBytes {
		return ErrTrafficUnavailable
	}
	target.Requests += requests
	target.PayloadBytes += payload
	if cache == "hit" {
		target.CacheHits += requests
	}
	if cache == "miss" {
		target.CacheMisses += requests
	}
	if status >= 400 && status < 500 {
		target.ClientErrors += requests
	}
	if status >= 500 {
		target.ServerErrors += requests
	}
	return nil
}
func aggregateTraffic(report *api.HostingTraffic, rows []trafficRow) error {
	buckets := map[time.Time]*api.HostingTrafficBucket{}
	for at := report.PeriodStart.Truncate(time.Hour); at.Before(report.PeriodEnd); at = at.Add(time.Hour) {
		buckets[at] = &api.HostingTrafficBucket{Start: at}
	}
	for _, row := range rows {
		at, err := time.Parse("2006-01-02 15:04:05", row.Bucket)
		if err != nil || buckets[at] == nil || !at.Equal(at.Truncate(time.Hour)) {
			return ErrTrafficUnavailable
		}
		status, statusErr := trafficInteger(row.Status)
		count, countErr := trafficInteger(row.Requests)
		payload, payloadErr := trafficInteger(row.PayloadBytes)
		interval, intervalErr := trafficInteger(row.SampleInterval)
		if statusErr != nil || countErr != nil || payloadErr != nil || intervalErr != nil || count < 1 || interval < 1 || status < 200 || status != trafficCodeStatus(row.Code) || (row.Cache != "hit" && row.Cache != "miss" && row.Cache != "none") || (status != 200 && (payload != 0 || row.Cache != "none")) {
			return ErrTrafficUnavailable
		}
		if err := addTraffic(&report.HostingTrafficCounters, count, payload, status, row.Cache); err != nil {
			return err
		}
		if err := addTraffic(&buckets[at].HostingTrafficCounters, count, payload, status, row.Cache); err != nil {
			return err
		}
		report.MaxSampleInterval = max(report.MaxSampleInterval, interval)
		if status >= 400 {
			report.Failures = append(report.Failures, api.HostingTrafficFailure{Start: at, Status: int(status), Code: row.Code, Requests: count})
		}
	}
	for _, bucket := range buckets {
		report.Buckets = append(report.Buckets, *bucket)
	}
	sort.Slice(report.Buckets, func(i, j int) bool { return report.Buckets[i].Start.Before(report.Buckets[j].Start) })
	sort.Slice(report.Failures, func(i, j int) bool {
		if report.Failures[i].Start.Equal(report.Failures[j].Start) {
			return report.Failures[i].Code < report.Failures[j].Code
		}
		return report.Failures[i].Start.After(report.Failures[j].Start)
	})
	if len(report.Failures) > api.HostingTrafficFailureGroupsMax {
		report.Failures = report.Failures[:api.HostingTrafficFailureGroupsMax]
	}
	return nil
}
