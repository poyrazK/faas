import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { createClient, FileError, parseFileCheckpoint, blobFileSource, fileSourceDigest, type FileCheckpoint } from '../src/preview.js';
import { JobError } from '../src/job-runtime.js';
const f = JSON.parse(readFileSync(new URL('../../../request-contracts/files.json', import.meta.url), 'utf8')) as {
    base_url: string;
    app_slug: string;
    app_id: string;
    bucket: string;
    upload_id: string;
    receipt_id: string;
    key: string;
    source: string;
    part_size: number;
    cursor: string;
    cases: {
        scenario: string;
        error: string | null;
    }[];
};
const bytes = new TextEncoder().encode(f.source), source = () => blobFileSource(new Blob([bytes]));
const sha = (b: Uint8Array) => createHash('sha256').update(b).digest('hex');
const code = (v: string) => (e: unknown) => e instanceof FileError && e.code === v;
const options = { timeoutMs: 3000, pollDelayMs: 0, maxPolls: 2 };
function harness(scenario = 'success', token = 'alice') {
    let state = 'active', putCalls = 0, completes = 0, reads = 0, receiptKey = f.key, receiptSize = 0;
    const saved = new Map<number, {
        etag: string;
        data: Uint8Array;
    }>(), objects = new Map<string, Uint8Array>();
    const session = () => ({ id: f.upload_id, key: f.key, size_bytes: bytes.length, part_size_bytes: f.part_size, part_count: Math.ceil(bytes.length / f.part_size) + (scenario === 'bad_layout' ? 1 : 0), content_type: 'text/csv', state, expires_at: scenario === 'expired' ? '2000-01-01T00:00:00Z' : '2099-01-01T00:00:00Z', ...(state === 'completed' ? { etag: '"complete"' } : {}) });
    const signed = (method: string, key: string, part?: number) => ({ method, url: (scenario === 'insecure_url' ? 'http://remote.test' : 'https://objects.test') + '/' + encodeURIComponent(key) + (part ? '?part=' + part : ''), headers: scenario === 'untrusted_headers' ? { Authorization: 'secret' } : { 'Content-Type': 'text/csv' }, expires_at: scenario === 'expired_capability' ? '2000-01-01T00:00:00Z' : '2099-01-01T00:00:00Z', ...(method === 'PUT' && !part ? { upload_id: f.receipt_id } : {}) });
    const fetch: typeof globalThis.fetch = async (url, init) => {
        const u = new URL(String(url)), p = u.pathname.replace('/prefix', '');
        reads++;
        assert.equal(new Headers(init?.headers).get('authorization'), 'Bearer ' + token);
        assert.equal(init?.redirect, 'error');
        if (scenario === 'forbidden')
            return Response.json({ status: 403 }, { status: 403 });
        if (p === '/v1/apps/imports')
            return Response.json({ id: f.app_id });
        if (p.endsWith('/signed-url') && !p.includes('/parts/')) {
            const body = JSON.parse(String(init?.body));
            receiptKey = body.key;
            receiptSize = body.size_bytes ?? 0;
            if (body.if_none_match === '*' && objects.has(body.key))
                return Response.json({ status: 409 }, { status: 409 });
            return Response.json(signed(body.method, body.key));
        }
        if (p.includes('/write-receipts/'))
            return Response.json({ id: f.receipt_id, bucket_id: scenario === 'wrong_receipt' ? f.app_id : f.bucket, key: receiptKey, bytes: receiptSize, operation: 'put', status: scenario === 'pending_receipt' ? 'pending' : 'completed', etag: '"put"' });
        if (p.endsWith('/objects')) {
            if (init?.method === 'DELETE') {
                objects.delete(u.searchParams.get('key')!);
                return new Response(null, { status: 204 });
            }
            if (u.searchParams.has('cursor'))
                assert.equal(u.searchParams.get('cursor'), f.cursor);
            return Response.json({ items: [{ key: f.key, size_bytes: bytes.length, last_modified: '2026-01-01T00:00:00Z' }], next_cursor: f.cursor });
        }
        if (p.endsWith('/parts'))
            return Response.json({ items: [...saved].sort(([a], [b]) => a - b).map(([part_number, v]) => ({ part_number, etag: v.etag, size_bytes: v.data.length })) });
        if (p.includes('/parts/') && p.endsWith('/signed-url'))
            return Response.json(signed('PUT', f.key, Number(p.split('/').at(-2))));
        if (p.endsWith('/complete')) {
            completes++;
            const body = JSON.parse(String(init?.body));
            assert.deepEqual(body.parts, [...saved].sort(([a], [b]) => a - b).map(([part_number, v]) => ({ part_number, etag: v.etag })));
            state = scenario === 'completing' ? 'completing' : 'completed';
            objects.set(f.key, Buffer.concat([...saved].sort(([a], [b]) => a - b).map(([, v]) => v.data)));
            if (scenario === 'lost_complete_ack' && completes === 1)
                throw new Error('secret signed URL');
            return Response.json(session());
        }
        if (init?.method === 'DELETE') {
            state = 'aborted';
            return new Response(null, { status: 204 });
        }
        return Response.json(session());
    };
    const dataFetch: typeof globalThis.fetch = async (url, init) => {
        assert.equal(init?.credentials, 'omit');
        assert.equal(init?.redirect, 'error');
        assert.equal(init?.referrerPolicy, 'no-referrer');
        assert.equal(new Headers(init.headers).has('Authorization'), false);
        assert.equal(new Headers(init.headers).has('cookie'), false);
        const u = new URL(String(url)), key = decodeURIComponent(u.pathname.slice(1)), n = Number(u.searchParams.get('part'));
        if (init.method === 'PUT') {
            putCalls++;
            const data = new Uint8Array(await new Response(init.body).arrayBuffer());
            if (n)
                saved.set(n, { etag: '"' + sha(data) + '"', data });
            else
                objects.set(key, data);
            if (scenario === 'lost_part_ack' && putCalls === 2)
                throw new Error('secret signed URL');
            return new Response(null, { headers: { ETag: '"' + sha(data) + '"' } });
        }
        const data = objects.get(key) ?? bytes;
        return new Response(init.method === 'HEAD' ? null : data, { headers: { 'Content-Length': String(data.length), 'Content-Type': 'text/csv', ETag: '"object"' } });
    };
    const client = createClient(f.base_url, { token, fetch, retry: { maxAttempts: 4, backoffMs: 0 } }), files = client.storage.files({ appSlug: f.app_slug, bucket: f.bucket, dataFetch });
    return { files, client, fetch, dataFetch, saved, objects, counts: () => ({ putCalls, completes, reads }) };
}
for (const c of f.cases)
    test('shared file protocol: ' + c.scenario, async () => {
        const h = harness(c.scenario), operation = h.files.upload(f.key, source(), { ...options, contentType: 'text/csv' });
        if (c.error)
            await assert.rejects(operation, code(c.error));
        else {
            const r = await operation;
            assert.equal(r.size_bytes, bytes.length);
            assert.deepEqual(h.objects.get(f.key), Buffer.from(bytes));
        }
    });
for (const scenario of ['lost_part_ack', 'lost_complete_ack'])
    test('resume after ' + scenario + ' recovers without blind replay', async () => {
        const h = harness(scenario);
        let checkpoint: FileCheckpoint | undefined;
        await assert.rejects(h.files.upload(f.key, source(), { ...options, contentType: 'text/csv', onCheckpoint: c => { checkpoint = c; } }), code('acceptance_unknown'));
        assert.ok(checkpoint);
        const before = h.counts();
        assert.equal(JSON.stringify(checkpoint).includes('objects.test'), false);
        await h.files.resume(parseFileCheckpoint(JSON.parse(JSON.stringify(checkpoint))), source(), options);
        assert.deepEqual(h.objects.get(f.key), Buffer.from(bytes));
        assert.equal(h.counts().completes, 1);
        if (scenario === 'lost_part_ack')
            assert.equal(h.counts().putCalls, Math.ceil(bytes.length / f.part_size) + 1);
        else
            assert.equal(h.counts().putCalls, before.putCalls);
    });
test('resume rejects changed same-length source and another bucket before transfer', async () => {
    const h = harness('lost_part_ack');
    let checkpoint: FileCheckpoint | undefined;
    await assert.rejects(h.files.upload(f.key, source(), { ...options, contentType: 'text/csv', onCheckpoint: c => { checkpoint = c; } }));
    assert.ok(checkpoint);
    const before = h.counts().putCalls, changed = bytes.slice();
    changed[0] = 90;
    await assert.rejects(h.files.resume(checkpoint, blobFileSource(new Blob([changed])), options), code('source_changed'));
    await assert.rejects(h.files.resume({ ...checkpoint, bucket: f.app_id }, source(), options), code('invalid_checkpoint'));
    assert.equal(h.counts().putCalls, before);
});
test('source mutation between fingerprint and part upload is refused before PUT', async () => {
    const h = harness();
    let reads = 0;
    const mutable = { size: bytes.length, async read(a: number, b: number) {
            const v = bytes.slice(a, b);
            if (++reads > Math.ceil(bytes.length / f.part_size))
                v[0] = 90;
            return v;
        } };
    await assert.rejects(h.files.upload(f.key, mutable, { ...options, contentType: 'text/csv' }), code('source_changed'));
    assert.equal(h.counts().putCalls, 0);
});
test('limits, cancellation, deadline and callbacks bound every stage', async () => {
    const h = harness();
    await assert.rejects(h.files.upload(f.key, source(), { ...options, maxBytes: 1 }), code('limit'));
    assert.equal(h.counts().reads, 0);
    const cancel = new AbortController();
    cancel.abort();
    await assert.rejects(h.files.upload(f.key, source(), { ...options, signal: cancel.signal }), code('cancelled'));
    await assert.rejects(h.files.upload(f.key, source(), { ...options, maxParts: 1 }), code('limit'));
    await assert.rejects(h.files.upload(f.key, source(), { ...options, onCheckpoint: () => { throw new Error('secret'); } }), code('callback_failed'));
    const hanging = { size: bytes.length, read: () => new Promise<Uint8Array>(() => { }) };
    await assert.rejects(h.files.upload(f.key, hanging, { ...options, timeoutMs: 20 }), code('deadline'));
    assert.equal(h.counts().putCalls, 0);
});
test('PUT persists receipt before dispatch and validates exact attempt identity', async () => {
    const h = harness();
    let receipt = '';
    await h.files.put(f.key, bytes, { ...options, onReceipt: id => { receipt = id; assert.equal(h.counts().putCalls, 0); } });
    assert.equal(receipt, f.receipt_id);
    await assert.rejects(harness('wrong_receipt').files.put(f.key, bytes, options), code('invalid_response'));
    await assert.rejects(harness('pending_receipt').files.put(f.key, bytes, options), code('poll_limit'));
    await h.files.put('alice/empty', new Uint8Array(), options);
});
test('download streams, caps bytes, reads metadata, preserves cursor, deletes exact key', async () => {
    const h = harness(), chunks: Uint8Array[] = [];
    assert.equal(await h.files.downloadTo(f.key, b => { chunks.push(b); }, options), bytes.length);
    assert.deepEqual(Buffer.concat(chunks), Buffer.from(bytes));
    await assert.rejects(h.files.download(f.key, { ...options, maxBytes: 1 }), code('limit'));
    assert.equal((await h.files.metadata(f.key, options)).size_bytes, bytes.length);
    const page = await h.files.list(options);
    assert.equal(page.next_cursor, f.cursor);
    await h.files.list({ ...options, cursor: page.next_cursor });
    await h.files.delete('alice/a?b#c.csv', options);
});
test('independent bucket credentials remain pinned during concurrent transfers and client mutation', async () => {
    const a = harness('success', 'alice'), b = harness('success', 'bob');
    a.client.setToken('new');
    a.client.setBaseURL('https://other.test');
    await Promise.all([a.files.upload(f.key, source(), { ...options, contentType: 'text/csv' }), b.files.upload(f.key, source(), { ...options, contentType: 'text/csv' })]);
    assert.equal(a.counts().putCalls, b.counts().putCalls);
});
test('managed JobStore uses exact owned app URI, verifies bytes and refuses outside references', async () => {
    const h = harness(), store = await h.files.jobStore({ timeoutMs: 3000 }), signal = new AbortController().signal, context = { inputId: 'row', attempt: 1, signal }, digest = sha(bytes);
    const uri = await store.put(bytes, digest, context);
    assert.equal(uri, `obj://${f.app_id}/${f.bucket}/jobs/${digest}`);
    assert.deepEqual(await store.get(uri, context), bytes);
    await store.put(bytes, digest, context);
    assert.equal(h.counts().putCalls, 1);
    await assert.rejects(store.get(uri.replace(f.app_id, f.bucket), context), e => e instanceof JobError && e.code === 'invalid_reference');
    h.objects.set('jobs/' + digest, new Uint8Array([1]));
    await assert.rejects(store.get(uri, context), e => e instanceof JobError && e.code === 'integrity_failed');
});
test('portable source fingerprint matches shared vector', async () => {
    const v = JSON.parse(readFileSync(new URL('../../../request-contracts/files-portable.json', import.meta.url), 'utf8'));
    assert.equal(await fileSourceDigest(source(), new AbortController().signal), v.fixed_block_digest);
    assert.deepEqual(parseFileCheckpoint(v.checkpoint), v.checkpoint);
});
test('deadlines cover response bodies and stalled checkpoint callbacks', async () => {
    const h = harness();
    let closed = false;
    const files = createClient(f.base_url, { token: 'alice', fetch: h.fetch }).storage.files({ appSlug: f.app_slug, bucket: f.bucket, dataFetch: async () => new Response(new ReadableStream({ start(c) { c.enqueue(new Uint8Array([1])); }, cancel() { closed = true; } })) });
    await assert.rejects(files.download(f.key, { timeoutMs: 20 }), code('deadline'));
    assert.equal(closed, true);
    await assert.rejects(h.files.upload(f.key, source(), { timeoutMs: 20, onCheckpoint: () => new Promise(() => { }) }), code('deadline'));
    assert.equal(h.counts().putCalls, 0);
});
test('uncertain PUT is recovered through its saved receipt without another transfer', async () => {
    const h = harness();
    let id = '';
    const files = createClient(f.base_url, { token: 'alice', fetch: h.fetch }).storage.files({ appSlug: f.app_slug, bucket: f.bucket, dataFetch: async (url, init) => { await h.dataFetch(url, init); throw new Error('lost response'); } });
    await assert.rejects(files.put(f.key, bytes, { ...options, onReceipt: value => { id = value; } }), code('acceptance_unknown'));
    await files.confirm(id, f.key, bytes.length, options);
    assert.equal(h.counts().putCalls, 1);
});
test('PUT snapshots caller bytes and explicit abort discards the session', async () => {
    const h = harness(), data = bytes.slice(), pending = h.files.put(f.key, data, options);
    data.fill(0);
    await pending;
    assert.deepEqual(h.objects.get(f.key), bytes);
    let checkpoint: FileCheckpoint | undefined;
    await assert.rejects(h.files.upload(f.key, source(), { ...options, onCheckpoint: c => { checkpoint = c; throw new Error('stop'); } }), code('callback_failed'));
    assert.ok(checkpoint);
    await h.files.abort(checkpoint, options);
    await assert.rejects(h.files.resume(checkpoint, source(), options), code('session_closed'));
});
