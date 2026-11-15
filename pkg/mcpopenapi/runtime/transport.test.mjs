import test from 'node:test';
import assert from 'node:assert/strict';
import https from 'node:https';
import { EventEmitter } from 'node:events';
import { Readable } from 'node:stream';
import { createPublicLookup, prepareRequest, publicAddress, publicLookup, requestJSON } from './http.js';

const manifest = {
  server_url: 'https://api.example.com/v1', request_max_bytes: 32768,
  response_max_bytes: 32768, timeout_ms: 1000, max_json_depth: 16, url_max_bytes: 8192,
};
const operation = {
  method: 'GET', path: '/reports/{id}', response_status: 200,
  parameters: [{ name: 'q', in: 'query' }],
};

test('public egress refuses local, private, transition and reserved destinations', async () => {
  for (const address of ['127.0.0.1', '10.0.0.1', '100.64.0.1', '169.254.169.254', '172.16.0.1', '192.168.1.1',
    '192.0.2.1', '198.51.100.1', '203.0.113.1', '224.0.0.1', '::1', 'fc00::1', 'fe80::1',
    '::ffff:127.0.0.1', '2002:7f00:1::', '2001:db8::1', '2001::1', '3fff::1']) {
    assert.equal(publicAddress(address), false, address);
  }
  for (const address of ['8.8.8.8', '1.1.1.1', '192.2.2.2', '192.0.3.1', '2606:4700:4700::1111', '2001:4860:4860::8888', '2001:67c::1']) {
    assert.equal(publicAddress(address), true, address);
  }
  const mixed = createPublicLookup((_host, _options, done) => done(null, [
    { address: '8.8.8.8', family: 4 }, { address: '127.0.0.1', family: 4 },
  ]));
  await assert.rejects(new Promise((resolve, reject) => mixed('api.example.com', {}, error => error ? reject(error) : resolve())));
  const allowed = createPublicLookup((_host, options, done) => {
    assert.equal(options.all, true);
    done(null, [{ address: '8.8.8.8', family: 4 }]);
  });
  await new Promise((resolve, reject) => allowed('api.example.com', { all: true }, (error, addresses) => {
    if (error) return reject(error);
    assert.deepEqual(addresses, [{ address: '8.8.8.8', family: 4 }]);
    resolve();
  }));
});

test('path and query arguments cannot replace the reviewed target or path structure', () => {
  const { target } = prepareRequest(manifest, operation, { path: { id: 'a?b#c' }, query: { q: 'x&token=y' } });
  assert.equal(target.href, 'https://api.example.com/v1/reports/a%3Fb%23c?q=x%26token%3Dy');
  for (const id of ['', '.', '..', '../private', 'a/b', 'a\\b', '%2f', 'a\nb']) {
    assert.throws(() => prepareRequest(manifest, operation, { path: { id } }));
  }
  assert.throws(() => prepareRequest({ ...manifest, url_max_bytes: 40 }, operation, { path: { id: 'long-value' } }));
  assert.throws(() => prepareRequest(manifest, operation, { path: { id: 'a' }, extra: Number.MAX_SAFE_INTEGER + 1 }));
});

function fakeHTTPS(t, { status = 200, type = 'application/json', body = '{"id":"report"}', stall = false } = {}) {
  const calls = [];
  t.mock.method(https, 'request', (url, options, onResponse) => {
    calls.push({ url, options });
    const request = new EventEmitter();
    request.destroy = error => queueMicrotask(() => request.emit('error', error || new Error('destroyed')));
    request.end = data => {
      calls.at(-1).body = data;
      if (stall) return;
      queueMicrotask(() => {
        const response = Readable.from([Buffer.isBuffer(body) ? body : Buffer.from(body)]);
        response.statusCode = status;
        response.headers = { 'content-type': type };
        onResponse(response);
      });
    };
    return request;
  });
  return calls;
}

test('bounded JSON requests use only explicit headers and validate response bytes', async t => {
  const calls = fakeHTTPS(t);
  const result = await requestJSON(manifest, operation, { path: { id: 'report' } });
  assert.deepEqual(result, { id: 'report' });
  assert.equal(calls.length, 1);
  assert.equal(calls[0].options.lookup, publicLookup);
  assert.equal(calls[0].options.agent, false);
  assert.equal(calls[0].options.rejectUnauthorized, true);
  assert.deepEqual(calls[0].options.headers, { accept: 'application/json', 'accept-encoding': 'identity' });
});

test('JSON body operations send only the reviewed body and accept their declared status', async t => {
  const calls = fakeHTTPS(t, { status: 201, body: '{"id":"new-report"}' });
  const create = { ...operation, method: 'POST', path: '/reports', response_status: 201 };
  const result = await requestJSON(manifest, create, { body: { name: 'new-report' } });
  assert.deepEqual(result, { id: 'new-report' });
  assert.equal(calls.length, 1);
  assert.equal(calls[0].options.method, 'POST');
  assert.equal(calls[0].body, '{"name":"new-report"}');
  assert.equal(calls[0].options.headers['content-type'], 'application/json');
  assert.equal(calls[0].options.headers['content-length'], String(Buffer.byteLength('{"name":"new-report"}')));
});

for (const fixture of [
  { name: 'redirect', status: 302 },
  { name: 'error response', status: 500 },
  { name: 'wrong success status', status: 201 },
  { name: 'wrong content type', type: 'text/plain' },
  { name: 'oversize response', body: JSON.stringify('x'.repeat(manifest.response_max_bytes)) },
  { name: 'unsafe integer', body: '{"value":9007199254740993}' },
  { name: 'invalid UTF-8', body: Buffer.from([0x22, 0xff, 0x22]) },
  { name: 'invalid JSON', body: '{"id":' },
]) {
  test(fixture.name + ' is refused without a retry', async t => {
    const calls = fakeHTTPS(t, fixture);
    await assert.rejects(requestJSON(manifest, operation, { path: { id: 'a' } }));
    assert.equal(calls.length, 1);
  });
}

test('cancellation before dispatch performs no request', async t => {
  const calls = fakeHTTPS(t);
  const controller = new AbortController();
  controller.abort();
  await assert.rejects(requestJSON(manifest, operation, { path: { id: 'a' } }, controller.signal));
  assert.equal(calls.length, 0);
});

test('the deadline bounds an entire stalled request without retry', async t => {
  const calls = fakeHTTPS(t, { stall: true });
  await assert.rejects(requestJSON({ ...manifest, timeout_ms: 5 }, operation, { path: { id: 'a' } }));
  assert.equal(calls.length, 1);
});
