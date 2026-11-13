import test from 'node:test';
import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { mkdtemp, mkdir, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const exec = promisify(execFile);
const sdk = fileURLToPath(new URL('..', import.meta.url));

test('packed SDK exposes isolated public/preview clients and the legacy migration API', { timeout: 60_000 }, async t => {
  const root = await mkdtemp(join(tmpdir(), 'gregale-client-isolation-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  // npm's JSON output lists thousands of generated files and can exceed the
  // child-process buffer. Silent output contains just the tarball filename.
  const { stdout } = await exec('npm', ['pack', '--silent', '--ignore-scripts', '--pack-destination', root], { cwd: sdk });
  const filename = stdout.trim();
  assert.match(filename, /^[\w.-]+\.tgz$/);
  const app = join(root, 'existing-app');
  await mkdir(app);
  await writeFile(join(app, 'package.json'), JSON.stringify({ private: true, type: 'module' }));
  await exec('npm', ['install', '--offline', '--ignore-scripts', '--no-audit', '--no-fund', '--omit=dev', join(root, filename)], { cwd: app });
  await writeFile(join(app, 'app.mjs'), `
    import assert from 'node:assert/strict';
    import { createClient, FaaSClient, AppsService, OpenAPI } from '@gregale/sdk-node';
    import { createClient as createPreviewClient } from '@gregale/sdk-node/preview';
    const original = globalThis.fetch;
    const seen = [];
    const transport = async (url, init) => {
      seen.push([String(url), new Headers(init.headers).get('Authorization')]);
      return Response.json({ id: String(url) });
    };
    const a = createClient('https://a.test', { token: 'a', fetch: transport });
    const b = createClient('https://b.test', { token: 'b', fetch: transport });
    await Promise.all([a.apps.getApp({ slug: 'a' }), b.apps.getApp({ slug: 'b' })]);
    assert.deepEqual(seen, [['https://a.test/v1/apps/a', 'Bearer a'], ['https://b.test/v1/apps/b', 'Bearer b']]);
    assert.equal(globalThis.fetch, original);
    assert.equal('flags' in a, false);
    assert.equal(typeof createPreviewClient('https://preview.test').flags, 'object');
    const config = { ...OpenAPI };
    const legacy = new FaaSClient('https://legacy.test', { token: 'legacy', fetch: transport });
    try {
      await Promise.all([AppsService.getApp({ slug: 'old' }), a.apps.getApp({ slug: 'after' })]);
      assert.ok(seen.some(([url, token]) => url === 'https://legacy.test/v1/apps/old' && token === 'Bearer legacy'));
      assert.ok(seen.some(([url, token]) => url === 'https://a.test/v1/apps/after' && token === 'Bearer a'));
    } finally { legacy.uninstall(); globalThis.fetch = original; Object.assign(OpenAPI, config); }
  `);
  await exec(process.execPath, ['app.mjs'], { cwd: app });
  await writeFile(join(app, 'app.ts'), `
    import { createClient, type GregaleClient, type AppResponse, type CancelablePromise, FaaSClient, AppsService } from '@gregale/sdk-node';
    import { createClient as createPreviewClient, type GregaleClient as PreviewClient } from '@gregale/sdk-node/preview';
    const client: GregaleClient = createClient('https://api.test');
    const request: CancelablePromise<AppResponse> = client.apps.getApp({ slug: 'typed' });
    request.cancel();
    const preview: PreviewClient = createPreviewClient('https://api.test');
    void preview.flags;
    // @ts-expect-error preview APIs are not exposed by the public factory
    void client.flags;
    // @ts-expect-error generated method arguments remain checked
    client.apps.getApp({});
    const legacy = new FaaSClient('https://api.test');
    void AppsService.getApp({ slug: 'legacy' });
    legacy.uninstall();
  `);
  await writeFile(join(app, 'tsconfig.json'), JSON.stringify({
    compilerOptions: {
      target: 'ES2022', module: 'NodeNext', moduleResolution: 'NodeNext', strict: true,
      noEmit: true, types: ['node'], typeRoots: [resolve(sdk, 'node_modules/@types')],
    },
    files: ['app.ts'],
  }));
  await exec(process.execPath, [resolve(sdk, 'node_modules/typescript/bin/tsc'), '-p', 'tsconfig.json'], { cwd: app });
});
