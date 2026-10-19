import {ClientBinding} from './binding-client-runtime.js';
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
  readonly #core: ClientBinding<T, ObjectStorageClientConfig, string>;
  constructor(prefix: string, factory: (config: ObjectStorageClientConfig, bucket: string, signal?: AbortSignal) => T | Promise<T>, options: ObjectStorageBindingOptions<T> = {}) {
    if (!/^[A-Z][A-Z0-9_]{0,47}$/.test(prefix) || typeof factory !== 'function' || !options ||
      options.retire !== undefined && typeof options.retire !== 'function' || options.onRetireError !== undefined && typeof options.onRetireError !== 'function') throw new ObjectStorageBindingError('Invalid object-storage binding');
    this.#core = new ClientBinding(values => {const {config, bucket} = configuration(prefix, values); return {config, value: bucket};}, factory,
      message => new ObjectStorageBindingError(`Object-storage ${message}`), options);
  }
  /** Adopt one atomic envelope; register secretConsumer() for coordinated ACKs. */
  reload(values: Readonly<Record<string, string | undefined>>, signal?: AbortSignal): Promise<void> {return this.#core.reload(values, signal);}
  invalidate(): void {this.#core.invalidate();}
  secretConsumer(): SecretConsumer {return this.#core.secretConsumer();}
  current(): Readonly<{client: T; bucket: string}> {
    const {client, value: bucket} = this.#core.current();
    return Object.freeze({client, bucket});
  }
  acquire(): ObjectStorageLease<T> {
    const inner = this.#core.acquire();
    const lease: ObjectStorageLease<T> = {
      current: () => {const {client, value: bucket} = inner.current(); return Object.freeze({client, bucket});},
      release: () => inner.release(),
    };
    Object.defineProperty(lease, 'toJSON', {value: () => '[ObjectStorageLease redacted]'});
    Object.defineProperty(lease, Symbol.for('nodejs.util.inspect.custom'), {value: () => '[ObjectStorageLease redacted]'});
    return Object.freeze(lease);
  }
  /** Stop requests/reloads and invalidate before draining shutdown. */
  waitForRetirement(signal?: AbortSignal): Promise<void> {return this.#core.waitForRetirement(signal);}
}
