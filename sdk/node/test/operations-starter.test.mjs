// Exercise the shipped SDK and starter outside the checkout. Source imports
// and mocked SDK classes cannot establish that a generated feature starts.
import test from 'node:test';
import assert from 'node:assert/strict';
import {cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {fileURLToPath, pathToFileURL} from 'node:url';
import {execFileSync} from 'node:child_process';

function prepareOfflineStarterInstall(directory) {
  const packagePath = join(directory, 'package.json');
  const manifest = JSON.parse(readFileSync(packagePath, 'utf8'));
  // npm still resolves dev dependency metadata with --omit=dev. These runtime
  // tests exercise the packed SDK and generated JavaScript, not TypeScript.
  delete manifest.devDependencies;
  writeFileSync(packagePath, `${JSON.stringify(manifest, null, 2)}\n`);
  rmSync(join(directory, 'package-lock.json'), {force: true});
}

for (const [template, minimumTests] of [['customer-operation-export', 6], ['customer-operation-job-export', 10], ['customer-operation-workflow-export', 8]]) {
test(`${template}: packed SDK installs outside the checkout and serves the browser module graph`, async t => {
  const temp = mkdtempSync(join(tmpdir(), 'gregale-operation-starter-'));
  t.after(() => rmSync(temp, {recursive: true, force: true}));
  const sdk = fileURLToPath(new URL('../', import.meta.url));
  const source = fileURLToPath(new URL(`../../../cmd/gregale/templates/${template}/`, import.meta.url));
  const dest = join(temp, 'feature');
  cpSync(source, dest, {recursive: true}); mkdirSync(join(dest, 'packages'));
  prepareOfflineStarterInstall(dest);
  const output = execFileSync('npm', ['pack', '--ignore-scripts', '--json', '--pack-destination', temp], {cwd: sdk, encoding: 'utf8', timeout: 30000});
  const [packed] = JSON.parse(output);
  cpSync(join(temp, packed.filename), join(dest, 'packages/gregale-sdk.tgz'));
  const childEnv = {...process.env};
  delete childEnv.NODE_TEST_CONTEXT;
  execFileSync('npm', ['install', '--offline', '--ignore-scripts', '--no-audit', '--no-fund'], {cwd: dest, env: childEnv, timeout: 60000});
  const checked = execFileSync('npm', ['test'], {cwd: dest, env: childEnv, encoding: 'utf8', timeout: 30000});
  assert.ok(Number(checked.match(/tests (\d+)/)?.[1]) >= minimumTests, 'starter behavior tests did not run');
  assert.match(checked, /fail 0/);
  assert.ok(readFileSync(join(dest, 'package-lock.json')).length > 0);

  const {createExportServer} = await import(pathToFileURL(join(dest, 'server.mjs')));
  const id = '11111111-1111-4111-8111-111111111111';
  const server = createExportServer({config: {apiURL: 'https://api.example.test', appID: id, scope: 'default', definitionID: id}});
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => { server.closeAllConnections(); server.close(); });
  const base = `http://127.0.0.1:${server.address().port}`;
  const visited = new Set();
  async function graph(path) {
    if (visited.has(path)) return;
    visited.add(path);
    const response = await fetch(base+path); assert.equal(response.status, 200, path);
    const text = await response.text(); assert.doesNotMatch(text, /(?:from|import)\s*['"](?:node:|@gregale\/)/);
    for (const match of text.matchAll(/(?:from|import)\s*['"]([^'"]+)['"]/g)) {
      const dependency = new URL(match[1], base+path); assert.equal(dependency.origin, base);
      await graph(dependency.pathname);
    }
  }
  await graph('/app.mjs');
  assert.ok(visited.has('/sdk/operation-session.js'));
  assert.ok(visited.has('/sdk/operation-submission.js'));
  assert.ok(visited.has('/sdk/customer-operations.js'));
  assert.ok(!visited.has('/sdk/operations-runtime.js'));
  assert.ok(!visited.has('/sdk/job-operations-runtime.js'));
  assert.ok(!visited.has('/sdk/workflow-operations-runtime.js'));
  if (template === 'customer-operation-workflow-export') assert.ok(visited.has('/progress.mjs'));
});
}

test('workflow export: packed SDK installs outside the checkout and needs no bucket writer', async t => {
  const temp = mkdtempSync(join(tmpdir(), 'gregale-workflow-export-'));
  t.after(() => rmSync(temp, {recursive: true, force: true}));
  const sdk = fileURLToPath(new URL('../', import.meta.url));
  const source = fileURLToPath(new URL('../../../examples/customer-operation-workflow-export/', import.meta.url));
  const dest = join(temp, 'feature'); cpSync(source, dest, {recursive: true}); mkdirSync(join(dest, 'packages'));
  prepareOfflineStarterInstall(dest);
  const [packed] = JSON.parse(execFileSync('npm', ['pack', '--ignore-scripts', '--json', '--pack-destination', temp], {cwd: sdk, encoding: 'utf8', timeout: 30000}));
  cpSync(join(temp, packed.filename), join(dest, 'packages/gregale-sdk.tgz'));
  const childEnv = {...process.env}; delete childEnv.NODE_TEST_CONTEXT;
  for (const name of ['GREGALE_STORAGE_TOKEN', 'GREGALE_EXPORT_BUCKET_URI', 'GREGALE_APP_SLUG']) delete childEnv[name];
  execFileSync('npm', ['install', '--offline', '--ignore-scripts', '--no-audit', '--no-fund'], {cwd: dest, env: childEnv, timeout: 60000});
  const checked = execFileSync('npm', ['test'], {cwd: dest, env: childEnv, encoding: 'utf8', timeout: 30000});
  assert.ok(Number(checked.match(/tests (\d+)/)?.[1]) >= 8); assert.match(checked, /fail 0/);
  assert.doesNotMatch(readFileSync(join(dest,'server.mjs'),'utf8'), /managedBucketWriter|GREGALE_STORAGE_TOKEN|GREGALE_EXPORT_BUCKET_URI/);
});
