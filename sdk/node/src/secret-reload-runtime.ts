import { readFile, writeFile } from 'node:fs/promises';
import { request as requestHTTP } from 'node:http';
import { request as requestHTTPS } from 'node:https';
import { setTimeout as sleep } from 'node:timers/promises';

export interface SecretSnapshot {
  readonly revision: string;
  readonly secrets: Readonly<Record<string, string>>;
}

/** Register every secret consumer in the workload. Invalidation must be
 * synchronous, idempotent and prevent late candidates from becoming available. Apply must
 * honor cancellation before publishing resources; retired resources are owned
 * by the application and may still be serving in-flight requests. */
export interface SecretConsumer {
  apply(snapshot: SecretSnapshot, signal: AbortSignal): void | Promise<void>;
  invalidate(): void;
}

export class SecretReloadError extends Error {
  constructor(message: string) { super(message); this.name = 'SecretReloadError'; }
}

export interface SecretReloadOptions {
  readonly consumers: readonly SecretConsumer[];
  readonly env?: Readonly<Record<string, string | undefined>>;
  readonly signal?: AbortSignal;
  /** Only bounds the initial wait for a versioned snapshot, not application work. */
  readonly startupTimeoutMs?: number;
  /** Signal-triggered failures receive only sanitized error text. */
  readonly onError?: (error: SecretReloadError) => void;
  /** Optional transport/file adapters; defaults use Node and the guest metadata service. */
  readonly io?: {
    readonly read?: (path: string, signal: AbortSignal) => Promise<string>;
    readonly ready?: (path: string, signal: AbortSignal) => Promise<void>;
    readonly fetch?: typeof globalThis.fetch;
    readonly sleep?: (ms: number, signal: AbortSignal) => Promise<void>;
  };
}

const revisionPattern = /^[0-9a-f]{64}$/;
const cancelled = (): SecretReloadError => new SecretReloadError('Secret reload cancelled');
function checkSignal(signal: AbortSignal): void { if (signal.aborted) throw cancelled(); }

// Use a dedicated transport, not global fetch: FaaSClient and applications can
// wrap fetch with API retries, logging and Problem decoding that hide a 409 ACK.
function requestAck(endpoint: string, body: string, signal: AbortSignal): Promise<number> {
  return new Promise((resolve, reject) => {
    const request = (endpoint.startsWith('https:') ? requestHTTPS : requestHTTP)(endpoint, {
      method: 'POST', agent: false, signal,
      headers: {'content-type': 'application/json', 'content-length': Buffer.byteLength(body)},
    }, response => {
      const status = response.statusCode ?? 500;
      response.destroy(); // Only status is authoritative; never parse or drain an unbounded body.
      resolve(status);
    });
    request.on('error', reject);
    request.end(body);
  });
}

// Observe shutdown even if a reader/factory ignores the signal. Shutdown is
// terminal: no subsequent callback can overlap a cancelled application call.
function abortable<T>(operation: () => T | Promise<T>, signal: AbortSignal): Promise<T> {
  checkSignal(signal);
  return new Promise((resolve, reject) => {
    const aborted = (): void => { reject(cancelled()); };
    signal.addEventListener('abort', aborted, {once: true});
    Promise.resolve().then(() => { checkSignal(signal); return operation(); }).then(resolve, reject)
      .finally(() => signal.removeEventListener('abort', aborted));
  });
}

/** Node-only coordinator for Gregale's atomic snapshot + generation-fenced ACK
 * protocol. Construct once per workload process. start() installs SIGHUP before
 * readiness, then resolves only after all consumers apply and the host accepts
 * the exact revision. It never mutates process.env or exits the process. */
export class SecretReloadCoordinator {
  readonly #consumers: readonly SecretConsumer[];
  readonly #snapshotPath: string;
  readonly #readyPath: string;
  readonly #endpoint: string;
  readonly #generation: string;
  readonly #startupTimeout: number;
  readonly #externalSignal: AbortSignal | undefined;
  readonly #onError: ((error: SecretReloadError) => void) | undefined;
  readonly #read: (path: string, signal: AbortSignal) => Promise<string>;
  readonly #ready: (path: string, signal: AbortSignal) => Promise<void>;
  readonly #request: (body: string, signal: AbortSignal) => Promise<number>;
  readonly #sleep: (ms: number, signal: AbortSignal) => Promise<void>;
  readonly #shutdown = new AbortController();
  #started = false;
  #stopped = false;
  #initial: Promise<string> | undefined;
  #pending: Promise<void> = Promise.resolve();
  #hangupPending = false;
  #hangupRunning = false;
  readonly #onAbort = (): void => this.stop();
  readonly #onHangup = (): void => {
    this.#hangupPending = true;
    if (!this.#hangupRunning) void this.#refreshFromSignal();
  };

  async #refreshFromSignal(): Promise<void> {
    this.#hangupRunning = true;
    try {
      do {
        this.#hangupPending = false;
        try { await this.reload(); } catch {
          if (!this.#stopped) {
            try { this.#onError?.(new SecretReloadError('Secret reload could not be completed')); } catch { /* Never log callback errors. */ }
          }
        }
      } while (this.#hangupPending && !this.#stopped);
    } finally { this.#hangupRunning = false; }
  }

  constructor(options: SecretReloadOptions) {
    if (!options.consumers?.length || options.consumers.some(c => !c || typeof c.apply !== 'function' || typeof c.invalidate !== 'function')) {
      throw new SecretReloadError('Secret consumers are unavailable');
    }
    // Capture callbacks and process identity once; later environment mutation
    // must never lend an old process the next execution's generation.
    this.#consumers = options.consumers.map(c => Object.freeze({apply: c.apply.bind(c), invalidate: c.invalidate.bind(c)}));
    const env = options.env ?? process.env;
    const path = (key: string): string => {
      const value = env[key];
      if (typeof value !== 'string' || !value.startsWith('/') || /[\0\r\n]/.test(value)) throw new SecretReloadError('Atomic secret reload paths are unavailable');
      return value;
    };
    this.#snapshotPath = path('FAAS_SECRETS_SNAPSHOT_FILE');
    this.#readyPath = path('FAAS_SECRETS_RELOAD_READY_FILE');
    const endpoint = env.FAAS_SECRETS_RELOAD_ACK_ENDPOINT;
    let url: URL;
    try {
      if (typeof endpoint !== 'string' || /[\0\r\n]/.test(endpoint)) throw new Error();
      url = new URL(endpoint);
      if (!['http:', 'https:'].includes(url.protocol) || !url.hostname || url.username || url.password || url.hash) throw new Error();
    } catch { throw new SecretReloadError('Secret acknowledgement endpoint is unavailable'); }
    this.#endpoint = url.href;
    const generation = env.FAAS_SECRETS_RELOAD_GENERATION;
    if (typeof generation !== 'string' || !/^[0-9a-f]{32}$/.test(generation)) throw new SecretReloadError('Secret execution identity is unavailable');
    this.#generation = generation;
    this.#startupTimeout = options.startupTimeoutMs ?? 30_000;
    if (!Number.isSafeInteger(this.#startupTimeout) || this.#startupTimeout <= 0 || this.#startupTimeout > 2_147_483_647) throw new SecretReloadError('Initial secret wait deadline is invalid');
    this.#externalSignal = options.signal;
    this.#onError = options.onError;
    this.#read = options.io?.read ?? ((file, signal) => readFile(file, {encoding: 'utf8', signal}));
    this.#ready = options.io?.ready ?? ((file, signal) => writeFile(file, 'ready\n', {mode: 0o600, signal}));
    const fetch = options.io?.fetch;
    this.#request = fetch ? async (body, signal) => {
      const response = await fetch(this.#endpoint, {
        method: 'POST', headers: {'content-type': 'application/json'}, body, signal, redirect: 'error',
      });
      void response.body?.cancel().catch(() => {});
      return response.status;
    } : (body, signal) => requestAck(this.#endpoint, body, signal);
    this.#sleep = options.io?.sleep ?? ((ms, signal) => sleep(ms, undefined, {signal}));
  }

  start(): Promise<string> {
    if (this.#stopped || this.#externalSignal?.aborted) { this.stop(); return Promise.reject(cancelled()); }
    if (this.#initial) return this.#initial;
    this.#started = true;
    process.on('SIGHUP', this.#onHangup);
    this.#externalSignal?.addEventListener('abort', this.#onAbort, {once: true});
    this.#initial = this.#enqueue(true).catch(error => { this.stop(); throw error; });
    return this.#initial;
  }

  /** Manual refresh and SIGHUP share the same queue, including startup.
   * A caller signal bounds one refresh, including queueing and ACK transport.
   * Cancellation after admission disables acquisition and fences late publication. */
  reload(signal?: AbortSignal): Promise<string> {
    if (!this.#started || this.#stopped) return Promise.reject(new SecretReloadError('Secret reload is not running'));
    if (signal?.aborted) return Promise.reject(cancelled());
    return this.#enqueue(false, signal);
  }

  /** Terminal shutdown. Detaches only this coordinator's handler and disables
   * registered consumers; it does not destroy acquired clients or await them. */
  stop(): void {
    if (this.#stopped) return;
    this.#stopped = true;
    this.#shutdown.abort();
    process.removeListener('SIGHUP', this.#onHangup);
    this.#externalSignal?.removeEventListener('abort', this.#onAbort);
    this.#disable();
  }

  #disable(): boolean {
    let success = true;
    for (const consumer of this.#consumers) {
      try {
        const result: unknown = consumer.invalidate();
        if (result !== undefined) {
          // TS permits async callbacks where void is expected. Reject that
          // contract at runtime without leaking an unhandled rejection.
          void Promise.resolve(result).catch(() => {});
          success = false;
        }
      } catch { success = false; }
    }
    return success;
  }

  #enqueue(initial: boolean, callerSignal?: AbortSignal): Promise<string> {
    const result = this.#pending.then(async () => {
      const signal = callerSignal ? AbortSignal.any([this.#shutdown.signal, callerSignal]) : this.#shutdown.signal;
      if (callerSignal?.aborted) throw cancelled();
      try {
        checkSignal(signal);
        if (!this.#disable()) throw new SecretReloadError('Secret consumers could not be disabled');
        if (initial) await abortable(() => this.#ready(this.#readyPath, signal), signal);
        return await this.#apply(initial, signal);
      } catch {
        this.#disable();
        checkSignal(signal);
        throw new SecretReloadError('Secret reload could not be completed');
      }
    });
    this.#pending = result.then(() => {}, () => {});
    return callerSignal ? abortable(() => result, callerSignal) : result;
  }

  async #snapshot(signal: AbortSignal, allowPending = false): Promise<SecretSnapshot> {
    for (let attempt = 0; attempt < 5; attempt++) {
      let raw: string;
      try { raw = await abortable(() => this.#read(this.#snapshotPath, signal), signal); }
      catch (error) {
        checkSignal(signal);
        if ((error as {code?: string})?.code === 'ENOENT' && attempt < 4) continue;
        throw new SecretReloadError('Secret snapshot could not be read');
      }
      let parsed: unknown;
      try { parsed = JSON.parse(raw); } catch { throw new SecretReloadError('Secret snapshot is invalid'); }
      if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) throw new SecretReloadError('Secret snapshot is invalid');
      const {revision, secrets} = parsed as Record<string, unknown>;
      if (typeof revision !== 'string' || (!(allowPending && revision === '') && !revisionPattern.test(revision)) ||
          !secrets || typeof secrets !== 'object' || Array.isArray(secrets) ||
          Object.entries(secrets).some(([key, value]) => !key || typeof value !== 'string')) throw new SecretReloadError('Secret snapshot is invalid');
      const snapshot = {revision, secrets: Object.freeze({...secrets} as Record<string, string>)};
      // Snapshots intentionally contain secrets. Prevent incidental logging;
      // explicit field access remains available to application consumers.
      Object.defineProperty(snapshot, 'toJSON', {value: () => '[SecretSnapshot redacted]'});
      Object.defineProperty(snapshot, Symbol.for('nodejs.util.inspect.custom'), {value: () => '[SecretSnapshot redacted]'});
      return Object.freeze(snapshot);
    }
    throw new SecretReloadError('Secret snapshot could not be read');
  }

  async #initialSnapshot(signal: AbortSignal): Promise<SecretSnapshot> {
    const deadline = AbortSignal.timeout(this.#startupTimeout);
    const waitSignal = AbortSignal.any([signal, deadline]);
    let backoff = 100;
    for (;;) {
      const snapshot = await this.#snapshot(waitSignal, true);
      if (snapshot.revision) return snapshot;
      await abortable(() => this.#sleep(backoff, waitSignal), waitSignal);
      backoff = Math.min(2000, backoff * 2);
    }
  }

  async #ack(revision: string, status: 'applied' | 'failed', signal: AbortSignal): Promise<'accepted' | 'stale'> {
    const body = JSON.stringify({revision, status, generation: this.#generation});
    for (let attempt = 0; attempt < 10; attempt++) {
      let statusCode: number | undefined;
      const requestSignal = AbortSignal.any([signal, AbortSignal.timeout(5000)]);
      try {
        statusCode = await abortable(() => this.#request(body, requestSignal), requestSignal);
      } catch { checkSignal(signal); }
      checkSignal(signal);
      if (statusCode === 202) return 'accepted';
      if (statusCode === 409) return 'stale';
      if ((statusCode !== undefined && statusCode !== 503) || attempt === 9) break;
      await abortable(() => this.#sleep(Math.min(2000, 100 * 2 ** attempt), signal), signal);
    }
    throw new SecretReloadError('Secret acknowledgement could not be confirmed');
  }

  async #apply(initial: boolean, signal: AbortSignal): Promise<string> {
    for (let attempt = 0; attempt <= 4; attempt++) {
      if (!this.#disable()) throw new SecretReloadError('Secret consumers could not be disabled');
      const snapshot = initial && attempt === 0 ? await this.#initialSnapshot(signal) : await this.#snapshot(signal);
      let status: 'applied' | 'failed' = 'applied';
      try {
        for (const consumer of this.#consumers) {
          await abortable(() => consumer.apply(snapshot, signal), signal);
          checkSignal(signal);
        }
      } catch {
        this.#disable();
        checkSignal(signal);
        status = 'failed';
      }
      const outcome = await this.#ack(snapshot.revision, status, signal);
      if (outcome === 'stale') continue;
      if (status === 'failed') throw new SecretReloadError('Secret snapshot could not be applied');
      return snapshot.revision;
    }
    throw new SecretReloadError('Secret projection changed too many times');
  }
}
