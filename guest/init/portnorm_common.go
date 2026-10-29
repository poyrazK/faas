// Port normalization (ADR-051). The in-guest ladder that surfaced an app's
// bind on :8080 is retired (ADR-051 amendment 2026-10-25): the host routes
// to the deployment's port (AppManifest.EffectivePort(), injected as PORT),
// and production apps listen on it (ADR-1170). An app that listens elsewhere
// fails readiness with app_not_listening naming the port it used. Only
// watch-mode development images bridge ports (dev_watch_bridge.go).
package main

// PortNormMode is the report's port_norm_mode. Stable string values: hosts
// still accept "dnat" and "forward" from older guests.
type PortNormMode string

// PortNormNone is the only mode this guest reports.
const PortNormNone PortNormMode = "none"
