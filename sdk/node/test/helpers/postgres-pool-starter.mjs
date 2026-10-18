import {cp, mkdir, mkdtemp, rm, symlink} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {fileURLToPath, pathToFileURL} from 'node:url';

export async function loadPostgresStarter(t) {
  const directory = await mkdtemp(join(tmpdir(), 'gregale-postgres-starter-'));
  t.after(() => rm(directory, {recursive: true, force: true}));
  await cp(new URL('../../../../examples/postgres-pool-rotation/', import.meta.url), directory, {recursive: true});
  await mkdir(join(directory, 'node_modules', '@gregale'), {recursive: true});
  await symlink(fileURLToPath(new URL('../../', import.meta.url)), join(directory, 'node_modules', '@gregale', 'sdk-node'), 'dir');
  return import(pathToFileURL(join(directory, 'pool.mjs')).href);
}
