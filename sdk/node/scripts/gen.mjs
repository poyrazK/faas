#!/usr/bin/env node
// scripts/gen.mjs — runs the pinned openapi-typescript-codegen against
// the canonical spec at api/openapi.yaml. Output goes into
// sdk/node/src/generated/ and is committed per ADR-013.
//
// The script is cwd-independent: paths are resolved from this file via
// import.meta.url. CI invokes it through `npm run gen`, local developers
// through `make sdk-gen-node`.

import { generate } from 'openapi-typescript-codegen';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';
import { rewriteImportsToJs, writeModelsBarrel } from './post-process.mjs';

const here = dirname(fileURLToPath(import.meta.url));
const repoRoot = resolve(here, '..', '..', '..');
const specPath = resolve(repoRoot, 'api', 'openapi.yaml');
const outDir = resolve(here, '..', 'src', 'generated');

await generate({
  input: specPath,
  output: outDir,
  httpClient: 'fetch',
  useOptions: true,
  useUnionTypes: true,
  exportCore: true,
  exportServices: true,
  exportModels: true,
  exportSchemas: false,
  indent: '2',
});

// Post-process pass 1: rewrite extensionless relative imports to
// `.js` (NodeNext requires explicit extensions on relative imports).
const importsChanged = await rewriteImportsToJs(outDir);

// Post-process pass 2: write the models barrel so customers can
// `import type { AppResponse } from '@gregale/sdk-node'`.
const modelsDir = resolve(outDir, 'models');
const barrelEntries = await writeModelsBarrel(modelsDir);

console.log(`sdk/node: generated clients from ${specPath} -> ${outDir}`);
console.log(`sdk/node: post-processed ${importsChanged} files with extensionless imports`);
console.log(`sdk/node: wrote models barrel (${barrelEntries} entries)`);
