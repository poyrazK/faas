import assert from 'node:assert/strict';
import { chmod, mkdtemp, readFile, readdir, rm, stat, symlink, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import {
  createMcpTaskReceiptTransport, resumeMcpTaskFromReceipt, submitMcpTaskWithReceipt,
  type McpTaskConnection, type McpTaskReceiptConnection, type McpTaskSubmissionConnection,
} from '../src/preview.js';

const endpoint = 'https://mcp.example/mcp';
const protocol = '2026-07-28';
const submit = 'gregale/tasks/submit';
const lookup = 'gregale/tasks/lookup';
const submission = { toolName: 'build_report', arguments: { report: 'private fixture arguments' } };
const task = { taskId: 'original task', status: 'working', createdAt: '2026-11-24T00:00:00Z',
  lastUpdatedAt: '2026-11-24T00:00:00Z', ttlMs: 120_000 };

class FixtureTransport {
  constructor(readonly url: URL, readonly options?: {
    fetch?: typeof fetch;
    onInsufficientScope?: 'throw' | 'reauthorize';
  }) {}
}

type Request = Parameters<McpTaskConnection['request']>[0] | Parameters<McpTaskSubmissionConnection['request']>[0];
type Wire = { id: number; method: string; params: Record<string, unknown> };

function connection(reply: (request: Wire) => Promise<object>, url = endpoint): McpTaskReceiptConnection {
  let id = 0;
  const transport = createMcpTaskReceiptTransport(FixtureTransport, url, {
    onInsufficientScope: 'throw',
    fetch: async (_, init) => {
      const message = JSON.parse(String(init?.body)) as Wire;
      const result = await reply(message);
      return Response.json({ jsonrpc: '2.0', id: message.id, result });
    },
  });
  return {
    transport,
    getNegotiatedProtocolVersion: () => protocol,
    getServerCapabilities: () => ({ extensions: { 'io.modelcontextprotocol/tasks': {},
      'io.gregale/task-submission': {}, 'io.gregale/task-submission-lookup': {} } }),
    async request({ method, params }: Request, _, { signal }) {
      const response = await transport.options!.fetch!(transport.url, {
        method: 'POST', signal, headers: { 'Mcp-Method': method, 'Mcp-Protocol-Version': protocol,
          Authorization: 'Bearer private fixture credential' },
        body: JSON.stringify({ jsonrpc: '2.0', id: ++id, method, params: {
          ...params, _meta: { ...('_meta' in params ? params._meta : {}), 'io.modelcontextprotocol/protocolVersion': protocol },
        } }),
      });
      return (await response.json()).result;
    },
  };
}

async function folder(t: { after: (fn: () => Promise<void>) => void }): Promise<string> {
  const directory = await mkdtemp(join(tmpdir(), 'gregale-task-receipt-test-'));
  t.after(() => rm(directory, { recursive: true, force: true }));
  return directory;
}

test('publishes one private receipt before sending and records the returned ID atomically', async t => {
  const directory = await folder(t);
  const path = join(directory, 'task.json');
  let submissions = 0;
  const client = connection(async message => {
    submissions++;
    assert.equal(message.method, 'tools/call');
    assert.equal((await stat(path)).mode & 0o777, 0o600);
    const text = await readFile(path, 'utf8');
    const saved = JSON.parse(text);
    assert.deepEqual(Object.keys(saved).sort(), ['endpointSha256', 'submissionKey', 'version']);
    assert.match(saved.submissionKey, /^[a-f0-9-]+-7[a-f0-9]{3}-[89ab][a-f0-9-]+$/);
    assert.equal(saved.submissionKey, (message.params._meta as Record<string, unknown>)['io.gregale/task-submission-key']);
    assert.ok(!text.includes(endpoint) && !text.includes('private fixture'));
    return { resultType: 'task', ...task };
  });

  const attempts = await Promise.allSettled([
    submitMcpTaskWithReceipt(client, path, submission), submitMcpTaskWithReceipt(client, path, submission),
  ]);
  const accepted = attempts.find(attempt => attempt.status === 'fulfilled');
  const rejected = attempts.find(attempt => attempt.status === 'rejected');
  assert.deepEqual(accepted?.value, { outcome: 'accepted', task: { ...task, nextAction: 'poll_status' }, taskIdRecorded: true });
  assert.equal(rejected?.reason.code, 'receipt_exists');
  assert.equal(submissions, 1);
  assert.equal(JSON.parse(await readFile(path, 'utf8')).taskId, task.taskId);
  assert.deepEqual(await readdir(directory), ['task.json']);

  const fresh = connection(async ({ method, params }) => {
    assert.equal(method, 'tasks/get');
    assert.equal(params.taskId, task.taskId);
    return { resultType: 'complete', ...task, status: 'completed', result: { content: [] } };
  });
  assert.equal((await resumeMcpTaskFromReceipt(fresh, path)).status, 'completed');
});

test('an unacknowledged receipt recovers using the saved key without a mutation', async t => {
  const path = join(await folder(t), 'task.json');
  const lost = connection(async () => { throw new Error('lost reply'); });
  assert.deepEqual(await submitMcpTaskWithReceipt(lost, path, submission), { outcome: 'unconfirmed' });
  const before = await readFile(path, 'utf8');
  const { submissionKey } = JSON.parse(before);
  const calls: string[] = [];
  const fresh = connection(async ({ method, params }) => {
    calls.push(method);
    if (method === lookup) {
      assert.equal(params.submissionKey, submissionKey);
      return { resultType: 'complete', ...task };
    }
    assert.equal(method, 'tasks/get');
    assert.equal(params.taskId, task.taskId);
    return { resultType: 'complete', ...task, status: 'completed', result: { content: [{ text: 'retained' }] } };
  });
  const recovered = await resumeMcpTaskFromReceipt(fresh, path);
  assert.ok(recovered.status === 'completed');
  assert.deepEqual(recovered.result, { content: [{ text: 'retained' }] });
  assert.deepEqual(calls, [lookup, 'tasks/get']);
  assert.equal(await readFile(path, 'utf8'), before);
});

test('receipts reject endpoint mismatches, symlinks and non-private files before requesting', async t => {
  const directory = await folder(t);
  const path = join(directory, 'task.json');
  await submitMcpTaskWithReceipt(connection(async () => { throw new Error('lost reply'); }), path, submission);
  const noWire = async () => assert.fail('receipt was sent over the network');
  await assert.rejects(resumeMcpTaskFromReceipt(connection(noWire, endpoint + '/other'), path), { code: 'endpoint_mismatch' });
  await assert.rejects(resumeMcpTaskFromReceipt({ ...connection(noWire), transport: undefined }, path), { code: 'unbound_connection' });
  const linked = join(directory, 'linked.json');
  await symlink(path, linked);
  await assert.rejects(resumeMcpTaskFromReceipt(connection(noWire), linked), { code: 'invalid_receipt' });
  await chmod(path, 0o644);
  await assert.rejects(resumeMcpTaskFromReceipt(connection(noWire), path), { code: 'invalid_receipt' });
  await chmod(path, 0o600);
  await writeFile(path, 'x'.repeat(4097));
  await assert.rejects(resumeMcpTaskFromReceipt(connection(noWire), path), { code: 'invalid_receipt' });
});

test('invalid submissions and failed receipt writes stop before sending', async t => {
  const directory = await folder(t);
  const client = connection(async () => assert.fail('submission reached transport'));
  await assert.rejects(submitMcpTaskWithReceipt(client, join(directory, 'invalid'), { toolName: '' }), TypeError);
  assert.deepEqual(await readdir(directory), []);
  const path = join(directory, 'existing');
  await writeFile(path, 'keep this file', { mode: 0o600 });
  await assert.rejects(submitMcpTaskWithReceipt(client, path, submission), { code: 'receipt_exists' });
  assert.equal(await readFile(path, 'utf8'), 'keep this file');
  await assert.rejects(submitMcpTaskWithReceipt(client, join(directory, 'absent', 'receipt'), submission), { code: 'receipt_io' });
});

test('a confirmed Task handle survives failure to record its ID', async t => {
  const path = join(await folder(t), 'task.json');
  const client = connection(async () => {
    // A concurrently replaced receipt must survive the acknowledgement write.
    const saved = JSON.parse(await readFile(path, 'utf8'));
    await writeFile(path, JSON.stringify({ ...saved, version: 2 }));
    return { resultType: 'task', ...task };
  });
  assert.deepEqual(await submitMcpTaskWithReceipt(client, path, submission), {
    outcome: 'accepted', task: { ...task, nextAction: 'poll_status' }, taskIdRecorded: false,
  });
  assert.equal(JSON.parse(await readFile(path, 'utf8')).version, 2);
});

test('the bound fetch checks the full destination and rejects redirects', async () => {
  let calls = 0;
  const transport = createMcpTaskReceiptTransport(FixtureTransport, endpoint, {
    fetch: async (_, init) => {
      calls++;
      assert.equal(init?.redirect, 'manual');
      return new Response(null, { status: 307, headers: { Location: endpoint + '/other' } });
    },
  });
  const init = { method: 'POST', headers: { 'Mcp-Method': 'tasks/get' } };
  await assert.rejects(transport.options!.fetch!(endpoint + '/other', init), { code: 'endpoint_mismatch' });
  assert.equal(calls, 0);
  await assert.rejects(transport.options!.fetch!(endpoint, init), { code: 'endpoint_mismatch' });
  assert.equal(calls, 1);
});
