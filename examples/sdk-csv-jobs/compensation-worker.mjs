import pg from "pg";
import { pathToFileURL } from "node:url";
import { csvCompensation } from "./compensation.mjs";
const runtime = await import(
    pathToFileURL(process.env.GREGALE_NODE_JOB_RUNTIME).href
  ),
  sdk = await import(
    new URL("./preview.js", pathToFileURL(process.env.GREGALE_NODE_JOB_RUNTIME))
      .href
  );
const scope = JSON.parse(process.env.GREGALE_PIPELINE_SCOPE),
  client = sdk.createClient(scope.base_url, { token: process.env.FAAS_TOKEN }),
  pool = new pg.Pool({
    connectionString: process.env.DATABASE_URL,
    statement_timeout: 5000,
  });
try {
  const files = client.storage.files({
      appSlug: scope.app_slug,
      bucket: process.env.GREGALE_FILE_BUCKET,
    }),
    jobStore = await files.jobStore({ timeoutMs: 10000 });
  const { recovery, handler } = await csvCompensation({
    sdk,
    runtime,
    client,
    scope,
    pool,
    jobStore,
    reviewBaseURL: process.env.GREGALE_COMP_REVIEW_URL ?? "http://127.0.0.1/",
  });
  await runtime.runJobTask(recovery.workerContract, handler, {
    timeoutMs: 60000,
    store: jobStore,
  });
} catch (error) {
  console.error(JSON.stringify({ code: error.code ?? "handler_failed" }));
  process.exitCode = 1;
} finally {
  await pool.end();
}
