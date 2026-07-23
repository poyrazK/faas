# ADR-031 · self-hosted Grafana OSS on the management bridge

- **Status:** accepted
- **Date:** 2026-07-23
- **Decision:** Spec §12 line 410 ("Prometheus (node_exporter + per-daemon `/metrics`) → Grafana Cloud free tier; alerting via Grafana → email + Pushover") is amended to read "…→ self-hosted Grafana OSS on the management bridge; alerting via Alertmanager → email + Pushover." Two concrete changes ship in PR #141:
  - `deploy/ansible/roles/grafana/` — apt-installs Grafana OSS (SHA-256 pinned), provisions the Prometheus datasource + `faas-fleet.json` dashboard from disk, and binds `10.0.0.1:3000`. The role mirrors `roles/alertmanager/` shape (apt + SHA-256 pin, `*_file` sealed-secret indirection for the admin password, dev_mode gate, hardened systemd unit).
  - `pkg/meter/residency.go` + `meterd_resident_gb_per_customer{plan}` gauge — emits the §12 "Resident GB per paying customer" panel (spec line 417) that the dashboard deferred until now.

- **Why:** Three reasons, in order of weight:
  1. **One-box architectural commitment.** CLAUDE.md §11 + §Component ownership put every operational component on the EX44. A third-party account on a managed service breaks that commitment, introduces a credential surface the rest of the role set doesn't have, and creates a billing surface that the financial model doesn't account for.
  2. **Unifies with Alertmanager.** PR #140 closed the §12 alert-rules + Alertmanager + degraded-flag loop. Spec line 410 named Grafana as the alert-notification path, but the only notification path in the repo is Alertmanager. Adding a Grafana-only notification path would duplicate routes and create two sources of truth.
  3. **Supplants a deferred dashboard row.** `deploy/grafana/faas-fleet.json` and `deploy/grafana/README.md` already deferred the §12 line 417 "Resident GB per paying customer" row until "meterd emits a per-customer timeseries". The new `meterd_resident_gb_per_customer` gauge closes that gap; the alert rule (`FaasResidentGbPerCustomerHigh`) follows.
- **Consequences:**
  - **Spec line 410 amended.** The single line is the only spec text that contradicts the new topology. No other §12 lines change. Recorded in this ADR's Cross-reference.
  - **New role shape.** `deploy/ansible/roles/grafana/` follows the alertmanager/prometheus canonical shape (defaults/tasks/handlers/templates + systemd unit, SHA-256 pin, loopback/bridge-bind assert, dev_mode gate, sealed-secret indirection). Operators pass `-e gv_release_sha256=<hex>` and `-e gv_admin_password_file=/etc/grafana/secrets/admin_password` at `make bootstrap`.
  - **New metric + alert rule.** `meterd_resident_gb_per_customer{plan}` (4-label cardinality, pre-instantiated) and `FaasResidentGbPerCustomerHigh` (warn-tier, family `residency`, fans out per-plan via `{{ $labels.plan }}`).
  - **Dashboard JSON corrected.** PR #141 fixes five threshold drifts against spec §12 in `deploy/grafana/faas-fleet.json` (D1 resident RAM 70/90 → 80/92, D2 wake latency red 0.8 → 1.5 with p50-specific override, D3 snapshot p95 clears thresholds, D4 cold-boot fallback ratio + percentunit, D5 build success gauge ordering) and adds M1 (API availability stat) + M2 (resident GB per paying customer stat) panels.
  - **Status page footer.** `deploy/statuspage/index.html:113-116` gains a "Grafana dashboard (operator)" link to the management-bridge bind so the operator can find the dashboard without grepping inventory.
- **Rejected alternatives:**
  - **Grafana Cloud free tier (no change).** Third-party account, third-party credential surface, third-party billing — three new failure modes for a one-box architectural commitment. Rejected.
  - **Commercial Grafana (paid tier).** Same architectural objection plus a recurring cost the financial model doesn't budget. Rejected.
  - **No dashboard surface at all (skip the §14 M8 gate).** Spec §12 mandates a Grafana-shaped SLO surface; the alertmanager+alert rules path covers notification but not visualisation. M8 acceptance ("SLO dashboard live") would never close. Rejected.
  - **Keep the deferred "resident GB per paying customer" row deferred.** Would leave a §12 row uncovered indefinitely. Rejected because the meterd residency tick is straightforward (one PG aggregate per minute) and the alert rule is one stanza of PromQL.

## File map

- `deploy/ansible/roles/grafana/{defaults,tasks,handlers,files,templates}/` — new role.
- `deploy/ansible/site.yml` — `grafana` role inserted after `prometheus`.
- `deploy/grafana/faas-fleet.json` — D1–D5 threshold fixes + M1, M2 panels.
- `deploy/statuspage/index.html:113-116` — Grafana footer link.
- `docs/STATUS.md:325-328` — closes the operator-verification follow-up.
- `deploy/grafana/README.md` — drops the "Deferred rows" note for `resident_gb_per_paying_customer` (now shipped).
- `pkg/wire/metrics.go` — `residentGBPerCustomer` GaugeVec + `SetResidentGBPerCustomer` method.
- `pkg/meter/residency.go` — `Residency` collaborator + `Paying` predicate.
- `pkg/meter/{loop,config}.go` — `ResidencyInterval` field + 5th timer in `Loop.Run`.
- `cmd/meterd/main.go` — wires `Residency` into the loop + `FAAS_METERD_RESIDENCY_INTERVAL` env override.
- `deploy/ansible/roles/prometheus/files/faas.rules.yml` — `FaasResidentGbPerCustomerHigh` rule.
- `docs/runbooks/FaasResidentGbPerCustomerHigh.md` — stub runbook following the PR #140 stubs.
- `docs/faas_implementation_spec.md:410` — the one-line spec amendment.
