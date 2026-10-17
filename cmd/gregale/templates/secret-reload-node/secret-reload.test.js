import assert from "node:assert/strict";
import {cp, mkdir, mkdtemp, readFile, rename, rm, writeFile} from "node:fs/promises";
import {fork} from "node:child_process";
import {once} from "node:events";
import {createServer} from "node:http";
import {setTimeout as delay} from "node:timers/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

const revision = digit => digit.repeat(64);
const generation = "e".repeat(32);
const envelope = (version = revision("a"), secrets = {DATABASE_URL: "postgres://worker:private-password@db.example:5432/app?sslmode=require"}) => JSON.stringify({revision:version,secrets});

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
  for (const file of ["handler.js", "database.js"]) {
    await writeFile(path.join(dir, file), await readFile(new URL(file, import.meta.url)));
  }
  await cp(new URL("runtime/", import.meta.url), path.join(dir, "runtime"), {recursive:true});
  await writeFile(path.join(dir, "package.json"), '{"type":"module"}');
  const stubs = {
    // GET answers "ready"; registered POST routes run the real handlers.
    express: `import { createServer } from "node:http";
      export default function () { const posts = new Map(); return { get() {}, post(path, handler) { posts.set(path, handler); }, listen(port, ready) {
        const server = createServer((req, res) => {
          const handler = req.method === "POST" && posts.get(req.url);
          if (!handler) return res.end("ready");
          req.get = (name) => req.headers[name.toLowerCase()];
          res.sendStatus = (code) => { res.statusCode = code; res.end(); };
          handler(req, res);
        });
        server.listen(port, "127.0.0.1", () => { process.send({ type: "listening", port: server.address().port }); ready(); });
        return server;
      } }; }`,
    pg: `import {EventEmitter} from "node:events"; let count = 0;
      export default { Pool: class extends EventEmitter {
        constructor() { super(); this.id = ++count; this.validated = false; }
        async connect() { const pool = this; return Object.assign(new EventEmitter(), {query: (...args) => pool.query(...args), release() {}}); }
        async query() {
          if (this.validated) return {rows:[]};
          this.validated = true;
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
      FAAS_SECRETS_RELOAD_GENERATION: generation, FAAS_SECRETS_RELOAD_ACK_ENDPOINT: `http://127.0.0.1:${ackServer.address().port}/ack` },
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
    await writeFile(staged, envelope(version, { DATABASE_URL: "postgres://worker:private-password@db.example:5432/app?sslmode=require" }));
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
  assert.deepEqual(starter.acks.slice(0, 2), [{ revision: revision("a"), status: "applied", generation }, { revision: revision("b"), status: "applied", generation }]);
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

test("loopback lifecycle hooks drain the pool before capture and rebuild it on restore", { timeout: 5000 }, async (t) => {
  const starter = await startStarter(t);
  await starter.publish(revision("a"));
  await waitUntil(() => starter.events.some((event) => event.type === "query"));
  starter.child.send("release");
  await waitUntil(() => starter.events.some((event) => event.type === "listening"));
  const {port} = starter.events.find((event) => event.type === "listening");
  const hook = (name, marker) => fetch(`http://127.0.0.1:${port}/internal/${name}`, {method: "POST", headers: marker ? {[marker]: "1"} : {}});
  assert.equal((await hook("before-checkpoint")).status, 403);
  assert.equal((await hook("before-checkpoint", "X-Faas-After-Restore")).status, 403);
  assert.equal((await hook("after-restore")).status, 403);
  assert.equal(starter.events.some((event) => event.type === "drained"), false);
  assert.equal((await hook("before-checkpoint", "X-Faas-Before-Checkpoint")).status, 204);
  assert.ok(starter.events.some((event) => event.type === "drained" && event.id === 1));
  assert.equal((await hook("before-checkpoint", "X-Faas-Before-Checkpoint")).status, 204);
  const acks = starter.acks.length;
  assert.equal((await hook("after-restore", "X-Faas-After-Restore")).status, 204);
  assert.ok(starter.events.some((event) => event.type === "query" && event.id === 2));
  assert.equal(starter.acks.length, acks + 1);
  assert.deepEqual(starter.acks.at(-1), {revision: revision("a"), status: "applied", generation});
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
