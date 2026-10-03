import { createHash } from "node:crypto";
import http from "node:http";
import { pathToFileURL } from "node:url";

const send = (response, status, body) => {
  response.writeHead(status, { "content-type": "application/json" });
  response.end(JSON.stringify(body));
};

export function createWorker({ store, notificationURL, runID, workerToken = process.env.WORKER_TEST_TOKEN, request = fetch, failFirstProcess = false } = {}) {
  return http.createServer(async (incoming, outgoing) => {
    const path = new URL(incoming.url, "http://localhost").pathname;
    if (incoming.method === "GET" && path === "/") return send(outgoing, 200, { ready: true });
    if (!workerToken || incoming.headers["x-worker-test-token"] !== workerToken) return send(outgoing, 401, { error: "unauthorized_worker_request" });
    const owner = incoming.headers["x-owner-digest"];
    if (typeof owner !== "string" || !/^[a-f0-9]{64}$/.test(owner)) return send(outgoing, 401, { error: "unauthorized" });
    try {
      if (incoming.method === "POST" && path === "/process") {
        const chunks = [];
        for await (const chunk of incoming) chunks.push(chunk);
        const body = JSON.parse(Buffer.concat(chunks).toString());
        if (typeof body.idempotency_key !== "string" || typeof body.report !== "string") {
          return send(outgoing, 400, { error: "invalid_export" });
        }
        const id = createHash("sha256").update(`${owner}:${body.idempotency_key}`).digest("hex").slice(0, 32);
        if (failFirstProcess) {
          const faultKey = `faults/${runID}/${id}.json`;
          if (!(await store.get(faultKey))) {
            await store.put(faultKey, { failed_once: true });
            return send(outgoing, 503, { id, error: "planned_retry" });
          }
        }
        const key = `reports/${runID}/${id}.json`;
        const existing = await store.get(key);
        if (existing) return send(outgoing, 200, { id, created: false });
        await store.put(key, { id, owner, report: body.report, run_id: runID });
        const notification = JSON.stringify({ export_id: id, run_id: runID });
        const statuses = [];
        for (let attempt = 0; attempt < 2; attempt++) {
          const result = await request(notificationURL, {
            method: "POST", headers: { "content-type": "application/json" }, body: notification,
          });
          statuses.push(result.status);
          if (result.ok) return send(outgoing, 202, { id, created: true, delivery_statuses: statuses });
        }
        return send(outgoing, 502, { id, error: "delivery_failed", delivery_statuses: statuses });
      }
      if (incoming.method === "GET" && /^\/result\/[a-f0-9]{32}$/.test(path)) {
        const id = path.slice("/result/".length);
        const result = await store.get(`reports/${runID}/${id}.json`);
        if (!result) return send(outgoing, 404, { error: "not_found" });
        if (result.owner !== owner) return send(outgoing, 403, { error: "forbidden" });
        return send(outgoing, 200, { id, report: result.report, run_id: result.run_id });
      }
      return send(outgoing, 404, { error: "not_found" });
    } catch (error) {
      return send(outgoing, 502, { error: "worker_failed", detail: String(error) });
    }
  });
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const { S3Client, GetObjectCommand, PutObjectCommand } = await import("@aws-sdk/client-s3");
  const prefix = "EXPORT_STORAGE";
  const bucket = process.env[`${prefix}_BUCKET`];
  const s3 = new S3Client({
    endpoint: process.env[`${prefix}_ENDPOINT`],
    region: process.env[`${prefix}_REGION`],
    forcePathStyle: process.env[`${prefix}_ADDRESSING_STYLE`] === "path",
    credentials: {
      accessKeyId: process.env[`${prefix}_ACCESS_KEY_ID`],
      secretAccessKey: process.env[`${prefix}_SECRET_ACCESS_KEY`],
    },
  });
  const store = {
    async get(key) {
      try {
        const result = await s3.send(new GetObjectCommand({ Bucket: bucket, Key: key }));
        return JSON.parse(await result.Body.transformToString());
      } catch (error) {
        if (error.name === "NoSuchKey" || error.$metadata?.httpStatusCode === 404) return null;
        throw error;
      }
    },
    async put(key, value) {
      await s3.send(new PutObjectCommand({
        Bucket: bucket, Key: key, Body: JSON.stringify(value), ContentType: "application/json",
      }));
    },
  };
  createWorker({ store, notificationURL: process.env.NOTIFICATION_URL, runID: process.env.TEST_RUN_ID,
    workerToken: process.env.WORKER_TEST_TOKEN,
    failFirstProcess: process.env.FAIL_FIRST_PROCESS === "1" })
    .listen(Number(process.env.PORT ?? "8080"), "0.0.0.0");
}
