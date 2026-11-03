// Internal service of the multi-service journey (tests/journeys,
// TestMultiServiceProject). Its Compose service publishes no ports, so it
// has no public URL; the web service calls it by name.
//   GET /healthz  readiness
//   GET /whoami   {"service":"api"}

import http from "node:http";

const server = http.createServer((req, res) => {
  const url = new URL(req.url, "http://api.invalid");
  if (url.pathname === "/healthz") {
    res.writeHead(200, { "content-type": "application/json" });
    res.end(JSON.stringify({ ok: true }));
    return;
  }
  if (url.pathname !== "/whoami") {
    res.writeHead(404, { "content-type": "application/json" });
    res.end(JSON.stringify({ error: "not found" }));
    return;
  }
  res.writeHead(200, { "content-type": "application/json", "cache-control": "no-store" });
  res.end(JSON.stringify({ service: "api", pid: process.pid }));
});

server.listen(Number(process.env.PORT) || 8080, () => {
  console.log("gregale-e2e-shop-api listening");
});
