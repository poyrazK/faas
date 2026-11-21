# ADR-20261121110000: Alertmanager receiver settings from an environment variable

- **Status:** accepted
- **Date:** 2026-11-21
- **Amends:** ADR-580 (control-plane role convergence),
  ADR-20261023124955 (observability on the control plane)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

The control plane's Ansible variables come from the `CP_ANSIBLE_VARS_B64`
environment secret. GitHub lets an operator write a secret but nobody, the
deploy included, read it back, so changing one value means rewriting the
whole secret from a copy someone kept.

The production control plane moved from GCP to OVH. Its Alertmanager now
pages by email (Resend) plus the heartbeat, and the Slack webhook file of
the GCP host was never put on the new one. The secret still names that
Slack file and no SMTP password file. Releases did not notice because the
per-role planner (ADR-20261030150000) skipped the unchanged alertmanager
role. v0.1.18-rc.363 changed `bootstrap.yml`, every role converged, and the
role's secret-file check stopped the control-plane stage on the missing
Slack file. Creating a placeholder file would have rendered a dead Slack
receiver and dropped the email receiver.

## Decision

Convergence applies the GitHub environment variable
`ALERTMANAGER_VARS_OVERRIDE` after the secret (`scripts/ci/alertmanager_vars.py`,
both the release and the rollback convergence in `cd-controlplane.yml`).

- It is a JSON object. Its keys replace the same top-level keys of the
  vars file.
- Only receiver settings are allowed: the eight `am_*_file` paths, the SMTP
  relay, username and TLS flag, and the sender and recipient addresses. A
  path is `""` (receiver off) or a file under `/etc/alertmanager/secrets/`.
  Anything else fails the deploy before Ansible runs.
- Unset or empty changes nothing.

These are paths and addresses, not secret bytes; the bytes stay in
`/etc/alertmanager/secrets` on the host, as before.

## Consequences

- A receiver change is a `gh variable set` on the environment, visible and
  reviewable, instead of a blind rewrite of the secret.
- The variable wins over the secret for these keys. Whoever later rewrites
  the secret should move the values into it and clear the variable, or the
  variable keeps overriding.
- Changing the variable changes the rendered inventory hash, so the next
  release converges every control-plane role once.
