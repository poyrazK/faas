import {ClientBinding} from './binding-client-runtime.js';
import type {SecretConsumer} from './secret-reload-runtime.js';

export class PostgresPoolBindingError extends Error {
  constructor(message: string) {super(message); this.name = 'PostgresPoolBindingError';}
}

/** Explicit connection identity; factories must avoid ambient credentials and
 * connection-identity overrides. Ordinary JSON/Node inspection is redacted. */
export interface PostgresPoolConfig {readonly connectionString: string;}

export interface PostgresPoolOptions<T> {
  /** Establish a connection, verify readiness, and release it before resolving.
   * Honor cancellation and bound driver connection/query timeouts. */
  readonly validate: (pool: T, signal?: AbortSignal) => void | Promise<void>;
  /** Once per exclusively owned pool, after the final lease releases. Different
   * retirements can overlap. Bound work; failures are sticky, without retries. */
  readonly retire: (pool: T) => void | Promise<void>;
  readonly onRetireError?: (error: PostgresPoolBindingError) => void | Promise<void>;
}

export interface PostgresPoolLease<T> {
  current(): T;
  /** Release the driver's connection first. Idempotent, including copied methods. */
  release(): void;
}

function configuration(key: string, values: Readonly<Record<string, string | undefined>>): PostgresPoolConfig {
  const value = values[key];
  const invalid = (): PostgresPoolBindingError => new PostgresPoolBindingError('Postgres binding connection URI is incomplete or invalid');
  if (typeof value !== 'string' || !value || /[\x00-\x20\x7f#]/.test(value) || /%(?![0-9a-f]{2})/i.test(value)) throw invalid();
  try {
    const url = new URL(value);
    if (!['postgres:', 'postgresql:'].includes(url.protocol) || !url.hostname || /[%/\\\0\r\n\t ]/.test(url.hostname) || !url.username || !url.password ||
      !url.pathname || url.pathname === '/' || /[\x00-\x1f\x7f]/.test(decodeURIComponent(url.username + url.password + url.pathname)) ||
      !url.port || +url.port < 1 || +url.port > 65535) throw invalid();
    for (const key of url.searchParams.keys()) {
      if (['host', 'hostaddr', 'port', 'user', 'username', 'password', 'dbname', 'database', 'service', 'servicefile', 'passfile',
        'connectionstring', 'connection_string', 'dsn'].includes(key.toLowerCase())) throw invalid();
    }
  } catch {throw invalid();}
  const config = {connectionString: value};
  Object.defineProperty(config, 'toJSON', {value: () => '[PostgresPoolConfig redacted]'});
  Object.defineProperty(config, Symbol.for('nodejs.util.inspect.custom'), {value: () => '[PostgresPoolConfig redacted]'});
  return Object.freeze(config);
}

/** Driver-independent server runtime. Build a fresh owned pool for each reload;
 * validation completes before publication and the coordinator's applied ACK.
 * Failed adoption disables new acquisition; outstanding leases can finish.
 * Leases do not extend database credential validity or replay transactions. */
export class PostgresPoolBinding<T> {
  readonly #core: ClientBinding<T, PostgresPoolConfig, undefined>;

  constructor(key: string, factory: (config: PostgresPoolConfig, signal?: AbortSignal) => T | Promise<T>, options: PostgresPoolOptions<T>) {
    if (!/^[A-Z][A-Z0-9_]*$/.test(key) || typeof factory !== 'function' || !options || typeof options.validate !== 'function' ||
      typeof options.retire !== 'function' || options.onRetireError !== undefined && typeof options.onRetireError !== 'function') throw new PostgresPoolBindingError('Invalid postgres pool binding');
    this.#core = new ClientBinding(values => ({config: configuration(key, values), value: undefined}),
      (config, _value, signal) => factory(config, signal),
      message => new PostgresPoolBindingError(`Postgres ${message.replaceAll('client', 'pool')}`), options);
  }

  reload(values: Readonly<Record<string, string | undefined>>, signal?: AbortSignal): Promise<void> {return this.#core.reload(values, signal);}
  invalidate(): void {this.#core.invalidate();}
  secretConsumer(): SecretConsumer {return this.#core.secretConsumer();}

  acquire(): PostgresPoolLease<T> {
    const inner = this.#core.acquire();
    const lease: PostgresPoolLease<T> = {current: () => inner.current().client, release: () => inner.release()};
    Object.defineProperty(lease, 'toJSON', {value: () => '[PostgresPoolLease redacted]'});
    Object.defineProperty(lease, Symbol.for('nodejs.util.inspect.custom'), {value: () => '[PostgresPoolLease redacted]'});
    return Object.freeze(lease);
  }

  /** Stop requests/reloads and invalidate first. Cancelling this wait never
   * closes checked-out transactions. The active pool is otherwise ignored. */
  waitForRetirement(signal?: AbortSignal): Promise<void> {return this.#core.waitForRetirement(signal);}
}

/** Node pg integration for Gregale's before_checkpoint and after_restore hooks.
 * The factory must return a fresh Pool each time. No driver internals, global
 * signal handlers, SQL retries, or application schema changes are installed. */
export interface PostgresQueryResult<Row = Record<string, unknown>> {
  rows: Row[];
  rowCount?: number | null;
}

export interface PostgresDriverClient {
  query(text: string, values?: unknown[]): Promise<PostgresQueryResult>;
  release(destroy?: boolean): void;
  on(event: 'error', listener: (error: unknown) => void): unknown;
  off(event: 'error', listener: (error: unknown) => void): unknown;
}

export interface PostgresDriverPool {
  connect(): Promise<PostgresDriverClient>;
  end(): Promise<void>;
  on(event: 'error', listener: (error: unknown) => void): unknown;
}

export type PostgresPoolErrorCode = 'database_unavailable' | 'database_cancelled' | 'database_busy';
export class PostgresPoolError extends Error {
  constructor(readonly code: PostgresPoolErrorCode = 'database_unavailable') {
    super(code);
    this.name = 'PostgresPoolError';
  }
}

export type PostgresPoolState = 'ready' | 'checkpointing' | 'checkpointed' | 'restoring' | 'unavailable' | 'closed';
export type PostgresPoolEventType = 'idle_connection_lost' | 'active_connection_lost' | 'connection_release_failed' |
  'request_cancelled' | 'pool_close_failed' | 'checkpoint_succeeded' | 'checkpoint_failed' | 'restore_succeeded' | 'restore_failed';
export interface PostgresPoolDiagnostics {
  readonly state: PostgresPoolState;
  readonly generation: number;
  readonly activeRequests: number;
  readonly openPools: number;
}
export interface PostgresPoolEvent extends PostgresPoolDiagnostics { readonly type: PostgresPoolEventType }
export interface RestoreAwarePostgresPoolOptions {
  /** Includes admission, connection acquisition, SQL, and COMMIT. Default 5s. */
  readonly timeoutMs?: number;
  /** Total hook/shutdown budget, 1–1500ms; leave room in a 2s platform hook. */
  readonly lifecycleTimeoutMs?: number;
  /** Bounds executing and queued requests together. Default 16. */
  readonly maxInFlight?: number;
  /** Run in a READ ONLY transaction before restore readiness. Default SELECT 1.
   * Prefer a read of an existing application table to detect a missing schema. */
  readonly readinessQuery?: { readonly text: string; readonly values?: readonly unknown[] };
  /** Receives fixed codes and counts only, never SQL, URLs, or driver errors. */
  readonly onEvent?: (event: PostgresPoolEvent) => void;
}
export interface PostgresQueryOptions {
  readonly signal?: AbortSignal;
  /** Single-query transactions are READ ONLY unless explicitly enabled. */
  readonly write?: boolean;
}

interface FunctionPoolResource {
  beforeCheckpoint(options: {signal?: AbortSignal}): Promise<void>;
  afterRestore(options: {signal?: AbortSignal}): Promise<void>;
}

// Installed before application imports only by a qualified generated adapter.
// Standalone HTTP applications keep their existing explicit lifecycle wiring.
export function registerFunctionPostgresResource(resource: FunctionPoolResource): (() => void) | undefined {
  const bridge: unknown = Reflect.get(globalThis, Symbol.for('gregale.function.postgres.lifecycle.v1'));
  if (bridge === undefined) return undefined;
  if (!bridge || typeof bridge !== 'object' || !('version' in bridge) || bridge.version !== 1 ||
      !('register' in bridge) || typeof bridge.register !== 'function') throw new PostgresPoolError();
  try {
    const unregister: unknown = bridge.register(resource);
    if (typeof unregister !== 'function') throw new PostgresPoolError();
    return unregister as () => void;
  } catch { throw new PostgresPoolError(); }
}

/** Keep pg's URL parser from overriding certificate and hostname verification.
 * A private CA belongs in NODE_EXTRA_CA_CERTS. Only explicit sslmode=disable
 * disables TLS (for local development). */
export function postgresPoolConfig(connectionString: string): {
  connectionString: string; ssl: false | { rejectUnauthorized: true };
  max: number; idleTimeoutMillis: number; connectionTimeoutMillis: number;
} {
  try {
    const url = new URL(connectionString);
    if (!['postgres:', 'postgresql:'].includes(url.protocol)) throw new Error();
    const mode = url.searchParams.get('sslmode');
    if (mode && !['disable', 'require', 'verify-ca', 'verify-full'].includes(mode)) throw new Error();
    for (const name of ['ssl', 'sslcert', 'sslkey', 'sslrootcert', 'uselibpqcompat']) {
      if (url.searchParams.has(name)) throw new Error();
    }
    url.searchParams.delete('sslmode');
    return {connectionString: url.href, ssl: mode === 'disable' ? false : {rejectUnauthorized: true},
      max: 5, idleTimeoutMillis: 30_000, connectionTimeoutMillis: 5_000};
  } catch { throw new PostgresPoolError(); }
}

function positiveInteger(value: number, maximum = 2_147_483_647): number {
  if (!Number.isSafeInteger(value) || value < 1 || value > maximum) throw new PostgresPoolError();
  return value;
}
function scope(parent: AbortSignal | undefined, timeoutMs: number) {
  const controller = new AbortController();
  const abort = (): void => controller.abort();
  const timer = setTimeout(abort, timeoutMs);
  parent?.addEventListener('abort', abort, {once: true});
  if (parent?.aborted) abort();
  return {controller, signal: controller.signal, dispose(): void {
    clearTimeout(timer); parent?.removeEventListener('abort', abort);
  }};
}
function check(signal: AbortSignal): void {
  if (signal.aborted) throw new PostgresPoolError('database_cancelled');
}
// Always observe late completion/rejection, including an uncooperative driver.
function bounded<T>(promise: Promise<T>, signal: AbortSignal): Promise<T> {
  return new Promise((resolve, reject) => {
    const abort = (): void => { signal.removeEventListener('abort', abort); reject(new PostgresPoolError('database_cancelled')); };
    if (signal.aborted) abort();
    else signal.addEventListener('abort', abort, {once: true});
    promise.then(value => { signal.removeEventListener('abort', abort); resolve(value); },
      error => { signal.removeEventListener('abort', abort); reject(error); });
  });
}

export class RestoreAwarePostgresPool {
  readonly #factory: () => PostgresDriverPool;
  readonly #timeout: number;
  readonly #lifecycleTimeout: number;
  readonly #maxInFlight: number;
  readonly #probe: {text: string; values: unknown[]};
  readonly #onEvent: RestoreAwarePostgresPoolOptions['onEvent'];
  readonly #active = new Set<ReturnType<typeof scope>>();
  readonly #pools = new Set<PostgresDriverPool>();
  readonly #owned = new WeakSet<PostgresDriverPool>();
  readonly #ends = new WeakMap<PostgresDriverPool, Promise<void>>();
  #pool: PostgresDriverPool | undefined;
  #state: PostgresPoolState = 'ready';
  #generation = 0;
  #operation: {kind: 'checkpoint' | 'restore'; request: ReturnType<typeof scope>; promise: Promise<void>} | undefined;
  #closing: Promise<void> | undefined;
  #unregister: (() => void) | undefined;

  constructor(factory: () => PostgresDriverPool, options: RestoreAwarePostgresPoolOptions = {}) {
    this.#factory = factory;
    this.#timeout = positiveInteger(options.timeoutMs ?? 5_000);
    this.#lifecycleTimeout = positiveInteger(options.lifecycleTimeoutMs ?? 1_500, 1_500);
    this.#maxInFlight = positiveInteger(options.maxInFlight ?? 16);
    const probe = options.readinessQuery ?? {text: 'SELECT 1'};
    if (typeof probe.text !== 'string' || !probe.text.trim()) throw new PostgresPoolError();
    this.#probe = {text: probe.text, values: [...(probe.values ?? [])]};
    this.#onEvent = options.onEvent;
    this.#unregister = registerFunctionPostgresResource(this);
    try { this.#pool = this.#open(); }
    catch (error) { void this.close().catch(() => {}); throw error; }
  }

  get generation(): number { return this.#generation; }
  get diagnostics(): PostgresPoolDiagnostics {
    return Object.freeze({state: this.#state, generation: this.#generation,
      activeRequests: this.#active.size, openPools: this.#pools.size});
  }
  #emit(type: PostgresPoolEventType): void {
    try {
      const result: unknown = this.#onEvent?.(Object.freeze({type, ...this.diagnostics}));
      // Async observers must not introduce unhandled rejections either.
      void Promise.resolve(result).catch(() => {});
    } catch { /* Observers cannot affect resource ownership or readiness. */ }
  }
  #open(): PostgresDriverPool {
    try {
      const pool = this.#factory();
      if (this.#owned.has(pool)) throw new Error();
      this.#owned.add(pool);
      this.#pools.add(pool);
      pool.on('error', () => this.#emit('idle_connection_lost'));
      return pool;
    } catch { throw new PostgresPoolError(); }
  }
  #end(pool: PostgresDriverPool): Promise<void> {
    let pending = this.#ends.get(pool);
    if (!pending) {
      pending = Promise.resolve().then(() => pool.end()).then(() => { this.#pools.delete(pool); this.#unregisterClosed(); }, () => {
        this.#emit('pool_close_failed'); throw new PostgresPoolError();
      });
      this.#ends.set(pool, pending);
    }
    return pending;
  }
  #drain(): Promise<void> {
    this.#pool = undefined;
    return Promise.all([...this.#pools].map(pool => this.#end(pool))).then(() => { this.#unregisterClosed(); });
  }
  #unregisterClosed(): void {
    if (this.#state === 'closed' && this.#pools.size === 0) {
      this.#unregister?.(); this.#unregister = undefined;
    }
  }
  #cancelRequests(): void { for (const request of this.#active) request.controller.abort(); }

  async #transaction(target: PostgresDriverPool, text: string, values: unknown[], signal: AbortSignal,
    write: boolean, timeoutMs: number): Promise<PostgresQueryResult> {
    let client: PostgresDriverClient | undefined, released = false, disconnected = false;
    const release = (destroy = false): void => {
      if (client && !released) {
        released = true;
        try { client.release(destroy); } catch { this.#emit('connection_release_failed'); }
        finally { client.off('error', connectionLost); }
      }
    };
    const connectionLost = (): void => {
      disconnected = true; this.#emit('active_connection_lost'); release(true);
    };
    const abort = (): void => release(true);
    signal.addEventListener('abort', abort, {once: true});
    try {
      check(signal);
      const pending = target.connect().then(value => {
        client = value;
        // pg-pool removes its idle error listener during checkout.
        client.on('error', connectionLost);
        if (signal.aborted) { release(true); check(signal); }
        return value;
      });
      client = await bounded(pending, signal);
      const query = async (sql: string, params: unknown[] = []): Promise<PostgresQueryResult> => {
        check(signal);
        if (disconnected) throw new PostgresPoolError();
        return bounded(client!.query(sql, params), signal);
      };
      await query(write ? 'BEGIN' : 'BEGIN READ ONLY');
      // Transaction-local settings also work with transaction-mode poolers.
      await query("SELECT set_config('statement_timeout', $1, true)", [`${timeoutMs}ms`]);
      const result = await query(text, values);
      await query('COMMIT');
      check(signal);
      if (disconnected) throw new PostgresPoolError();
      return result;
    } catch {
      release(true); // A disconnect rolls back work that has not committed.
      throw new PostgresPoolError(signal.aborted ? 'database_cancelled' : 'database_unavailable');
    } finally {
      signal.removeEventListener('abort', abort); release();
    }
  }

  /** Execute once in a single-query transaction. A lost COMMIT response has an
   * uncertain outcome; application idempotency is required for safe retries. */
  async query<Row = Record<string, unknown>>(text: string, values: readonly unknown[] = [],
    options: PostgresQueryOptions = {}): Promise<PostgresQueryResult<Row>> {
    if (this.#state !== 'ready' || !this.#pool) throw new PostgresPoolError();
    if (this.#active.size >= this.#maxInFlight) throw new PostgresPoolError('database_busy');
    const request = scope(options.signal, this.#timeout);
    this.#active.add(request);
    try {
      return await this.#transaction(this.#pool, text, [...values], request.signal, options.write ?? false, this.#timeout) as PostgresQueryResult<Row>;
    } catch (error) {
      if (request.signal.aborted) this.#emit('request_cancelled');
      throw error;
    } finally { this.#active.delete(request); request.dispose(); }
  }

  /** Stop admission, cancel unfinished work and drain all sessions. Remains
   * checkpointed until afterRestore; repeated calls do not reopen a pool. */
  beforeCheckpoint(options: {signal?: AbortSignal} = {}): Promise<void> { return this.#lifecycle('checkpoint', options.signal); }

  /** Used for snapshot restore AND frozen thaw, with or without a preceding
   * beforeCheckpoint. Only a fresh pool with a successful read probe is served. */
  afterRestore(options: {signal?: AbortSignal} = {}): Promise<void> { return this.#lifecycle('restore', options.signal); }

  #lifecycle(kind: 'checkpoint' | 'restore', signal: AbortSignal | undefined): Promise<void> {
    if (this.#state === 'closed') return Promise.reject(new PostgresPoolError());
    if (signal?.aborted) return Promise.reject(new PostgresPoolError('database_cancelled'));
    if (this.#operation) {
      if (this.#operation.kind !== kind) return Promise.reject(new PostgresPoolError('database_busy'));
      // A duplicate caller can stop waiting without cancelling the owner hook.
      return signal ? bounded(this.#operation.promise, signal) : this.#operation.promise;
    }
    if (kind === 'checkpoint' && this.#state === 'checkpointed') return Promise.resolve();
    this.#state = kind === 'checkpoint' ? 'checkpointing' : 'restoring';
    const request = scope(signal, this.#lifecycleTimeout);
    const promise = Promise.resolve().then(async () => {
      try {
        check(request.signal);
        await bounded(this.#drain(), request.signal);
        check(request.signal);
        if (kind === 'restore') {
          const candidate = this.#open();
          await this.#transaction(candidate, this.#probe.text, this.#probe.values, request.signal, false, this.#lifecycleTimeout);
          check(request.signal);
          this.#pool = candidate;
          this.#generation += 1;
          this.#state = 'ready';
        } else this.#state = 'checkpointed';
        this.#emit(kind === 'restore' ? 'restore_succeeded' : 'checkpoint_succeeded');
      } catch {
        if (this.#state !== 'closed') this.#state = 'unavailable';
        void this.#drain().catch(() => {});
        this.#emit(kind === 'restore' ? 'restore_failed' : 'checkpoint_failed');
        throw new PostgresPoolError();
      }
    }).finally(() => { request.dispose(); this.#operation = undefined; });
    this.#operation = {kind, request, promise};
    this.#cancelRequests();
    return promise;
  }

  /** Terminal, idempotent and bounded by lifecycleTimeoutMs. */
  close(): Promise<void> {
    if (this.#closing) return this.#closing;
    this.#state = 'closed';
    this.#operation?.request.controller.abort();
    this.#cancelRequests();
    const request = scope(undefined, this.#lifecycleTimeout);
    this.#closing = bounded(this.#drain(), request.signal).catch(() => { throw new PostgresPoolError(); })
      .finally(() => request.dispose());
    return this.#closing;
  }
}

export function createPostgresPool(factory: () => PostgresDriverPool, options: RestoreAwarePostgresPoolOptions = {}): RestoreAwarePostgresPool {
  return new RestoreAwarePostgresPool(factory, options);
}
