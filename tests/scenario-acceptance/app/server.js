import http from "node:http";
import { pathToFileURL } from "node:url";
import { deliverWithRetry } from "./delivery.js";

export function createApp({ sinkURL = process.env.SINK_URL, runID = process.env.TEST_RUN_ID } = {}) {
  return http.createServer(async (request, response) => {
    const path = new URL(request.url, "http://localhost").pathname;
    if (request.method === "GET" && path === "/") {
      response.writeHead(200, { "content-type": "text/plain" });
      response.end("ready\n");
      return;
    }
    if (request.method !== "POST" || path !== "/start") {
      response.writeHead(404);
      response.end();
      return;
    }
    try {
      const body = JSON.stringify({ run_id: runID });
      const statuses = await deliverWithRetry(fetch, sinkURL, body);
      response.writeHead(202, { "content-type": "application/json" });
      response.end(JSON.stringify({ statuses }));
    } catch (error) {
      response.writeHead(502, { "content-type": "application/json" });
      response.end(JSON.stringify({ error: String(error) }));
    }
  });
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  createApp().listen(Number(process.env.PORT ?? "8080"), "0.0.0.0");
}
