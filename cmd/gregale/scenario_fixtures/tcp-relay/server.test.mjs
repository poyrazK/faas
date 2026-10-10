import test from "node:test";
import assert from "node:assert/strict";
import net from "node:net";
import { once } from "node:events";
import { createTCPRelay } from "./server.js";

test("TCP relay preserves binary payloads and a client's half-close", async t => {
  const upstream = net.createServer({ allowHalfOpen: true }, socket => {
    const chunks = [];
    socket.on("data", chunk => chunks.push(chunk));
    socket.on("end", () => socket.end(Buffer.concat(chunks)));
    socket.on("error", () => socket.destroy());
  });
  upstream.listen(0, "127.0.0.1");
  await once(upstream, "listening");
  const relay = createTCPRelay({ upstream: "127.0.0.1:" + upstream.address().port });
  relay.listen(0, "127.0.0.1");
  await once(relay, "listening");
  t.after(() => { relay.stop(); upstream.close(); });
  const client = net.connect(relay.address().port, "127.0.0.1");
  t.after(() => client.destroy());
  const payload = Buffer.alloc(32768);
  for (let i = 0; i < payload.length; i++) payload[i] = i % 256;
  const chunks = [];
  client.on("data", chunk => chunks.push(chunk));
  await once(client, "connect");
  client.end(payload);
  await once(client, "end");
  assert.deepEqual(Buffer.concat(chunks), payload);
});

test("TCP relay rejects upstream credentials", () => {
  assert.throws(() => createTCPRelay({ upstream: "user:pass@host:5432" }), /host:port/);
});
