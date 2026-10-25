// ADR-20261023115741: the default entry exposes generally available and beta
// capabilities; '@gregale/sdk-node/preview' adds the preview ones.
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import * as publicEntry from '../src/index.js';
import * as previewEntry from '../src/preview.js';

test('the default entry leaves preview services and helpers out', () => {
  const pub = publicEntry as Record<string, unknown>;
  const preview = previewEntry as Record<string, unknown>;
  for (const name of ['AppsService', 'CronsService', 'FaaSClient', 'verifyWebhook', 'ErrNotFound']) {
    assert.ok(pub[name], `${name} must stay in the default entry`);
  }
  for (const name of ['RealtimeService', 'StorageService', 'QueuesService', 'McpService', 'createRealtimeClient', 'GregaleOperations', 'insertCommitEvent']) {
    assert.equal(pub[name], undefined, `${name} is a preview export`);
    assert.ok(preview[name], `${name} must be in the preview entry`);
  }
});

test('the preview entry publishes events', () => {
  // The default entry's EventsService is the beta workflow-recovery subset;
  // publishing is a preview operation.
  const preview = previewEntry as Record<string, Record<string, unknown>>;
  for (const name of ['publishEvent', 'publishEventBatch', 'publishAppEvent', 'getEventReceipt']) {
    assert.equal(typeof preview.EventsService?.[name], 'function', `EventsService.${name} must be in the preview entry`);
  }
});

test('only the preview client runs disposable executions', () => {
  assert.equal((publicEntry.FaaSClient.prototype as unknown as Record<string, unknown>).runExecution, undefined);
  assert.equal(typeof (previewEntry.FaaSClient.prototype as unknown as Record<string, unknown>).runExecution, 'function');
  assert.ok(new previewEntry.FaaSClient('https://api.example') instanceof publicEntry.FaaSClient);
});

test('preview subpaths live under ./preview', () => {
  const pkg = JSON.parse(readFileSync(new URL('../../package.json', import.meta.url), 'utf8')) as { exports: Record<string, unknown> };
  const outside = Object.keys(pkg.exports).filter((key) => key !== './preview' && !key.startsWith('./preview/'));
  assert.deepEqual(outside.sort(), ['.', './browser', './secrets/runtime', './workflows']);
});
