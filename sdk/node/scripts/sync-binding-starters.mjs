// Vendor the complete canonical runtime import closure into self-contained CLI apps.
import {mkdir, readFile, readdir, rm, writeFile} from 'node:fs/promises';
const checking = process.argv.includes('--check');
const templates = new URL('../../../cmd/gregale/templates/', import.meta.url);
const generated = '// Generated from sdk/node; run npm run build && npm run starter:sync.\n';
async function sync(target, source) {
  if (checking) {
    if (await readFile(target, 'utf8').catch(() => '') !== source) throw new Error('Binding starter drift: run npm run build && npm run starter:sync');
  } else await writeFile(target, source);
}
async function runtimeClosure(entries) {
  const modules = new Map();
  for (const name of entries) {
    if (modules.has(name)) continue;
    const compiled = await readFile(new URL(`../dist/${name}.js`, import.meta.url), 'utf8');
    modules.set(name, generated + compiled.replace(/^\/\/# sourceMappingURL=.*\n?/gm, ''));
    for (const match of compiled.matchAll(/(?:from\s*|import\s*)['"]([^'"]+)['"]/g)) {
      const path = match[1];
      if (path.startsWith('node:')) continue;
      if (!/^\.\/[a-z-]+\.js$/.test(path)) throw new Error('Unsupported starter runtime dependency');
      entries.push(path.slice(2, -3));
    }
  }
  return modules;
}
for (const [template, entries] of [
  ['object-storage-node', ['object-storage-runtime', 'secret-reload-runtime']],
  ['rest-api-postgres', ['postgres-pool-runtime', 'secret-reload-runtime']],
  ['secret-reload-node', ['postgres-pool-runtime', 'secret-reload-runtime']],
]) {
  const destination = new URL(`${template}/runtime/`, templates);
  if (!checking) await mkdir(destination, {recursive: true});
  const modules = await runtimeClosure(entries);
  for (const [name, source] of modules) await sync(new URL(`${name}.js`, destination), source);
  for (const name of await readdir(destination)) {
    if (modules.has(name.replace(/\.js$/, ''))) continue;
    const file = new URL(name, destination);
    if (!(await readFile(file, 'utf8')).startsWith(generated)) throw new Error('Unrecognized file in generated starter runtime');
    if (checking) throw new Error('Obsolete generated starter runtime');
    await rm(file);
  }
}
const adapter = await readFile(new URL('rest-api-postgres/database.js', templates), 'utf8');
await sync(new URL('secret-reload-node/database.js', templates),
  '// Generated from rest-api-postgres/database.js; run npm --prefix sdk/node run starter:sync.\n' + adapter);
