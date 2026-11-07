# Drills and exercises

A runbook nobody has run is a guess. This page lists what we rehearse, how
often, and when each was last done. Update the "Last run" column in the same
pull request as the drill record.

Records live next to this file as `YYYY-MM-DD[-HHMMSS]-<drill>.md`, or under
`docs/ops/evidence/<date>-<name>/` when the drill produces raw data. Use the
templates here where one exists. A failed drill is still recorded: it is the
most useful kind.

## Live drills

Run against a non-production fleet unless the row says otherwise.

| Drill | Runbook | Cadence | Last run | Result |
|---|---|---|---|---|
| Postgres restore from off-host backup (M8) | [PostgresBackup.md](../runbooks/PostgresBackup.md), `deploy/scripts/faas-m8-restore-drill.sh` | monthly; `check-restore-drill-evidence.sh` rejects records older than 30 days | 2026-09-09 ([record](2026-09-09-060104-restore-drill.md)) | PASS. **Overdue** |
| Public TLS cutover | `deploy/scripts/faas-tls-cutover-drill.sh` | before each edge change, at least quarterly | 2026-09-06 ([record](2026-09-06-124853-tls-cutover-drill.md)) | PASS |
| Safe release (bad release aborts, rollback within 60 s p95) | [FaasSafeDeployRollout.md](../runbooks/FaasSafeDeployRollout.md) | quarterly | 2026-10-10 ([record](2026-10-10-safe-release-drill.md)) | see record |
| Event pipeline failover | [event-failover-drill.md](../ops/event-failover-drill.md) | quarterly | 2026-10-29 ([report](../ops/evidence/20261029-event-ga-drill/REPORT.md)) | provisional, one host |
| Control-plane unplanned failover to the standby | [ControlPlaneFailover.md](../runbooks/ControlPlaneFailover.md) | quarterly | never recorded | **Missing** |
| Control-plane rebuild from state backup | [ControlPlaneStateBackup.md](../runbooks/ControlPlaneStateBackup.md) | twice a year | never recorded | **Missing** |
| Compute node loss | [twonode-fault-injection.md](../runbooks/twonode-fault-injection.md) | quarterly | never recorded here | **Missing** |
| Break-glass release pause, credential revoke (dry run), resume | [customer-secret-store-compromise.md](../runbooks/customer-secret-store-compromise.md) §1 and §5 | twice a year | never recorded | **Missing** |
| Seal-domain break-glass | [seal-domains-rollout.md](../runbooks/seal-domains-rollout.md) §3 | after each domain contracts | see that runbook | |
| Paging path (test page reaches Tier 0, Tier 1 reachable) | [escalation.md](../ops/escalation.md) | monthly, and at every on-call change | never recorded | **Missing** |
| Row-level restore after an operator error | [operator-error-recovery.md](../runbooks/operator-error-recovery.md) | twice a year | never | **Missing** |

## Tabletop exercises

A tabletop walks a scenario on paper with the people who would respond: no
production changes. Use the runbook as the script, read each step aloud, and
stop wherever someone does not know the answer, lacks access, or the step
names a command that does not exist. Those stops are the output; file each
one as a follow-up.

Run one per quarter, rotating through the list. Record each as
`YYYY-MM-DD-tabletop-<scenario>.md` with attendees, the stops and the
follow-ups.

| Scenario | Runbook | Last run |
|---|---|---|
| Customer secret store exposure (honeytoken fires) | [customer-secret-store-compromise.md](../runbooks/customer-secret-store-compromise.md) | never |
| GitHub maintainer account taken over; unexpected release tag | [release-pipeline-compromise.md](../runbooks/release-pipeline-compromise.md) | never |
| GCP project suspended on a Friday night | [cloud-provider-loss.md](../runbooks/cloud-provider-loss.md) | never |
| `gregale.dev` name servers changed at the registrar | [dns-registrar-compromise.md](../runbooks/dns-registrar-compromise.md) | never |
| Operator laptop stolen | [operator-credential-compromise.md](../runbooks/operator-credential-compromise.md) | never |
| Internal CA key exposed with a control-plane backup | [internal-ca-compromise.md](../runbooks/internal-ca-compromise.md) | never |
| Cloudflare down for four hours | [third-party-outage.md](../runbooks/third-party-outage.md) | never |
| Personal-data breach, 72-hour notice | [breach-notification.md](../runbooks/breach-notification.md) | never |

## Each drill also checks

- The runbook's commands still exist and do what it says.
- [escalation.md](../ops/escalation.md) still matches the deployed paging
  path and contacts.
- The "Before you need it" checklists in the scenario runbooks.
