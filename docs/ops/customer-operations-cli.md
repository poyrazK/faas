# Customer Operations for backend developers and operators

The local backend and CLI slice gives owning accounts access to customer work
without building a frontend. Admission remains gated by the bounded HTTP preview
policy. Listing, evidence, result retrieval and reconciliation continue to work
when new admission is closed. Native KVM and fleet activation qualification are
still separate release gates.

Account commands use account credentials, read/deploy scopes and the backend's
MFA boundary. `start --self` requires a customer credential with
`platform_tenant:operations:manage`. Status, events, watch and download support
`--self` with the customer read scope; cancellation requires manage. Tenant
ownership comes from the credential. Self commands omit `--app` and cannot choose
another tenant. `gregale operations` retains its existing exclusive-work policy
and lease commands.

## Define, inspect and validate

Definition discovery pins an exact deployment. `definitions list` returns sorted
metadata including immutable IDs and revisions; `definitions get` returns the
full contract and bundled schemas as JSON. These account reads remain available
when new Operations admission closes.

```sh
gregale customer-operations definitions list --app exports --deployment DEPLOYMENT_UUID --json
gregale customer-operations definitions get customer-export --app exports --deployment DEPLOYMENT_UUID
gregale customer-operations validate --dir examples/customer-operation-export --app exports --plan pro --json
gregale customer-operations validate --dir examples/customer-operation-export --app exports --plan pro \
  --name customer-export --input-file input.json --json
gregale customer-operations types --dir examples/customer-operation-export --app exports --plan pro
gregale customer-operations types --dir examples/customer-operation-export --app exports --plan pro --check
(cd examples/customer-operation-export && npm run typecheck)
```

`validate` runs offline without credentials. It uses the backend's strict
YAML/TOML manifest parser, plan limits and schema compiler, and emits normalized
contracts and their revisions. Select an explicit target plan. Sample input
requires `--name` and is checked against that operation's input schema. Manifest
and schema files are bounded, regular files confined to the selected source
root; symlink components and external schema resources are rejected.

`types` runs the same source validation, then projects each selected input and
output JSON Schema into a generated TypeScript declaration file. It defaults to
`customer-operations.generated.d.ts`, refuses to replace an unmarked file and
can select one operation with `--name`. The starter apps reference the generated
types through JSDoc. This is compile-time guidance; Gregale continues to enforce
the schemas when accepting and completing work. Commit the generated declaration
and use `types --check` plus `npm run typecheck` in CI. The first reads but never
writes, reports `current`, `missing` or `stale` in JSON mode, and exits nonzero
when the file is missing or stale. The second checks the starter browser code
against the generated declarations and installed SDK types without emitting
files. Existing generated paths without the marker are rejected in both modes.

## Diagnose submission prerequisites

```sh
gregale customer-operations doctor --app exports --deployment DEPLOYMENT_UUID \
  --tenant TENANT_UUID --name customer-export --timeout 15s --json
```

`doctor` uses account read credentials and MFA. Select an exact owned deployment
and an owned tenant; omit `--name` to inspect every definition in that deployment.
It reads metadata and the responding API node's current preview policy. It checks
tenant status, plan allowance, pending capacity, deployment and contract pins,
release availability, and the presence of workload trust and private result
storage. It reports stable reason codes and remediation without exposing policy
contents, infrastructure errors, webhook targets or secrets. Reads remain
available when new admission is closed.

Each definition's `execution_preview` check includes `execution_kind` (`http`,
`workflow` or `job`). Human output shows the type beside the operation name.
`preview_execution_kind_excluded` means that cohort allows other types but
excludes this definition; legacy policies permit HTTP only. Use `--name` to
observe one definition when a mixed deployment contains blocked types.
Allowlist changes do not hide retained work or retry completed business work.

The report separates submission prerequisites, completion delivery configuration
and runtime qualification. A disabled completion webhook produces a delivery
warning without blocking otherwise eligible business work. Capacity reads reserve
no slot. `configured` means configuration is present; it does not prove storage
I/O, reporting authority or destination transport. Gateway admission, runtime
reporting, storage I/O, native lifecycle and fleet rollback remain explicitly
`unknown` because this endpoint does not probe them. The report makes no mutation
or outbound request and does not open admission.

`eligible` means the observed submission prerequisites were satisfied on the
responding API node at the report's timestamp. State and policy can change after
the reads, and other serving nodes may differ. Use native and fleet qualification
receipts before enabling a customer cohort. Human output includes remediation;
`--json` returns the typed report. Exit codes are 0 for observed eligibility, 1
for a submission blocker and 3 for an unknown prerequisite or invalid report.
Authentication and HTTP errors retain normal CLI error handling; a local timeout
exits 124 and Ctrl-C exits 130. Delivery warnings and unverified qualification do
not change the submission exit code.

## Submit and resume as the customer

```sh
gregale customer-operations start --self --definition DEFINITION_UUID \
  --input-file input.json --idempotency-key export-order-42 \
  --receipt-file export-order-42.request.json --timeout 30s --json
# After an uncertain response, reuse the receipt and original request identity.
gregale customer-operations start --self --receipt-file export-order-42.request.json --json
gregale customer-operations get OPERATION_UUID --self --json
gregale customer-operations events OPERATION_UUID --self --after 0
gregale customer-operations watch OPERATION_UUID --self --timeout 5m --json
gregale customer-operations download OPERATION_UUID --self --output export.csv
gregale customer-operations cancel OPERATION_UUID --self --expected-generation 2 --json
```

Before its first submission, the CLI verifies the credential's server-derived
account and tenant identity, then exclusively publishes a private `0600` request
receipt and flushes it to disk. The receipt contains the API origin, tenant
identity, original definition, stable idempotency key and canonical input; it
contains no bearer credential. Treat its input as customer data. Keep it with the
operation and do not edit it.

On acceptance, the CLI separately publishes
`export-order-42.request.json.accepted.json`, binding the accepted ID and status
paths to the exact request receipt. Resuming a confirmed receipt verifies the
current credential's identity and returns that ID without another submission.
An unconfirmed receipt replays the same key, definition and input. Equivalent
JSON input is accepted; changed payloads, keys, definitions, API origins or tenant
identities are rejected. Public, corrupt or symlinked receipts are rejected, and
existing receipt files are never overwritten. Concurrent starts share the
published request and server idempotency key.

Unconfirmed replay is limited to the shortest enabled-plan idempotency window,
currently 30 days, so a plan downgrade cannot extend the CLI's safety assumption.
After that window, inspect retained work and reconcile an uncertain submission
before creating another request. A known acceptance still returns its original
ID after the window. HTTP admission remains governed by the existing preview
policy; these commands do not activate it.

## Inspect and reconcile as the owning account

```sh
gregale customer-operations list --app exports --scope production --limit 20 --json
gregale customer-operations list --app exports --scope production --tenant TENANT_UUID --state requires_reconciliation --json
gregale customer-operations get OPERATION_UUID --app exports --json
gregale customer-operations events OPERATION_UUID --app exports --after 0
gregale customer-operations executions OPERATION_UUID --app exports --limit 20
gregale customer-operations watch OPERATION_UUID --app exports --timeout 5m --json
gregale customer-operations download OPERATION_UUID --app exports --output export.csv
gregale customer-operations delivery OPERATION_UUID --app exports --json
gregale customer-operations delivery-attempts OPERATION_UUID --app exports --limit 20 --json
gregale customer-operations retry-delivery OPERATION_UUID --app exports \
  --delivery DELIVERY_UUID --expected-replay-generation 0 \
  --retry-id completion-1 --receipt-file ./completion-retry.json --json
# After any lost response, resume the exact request:
gregale customer-operations retry-delivery OPERATION_UUID --app exports \
  --receipt-file ./completion-retry.json --json
```

History requires an explicit environment. `--tenant`, `--name` and `--state`
are optional filters. The app comes from `--app`; account identity comes from
credentials. Pages contain at most 100 rows. Pass `next_cursor` back as `--cursor`
with the same selectors. Cursors cannot be reused across an account operator and
customer view. Summaries omit original input, results, artifact locations, runtime
capabilities and notification errors. Only operator summaries contain tenant IDs.

`events` returns a bounded JSON page. Continue from the last event sequence using
`--after`. If `resync_required` is true, fetch `get` and resume from its
`latest_sequence`. `executions` returns retained execution generations ordered
ascending. Pass `next_generation` as `--after` for the next page. `attempts` is
the execution ledger's aggregate attempt count; this endpoint does not invent
individual attempt outcomes. Both evidence commands emit JSON in either mode.

`watch` polls the durable status API and prints only changed snapshots. `--json`
emits one OperationResponse per line (NDJSON). Its exit code follows business
work: 0 for succeeded, 1 for failed/cancelled, 4 for requires_reconciliation.
A successful operation still exits 0 when its notification is pending or dead.
Local timeout exits 124 and Ctrl-C exits 130; neither cancels the operation.
Authentication failures use 2 and infrastructure errors use 3. Inspection and
mutation commands return 0 when their request succeeds, irrespective of the
business state in the response.

Downloads require a new `--output` path. With more than one artifact, select
`--artifact ARTIFACT_UUID`. The client refuses provider redirects, verifies the
HTTP digest, and compares the size and digest to retained operation metadata.
The CLI writes a private temporary file beside the destination, flushes it,
then atomically publishes it without overwriting any existing file or symlink.
Failures remove the temporary file and leave the destination untouched. The
JSON receipt contains operation_id, artifact_id, path, size_bytes and sha256.

Cancellation and recovery require an observed generation. Recovery additionally
requires a stable decision ID and nonempty evidence read from a bounded, regular
UTF-8 file. Evidence and result inputs reject symlinks. Store this ID with the
operator's decision so an uncertain response can
be retried with exactly the same request. A changed payload under the same ID
conflicts. A new decision against a stale generation conflicts.

```sh
gregale customer-operations cancel OPERATION_UUID --app exports --expected-generation 2 --json
gregale customer-operations recover OPERATION_UUID --app exports \
  --expected-generation 2 --recovery-id provider-check-42 \
  --resolution succeeded --evidence-file evidence.txt --result-file result.json --json
```

The backend validates a succeeded result against the original output schema.
Other resolutions are failed, cancelled and safe_to_retry. Only safe_to_retry
creates a fresh execution generation, using the retained original input and
pinned deployment/release. Operators must first establish that repeating the
work is safe. The CLI never chooses a recovery resolution automatically.

`delivery` reports the retained business state separately from notification state,
replay generation, response status, next attempt, and receiver cooldown. It makes
no receiver probe. `delivery-attempts` pages the actual webhook attempt ledger,
newest replay/attempt first. Its bounded cursor is tied to the operation and its
completion delivery. Both reads omit raw errors, destination URLs and payloads.

A new `retry-delivery` decision requires the observed delivery UUID and replay
generation (including explicit zero), a stable retry ID, and a private receipt
file. The CLI requires an admin credential or both account read and deploy-write
scopes because it reads identity and delivery before mutation. It verifies the
API origin and current account, exclusively publishes
a 0600 request and fsyncs it before any mutation, then saves a separate
`.queued.json` acknowledgement. Resume with the same receipt file after a lost
reply; omit all new-decision selectors. Conflicting flags, account/origin changes,
public or symlink receipts, invalid acknowledgements and expired unconfirmed
receipts cause no mutation. A cached acknowledgement is an original decision,
not a claim about the delivery's current status.

The new receipt endpoint atomically records the decision and resets only its
observed dead delivery. Exact retries return the original receipt, even if later
dispatches fail again or the shorter-lived transport ledger has been cleaned up.
Different payloads under the same retry ID conflict; distinct IDs observing the
same replay generation have one winner. There are at most 32 decisions per
operation. Receipts expire with operation retention and cascade on owner/operation
deletion; retry IDs cannot be reused during that window. Notification dispatch
still follows subscription policy and receiver cooldown. Business state, result,
execution generation and invocation identity remain unchanged. The older
`/retry-delivery` API remains compatible but has no decision receipt; new CLI and
SDK integrations use `/delivery-retries`.

The account routes added by the operator and developer slices are:

| Method | Route | Authority |
|---|---|---|
| GET | /v1/apps/{slug}/deployments/{deployment_id}/operation-doctor | Account read and MFA |
| GET | /v1/apps/{slug}/deployments/{deployment_id}/operation-definitions | Account read and MFA |
| GET | /v1/apps/{slug}/deployments/{deployment_id}/operation-definitions/{name} | Account read and MFA |
| GET | /v1/apps/{slug}/operations | Account read and MFA |
| GET | /v1/apps/{slug}/operations/{id}/events | Account read and MFA |
| GET | /v1/apps/{slug}/operations/{id}/executions | Account read and MFA |
| GET | /v1/apps/{slug}/operations/{id}/delivery | Account read and MFA |
| GET | /v1/apps/{slug}/operations/{id}/delivery-attempts | Account read and MFA |
| POST | /v1/apps/{slug}/operations/{id}/delivery-retries | Account deploy write and MFA |
| POST | /v1/apps/{slug}/operations/{id}/retry-delivery | Legacy account deploy write and MFA |

Existing account get, download, cancel and recover routes supply the other
commands. Additive migrations supply the account history index and bounded
completion retry receipts. Existing execution recovery and webhook transport
policies remain authoritative.

`GET /v1/platform-tenant-self/customer-operations/identity` supplies only the
server-derived account and tenant IDs for submission receipts. It requires the
manage scope, rejects owner selectors and uses `Cache-Control: no-store`.

## Resume a recovery decision after losing its response

Add `--receipt-file` when applying an explicit reconciliation decision:

```sh
gregale customer-operations recover OPERATION_UUID --app exports \
  --expected-generation 2 --recovery-id provider-check-42 \
  --resolution safe_to_retry --evidence-file ./provider-check.txt \
  --inspection-revision sha256:INSPECTION_HASH \
  --receipt-file ./export-recovery.json --json

# Resume with the exact saved request; original evidence/result files are unnecessary.
gregale customer-operations recover OPERATION_UUID --app exports \
  --receipt-file ./export-recovery.json --json

gregale customer-operations get OPERATION_UUID --app exports --json
```

Before sending a mutation, the CLI verifies the API/account and publishes a
private immutable request containing the exact evidence, result and selectors.
Keep this sensitive file private; it contains no credentials. Supplied resume
selectors must match. The CLI refuses public, symbolic-link, corrupted or
oversized receipts and expired unconfirmed requests.

The server retains the decision atomically with execution creation or native
workflow resume. A lost response can be retried with the same request without
recording another decision. `export-recovery.json.decided.json` stores its private
immutable acknowledgement, bound to the request file SHA-256. JSON output and
`state_at_decision` describe historical acceptance; use `get` for current business
and notification status. A saved acknowledgement can be read after its replay
window, with the same authenticated account and API, without another mutation.

The API is account-only `POST .../{id}/recover-receipt`, using
`OperationRecoveryRequest` and returning `OperationRecoveryDecision`. Go's
`RecoverOperationWithReceipt`, Node's generated `OperationsService.recoverOperationWithReceipt`
and Python's generated `recover_operation_with_receipt` expose the same contract.
The legacy `/recover` response remains current operation status. Pre-upgrade
accepted decisions without an immutable acknowledgement return
`409 operation_recovery_receipt_unavailable` on the receipt endpoint and remain
deduplicated; inspect work and preserve their original decision ID.

Receipt replay expires at the operation retention deadline observed before the
decision. Later recovery does not extend that receipt. An expired unconfirmed
request is never automatically replaced with another decision. Preview remains
read-only and rejects `--receipt-file`. See [ADR-662](../adr/662-operation-recovery-decision-receipts.md).

## Browser submission lookup

The opt-in Node feature session stores scoped metadata before submitting and
calls `POST /v1/platform-tenant-self/customer-operations/submissions/lookup` on
`resume()`. This read-only route requires `platform_tenant:operations:read` and
remains available while preview admission is closed. The bounded JSON body
contains `app_id`, explicit `scope`, `name` and `idempotency_key`, with optional
`expected_identity`. Authenticated account/customer determine ownership. It
rejects query parameters and uses `Cache-Control: no-store`.

`accepted` includes the original `accepted_at`, receipt and ledger horizon;
read status to see current business and notification state. `expired` identifies
known unusable retained acceptance. `unresolved` can mean admission is in flight
or the ledger was pruned, and must never be interpreted as proof of rejection.
No lookup creates an operation or execution. Browser retries reuse their frozen
key/definition and matching input, with a conservative one-day deadline.

Go exposes `LookupPlatformTenantSelfOperationSubmission`, Node exposes
`GregaleOperationClient.lookupSubmission` and generated
`OperationsService.lookupPlatformTenantSelfOperationSubmission`, and Python has
`faas_sdk.api.operations.lookup_platform_tenant_self_operation_submission`.
Optional principal and feature fences on the start request reject credential
or feature changes; they never authorize another owner. See
[ADR-663](../adr/663-browser-operation-submission-receipts.md).
