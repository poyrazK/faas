// adr: 20261027090013 §8 — GregaleSandboxClient creates a session, waits for
// it to run, materializes the manifest, runs commands (polling background
// ones by session ID), reads and edits files, exposes ports with a token
// that is never part of the state, serializes only the session's identity,
// refuses state for another API origin, and closes the session.

import assert from 'node:assert/strict';
import { mkdtemp, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { gunzipSync } from 'node:zlib';
import { test } from 'node:test';
import { run } from '@openai/agents-core';
import { Manifest, SandboxAgent, SandboxUnsupportedFeatureError, shell } from '@openai/agents-core/sandbox';
import { ScriptedModel, assistantMessage, functionCall } from '@openai/agents-core/testing';
import { GregaleSandboxClient } from '../src/index.js';
import { triagePullRequest } from '../examples/pr-triage.js';

const API = 'https://api.gregale.test';
const ID = '0b0e6a1c-2f6a-4c8e-9d43-6f3f1d2a9b10';

type Command = { cmd: string[]; env?: Record<string, string>; run_as?: string };

/** An in-memory Gregale API: one session, a workspace of files, and commands. */
class FakeGregale {
  state = 'queued';
  reads = 0;
  tokens = 0;
  staleTokens = new Set<string>();
  files = new Map<string, Uint8Array>();
  hydrated: Uint8Array[] = [];
  commands: Command[] = [];
  created?: Record<string, unknown>;
  ports: Record<string, unknown>[] = [];
  deleted = 0;
  background = new Map<string, { out: string; polls: number; finishAfter: number }>();

  fetch = async (input: string | URL | Request, init: RequestInit = {}): Promise<Response> => {
    const url = new URL(String(input));
    const auth = new Headers(init.headers).get('Authorization') ?? '';
    const method = init.method ?? 'GET';
    const path = url.pathname;
    const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
    const control = auth === 'Bearer key-1';
    const data = auth.startsWith('Bearer sbx-') && !this.staleTokens.has(auth.slice(7));
    const body = init.body;
    const text = typeof body === 'string' ? body : '';
    const base = `/v1/sandboxes/${ID}`;

    if (control && method === 'POST' && path === '/v1/sandboxes') {
      this.created = JSON.parse(text);
      return json(201, this.view());
    }
    if (control && method === 'GET' && path === base) {
      this.reads++;
      if (this.state === 'queued') this.state = 'starting';
      else if (this.state === 'starting') this.state = 'ready';
      return json(200, this.view());
    }
    if (control && method === 'DELETE' && path === base) {
      this.deleted++;
      this.state = 'closed';
      return json(200, this.view());
    }
    if (control && method === 'POST' && path === `${base}/ports`) {
      const req = JSON.parse(text);
      this.ports.push(req);
      return json(201, {
        port: req.port,
        visibility: req.visibility,
        url: `https://${req.port}-${ID}.preview.test`,
        created_at: '2026-11-05T00:00:00Z',
        ...(req.visibility === 'private' ? { preview_token: 'sbxp1.secret', preview_token_expires_at: '2026-11-05T01:00:00Z' } : {}),
      });
    }
    if (!path.startsWith(base)) return new Response('404 page not found', { status: 404 });
    if (!data) return new Response('404 page not found', { status: 404 });
    const sub = path.slice(base.length);
    const rel = url.searchParams.get('path') ?? '';
    switch (`${method} ${sub}`) {
      case 'POST /exec': {
        const req = JSON.parse(text) as Command & { background?: boolean };
        this.commands.push(req);
        const script = req.cmd[2] ?? '';
        if (req.background) {
          const handle = `c${this.background.size + 1}`;
          const long = script.includes('server');
          this.background.set(handle, { out: long ? 'listening\n' : `ran ${script}\n`, polls: 0, finishAfter: long ? 4 : 0 });
          return json(202, { handle, exit_code: 0, wall_time_ms: 0 });
        }
        if (script.startsWith('tar ')) {
          const name = /-cf \/workspace\/(\S+)/.exec(script)![1]!;
          this.files.set(name, new TextEncoder().encode('TARBYTES'));
        }
        return json(200, { stdout: `ran\n`, stderr: '', exit_code: 0, wall_time_s: 0.01 });
      }
      case 'GET /files': {
        const f = this.files.get(rel);
        return f ? new Response(f, { status: 200 }) : json(404, { code: 'sandbox_entry_not_found', status: 404 });
      }
      case 'PUT /files':
        this.files.set(rel, new Uint8Array(body as Uint8Array));
        return json(200, { bytes: (body as Uint8Array).length });
      case 'GET /files:stat':
        return this.files.has(rel)
          ? json(200, { path: rel, type: 'file', size: this.files.get(rel)!.length })
          : rel === 'src'
            ? json(200, { path: rel, type: 'dir', size: 0 })
            : json(404, { code: 'sandbox_entry_not_found', status: 404 });
      case 'GET /files:list':
        return json(200, {
          entries: [
            { path: 'src/a.ts', type: 'file', size: 1 },
            { path: 'src/lib', type: 'dir', size: 0 },
            { path: 'src/link', type: 'symlink', size: 0 },
          ],
        });
      case 'POST /files:remove':
        this.files.delete(JSON.parse(text).path);
        return json(200, {});
      case 'POST /workspace:hydrate':
        this.hydrated.push(new Uint8Array(body as Uint8Array));
        return json(200, { bytes: 1, entries: 1 });
    }
    const out = /^\/exec\/(c\d+)$/.exec(sub);
    if (method === 'GET' && out) {
      const cmd = this.background.get(out[1]!)!;
      const offset = Number(url.searchParams.get('stdout_offset'));
      cmd.polls++;
      const running = cmd.polls <= cmd.finishAfter;
      const chunk = cmd.out.slice(offset);
      return json(200, {
        handle: out[1],
        running,
        exit_code: running ? 0 : 3,
        stdout: Buffer.from(chunk).toString('base64'),
        stderr: null,
        stdout_offset: cmd.out.length,
        stderr_offset: 0,
      });
    }
    return json(404, { code: 'not_found' });
  };

  view() {
    const placed = this.state === 'ready';
    const token = `sbx-${++this.tokens}`;
    return {
      id: ID,
      state: this.state,
      kind: 'ephemeral',
      preset: 'codex',
      ram_mb: 1024,
      network: { mode: 'registries' },
      expires_at: '2026-11-05T04:00:00Z',
      labels: {},
      ...(this.state !== 'closed' ? { token, token_expires_at: new Date(Date.now() + (placed ? 900_000 : 900_000)).toISOString() } : {}),
    };
  }
}

function setup() {
  const fake = new FakeGregale();
  const client = new GregaleSandboxClient({ apiKey: 'key-1', apiBase: API, fetch: fake.fetch as typeof fetch, readyTimeoutMs: 10_000 });
  return { fake, client };
}

test('create waits for the VM and materializes the manifest', async () => {
  const { fake, client } = setup();
  const dir = await mkdtemp(join(tmpdir(), 'gregale-sbx-'));
  await writeFile(join(dir, 'notes.txt'), 'local notes');
  const manifest = new Manifest({
    entries: {
      'README.md': { type: 'file', content: 'hello' },
      docs: { type: 'local_dir', src: dir },
      repo: { type: 'git_repo', repo: 'openai/openai-agents-js', ref: 'main' },
    },
    environment: { CI: '1' },
  });
  const session = await client.create({ manifest, options: { network: 'web', labels: { team: 'triage' } } });
  assert.equal(fake.state, 'ready');
  assert.deepEqual(fake.created, { preset: 'codex', network: { mode: 'web' }, labels: { team: 'triage' } });
  assert.equal(fake.hydrated.length, 1);
  const archive = new TextDecoder('latin1').decode(gunzipSync(fake.hydrated[0]!));
  for (const name of ['README.md', 'hello', 'docs/', 'docs/notes.txt', 'local notes']) assert.ok(archive.includes(name), name);
  const clone = fake.commands.find((c) => c.cmd[2]?.includes('git fetch'));
  assert.ok(clone?.cmd[2]?.includes("'https://github.com/openai/openai-agents-js.git'"));
  assert.ok(clone?.cmd[2]?.includes("origin 'main'"));
  assert.deepEqual(clone?.env, { CI: '1' });
  assert.equal(session.state.workspaceReady, true);
});

test('unsupported manifests fail before a session is created', async () => {
  const { fake, client } = setup();
  const manifest = new Manifest({ entries: { data: { type: 's3_mount', bucket: 'b' } as never } });
  await assert.rejects(client.create({ manifest }), (err) => err instanceof SandboxUnsupportedFeatureError && /"data"/.test(err.message));
  await assert.rejects(client.create({ manifest: new Manifest({ root: '/app' }) }), SandboxUnsupportedFeatureError);
  assert.equal(fake.created, undefined);
});

test('commands run to completion or yield a session ID that write_stdin polls', async () => {
  const { fake, client } = setup();
  const session = await client.create();
  const quick = await session.execCommand({ cmd: 'ls', yieldTimeMs: 2_000 });
  assert.match(quick, /Process exited with code 3/);
  assert.match(quick, /ran ls/);
  const server = await session.execCommand({ cmd: 'npm run server', yieldTimeMs: 0 });
  assert.match(server, /Process running with session ID 1/);
  assert.match(server, /listening/);
  await assert.rejects(session.writeStdin({ sessionId: 1, chars: 'q' }), SandboxUnsupportedFeatureError);
  const done = await session.writeStdin({ sessionId: 1, yieldTimeMs: 5_000 });
  assert.match(done, /Process exited with code 3/);
  await assert.rejects(session.writeStdin({ sessionId: 1 }), /no running command/);
  assert.equal(session.supportsPty(), true);
  assert.deepEqual(fake.commands.at(-1)?.cmd, ['/bin/sh', '-c', 'npm run server']);
  await assert.rejects(session.exec({ cmd: 'id', runAs: 'nobody' }), SandboxUnsupportedFeatureError);
  const root = await session.exec({ cmd: 'id', runAs: 'root' });
  assert.equal(root.exitCode, 0);
  assert.equal(fake.commands.at(-1)?.run_as, 'root');
});

test('files: read, list, stat and the editor', async () => {
  const { fake, client } = setup();
  const session = await client.create();
  fake.files.set('src/a.ts', new TextEncoder().encode('const a = 1;\n'));
  assert.equal(new TextDecoder().decode(await session.readFile({ path: '/workspace/src/a.ts' })), 'const a = 1;\n');
  assert.equal(await session.pathExists('src/a.ts'), true);
  assert.equal(await session.pathExists('/workspace/missing'), false);
  assert.equal(await session.directoryExists('src'), true);
  assert.deepEqual(await session.listDir({ path: '/workspace/src' }), [
    { name: 'a.ts', path: '/workspace/src/a.ts', type: 'file' },
    { name: 'lib', path: '/workspace/src/lib', type: 'dir' },
    { name: 'link', path: '/workspace/src/link', type: 'other' },
  ]);
  await assert.rejects(session.readFile({ path: '/workspace/missing' }), /no such file/);
  await assert.rejects(session.readFile({ path: 'src/a.ts', maxBytes: 4 }), /over maxBytes/);
  const editor = session.createEditor();
  await editor.updateFile({ type: 'update_file', path: 'src/a.ts', diff: '@@\n-const a = 1;\n+const a = 2;\n' });
  assert.equal(new TextDecoder().decode(fake.files.get('src/a.ts')), 'const a = 2;\n');
  await editor.createFile({ type: 'create_file', path: 'src/b.ts', diff: '+export {};\n' });
  assert.equal(new TextDecoder().decode(fake.files.get('src/b.ts')), 'export {};');
  await editor.deleteFile({ type: 'delete_file', path: 'src/b.ts' });
  assert.equal(fake.files.has('src/b.ts'), false);
  await assert.rejects(session.writeFile('/etc/passwd', 'x'), /outside/);
});

test('a stale token is re-minted once', async () => {
  const { fake, client } = setup();
  const session = await client.create();
  for (let i = 1; i <= fake.tokens; i++) fake.staleTokens.add(`sbx-${i}`);
  const reads = fake.reads;
  const res = await session.exec({ cmd: 'true' });
  assert.equal(res.exitCode, 0);
  assert.equal(fake.reads, reads + 1);
});

test('ports carry their token outside the session state', async () => {
  const { fake, client } = setup();
  const session = await client.create();
  const endpoint = await session.resolveExposedPort(3000);
  assert.deepEqual(fake.ports, [{ port: 3000, visibility: 'private' }]);
  assert.equal(endpoint.host, `3000-${ID}.preview.test`);
  assert.equal(endpoint.port, 443);
  assert.equal(endpoint.tls, true);
  assert.equal(endpoint.query, 'gregale_preview_token=sbxp1.secret');
  assert.equal(session.state.exposedPorts, undefined);
  const serialized = JSON.stringify(await client.serializeSessionState(session.state));
  assert.ok(!serialized.includes('secret') && !serialized.includes('sbx-'));
});

test('state serializes to its identity and resumes only on this API', async () => {
  const { fake, client } = setup();
  const session = await client.create();
  const saved = await client.serializeSessionState(session.state);
  assert.deepEqual(saved, { sandboxId: ID, apiBase: API });
  const state = await client.deserializeSessionState({ ...saved, manifest: { root: '/workspace' } });
  const resumed = await client.resume(state);
  assert.equal(resumed.sandboxId, ID);
  await assert.rejects(client.deserializeSessionState({ sandboxId: ID, apiBase: 'https://evil.test' }), /not this client's/);
  await assert.rejects(client.deserializeSessionState({ sandboxId: '../../v1/keys', apiBase: API }), /names no Gregale sandbox/);
  await resumed.close();
  await resumed.close();
  assert.equal(fake.deleted, 1);
  assert.equal(await resumed.running(), false);
  await assert.rejects(client.resume(state), /is closed/);
});

test('the workspace round-trips as an archive', async () => {
  const { fake, client } = setup();
  const session = await client.create();
  const archive = await session.persistWorkspace();
  assert.equal(new TextDecoder().decode(archive), 'TARBYTES');
  assert.equal([...fake.files.keys()].filter((k) => k.startsWith('.gregale-persist-')).length, 0);
  await session.hydrateWorkspace(archive);
  assert.equal(gunzipSync(fake.hydrated.at(-1)!).toString(), 'TARBYTES');
});

test('the client needs an API key', () => {
  const saved = process.env.GREGALE_API_KEY;
  delete process.env.GREGALE_API_KEY;
  try {
    assert.throws(() => new GregaleSandboxClient({ apiBase: API }), /API key/);
  } finally {
    if (saved !== undefined) process.env.GREGALE_API_KEY = saved;
  }
});

// The SDK's own runner drives the client: it creates the session, the
// shell tool runs in it, and the run's owned session is closed at the end.
test('a SandboxAgent run executes its shell tool in a Gregale session', async () => {
  const { fake, client } = setup();
  const model = new ScriptedModel([
    [functionCall('exec_command', { cmd: 'ls' }, { callId: 'call-1' })],
    [assistantMessage('triaged')],
  ]);
  const agent = new SandboxAgent({ name: 'triage', instructions: 'Triage the PR.', model, capabilities: [shell()] });
  const result = await run(agent, 'go', { sandbox: { client, manifest: new Manifest({ entries: { 'PR.md': { type: 'file', content: '# PR' } } }) } });
  assert.equal(result.finalOutput, 'triaged');
  assert.ok(fake.commands.some((c) => c.cmd[2] === 'ls'), 'the shell tool ran in the session');
  assert.equal(fake.hydrated.length, 1);
  const toolOutput = JSON.stringify(model.calls.at(-1));
  assert.match(toolOutput, /Process exited with code 3/);
  assert.equal(fake.deleted, 1, 'the owned session was closed');
});

// The acceptance example, with a scripted model: the PR head is checked out
// in the session, the agent inspects it there, and the report comes back.
test('the PR triage example checks out the PR and reports', async () => {
  const { fake, client } = setup();
  const model = new ScriptedModel([
    [functionCall('exec_command', { cmd: 'git -C /workspace/repo log -1 --stat' }, { callId: 'call-1' })],
    [assistantMessage('Risk: low. Labels: docs.')],
  ]);
  const report = await triagePullRequest({ repo: 'openai/openai-agents-js', pr: 42, client, model });
  assert.equal(report, 'Risk: low. Labels: docs.');
  const clone = fake.commands.find((c) => c.cmd[2]?.includes('git fetch'));
  assert.ok(clone?.cmd[2]?.includes("origin 'refs/pull/42/head'"));
  assert.ok(fake.commands.some((c) => c.cmd[2] === 'git -C /workspace/repo log -1 --stat'));
  assert.equal(fake.deleted, 1);
});
