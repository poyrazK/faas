// test/post-process.test.mjs — unit tests for the gen.mjs post-processors.
//
// These cover the glue logic that the openapi-typescript-codegen
// generator doesn't do natively. They're .mjs (not .ts) because
// post-process.mjs is plain ESM and the test fixtures are
// filesystem-bound — no need to involve the TypeScript loader.

import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdir, mkdtemp, readFile, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

import { rewriteImportsToJs, writeModelsBarrel } from '../scripts/post-process.mjs';

async function withTempDir(fn) {
  const dir = await mkdtemp(join(tmpdir(), 'post-process-test-'));
  try {
    return await fn(dir);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
}

test('rewriteImportsToJs appends .js to extensionless relative imports', async () => {
  await withTempDir(async (dir) => {
    const sub = join(dir, 'core');
    await mkdir(sub, { recursive: true });
    await writeFile(join(sub, 'request.ts'), [
      "import { foo } from './bar';",
      "import { baz } from '../models/qux';",
      "import type { Hello } from 'some-pkg';",
      '',
    ].join('\n'));

    const changed = await rewriteImportsToJs(dir);
    assert.equal(changed, 1);

    const out = await readFile(join(sub, 'request.ts'), 'utf8');
    assert.match(out, /from '\.\/bar\.js';/);
    assert.match(out, /from '\.\.\/models\/qux\.js';/);
    // Bare specifier imports are left untouched — the rewrite only
    // touches relative imports.
    assert.match(out, /from 'some-pkg';/);
  });
});

test('rewriteImportsToJs is idempotent on already-rewritten files', async () => {
  await withTempDir(async (dir) => {
    const sub = join(dir, 'core');
    await mkdir(sub, { recursive: true });
    const before = "import { foo } from './bar.js';\n";
    await writeFile(join(sub, 'request.ts'), before);

    const changed = await rewriteImportsToJs(dir);
    assert.equal(changed, 0);

    const after = await readFile(join(sub, 'request.ts'), 'utf8');
    assert.equal(after, before);
  });
});

test('rewriteImportsToJs does NOT add .js to .js imports (no .js.js)', async () => {
  // The defence against future generator releases that emit `.js`
  // natively. The regex matches `from '(\.{1,2}/[^']+)'` and the
  // rewrite callback skips paths that already end in `.js`.
  await withTempDir(async (dir) => {
    const sub = join(dir, 'core');
    await mkdir(sub, { recursive: true });
    await writeFile(join(sub, 'a.ts'), "import { foo } from './bar.js';\n");

    const changed = await rewriteImportsToJs(dir);
    assert.equal(changed, 0);

    const out = await readFile(join(sub, 'a.ts'), 'utf8');
    assert.equal(out, "import { foo } from './bar.js';\n");
  });
});

test('rewriteImportsToJs walks nested directories', async () => {
  await withTempDir(async (dir) => {
    await mkdir(join(dir, 'services'), { recursive: true });
    await mkdir(join(dir, 'models'), { recursive: true });
    await writeFile(join(dir, 'services', 'One.ts'), "import x from './Two';\n");
    await writeFile(join(dir, 'models', 'Two.ts'), "import x from '../services/One';\n");

    const changed = await rewriteImportsToJs(dir);
    assert.equal(changed, 2);

    const one = await readFile(join(dir, 'services', 'One.ts'), 'utf8');
    const two = await readFile(join(dir, 'models', 'Two.ts'), 'utf8');
    assert.match(one, /from '\.\/Two\.js';/);
    assert.match(two, /from '\.\.\/services\/One\.js';/);
  });
});

test('writeModelsBarrel writes a sorted barrel of all sibling .ts files', async () => {
  await withTempDir(async (dir) => {
    await writeFile(join(dir, 'AppResponse.ts'), 'export type AppResponse = unknown;\n');
    await writeFile(join(dir, 'AppRequest.ts'), 'export type AppRequest = unknown;\n');
    await writeFile(join(dir, 'Zebra.ts'), 'export type Zebra = unknown;\n');
    // Non-TS files are ignored.
    await writeFile(join(dir, 'README.md'), '# not a model\n');

    const count = await writeModelsBarrel(dir);
    assert.equal(count, 3);

    const barrel = await readFile(join(dir, 'index.ts'), 'utf8');
    assert.match(barrel, /export type \{ AppRequest \} from '\.\/AppRequest\.js';/);
    assert.match(barrel, /export type \{ AppResponse \} from '\.\/AppResponse\.js';/);
    assert.match(barrel, /export type \{ Zebra \} from '\.\/Zebra\.js';/);
    // Sorted alphabetically.
    const lines = barrel.split('\n').filter((l) => l.startsWith('export type'));
    assert.equal(lines[0], "export type { AppRequest } from './AppRequest.js';");
    assert.equal(lines[1], "export type { AppResponse } from './AppResponse.js';");
    assert.equal(lines[2], "export type { Zebra } from './Zebra.js';");
    // README.md is not picked up.
    assert.doesNotMatch(barrel, /README/);
  });
});

test('writeModelsBarrel is idempotent (overwrites existing index.ts)', async () => {
  await withTempDir(async (dir) => {
    await writeFile(join(dir, 'Foo.ts'), 'export type Foo = unknown;\n');
    await writeFile(join(dir, 'index.ts'), '/* stale */\n');

    const count = await writeModelsBarrel(dir);
    assert.equal(count, 1);

    const barrel = await readFile(join(dir, 'index.ts'), 'utf8');
    assert.doesNotMatch(barrel, /stale/);
    assert.match(barrel, /export type \{ Foo \} from '\.\/Foo\.js';/);
  });
});

test('writeModelsBarrel does not recurse into nested directories', async () => {
  // The generator emits one file per model at the top level of
  // models/; sub-directories are not generated. The barrel writer
  // must not recurse — a stray nested file would be misnamed in
  // the export.
  await withTempDir(async (dir) => {
    await mkdir(join(dir, 'nested'), { recursive: true });
    await writeFile(join(dir, 'Top.ts'), 'export type Top = unknown;\n');
    await writeFile(join(dir, 'nested', 'Skipped.ts'), 'export type Skipped = unknown;\n');

    const count = await writeModelsBarrel(dir);
    assert.equal(count, 1);

    const barrel = await readFile(join(dir, 'index.ts'), 'utf8');
    assert.match(barrel, /export type \{ Top \} from '\.\/Top\.js';/);
    assert.doesNotMatch(barrel, /Skipped/);
  });
});
