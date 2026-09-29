import assert from "node:assert/strict";

const runID = process.env.GREGALE_TEST_RUN_ID;
const response = await fetch(`${process.env.GREGALE_TEST_URL}/exports`, {
  method: "POST",
  headers: {
    Authorization: `Bearer ${process.env.GREGALE_TEST_CONSUMER_CUSTOMER_A_KEY}`,
    "Content-Type": "application/json",
  },
  body: JSON.stringify({ idempotency_key: `export-${runID}`, report: "Customer A report" }),
});
const body = await response.text();
assert.equal(response.status, 202, body);
assert.equal(JSON.parse(body).created, true);
assert.deepEqual(JSON.parse(body).delivery_statuses, [503, 200]);
