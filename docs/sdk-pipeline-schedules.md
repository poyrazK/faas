# Typed scheduled pipelines (preview)

Define a recurring native workflow schedule with a generated pipeline contract,
then inspect its calendar, read admission history, attach to typed runs, and
explicitly replay selected skipped occurrences from Node or Python.

The SDK emits the existing deployment `trigger` definition. Native scheduling
owns the clock, timezone/DST rules, quota, overlap checks, cursor and durable
admission. Schedule previews inspect **deployed** definitions; they do not admit
runs or preview an arbitrary unpublished configuration.

## Define and deploy

```ts
import {definePipelineSchedule} from '@gregale/sdk-node/preview';
import {contract} from './reviewed-pipeline-generated.js';

const schedule = definePipelineSchedule(contract, input, {
  scope, ownerId: 'alice', cron: '0 2 * * *', timezone: 'America/New_York',
  overlap: 'skip', catchUp: 'latest', catchUpWindowMinutes: 60,
});
// Include this workflow in your normal reviewed deployment:
const workflow = {
  name: contract.spec.name, trigger: schedule.trigger, steps: [],
  code: {path: '/csv-reviewed-pipeline', timeout: '30s'},
};
```

```python
from faas_sdk.preview import PipelineSchedule
from reviewed_pipeline_generated import contract

schedule = PipelineSchedule(contract, input, scope=scope, owner_id='alice',
    cron='0 2 * * *', timezone='America/New_York', overlap='skip',
    catch_up='latest', catch_up_window_minutes=60)
workflow = {'name': contract.spec['name'], 'trigger': schedule.trigger,
    'steps': [], 'code': {'path': '/csv-reviewed-pipeline', 'timeout': '30s'}}
```

Use the existing pipeline type generators and workflow handler. Update both
Node/Python handlers to this SDK version before deploying an input envelope with
`trigger.kind: schedule`. Existing manual and event callers retain their APIs.
Authenticate and authorize the owner before preparing a deployment.

Input is a **fixed validated snapshot**. The definition binds the owner,
endpoint/account/app, pipeline contract and calendar into a shared identity.
Changing any of them changes that identity. Python returns copies of trigger
JSON; Node freezes it. The helper checks five cron fields and a locally known
timezone; native deployment validation remains authoritative for full cron
syntax, available timezones and platform limits.

Defaults are UTC, skip overlap, skip missed fires and enabled. Latest catch-up
accepts a 1–1,440 minute window, default 60; skip rejects a window. Only the live
**default deployment** schedules work. Other environment values are rejected;
this API does not enable tenant-configurable schedules. Change cadence/input or
`enabled` through an ordinary reviewed deployment. Disabling does not cancel
already admitted runs. New deployments/changed triggers arm before firing.

A schedule does not produce a new import ID or source file each day. To process
changing data, schedule a coordinator pipeline with a typed selector, then resolve
and authorize its current work and invoke existing child pipelines. The CSV
example intentionally uses an immutable verified source snapshot.

## Inspect and attach

```ts
import {createPipelineScheduleClient} from '@gregale/sdk-node/preview';
const schedules = createPipelineScheduleClient(schedule, {
  deploymentId, token: backendToken,
  authorize: async (session, action, signal) =>
    currentOwnerPermission(session, schedule.ownerId, action, signal),
});
const calendar = await schedules.preview(session, {timeoutMs: 5000, count: 5, signal});
const page = await schedules.history(session, {timeoutMs: 5000, limit: 100, signal});
const run = await schedules.attach(page.occurrences[0].id, session, {timeoutMs: 5000, signal});
const output = await run.result({timeoutMs: 5000, signal}); // generated Output
```

```python
from faas_sdk.preview import PipelineScheduleClient

async def authorize(session, action):
    return await current_owner_permission(session, schedule.owner_id, action)

async with PipelineScheduleClient(schedule, deployment_id=deployment_id,
        token=backend_token, authorize=authorize) as schedules:
    calendar = await schedules.preview(session, timeout=5, count=5)
    page = await schedules.history(session, timeout=5, limit=100)
    run = await schedules.attach(page.occurrences[0].id, session, timeout=5)
    output = await run.result(timeout=5)  # generated Output
```

Every operation requires current application authorization returning exactly
`true`/`True`, then verifies the native account and app. All schedule operations verify
the pinned deployment is default-scoped and its workflow trigger/input match
the typed definition. Live inspection, preview and replay also require that
deployment to remain current. History and attachment continue to work after a
rollout while the pinned deployment and its history are retained. Construct a new
client for the reviewed new definition/deployment to manage its schedule. Older servers
without deployment workflow metadata cannot establish this binding.

Authorization actions are `inspect`, `preview`, `history`, `preview_replay`,
`replay`, `attach`, `read_run`, and `cancel_run`. Keep the backend token private.
Returned run handles reauthorize subsequent reads and cancellation using the
captured application context. Native read/write permissions remain mandatory.
Python supports `httpx_args` for an application transport; authority headers,
redirects and timeout configuration cannot override the client's binding.

Preview returns native UTC fires, offset-bearing local times, DST behavior and
catch-up decisions. Optional `at` and `since` support server-side what-if
inspection. Counts are bounded to 1–20; previews are advisory and reserve no
capacity. API runtime availability is not a scheduler heartbeat.

History returns one bounded native page (1–200 rows) filtered to the configured
workflow, deployment and account-owned runs. Continue using `next_cursor`, even
when a page contains no matching rows. An expired cursor can return no rows.
Dates and cursors retain their original strings; internal comparisons preserve
nanoseconds. Python's frozen metadata objects expose `to_dict()` without rounding
through datetime. History can retain a started occurrence after its run is
pruned; inspection still works and attachment reports `not_found`.

Attach accepts an occurrence ID, not an arbitrary workflow run ID. It verifies
app, deployment, workflow, nominal fire time and the exact fixed input before
returning a typed run. Use `replayed: true` / `replayed=True` to attach to a replay.
Cancellation stops orchestration; already accepted Jobs may continue.

## Controlled replay and uncertainty

```ts
const ids = selectedSkippedOccurrenceIds;
const eligibility = await schedules.previewReplay(ids, session, {timeoutMs: 5000});
// Review eligibility, then explicitly request these same IDs:
const outcomes = await schedules.replay(ids, session, {timeoutMs: 5000});
```

```python
eligibility = await schedules.preview_replay(ids, session, timeout=5)
outcomes = await schedules.replay(ids, session, timeout=5)
```

Select 1–20 unique occurrence IDs. The SDK checks they belong to this workflow,
deployment and account-owned history before sending a request. Discovery scans
at most five pages by default; `maxPages` / `max_pages` may bound it to 1–100.
Missing history or an exhausted scan stops before replay. Supply older retained
IDs only when the configured bounded scan can reach them.

The native replay transaction rechecks the live definition, deployment, schedule,
quota and overlap. It returns per-occurrence outcomes, such as `replayed`,
`already_replayed`, `quota_full`, `deployment_changed`, or `not_skipped`.
Outcomes retain chronological order, including instants within one millisecond.
The original skip is retained. This is explicit recovery of selected retained
occurrences, not automatic catch-up of every missed minute.

Each operation has one total deadline, including authorization, lookups and run
attachment. Node uses AbortSignal; Python cancels its asyncio task. Requests have
no SDK transport retry. Before dispatch, deadline/cancellation prevents mutation.
After replay dispatch, a lost reply, timeout, cancellation or invalid response
reports `PipelineScheduleError('replay_unknown')` with the selected
`occurrenceIds` / `occurrence_ids`. It never invents success or automatically
sends another replay. Inspect history, then attach with the replay flag when its
retained run ID is present. An explicit retry with the same IDs uses native
at-most-one replay admission; external workflow effects still require idempotency.

## Verification

Shared Node/Python fixtures cover input/calendar identity, timezone gap/fold
previews, catch-up, exact timestamps, scoped history, pruned runs, quota outcomes,
duplicate concurrent requests, lost replies, bounds, deadlines and cancellation.
Installed npm/wheel consumers verify generated input/output types.

The [nightly reviewed CSV flow](../examples/sdk-csv-jobs/README.md#scheduled-reviewed-imports)
uses real isolated PostgreSQL, competing admission fixture transactions, Node and
Python attachment to one run, mixed workers, private browser review and verified
report download. Native scheduling, control-plane and realtime are local protocol
fixtures; this does not qualify a deployed native scheduler or broker.
