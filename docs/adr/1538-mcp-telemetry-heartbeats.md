# ADR 1538: Independent MCP telemetry heartbeats

Updated Go, Python and Node web starters emit a payload-free version-1 heartbeat
immediately and every 30 seconds, independent of requests. Shutdown stops the
background emitter; task-only workers do not emit web coverage. Go startup disables the standard logger timestamp prefix so RPC events also
remain parseable JSON at host intake. Existing apps
must regenerate/adopt the telemetry module and startup wiring.

vmmd binds each log lifecycle to scheduler-supplied app/deployment identity and
the host instance ID. The collector accepts at most 4096 instance lifecycles,
parses at most 4 KiB per line, and ignores guest identity and timestamp fields.
Only newly committed log lines count. Replacement/retirement callbacks cannot
mutate another generation. Scrapes snapshot under a lock and send after release.
Pause removes series; resume resets heartbeat age and the startup grace period.

The account-scoped MCP dashboard compares current reports against scheduler
instances that execute or are starting. Parked, frozen, warm, failed and stopped
instances do not require heartbeats. Missing executing instances or backend
failures are unavailable, rather than a reduced healthy denominator. Empty
scheduler inventory is idle. Reporting, partial, starting and stale states are
separate from protocol performance, readiness and tool success. A live heartbeat
with no recent RPC completions means process telemetry is reporting without
recent completions; it does not prove an absence of in-flight requests.

Telemetry-loss notifications are opt-in operator alerts via
`MCP_TELEMETRY_LOSS_ALERT=true`, independent of customer performance rules. The
threshold is two minutes, startup/resume grace three minutes, sustained failure
five minutes. Missing scrapes and never-instrumented instances require host
availability monitoring/dashboard investigation. No customer webhook API or
schema change is introduced in this increment.
