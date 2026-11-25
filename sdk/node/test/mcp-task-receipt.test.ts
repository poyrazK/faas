import assert from 'node:assert/strict';
import { chmod, mkdtemp, readFile, readdir, rm, stat, symlink, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import {
  createMcpTaskGroup, createMcpTaskReceiptTransport, resumeMcpTaskFromReceipt, retryMcpTaskFromReceipt,
  submitMcpTaskWithReceipt, watchMcpTaskGroup,
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

function connection(
  reply: (request: Wire) => Promise<object>, url = endpoint,
  credential: string | (() => string) = 'private-fixture-credential',
): McpTaskReceiptConnection {
  let id = 0;
  const transport = createMcpTaskReceiptTransport(FixtureTransport, url, {
    onInsufficientScope: 'throw',
    fetch: async (_, init) => {
      const message = JSON.parse(String(init?.body)) as Wire;
      const result = await reply(message);
      if (result instanceof Response) return result;
      return Response.json({ jsonrpc: '2.0', id: message.id, result });
    },
  });
  return {
    transport,
    getNegotiatedProtocolVersion: () => protocol,
    getServerCapabilities: () => ({ extensions: { 'io.modelcontextprotocol/tasks': {},
      'io.gregale/task-submission': {}, 'io.gregale/task-submission-lookup': {} } }),
    async request({ method, params }: Request, _, { signal }) {
      const body = JSON.stringify({ jsonrpc: '2.0', id: ++id, method, params: {
        ...params, _meta: { ...('_meta' in params ? params._meta : {}), 'io.modelcontextprotocol/protocolVersion': protocol },
      } });
      const send = () => transport.options!.fetch!(transport.url, {
        method: 'POST', signal, headers: { 'Mcp-Method': method, 'Mcp-Protocol-Version': protocol,
          Authorization: `Bearer ${typeof credential === 'function' ? credential() : credential}` },
        body,
      });
      const response = await send();
      if (response.status === 401 && typeof credential === 'function') return (await (await send()).json()).result;
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
    assert.deepEqual(Object.keys(saved).sort(), ['credentialProof', 'endpointSha256', 'requestSha256', 'submissionKey', 'version']);
    assert.equal(saved.version, 2);
    assert.match(saved.credentialProof, /^[a-f0-9]{64}$/);
    assert.match(saved.requestSha256, /^[a-f0-9]{64}$/);
    assert.match(saved.submissionKey, /^[a-f0-9-]+-7[a-f0-9]{3}-[89ab][a-f0-9-]+$/);
    assert.equal(saved.submissionKey, (message.params._meta as Record<string, unknown>)['io.gregale/task-submission-key']);
    assert.ok(!text.includes(endpoint) && !text.includes('private fixture') && !text.includes('private-fixture-credential'));
    assert.equal(Object.hasOwn(message.params._meta as object, 'io.gregale/local-task-receipt-operation'), false);
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
    await writeFile(path, JSON.stringify({ ...saved, version: 3 }));
    return { resultType: 'task', ...task };
  });
  assert.deepEqual(await submitMcpTaskWithReceipt(client, path, submission), {
    outcome: 'accepted', task: { ...task, nextAction: 'poll_status' }, taskIdRecorded: false,
  });
  assert.equal(JSON.parse(await readFile(path, 'utf8')).version, 3);
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

test('explicit retry uses the saved key and original canonical request', async t => {
  const path = join(await folder(t), 'task.json');
  const initial = { ...submission, arguments: { nested: { a: 'one', b: [2, 3] }, report: 'weekly' }, deferFormInput: true };
  const lost = connection(async () => { throw new Error('lost initial reply'); });
  assert.deepEqual(await submitMcpTaskWithReceipt(lost, path, initial), { outcome: 'unconfirmed' });
  const seed = JSON.parse(await readFile(path, 'utf8'));
  const retry = { ...initial, arguments: { report: 'weekly', nested: { b: [2, 3], a: 'one' } } };
  let sent = 0;
  const fresh = connection(async ({ method, params }) => {
    assert.equal(method, 'tools/call');
    sent++;
    assert.equal((params._meta as Record<string, unknown>)['io.gregale/task-submission-key'], seed.submissionKey);
    return { resultType: 'task', ...task };
  });

  const attempts = await Promise.all([
    retryMcpTaskFromReceipt(fresh, path, retry), retryMcpTaskFromReceipt(fresh, path, retry),
  ]);
  for (const result of attempts) {
    assert.deepEqual(result, { outcome: 'accepted', task: { ...task, nextAction: 'poll_status' }, taskIdRecorded: true });
  }
  assert.equal(sent, 2);
  assert.deepEqual(JSON.parse(await readFile(path, 'utf8')), { ...seed, taskId: task.taskId });
  const conflicting = connection(async () => ({ resultType: 'task', ...task, taskId: 'conflicting Task' }));
  await assert.rejects(retryMcpTaskFromReceipt(conflicting, path, retry), { code: 'task_mismatch' });
  assert.equal(JSON.parse(await readFile(path, 'utf8')).taskId, task.taskId);
});

test('changed request and credential are rejected before transport', async t => {
  const path = join(await folder(t), 'task.json');
  const lost = connection(async () => { throw new Error('lost initial reply'); });
  await submitMcpTaskWithReceipt(lost, path, submission);
  const seed = await readFile(path, 'utf8');
  let sent = 0;
  const rejectWire = async () => { sent++; return { resultType: 'task', ...task }; };
  const original = connection(rejectWire);
  const changed = connection(rejectWire, endpoint, 'a-new-credential');
  for (const input of [
    { ...submission, toolName: 'other_tool' },
    { ...submission, arguments: { report: 'changed' } },
    { ...submission, deferFormInput: true },
  ]) {
    await assert.rejects(retryMcpTaskFromReceipt(original, path, input), { code: 'request_mismatch' });
  }
  await assert.rejects(retryMcpTaskFromReceipt(changed, path, submission), { code: 'credential_mismatch' });
  await assert.rejects(retryMcpTaskFromReceipt(connection(rejectWire, endpoint + '/other'), path, submission),
    { code: 'endpoint_mismatch' });
  assert.equal(await readFile(path, 'utf8'), seed);
  assert.equal(sent, 0);
});

test('legacy receipts remain resumable and the retry window is checked before sending', async t => {
  const directory = await folder(t);
  const path = join(directory, 'task.json');
  const initial = connection(async () => { throw new Error('lost initial reply'); });
  await submitMcpTaskWithReceipt(initial, path, submission);
  const receipt = JSON.parse(await readFile(path, 'utf8'));
  let sent = 0;
  const client = connection(async ({ method }) => { sent++; assert.equal(method, 'tasks/get'); return task; });
  const legacy = join(directory, 'legacy.json');
  await writeFile(legacy, JSON.stringify({
    version: 1, endpointSha256: receipt.endpointSha256, submissionKey: receipt.submissionKey, taskId: task.taskId,
  }), { mode: 0o600 });
  await assert.rejects(retryMcpTaskFromReceipt(client, legacy, submission), { code: 'retry_unavailable' });
  assert.equal((await resumeMcpTaskFromReceipt(client, legacy)).taskId, task.taskId);
  assert.equal(sent, 1);

  const expiredKey = '019b0000-0000-7123-8123-0123456789ab';
  await writeFile(path, JSON.stringify({ ...receipt, submissionKey: expiredKey }));
  await assert.rejects(retryMcpTaskFromReceipt(client, path, submission), { code: 'retry_window_closed' });
  assert.equal(sent, 1);
});

test('an auth refresh cannot send a receipt-backed mutation with a different bearer', async t => {
  const path = join(await folder(t), 'task.json');
  let bearer = 'private-fixture-credential';
  let sent = 0;
  const refreshing = connection(async () => {
    sent++;
    bearer = 'renewed-fixture-credential';
    return new Response(null, { status: 401 });
  }, endpoint, () => bearer);
  await assert.rejects(submitMcpTaskWithReceipt(refreshing, path, submission), { code: 'credential_mismatch' });
  assert.equal(sent, 1);
  const saved = JSON.parse(await readFile(path, 'utf8'));
  assert.equal(saved.version, 2);
  assert.equal(saved.taskId, undefined);
  assert.equal((await retryMcpTaskFromReceipt(
    connection(async () => ({ resultType: 'task', ...task })), path, submission,
  )).outcome, 'accepted');
});

test('Task groups recover lost replies and remember only the exact acknowledged update', async t => {
  const directory = await folder(t);
  const path = join(directory, 'receipt');
  const group = join(directory, 'group');
  assert.deepEqual(await submitMcpTaskWithReceipt(connection(async () => { throw new Error('lost reply'); }),
    path, submission), { outcome: 'unconfirmed' });
  const before = await readFile(path, 'utf8');
  const { submissionKey } = JSON.parse(before);
  const creations = await Promise.allSettled([
    createMcpTaskGroup(group, [path]), createMcpTaskGroup(group, [path]),
  ]);
  assert.equal(creations.filter(result => result.status === 'fulfilled').length, 1);
  assert.ok(creations.some(result => result.status === 'rejected' && result.reason.code === 'group_exists'));
  assert.equal((await stat(group)).mode & 0o777, 0o700);
  const manifest = join(group, 'group.json');
  assert.equal((await stat(manifest)).mode & 0o777, 0o600);
  const saved = await readFile(manifest, 'utf8');
  assert.deepEqual(JSON.parse(saved).members, [{ submissionKey }]);
  for (const privateValue of [endpoint, 'private-fixture-credential', 'private fixture arguments', 'credentialProof', path]) {
    assert.equal(saved.includes(privateValue), false);
  }

  const requests: string[] = [];
  let current: Record<string, unknown> = { ...task };
  const connect = () => connection(async ({ method, params }) => {
    requests.push(method);
    if (method === lookup) {
      assert.equal(params.submissionKey, submissionKey);
      return task;
    }
    assert.equal(method, 'tasks/get');
    assert.equal(params.taskId, task.taskId);
    return current;
  }, endpoint, 'refreshed-credential');

  const first = watchMcpTaskGroup(connect(), group);
  const working = (await first.next()).value;
  assert.equal(working?.outcome, 'available');
  assert.equal(working.task.status, 'working');
  await first.return();

  const replay = watchMcpTaskGroup(connect(), group);
  const repeated = (await replay.next()).value;
  assert.equal(repeated?.outcome, 'available');
  assert.equal(repeated.updateId, working.updateId);
  await Promise.all([working.acknowledge(), repeated.acknowledge()]);
  await replay.return();

  current = { ...task, status: 'input_required', lastUpdatedAt: '2026-11-24T00:01:00Z', inputRequests: { private: true } };
  const afterPause = watchMcpTaskGroup(connect(), group);
  const paused = (await afterPause.next()).value;
  assert.equal(paused?.outcome, 'available');
  assert.equal(paused.task.status, 'input_required');
  assert.equal(Object.hasOwn(paused.task, 'inputRequests'), false);
  assert.notEqual(paused.updateId, working.updateId);
  await afterPause.return();

  await working.acknowledge();
  const pausedAgain = watchMcpTaskGroup(connect(), group);
  const unacknowledged = (await pausedAgain.next()).value;
  assert.equal(unacknowledged?.outcome, 'available');
  assert.equal(unacknowledged.updateId, paused.updateId);
  await unacknowledged.acknowledge();
  assert.equal((await pausedAgain.next()).done, true);

  current.pollIntervalMs = 1_000;
  assert.equal((await watchMcpTaskGroup(connect(), group).next()).done, true);
  assert.deepEqual(requests, Array.from({ length: 5 }, () => [lookup, 'tasks/get']).flat());
  assert.equal(await readFile(path, 'utf8'), before);
});

test('Task groups can poll through pauses and deliver a later input notice', async t => {
  const directory = await folder(t);
  const receipt = join(directory, 'receipt');
  const group = join(directory, 'group');
  await submitMcpTaskWithReceipt(connection(async () => ({ resultType: 'task', ...task })), receipt, submission);
  await createMcpTaskGroup(group, [receipt]);

  const paused = { ...task, status: 'input_required', pollIntervalMs: 0, inputRequests: { private: true } };
  const snapshots = [
    paused,
    paused,
    { ...paused, status: 'working', lastUpdatedAt: '2026-11-24T00:01:00Z' },
    { ...paused, lastUpdatedAt: '2026-11-24T00:02:00Z' },
    { ...paused, status: 'completed', lastUpdatedAt: '2026-11-24T00:03:00Z', result: { private: true } },
  ];
  let reads = 0;
  const client = connection(async ({ method, params }) => {
    assert.equal(method, 'tasks/get');
    assert.equal(params.taskId, task.taskId);
    const snapshot = snapshots[reads++];
    assert.ok(snapshot);
    return snapshot;
  });
  const statuses = [];
  const ids = new Set<string>();
  for await (const event of watchMcpTaskGroup(client, group, { followInput: true, timeoutMs: 5_000 })) {
    assert.equal(event.outcome, 'available');
    assert.equal(Object.hasOwn(event.task, 'inputRequests'), false);
    assert.equal(Object.hasOwn(event.task, 'result'), false);
    statuses.push(event.task.status);
    ids.add(event.updateId);
    await event.acknowledge();
  }
  assert.deepEqual(statuses, ['input_required', 'working', 'input_required', 'completed']);
  assert.equal(ids.size, statuses.length);
  assert.equal(reads, snapshots.length);
});

test('Task groups pin the endpoint and isolate inaccessible receipts without readable metadata', async t => {
  const directory = await folder(t);
  const first = join(directory, 'first');
  const other = join(directory, 'other');
  const group = join(directory, 'group');
  const confirm = async () => ({ resultType: 'task', ...task });
  await submitMcpTaskWithReceipt(connection(confirm), first, submission);
  await submitMcpTaskWithReceipt(connection(confirm, endpoint + '/second'), other, submission);
  await assert.rejects(createMcpTaskGroup(group, [first, other]), { code: 'invalid_group' });
  await assert.rejects(createMcpTaskGroup(group, [first, first]), { code: 'invalid_group' });
  await createMcpTaskGroup(group, [first]);

  let requested = 0;
  const wrong = connection(async () => { requested++; return task; }, endpoint + '/wrong');
  await assert.rejects(watchMcpTaskGroup(wrong, group).next(), { code: 'endpoint_mismatch' });
  assert.equal(requested, 0);
  const link = join(directory, 'linked');
  await symlink(group, link);
  await assert.rejects(watchMcpTaskGroup(wrong, link).next(), { code: 'invalid_group' });
  await chmod(join(group, 'group.json'), 0o644);
  await assert.rejects(watchMcpTaskGroup(wrong, group).next(), { code: 'invalid_group' });
  await chmod(join(group, 'group.json'), 0o600);

  const denied = connection(async () => { throw { code: -32002 }; });
  const watch = watchMcpTaskGroup(denied, group);
  const refused = (await watch.next()).value;
  assert.equal(refused?.outcome, 'unavailable');
  assert.equal(refused.receiptIndex, 0);
  assert.deepEqual(Object.keys(refused).sort(), ['error', 'outcome', 'receiptIndex']);
  assert.equal((await watch.next()).done, true);
  assert.deepEqual(await readdir(group), ['group.json']);

  const owner = connection(async () => task);
  const original = watchMcpTaskGroup(owner, group);
  const event = (await original.next()).value;
  assert.equal(event?.outcome, 'available');
  await original.return();
  await rm(group, { recursive: true });
  await createMcpTaskGroup(group, [first]);
  await assert.rejects(event.acknowledge(), { code: 'group_changed' });
  assert.deepEqual(await readdir(group), ['group.json']);
});
