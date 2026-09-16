// Sidecar portnorm routing-key split (issue #463 / ADR-069 /
// ADR-071 / PR-C §5).
//
// The public listener's hostname carries both the app id
// AND the optional workload selector. The conventions are:
//
//	<app>.on-faas.com              → main workload (port 8080)
//	<app>--<sidecar>.on-faas.com   → sidecar <sidecar>'s port
//	<app>--port-<name>.on-faas.com  → app listener <name>'s port
//
// `--` is the separator: two ASCII hyphens. The hostname
// segment right of the separator is the sidecar's stable
// name (the value deployments.sidecars[].Name on the
// deployment row, set at build time by imaged).
//
// Why `--`? It's URL-safe, distinct from the `.` that
// already separates the app id from the public suffix, and
// distinguishable from a single `-` that customer app names
// may legitimately use (e.g. `acme-staging.on-faas.com`).
// The separator is internal-only — the customer-facing
// router record derives it from the sidecar name; a future
// PR can switch the routing-key convention to a more
// RESTful `/<host>/<sidecar>` path-based selector without
// breaking the wire, because the data plumbing (per-port
// Target) is independent of the routing-key split.
//
// The function is the canonical split primitive; both the
// public listener and the in-guest portnorm forwarder
// (guest/init/portnorm_linux.go) reference the same
// separator so an operator wiring `<host>--<name>` into a
// postmortem dashboard lands at the right sidecar port.

package gateway

import (
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// PublicPortsFromWorkloadPorts converts the shared manifest shape into the
// gateway's narrow routing projection without retaining caller-owned slices.
func PublicPortsFromWorkloadPorts(ports []api.WorkloadPort) []AppPort {
	if ports == nil {
		return nil
	}
	out := make([]AppPort, len(ports))
	for i, port := range ports {
		out[i] = AppPort{Name: port.Name, Port: port.Port, Protocol: string(port.EffectiveProtocol())}
	}
	return out
}

// SidecarHostSeparator is the canonical split between the
// app id and the sidecar selector (issue #463 / ADR-069 /
// ADR-071 / PR-C §5). Two ASCII hyphens. Exported so the
// listener, the test suite, the dashboard, and the
// provisioner can refer to the same string without drift.
const SidecarHostSeparator = "--"

// PublicPortSelectorPrefix reserves a separate hostname namespace for app
// listeners so a sidecar named "metrics" and a listener named "metrics" can
// coexist without changing the existing sidecar routing contract.
const PublicPortSelectorPrefix = "port-"

// SplitHostSelector parses a routing-key hostname into
// (appHost, sidecarName). The sidecarName is empty for the
// main workload (the `host` alone branch of the routing
// table). The function is the canonical split: a
// downstream rewrite to a path-based selector (e.g.
// /<host>/<sidecar>) can swap implementations without
// touching the surrounding handler.
//
// appsSuffix is the configured public suffix (e.g.
// ".on-faas.com"). When set, the listener hands us a
// hostname like "acme--metrics.on-faas.com"; we strip the
// suffix, split at the FIRST `--`, and re-attach the
// suffix to the appHost so the routing-cache key matches
// the apps row stored at provision time
// ("acme.on-faas.com"). When appsSuffix is empty the
// function is a bare split at first `--` — the unit-test
// seam.
//
// Inputs (with appsSuffix=".on-faas.com"):
//
//	"acme.on-faas.com"              → ("acme.on-faas.com", "")
//	"acme--metrics.on-faas.com"     → ("acme.on-faas.com", "metrics")
//	"acme--metrics"                 → ("acme", "metrics")
//	""                              → ("", "")
//
// Bare (no suffix) inputs:
//
//	"acme"                          → ("acme", "")
//	"acme--metrics"                 → ("acme", "metrics")
//	""                              → ("", "")
func SplitHostSelectorWithSuffix(host, appsSuffix string) (appHost, sidecarName string) {
	if host == "" {
		return "", ""
	}
	// Strip the suffix first so the `--` search bounds
	// itself to the bare app+selector. The reattach
	// happens after the split.
	if appsSuffix != "" && strings.HasSuffix(host, appsSuffix) {
		body := strings.TrimSuffix(host, appsSuffix)
		// Split inside the body (no suffix). This is the
		// canonical "--" split: the FIRST occurrence wins,
		// so a sidecar-name with `--` in it would parse
		// but the dashboard's name validation rejects
		// double-hyphens upstream so this case is rare.
		appHost, sidecarName, _ = strings.Cut(body, SidecarHostSeparator)
		appHost = appHost + appsSuffix
		return appHost, sidecarName
	}
	// No suffix configured — bare split.
	appHost, sidecarName, _ = strings.Cut(host, SidecarHostSeparator)
	return appHost, sidecarName
}

// SplitHostSelector is the unit-test seam that calls
// SplitHostSelectorWithSuffix with an empty suffix. The
// handler always goes through the suffix-aware variant;
// tests use this for table-driven coverage without
// carrying the suffix through every case.
func SplitHostSelector(host string) (string, string) {
	return SplitHostSelectorWithSuffix(host, "")
}

// SidecarSelectorForApp returns the (sidecarName, port)
// pair from an app's deployment for the sidecar selected
// by SplitHostSelector's second return value. The empty
// sidecarName resolves to (0, false) — the main workload
// route. The sidecar name MUST match a deployment row's
// sidecars[].Name; otherwise the lookup returns
// (port=0, ok=false) and the handler short-circuits to a
// 404.
//
// SidecarPort is taken from the per-deployment `sidecars`
// column's `port` field, NOT a fixed port (e.g. 9100). The
// port is set by imaged at build time and ships verbatim
// on the wake wire (issue #460 / PR-C). A future PR may
// move the sidecar-port lookup into a state.Store
// accessor; for now the AppResolver adapter keeps the
// hot-path test surface here so unit tests substitute a
// table without a Postgres round-trip.
//
// Kept intentionally narrow: this is a routing-key split +
// port lookup, not a full sidecar store accessor.
func SidecarSelectorForApp(app App, sidecarName string) (port int, ok bool) {
	if sidecarName == "" {
		// Main workload — port comes from the per-target
		// override (Target.Port, set by AdmitInstance on the
		// ScheddClient.AdmitInstance response). 0 here means
		// the main workload port, which the forwarder
		// resolves to netns.AppPort (8080) for legacy
		// cached targets (PR-B precedent).
		return 0, true
	}
	for _, sc := range app.Sidecars {
		if sc.Name == sidecarName {
			return sc.Port, true
		}
	}
	return 0, false
}

// PublicPortSelectorForApp resolves a reserved `port-<name>` selector to a
// TCP listener. A name is preferred when present; unnamed listeners use the
// deterministic `<protocol>-<port>` key generated from the manifest. UDP is
// intentionally rejected because the public edge is HTTP/TCP only.
func PublicPortSelectorForApp(app App, selector string) (port int, ok bool) {
	if !strings.HasPrefix(selector, PublicPortSelectorPrefix) {
		return 0, false
	}
	key := strings.ToLower(strings.TrimPrefix(selector, PublicPortSelectorPrefix))
	if key == "" {
		return 0, false
	}
	for _, candidate := range app.Ports {
		protocol := strings.ToLower(strings.TrimSpace(candidate.Protocol))
		if protocol == "" {
			protocol = "tcp"
		}
		if protocol != "tcp" || candidate.Port < 1 || candidate.Port > 65535 {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(candidate.Name))
		if name == "" {
			name = protocol + "-" + strconv.Itoa(candidate.Port)
		}
		if name == key {
			return candidate.Port, true
		}
	}
	return 0, false
}
