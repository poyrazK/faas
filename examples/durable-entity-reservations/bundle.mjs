import { cp, mkdir, mkdtemp, readFile, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

// Package only example code and built SDK modules, never environment files.
const target = await mkdtemp(join(tmpdir(), 'gregale-reservations-'));
for (const file of ['server.mjs', 'reservations.mjs', 'gregale.yaml']) {
  await cp(new URL(file, import.meta.url), join(target, file));
}
const manifest = JSON.parse(await readFile(new URL('package.json', import.meta.url), 'utf8'));
manifest.scripts = { start: manifest.scripts.start };
await writeFile(join(target, 'package.json'), JSON.stringify(manifest, null, 2) + '\n');
await mkdir(join(target, 'gregale-sdk'));
for (const file of ['durable-entity-handler.js', 'durable-entity-contract.js']) {
  await cp(new URL(`../../sdk/node/dist/${file}`, import.meta.url), join(target, 'gregale-sdk', file), { recursive: true });
}
await writeFile(join(target, 'sdk.mjs'), "export * from './gregale-sdk/durable-entity-handler.js';\nexport * from './gregale-sdk/durable-entity-contract.js';\n");
process.stdout.write(target + '\n');
