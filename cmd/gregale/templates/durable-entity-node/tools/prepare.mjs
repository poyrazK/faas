// Explicit source packaging only: never executes the validator or reads environment files.
import {readFile, writeFile} from 'node:fs/promises';
const files = [];
for (const path of ['validator.mjs', 'state.mjs']) {
  files.push({path, content: (await readFile(new URL(`../${path}`, import.meta.url))).toString('base64')});
}
await writeFile(new URL('../gregale.validator.json', import.meta.url), JSON.stringify({runtime: 'node22', entrypoint: 'validator.mjs', files}, null, 2) + '\n');
process.stdout.write('Prepared gregale.validator.json; run gregale doctor before deployment.\n');
