import test from 'node:test';
import assert from 'node:assert/strict';
import { cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { execFileSync } from 'node:child_process';
import { buildIssueSourceMapManifest, issueSourceMapDiscoveryLimits, loadIssueSourceMapManifest } from '../src/issue-source-map-manifest.js';
import { registerIssueSourceMaps } from '../src/issue-source-maps.js';
import { createIssueReporter } from '../src/issues.js';

const map = { version: 3, sourceRoot: '../', sources: ['src/checkout.ts'], names: ['charge'],
  mappings: 'AASAA', sourcesContent: ['PRIVATE_SOURCE_CONTENT'], producerSecret: 'PRIVATE_EXTENSION' };

function tree(t: { after(fn: () => void): void }) {
  const directory = mkdtempSync(join(tmpdir(), 'gregale-issue-discovery-'));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  return directory;
}
function put(file: string, contents: string | Buffer) {
  mkdirSync(dirname(file), { recursive: true });
  writeFileSync(file, contents);
}
function fixture(root: string, name = 'chunk-a.mjs') {
  put(join(root, 'dist', name), `export function a(){throw new Error("payment failed")}\n//# sourceMappingURL=../maps/${name}.map\n`);
  put(join(root, 'maps', name + '.map'), JSON.stringify(map));
}

test('build discovery creates portable sanitized maps and verifies relocated deployment bundles', t => {
  const root = tree(t), build = join(root, 'build'), deployed = join(root, 'deployment');
  fixture(build);
  const payload = buildIssueSourceMapManifest(build);
  assert.ok(!payload.includes('PRIVATE_'));
  assert.ok(!payload.includes(build));
  const manifest = JSON.parse(payload);
  assert.deepEqual(Object.keys(manifest.artifacts[0]).sort(), ['generatedFile', 'payload', 'sha256']);
  assert.equal(manifest.artifacts[0].generatedFile, 'dist/chunk-a.mjs');
  // Neither original sources nor the original .map file need to ship at runtime.
  cpSync(join(build, 'dist'), join(deployed, 'dist'), { recursive: true });
  const file = join(deployed, 'issues.json'); put(file, payload);
  const maps = registerIssueSourceMaps(loadIssueSourceMapManifest({ file, directory: deployed }));
  assert.deepEqual(maps.resolve({ file: join(deployed, 'dist/chunk-a.mjs'), function: 'a', line: 1, in_app: true }, 1), {
    file: join(deployed, 'src/checkout.ts'), function: 'charge', line: 10, in_app: true,
  });
  assert.equal(maps.resolve({ file: join(build, 'dist/chunk-a.mjs'), function: 'a', line: 1, in_app: true }, 1), undefined);
});

test('real bundled failures keep original identities across renamed builds and map wrapped causes', async t => {
  const root = tree(t), build = join(root, 'build'), deployed = join(root, 'deployment');
  const events: Array<Record<string, unknown>> = [];
  for (const name of ['chunk-a.mjs', 'chunk-b.mjs']) {
    fixture(build, name);
    cpSync(join(build, 'dist'), join(deployed, 'dist'), { recursive: true });
    const file = join(deployed, 'issues.json'); put(file, buildIssueSourceMapManifest(build));
    const reporter = createIssueReporter({ baseURL: 'https://api.gregale.dev', app: 'payments', token: 'g_issue_test',
      groupingStrategy: 'cause-chain-v1', sourceMapManifest: { file, directory: deployed },
      fetch: (async (_url, options) => {
        events.push(JSON.parse(String(options?.body)));
        return new Response('{}', { status: 202 });
      }) as typeof fetch,
    });
    // Initialization has consumed the manifest. Capture must not read it again.
    rmSync(file);
    const module = await import(pathToFileURL(join(deployed, 'dist', name)).href) as { a(): never };
    try { module.a(); } catch (cause) {
      reporter.captureException(new Error('request failed', { cause }));
    }
    assert.equal(await reporter.close(), true);
    assert.ok(reporter.stats().mappedFrames >= 1);
  }
  const causes = events.map(event => (event.causes as Array<{ frames: unknown[] }>)[0]!);
  assert.deepEqual(causes[0]!.frames[0], causes[1]!.frames[0]);
  assert.deepEqual(causes[0]!.frames[0], { file: join(deployed, 'src/checkout.ts'), function: 'charge', line: 10, in_app: true });
  assert.ok(!JSON.stringify(events).includes('PRIVATE_'));
});

test('discovery supports hidden adjacent maps and virtual roots; excludes dependencies and links', t => {
  const root = tree(t);
  put(join(root, 'a.cjs'), 'throw new Error("hidden map")');
  put(join(root, 'a.cjs.map'), JSON.stringify({ ...map, sourceRoot: 'webpack://payments/', sources: ['src/checkout.ts'] }));
  put(join(root, 'node_modules', 'dependency.js'), '//# sourceMappingURL=https://example.com/private.map');
  put(join(root, '.git', 'ignored.js'), '//# sourceMappingURL=https://example.com/private.map');
  symlinkSync(join(root, 'node_modules'), join(root, 'linked-directory'));
  symlinkSync(join(root, 'a.cjs'), join(root, 'linked.js'));
  const file = join(root, 'issues.json'); put(file, buildIssueSourceMapManifest(root));
  const artifacts = loadIssueSourceMapManifest({ file, directory: root });
  assert.equal(artifacts.length, 1);
  assert.equal(registerIssueSourceMaps(artifacts).resolve({ file: join(root, 'a.cjs'), function: 'a', line: 1, in_app: true }, 1)?.file,
    'webpack://payments/src/checkout.ts');
});

test('discovery takes the last local source-map directive and resolves map-relative source roots', t => {
  const root = tree(t);
  put(join(root, 'dist', 'a.js'), '//# sourceMappingURL=https://example.com/old.map\n/*# sourceMappingURL=../maps/current.map */');
  put(join(root, 'maps', 'current.map'), JSON.stringify({ ...map, sourceRoot: './', sources: ['original.ts'] }));
  const file = join(root, 'issues.json'); put(file, buildIssueSourceMapManifest(root));
  const maps = registerIssueSourceMaps(loadIssueSourceMapManifest({ file, directory: root }));
  assert.equal(maps.resolve({ file: join(root, 'dist/a.js'), function: 'a', line: 1, in_app: true }, 1)?.file,
    join(root, 'maps/original.ts'));
});

test('wrong deployments, missing bundles, tampered paths, and ambiguous manifests fail initialization', t => {
  const root = tree(t); fixture(root);
  const file = join(root, 'issues.json'), payload = buildIssueSourceMapManifest(root);
  put(file, payload);
  put(join(root, 'dist/chunk-a.mjs'), 'changed deployment');
  assert.throws(() => loadIssueSourceMapManifest({ file, directory: root }), /bundle digest mismatch/);
  fixture(root);
  const manifest = JSON.parse(payload), artifact = manifest.artifacts[0];
  for (const malformed of [
    { ...manifest, version: 2 }, { ...manifest, extra: true },
    { ...manifest, artifacts: [artifact, artifact] },
    ...['../outside.js', '/app/a.js', 'dist/../a.js', 'dist\\a.js', 'https://example.com/a.js'].map(generatedFile =>
      ({ ...manifest, artifacts: [{ ...artifact, generatedFile }] })),
    { ...manifest, artifacts: [{ ...artifact, sha256: 'not a digest' }] },
  ]) {
    put(file, JSON.stringify(malformed));
    assert.throws(() => loadIssueSourceMapManifest({ file, directory: root }), /Invalid issue source/);
  }
  put(file, payload);
  assert.throws(() => createIssueReporter({ baseURL: 'https://api.gregale.dev', app: 'payments', token: 'g_issue_test',
    sourceMapManifest: { file, directory: root } }), /require failure-site-v1/);
  assert.throws(() => createIssueReporter({ baseURL: 'https://api.gregale.dev', app: 'payments', token: 'g_issue_test',
    groupingStrategy: 'failure-site-v1', sourceMaps: [], sourceMapManifest: { file, directory: root } }), /not both/);
  rmSync(join(root, 'dist/chunk-a.mjs'));
  assert.throws(() => loadIssueSourceMapManifest({ file, directory: root }), /ENOENT/);
});

test('map discovery rejects remote, inline, escaping, linked, and malformed map references', t => {
  const root = tree(t), build = join(root, 'build'); mkdirSync(build);
  put(join(root, 'outside.map'), JSON.stringify(map));
  for (const reference of ['https://example.com/a.map', '//example.com/a.map', 'data:application/json;base64,e30=',
    '../outside.map', '%2e%2e/outside.map', '/outside.map', 'a.map?version=2', 'a.map#section']) {
    put(join(build, 'a.js'), '//# sourceMappingURL=' + reference);
    assert.throws(() => buildIssueSourceMapManifest(build), /Invalid issue source-map manifest/);
  }
  put(join(build, 'a.js'), '//# sourceMappingURL=a.map');
  symlinkSync(join(root, 'outside.map'), join(build, 'a.map'));
  assert.throws(() => buildIssueSourceMapManifest(build));
  rmSync(join(build, 'a.map'));
  for (const payload of ['not JSON', JSON.stringify({ ...map, sections: [] }), JSON.stringify({ ...map, mappings: 'g' }),
    JSON.stringify({ ...map, sourceRoot: '/workspace/build' })]) {
    put(join(build, 'a.map'), payload);
    assert.throws(() => buildIssueSourceMapManifest(build), /Invalid issue source/);
  }
});

test('discovery bounds bundles, directory entries, map count, and aggregate source-map decoding', t => {
  const root = tree(t);
  put(join(root, 'large.js'), Buffer.alloc(issueSourceMapDiscoveryLimits.bundleBytes + 1));
  assert.throws(() => buildIssueSourceMapManifest(root), /byte bound/);
  rmSync(join(root, 'large.js'));
  for (let i = 0; i < 33; i++) {
    put(join(root, i + '.js'), 'const x = 1;');
    put(join(root, i + '.js.map'), JSON.stringify(map));
  }
  assert.throws(() => buildIssueSourceMapManifest(root), /too many maps/);
  rmSync(join(root, '32.js')); rmSync(join(root, '32.js.map'));
  for (let i = 0; i < 32; i++) {
    put(join(root, i + '.js.map'), JSON.stringify({ ...map, mappings: ';'.repeat(4000) }));
  }
  assert.throws(() => buildIssueSourceMapManifest(root), /Invalid issue source map/);
  const empty = join(root, 'entry-bound'); mkdirSync(empty);
  for (let i = 0; i <= issueSourceMapDiscoveryLimits.files; i++) put(join(empty, i + '.txt'), '');
  assert.throws(() => buildIssueSourceMapManifest(empty), /entry bound/);
});

test('build CLI produces a usable manifest and reports invalid builds without overwriting existing output', t => {
  const root = tree(t); fixture(root);
  const file = join(root, 'issues.json');
  const command = fileURLToPath(new URL('../src/issue-source-map-build.js', import.meta.url));
  const output = execFileSync(process.execPath, [command, root, file], { encoding: 'utf8' });
  assert.match(output, /1 maps/);
  assert.equal(loadIssueSourceMapManifest({ file, directory: root }).length, 1);
  const before = readFileSync(file);
  assert.throws(() => execFileSync(process.execPath, [command, root, file], { stdio: 'pipe' }));
  assert.deepEqual(readFileSync(file), before);
  fixture(root, 'second.mjs');
  execFileSync(process.execPath, [command, root, file, '--replace'], { stdio: 'pipe' });
  assert.equal(loadIssueSourceMapManifest({ file, directory: root }).length, 2);
  assert.throws(() => execFileSync(process.execPath, [command], { stdio: 'pipe' }));
  put(join(root, 'dist/chunk-a.mjs'), '//# sourceMappingURL=https://example.com/a.map');
  const invalidOutput = join(root, 'invalid.json');
  assert.throws(() => execFileSync(process.execPath, [command, root, invalidOutput], { stdio: 'pipe' }));
  assert.throws(() => readFileSync(invalidOutput), /ENOENT/);
});
