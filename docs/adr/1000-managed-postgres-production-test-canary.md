# ADR-1000: Managed PostgreSQL production test account canary

- **Status:** accepted
- **Date:** 2026-10-12

## Context

The operator wants to test PostgreSQL through the deployed customer API using
an existing account. The staging-only gate makes that impossible on a production
deployment. Relabeling the deployment as staging would hide its actual purpose
and retain staging's optional account allowlist. The current production release
also predates the latest API wiring and explicitly unsets the registry path.

## Decision

Add a separate, default-off production test canary. It requires all of:

- `FAAS_ENVIRONMENT=production` and
  `FAAS_MANAGED_POSTGRES_PRODUCTION_CANARY=true`.
- The registry's explicit `provisioning_enabled` flag.
- A complete, passing version-8 provider and durable SQL lifecycle artifact,
  bound to the configured placement and exact nonempty account allowlist.
- An approval lifetime of at most 24 hours, already qualified and unexpired.
  Expiry is checked at every provisioning decision, including worker sweeps.
- Enabled, valid usage policy with positive compute, storage, history and egress
  prices and positive ceilings for all canonical meters and estimated cost.

Production cannot fall back to environment-only approval. Staging admission
keeps its existing behavior. The account gate rejects an empty or malformed
production allowlist; customer plan limits and existing usage freshness and
health admission continue to apply. Database reads, deletion and credential
revocation remain available after admission closes so the canary can drain.

The approval is operator-owned evidence, not cryptographic remote attestation.
Metering guards admission using complete provider windows; these ceilings are
not a provider-enforced spending cap or a promise that active resources stop
accruing charges when provisioning closes.

This explicitly permits an operator test account on the production API while
native guest acceptance is pending. It does not declare general availability
or qualify deployment, guest networking, snapshot restore, or scheduler behavior.
Supported native host acceptance remains required before a broader rollout.
No plan changes, automatic qualification, or billing-system changes are added.

## Activation and rollback

Deploy a signed release containing this gate through the existing production
rollout mechanism. Prepare and verify qualification against an isolated marked
catalog with the intended production placement and account ID. Never run the
durable qualifier against the production catalog. Install the resulting artifact
and private provider credential, remove the explicit registry unset, then restart
apid after checking migration compatibility and the existing deployment gates.

For rollback, turn off the canary flag or registry provisioning flag, keeping
the registry and key installed until all known resources and bindings are
provider-confirmed deleted. Revoke the test key after draining. Native acceptance
and fresh live qualification results must be reported separately from activation.

## Validation

Regression tests cover default-off production, exact account isolation, missing
and tampered evidence, old artifact versions, absent durable lifecycle proof,
expiry without restart, future qualification, oversized approval duration,
missing prices/ceilings, and preservation of staging account behavior.

## Immediate operator test follow-up

An explicitly requested immediate test may use a separate private
`OperatorTestApproval` with purpose `operator_test`, exact placement and account
binding, and a lifetime of at most two hours. Set
`FAAS_MANAGED_POSTGRES_OPERATOR_TEST_PATH` alongside the explicit production
canary flag and nonempty allowlist. The file must be regular and not writable
by group or others. It is authoritative when configured: missing, malformed,
expired or mismatched operator approval fails closed even if v8 evidence exists.

This approval grants temporary test admission while qualification runs. It is
rejected by the provider qualification artifact parser and cannot be represented
as a passing v8 result. Existing entitlements, usage safeguards, authentication,
credential sealing and cleanup behavior continue to apply. Admission closes at
expiry without restarting; existing resources must still be drained. An API-only
component hotfix can apply this gate from the deployed release source, with an
independently recorded checksum, source patch and rollback, when the operator
explicitly requests immediate activation instead of the signed fleet workflow.
