import http from "node:http";
import net from "node:net";
import { pathToFileURL } from "node:url";

// The upstream is fixed at provisioning time. Payloads (including TLS) pass
// through unchanged, and pipe backpressure bounds buffering in both directions.
export function createTCPRelay({ upstream, idleMs = 60000 }) {
  const target = new URL("tcp://" + upstream);
  if (!target.hostname || !target.port || target.username || target.password ||
      target.pathname || target.search || target.hash) {
    throw new Error("upstream must be a fixed host:port");
  }
  const connections = new Set();
  const server = net.createServer({ allowHalfOpen: true }, client => {
    const remote = net.connect({
      host: target.hostname.replace(/^\[|\]$/g, ""),
      port: Number(target.port),
      allowHalfOpen: true,
    });
    connections.add(client);
    connections.add(remote);
    for (const socket of [client, remote]) {
      socket.on("close", () => connections.delete(socket));
      socket.setTimeout(idleMs, () => { client.destroy(); remote.destroy(); });
      socket.on("error", () => { client.destroy(); remote.destroy(); });
    }
    client.on("close", () => { if (!client.readableEnded) remote.destroy(); });
    remote.on("close", () => { if (!remote.readableEnded) client.destroy(); });
    client.pipe(remote);
    remote.pipe(client);
  });
  server.on("close", () => { for (const socket of connections) socket.destroy(); });
  server.stop = () => {
    for (const socket of connections) socket.destroy();
    server.close();
  };
  return server;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const relay = createTCPRelay({ upstream: process.env.GREGALE_TEST_TCP_UPSTREAM });
  const health = http.createServer((request, response) => {
    const ready = request.url === "/" || request.url === "/healthz";
    response.writeHead(ready ? 200 : 404);
    response.end(ready ? "ready\n" : "");
  });
  relay.listen(Number(process.env.GREGALE_TEST_TCP_PORT), "0.0.0.0", () => {
    health.listen(Number(process.env.PORT ?? "8080"), "0.0.0.0");
  });
  for (const signal of ["SIGINT", "SIGTERM"]) {
    process.once(signal, () => { relay.stop(); health.close(); });
  }
}
