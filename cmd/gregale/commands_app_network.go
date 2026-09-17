// `gregale app <slug> network {show|doctor}` — the customer-facing view of
// Gregale's current networking contract.
//
// This is intentionally read-only. It composes the existing app, egress
// allowlist, static-egress-IP, and captured-upstream APIs into one useful
// surface while the provider-neutral private-network attachment API is still
// being designed. The doctor reports observed probe telemetry only; it never
// sends traffic to an upstream and never exposes upstream hostnames.
package main

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	subNetwork                 = "network"
	appNetworkServiceDomain    = "svc.gregale"
	appNetworkServiceProxyPort = 10080
	appNetworkProbeFreshness   = 15 * time.Minute
)

type appNetworkSnapshot struct {
	App              appNetworkApp              `json:"app"`
	EgressAllowlist  []string                   `json:"egress_allowlist"`
	StaticEgress     appNetworkStaticEgress     `json:"static_egress"`
	ServiceDiscovery appNetworkServiceDiscovery `json:"service_discovery"`
	PrivateNetwork   appNetworkPrivateNetwork   `json:"private_network"`
	Upstreams        appNetworkUpstreams        `json:"upstreams"`
	ObservedAt       string                     `json:"observed_at"`
}

type appNetworkApp struct {
	ID     string `json:"id"`
	Slug   string `json:"slug"`
	Status string `json:"status"`
	URL    string `json:"url,omitempty"`
}

type appNetworkStaticEgress struct {
	IP          *netip.Addr `json:"ip"`
	SetAt       *time.Time  `json:"set_at"`
	PlanCap     int         `json:"plan_cap"`
	PlanAllowed bool        `json:"plan_allowed"`
	Available   bool        `json:"available"`
	Error       string      `json:"error,omitempty"`
}

type appNetworkServiceDiscovery struct {
	Enabled      bool   `json:"enabled"`
	HostnameForm string `json:"hostname_form"`
	ProxyPort    int    `json:"proxy_port"`
	Scope        string `json:"scope"`
}

type appNetworkPrivateNetwork struct {
	Attached bool   `json:"attached"`
	Status   string `json:"status"`
	Detail   string `json:"detail"`
}

type appNetworkUpstreams struct {
	Available bool                       `json:"available"`
	Count     int                        `json:"count"`
	Quota     int                        `json:"quota"`
	Items     []api.DataUpstreamResponse `json:"items"`
	Error     string                     `json:"error,omitempty"`
}

type appNetworkCheck struct {
	Name        string `json:"name"`
	Status      string `json:"status"`
	Detail      string `json:"detail"`
	Observed    string `json:"observed,omitempty"`
	Remediation string `json:"remediation,omitempty"`
}

type appNetworkDoctorReport struct {
	App        appNetworkApp     `json:"app"`
	Healthy    bool              `json:"healthy"`
	ObservedAt string            `json:"observed_at"`
	Checks     []appNetworkCheck `json:"checks"`
}

// cmdAppNetwork dispatches `gregale app <slug> network show|doctor`.
func cmdAppNetwork(slug string, args []string) int {
	if slug == "" || len(args) != 1 {
		PrintUsage(os.Stderr, "usage: gregale app <slug> network {show|doctor}", "apps")
		return 1
	}
	if args[0] != "show" && args[0] != "doctor" {
		PrintUsage(os.Stderr, "usage: gregale app <slug> network {show|doctor}", "apps")
		return 1
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	snapshot, err := loadAppNetworkSnapshot(ctx, client, slug)
	if err != nil {
		return printErr("Could not load app network", err)
	}

	switch args[0] {
	case "show":
		return renderAppNetworkSnapshot(snapshot)
	case "doctor":
		return renderAppNetworkDoctor(snapshot)
	}
	return 1
}

func loadAppNetworkSnapshot(ctx context.Context, client *Client, slug string) (appNetworkSnapshot, error) {
	app, err := client.GetApp(ctx, slug)
	if err != nil {
		return appNetworkSnapshot{}, err
	}

	snapshot := appNetworkSnapshot{
		App:             appNetworkApp{ID: app.ID, Slug: app.Slug, Status: app.Status, URL: app.URL},
		EgressAllowlist: append([]string{}, app.EgressAllowlist...),
		ServiceDiscovery: appNetworkServiceDiscovery{
			Enabled:      true,
			HostnameForm: "<app-slug>." + appNetworkServiceDomain,
			ProxyPort:    appNetworkServiceProxyPort,
			Scope:        "same-account, same-region service calls",
		},
		PrivateNetwork: appNetworkPrivateNetwork{
			Attached: false,
			Status:   "not_configured",
			Detail:   "Customer-facing private network attachment is not configured yet.",
		},
		Upstreams:  appNetworkUpstreams{Items: []api.DataUpstreamResponse{}},
		ObservedAt: time.Now().UTC().Format(time.RFC3339),
	}

	static, staticErr := client.GetAppStaticEgressIP(ctx, slug)
	if staticErr != nil {
		snapshot.StaticEgress.Error = staticErr.Error()
	} else {
		snapshot.StaticEgress = appNetworkStaticEgress{
			IP:          static.IP,
			SetAt:       static.SetAt,
			PlanCap:     static.PlanCap,
			PlanAllowed: static.PlanAllowed,
			Available:   true,
		}
	}

	rows, count, quota, upstreamErr := client.ListAppDataUpstreamsWithQuota(ctx, slug, "")
	if upstreamErr != nil {
		snapshot.Upstreams.Error = upstreamErr.Error()
	} else {
		snapshot.Upstreams = appNetworkUpstreams{
			Available: true,
			Count:     count,
			Quota:     quota,
			Items:     rows,
		}
	}
	return snapshot, nil
}

func renderAppNetworkSnapshot(snapshot appNetworkSnapshot) int {
	if jsonOutput {
		return jsonOut(writeJSON(snapshot))
	}
	_, _ = fmt.Fprintf(osStdout, "App network: %s\n", snapshot.App.Slug)
	_, _ = fmt.Fprintf(osStdout, "  status:              %s\n", snapshot.App.Status)
	_, _ = fmt.Fprintf(osStdout, "  private network:     %s\n", snapshot.PrivateNetwork.Status)
	_, _ = fmt.Fprintf(osStdout, "  service discovery:   %s (<app-slug>.%s:%d)\n", enabledLabel(snapshot.ServiceDiscovery.Enabled), appNetworkServiceDomain, appNetworkServiceProxyPort)
	if len(snapshot.EgressAllowlist) == 0 {
		_, _ = fmt.Fprintln(osStdout, "  egress policy:       unrestricted (no CIDR allowlist)")
	} else {
		_, _ = fmt.Fprintf(osStdout, "  egress policy:       %d CIDR(s)\n", len(snapshot.EgressAllowlist))
		for _, cidr := range snapshot.EgressAllowlist {
			_, _ = fmt.Fprintf(osStdout, "    %s\n", cidr)
		}
	}
	if !snapshot.StaticEgress.Available {
		_, _ = fmt.Fprintf(osStdout, "  static egress:       unavailable (%s)\n", snapshot.StaticEgress.Error)
	} else if snapshot.StaticEgress.IP == nil {
		_, _ = fmt.Fprintf(osStdout, "  static egress:       not pinned (plan allowed: %t)\n", snapshot.StaticEgress.PlanAllowed)
	} else {
		_, _ = fmt.Fprintf(osStdout, "  static egress:       %s\n", snapshot.StaticEgress.IP.String())
	}
	if !snapshot.Upstreams.Available {
		_, _ = fmt.Fprintf(osStdout, "  upstream telemetry:  unavailable (%s)\n", snapshot.Upstreams.Error)
	} else {
		_, _ = fmt.Fprintf(osStdout, "  upstream telemetry:  %d/%d captured\n", snapshot.Upstreams.Count, snapshot.Upstreams.Quota)
		renderAppNetworkUpstreamHealth(osStdout, snapshot.Upstreams.Items, time.Now())
	}
	_, _ = fmt.Fprintf(osStdout, "  observed at:         %s\n", snapshot.ObservedAt)
	return 0
}

func renderAppNetworkUpstreamHealth(w interface{ Write([]byte) (int, error) }, rows []api.DataUpstreamResponse, now time.Time) {
	for _, row := range rows {
		state, detail := appNetworkProbeState(row, now)
		host := row.HostLast4
		if host == "" {
			host = GlyphEmDash
		}
		_, _ = fmt.Fprintf(w, "    %s:%d  %s (%s)\n", host, row.Port, state, detail)
	}
}

func renderAppNetworkDoctor(snapshot appNetworkSnapshot) int {
	report := buildAppNetworkDoctorReport(snapshot, time.Now())
	if jsonOutput {
		return jsonOut(writeJSON(report))
	}
	_, _ = fmt.Fprintf(osStdout, "App network doctor: %s\n", report.App.Slug)
	_, _ = fmt.Fprintf(osStdout, "Observed at: %s\n\n", report.ObservedAt)
	for _, check := range report.Checks {
		_, _ = fmt.Fprintf(osStdout, "%s %-20s %s\n", appNetworkCheckMarker(check.Status), check.Name, check.Detail)
		if check.Observed != "" {
			_, _ = fmt.Fprintf(osStdout, "  observed: %s\n", check.Observed)
		}
		if check.Remediation != "" {
			_, _ = fmt.Fprintf(osStdout, "  fix:      %s\n", check.Remediation)
		}
	}
	if report.Healthy {
		return 0
	}
	return 1
}

func buildAppNetworkDoctorReport(snapshot appNetworkSnapshot, now time.Time) appNetworkDoctorReport {
	report := appNetworkDoctorReport{
		App:        snapshot.App,
		Healthy:    true,
		ObservedAt: snapshot.ObservedAt,
		Checks:     []appNetworkCheck{},
	}
	report.Checks = append(report.Checks, appNetworkCheck{
		Name:     "app",
		Status:   "ok",
		Detail:   "app configuration loaded",
		Observed: snapshot.App.Status,
	})
	report.Checks = append(report.Checks, appNetworkCheck{
		Name:        "service-discovery",
		Status:      "ok",
		Detail:      "same-account service discovery is enabled",
		Observed:    fmt.Sprintf("<app-slug>.%s:%d", appNetworkServiceDomain, appNetworkServiceProxyPort),
		Remediation: "",
	})
	if len(snapshot.EgressAllowlist) == 0 {
		report.Checks = append(report.Checks, appNetworkCheck{
			Name:        "egress-policy",
			Status:      "warn",
			Detail:      "outbound traffic is unrestricted by app CIDR policy",
			Remediation: "Add only the required destinations with `gregale app " + snapshot.App.Slug + " egress-allowlist add <cidr>`.",
		})
	} else {
		report.Checks = append(report.Checks, appNetworkCheck{
			Name:     "egress-policy",
			Status:   "ok",
			Detail:   fmt.Sprintf("outbound traffic is limited to %d CIDR(s)", len(snapshot.EgressAllowlist)),
			Observed: strings.Join(snapshot.EgressAllowlist, ", "),
		})
	}

	switch {
	case !snapshot.StaticEgress.Available:
		report.Checks = append(report.Checks, appNetworkCheck{
			Name:        "static-egress",
			Status:      "warn",
			Detail:      "static egress status could not be read",
			Observed:    snapshot.StaticEgress.Error,
			Remediation: "Check the app plan and ask an operator to enable the static egress surface if required.",
		})
	case snapshot.StaticEgress.IP != nil:
		report.Checks = append(report.Checks, appNetworkCheck{
			Name:     "static-egress",
			Status:   "ok",
			Detail:   "a stable outbound address is pinned",
			Observed: snapshot.StaticEgress.IP.String(),
		})
	case !snapshot.StaticEgress.PlanAllowed:
		report.Checks = append(report.Checks, appNetworkCheck{
			Name:   "static-egress",
			Status: "skipped",
			Detail: "not available on this plan",
		})
	default:
		report.Checks = append(report.Checks, appNetworkCheck{
			Name:        "static-egress",
			Status:      "skipped",
			Detail:      "plan supports static egress, but no address is pinned",
			Remediation: "Pin the address only when an upstream requires IP allowlisting.",
		})
	}

	if !snapshot.Upstreams.Available {
		report.Checks = append(report.Checks, appNetworkCheck{
			Name:        "upstream-telemetry",
			Status:      "warn",
			Detail:      "captured upstream telemetry could not be read",
			Observed:    snapshot.Upstreams.Error,
			Remediation: "Retry after the upstream API is available; hostnames remain redacted by design.",
		})
	} else if len(snapshot.Upstreams.Items) == 0 {
		report.Checks = append(report.Checks, appNetworkCheck{
			Name:   "upstream-telemetry",
			Status: "skipped",
			Detail: "no captured data upstreams",
		})
	} else {
		stale, failed := 0, 0
		for _, row := range snapshot.Upstreams.Items {
			state, _ := appNetworkProbeState(row, now)
			if state == "stale" {
				stale++
			}
			if state == "failed" {
				failed++
			}
		}
		switch {
		case failed > 0:
			report.Checks = append(report.Checks, appNetworkCheck{
				Name:        "upstream-telemetry",
				Status:      "warn",
				Detail:      fmt.Sprintf("%d upstream probe(s) have no successful RTT", failed),
				Observed:    fmt.Sprintf("%d/%d captured; freshness window %s", snapshot.Upstreams.Count, snapshot.Upstreams.Quota, appNetworkProbeFreshness),
				Remediation: "Inspect the upstream's network policy and credentials from the application runtime.",
			})
		case stale > 0:
			report.Checks = append(report.Checks, appNetworkCheck{
				Name:        "upstream-telemetry",
				Status:      "warn",
				Detail:      fmt.Sprintf("%d upstream probe(s) have stale or missing observations", stale),
				Observed:    fmt.Sprintf("freshness window %s", appNetworkProbeFreshness),
				Remediation: "Generate traffic to refresh probe observations; this command never sends probe traffic.",
			})
		default:
			report.Checks = append(report.Checks, appNetworkCheck{
				Name:     "upstream-telemetry",
				Status:   "ok",
				Detail:   "captured upstream probes are fresh",
				Observed: fmt.Sprintf("%d/%d captured; freshness window %s", snapshot.Upstreams.Count, snapshot.Upstreams.Quota, appNetworkProbeFreshness),
			})
		}
	}

	report.Checks = append(report.Checks, appNetworkCheck{
		Name:        "private-network",
		Status:      "skipped",
		Detail:      "no customer-facing private network attachment is configured",
		Remediation: "Use same-account service discovery today; a provider-neutral private network connector is the next networking slice.",
	})
	return report
}

func appNetworkProbeState(row api.DataUpstreamResponse, now time.Time) (string, string) {
	if row.LastProbedAt == "" {
		return "stale", "no probe observation"
	}
	probedAt, err := time.Parse(time.RFC3339Nano, row.LastProbedAt)
	if err != nil || now.Sub(probedAt) > appNetworkProbeFreshness {
		return "stale", "last probe is older than 15m"
	}
	if row.LastRTTMs == nil {
		return "failed", "last probe had no successful RTT"
	}
	return "fresh", fmt.Sprintf("last RTT %dms", *row.LastRTTMs)
}

func appNetworkCheckMarker(status string) string {
	switch status {
	case "ok":
		return glyphOK()
	case "warn":
		return "!"
	case "skipped":
		return "·"
	default:
		return glyphFail()
	}
}

func enabledLabel(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}
