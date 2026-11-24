// adr: 20261124075005 §6 — E2B Desktop-style code drives a Gregale desktop:
// Desktop.create asks for preset desktop and its resolution, every action
// runs gregale-desktop as an argv with DISPLAY set (never shell text), a
// screenshot comes back as PNG bytes, and the live view is the noVNC page of
// a private preview port with its token, view-only on its own port.

import assert from 'node:assert/strict';
import { test } from 'node:test';
import { CommandExitError, Desktop, InvalidArgumentError } from '../src/index.js';

const API = 'https://api.gregale.test';
const ID = '0b0e6a1c-2f6a-4c8e-9d43-6f3f1d2a9b10';
const PNG = Uint8Array.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 1, 2, 3]);

type Exec = { cmd: string[]; env?: Record<string, string>; timeout_ms?: number };

class FakeDesktop {
  created?: Record<string, unknown>;
  execs: Exec[] = [];
  ports = new Map<number, string>();
  portCalls: string[] = [];

  fetch = async (input: string | URL | Request, init: RequestInit = {}): Promise<Response> => {
    const url = new URL(String(input));
    const auth = new Headers(init.headers).get('Authorization');
    const method = init.method ?? 'GET';
    const text = typeof init.body === 'string' ? init.body : '';
    const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
    const base = `/v1/sandboxes/${ID}`;
    const path = url.pathname;
    const view = () => ({
      id: ID, state: 'ready', kind: 'ephemeral', preset: 'image', ram_mb: 2048, network: { mode: 'web' }, idle_timeout_s: 300,
      expires_at: '2026-11-25T04:00:00Z', created_at: '2026-11-25T00:00:00Z', resolution: '1280x800',
      token: 'sbx-1', token_expires_at: new Date(Date.now() + 900_000).toISOString(),
    });
    const portView = (port: number) => ({ port, url: `https://${port}-${ID}.preview.test`, preview_token: `tok-${port}` });
    if (auth === 'Bearer key-1') {
      if (method === 'POST' && path === '/v1/sandboxes') {
        this.created = JSON.parse(text);
        for (const p of [6080, 6081]) this.ports.set(p, 'private');
        return json(201, view());
      }
      if (method === 'GET' && path === base) return json(200, view());
      const port = /^\/ports(?:\/(\d+))?$/.exec(path.slice(base.length));
      if (port) {
        this.portCalls.push(`${method} ${path.slice(base.length)}`);
        if (method === 'POST') {
          const req = JSON.parse(text);
          this.ports.set(req.port, req.visibility);
          return json(201, portView(req.port));
        }
        const n = Number(port[1]);
        if (!this.ports.has(n)) return json(404, { code: 'sandbox_port_not_found', status: 404 });
        if (method === 'DELETE') {
          this.ports.delete(n);
          return new Response(null, { status: 204 });
        }
        return json(200, portView(n));
      }
      return json(404, { code: 'not_found' });
    }
    if (auth !== 'Bearer sbx-1' || path !== `${base}/exec` || method !== 'POST') return new Response('404 page not found', { status: 404 });
    const req = JSON.parse(text) as Exec;
    this.execs.push(req);
    const [, sub, ...rest] = req.cmd;
    const ok = (stdout: string) => json(200, { stdout, stderr: '', exit_code: 0, wall_time_s: 0.01 });
    switch (sub) {
      case 'screenshot':
        return ok(Buffer.from(PNG).toString('base64') + '\n');
      case 'cursor':
        return ok('17 42\n');
      case 'size':
        return ok('1280x800\n');
      case 'windows':
        return ok(rest.includes('mousepad') ? '41943043\tmousepad.Mousepad\tUntitled 1 - Mousepad\n' : '41943043\tmousepad.Mousepad\tUntitled 1 - Mousepad\n52428803\tchromium.Chromium\tNew Tab\n');
      case 'window':
        return ok('52428803\t\tNew Tab\n');
      case 'click':
        if (rest[0] === '5000') return json(200, { stdout: '', stderr: 'gregale-desktop: (5000, 1) is outside the 1280x800 screen\n', exit_code: 2, wall_time_s: 0 });
        return ok('');
      default:
        return ok('');
    }
  };
}

async function desktop() {
  const fake = new FakeDesktop();
  const d = await Desktop.create({ apiKey: 'key-1', apiBase: API, fetch: fake.fetch as typeof fetch, resolution: [1280, 800] });
  return { fake, d };
}

const argv = (fake: FakeDesktop) => fake.execs.map((e) => e.cmd.slice(1));

test('Desktop.create asks for preset desktop at its resolution', async () => {
  const { fake, d } = await desktop();
  assert.ok(d instanceof Desktop);
  assert.deepEqual(fake.created, { preset: 'desktop', resolution: '1280x800' });
});

test('actions run gregale-desktop as an argv with DISPLAY', async () => {
  const { fake, d } = await desktop();
  await d.leftClick(10, 20);
  await d.rightClick(1, 2);
  await d.doubleClick(3, 4);
  await d.middleClick();
  await d.moveMouse(5, 6);
  await d.mousePress('right');
  await d.mouseRelease();
  await d.drag([1, 2], [30, 40]);
  await d.scroll('up', 3);
  await d.write('$(reboot); echo "hi"');
  await d.press('enter');
  await d.press(['ctrl', 'c']);
  await d.launch('chromium', 'https://example.com');
  await d.open('/workspace/report.pdf');
  assert.deepEqual(argv(fake), [
    ['click', '10', '20', '--button', 'left'],
    ['click', '1', '2', '--button', 'right'],
    ['click', '3', '4', '--button', 'left', '--double'],
    ['cursor'],
    ['click', '17', '42', '--button', 'middle'],
    ['move', '5', '6'],
    ['down', '--button', 'right'],
    ['up', '--button', 'left'],
    ['drag', '1', '2', '30', '40'],
    ['scroll', 'up', '3'],
    ['type', '--', '$(reboot); echo "hi"'],
    ['key', '--', 'enter'],
    ['key', '--', 'ctrl+c'],
    ['launch', 'chromium', '--', 'https://example.com'],
    ['open', '--', '/workspace/report.pdf'],
  ]);
  for (const e of fake.execs) {
    assert.equal(e.cmd[0], '/usr/local/bin/gregale-desktop');
    assert.equal(e.env?.DISPLAY, ':0');
  }
});

test('reads: screenshot bytes, cursor, size and windows', async () => {
  const { d } = await desktop();
  assert.deepEqual(await d.screenshot(), PNG);
  const blob = await d.screenshot('blob');
  assert.equal(blob.type, 'image/png');
  assert.deepEqual(new Uint8Array(await blob.arrayBuffer()), PNG);
  assert.deepEqual(await d.getCursorPosition(), { x: 17, y: 42 });
  assert.deepEqual(await d.getScreenSize(), { width: 1280, height: 800 });
  assert.deepEqual(await d.getApplicationWindows('mousepad'), ['41943043']);
  assert.equal(await d.getCurrentWindowId(), '52428803');
  assert.equal(await d.getWindowTitle('41943043'), 'Untitled 1 - Mousepad');
  await assert.rejects(d.getWindowTitle('1'), InvalidArgumentError);
});

test('bad input fails before or in the guest', async () => {
  const { fake, d } = await desktop();
  await assert.rejects(d.leftClick(1.5, 2), InvalidArgumentError);
  await assert.rejects(d.leftClick(-1, 2), InvalidArgumentError);
  await assert.rejects(d.leftClick(1), InvalidArgumentError);
  assert.equal(fake.execs.length, 0);
  const err = await d.leftClick(5000, 1).then(
    () => undefined,
    (e: unknown) => e,
  );
  assert.ok(err instanceof CommandExitError);
  assert.equal(err.exitCode, 2);
  assert.match(err.message, /outside the 1280x800 screen/);
});

test('long text is typed in chunks', async () => {
  const { fake, d } = await desktop();
  await d.write('abcdefgh', { chunkSize: 3 });
  assert.deepEqual(argv(fake), [
    ['type', '--', 'abc'],
    ['type', '--', 'def'],
    ['type', '--', 'gh'],
  ]);
});

test('the stream is the noVNC page of a private port with its token', async () => {
  const { fake, d } = await desktop();
  const url = new URL(await d.stream.getUrl());
  assert.equal(url.host, `6080-${ID}.preview.test`);
  assert.equal(url.pathname, '/vnc.html');
  assert.equal(url.searchParams.get('autoconnect'), '1');
  assert.equal(url.searchParams.get('resize'), 'scale');
  assert.equal(url.searchParams.get('path'), 'websockify');
  assert.equal(url.searchParams.get('gregale_preview_token'), 'tok-6080');
  assert.equal(url.searchParams.get('view_only'), null);

  const viewOnly = new URL(await d.stream.getUrl({ viewOnly: true, autoConnect: false }));
  assert.equal(viewOnly.host, `6081-${ID}.preview.test`);
  assert.equal(viewOnly.searchParams.get('view_only'), '1');
  assert.equal(viewOnly.searchParams.get('autoconnect'), '0');
  assert.equal(viewOnly.searchParams.get('gregale_preview_token'), 'tok-6081');
  assert.equal(await d.stream.getAuthKey({ viewOnly: true }), 'tok-6081');

  await d.stream.stop();
  assert.equal(fake.ports.size, 0);
  await assert.rejects(d.stream.getUrl());
  await d.stream.start();
  assert.deepEqual([...fake.ports], [
    [6080, 'private'],
    [6081, 'private'],
  ]);
});
