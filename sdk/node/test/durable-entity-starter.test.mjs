import {writePackedSDKRuntimeLock} from './fixtures/packed-sdk-runtime-lock.mjs';
// Install the real packed SDK outside the checkout; no source-import shortcut.
import test from 'node:test';
import assert from 'node:assert/strict';
import {cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {execFileSync} from 'node:child_process';

test('durable entity starter installs, typechecks, prepares its validator and exercises HTTP workflow', t => {
  const temp = mkdtempSync(join(tmpdir(), 'gregale-entity-starter-'));
  t.after(() => rmSync(temp, {recursive: true, force: true}));
  const sdk = fileURLToPath(new URL('../', import.meta.url));
  const dest = join(temp, 'app');
  cpSync(fileURLToPath(new URL('../../../cmd/gregale/templates/durable-entity-node/', import.meta.url)), dest, {recursive: true});
  mkdirSync(join(dest, 'packages'));
  const manifest = JSON.parse(readFileSync(join(dest, 'package.json'), 'utf8'));
  delete manifest.devDependencies;
  writeFileSync(join(dest, 'package.json'), JSON.stringify(manifest));
  // Request only the archive filename; the full generated SDK file inventory can exceed spawnSync's output buffer.
  const packedFilename = execFileSync('npm', ['pack', '--ignore-scripts', '--silent', '--pack-destination', temp], {cwd: sdk, encoding: 'utf8', timeout: 30000}).trim();
  cpSync(join(temp, packedFilename), join(dest, 'packages/gregale-sdk.tgz'));
  writePackedSDKRuntimeLock(dest, sdk);
  const env = {...process.env}; delete env.NODE_TEST_CONTEXT;
  const run = args => execFileSync('npm', args, {cwd: dest, env, encoding: 'utf8', timeout: 60000});
  run(['ci', '--offline', '--ignore-scripts', '--no-audit', '--no-fund']);
  run(['run', 'generate:client']);
  run(['run', 'generate:client:check']);
  const contractsPath = join(dest, 'contracts.mjs');
  const contracts = readFileSync(contractsPath);
  writeFileSync(contractsPath, 'throw new Error("generator executed customer code");');
  try {run(['run', 'generate:client:check']);} finally {writeFileSync(contractsPath, contracts);}
  writeFileSync(join(dest, 'client.mjs'), '// stale generated file');
  assert.throws(() => run(['run', 'generate:client:check']));
  run(['run', 'generate:client']);
  execFileSync(process.execPath, [join(sdk, 'node_modules/typescript/bin/tsc'), '--noEmit', '--typeRoots', join(sdk, 'node_modules/@types'), '-p', join(dest, 'tsconfig.json')], {cwd: dest, env, timeout: 30000});
  run(['run', 'prepare:validator']);
  const descriptor = JSON.parse(readFileSync(join(dest, 'gregale.validator.json'), 'utf8'));
  assert.equal(descriptor.runtime, 'node22');
  for (const {path, content} of descriptor.files) assert.deepEqual(Buffer.from(content, 'base64'), readFileSync(join(dest, path)));
  const output = run(['test']);
  assert.ok(Number(output.match(/tests (\d+)/)?.[1]) >= 8); assert.match(output, /fail 0/);
  assert.ok(JSON.parse(readFileSync(join(dest, 'package-lock.json'), 'utf8')).packages['node_modules/@gregale/sdk-node'].integrity);
});
