# ADR-20261021080847: Release GitOps execution per account with a platform flag

- **Status:** accepted
- **Date:** 2026-10-21
- **Related:** ADR-568, ADR-1700, ADR-20261017190802, ADR-20261017190812

## Context

GitOps runs in report mode in production. Enforce mode, which applies an
approved revision and builds, qualifies, activates and serves its candidates,
was reachable only in an isolated development acceptance process
(`FAAS_DEV`, `GREGALE_GITOPS_ACCEPTANCE` and
`FAAS_ENVIRONMENT_GITOPS_ACCEPTANCE_ENFORCEMENT`, which the production
control-plane role rejects). schedd's qualification dispatch had its own
boot-time switch, `FAAS_ENVIRONMENT_GITOPS_QUALIFICATION_DISPATCH`, that applied
to the whole node.

The native journey (`TestEnvironmentGitOpsNativeMetal` and
`TestEnvironmentGitOpsSourceJobsNativeMetal`) runs on the internal n2 node,
which is the accepted evidence host for metal suites. Opening execution needs
a gate that can admit one design partner first, widen later, and stop
everything at once, without a restart or a deploy.

## Decision

1. Add the platform flag `environment_gitops_execution` (ADR-1700), consumed
   by apid and schedd. It decides release only; plan entitlements are unchanged.
2. **apid decides accounts.** The enforce worker now runs in every apid. It
   claims only enforce-mode sources. Before it observes or applies anything it
   evaluates the flag for the source's account (looked up by ID, so plan rules
   work). A source whose account is not admitted, or whose account cannot be
   read, finishes its run as `blocked` with
   `environment_git_enforcement_unavailable` and is checked again at the
   normal interval. It has no intent, build, qualification or serving effect.
3. **Customer controls follow the same decision.** Creating a source in, or
   switching a source to, enforce mode returns
   `409 environment_git_enforcement_unavailable` unless the flag admits the
   account and the enforce worker is running in this apid. The dashboard
   offers enforce mode only then. Report mode is always available.
4. **schedd follows the kill switch.** Qualification dispatch starts at boot
   whenever admission ledger seeding succeeded, and each 30-second pass first
   reads the flag's `enabled`. While it is off, the pass does nothing. schedd
   does not evaluate accounts: qualification requests exist only because apid
   queued them for an admitted account. A request queued before an account was
   withdrawn can still be qualified. It cannot activate, because activation is
   the held source's next run.
5. **Boot fallbacks.** In both daemons,
   `FAAS_ENVIRONMENT_GITOPS_QUALIFICATION_DISPATCH=1` seeds
   `{"enabled": true, "default": false}`. That turns the kill switch on and
   admits no account. Only the isolated acceptance markers seed
   `{"enabled": true, "default": true}` in apid. A production control plane
   therefore admits no account until an operator does. As ADR-1700 specifies,
   the first operator write replaces these fallbacks, including after a
   restart.
6. **Acceptance uses the production path.** The acceptance markers no longer
   start a separate executor. They seed the flag, and the same enforce worker
   and the same checks run in acceptance and in production.
7. **Diagnostics.** A held source explains itself as
   `environment_git_enforcement_unavailable` (stage `mode`, status `blocked`)
   in the API, the CLI (`gitops status`/`wait`) and the dashboard. The
   guidance does not suggest enabling the gate.

## Rollout

1. Deploy, with `FAAS_ENVIRONMENT_GITOPS_QUALIFICATION_DISPATCH` unset. Nothing
   changes: the flag is off in both daemons.
2. Set the qualification service proxy URLs on compute nodes, so that graphs
   with service bindings can be qualified.
3. Admit the first account and enable the kill switch:
   `gregalectl flags allow --key environment_gitops_execution --account-id <uuid>`
   and then `gregalectl flags enable --key environment_gitops_execution`.
   Check the apply receipts for apid and every schedd with `flags show`.
4. Widen with groups or a rollout. Use `flags disable` as the stop switch.

## Consequences

- An operator can release, widen or stop GitOps execution within seconds,
  with an audit trail and MFA, with no restart.
- Every apid polls for enforce-mode claims every five seconds. Each poll is
  one claim transaction, which finds nothing until a source is in enforce
  mode. A held source records one blocked run per check interval, inside the
  existing per-source run retention.
- Withdrawing an account holds its enforced sources at their last applied
  state. Customers can switch them back to report mode at any time.
- Not changed: report mode, source polling, the review/approval flow,
  promotion and rollback proposals, and the dev-only acceptance variable's
  rejection on production control planes.

## Validation

- `pkg/environmentgitops`: a withheld account is never observed or applied,
  an admitted one is, and report mode ignores admission.
- `pkg/apid/apidserver`: the HTTP gate refuses enforce mode for a production
  control plane and for partial acceptance markers, and admits an account
  released by a rule. The enforce worker holds an unadmitted account without
  changing intent, and the kill switch overrides an account rule.
- `cmd/schedd`: the boot fallback admits no account. Operator writes stop and
  restart dispatch, and a failed ledger seed never starts it.
- `pkg/environmentgitops/diagnostics` and `pkg/dashboard`: the held status is
  explained, and enforce mode is offered only to released accounts.
- The native journey on n2 runs through the same enforce worker, with the
  flag seeded by the acceptance markers.
