// Tests gregale-browser's relay against a fake Chromium DevTools endpoint.
// Run with: node --test images/agent-base/gregale-browser.test.mjs
import assert from 'node:assert/strict';
import http from 'node:http';
import net from 'node:net';
import { test } from 'node:test';

process.env.GREGALE_BROWSER_PORT = '19222';
const { ACT_KEYS, actArgs, fetchArgs, isLocal, pageResult, refScript, relay, rewriteBody, snapshotScript, upgradeHead } = await import('./gregale-browser.mjs');

test('answers point WebSocket URLs at the caller', () => {
  const body = '{"webSocketDebuggerUrl":"ws://127.0.0.1:19221/devtools/browser/b","devtoolsFrontendUrl":"/x?ws=127.0.0.1:19221/devtools/page/p"}';
  const remote = rewriteBody(body, '9222-sbx.preview.example');
  assert.match(remote, /"wss:\/\/9222-sbx\.preview\.example\/devtools\/browser\/b"/);
  assert.match(remote, /wss=9222-sbx\.preview\.example\/devtools\/page\/p/);
  assert.match(rewriteBody(body, '127.0.0.1:19222'), /"ws:\/\/127\.0\.0\.1:19222\/devtools\/browser\/b"/);
  assert.ok(isLocal('localhost:9222') && isLocal('[::1]:9222') && !isLocal('9222-sbx.preview.example'));
});

test('upgrades reach Chromium with its own Host and no Origin', () => {
  const head = upgradeHead({ method: 'GET', url: '/devtools/browser/b', rawHeaders: ['Host', 'preview.example', 'Origin', 'https://evil', 'Upgrade', 'websocket'] });
  assert.ok(head.startsWith('GET /devtools/browser/b HTTP/1.1\r\nHost: 127.0.0.1:19221\r\n'));
  assert.ok(!/origin/i.test(head) && head.includes('Upgrade: websocket') && head.endsWith('\r\n\r\n'));
});

test('the relay forwards /json and WebSocket upgrades', async (t) => {
  const seen = [];
  const cdp = http.createServer((req, res) => {
    seen.push(req.headers.host);
    res.writeHead(200, { 'content-type': 'application/json' });
    res.end('{"webSocketDebuggerUrl":"ws://127.0.0.1:19221/devtools/browser/b"}');
  });
  cdp.on('upgrade', (req, socket) => {
    seen.push(req.headers.host);
    socket.end('HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\nhello');
  });
  await new Promise((r) => cdp.listen(19221, '127.0.0.1', r));
  const server = relay();
  await new Promise((r) => server.once('listening', r));
  t.after(() => {
    server.close();
    cdp.close();
  });

  const body = await new Promise((resolve, reject) => {
    http.get({ host: '127.0.0.1', port: 19222, path: '/json/version', headers: { host: '9222-sbx.preview.example' } }, (res) => {
      let data = '';
      res.on('data', (c) => (data += c));
      res.on('end', () => resolve(data));
    }).on('error', reject);
  });
  assert.equal(JSON.parse(body).webSocketDebuggerUrl, 'wss://9222-sbx.preview.example/devtools/browser/b');

  const reply = await new Promise((resolve, reject) => {
    const s = net.connect(19222, '127.0.0.1', () => {
      s.write('GET /devtools/browser/b HTTP/1.1\r\nHost: 9222-sbx.preview.example\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: x\r\nSec-WebSocket-Version: 13\r\n\r\n');
    });
    let data = '';
    s.on('data', (c) => (data += c));
    s.on('end', () => resolve(data));
    s.on('error', reject);
  });
  assert.match(reply, /^HTTP\/1\.1 101/);
  assert.ok(reply.endsWith('hello'));
  assert.deepEqual(seen, ['127.0.0.1:19221', '127.0.0.1:19221']);
});

test('fetch reads its flags and takes the URL only after --', () => {
  assert.deepEqual(fetchArgs(['--max-chars', '500', '--timeout', '30', '--', 'https://example.com/a']),
    { maxChars: 500, timeout: 30, url: 'https://example.com/a' });
  assert.deepEqual(fetchArgs(['--', 'http://example.com']), { maxChars: 20000, timeout: 60, url: 'http://example.com' });
  for (const argv of [['https://example.com'], ['--', 'file:///etc/passwd'], ['--max-chars', '0', '--', 'https://e.com'],
    ['--bogus', '1', '--', 'https://e.com'], ['--', '--max-chars']]) {
    assert.throws(() => fetchArgs(argv), /gregale-browser fetch/, argv.join(' '));
  }
});

test('a page result keeps at most max_chars characters, whole', () => {
  const page = pageResult('https://e.com', 'https://e.com/', 200, 'Title', 'héllo 😀 world', 7);
  assert.equal(page.text, 'héllo 😀');
  assert.ok(page.truncated);
  const whole = pageResult('https://e.com', '', 0, undefined, 'short', 100);
  assert.deepEqual(whole, { url: 'https://e.com', final_url: 'https://e.com', status: 0, title: '', text: 'short', truncated: false });
});

test('act reads its kind, flags and value, and refuses what the kind lacks', () => {
  assert.deepEqual(actArgs(['navigate', '--max-chars', '500', '--', 'https://example.com/']),
    { kind: 'navigate', maxChars: 500, timeout: 30, value: 'https://example.com/', submit: false });
  assert.deepEqual(actArgs(['type', '--ref', '3', '--submit', '--', '--not-a-flag']),
    { kind: 'type', maxChars: 4000, timeout: 30, value: '--not-a-flag', submit: true, ref: 3 });
  assert.equal(actArgs(['type', '--ref', '3', '--', '']).value, '');
  assert.equal(actArgs(['press', '--key', 'Enter']).key, 'Enter');
  assert.equal(actArgs(['scroll', '--direction', 'up']).direction, 'up');
  assert.equal(actArgs(['snapshot']).kind, 'snapshot');
  for (const bad of [['open'], ['navigate', '--', 'file:///etc/passwd'], ['click'], ['click', '--ref', '0'],
    ['type', '--ref', '2'], ['press', '--key', 'F12'], ['press', '--key', 'constructor'], ['scroll', '--direction', 'left'],
    ['back', '--ref', '1x'], ['snapshot', '--bogus', '1']]) {
    assert.throws(() => actArgs(bad), /gregale-browser act/, bad.join(' '));
  }
  assert.ok(Object.keys(ACT_KEYS).includes('PageDown'));
});

test('page scripts embed only the numbers act validated', () => {
  assert.match(refScript(12), /data-gregale-ref="12"/);
  assert.match(refScript('12"]; alert(1); //'), /data-gregale-ref="NaN"/);
  assert.match(snapshotScript(4000), /maxChars = 4000, limit = 150/);
  assert.doesNotThrow(() => new Function(snapshotScript(10)));
  assert.doesNotThrow(() => new Function(refScript(1)));
});

test('act takes a screenshot path only where vmmd puts it', () => {
  const path = '/workspace/.gregale-screenshot-0b6f8c1e-4d2a-4c3b-9f00-1234567890ab.jpg';
  assert.equal(actArgs(['snapshot', '--screenshot', path]).screenshot, path);
  for (const bad of ['/etc/passwd', '/workspace/.gregale-screenshot-x.jpg', '/tmp/../etc/gregale-screenshot-0b6f8c1e-4d2a-4c3b-9f00-1234567890ab.jpg', '']) {
    assert.throws(() => actArgs(['snapshot', '--screenshot', bad]), /--screenshot must be/, bad);
  }
});
