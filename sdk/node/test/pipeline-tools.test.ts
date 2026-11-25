import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { randomUUID } from 'node:crypto';
import { JobContract, jobHash } from '../src/job-runtime.js';
import { FileError, type FileBucket } from '../src/files.js';
import { definePipelineTool, pipelineToolSchema, parsePipelineToolReceipt, type PipelineToolStore, type PipelineToolReceipt, type PipelineToolDefinition } from '../src/pipeline-tools.js';
import { readPinnedPipelineSource, type PinnedPipelineSource } from '../src/pipeline-state.js';
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
    const rows = new Map<string, PipelineToolReceipt>(), records = new Map<string, any>(), keys = new Map<string, string>(), state = { posts: 0, lookups: 0, reserves: 0, auth: [] as string[], revoked: false, owner: 'alice', loseReserve: false, loseStart: false, loseComplete: false, wrongAccount: false, gate: null as any, expired: false };
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
        if (path.includes('/workflows/run-starts/')) {
            assert.equal(init?.method, 'GET');
            state.lookups++;
            const id = keys.get(new Headers(init.headers).get('idempotency-key')!);
            return Response.json(id ? { run_id: id } : { code: 'workflow_run_not_found' }, { status: id ? 200 : 404 });
        }
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


test('lost start lookup verifies the original trigger and input without attaching or starting', async () => {
    const s = setup();
    const id = f.request_id, expectedRunId = f.receipt.checkpoint.run_id;
    s.state.loseStart = true;
    const unknown = await s.tool.start(f.input, id, 'alice', options);
    assert.equal(unknown.state, 'start_unknown');
    const key = unknown.receipt.intent.idempotency_key;
    assert.deepEqual(await s.tool.findAcceptedRun(id, 'alice', options), { state: 'found', request_id: id, run_id: expectedRunId });
    assert.equal((await s.tool.inspect(id, 'alice', options))!.state, 'start_unknown');
    await assert.rejects(s.tool.findAcceptedRun(id, 'bob', options), { code: 'unauthorized' });
    assert.equal(s.state.lookups, 1);

    const record = s.records.get(expectedRunId);
    s.records.set(expectedRunId, { ...record, input: { ...record.input, input: { n: 19 } } });
    await assert.rejects(s.tool.findAcceptedRun(id, 'alice', options), { code: 'receipt_conflict' });
    s.records.set(expectedRunId, record);
    s.state.loseStart = false;
    const decoy = await s.tool.start(f.input, randomUUID(), 'alice', options);
    s.keys.set(key, decoy.receipt.checkpoint!.run_id);
    await assert.rejects(s.tool.findAcceptedRun(id, 'alice', options), { code: 'receipt_conflict' });

    s.keys.delete(key);
    assert.deepEqual(await s.tool.findAcceptedRun(id, 'alice', options), { state: 'start_unknown', request_id: id });
    assert.equal((await s.tool.inspect(id, 'alice', options))!.state, 'start_unknown');
    s.keys.set(key, expectedRunId);
    const matches = await Promise.all([
        s.tool.findAcceptedRun(id, 'alice', options),
        definePipelineTool(contract, s.definition).findAcceptedRun(id, 'alice', options),
    ]);
    assert.ok(matches.every(match => match.state === 'found' && match.run_id === expectedRunId));
    const attached = await Promise.all(matches.map(match =>
        s.tool.reconcile(id, match.state === 'found' ? match.run_id : assert.fail('Missing match'), 'alice', options),
    ));
    assert.deepEqual(attached.map(result => result.receipt), [f.receipt, f.receipt]);
    assert.equal(s.state.posts, 2, 'only the original start and the deliberate decoy');
});


test('pipeline output resources check current result access before and after downloading', async () => {
    const s = setup();
    await s.tool.start(f.input, f.request_id, 'alice', options);
    const native = s.records.get(f.receipt.checkpoint.run_id);
    native.status = 'succeeded';
    native.output = { version: 1, scope, owner_id: 'alice', contract_sha256: contract.sha256, output: f.output };

    const bytes = Buffer.from('result\n');
    const fileScope = { base_url: scope.base_url, app_slug: scope.app_slug, bucket: randomUUID() };
    let downloads = 0;
    let revokeOnDownload = false;
    const files = {
        scope: fileScope,
        async download(key: string) {
            assert.equal(key, 'reports/' + f.output.n);
            downloads++;
            if (revokeOnDownload) s.state.revoked = true;
            return bytes;
        },
    } as unknown as FileBucket;
    const adapter = createPipelineToolMCP(s.tool, {
        outputResources: [{
            name: 'report',
            description: 'Pipeline report',
            mimeType: 'text/plain',
            files,
            select: (output) => ({
                version: 1, scope: fileScope, owner_id: 'alice', key: 'reports/' + output.n,
                sha256: jobHash(bytes), size_bytes: bytes.length,
            }),
        }],
    });
    const { content } = await adapter.call(f.name + '_result', { request_id: f.request_id }, 'alice', options);
    const link = content[1];
    assert.ok(link);
    assert.equal(downloads, 0);
    await assert.rejects(adapter.readResource(link.uri, 'bob', options), { code: -32002 });
    assert.equal(downloads, 0);
    assert.equal((await adapter.readResource(link.uri, 'alice', options)).contents[0]?.text, bytes.toString());
    revokeOnDownload = true;
    await assert.rejects(adapter.readResource(link.uri, 'alice', options), { code: -32002 });
    assert.equal(downloads, 2);
});

test('retaining a revision replays its authorized receipt and fences new work', async () => {
    const s = setup(), start = f.name + '_start';
    const publish = (starts: 'default' | 'retained') => createPipelineToolMCP(
        definePipelineTool(contract, s.definition), { revision: { family: 'imports', starts } },
    );
    const args = { request_id: f.request_id, input: f.input, expected_contract_sha256: contract.sha256 };
    const current = publish('default');
    const failure = async (adapter: typeof current, args: unknown, code: string, actor = 'alice') => {
        const result = await adapter.call(start, args, actor, options);
        assert.equal(result.isError, true);
        assert.deepEqual(JSON.parse(result.content[0]!.text), { code });
    };
    await failure(current, { ...args, expected_contract_sha256: '0'.repeat(64) }, 'revision_unavailable');
    assert.equal(s.state.reserves, 0);
    s.state.loseStart = true;
    const first = await current.call(start, args, 'alice', options);
    assert.equal(first.structuredContent?.state, 'start_unknown');
    const retained = publish('retained');
    const replay = await retained.call(start, args, 'alice', options);
    assert.deepEqual(replay.structuredContent, { ...first.structuredContent, replayed: true });
    await failure(retained, { ...args, request_id: randomUUID() }, 'revision_unavailable');
    await failure(retained, { ...args, input: { ...args.input, n: 18 } }, 'receipt_conflict');
    await failure(retained, args, 'unauthorized', 'bob');
    s.state.revoked = true;
    await failure(retained, args, 'unauthorized');
    assert.equal(s.state.reserves, 1);
    assert.equal(s.state.posts, 1);
});

test('pipeline previews bind current input, caller and source before reserving a start', async (t) => {
    const fixture = JSON.parse(readFileSync(new URL('../../../request-contracts/pipeline-start-preview.json', import.meta.url), 'utf8'));
    let sourceVersion = fixture.source_version, reads = 0, revoked = false;
    let now = Date.now(), expireDuringAuthorization = false, sourceRead = false;
    t.mock.method(Date, 'now', () => now);
    const s = setup({
        authorize: async (_request, actor) => {
            if (expireDuringAuthorization && sourceRead)
                now += 301000;
            return revoked ? null : { owner_id: actor, subject_id: actor };
        },
    });
    const mcp = createPipelineToolMCP(s.tool, {
        preview: {
            contract: new JobContract<null, number>(fixture.contract),
            signingKey: 'local-preview-test-key'.repeat(2),
            async read() {
                reads++;
                sourceRead = true;
                return { sourceVersion, summary: fixture.summary };
            },
        },
    });
    const args = { request_id: f.request_id, input: f.input, expected_contract_sha256: contract.sha256 };
    const call = (op: string, arguments_: Record<string, unknown>, actor = 'alice') =>
        mcp.call(f.name + '_' + op, arguments_, actor, options);
    async function rejected(arguments_: Record<string, unknown>, code: string, actor?: string) {
        const response = await call('start', arguments_, actor);
        assert.equal(response.isError, true);
        assert.deepEqual(JSON.parse(response.content[0]!.text), { code });
    }
    assert.equal(mcp.tools.find(({ name }) => name.endsWith('_preview'))?.annotations.readOnlyHint, true);
    const shown = (await call('preview', args)).structuredContent!;
    assert.equal(shown.summary, fixture.summary);
    assert.equal(s.state.reserves, 0);
    await rejected(args, 'preview_required');
    const confirmed = { ...args, preview_token: shown.preview_token };
    await rejected(confirmed, 'preview_stale', 'bob');
    await rejected({ ...confirmed, input: { ...f.input, n: 18 } }, 'preview_stale');
    await rejected({ ...confirmed, request_id: randomUUID() }, 'preview_stale');
    sourceVersion = fixture.changed_version;
    await rejected(confirmed, 'preview_stale');
    await rejected({ ...confirmed, preview_token: '1.' + 'a'.repeat(64) }, 'preview_stale');
    assert.equal(s.state.reserves, 0);
    const expiring = (await call('preview', args)).structuredContent!;
    sourceRead = false;
    expireDuringAuthorization = true;
    await rejected({ ...args, preview_token: expiring.preview_token }, 'preview_stale');
    expireDuringAuthorization = false;
    assert.equal(s.state.reserves, 0);
    const refreshed = (await call('preview', args)).structuredContent!;
    const accepted = await call('start', { ...args, preview_token: refreshed.preview_token });
    assert.equal(accepted.structuredContent?.state, 'started');
    sourceVersion = fixture.source_version;
    const snapshots = reads;
    const replayed = await call('start', confirmed);
    assert.deepEqual(replayed.structuredContent, { ...accepted.structuredContent, replayed: true });
    assert.equal(reads, snapshots);
    revoked = true;
    await rejected(confirmed, 'unauthorized');
    assert.equal(s.state.reserves, 1);
    assert.equal(s.state.posts, 1);
});

test('confirmation pins a file version in the native envelope and recovery checks it', async () => {
    const fixture = JSON.parse(readFileSync(new URL('../../../request-contracts/pipeline-start-preview.json', import.meta.url), 'utf8'));
    const s = setup();
    let source: PinnedPipelineSource = fixture.pinned_source;
    const adapter = createPipelineToolMCP(s.tool, { preview: {
        contract: new JobContract<null, number>(fixture.contract),
        signingKey: 'fixture-pinned-preview-key'.repeat(2),
        async read() { return { sourceVersion: source.sha256, summary: fixture.summary, pinnedSource: source }; },
    } });
    const args = { request_id: f.request_id, input: f.input };
    const preview = async () => (await adapter.call(f.name + '_preview', args, 'alice', options)).structuredContent!;
    const confirm = (shown: Record<string, unknown>) => adapter.call(f.name + '_start',
        { ...args, preview_token: shown.preview_token }, 'alice', options);
    const original = await preview();
    assert.equal('pinned_source' in original, false);
    source = { ...source, version_id: fixture.replacement_version_id };
    const stale = await confirm(original);
    assert.deepEqual(JSON.parse(stale.content[0]!.text), { code: 'preview_stale' });
    assert.equal(s.state.reserves, 0);
    s.state.loseStart = true;
    assert.equal((await confirm(await preview())).structuredContent!.state, 'start_unknown');
    const run = s.records.get(f.receipt.checkpoint.run_id);
    assert.deepEqual(run.input.pinned_source, source);
    const adopted = await s.tool.reconcile(f.request_id, run.id, 'alice', options);
    assert.equal(adopted.receipt.intent.request_sha256, f.receipt.intent.input_sha256);
    assert.notEqual(adopted.receipt.intent.input_sha256, adopted.receipt.intent.request_sha256);
    assert.equal((await confirm(original)).structuredContent?.replayed, true);
    await assert.rejects(s.tool.start({ ...f.input, n: 18 }, f.request_id, 'alice', options), { code: 'receipt_conflict' });
    run.input.pinned_source = fixture.pinned_source;
    await assert.rejects(s.tool.inspect(f.request_id, 'alice', options), { code: 'receipt_conflict' });
    assert.equal(s.state.posts, 1);
});

test('pinned reads verify the version and current authority without reading the replacement', async () => {
    const fixture = JSON.parse(readFileSync(new URL('../../../request-contracts/pipeline-start-preview.json', import.meta.url), 'utf8'));
    const pinned: PinnedPipelineSource = fixture.pinned_source;
    let downloads = 0, authorized = true, revokeDuringRead = false, missing = false;
    const files = { scope: pinned.scope, async download(key, settings) {
        downloads++;
        assert.equal(key, pinned.key);
        assert.equal(settings.versionId, pinned.version_id);
        if (missing) throw new FileError('request_failed', 404);
        if (revokeDuringRead) authorized = false;
        return Buffer.from(fixture.pinned_bytes);
    } } satisfies Pick<FileBucket, 'scope' | 'download'>;
    const read = (source = pinned) => readPinnedPipelineSource(files, source, 'alice', {
        ...options, authorize: async () => authorized,
    });
    assert.deepEqual(await read(), Buffer.from(fixture.pinned_bytes));
    await assert.rejects(async () => read({ ...pinned, owner_id: 'bob' }), { code: 'source_unavailable' });
    assert.equal(downloads, 1);
    revokeDuringRead = true;
    await assert.rejects(read(), { code: 'source_forbidden' });
    await assert.rejects(read(), { code: 'source_forbidden' });
    assert.equal(downloads, 2);
    authorized = true;
    missing = true;
    await assert.rejects(read(), { code: 'source_unavailable' });
    assert.equal(downloads, 3);
});
