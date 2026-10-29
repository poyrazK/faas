// commands_obs_node.go — `gregalectl obs node <name>`.
//
// One support page for one compute node, from three existing admin reads:
// GET /v1/admin/obs/nodes/{name}/detail (capacity, admitted RAM against the
// ceiling, apps, instances, drain safety), .../heartbeats (freshness and
// gaps) and /v1/admin/obs/nodes/wake-latency (this node's wake quantiles
// against the fleet p95). Heartbeats and wake latency are best effort: the
// page still prints when one of them is unavailable.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const subObsNode = "node"

// obsNodeNameRE is the manifest's node-name rule (pkg/manifest validNodeName).
var obsNodeNameRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]{0,61}[a-z0-9])?$`)

// obsNodePage is what `obs node --json` emits.
type obsNodePage struct {
	Detail         api.ObsNodeDetailResponse     `json:"detail"`
	Heartbeats     *api.ObsHeartbeatListResponse `json:"heartbeats,omitempty"`
	HeartbeatsNote string                        `json:"heartbeats_note,omitempty"`
	WakeLatency    *api.ObsWakeLatencyQuantile   `json:"wake_latency,omitempty"`
	FleetWakeP95MS float64                       `json:"fleet_wake_p95_ms,omitempty"`
	WakeWindow     string                        `json:"wake_window,omitempty"`
	WakeNote       string                        `json:"wake_note,omitempty"`
}

func cmdObsNode(args []string) int {
	fs := flag.NewFlagSet(subObsNode, flag.ContinueOnError)
	fs.SetOutput(osStderr)
	token := fs.String("admin-token", "", obsAdminTokenUsage)
	timeout := fs.Duration("timeout", 30*time.Second, "HTTP timeout per request")
	heartbeats := fs.Int("heartbeats", 20, "recent heartbeats to read (1-100)")
	instances := fs.Int("instances", 20, "live instances to list (0-100)")
	jsonOut := fs.Bool("json", false, "emit structured JSON")
	if err := fs.Parse(reorderObsArgs(args, "admin-token", "timeout", "heartbeats", "instances")); err != nil {
		return 2
	}
	name := fs.Arg(0)
	if fs.NArg() != 1 || !obsNodeNameRE.MatchString(name) || strings.Contains(name, "..") || *timeout <= 0 ||
		*heartbeats < 1 || *heartbeats > obsAppListMax || *instances < 0 || *instances > obsAppListMax {
		_, _ = fmt.Fprintln(osStderr, "usage: gregalectl obs node <name> [--heartbeats 1-100] [--instances 0-100] [--json]")
		return 2
	}
	cred, ok := resolveObsCredential("obs node", *token)
	if !ok {
		return 2
	}
	client := &http.Client{Timeout: *timeout}
	ctx := context.Background()
	base := apidBase() + "/v1/admin/obs/nodes/"
	var page obsNodePage
	if err := fetchObsBundleSection(ctx, client, base+url.PathEscape(name)+"/detail", cred, "node", &page.Detail); err != nil {
		if strings.Contains(err.Error(), "HTTP 404") {
			_, _ = fmt.Fprintf(osStderr, "gregalectl obs node: no compute node is named %s\n", name)
			return 1
		}
		_, _ = fmt.Fprintf(osStderr, "gregalectl obs %v\n", err)
		return 1
	}
	var hb api.ObsHeartbeatListResponse
	if err := fetchObsBundleSection(ctx, client, base+url.PathEscape(name)+"/heartbeats?limit="+strconv.Itoa(*heartbeats), cred, "heartbeats", &hb); err != nil {
		page.HeartbeatsNote = err.Error()
	} else {
		page.Heartbeats = &hb
	}
	var latency api.ObsNodeWakeLatencyResponse
	if err := fetchObsBundleSection(ctx, client, base+"wake-latency", cred, "wake latency", &latency); err != nil {
		page.WakeNote = err.Error()
	} else {
		page.WakeWindow, page.FleetWakeP95MS = latency.Window, latency.FleetP95MS
		for i, q := range latency.Quantiles {
			if q.NodeID == page.Detail.Node.ID {
				page.WakeLatency = &latency.Quantiles[i]
			}
		}
		if !latency.PrometheusAvailable {
			page.WakeNote = "Prometheus is not available to apid"
		}
	}
	if *jsonOut || jsonOutput || jsonFlagEnabled(args) {
		return emitOperatorJSON(page)
	}
	writeObsNode(osStdout, page, time.Now(), *instances)
	return 0
}

func writeObsNode(w io.Writer, p obsNodePage, now time.Time, instances int) {
	n := p.Detail.Node
	active := "active"
	if !n.Active {
		active = "INACTIVE"
	}
	_, _ = fmt.Fprintf(w, "node %s  id=%s  %s  overlay=%s  created %s\n", n.Name, n.ID, active, orDash(n.OverlayIP), obsAppTime(n.CreatedAt))
	_, _ = fmt.Fprintf(w, "capacity  %d vCPU  %d MB  max_concurrency %d  admission ceiling %d MB\n", n.VPCPUs, n.MemMB, n.MaxConcurrency, n.AdmissionCeilingMB)
	line := fmt.Sprintf("now  RAM admitted %d of %d MB", n.RAMUsedMB, n.AdmissionCeilingMB)
	if n.AdmissionCeilingMB > 0 {
		line += fmt.Sprintf(" (%.0f%%, margin %d MB)", 100*float64(n.RAMUsedMB)/float64(n.AdmissionCeilingMB), n.AdmissionMarginMB)
	}
	line += fmt.Sprintf("  live %d (%d running, %d waking, %d cold booting)", n.InstancesLive, n.InstancesRunning, n.InstancesWaking, n.InstancesColdBooting)
	if n.CPUPct60s != nil {
		line += fmt.Sprintf("  cpu %.1f%% (60s)", *n.CPUPct60s)
	}
	if n.DiskUsedBytes != nil {
		line += fmt.Sprintf("  snapshot disk %.1f GB", float64(*n.DiskUsedBytes)/1e9)
	}
	_, _ = fmt.Fprintln(w, line)
	if n.AdmissionMarginMB < 0 {
		_, _ = fmt.Fprintln(w, "  over the admission ceiling: new wakes go to other nodes")
	}
	writeObsNodeHeartbeats(w, p, now)
	writeObsNodeWakes(w, p)
	d := p.Detail.Drain
	if d.DrainSafe {
		_, _ = fmt.Fprintln(w, "drain  safe: no running, waking or cold-booting instances")
	} else {
		_, _ = fmt.Fprintf(w, "drain  not safe: %d running, %d waking, %d cold booting\n", d.RunningInstances, d.WakingInstances, d.ColdBooting)
	}
	writeObsNodeApps(w, p.Detail.Apps)
	writeObsNodeInstances(w, p.Detail.Instances, instances)
}

func writeObsNodeHeartbeats(w io.Writer, p obsNodePage, now time.Time) {
	last := p.Detail.Node.LastHeartbeatAt
	line := "heartbeat  last -"
	if !last.IsZero() {
		line = fmt.Sprintf("heartbeat  last %s (%s ago)", obsAppTime(last), now.Sub(last).Round(time.Second))
	}
	switch {
	case p.Heartbeats == nil:
		line += "; history unavailable: " + p.HeartbeatsNote
	case len(p.Heartbeats.Heartbeats) == 0:
		line += "; no heartbeats in the history window"
	default:
		var missed, stale int
		var maxGap int64
		for _, h := range p.Heartbeats.Heartbeats {
			if h.Missed {
				missed++
			}
			if h.Stale {
				stale++
			}
			if h.GapToPreviousMs > maxGap {
				maxGap = h.GapToPreviousMs
			}
		}
		line += fmt.Sprintf("; last %d: max gap %s, %d missed, %d stale", len(p.Heartbeats.Heartbeats), (time.Duration(maxGap) * time.Millisecond).Round(time.Millisecond), missed, stale)
	}
	_, _ = fmt.Fprintln(w, line)
}

func writeObsNodeWakes(w io.Writer, p obsNodePage) {
	switch {
	case p.WakeNote != "" && p.WakeLatency == nil:
		_, _ = fmt.Fprintln(w, "wakes  unavailable: "+p.WakeNote)
	case p.WakeLatency == nil || p.WakeLatency.SampleCount == 0:
		_, _ = fmt.Fprintf(w, "wakes (%s)  none on this node  (fleet p95 %.0f ms)\n", orDash(p.WakeWindow), p.FleetWakeP95MS)
	default:
		q := p.WakeLatency
		_, _ = fmt.Fprintf(w, "wakes (%s)  p50 %.0f ms  p95 %.0f ms  p99 %.0f ms over %d wakes  (fleet p95 %.0f ms)\n",
			p.WakeWindow, q.P50MS, q.P95MS, q.P99MS, q.SampleCount, p.FleetWakeP95MS)
	}
}

// writeObsNodeApps lists the apps holding the most admitted RAM first.
func writeObsNodeApps(w io.Writer, apps []api.ObsNodeApp) {
	sorted := append([]api.ObsNodeApp(nil), apps...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].RAMUsedMB > sorted[j].RAMUsedMB })
	_, _ = fmt.Fprintf(w, "apps  %d", len(sorted))
	if len(sorted) > 10 {
		_, _ = fmt.Fprint(w, ", top 10 by admitted RAM")
	}
	_, _ = fmt.Fprintln(w)
	for i, a := range sorted {
		if i == 10 {
			break
		}
		last := "-"
		if a.LastRequestAt != nil {
			last = obsAppTime(*a.LastRequestAt)
		}
		_, _ = fmt.Fprintf(w, "  %-24s %6d MB  live %d (%d running, %d waking, %d cold)  last request %s\n",
			shortID(a.Slug, 24), a.RAMUsedMB, a.InstancesLive, a.InstancesRunning, a.InstancesWaking, a.InstancesColdBooting, last)
	}
}

// writeObsNodeInstances lists live instances (not parked, stopped or failed),
// each with the wake that started it.
func writeObsNodeInstances(w io.Writer, rows []api.ObsInstanceRow, limit int) {
	var live []api.ObsInstanceRow
	for _, in := range rows {
		if s := state.State(in.State); s != state.StateParked && s != state.StateStopped && s != state.StateFailed {
			live = append(live, in)
		}
	}
	_, _ = fmt.Fprintf(w, "instances  %d live of %d\n", len(live), len(rows))
	withWake := false
	for i, in := range live {
		if i == limit {
			break
		}
		_, _ = fmt.Fprintf(w, "  %-12s %-24s %5d MB  started %s  last request %s  wake %s\n",
			in.State, shortID(orDash(in.AppSlug), 24), in.RAMMB, obsAppTime(in.StartedAt), obsAppTime(in.LastRequestAt), orDash(in.WakeID))
		withWake = withWake || in.WakeID != ""
	}
	if withWake && limit > 0 {
		_, _ = fmt.Fprintln(w, "  one wake's events and logs: gregalectl obs wake <wake_id>")
	}
}
