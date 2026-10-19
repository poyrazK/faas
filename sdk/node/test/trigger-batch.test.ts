import test from 'node:test';
import assert from 'node:assert/strict';

import {
  handleTriggerBatch,
  isTriggerBatch,
  parseTriggerBatch,
  TriggerBatchError,
  type TriggerBatchRecord,
} from '../src/trigger-batch.js';

const body = {
  records: [
    { item_identifier: 'a', data: { n: 1 }, headers: { 'content-type': 'application/json' }, metadata: { channel: 'orders', sequence: '41' } },
    { item_identifier: 'b', data_base64: Buffer.from('raw bytes').toString('base64') },
    { item_identifier: 'c', data: null },
  ],
};

test('parses JSON and binary records in delivery order', () => {
  const records = parseTriggerBatch<{ n: number }>(JSON.stringify(body));
  assert.deepEqual(records.map((record) => record.itemIdentifier), ['a', 'b', 'c']);
  assert.equal(records[0]!.isJSON, true);
  assert.deepEqual(records[0]!.data, { n: 1 });
  assert.equal(records[0]!.text(), '{"n":1}');
  assert.equal(records[0]!.headers['content-type'], 'application/json');
  assert.equal(records[0]!.metadata.sequence, '41');
  assert.equal(records[1]!.isJSON, false);
  assert.equal(records[1]!.data, undefined);
  assert.equal(records[1]!.text(), 'raw bytes');
  assert.equal(records[2]!.isJSON, true, 'a JSON null payload is still JSON');
  assert.equal(records[2]!.data, null);
  assert.deepEqual(parseTriggerBatch(new TextEncoder().encode(JSON.stringify(body))).length, 3);
});

test('rejects malformed envelopes', () => {
  for (const bad of [
    'not json', { records: 'x' }, { records: [{}] },
    { records: [{ item_identifier: 'a', data: 1, data_base64: 'MQ==' }] },
    { records: [{ item_identifier: 'a', data_base64: '%%%' }] },
    { records: [{ item_identifier: 'a', data: 1 }, { item_identifier: 'a', data: 2 }] },
  ]) {
    assert.throws(() => parseTriggerBatch(bad), TriggerBatchError, JSON.stringify(bad));
  }
});

test('detects a batch delivery from its header', () => {
  assert.equal(isTriggerBatch(new Headers({ 'X-Gregale-Batch-Size': '3' })), true);
  assert.equal(isTriggerBatch({ 'X-Gregale-Batch-Size': '3' }), true);
  assert.equal(isTriggerBatch({ 'content-type': 'application/json' }), false);
});

test('reports only the records whose handler failed', async () => {
  const seen: string[] = [];
  const errors: string[] = [];
  const response = await handleTriggerBatch(body, async (record: TriggerBatchRecord) => {
    seen.push(record.itemIdentifier);
    if (record.itemIdentifier === 'b') throw new Error('boom');
  }, { onError: (_error, record) => { errors.push(record.itemIdentifier); } });
  assert.deepEqual(seen, ['a', 'b', 'c'], 'records run in delivery order');
  assert.deepEqual(errors, ['b']);
  assert.deepEqual(response, { batchItemFailures: [{ itemIdentifier: 'b' }] });
  assert.deepEqual(await handleTriggerBatch(body, () => {}), { batchItemFailures: [] });
});

test('stopOnFailure retries the failed record and every later one without running them', async () => {
  const seen: string[] = [];
  const response = await handleTriggerBatch(body, (record) => {
    seen.push(record.itemIdentifier);
    if (record.itemIdentifier === 'b') throw new Error('boom');
  }, { stopOnFailure: true });
  assert.deepEqual(seen, ['a', 'b']);
  assert.deepEqual(response.batchItemFailures.map((failure) => failure.itemIdentifier), ['b', 'c']);
});

test('runs records concurrently when asked', async () => {
  let running = 0, peak = 0;
  const response = await handleTriggerBatch(body, async (record) => {
    running++;
    peak = Math.max(peak, running);
    await new Promise((resolve) => setTimeout(resolve, 5));
    running--;
    if (record.itemIdentifier === 'c') throw new Error('boom');
  }, { concurrency: 3 });
  assert.equal(peak, 3);
  assert.deepEqual(response, { batchItemFailures: [{ itemIdentifier: 'c' }] });
  await assert.rejects(handleTriggerBatch(body, () => {}, { concurrency: 2, stopOnFailure: true }), TypeError);
  await assert.rejects(handleTriggerBatch(body, () => {}, { concurrency: 0 }), TypeError);
});
