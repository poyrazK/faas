// Generated from rest-api-postgres/database.js; run npm --prefix sdk/node run starter:sync.
import {PostgresPoolBinding} from "./runtime/postgres-pool-runtime.js";
import {SecretReloadCoordinator} from "./runtime/secret-reload-runtime.js";

// Pool sockets must not be captured in a VM snapshot: before_checkpoint drains
// every session, and after_restore replaces any pool a capture still carried
// before readiness. No SQL mutation is retried: a failed COMMIT can have an
// uncertain outcome.
export class DatabaseError extends Error {
  constructor(code = "database_unavailable") { super(code); this.code = code; }
}

export function poolConfig(connectionString) {
  const url = new URL(connectionString);
  if (!["postgres:", "postgresql:"].includes(url.protocol)) throw new DatabaseError();
  const mode = url.searchParams.get("sslmode");
  if (mode && !["disable", "require", "verify-ca", "verify-full"].includes(mode)) throw new DatabaseError();
  // pg's URL parser can replace the explicit SSL object. Keep verification
  // enabled for managed URLs, including sslmode=require. Custom CAs can use
  // Node's ordinary NODE_EXTRA_CA_CERTS trust store.
  for (const name of ["ssl", "sslcert", "sslkey", "sslrootcert", "uselibpqcompat"]) {
    if (url.searchParams.has(name)) throw new DatabaseError();
  }
  url.searchParams.delete("sslmode");
  return { connectionString: url.href, ssl: mode === "disable" ? false : { rejectUnauthorized: true },
    max: 5, idleTimeoutMillis: 30_000, connectionTimeoutMillis: 5_000 };
}

function scope(parent, timeoutMS) {
  const controller = new AbortController();
  const abort = () => controller.abort();
  const timer = setTimeout(abort, timeoutMS);
  parent?.addEventListener("abort", abort, { once: true });
  if (parent?.aborted) abort();
  return { controller, signal: controller.signal, dispose() {
    clearTimeout(timer); parent?.removeEventListener("abort", abort);
  } };
}

function bounded(promise, signal) {
  return new Promise((resolve, reject) => {
    const abort = () => { signal.removeEventListener("abort", abort); reject(new DatabaseError("database_cancelled")); };
    if (signal.aborted) abort();
    else signal.addEventListener("abort", abort, { once: true });
    Promise.resolve(promise).then(resolve, reject).finally(() => signal.removeEventListener("abort", abort));
  });
}

export function createDatabase(poolFactory, {
  env = process.env, requireReload = false, readinessQuery = "SELECT id FROM notes LIMIT 1",
  timeoutMS = 5_000, restoreTimeoutMS = 1_500, shutdownTimeoutMS = 5_000, maxInFlight = 16,
  log = console.error, io, startupTimeoutMs,
} = {}) {
  let starting, started = false, restoring, restoreRequest, checkpointing, checkpointRequest, closing,
    closed = false, restoreFailed = false, checkpointed = false, drained = false, generation = 0;
  const active = new Set();
  const shutdown = new AbortController();
  // Partial/legacy guest contracts must never downgrade to local env loading.
  const contractKeys = ["FAAS_SECRETS_SNAPSHOT_FILE", "FAAS_SECRETS_RELOAD_READY_FILE", "FAAS_SECRETS_RELOAD_ACK_ENDPOINT",
    "FAAS_SECRETS_RELOAD_GENERATION", "FAAS_SECRETS_FILE", "FAAS_SECRETS_REVISION_FILE"];
  const managed = requireReload || contractKeys.some(key => env[key] !== undefined);
  const local = Object.freeze({DATABASE_URL: env.DATABASE_URL});
  if (env.MIGRATION_DATABASE_URL) throw new DatabaseError("migration_credentials_forbidden");
  const binding = new PostgresPoolBinding("DATABASE_URL", config => {
    const pool = poolFactory(config);
    pool.on("error", () => log("database_idle_connection_lost"));
    return pool;
  }, {
    validate: async (pool, signal) => {
      const request = scope(signal, timeoutMS);
      try { await transaction(pool, readinessQuery, [], request.signal, false); }
      finally { request.dispose(); }
    },
    retire: pool => pool.end(),
    onRetireError: () => log("database_pool_close_failed"),
  });
  const consumer = binding.secretConsumer();
  const coordinator = managed ? new SecretReloadCoordinator({env, io, startupTimeoutMs,
    onError: () => log("secret_reload_failed"), consumers: [{
      apply: (snapshot, signal) => {
        if (snapshot.secrets.MIGRATION_DATABASE_URL) throw new DatabaseError("migration_credentials_forbidden");
        // A pool built after the checkpoint drain would be captured; restore
        // rereads the latest snapshot anyway.
        if (checkpointed && !restoring) throw new DatabaseError();
        return consumer.apply(snapshot, signal);
      }, invalidate: consumer.invalidate,
    }],
  }) : undefined;

  function refresh(signal) {
    if (closed || (checkpointed && !restoring)) return Promise.reject(new DatabaseError());
    const combined = AbortSignal.any([signal, shutdown.signal]);
    const work = coordinator ? coordinator.reload(combined) : binding.reload(local, combined);
    return bounded(work, signal).catch(() => {
      binding.invalidate();
      throw new DatabaseError(signal.aborted ? "database_cancelled" : "database_unavailable");
    });
  }

  function start() {
    if (starting) return starting;
    if (closed) return Promise.reject(new DatabaseError());
    // Install SIGHUP synchronously, before readiness or asynchronous SQL work.
    starting = (coordinator ? coordinator.start() : binding.reload(local, shutdown.signal)).then(() => {
      started = true;
    }, () => {
      binding.invalidate();
      throw new DatabaseError();
    });
    return starting;
  }

  async function transaction(target, text, values, signal, write, releasePool = () => {}) {
    let client, released = false, disconnected = false, connecting = false, finished = false;
    const release = (destroy = false) => {
      if (client && !released) {
        released = true;
        client.release(destroy);
        client.off("error", connectionLost);
      }
    };
    const connectionLost = () => {
      disconnected = true;
      log("database_active_connection_lost");
      release(true);
    };
    const abort = () => release(true);
    signal.addEventListener("abort", abort, { once: true });
    try {
      if (signal.aborted) throw new DatabaseError("database_cancelled");
      connecting = true;
      const pending = Promise.resolve().then(() => {
        if (signal.aborted) throw new DatabaseError("database_cancelled");
        return target.connect();
      }).then((value) => {
        connecting = false;
        client = value;
        // pg-pool removes its idle error listener while a client is checked out.
        client.on("error", connectionLost);
        if (signal.aborted || finished) {
          try { release(true); } finally { releasePool(); }
          throw new DatabaseError("database_cancelled");
        }
        return value;
      }, (error) => {
        connecting = false;
        if (finished) releasePool();
        throw error;
      });
      client = await bounded(pending, signal);
      if (signal.aborted) throw new DatabaseError("database_cancelled");
      const query = async (sql, params = []) => {
        if (signal.aborted) throw new DatabaseError("database_cancelled");
        if (disconnected) throw new DatabaseError();
        return bounded(client.query(sql, params), signal);
      };
      await query(write ? "BEGIN" : "BEGIN READ ONLY");
      // Transaction-local settings work with transaction-mode poolers.
      await query("SELECT set_config('statement_timeout', $1, true)", [`${timeoutMS}ms`]);
      const result = await query(text, values);
      await query("COMMIT");
      return result;
    } catch {
      release(true); // Disconnect rolls back an uncommitted transaction.
      throw new DatabaseError(signal.aborted ? "database_cancelled" : "database_unavailable");
    } finally {
      finished = true;
      signal.removeEventListener("abort", abort);
      try { release(); } finally { if (!connecting) releasePool(); }
    }
  }

  async function run(text, values = [], { signal, write = false } = {}) {
    if (closed || restoring || restoreFailed || checkpointed) throw new DatabaseError();
    if (active.size >= maxInFlight) throw new DatabaseError("database_busy");
    let lease;
    try { lease = binding.acquire(); } catch { throw new DatabaseError(); }
    const request = scope(signal, timeoutMS);
    active.add(request);
    try { return await transaction(lease.current(), text, values, request.signal, write, () => lease.release()); }
    catch (error) {
      if (request.signal.aborted) log("database_request_cancelled");
      throw error;
    } finally { active.delete(request); request.dispose(); }
  }

  function reload({signal} = {}) {
    const request = scope(signal, timeoutMS);
    return refresh(request.signal).finally(() => request.dispose());
  }

  // before_checkpoint (ADR-351): close admission, cancel unfinished work and
  // wait until every session has closed, so a capture holds no connection or
  // open transaction. A guest is then captured or destroyed, never resumed, so
  // only restore() reopens the pool. A retried capture finds it drained; a call
  // after a failed drain drains again instead of reporting success.
  function checkpoint({ signal } = {}) {
    if (closed) return Promise.reject(new DatabaseError());
    if (restoring) return Promise.reject(new DatabaseError("database_busy"));
    if (checkpointing) return checkpointing;
    if (drained) return Promise.resolve();
    const request = scope(signal, Math.min(restoreTimeoutMS, 1_500));
    checkpointRequest = request;
    checkpointed = true;
    for (const work of active) work.controller.abort();
    binding.invalidate();
    checkpointing = binding.waitForRetirement(request.signal).then(() => {
      if (closed) throw new DatabaseError();
      drained = true;
    }, () => {
      log("database_checkpoint_drain_failed");
      throw new DatabaseError();
    }).finally(() => { request.dispose(); checkpointing = undefined; checkpointRequest = undefined; });
    return checkpointing;
  }

  function restore({ signal } = {}) {
    if (closed) return Promise.reject(new DatabaseError());
    if (checkpointing) return Promise.reject(new DatabaseError("database_busy"));
    if (restoring) return restoring;
    const request = scope(signal, Math.min(restoreTimeoutMS, 1_500));
    restoreRequest = request;
    restoreFailed = true;
    for (const work of active) work.controller.abort();
    binding.invalidate();
    // Refresh the latest atomic envelope, even if its revision is unchanged.
    // The caller signal also fences refreshes still queued behind rotation.
    restoring = Promise.resolve().then(async () => {
      let phase = "reload";
      try {
        await refresh(request.signal);
        phase = "drain";
        await binding.waitForRetirement(request.signal);
        phase = "acquire";
        const lease = binding.acquire();
        lease.release();
        if (closed || request.signal.aborted) throw new DatabaseError();
        generation += 1;
        restoreFailed = false;
        checkpointed = false;
        drained = false;
      } catch {
        binding.invalidate();
        log(`database_restore_${phase}_failed`);
        throw new DatabaseError();
      }
    }).finally(() => { request.dispose(); restoring = undefined; restoreRequest = undefined; });
    return restoring;
  }

  function close() {
    if (closing) return closing;
    closed = true;
    shutdown.abort();
    coordinator?.stop();
    binding.invalidate();
    restoreRequest?.controller.abort();
    checkpointRequest?.controller.abort();
    for (const work of active) work.controller.abort();
    closing = binding.waitForRetirement(AbortSignal.timeout(shutdownTimeoutMS));
    return closing;
  }

  // Fixed state names and counts only: never SQL, URLs or driver errors.
  function state() {
    if (closed) return "closed";
    if (restoring) return "restoring";
    if (checkpointing) return "checkpointing";
    if (drained) return "checkpointed";
    if (checkpointed || restoreFailed) return "unavailable";
    return started ? "ready" : "starting";
  }
  return { start, query: run, reload, checkpoint, restore, close,
    get generation() { return generation; },
    get diagnostics() { return Object.freeze({ state: state(), generation, activeRequests: active.size }); } };
}
