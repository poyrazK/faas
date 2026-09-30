const http = require("node:http");
const { randomUUID } = require("node:crypto");

const keys = new Map([
  [process.env.GREGALE_TEST_CONSUMER_CUSTOMER_A_KEY, "customer-a"],
  [process.env.GREGALE_TEST_CONSUMER_CUSTOMER_B_KEY, "customer-b"],
].filter(([key]) => key));
const exportsById = new Map();
const exportsByIdempotencyKey = new Map();

function send(response, status, body) {
  response.writeHead(status, { "content-type": "application/json" });
  response.end(JSON.stringify(body));
}

const server = http.createServer((request, response) => {
  if (request.method === "GET" && request.url === "/health") {
    return send(response, 200, { status: "ok" });
  }

  const token = request.headers.authorization?.replace(/^Bearer\s+/i, "");
  const owner = keys.get(token);
  if (!owner) return send(response, 401, { error: "unauthorized" });

  if (request.method === "POST" && request.url === "/exports") {
    const idempotencyKey = request.headers["idempotency-key"];
    if (!idempotencyKey) return send(response, 400, { error: "idempotency_key_required" });
    let body = "";
    request.setEncoding("utf8");
    request.on("data", (chunk) => { body += chunk; });
    request.on("end", () => {
      let input;
      try { input = JSON.parse(body); } catch { return send(response, 400, { error: "invalid_json" }); }
      const scopedKey = `${owner}:${idempotencyKey}`;
      const existingId = exportsByIdempotencyKey.get(scopedKey);
      const record = existingId ? exportsById.get(existingId) : {
        id: randomUUID(), owner, format: input.format, status: "queued",
      };
      if (!existingId) {
        exportsById.set(record.id, record);
        exportsByIdempotencyKey.set(scopedKey, record.id);
      }
      return send(response, 202, { ...record, created: !existingId });
    });
    return;
  }

  const match = request.url.match(/^\/exports\/([a-f0-9-]+)$/i);
  if (request.method === "GET" && match) {
    const record = exportsById.get(match[1]);
    if (!record || record.owner !== owner) return send(response, 404, { error: "not_found" });
    return send(response, 200, record);
  }
  return send(response, 404, { error: "not_found" });
});

server.listen(Number(process.env.PORT), process.env.HOST);
