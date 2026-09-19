# Security

Gregale isolates workloads in Firecracker-backed microVMs and applies tenant, network, and resource boundaries at the platform edge. You still own application authorization, dependency hygiene, and data classification.

Use scoped, expiring tokens; MFA for interactive accounts; secret storage for credentials; and digest-pinned images for production. Review audit events after identity, billing, registry, or egress changes. Do not put secrets in source, logs, alert URLs, support tickets, or image layers.

## App deploy guard

Every app exposes a read-only security posture report at
`GET /v1/apps/{slug}/security`. An administrator can set the deploy-time
policy through the same MFA-protected surface (or with
`gregale app <slug> security --security-policy=enforce`):

- `off` reports findings without affecting deploys (the default).
- `warn` keeps deploys flowing while making the policy explicit for staged
  rollout.
- `enforce` rejects new deploys while high-severity findings remain, such as
  anonymous access or credentialed wildcard CORS.

For `enforce` apps, an image promotion also requires a fresh, complete scan
whose recorded image reference matches the deployment. Failed, incomplete,
unmatched, or high/critical/unknown-severity scan results fail the deployment
before snapshotting. This proves that the scanned artifact is the one being
promoted; it does not replace dependency patching or application review.

Use `gregale app <slug> security --posture` in CI before enabling enforcement.

## Quarantine recovery

When an enforce-policy app is parked after a live image scan regresses,
`GET /v1/apps/{slug}/security` reports the quarantined deployment and digest.
After deploying a newer image, use `POST /v1/apps/{slug}/security/recover` with
that deployment id. Recovery restores traffic only when every live canary has
fresh, complete, digest-matched scan evidence with zero high, critical, or
unknown findings. The operation requires deploy-write scope and MFA and emits
an audit event when the app returns to `active`.

Report a suspected vulnerability through the security contact listed in [security.txt](security.txt). Include a minimal reproduction and affected resource ids, but never include live credentials or customer data. Gregale will acknowledge receipt and coordinate a safe disclosure window.
