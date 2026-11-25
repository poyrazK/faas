// Native workflow entry only. Mount authenticated MCP and human review routes in the application backend.
import http from "node:http";
import { pathToFileURL } from "node:url";
import { resolve } from "node:path";
import { csvRefund } from "./refund.mjs";
const runtime = process.env.GREGALE_NODE_JOB_RUNTIME
  ? pathToFileURL(resolve(process.env.GREGALE_NODE_JOB_RUNTIME))
  : new URL("../../sdk/node/dist/job-runtime.js", import.meta.url);
const sdk = await import(new URL("./preview.js", runtime)),
  { serveWorkflows, toNodeListener } = await import(
    new URL("./workflows.js", runtime)
  );
const { default: pg } = await import("pg"),
  pool = new pg.Pool({
    connectionString: process.env.DATABASE_URL,
    statement_timeout: 5000,
  });
const action = await csvRefund({
  sdk,
  pool,
  scope: JSON.parse(process.env.GREGALE_PIPELINE_SCOPE),
  relayURL: process.env.GREGALE_OUTBOUND_URL,
  reviewBaseURL: process.env.GREGALE_REFUND_REVIEW_URL,
});
const entry = toNodeListener(serveWorkflows([action.definition]));
const server = http.createServer((request, response) => {
  if (request.method === "POST" && request.url === "/action-csv-refund")
    return entry(request, response);
  response.writeHead(404);
  response.end();
});
server.listen(
  Number(process.env.PORT ?? 8080),
  process.env.HOST ?? "127.0.0.1",
);
process.on("SIGTERM", () =>
  server.close(() => {
    void pool.end();
  }),
);
