// commands_obs.go — Obs-Meta + Trace-IDs Mega-PR / C8:
// operator-side `gregalectl obs` subcommand dispatcher.
//
// Subcommands:
//
//	gregalectl obs health    — fetch GET /v1/admin/obs/health
//	                           from the local apid and emit
//	                           either a human-readable summary
//	                           (default) or the raw JSON snapshot
//	                           (--json / $FAAS_JSON=1).
//	gregalectl obs incidents — fetch the bounded, read-only incident inbox
//	                           (deployment/job/node/alert correlation).
//	gregalectl obs overview  — fetch the bounded fleet/account KPI snapshot.
//	gregalectl obs capacity  — fetch the bounded fleet capacity snapshot.
//
// The CLI dials apid via HTTP (not gRPC) — same path the operator
// would use from a browser. The endpoint requires admin scope +
// MFA + FAAS_ADMIN_EMAILS allowlist, so the CLI needs the same
// admin bearer that the existing /v1/admin/* surface uses.
//
// apid URL resolution: $FAAS_APID_URL wins; defaults to
// http://127.0.0.1:8080 (the apid loopback listen addr on
// control-plane nodes; matches the convention at
// cmd/apid/main.go::resolveListenAddr).
//
// The inbox is deliberately read-only. Existing deployments/jobs/nodes
// commands remain the explicit action surface for cancellation, retry, and
// drain operations.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// dispatchObs is the top-level `obs` command name. Matches the
// cli_meta.go entry below + the main.go case dispatchObs arm.
// Used as both the dispatch key AND the cliCommand.Name so the
// manifest-drift test in commands_completion_test.go catches
// any future rename.
const dispatchObs = "obs"

// subObsHealth is the `obs health` subcommand name. Exported
// indirectly via cli_meta.go; keep the constant in this file
// rather than cli_meta.go so the dispatcher's switch is the
// single source of truth for subcommand routing.
const subObsHealth = "health"

const subObsIncidents = "incidents"

const subObsOverview = "overview"

const subObsCapacity = "capacity"

// defaultAPIDURL is the loopback default for the apid listen
// addr. Matches cmd/apid/main.go::resolveListenAddr default of
// :8080 on loopback. Operators running apid behind a public
// hostname should set FAAS_APID_URL explicitly.
const defaultAPIDURL = "http://127.0.0.1:8080"

// cmdObsDispatch routes the `gregalectl obs` top-level command
// to its subcommand handlers. Mirrors the shape of
// cmdInstancesDispatch (commands_instances.go:75) and
// cmdBuildsDispatch (commands_builds.go:32): if no subcommand
// is supplied, the dispatcher prints a stable usage line + a
// 2 exit code (per the spec convention at commands_manifest.go
// for `manifest validate` without --file).
func cmdObsDispatch(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "gregalectl obs: missing subcommand; want health|incidents|overview|capacity")
		return 2
	}
	switch args[0] {
	case subObsHealth:
		return cmdObsHealth(args[1:])
	case subObsIncidents:
		return cmdObsIncidents(args[1:])
	case subObsOverview:
		return cmdObsOverview(args[1:])
	case subObsCapacity:
		return cmdObsCapacity(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "gregalectl obs: unknown subcommand %q\nRun 'gregalectl obs overview --help' for usage.\n", args[0])
		return 2
	}
}

// apidBase returns the apid base URL, overridable via
// $FAAS_APID_URL for local/dev. Mirrors cmd/gregale/config.go
// ::apiBase — kept as a per-binary helper because the operator
// binary and the customer binary target different surfaces
// (FAAS_APID_URL vs FAAS_API) and the defaults differ.
func apidBase() string {
	if v := os.Getenv("FAAS_APID_URL"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return defaultAPIDURL
}

// cmdObsHealth fetches GET /v1/admin/obs/health and either
// pretty-prints a human summary (default) or emits the raw JSON
// snapshot (--json / $FAAS_JSON=1).
//
// Human summary shape (mirrors the dashboard's red/yellow/green
// tile conventions so an on-call reading the CLI gets the same
// gist without parsing JSON):
//
//	audit_log_write_total_5m:           1234
//	audit_log_write_failures_5m:            0
//	audit_log_coverage_ratio_5m:        1.00
//	operator_intent_outcome_missing_total:
//	  force_park:        0
//	  force_cold_boot:   0
//	  force_restart:     0
//	trace_id_completeness_ratio:
//	  force_park:        1.00
//	  force_cold_boot:   1.00
//	  force_restart:     1.00
//	alerts_firing:                       0
//	prometheus_available:                true
//
// --json / FAAS_JSON=1 overrides the human format. Both paths
// emit stable, greppable output (no ANSI codes; the operator's
// pager-friendly escape sequences are the dashboard's job).
func cmdObsHealth(args []string) int {
	fs := flag.NewFlagSet(subObsHealth, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOut := fs.Bool("json", false, "emit structured JSON to stdout (overrides human summary)")
	adminToken := fs.String("admin-token", "", "admin bearer for the FAAS_ADMIN_EMAILS-allowlisted admin scope (default: $FAAS_ADMIN_TOKEN)")
	timeout := fs.Duration("timeout", 10*time.Second, "HTTP timeout for the apid round-trip (default 10s)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	// Resolve bearer: --admin-token wins, then $FAAS_ADMIN_TOKEN.
	// Empty bearer surfaces a stable error so the operator can
	// tell "no auth supplied" from "apid rejected the request".
	token := *adminToken
	if token == "" {
		token = os.Getenv("FAAS_ADMIN_TOKEN")
	}
	if token == "" {
		fmt.Fprintln(os.Stderr, "gregalectl obs health: --admin-token (or $FAAS_ADMIN_TOKEN) required (admin scope is gated)")
		return 2
	}

	// Build the GET request. The endpoint is /v1/admin/obs/health;
	// no query params — the snapshot is windowless (the handler
	// hard-codes a 5m window for PromQL + SQL aggregates).
	url := apidBase() + "/v1/admin/obs/health"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gregalectl obs health:", err)
		return 1
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	httpClient := &http.Client{Timeout: *timeout}
	resp, err := httpClient.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gregalectl obs health: dial apid:", err)
		return 1
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gregalectl obs health: read body:", err)
		return 1
	}
	if resp.StatusCode != http.StatusOK {
		// Stable error shape so the on-call can grep for the
		// status code + a JSON problem envelope (matches the
		// pattern at cmd/gregale/commands5.go for the customer
		// HTTP surface).
		fmt.Fprintf(os.Stderr, "gregalectl obs health: apid returned %d: %s\n", resp.StatusCode, strings.TrimSpace(string(body)))
		return 1
	}

	if *jsonOut || jsonFlagEnabled(args) {
		// Raw JSON pass-through. Pretty-print only when the
		// output is going to a terminal — pipes get the dense
		// shape so downstream tooling can grep.
		var v any
		if err := json.Unmarshal(body, &v); err != nil {
			fmt.Fprintln(os.Stderr, "gregalectl obs health: decode response:", err)
			return 1
		}
		enc := json.NewEncoder(os.Stdout)
		if isTerminal(os.Stdout) {
			enc.SetIndent("", "  ")
		}
		if err := enc.Encode(v); err != nil {
			fmt.Fprintln(os.Stderr, "gregalectl obs health: encode json:", err)
			return 1
		}
		return 0
	}

	// Human summary. Decode into a permissive map so unknown
	// fields are tolerated; the closed-set keys are the only
	// ones we render.
	var snap map[string]any
	if err := json.Unmarshal(body, &snap); err != nil {
		fmt.Fprintln(os.Stderr, "gregalectl obs health: decode response:", err)
		return 1
	}
	writeObsHealthHuman(os.Stdout, snap)
	return 0
}

// cmdObsIncidents fetches the bounded operator incident inbox. It shares the
// admin bearer and apid resolution with `obs health`, while exposing filters
// that keep routine triage focused on one signal family or severity.
func cmdObsIncidents(args []string) int {
	fs := flag.NewFlagSet(subObsIncidents, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOut := fs.Bool("json", false, "emit structured JSON to stdout")
	adminToken := fs.String("admin-token", "", "admin bearer (default: $FAAS_ADMIN_TOKEN)")
	typeFilter := fs.String("type", "", "incident type: deployment|job_run|compute_node|platform_alert")
	severity := fs.String("severity", "", "severity: warning|error|critical")
	since := fs.String("since", "", "RFC 3339 lower bound (default: last 24h; capped at 7d)")
	cursorValue := fs.String("cursor", "", "opaque cursor returned as next_cursor")
	limit := fs.Int("limit", api.ObsIncidentLimitDefault, "maximum incidents to return (1..200)")
	timeout := fs.Duration("timeout", 10*time.Second, "HTTP timeout for the apid round-trip")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "gregalectl obs incidents: positional arguments are not accepted")
		return 2
	}
	if *limit < 1 || *limit > api.ObsIncidentLimitMax {
		fmt.Fprintf(os.Stderr, "gregalectl obs incidents: --limit must be 1..%d\n", api.ObsIncidentLimitMax)
		return 2
	}
	if *since != "" {
		if _, err := time.Parse(time.RFC3339, *since); err != nil {
			fmt.Fprintln(os.Stderr, "gregalectl obs incidents: --since must be RFC 3339")
			return 2
		}
	}
	token := *adminToken
	if token == "" {
		token = os.Getenv("FAAS_ADMIN_TOKEN")
	}
	if token == "" {
		fmt.Fprintln(os.Stderr, "gregalectl obs incidents: --admin-token (or $FAAS_ADMIN_TOKEN) required")
		return 2
	}
	query := url.Values{"limit": {strconv.Itoa(*limit)}}
	if strings.TrimSpace(*typeFilter) != "" {
		query.Set("type", strings.TrimSpace(*typeFilter))
	}
	if strings.TrimSpace(*severity) != "" {
		query.Set("severity", strings.TrimSpace(*severity))
	}
	if strings.TrimSpace(*since) != "" {
		query.Set("since", strings.TrimSpace(*since))
	}
	if strings.TrimSpace(*cursorValue) != "" {
		query.Set("cursor", strings.TrimSpace(*cursorValue))
	}
	req, err := http.NewRequest(http.MethodGet, apidBase()+"/v1/admin/obs/incidents?"+query.Encode(), nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gregalectl obs incidents:", err)
		return 1
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := (&http.Client{Timeout: *timeout}).Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gregalectl obs incidents: dial apid:", err)
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gregalectl obs incidents: read body:", err)
		return 1
	}
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "gregalectl obs incidents: apid returned %d: %s\n", resp.StatusCode, strings.TrimSpace(string(body)))
		return 1
	}
	var response api.ObsIncidentListResponse
	if err := json.Unmarshal(body, &response); err != nil {
		fmt.Fprintln(os.Stderr, "gregalectl obs incidents: decode response:", err)
		return 1
	}
	if *jsonOut || jsonFlagEnabled(args) {
		enc := json.NewEncoder(os.Stdout)
		if isTerminal(os.Stdout) {
			enc.SetIndent("", "  ")
		}
		if err := enc.Encode(response); err != nil {
			fmt.Fprintln(os.Stderr, "gregalectl obs incidents: encode json:", err)
			return 1
		}
		return 0
	}
	writeObsIncidentsHuman(os.Stdout, response)
	return 0
}

// cmdObsOverview fetches the existing bounded operator KPI projection. It is
// deliberately read-only: all aggregation remains in apid and the CLI only
// renders the response, so this command cannot add work to the deployment
// request path or require direct database access.
func cmdObsOverview(args []string) int {
	fs := flag.NewFlagSet(subObsOverview, flag.ContinueOnError)
	fs.SetOutput(osStderr)
	jsonOut := fs.Bool("json", false, "emit structured JSON to stdout")
	adminToken := fs.String("admin-token", "", "admin bearer (default: $FAAS_ADMIN_TOKEN)")
	timeout := fs.Duration("timeout", 10*time.Second, "HTTP timeout for the apid round-trip")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs overview: positional arguments are not accepted")
		return 2
	}
	token := *adminToken
	if token == "" {
		token = os.Getenv("FAAS_ADMIN_TOKEN")
	}
	if token == "" {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs overview: --admin-token (or $FAAS_ADMIN_TOKEN) required")
		return 2
	}
	body, code := fetchObsSnapshot(subObsOverview, "/v1/admin/obs/overview", token, *timeout)
	if code != 0 {
		return code
	}
	var response api.ObsOverviewResponse
	if err := json.Unmarshal(body, &response); err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs overview: decode response:", err)
		return 1
	}
	if *jsonOut || jsonOutput || jsonFlagEnabled(args) {
		return emitOperatorJSON(response)
	}
	writeObsOverviewHuman(osStdout, response)
	return 0
}

// cmdObsCapacity fetches the existing bounded fleet capacity projection. The
// endpoint returns aggregate headroom plus per-node counters, never workload
// rows, which keeps the operator snapshot safe to poll during incidents.
func cmdObsCapacity(args []string) int {
	fs := flag.NewFlagSet(subObsCapacity, flag.ContinueOnError)
	fs.SetOutput(osStderr)
	jsonOut := fs.Bool("json", false, "emit structured JSON to stdout")
	adminToken := fs.String("admin-token", "", "admin bearer (default: $FAAS_ADMIN_TOKEN)")
	timeout := fs.Duration("timeout", 10*time.Second, "HTTP timeout for the apid round-trip")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs capacity: positional arguments are not accepted")
		return 2
	}
	token := *adminToken
	if token == "" {
		token = os.Getenv("FAAS_ADMIN_TOKEN")
	}
	if token == "" {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs capacity: --admin-token (or $FAAS_ADMIN_TOKEN) required")
		return 2
	}
	body, code := fetchObsSnapshot(subObsCapacity, "/v1/admin/obs/capacity", token, *timeout)
	if code != 0 {
		return code
	}
	var response api.ObsCapacityResponse
	if err := json.Unmarshal(body, &response); err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs capacity: decode response:", err)
		return 1
	}
	if *jsonOut || jsonOutput || jsonFlagEnabled(args) {
		return emitOperatorJSON(response)
	}
	writeObsCapacityHuman(osStdout, response)
	return 0
}

func fetchObsSnapshot(command, path, token string, timeout time.Duration) ([]byte, int) {
	req, err := http.NewRequest(http.MethodGet, apidBase()+path, nil)
	if err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs "+command+":", err)
		return nil, 1
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs "+command+": dial apid:", err)
		return nil, 1
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs "+command+": read body:", err)
		return nil, 1
	}
	if resp.StatusCode != http.StatusOK {
		_, _ = fmt.Fprintf(osStderr, "gregalectl obs %s: apid returned %d: %s\n", command, resp.StatusCode, strings.TrimSpace(string(body)))
		return nil, 1
	}
	return body, 0
}

func writeObsIncidentsHuman(w io.Writer, response api.ObsIncidentListResponse) {
	_, _ = fmt.Fprintf(w, "incidents=%d since=%s type=%s severity=%s limit=%d since_clamped=%t\n",
		len(response.Items), response.Since.UTC().Format(time.RFC3339), response.Type, response.Severity,
		response.Limit, response.SinceClamped)
	for _, item := range response.Items {
		_, _ = fmt.Fprintf(w, "incident_id=%s type=%s severity=%s status=%s observed_at=%s resource_id=%s resource_name=%s summary=%q",
			item.ID, item.Type, item.Severity, item.Status, item.ObservedAt.UTC().Format(time.RFC3339),
			item.ResourceID, item.ResourceName, item.Summary)
		if item.ActionPath != "" {
			_, _ = fmt.Fprintf(w, " action_path=%s", item.ActionPath)
		}
		if item.AuditPath != "" {
			_, _ = fmt.Fprintf(w, " audit_path=%s", item.AuditPath)
		}
		if item.RunbookURL != "" {
			_, _ = fmt.Fprintf(w, " runbook=%s", item.RunbookURL)
		}
		_, _ = fmt.Fprintln(w)
	}
	if response.NextCursor != "" {
		_, _ = fmt.Fprintf(w, "next_cursor=%s\n", response.NextCursor)
	}
}

func writeObsOverviewHuman(w io.Writer, response api.ObsOverviewResponse) {
	_, _ = fmt.Fprintf(w, "generated_at=%s\n", response.GeneratedAt.UTC().Format(time.RFC3339))
	_, _ = fmt.Fprintln(w, "totals:")
	_, _ = fmt.Fprintf(w, "  accounts_active=%d accounts_past_due=%d accounts_suspended=%d orgs_total=%d apps_total=%d instances_live=%d instances_waking=%d nodes_active=%d nodes_inactive=%d audit_events_24h=%d\n",
		response.Totals.AccountsActive, response.Totals.AccountsPastDue, response.Totals.AccountsSuspended,
		response.Totals.OrgsTotal, response.Totals.AppsTotal, response.Totals.InstancesLive,
		response.Totals.InstancesWaking, response.Totals.NodesActive, response.Totals.NodesInactive,
		response.Totals.AuditEvents24h)
	funnel := response.BetaFunnel14d
	_, _ = fmt.Fprintf(w, "beta_funnel_14d window_started_at=%s accounts_created=%d email_verified=%d with_app=%d with_live_deployment=%d with_successful_request=%d first_success_samples=%d first_success_p50_seconds=%d first_success_p95_seconds=%d\n",
		funnel.WindowStartedAt.UTC().Format(time.RFC3339), funnel.AccountsCreated, funnel.EmailVerified, funnel.WithApp,
		funnel.WithLiveDeployment, funnel.WithSuccessfulRequest, funnel.SignupToFirstSuccessSamples,
		funnel.SignupToFirstSuccessP50Seconds, funnel.SignupToFirstSuccessP95Seconds)
	_, _ = fmt.Fprintf(w, "top_rate_limited_accounts_24h=%d\n", len(response.TopRateLimitedAccounts24h))
	for _, row := range response.TopRateLimitedAccounts24h {
		_, _ = fmt.Fprintf(w, "  rate_limited account_id=%s hits=%d\n", row.AccountID, row.Hits)
	}
	_, _ = fmt.Fprintf(w, "node_health=%d\n", len(response.NodeHealth))
	for _, node := range response.NodeHealth {
		_, _ = fmt.Fprintf(w, "  node name=%s active=%t stale=%t last_heartbeat_at=%s\n", node.Name, node.Active, node.Stale, node.LastHeartbeatAt.UTC().Format(time.RFC3339))
	}
	_, _ = fmt.Fprintf(w, "recent_failures_1h=%d\n", len(response.RecentFailures1h))
	for _, failure := range response.RecentFailures1h {
		_, _ = fmt.Fprintf(w, "  failure kind=%s count=%d\n", failure.Kind, failure.Count)
	}
}

func writeObsCapacityHuman(w io.Writer, response api.ObsCapacityResponse) {
	_, _ = fmt.Fprintf(w, "generated_at=%s\n", response.GeneratedAt.UTC().Format(time.RFC3339))
	summary := response.Summary
	_, _ = fmt.Fprintf(w, "summary total_nodes=%d active_nodes=%d inactive_nodes=%d total_vcpus=%d total_vcpu_budget=%d total_mem_mb=%d total_admission_ceiling_mb=%d ram_used_mb=%d admission_margin_mb=%d instances_live=%d instances_running=%d instances_waking=%d instances_cold_booting=%d apps_total=%d tenants_total=%d unplaced_apps=%d\n",
		summary.TotalNodes, summary.ActiveNodes, summary.InactiveNodes, summary.TotalVCPUs, summary.TotalVCPUBudget,
		summary.TotalMemMB, summary.TotalAdmissionCeilingMB, summary.RAMUsedMB, summary.AdmissionMarginMB,
		summary.InstancesLive, summary.InstancesRunning, summary.InstancesWaking, summary.InstancesColdBooting,
		summary.AppsTotal, summary.TenantsTotal, summary.UnplacedApps)
	_, _ = fmt.Fprintf(w, "nodes=%d\n", len(response.Nodes))
	for _, node := range response.Nodes {
		_, _ = fmt.Fprintf(w, "  node id=%s name=%s active=%t vcpus=%d vcpu_budget=%d mem_mb=%d admission_ceiling_mb=%d instances_live=%d instances_running=%d instances_waking=%d instances_cold_booting=%d ram_used_mb=%d admission_margin_mb=%d apps=%d tenants=%d\n",
			node.ID, node.Name, node.Active, node.VPCPUs, node.VCPUBudget, node.MemMB, node.AdmissionCeilingMB,
			node.InstancesLive, node.InstancesRunning, node.InstancesWaking, node.InstancesColdBooting,
			node.RAMUsedMB, node.AdmissionMarginMB, node.AppsCount, node.TenantsCount)
	}
}

// jsonFlagEnabled reports whether --json or $FAAS_JSON=1 was
// supplied. Mirrors the applyJSONFlag helper at
// commands5.go:94 — duplicated here so the obs subcommand
// stays self-contained rather than importing the customer
// binary's flag-walk. Per the C8 plan, the existing
// gregalectl/json_flag.go convention is honoured; if a future
// PR adds more CLI-side --json dispatchers, hoist this helper
// into the shared internal package.
func jsonFlagEnabled(args []string) bool {
	if os.Getenv("FAAS_JSON") == "1" {
		return true
	}
	for _, a := range args {
		if a == "--json" || a == "--json=1" {
			return true
		}
	}
	return false
}

// isTerminal reports whether w is a terminal (vs a pipe or
// file). Used by cmdObsHealth to decide whether to pretty-print
// the JSON output. Stdlib-only — no termcap dependency.
func isTerminal(w *os.File) bool {
	fi, err := w.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// writeObsHealthHuman renders the closed-set snapshot as a
// flat greppable summary. Subcommand-level helper so the
// handler stays ≤50 lines and the human-shape contract has
// its own focused test file (commands_obs_test.go).
//
// Field order follows api.ObsHealthResponse JSON tags (excluding
// generated_at, which is not rendered) so a `jq` consumer can pin
// the order too.
func writeObsHealthHuman(w io.Writer, snap map[string]any) {
	_, _ = fmt.Fprintln(w, "prometheus_available:", snap["prometheus_available"])
	_, _ = fmt.Fprintln(w, "audit_log_write_total_5m:", snap["audit_log_write_total_5m"])
	_, _ = fmt.Fprintln(w, "audit_log_write_failures_5m:", snap["audit_log_write_failures_5m"])
	_, _ = fmt.Fprintln(w, "audit_log_coverage_ratio_5m:", snap["audit_log_coverage_ratio_5m"])
	_, _ = fmt.Fprintln(w, "operator_intent_outcome_missing_total:")
	writeObsHealthKindBlock(w, snap["operator_intent_outcome_missing_total"], false)
	_, _ = fmt.Fprintln(w, "trace_id_completeness_ratio:")
	writeObsHealthKindBlock(w, snap["trace_id_completeness_ratio"], true)
	_, _ = fmt.Fprintln(w, "alerts_firing:", snap["alerts_firing"])
}

// writeObsHealthKindBlock renders a per-kind sub-map
// (operator_intent_outcome_missing_total or
// trace_id_completeness_ratio) as a stable alphabetical
// list. pretty=true is reserved for future use (rational
// formatting); today both modes render the raw value.
func writeObsHealthKindBlock(w io.Writer, raw any, pretty bool) {
	m, ok := raw.(map[string]any)
	if !ok {
		_, _ = fmt.Fprintln(w, "  (absent)")
		return
	}
	// Stable alphabetical iteration: pull the keys, sort them,
	// render. Matches the dashboard's tile-mapping convention.
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for _, k := range sortedObsHealthKeys(keys) {
		v := m[k]
		if pretty {
			switch x := v.(type) {
			case float64:
				_, _ = fmt.Fprintf(w, "  %s: %.2f\n", k, x)
				continue
			}
		}
		_, _ = fmt.Fprintf(w, "  %s: %v\n", k, v)
	}
}

// sortedObsHealthKeys returns the keys in lexical order so the
// CLI's output is stable across runs (map iteration order in
// Go is randomized). Extracted so commands_obs_test.go can
// pin the order without copy-pasting the sort.
func sortedObsHealthKeys(keys []string) []string {
	out := append([]string(nil), keys...)
	// insertion sort — keys are short (≤ 8 operator-action
	// kinds); insertion sort beats sort.Strings for n < 24.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
