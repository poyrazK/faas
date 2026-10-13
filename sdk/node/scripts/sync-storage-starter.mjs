// Ship the canonical compiled runtime helpers in a self-contained CLI starter.
import {mkdir, readFile, writeFile} from 'node:fs/promises';
const checking = process.argv.includes('--check');
const destination = new URL('../../../cmd/gregale/templates/object-storage-node/runtime/', import.meta.url);
if (!checking) await mkdir(destination, {recursive: true});
for (const name of ['object-storage-runtime', 'secret-reload-runtime']) {
  const compiled = await readFile(new URL(`../dist/${name}.js`, import.meta.url), 'utf8');
  const source = '// Generated from sdk/node; run npm run build && npm run starter:sync.\n' +
    compiled.replace(/^\/\/# sourceMappingURL=.*\n?/gm, '');
  const target = new URL(`${name}.js`, destination);
  if (checking) {
    if (await readFile(target, 'utf8').catch(() => '') !== source) {
      throw new Error('Storage starter runtime drift: run npm run build && npm run starter:sync');
    }
  } else await writeFile(target, source);
}
