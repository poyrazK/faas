import assert from "node:assert/strict";
import net from "node:net";
import tls from "node:tls";
import { tlsFixture } from "./tls-fixture.mjs";
import { acceptPostgresTLS, connectPostgresTLS } from "./pg-tls-transport.mjs";
import test from "node:test";
import { createCommitProxy } from "./pg-commit-proxy.mjs";

function frame(type, body) {
  const result = Buffer.alloc(5 + body.length);
  result[0] = type.charCodeAt(0); result.writeInt32BE(body.length + 4, 1); body.copy(result, 5);
  return result;
}
const startup = Buffer.alloc(8);
startup.writeInt32BE(8, 0); startup.writeInt32BE(196608, 4);
const commit = frame("Q", Buffer.from("COMMIT\0"));
const complete = frame("C", Buffer.from("COMMIT\0"));
const ready = frame("Z", Buffer.from("I"));

const identity = await tlsFixture();
test.after(() => identity.close());
for (const encrypted of [false, true]) for (const mode of ["drop", "unarmed", "reject", "rollback", "reject-delay", "reject-stall", "rollback-delay", "rollback-stall"]) {
  test(`fragmented ${encrypted ? "TLS" : "plaintext"} Postgres frames ${mode} only successful armed COMMIT replies`, { timeout: 3000 }, async t => {
    const resultMode = mode.split("-")[0];
    const faultMode = mode.split("-")[1];
    const peers = new Set();
    const reply = resultMode === "reject" ? Buffer.concat([frame("E", Buffer.from("Mrejected\0\0")), ready]) : Buffer.concat([resultMode === "rollback" ? frame("C", Buffer.from("ROLLBACK\0")) : complete, ready]);
    const track = socket => { peers.add(socket); socket.on("error", () => {}); socket.on("close", () => peers.delete(socket)); };
    const serve = socket => {
      let received = Buffer.alloc(0);
      let sent = false;
      socket.on("data", chunk => {
        received = Buffer.concat([received, chunk]);
        if (sent || received.length < startup.length + commit.length) return;
        sent = true;
        assert.deepEqual(received, Buffer.concat([startup, commit]));
        let index = 0;
        const send = () => { if (index < reply.length && !socket.destroyed) { socket.write(reply.subarray(index, ++index)); setImmediate(send); } };
        send();
      });
    };
    const context = tls.createSecureContext(identity);
    const server = net.createServer(socket => {
      track(socket);
      if (encrypted) acceptPostgresTLS(socket, context, track).then(serve).catch(() => socket.destroy());
      else serve(socket);
    });
    await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
    const proxy = await createCommitProxy(`postgres://fixture@127.0.0.1:${server.address().port}/fixture?sslmode=${encrypted ? "verify-full" : "disable"}`, encrypted ? { tls: identity } : {});
    t.after(async () => { await proxy.close(); for (const socket of peers) socket.destroy(); await new Promise(resolve => server.close(resolve)); });
    if (mode !== "unarmed") proxy.arm(faultMode ? { mode: faultMode, durationMS: 80 } : undefined);
    const destination = new URL(proxy.connectionString);
    const client = encrypted ? await connectPostgresTLS(destination, identity, socket => socket.on("error", () => {})) : net.connect(Number(destination.port), destination.hostname);
    client.on("error", () => {});
    const output = [];
    await new Promise(resolve => {
      let bytes = 0;
      client.on("data", chunk => { output.push(chunk); bytes += chunk.length; if (bytes === reply.length) client.destroy(); });
      client.once("close", resolve);
      const sendInput = () => {
        const input = Buffer.concat([startup, commit]);
        let index = 0;
        const send = () => { if (index < input.length && !client.destroyed) { client.write(input.subarray(index, ++index)); setImmediate(send); } };
        send();
      };
      if (encrypted) sendInput(); else client.once("connect", sendInput);
    });
    assert.deepEqual(Buffer.concat(output), mode === "drop" ? Buffer.alloc(0) : reply);
    assert.equal(proxy.evidence().acknowledgements_dropped, mode === "drop" ? 1 : 0);
    assert.equal(proxy.evidence().commit_errors_forwarded, resultMode === "reject" ? 1 : 0);
    assert.equal(proxy.evidence().faults_fired, mode === "drop" ? 1 : 0);
    assert.equal(proxy.evidence().invalid_frames, 0);
    assert.equal(proxy.evidence().tls_connections_verified, encrypted ? 1 : 0);
    await proxy.close(); assert.equal(proxy.evidence().connections, 0);
  });
}

test("commit proxy requires explicit TLS options and loopback destinations", async () => {
  await assert.rejects(createCommitProxy("postgres://fixture@127.0.0.1:5432/db?sslmode=require"));
  await assert.rejects(createCommitProxy("postgres://fixture@example.invalid:5432/db?sslmode=disable"));
  await assert.rejects(createCommitProxy("postgres://fixture@127.0.0.1:5432/db?sslmode=no-verify", { tls: identity }));
  await assert.rejects(createCommitProxy("postgres://fixture@127.0.0.1:5432/db?sslmode=disable", { tls: identity }));
  await assert.rejects(createCommitProxy("postgres://fixture@127.0.0.1:5432/db?sslmode=verify-full", { tls: { ...identity, ca: undefined } }));
  await assert.rejects(createCommitProxy("postgres://fixture@127.0.0.1:5432/db?sslmode=verify-full", { tls: { ...identity, servername: undefined } }));
});

for (const failure of ["untrusted-client", "wrong-client-host", "untrusted-upstream", "wrong-upstream-host", "plaintext-client", "tls-refused-upstream"]) {
  test(`TLS proxy fails closed for ${failure}`, { timeout: 3000 }, async t => {
    const peers = new Set();
    let applicationBytes = 0;
    const track = socket => { peers.add(socket); socket.on("error", () => {}); socket.on("close", () => peers.delete(socket)); };
    const context = tls.createSecureContext(identity);
    const server = net.createServer(socket => {
      track(socket);
      if (failure === "tls-refused-upstream") socket.once("data", () => socket.end("N"));
      else acceptPostgresTLS(socket, context, track).then(encrypted => encrypted.on("data", chunk => { applicationBytes += chunk.length; })).catch(() => socket.destroy());
    });
    await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
    const upstreamIdentity = { ...identity };
    if (failure === "untrusted-upstream") upstreamIdentity.ca = tls.rootCertificates;
    if (failure === "wrong-upstream-host") upstreamIdentity.servername = "wrong.example.invalid";
    const proxy = await createCommitProxy(`postgres://fixture@127.0.0.1:${server.address().port}/fixture?sslmode=verify-full`, { tls: upstreamIdentity });
    t.after(async () => { await proxy.close(); for (const socket of peers) socket.destroy(); await new Promise(resolve => server.close(resolve)); });
    proxy.arm();
    const destination = new URL(proxy.connectionString);
    if (failure === "plaintext-client") {
      const client = net.connect(Number(destination.port), destination.hostname);
      client.on("error", () => {});
      await new Promise(resolve => { client.once("close", resolve); client.once("connect", () => client.write(startup)); });
    } else {
      const clientIdentity = { ...identity };
      if (failure === "untrusted-client") clientIdentity.ca = tls.rootCertificates;
      if (failure === "wrong-client-host") clientIdentity.servername = "wrong.example.invalid";
      const connecting = connectPostgresTLS(destination, clientIdentity, socket => socket.on("error", () => {}));
      if (["untrusted-client", "wrong-client-host"].includes(failure)) await assert.rejects(connecting, /postgres_tls_verification_failed/);
      else {
        const client = await connecting;
        await new Promise(resolve => { client.once("close", resolve); client.write(Buffer.concat([startup, commit])); });
      }
    }
    while (proxy.evidence().connections) await new Promise(resolve => setImmediate(resolve));
    await proxy.close();
    assert.equal(proxy.evidence().armed, true);
    assert.equal(applicationBytes, 0);
    assert.equal(proxy.evidence().commits_forwarded, 0);
    assert.equal(proxy.evidence().acknowledgements_dropped, 0);
    assert.equal(proxy.evidence().tls_connections_verified, 0);
    assert.equal(proxy.evidence().connections, 0);
    assert.equal(proxy.evidence().tls_failures, 1);
  });
}

for (const termination of ["shutdown", "deadline"]) for (const phase of ["client-ssl-request", "upstream-ssl-request"]) {
  test(`${termination} joins incomplete ${phase} negotiation`, { timeout: 5000 }, async t => {
    const peers = new Set();
    let accepted;
    const received = new Promise(resolve => { accepted = resolve; });
    const server = net.createServer(socket => {
      peers.add(socket); socket.on("error", () => {}); socket.on("close", () => peers.delete(socket));
      socket.once("data", accepted); // Intentionally never answer SSLRequest.
    });
    await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
    const proxy = await createCommitProxy(`postgres://fixture@127.0.0.1:${server.address().port}/fixture?sslmode=verify-full`, { tls: identity });
    t.after(async () => { await proxy.close(); for (const socket of peers) socket.destroy(); await new Promise(resolve => server.close(resolve)); });
    const destination = new URL(proxy.connectionString);
    if (phase === "client-ssl-request") {
      const client = net.connect(Number(destination.port), destination.hostname);
      client.on("error", () => {});
      await new Promise(resolve => client.once("connect", resolve));
      client.write(Buffer.from([0, 0]));
      // Wait for the proxy to accept the connection before exercising close.
      while (!proxy.evidence().connections) await new Promise(resolve => setImmediate(resolve));
      const closed = new Promise(resolve => client.once("close", resolve));
      if (termination === "shutdown") await proxy.close();
      await closed;
    } else {
      const client = await connectPostgresTLS(destination, identity, socket => socket.on("error", () => {}));
      const closed = new Promise(resolve => client.once("close", resolve));
      await received;
      if (termination === "shutdown") await proxy.close();
      await closed;
    }
    await proxy.close();
    assert.equal(proxy.evidence().connections, 0);
    assert.equal(proxy.evidence().tls_connections_verified, 0);
    assert.equal(proxy.evidence().commits_forwarded, 0);
    assert.equal(proxy.evidence().tls_failures, termination === "deadline" ? 1 : 0);
    assert.throws(() => proxy.arm(), /commit_proxy_closed/);
  });
}

async function until(check, timeoutMS = 2500) {
  const deadline = Date.now() + timeoutMS;
  while (!check()) {
    if (Date.now() >= deadline) throw new Error("fixture condition timed out");
    await new Promise(resolve => setTimeout(resolve, 5));
  }
}

for (const encrypted of [false, true]) for (const direction of ["request", "response"]) for (const mode of ["delay", "stall"]) {
  test(`${encrypted ? "TLS" : "plaintext"} ${direction} ${mode} preserves frame order and measures the hold`, { timeout: 3000 }, async t => {
    const peers = new Set();
    let received = Buffer.alloc(0);
    const next = frame("Q", Buffer.from("SELECT 1\0"));
    const input = Buffer.concat([startup, commit, next]);
    const reply = Buffer.concat([complete, ready, frame("N", Buffer.from("Mafter-ready\0\0"))]);
    const track = socket => { peers.add(socket); socket.on("error", () => {}); socket.on("close", () => peers.delete(socket)); };
    const serve = socket => {
      let sent = false;
      socket.on("data", chunk => {
        received = Buffer.concat([received, chunk]);
        if (!sent && received.length >= startup.length + commit.length) { sent = true; socket.write(reply); }
      });
    };
    const context = tls.createSecureContext(identity);
    const server = net.createServer(socket => {
      track(socket);
      if (encrypted) acceptPostgresTLS(socket, context, track).then(serve).catch(() => socket.destroy());
      else serve(socket);
    });
    await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
    const proxy = await createCommitProxy(`postgres://fixture@127.0.0.1:${server.address().port}/fixture?sslmode=${encrypted ? "verify-full" : "disable"}`, encrypted ? { tls: identity } : {});
    t.after(async () => { await proxy.close(); for (const socket of peers) socket.destroy(); await new Promise(resolve => server.close(resolve)); });
    proxy.arm({ mode, direction, durationMS: 80 });
    const destination = new URL(proxy.connectionString);
    const client = encrypted ? await connectPostgresTLS(destination, identity, socket => socket.on("error", () => {})) : net.connect(Number(destination.port), destination.hostname);
    client.on("error", () => {});
    const chunks = [];
    client.on("data", chunk => chunks.push(chunk));
    const closed = new Promise(resolve => client.once("close", resolve));
    if (!encrypted) await new Promise(resolve => client.once("connect", resolve));
    client.write(input);
    await until(() => proxy.evidence().active_faults === 1);
    assert.deepEqual(Buffer.concat(chunks), Buffer.alloc(0));
    if (direction === "request") { await until(() => received.length >= startup.length); assert.deepEqual(received, startup); }
    await until(() => proxy.evidence().faults[0]?.outcome !== "holding");
    if (mode === "delay") {
      await until(() => Buffer.concat(chunks).length === reply.length && received.length === input.length);
      assert.deepEqual(received, input);
      assert.deepEqual(Buffer.concat(chunks), reply);
      client.destroy();
    }
    await closed;
    await proxy.close();
    const evidence = proxy.evidence();
    assert.equal(evidence.faults_fired, 1);
    assert.equal(evidence.faults[0].outcome, mode === "delay" ? "released" : "timed_out");
    assert.equal(evidence.faults[0].configured_ms, 80);
    assert.ok(evidence.faults[0].elapsed_ms >= 80);
    assert.equal(evidence.commits_forwarded, direction === "request" && mode === "stall" ? 0 : 1);
    assert.equal(evidence.acknowledgements_dropped, direction === "response" && mode === "stall" ? 1 : 0);
    assert.equal(evidence.active_faults, 0); assert.equal(evidence.fault_timers, 0);
    assert.equal(evidence.connections, 0); assert.equal(evidence.invalid_frames, 0);
  });
}

for (const cancellation of ["proxy-close", "client-close"]) {
  test(`cancel delayed TLS acknowledgement on ${cancellation} without releasing late frames`, { timeout: 3000 }, async t => {
    const peers = new Set();
    const track = socket => { peers.add(socket); socket.on("error", () => {}); socket.on("close", () => peers.delete(socket)); };
    const context = tls.createSecureContext(identity);
    const server = net.createServer(socket => {
      track(socket);
      acceptPostgresTLS(socket, context, track).then(encrypted => {
        let bytes = 0;
        encrypted.on("data", chunk => { bytes += chunk.length; if (bytes === startup.length + commit.length) encrypted.write(Buffer.concat([complete, ready])); });
      }).catch(() => socket.destroy());
    });
    await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
    const proxy = await createCommitProxy(`postgres://fixture@127.0.0.1:${server.address().port}/fixture?sslmode=verify-full`, { tls: identity });
    t.after(async () => { await proxy.close(); for (const socket of peers) socket.destroy(); await new Promise(resolve => server.close(resolve)); });
    proxy.arm({ mode: "delay", durationMS: 1000 });
    const client = await connectPostgresTLS(new URL(proxy.connectionString), identity, socket => socket.on("error", () => {}));
    let bytes = 0;
    client.on("data", chunk => { bytes += chunk.length; });
    const closed = new Promise(resolve => client.once("close", resolve));
    client.write(Buffer.concat([startup, commit]));
    await until(() => proxy.evidence().active_faults === 1);
    if (cancellation === "proxy-close") await proxy.close(); else client.destroy();
    await closed;
    await until(() => proxy.evidence().connections === 0);
    await proxy.close();
    const evidence = proxy.evidence();
    assert.equal(bytes, 0); assert.equal(evidence.faults[0].outcome, "canceled");
    assert.ok(evidence.faults[0].elapsed_ms < 1000);
    assert.equal(evidence.active_faults, 0); assert.equal(evidence.fault_timers, 0);
    assert.equal(evidence.acknowledgements_dropped, 0);
  });
}

test("fault duration and mode validation does not arm invalid plans", async t => {
  const proxy = await createCommitProxy("postgres://fixture@127.0.0.1:5432/db?sslmode=disable");
  t.after(() => proxy.close());
  for (const durationMS of [0, -1, 5001, 1.5, "80", NaN, Infinity, undefined]) {
    assert.throws(() => proxy.arm({ mode: "delay", durationMS }), /invalid_commit_fault/);
    assert.equal(proxy.evidence().armed, false);
  }
  for (const plan of [null, [], { mode: "drop", direction: "request" }, { mode: "delay", durationMS: 80, direction: "both" },
    { mode: "delay", durationMS: 80, jitter: true }, { mode: "reset", durationMS: 80 }]) assert.throws(() => proxy.arm(plan), /invalid_commit_fault/);
  const plan = { mode: "delay", durationMS: 80 };
  proxy.arm(plan); plan.durationMS = 0;
  assert.throws(() => proxy.arm(), /already_armed/);
  assert.equal(proxy.evidence().faults_fired, 0);
});

async function bandwidthFixture(t, encrypted, response) {
  const peers = new Set();
  const input = Buffer.concat([startup, frame("Q", Buffer.from("SELECT '" + "a".repeat(4096) + "'\0"))]);
  const received = [];
  const track = socket => { peers.add(socket); socket.on("error", () => {}); socket.on("close", () => peers.delete(socket)); };
  const serve = socket => {
    let bytes = Buffer.alloc(0);
    let sent = false;
    socket.on("data", chunk => {
      bytes = Buffer.concat([bytes, chunk]);
      if (!sent && bytes.length === input.length) { sent = true; received.push(bytes); socket.write(response); }
    });
  };
  const context = tls.createSecureContext(identity);
  const server = net.createServer(socket => {
    track(socket);
    if (encrypted) acceptPostgresTLS(socket, context, track).then(serve).catch(() => socket.destroy()); else serve(socket);
  });
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  const proxy = await createCommitProxy(`postgres://fixture@127.0.0.1:${server.address().port}/fixture?sslmode=${encrypted ? "verify-full" : "disable"}`, encrypted ? { tls: identity } : {});
  t.after(async () => { await proxy.close(); for (const socket of peers) socket.destroy(); await new Promise(resolve => server.close(resolve)); });
  async function connect() {
    const destination = new URL(proxy.connectionString);
    const client = encrypted ? await connectPostgresTLS(destination, identity, socket => socket.on("error", () => {})) : net.connect(Number(destination.port), destination.hostname);
    client.on("error", () => {});
    if (!encrypted) await new Promise(resolve => client.once("connect", resolve));
    return client;
  }
  return { proxy, connect, input, received };
}

for (const encrypted of [false, true]) for (const direction of ["request", "response"]) {
  test(`${encrypted ? "TLS" : "plaintext"} bandwidth ${direction} preserves exact bytes and enforces rate`, { timeout: 4000 }, async t => {
    const reply = Buffer.concat([frame("D", Buffer.from("x".repeat(4096))), ready]);
    const { proxy, connect, input, received } = await bandwidthFixture(t, encrypted, reply);
    proxy.limitBandwidth({ direction, bytesPerSecond: 32768 });
    const client = await connect();
    const chunks = [];
    client.on("data", chunk => chunks.push(chunk));
    client.write(input);
    await until(() => Buffer.concat(chunks).length === reply.length);
    const record = proxy.evidence().bandwidth[0];
    assert.deepEqual(Buffer.concat(chunks), reply); assert.deepEqual(received, [input]);
    assert.equal(record.bytes_forwarded, direction === "request" ? input.length : reply.length);
    assert.ok(record.elapsed_ms >= record.bytes_forwarded / record.bytes_per_second * 1000);
    proxy.clearBandwidth(direction);
    client.destroy(); await proxy.close();
    const evidence = proxy.evidence();
    assert.equal(evidence.bandwidth_pending_transfers, 0); assert.equal(evidence.bandwidth_timers, 0);
    assert.equal(evidence.bandwidth_buffered_bytes, 0); assert.equal(evidence.connections, 0);
    assert.equal(evidence.invalid_frames, 0);
  });
}

test("concurrent TLS reads share one aggregate response bandwidth budget", { timeout: 4000 }, async t => {
  const reply = Buffer.concat([frame("D", Buffer.from("x".repeat(4096))), ready]);
  const { proxy, connect, input } = await bandwidthFixture(t, true, reply);
  const clients = await Promise.all([connect(), connect()]);
  proxy.limitBandwidth({ direction: "response", bytesPerSecond: 32768 });
  await Promise.all(clients.map(client => new Promise(resolve => {
    let bytes = 0;
    client.on("data", chunk => { bytes += chunk.length; if (bytes === reply.length) resolve(); });
    client.write(input);
  })));
  const record = proxy.evidence().bandwidth[0];
  assert.equal(record.bytes_forwarded, reply.length * 2);
  assert.ok(record.elapsed_ms >= record.bytes_forwarded / 32768 * 1000);
  proxy.clearBandwidth("response");
  for (const client of clients) client.destroy();
  await proxy.close();
  assert.equal(proxy.evidence().bandwidth_buffered_bytes, 0);
});

for (const action of ["clear", "client-close", "proxy-close"]) {
  test(`TLS bandwidth ${action} joins paced chunks without corrupting or leaking data`, { timeout: 4000 }, async t => {
    const reply = Buffer.concat([frame("D", Buffer.from("x".repeat(8192))), ready]);
    const { proxy, connect, input } = await bandwidthFixture(t, true, reply);
    const client = await connect();
    const chunks = [];
    client.on("data", chunk => chunks.push(chunk));
    const closed = new Promise(resolve => client.once("close", resolve));
    proxy.limitBandwidth({ direction: "response", bytesPerSecond: 4096 });
    client.write(input);
    await until(() => proxy.evidence().bandwidth[0].bytes_forwarded >= 80);
    if (action === "clear") {
      proxy.clearBandwidth("response");
      await until(() => Buffer.concat(chunks).length === reply.length);
      assert.deepEqual(Buffer.concat(chunks), reply); client.destroy();
    } else if (action === "client-close") client.destroy(); else await proxy.close();
    await closed; await proxy.close();
    const evidence = proxy.evidence();
    assert.ok(evidence.bandwidth[0].bytes_forwarded > 0 && evidence.bandwidth[0].bytes_forwarded < reply.length);
    if (action !== "clear") assert.equal(evidence.bandwidth[0].canceled_transfers, 1);
    assert.equal(evidence.bandwidth_active_limits, 0); assert.equal(evidence.bandwidth_pending_transfers, 0);
    assert.equal(evidence.bandwidth_timers, 0); assert.equal(evidence.bandwidth_buffered_bytes, 0);
    assert.equal(evidence.connections, 0);
  });
}

test("bandwidth limits reject invalid rates, directions and duplicate controls", async t => {
  const proxy = await createCommitProxy("postgres://fixture@127.0.0.1:5432/db?sslmode=disable");
  t.after(() => proxy.close());
  for (const bytesPerSecond of [0, -1, 1023, 16777217, 1.5, "4096", NaN, undefined]) assert.throws(() => proxy.limitBandwidth({ direction: "response", bytesPerSecond }), /invalid_bandwidth_limit/);
  assert.throws(() => proxy.limitBandwidth({ direction: "both", bytesPerSecond: 4096 }), /invalid_bandwidth_limit/);
  proxy.limitBandwidth({ direction: "request", bytesPerSecond: 4096 });
  assert.throws(() => proxy.limitBandwidth({ direction: "request", bytesPerSecond: 8192 }), /already_limited/);
  assert.throws(() => proxy.clearBandwidth("both"), /invalid_bandwidth_direction/);
  proxy.clearBandwidth("request"); proxy.clearBandwidth("request");
  await proxy.close();
  assert.throws(() => proxy.limitBandwidth({ direction: "response", bytesPerSecond: 4096 }), /commit_proxy_closed/);
});

for (const encrypted of [false, true]) for (const afterBytes of [1, 5, 2048, 4100]) {
  test(`${encrypted ? "TLS" : "plaintext"} read cut flushes exactly ${afterBytes} row bytes and fires once`, { timeout: 4000 }, async t => {
    const row = frame("D", Buffer.from("x".repeat(4096)));
    const reply = Buffer.concat([row, ready]);
    const { proxy, connect, input } = await bandwidthFixture(t, encrypted, reply);
    proxy.armReadCut({ afterBytes });
    const client = await connect();
    const chunks = [];
    client.on("data", chunk => chunks.push(chunk));
    const closed = new Promise(resolve => client.once("close", resolve));
    client.write(input); await closed;
    assert.deepEqual(Buffer.concat(chunks), row.subarray(0, afterBytes));
    const cut = proxy.evidence().read_cuts[0];
    assert.equal(cut.frame_bytes, row.length); assert.equal(cut.after_bytes, afterBytes);
    assert.equal(cut.prefix_flushed_bytes, afterBytes); assert.equal(cut.outcome, "cut");
    assert.equal(proxy.evidence().read_cuts_fired, 1);
    // A fresh stream receives the full response; the fault is one-shot.
    const replacement = await connect();
    const fresh = [];
    replacement.on("data", chunk => fresh.push(chunk)); replacement.write(input);
    await until(() => Buffer.concat(fresh).length === reply.length);
    assert.deepEqual(Buffer.concat(fresh), reply); replacement.destroy(); await proxy.close();
    assert.equal(proxy.evidence().read_cuts_fired, 1); assert.equal(proxy.evidence().active_read_cuts, 0);
    assert.equal(proxy.evidence().connections, 0); assert.equal(proxy.evidence().invalid_frames, 0);
  });
}

for (const action of ["clear", "client-close", "proxy-close"]) {
  test(`TLS read cut ${action} during paced prefix avoids false fault evidence`, { timeout: 4000 }, async t => {
    const reply = Buffer.concat([frame("D", Buffer.from("x".repeat(8192))), ready]);
    const { proxy, connect, input } = await bandwidthFixture(t, true, reply);
    const client = await connect();
    const chunks = []; client.on("data", chunk => chunks.push(chunk));
    const closed = new Promise(resolve => client.once("close", resolve));
    proxy.limitBandwidth({ direction: "response", bytesPerSecond: 4096 });
    proxy.armReadCut({ afterBytes: 4096 }); client.write(input);
    await until(() => proxy.evidence().bandwidth[0].bytes_forwarded >= 80);
    if (action === "clear") {
      proxy.clearReadCut(); proxy.clearBandwidth("response");
      await until(() => Buffer.concat(chunks).length === reply.length);
      assert.deepEqual(Buffer.concat(chunks), reply); client.destroy();
    } else if (action === "client-close") client.destroy(); else await proxy.close();
    await closed; await proxy.close();
    const evidence = proxy.evidence();
    assert.equal(evidence.read_cuts[0].outcome, action === "clear" ? "cleared" : "canceled");
    assert.equal(evidence.read_cuts_fired, 0); assert.equal(evidence.active_read_cuts, 0);
    assert.equal(evidence.bandwidth_timers, 0); assert.equal(evidence.bandwidth_pending_transfers, 0);
    assert.equal(evidence.bandwidth_buffered_bytes, 0); assert.equal(evidence.connections, 0);
  });
}

test("unreached and cleared read-cut thresholds do not interrupt short reads", { timeout: 4000 }, async t => {
  const reply = Buffer.concat([frame("D", Buffer.from("short")), ready]);
  const { proxy, connect, input } = await bandwidthFixture(t, true, reply);
  proxy.armReadCut({ afterBytes: 32768 });
  const client = await connect();
  const chunks = []; client.on("data", chunk => chunks.push(chunk)); client.write(input);
  await until(() => Buffer.concat(chunks).length === reply.length);
  assert.deepEqual(Buffer.concat(chunks), reply); assert.equal(proxy.evidence().read_cut_armed, true);
  assert.equal(proxy.evidence().read_cuts_fired, 0);
  proxy.clearReadCut(); client.destroy(); await proxy.close();
  assert.equal(proxy.evidence().read_cuts[0].outcome, "cleared");
});

test("read cut validates offsets and rejects duplicate arming", async t => {
  const proxy = await createCommitProxy("postgres://fixture@127.0.0.1:5432/db?sslmode=disable");
  t.after(() => proxy.close());
  for (const afterBytes of [0, -1, 1048576, 1.5, "4096", NaN, undefined]) assert.throws(() => proxy.armReadCut({ afterBytes }), /invalid_read_cut/);
  assert.throws(() => proxy.armReadCut({ afterBytes: 4096, direction: "request" }), /invalid_read_cut/);
  const plan = { afterBytes: 4096 }; proxy.armReadCut(plan); plan.afterBytes = 0;
  assert.throws(() => proxy.armReadCut({ afterBytes: 1 }), /already_armed/);
  assert.equal(proxy.evidence().read_cuts[0].after_bytes, 4096);
  proxy.clearReadCut(); proxy.clearReadCut(); await proxy.close();
  assert.throws(() => proxy.armReadCut({ afterBytes: 1 }), /commit_proxy_closed/);
});

test("one armed TLS row cut affects exactly one concurrent connection", { timeout: 4000 }, async t => {
  const row = frame("D", Buffer.from("x".repeat(4096)));
  const reply = Buffer.concat([row, ready]);
  const { proxy, connect, input } = await bandwidthFixture(t, true, reply);
  const clients = await Promise.all([connect(), connect()]);
  proxy.armReadCut({ afterBytes: 2048 });
  const results = await Promise.all(clients.map(client => new Promise(resolve => {
    const chunks = [];
    client.on("data", chunk => { chunks.push(chunk); if (Buffer.concat(chunks).length === reply.length) client.destroy(); });
    client.once("close", () => resolve(Buffer.concat(chunks)));
    client.write(input);
  })));
  results.sort((left, right) => left.length - right.length);
  assert.deepEqual(results[0], row.subarray(0, 2048)); assert.deepEqual(results[1], reply);
  assert.equal(proxy.evidence().read_cuts_fired, 1); await proxy.close();
  assert.equal(proxy.evidence().active_read_cuts, 0); assert.equal(proxy.evidence().connections, 0);
});

for (const action of ["expire", "clear", "client-close", "proxy-close"]) {
  test(`SSLRequest stall ${action} proves phase, bounds and joined negotiation`, { timeout: 4000 }, async t => {
    const reply = Buffer.concat([frame("D", Buffer.from("ready")), ready]);
    const { proxy, connect, input, received } = await bandwidthFixture(t, true, reply);
    proxy.stallConnections({ phase: "ssl_request", durationMS: action === "expire" ? 80 : 1000 });
    const destination = new URL(proxy.connectionString);
    // A fragmented SSLRequest must be recognized, without an early S reply.
    const client = net.connect(Number(destination.port), destination.hostname);
    client.on("error", () => {});
    const chunks = []; client.on("data", chunk => chunks.push(chunk));
    const closed = new Promise(resolve => client.once("close", resolve));
    await new Promise(resolve => client.once("connect", resolve));
    const sslRequest = Buffer.from([0, 0, 0, 8, 4, 210, 22, 47]);
    client.write(sslRequest.subarray(0, 3)); client.write(sslRequest.subarray(3));
    await until(() => proxy.evidence().active_connection_stalls === 1);
    assert.deepEqual(Buffer.concat(chunks), Buffer.alloc(0)); assert.deepEqual(received, []);
    assert.equal(proxy.evidence().tls_connections_verified, 0); assert.equal(proxy.evidence().ssl_requests_seen, 1);
    if (action === "clear") {
      proxy.clearConnectionStall();
      await until(() => Buffer.concat(chunks).length === 1);
      assert.deepEqual(Buffer.concat(chunks), Buffer.from("S")); client.destroy();
    } else if (action === "client-close") client.destroy(); else if (action === "proxy-close") await proxy.close();
    await closed;
    await until(() => proxy.evidence().pending_connections === 0 && proxy.evidence().connections === 0);
    const outcome = action === "expire" ? "timed_out" : action === "clear" ? "released" : "canceled";
    const record = proxy.evidence().connection_stalls[0];
    assert.equal(record.phase, "ssl_request"); assert.equal(record.leg, "client"); assert.equal(record.outcome, outcome);
    if (action === "expire") assert.ok(record.elapsed_ms >= 80);
    assert.equal(proxy.evidence().active_connection_stalls, 0); assert.equal(proxy.evidence().connection_stall_timers, 0);
    if (action !== "proxy-close") {
      proxy.clearConnectionStall();
      const healthy = await connect();
      const output = []; healthy.on("data", chunk => output.push(chunk)); healthy.write(input);
      await until(() => Buffer.concat(output).length === reply.length);
      assert.deepEqual(Buffer.concat(output), reply); healthy.destroy();
      assert.equal(proxy.evidence().tls_connections_verified, 1);
      assert.equal(proxy.evidence().connection_stalls_fired, 1);
    }
    await proxy.close(); assert.equal(proxy.evidence().pending_connections, 0);
  });
}

test("stall release still rejects an untrusted TLS client certificate chain", { timeout: 4000 }, async t => {
  const { proxy } = await bandwidthFixture(t, true, ready);
  proxy.stallConnections({ phase: "ssl_request", durationMS: 1000 });
  const attempt = connectPostgresTLS(new URL(proxy.connectionString), { ...identity, ca: tls.rootCertificates }, socket => socket.on("error", () => {}));
  const rejected = assert.rejects(attempt, /postgres_tls_verification_failed/);
  await until(() => proxy.evidence().active_connection_stalls === 1);
  proxy.clearConnectionStall(); await rejected;
  await until(() => proxy.evidence().connections === 0 && proxy.evidence().pending_connections === 0);
  assert.equal(proxy.evidence().connection_stalls[0].outcome, "released");
  assert.equal(proxy.evidence().tls_connections_verified, 0); assert.equal(proxy.evidence().commits_forwarded, 0);
});

test("connection stalls require TLS and validate configuration without activating a rule", async t => {
  const { proxy } = await bandwidthFixture(t, true, ready);
  for (const durationMS of [0, -1, 5001, 1.5, "1000", NaN, undefined]) assert.throws(() => proxy.stallConnections({ phase: "ssl_request", durationMS }), /invalid_connection_stall/);
  assert.throws(() => proxy.stallConnections({ phase: "certificate", durationMS: 80 }), /invalid_connection_stall/);
  assert.equal(proxy.evidence().connection_stall_enabled, false);
  proxy.stallConnections({ phase: "ssl_request", durationMS: 80 });
  assert.throws(() => proxy.stallConnections({ phase: "ssl_request", durationMS: 80 }), /already_enabled/);
  proxy.clearConnectionStall(); proxy.clearConnectionStall(); await proxy.close();
  assert.throws(() => proxy.stallConnections({ phase: "ssl_request", durationMS: 80 }), /commit_proxy_closed/);
  const plaintext = await createCommitProxy("postgres://fixture@127.0.0.1:5432/db?sslmode=disable");
  t.after(() => plaintext.close());
  assert.throws(() => plaintext.stallConnections({ phase: "ssl_request", durationMS: 80 }), /requires_tls/);
});

test("malformed SSLRequest never creates positive stall evidence or enables plaintext", { timeout: 4000 }, async t => {
  const { proxy, received } = await bandwidthFixture(t, true, ready);
  proxy.stallConnections({ phase: "ssl_request", durationMS: 1000 });
  const destination = new URL(proxy.connectionString);
  const client = net.connect(Number(destination.port), destination.hostname);
  client.on("error", () => {});
  const output = []; client.on("data", chunk => output.push(chunk));
  const closed = new Promise(resolve => client.once("close", resolve));
  client.once("connect", () => client.write(startup)); await closed;
  await until(() => proxy.evidence().pending_connections === 0);
  assert.deepEqual(Buffer.concat(output), Buffer.alloc(0)); assert.deepEqual(received, []);
  assert.equal(proxy.evidence().connection_stalls_fired, 0); assert.equal(proxy.evidence().ssl_requests_seen, 0);
  assert.equal(proxy.evidence().tls_connections_verified, 0); assert.equal(proxy.evidence().connection_stall_timers, 0);
});

for (const action of ["expire", "clear", "client-close", "proxy-close"]) {
  test(`verified TLS authentication stall ${action} withholds only the initial challenge`, { timeout: 4000 }, async t => {
    const peers = new Set();
    const challengeBody = Buffer.alloc(4); challengeBody.writeInt32BE(3);
    const challenge = frame("R", challengeBody);
    const track = socket => { peers.add(socket); socket.on("error", () => {}); socket.on("close", () => peers.delete(socket)); };
    const context = tls.createSecureContext(identity);
    let frontendBytes = 0;
    const server = net.createServer(socket => {
      track(socket);
      acceptPostgresTLS(socket, context, track).then(encrypted => encrypted.on("data", chunk => {
        frontendBytes += chunk.length;
        if (frontendBytes === startup.length) encrypted.write(challenge);
      })).catch(() => socket.destroy());
    });
    await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
    const proxy = await createCommitProxy(`postgres://fixture@127.0.0.1:${server.address().port}/fixture?sslmode=verify-full`, { tls: identity });
    t.after(async () => { await proxy.close(); for (const socket of peers) socket.destroy(); await new Promise(resolve => server.close(resolve)); });
    proxy.stallConnections({ phase: "authentication", durationMS: action === "expire" ? 80 : 1000 });
    const client = await connectPostgresTLS(new URL(proxy.connectionString), identity, socket => socket.on("error", () => {}));
    const output = []; client.on("data", chunk => output.push(chunk));
    const closed = new Promise(resolve => client.once("close", resolve)); client.write(startup);
    await until(() => proxy.evidence().active_connection_stalls === 1);
    assert.equal(proxy.evidence().tls_connections_verified, 1); assert.equal(proxy.evidence().pending_authentication, 1);
    assert.equal(proxy.evidence().authentication_challenges_seen, 1);
    assert.deepEqual(Buffer.concat(output), Buffer.alloc(0)); assert.equal(frontendBytes, startup.length);
    if (action === "clear") {
      proxy.clearConnectionStall();
      await until(() => Buffer.concat(output).length === challenge.length);
      assert.deepEqual(Buffer.concat(output), challenge); client.destroy();
    } else if (action === "client-close") client.destroy(); else if (action === "proxy-close") await proxy.close();
    await closed; await proxy.close();
    const evidence = proxy.evidence();
    assert.equal(evidence.connection_stalls[0].phase, "authentication");
    assert.equal(evidence.connection_stalls[0].outcome, action === "expire" ? "timed_out" : action === "clear" ? "released" : "canceled");
    assert.equal(evidence.connection_stalls_fired, 1); assert.equal(evidence.pending_authentication, 0);
    assert.equal(evidence.active_connection_stalls, 0); assert.equal(evidence.connection_stall_timers, 0);
    assert.equal(evidence.pending_connections, 0); assert.equal(evidence.connections, 0);
    assert.equal(frontendBytes, startup.length); // No password response is sent or captured.
  });
}

for (const replyMode of ["already-authenticated", "rejected", "malformed", "malformed-md5", "malformed-sasl"]) {
  test(`authentication stall ${replyMode} does not fabricate challenge evidence`, { timeout: 4000 }, async t => {
    const authOK = frame("R", Buffer.alloc(4));
    const badCode = Buffer.alloc(4); badCode.writeInt32BE(replyMode === "malformed-md5" ? 5 : 10);
    const malformed = replyMode.startsWith("malformed");
    const reply = replyMode === "already-authenticated" ? Buffer.concat([authOK, ready]) : replyMode === "rejected" ?
      Buffer.concat([frame("E", Buffer.from("Mfixture rejected\0\0")), ready]) : frame("R", replyMode === "malformed" ? Buffer.alloc(2) : badCode);
    const { proxy, connect, input } = await bandwidthFixture(t, true, reply);
    proxy.stallConnections({ phase: "authentication", durationMS: 1000 });
    const client = await connect();
    const output = []; client.on("data", chunk => output.push(chunk));
    const closed = new Promise(resolve => client.once("close", resolve)); client.write(input);
    if (!malformed) {
      await until(() => Buffer.concat(output).length === reply.length);
      assert.deepEqual(Buffer.concat(output), reply); client.destroy();
    }
    await closed; await proxy.close();
    assert.equal(proxy.evidence().connection_stalls_fired, 0); assert.equal(proxy.evidence().authentication_challenges_seen, 0);
    assert.equal(proxy.evidence().pending_authentication, 0);
    assert.equal(proxy.evidence().invalid_frames, malformed ? 1 : 0);
  });
}
