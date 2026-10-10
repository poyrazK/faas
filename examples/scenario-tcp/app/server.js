import http from "node:http";
import net from "node:net";
import { performance } from "node:perf_hooks";

let pooledSocket;
let connecting;
let connectionGeneration = 0;
let sequence = 0;
let pendingExchange;

function getPooledSocket() {
  if (pooledSocket && !pooledSocket.destroyed) return Promise.resolve(pooledSocket);
  if (connecting) return connecting;
  const socket = net.connect({ host: process.env.TCP_HOST, port: Number(process.env.TCP_PORT) });
  pooledSocket = socket;
  connectionGeneration++;
  connecting = new Promise((resolve, reject) => {
    socket.once("connect", () => {
      connecting = undefined;
      resolve(socket);
    });
    socket.once("error", error => {
      connecting = undefined;
      if (pooledSocket === socket) pooledSocket = undefined;
      reject(error);
    });
  });
  socket.on("data", chunk => {
    const operation = pendingExchange;
    if (!operation || operation.socket !== socket) return;
    operation.received = Buffer.concat([operation.received, chunk]);
    if (operation.received.length > operation.payload.length) {
      operation.finish("error", "unexpected_extra_bytes");
    } else if (operation.received.length === operation.payload.length) {
      operation.finish(operation.received.equals(operation.payload) ? "ok" : "error",
        operation.received.equals(operation.payload) ? undefined : "payload_changed");
    }
  });
  socket.on("error", error => {
    if (pooledSocket === socket) pooledSocket = undefined;
    if (pendingExchange?.socket === socket) pendingExchange.finish("error", error.code);
  });
  socket.on("close", () => {
    if (pooledSocket === socket) pooledSocket = undefined;
    if (pendingExchange?.socket === socket) pendingExchange.finish("error", "closed");
  });
  return connecting;
}

async function pooledProbe(response) {
  if (pendingExchange) {
    response.writeHead(409, { "content-type": "application/json" });
    response.end(JSON.stringify({ error: "probe_already_running" }));
    return;
  }
  const started = performance.now();
  let socket;
  try {
    socket = await getPooledSocket();
  } catch (error) {
    response.writeHead(200, { "content-type": "application/json" });
    response.end(JSON.stringify({ outcome: "error", error: error.code, connection_generation: connectionGeneration }));
    return;
  }
  const payload = Buffer.alloc(16);
  payload.writeUInt32BE(++sequence);
  let done = false;
  let timer;
  const operation = {
    socket,
    payload,
    received: Buffer.alloc(0),
    finish(outcome, error) {
      if (done) return;
      done = true;
      clearTimeout(timer);
      if (pendingExchange === operation) pendingExchange = undefined;
      if (outcome !== "ok") {
        if (pooledSocket === socket) pooledSocket = undefined;
        socket.destroy();
      }
      if (!response.destroyed) {
        response.writeHead(200, { "content-type": "application/json" });
        response.end(JSON.stringify({
          outcome,
          error,
          bytes: operation.received.length,
          elapsed_ms: performance.now() - started,
          connection_generation: connectionGeneration,
        }));
      }
    },
  };
  pendingExchange = operation;
  timer = setTimeout(() => operation.finish("timeout", "application_deadline"), 4000);
  response.on("close", () => {
    if (!done) operation.finish("error", "http_client_closed");
  });
  socket.write(payload);
}

http.createServer((request, response) => {
  const url = new URL(request.url, "http://app");
  if (url.pathname === "/pool/probe") {
    void pooledProbe(response);
    return;
  }
  if (url.pathname !== "/probe") {
    response.writeHead(200);
    response.end("ready\n");
    return;
  }
  const bytes = Math.max(1, Math.min(65536, Number(url.searchParams.get("bytes")) || 16));
  const payload = Buffer.alloc(bytes, 0x67);
  const started = performance.now();
  let received = 0;
  let done = false;
  const socket = net.connect({ host: process.env.TCP_HOST, port: Number(process.env.TCP_PORT) });
  const finish = (outcome, error) => {
    if (done) return;
    done = true;
    clearTimeout(deadline);
    socket.destroy();
    response.writeHead(200, { "content-type": "application/json" });
    response.end(JSON.stringify({ outcome, error, bytes: received, elapsed_ms: performance.now() - started }));
  };
  // A total application deadline, including periods without forwarded bytes.
  const deadline = setTimeout(() => finish("timeout", "application_deadline"), 2000);
  socket.on("connect", () => socket.write(payload));
  socket.on("data", chunk => {
    if (!chunk.equals(payload.subarray(received, received + chunk.length))) {
      finish("error", "payload_changed");
      return;
    }
    received += chunk.length;
    if (received === bytes) finish("ok");
  });
  socket.on("error", error => finish("error", error.code));
  socket.on("end", () => finish("error", "closed"));
  response.on("close", () => { clearTimeout(deadline); socket.destroy(); });
}).listen(Number(process.env.PORT || 8080), "0.0.0.0");
