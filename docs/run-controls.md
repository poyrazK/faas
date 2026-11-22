# Run attribution, budgets and history

Run controls assign source Runs and published task invocations to customers and
agents, enforce admission allowances, and expose metadata-only history. A fresh
VM, execution receipt and existing usage ledger still belong to each Run.

## Configure a customer

An unrestricted account administrator or deploy/write credential creates
`customer-budget.json`:

```json
{
  "expected_revision": 0,
  "enabled": true,
  "limits": {
    "max_concurrent": 2,
    "max_invocations_per_day": 1000,
    "max_compute_mb_ms_per_day": 136000000
  }
}
```

```sh
gregale sandbox customer budget customer-42 --manifest customer-budget.json
gregale sandbox customer budget customer-42
```

Use opaque customer and agent references, up to 96 ASCII letters, digits,
periods, underscores, colons or hyphens. They are retained control-plane
metadata; keep personal data and secrets out of them. `expected_revision: 0`
creates a policy. Subsequent replacements require its current revision. An
explicit empty `limits` object adds no restriction beyond existing account and
plan limits; an explicit zero limit refuses new work. Every replacement must
include `expected_revision`, `enabled` and `limits`.

Set `enabled: false` using the current revision to stop new admissions. Already
admitted queued or running work can finish, publish artifacts and deliver Agent
continuations. Lowering a limit does not cancel work or reset usage. After an
uncertain configuration response, read the current policy before retrying;
preserve the original expected revision rather than incrementing it blindly.

## Bind an authenticated caller

Create `binding.json` and use the public API key ID from key management:

```json
{"expected_revision":0,"customer_ref":"customer-42","agent_ref":"report-agent"}
```

```sh
gregale sandbox binding controls api_key KEY_ID --manifest binding.json
gregale sandbox binding controls api_key KEY_ID
gregale sandbox binding controls agent_endpoint ENDPOINT_ID --manifest binding.json
```

API key bindings follow the existing stable key family through rotation. The
private family ID is never exposed. Omitted agent references preserve an
existing label, or initially default to the public key/endpoint ID with its
kind prefix. Endpoint binding requires an endpoint owned by the account.

The server assigns attribution from the authenticated binding. Runs-only keys
cannot select another customer or manage policies. Unrestricted account writers
may pass `customer_ref` on a source request or task invocation to select an
existing policy. A bound caller must still match its binding. The managed
`gregale_task` tool derives its customer from its endpoint binding and accepts
only its existing task name/version and JSON input arguments. Conflicting key
and endpoint bindings refuse admission.

```sh
gregale sandbox task call report@v1 --customer-ref customer-42 \
  --input '{"rows":3}' --idempotency-key report-123 --wait --json
```

For a bound Runs key, omit `--customer-ref`: attribution is automatic, including
on raw source Runs. Changing a binding never rewrites historical receipts.
`gregale run controls unbind api_key KEY_ID --expected-revision N` disables it
and retains a revision tombstone. Read that revision before reactivating it.

## Enforce a task allowance

```sh
gregale sandbox task budget report --manifest task-budget.json
gregale sandbox task budget report
```

The manifest has the same shape as a customer policy. Task limits cover every
version of that name; publish the task before configuring its policy. A Run
must fit both its customer and task policies. The same underlying usage enters
both allowance calculations and one account usage ledger entry.

Admission reserves `(memory_mb + 8) * timeout_ms` in compute MB-ms. For example,
128 MB with a 30,000 ms deadline reserves 4,080,000 MB-ms. A terminal receipt
with 100 ms of wall time settles that reservation to 13,600 MB-ms. Settlement
uses the coefficient captured at admission and is bounded by the reservation.
Queued cancellation settles compute to zero but retains its invocation count.
Missing terminal ledger evidence retains the reservation and concurrent slot.
These are execution planning allowances; monetary invoices and account spend
caps retain their existing contracts.

Daily invocation and compute allowances use the UTC day assigned by PostgreSQL
at admission. A Run completing after midnight settles against that original
day; its active slot remains occupied until terminal ledger evidence exists.
Idempotent admission replay returns the original Run before checking current
policies, quotas or bindings and does not reserve or count work twice.

Refusals return HTTP 429 with `code: run_budget_exceeded`, `limit`, `observed`
and a `run_budget` object naming `kind`, `ref` and `metric`. Attribution
conflicts return `run_attribution_denied`. Managed Agent budget refusals remain
unadmitted HTTP errors rather than terminal inbox results, so the same call may
retry after capacity or policy changes. Ordinary Run replay retains its existing
24-hour window; managed task callback identities retain their durable contract.

## Inspect history and alerts

```sh
gregale run history --customer-ref customer-42 --agent-ref report-agent \
  --task-name report --task-version 1 --status succeeded --json
gregale run history --since 2026-11-16T00:00:00Z --cursor NEXT_CURSOR --json
```

Pages contain at most 50 entries with task lineage, attribution, timestamps,
elapsed time, terminal usage, failure code, result-contract status and a receipt
link. They exclude source, input, results and logs. Reads preserve the existing
account/key-family receipt boundary. `since` is inclusive and `until` exclusive;
both use RFC 3339 timestamps. Keep filters unchanged while following a cursor.
Elapsed time covers admission through completion/current observation; separate
queue and startup timings are not introduced by this increment.

Budget reads include current UTC-day usage and durable alerts at 80% and 100%
of each configured limit. Subscribe an account webhook to
`execution.budget.threshold` for signed delivery through the existing retry,
dead-letter and redrive system. One alert is retained per policy/day/metric/
threshold. It includes reservations, so it describes admission pressure rather
than finalized monetary cost. Replaying a Run or completing it twice creates
no duplicate alert obligation. Raising a limit does not reset that day's
deduplication identity. New Run-event subscriptions require the API/V2 gates.

## API and SDK

| Action | Route |
| --- | --- |
| Read/replace policy | `GET` / `PUT /v1/run-controls/budgets/{customer|task}/{ref}` |
| Read/replace binding | `GET` / `PUT /v1/run-controls/bindings/{api_key|agent_endpoint}/{id}` |
| Disable binding | `DELETE /v1/run-controls/bindings/{kind}/{id}?expected_revision=N` |
| Invocation history | `GET /v1/run-history` |

Go's preview client exposes `GetRunBudget`, `SetRunBudget`,
`GetRunAttributionBinding`, `SetRunAttributionBinding`,
`DeleteRunAttributionBinding` and `ListRunHistory`. Node's preview client and
`RunsService` expose corresponding camelCase methods. Python's preview
`api.runs` modules provide typed sync and async functions. Run capabilities
include configuration availability, plan customer/binding caps and page size.

Hobby/Pro/Scale retain up to 25/250/2,500 customer policies and
100/1,000/10,000 bindings, including disabled records and tombstones. Free
cannot create customer policies. Task policies are bounded by the existing task
catalog. These controls cover source Runs and named tasks; OCI/Job Runs,
Sandbox sessions and model-token spending use their respective admission paths.

## Rollout and compatibility

Apply the sidecar tables and validated receiver expansion before compatible apid
writers. Complete the receiver constraint transition in the release contract
phase before enabling threshold subscriptions. New configuration
requires the existing Run API/V2 preview gates. Reads, removal of bindings and
disabling existing policies survive writer rollback; enforcement remains in
state admission even when configuration writers are off. Task admissions retain
their existing default-off task gate. No production flags are enabled here.

Once an account has configured controls, the database refuses an older
admission writer that cannot enforce the protocol. Compatible readers and
terminal writers can coexist. Roll back with a compatible admission writer or
disable new admissions; retain the sidecar tables and pending deliveries.
Accounting begins with reservations recorded by compatible writers. Finish
older untracked Runs before enabling the first task policy during an upgrade;
daily allowance totals cover recorded admissions. Existing receipt history
remains readable.
Functional KVM qualification and native production runtime eligibility remain
separate gates. See the [decision](adr/20261116090000-run-customer-attribution-and-budgets.md).

The [functional verification](ops/evidence/20261116-run-controls/README.md)
records the exact-source customer/task budget, Agent continuation, usage
settlement and history journey, including process restarts and physical cleanup.
