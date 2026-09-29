import { createHash } from "node:crypto";
import http from "node:http";
import { pathToFileURL } from "node:url";

const send = (response, status, body) => {
  response.writeHead(status, { "content-type": "application/json" });
  response.end(JSON.stringify(body));
};

export function createExportAPI({ workerURL = "http://worker.svc.gregale:10080", request = fetch } = {}) {
  return http.createServer(async (incoming, outgoing) => {
    const path = new URL(incoming.url, "http://localhost").pathname;
    if (incoming.method === "GET" && path === "/") return send(outgoing, 200, { ready: true });
    if (!(incoming.method === "POST" && path === "/exports") &&
        !(incoming.method === "GET" && /^\/exports\/[a-f0-9]{32}$/.test(path))) {
      return send(outgoing, 404, { error: "not_found" });
    }
    const bearer = incoming.headers.authorization;
    if (!bearer?.startsWith("Bearer ") || bearer.length <= 7) {
      return send(outgoing, 401, { error: "unauthorized" });
    }
    const owner = createHash("sha256").update(bearer.slice(7)).digest("hex");
    let body;
    if (incoming.method === "POST") {
      const chunks = [];
      for await (const chunk of incoming) chunks.push(chunk);
      if (Buffer.concat(chunks).length > 4096) return send(outgoing, 413, { error: "too_large" });
      try { body = JSON.parse(Buffer.concat(chunks).toString()); }
      catch { return send(outgoing, 400, { error: "invalid_json" }); }
      if (typeof body.idempotency_key !== "string" || body.idempotency_key.length < 1 || body.idempotency_key.length > 128 ||
          typeof body.report !== "string" || body.report.length < 1 || body.report.length > 512) {
        return send(outgoing, 400, { error: "invalid_export" });
      }
    }
    const target = incoming.method === "POST" ? "/process" : `/result/${path.slice("/exports/".length)}`;
    try {
      const result = await request(`${workerURL}${target}`, {
        method: incoming.method,
        headers: { "content-type": "application/json", "x-owner-digest": owner },
        body: body && JSON.stringify(body),
      });
      return send(outgoing, result.status, await result.json());
    } catch (error) {
      return send(outgoing, 502, { error: "worker_unavailable", detail: String(error) });
    }
  });
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  createExportAPI().listen(Number(process.env.PORT ?? "8080"), "0.0.0.0");
}
