import http from "node:http";
import net from "node:net";
import { randomUUID } from "node:crypto";

// Acceptance-only fixture. The dependency probe returns HTTP 200 even when its
// TCP dependency times out, so a healthy stream can coexist with a stuck one.
export function createRecoveryProbe({ dependencyHost, dependencyPort, delayMS, payloadBytes = 6 }) {
  if (!Number.isInteger(delayMS) || delayMS < 0 || delayMS > 15000) throw new Error("invalid probe delay");
  if (![6, 16384].includes(payloadBytes)) throw new Error("invalid probe payload");
  const payload = payloadBytes === 6 ? Buffer.from("probe\n") : Buffer.alloc(payloadBytes, 120);
  const instanceID = randomUUID(), sockets = new Set();
  const probe = response => new Promise(resolve => {
    const socket = net.connect({ host: dependencyHost, port: dependencyPort });
    sockets.add(socket);
    let finished = false, received = 0, reachable = false;
    const chunks = [];
    const disconnected = () => finish(false);
    const finish = result => {
      if (finished) return;
      finished = true;
      clearTimeout(timer);
      socket.destroy();
      reachable = result;
      response.off("close", disconnected);
    };
    const timer = setTimeout(() => finish(false), 250);
    response.once("close", disconnected);
    socket.once("close", () => { sockets.delete(socket); if (!finished) finish(false); resolve(reachable); });
    socket.once("connect", () => socket.write(payload));
    socket.on("data", chunk => {
      received += chunk.length;
      if (received > payload.length) return finish(false);
      chunks.push(chunk);
      if (received === payload.length) finish(Buffer.concat(chunks).equals(payload));
    });
    socket.once("error", () => finish(false));
    socket.once("end", () => finish(false));
  });
  return http.createServer(async (request, response) => {
    const send = body => {
      if (response.destroyed) return;
      response.writeHead(200, { "content-type": "application/json", "cache-control": "no-store" });
      response.end(JSON.stringify(body));
    };
    if (request.url === "/dependency") send({ dependency: await probe(response) ? "reachable" : "unavailable" });
    else if (request.method === "GET" && request.url === "/connections") send({ instance_id: instanceID, active_connections: sockets.size });
    else if (request.url === "/stalled") {
      // Cancel the timer if the client disconnects; shutdown owns no jobs.
      const timer = setTimeout(() => send({ completed: true }), delayMS);
      response.once("close", () => clearTimeout(timer));
    } else if (request.url === "/") send({ ready: true });
    else { response.writeHead(404); response.end(); }
  });
}

export function createEchoDependency({ maxBytes = 32 } = {}) {
  if (![32, 16384].includes(maxBytes)) throw new Error("invalid echo limit");
  return net.createServer(socket => {
    let bytes = 0;
    socket.on("error", () => {});
    socket.on("data", chunk => {
      bytes += chunk.length;
      if (bytes > maxBytes) socket.destroy();
      else socket.write(chunk);
    });
    socket.setTimeout(1000, () => socket.destroy());
  });
}
