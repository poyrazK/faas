import { writeFile } from "node:fs/promises";
import { submitCustomerExport } from "./contract.mjs";

const { invocation_id } = await submitCustomerExport({
  url: process.env.GREGALE_TEST_URL,
  runID: process.env.GREGALE_TEST_RUN_ID,
  customerA: process.env.GREGALE_TEST_CONSUMER_CUSTOMER_A_KEY,
});
await writeFile(process.env.GREGALE_TEST_TRIGGER_OUTPUT, JSON.stringify({ worker_invocation_id: invocation_id }));
