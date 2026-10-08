import {cp, mkdtemp, readFile, writeFile} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';

// A fresh directory avoids touching an existing source tree or including local credentials.
const target = await mkdtemp(join(tmpdir(), 'gregale-orders-'));
for (const file of ['server.mjs', 'orders.mjs', 'setup.mjs', 'schema.sql', 'schemas', 'gregale.yaml', 'package-lock.json']) {
  await cp(new URL(file, import.meta.url), join(target, file), {recursive: true});
}
const manifest = JSON.parse(await readFile(new URL('package.json', import.meta.url), 'utf8'));
delete manifest.scripts.bundle;
await writeFile(join(target, 'package.json'), JSON.stringify(manifest, null, 2) + '\n');
await cp(new URL('../../sdk/node/dist/', import.meta.url), join(target, 'gregale-sdk'), {recursive: true});
await writeFile(join(target, 'sdk.mjs'), "export {GregaleOperations, customerOperationRequestFromHeaders, customerOperationReceiptSchema} from './gregale-sdk/index.js';\n");
process.stdout.write(target + '\n');
