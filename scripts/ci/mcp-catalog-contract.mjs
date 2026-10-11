// Run from a freshly materialized starter with its pinned dependencies installed:
// node mcp-catalog-contract.mjs /path/gregale /path/testdata/mcp-catalog /path/mcp-catalog-check.sh
import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { once } from 'node:events';
import { mkdir, mkdtemp, readFile, rm, symlink, writeFile } from 'node:fs/promises';
import { createServer } from 'node:http';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { promisify } from 'node:util';
import { generateKeyPair, SignJWT } from 'jose';
import { createApp } from './app.js';

const [cli, baselineDir, gateScript] = process.argv.slice(2);
assert.ok(cli && baselineDir && gateScript, 'provide CLI, reviewed baseline directory and workflow gate script');
const runCLI = promisify(execFile);
const work = await mkdtemp(join(tmpdir(), 'gregale-mcp-catalog-'));
await mkdir(join(work, 'caller-baseline'));
const { publicKey, privateKey } = await generateKeyPair('RS256');
const config = {
  version: 1, endpoint: '/mcp', transport: 'streamable-http', mode: 'stateless', legacy: true, allowed_origins: [],
  auth: {
    mode: 'external-oauth', issuer: 'https://issuer.example', jwks_url: 'https://issuer.example/jwks',
    resource: 'https://mcp.example/mcp', scopes: ['mcp:tools'],
  },
};
const credentials = {};
for (const [role, scope] of [['reader', 'mcp:tools math:read'], ['writer', 'mcp:tools math:read math:write']]) {
  credentials[role] = await new SignJWT({ scope }).setProtectedHeader({ alg: 'RS256' })
    .setAudience(config.auth.resource).setIssuer(config.auth.issuer).setSubject('fixture-private-identity')
    .setExpirationTime('5m').sign(privateKey);
}
const methods = [], calls = [], servers = [];
async function serve(toolScopes) {
  const { app, handler } = createApp({ ...config, auth: { ...config.auth, tool_scopes: toolScopes } }, { keyResolver: publicKey, log: line => calls.push(line) });
  const server = createServer((req, res) => {
    // Observe Express's parsed request after dispatch without consuming its stream.
    res.on('finish', () => { if (req.body?.method) methods.push(req.body.method); });
    app(req, res);
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  servers.push(async () => { await handler.close(); await new Promise(resolve => server.close(resolve)); });
  return `http://127.0.0.1:${server.address().port}/mcp`;
}
async function command(args, expected = 0, env = process.env) {
  try {
    const result = await runCLI(cli, args, { env, timeout: 30000 });
    assert.equal(expected, 0, 'expected a rejected gate');
    return JSON.parse(result.stdout);
  } catch (error) {
    if (expected !== 0 && error.code === expected) return JSON.parse(error.stdout);
    throw error;
  }
}
async function capture(endpoint, role, legacy, name) {
  const path = join(work, `${name}.json`);
  const args = ['mcp', 'lock', '--url', endpoint, '--out', path, '--token-env', 'GREG_MCP_FIXTURE_TOKEN', '--json'];
  if (legacy) args.push('--legacy');
  assert.equal((await command(args, 0, { ...process.env, GREG_MCP_FIXTURE_TOKEN: credentials[role] })).written, true);
  const body = await readFile(path, 'utf8');
  for (const value of [...Object.values(credentials), endpoint, 'fixture-private-identity']) assert.ok(!body.includes(value));
  return path;
}
async function compare(before, after, expected, strict = true) {
  const args = ['mcp', 'diff', '--before', before, '--after', after, '--check', '--json'];
  if (strict) args.push('--strict-catalog');
  return command(args, expected);
}
async function workflowGate(endpoint, role, legacy, baseline, expected, strict = true) {
  const runner = await mkdtemp(join(work, 'runner-'));
  const env = {
    ...process.env, GITHUB_WORKSPACE: work, RUNNER_TEMP: runner,
    GREG_MCP_ENDPOINT: endpoint, GREG_MCP_BASELINE: baseline,
    GREG_MCP_LEGACY: String(legacy), GREG_MCP_STRICT: String(strict), GREG_MCP_ENDPOINT_TOKEN: credentials[role],
  };
  try {
    await runCLI('bash', [gateScript, cli], { env, timeout: 30000 });
    assert.equal(expected, 0, 'workflow gate should reject this candidate');
  } catch (error) {
    if (expected === 0 || error.code !== expected) throw error;
  }
  const receipt = join(runner, 'mcp-catalog-receipt');
  for (const name of ['baseline.json', 'candidate.json', 'capture.json', 'diff.json']) {
    const data = await readFile(join(receipt, name), 'utf8');
    for (const token of Object.values(credentials)) assert.ok(!data.includes(token), 'receipt must omit credentials');
  }
  return JSON.parse(await readFile(join(receipt, 'diff.json'), 'utf8'));
}
try {
  const normal = await serve({ greet: [], add: ['math:read', 'math:write'] });
  const expanded = await serve({ greet: [], add: ['math:read'] });
  const removed = await serve({ greet: [] });
  for (const legacy of [false, true]) {
    const protocol = legacy ? '2025-11-25' : '2026-07-28';
    const baselines = {};
    for (const role of ['reader', 'writer']) {
      const expected = JSON.parse(await readFile(join(baselineDir, `${role}.lock.json`), 'utf8'));
      expected.protocol_version = protocol;
      const baseline = join(work, 'caller-baseline', `${role}-${protocol}-baseline.json`);
      await writeFile(baseline, JSON.stringify(expected));
      const candidate = await capture(normal, role, legacy, `${role}-${protocol}-candidate`);
      assert.deepEqual(JSON.parse(await readFile(candidate, 'utf8')), expected, 'official SDK catalog differs from the reviewed role baseline');
      const repeat = await capture(normal, role, legacy, `${role}-${protocol}-repeat`);
      assert.equal(await readFile(candidate, 'utf8'), await readFile(repeat, 'utf8'), 'role captures must be deterministic');
      assert.equal((await compare(baseline, candidate, 0)).compatible, true);
      assert.equal((await workflowGate(normal, role, legacy, `${role}-${protocol}-baseline.json`, 0)).compatible, true);
      baselines[role] = baseline;
    }
    const reader = await capture(expanded, 'reader', legacy, `expanded-reader-${protocol}`);
    const ordinary = await compare(baselines.reader, reader, 0, false);
    assert.equal(ordinary.compatible, true, 'default comparison stays backwards compatible');
    const strict = await compare(baselines.reader, reader, 1);
    assert.deepEqual(await workflowGate(expanded, 'reader', legacy, `reader-${protocol}-baseline.json`, 1), strict, 'workflow must retain the failed comparison receipt');
    assert.equal((await workflowGate(expanded, 'reader', legacy, `reader-${protocol}-baseline.json`, 0, false)).compatible, true);
    assert.equal(strict.strict_catalog, true);
    assert.equal(strict.needs_review, true);
    assert.equal(strict.breaking, false);
    assert.deepEqual(strict.changes, [{ tool: 'add', path: '', kind: 'tool_added', severity: 'needs_review' }]);
    const writer = await capture(expanded, 'writer', legacy, `expanded-writer-${protocol}`);
    assert.equal((await compare(baselines.writer, writer, 0)).compatible, true);
    const reduced = await capture(removed, 'writer', legacy, `reduced-writer-${protocol}`);
    assert.equal((await compare(baselines.writer, reduced, 1)).breaking, true);
  }
  await symlink(baselineDir, join(work, 'caller-baseline', 'outside'));
  await assert.rejects(workflowGate(normal, 'reader', false, 'outside/reader.lock.json', 0), 'baseline symlinks must not escape the checkout');
  assert.ok(methods.includes('server/discover') && methods.includes('tools/list') && methods.includes('initialize'));
  assert.ok(methods.every(method => ['server/discover', 'initialize', 'notifications/initialized', 'tools/list', 'resources/list', 'resources/templates/list', 'prompts/list'].includes(method)), `unexpected methods: ${methods}`);
  assert.equal(calls.length, 0, 'catalog gates must never execute a tool callback');
  console.log('PASS: reader/writer baselines, modern/legacy capture, strict expansion and removal gates; zero tool calls');
} finally {
  await Promise.all(servers.map(close => close()));
  await rm(work, { recursive: true, force: true });
}
