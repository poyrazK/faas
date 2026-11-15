import assert from "node:assert/strict";
import test from "node:test";

import worker, { DEFAULT_TARGETS, PROBE_CACHE_CONTROL, PROBE_KEY, parseTargets, probeTarget, runProbe } from "./worker.mjs";

function fakeBucket(initial = null) {
  const objects = new Map();
  if (initial) objects.set(PROBE_KEY, { body: JSON.stringify(initial), options: {} });
  return {
    objects,
    async get(key) {
      const object = objects.get(key);
      return object ? { json: async () => JSON.parse(object.body) } : null;
    },
    async put(key, body, options) {
      objects.set(key, { body, options });
    },
  };
}

const respond = (status) => async () => ({ status, body: { cancel: async () => {} } });
const fail = (name) => async () => {
  const error = new Error(name);
  error.name = name;
  throw error;
};

let tick = 0;
const clock = () => Date.UTC(2026, 10, 15, 9, 0, 0) + tick;

test("defaults probe the API readiness endpoint", () => {
  assert.deepEqual(parseTargets(undefined), DEFAULT_TARGETS);
  assert.deepEqual(parseTargets(""), DEFAULT_TARGETS);
});

test("rejects unsafe or malformed targets", () => {
  assert.throws(() => parseTargets('[{"id":"x","url":"http://api.gregale.dev"}]'));
  assert.throws(() => parseTargets('[{"id":"Bad Id","url":"https://api.gregale.dev"}]'));
  assert.throws(() => parseTargets("[]"));
  assert.throws(() => parseTargets("{}"));
});

test("2xx is required by default; any_below_500 accepts 404", async () => {
  const strict = parseTargets('[{"id":"app","url":"https://canary.example.com/"}]')[0];
  const lenient = parseTargets('[{"id":"edge","url":"https://edge.example.com/","expect":"any_below_500"}]')[0];
  assert.equal((await probeTarget(strict, respond(404), clock)).ok, false);
  assert.equal((await probeTarget(lenient, respond(404), clock)).ok, true);
  assert.equal((await probeTarget(lenient, respond(502), clock)).ok, false);
});

test("timeouts and network errors are failures with a bounded reason", async () => {
  const target = DEFAULT_TARGETS[0];
  assert.deepEqual(
    { ok: (await probeTarget(target, fail("TimeoutError"), clock)).ok, error: (await probeTarget(target, fail("TimeoutError"), clock)).error },
    { ok: false, error: "timeout" },
  );
  const unreachable = await probeTarget(target, fail("TypeError"), clock);
  assert.equal(unreachable.error, "unreachable");
  assert.equal(unreachable.status, 0);
});

test("counts consecutive failures across runs and resets on success", async () => {
  const bucket = fakeBucket();
  const env = { STATUS_BUCKET: bucket };
  let doc = await runProbe(env, fail("TypeError"), clock);
  assert.equal(doc.checks[0].consecutive_failures, 1);
  const since = doc.checks[0].failing_since;
  tick += 60_000;
  doc = await runProbe(env, respond(503), clock);
  assert.equal(doc.checks[0].consecutive_failures, 2);
  assert.equal(doc.checks[0].failing_since, since);
  tick += 60_000;
  doc = await runProbe(env, respond(200), clock);
  assert.equal(doc.checks[0].consecutive_failures, 0);
  assert.equal(doc.checks[0].failing_since, null);
  const stored = bucket.objects.get(PROBE_KEY);
  assert.equal(stored.options.httpMetadata.cacheControl, PROBE_CACHE_CONTROL);
  assert.equal(stored.options.httpMetadata.contentType, "application/json");
  assert.equal(JSON.parse(stored.body).schema, 1);
});

test("a corrupt previous document does not stop the probe", async () => {
  const bucket = fakeBucket();
  bucket.objects.set(PROBE_KEY, { body: "{not json", options: {} });
  const doc = await runProbe({ STATUS_BUCKET: bucket }, respond(200), clock);
  assert.equal(doc.checks[0].ok, true);
});

test("the fetch handler never serves data", async () => {
  const response = await worker.fetch(new Request("https://status.gregale.dev/v1/status.json"));
  assert.equal(response.status, 404);
});
