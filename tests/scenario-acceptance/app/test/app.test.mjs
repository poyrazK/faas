import assert from "node:assert/strict";
import { test } from "node:test";
import { deliverWithRetry } from "../delivery.js";
import { createApp } from "../server.js";
import { createDeliverySink } from "../../../../cmd/gregale/scenario_fixtures/delivery-sink/server.js";

test("application retries a failed notification once", async () => {
  const statuses = [503, 200];
  const calls = [];
  const send = async (url, request) => {
    calls.push({ url, request });
    return { status: statuses.shift(), ok: calls.length === 2 };
  };
  assert.deepEqual(await deliverWithRetry(send, "https://sink.example/deliver", '{"run_id":"test"}'), [503, 200]);
  assert.equal(calls.length, 2);
  assert.equal(calls[0].request.body, calls[1].request.body);
});

test("application sends a notification through the delivery sink after a retry", async (t) => {
  const sink = createDeliverySink({ failFirst: 1, token: "local-token" });
  await new Promise((resolve) => sink.listen(0, "127.0.0.1", resolve));
  t.after(() => sink.close());
  const sinkURL = `http://127.0.0.1:${sink.address().port}`;

  const app = createApp({ sinkURL: `${sinkURL}/deliver`, runID: "local-run" });
  await new Promise((resolve) => app.listen(0, "127.0.0.1", resolve));
  t.after(() => app.close());

  const response = await fetch(`http://127.0.0.1:${app.address().port}/start`, { method: "POST" });
  assert.equal(response.status, 202);
  assert.deepEqual((await response.json()).statuses, [503, 200]);

  const attemptsResponse = await fetch(`${sinkURL}/__gregale_test__/attempts`, {
    headers: { Authorization: "Bearer local-token" },
  });
  assert.equal(attemptsResponse.status, 200);
  const { attempts } = await attemptsResponse.json();
  assert.deepEqual(attempts.map(({ status }) => status), [503, 200]);
  for (const attempt of attempts) {
    assert.deepEqual(JSON.parse(attempt.body), { run_id: "local-run" });
  }
});
