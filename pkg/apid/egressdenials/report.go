// Package egressdenials builds the operator egress denials and abuse report
// (ADR-20261127100000): tenant egress the platform dropped (vmmd's per-app
// nftables counters, by class), new destinations, and the abuse signals
// schedd, gatewayd's bridge resolver and imaged emit. It reads only
// Prometheus; app labels are resolved to slugs and accounts through a lookup.
package egressdenials

import (
	"context"
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

// Denylist sources.
const (
	DenylistFirewall  = "firewall"
	DenylistImagePull = "image_pull"
)

// Query names.
const (
	qDeniedByClass  = "denied_by_class"
	qDeniedByNode   = "denied_by_node"
	qDeniedLastHour = "denied_last_hour"
	qDeniedApps     = "denied_apps"
	qAppDenials     = "app_denials"
	qAppsDenied     = "apps_denied"
	qNewDestTotal   = "new_destinations_total"
	qNewDestApps    = "new_destinations_apps"
	qRecycles       = "abuse_recycles"
	qHolds          = "abuse_holds"
	qCPU            = "cpu_abuse"
	qDNS            = "dns_blocked"
	qScan           = "scan_findings"
	qDenyFirewall   = "denylist_firewall"
	qDenyImagePull  = "denylist_image_pull"
	qFlowLogRows    = "flow_log_rows"
)

func rangeOf(window time.Duration) string { return strconv.Itoa(int(window.Seconds())) + "s" }

// appIDPattern matches the app UUIDs the report lists; it is used in Go and,
// as an RE2 label matcher, in PromQL.
const appIDPattern = `[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`

// RecentWindow is the span denied_last_hour covers: the last hour, or the
// whole window when that is shorter.
func RecentWindow(window time.Duration) time.Duration {
	return min(window, time.Hour)
}

// Queries returns the report's first-phase PromQL for a window, by name.
func Queries(window time.Duration) map[string]string {
	w := rangeOf(window)
	k := strconv.Itoa(api.ObsEgressAppsMax)
	inc := func(m string) string { return "increase(" + m + "[" + w + "])" }
	denied := inc(`vmmd_egress_denied_total{app!=""}`)
	return map[string]string{
		qDeniedByClass:  "sum by (class) (" + denied + ") > 0",
		qDeniedByNode:   "sum by (node) (" + denied + ") > 0",
		qDeniedLastHour: `sum(increase(vmmd_egress_denied_total{app!=""}[` + rangeOf(RecentWindow(window)) + "]))",
		qDeniedApps:     "topk(" + k + ", sum by (app) (" + denied + ") > 0)",
		qAppsDenied:     "count(sum by (app) (" + inc(`vmmd_egress_denied_total{app=~"`+appIDPattern+`"}`) + ") > 0)",
		qNewDestTotal:   "sum(" + inc(`vmmd_egress_new_destinations_total{app!=""}`) + ")",
		qNewDestApps:    "topk(" + k + `, sum by (app) (` + inc(`vmmd_egress_new_destinations_total{app!=""}`) + ") > 0)",
		qRecycles:       `sum by (app, reason) (` + inc(`schedd_egress_abuse_recycles_total{app!=""}`) + ") > 0",
		qHolds:          "sum by (reason) (" + inc("schedd_account_abuse_holds_total") + ") > 0",
		qCPU:            "sum by (plan, action) (" + inc("schedd_cpu_abuse_signals_total") + ") > 0",
		qDNS:            "sum by (category) (" + inc("gatewayd_dns_blocked_total") + ") > 0",
		qScan:           "sum by (category, action) (" + inc("imaged_abuse_scan_findings_total") + ") > 0",
		qDenyFirewall:   "sum by (cidr) (" + inc("vmmd_egress_deny_total") + ") > 0",
		qDenyImagePull:  "sum by (cidr) (" + inc("imaged_oci_egress_deny_total") + ") > 0",
		qFlowLogRows:    "sum(" + inc("vmmd_egress_flow_log_rows_total") + ")",
	}
}

// AppDenialsQuery reads denials by class for exactly the listed apps, so an
// app listed for new destinations or recycles still shows its denials. It
// is empty when no app is listed. IDs are UUIDs, safe inside the matcher.
func AppDenialsQuery(window time.Duration, appIDs []string) string {
	if len(appIDs) == 0 {
		return ""
	}
	return `sum by (app, class) (increase(vmmd_egress_denied_total{app=~"` + strings.Join(appIDs, "|") + `"}[` + rangeOf(window) + "])) > 0"
}

// Budget is the Prometheus budget for a window: long windows read more
// samples.
func Budget(window time.Duration) time.Duration {
	if window > api.ObsEgressShortWindowMax {
		return api.ObsEgressLongWindowBudget
	}
	return api.ObsEgressPrometheusBudget
}

// VectorQuerier is the slice of the Prometheus client the report uses.
type VectorQuerier interface {
	QueryVectorAt(ctx context.Context, query string, at time.Time) ([]promql.VectorSample, error)
}

// Samples is what Read returned.
type Samples struct {
	ByQuery map[string][]promql.VectorSample
	Failed  []string
	Source  string
}

// Read runs every query at one instant under Budget(window): the first phase
// concurrently, then the listed apps' denials. The client's own per-query
// timeout is raised to the budget, or it would cut long windows short.
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
	if query := AppDenialsQuery(window, listedAppIDs(out.ByQuery)); query != "" {
		attempted++
		if rows, err := q.QueryVectorAt(ctx, query, at); err != nil {
			out.Failed = append(out.Failed, qAppDenials)
		} else {
			out.ByQuery[qAppDenials] = rows
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

// AppInfo is what the store knows about an app.
type AppInfo struct {
	Slug      string
	AccountID string
}

// Input is the instant, window, samples and an app lookup.
type Input struct {
	Now     time.Time
	Window  time.Duration
	Samples Samples
	// Lookup resolves an app ID; nil or a miss leaves slug and account
	// empty.
	Lookup func(appID string) (AppInfo, bool)
}

var appIDRE = regexp.MustCompile(`^` + appIDPattern + `$`)

// byCountThen orders largest count first, then by the given keys in turn.
func byCountThen(a, b int64, keys ...[2]string) bool {
	if a != b {
		return a > b
	}
	for _, k := range keys {
		if k[0] != k[1] {
			return k[0] < k[1]
		}
	}
	return false
}

// count rounds an increase() to a whole count; increase extrapolates.
func count(v float64) int64 {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return 0
	}
	return int64(math.Round(v))
}

// Build folds the samples into the report.
func Build(in Input) api.ObsEgressReport {
	s := in.Samples.ByQuery
	out := api.ObsEgressReport{
		AsOf: in.Now, WindowSeconds: int(in.Window.Seconds()), Source: in.Samples.Source, FailedQueries: in.Samples.Failed,
		Classes: counts(s[qDeniedByClass], "class"), Nodes: counts(s[qDeniedByNode], "node"),
		Holds: counts(s[qHolds], "reason"), DNSBlocked: counts(s[qDNS], "category"),
		Apps: []api.ObsEgressApp{}, Recycles: []api.ObsEgressRecycle{},
		CPUSignals: []api.ObsEgressCPUSignal{}, ScanFindings: []api.ObsEgressScanFinding{}, Denylist: []api.ObsEgressDenylistDrop{},
	}
	if out.FailedQueries == nil {
		out.FailedQueries = []string{}
	}
	t := &out.Totals
	for _, c := range out.Classes {
		t.Denied += c.Count
	}
	if rows := s[qDeniedLastHour]; len(rows) > 0 {
		t.DeniedLastHour = count(rows[0].Value)
	}
	if rows := s[qNewDestTotal]; len(rows) > 0 {
		t.NewDestinations = count(rows[0].Value)
	}
	if rows, ok := s[qFlowLogRows]; ok {
		var n int64
		if len(rows) > 0 {
			n = count(rows[0].Value)
		}
		t.FlowLogRows = &n
	}
	for _, h := range out.Holds {
		t.AbuseHolds += h.Count
	}
	for _, d := range out.DNSBlocked {
		t.DNSBlocked += d.Count
	}
	for _, r := range s[qCPU] {
		if n := count(r.Value); n > 0 {
			out.CPUSignals = append(out.CPUSignals, api.ObsEgressCPUSignal{Plan: r.Labels["plan"], Action: r.Labels["action"], Count: n})
			t.CPUAbuseSignals += n
		}
	}
	for _, r := range s[qScan] {
		if n := count(r.Value); n > 0 {
			f := api.ObsEgressScanFinding{Category: r.Labels["category"], Action: r.Labels["action"], Count: n}
			out.ScanFindings = append(out.ScanFindings, f)
			switch f.Action {
			case "block":
				t.ScanBlocked += n
			case "flag":
				t.ScanFlagged += n
			}
		}
	}
	for src, q := range map[string]string{DenylistFirewall: qDenyFirewall, DenylistImagePull: qDenyImagePull} {
		for _, r := range s[q] {
			if n := count(r.Value); n > 0 {
				out.Denylist = append(out.Denylist, api.ObsEgressDenylistDrop{Source: src, CIDR: r.Labels["cidr"], Count: n})
			}
		}
	}
	lookup := cachedLookup(in.Lookup)
	out.Recycles = recycles(s[qRecycles], lookup)
	for _, r := range out.Recycles {
		t.AbuseRecycles += r.Count
	}
	out.Apps = apps(s, lookup)
	if rows := s[qAppsDenied]; len(rows) > 0 {
		t.AppsDenied = int(count(rows[0].Value))
	}
	sort.Slice(out.CPUSignals, func(i, j int) bool {
		a, b := out.CPUSignals[i], out.CPUSignals[j]
		return byCountThen(a.Count, b.Count, [2]string{a.Plan, b.Plan}, [2]string{a.Action, b.Action})
	})
	sort.Slice(out.ScanFindings, func(i, j int) bool {
		a, b := out.ScanFindings[i], out.ScanFindings[j]
		return byCountThen(a.Count, b.Count, [2]string{a.Category, b.Category}, [2]string{a.Action, b.Action})
	})
	sort.Slice(out.Denylist, func(i, j int) bool {
		a, b := out.Denylist[i], out.Denylist[j]
		return byCountThen(a.Count, b.Count, [2]string{a.Source, b.Source}, [2]string{a.CIDR, b.CIDR})
	})
	out.CPUSignals = capRows(out.CPUSignals)
	out.ScanFindings = capRows(out.ScanFindings)
	out.Denylist = capRows(out.Denylist)
	return out
}

func capRows[T any](rows []T) []T {
	if len(rows) > api.ObsEgressRowsMax {
		return rows[:api.ObsEgressRowsMax]
	}
	return rows
}

// counts turns samples into counts keyed by a label, largest first, zeros
// dropped.
func counts(rows []promql.VectorSample, label string) []api.ObsEgressCount {
	out := []api.ObsEgressCount{}
	for _, r := range rows {
		if n := count(r.Value); n > 0 {
			out = append(out, api.ObsEgressCount{Key: r.Labels[label], Count: n})
		}
	}
	sortCounts(out)
	return capRows(out)
}

func sortCounts(c []api.ObsEgressCount) {
	sort.Slice(c, func(i, j int) bool {
		return byCountThen(c[i].Count, c[j].Count, [2]string{c[i].Key, c[j].Key})
	})
}

// cachedLookup resolves each app at most once.
func cachedLookup(lookup func(string) (AppInfo, bool)) func(string) AppInfo {
	seen := map[string]AppInfo{}
	return func(id string) AppInfo {
		if info, ok := seen[id]; ok {
			return info
		}
		var info AppInfo
		if lookup != nil {
			if got, ok := lookup(id); ok {
				info = got
			}
		}
		seen[id] = info
		return info
	}
}

func recycles(rows []promql.VectorSample, lookup func(string) AppInfo) []api.ObsEgressRecycle {
	out := []api.ObsEgressRecycle{}
	for _, r := range rows {
		id, n := r.Labels["app"], count(r.Value)
		if !appIDRE.MatchString(id) || n <= 0 {
			continue
		}
		info := lookup(id)
		out = append(out, api.ObsEgressRecycle{AppID: id, AppSlug: info.Slug, AccountID: info.AccountID, Reason: r.Labels["reason"], Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		return byCountThen(a.Count, b.Count, [2]string{a.AppID, b.AppID}, [2]string{a.Reason, b.Reason})
	})
	return capRows(out)
}

// rankApps merges the top apps by denials, the top apps by new destinations
// and the recycled apps, ranks them (recycled first, then by denials, then by
// new destinations) and keeps the first ObsEgressAppsMax. Denials here come
// from the topk read, so they are zero for an app outside the top denied.
func rankApps(s map[string][]promql.VectorSample) []*api.ObsEgressApp {
	rows := map[string]*api.ObsEgressApp{}
	row := func(id string) *api.ObsEgressApp {
		if rows[id] == nil {
			rows[id] = &api.ObsEgressApp{AppID: id, Classes: []api.ObsEgressCount{}}
		}
		return rows[id]
	}
	for _, r := range s[qDeniedApps] {
		if id, n := r.Labels["app"], count(r.Value); appIDRE.MatchString(id) && n > 0 {
			row(id).Denied = n
		}
	}
	for _, r := range s[qNewDestApps] {
		if id, n := r.Labels["app"], count(r.Value); appIDRE.MatchString(id) && n > 0 {
			row(id).NewDestinations = n
		}
	}
	for _, r := range s[qRecycles] {
		if id, n := r.Labels["app"], count(r.Value); appIDRE.MatchString(id) && n > 0 {
			row(id).AbuseRecycles += n
		}
	}
	out := make([]*api.ObsEgressApp, 0, len(rows))
	for _, a := range rows {
		out = append(out, a)
	}
	sortApps(out)
	if len(out) > api.ObsEgressAppsMax {
		out = out[:api.ObsEgressAppsMax]
	}
	return out
}

func sortApps(out []*api.ObsEgressApp) {
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.AbuseRecycles != b.AbuseRecycles {
			return a.AbuseRecycles > b.AbuseRecycles
		}
		if a.Denied != b.Denied {
			return a.Denied > b.Denied
		}
		if a.NewDestinations != b.NewDestinations {
			return a.NewDestinations > b.NewDestinations
		}
		return a.AppID < b.AppID
	})
}

// listedAppIDs is the IDs of the apps the report lists.
func listedAppIDs(s map[string][]promql.VectorSample) []string {
	rows := rankApps(s)
	ids := make([]string, len(rows))
	for i, a := range rows {
		ids[i] = a.AppID
	}
	return ids
}

// apps is the listed apps with their denials by class from the second-phase
// read, which covers every listed app. When that read failed (it is then in
// failed_queries) the topk denials stand and the classes are empty.
func apps(s map[string][]promql.VectorSample, lookup func(string) AppInfo) []api.ObsEgressApp {
	listed := rankApps(s)
	if rows, ok := s[qAppDenials]; ok {
		byID := make(map[string]*api.ObsEgressApp, len(listed))
		for _, a := range listed {
			a.Denied = 0
			byID[a.AppID] = a
		}
		for _, r := range rows {
			a, n := byID[r.Labels["app"]], count(r.Value)
			if a == nil || n <= 0 {
				continue
			}
			a.Classes = append(a.Classes, api.ObsEgressCount{Key: r.Labels["class"], Count: n})
			a.Denied += n
		}
		sortApps(listed)
	}
	out := make([]api.ObsEgressApp, 0, len(listed))
	for _, a := range listed {
		sortCounts(a.Classes)
		info := lookup(a.AppID)
		a.AppSlug, a.AccountID = info.Slug, info.AccountID
		out = append(out, *a)
	}
	return out
}
