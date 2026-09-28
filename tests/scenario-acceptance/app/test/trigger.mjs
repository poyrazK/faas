import assert from "node:assert/strict";

const response = await fetch(`${process.env.GREGALE_TEST_URL}/start`, { method: "POST" });
const body = await response.text();
assert.equal(response.status, 202, body);
assert.deepEqual(JSON.parse(body).statuses, [503, 200]);
