# Disposable isolated runs

Gregale Runs executes caller-supplied Node.js or Python source in a fresh
Firecracker microVM. The VM receives loopback-only networking, bounded CPU,
memory, scratch space, and output limits, then is destroyed before the result
becomes terminal. Runs never expose a customer-mounted disk or preserve guest
state between requests. A run may include a bounded multi-file source bundle;
the files are sealed with the request and materialized only in the guest's
ephemeral scratch filesystem.

## CLI

```sh
gregale run --runtime node22 --file handler.js --input '{"url":"https://example.invalid"}' --wait
gregale run --runtime node22 --file handler.js --watch
gregale run --runtime node22 --file handler.js --watch --json
gregale run --runtime node22 --dir . --entrypoint src/index.mjs --wait
gregale runs list --status running --json
gregale runs list --workflow-id incident-42 --json
gregale runs workflow incident-42 --json
gregale runs workflow run --manifest incident.json --json
gregale runs capabilities --json
gregale runs status <execution-id>
gregale runs cancel <execution-id>
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

Agents can call `gregale runs capabilities --json` before submitting work. The
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
* `GET /v1/execution-workflows/{workflow_id}` — aggregate lifecycle counts and terminal-run usage for one visible workflow.
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

`gregale runs workflow run --manifest PLAN.json` is a restartable sequential
runner for agent-owned plans. Each step is an ordinary Run with a namespaced
step label; Gregale persists its receipt, event log, result, and artifacts as
usual. The label contains a short digest of the complete manifest, including
its version and every step request, so changing a later step cannot silently
reuse earlier receipts. The control plane enforces one `gwf:` step receipt per
workflow and API-key family; if two copies of the same manifest resume at once,
the runner adopts the receipt created by the other copy. Before creating Runs,
the CLI checks every pending step against the account's current runtime,
profile, network, and statically knowable request limits. The API checks again
at admission because capabilities can change after preflight, and artifact
size and ownership depend on the producer's stored receipt.
Later steps can consume the previous JSON result or explicitly named artifacts
from earlier successful steps. If a step uses artifact inputs with inline
`source`, the runner wraps that source as a one-file ephemeral bundle. If the
CLI exits or the machine restarts, rerun the same manifest and workflow ID:
completed steps are reused, in-flight steps are watched to terminal state, and
later steps continue only after success. A failed step stops the plan. The
runner does not retry a terminal failure or cancel a Run when local waiting is
interrupted, so it will not silently repeat a side effect. The manifest remains
with the caller and must be supplied again; this command does not create a
persistent guest workspace or a server-side workflow definition. JSON output
includes a `plan_id` digest, a `complete` flag, and retains the current Run
receipt if local waiting is interrupted. Use a new `workflow_id` when you want
to run an edited manifest.

```json
{
  "workflow_id": "incident-42",
  "version": "v1",
  "steps": [
    {
      "label": "collect",
      "request": {
        "runtime": "python313",
        "source": "print('collect evidence')",
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
        "source": "print(input)"
      }
    }
  ]
}
```

The manifest supports up to 16 sequential steps. Step labels are unique within
the plan. Its short `version` is namespaced into the persisted step labels.
Keep the manifest unchanged when resuming the same workflow ID and version: the
runner reuses matching step receipts and does not compare source contents. Use
a new workflow ID whenever an already-started plan changes. Each step still
gets its own normal Run limits, execution, result budget, and billing.

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
gregale runs artifacts <execution-id> --output-dir ./results-again
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
