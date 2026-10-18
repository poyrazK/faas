const { test } = require('node:test');
const assert = require('node:assert/strict');
const { webcrypto, randomUUID } = require('node:crypto');
const { Receipts } = require('./portal-core.js');
const scope = [randomUUID(), randomUUID(), randomUUID(), 'export'];
const quote = 'a'.repeat(64);
function fixture() {
  const data = new Map(); let tail = Promise.resolve();
  const storage = { get length() { return data.size; }, key: index => [...data.keys()][index] ?? null, getItem: k => data.get(k) ?? null, setItem: (k, v) => data.set(k, v) };
  const locks = { request: (_key, task) => { const next = tail.then(task); tail = next.catch(() => {}); return next; } };
  return { data, storage, options: { storage, locks, crypto: { subtle: webcrypto.subtle, randomUUID }, maxBytes: 8192, maxEntries: 64, replaySeconds: 86400, now: () => 1000000 } };
}
test('reload retains request identity and fingerprint without credentials or request contents', async () => {
  const f = fixture(), first = new Receipts(f.options);
  const saved = await first.lock(scope, () => first.prepare(scope, { count: 8, confidential: 'private value' }, quote));
  const raw = [...f.data.values()][0];
  assert.ok(!raw.includes('private value')); assert.ok(!raw.includes('count')); assert.ok(!raw.includes('credential'));
  const reloaded = new Receipts(f.options);
  assert.deepEqual(await reloaded.prepare(scope, { confidential: 'private value', count: 8 }, 'b'.repeat(64)), saved);
  await assert.rejects(reloaded.prepare(scope, { count: 9 }, quote), /original request details/);
  assert.equal(reloaded.load([scope[0], scope[1], randomUUID(), 'export']), null);
});
test('acknowledgements remain durable and stale tabs cannot replace another request', async () => {
  const f = fixture(), first = new Receipts(f.options), second = new Receipts(f.options);
  const receipt = await first.prepare(scope, { count: 8 }, quote), id = randomUUID();
  second.acknowledge(scope, receipt, id);
  assert.equal(first.load(scope).operationID, id);
  const next = await first.prepare(scope, { count: 9 }, quote);
  assert.notEqual(next.key, receipt.key);
  assert.throws(() => second.acknowledge(scope, receipt, id), /another tab/);
});
test('storage failures stop preparation before a submission can be sent', async () => {
  const f = fixture();
  f.options.storage = { getItem: () => null, setItem: () => { throw new Error('storage denied'); } };
  await assert.rejects(new Receipts(f.options).prepare(scope, { count: 8 }, quote), /storage denied/);
  f.options.storage = { getItem: () => null, setItem: () => {} };
  await assert.rejects(new Receipts(f.options).prepare(scope, {}, quote), /could not save/);
  assert.throws(() => new Receipts({ ...f.options, locks: null }).lock(scope, () => {}), /supported browser/);
});
test('expired or altered recovery records never authorize a fresh request', async () => {
  const f = fixture(), store = new Receipts(f.options), receipt = await store.prepare(scope, {}, quote);
  const expired = new Receipts({ ...f.options, now: () => receipt.replayNotAfter });
  await assert.rejects(expired.prepare(scope, {}, quote), /safe retry window/);
  for (const change of [{ input: { secret: 1 } }, { credential: 'secret' }, { scope: [scope[0], scope[1], randomUUID(), 'export'] }, { replayNotAfter: Number.MAX_SAFE_INTEGER }, { key: '../foreign' }]) {
    f.storage.setItem(store.storageKey(scope), JSON.stringify({ ...receipt, ...change }));
    assert.throws(() => store.load(scope), /invalid/);
  }
});
test('browser locks serialize concurrent request preparations to the same saved identity', async () => {
  const f = fixture(), one = new Receipts(f.options), two = new Receipts(f.options);
  const saved = await Promise.all([one.lock(scope, () => one.prepare(scope, {}, quote)), two.lock(scope, () => two.prepare(scope, {}, quote))]);
  assert.equal(saved[0].key, saved[1].key);
});
test('recovery includes retired features and stops when the scoped receipt bound is full', async () => {
  const f = fixture(), store = new Receipts({ ...f.options, maxEntries: 2 });
  await store.prepare(scope, { count: 8 }, quote);
  await store.prepare([...scope.slice(0, 3), 'retired-export'], {}, quote);
  assert.equal(store.list(scope.slice(0, 3)).length, 2);
  assert.equal(store.list([scope[0], scope[1], randomUUID()]).length, 0);
  await assert.rejects(store.prepare([...scope.slice(0, 3), 'third-feature'], {}, quote), /Too many saved/);
});
