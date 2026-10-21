import assert from 'node:assert/strict';
import {execFileSync} from 'node:child_process';
import {mkdir, mkdtemp, rm, writeFile} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {test} from 'node:test';

test('packed PostgreSQL runtime imports and typechecks without pg or control-plane SDK dependencies', async t => {
  const directory = await mkdtemp(join(tmpdir(), 'gregale-postgres-package-'));
  t.after(() => rm(directory, {recursive: true, force: true}));
  const sdk = fileURLToPath(new URL('..', import.meta.url));
  const archive = execFileSync('npm', ['pack', '--ignore-scripts', '--silent', '--pack-destination', directory],
    {cwd: sdk, encoding: 'utf8', env: {...process.env, npm_config_cache: join(directory, 'cache')}}).trim();
  const installed = join(directory, 'node_modules', '@gregale', 'sdk-node');
  await mkdir(installed, {recursive: true});
  execFileSync('tar', ['-xzf', join(directory, archive), '--strip-components=1', '-C', installed]);
  const consumer = join(directory, 'consumer.mts');
  await writeFile(consumer, `
    import assert from 'node:assert/strict';
    import {EventEmitter} from 'node:events';
    import {createPostgresPool, type PostgresDriverPool} from '@gregale/sdk-node/postgres/runtime';
    let ended = 0;
    const database = createPostgresPool((): PostgresDriverPool => Object.assign(new EventEmitter(), {
      async connect() { return Object.assign(new EventEmitter(), {
        async query() { return {rows: [{id: 7}]}; }, release() {},
      }); },
      async end() { ended += 1; },
    }));
    const result = await database.query<{id: number}>('SELECT 7 AS id');
    const id: number = result.rows[0]!.id;
    assert.equal(id, 7);
    await database.beforeCheckpoint();
    assert.equal(database.diagnostics.state, 'checkpointed');
    await database.afterRestore();
    assert.equal(database.generation, 1);
    await database.close();
    assert.equal(ended, 2);
  `);
  execFileSync(process.execPath, [join(sdk, 'node_modules/typescript/bin/tsc'), consumer,
    '--target', 'ES2022', '--module', 'NodeNext', '--strict', '--skipLibCheck',
    '--typeRoots', join(sdk, 'node_modules/@types'), '--outDir', join(directory, 'build')], {encoding: 'utf8'});
  execFileSync(process.execPath, [join(directory, 'build/consumer.mjs')], {cwd: directory, encoding: 'utf8'});
});
