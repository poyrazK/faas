import http from "node:http";

export function createNativeAuthCaller({ dependencyURL, deadlineMS = 4000 }) {
  return http.createServer(async (request, response) => {
    const path = request.url === "/query" ? "/query" : request.url === "/status" ? "/native-auth-status" : null;
    if (request.method !== "GET" || !path) { response.writeHead(request.url === "/" ? 200 : 404); response.end(); return; }
    const controller = new AbortController(); response.once("close", () => controller.abort());
    let status = 503, body = { error: "dependency_unavailable", retryable: true };
    try {
      const result = await fetch(dependencyURL + path, { signal: AbortSignal.any([controller.signal, AbortSignal.timeout(deadlineMS)]), redirect: "error" });
      const value = await result.json();
      if (result.status === 200) { status = 200; body = value; }
    } catch { /* Fixed receipt; no dependency errors or credentials. */ }
    if (!response.destroyed) { response.writeHead(status, { "content-type": "application/json", "cache-control": "no-store" }); response.end(JSON.stringify(body)); }
  });
}
