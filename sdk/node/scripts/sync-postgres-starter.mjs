// Keep the embedded starter independent of an unpublished npm SDK version.
import {mkdir, readFile, writeFile} from 'node:fs/promises';
const checking = process.argv.includes('--check');
const directory = new URL('../../../cmd/gregale/templates/rest-api-postgres/runtime/', import.meta.url);
const target = new URL('postgres-pool-runtime.js', directory);
const compiled = await readFile(new URL('../dist/postgres-pool-runtime.js', import.meta.url), 'utf8');
const source = '// Generated from sdk/node; run npm run build && npm run starter:sync.\n' +
  compiled.replace(/^\/\/# sourceMappingURL=.*\n?/gm, '');
if (checking) {
  if (await readFile(target, 'utf8').catch(() => '') !== source) {
    throw new Error('PostgreSQL starter runtime drift: run npm run build && npm run starter:sync');
  }
} else {
  await mkdir(directory, {recursive: true});
  await writeFile(target, source);
}
