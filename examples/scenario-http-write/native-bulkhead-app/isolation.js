import http from "node:http";
import { randomUUID, timingSafeEqual } from "node:crypto";
import { createNativeCombinedCaller } from "./combined.js";

// Both routes run in this application process. Each owns its dependency policies.
export function createNativeIsolationCaller({ dependencyURLs, namespace, token }) {
  if (!dependencyURLs?.a || !dependencyURLs?.b) throw new Error("two_dependencies_required");
  const callers = Object.fromEntries(["a", "b"].map(name => [name,
    createNativeCombinedCaller({ dependencyURL: dependencyURLs[name], namespace, token })]));
  const instanceID = randomUUID();
  let stopped = false, stopping, health = 0;
  const status = () => ({ namespace, instance_id: instanceID, stopped, health_requests: health,
    dependencies: Object.fromEntries(Object.entries(callers).map(([name, caller]) => [name, caller.status()])) });
  const stop = () => stopping ||= (async () => { stopped = true; await Promise.all(Object.values(callers).map(caller => caller.stop())); })();
  const send = (response, code, body) => {
    if (response.destroyed) return;
    response.writeHead(code, { "content-type": "application/json", "cache-control": "no-store" });
    response.end(JSON.stringify(body));
  };
  const server = http.createServer((request, response) => {
    const name = request.url === "/a/query" ? "a" : request.url === "/b/query" ? "b" : undefined;
    if (name && request.method === "GET") {
      // Dispatch directly to the existing handler: no extra HTTP hop or socket.
      request.url = "/query";
      callers[name].server.emit("request", request, response);
    } else if (request.method === "GET" && request.url === "/isolation-status") send(response, 200, status());
    else if (request.method === "GET" && request.url === "/health") {
      health++;
      send(response, 200, { ready: !stopped, active_dependencies: Object.fromEntries(Object.entries(callers).map(([key, caller]) => [key, caller.status().active_dependencies])) });
    } else if (request.url === "/isolation-control") {
      const value = Buffer.from(request.headers.authorization || ""), expected = Buffer.from("Bearer " + token);
      if (value.length !== expected.length || !timingSafeEqual(value, expected)) send(response, 401, { error: "unauthorized" });
      else if (request.method !== "POST" || request.headers["x-test-namespace"] !== namespace) send(response, 400, { error: "invalid_control" });
      else stop().then(() => send(response, 200, status())).catch(() => send(response, 503, { error: "stop_failed" }));
    } else send(response, request.url === "/" ? 200 : 404, { ready: !stopped });
  });
  server.once("close", () => { stop().catch(() => {}); });
  return { server, stop, status };
}
