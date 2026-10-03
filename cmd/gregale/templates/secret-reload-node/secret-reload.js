import { readFile as readFileDefault, writeFile as writeFileDefault } from "node:fs/promises";
import { setTimeout as sleepDefault } from "node:timers/promises";

const REVISION_RE = /^[0-9a-f]{64}$/;
const ACK_STATUSES = new Set(["applied", "failed"]);

// Keep error text deliberately non-specific. File parsing errors, fetch errors,
// and database errors can contain credentials and must never be logged or
// returned to the platform as acknowledgement metadata.
export class SecretReloadError extends Error {
  constructor(message) {
    super(message);
    this.name = "SecretReloadError";
  }
}

const delay = (ms, { signal } = {}) => sleepDefault(ms, undefined, { signal });

function checkSignal(signal) {
  if (signal?.aborted) throw new SecretReloadError("secret reload cancelled");
}

// Observe cancellation even when an injected reader or application callback
// cannot cancel its underlying work. Never continue to an ACK after cancellation.
function withSignal(operation, signal) {
  checkSignal(signal);
  if (!signal) return Promise.resolve().then(operation);
  return new Promise((resolve, reject) => {
    const finish = (callback, value) => {
      signal.removeEventListener("abort", aborted);
      callback(value);
    };
    const aborted = () => finish(reject, new SecretReloadError("secret reload cancelled"));
    signal.addEventListener("abort", aborted, { once: true });
    Promise.resolve().then(() => {
      checkSignal(signal);
      return operation();
    }).then((value) => finish(resolve, value), (error) => finish(reject, error));
  });
}

async function readRevision(path, readFile, signal, allowPending) {
  let value;
  try {
    value = (await withSignal(() => readFile(path, { encoding: "utf8", signal }), signal)).trim();
  } catch {
    checkSignal(signal);
    throw new SecretReloadError("secret revision could not be read");
  }
  if (!(allowPending && value === "") && !REVISION_RE.test(value)) {
    throw new SecretReloadError("secret revision is invalid");
  }
  return value;
}

function parseSecretMap(raw) {
  let parsed;
  try {
    parsed = JSON.parse(raw);
  } catch {
    throw new SecretReloadError("secret projection is invalid");
  }
  return validateSecretMap(parsed);
}

function validateSecretMap(parsed) {
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new SecretReloadError("secret projection is invalid");
  }
  for (const [key, value] of Object.entries(parsed)) {
    if (!key || typeof value !== "string") {
      throw new SecretReloadError("secret projection is invalid");
    }
  }
  return Object.freeze({ ...parsed });
}

async function readAtomicSecretSnapshot(path, readFile, maxAttempts, signal, allowPending) {
  if (typeof path !== "string" || !path) {
    throw new SecretReloadError("secret snapshot path is unavailable");
  }
  for (let attempt = 0; attempt < maxAttempts; attempt += 1) {
    let raw;
    try {
      raw = await withSignal(() => readFile(path, { encoding: "utf8", signal }), signal);
    } catch (error) {
      checkSignal(signal);
      // A lookup through the generation pointer can race old-file cleanup.
      // Retry the authoritative path; never downgrade to separate files.
      if (error?.code === "ENOENT" && attempt + 1 < maxAttempts) continue;
      throw new SecretReloadError("secret snapshot could not be read");
    }
    let parsed;
    try {
      parsed = JSON.parse(raw);
    } catch {
      throw new SecretReloadError("secret snapshot is invalid");
    }
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed) ||
        typeof parsed.revision !== "string" ||
        (!(allowPending && parsed.revision === "") && !REVISION_RE.test(parsed.revision))) {
      throw new SecretReloadError("secret snapshot is invalid");
    }
    return { revision: parsed.revision, secrets: validateSecretMap(parsed.secrets) };
  }
  throw new SecretReloadError("secret snapshot could not be read");
}

/** Call only after installing the signal handler; this is readiness, not an ACK. */
export async function markSecretReloadReady({ env = process.env, writeFile = writeFileDefault } = {}) {
  const path = env.FAAS_SECRETS_RELOAD_READY_FILE;
  if (path === undefined) return; // Older guests do not advertise the handshake.
  if (typeof path !== "string" || !path) throw new SecretReloadError("secret reload readiness is unavailable");
  try {
    await writeFile(path, "ready\n", { mode: 0o600 });
  } catch {
    throw new SecretReloadError("secret reload readiness could not be confirmed");
  }
}

/** Prefer the atomic envelope; retain the separate-file contract for older guests. */
export async function readSecretSnapshot(options = {}) {
  return readSecretSnapshotInternal(options, false);
}

async function readSecretSnapshotInternal({
  env = process.env,
  readFile = readFileDefault,
  maxSnapshotAttempts = 5,
  signal,
}, allowPending) {
  checkSignal(signal);
  if (env.FAAS_SECRETS_SNAPSHOT_FILE !== undefined) {
    return readAtomicSecretSnapshot(env.FAAS_SECRETS_SNAPSHOT_FILE, readFile, maxSnapshotAttempts, signal, allowPending);
  }
  const secretsPath = env.FAAS_SECRETS_FILE;
  const revisionPath = env.FAAS_SECRETS_REVISION_FILE;
  if (!secretsPath || !revisionPath) {
    throw new SecretReloadError("secret projection paths are unavailable");
  }

  for (let attempt = 0; attempt < maxSnapshotAttempts; attempt += 1) {
    const before = await readRevision(revisionPath, readFile, signal, allowPending);
    let raw;
    try {
      raw = await withSignal(() => readFile(secretsPath, { encoding: "utf8", signal }), signal);
    } catch {
      checkSignal(signal);
      throw new SecretReloadError("secret projection could not be read");
    }
    const after = await readRevision(revisionPath, readFile, signal, allowPending);
    if (before !== after) continue;
    return { revision: before, secrets: parseSecretMap(raw) };
  }
  throw new SecretReloadError("secret projection changed while it was being read");
}

async function waitForInitialSecretSnapshot({ startupTimeoutMs, sleep = delay, signal, ...options }) {
  if (!Number.isSafeInteger(startupTimeoutMs) || startupTimeoutMs <= 0 || startupTimeoutMs > 2_147_483_647) {
    throw new SecretReloadError("initial secret wait deadline is invalid");
  }
  checkSignal(signal);
  const timeout = new AbortController();
  const timer = setTimeout(() => timeout.abort(), startupTimeoutMs);
  const deadline = timeout.signal;
  const waitSignal = signal ? AbortSignal.any([signal, deadline]) : deadline;
  let backoff = 100;
  try {
    for (;;) {
      const snapshot = await readSecretSnapshotInternal({ ...options, signal: waitSignal }, true);
      checkSignal(waitSignal);
      if (snapshot.revision !== "") return snapshot;
      await withSignal(() => sleep(backoff, { signal: waitSignal }), waitSignal);
      backoff = Math.min(2000, backoff * 2);
    }
  } catch (error) {
    checkSignal(signal);
    if (deadline.aborted) throw new SecretReloadError("initial secret revision was not available before the deadline");
    throw error;
  } finally {
    clearTimeout(timer);
  }
}

/** Post an app self-attestation; retry transient host failures with the same body. */
export async function postSecretAck({
  revision,
  status,
  env = process.env,
  fetchImpl = globalThis.fetch,
  sleep = delay,
  maxTransportAttempts = 10,
  signal,
} = {}) {
  checkSignal(signal);
  if (!REVISION_RE.test(revision || "") || !ACK_STATUSES.has(status)) {
    throw new SecretReloadError("secret acknowledgement is invalid");
  }
  const endpoint = env.FAAS_SECRETS_RELOAD_ACK_ENDPOINT;
  if (!endpoint || typeof fetchImpl !== "function") {
    throw new SecretReloadError("secret acknowledgement endpoint is unavailable");
  }
  const generation = env.FAAS_SECRETS_RELOAD_GENERATION;
  if (generation !== undefined && (typeof generation !== "string" || !/^[0-9a-f]{32}$/.test(generation))) {
    throw new SecretReloadError("secret execution identity is invalid");
  }
  // Capture this process's identity once; transport retries reuse the same body.
  const body = JSON.stringify({ revision, status, ...(generation === undefined ? {} : { generation }) });

  for (let attempt = 0; attempt < maxTransportAttempts; attempt += 1) {
    let response;
    try {
      response = await withSignal(() => fetchImpl(endpoint, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body,
        signal: signal ? AbortSignal.any([signal, AbortSignal.timeout(5000)]) : AbortSignal.timeout(5000),
      }), signal);
      checkSignal(signal);
    } catch {
      checkSignal(signal);
      if (attempt + 1 === maxTransportAttempts) break;
      await withSignal(() => sleep(Math.min(2000, 100 * 2 ** attempt), { signal }), signal);
      continue;
    }

    if (response.status === 202) return "accepted";
    if (response.status === 409) return "stale";
    if (response.status !== 503 || attempt + 1 === maxTransportAttempts) break;
    await withSignal(() => sleep(Math.min(2000, 100 * 2 ** attempt), { signal }), signal);
  }
  throw new SecretReloadError("secret acknowledgement could not be confirmed");
}

/** Apply and acknowledge the latest snapshot, rereading whenever it goes stale. */
export async function applyLatestSecretSnapshot(options = {}) {
  return applySecretSnapshots(options, () => readSecretSnapshot(options));
}

/** Bootstrap only: wait for the initial version, then preserve strict reloads. */
export async function applyInitialSecretSnapshot({ startupTimeoutMs = 30_000, ...options } = {}) {
  let initial = true;
  return applySecretSnapshots(options, async () => {
    if (!initial) return readSecretSnapshot(options);
    const snapshot = await waitForInitialSecretSnapshot({ ...options, startupTimeoutMs });
    initial = false;
    return snapshot;
  });
}

async function applySecretSnapshots({
  apply,
  env = process.env,
  fetchImpl = globalThis.fetch,
  sleep = delay,
  maxStaleRetries = 4,
  signal,
}, readSnapshot) {
  checkSignal(signal);
  if (typeof apply !== "function") {
    throw new SecretReloadError("secret reload handler is unavailable");
  }

  for (let staleAttempt = 0; staleAttempt <= maxStaleRetries; staleAttempt += 1) {
    const snapshot = await readSnapshot();
    try {
      await withSignal(() => apply(snapshot), signal);
      checkSignal(signal);
    } catch {
      checkSignal(signal);
      const outcome = await postSecretAck({
        revision: snapshot.revision,
        status: "failed",
        env,
        fetchImpl,
        sleep,
        signal,
      });
      if (outcome === "stale") continue;
      throw new SecretReloadError("secret snapshot could not be applied");
    }

    const outcome = await postSecretAck({
      revision: snapshot.revision,
      status: "applied",
      env,
      fetchImpl,
      sleep,
      signal,
    });
    if (outcome === "stale") continue;
    return snapshot.revision;
  }
  throw new SecretReloadError("secret projection changed too many times");
}
