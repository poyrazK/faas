import {createHash, timingSafeEqual} from 'node:crypto';
import {createReadStream, createWriteStream} from 'node:fs';
import {mkdtemp, rm} from 'node:fs/promises';
import {createServer} from 'node:http';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {Transform} from 'node:stream';
import {pipeline} from 'node:stream/promises';
import {S3Client, ListObjectsV2Command, GetObjectCommand, PutObjectCommand} from '@aws-sdk/client-s3';
import {ObjectStorageBinding} from './runtime/object-storage-runtime.js';
import {SecretReloadCoordinator} from './runtime/secret-reload-runtime.js';

// Application safeguards, independent of account/bucket quotas at the gateway.
export const limits = Object.freeze({uploadBytes: 10 * 1024 * 1024, uploads: 2, transferMs: 30_000});
const digest = value => createHash('sha256').update(value).digest();
// AWS annotates resolved credentials. Give each transport its own mutable copy
// while the managed binding's captured configuration stays immutable.
const defaultClient = config => new S3Client({...config, credentials: {...config.credentials}, maxAttempts: 1,
  requestHandler: {connectionTimeout: 5000, requestTimeout: limits.transferMs}});

function problem(response, status, code) {
  if (response.destroyed || response.writableEnded) return;
  if (response.headersSent) {response.destroy(); return;}
  response.writeHead(status, {'content-type': 'application/json', 'cache-control': 'no-store'});
  response.end(JSON.stringify({ok: status < 400, code}));
}

function objectKey(request) {
  const url = new URL(request.url, 'http://app.local');
  const encoded = url.pathname.slice('/objects/'.length);
  let key;
  try {key = decodeURIComponent(encoded);} catch {return undefined;}
  // This starter exposes only simple file identifiers in its private bucket.
  if (url.search || !/^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/.test(key) || key === '.' || key === '..') return undefined;
  return key;
}

function authorize(request, token) {
  if (!token) return false;
  const header = request.headers.authorization;
  if (typeof header !== 'string' || !header.startsWith('Bearer ') || header.length > 1024) return false;
  return timingSafeEqual(digest(header.slice(7)), token);
}

function requestLifetime(request, response, active) {
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), limits.transferMs);
  timeout.unref();
  const aborted = () => controller.abort();
  const closed = () => {if (!response.writableFinished) controller.abort();};
  const cancelled = () => response.destroy();
  request.once('aborted', aborted);
  response.once('close', closed);
  controller.signal.addEventListener('abort', cancelled, {once: true});
  active.add(controller);
  if (request.aborted || response.destroyed) controller.abort();
  return {
    signal: controller.signal,
    finish() {
      clearTimeout(timeout);
      request.removeListener('aborted', aborted);
      response.removeListener('close', closed);
      controller.signal.removeEventListener('abort', cancelled);
      active.delete(controller);
    },
  };
}

async function closeBody(body) {
  if (body.closed) return;
  // destroy() starts cleanup. Keep the lease/file until the actual close event,
  // including transports whose destruction completes asynchronously.
  const closed = new Promise(resolve => body.once('close', resolve));
  const ignored = () => {};
  body.on('error', ignored);
  try {body.destroy(); await closed;} finally {body.off('error', ignored);}
}

function joinRestore(promise, signal) {
  if (!signal) return promise;
  return new Promise((resolve, reject) => {
    const finish = (callback, value) => {signal.removeEventListener('abort', aborted); callback(value);};
    const aborted = () => finish(reject, new Error('storage_unavailable'));
    signal.addEventListener('abort', aborted, {once: true});
    promise.then(value => finish(resolve, value), error => finish(reject, error));
    if (signal.aborted) aborted();
  });
}

async function upload(request, response, pair, key, signal, spoolRoot) {
  signal.throwIfAborted();
  const declared = request.headers['content-length'];
  if (declared !== undefined && (!/^\d+$/.test(declared) || Number(declared) > limits.uploadBytes)) {
    problem(response, 413, 'upload_too_large'); request.resume(); return;
  }
  const directory = await mkdtemp(join(spoolRoot, 'gregale-upload-'));
  const path = join(directory, 'body');
  let size = 0;
  const measure = new Transform({transform(chunk, _encoding, done) {
    size += chunk.length;
    done(size > limits.uploadBytes ? new Error('upload_too_large') : null, chunk);
  }});
  try {
    // Disk staging bounds memory and supplies an exact ContentLength. A timeout,
    // disconnect or failed upload removes the private file in the finally block.
    await pipeline(request, measure, createWriteStream(path, {flags: 'wx', mode: 0o600}), {signal});
    if (declared !== undefined && size !== Number(declared)) throw new Error('invalid_upload_size');
    signal.throwIfAborted();
    const body = createReadStream(path);
    try {
      await pair.client.send(new PutObjectCommand({Bucket: pair.bucket, Key: key, Body: body,
        ContentLength: size, ContentType: request.headers['content-type'] || 'application/octet-stream'}), {abortSignal: signal});
    } finally {await closeBody(body);}
  } finally {await rm(directory, {recursive: true, force: true});}
  signal.throwIfAborted(); // A late/uncertain PUT must never publish success or be replayed.
  response.writeHead(201, {'content-type': 'application/json', 'cache-control': 'no-store'});
  response.end(JSON.stringify({ok: true, key}));
}

async function download(response, pair, key, signal) {
  signal.throwIfAborted();
  const result = await pair.client.send(new GetObjectCommand({Bucket: pair.bucket, Key: key}), {abortSignal: signal});
  const body = result.Body;
  if (!body || typeof body.destroy !== 'function') throw new Error('storage_unavailable');
  try {
    signal.throwIfAborted(); // Close late responses before forwarding headers.
    // Never forward stored Content-Type/Disposition as executable app-origin
    // content. The private starter serves downloads as opaque attachments.
    const headers = {'content-type': 'application/octet-stream', 'content-disposition': 'attachment',
      'x-content-type-options': 'nosniff', 'cache-control': 'no-store'};
    if (Number.isSafeInteger(result.ContentLength) && result.ContentLength >= 0) headers['content-length'] = String(result.ContentLength);
    response.writeHead(200, headers);
    await pipeline(body, response, {signal});
  } finally {await closeBody(body);}
}

export function createStorageApp({env = process.env, io, clientFactory = defaultClient, spoolRoot = tmpdir(),
  log = () => {}, restoreTimeoutMs = 1500} = {}) {
  if (!Number.isFinite(restoreTimeoutMs) || restoreTimeoutMs <= 0) throw new Error('invalid_restore_timeout');
  let token, stopping = false, started = false, stopPromise, uploads = 0;
  let restoring, restoreController, restoreFailed = false, generation = 0;
  const active = new Set();
  const storage = new ObjectStorageBinding('GREGALE_S3_ASSETS', async (config, bucket, signal) => {
    const client = clientFactory(config);
    try {
      // Read/write binding required. Construction alone cannot validate access.
      await client.send(new ListObjectsV2Command({Bucket: bucket, MaxKeys: 1}),
        {abortSignal: AbortSignal.any([...(signal ? [signal] : []), AbortSignal.timeout(5000)])});
      return client;
    } catch {client.destroy(); throw new Error('storage_unavailable');}
  }, {retire: client => client.destroy(), onRetireError: () => log('storage_retirement_failed')});
  const authorization = {
    apply(snapshot) {
      const value = snapshot.secrets.STORAGE_APP_TOKEN;
      if (typeof value !== 'string' || value.length < 32 || value.length > 512 || /[\r\n\0]/.test(value)) throw new Error('invalid_app_token');
      token = digest(value);
    },
    invalidate() {token = undefined;},
  };
  const coordinator = new SecretReloadCoordinator({env, io, consumers: [storage.secretConsumer(), authorization],
    onError: () => log('storage_reload_failed')});
  function ready() {
    if (!started || stopping || restoring || restoreFailed || !token) return false;
    let lease;
    try {lease = storage.acquire(); return true;} catch {return false;} finally {lease?.release();}
  }
  function restore({signal} = {}) {
    if (!started || stopping || signal?.aborted) return Promise.reject(new Error('storage_unavailable'));
    if (restoring) return joinRestore(restoring, signal);
    restoreFailed = true;
    token = undefined;
    for (const controller of active) controller.abort();
    storage.invalidate();
    const controller = new AbortController();
    restoreController = controller;
    const abort = () => controller.abort();
    const timer = setTimeout(abort, Math.min(restoreTimeoutMs, 1500));
    timer.unref();
    signal?.addEventListener('abort', abort, {once: true});
    restoring = Promise.resolve().then(async () => {
      try {
        // Refresh even at the same revision; a captured S3 transport is stale.
        await coordinator.reload(controller.signal);
        await storage.waitForRetirement(controller.signal);
        controller.signal.throwIfAborted();
        if (stopping || !token) throw new Error('storage_unavailable');
        const lease = storage.acquire();
        lease.release();
        generation++;
        restoreFailed = false;
      } catch {
        storage.invalidate();
        log('storage_restore_failed');
        throw new Error('storage_unavailable');
      }
    }).finally(() => {
      clearTimeout(timer);
      signal?.removeEventListener('abort', abort);
      restoring = restoreController = undefined;
    });
    return restoring;
  }
  async function afterRestore(request, response) {
    if (!['127.0.0.1', '::1', '::ffff:127.0.0.1'].includes(request.socket.remoteAddress) ||
        request.headers['x-faas-after-restore'] !== '1') {
      problem(response, 403, 'forbidden'); request.resume(); return;
    }
    if (request.method !== 'POST') {problem(response, 405, 'method_not_allowed'); request.resume(); return;}
    const controller = new AbortController();
    const aborted = () => controller.abort();
    const closed = () => {if (!response.writableFinished) aborted();};
    request.once('aborted', aborted);
    response.once('close', closed);
    if (request.aborted) aborted();
    request.resume();
    try {await restore({signal: controller.signal}); response.writeHead(204).end();}
    catch {problem(response, 503, 'storage_unavailable');}
    finally {request.off('aborted', aborted); response.off('close', closed);}
  }
  async function route(request, response) {
    if (request.url === '/internal/after-restore') {await afterRestore(request, response); return;}
    if (request.url === '/healthz' && request.method === 'GET') {
      response.writeHead(200, {'content-type': 'application/json'}).end('{"ok":true}'); return;
    }
    if (request.url === '/readyz' && request.method === 'GET') {
      const available = ready();
      problem(response, available ? 200 : 503, available ? 'ready' : 'storage_unavailable'); return;
    }
    if (!ready()) {problem(response, 503, 'storage_unavailable'); request.resume(); return;}
    if (!authorize(request, token)) {problem(response, 401, 'unauthorized'); request.resume(); return;}
    if (!request.url.startsWith('/objects/')) {problem(response, 404, 'not_found'); request.resume(); return;}
    const key = objectKey(request);
    if (!key) {problem(response, 400, 'invalid_key'); request.resume(); return;}
    if (!['PUT', 'GET'].includes(request.method)) {problem(response, 405, 'method_not_allowed'); request.resume(); return;}
    if (request.method === 'PUT' && uploads >= limits.uploads) {problem(response, 503, 'upload_capacity'); request.resume(); return;}
    await transfer(request, response, key);
  }
  async function transfer(request, response, key) {
    const lease = storage.acquire();
    const lifetime = requestLifetime(request, response, active);
    const isUpload = request.method === 'PUT';
    if (isUpload) uploads++;
    try {
      const pair = lease.current();
      if (isUpload) await upload(request, response, pair, key, lifetime.signal, spoolRoot);
      else await download(response, pair, key, lifetime.signal);
    } catch (error) {
      const code = error?.name === 'NoSuchKey' ? 'not_found' : error?.message === 'upload_too_large' ? 'upload_too_large' : 'storage_unavailable';
      problem(response, code === 'not_found' ? 404 : code === 'upload_too_large' ? 413 : 503, code);
      log(code); // Never expose SDK errors, credentials or private provider URLs.
    } finally {
      if (isUpload) uploads--;
      lifetime.finish();
      lease.release(); // Upload spool and response body have finished/closed.
      // A connection that was active when close() ran can become idle later.
      if (stopping) setImmediate(() => server.closeIdleConnections());
    }
  }
  const server = createServer((request, response) => {
    void route(request, response).catch(() => {problem(response, 503, 'storage_unavailable'); log('request_failed');});
  });
  server.requestTimeout = limits.transferMs;
  server.headersTimeout = 10_000;
  return {
    storage, coordinator, server, ready, restore, get generation() {return generation;},
    async start(port = Number(env.PORT ?? 8080), host = '0.0.0.0') {
      if (!Number.isInteger(port) || port < 0 || port > 65535) throw new Error('invalid_port');
      await coordinator.start();
      if (stopping) throw new Error('startup_cancelled');
      started = true;
      await new Promise((resolve, reject) => {
        server.once('error', reject);
        server.listen(port, host, () => {server.removeListener('error', reject); resolve();});
      });
      return server.address();
    },
    stop(timeoutMs = 5000) {
      if (stopPromise) return stopPromise;
      if (!Number.isInteger(timeoutMs) || timeoutMs < 0 || timeoutMs > 2147483647) throw new Error('invalid_shutdown_timeout');
      stopping = true;
      coordinator.stop();
      restoreController?.abort();
      const closed = new Promise(resolve => server.close(() => resolve()));
      const shutdown = new AbortController();
      // Keep shutdown's deadline referenced even if closing the listener leaves
      // only a cancellation-suppressing factory/cleanup promise outstanding.
      const timer = setTimeout(() => shutdown.abort(), timeoutMs);
      const deadline = shutdown.signal;
      const interrupted = new Promise((_, reject) => deadline.addEventListener('abort', () => reject(new Error('shutdown_timeout')), {once: true}));
      stopPromise = Promise.race([Promise.all([closed, storage.waitForRetirement(deadline), restoring?.catch(() => {})]), interrupted])
        .then(() => true).catch(() => {
          for (const controller of active) controller.abort();
          server.closeAllConnections();
          log('shutdown_timeout');
          return false;
        }).finally(() => clearTimeout(timer));
      return stopPromise;
    },
  };
}
