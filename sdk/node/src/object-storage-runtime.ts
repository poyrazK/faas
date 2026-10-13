import type {SecretConsumer} from './secret-reload-runtime.js';

/** Server-side helper. The factory supplies the application's S3 implementation;
 * it must use these explicit credentials, never ambient provider credentials. */
export interface ObjectStorageClientConfig {
  readonly endpoint: string;
  readonly region: string;
  readonly forcePathStyle: true;
  readonly requestChecksumCalculation: 'WHEN_REQUIRED';
  readonly responseChecksumValidation: 'WHEN_REQUIRED';
  readonly credentials: Readonly<{ accessKeyId: string; secretAccessKey: string }>;
}

export class ObjectStorageBindingError extends Error {
  constructor(message: string) { super(message); this.name = 'ObjectStorageBindingError'; }
}

/** Retire is opt-in: factories must return fresh, exclusively owned resources.
 * Different retirements may overlap; callbacks must bound their work and avoid
 * blocking the event loop. Cleanup runs once without retries. Errors are redacted. */
export interface ObjectStorageBindingOptions<T> {
  readonly retire?: (client: T) => void | Promise<void>;
  readonly onRetireError?: (error: ObjectStorageBindingError) => void | Promise<void>;
}

export interface ObjectStorageLease<T> {
  /** Hold this lease until every request/response stream is consumed or closed. */
  current(): Readonly<{client: T; bucket: string}>;
  /** Idempotent; schedules cleanup without waiting for retirement callbacks. */
  release(): void;
}

interface StorageClient<T> {
  readonly pair: Readonly<{client: T; bucket: string}>;
  leases: number;
  retired: boolean;
  scheduled: boolean;
}

function configuration(prefix: string, values: Readonly<Record<string, string | undefined>>): {config: ObjectStorageClientConfig; bucket: string} {
  const read = (suffix: string): string => {
    const value = values[`${prefix}_${suffix}`];
    if (typeof value !== 'string' || !value || /[\0\r\n]/.test(value)) throw new ObjectStorageBindingError('Object-storage binding environment is incomplete or invalid');
    return value;
  };
  const endpoint = read('ENDPOINT'), region = read('REGION'), bucket = read('BUCKET');
  const accessKeyId = read('ACCESS_KEY_ID'), secretAccessKey = read('SECRET_ACCESS_KEY');
  let url: URL;
  try { url = new URL(endpoint); } catch { throw new ObjectStorageBindingError('Object-storage binding configuration is invalid'); }
  if (!url.hostname || !['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.search || url.hash ||
      read('ADDRESSING_STYLE') !== 'path' || /[/\\ \t?#]/.test(region + bucket)) throw new ObjectStorageBindingError('Object-storage binding configuration is invalid');
  const credentials = Object.freeze({accessKeyId, secretAccessKey});
  const config = {endpoint, region, forcePathStyle: true as const, credentials,
    requestChecksumCalculation: 'WHEN_REQUIRED' as const, responseChecksumValidation: 'WHEN_REQUIRED' as const};
  // Keep credentials enumerable: S3 constructors commonly spread their options.
  // Redact ordinary JSON and Node inspection without changing factory inputs.
  Object.defineProperty(config, 'toJSON', {value: () => '[ObjectStorageClientConfig redacted]'});
  Object.defineProperty(config, Symbol.for('nodejs.util.inspect.custom'), {value: () => '[ObjectStorageClientConfig redacted]'});
  return {config: Object.freeze(config) as ObjectStorageClientConfig, bucket};
}

export class ObjectStorageBinding<T> {
  readonly #prefix: string;
  readonly #factory: (config: ObjectStorageClientConfig, bucket: string, signal?: AbortSignal) => T | Promise<T>;
  #active: StorageClient<T> | undefined;
  #pending: Promise<void> = Promise.resolve();
  #epoch = 0;
  readonly #retire: ObjectStorageBindingOptions<T>['retire'];
  readonly #onRetireError: ObjectStorageBindingOptions<T>['onRetireError'];
  readonly #retired = new Set<StorageClient<T>>();
  readonly #waiters = new Set<() => void>();
  #building = 0;
  #retirementFailed = false;

  constructor(prefix: string, factory: (config: ObjectStorageClientConfig, bucket: string, signal?: AbortSignal) => T | Promise<T>, options: ObjectStorageBindingOptions<T> = {}) {
    if (!/^[A-Z][A-Z0-9_]{0,47}$/.test(prefix) || typeof factory !== 'function' || !options ||
      options.retire !== undefined && typeof options.retire !== 'function' || options.onRetireError !== undefined && typeof options.onRetireError !== 'function') throw new ObjectStorageBindingError('Invalid object-storage binding');
    this.#prefix = prefix;
    this.#factory = factory;
    this.#retire = options.retire;
    this.#onRetireError = options.onRetireError;
  }

  /** Use one atomic secret envelope. ACK its revision only after this resolves
   * and all other consumers apply. Acquire leases through stream closure.
   * Without a retirement callback, current() retains caller-owned behavior.
   * Failure disables new acquisition, including revocation. */
  reload(values: Readonly<Record<string, string | undefined>>, signal?: AbortSignal): Promise<void> {
    const snapshot = {...values};
    const epoch = this.#epoch;
    const apply = async (): Promise<void> => {
      if (epoch !== this.#epoch) throw new ObjectStorageBindingError('Object-storage reload cancelled');
      this.#building++;
      this.#clearActive();
      try {
        if (signal?.aborted) throw new ObjectStorageBindingError('Object-storage reload cancelled');
        const {config, bucket} = configuration(this.#prefix, snapshot);
        let client: T;
        try { client = await this.#factory(config, bucket, signal); }
        catch { throw new ObjectStorageBindingError('Object-storage client could not be configured'); }
        if (client === undefined || client === null) throw new ObjectStorageBindingError('Object-storage client is unavailable');
        if (this.#retire && [...this.#retired].some(entry => Object.is(entry.pair.client, client))) {
          throw new ObjectStorageBindingError('Object-storage factory reused an owned client');
        }
        const candidate: StorageClient<T> = {pair: Object.freeze({client, bucket}), leases: 0, retired: false, scheduled: false};
        if (signal?.aborted || epoch !== this.#epoch) {
          this.#retireClient(candidate);
          throw new ObjectStorageBindingError('Object-storage reload cancelled');
        }
        this.#active = candidate;
      } finally {
        this.#building--;
        this.#notify();
      }
    };
    const result = this.#pending.then(apply);
    this.#pending = result.catch(() => {});
    return result;
  }

  /** Disable new acquisition and fence in-progress/queued candidates. Existing
   * outstanding leases keep their clients alive. A future reload may recover. */
  invalidate(): void {
    this.#epoch++;
    this.#clearActive();
  }

  /** Register with SecretReloadCoordinator alongside every other secret user. */
  secretConsumer(): SecretConsumer {
    const consumer: SecretConsumer = {
      apply: (snapshot, signal) => this.reload(snapshot.secrets, signal),
      invalidate: () => this.invalidate(),
    };
    return Object.freeze(consumer);
  }

  current(): Readonly<{client: T; bucket: string}> {
    if (!this.#active) throw new ObjectStorageBindingError('Object-storage binding is not ready');
    if (this.#retire) throw new ObjectStorageBindingError('Object-storage binding requires lease acquisition for managed retirement');
    return this.#active.pair;
  }

  acquire(): ObjectStorageLease<T> {
    const entry = this.#active;
    if (!entry) throw new ObjectStorageBindingError('Object-storage binding is not ready');
    entry.leases++;
    let released = false;
    const lease: ObjectStorageLease<T> = {
      current: () => {
        if (released) throw new ObjectStorageBindingError('Object-storage lease is released');
        return entry.pair;
      },
      release: () => {
        if (released) return;
        released = true;
        entry.leases--;
        this.#scheduleRetirement(entry);
      },
    };
    Object.defineProperty(lease, 'toJSON', {value: () => '[ObjectStorageLease redacted]'});
    Object.defineProperty(lease, Symbol.for('nodejs.util.inspect.custom'), {value: () => '[ObjectStorageLease redacted]'});
    return Object.freeze(lease);
  }

  /** Wait for admitted factories and retired leases/cleanup, ignoring the current
   * client. Stop serving/reloading and invalidate first for shutdown. Cancelling
   * only ends this wait. Callback failures are sticky; uncertain cleanup is not retried. */
  async waitForRetirement(signal?: AbortSignal): Promise<void> {
    while (true) {
      if (signal?.aborted) throw new ObjectStorageBindingError('Object-storage retirement wait cancelled');
      if (this.#building === 0 && this.#retired.size === 0) {
        if (this.#retirementFailed) throw new ObjectStorageBindingError('Object-storage client retirement failed');
        return;
      }
      await new Promise<void>((resolve, reject) => {
        const cleanup = (): void => {this.#waiters.delete(changed); signal?.removeEventListener('abort', aborted);};
        const changed = (): void => {cleanup(); resolve();};
        const aborted = (): void => {cleanup(); reject(new ObjectStorageBindingError('Object-storage retirement wait cancelled'));};
        this.#waiters.add(changed);
        signal?.addEventListener('abort', aborted, {once: true});
        if (signal?.aborted) aborted();
      });
    }
  }

  #notify(): void {
    const waiters = [...this.#waiters];
    this.#waiters.clear();
    for (const wake of waiters) wake();
  }

  #clearActive(): void {
    if (!this.#active) return;
    const entry = this.#active;
    this.#active = undefined;
    this.#retireClient(entry);
  }

  #retireClient(entry: StorageClient<T>): void {
    if (entry.retired) return;
    entry.retired = true;
    this.#retired.add(entry);
    this.#scheduleRetirement(entry);
    this.#notify();
  }

  #scheduleRetirement(entry: StorageClient<T>): void {
    if (!entry.retired || entry.leases !== 0 || entry.scheduled) return;
    entry.scheduled = true;
    if (!this.#retire) {this.#retired.delete(entry); this.#notify(); return;}
    void this.#disposeClient(entry);
  }

  async #disposeClient(entry: StorageClient<T>): Promise<void> {
    let failed = false;
    // Invoke outside the synchronous state mutation, even for a sync disposer.
    try {await Promise.resolve().then(() => this.#retire!(entry.pair.client));}
    catch {failed = true;}
    this.#retired.delete(entry);
    this.#retirementFailed ||= failed;
    this.#notify();
    if (failed && this.#onRetireError) {
      try {await this.#onRetireError(new ObjectStorageBindingError('Object-storage client retirement failed'));} catch { /* Observer cannot poison cleanup. */ }
    }
  }
}
