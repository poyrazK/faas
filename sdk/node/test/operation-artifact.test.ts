import test from 'node:test';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { GregaleOperations, type OperationArtifactInput, type PreparedOperationArtifact } from '../src/operations-runtime.js';
import { OperationHTTPError, type Operation, type OperationArtifactReport } from '../src/customer-operations.js';

const id = '11111111-1111-4111-8111-111111111111';
const invocation = '22222222-2222-4222-8222-222222222222';
const bucket = '33333333-3333-4333-8333-333333333333';
const headers = { 'X-Gregale-Customer-Operation-Id': id, 'X-Faas-Invocation-Id': invocation,
  'X-Gregale-Operation-Attempt': '2', 'X-Gregale-Operation-Capability': 'a'.repeat(64) };
const input: OperationArtifactInput = { name: 'export.csv', uri: `obj://${id}/${bucket}/exports/../customer.csv`, data: 'id,value\n1,π\n', maxBytes: 1024, report_id: 'export-file' };
const snapshot: Operation = { id, name: 'export', generation: 1, state: 'running', completion_delivery: { state: 'not_requested', attempts: 0 },
  cancellation_requested: false, latest_sequence: 2, created_at: '2026-10-05T00:00:00Z', updated_at: '2026-10-05T00:00:00Z', expires_at: '2026-10-12T00:00:00Z' };

function runtime(report: (body: OperationArtifactReport, proof: Headers) => Promise<Response> = async () => Response.json(snapshot)) {
  let assertions = 0, reports = 0;
  const instance = new GregaleOperations({ apiURL: 'https://api.example.test', identityEndpoint: 'http://127.0.0.1/identity', fetch: async (url, init) => {
    const target = new URL(String(url));
    if (target.hostname === '127.0.0.1') return Response.json({ access_token: `workload-${++assertions}` });
    assert.equal(target.pathname, `/v1/runtime/operations/${id}/artifacts`);
    reports++; return report(JSON.parse(String(init?.body)) as OperationArtifactReport, new Headers(init?.headers));
  } });
  return { instance, counts: () => ({ assertions, reports }) };
}

test('prepared artifact snapshots bytes and metadata before writing, hashes UTF-8 and preserves opaque keys', async () => {
  const api = runtime();
  await api.instance.runRequest(headers, async () => {
    const data = Buffer.from(input.data as string), options = { ...input, data };
    const file = api.instance.prepareArtifact(options);
    const original = Buffer.from(data);
    data.fill(0); options.name = 'changed.csv'; options.uri = 'changed';
    assert.equal(file.report.name, input.name); assert.equal(file.report.uri, input.uri);
    assert.equal(file.report.size_bytes, original.length);
    assert.equal(file.report.sha256, 'sha256:' + createHash('sha256').update(original).digest('hex'));
    assert.ok(Object.isFrozen(file.report)); assert.ok(Object.isFrozen(file));
    let writes = 0;
    await file.uploadAndAttach(async upload => {
      writes++; assert.deepEqual(Buffer.from(upload.bytes), original);
      assert.equal(upload.report, file.report); upload.bytes.fill(0);
    });
    await file.uploadAndAttach(async () => { throw Error('uploaded twice'); });
    assert.equal(writes, 1);
  });
  assert.deepEqual(api.counts(), { assertions: 1, reports: 1 });
});

test('lost attachment response replays the same report with fresh identity and never writes twice', async () => {
  const bodies: OperationArtifactReport[] = [], tokens: string[] = [];
  const api = runtime(async (body, proof) => {
    bodies.push(body); tokens.push(proof.get('Authorization')!);
    assert.equal(proof.get('X-Faas-Invocation-Id'), invocation);
    assert.equal(proof.get('X-Gregale-Operation-Attempt'), '2');
    assert.equal(proof.get('X-Gregale-Operation-Capability'), 'a'.repeat(64));
    if (bodies.length === 1) throw Error('attachment response lost');
    return Response.json(snapshot);
  });
  await api.instance.runRequest(headers, async () => {
    const file = api.instance.prepareArtifact({ ...input, report_id: undefined }); let writes = 0;
    const write = async () => { writes++; };
    await assert.rejects(file.uploadAndAttach(write), /response lost/);
    assert.deepEqual(await file.uploadAndAttach(write), snapshot);
    assert.equal(writes, 1); assert.deepEqual(bodies[0], bodies[1]);
    assert.ok(bodies[0]?.report_id);
  });
  assert.deepEqual(tokens, ['Bearer workload-1', 'Bearer workload-2']);
});

test('lost upload response reconciles by attaching and does not automatically repeat the external write', async () => {
  const api = runtime();
  await api.instance.runRequest(headers, async () => {
    const file = api.instance.prepareArtifact(input); let writes = 0;
    const write = async () => { writes++; throw Error('upload outcome unknown'); };
    await assert.rejects(file.uploadAndAttach(write), /outcome unknown/);
    assert.deepEqual(api.counts(), { assertions: 0, reports: 0 });
    await file.uploadAndAttach(write);
    assert.equal(writes, 1); assert.deepEqual(api.counts(), { assertions: 1, reports: 1 });
  });
});

test('missing source after an uncertain upload remains a reporting error without another write', async () => {
  const api = runtime(async () => Response.json({ code: 'not_found' }, { status: 404 }));
  await api.instance.runRequest(headers, async () => {
    const file = api.instance.prepareArtifact(input); let writes = 0;
    await assert.rejects(file.uploadAndAttach(async () => { writes++; throw Error('upload lost'); }));
    await assert.rejects(file.attach(), error => error instanceof OperationHTTPError && error.status === 404);
    await assert.rejects(file.uploadAndAttach(async () => { writes++; }), OperationHTTPError);
    assert.equal(writes, 1);
  });
});

test('duplicate upload and attach calls share the pending transfer', async () => {
  const api = runtime(); let finish!: () => void;
  const uploading = new Promise<void>(resolve => { finish = resolve; });
  await api.instance.runRequest(headers, async () => {
    const file = api.instance.prepareArtifact(input); let writes = 0;
    const first = file.uploadAndAttach(async () => { writes++; await uploading; });
    assert.equal(file.uploadAndAttach(async () => { writes++; }), first);
    assert.equal(file.attach(), first);
    assert.equal(api.counts().reports, 0);
    finish(); await first; assert.equal(writes, 1); assert.equal(api.counts().reports, 1);
  });
});

test('an upload hook can observe its pending attachment without reporting before the write finishes', async () => {
  const api = runtime();
  await api.instance.runRequest(headers, async () => {
    const file = api.instance.prepareArtifact(input);
    const pending = file.uploadAndAttach(async () => {
      assert.equal(file.attach(), pending);
      assert.equal(api.counts().reports, 0);
    });
    await pending; assert.equal(api.counts().reports, 1);
  });
});

test('attachment of an existing source does not invoke an upload callback on retries', async () => {
  const api = runtime();
  await api.instance.runRequest(headers, async () => {
    const file = api.instance.prepareArtifact(input);
    await file.attach(); await file.uploadAndAttach(async () => { throw Error('existing object overwritten'); });
    assert.equal(api.counts().reports, 1);
  });
});

test('invalid or oversized preparation performs no storage or workload requests', () => {
  const api = runtime();
  api.instance.runRequest(headers, () => {
    for (const change of [
      { maxBytes: -1 }, { maxBytes: NaN }, { maxBytes: Infinity }, { maxBytes: 0.5 }, { maxBytes: 2 },
      { name: '../export.csv' }, { name: 'x'.repeat(129) }, { name: 'π'.repeat(65) }, { name: '\ud800' },
      { report_id: '' }, { report_id: 'x'.repeat(129) }, { report_id: 'report\n' },
      { uri: 'https://storage.example.test/export?signature=secret' }, { uri: `obj://${id}/${bucket}/a%2fb` },
      { uri: `obj://${id}/${bucket}/` }, { uri: `obj://${id}/${bucket}/a?version=1` },
      { uri: `obj://${id}/${bucket}/a\n` }, { uri: `obj://${id}/${bucket}/${'x'.repeat(1025)}` },
      { data: {} },
    ]) assert.throws(() => api.instance.prepareArtifact({ ...input, ...change } as OperationArtifactInput));
    assert.equal(api.instance.prepareArtifact({ ...input, data: '', maxBytes: 0 }).report.size_bytes, 0);
  });
  assert.deepEqual(api.counts(), { assertions: 0, reports: 0 });
});

test('prepared receipts cannot be used by a different request or after leaving their request', async () => {
  const api = runtime(); let file!: PreparedOperationArtifact;
  assert.throws(() => api.instance.prepareArtifact(input), /original operation request/);
  api.instance.runRequest(headers, () => { file = api.instance.prepareArtifact(input); });
  await assert.rejects(file.attach(), /original operation request/);
  await api.instance.runRequest({ ...headers, 'X-Gregale-Operation-Attempt': '3' }, async () => {
    await assert.rejects(file.uploadAndAttach(async () => { throw Error('unexpected write'); }), /original operation request/);
  });
  assert.deepEqual(api.counts(), { assertions: 0, reports: 0 });
});

test('detached callbacks cannot upload after their handler finishes, even with inherited async context', async () => {
  const api = runtime(); let late!: Promise<void>;
  await api.instance.runRequest(headers, async () => {
    const file = api.instance.prepareArtifact(input);
    late = new Promise((resolve, reject) => {
      setImmediate(() => {
        assert.rejects(file.uploadAndAttach(async () => { throw Error('unexpected write'); }), /original operation request/).then(resolve, reject);
      });
    });
  });
  await late; assert.deepEqual(api.counts(), { assertions: 0, reports: 0 });
});

test('a mismatched attachment response cannot become the cached receipt or cause another upload', async () => {
  let reports = 0;
  const api = runtime(async () => Response.json({ ...snapshot, id: ++reports === 1 ? bucket : id }));
  await api.instance.runRequest(headers, async () => {
    const file = api.instance.prepareArtifact(input); let writes = 0;
    await assert.rejects(file.uploadAndAttach(async () => { writes++; }), /response identity changed/);
    assert.deepEqual(await file.uploadAndAttach(async () => { writes++; }), snapshot);
    assert.equal(writes, 1);
  });
});
