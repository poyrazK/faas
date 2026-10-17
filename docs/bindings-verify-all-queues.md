# All-binding verification and strict coverage

Verify an app's bindings, including eligible push queues on an exact deployment:

```sh
gregale bindings verify worker --all --deployment v12 --queue-publish --json
```

`--queue-publish` explicitly consents to one bounded safe message per eligible
queue binding. With `--all`, it requires `--deployment ID|vN`; the CLI confirms
that deployment and its scope through inventory before starting probes. Existing
service, PostgreSQL, storage, and outbound probes keep their behavior unless
explicitly opted into storage writes as described below. Queues use
the existing completion-canary runner and server admission checks, including the
deployment's declared safe handler capability. This command does not enable that
capability or deploy a handler.

Without `--queue-publish`, no queue messages are published. Eligible queues are
reported as unsupported with `queue_publish_consent_required`. Other omission
reasons include `queue_delivery_unsupported` for non-push queues,
`queue_handler_unsupported` for absent safe handler capability,
`queue_consumer_not_ready` for consumers not configured active,
`queue_binding_invalid`, and `queue_binding_ambiguous` for duplicate in-scope
binding identities. Disabled bindings and bindings outside the selected scope
are skipped. Shared legacy queues follow the existing queue scope rules.

The combined JSON report includes a `queue_report` receipt, per-binding status,
and any queue failure `reason` and `error`. A queue passes only after the runner
confirms handler completion for the exact binding, deployment, and scope.
Publishing or dispatching alone does not pass. Human output also shows queue
outcomes and receipt identities. Unsuccessful or interrupted canaries with
confirmed identities are cancelled with the existing bounded cleanup. Unconfirmed
receipt identities are rejected without issuing cancellation against an unverified resource; the batch
shares a single wait budget across all binding types. Later probes can remain
`not_checked` if that budget expires.

Coverage remains `partial` when unsupported or unchecked bindings remain, or
inventory reads are incomplete, and `none` when nothing was checked. As with the
existing batch command, exit 0 means the selected probes passed; it can accompany
partial coverage from unsupported bindings. Failed probes, incomplete inventory,
unknown binding types, unchecked work, or no selected probes return nonzero;
interruption returns 130. Inspect `coverage` and per-binding reasons in CI rather
than treating exit 0 as proof every configured binding was tested.

## Preview before execution

```sh
gregale bindings verify worker --all --deployment v12 --dry-run --json
```

`--all --dry-run` reads inventory and any selected outbound binding identities.
It uses the same selection and storage mode rules as execution and shows planned
probes, blocked bindings, exclusions, and required consent. A write-only binding
without storage consent shows its proposed upload/cleanup and missing flag;
a read/write binding without that flag keeps the existing read-only listing
probe. Disabled, out-of-scope, and release-only bindings remain excluded.

Consent flags can be supplied to review the corresponding full plan:

```sh
gregale bindings verify worker --all --deployment v12 --queue-publish --object-storage-write --require-complete --dry-run --json
```

The preview makes only read requests even with both consent flags. It creates no
tasks, queue messages, canaries, durable runs, or verification evidence. Consent
flags retain their normal deployment requirements, including an explicit
`--deployment` for queue publication or storage writes in `--all` mode.

JSON previews have a separate shape from execution results:

| Field | Meaning |
| --- | --- |
| `dry_run` | Always true for the preview |
| `plan_ready` | Whether known metadata allows the requested plan to run, assuming server admission and successful probes |
| `planned_coverage` | Prospective `complete`, `partial`, or `none` coverage under the supplied flags |
| `complete_coverage_possible` | Whether every eligible binding has a planned probe and inventory is complete without blocking issues |
| `plan` | Binding identities and scopes, `planned` / `blocked` / `excluded` status, action, probe mode, operations, and reason |
| `plan[].consent` | Required flag and whether it was supplied, where applicable |
| `coverage_blockers` | Known blockers using the strict gate's codes, identities, and reasons |
| `planned`, `blocked`, `excluded` | Counts of the corresponding plan steps |

Configured outbound request method, path, and expected status are included when
the policy is valid; provider origins and credentials are excluded. In default
mode, `plan_ready` may be true with partial planned coverage, matching execution's
existing treatment of known unsupported bindings. With `--require-complete`,
known coverage blockers make the plan unready. Inventory errors, unavailable
outbound identities, unknown binding types, and no planned work make either mode
unready. Exit 0 means plan-ready and exit 1 means unready; CLI/API failures remain
nonzero and can occur before a preview report is available. Human output shows
the same readiness, operations, consent, and blocker information.

Preview results contain no passed probe counts or verification gate result.
They describe a metadata snapshot, not actual verification. Later execution
reads fresh metadata and still needs server admission, successful probes, and
any required runtime checks; the preview does not reserve or bind that execution.
Dry-run remains supported with `--release-policy`; individual probe modes and
`--all --durable` remain unsupported combinations.

## Verify writable object storage

```sh
gregale bindings verify worker --all --deployment v12 --object-storage-write --queue-publish --require-complete --json
```

With `--all`, `--object-storage-write` requires an explicit `--deployment ID|vN`.
The CLI reads that deployment's inventory and uses the existing storage canary
runner and deployment-fenced task admission. It chooses the probe from each
in-scope binding's declared access:

| Access | Probe with storage write consent |
| --- | --- |
| `read` | Existing read-only listing probe; no object mutation |
| `write` | Tiny conditional upload and deletion of the owned probe object; no listing or download |
| `read_write` | Upload, verify the exact uploaded bytes, delete, and verify absence of the owned object/version |

Write-only cleanup confirms the provider acknowledged deletion; its permission
cannot verify absence through a read. The existing runner generates the private
probe key and limits operations to its owned disposable object/version. The
combined `object_storage_report` includes mode and upload, download, and cleanup
checks. Failed operations, missing or incompatible evidence, and cleanup failures
fail the probe and therefore the strict gate. An older guest returning only a
read-probe report cannot pass a requested write probe.

Without the flag, or with `--object-storage-write=false`, existing batch behavior
is preserved: read/write bindings receive read-only probes and write-only
bindings remain unsupported with `write_probe_requires_opt_in`. Storage consent
and queue publication consent are independent; neither flag grants the other.
Disabled and out-of-scope storage bindings remain excluded.

When write probes are enabled, invalid prefixes, unknown access modes, duplicate
in-scope storage prefixes, or unconfirmed deployment selection produce explicit
unsupported reasons (`object_storage_binding_invalid`,
`object_storage_access_unsupported`, `object_storage_binding_ambiguous`, or
`object_storage_deployment_required`) and no task for that binding. Server
admission remains authoritative if binding settings change after inventory was
read. A timeout or missing report fails verification and does not establish that
cleanup completed; the existing task execution and cleanup budgets still apply.

## Require complete coverage in CI

```sh
gregale bindings verify worker --all --deployment v12 --queue-publish --require-complete --json
```

`--require-complete` is available with `--all` only. It returns success only when
at least one binding is eligible, all eligible bindings were checked and passed,
and inventory is complete without error-level issues. It does not grant queue
publication or object-storage write consent. A disabled binding, an out-of-scope
binding, or a release-only PostgreSQL migration binding remains an explicit
exclusion and does not block an otherwise successful gate. Unknown exclusion
reasons fail closed. If every binding is excluded, the gate fails with
`no_eligible_bindings`.

In strict mode the report adds `gate` with `required: true`, `passed`, and a
`blockers` array (empty on success). Each blocker has a stable `code` and, when
applicable, binding `type`, `name`, `binding`, `scope`, and `reason`:

| Code | Meaning |
| --- | --- |
| `inventory_incomplete` | Some inventory metadata could not be read |
| `inventory_issue` | An inventory issue has non-warning severity; `reason` contains its issue code |
| `probe_failed` | A selected probe failed |
| `probe_not_checked` | Work was not checked; `reason` explains why |
| An omission reason such as `queue_publish_consent_required`, `queue_handler_unsupported`, `outbound_probe_unconfigured`, or `write_probe_requires_opt_in` | An eligible binding could not be verified |
| `unexpected_probe_exclusion` / `unrecognized_probe_status` | The result cannot establish that the binding was tested |
| `no_eligible_bindings` | No binding was eligible for verification |

Human output shows the same gate result and blocker codes. A failed gate returns
1; interruption keeps exit 130. Usage and API failures remain nonzero under the
existing CLI error handling, and may occur before a batch report is available.
The default mode and `--require-complete=false` keep their existing result and
output shape. Strict mode adds no rechecks or mutations beyond the probes
selected by the existing flags.

The gate describes the batch's selected scope and observed probe outcomes. It
does not create an atomic configuration snapshot, require guest application
acknowledgements, or authorize traffic promotion. Use the release-policy workflow
for deployment promotion and release-specific requirements.

For release-policy enforcement, durable recovery, or policy-driven queue
publication plans, continue using `--release-policy` and its existing
`--dry-run` / `--durable` workflow. `--all` is a sequential batch and does not acquire those guarantees.
