import assert from "node:assert/strict";
import { after, before, test } from "node:test";
import { createDeliverySink } from "./server.js";

const server = createDeliverySink({ failFirst: 1, token: "test-token" });
let base;

before(async () => {
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  base = `http://127.0.0.1:${server.address().port}`;
});
after(() => new Promise((resolve) => server.close(resolve)));

test("fails the first delivery and records the successful retry", async () => {
  const first = await fetch(`${base}/deliver`, { method: "POST", body: '{"export":"a"}' });
  const second = await fetch(`${base}/deliver`, { method: "POST", body: '{"export":"a"}' });
  assert.equal(first.status, 503);
  assert.equal(second.status, 200);
  assert.equal((await first.json()).attempt, 1);
  assert.equal((await second.json()).attempt, 2);

  const denied = await fetch(`${base}/__gregale_test__/attempts`);
  assert.equal(denied.status, 401);
  const response = await fetch(`${base}/__gregale_test__/attempts`, {
    headers: { authorization: "Bearer test-token" },
  });
  assert.equal(response.status, 200);
  assert.deepEqual((await response.json()).attempts.map(({ status, body }) => ({ status, body })), [
    { status: 503, body: '{"export":"a"}' },
    { status: 200, body: '{"export":"a"}' },
  ]);
});
