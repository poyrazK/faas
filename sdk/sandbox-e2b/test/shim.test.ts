// adr: 20261027090013 §12 — E2B-style code runs on Gregale by changing the
// import and the API key: Sandbox.create/connect/list/kill, commands in the
// foreground (streamed when callbacks are given) and background, files in
// and outside /workspace, ports and their preview tokens; unsupported calls
// fail with an error naming the call and the alternative.

import assert from 'node:assert/strict';
import { test } from 'node:test';
import { CommandExitError, NotFoundError, Sandbox, SandboxNotFoundError, UnsupportedError } from '../src/index.js';

const API = 'https://api.gregale.test';
const ID = '0b0e6a1c-2f6a-4c8e-9d43-6f3f1d2a9b10';

type Exec = { cmd: string[]; env?: Record<string, string>; run_as?: string; workdir?: string; timeout_ms?: number; background?: boolean };

class FakeGregale {
  state = 'queued';
  created?: Record<string, unknown>;
  patched?: Record<string, unknown>;
  deleted = 0;
  execs: Exec[] = [];
  files = new Map<string, Uint8Array>();
  ports: Record<string, unknown>[] = [];
  background = new Map<string, { out: string; polls: number; finishAfter: number; exit: number; killed?: boolean }>();

  fetch = async (input: string | URL | Request, init: RequestInit = {}): Promise<Response> => {
    const url = new URL(String(input));
    const headers = new Headers(init.headers);
    const auth = headers.get('Authorization');
    const method = init.method ?? 'GET';
    const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
    const text = typeof init.body === 'string' ? init.body : '';
    const base = `/v1/sandboxes/${ID}`;
    const path = url.pathname;
    if (auth === 'Bearer key-1') {
      if (method === 'POST' && path === '/v1/sandboxes') {
        this.created = JSON.parse(text);
        return json(201, this.view());
      }
      if (method === 'GET' && path === '/v1/sandboxes') return json(200, { items: [this.view()], next_before: '' });
      if (path === `/v1/sandboxes/${'1'.repeat(8)}-0000-0000-0000-000000000000`) return json(404, { code: 'sandbox_not_found', status: 404 });
      if (method === 'GET' && path === base) {
        if (this.state === 'queued') this.state = 'ready';
        return json(200, this.view());
      }
      if (method === 'PATCH' && path === base) {
        this.patched = JSON.parse(text);
        return json(200, this.view());
      }
      if (method === 'DELETE' && path === base) {
        this.deleted++;
        this.state = 'closed';
        return json(200, this.view());
      }
      if (method === 'POST' && path === `${base}/ports`) {
        const req = JSON.parse(text);
        this.ports.push(req);
        return json(201, { port: req.port, url: `https://${req.port}-${ID}.preview.test`, ...(req.visibility === 'private' ? { preview_token: 'sbxp1.tok' } : {}) });
      }
      if (method === 'GET' && path === `${base}/ports`) return json(200, { items: [{ port: 8080, url: `https://8080-${ID}.preview.test` }] });
    }
    if (auth !== 'Bearer sbx-1' || !path.startsWith(base)) return new Response('404 page not found', { status: 404 });
    const sub = path.slice(base.length);
    const rel = url.searchParams.get('path') ?? '';
    if (method === 'POST' && sub === '/exec') {
      const req = JSON.parse(text) as Exec;
      this.execs.push(req);
      const script = req.cmd.at(-1) ?? '';
      if (req.background) {
        const handle = `c${this.background.size + 1}`;
        this.background.set(handle, { out: 'serving\n', polls: 0, finishAfter: script.includes('server') ? 100 : 1, exit: 0 });
        return json(202, { handle, exit_code: 0, wall_time_ms: 0 });
      }
      const exit = script.includes('false') ? 1 : 0;
      if (script.includes('base64 < ')) return json(200, { stdout: Buffer.from('outside').toString('base64') + '\n', stderr: '', exit_code: 0, wall_time_s: 0 });
      if (headers.get('Accept') === 'text/event-stream') {
        const body = ['event: stdout\ndata: "line 1\\n"\n\n', 'event: stderr\ndata: "warn\\n"\n\n', `event: result\ndata: {"exit_code":${exit},"stdout":"","stderr":"","wall_time_s":0.1}\n\n`].join('');
        return new Response(body, { status: 200, headers: { 'Content-Type': 'text/event-stream' } });
      }
      return json(200, { stdout: `out:${script}`, stderr: exit ? 'boom' : '', exit_code: exit, wall_time_s: 0.1 });
    }
    if (method === 'GET' && sub === '/exec') {
      return json(200, { commands: [...this.background].map(([handle, c]) => ({ handle, argv: ['/bin/bash', '-l', '-c', 'npm run server'], running: c.polls < c.finishAfter && !c.killed })) });
    }
    const one = /^\/exec\/(c\d+)$/.exec(sub);
    if (one) {
      const c = this.background.get(one[1]!);
      if (!c) return json(404, { code: 'sandbox_entry_not_found', status: 404 });
      if (method === 'DELETE') {
        c.killed = true;
        c.exit = 137;
        return json(200, {});
      }
      c.polls++;
      const running = c.polls < c.finishAfter && !c.killed;
      const offset = Number(url.searchParams.get('stdout_offset'));
      return json(200, {
        handle: one[1],
        running,
        exit_code: c.exit,
        stdout: Buffer.from(c.out.slice(offset)).toString('base64'),
        stderr: null,
        stdout_offset: c.out.length,
        stderr_offset: 0,
      });
    }
    switch (`${method} ${sub}`) {
      case 'GET /files': {
        const f = this.files.get(rel);
        return f ? new Response(f) : json(404, { code: 'sandbox_entry_not_found', status: 404 });
      }
      case 'PUT /files':
        this.files.set(rel, new Uint8Array(init.body as Uint8Array));
        return json(200, { bytes: 1 });
      case 'GET /files:stat':
        return this.files.has(rel)
          ? json(200, { path: rel, type: 'file', size: this.files.get(rel)!.length, mode: 0o644, mtime_unix_ms: 0 })
          : json(404, { code: 'sandbox_entry_not_found', status: 404 });
      case 'GET /files:list':
        return json(200, { entries: [...this.files.keys()].map((p) => ({ path: p, type: 'file', size: 1, mode: 0o644, mtime_unix_ms: 0 })) });
      case 'POST /files:mkdir':
        return json(200, {});
      case 'POST /files:rename': {
        const { from, to } = JSON.parse(text);
        this.files.set(to, this.files.get(from)!);
        this.files.delete(from);
        return json(200, {});
      }
      case 'POST /files:remove':
        this.files.delete(JSON.parse(text).path);
        return json(200, {});
      case 'POST /workspace:persist':
        return json(200, { snapshot_id: 'snap-1' });
    }
    return json(404, { code: 'not_found' });
  };

  view() {
    return {
      id: ID,
      state: this.state,
      preset: 'codex',
      ram_mb: 2048,
      network: { mode: 'registries' },
      idle_timeout_s: 300,
      expires_at: '2026-11-05T04:00:00Z',
      created_at: '2026-11-05T00:00:00Z',
      labels: { team: 'evals' },
      ...(this.state === 'closed' ? {} : { token: 'sbx-1', token_expires_at: new Date(Date.now() + 900_000).toISOString() }),
    };
  }
}

function setup() {
  const fake = new FakeGregale();
  const opts = { apiKey: 'key-1', apiBase: API, fetch: fake.fetch as typeof fetch };
  return { fake, opts };
}

test('Sandbox.create maps E2B options and waits for the VM', async () => {
  const { fake, opts } = setup();
  const sbx = await Sandbox.create('base', { ...opts, timeoutMs: 120_500, metadata: { team: 'evals' }, envs: { CI: '1' }, allowInternetAccess: false, ports: [3000] });
  assert.equal(sbx.sandboxId, ID);
  assert.deepEqual(fake.created, { preset: 'codex', network: { mode: 'none' }, idle_timeout_s: 121, labels: { team: 'evals' } });
  assert.equal(sbx.getHost(3000), `3000-${ID}.preview.test`);
  assert.equal(sbx.previewToken(3000), 'sbxp1.tok');
  assert.throws(() => sbx.getHost(5173), (err) => err instanceof UnsupportedError && /exposePort\(5173\)/.test(err.message));
  await sbx.commands.run('env');
  assert.deepEqual(fake.execs.at(-1)?.env, { CI: '1' });
  await assert.rejects(Sandbox.create('my-template', opts), (err) => err instanceof UnsupportedError && /my-template/.test(err.message));
});

test('foreground commands answer results, stream to callbacks and throw on failure', async () => {
  const { fake, opts } = setup();
  const sbx = await Sandbox.create(opts);
  const res = await sbx.commands.run('ls -la', { cwd: '/workspace/app', user: 'root', timeoutMs: 5_000, envs: { A: 'b' } });
  assert.equal(res.stdout, 'out:ls -la');
  assert.equal(res.exitCode, 0);
  assert.deepEqual(fake.execs.at(-1), { cmd: ['/bin/bash', '-l', '-c', 'ls -la'], workdir: '/workspace/app', env: { A: 'b' }, run_as: 'root', timeout_ms: 5_000 });
  const seen: string[] = [];
  const streamed = await sbx.commands.run('make', { onStdout: (d) => void seen.push(`o:${d}`), onStderr: (d) => void seen.push(`e:${d}`) });
  assert.deepEqual(seen, ['o:line 1\n', 'e:warn\n']);
  assert.equal(streamed.stdout, 'line 1\n');
  await assert.rejects(sbx.commands.run('false'), (err) => err instanceof CommandExitError && err.exitCode === 1 && err.stderr === 'boom');
  await assert.rejects(sbx.commands.run('id', { user: 'nobody' }), UnsupportedError);
  await assert.rejects(sbx.commands.run('cat', { stdin: true }), UnsupportedError);
});

test('background commands give a pid that list, kill and wait use', async () => {
  const { opts } = setup();
  const sbx = await Sandbox.create(opts);
  const quick = await sbx.commands.run('echo hi', { background: true });
  const result = await quick.wait();
  assert.equal(result.stdout, 'serving\n');
  const server = await sbx.commands.run('npm run server', { background: true });
  assert.equal(server.pid, 2);
  const running = await sbx.commands.list();
  assert.deepEqual(
    running.map((p) => p.pid),
    [2],
  );
  assert.equal(await sbx.commands.kill(server.pid), true);
  await assert.rejects(server.wait(), (err) => err instanceof CommandExitError && err.exitCode === 137);
  assert.equal(await sbx.commands.kill(99), false);
});

test('files inside /workspace use the files API; outside paths go through commands', async () => {
  const { fake, opts } = setup();
  const sbx = await Sandbox.create(opts);
  const info = await sbx.files.write('notes/a.txt', 'hello');
  assert.deepEqual(info, { name: 'a.txt', path: '/workspace/notes/a.txt', type: 'file' });
  assert.equal(await sbx.files.read('/workspace/notes/a.txt'), 'hello');
  assert.deepEqual(await sbx.files.read('notes/a.txt', { format: 'bytes' }), new TextEncoder().encode('hello'));
  assert.equal(await sbx.files.exists('notes/a.txt'), true);
  assert.equal(await sbx.files.exists('notes/missing'), false);
  assert.equal((await sbx.files.getInfo('notes/a.txt')).permissions, '-rw-r--r--');
  assert.deepEqual(
    (await sbx.files.list('/workspace')).map((e) => e.path),
    ['/workspace/notes/a.txt'],
  );
  await sbx.files.rename('notes/a.txt', 'notes/b.txt');
  assert.ok(fake.files.has('notes/b.txt'));
  await sbx.files.remove('notes/b.txt');
  assert.equal(fake.files.size, 0);
  await assert.rejects(sbx.files.read('/workspace/missing'), NotFoundError);
  assert.equal(await sbx.files.read('/home/user/outside.txt'), 'outside');
  await sbx.files.write('/home/user/x.txt', 'abc');
  assert.match(fake.execs.at(-1)!.cmd.at(-1)!, /base64 -d > '\/home\/user\/x.txt'/);
  await assert.rejects(sbx.files.write('/tmp/big', new Uint8Array(300 << 10)), UnsupportedError);
  await assert.rejects(sbx.files.list('/etc'), UnsupportedError);
  await assert.rejects(sbx.files.watchDir(), UnsupportedError);
});

test('connect, list, setTimeout and kill', async () => {
  const { fake, opts } = setup();
  const first = await Sandbox.create(opts);
  const again = await Sandbox.connect(first.sandboxId, opts);
  assert.equal(again.getHost(8080), `8080-${ID}.preview.test`);
  const page = await Sandbox.list(opts).nextItems();
  assert.deepEqual(
    page.map((i) => [i.sandboxId, i.state, i.memoryMB, i.metadata.team]),
    [[ID, 'running', 2048, 'evals']],
  );
  assert.equal((await again.createSnapshot()).snapshotId, 'snap-1');
  await again.setTimeout(60_000);
  assert.deepEqual(fake.patched, { idle_timeout_s: 60 });
  assert.equal(await again.isRunning(), true);
  assert.equal(await again.kill(), true);
  assert.equal(fake.deleted, 1);
  assert.equal(await again.isRunning(), false);
  await assert.rejects(Sandbox.connect('11111111-0000-0000-0000-000000000000', opts), SandboxNotFoundError);
  await assert.rejects(Sandbox.connect('../../v1/keys', opts), /not a Gregale sandbox ID/);
});

test('unsupported calls name the call and the alternative', async () => {
  const { opts } = setup();
  const sbx = await Sandbox.create(opts);
  await assert.rejects(sbx.betaPause(), (err) => err instanceof UnsupportedError && /freeze on their own/.test(err.message));
  await assert.rejects(sbx.pty.create(), (err) => err instanceof UnsupportedError && err.call === 'pty.create');
  await assert.rejects(sbx.commands.sendStdin(), UnsupportedError);
});
