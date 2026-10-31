package api

import (
	"strconv"
	"strings"
)

// SelectorHostSeparator joins an app's platform host label and a routing
// selector. The public edge routes <app>--<companion>.<apps domain> to that
// companion's port (ADR-069, ADR-072) and <app>--port-<listener>.<apps
// domain> to a declared public TCP listener (ADR-176).
const SelectorHostSeparator = "--"

// PublicPortSelectorPrefix marks a selector that names an app-owned public
// TCP listener rather than a companion (ADR-176).
const PublicPortSelectorPrefix = "port-"

// PublicPortSelectorKey returns the selector key for a declared listener: its
// name, or "<protocol>-<port>" when unnamed. ok is false for a listener the
// public edge does not route: internal (ADR-576), non-TCP or out of range.
func PublicPortSelectorKey(p WorkloadPort) (string, bool) {
	protocol := strings.TrimSpace(string(p.EffectiveProtocol()))
	if p.Internal || protocol != string(WorkloadPortTCP) || p.Port < 1 || p.Port > 65535 {
		return "", false
	}
	if name := strings.ToLower(strings.TrimSpace(p.Name)); name != "" {
		return name, true
	}
	return protocol + "-" + strconv.Itoa(p.Port), true
}

// SelectorHostname returns <label>--<selector>.<rest> for an app platform
// hostname <label>.<rest>, or "" when platformHost has no dot.
func SelectorHostname(platformHost, selector string) string {
	label, rest, ok := strings.Cut(strings.ToLower(platformHost), ".")
	if !ok || label == "" || rest == "" || selector == "" {
		return ""
	}
	return label + SelectorHostSeparator + strings.ToLower(selector) + "." + rest
}
