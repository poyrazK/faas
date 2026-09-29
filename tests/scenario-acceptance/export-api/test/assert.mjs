import { readFile } from "node:fs/promises";
import { assertCustomerExport } from "./contract.mjs";

const invocationID = JSON.parse(await readFile(process.env.GREGALE_TEST_TRIGGER_OUTPUT, "utf8")).worker_invocation_id;
await assertCustomerExport({
  url: process.env.GREGALE_TEST_URL,
  workerURL: process.env.GREGALE_TEST_SERVICE_WORKER_URL,
  sinkURL: process.env.GREGALE_TEST_SERVICE_NOTIFICATIONS_URL,
  sinkToken: process.env.GREGALE_TEST_SINK_NOTIFICATIONS_TOKEN,
  runID: process.env.GREGALE_TEST_RUN_ID,
  customerA: process.env.GREGALE_TEST_CONSUMER_CUSTOMER_A_KEY,
  customerB: process.env.GREGALE_TEST_CONSUMER_CUSTOMER_B_KEY,
  invocationID,
});
