// commands_obs_app.go — `gregalectl obs app <app_id|slug>`.
//
// One support page for one app, from GET /v1/admin/obs/apps/{id}: what the
// app is, what is live, whether its next wake restores or cold boots, where
// its instances run, its health over --range, its recent wakes (each one
// `gregalectl obs wake <wake_id>` away), its top errors and its last
// deployments. An operator with a customer report usually has the slug.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/state"
)

const subObsApp = "app"

// obsAppListMax bounds every table the page prints; --json has everything.
const obsAppListMax = 100

func cmdObsApp(args []string) int {
	fs := flag.NewFlagSet(subObsApp, flag.ContinueOnError)
	fs.SetOutput(osStderr)
	token := fs.String("admin-token", "", obsAdminTokenUsage)
	timeout := fs.Duration("timeout", 30*time.Second, "HTTP timeout")
	rng := fs.String("range", "1h", "health window: "+strings.Join(appmetrics.Ranges(), "|"))
	wakes := fs.Int("wakes", 10, "recent wakes to list (0-100)")
	jsonOut := fs.Bool("json", false, "emit the full response as JSON")
	if err := fs.Parse(reorderObsArgs(args, "admin-token", "timeout", "range", "wakes")); err != nil {
		return 2
	}
	ref := fs.Arg(0)
	if fs.NArg() != 1 || (uuid.Validate(ref) != nil && !api.ValidAppSlug(ref)) || !appmetrics.IsValidRange(*rng) ||
		*timeout <= 0 || *wakes < 0 || *wakes > obsAppListMax {
		_, _ = fmt.Fprintf(osStderr, "usage: gregalectl obs app <app_id|slug> [--range %s] [--wakes 0-100] [--json]\n", strings.Join(appmetrics.Ranges(), "|"))
		return 2
	}
	cred, ok := resolveObsCredential("obs app", *token)
	if !ok {
		return 2
	}
	var out api.ObsAppDetailResponse
	address := apidBase() + "/v1/admin/obs/apps/" + url.PathEscape(ref) + "?range=" + url.QueryEscape(*rng)
	if err := fetchObsBundleSection(context.Background(), &http.Client{Timeout: *timeout}, address, cred, "app", &out); err != nil {
		if strings.Contains(err.Error(), "HTTP 404") {
			_, _ = fmt.Fprintf(osStderr, "gregalectl obs app: no app has the id or slug %s\n", ref)
			return 1
		}
		_, _ = fmt.Fprintf(osStderr, "gregalectl obs %v\n", err)
		return 1
	}
	if *jsonOut || jsonOutput || jsonFlagEnabled(args) {
		return emitOperatorJSON(out)
	}
	writeObsApp(osStdout, out, *wakes)
	return 0
}

func writeObsApp(w io.Writer, d api.ObsAppDetailResponse, wakes int) {
	a := d.App
	_, _ = fmt.Fprintf(w, "app %s  id=%s  account=%s  %s %s  status=%s\n", a.Slug, a.ID, a.AccountID, a.Type, orDash(a.Runtime), a.Status)
	_, _ = fmt.Fprintf(w, "shape  ram=%d MB  max_concurrency=%d  min_instances=%d  created %s\n", a.RAMMB, a.MaxConcurrency, a.MinInstances, obsAppTime(a.CreatedAt))
	writeObsAppLive(w, d)
	writeObsAppInstances(w, d.Instances)
	writeObsAppHealth(w, d.Health)
	writeObsAppWakes(w, d.Wakes, wakes)
	writeObsAppDeployments(w, d.Deployments)
}

// writeObsAppLive prints each live deployment with what its next wake does:
// restore from the first usable tier in schedd's order, or cold boot.
func writeObsAppLive(w io.Writer, d api.ObsAppDetailResponse) {
	if d.Snapshots == nil {
		_, _ = fmt.Fprintln(w, "next wake  unknown (this apid does not report snapshots)")
		return
	}
	if len(d.Snapshots) == 0 {
		_, _ = fmt.Fprintln(w, "live  none: no live deployment, so wakes cannot start")
		return
	}
	byID := map[string]api.ObsDeploymentRow{}
	for _, dep := range d.Deployments {
		byID[dep.ID] = dep
	}
	var order []string
	tiers := map[string][]api.ObsAppSnapshot{}
	for _, s := range d.Snapshots {
		if _, seen := tiers[s.DeploymentID]; !seen {
			order = append(order, s.DeploymentID)
		}
		tiers[s.DeploymentID] = append(tiers[s.DeploymentID], s)
	}
	for _, id := range order {
		dep := byID[id]
		line := fmt.Sprintf("live  deployment %s", id)
		if !dep.CreatedAt.IsZero() {
			line += fmt.Sprintf(" (%s, %s", orDash(dep.Kind), obsAppTime(dep.CreatedAt))
			if dep.CommitSHA != "" {
				line += " commit " + shortID(dep.CommitSHA, 12)
			}
			line += ")"
		}
		_, _ = fmt.Fprintln(w, line)
		next := "cold boot: no usable snapshot (ADR-005)"
		for _, s := range tiers[id] {
			if s.Present {
				next = fmt.Sprintf("restore from the %s snapshot (%d MB memory, fc %s, taken %s)", s.Tier, s.MemBytes>>20, orDash(s.FCVersion), obsAppTimePtr(s.CreatedAt))
				break
			}
		}
		_, _ = fmt.Fprintln(w, "      next wake: "+next)
	}
}

func writeObsAppInstances(w io.Writer, instances []api.ObsInstanceRow) {
	counts := map[string]int{}
	var active []api.ObsInstanceRow
	for _, in := range instances {
		counts[in.State]++
		if s := state.State(in.State); s != state.StateParked && s != state.StateStopped && s != state.StateFailed {
			active = append(active, in)
		}
	}
	states := make([]string, 0, len(counts))
	for s := range counts {
		states = append(states, fmt.Sprintf("%d %s", counts[s], s))
	}
	sort.Strings(states)
	_, _ = fmt.Fprintf(w, "instances  %d", len(instances))
	if len(states) > 0 {
		_, _ = fmt.Fprintf(w, " (%s)", strings.Join(states, ", "))
	}
	_, _ = fmt.Fprintln(w)
	for i, in := range active {
		if i == obsAppListMax {
			break
		}
		_, _ = fmt.Fprintf(w, "  %-12s %-12s started %s  last request %s  %s  wake %s\n", in.State, orDash(in.NodeName), obsAppTime(in.StartedAt), obsAppTime(in.LastRequestAt), in.ID, orDash(in.WakeID))
	}
}

func writeObsAppHealth(w io.Writer, h api.ObsAppHealth) {
	m := h.Metrics
	_, _ = fmt.Fprintf(w, "health (%s, %s)  %d requests  5xx %.2f%%  p50 %.0f ms  p95 %.0f ms  p99 %.0f ms  cold starts %.1f%%\n",
		orDash(m.Range), orDash(m.Source), m.RequestCount, m.ErrorRatePct, m.LatencyP50MS, m.LatencyP95MS, m.LatencyP99MS, m.ColdStartPct)
	switch {
	case h.ErrorsUnavailable:
		_, _ = fmt.Fprintln(w, "errors (24h)  unavailable: apid could not read the error groups")
	case len(h.Errors) == 0:
		_, _ = fmt.Fprintln(w, "errors (24h)  none")
	default:
		_, _ = fmt.Fprintf(w, "errors (24h)  %d groups\n", len(h.Errors))
		for i, e := range h.Errors {
			if i == 5 {
				break
			}
			_, _ = fmt.Fprintf(w, "  %d %-24s %-18s x%-6d last %s  %s\n", e.HTTPStatus, shortID(e.Route, 24), e.ErrorClass, e.Count, obsAppRFC3339(e.LastSeenAt), shortID(e.SampleMessage, 60))
		}
	}
}

func writeObsAppWakes(w io.Writer, t *api.AppWakeTimelineResponse, limit int) {
	if t == nil {
		_, _ = fmt.Fprintln(w, "wakes (24h)  unavailable")
		return
	}
	_, _ = fmt.Fprintf(w, "wakes (24h)  %d, %d at capacity (%.0f%%)\n", t.WakeCount24h, t.AtCapacityCount, t.AtCapacityPct)
	for i, r := range t.Rows {
		if i == limit {
			break
		}
		ready := "-"
		if r.ReadyInMS > 0 {
			ready = fmt.Sprintf("%d ms", r.ReadyInMS)
		}
		capacity := ""
		if r.AtCapacity {
			capacity = "  at capacity"
		}
		_, _ = fmt.Fprintf(w, "  %s  %-36s  %-8s %-5s %8s  %-10s %s%s\n", obsAppRFC3339(r.At), orDash(r.WakeID), orDash(r.Method), orDash(r.Tier), ready, orDash(r.Trigger), r.State, capacity)
	}
	if len(t.Rows) > 0 && limit > 0 {
		_, _ = fmt.Fprintln(w, "  one wake's events and logs: gregalectl obs wake <wake_id>")
	}
}

func writeObsAppDeployments(w io.Writer, deployments []api.ObsDeploymentRow) {
	_, _ = fmt.Fprintf(w, "deployments  %d", len(deployments))
	if len(deployments) > 5 {
		_, _ = fmt.Fprint(w, ", newest 5")
	}
	_, _ = fmt.Fprintln(w)
	for i, dep := range deployments {
		if i == 5 {
			break
		}
		line := fmt.Sprintf("  %s  %-10s %-8s %s", obsAppTime(dep.CreatedAt), dep.Status, orDash(dep.Kind), dep.ID)
		if dep.CommitSHA != "" {
			line += " commit " + shortID(dep.CommitSHA, 12)
		}
		if dep.ErrorCode != "" {
			line += " error " + dep.ErrorCode
		}
		_, _ = fmt.Fprintln(w, line)
	}
}

func obsAppTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.UTC().Format("2006-01-02 15:04:05")
}

func obsAppTimePtr(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return obsAppTime(*t)
}

// obsAppRFC3339 shortens the API's RFC 3339 strings to the page's format.
func obsAppRFC3339(s string) string {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return orDash(s)
	}
	return obsAppTime(t)
}

func shortID(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
