import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { JobContract } from '../src/job-runtime.js';
import { definePipelineSchedule, createPipelineScheduleClient, type PipelineScheduleOptions } from '../src/pipeline-schedules.js';
const f = JSON.parse(readFileSync(new URL('../../../request-contracts/pipeline-schedules.json', import.meta.url), 'utf8'));
const contract = new JobContract<{
    import_id: string;
}, number>(f.contract), scope = f.scope, settings = { timeoutMs: 1000 };
function define(extra: Record<string, unknown> = {}) { const v = { ...f.options, ...extra }; return definePipelineSchedule(contract, f.input, { scope, ownerId: f.owner_id, cron: v.cron, timezone: v.timezone, catchUp: v.catch_up, catchUpWindowMinutes: v.catch_up_window_minutes, overlap: v.overlap, enabled: v.enabled, environment: v.environment } as PipelineScheduleOptions); }
function fixture(extra: Record<string, unknown> = {}) {
    const s = define(extra), records = structuredClone(f.occurrences), state = { calls: 0, posts: 0, authorized: true, lost: false, hang: false, foreign: false, badApp: false, badDefinition: false, changed: false, pages: false, refuse: false, malformed: false, denied: false, preview: structuredClone(f.previews[0].response) }, replays = new Map<string, string>();
    const fetch: typeof globalThis.fetch = async (url, init) => {
        state.calls++;
        assert.equal(new Headers(init?.headers).get('authorization'), 'Bearer platform');
        const u = new URL(String(url)), path = u.pathname;
        if (path === '/v1/account')
            return Response.json({ id: scope.account_id });
        if (path === '/v1/apps/imports')
            return Response.json({ id: state.badApp ? f.deployment_id : scope.app_id });
        if (path === '/v1/deployments/' + f.deployment_id)
            return Response.json({ id: f.deployment_id, app_id: scope.app_id, scope: 'default', workflows: [{ name: contract.spec.name, trigger: state.badDefinition ? { ...s.trigger, input: {} } : s.trigger }] });
        if (path.endsWith('/schedules'))
            return Response.json({ runtime_enabled: true, schedules: [{ workflow_name: contract.spec.name, deployment_id: state.changed ? f.run_id : f.deployment_id, ...s.trigger, schedule: s.trigger.schedule }] });
        if (path.endsWith('/preview'))
            return Response.json(state.preview);
        if (path.endsWith('/occurrences'))
            return Response.json({ occurrences: records.map((r: any) => ({ ...r, ...(replays.has(r.id) ? { replay_run_id: replays.get(r.id) } : {}), ...(state.foreign ? { app_id: f.deployment_id } : {}) })), ...(state.pages ? { next_cursor: f.replay_run_id } : {}) });
        if (path.includes('/occurrences:')) {
            const preview = path.endsWith('replay-preview');
            if (!preview)
                state.posts++;
            if(state.denied&&!preview)return Response.json({},{status:403});
            const ids = JSON.parse(String(init?.body)).occurrence_ids;
            const outcomes = ids.map((id: string) => records.find((r: any) => r.id === id)).sort((a: any, b: any) => a.scheduled_for.localeCompare(b.scheduled_for)).map((r: any) => {
                const outcome = state.refuse ? 'quota_full' : r.status === 'started' ? 'not_skipped' : replays.has(r.id) ? 'already_replayed' : preview ? 'eligible' : 'replayed';
                if (outcome === 'replayed')
                    replays.set(r.id, f.replay_run_id);
                return { occurrence_id: r.id, workflow_name: r.workflow_name, scheduled_for: r.scheduled_for, outcome, ...(replays.has(r.id) ? { replay_run_id: replays.get(r.id) } : {}) };
            });
            if (state.hang && !preview)
                return new Promise(() => { });
            if (state.lost && !preview)
                throw Error('private network diagnostics');
            return Response.json({ outcomes: state.malformed ? [] : outcomes });
        }
        if (path.startsWith('/v1/workflows/runs/')) {
            const id = path.split('/')[4];
            if (path.endsWith('/steps'))
                return Response.json({ steps: [] });
            const row = id === f.run_id ? records[0] : records.find((r: any) => replays.has(r.id));
            return Response.json({ id, app_id: scope.app_id, deployment_id: f.deployment_id, workflow_name: contract.spec.name, scheduled_for: row.scheduled_for, status: 'succeeded', input: s.trigger.input, output: { version: 1, scope, owner_id: f.owner_id, contract_sha256: contract.sha256, output: 17 } });
        }
        throw Error('unexpected fixture request ' + path);
    };
    const client = createPipelineScheduleClient(s, { deploymentId: f.deployment_id, token: 'platform', fetch, authorize: async (actor) => actor === 'alice' && state.authorized });
    return { s, client, state, records, replays, fetch };
}
test('shared identity and immutable trigger reject invalid definitions', () => { const s = define(); assert.equal(s.identity, f.identity); assert.deepEqual(s.trigger, f.trigger); assert.equal(s.trigger.input.trigger?.kind, 'schedule'); for (const bad of f.invalid_options)
    assert.throws(() => define(bad), { code: 'invalid_input' }); assert.throws(() => { (s.trigger.input as any).owner_id = 'bob'; }); });
for (const c of f.previews)
    test('native calendar preview: ' + c.name, async () => { const { client, state } = fixture(c.options); state.preview = structuredClone(c.response); assert.deepEqual(await client.preview('alice', { ...settings, count: 5, at: c.response.observed_at }), c.response); assert.equal(state.posts, 0); });
test('history keeps nanoseconds, filters unrelated tenants/workflows and handles pruned runs', async () => { const { client, records, state } = fixture(); delete records[0].run_id; records.push({ ...records[2], id: f.deployment_id, workflow_name: 'other' }); records.push({ ...records[2], id: f.run_id, platform_tenant_id: f.replay_run_id }); const page = await client.history('alice', settings); assert.equal(page.occurrences.length, 3); assert.equal(page.occurrences[0]!.scheduled_for, f.occurrences[0].scheduled_for); await assert.rejects(client.attach(records[0].id, 'alice', settings), { code: 'not_found' }); assert.equal(state.posts, 0); });
test('read-only previews, duplicate concurrent replay, quota outcomes', async () => { const { s, client, state, fetch } = fixture(), id = f.occurrences[1].id; assert.equal((await client.previewReplay([id], 'alice', settings))[0]!.outcome, 'eligible'); assert.equal(state.posts, 0); const other = createPipelineScheduleClient(s, { deploymentId: f.deployment_id, token: 'platform', fetch, authorize: async (a) => a === 'alice' }); const outcomes = await Promise.all([client.replay([id], 'alice', settings), other.replay([id], 'alice', settings)]); assert.deepEqual(outcomes.map(v => v[0]!.outcome).sort(), ['already_replayed', 'replayed']); state.refuse = true; assert.equal((await client.replay([f.occurrences[2].id], 'alice', settings))[0]!.outcome, 'quota_full'); });
test('replay preserves adjacent nanosecond ordering', async () => { const { client } = fixture(), ids = [f.occurrences[1].id, f.occurrences[2].id]; assert.deepEqual((await client.previewReplay(ids, 'alice', settings)).map(r => r.occurrence_id), [...ids].reverse()); });
test('authorization and immutable scope/deployment/definition precede mutations', async () => { for (const mode of ['unauthorized', 'scope', 'deployment', 'definition'] as const) {
    const { client, state } = fixture();
    if (mode === 'unauthorized')
        state.authorized = false;
    if (mode === 'scope')
        state.badApp = true;
    if (mode === 'deployment')
        state.changed = true;
    if (mode === 'definition')
        state.badDefinition = true;
    await assert.rejects(client.replay([f.occurrences[1].id], 'alice', settings), { code: { unauthorized: 'unauthorized', scope: 'scope_mismatch', deployment: 'deployment_changed', definition: 'definition_changed' }[mode] });
    assert.equal(state.posts, 0);
    if (mode === 'unauthorized')
        assert.equal(state.calls, 0);
} });
test('selection and history scan limits bound backfills', async () => { const { client, state } = fixture(); assert.throws(() => client.replay(Array(21).fill(f.run_id), 'alice', settings), { code: 'invalid_input' }); assert.throws(() => client.replay([f.run_id, f.run_id], 'alice', settings), { code: 'invalid_input' }); state.pages = true; await assert.rejects(client.replay([f.run_id], 'alice', { ...settings, maxPages: 1 }), { code: 'history_limit' }); assert.equal(state.posts, 0); });
test('lost, malformed or timed out replay replies retain IDs and never auto-retry', async () => { for (const mode of ['lost', 'malformed', 'deadline']) {
    const { client, state } = fixture();
    state.lost = mode === 'lost';
    state.malformed = mode === 'malformed';
    state.hang = mode === 'deadline';
    const id = f.occurrences[1].id;
    await assert.rejects(client.replay([id], 'alice', { timeoutMs: mode === 'deadline' ? 20 : 1000 }), (e: any) => e.code === 'replay_unknown' && e.occurrenceIds[0] === id && !e.message.includes('private'));
    assert.equal(state.posts, 1);
    state.lost = false;
    state.malformed = false;
    state.hang = false;
    assert.equal((await client.history('alice', settings)).occurrences[1]!.replay_run_id, f.replay_run_id);
    assert.equal(await (await client.attach(id, 'alice', { ...settings, replayed: true })).result(settings), 17);
} });
test('typed run reads and cancellation reauthorize', async () => { const { client, state } = fixture(); const run = await client.attach(f.occurrences[0].id, 'alice', settings); assert.equal(await run.result(settings), 17); state.authorized = false; const before = state.calls; await assert.rejects(run.result(settings), { code: 'unauthorized' }); await assert.rejects(run.cancel(settings), { code: 'unauthorized' }); assert.equal(state.calls, before); });
test('deadline and cancellation fence late authorization before I/O', async () => { for (const cancel of [false, true]) {
    const { s, state, fetch } = fixture(), controller = new AbortController();
    let finish!: (v: boolean) => void;
    const client = createPipelineScheduleClient(s, { deploymentId: f.deployment_id, token: 'platform', fetch, authorize: async () => new Promise<boolean>(r => finish = r) });
    const pending = client.replay([f.occurrences[1].id], 'alice', { timeoutMs: cancel ? 1000 : 10, signal: controller.signal });
    await new Promise(r => setTimeout(r, 1));
    if (cancel)
        controller.abort();
    await assert.rejects(pending, { code: cancel ? 'cancelled' : 'deadline' });
    finish(true);
    await new Promise(r => setTimeout(r, 1));
    assert.equal(state.calls, 0);
} });
test('wrong local instant and foreign history fail closed', async () => { const { client, state } = fixture(); state.preview.upcoming[0].local_time = '2026-03-08T04:00:00-04:00'; await assert.rejects(client.preview('alice', settings), { code: 'invalid_response' }); state.foreign = true; await assert.rejects(client.history('alice', settings), { code: 'invalid_response' }); });

test('native write rejection is explicit and creates no replay',async()=>{const {client,state,replays}=fixture();state.denied=true;await assert.rejects(client.replay([f.occurrences[1].id],'alice',settings),{code:'request_failed'});assert.equal(state.posts,1);assert.equal(replays.size,0);});

test('client captures credentials and authorization independently of mutable caller options',async()=>{const {s,fetch}=fixture();const config={deploymentId:f.deployment_id,token:'platform',fetch,authorize:async(actor:string)=>actor==='alice'};const client=createPipelineScheduleClient(s,config);config.token='other';config.authorize=async()=>false;assert.equal((await client.inspect('alice',settings)).runtime_enabled,true);});

test('historical run attachment survives rollout while live replay remains pinned',async()=>{const {client,state}=fixture();state.changed=true;assert.equal((await client.history('alice',settings)).occurrences.length,3);assert.equal(await(await client.attach(f.occurrences[0].id,'alice',settings)).result(settings),17);await assert.rejects(client.replay([f.occurrences[1].id],'alice',settings),{code:'deployment_changed'});assert.equal(state.posts,0);});

test('mismatched definition owner cannot reuse another owners deployed input',()=>{const {s,fetch}=fixture();assert.throws(()=>createPipelineScheduleClient({...s,ownerId:'bob'},{deploymentId:f.deployment_id,token:'platform',fetch,authorize:async()=>true}),{code:'invalid_input'});});
