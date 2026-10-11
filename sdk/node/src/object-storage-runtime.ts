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
  #active: Readonly<{client: T; bucket: string}> | undefined;
  #pending: Promise<void> = Promise.resolve();

  constructor(prefix: string, factory: (config: ObjectStorageClientConfig, bucket: string, signal?: AbortSignal) => T | Promise<T>) {
    if (!/^[A-Z][A-Z0-9_]{0,47}$/.test(prefix) || typeof factory !== 'function') throw new ObjectStorageBindingError('Invalid object-storage binding');
    this.#prefix = prefix;
    this.#factory = factory;
  }

  /** Use one atomic secret envelope. ACK its revision only after this resolves
   * and all other consumers apply. Reacquire current() for new requests; callers
   * own retired clients. Failure disables acquisition, including revocation. */
  reload(values: Readonly<Record<string, string | undefined>>, signal?: AbortSignal): Promise<void> {
    const snapshot = {...values};
    const apply = async (): Promise<void> => {
      this.#active = undefined;
      if (signal?.aborted) throw new ObjectStorageBindingError('Object-storage reload cancelled');
      const {config, bucket} = configuration(this.#prefix, snapshot);
      let client: T;
      try { client = await this.#factory(config, bucket, signal); }
      catch { throw new ObjectStorageBindingError('Object-storage client could not be configured'); }
      if (signal?.aborted) throw new ObjectStorageBindingError('Object-storage reload cancelled');
      if (client === undefined || client === null) throw new ObjectStorageBindingError('Object-storage client is unavailable');
      this.#active = Object.freeze({client, bucket});
    };
    const result = this.#pending.then(apply);
    this.#pending = result.catch(() => {});
    return result;
  }

  current(): Readonly<{client: T; bucket: string}> {
    if (!this.#active) throw new ObjectStorageBindingError('Object-storage binding is not ready');
    return this.#active;
  }
}
