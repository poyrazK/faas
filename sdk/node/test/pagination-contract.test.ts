import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { setImmediate as nextTurn } from 'node:timers/promises';
import { createClient, ErrNotFound, PaginationError, type PaginationOptions } from '../src/index.js';
import { createClient as createPreviewClient } from '../src/preview.js';

interface Case {
  name: string; route: 'app' | 'account' | 'builds'; limit?: number; before?: string;
  app?: string; status?: 'running'; max_pages?: number; max_items?: number; timeout_ms?: number; max_attempts?: number;
  phase?: 'body' | 'headers' | 'retry' | 'not_found';
  pages: { ids: string[]; next_before?: unknown }[];
  ids: string[]; calls: number; cursors?: (string | null)[]; limits?: number[]; closed?: number;
  outcome?: 'repeated_cursor' | 'invalid_page' | 'timeout' | 'cancelled' | 'not_found';
}
const contract = JSON.parse(readFileSync(new URL('../../../request-contracts/pagination.json', import.meta.url), 'utf8')) as { version: number; cases: Case[] };
const row = (id: string) => ({ id, app_id: 'app', image_digest: 'sha256:abc', kind: 'tarball', status: 'succeeded',
  created_at: '2026-11-14T00:00:00Z', deployment_id: 'deployment', source_bytes: 1, enqueued_at: '2026-11-14T00:00:00Z' });

for (const [surface, factory] of [['public', createClient], ['preview', createPreviewClient]] as const) {
  for (const mode of ['items', 'pages'] as const) {
    for (const fixture of contract.cases) {
      test(`${surface} ${mode} shared pagination: ${fixture.name}`, { timeout: 5000 }, async t => {
        assert.equal(contract.version, 1);
        if (fixture.phase === 'body') t.mock.method(console, 'error', () => {});
        const urls: URL[] = [], ids: string[] = [];
        let closed = 0, started!: () => void;
        const entered = new Promise<void>(resolve => { started = resolve; });
        const client = factory('https://contracts.test/prefix', { fetch: async (url, init) => {
          const parsed = new URL(String(url)); urls.push(parsed); started();
          assert.equal(parsed.pathname, '/prefix' + ({ app: '/v1/apps/my%2Fapp/deployments', account: '/v1/deployments', builds: '/v1/builds' }[fixture.route]));
          if (fixture.route === 'builds') {
            assert.equal(parsed.searchParams.get('app'), fixture.app ?? null);
            assert.equal(parsed.searchParams.get('status'), fixture.status ?? null);
          }
          if (fixture.phase === 'headers') return new Promise<Response>((_resolve, reject) => {
            init!.signal!.addEventListener('abort', () => reject(init!.signal!.reason), { once: true });
          });
          if (fixture.phase === 'body') return new Response(new ReadableStream({
            start(stream) { stream.enqueue(new TextEncoder().encode('{"items":[')); }, cancel() { closed++; },
          }), { headers: { 'Content-Type': 'application/json' } });
          if (fixture.phase === 'retry') return Response.json({ code: 'capacity_unavailable' }, { status: 503, headers: { 'Retry-After': '60' } });
          if (fixture.phase === 'not_found') return Response.json({ code: 'not_found', tx_id: 'original-tx', status: 404 }, { status: 404 });
          const page = fixture.pages[urls.length - 1]!;
          return Response.json({ items: page.ids.map(row), next_before: page.next_before });
        } });
        const options: PaginationOptions = { timeoutMs: fixture.timeout_ms ?? 2000, maxPages: fixture.max_pages, maxItems: fixture.max_items,
          retry: fixture.max_attempts ? { maxAttempts: fixture.max_attempts, backoffMs: 1 } : false };
        const params = { limit: fixture.limit ?? 50, before: fixture.before };
        const api = client.deployments;
        const iterator = fixture.route === 'app'
          ? mode === 'items' ? api.iterateAppDeployments({ ...params, slug: 'my/app' }, options) : api.iterateAppDeploymentPages({ ...params, slug: 'my/app' }, options)
          : fixture.route === 'account'
            ? mode === 'items' ? api.iterateDeployments(params, options) : api.iterateDeploymentPages(params, options)
            : mode === 'items' ? api.iterateBuilds({ ...params, app: fixture.app, status: fixture.status }, options) : api.iterateBuildPages({ ...params, app: fixture.app, status: fixture.status }, options);
        assert.equal(urls.length, 0);
        const consume = async () => {
          for await (const value of iterator) {
            if ('items' in value) ids.push(...value.items.map(item => item.id));
            else ids.push(value.id);
          }
        };
        const pending = consume();
        if (fixture.outcome === 'cancelled') {
          const rejected = assert.rejects(pending, { name: 'AbortError' });
          await entered; await iterator.return(); await rejected;
        } else if (fixture.outcome === 'timeout') await assert.rejects(pending, { name: 'TimeoutError' });
        else if (fixture.outcome === 'not_found') await assert.rejects(pending, error => error instanceof ErrNotFound && error.txId === 'original-tx');
        else if (fixture.outcome) await assert.rejects(pending, error => error instanceof PaginationError && error.code === fixture.outcome && !error.message.includes('private-cursor'));
        else await pending;
        await nextTurn();
        assert.deepEqual(ids, fixture.ids);
        assert.equal(urls.length, fixture.calls);
        if (fixture.cursors) assert.deepEqual(urls.map(url => url.searchParams.get('before')), fixture.cursors);
        if (fixture.limits) assert.deepEqual(urls.map(url => Number(url.searchParams.get('limit'))), fixture.limits);
        if (fixture.closed !== undefined) assert.equal(closed, fixture.closed);
      });
    }
  }
}
