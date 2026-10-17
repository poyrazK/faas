import assert from 'node:assert/strict';
import {mkdtemp, readdir, rm} from 'node:fs/promises';
import {request} from 'node:http';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {PassThrough, Readable} from 'node:stream';
import {test} from 'node:test';
import {createStorageApp} from '../app.mjs';

const token = 'a'.repeat(48);
const headers = {authorization: 'Bearer ' + token};
const hookHeaders = {'x-faas-after-restore': '1'};
const settings = () => ({GREGALE_S3_ASSETS_ENDPOINT: 'https://gateway.local.test', GREGALE_S3_ASSETS_REGION: 'auto',
  GREGALE_S3_ASSETS_BUCKET: 'assets', GREGALE_S3_ASSETS_ACCESS_KEY_ID: 'private-access',
  GREGALE_S3_ASSETS_SECRET_ACCESS_KEY: 'private-secret', GREGALE_S3_ASSETS_ADDRESSING_STYLE: 'path', STORAGE_APP_TOKEN: token});

function gate() {
  let release, entered;
  return {promise: new Promise(resolve => {release = resolve;}), entered: new Promise(resolve => {entered = resolve;}),
    release: () => release(), enter: () => entered()};
}

async function until(predicate) {
  const deadline = Date.now() + 2000;
  while (!await predicate()) {
    if (Date.now() >= deadline) throw new Error('condition_timeout');
    await new Promise(resolve => setTimeout(resolve, 2));
  }
}

async function fixture(t, {restoreTimeoutMs = 1500} = {}) {
  const directory = await mkdtemp(join(tmpdir(), 'gregale-storage-restore-'));
  const clients = [], bodies = [], puts = [], acks = [], logs = [], gates = [];
  const blocks = {};
  let values = settings(), revision = 'a', failList = false;
  const app = createStorageApp({restoreTimeoutMs, spoolRoot: directory, log: code => logs.push(code),
    env: {FAAS_SECRETS_SNAPSHOT_FILE: '/snapshot', FAAS_SECRETS_RELOAD_READY_FILE: '/ready',
      FAAS_SECRETS_RELOAD_ACK_ENDPOINT: 'http://metadata.local.test/ack', FAAS_SECRETS_RELOAD_GENERATION: 'e'.repeat(32)},
    io: {read: async () => JSON.stringify({revision: revision.repeat(64), secrets: values}), ready: async () => {},
      fetch: async (_url, options) => {acks.push(JSON.parse(options.body)); return new Response(null, {status: 202});}},
    clientFactory: () => {
      const client = {destroyed: 0, bodies: [], destroy() {
        assert.ok(this.bodies.every(body => body.closed), 'client retired before actual body closure');
        this.destroyed++;
      }, async send(command, {abortSignal}) {
        abortSignal.throwIfAborted();
        const kind = command.constructor.name;
        const block = blocks[kind];
        if (kind === 'PutObjectCommand') {
          const chunks = []; for await (const chunk of command.input.Body) chunks.push(chunk);
          puts.push(Buffer.concat(chunks)); // Commit before an intentionally late response.
        }
        if (block) {block.enter(); await block.promise;} // Deliberately ignore cancellation.
        if (kind === 'ListObjectsV2Command') {
          if (failList) throw new Error('private-provider-error');
          return {};
        }
        if (kind === 'PutObjectCommand') return {};
        const close = blocks.close;
        const options = {destroy(error, done) {
          if (close) {close.enter(); void close.promise.then(() => done(error));} else done(error);
        }};
        // Readable.from() in early Node 22 replaces the supplied destroy hook.
        // Construct the byte stream directly so delayed close is exercised on
        // every supported runtime.
        const body = command.input.Key === 'slow.bin' ? new PassThrough(options) : new Readable({...options,
          read() {this.push(Buffer.from('file')); this.push(null);}});
        bodies.push(body); client.bodies.push(body);
        if (command.input.Key === 'slow.bin') body.write('first');
        return {Body: body, ContentLength: command.input.Key === 'slow.bin' ? 9 : 4};
      }};
      clients.push(client); return client;
    }});
  t.after(async () => {
    for (const block of gates) block.release();
    for (const body of bodies) body.destroy();
    await app.stop(1000);
    await rm(directory, {recursive: true, force: true});
  });
  const address = await app.start(0, '127.0.0.1');
  const base = `http://127.0.0.1:${address.port}`;
  return {app, base, directory, clients, bodies, puts, acks, logs,
    hook: () => fetch(base + '/internal/after-restore', {method: 'POST', headers: hookHeaders}),
    publish(nextRevision, nextValues = settings()) {revision = nextRevision; values = nextValues;},
    failList(value = true) {failList = value;},
    block(kind) {const held = gate(); blocks[kind] = held; gates.push(held); return held;},
    unblock(kind) {blocks[kind]?.release(); delete blocks[kind];},
  };
}

test('restore authenticates the actual peer and header and rebuilds at the same revision', async t => {
  const f = await fixture(t);
  assert.equal((await fetch(f.base + '/internal/after-restore', {method: 'POST'})).status, 403);
  const denied = await new Promise((resolve, reject) => {
    const req = request(f.base + '/internal/after-restore', {method: 'POST', localAddress: '127.0.0.2',
      headers: {...hookHeaders, 'x-forwarded-for': '127.0.0.1', forwarded: 'for=127.0.0.1'}}, response => {
      response.resume(); resolve(response.statusCode);
    });
    req.on('error', reject); req.end();
  });
  assert.equal(denied, 403);
  assert.equal((await fetch(f.base + '/internal/after-restore', {headers: hookHeaders})).status, 405);
  assert.equal((await f.hook()).status, 204);
  assert.equal(f.app.generation, 1);
  assert.equal(f.clients.length, 2);
  assert.equal(f.clients[0].destroyed, 1);
  assert.equal(f.app.ready(), true);
  assert.deepEqual(f.acks.map(ack => ack.revision), ['a'.repeat(64), 'a'.repeat(64)]);
});

test('restore gates admission, cancels a captured stream and removes a partial upload before success', async t => {
  const f = await fixture(t);
  const stream = await fetch(f.base + '/objects/slow.bin', {headers});
  const reader = stream.body.getReader();
  assert.equal((await reader.read()).value.length, 5);
  const input = request(f.base + '/objects/partial.bin', {method: 'PUT', headers});
  input.on('error', () => {}); input.write('partial');
  t.after(() => input.destroy());
  await until(async () => (await readdir(f.directory)).length === 1);
  const listing = f.block('ListObjectsV2Command');
  const recovering = f.hook();
  await listing.entered;
  assert.equal((await fetch(f.base + '/readyz')).status, 503);
  assert.equal((await fetch(f.base + '/objects/file', {headers})).status, 503);
  assert.equal((await fetch(f.base + '/healthz')).status, 200);
  f.unblock('ListObjectsV2Command');
  assert.equal((await recovering).status, 204);
  await assert.rejects(reader.read());
  assert.equal(f.bodies[0].closed, true);
  assert.equal(f.clients[0].destroyed, 1);
  assert.deepEqual(await readdir(f.directory), []);
  assert.equal(f.puts.length, 0);
});

test('late GET and delayed body close retain the captured lease after restore times out', async t => {
  const f = await fixture(t, {restoreTimeoutMs: 30});
  const get = f.block('GetObjectCommand');
  const close = f.block('close');
  const transfer = assert.rejects(fetch(f.base + '/objects/file', {headers}));
  await get.entered;
  assert.equal((await f.hook()).status, 503);
  assert.equal(f.app.ready(), false);
  assert.equal(f.clients[0].destroyed, 0);
  f.unblock('GetObjectCommand');
  await transfer; // Caller leaves promptly while ignored driver cancellation stays owned.
  await close.entered;
  assert.equal(f.bodies[0].destroyed, true);
  assert.equal(f.bodies[0].closed, false);
  assert.equal(f.clients[0].destroyed, 0);
  f.unblock('close');
  await f.app.storage.waitForRetirement(AbortSignal.timeout(1000));
  assert.equal(f.bodies[0].closed, true);
  assert.equal(f.clients[0].destroyed, 1);
  assert.equal(f.app.ready(), false);
  assert.equal((await f.hook()).status, 204);
});

test('an uncertain late PUT is not replayed and keeps its spool until actual return', async t => {
  const f = await fixture(t, {restoreTimeoutMs: 30});
  const put = f.block('PutObjectCommand');
  const transfer = assert.rejects(fetch(f.base + '/objects/file', {method: 'PUT', headers, body: Buffer.from('once')}));
  await put.entered;
  assert.equal((await readdir(f.directory)).length, 1);
  assert.equal((await f.hook()).status, 503);
  assert.equal(f.clients[0].destroyed, 0);
  assert.equal((await readdir(f.directory)).length, 1);
  await transfer;
  f.unblock('PutObjectCommand');
  await f.app.storage.waitForRetirement(AbortSignal.timeout(1000));
  assert.deepEqual(await readdir(f.directory), []);
  assert.deepEqual(f.puts, [Buffer.from('once')]);
  assert.equal(f.clients[0].destroyed, 1);
  assert.equal((await f.hook()).status, 204);
});

test('cancelled validation cannot publish or start an applied ACK and a later restore recovers', async t => {
  const f = await fixture(t, {restoreTimeoutMs: 30});
  const listing = f.block('ListObjectsV2Command');
  const recovering = f.app.restore();
  await listing.entered;
  await assert.rejects(recovering, /storage_unavailable/);
  assert.equal(f.app.ready(), false);
  assert.equal(f.acks.length, 1);
  assert.equal(f.clients[1].destroyed, 0);
  f.unblock('ListObjectsV2Command');
  await f.app.storage.waitForRetirement(AbortSignal.timeout(1000));
  assert.equal(f.clients[1].destroyed, 1);
  assert.equal(f.acks.length, 1);
  await f.app.coordinator.reload();
  assert.equal(f.app.ready(), false); // Ordinary reload cannot clear failed recovery.
  await f.app.restore();
  assert.equal(f.app.ready(), true);
});

test('restore queued behind a blocked reload times out without late adoption and then retries', async t => {
  const f = await fixture(t, {restoreTimeoutMs: 30});
  const listing = f.block('ListObjectsV2Command');
  const reload = f.app.coordinator.reload();
  void reload.catch(() => {});
  await listing.entered;
  await assert.rejects(f.app.restore(), /storage_unavailable/);
  assert.equal(f.acks.length, 1);
  f.unblock('ListObjectsV2Command');
  await assert.rejects(reload);
  assert.ok(f.acks.slice(1).every(ack => ack.status !== 'applied'));
  assert.equal(f.app.ready(), false);
  await f.app.restore();
  assert.equal(f.app.ready(), true);
});

test('concurrent restores coalesce and follower cancellation leaves the owner running', async t => {
  const f = await fixture(t);
  const listing = f.block('ListObjectsV2Command');
  const owner = f.app.restore();
  await listing.entered;
  const controller = new AbortController();
  const follower = f.app.restore({signal: controller.signal});
  controller.abort();
  await assert.rejects(follower, /storage_unavailable/);
  const another = f.app.restore();
  assert.equal(f.clients.length, 2);
  f.unblock('ListObjectsV2Command');
  await Promise.all([owner, another]);
  assert.equal(f.app.generation, 1);
  assert.equal(f.acks.length, 2);
  assert.equal(f.app.ready(), true);
});

test('owner cancellation fences all coalesced callers and a fresh restore can retry', async t => {
  const f = await fixture(t);
  const listing = f.block('ListObjectsV2Command');
  const controller = new AbortController();
  const owner = f.app.restore({signal: controller.signal});
  await listing.entered;
  const follower = f.app.restore();
  controller.abort();
  await Promise.all([assert.rejects(owner), assert.rejects(follower)]);
  assert.equal(f.acks.length, 1);
  assert.equal(f.app.ready(), false);
  f.unblock('ListObjectsV2Command');
  await f.app.restore();
  assert.equal(f.app.ready(), true);
});

test('a disconnected hook fences its owned validation and leaves readiness closed', async t => {
  const f = await fixture(t);
  const listing = f.block('ListObjectsV2Command');
  const req = request(f.base + '/internal/after-restore', {method: 'POST', headers: hookHeaders});
  req.on('error', () => {}); req.end();
  await listing.entered;
  req.destroy();
  await until(() => f.logs.includes('storage_restore_failed'));
  assert.equal(f.app.ready(), false);
  f.unblock('ListObjectsV2Command');
  await f.app.storage.waitForRetirement(AbortSignal.timeout(1000));
  assert.equal(f.acks.length, 1);
  assert.equal((await f.hook()).status, 204);
});

test('failed storage or auth adoption stays closed through SIGHUP until successful restore', async t => {
  const f = await fixture(t);
  f.failList();
  assert.equal((await f.hook()).status, 503);
  assert.equal(f.acks.at(-1).status, 'failed');
  f.failList(false);
  f.publish('b', {...settings(), STORAGE_APP_TOKEN: 'too-short'});
  assert.equal((await f.hook()).status, 503);
  assert.equal(f.acks.at(-1).status, 'failed');
  f.publish('c');
  const before = f.acks.length;
  process.emit('SIGHUP');
  await until(() => f.acks.length === before + 1);
  assert.equal(f.app.ready(), false);
  assert.equal((await f.hook()).status, 204);
  assert.equal(f.app.ready(), true);
  assert.ok(!JSON.stringify(f.logs).includes('private-'));
});

test('shutdown cancels recovery without retiring a late candidate prematurely', async t => {
  const f = await fixture(t);
  const listing = f.block('ListObjectsV2Command');
  const recovering = f.app.restore();
  void recovering.catch(() => {});
  await listing.entered;
  assert.equal(await f.app.stop(30), false);
  await assert.rejects(recovering);
  assert.equal(f.clients[1].destroyed, 0);
  assert.equal(f.acks.length, 1);
  f.unblock('ListObjectsV2Command');
  await f.app.storage.waitForRetirement(AbortSignal.timeout(1000));
  assert.ok(f.clients.every(client => client.destroyed === 1));
  await assert.rejects(f.app.restore());
});
