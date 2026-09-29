import assert from "node:assert/strict";

const response = await fetch(
  `${process.env.GREGALE_TEST_SERVICE_SINK_URL}/__gregale_test__/attempts`,
  { headers: { Authorization: `Bearer ${process.env.GREGALE_TEST_SINK_SINK_TOKEN}` } },
);
assert.equal(response.status, 200);
const { attempts } = await response.json();
assert.deepEqual(attempts.map(({ status }) => status), [503, 200]);
for (const attempt of attempts) {
  assert.equal(JSON.parse(attempt.body).run_id, process.env.GREGALE_TEST_RUN_ID);
}
