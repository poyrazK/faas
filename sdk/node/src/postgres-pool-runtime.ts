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
