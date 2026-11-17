import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { randomUUID } from 'node:crypto';
import { JobContract } from '../src/job-runtime.js';
import { definePipelineTool, pipelineToolSchema, parsePipelineToolReceipt, type PipelineToolStore, type PipelineToolReceipt, type PipelineToolDefinition } from '../src/pipeline-tools.js';
import { createPipelineToolMCP } from '../src/pipeline-tool-mcp.js';
import { pipelineGateIntent, type PipelineGateStore } from '../src/pipeline-gates.js';
const f = JSON.parse(readFileSync(new URL('../../../request-contracts/pipeline-tools.json', import.meta.url), 'utf8')), scope = f.scope, options = { timeoutMs: 1000 };
const contract = new JobContract<{
    n: number;
    label?: string;
}, {
    n: number;
}>(f.contract);
function setup(overrides: Partial<PipelineToolDefinition<{
    n: number;
    label?: string;
}, string>> = {}) {
    const rows = new Map<string, PipelineToolReceipt>(), records = new Map<string, any>(), keys = new Map<string, string>(), state = { posts: 0, reserves: 0, auth: [] as string[], revoked: false, owner: 'alice', loseReserve: false, loseStart: false, loseComplete: false, wrongAccount: false, gate: null as any, expired: false };
    const store: PipelineToolStore = { async load(id) { return rows.get(id) ?? null; }, async reserve(intent) {
            state.reserves++;
            const old = rows.get(intent.identity);
            if (old)
                return { created: false, receipt: old };
            const receipt = parsePipelineToolReceipt({ intent, checkpoint: null });
            rows.set(intent.identity, receipt);
            if (state.loseReserve)
                throw Error('private database details');
            return { created: true, receipt };
        }, async complete(intent, checkpoint) {
            const old = rows.get(intent.identity);
            if (!old || old.checkpoint && old.checkpoint.run_id !== checkpoint.run_id)
                throw Error('conflict');
            const receipt = parsePipelineToolReceipt({ intent, checkpoint });
            rows.set(intent.identity, receipt);
            if (state.loseComplete)
                throw Error('private database details');
            return receipt;
        } };
    const fetch: typeof globalThis.fetch = async (url, init) => {
        assert.equal(new Headers(init?.headers).get('authorization'), 'Bearer backend');
        const path = new URL(String(url)).pathname;
        if (path === '/v1/account')
            return Response.json({ id: state.wrongAccount ? randomUUID() : scope.account_id });
        if (path === '/v1/apps/imports')
            return Response.json({ id: scope.app_id });
        if (path.endsWith('/steps'))
            return Response.json({ steps: [{ step_name: 'validate', status: 'succeeded', attempt: 1 }] });
        if (path.endsWith('/cancel')) {
            const r = records.get(path.split('/').at(-2)!);
            r.status = 'failed';
            r.cancelled_at = '2026-11-17T00:00:00Z';
            return Response.json(r);
        }
        if (init?.method === 'POST') {
            state.posts++;
            const key = new Headers(init.headers).get('idempotency-key')!, id = keys.get(key) ?? (records.size ? randomUUID() : f.receipt.checkpoint.run_id);
            keys.set(key, id);
            if (!records.has(id))
                records.set(id, { id, app_id: scope.app_id, workflow_name: contract.spec.name, status: 'running', input: JSON.parse(String(init.body)) });
            if (state.loseStart)
                throw Error('private HTTP details');
            return Response.json(records.get(id), { status: 201 });
        }
        const r = records.get(path.split('/').at(-1)!);
        return Response.json(r ?? {}, { status: r ? 200 : 404 });
    };
    const gates = { async list() { return state.gate ? [state.gate] : []; }, async resolve() { return state.expired ? { ...state.gate, decision: { state: 'expired', actor_id: null, payload: null, decided_at: '2026-11-17T00:00:00.000Z' } } : state.gate; } } as unknown as PipelineGateStore;
    const definition: PipelineToolDefinition<{
        n: number;
        label?: string;
    }, string> = { name: f.name, description: f.description, scope, store, token: 'backend', fetch, gateStore: gates, reviewBaseURL: 'https://review.example.test/', authorize: async (req, actor, action) => { state.auth.push(action); return actor === 'alice' && !state.revoked ? { subject_id: 'alice-session', owner_id: state.owner } : null; }, ...overrides };
    const tool = definePipelineTool(contract, definition), mcp = createPipelineToolMCP(tool);
    function gate() { const c = rows.values().next().value!.checkpoint!, intent = pipelineGateIntent({ kind: 'approval', scope, workflow_run_id: c.run_id, owner_id: 'alice', pipeline_sha256: contract.sha256, input_sha256: c.input_sha256, name: 'review', contract_sha256: 'a'.repeat(64), review: { digest: 'b'.repeat(64) }, required_roles: ['reviewer'], timeout_ms: 1000 }); state.gate = { intent, expires_at: '2026-11-17T00:00:01.123Z', decision: null, delivery: 'pending' }; return state.gate; }
    return { tool, mcp, state, rows, records, keys, definition, gate };
}
test('shared schemas, envelope, portable receipt and MCP descriptors agree', async () => { const s = setup(); assert.deepEqual(s.mcp.tools, f.tools); assert.equal(pipelineToolSchema, readFileSync(new URL('../../../request-contracts/pipeline-tools.sql', import.meta.url), 'utf8')); const first = await s.tool.start(f.input, f.request_id, 'alice', options); assert.deepEqual(first.receipt, f.receipt); assert.deepEqual(s.records.get(f.receipt.checkpoint.run_id).input, f.envelope); assert.equal(first.replayed, false); assert.equal(JSON.stringify(first.receipt).includes('café'), false); assert.equal(JSON.stringify(first.receipt).includes('backend'), false); });
test('concurrent calls and restarted tools start only once', async () => { const s = setup(); const results = await Promise.all(Array.from({ length: 24 }, () => s.tool.start(f.input, f.request_id, 'alice', options))); assert.equal(s.state.posts, 1); assert.equal(results.filter(r => !r.replayed).length, 1); s.keys.clear(); assert.equal((await definePipelineTool(contract, s.definition).start(f.input, f.request_id, 'alice', options)).state, 'started'); assert.equal(s.state.posts, 1); });
test('lost reserve, native and completion acknowledgements never permit another start', async () => {
    for (const phase of ['Reserve', 'Start', 'Complete'] as const) {
        const s = setup();
        s.state[('lose' + phase) as 'loseReserve'] = true;
        if (phase === 'Start')
            assert.equal((await s.tool.start(f.input, f.request_id, 'alice', options)).state, 'start_unknown');
        else
            await assert.rejects(s.tool.start(f.input, f.request_id, 'alice', options), { code: 'store_failed' });
        s.state.loseReserve = s.state.loseStart = s.state.loseComplete = false;
        s.keys.clear();
        const repeated = await definePipelineTool(contract, s.definition).start(f.input, f.request_id, 'alice', options);
        assert.equal(repeated.state, phase === 'Complete' ? 'started' : 'start_unknown');
        assert.equal(s.state.posts, phase === 'Reserve' ? 0 : 1);
        if (phase === 'Start')
            assert.deepEqual((await s.tool.reconcile(f.request_id, f.receipt.checkpoint.run_id, 'alice', options)).receipt, f.receipt);
    }
});
test('same ID changed input/config or transferred ownership conflicts', async () => {
    const s = setup();
    await s.tool.start(f.input, f.request_id, 'alice', options);
    await assert.rejects(s.tool.start({ ...f.input, n: 18 }, f.request_id, 'alice', options), { code: 'receipt_conflict' });
    const changed = new JobContract<{
        n: number;
        label?: string;
    }, string>({ ...f.contract, output: { type: 'string' } });
    await assert.rejects(definePipelineTool(changed, s.definition).inspect(f.request_id, 'alice', options), { code: 'receipt_conflict' });
    s.state.owner = 'bob';
    await assert.rejects(s.tool.inspect(f.request_id, 'alice', options), { code: 'receipt_conflict' });
    assert.equal(s.state.posts, 1);
});
test('current authorization covers all operations and duplicate calls', async () => {
    const s = setup();
    await assert.rejects(s.tool.start(f.input, f.request_id, 'bob', options), { code: 'unauthorized' });
    assert.equal(s.state.reserves, 0);
    await s.tool.start(f.input, f.request_id, 'alice', options);
    s.state.revoked = true;
    for (const action of [() => s.tool.start(f.input, f.request_id, 'alice', options), () => s.tool.inspect(f.request_id, 'alice', options), () => s.tool.result(f.request_id, 'alice', options), () => s.tool.cancel(f.request_id, 'alice', options), () => s.tool.reconcile(f.request_id, f.receipt.checkpoint.run_id, 'alice', options)])
        await assert.rejects(action(), { code: 'unauthorized' });
    assert.equal(s.state.posts, 1);
});
test('approval reads bind exact run/owner/input, resolve expiry and never decide', async () => { const s = setup(); await s.tool.start(f.input, f.request_id, 'alice', options); const gate = s.gate(), status = await s.tool.inspect(f.request_id, 'alice', options); assert.equal(status!.state, 'awaiting_approval'); assert.equal(status!.approval!.id, gate.intent.id); assert.equal(status!.approval!.review_url, `https://review.example.test/?run=${f.receipt.checkpoint.run_id}&gate=${gate.intent.id}`); assert.equal('review' in status!.approval!, false); s.state.expired = true; assert.equal((await s.tool.inspect(f.request_id, 'alice', options))!.state, 'running'); s.state.expired = false; s.state.gate = { ...gate, intent: pipelineGateIntent({ ...(({ version, id, review_sha256, binding_sha256, ...base }) => base)(gate.intent), owner_id: 'bob' }) }; await assert.rejects(s.tool.inspect(f.request_id, 'alice', options), { code: 'receipt_conflict' }); });
test('MCP lifecycle returns structured states, typed result, sanitized errors and explicit cancellation', async () => { const s = setup(); let r = await s.mcp.call(f.name + '_status', { request_id: f.request_id }, 'alice', options); assert.equal(r.structuredContent!.state, 'not_found'); r = await s.mcp.call(f.name + '_start', { request_id: f.request_id, input: f.input }, 'alice', options); assert.equal(r.structuredContent!.state, 'started'); s.gate(); assert.equal((await s.mcp.call(f.name + '_result', { request_id: f.request_id }, 'alice', options)).structuredContent!.state, 'awaiting_approval'); await assert.rejects(s.tool.result(f.request_id, 'alice', options), { code: 'not_terminal' }); const native = s.records.get(f.receipt.checkpoint.run_id); native.status = 'succeeded'; native.output = { version: 1, scope, owner_id: 'alice', contract_sha256: contract.sha256, output: f.output }; r = await s.mcp.call(f.name + '_result', { request_id: f.request_id }, 'alice', options); assert.deepEqual(r.structuredContent!.output, f.output); assert.deepEqual(JSON.parse(r.content[0]!.text), r.structuredContent); native.output.output = { n: 'bad' }; r = await s.mcp.call(f.name + '_result', { request_id: f.request_id }, 'alice', options); assert.equal(r.isError, true); assert.equal(JSON.stringify(r).includes('backend'), false); native.status = 'running'; r = await s.mcp.call(f.name + '_cancel', { request_id: f.request_id }, 'alice', options); assert.equal(r.structuredContent!.state, 'cancelled'); assert.equal((await createPipelineToolMCP(s.tool, { protocolVersion: '2026-07-28' }).call(f.name + '_status', { request_id: f.request_id }, 'alice', options)).resultType, 'complete'); });
test('MCP rejects extra ownership/approval fields, bad inputs and unknown tools', async () => {
    const s = setup();
    for (const [name, args, code] of [[f.name + '_start', { request_id: f.request_id, input: f.input, owner_id: 'alice' }, -32602], [f.name + '_start', { request_id: f.request_id, input: { n: 'bad' } }, -32602], [f.name + '_status', { request_id: 'bad' }, -32602], ['unknown', {}, -32601], [f.name + '_approve', { request_id: f.request_id }, -32601]] as const)
        await assert.rejects(s.mcp.call(name, args, 'alice', options), { code });
    assert.equal(s.state.posts, 0);
});
test('reconciliation rejects another request with identical business input', async () => { const s = setup(); s.state.loseStart = true; await s.tool.start(f.input, f.request_id, 'alice', options); s.state.loseStart = false; const other = await s.tool.start(f.input, randomUUID(), 'alice', options); await assert.rejects(s.tool.reconcile(f.request_id, other.receipt.checkpoint!.run_id, 'alice', options), { code: 'receipt_conflict' }); });
test('deadline and cancellation fence late authorization before reservation or HTTP', async () => {
    for (const cancel of [false, true]) {
        let finish!: (actor: any) => void;
        const s = setup({ authorize: async () => new Promise(r => finish = r) }), controller = new AbortController();
        const pending = s.tool.start(f.input, f.request_id, 'alice', { timeoutMs: cancel ? 1000 : 10, signal: controller.signal });
        await new Promise(r => setTimeout(r, 1));
        if (cancel)
            controller.abort();
        await assert.rejects(pending, { code: cancel ? 'cancelled' : 'deadline' });
        finish({ subject_id: 'alice-session', owner_id: 'alice' });
        await new Promise(r => setTimeout(r, 1));
        assert.equal(s.state.reserves, 0);
        assert.equal(s.state.posts, 0);
    }
});
test('late reservation acknowledgement is fenced after deadline and cannot dispatch', async () => {
    const s = setup();
    let finish!: () => void;
    const wait = new Promise<void>(r => finish = r), base = s.definition.store;
    const store: PipelineToolStore = { ...base, async reserve(intent, signal) { const result = await base.reserve(intent, signal); await wait; return result; } };
    const tool = definePipelineTool(contract, { ...s.definition, store });
    await assert.rejects(tool.start(f.input, f.request_id, 'alice', { timeoutMs: 20 }), { code: 'deadline' });
    assert.equal(s.rows.size, 1);
    finish();
    await new Promise(r => setTimeout(r, 5));
    assert.equal(s.state.posts, 0);
    assert.equal((await tool.start(f.input, f.request_id, 'alice', options)).state, 'start_unknown');
    assert.equal(s.state.posts, 0);
});
test('MCP snapshots arguments across result observation and enforces frame limits', async () => {
    const s = setup();
    await s.tool.start(f.input, f.request_id, 'alice', options);
    const native = s.records.get(f.receipt.checkpoint.run_id);
    native.status = 'succeeded';
    native.output = { version: 1, scope, owner_id: 'alice', contract_sha256: contract.sha256, output: f.output };
    let release!: () => void, entered!: () => void;
    const ready = new Promise<void>(r => entered = r), wait = new Promise<void>(r => release = r);
    let requested = '';
    const wrapped = { ...s.tool, async inspect(...args: Parameters<typeof s.tool.inspect>) { entered(); await wait; return s.tool.inspect(...args); }, async result(...args: Parameters<typeof s.tool.result>) { requested = args[0]; return s.tool.result(...args); } };
    const args = { request_id: f.request_id }, pending = createPipelineToolMCP(wrapped).call(f.name + '_result', args, 'alice', options);
    await ready;
    args.request_id = randomUUID();
    release();
    assert.equal((await pending).isError, false);
    assert.equal(requested, f.request_id);
    const big = { ...s.tool, contract: new JobContract<{
            n: number;
        }, string>({ ...f.contract, output: { type: 'string' } }), async result() { return 'x'.repeat(600000); } };
    const response = await createPipelineToolMCP(big).call(f.name + '_result', { request_id: f.request_id }, 'alice', options);
    assert.equal(response.isError, true);
    assert.equal(JSON.parse(response.content[0]!.text).code, 'limit');
});
