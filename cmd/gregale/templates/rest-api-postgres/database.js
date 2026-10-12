// Pool sockets captured in a VM snapshot must be replaced before readiness.
// No SQL mutation is retried: a failed COMMIT can have an uncertain outcome.
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

export function createDatabase(poolFactory, { timeoutMS = 5_000, restoreTimeoutMS = 1_500, log = console.error } = {}) {
  let pool, restoring, restoreRequest, closed = false, restoreFailed = false;
  const active = new Set(), pools = new Set(), ends = new WeakMap();
  let generation = 0;
  function open() {
    const next = poolFactory();
    next.on("error", () => log("database_idle_connection_lost"));
    pools.add(next);
    return next;
  }
  function end(target) {
    if (!ends.has(target)) {
      ends.set(target, Promise.resolve().then(() => target.end()).finally(() => pools.delete(target)));
    }
    return ends.get(target);
  }
  pool = open();

  async function transaction(target, text, values, signal, write) {
    let client, released = false, disconnected = false;
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
      const pending = target.connect().then((value) => {
        client = value;
        // pg-pool removes its idle error listener while a client is checked out.
        client.on("error", connectionLost);
        if (signal.aborted) { release(true); throw new DatabaseError("database_cancelled"); }
        return value;
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
      signal.removeEventListener("abort", abort);
      release();
    }
  }

  async function run(text, values = [], { signal, write = false } = {}) {
    if (closed || restoring || restoreFailed) throw new DatabaseError();
    if (active.size >= 16) throw new DatabaseError("database_busy");
    const request = scope(signal, timeoutMS);
    active.add(request);
    try { return await transaction(pool, text, values, request.signal, write); }
    catch (error) {
      if (request.signal.aborted) log("database_request_cancelled");
      throw error;
    }
    finally { active.delete(request); request.dispose(); }
  }

  function restore({ signal } = {}) {
    if (closed) return Promise.reject(new DatabaseError());
    if (restoring) return restoring;
    const request = scope(signal, Math.min(restoreTimeoutMS, 1_500));
    restoreRequest = request;
    restoreFailed = true;
    for (const work of active) work.controller.abort();
    const old = pool;
    // Publish the gate before asynchronous work or customer requests.
    restoring = Promise.resolve().then(async () => {
      if (closed || request.signal.aborted) throw new DatabaseError();
      try {
        pool = open();
        await bounded(Promise.all([end(old), transaction(pool, "SELECT id FROM notes LIMIT 1", [], request.signal, false)]), request.signal);
        if (closed || request.signal.aborted) throw new DatabaseError();
        generation += 1;
        restoreFailed = false;
      } catch {
        void end(old).catch(() => log("database_pool_close_failed"));
        void end(pool).catch(() => log("database_pool_close_failed"));
        throw new DatabaseError();
      }
    }).finally(() => { request.dispose(); restoring = undefined; restoreRequest = undefined; });
    return restoring;
  }

  let closing;
  function close() {
    if (closing) return closing;
    closed = true;
    restoreRequest?.controller.abort();
    for (const work of active) work.controller.abort();
    closing = Promise.all([...pools].map(end)).then(() => undefined);
    return closing;
  }
  return { query: run, restore, close, get generation() { return generation; } };
}
