// Compiled and executed from a clean project with the packed npm dependency.
import assert from 'node:assert/strict';
import { AccountService, AppsService, ErrNotFound, FaaSClient, type AppResponse } from '@gregale/sdk-node';
import { defineWorkflow } from '@gregale/sdk-node/workflows';
import { createGregaleBrowserFetch } from '@gregale/sdk-node/browser';

const client = new FaaSClient(process.argv[2]!, { token: 'sdk-install-fixture' });
try {
  const account = await AccountService.getAccount();
  assert.equal(account.plan, 'hobby');
  const app: AppResponse = await AppsService.getApp({ slug: 'hello-world' });
  assert.equal(app.slug, 'hello-world');
  assert.equal(typeof defineWorkflow, 'function');
  assert.equal(typeof createGregaleBrowserFetch, 'function');
  await assert.rejects(
    () => AppsService.getApp({ slug: 'missing-app-404' }),
    (error: unknown) => {
      assert.ok(error instanceof ErrNotFound);
      assert.equal(error.problem.code, 'not_found');
      assert.equal(error.status, 404);
      assert.ok(error.txId);
      return true;
    },
  );
  console.log('Node installed SDK: account, typed app, subpaths and typed 404 verified');
} finally {
  client.uninstall();
}
