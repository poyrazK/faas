# Disposable isolated runs

Gregale Runs executes caller-supplied Node.js or Python source in a fresh
Firecracker microVM. The VM receives loopback-only networking, bounded CPU,
memory, scratch space, and output limits, then is destroyed before the result
becomes terminal. Runs never expose a customer-mounted disk or preserve guest
state between requests. A run may include a bounded multi-file source bundle;
the files are sealed with the request and materialized only in the guest's
ephemeral scratch filesystem.

See the [customer examples](../examples/gregale-run/README.md) for runnable CSV
reports, structured invoice checks, parallel task reports, failed-branch recovery
and an Agent report tool.

## CLI

```sh
gregale run --runtime node22 --file handler.js --input '{"url":"https://example.invalid"}' --wait
gregale run --runtime node22 --file handler.js --follow
gregale run --runtime node22 --file handler.js --follow --json
gregale run --runtime node22 --dir . --entrypoint src/index.mjs --wait
gregale sandbox list --status running --json
gregale sandbox list --workflow-id incident-42 --json
gregale sandbox workflow incident-42 --json
gregale sandbox workflow run --manifest incident.json --json
gregale sandbox workflow run --manifest incident.json --managed
gregale sandbox workflow run --manifest incident.json --dry-run --json
gregale sandbox capabilities --json
gregale sandbox status <execution-id>
gregale sandbox cancel <execution-id>
```

`--watch` implies `--wait` and follows the resumable execution event stream,
printing stdout/stderr as they arrive. If the connection drops, the CLI
reconnects from the last event ID without duplicating output. Combine it with
`--json` to emit one NDJSON object per event (`type`, `id`, and `data`); the
terminal event also includes the complete `receipt` for agent consumers.

`--input` accepts inline JSON, `@path.json`, or `-` for stdin. `--file` accepts
only a regular, non-symlink file. `--dir` walks regular files below the
directory (skipping `.git`), rejects symlinks and special files, and enforces a
256-file / 1 MiB source cap before upload. `--entrypoint` must be a normalized
relative path present in the bundle. Repeat `--artifact-input
RUN_ID:ARTIFACT_NAME=DESTINATION_PATH` with `--dir` to stage successful
same-account run artifacts in that bundle. Use `--json` for a machine-readable
receipt.

### Larger data files and standalone contracts

With extended admissions enabled, keep code and JSON small while streaming
larger data separately:

```sh
gregale run --runtime node24 --file process.mjs \
  --input-file data/input.csv=./large-input.csv \
  --artifact-storage stored --output-file report.csv \
  --max-artifact-bytes 8388608 \
  --input-schema input.schema.json --result-schema result.schema.json \
  --idempotency-key report-42 --output-dir ./results --json
```

`--input-file` is repeatable, up to eight normalized relative paths. The
Node handler reads `context.input_dir + '/data/input.csv'`; Python uses
`context['input_dir']`. Source bundles and JSON input retain their 1 MiB caps.
Input and output directories live on the guest's bounded ephemeral drive.
No host directory, object-store URL, or storage credential reaches the guest.

| Plan | Maximum single file / total input / total stored output per Run | Retained account storage | File count |
| --- | ---: | ---: | ---: |
| Hobby | 8 MiB | 64 MiB | 32 |
| Pro | 32 MiB | 1 GiB | 256 |
| Scale | 64 MiB | 4 GiB | 1024 |

Files expire 24 hours after reservation. Inputs plus the output allowance must
fit the selected ephemeral disk with source, JSON and scratch headroom. The
default output allowance is reduced to available space; an explicit allowance
that does not fit is rejected. Transfers share the existing overall Run
deadline and do not extend it. Stored bytes have their own budget; result JSON,
logs and artifact metadata continue to share `max_output_bytes`.

Stored artifacts contain `file_id`, `size_bytes`, `sha256` and null `content`.
`gregale sandbox artifacts ID --output-dir DIR` streams authenticated downloads,
checks length and SHA-256 and removes an incomplete local file on failure.
Expiry is independent of VM destruction. Downloads and metadata follow the
same account and Runs-key-family boundary as receipts; file IDs are locators,
not bearer capabilities. Use `--input-file-id data/input.csv=FILE_UUID` to reuse retained data without
a local copy. API clients import stored outputs using `input_files` with a
`file_id`, or a single-use artifact `grant_token` for cross-family handoff.
The legacy `artifact_inputs` bundle mechanism remains for inline artifacts.

`input_schema` and `result_schema` accept local JSON Schema Draft 2020-12,
including boolean schemas, with a 64 KiB bound per schema and no remote
references. Invalid input is rejected before admission or quota consumption.
A successful execution whose result violates its schema retains its result,
artifacts, usage and `status: succeeded`, and adds
`result_validation: {status: invalid, message: "Run result does not match result_schema"}`.
CLI waits return nonzero, and workflow dependents stop on this contract failure.
Neither validation nor receipt recovery executes the code again.

Extended requests use `POST /v2/executions`; SDK helpers choose this route
when contracts or stored files are present. They never fall back to v1.
Retained-file API calls are `POST /v1/execution-files` (prepare only),
`PUT /v1/execution-files/{id}/content` (length/checksum verified upload),
`GET /v1/execution-files/{id}` and `GET /v1/execution-files/{id}/content`, and
`DELETE /v1/execution-files/{id}` (revoke future use and schedule cleanup).
Preparation derives a stable ID from account, key family, digest and size and
does not reserve quota. CLI submission first probes the same admission key;
it uploads only after an explicit `execution_files_not_ready` rejection and
then retries the unchanged request/key. An uncertain acknowledgement requires
receipt recovery using the original key. Cleanup releases account capacity only
after storage confirms deletion; active admitted inputs remain protected.

Operators apply the additive migrations and upgrade schedd, vmmd, guest images,
apid and the public/internal API proxies before setting
`FAAS_EXECUTION_V2_ENABLED=1` on apid. This separate
writer gate defaults off. Disable it before rollback, drain queued extended
Runs and retain compatible file readers, keys, storage and GC until all retained
files expire or are deleted. `extended_admissions` in Runs capabilities reports
the new admission gate. The feature remains preview.

### Retry a source Run submission

Give a logical submission a stable key when it may need to be retried:

```sh
gregale run --runtime python313 --profile python-data-v1 --file analyze.py --input @input.json \
  --idempotency-key customer-42-report-1 --wait --json
```

Reuse the key with the same source, input, dependency profile, limits and artifact
references, on the same API origin and account and with the same API-key family.
Keys contain 1–255 printable ASCII characters without spaces. On a fully
upgraded control plane, the API commits the key and Run atomically and retains
the binding for 24 hours from admission. A retry returns the same Run's current
receipt with `Idempotent-Replayed: true`, even after it finishes, quota fills,
or an artifact grant is consumed. Concurrent retries wait for the admission
transaction. Changing the request while reusing a live key returns
`422 idempotency_key_reused`.
Choose a new key for a new logical task. Without this flag, each CLI invocation
gets a new SDK-generated key, even when the source is identical.

If submission ends without a readable receipt, retry the same request and key
within that window to recover its receipt. Keep the source, input and artifact
references unchanged. After expiry, the same key can create another Run.
Requests admitted before atomic admission may return `409` for a legacy key;
inspect `gregale sandbox list` for their existing receipt before submitting new
work. An empty list does not establish that the original request failed.
Idempotent admission does not guarantee exactly-once execution or external
side effects. This option applies to source Runs; image runs have a separate
submission and interactive-attachment contract.

Operators must apply the additive migration and upgrade every source-admitting
apid replica before relying on atomic replay. Replicas must share the same
host HMAC key; a rotation that removes the original key fails retries closed
until that key is restored or the binding expires. See the
[admission ADR](adr/20261113010000-atomic-source-run-admission.md) for cutover and
rollback requirements.

With `--wait` or `--follow`, a failed, timed-out, out-of-memory or cancelled Run
returns a nonzero CLI exit code, including with `--json`. A queued receipt from
a command without either waiting option confirms admission only.

Agents can call `gregale sandbox capabilities --json` before submitting work. The
response reports the account's Runs entitlement, accepted runtimes and
profiles, network mode, plan limits, and fixed request caps. It lets an agent
reject or resize work locally before upload. `admission_available` means the
account is entitled and the apid admission gate is enabled; it does not promise
that schedd dispatch or a requested profile image is ready at that moment.

## API

The API requires a Bearer API key. A key granted only `runs:read` and/or
`runs:write` sees runs created by that key family. Separate agent keys in the
same account therefore have independent receipts, event streams, cancellation
rights, and artifact handoff. Existing broad keys with `admin`, `apps:read`, or
`deploy:write` retain account-wide access; dashboard sessions remain account
wide as well.

* `POST /v1/executions` — admit a run and return a queued receipt; requires `runs:write`, `deploy:write`, or `admin`.
* `GET /v1/executions/capabilities` — read the account's Runs admission contract and request limits; requires `runs:read`, `runs:write`, `apps:read`, or `admin`.
* `GET /v1/executions` — list receipts visible to the caller with `limit`, `offset`, and optional `status` filters; requires `runs:read`, `runs:write`, `apps:read`, or `admin`.
* `GET /v1/executions?workflow_id=...` — list receipts in one workflow, with the same visibility and pagination rules.
* `POST /v1/execution-workflows` — store an encrypted sequential plan and continue admitting Runs after the client disconnects; requires `runs:write`, `deploy:write`, or `admin`.
* `GET /v1/execution-workflows/{workflow_id}` — aggregate lifecycle counts and terminal-run usage, plus server-managed continuation status when present.
* `GET /v1/executions/{id}` — read the current or terminal receipt; requires `runs:read`, `runs:write`, `apps:read`, or `admin`.
* `GET /v1/executions/{id}/events` — stream ordered status/output events over SSE; reconnect with `after` or `Last-Event-ID`; requires `runs:read`, `runs:write`, `apps:read`, or `admin`.
* `DELETE /v1/executions/{id}` — request idempotent cancellation; requires `runs:write`, `deploy:write`, or `admin`.

`runs:write` includes Reads, so an agent can create work and inspect its
receipt and event stream using one narrow key. The stable owner identity belongs
to the API-key family and survives key rotation. Runs created before ownership
tracking have no agent owner; only broad account principals can see them.
Creating a new API key creates a new agent boundary, even when it receives the
same Runs scopes as another key.

An orchestrator can set `workflow_id` and an optional `step_label` on each
`POST /v1/executions` request. The workflow ID is a caller-generated grouping
value (1–96 ASCII letters, digits, periods, underscores, colons, or hyphens);
the step label is bounded to 128 UTF-8 bytes. The label requires a workflow ID.
Both values are control-plane metadata: the guest receives neither field, and
each execution still uses a new microVM and ephemeral scratch filesystem.

Workflow filters and summaries follow the same ownership boundary as run
receipts. If two agent keys happen to use the same workflow ID, each narrow key
sees only its own runs and usage. Sharing an artifact through a one-time grant
does not expose the producer's workflow metadata. Broad account credentials
can aggregate all matching runs. The workflow summary reports counts for every
lifecycle state; its wall time, CPU time, output bytes, and maximum per-run peak
memory cover terminal runs only.

`gregale sandbox workflow run --manifest PLAN.json` is a restartable runner for
agent-owned plans. Add `--dry-run` to validate the plan and preview its current
receipt state, dependency readiness, account concurrency cap, and available
parallel slots without creating Runs. Step states distinguish dependency-ready
work, waiting dependencies, in-progress receipts, failed contracts, blocked
dependents, and steps withheld by fail-fast policy. The preview reads workflow
receipts and (when new steps can be admitted) current Runs capabilities. It
does not reserve quota; admission and limits may change before the actual run,
which repeats preflight. The command runs steps sequentially by default; set
`max_parallel_steps` to opt into bounded fan-out. The runner caps that value at
the account's current `MaxConcurrentRuns` limit, so an agent can run independent
work concurrently without bypassing its account quota. Each step remains an
ordinary Run with its own fresh, disposable VM and namespaced step label;
Gregale persists its receipt, event log, result, and artifacts as usual. The
label contains a short digest of the complete manifest, including its version
and every step request, so changing a later step cannot silently reuse earlier
receipts. The control plane enforces one `gwf:` step receipt per workflow and
API-key family; if two copies of the same manifest resume at once, the runner
adopts the receipt created by the other copy. Before creating Runs, the CLI
checks every pending step against the account's current runtime, profile,
network, and statically knowable request limits. The API checks again at
admission because capabilities can change after preflight, and artifact size
and ownership depend on the producer's stored receipt.
Each step may declare a `result_schema` to validate its JSON output locally
before the runner admits any dependent step. A mismatch is reported in
`result_contract_failures`; the Run receipt remains visible with its original
Run status, and dependents treat the output as `contract_failed`. Schemas use
JSON Schema Draft 2020-12, may use local fragment references, and cannot fetch
external resources. A schema is limited to 64 KiB and all schemas in one
manifest to 1 MiB.
Later steps can consume the previous JSON result or explicitly named artifacts
from earlier successful steps. If a step uses artifact inputs with inline
`source`, the runner wraps that source as a one-file ephemeral bundle. If the
CLI exits or the machine restarts, rerun the same manifest and workflow ID:
completed steps are reused, in-flight steps are watched to terminal state, and
later steps continue only after their dependencies succeed. By default, a
failed Run stops admission of new steps while already-started Runs settle. Set
`failure_policy` to `continue_independent` to keep admitting independent
branches after a Run reaches a failed, timed-out, or canceled state. Ordinary
dependents of an unsuccessful step are reported in `blocked_steps`. A step
using `include_dependency_results` waits until all direct dependencies settle
and receives their statuses, so it can summarize partial results; a failed Run
or result contract still makes the overall workflow incomplete and the command
exits with an error. Artifact handoffs still require a successful producer.
Control-plane, submission, and local handoff errors keep the runner fail-fast.
The runner does not retry terminal failures or cancel a Run when local waiting
is interrupted, so it will not silently repeat a side effect. In default local
mode, the manifest remains with the caller and must be supplied again; it does
not create a server-side workflow definition. JSON output includes a `plan_id`
digest, a `complete` flag, result contract failures, any blocked step labels,
and Run receipts if local waiting is interrupted. Use a new `workflow_id` when
you want to run an edited manifest.

Add `--managed` to submit a bounded DAG to the control plane. Gregale stores
the plan encrypted with the host age identity, admits ready steps up to
`max_parallel_steps` and the account's concurrent Run limit, and resumes from
Run receipts after apid restarts or CLI disconnects. Every step still runs in
its own fresh, disposable VM. `depends_on` declares prerequisites;
`input_from_previous_result` passes the preceding successful Run's JSON result,
and `include_dependency_results` passes a status/result object for every direct
dependency to a fan-in step. `failure_policy: fail_fast` stops new admissions
after a failed Run while already-admitted Runs settle. With
`continue_independent`, Gregale can keep running unrelated branches, and a
fan-in step that includes dependency results can inspect failed or blocked
dependency statuses. The manifest is erased from the workflow job at terminal
state. A step can also declare `artifact_inputs` from an earlier step's
`output_files`; Gregale adds that producer as a dependency and stages the
verified artifact in the consumer's fresh files bundle. The CLI converts
inline source to an entrypoint bundle when needed. Direct API requests with
artifact inputs must provide `request.entrypoint` and `request.files`. Artifact
bytes remain in ordinary bounded Run receipts and are copied into the next
Run's sealed request; guest filesystems remain disposable, with no persistent
disk. `gregale sandbox workflow <workflow-id>` reports managed status and the
number of admitted steps alongside normal Run counts. Managed steps can declare
the same bounded `result_schema` contract as local workflows. The control plane
checks each successful Run result before admitting dependents and retains the
original Run receipt status. A mismatch appears as `contract_failed` in fan-in
input, omits that dependency's result, blocks ordinary dependents, and fails the
overall workflow. With `continue_independent`, unrelated branches and fan-in
steps that include dependency results can still proceed. Managed schemas use
Draft 2020-12 and local fragment references; external resources are not loaded.
Each account can have 16 active managed workflows; a terminal workflow
releases its queue slot.

The manifest supports up to 16 steps. `max_parallel_steps` accepts 1 through 8
and defaults to 1 for existing manifests. `failure_policy` accepts `fail_fast`
(the default) or `continue_independent`. Dependencies must name earlier steps,
which keeps the manifest topologically ordered. `depends_on` declares
dependencies explicitly; previous-result and artifact handoffs also add their
producer as a dependency. `include_dependency_results` passes one object keyed
by direct dependency label. Each value contains `status` and, when the Run
succeeded with valid JSON output, `result`. For example, a failed Run is
`{"status":"failed"}`, a blocked step is `{"status":"blocked"}`, and a
successful dependency can be `{"status":"succeeded","result":{"count":12}}`.
A successful Run whose result violates its declared schema is reported as
`{"status":"contract_failed"}` and does not pass a result downstream. With
`continue_independent`, this lets an aggregator inspect partial results after
every dependency settles. An artifact input still requires its producer to
succeed. The runner uses receipts and result or artifact handoffs between
fresh Runs; it does not provide persistent disks or a shared guest filesystem.

```json
{
  "workflow_id": "incident-43",
  "version": "v1",
  "max_parallel_steps": 4,
  "failure_policy": "continue_independent",
  "steps": [
    {
      "label": "logs",
      "result_schema": {
        "type": "object",
        "required": ["count"],
        "properties": {"count": {"type": "integer"}}
      },
      "request": {"runtime": "python313", "source": "def main(input, context): return {'count': 12}"}
    },
    {
      "label": "metrics",
      "request": {"runtime": "python313", "source": "def main(input, context): return {'errors': 2}"}
    },
    {
      "label": "deployments",
      "request": {"runtime": "python313", "source": "def main(input, context): return {'recent': []}"}
    },
    {
      "label": "summarize",
      "depends_on": ["logs", "metrics", "deployments"],
      "include_dependency_results": true,
      "request": {"runtime": "python313", "source": "def main(input, context): return input"}
    }
  ]
}
```

```json
{
  "workflow_id": "incident-42",
  "version": "v1",
  "steps": [
    {
      "label": "collect",
      "request": {
        "runtime": "python313",
        "source": "def main(input, context): return {'items': []}",
        "output_files": ["evidence.json"]
      }
    },
    {
      "label": "analyze",
      "input_from_previous_result": true,
      "artifact_inputs": [
        {"from_step": "collect", "name": "evidence.json", "path": "input/evidence.json"}
      ],
      "request": {
        "runtime": "python313",
        "source": "def main(input, context): return input"
      }
    }
  ]
}
```

Step labels are unique within the plan. Its short `version` is namespaced into
the persisted step labels. Keep the manifest unchanged when resuming the same
workflow ID and version: the runner reuses matching step receipts and does not
compare source contents. Use a new workflow ID whenever an already-started plan
changes. Each step still gets its own normal Run limits, execution, result
budget, and billing.

```json
{
  "workflow_id": "incident-42",
  "step_label": "collect logs",
  "runtime": "python313",
  "source": "def main(input, context): return {'items': input}"
}
```

The response receipt echoes the workflow metadata. Use
`GET /v1/executions?workflow_id=incident-42` to inspect its visible run receipts
and `GET /v1/execution-workflows/incident-42` for aggregate counts and usage.

Successful submissions and accepted cancellation requests emit the account
audit events `execution.created` and `execution.cancel_requested`. For API-key
requests, the audit actor is `api:<key-id>` and the metadata includes the key
ID and sanitized label. The payload records the execution ID, runtime, and
normalized profile; it excludes source, input, result, stdout/stderr, artifact
bytes, and host details.

The event stream is backed by a bounded control-plane replay log. It does not
provide a persistent guest workspace: each run still uses a fresh microVM and
its ephemeral scratch filesystem is destroyed before terminal acknowledgement.

If a watcher falls behind the retained log, the stream emits a `gap` event
with `execution_id` and `through_sequence` before continuing at the oldest
retained event. The CLI warns about the missing output; JSON watchers receive
the gap as an event. Read the complete stored stdout/stderr from the terminal
receipt with `gregale sandbox get RUN_ID`. The receipt's `output_truncated`
describes the execution's output byte limit, independently of replay retention.

`GET /v1/executions/capabilities` is a read-only account-scoped preflight for
agents. Its profile matrix describes the API contract and declared package
set, while `limits` contains plan-specific resource bounds and fixed bundle and
artifact caps. `admission_available` combines plan entitlement with the apid
gate only; scheduler dispatcher and image deployment readiness remain runtime
conditions and must not be inferred from this response.

Output is persisted into that replay log while the guest is still running.
Schedulers negotiate the additive vmmd streaming transport when it is
available, append each bounded stdout/stderr chunk before forwarding the next
one, and finish with a metadata-only terminal event. Older vmmd nodes use the
unary exchange and retain the original terminal-output behavior, so a mixed
version fleet remains compatible. The stream is back-pressured by the event
store and inherits the execution deadline; it never requires a customer disk.

The v1 runtime set is `node22`, `node24`, `python312`, and `python313`.
`network.mode` is always `none`; dependency installation, secrets, environment
injection, and persistent volumes are intentionally not supported. Source
bundles contain regular file bytes only—there is no symlink, device, or host
path representation. Source and input are encrypted before durable admission
and are never returned by reads.

## Managed outbound integrations

A Run may name already-bound managed integrations in the optional
`integration_ids` request field. This list grants access only for that
Run; an app binding does not grant Run access. Each call is authorized against
the current Runs binding and integration policy. `network.mode` remains
`none`: the guest has no network interface, DNS, general egress, or provider
credentials. The trusted runtime wrapper exposes a temporary loopback helper
which sends only through Gregale's host broker and `outboundd`.

JavaScript can call:

```js
const response = await context.outbound.request(integrationId, {
  method: "GET",
  path: "/v1/issues?state=open",
});
const issues = JSON.parse(response.body);
```

Python uses the same request fields:

```python
response = context["outbound"]["request"](
    integration_id, method="GET", path="/v1/issues?state=open"
)
issues = json.loads(response["body"])
```

The helper accepts an integration ID, an allowed HTTP method, a relative
provider path, and an optional JSON body. It does not accept origins, caller
headers, credentials, or redirect targets. Responses contain status, a small
safe header set, and a text body. Request and response bodies are capped at
1 MiB each; the whole Run is capped at 128 calls and 8 MiB of broker frames.
These bytes exist only for the active Run and do not create a workspace or
persisted disk state.

## Preinstalled dependency profiles

Set `profile` in the API/SDK request or `--profile` on the CLI. Omission selects
`standard`, which retains the standard-library-only Python environment.
`python-data-v1` requires `python313` and provides NumPy 2.5.3, pandas 3.0.6,
python-dateutil 2.9.0.post0, and six 1.17.0. This versioned package set is built
into a shared read-only image; packages are available without downloads or
per-run installation. Source bundles can include local modules, but a run
cannot request arbitrary third-party dependencies.

For example, `analyze.py`:

```python
import os
import numpy as np
import pandas as pd

def main(input, context):
    frame = pd.DataFrame({"value": input})
    frame.to_csv(os.path.join(context["output_dir"], "report.csv"), index=False)
    return {"sum": int(frame.value.sum()), "mean": float(np.mean(input))}
```

```sh
gregale run --runtime python313 --profile python-data-v1 --file analyze.py \
  --input '[1,2,3]' --memory-mb 256 --timeout-ms 30000 \
  --output-file report.csv --output-dir ./results --json
```

The receipt returns `profile` and `packages`. Once the scheduler selects an
image it also returns `runtime_image_digest` (`sha256:` plus lowercase hex),
pinned before dispatch and preserved across restore retries. Package versions
are declared at admission and checked in the guest before caller code runs.
Unknown profiles, incompatible runtimes, and mismatched guest images fail
closed. Standard and data runs never share a snapshot identity. Every run
keeps its existing network, resource, output, and ephemeral-storage limits.

Operators must publish and validate the profile image and matching guest
artifacts before use. On imaged, set `FAAS_EXECUTION_PYTHON_DATA_V1_BASE_REF`
to the concrete linux/amd64 OCI manifest reference (`...@sha256:...`). It
opts into the existing verified staging path under
`base/runner-python-data-v1-amd64.ext4`; omission stages no data base. Use the
reported image configuration digest for `BASE_DIGEST` below, and publish a
separate payload-free execution layer preserving the data profile marker.
The scheduler reads all eight artifact fields from
`FAAS_EXECUTION_PYTHON313_PYTHON_DATA_V1_`: `ARCH`, `KERNEL_DIGEST`,
`EXECUTOR_DIGEST`, `BASE_DIGEST`, `KERNEL_KEY`, `BASE_KEY`, `LAYER_KEY`, and
`FC_VERSION`. Digests are lowercase SHA-256 hex without the `sha256:` prefix.
This namespace never falls back to plain `FAAS_EXECUTION_PYTHON313_*` values.
Dependency profiles require guest protocol v3 and native KVM/leak acceptance;
see [ADR-383](adr/383-curated-stateless-execution-profiles.md) for rollout.

## Usage accounting

`GET /v1/usage/summary` (and its `compute` projection in
`GET /v1/account/usage`) includes an `executions` object for the requested UTC
calendar month. It reports terminal run count, summed wall/CPU time, maximum
peak memory, output bytes, and counts by terminal outcome. These values come
from an execution-ID-keyed ledger written in the same transaction as
terminalization, so retries cannot double-count and deleting the sealed
payload does not erase usage history.

The control-plane and scheduler gates are explicit. Set
`FAAS_EXECUTION_API_ENABLED=1` on apid and `FAAS_EXECUTION_DISPATCH=1` on
schedd only after the host's restore/execute/destroy isolation checks pass.
When dispatch is enabled, schedd uses one worker by default. Set
`FAAS_SCHEDD_EXECUTION_DISPATCH_CONCURRENCY` to a value from 1 to 32 to raise
the bounded worker pool; each account's plan concurrency limit still applies,
and the scheduler rotates claims across accounts so one busy tenant cannot
monopolize the pool.

## Pass artifacts between runs

An agent can use `artifact_inputs` when submitting an entrypoint bundle to
stage up to eight successful-run artifacts in the new guest's ephemeral
bundle. A Runs-only key can directly reference runs from its own key family;
each reference names the source execution and artifact, plus the destination
path:

```json
{
  "runtime": "python313",
  "entrypoint": "analyze.py",
  "files": [{"path": "analyze.py", "content": "...base64..."}],
  "artifact_inputs": [{
    "execution_id": "<successful-run-id>",
    "name": "clean.csv",
    "path": "input/clean.csv"
  }]
}
```

Admission verifies the source receipt, success status, artifact checksum, and
the new run's plan byte limit before sealing the copied bytes with its request.
Broad account principals can directly reference any account artifact. For
least-privilege sharing between separate agent keys, the producer can create a
short-lived, single-use grant for one named artifact:

```http
POST /v1/executions/<producer-run-id>/artifact-grants
Content-Type: application/json

{"artifact_name":"clean.csv","expires_in_seconds":300}
```

The response contains a bearer `token` once. Share it with the intended agent
over the orchestrator's secure channel. The receiver supplies only that token
and its destination path; it cannot read the source receipt, events, other
artifacts, or cancel the producer's run:

```json
{"artifact_inputs":[{"grant_token":"rag_<one-time-token>","path":"input/clean.csv"}]}
```

Grants expire after 30 seconds to one hour (five minutes by default), can be
revoked before redemption with `DELETE /v1/execution-artifact-grants/<grant-id>`,
and are consumed atomically with the receiving run's admission. Concurrent
replays cannot redeem the same grant twice. The database stores only a hash of
the token. Grant creation, redemption, and revocation are audited without the
token or artifact bytes. Once redeemed, the new run has its own encrypted
request and ephemeral guest copy; revocation cannot remove that admitted copy.
Artifact handoff requires a bundle request with an `entrypoint`; source-string
requests can use a one-file bundle when they also need staged artifacts.

## Scheduler observability

The schedd `/metrics` registry exposes payload-free execution signals:

* `schedd_execution_active{runtime}` — claimed runs currently in restore or execution.
* `schedd_execution_total{runtime,status}` — runs durably acknowledged in a terminal state.
* `schedd_execution_phase_duration_seconds{runtime,phase}` — restore, execute, teardown, and finalize latency.
* `schedd_execution_failures_total{runtime,reason}` — bounded restore, transport, teardown, finalization, lease, protocol, and output-limit failures.
* `schedd_execution_output_bytes_total{runtime}` — result/stdout/stderr byte volume, without output content.
* `schedd_execution_sweeps_total{outcome}` — recovery-sweep success and error counts.
* `schedd_execution_queue_depth` — eligible queued runs awaiting dispatch.
* `schedd_execution_queue_oldest_wait_seconds` — age of the oldest eligible queued run.
* `schedd_execution_workers` — configured bounded dispatch-worker count.

Runtime, status, phase, and reason labels are closed sets. Execution IDs,
account IDs, source, input, guest output, and raw backend errors are not
exported in metrics or scheduler logs.

## Production smoke

After a release, run the operator smoke with an eligible account token:

```sh
FAAS_TOKEN=... make disposable-execution-release-smoke
```

The check first reads `GET /v1/capabilities`. When `disposable-runs` is
disabled, it verifies that admission fails closed with the documented 501
problem. When enabled, it submits a bounded Node 24 run and polls its receipt
until stdout contains the smoke marker. Set `GREGALE_API_URL` for a non-default
origin and `FAAS_EXECUTION_SMOKE_TIMEOUT_SECONDS` to change the polling limit.

## Export generated files

Declare the exact files to export with `output_files` (API/SDK) or repeat
`--output-file` on the CLI. Both runtimes expose `context.output_dir`: use
`context.output_dir` in Node and `context["output_dir"]` in Python. It points to
a fresh scratch directory for generated outputs, separate from staged inputs.

For example, `tool.py`:

```python
import os

def main(input, context):
    with open(os.path.join(context["output_dir"], "data.csv"), "w") as output:
        output.write("x,y\n1,2\n")
    with open(os.path.join(context["output_dir"], "patch.diff"), "w") as output:
        output.write("--- a/example\n+++ b/example\n")
    return {"rows": 1}
```

```sh
gregale run --runtime python313 --file tool.py \
  --output-file data.csv --output-file patch.diff --output-dir ./results
# --output-dir implies --wait; omit it to receive inline artifacts in JSON.
gregale sandbox artifacts <execution-id> --output-dir ./results-again
```

Up to eight unique normalized relative paths, each at most 256 UTF-8 bytes,
are allowed. No globs or automatic directory export occurs. Missing files,
symlinks (including parent directories), directories, and special files fail
the run with `artifact_invalid`; no partial artifact set is returned. Outputs
are collected only on success, before scratch removal and VM teardown.

The successful terminal receipt contains an optional `artifacts` array. Each
entry has `name`, `size_bytes` (raw file size), `sha256` (`sha256:` plus lowercase
hex), and `content` (base64). Result/stdout/stderr plus the compact JSON artifact
array must fit `limits.max_output_bytes`; encoded content and all artifact
metadata count toward the existing budget and usage output-byte total.
Artifacts use the same account-scoped receipt storage and lifecycle as other
terminal output. This feature retains neither a guest disk nor a guest session.

The CLI verifies integrity before saving and refuses to overwrite local files.
Node's `decodeExecutionArtifact(artifact)`, Python's
`decode_execution_artifact(artifact)`, and Go's `artifact.Bytes()` return verified
bytes without writing files. Use these helpers on artifacts from the final
receipt returned by the existing submit-and-watch SDK methods.

Exports require guest protocol v2 and rebuilt guest images/runtime snapshots.
Legacy runs retain v1. Older nodes reject export requests; a missing export is
never silently accepted as success. Native KVM isolation and leak checks must
pass before enabling this release's export path.

## Five-minute execution class

Source Runs can opt into `limits.execution_class: extended` through v2 admission.
The default and maximum deadline for that class is **300000 ms**, measured from
admission through queueing, restore, execution and output publication. Omit the
class to retain the existing five-second default and standard plan ceiling
(10 seconds on Hobby, 30 seconds on Pro/Scale). A longer class grants the same
memory, CPU, PID, scratch and output envelopes. Existing RAM-second usage applies.

Extended Runs share the account's total live Run cap and have an additional cap:
one on Hobby/Pro, two on Scale. A separate bounded scheduler lane reserves
standard worker capacity while extended Runs are active. Cancellation and teardown retain their existing
acknowledgement semantics. Submitting a class without the operator preview gate
returns an unavailable response; it never silently shortens the deadline.
Retrying an already admitted idempotency key still returns its original receipt
when the writer gate is disabled.

```sh
gregale run --runtime node24 --file transform.mjs \
  --execution-class extended --timeout-ms 300000 --wait --json
```

The CLI waits up to six minutes by default so the admitted deadline can settle.
`--wait-timeout` changes only the client's wait, and never extends or cancels the
server's Run. Capabilities expose each class's resolved duration, concurrency
and current admission switch.

## Dependency environments

Build an immutable dependency environment once, then select its ID on later
source Runs. Each Run receives a fresh writable scratch filesystem. Environment
reuse preserves installed dependencies; data and runtime mutations remain local
to the individual Run.

```sh
gregale sandbox environment publish --runtime python313 \
  --requirements requirements.lock --wait --json

gregale run --runtime python313 --environment ENVIRONMENT_UUID \
  --execution-class extended --file analyze.py --wait --json
```

Python requirements must contain exact `name==version` pins and SHA-256 hashes
for all transitive dependencies. Only binary wheels are installed. Requirement
URLs, editable installs, index options and source distributions are rejected.
Dependencies live in an explicit import path; package startup hooks are not run.

For npm, pass the dependency manifest and a matching lockfile v3:

```sh
gregale sandbox environment publish --runtime node24 \
  --package-json package.json --package-lock package-lock.json --wait --json
```

Every package must resolve from the public npm registry with SHA-512 integrity.
Install scripts are disabled. Workspaces, registry overrides, URL dependencies
and private registry credentials are excluded from this preview. Packages that
need installation scripts require a different environment preparation path.
Supported runtimes depend on the installer images published by the operator;
building an environment never runs a package manager on the Gregale host.

Environment requests contain dependency metadata only. They accept no custom
Dockerfile, shell command, secret or base-image reference. The control plane pins
the complete recipe, format and operator-selected installer image. Matching
recipes reuse the same environment within the same account and key family.
Changing dependencies or the installer image produces a new environment.
Before readiness, imaged retains the verified OCI chain, actual consumed-layer
proof, exact artifact identity and a complete vulnerability scan; a fixable
critical finding or unavailable scan refuses publication. The guest checks the immutable selection before starting customer code, and
Run receipts include `environment_id` and the selected `runtime_image_digest`.
Custom environments use the standard profile; they cannot be combined with
`python-data-v1`.

The API provides `POST/GET /v1/execution-environments` and
`GET/DELETE /v1/execution-environments/{id}`. Poll a build until `ready` before
selecting it on `POST /v2/executions`. Reads return metadata, without manifests,
host paths, storage keys or credentials. Runs-only keys see their own family;
account-wide credentials retain account-wide access. The Go preview client and
the generated Node/Python preview Runs APIs expose the same operations.

```sh
gregale run environments list
gregale run environments get ENVIRONMENT_UUID
gregale run environments delete ENVIRONMENT_UUID
```

Retirement immediately blocks new selections. Previously admitted Runs retain
their pinned environment until completion. Physical deletion waits for active
Runs and build/image leases, and the environment counts toward quota until GC
acknowledges deletion. Ownership survives account removal so cleanup can finish.
Hobby/Pro/Scale can retain 2/5/10 environments, with a final base-plus-layer
allowance of 256/512/1024 MiB per environment. Recipes share a combined 1 MiB
limit and at most 2048 dependencies. Builders use the existing isolated build
slot and 15-minute timeout; imaging has a five-minute host deadline.

New writers default off. Apply migrations and upgrade all readers, workers,
vmmd and protocol-v5 guest artifacts first. Then configure digest-pinned
`FAAS_EXECUTION_ENVIRONMENT_<RUNTIME>_BASE_REF` installer images on apid and enable
`FAAS_EXECUTION_V2_ENABLED=1`, plus the desired feature gates:
`FAAS_EXECUTION_EXTENDED_ENABLED=1` on apid and
`FAAS_EXECUTION_ENVIRONMENTS_ENABLED=1` on apid, builderd and imaged. The installer
image must match the selected runtime/architecture and include npm or pip.
Operator image recipes are in `images/execution-environment-*.Dockerfile`.
Pass each base as an image and a digest: `RUNTIME_BASE_IMAGE` and
`RUNTIME_BASE_DIGEST` (`sha256:...`), plus `NODE_TOOLS_IMAGE` and
`NODE_TOOLS_DIGEST` for Node. A build without a digest fails.
Local deployments also need `/srv/fc/run-environments` owned by `root:faas`, mode
0770; the compute bootstrap and generated unit permissions include it. Shared
artifact backends carry the same immutable objects across nodes.

Disable new writers to roll back, drain admitted v5 work, and retain compatible
readers and imaged GC. Publication and native production eligibility remain
separate qualification gates.

### System package environments

Environment recipes may add `system_packages` containing up to 32 exact Wolfi
`NAME=VERSION-rN` pins. Use `gregale sandbox environment publish --runtime python313
--system-package poppler-utils=26.08.0-r0 --wait` with a compatible installer.
Repeat the flag for additional packages. A packages-only recipe needs no Python
requirements or npm manifest. Existing locked language dependencies may also be
included. The combined recipe and produced image keep their existing limits.

Builds use the installer's configured repositories and verify that the selected
Node/Python runtime still matches after installation. Package changes produce a
new immutable environment. The produced image must pass the existing provenance
and vulnerability checks before use. See the [PDF report example](../examples/gregale-run/pdf-report/README.md)
for a complete task and SDK invocation.

## Build diagnostics

Follow package-manager output and build, export, verification, scan and publication
progress while preparing an environment:

```sh
gregale run environments logs ENVIRONMENT_UUID --follow
gregale run environments logs ENVIRONMENT_UUID --after 42 --follow --json
```

`build --wait` shows progress on stderr in human-readable mode. With `--json`,
it preserves a single final receipt on stdout. A CLI talking to an older server
falls back to status polling when the diagnostics endpoint is unavailable.

`GET /v1/execution-environments/{id}/events?after=N` returns `events`,
`next_sequence`, `status`, and an optional `gap`. Events contain a durable
`sequence`, `stage`, `code`, `message`, worker `attempt`, and `created_at`.
Persist `next_sequence` and pass it as `after` to resume. Cursors must be
nonnegative and cannot exceed the current sequence; invalid cursors return 400.
The endpoint retains the latest 128 events, each at most 1024 UTF-8 bytes. A
`gap` means earlier events have left that tail. This is polling, with no SSE
connection to keep alive. Retirement GC erases the retained diagnostics.

The Go preview client exposes `ExecutionEnvironmentEvents` and
`WatchExecutionEnvironment`; Node preview exports `watchExecutionEnvironment`,
and Python preview provides `watch_execution_environment` and
`awatch_execution_environment`. Watchers resume cursors, report gaps, and stop
on a terminal environment status. Interrupting a watch leaves the build running.
Reads keep the same account and runs-only key-family boundaries as environment
receipts and remain available when new environment writers are disabled.

Only bounded BuildKit/package-manager lines enter customer diagnostics. Boot
output, known credential fields, URL user information/query parameters and host
paths are filtered.
Avoid secrets in dependency metadata or package names. Live output is best
effort: `log_gap` and `logs_unavailable` explain interruptions, and workers retain
a bounded final output tail. Actionable failure codes distinguish dependency
resolution, integrity mismatch, size limits, build timeout, unavailable builders,
unavailable scans and scan rejection. They never override publication checks.
To rebuild an unchanged failed recipe, retire its environment and wait for
collection before issuing the build again; matching recipes reuse the existing
environment record.

## Durable completion callbacks

An account administrator can subscribe an existing account receiver to
`execution.finished` while the Run API and V2 preview writers are enabled:

```sh
gregale webhook account add \
  --target-url https://agents.example.com/gregale/completions \
  --event execution.finished --delivery-format cloudevents
```

The CLI displays the generated signing secret once. Store it in the receiver's
secret manager. The event covers succeeded, failed, timed-out, out-of-memory and
cancelled source Runs, including queued cancellation and recovery. Run completion
and enqueueing commit together. Existing retry, signature, delivery-history,
dead-letter and manual-redrive APIs apply; retrying a delivery never creates a Run.
Pending deliveries continue when new Run writers are disabled. A subscription
receives future terminal transitions, without backfilling historical receipts.

The payload contains `event_id`, `execution_id`, `status`, `runtime`,
`finished_at`, `receipt_url`, and optional `environment_id`, `workflow_id` and
`result_validation_status`. It carries no source, input, result, logs or artifact
bytes. A `succeeded` Run with `result_validation_status: invalid` has a result
contract failure; check it before using the result. `receipt_url` is an
API-relative path; fetch it from your configured
Gregale API origin using credentials authorized for that Run, then retrieve
results and artifacts through their normal scoped APIs.

Verify the signature and timestamp against the raw body before acting. Deduplicate
`event_id`, which equals the Run ID and remains stable across retries and redrive.
Delivery is at least once, so persist that deduplication alongside your receiver's
effects. With CloudEvents, the payload is in `data`; with the default Gregale
JSON envelope, it is in `payload.data`. See
[receiver verification](webhook-receiver-verification.md) and
[account receiver operations](account-release-webhooks.md) for SDK verification,
secret rotation, retry policy and retention. Runs-only keys cannot administer
account receivers.

See [versioned Run tasks](run-tasks.md) for reusable code publication and stateless Agent continuation.

See [durable artifact delivery](run-artifact-delivery.md) to promote selected
outputs into app-owned storage and issue customer download capabilities after
temporary Run files or Agent callbacks expire.

See [Run controls](run-controls.md) for authenticated customer/agent attribution,
source/task admission allowances and payload-free invocation history.
