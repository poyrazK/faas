import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { JobContract, prepareJobInput, jobHash, jobSnapshot, jobValueHash } from '../src/job-runtime.js';
import { createFilePipeline, createNativeFileVerifier, parseFileCreatedEvent, fileEventInvocationId, parseFilePipelineReceipt, FilePipelineError, postgresFilePipelineStore, type FilePipelineStore, type FilePipelineReceipt, type FilePipelineIntent } from '../src/file-pipelines.js';
import type { FileBucket } from '../src/files.js';
import type { TypedJob, JobCheckpoint } from '../src/typed-jobs.js';
const f = JSON.parse(readFileSync(new URL('../../../request-contracts/file-pipelines.json', import.meta.url), 'utf8'));
const contract = new JobContract<{
    row: number;
    email: string;
}, {
    row: number;
    saved: boolean;
    rejection: string;
}>(f.contract), bytes = Buffer.from(f.source), options = { timeoutMs: 3000 };
const code = (c: string) => (e: unknown) => e instanceof FilePipelineError && e.code === c;
function harness(scenario = 'success') {
    const records = new Map<string, FilePipelineReceipt>();
    let posts = 0, reads = 0, authorizations = 0;
    let captured: JobCheckpoint | undefined;
    let changedOwner = false;
    const payload = structuredClone(f.payload);
    if (scenario === 'output')
        payload.Records[0].s3.object.key = 'reports/result.csv';
    const headers = { 'x-gregale-event-id': f.event_id, 'x-gregale-event-source': 'gregale.storage', 'x-gregale-event-type': 'object.created', 'x-gregale-event-subscription-id': f.subscription_id };
    const invocation = { id: f.invocation_id, account_id: scenario === 'foreign_proof' ? f.scope.app_id : f.scope.account_id, app_id: f.scope.app_id, source: 'async_invoke', method: 'POST', path: '/', headers, payload };
    const lookup = async (id: string) => {
        if (scenario === 'forged_invocation')
            return { ...invocation, id: '77777777-7777-4777-8777-777777777777' };
        assert.equal(id, f.invocation_id);
        return invocation;
    };
    const delivery = { body: Buffer.from(JSON.stringify(scenario === 'tampered_body' ? f.cloud_event : payload)), invocationId: f.invocation_id };
    const store: FilePipelineStore = { async load(id) { return records.has(id) ? structuredClone(records.get(id)!) : null; }, async reserve(intent) {
            const old = records.get(intent.identity);
            if (old)
                return { created: false, receipt: structuredClone(old) };
            const receipt = { intent: jobSnapshot(intent), checkpoint: null };
            records.set(intent.identity, receipt);
            if (scenario === 'lost_reserve_ack')
                throw new Error('private db failure');
            return { created: true, receipt };
        }, async complete(intent, checkpoint) {
            const old = records.get(intent.identity)!;
            assert.equal(jobValueHash(old.intent), jobValueHash(intent));
            const receipt = { intent, checkpoint };
            records.set(intent.identity, receipt);
            if (scenario === 'lost_finish_ack')
                throw new Error('private db failure');
            return receipt;
        } };
    const job = { async submit(inputs: any[], settings: any) {
            posts++;
            assert.match(settings.idempotencyKey, /^file-pipeline:[a-f0-9]{64}$/);
            const refs = [];
            for (const i of inputs)
                refs.push(await prepareJobInput(contract, i, { inputId: i.id, attempt: 0, signal: settings.signal }));
            captured = { version: 1, base_url: f.scope.base_url, job_name: contract.spec.name, job_id: f.job_id, account_id: f.scope.account_id, run_id: f.run_id, contract_sha256: contract.sha256, input_digest: jobHash(Buffer.from(JSON.stringify(refs))), inputs: refs.map(r => ({ input_id: r.input_id, binding_sha256: jobHash(Buffer.from(r.input_ref)), source_task_index: null })), source_run_id: null };
            if (scenario === 'lost_ack')
                throw new Error('secret URL');
            return { checkpoint: captured };
        }, async resumeById(id: string) { assert.equal(id, f.run_id); return { checkpoint: captured }; } } as unknown as TypedJob<{
        row: number;
        email: string;
    }, {
        row: number;
        saved: boolean;
        rejection: string;
    }>;
    const files = { scope: { base_url: f.scope.base_url, app_slug: f.scope.app_slug, bucket: f.scope.bucket }, async download() {
            reads++;
            if (scenario === 'source_missing')
                throw new Error('private URL');
            return scenario === 'source_changed' ? Buffer.from('x'.repeat(bytes.length)) : bytes;
        } } as unknown as FileBucket;
    const definition = { name: 'csv-files-v1', revision: '1', scope: f.scope, prefix: 'imports/', suffix: '/input.csv', contract, job, files, store, verify: createNativeFileVerifier(f.scope, lookup), authorize: async () => { authorizations++; return scenario === 'unowned' ? null : { id: changedOwner ? 'bob' : 'alice', ...(scenario === 'version_required' ? {} : { sha256: f.sha256 }) }; }, map: async () => [{ id: f.payload.Records[0].s3.object.key.split('%2F')[1] + ':1', value: { row: 1, email: 'a@example.test' } }] };
    return { pipeline: createFilePipeline(definition), definition, delivery, records, store, job, counts: () => ({ posts, reads, authorizations }), checkpoint: () => captured!, changeOwner: () => { changedOwner = true; } };
}
for (const c of f.cases)
    test('shared file pipeline: ' + c.scenario, async () => {
        const h = harness(c.scenario), call = h.pipeline.handle(h.delivery, options);
        if (c.error)
            await assert.rejects(call, code(c.error));
        else
            assert.equal((await call).outcome, c.outcome);
    });
test('20 concurrent deliveries reserve one submission; duplicates remain readable after source deletion', async () => {
    const h = harness();
    const results = await Promise.all(Array.from({ length: 20 }, () => h.pipeline.handle(h.delivery, options)));
    assert.equal(results.filter(r => r.outcome === 'submitted').length, 1);
    assert.equal(h.counts().posts, 1);
    const before = h.counts().reads;
    await h.pipeline.handle(h.delivery, options);
    assert.equal(h.counts().reads, before);
    h.changeOwner();
    await assert.rejects(h.pipeline.handle(h.delivery, options), code('conflict'));
});
test('lost acceptance stays unresolved and is reconciled only to matching manifest/account', async () => {
    const h = harness('lost_ack'), first = await h.pipeline.handle(h.delivery, options);
    assert.equal(first.outcome, 'unresolved');
    assert.ok('receipt' in first);
    assert.equal((await h.pipeline.handle(h.delivery, options)).outcome, 'unresolved');
    assert.equal(h.counts().posts, 1);
    const r = await h.pipeline.reconcile(first.receipt.intent.identity, f.run_id, options);
    assert.equal(r.outcome, 'duplicate');
    assert.equal(h.counts().posts, 1);
    const other = harness('lost_ack'), unknown = await other.pipeline.handle(other.delivery, options);
    assert.ok('receipt' in unknown);
    (other.checkpoint() as any).input_digest = 'f'.repeat(64);
    await assert.rejects(other.pipeline.reconcile(unknown.receipt.intent.identity, f.run_id, options), code('conflict'));
});
test('uncertain reservation never submits, while uncertain completion exposes run ID and survives restart', async () => {
    const h = harness('lost_reserve_ack');
    await assert.rejects(h.pipeline.handle(h.delivery, options), code('store_failed'));
    assert.equal((await h.pipeline.handle(h.delivery, options)).outcome, 'unresolved');
    assert.equal(h.counts().posts, 0);
    const done = harness('lost_finish_ack'), result = await done.pipeline.handle(done.delivery, options);
    assert.equal(result.outcome, 'unresolved');
    assert.ok('runId' in result);
    assert.equal(result.runId, f.run_id);
    assert.equal((await createFilePipeline(done.definition).handle(done.delivery, options)).outcome, 'duplicate');
    assert.equal(done.counts().posts, 1);
});
test('native S3 and CloudEvents normalize identically, decode keys once and reject malformed scopes', async () => {
    assert.equal(fileEventInvocationId(f.scope.account_id, f.event_id, f.subscription_id), f.invocation_id);
    assert.deepEqual(parseFileCreatedEvent(f.payload, f.event_id, f.scope), parseFileCreatedEvent(f.cloud_event, f.event_id, f.scope));
    const record = structuredClone(f.payload);
    record.Records[0].s3.object.key = 'imports%2Fa+b%2B%25%2F%E7%9B%AE%E5%BD%95';
    assert.equal(parseFileCreatedEvent(record, f.event_id, f.scope).key, 'imports/a b+%/目录');
    for (const key of ['bad%', 'bad%ZZ', 'bad%C0%AF']) {
        record.Records[0].s3.object.key = key;
        assert.throws(() => parseFileCreatedEvent(record, f.event_id, f.scope), code('invalid_event'));
    }
    const foreign = structuredClone(f.payload);
    foreign.Records[0].s3.bucket.ownerIdentity.principalId = f.scope.app_id;
    assert.throws(() => parseFileCreatedEvent(foreign, f.event_id, f.scope), code('scope_mismatch'));
});
test('native replay verifies retained original, permits refreshed trace headers, fails closed on pruned proof', async () => {
    const original = { id: f.invocation_id, account_id: f.scope.account_id, app_id: f.scope.app_id, source: 'async_invoke', method: 'POST', path: '/', payload: f.payload, headers: { 'x-gregale-event-id': f.event_id, 'x-gregale-event-source': 'gregale.storage', 'x-gregale-event-type': 'object.created', 'x-gregale-event-subscription-id': f.subscription_id } };
    const id = '77777777-7777-4777-8777-777777777777', replay = { ...original, id, source: 'replay', headers: { ...original.headers, traceparent: 'new' } };
    await createNativeFileVerifier(f.scope, async (key) => key === id ? replay : original)({ body: Buffer.from(JSON.stringify(f.payload)), invocationId: id }, new AbortController().signal);
    await assert.rejects(createNativeFileVerifier(f.scope, async () => replay)({ body: Buffer.from(JSON.stringify(f.payload)), invocationId: id }, new AbortController().signal), code('unverified_delivery'));
});
test('deadlines and cancellation fence late source/mapping/reservation before Jobs', async () => {
    for (const stage of ['verify', 'authorize', 'map'] as const) {
        const h = harness(), d = { ...h.definition, [stage]: () => new Promise<any>(() => { }) };
        await assert.rejects(createFilePipeline(d).handle(h.delivery, { timeoutMs: 20 }), code('deadline'));
        assert.equal(h.counts().posts, 0);
    }
    const h = harness(), cancel = new AbortController();
    cancel.abort();
    await assert.rejects(h.pipeline.handle(h.delivery, { ...options, signal: cancel.signal }), code('cancelled'));
    let release: () => void = () => { };
    const late = { ...h.store, reserve: async (i: FilePipelineIntent, s: AbortSignal) => { await new Promise<void>(r => { release = r; }); return h.store.reserve(i, s); } };
    await assert.rejects(createFilePipeline({ ...h.definition, store: late }).handle(h.delivery, { timeoutMs: 20 }), code('deadline'));
    release();
    await new Promise(r => setTimeout(r, 10));
    assert.equal(h.counts().posts, 0);
    const fresh = harness();
    await assert.rejects(fresh.pipeline.handle(fresh.delivery, { ...options, maxBytes: 1 }), code('limit'));
});
test('mutable file needs owner digest; immutable events select exact version and stale bytes never submit', async () => {
    const h = harness('version_required');
    let selected: string | undefined;
    const event = { ...parseFileCreatedEvent(f.payload, f.event_id, f.scope), version_id: '88888888-8888-4888-8888-888888888888' };
    const files = { ...h.definition.files, download: async (_key: string, o: any) => { selected = o.versionId; return bytes; } } as FileBucket;
    const p = createFilePipeline({ ...h.definition, files, verify: async () => event });
    assert.equal((await p.handle(h.delivery, options)).outcome, 'submitted');
    assert.equal(selected, event.version_id);
});
test('portable receipts and SQL schema match both SDKs', async () => {
    const h = harness(), result = await h.pipeline.handle(h.delivery, options);
    assert.ok('receipt' in result);
    const portable = JSON.parse(readFileSync(new URL('../../../request-contracts/file-pipelines-portable.json', import.meta.url), 'utf8'));
    assert.equal(result.receipt.intent.identity, portable.identity);
    assert.equal(result.receipt.intent.request_digest, portable.request_digest);
    assert.deepEqual(parseFilePipelineReceipt(portable.receipt), portable.receipt);
    const parsed = structuredClone(portable.receipt);
    parsed.checkpoint.account_id = f.scope.app_id;
    assert.throws(() => parseFilePipelineReceipt(parsed), code('conflict'));
    const store = postgresFilePipelineStore(async () => ({ rows: [] }));
    await assert.rejects(store.complete(result.receipt.intent, h.checkpoint(), new AbortController().signal), code('conflict'));
});
test('out-of-order immutable versions read their own bytes; a missing old version never reads current', async () => {
    const h = harness(), base = parseFileCreatedEvent(f.payload, f.event_id, f.scope), old = '88888888-8888-4888-8888-888888888888', newer = '99999999-9999-4999-8999-999999999999';
    let event = { ...base, version_id: newer };
    const mapped: string[] = [], versions = new Map([[old, bytes], [newer, Buffer.from(f.source.replace('a@example', 'c@example'))]]);
    const pipeline = createFilePipeline({ ...h.definition, verify: async () => event, authorize: async () => ({ id: 'alice' }), files: { ...h.definition.files, download: async (_key: string, o: any) => {
                assert.ok(o.versionId);
                const b = versions.get(o.versionId);
                if (!b)
                    throw new Error('version unavailable');
                return b;
            } } as FileBucket, map: async (_event, data) => { mapped.push(Buffer.from(data).toString()); return [{ id: _event.receipt_id + ':1', value: { row: 1, email: 'a@example.test' } }]; } });
    await pipeline.handle(h.delivery, options);
    event = { ...base, version_id: old, receipt_id: old, event_id: 'multipart:' + old };
    await pipeline.handle(h.delivery, options);
    assert.equal(mapped[0], f.source.replace('a@example', 'c@example'));
    assert.equal(mapped[1], f.source);
    assert.equal(h.counts().posts, 2);
    versions.delete(old);
    event = { ...event, receipt_id: newer, event_id: 'multipart:' + newer };
    await assert.rejects(pipeline.handle(h.delivery, options), code('source_unavailable'));
    assert.equal(h.counts().posts, 2);
    const mutable = structuredClone(f.payload);
    mutable.Records[0].s3.object.versionId = 'null';
    assert.equal(parseFileCreatedEvent(mutable, f.event_id, f.scope).version_id, null);
});
test('scope, owner and verifier extensions do not persist secrets or change optional-field fingerprints', async () => {
    const h = harness(), scope = { ...f.scope, token: 'scope-secret' };
    const definition = { ...h.definition, scope, authorize: async () => ({ id: 'alice', sha256: f.sha256, source_digest: undefined, token: 'owner-secret' }), verify: async (d: any, s: AbortSignal) => ({ ...await h.definition.verify(d, s), token: 'event-secret' }) };
    const result = await createFilePipeline(definition).handle(h.delivery, options);
    assert.ok('receipt' in result);
    assert.equal(JSON.stringify(result.receipt).includes('secret'), false);
    const normal = await h.pipeline.handle(h.delivery, options);
    assert.equal(normal.outcome, 'duplicate');
});
test('a callback delaying timers past the budget cannot reserve or submit', async () => {
    const h = harness(), map = async () => { const end = performance.now() + 30; while (performance.now() < end) { } return [{ id: 'cpu', value: { row: 1, email: 'a@example.test' } }]; };
    await assert.rejects(createFilePipeline({ ...h.definition, map }).handle(h.delivery, { timeoutMs: 10 }), code('deadline'));
    assert.equal(h.counts().posts, 0);
    assert.equal(h.records.size, 0);
});
