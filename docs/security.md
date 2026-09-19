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

Use `gregale app <slug> security --posture` in CI before enabling enforcement.

Report a suspected vulnerability through the security contact listed in [security.txt](security.txt). Include a minimal reproduction and affected resource ids, but never include live credentials or customer data. Gregale will acknowledge receipt and coordinate a safe disclosure window.
