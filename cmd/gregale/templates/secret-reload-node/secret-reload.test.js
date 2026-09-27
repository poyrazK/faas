import assert from "node:assert/strict";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import {
  applyLatestSecretSnapshot,
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
