// test/smoke.test.ts — Node SDK smoke test against the Go fixture
// (sdk/fakeapid). Runs the full SDK surface — generated services
// + wrapper stack — through six canonical routes plus the 404
// sentinel. Mirrors `sdk/fakeapid/main_test.go` (raw HTTP) and
// `sdk/go/transport_e2e_test.go` (SDK round-trip).
//
// `node:test` runs test files serially by default. We use a single
// `test()` block with sub-tests rather than per-test spawn/teardown
// so the fixture boots exactly once.

import test, { after } from 'node:test';
import assert from 'node:assert/strict';

import {
  AccountService,
  AppsService,
  ErrNotFound,
  FaaSClient,
  UsageService,
} from '../src/index.js';
import { spawnFakeApid, type SpawnedFakeApid } from './helpers/spawn-fakeapid.js';

// Build the fixture binary ONCE before the suite runs. The CI job's
// Makefile target builds it at sdk/fakeapid/bin/fakeapid; if not
// present, the helper throws with a clear message.
const fixture = await spawnFakeApid();
test.after(async () => {
  await fixture.stop();
});

const baseURL = fixture.baseURL;
const client = new FaaSClient(baseURL, {
  token: 'fp_smoke_test_token',
  retry: { maxAttempts: 1, backoffMs: 0 }, // smoke test is a happy path; no retry
});

test('healthz returns ok', async () => {
  const resp = await fetch(`${baseURL}/__healthz`);
  assert.equal(resp.status, 200);
  const body = (await resp.json()) as { ok?: boolean };
  assert.equal(body.ok, true);
});

test('account round-trip via generated AccountService', async () => {
  // AccountService.getAccount is the canonical GET /v1/account.
  const account = await AccountService.getAccount();
  assert.equal(account.plan, 'hobby');
  assert.ok(account.email, 'email is required');
});

test('create app + list apps via generated AppsService', async () => {
  const created = await AppsService.createApp({
    requestBody: { slug: 'hello-world' },
  });
  assert.equal(created.slug, 'hello-world');

  const list = await AppsService.listApps();
  assert.ok(Array.isArray(list), 'listApps must return an array');
  assert.ok(list.length >= 1, 'at least one app in listApps response');
  assert.equal(list[0]?.slug, 'hello-world');
});

test('get app by slug via generated AppsService', async () => {
  const app = await AppsService.getApp({ slug: 'hello' });
  assert.equal(app.slug, 'hello');
});

test('get usage returns an array (memory: getusage-wire-shape-mismatch)', async () => {
  const usage = await UsageService.getUsage({ month: '2026-07' });
  assert.ok(Array.isArray(usage), 'getUsage must return an array of UsageResponse');
  assert.ok(usage.length >= 1, 'at least one usage row in response');
  const row = usage[0];
  assert.ok(row, 'usage row must exist');
  assert.ok(typeof row.app_id === 'string');
});

test('unknown slug surfaces ErrNotFound (RFC 7807 unwrap)', async () => {
  await assert.rejects(
    () => AppsService.getApp({ slug: 'missing-app-404' }),
    (err: unknown) => {
      // The wrapper's rfc7807 layer decodes Problem envelopes and
      // raises typed sentinels. The generator would otherwise
      // surface its own `ApiError`; we want the typed contract.
      assert.ok(
        err instanceof ErrNotFound,
        `expected ErrNotFound, got ${String(err)}`,
      );
      const faasErr = err as InstanceType<typeof ErrNotFound>;
      assert.equal(faasErr.problem.code, 'not_found');
      assert.equal(faasErr.status, 404);
      assert.ok(faasErr.txId, 'tx_id must be present on canonical Problem');
      return true;
    },
  );
});

test('mutating calls layer does not break happy path', async () => {
  // The wrapper's idempotencyLayer stamps a fresh UUIDv4 on every
  // mutating call. The fakeapid fixture doesn't echo the header
  // back, so we can only assert that the call succeeds (the
  // header assertion requires a server-side inspection tool the
  // fixture doesn't expose; PR 9 adds a `__echo` route to the
  // fixture and this test grows an outbound-header assertion).
  const result = await AppsService.createApp({
    requestBody: { slug: 'idem-test' },
  });
  assert.ok(result.slug);
});

// Suppress an unused-import lint for SpawnedFakeApid type — it
// would only appear if we destructured it; reference it via type
// only.
const _typeOnly: SpawnedFakeApid | undefined = undefined;
void _typeOnly;