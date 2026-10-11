# ADR-944: Isolated durable entity restore validation

Status: local implementation, unqualified; default off.

## Decision

`FAAS_DURABLE_ENTITY_RESTORE_ISOLATION_ENABLED=1` requires the durable entity
preview, application restore validation and the disposable execution API. It
replaces ADR-943's ordinary application invocation with the existing sanitized,
networkless disposable execution lifecycle. apid seals source/input and admits
an execution; schedd owns execution and teardown, and vmmd owns the VM. No new
VM ownership path is introduced. Application state still commits through object
storage conditional writes; execution scheduling uses the existing control-plane
SQL queue.

`FAAS_DURABLE_ENTITY_RESTORE_VALIDATOR_BUNDLES_FILE` names a startup-loaded JSON
array of release-owned bundles. Each entry has `app_id`, `deployment_id`,
`runtime`, `entrypoint`, `files` and `sha256`. Files use the ordinary execution
bundle representation (path plus base64 content). Supported sanitized runtimes
are node22, node24, python312 and python313. The SHA-256 covers compact JSON of
`{"runtime":...,"entrypoint":...,"files":...}` in this field order; file order
is significant. Registry loading rejects unknown fields, duplicates, invalid
identities, paths and integrity mismatches. The registry is bounded to 4 MiB and
128 bundles. Release tooling must publish a reviewed, secret-free validator
bundle for each intended live deployment and configure all writers consistently.
Changing a registry requires a process restart. This is an operator-managed
registry, not automatic application build artifact extraction.

The validator receives the ADR-943 envelope as its one-shot execution input and
returns only `{"protocol_version":1,"valid":true|false}` as its result. A Node
entrypoint exports a default input function; Python defines `main(input, context)`.
Standalone validation returns `bundle_sha256` and `isolation: "networkless"`.
Copy the digest into `validation_bundle_sha256` and the deployment into
`validation_deployment_id` for restore. The digest and deployment participate in
receipt identity. Restore revalidates under the private claim; successful receipt
replay does not run code or require the bundle to remain available. Bundle digest
pins cannot be silently accepted by the cooperative validation path.

Executions receive network mode none, no application environment, integration
IDs, artifact grants or output files. They use standard plan admission, encrypted
payloads, a ten-second deadline and a 1024-byte output limit. Missing bundle,
unavailable runtime, plan/input limit failures, rejected verdicts, truncated
results and failed executions cannot publish entity state. API cancellation or
timeout requests execution cancellation; schedd retains lifecycle ownership.
There is no fallback into an application VM. Validator code can write disposable
scratch space. The platform does not inject credentials, but cannot remove secrets
embedded in source or candidate application state. Do not print candidate data:
execution stdout/stderr remain subject to ordinary account execution retention.

## Limits and qualification

Live deployment is checked before and after validation, but SQL deployment
selection and the bucket commit are not atomic; the deployment race described in
ADR-943 remains. The digest pins validator bytes, not a transactional deployment
reservation. This feature is not qualified for production by source inspection.

The testing agent must run source cases and native acceptance on a dedicated host:
verify no NIC/public/DNS/service egress, no app secret or integration credentials,
fresh scratch isolation, normal scheduler teardown and cancellation, encrypted
payload persistence, plan quota rejection, malformed/truncated verdict rejection,
missing/wrong-app/tampered bundle rejection, deployment changes, stale-owner fencing,
receipt replay and bundle-change conflicts. No tests, builds or live bucket/VM
checks were run during this implementation, as requested.
