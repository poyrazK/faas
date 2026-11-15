import test from 'node:test';
import assert from 'node:assert/strict';
import { createElement } from 'react';
import TestRenderer, { act, type ReactTestRenderer } from 'react-test-renderer';
import { createLiveJob, type LiveJob, type LiveJobSink } from '../src/live-jobs.js';
import { useLiveJob } from '../src/react.js';
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
const snapshot = { version: 1, resource_id: 'import-1', run_id: 'run-1', status: 'running', total: 1, succeeded: 0, failed: 0, cancelled: 0, running: 1, report: 'none', failures: [] };
test('React shares a live job and replaces views when the authenticated controller changes', async () => {
  let stops = 0, sink!: LiveJobSink;
  const job = createLiveJob({ resourceId: 'import-1', timeoutMs: 1000, loadSnapshot: async () => snapshot,
    source: next => { sink = next; return () => { stops++; }; } });
  const other = createLiveJob({ resourceId: 'import-1', timeoutMs: 1000, loadSnapshot: async () => { throw new Error('denied'); }, source: () => () => {} });
  let shown: string[] = [];
  function Viewer({ live }: { live: LiveJob }) { const view = useLiveJob(live); shown.push(view.snapshot?.run_id ?? view.connection); return null; }
  let renderer!: ReactTestRenderer;
  await act(async () => { renderer = TestRenderer.create(createElement('div', null, createElement(Viewer, { live: job }), createElement(Viewer, { live: job }))); });
  await act(async () => { sink.ready(); await new Promise(resolve => setTimeout(resolve, 1)); });
  assert.ok(shown.includes('run-1'));
  shown = [];
  await act(async () => { renderer.update(createElement(Viewer, { live: other })); });
  assert.equal(shown.includes('run-1'), false); assert.equal(stops, 1);
  await act(async () => { renderer.unmount(); }); job.close(); other.close();
});
