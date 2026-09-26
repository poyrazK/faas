import { readFile as readFileDefault } from "node:fs/promises";

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

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

async function readRevision(path, readFile) {
  let value;
  try {
    value = (await readFile(path, "utf8")).trim();
  } catch {
    throw new SecretReloadError("secret revision could not be read");
  }
  if (!REVISION_RE.test(value)) {
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

/** Read the secret JSON and its opaque revision as one version-fenced snapshot. */
export async function readSecretSnapshot({
  env = process.env,
  readFile = readFileDefault,
  maxSnapshotAttempts = 5,
} = {}) {
  const secretsPath = env.FAAS_SECRETS_FILE;
  const revisionPath = env.FAAS_SECRETS_REVISION_FILE;
  if (!secretsPath || !revisionPath) {
    throw new SecretReloadError("secret projection paths are unavailable");
  }

  for (let attempt = 0; attempt < maxSnapshotAttempts; attempt += 1) {
    const before = await readRevision(revisionPath, readFile);
    let raw;
    try {
      raw = await readFile(secretsPath, "utf8");
    } catch {
      throw new SecretReloadError("secret projection could not be read");
    }
    const after = await readRevision(revisionPath, readFile);
    if (before !== after) continue;
    return { revision: before, secrets: parseSecretMap(raw) };
  }
  throw new SecretReloadError("secret projection changed while it was being read");
}

/** Post an app self-attestation; retry transient host failures with the same body. */
export async function postSecretAck({
  revision,
  status,
  env = process.env,
  fetchImpl = globalThis.fetch,
  sleep = delay,
  maxTransportAttempts = 10,
} = {}) {
  if (!REVISION_RE.test(revision || "") || !ACK_STATUSES.has(status)) {
    throw new SecretReloadError("secret acknowledgement is invalid");
  }
  const endpoint = env.FAAS_SECRETS_RELOAD_ACK_ENDPOINT;
  if (!endpoint || typeof fetchImpl !== "function") {
    throw new SecretReloadError("secret acknowledgement endpoint is unavailable");
  }
  const body = JSON.stringify({ revision, status });

  for (let attempt = 0; attempt < maxTransportAttempts; attempt += 1) {
    let response;
    try {
      response = await fetchImpl(endpoint, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body,
        signal: AbortSignal.timeout(5000),
      });
    } catch {
      if (attempt + 1 === maxTransportAttempts) break;
      await sleep(Math.min(2000, 100 * 2 ** attempt));
      continue;
    }

    if (response.status === 202) return "accepted";
    if (response.status === 409) return "stale";
    if (response.status !== 503 || attempt + 1 === maxTransportAttempts) break;
    await sleep(Math.min(2000, 100 * 2 ** attempt));
  }
  throw new SecretReloadError("secret acknowledgement could not be confirmed");
}

/** Apply and acknowledge the latest snapshot, rereading whenever it goes stale. */
export async function applyLatestSecretSnapshot({
  apply,
  env = process.env,
  readFile = readFileDefault,
  fetchImpl = globalThis.fetch,
  sleep = delay,
  maxStaleRetries = 4,
} = {}) {
  if (typeof apply !== "function") {
    throw new SecretReloadError("secret reload handler is unavailable");
  }

  for (let staleAttempt = 0; staleAttempt <= maxStaleRetries; staleAttempt += 1) {
    const snapshot = await readSecretSnapshot({ env, readFile });
    try {
      await apply(snapshot);
    } catch {
      const outcome = await postSecretAck({
        revision: snapshot.revision,
        status: "failed",
        env,
        fetchImpl,
        sleep,
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
    });
    if (outcome === "stale") continue;
    return snapshot.revision;
  }
  throw new SecretReloadError("secret projection changed too many times");
}
