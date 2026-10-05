import assert from "node:assert/strict";
import { mkdir, mkdtemp, readFile, rename, rm, writeFile } from "node:fs/promises";
import { fork } from "node:child_process";
import { once } from "node:events";
import { createServer } from "node:http";
import { setTimeout as delay } from "node:timers/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import {
  applyLatestSecretSnapshot,
  applyInitialSecretSnapshot,
  markSecretReloadReady,
  postSecretAck,
  readSecretSnapshot,
} from "./secret-reload.js";

const revision = (digit) => digit.repeat(64);

async function withProjection(t, initialRevision = revision("a")) {
  const dir = await mkdtemp(path.join(os.tmpdir(), "gregale-secret-reload-"));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const env = {
    FAAS_SECRETS_FILE: path.join(dir, "secrets.json"),
    FAAS_SECRETS_REVISION_FILE: path.join(dir, "revision"),
    FAAS_SECRETS_RELOAD_ACK_ENDPOINT: "http://127.0.0.1/ack",
  };
  await writeFile(env.FAAS_SECRETS_FILE, JSON.stringify({ DATABASE_URL: "postgres://first" }));
  await writeFile(env.FAAS_SECRETS_REVISION_FILE, `${initialRevision}\n`);
  return env;
}

test("snapshot retries when the opaque revision changes during the read", async (t) => {
  const env = await withProjection(t);
  let revisionReads = 0;
  let secretReads = 0;
  const readConsistentFiles = async (file, encoding) => {
    if (file === env.FAAS_SECRETS_REVISION_FILE) {
      revisionReads += 1;
      return revisionReads === 1 ? revision("a") : revision("b");
    }
    secretReads += 1;
    return JSON.stringify({ DATABASE_URL: secretReads === 1 ? "postgres://old" : "postgres://new" });
  };

  const snapshot = await readSecretSnapshot({ env, readFile: readConsistentFiles });
  assert.equal(snapshot.revision, revision("b"));
  assert.equal(snapshot.secrets.DATABASE_URL, "postgres://new");
});

test("ack retries a transient 503 with the exact same non-secret body", async () => {
  const bodies = [];
  let calls = 0;
  const outcome = await postSecretAck({
    revision: revision("c"),
    status: "applied",
    env: { FAAS_SECRETS_RELOAD_ACK_ENDPOINT: "http://local/ack" },
    fetchImpl: async (_url, request) => {
      bodies.push(request.body);
      calls += 1;
      return new Response(null, { status: calls === 1 ? 503 : 202 });
    },
    sleep: async () => {},
  });
  assert.equal(outcome, "accepted");
  assert.equal(calls, 2);
  assert.equal(bodies[0], bodies[1]);
  assert.deepEqual(JSON.parse(bodies[0]), { revision: revision("c"), status: "applied" });
});

test("stale acknowledgement rereads and applies the current projection", async (t) => {
  const env = await withProjection(t);
  const applied = [];
  let acknowledgements = 0;
  const result = await applyLatestSecretSnapshot({
    env,
    apply: async (snapshot) => applied.push(snapshot),
    fetchImpl: async () => {
      acknowledgements += 1;
      if (acknowledgements === 1) {
        await writeFile(env.FAAS_SECRETS_FILE, JSON.stringify({ DATABASE_URL: "postgres://rotated" }));
        await writeFile(env.FAAS_SECRETS_REVISION_FILE, `${revision("b")}\n`);
        return new Response(null, { status: 409 });
      }
      return new Response(null, { status: 202 });
    },
    sleep: async () => {},
  });
  assert.equal(result, revision("b"));
  assert.deepEqual(applied.map((snapshot) => snapshot.revision), [revision("a"), revision("b")]);
  assert.equal(applied[1].secrets.DATABASE_URL, "postgres://rotated");
});

test("failed apply sends only a failed status and does not expose the error", async (t) => {
  const env = await withProjection(t);
  const secretInError = "postgres://user:super-secret@example/db";
  let requestBody;
  await assert.rejects(
    () => applyLatestSecretSnapshot({
      env,
      apply: async () => { throw new Error(secretInError); },
      fetchImpl: async (_url, request) => {
        requestBody = request.body;
        return new Response(null, { status: 202 });
      },
      sleep: async () => {},
    }),
    (error) => {
      assert.match(error.message, /could not be applied/);
      assert.equal(error.message.includes(secretInError), false);
      return true;
    },
  );
  assert.deepEqual(JSON.parse(requestBody), { revision: revision("a"), status: "failed" });
  assert.equal(requestBody.includes(secretInError), false);
});

test("malformed secret input errors are sanitized", async (t) => {
  const env = await withProjection(t);
  await writeFile(env.FAAS_SECRETS_FILE, '{"DATABASE_URL":"postgres://user:super-secret');
  await assert.rejects(
    () => readSecretSnapshot({ env, readFile }),
    (error) => {
      assert.equal(error.message.includes("super-secret"), false);
      return true;
    },
  );
});


test("atomic snapshot binds values to its own revision even when legacy files disagree", async () => {
  const reads = [];
  const env = {
    FAAS_SECRETS_SNAPSHOT_FILE: "/projection/snapshot.json",
    FAAS_SECRETS_FILE: "/projection/secrets.json",
    FAAS_SECRETS_REVISION_FILE: "/projection/revision",
  };
  const snapshot = await readSecretSnapshot({ env, readFile: async (file) => {
    reads.push(file);
    if (file === env.FAAS_SECRETS_SNAPSHOT_FILE) {
      return JSON.stringify({ revision: revision("b"), secrets: { DATABASE_URL: "new" } });
    }
    return file === env.FAAS_SECRETS_REVISION_FILE ? revision("a") : JSON.stringify({ DATABASE_URL: "old" });
  } });
  assert.equal(snapshot.revision, revision("b"));
  assert.deepEqual(snapshot.secrets, { DATABASE_URL: "new" });
  assert.deepEqual(reads, [env.FAAS_SECRETS_SNAPSHOT_FILE]);
  assert.equal(Object.isFrozen(snapshot.secrets), true);
});

test("atomic snapshot retries a retired generation lookup without using legacy paths", async () => {
  let reads = 0;
  const env = { FAAS_SECRETS_SNAPSHOT_FILE: "/snapshot" };
  const snapshot = await readSecretSnapshot({ env, readFile: async (file) => {
    assert.equal(file, "/snapshot");
    reads += 1;
    if (reads < 3) throw Object.assign(new Error("private error"), { code: "ENOENT" });
    return JSON.stringify({ revision: revision("c"), secrets: {} });
  } });
  assert.equal(reads, 3);
  assert.equal(snapshot.revision, revision("c"));
  assert.deepEqual(snapshot.secrets, {});
});

test("advertised atomic snapshots fail closed and sanitize every invalid input", async () => {
  const invalid = [
    '{"private-secret-value":', null, [],
    { revision: "", secrets: {} },
    { revision: "g".repeat(64), secrets: {} },
    { revision: revision("a"), secrets: null },
    { revision: revision("a"), secrets: [] },
    { revision: revision("a"), secrets: { DATABASE_URL: 42 } },
    { revision: revision("a") },
  ];
  for (const value of invalid) {
    await assert.rejects(() => readSecretSnapshot({
      env: { FAAS_SECRETS_SNAPSHOT_FILE: "/snapshot", FAAS_SECRETS_FILE: "/legacy", FAAS_SECRETS_REVISION_FILE: "/revision" },
      readFile: async (file) => {
        assert.equal(file, "/snapshot");
        return typeof value === "string" ? value : JSON.stringify(value);
      },
    }), (error) => !error.message.includes("private-secret-value"));
  }
  for (const code of ["ENOENT", "EACCES"]) {
    let reads = 0;
    await assert.rejects(() => readSecretSnapshot({
      env: { FAAS_SECRETS_SNAPSHOT_FILE: "/snapshot", FAAS_SECRETS_FILE: "/legacy", FAAS_SECRETS_REVISION_FILE: "/revision" },
      readFile: async (file) => {
        assert.equal(file, "/snapshot");
        reads += 1;
        throw Object.assign(new Error("private-secret-value"), { code });
      },
    }), (error) => error.message === "secret snapshot could not be read");
    assert.equal(reads, code === "ENOENT" ? 5 : 1);
  }
  for (const value of ["", null, 42]) {
    await assert.rejects(() => readSecretSnapshot({ env: { FAAS_SECRETS_SNAPSHOT_FILE: value } }));
  }
});

test("atomic snapshots stay paired during rapid replacement and revocation", async (t) => {
  const dir = await mkdtemp(path.join(os.tmpdir(), "gregale-atomic-secret-"));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const env = { FAAS_SECRETS_SNAPSHOT_FILE: path.join(dir, "snapshot.json") };
  async function publish(index) {
    const id = index.toString(16).padStart(64, "0");
    const secrets = index % 2 === 0 ? {} : { DATABASE_URL: id };
    const staged = path.join(dir, "next.json");
    await writeFile(staged, JSON.stringify({ revision: id, secrets }));
    await rename(staged, env.FAAS_SECRETS_SNAPSHOT_FILE);
  }
  await publish(0);
  let reads = 0;
  await Promise.all([
    (async () => { for (let index = 1; index <= 100; index += 1) await publish(index); })(),
    (async () => {
      for (let index = 0; index < 200; index += 1) {
        const snapshot = await readSecretSnapshot({ env });
        const id = Number.parseInt(snapshot.revision, 16);
        assert.deepEqual(snapshot.secrets, id % 2 === 0 ? {} : { DATABASE_URL: snapshot.revision });
        reads += 1;
      }
    })(),
  ]);
  assert.equal(reads, 200);
});

test("application acknowledgements use the revision from the atomic envelope", async () => {
  const applied = [];
  const acknowledgements = [];
  const result = await applyLatestSecretSnapshot({
    env: { FAAS_SECRETS_SNAPSHOT_FILE: "/snapshot", FAAS_SECRETS_RELOAD_ACK_ENDPOINT: "http://local/ack" },
    readFile: async () => JSON.stringify({ revision: revision("d"), secrets: {} }),
    apply: async (snapshot) => applied.push(snapshot),
    fetchImpl: async (_url, request) => { acknowledgements.push(JSON.parse(request.body)); return new Response(null, { status: 202 }); },
  });
  assert.equal(result, revision("d"));
  assert.deepEqual(applied, [{ revision: revision("d"), secrets: {} }]);
  assert.deepEqual(acknowledgements, [{ revision: revision("d"), status: "applied" }]);
});


test("ready marker is generation scoped and does not acknowledge application state", async (t) => {
  const dir = await mkdtemp(path.join(os.tmpdir(), "gregale-secret-ready-"));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const first = path.join(dir, "first");
  const second = path.join(dir, "second");
  await writeFile(first, "");
  await writeFile(second, "");
  await markSecretReloadReady({ env: { FAAS_SECRETS_RELOAD_READY_FILE: first } });
  assert.equal(await readFile(first, "utf8"), "ready\n");
  assert.equal(await readFile(second, "utf8"), "");
  await markSecretReloadReady({ env: {}, writeFile: () => { throw new Error("must not write on old guests"); } });
});

test("ready marker errors are sanitized and an invalid advertised path fails closed", async () => {
  await assert.rejects(markSecretReloadReady({ env: { FAAS_SECRETS_RELOAD_READY_FILE: "" } }), /readiness is unavailable/);
  await assert.rejects(markSecretReloadReady({ env: { FAAS_SECRETS_RELOAD_READY_FILE: "/marker" }, writeFile: () => { throw new Error("secret-value"); } }), (error) => {
    assert.equal(error.message, "secret reload readiness could not be confirmed");
    assert.ok(!error.message.includes("secret-value"));
    return true;
  });
});

// adr:437 — Bootstrap may wait only for the platform's empty initial revision.
const bootstrapEnv = {
  FAAS_SECRETS_SNAPSHOT_FILE: "/snapshot",
  FAAS_SECRETS_RELOAD_ACK_ENDPOINT: "http://local/ack",
};
const envelope = (version = revision("a"), secrets = {}) => JSON.stringify({ revision: version, secrets });

test("bootstrap recovers from pending publication with capped backoff and no premature apply or ACK", async () => {
  const sleeps = [];
  const applied = [];
  const acks = [];
  let reads = 0;
  const result = await applyInitialSecretSnapshot({
    env: bootstrapEnv,
    readFile: async () => ++reads <= 8 ? envelope("", { DATABASE_URL: "unversioned" }) : envelope(revision("b"), { DATABASE_URL: "current" }),
    sleep: async (ms) => {
      assert.deepEqual(applied, []);
      assert.deepEqual(acks, []);
      sleeps.push(ms);
    },
    apply: async (snapshot) => applied.push(snapshot),
    fetchImpl: async (_url, request) => { acks.push(JSON.parse(request.body)); return new Response(null, { status: 202 }); },
  });
  assert.equal(result, revision("b"));
  assert.deepEqual(sleeps, [100, 200, 400, 800, 1600, 2000, 2000, 2000]);
  assert.deepEqual(applied, [{ revision: revision("b"), secrets: { DATABASE_URL: "current" } }]);
  assert.deepEqual(acks, [{ revision: revision("b"), status: "applied" }]);
});

test("bootstrap preserves legacy revision consistency and validates pending secret maps", async () => {
  const env = { FAAS_SECRETS_FILE: "/secrets", FAAS_SECRETS_REVISION_FILE: "/revision", FAAS_SECRETS_RELOAD_ACK_ENDPOINT: "http://local/ack" };
  let pending = true;
  const applied = [];
  await applyInitialSecretSnapshot({
    env,
    readFile: async (file) => file === "/revision" ? pending ? "\n" : revision("b") : JSON.stringify({ DATABASE_URL: pending ? "initial" : "current" }),
    sleep: async () => { pending = false; },
    apply: async (snapshot) => applied.push(snapshot),
    fetchImpl: async () => new Response(null, { status: 202 }),
  });
  assert.deepEqual(applied, [{ revision: revision("b"), secrets: { DATABASE_URL: "current" } }]);
  await assert.rejects(applyInitialSecretSnapshot({
    env,
    readFile: async (file) => file === "/revision" ? "" : "[]",
    apply: () => assert.fail("must not apply malformed pending secrets"),
    sleep: () => assert.fail("must not wait for malformed pending secrets"),
  }), /secret projection is invalid/);
});

test("bootstrap rejects malformed and unreadable snapshots without waiting or legacy fallback", async () => {
  for (const raw of ["private-secret-value", "null", "[]", envelope(null), envelope("wrong"), envelope("", null), envelope("", []), envelope("", { DATABASE_URL: 42 })]) {
    await assert.rejects(applyInitialSecretSnapshot({
      env: { ...bootstrapEnv, FAAS_SECRETS_FILE: "/legacy", FAAS_SECRETS_REVISION_FILE: "/revision" },
      readFile: async (file) => { assert.equal(file, "/snapshot"); return raw; },
      apply: () => assert.fail("must not apply invalid data"),
      sleep: () => assert.fail("must not retry invalid data"),
      fetchImpl: () => assert.fail("must not ACK invalid data"),
    }), (error) => !error.message.includes("private-secret-value"));
  }
  for (const code of ["ENOENT", "EACCES"]) {
    let reads = 0;
    await assert.rejects(applyInitialSecretSnapshot({
      env: bootstrapEnv,
      readFile: async () => { reads++; throw Object.assign(new Error("private-secret-value"), { code }); },
      apply: () => assert.fail("must not apply unreadable data"),
      sleep: () => assert.fail("must not wait on read errors"),
    }), (error) => error.message === "secret snapshot could not be read");
    assert.equal(reads, code === "ENOENT" ? 5 : 1);
  }
});

test("bootstrap deadline bounds pending publication and a stalled read", async (t) => {
  for (const stalled of [false, true]) {
    await t.test(stalled ? "stalled read" : "permanent pending revision", async () => {
      await assert.rejects(applyInitialSecretSnapshot({
        env: bootstrapEnv,
        startupTimeoutMs: 30,
        readFile: async () => stalled ? new Promise(() => {}) : envelope(""),
        apply: () => assert.fail("must not apply before a version exists"),
        fetchImpl: () => assert.fail("must not acknowledge an empty revision"),
      }), (error) => error.message === "initial secret revision was not available before the deadline");
    });
  }
});

test("shutdown cancels bootstrap read, wait, application, ACK and ACK backoff", async (t) => {
  for (const phase of ["read", "wait", "apply", "ack", "ack backoff"]) {
    await t.test(phase, async () => {
      const controller = new AbortController();
      let entered;
      const started = new Promise((resolve) => { entered = resolve; });
      let finish;
      const blocked = new Promise((resolve) => { finish = resolve; });
      const block = () => { entered(); return blocked; };
      let ackCalls = 0;
      let applyCalls = 0;
      const operation = applyInitialSecretSnapshot({
        env: bootstrapEnv,
        signal: controller.signal,
        readFile: async () => phase === "read" ? block() : envelope(phase === "wait" ? "" : revision("a")),
        sleep: block,
        apply: async () => { applyCalls++; if (phase === "apply") return block(); },
        fetchImpl: async () => { ackCalls++; return phase === "ack" ? block() : new Response(null, { status: 503 }); },
      });
      await started;
      controller.abort(new Error("private-secret-value"));
      await assert.rejects(operation, (error) => error.message === "secret reload cancelled");
      finish(phase === "read" ? envelope() : new Response(null, { status: 202 }));
      await delay(0); // Late completion cannot advance the cancelled operation.
      assert.equal(applyCalls, ["read", "wait"].includes(phase) ? 0 : 1);
      assert.equal(ackCalls, ["ack", "ack backoff"].includes(phase) ? 1 : 0);
    });
  }
  const controller = new AbortController();
  controller.abort();
  await assert.rejects(applyInitialSecretSnapshot({ signal: controller.signal, apply: () => assert.fail(), readFile: () => assert.fail() }), /cancelled/);
});

test("bootstrap follows rotation and stale ACKs but never accepts another empty revision", async () => {
  for (const resetToEmpty of [false, true]) {
    let raw = envelope("");
    const applied = [];
    const acks = [];
    const operation = applyInitialSecretSnapshot({
      env: bootstrapEnv,
      readFile: async () => raw,
      sleep: async () => { raw = envelope(revision("a")); },
      apply: async (snapshot) => applied.push(snapshot.revision),
      fetchImpl: async (_url, request) => {
        acks.push(JSON.parse(request.body));
        if (acks.length === 1) {
          raw = envelope(resetToEmpty ? "" : revision("b"));
          return new Response(null, { status: 409 });
        }
        return new Response(null, { status: 202 });
      },
    });
    if (resetToEmpty) {
      await assert.rejects(operation, /secret snapshot is invalid/);
      assert.deepEqual(applied, [revision("a")]);
    } else {
      assert.equal(await operation, revision("b"));
      assert.deepEqual(applied, [revision("a"), revision("b")]);
    }
    assert.ok(acks.every((ack) => ack.revision !== ""));
  }
  await assert.rejects(applyLatestSecretSnapshot({ env: bootstrapEnv, readFile: async () => envelope(""), apply: () => assert.fail() }), /secret snapshot is invalid/);
});

test("initial availability deadline is retired before application and ACK", async () => {
  assert.equal(await applyInitialSecretSnapshot({
    env: bootstrapEnv,
    startupTimeoutMs: 10,
    readFile: async () => envelope(),
    apply: async () => delay(30),
    fetchImpl: async () => new Response(null, { status: 202 }),
  }), revision("a"));
  for (const startupTimeoutMs of [0, -1, NaN, Infinity, "30", 2_147_483_648]) {
    await assert.rejects(applyInitialSecretSnapshot({ startupTimeoutMs, apply: () => assert.fail() }), /deadline is invalid/);
  }
});

async function waitUntil(predicate) {
  for (let attempt = 0; attempt < 300; attempt++) {
    if (await predicate()) return;
    await delay(10);
  }
  assert.fail("starter did not reach the expected state");
}

// Exercise the production handler without requiring an external PostgreSQL
// server. The HTTP listener and ACK transport are real; only DB work is held.
async function startStarter(t, initial = envelope(""), { holdAcks = false } = {}) {
  const dir = await mkdtemp(path.join(os.tmpdir(), "gregale-secret-startup-"));
  t.after(() => rm(dir, { recursive: true, force: true }));
  for (const file of ["handler.js", "secret-reload.js"]) {
    await writeFile(path.join(dir, file), await readFile(new URL(file, import.meta.url)));
  }
  await writeFile(path.join(dir, "package.json"), '{"type":"module"}');
  const stubs = {
    express: `import { createServer } from "node:http";
      export default function () { return { get() {}, listen(port, ready) {
        const server = createServer((_req, res) => res.end("ready"));
        server.listen(port, "127.0.0.1", () => { process.send({ type: "listening", port: server.address().port }); ready(); });
        return server;
      } }; }`,
    pg: `let count = 0;
      export default { Pool: class {
        constructor() { this.id = ++count; }
        async query() {
          process.send({ type: "query", id: this.id });
          if (this.id === 1) await new Promise((resolve) => {
            const keepAlive = setInterval(() => {}, 1000);
            const stopping = () => process.send({ type: "stopping" });
            const signalled = () => process.send({ type: "signalled" });
            process.once("SIGTERM", stopping);
            process.once("SIGHUP", signalled);
            const release = (message) => { if (message === "release") {
              clearInterval(keepAlive); process.off("message", release);
              process.off("SIGTERM", stopping); process.off("SIGHUP", signalled); resolve();
            } };
            process.on("message", release);
            process.channel.unref();
          });
        }
        async end() { process.send({ type: "drained", id: this.id }); }
      } };`,
  };
  for (const [name, source] of Object.entries(stubs)) {
    const dep = path.join(dir, "node_modules", name);
    await mkdir(dep, { recursive: true });
    await writeFile(path.join(dep, "package.json"), '{"type":"module","exports":"./index.js"}');
    await writeFile(path.join(dep, "index.js"), source);
  }
  const snapshotFile = path.join(dir, "snapshot.json");
  const readyFile = path.join(dir, "ready");
  await writeFile(snapshotFile, initial);
  await writeFile(readyFile, "");
  const acks = [];
  let releaseAcks;
  const ackGate = new Promise((resolve) => { releaseAcks = resolve; });
  t.after(releaseAcks);
  let current = revision("a");
  const ackServer = createServer(async (req, res) => {
    let body = "";
    for await (const part of req) body += part;
    const ack = JSON.parse(body);
    acks.push(ack);
    if (holdAcks) await ackGate;
    res.writeHead(ack.revision === current ? 202 : 409).end();
  });
  await new Promise((resolve) => ackServer.listen(0, "127.0.0.1", resolve));
  t.after(() => new Promise((resolve) => { ackServer.closeAllConnections(); ackServer.close(resolve); }));
  const child = fork(path.join(dir, "handler.js"), [], {
    execArgv: [],
    stdio: ["ignore", "pipe", "pipe", "ipc"],
    env: { ...process.env, PORT: "0", FAAS_SECRETS_SNAPSHOT_FILE: snapshotFile, FAAS_SECRETS_RELOAD_READY_FILE: readyFile,
      FAAS_SECRETS_RELOAD_ACK_ENDPOINT: `http://127.0.0.1:${ackServer.address().port}/ack` },
  });
  t.after(() => { if (child.exitCode === null && child.signalCode === null) child.kill("SIGKILL"); });
  const exited = once(child, "exit");
  const events = [];
  let output = "";
  child.on("message", (event) => events.push(event));
  child.stdout.on("data", (chunk) => { output += chunk; });
  child.stderr.on("data", (chunk) => { output += chunk; });
  return { child, exited, events, acks, readyFile, releaseAcks, output: () => output, async publish(version) {
    current = version;
    const staged = path.join(dir, "next.json");
    await writeFile(staged, envelope(version, { DATABASE_URL: "postgres://example/db?sslmode=require" }));
    await rename(staged, snapshotFile);
  } };
}

test("starter waits off HTTP, serializes a signal during bootstrap and serves after the latest ACK", { timeout: 5000 }, async (t) => {
  const starter = await startStarter(t);
  await waitUntil(async () => await readFile(starter.readyFile, "utf8") === "ready\n");
  await delay(150);
  assert.deepEqual(starter.events, []);
  assert.deepEqual(starter.acks, []);
  await starter.publish(revision("a"));
  await waitUntil(() => starter.events.some((event) => event.type === "query"));
  starter.child.kill("SIGHUP");
  await starter.publish(revision("b"));
  await waitUntil(() => starter.events.some((event) => event.type === "signalled"));
  assert.equal(starter.events.filter((event) => event.type === "query").length, 1);
  assert.equal(starter.events.some((event) => event.type === "listening"), false);
  starter.child.send("release");
  await waitUntil(() => starter.events.some((event) => event.type === "listening"));
  assert.deepEqual(starter.acks.slice(0, 2), [{ revision: revision("a"), status: "applied" }, { revision: revision("b"), status: "applied" }]);
  const listening = starter.events.find((event) => event.type === "listening");
  const response = await fetch(`http://127.0.0.1:${listening.port}/healthz`);
  assert.equal(await response.text(), "ready");
  starter.child.kill("SIGTERM");
  assert.deepEqual(await starter.exited, [0, null]);
  assert.ok(!starter.output().includes("could not be confirmed"));
});

test("starter shutdown cancels pending startup without applying, ACKing or listening", { timeout: 5000 }, async (t) => {
  for (const signal of ["SIGTERM", "SIGINT"]) {
    await t.test(signal, async (t) => {
      const starter = await startStarter(t);
      await waitUntil(async () => await readFile(starter.readyFile, "utf8") === "ready\n");
      starter.child.kill(signal);
      assert.deepEqual(await starter.exited, [0, null]);
      assert.deepEqual(starter.events, []);
      assert.deepEqual(starter.acks, []);
      assert.equal(starter.output(), "");
    });
  }
});

test("starter drains a late candidate after shutdown without serving or acknowledging it", { timeout: 5000 }, async (t) => {
  const starter = await startStarter(t);
  await starter.publish(revision("a"));
  await waitUntil(() => starter.events.some((event) => event.type === "query"));
  starter.child.kill("SIGTERM");
  await waitUntil(() => starter.events.some((event) => event.type === "stopping"));
  starter.child.send("release");
  assert.deepEqual(await starter.exited, [0, null]);
  assert.deepEqual(starter.events.map((event) => event.type), ["query", "stopping", "drained"]);
  assert.deepEqual(starter.acks, []);
  assert.equal(starter.output(), "");
});

test("starter stays off HTTP until the initial ACK is accepted", { timeout: 5000 }, async (t) => {
  const starter = await startStarter(t, envelope(""), { holdAcks: true });
  await starter.publish(revision("a"));
  await waitUntil(() => starter.events.some((event) => event.type === "query"));
  starter.child.send("release");
  await waitUntil(() => starter.acks.length === 1);
  await delay(50);
  assert.equal(starter.events.some((event) => event.type === "listening"), false);
  starter.releaseAcks();
  await waitUntil(() => starter.events.some((event) => event.type === "listening"));
  starter.child.kill("SIGTERM");
  assert.deepEqual(await starter.exited, [0, null]);
});

test("starter invalid startup fails closed with sanitized logging", { timeout: 5000 }, async (t) => {
  const starter = await startStarter(t, '{"private-secret-value":');
  assert.deepEqual(await starter.exited, [1, null]);
  assert.equal(starter.output(), "initial secret application could not be confirmed\n");
  assert.deepEqual(starter.events, []);
  assert.deepEqual(starter.acks, []);
});

test("generation-aware ACK retries preserve the caller's identity", async () => {
  const generation = "a".repeat(32);
  const env = { FAAS_SECRETS_RELOAD_ACK_ENDPOINT: "http://local/ack", FAAS_SECRETS_RELOAD_GENERATION: generation };
  const bodies = [];
  const result = await postSecretAck({ revision: revision("b"), status: "applied", env,
    fetchImpl: async (_url, request) => {
      bodies.push(request.body);
      // Mutating a supplied environment while a request is in flight must not
      // relabel a retry as a replacement execution.
      env.FAAS_SECRETS_RELOAD_GENERATION = "c".repeat(32);
      return new Response(null, { status: bodies.length === 1 ? 503 : 202 });
    }, sleep: async () => {},
  });
  assert.equal(result, "accepted");
  assert.equal(bodies[0], bodies[1]);
  assert.deepEqual(JSON.parse(bodies[0]), { revision: revision("b"), status: "applied", generation });
});

test("invalid advertised execution identity fails before HTTP without exposing it", async () => {
  for (const generation of ["", "A".repeat(32), "short", "database-password", null]) {
    let sent = false;
    await assert.rejects(() => postSecretAck({ revision: revision("a"), status: "applied",
      env: { FAAS_SECRETS_RELOAD_ACK_ENDPOINT: "http://local/ack", FAAS_SECRETS_RELOAD_GENERATION: generation },
      fetchImpl: async () => { sent = true; return new Response(null, { status: 202 }); },
    }), (error) => error.message === "secret execution identity is invalid");
    assert.equal(sent, false);
  }
});

test("a retired process cannot adopt a same-version replacement identity", async () => {
  const env = { FAAS_SECRETS_SNAPSHOT_FILE: "/snapshot", FAAS_SECRETS_RELOAD_ACK_ENDPOINT: "http://local/ack", FAAS_SECRETS_RELOAD_GENERATION: "a".repeat(32) };
  const bodies = [];
  await assert.rejects(() => applyLatestSecretSnapshot({ env,
    readFile: async () => JSON.stringify({ revision: revision("b"), secrets: {} }),
    apply: async () => {}, sleep: async () => {},
    fetchImpl: async (_url, request) => { bodies.push(JSON.parse(request.body)); return new Response(null, { status: 409 }); },
  }), /secret projection changed too many times/);
  assert.ok(bodies.length > 1);
  assert.ok(bodies.every((body) => body.generation === "a".repeat(32)));
});
