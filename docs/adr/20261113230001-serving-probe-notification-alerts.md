# ADR-20261113230001 · Serving-probe notification warning rules

- **Status:** accepted for implementation; production convergence separate.
- **Date:** 2026-11-13
- **Extends:** [direct notification evidence](20261113220001-serving-probe-notification-transport.md).

The probe exports direct webhook and heartbeat transport outcomes. Add
recordings and a warning so failed submissions become actionable in the
control-plane monitoring stack without changing serving assessments.

Extend the existing one-minute serving coverage group, preserving in-order
evaluation of inventory and source guards. Project two channel telemetry slots
per expected node. Assess only one discovered compute target with a fresh
successful scrape, a unique matching run clock within the inventory age policy,
and unique channel fields from that exact scrape/source. Keep non-finite,
duplicate, stale, future or inconsistent metadata unknown. Strip arbitrary
labels before intermediate recording and expose only stable node/channel
identity on completed outcomes and alerts.

State 0 is explicitly disabled, 1 unknown, 2 last completed submission succeeded
and 3 last completed submission failed. Require Boolean configuration/attempt
flags, valid fixed failure codes and coherent completion/success clocks. A
present but unusable optional success clock is invalid, not absent history.
Disabled channels with retained history or an attempted request are unknown.
Older helpers and cleared history remain unknown, never fabricated success.
Quiet or suppressed channels retain a known completed outcome. Historical
success does not prove present reachability; a historical failure is described
as the last completed outcome without claiming a continuous provider outage.

Warn with `FaasServingProbeNotificationFailed` when a validated failure remains
for five minutes. Use the fixed code as its value, normal warn routing, internal
status, and a dedicated runbook. Exclude raw URLs, credentials, provider text,
scrape addresses and arbitrary labels. A verified successful submission resolves
the warning, while missing evidence or disablement also removes it without
claiming recovery. Keep external missing-heartbeat monitoring because the
control plane or a shared provider can prevent warning delivery.

Regenerate alert and recording catalogs. Test exact shipped rules through
promtool, including the hold boundary, recovery, rotation, both channels,
skips, older helpers, malformed clocks/codes, duplicate/foreign sources and
label projection. Keep operator API/CLI additions for a subsequent change.

See the [operator guide](../ops/serving-probe.md#warnings-from-completed-notification-failures).
