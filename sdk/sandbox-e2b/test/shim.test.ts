// adr: 20261027090013 §12 — E2B-style code runs on Gregale by changing the
// import and the API key: Sandbox.create/connect/list/kill, commands in the
// foreground (streamed when callbacks are given) and background, files in
// and outside /workspace, ports and their preview tokens, terminals and
// command input (step 12); unsupported calls fail with an error naming the
// call and the alternative.

import assert from 'node:assert/strict';
import { test } from 'node:test';
import { CommandExitError, NotFoundError, Sandbox, SandboxNotFoundError, UnsupportedError } from '../src/index.js';

const API = 'https://api.gregale.test';
const ID = '0b0e6a1c-2f6a-4c8e-9d43-6f3f1d2a9b10';

type Exec = {
  cmd: string[];
  env?: Record<string, string>;
  run_as?: string;
  workdir?: string;
  timeout_ms?: number;
  background?: boolean;
  tty?: boolean;
  rows?: number;
  cols?: number;
};

class FakeGregale {
  state = 'queued';
  created?: Record<string, unknown>;
  patched?: Record<string, unknown>;
  deleted = 0;
  execs: Exec[] = [];
  files = new Map<string, Uint8Array>();
  ports: Record<string, unknown>[] = [];
  background = new Map<string, { out: string; polls: number; finishAfter: number; exit: number; killed?: boolean }>();
  inputs: { handle: string; data?: string; eof?: boolean; rows?: number; cols?: number }[] = [];
  sessionEnv?: Record<string, string>;
  code: Record<string, unknown>[] = [];

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
        const out = req.tty ? '$ ' : 'serving\n';
        this.background.set(handle, { out, polls: 0, finishAfter: script.includes('server') || req.tty ? 100 : 1, exit: 0 });
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
    const toInput = /^\/exec\/(c\d+)\/input$/.exec(sub);
    if (toInput && method === 'POST') {
      const c = this.background.get(toInput[1]!);
      if (!c) return json(404, { code: 'sandbox_entry_not_found', status: 404 });
      this.inputs.push({ handle: toInput[1]!, ...JSON.parse(text) });
      return json(200, { handle: toInput[1], argv: [], running: true });
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
      case 'POST /code': {
        const req = JSON.parse(text);
        this.code.push(req);
        const exec = {
          results: [
            { data: { 'image/png': 'iVBORw0KGgo=', 'text/plain': '<Figure>' } },
            { data: { 'text/plain': '42' }, is_main_result: true },
          ],
          logs: { stdout: ['hi\n'], stderr: [] },
          ...(req.code.includes('1/0') ? { error: { name: 'ZeroDivisionError', value: 'division by zero', traceback: 'Traceback' } } : {}),
          execution_count: 2,
        };
        if (headers.get('Accept') === 'text/event-stream') {
          const body = `event: stdout\ndata: "hi\\n"\n\nevent: result\ndata: ${JSON.stringify({ ...exec, logs: { stdout: [], stderr: [] } })}\n\n`;
          return new Response(body, { status: 200, headers: { 'Content-Type': 'text/event-stream' } });
        }
        return json(200, exec);
      }
      case 'POST /code/contexts':
        this.code.push({ create: JSON.parse(text) });
        return json(201, { id: 'abc123', language: 'python', cwd: '/workspace' });
      case 'GET /code/contexts':
        return json(200, { contexts: [{ id: 'default', language: 'python', cwd: '/workspace' }] });
      case 'POST /code/contexts/abc123/restart':
      case 'DELETE /code/contexts/abc123':
        this.code.push({ [method]: sub });
        return json(200, {});
      case 'PUT /env':
        this.sessionEnv = JSON.parse(text).env;
        return json(200, { names: Object.keys(this.sessionEnv ?? {}).sort() });
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
  // The envs are set in the session, so a command carries none of its own.
  assert.deepEqual(fake.sessionEnv, { CI: '1' });
  await sbx.commands.run('env');
  assert.equal(fake.execs.at(-1)?.env, undefined);
  assert.deepEqual(await sbx.setEnvs({ B: '2', A: '1' }), ['A', 'B']);
  const custom = await Sandbox.create('my-template', opts);
  assert.equal(custom.sandboxId, ID);
  assert.deepEqual(fake.created, { template: 'my-template' });
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
  await assert.rejects(sbx.commands.run('cat', { stdin: true }), (err) => err instanceof UnsupportedError && /background: true/.test(err.message));
});

test('a terminal is a background command on a pty, driven through its input', async () => {
  const { fake, opts } = setup();
  const sbx = await Sandbox.create(opts);
  const seen: Uint8Array[] = [];
  const term = await sbx.pty.create({ cols: 80, rows: 24, onData: (d) => void seen.push(d) });
  const exec = fake.execs.at(-1)!;
  assert.deepEqual([exec.tty, exec.background, exec.rows, exec.cols, exec.cmd], [true, true, 24, 80, ['/bin/bash', '-i', '-l']]);
  await sbx.pty.sendInput(term.pid, new TextEncoder().encode('ls\n'));
  await sbx.pty.resize(term.pid, { cols: 120, rows: 40 });
  assert.deepEqual(fake.inputs, [
    { handle: 'c1', data: Buffer.from('ls\n').toString('base64') },
    { handle: 'c1', rows: 40, cols: 120 },
  ]);
  for (let i = 0; i < 50 && seen.length === 0; i++) await new Promise((r) => setTimeout(r, 10));
  assert.equal(Buffer.from(seen[0]!).toString(), '$ ');
  assert.equal(await sbx.pty.kill(term.pid), true);
  await assert.rejects(term.wait(), CommandExitError);
});

test('runCode answers results, logs, errors and the value; contexts are managed', async () => {
  const { fake, opts } = setup();
  const sbx = await Sandbox.create('code-interpreter', opts);
  assert.deepEqual(fake.created, { preset: 'code-interpreter' });
  const exec = await sbx.runCode('x + 2');
  assert.equal(exec.text, '42');
  assert.equal(exec.executionCount, 2);
  assert.deepEqual(exec.logs, { stdout: ['hi\n'], stderr: [] });
  assert.equal(exec.results[0]!.png, 'iVBORw0KGgo=');
  assert.deepEqual(exec.results[0]!.formats(), ['text', 'png']);
  const seen: string[] = [];
  const failed = await sbx.runCode('1/0', {
    onStdout: (m) => void seen.push(`out:${m.line}`),
    onResult: (r) => void seen.push(`result:${r.text}`),
    onError: (e) => void seen.push(`error:${e.name}`),
  });
  assert.equal(failed.error?.name, 'ZeroDivisionError');
  assert.deepEqual(seen, ['out:hi\n', 'result:<Figure>', 'result:42', 'error:ZeroDivisionError']);
  assert.deepEqual(failed.logs.stdout, ['hi\n']);
  const ctx = await sbx.createCodeContext({ cwd: '/workspace/app' });
  await sbx.runCode('y = 1', { context: ctx, timeoutMs: 5_000 });
  assert.deepEqual(fake.code.at(-1), { code: 'y = 1', context: 'abc123', timeout_ms: 5_000 });
  assert.deepEqual((await sbx.listCodeContexts()).map((c) => c.id), ['default']);
  await sbx.restartCodeContext(ctx);
  await sbx.removeCodeContext('abc123');
  assert.deepEqual(fake.code.slice(-2), [{ POST: '/code/contexts/abc123/restart' }, { DELETE: '/code/contexts/abc123' }]);
  await assert.rejects(sbx.runCode('1', { envs: { A: '1' } }), UnsupportedError);
});

test('background commands take stdin and its end', async () => {
  const { fake, opts } = setup();
  const sbx = await Sandbox.create(opts);
  const cat = await sbx.commands.run('cat', { background: true, stdin: true });
  await sbx.commands.sendStdin(cat.pid, 'hello');
  await cat.sendStdin(' world');
  await sbx.commands.closeStdin(cat.pid);
  assert.deepEqual(fake.inputs, [
    { handle: 'c1', data: Buffer.from('hello').toString('base64') },
    { handle: 'c1', data: Buffer.from(' world').toString('base64') },
    { handle: 'c1', eof: true },
  ]);
  await assert.rejects(sbx.commands.sendStdin(42, 'x'), NotFoundError);
});
