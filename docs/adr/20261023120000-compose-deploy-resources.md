# Compose deploy resources and replicas set app settings

Date: 2026-10-23
Status: Accepted
Amends: ADR-1337 (Compose key reporting)
Related: ADR-137 (execution modes and replicas), spec §6.2 invariants 1 and 2

## Context

A Compose service states its size with `deploy.resources.limits` (`memory`,
`cpus`) and its count with `deploy.replicas`, or with the older service-level
`mem_limit`, `cpus` and `scale`. The scan warned that these "come from the plan
and app scaling settings", so a project app started with the store's defaults
(128 MB, one instance) whatever the file asked for, and the user had to find
and set the same values again with `gregale app <slug> --ram` and `--max-concurrency`.

The app row already has these settings: `ram_mb`, `cpu_millicores` and
`max_concurrency`, plus the worker and service replica policies in the
manifest. The apps API checks them against the plan.

## Decision

Declared Compose resources set the app's own settings when the project is
applied.

- **Memory.** `deploy.resources.limits.memory` (or `mem_limit`) is a size in
  bytes with an optional `k`, `m`, `g` or `t` suffix in powers of 1024 (`b`,
  `ib` endings accepted, as Docker accepts them), rounded up to whole MiB, and
  becomes `ram_mb`. More than the plan's RAM per app fails project admission
  with the limit and the requested value, as the apps API refuses it.
- **CPU.** `deploy.resources.limits.cpus` (or `cpus`) becomes
  `cpu_millicores`, rounded up to the next app CPU share (0.25, 0.5 or 1) with
  a warning when it moves. More than one CPU is lowered to 1 with a warning:
  an app's share is at most one CPU on every plan.
- **Replicas.** `deploy.replicas` (or `scale`) follows the app's execution
  mode: a request-mode app's `max_concurrency` (its instance cap; instances
  still start on demand and park when idle), a worker's fixed replica count
  (`worker_replicas` min = max = N) and a service's (`service_replicas`
  min = max = desired = N, with `max_concurrency` N). It does not apply to
  jobs. More than the plan's cap for that mode fails admission. `replicas: 0`
  is warned and ignored.
- **Ownership.** A declared setting is written on every apply, replacing a
  value set through the API; one the file does not declare keeps the app's
  value, and removing a declaration leaves the last value in place. The
  project apply transaction writes them only when the service declares
  resources (`ProjectReconcileMutation.SetResources`), and only positive
  values. A changed declaration is an app change in the plan. PR previews take
  the values their head declares.
- **Everything else is reported.** `deploy.resources.reservations`, limits
  other than memory and cpus, `deploy.restart_policy`, `update_config`,
  `rollback_config`, `placement`, `endpoint_mode`, `labels` and a non-
  replicated `mode` are warned, each with its reason. `cpu_shares`,
  `cpu_quota`, `cpuset`, `mem_reservation` and `memswap_limit` still warn.

## Consequences

- A Compose file's sizes and counts apply without a second command, within
  the same plan limits as the apps API; invariants 1 and 2 are unchanged
  because admission keeps every value within the plan.
- Billing still uses plan-checked `ram_mb` (spec §4.7); a smaller declared
  memory bills less, as the same API setting does.
- Editing a declared setting through the API lasts only until the next
  apply; plans show the declared values (`resources` on each workload).
