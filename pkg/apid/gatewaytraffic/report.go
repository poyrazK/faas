// Package gatewaytraffic builds the operator gateway traffic report
// (ADR-20261124093000): what happened to requests in a window, per node and
// per app. It reads gatewayd's existing Prometheus series: status codes,
// 5xx the platform produced before the guest answered, capacity refusals,
// wake admission outcomes (the per-app and gateway-wide wake queues), edge
// rejections and refusals, retry give-ups, and the requests held for a wake
// right now.
package gatewaytraffic

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

// Report sources.
const (
	SourceObserved      = "observed"
	SourcePartial       = "partial"
	SourceUnavailable   = "unavailable"
	SourceNotConfigured = "not_configured"
)

// UnknownHostApp is the app label gatewayd gives a request for a host that
// names no app.
const UnknownHostApp = "-"

// Query names. Each is one instant PromQL query over the window.
const (
	qRequestsByNodeCode = "requests_by_node_code"
	qPlatformByNode     = "platform_errors_by_node"
	qRefusalsByNode     = "capacity_refusals_by_node"
	qApp5xx             = "app_5xx_by_code"
	qAppPlatform        = "app_platform_errors"
	qAppRefusals        = "app_capacity_refusals"
	qAppRequests        = "app_requests"
	qWakeAdmission      = "wake_admission"
	qEdgeRejections     = "edge_rejections"
	qEdgeRefused        = "edge_refused"
	qRetryExhausted     = "retry_exhausted"
	qWaitersByNode      = "waiters_by_node"
	qWaitersByApp       = "waiters_by_app"
	qAdmissionLeaders   = "admission_leaders"
	qOverload           = "overload_by_node"
)

func rangeOf(window time.Duration) string { return fmt.Sprintf("%ds", int(window.Seconds())) }

// Queries returns the report's first-phase PromQL for a window, by name. The
// per-app queries are bounded by topk; requests for the listed apps are a
// second query (AppRequestsQuery) once the list is known.
func Queries(window time.Duration) map[string]string {
	w := rangeOf(window)
	inc := func(sel string) string { return "increase(" + sel + "[" + w + "])" }
	k := strconv.Itoa(api.ObsGatewayTrafficAppsMax)
	app5xx := inc(`gateway_requests_total{code=~"5.."}`)
	return map[string]string{
		qRequestsByNodeCode: "sum by (node, code) (" + inc("gateway_requests_total") + ")",
		qPlatformByNode:     "sum by (node) (" + inc("gateway_platform_errors_total") + ")",
		qRefusalsByNode:     "sum by (node) (" + inc("gateway_capacity_refusals_total") + ")",
		qApp5xx:             "sum by (app, plan, code) (" + app5xx + ") > 0 and on (app) topk(" + k + ", sum by (app) (" + app5xx + ") > 0)",
		qAppPlatform:        "topk(" + k + ", sum by (app, plan) (" + inc("gateway_platform_errors_total") + ") > 0)",
		qAppRefusals:        "topk(" + k + ", sum by (app) (" + inc("gateway_capacity_refusals_total") + ") > 0)",
		qWakeAdmission:      "sum by (plan, outcome) (" + inc("gateway_wake_admission_total") + ")",
		qEdgeRejections:     "sum by (kind, status) (" + inc("gateway_edge_rejections_total") + ") > 0",
		qEdgeRefused:        "sum by (reason) (" + inc("gatewayd_public_edge_refused_total") + ") > 0",
		qRetryExhausted:     "sum by (reason) (" + inc("gateway_retry_exhausted_total") + ") > 0",
		qWaitersByNode:      "sum by (node) (gateway_wake_queue_depth)",
		qWaitersByApp:       "topk(" + k + ", sum by (app, plan) (gateway_wake_queue_depth) > 0)",
		qAdmissionLeaders:   "sum(gateway_wake_admission_queue_depth)",
		qOverload:           "max by (node) (gatewayd_public_overload_level)",
	}
}

// appLabel is an app label AppRequestsQuery may name: an app ID (a UUID)
// or gatewayd's unknown-host row. Anything else stays out of the regex.
var appLabel = regexp.MustCompile(`^([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}|-)$`)

// AppRequestsQuery counts requests in the window for exactly the listed
// apps, or "" when none can be named.
func AppRequestsQuery(window time.Duration, appIDs []string) string {
	var ids []string
	for _, id := range appIDs {
		if appLabel.MatchString(id) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return ""
	}
	return `sum by (app) (increase(gateway_requests_total{app=~"` + strings.Join(ids, "|") + `"}[` + rangeOf(window) + "]))"
}

// VectorQuerier is the slice of the Prometheus client the report uses.
type VectorQuerier interface {
	QueryVectorAt(ctx context.Context, query string, at time.Time) ([]promql.VectorSample, error)
}

// Samples is what Read returned: each query's samples by name, the names
// that failed, and the source.
type Samples struct {
	ByQuery map[string][]promql.VectorSample
	Failed  []string
	Source  string
}

// Budget is the Prometheus time a window's read gets.
func Budget(window time.Duration) time.Duration {
	if window > api.ObsGatewayTrafficShortWindowMax {
		return api.ObsGatewayTrafficLongWindowBudget
	}
	return api.ObsGatewayTrafficPrometheusBudget
}

// Read runs the first-phase queries concurrently, then counts requests for
// the apps the report will list, all evaluated at at and under Budget. A
// missing client is not_configured; every query failing is unavailable; some
// failing is partial, and the report names them.
func Read(ctx context.Context, client *promql.Client, window time.Duration, at time.Time) Samples {
	if client == nil || client.BaseURL() == "" {
		return Samples{Source: SourceNotConfigured, ByQuery: map[string][]promql.VectorSample{}}
	}
	return read(ctx, client.WithTimeout(Budget(window)), window, at)
}

func read(ctx context.Context, q VectorQuerier, window time.Duration, at time.Time) Samples {
	ctx, cancel := context.WithTimeout(ctx, Budget(window))
	defer cancel()
	queries := Queries(window)
	out := Samples{ByQuery: make(map[string][]promql.VectorSample, len(queries)+1)}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for name, query := range queries {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rows, err := q.QueryVectorAt(ctx, query, at)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				out.Failed = append(out.Failed, name)
				return
			}
			out.ByQuery[name] = rows
		}()
	}
	wg.Wait()
	attempted := len(queries)
	if query := AppRequestsQuery(window, listedAppIDs(out.ByQuery)); query != "" {
		attempted++
		if rows, err := q.QueryVectorAt(ctx, query, at); err != nil {
			out.Failed = append(out.Failed, qAppRequests)
		} else {
			out.ByQuery[qAppRequests] = rows
		}
	}
	sort.Strings(out.Failed)
	switch {
	case len(out.Failed) == 0:
		out.Source = SourceObserved
	case len(out.Failed) == attempted:
		out.Source = SourceUnavailable
	default:
		out.Source = SourcePartial
	}
	return out
}

// listedAppIDs is the IDs of the apps the report lists, in its order.
func listedAppIDs(s map[string][]promql.VectorSample) []string {
	rows := apps(s, nil)
	ids := make([]string, len(rows))
	for i, a := range rows {
		ids[i] = a.AppID
	}
	return ids
}

// AppInfo is what the store knows about an app the report lists.
type AppInfo struct {
	Slug      string
	AccountID string
}

// Input is the window, the Prometheus samples and an app lookup.
type Input struct {
	Now     time.Time
	Window  time.Duration
	Samples Samples
	// Lookup resolves an app ID to its slug and account; nil or a miss
	// leaves them empty.
	Lookup func(appID string) (AppInfo, bool)
}

// Build folds the samples into the report.
func Build(in Input) api.ObsGatewayTrafficReport {
	out := api.ObsGatewayTrafficReport{
		AsOf: in.Now, WindowSeconds: int(in.Window.Seconds()), Source: in.Samples.Source, FailedQueries: in.Samples.Failed,
		Nodes: []api.ObsGatewayNodeTraffic{}, Statuses: []api.ObsGatewayStatusCount{}, WakeAdmission: []api.ObsGatewayAdmissionCount{},
		EdgeRejections: []api.ObsGatewayEdgeRejection{}, EdgeRefusals: []api.ObsGatewayReasonCount{},
		RetryExhausted: []api.ObsGatewayReasonCount{}, Apps: []api.ObsGatewayAppTraffic{},
	}
	if out.FailedQueries == nil {
		out.FailedQueries = []string{}
	}
	s := in.Samples.ByQuery
	nodes := map[string]*api.ObsGatewayNodeTraffic{}
	node := func(name string) *api.ObsGatewayNodeTraffic {
		if nodes[name] == nil {
			nodes[name] = &api.ObsGatewayNodeTraffic{Node: name}
		}
		return nodes[name]
	}
	statuses := map[string]int64{}
	for _, r := range s[qRequestsByNodeCode] {
		n, v, code := node(r.Labels["node"]), count(r.Value), r.Labels["code"]
		n.Requests += v
		out.Totals.Requests += v
		statuses[code] += v
		addClass(&out.Totals, code, v)
		if len(code) == 3 && code[0] == '5' {
			n.Status5xx += v
		}
	}
	for _, r := range s[qPlatformByNode] {
		v := count(r.Value)
		node(r.Labels["node"]).PlatformErrors += v
		out.Totals.PlatformErrors += v
	}
	for _, r := range s[qRefusalsByNode] {
		v := count(r.Value)
		node(r.Labels["node"]).CapacityRefusals += v
		out.Totals.CapacityRefusals += v
	}
	for _, r := range s[qWaitersByNode] {
		v := count(r.Value)
		node(r.Labels["node"]).HeldNow += v
		out.Totals.HeldNow += v
	}
	for _, r := range s[qOverload] {
		v := math.Round(r.Value*100) / 100
		node(r.Labels["node"]).OverloadLevel = &v
	}
	for _, r := range s[qAdmissionLeaders] {
		out.Totals.AdmissionLeadersNow += count(r.Value)
	}
	for code, v := range statuses {
		if v > 0 {
			out.Statuses = append(out.Statuses, api.ObsGatewayStatusCount{Code: code, Count: v})
		}
	}
	sort.Slice(out.Statuses, func(i, j int) bool { return out.Statuses[i].Code < out.Statuses[j].Code })
	out.Totals.ErrorRatePercent = rate(out.Totals.Status5xx, out.Totals.Requests)
	out.Totals.PlatformErrorRatePercent = rate(out.Totals.PlatformErrors, out.Totals.Requests)
	admission(&out, s[qWakeAdmission])
	for _, r := range s[qEdgeRejections] {
		if v := count(r.Value); v > 0 {
			out.EdgeRejections = append(out.EdgeRejections, api.ObsGatewayEdgeRejection{Kind: r.Labels["kind"], Status: r.Labels["status"], Count: v})
		}
	}
	sort.Slice(out.EdgeRejections, func(i, j int) bool {
		a, b := out.EdgeRejections[i], out.EdgeRejections[j]
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return a.Kind+a.Status < b.Kind+b.Status
	})
	out.EdgeRefusals = reasons(s[qEdgeRefused])
	out.RetryExhausted = reasons(s[qRetryExhausted])
	for _, n := range nodes {
		n.ErrorRatePercent = rate(n.Status5xx, n.Requests)
		out.Nodes = append(out.Nodes, *n)
	}
	sort.Slice(out.Nodes, func(i, j int) bool {
		a, b := out.Nodes[i], out.Nodes[j]
		if a.PlatformErrors != b.PlatformErrors {
			return a.PlatformErrors > b.PlatformErrors
		}
		return a.Node < b.Node
	})
	out.Totals.Nodes = len(out.Nodes)
	out.Apps = apps(s, in.Lookup)
	return out
}

// addClass counts a full status code into the totals' classes.
func addClass(t *api.ObsGatewayTrafficTotals, code string, v int64) {
	if len(code) != 3 {
		return
	}
	switch code[0] {
	case '2':
		t.Status2xx += v
	case '3':
		t.Status3xx += v
	case '4':
		t.Status4xx += v
	case '5':
		t.Status5xx += v
	}
	switch code {
	case "429":
		t.Status429 += v
	case "502":
		t.Status502 += v
	case "503":
		t.Status503 += v
	case "504":
		t.Status504 += v
	}
}

// admission counts wake admission outcomes by plan and folds the queue full
// and timeout totals.
func admission(out *api.ObsGatewayTrafficReport, rows []promql.VectorSample) {
	for _, r := range rows {
		v := count(r.Value)
		if v == 0 {
			continue
		}
		outcome := r.Labels["outcome"]
		out.WakeAdmission = append(out.WakeAdmission, api.ObsGatewayAdmissionCount{Plan: r.Labels["plan"], Outcome: outcome, Count: v})
		switch outcome {
		case "admitted":
			out.Totals.WakesAdmitted += v
		case "app_queue_full":
			out.Totals.WakeAppQueueFull += v
		case "gateway_queue_full":
			out.Totals.WakeGatewayQueueFull += v
		case "queue_timeout":
			out.Totals.WakeQueueTimeouts += v
		}
	}
	sort.Slice(out.WakeAdmission, func(i, j int) bool {
		a, b := out.WakeAdmission[i], out.WakeAdmission[j]
		if a.Outcome != b.Outcome {
			return a.Outcome < b.Outcome
		}
		return a.Plan < b.Plan
	})
}

func reasons(rows []promql.VectorSample) []api.ObsGatewayReasonCount {
	out := []api.ObsGatewayReasonCount{}
	for _, r := range rows {
		if v := count(r.Value); v > 0 {
			out = append(out, api.ObsGatewayReasonCount{Reason: r.Labels["reason"], Count: v})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Reason < out[j].Reason
	})
	return out
}

// apps joins the per-app queries into the most affected apps: by platform
// errors, then 5xx, then capacity refusals, then requests held now.
func apps(s map[string][]promql.VectorSample, lookup func(string) (AppInfo, bool)) []api.ObsGatewayAppTraffic {
	byID := map[string]*api.ObsGatewayAppTraffic{}
	app := func(r promql.VectorSample) *api.ObsGatewayAppTraffic {
		id := r.Labels["app"]
		if byID[id] == nil {
			byID[id] = &api.ObsGatewayAppTraffic{AppID: id}
		}
		a := byID[id]
		if p := r.Labels["plan"]; p != "" && p != UnknownHostApp && a.Plan == "" {
			a.Plan = p
		}
		return a
	}
	for _, r := range s[qApp5xx] {
		a, v := app(r), count(r.Value)
		a.Status5xx += v
		switch r.Labels["code"] {
		case "502":
			a.Status502 += v
		case "503":
			a.Status503 += v
		case "504":
			a.Status504 += v
		}
	}
	for _, r := range s[qAppPlatform] {
		app(r).PlatformErrors += count(r.Value)
	}
	for _, r := range s[qAppRefusals] {
		app(r).CapacityRefusals += count(r.Value)
	}
	for _, r := range s[qWaitersByApp] {
		app(r).HeldNow += count(r.Value)
	}
	requests := map[string]int64{}
	for _, r := range s[qAppRequests] {
		requests[r.Labels["app"]] = count(r.Value)
	}
	out := make([]api.ObsGatewayAppTraffic, 0, len(byID))
	for id, a := range byID {
		if id == "" {
			continue
		}
		a.Requests = requests[id]
		a.ErrorRatePercent = rate(a.Status5xx, a.Requests)
		out = append(out, *a)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.PlatformErrors != b.PlatformErrors {
			return a.PlatformErrors > b.PlatformErrors
		}
		if a.Status5xx != b.Status5xx {
			return a.Status5xx > b.Status5xx
		}
		if a.CapacityRefusals != b.CapacityRefusals {
			return a.CapacityRefusals > b.CapacityRefusals
		}
		if a.HeldNow != b.HeldNow {
			return a.HeldNow > b.HeldNow
		}
		return a.AppID < b.AppID
	})
	if len(out) > api.ObsGatewayTrafficAppsMax {
		out = out[:api.ObsGatewayTrafficAppsMax]
	}
	for i := range out {
		if out[i].AppID == UnknownHostApp {
			out[i].UnknownHost = true
			continue
		}
		if lookup != nil {
			if info, ok := lookup(out[i].AppID); ok {
				out[i].AppSlug, out[i].AccountID = info.Slug, info.AccountID
			}
		}
	}
	return out
}

// count rounds an increase() to a whole count; increase extrapolates, so
// it is rarely an integer.
func count(v float64) int64 {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return 0
	}
	return int64(math.Round(v))
}

// rate is part of whole as a percentage with two decimals, 0 without traffic.
func rate(part, whole int64) float64 {
	if whole <= 0 {
		return 0
	}
	return math.Round(float64(part)*10000/float64(whole)) / 100
}
