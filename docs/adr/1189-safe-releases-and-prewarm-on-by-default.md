# ADR-1189: Safe Deploy and scheduled prewarm on by default

- Status: Accepted
- Date: 2026-10-15
- Related: ADR-122 (Safe Deploy), ADR-160 (scheduled prewarm), ADR-1152 (completion-only rollouts), ADR-143 (environment contract)

## Context

Two shipped capabilities never run on a control plane built from `deploy/`:

- **Safe Deploy** (canary progression, alert rollbacks, stuck-canary
  recovery) needs the internal service-token pair
  `FAAS_CANARY_PROGRESSION_TOKEN` and `FAAS_SAFEDEPLOY_TOKEN` in both APID's
  `/etc/faas/sealed.env` and meterd's `/etc/faas/secrets/meterd/billing.env`.
  The runbook asked operators to create them by hand; nothing did, so canary
  ladders never advanced and alert rules with `action: rollback` only sent
  their webhook. ADR-1152 completes ladder-less rollouts without the tokens,
  but nothing more.
- **Scheduled prewarm** needs `FAAS_PREWARM_ENABLED=1` on schedd. The API
  accepts prewarm intents regardless, so without the flag customer intents
  stayed `pending` forever.

## Decision

- The `control_plane_service` role generates the token pair when it is
  missing (`provision-safe-release-tokens.sh`): two distinct 32-byte random
  hex values written to both files. Existing values are never replaced; a
  value present in only one file is copied to the other; differing values or
  identical tokens fail the run. Files keep their mode and owner, values are
  never printed, and APID and meterd restart only when something changed.
- `faas_safe_releases_enabled` (default `true`) gates the task. Switching
  Safe Deploy off now means setting it to `false` before removing the tokens,
  otherwise the next run restores them.
- The schedd unit sets `FAAS_PREWARM_ENABLED=1`; the environment contract
  records it as unit-delivered.

## Consequences

- New and existing control planes run Safe Deploy and the prewarm drain
  after their next playbook run. Behaviour with tokens present is unchanged.
- The prewarm drain only acts on intents customers create; without intents
  nothing is woken.
- The kill switch in `docs/runbooks/FaasSafeDeployRollout.md` gains the
  `faas_safe_releases_enabled: false` step.
