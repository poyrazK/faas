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
import { rewriteImportsToJs, writeModelsBarrel, patchBinaryResponses, patchBinaryServices, patchAutomationPathEncoding, shareGeneratedCore, writeServicesBarrel } from './post-process.mjs';

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

// The pinned generator declares Blob downloads but its fetch runtime decodes
// them as text. Preserve binary success bodies while still decoding Problems.
await patchBinaryResponses(outDir);
await patchAutomationPathEncoding(outDir);

// Post-process pass 2: write the models barrel so customers can
// `import type { AppResponse } from '@gregale/sdk-node'`.
const modelsDir = resolve(outDir, 'models');
const barrelEntries = await writeModelsBarrel(modelsDir);

// ADR-20261023115741: the package's default entry exposes only generally
// available and beta capabilities. Generate that client from sdk/openapi.yaml
// (`make sdk-spec`) into src/generated-public, sharing the full tree's core;
// the full client stays available from the preview entry.
const publicSpecPath = resolve(repoRoot, 'sdk', 'openapi.yaml');
const publicOutDir = resolve(here, '..', 'src', 'generated-public');
await generate({
  input: publicSpecPath,
  output: publicOutDir,
  httpClient: 'fetch',
  useOptions: true,
  useUnionTypes: true,
  exportCore: false,
  exportServices: true,
  exportModels: true,
  exportSchemas: false,
  indent: '2',
});
await rewriteImportsToJs(publicOutDir);
await shareGeneratedCore(publicOutDir, '../../generated/core');
await patchBinaryServices(publicOutDir);
const publicBarrelEntries = await writeModelsBarrel(resolve(publicOutDir, 'models'));
await writeServicesBarrel(resolve(publicOutDir, 'services'));

console.log(`sdk/node: generated clients from ${specPath} -> ${outDir}`);
console.log(`sdk/node: generated the public client from ${publicSpecPath} -> ${publicOutDir} (${publicBarrelEntries} models)`);
console.log(`sdk/node: post-processed ${importsChanged} files with extensionless imports`);
console.log(`sdk/node: wrote models barrel (${barrelEntries} entries)`);
