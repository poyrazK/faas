import assert from "node:assert/strict";
import { readFile, writeFile } from "node:fs/promises";

const health = await fetch(process.env.GREGALE_TEST_URL + "/", { signal: AbortSignal.timeout(10000) });
assert.equal(health.status, 200);
assert.deepEqual(await health.json(), { ready: true });
if (process.argv[2] === "trigger") {
  const response = await fetch(process.env.GREGALE_TEST_SERVICE_WORKER_URL + "/import", {
    method: "POST", headers: { "content-type": "application/json" },
    body: await readFile("input.json", "utf8"), signal: AbortSignal.timeout(10000),
  });
  assert.equal(response.status, 202);
  const { invocation_id } = await response.json();
  assert.match(invocation_id, /^[0-9a-f-]{36}$/);
  await writeFile(process.env.GREGALE_TEST_TRIGGER_OUTPUT, JSON.stringify({ worker_invocation_id: invocation_id }));
} else if (process.argv[2] === "observe") {
  // Application observations stay separate from the independent platform collectors.
  await writeFile(process.env.GREGALE_TEST_BEHAVIOR_OUTPUT, JSON.stringify({
    version: 1, observations: [{ key: "api.ready", value: { ready: true } }],
  }));
} else {
  assert.equal(process.argv[2], "setup");
}
