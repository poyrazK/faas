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
	qDeniedByClass   = "denied_by_class"
	qDeniedByNode    = "denied_by_node"
	qDeniedLastHour  = "denied_last_hour"
	qDeniedApps      = "denied_apps"
	qDeniedAppsClass = "denied_apps_by_class"
	qAppsDenied      = "apps_denied"
	qNewDestTotal    = "new_destinations_total"
	qNewDestApps     = "new_destinations_apps"
	qRecycles        = "abuse_recycles"
	qHolds           = "abuse_holds"
	qCPU             = "cpu_abuse"
	qDNS             = "dns_blocked"
	qScan            = "scan_findings"
	qDenyFirewall    = "denylist_firewall"
	qDenyImagePull   = "denylist_image_pull"
	qFlowLogRows     = "flow_log_rows"
)

func rangeOf(window time.Duration) string { return strconv.Itoa(int(window.Seconds())) + "s" }

// Queries returns the report's PromQL for a window, by name.
func Queries(window time.Duration) map[string]string {
	w := rangeOf(window)
	k := strconv.Itoa(api.ObsEgressAppsMax)
	inc := func(m string) string { return "increase(" + m + "[" + w + "])" }
	denied := inc(`vmmd_egress_denied_total{app!=""}`)
	topDenied := "topk(" + k + ", sum by (app) (" + denied + ") > 0)"
	return map[string]string{
		qDeniedByClass:   "sum by (class) (" + denied + ") > 0",
		qDeniedByNode:    "sum by (node) (" + denied + ") > 0",
		qDeniedLastHour:  `sum(increase(vmmd_egress_denied_total{app!=""}[1h]))`,
		qDeniedApps:      topDenied,
		qDeniedAppsClass: "sum by (app, class) (" + denied + ") > 0 and on (app) " + topDenied,
		qAppsDenied:      "count(sum by (app) (" + denied + ") > 0)",
		qNewDestTotal:    "sum(" + inc(`vmmd_egress_new_destinations_total{app!=""}`) + ")",
		qNewDestApps:     "topk(" + k + `, sum by (app) (` + inc(`vmmd_egress_new_destinations_total{app!=""}`) + ") > 0)",
		qRecycles:        `sum by (app, reason) (` + inc(`schedd_egress_abuse_recycles_total{app!=""}`) + ") > 0",
		qHolds:           "sum by (reason) (" + inc("schedd_account_abuse_holds_total") + ") > 0",
		qCPU:             "sum by (plan, action) (" + inc("schedd_cpu_abuse_signals_total") + ") > 0",
		qDNS:             "sum by (category) (" + inc("gatewayd_dns_blocked_total") + ") > 0",
		qScan:            "sum by (category, action) (" + inc("imaged_abuse_scan_findings_total") + ") > 0",
		qDenyFirewall:    "sum by (cidr) (" + inc("vmmd_egress_deny_total") + ") > 0",
		qDenyImagePull:   "sum by (cidr) (" + inc("imaged_oci_egress_deny_total") + ") > 0",
		qFlowLogRows:     "sum(" + inc("vmmd_egress_flow_log_rows_total") + ")",
	}
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

// Read runs every query concurrently at one instant under Budget(window).
func Read(ctx context.Context, client *promql.Client, window time.Duration, at time.Time) Samples {
	if client == nil || client.BaseURL() == "" {
		return Samples{Source: SourceNotConfigured, ByQuery: map[string][]promql.VectorSample{}}
	}
	return read(ctx, client, window, at)
}

func read(ctx context.Context, q VectorQuerier, window time.Duration, at time.Time) Samples {
	ctx, cancel := context.WithTimeout(ctx, Budget(window))
	defer cancel()
	queries := Queries(window)
	out := Samples{ByQuery: make(map[string][]promql.VectorSample, len(queries))}
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
	sort.Strings(out.Failed)
	switch {
	case len(out.Failed) == 0:
		out.Source = SourceObserved
	case len(out.Failed) == len(queries):
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

var appIDRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

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
	out.Apps = apps(s, out.Recycles, lookup)
	if rows := s[qAppsDenied]; len(rows) > 0 {
		t.AppsDenied = int(count(rows[0].Value))
	}
	sortCPU(out.CPUSignals)
	sortScan(out.ScanFindings)
	sort.Slice(out.Denylist, func(i, j int) bool {
		if out.Denylist[i].Count != out.Denylist[j].Count {
			return out.Denylist[i].Count > out.Denylist[j].Count
		}
		return out.Denylist[i].Source+out.Denylist[i].CIDR < out.Denylist[j].Source+out.Denylist[j].CIDR
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
		if c[i].Count != c[j].Count {
			return c[i].Count > c[j].Count
		}
		return c[i].Key < c[j].Key
	})
}

func sortCPU(c []api.ObsEgressCPUSignal) {
	sort.Slice(c, func(i, j int) bool {
		if c[i].Count != c[j].Count {
			return c[i].Count > c[j].Count
		}
		return c[i].Plan+c[i].Action < c[j].Plan+c[j].Action
	})
}

func sortScan(c []api.ObsEgressScanFinding) {
	sort.Slice(c, func(i, j int) bool {
		if c[i].Count != c[j].Count {
			return c[i].Count > c[j].Count
		}
		return c[i].Category+c[i].Action < c[j].Category+c[j].Action
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
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].AppID+out[i].Reason < out[j].AppID+out[j].Reason
	})
	return capRows(out)
}

// apps merges the top apps by denials, by new destinations and the recycled
// apps: recycled first, then by denials, then by new destinations.
func apps(s map[string][]promql.VectorSample, recycled []api.ObsEgressRecycle, lookup func(string) AppInfo) []api.ObsEgressApp {
	rows := map[string]*api.ObsEgressApp{}
	row := func(id string) *api.ObsEgressApp {
		if rows[id] == nil {
			rows[id] = &api.ObsEgressApp{AppID: id, Classes: []api.ObsEgressCount{}}
		}
		return rows[id]
	}
	for _, r := range s[qDeniedApps] {
		if id := r.Labels["app"]; appIDRE.MatchString(id) {
			if n := count(r.Value); n > 0 {
				row(id).Denied = n
			}
		}
	}
	for _, r := range s[qDeniedAppsClass] {
		id := r.Labels["app"]
		if rows[id] == nil {
			continue
		}
		if n := count(r.Value); n > 0 {
			rows[id].Classes = append(rows[id].Classes, api.ObsEgressCount{Key: r.Labels["class"], Count: n})
		}
	}
	for _, r := range s[qNewDestApps] {
		if id := r.Labels["app"]; appIDRE.MatchString(id) {
			if n := count(r.Value); n > 0 {
				row(id).NewDestinations = n
			}
		}
	}
	for _, r := range recycled {
		row(r.AppID).AbuseRecycles += r.Count
	}
	out := make([]api.ObsEgressApp, 0, len(rows))
	for _, a := range rows {
		sortCounts(a.Classes)
		info := lookup(a.AppID)
		a.AppSlug, a.AccountID = info.Slug, info.AccountID
		out = append(out, *a)
	}
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
	if len(out) > api.ObsEgressAppsMax {
		out = out[:api.ObsEgressAppsMax]
	}
	return out
}
