import { createOpenAPIConfig, type OpenAPIConfig } from './generated/core/OpenAPI.js';
import { createHttpTransport, type FaaSClientOptions } from './http-transport.js';
import type { IdempotencyKey } from './idempotency.js';
import type { CancelablePromise } from './generated/core/CancelablePromise.js';
import type { RequestOptions } from './request-options.js';
import { scopedRequest } from './request-scope.js';

type WithOptions<T> = T extends (...args: infer Args) => CancelablePromise<infer Result>
  ? (...args: Args extends [] ? [params?: undefined, options?: RequestOptions] : [...Args, options?: RequestOptions]) => CancelablePromise<Result>
  : T;

type Methods<T> = {
  readonly [K in keyof T as T[K] extends (...args: never[]) => unknown ? K : never]: WithOptions<T[K]>;
};

/** Internal transport snapshot; independent clients always supply FETCH. */
export type ClientConfig = Readonly<OpenAPIConfig> & {
  readonly FETCH: typeof fetch;
  readonly timeoutMs?: number;
  readonly requestFetch: (options: RequestOptions) => typeof fetch;
};

/** Bind a generated service to a configuration provider. */
export function bindService<T extends object>(service: T, getConfig: () => ClientConfig): Methods<T> {
  const methods: Record<string, unknown> = {};
  for (const key of Object.getOwnPropertyNames(service)) {
    const method: unknown = Reflect.get(service, key);
    if (typeof method === 'function') methods[key] = (params: unknown, options?: RequestOptions) => {
      const config = getConfig();
      // Copy mutable caller-owned options before the generated request begins.
      const snapshot = options && { ...options, retry: options.retry && { ...options.retry } };
      const receiver = Object.create(service) as object;
      Object.defineProperty(receiver, 'clientConfig', { value: {
        ...config, REQUEST: <Result>(api: Parameters<NonNullable<OpenAPIConfig['REQUEST']>>[0]) => scopedRequest<Result>(config, api, snapshot),
      } });
      return method.call(receiver, params);
    };
  }
  return Object.freeze(methods) as Methods<T>;
}

export type BoundClient<Services> = {
  readonly [K in keyof Services as K extends `${infer Name}Service` ? Uncapitalize<Name> : never]: Methods<Services[K]>;
} & {
  /** Applies to future requests; in-flight requests keep their original token. */
  setToken(token: string | undefined): void;
  /** Applies to future requests, including their retries. */
  setBaseURL(baseURL: string): void;
  /** Pin a key for one logical operation, or clear the default with undefined.
   * Explicit per-request keys take precedence. Otherwise each mutation gets a new key. */
  setIdempotencyKey(key: IdempotencyKey | undefined): void;
};

function normalizeBaseURL(value: string): string {
  const url = new URL(value);
  if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.href.includes('?') || url.href.includes('#')) {
    throw new TypeError('SDK base URL must be HTTP(S) without credentials, query, or fragment');
  }
  return url.href.replace(/\/+$/, '');
}

/** Internal shared builder for public and preview service surfaces. Each call
 * snapshots its config before the generated request starts resolving headers.
 * Replacing config on updates also isolates requests waiting to retry.
 */
export function bindClient<Services extends Record<string, object>, Helpers extends object = object>(
  services: Services,
  baseURL: string,
  options: FaaSClientOptions = {},
  helpers?: (getConfig: () => ClientConfig) => Helpers,
): BoundClient<Services> & Helpers {
  const transportOptions = { ...options, retry: options.retry && { ...options.retry } };
  const transports = (key?: IdempotencyKey) => ({
    FETCH: createHttpTransport(transportOptions, () => key),
    requestFetch: (requestOptions: RequestOptions) => createHttpTransport(transportOptions, () => key, requestOptions),
  });
  let config: ClientConfig = Object.freeze({
    ...createOpenAPIConfig(),
    BASE: normalizeBaseURL(baseURL),
    TOKEN: options.token,
    ENCODE_PATH: encodeURIComponent,
    timeoutMs: options.timeoutMs,
    ...transports(),
  });
  const client: Record<string, unknown> = {
    setToken(token: string | undefined) {
      config = Object.freeze({ ...config, TOKEN: token });
    },
    setBaseURL(value: string) {
      config = Object.freeze({ ...config, BASE: normalizeBaseURL(value) });
    },
    setIdempotencyKey(key: IdempotencyKey | undefined) {
      config = Object.freeze({ ...config, ...transports(key) });
    },
  };
  for (const [name, service] of Object.entries(services)) {
    if (!name.endsWith('Service')) throw new Error(`Unexpected SDK service: ${name}`);
    const namespace = name[0]!.toLowerCase() + name.slice(1, -'Service'.length);
    if (namespace in client) throw new Error(`Duplicate SDK namespace: ${namespace}`);
    client[namespace] = bindService(service, () => config);
  }
  for (const [name, helper] of Object.entries(helpers?.(() => config) ?? {})) {
    if (name in client) throw new Error(`Duplicate SDK helper: ${name}`);
    client[name] = helper;
  }
  return Object.freeze(client) as BoundClient<Services> & Helpers;
}
