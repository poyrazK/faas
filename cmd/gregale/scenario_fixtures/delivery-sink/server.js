import http from "node:http";
import { timingSafeEqual } from "node:crypto";
import { pathToFileURL } from "node:url";

function tokenMatches(expected, actual) {
  const left = Buffer.from(expected);
  const right = Buffer.from(actual);
  return left.length === right.length && timingSafeEqual(left, right);
}

export function createDeliverySink({ failFirst = 0, token = "" } = {}) {
  const attempts = [];
  return http.createServer(async (request, response) => {
    const path = new URL(request.url, "http://localhost").pathname;
    if (request.method === "GET" && path === "/") {
      response.writeHead(200, { "content-type": "text/plain" });
      response.end("ready\n");
      return;
    }
    if (request.method === "GET" && path === "/__gregale_test__/attempts") {
      const presented = request.headers.authorization?.replace(/^Bearer /, "") ?? "";
      if (!token || !tokenMatches(token, presented)) {
        response.writeHead(401);
        response.end();
        return;
      }
      response.writeHead(200, { "content-type": "application/json" });
      response.end(JSON.stringify({ attempts }));
      return;
    }
    if (request.method !== "POST" || path !== "/deliver") {
      response.writeHead(404);
      response.end();
      return;
    }
    if (attempts.length >= 100) {
      response.writeHead(429);
      response.end();
      return;
    }
    let body = "";
    for await (const chunk of request) {
      body += chunk;
      if (body.length > 65536) {
        response.writeHead(413);
        response.end();
        return;
      }
    }
    const attempt = attempts.length + 1;
    const status = attempt <= failFirst ? 503 : 200;
    attempts.push({
      attempt,
      status,
      at: new Date().toISOString(),
      body: body.slice(0, 4096),
      body_truncated: body.length > 4096,
    });
    response.writeHead(status, { "content-type": "application/json" });
    response.end(JSON.stringify({ attempt, accepted: status === 200 }));
  });
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const failFirst = Number.parseInt(process.env.GREGALE_TEST_SINK_FAIL_FIRST ?? "0", 10);
  const token = process.env.GREGALE_TEST_SINK_TOKEN ?? "";
  createDeliverySink({ failFirst, token }).listen(Number(process.env.PORT ?? "8080"), "0.0.0.0");
}
