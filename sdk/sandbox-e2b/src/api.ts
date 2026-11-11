// HTTP access to Gregale sandbox sessions (ADR-20261027090013 §3): the
// control plane with the API key, the data plane with the session's token.

import {
  AuthenticationError,
  InvalidArgumentError,
  NotFoundError,
  RateLimitError,
  SandboxError,
  SandboxNotFoundError,
  TimeoutError,
  UnsupportedError,
} from './errors.js';

export const DEFAULT_API_BASE = 'https://api.gregale.dev';

export type Problem = { title?: string; status?: number; detail?: string; code?: string };

/** The API's answer to a refused request. */
export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly problem: Problem;
  constructor(status: number, problem: Problem) {
    super(problem.detail ?? problem.title ?? `Gregale API error ${status}`);
    this.status = status;
    this.code = problem.code ?? '';
    this.problem = problem;
  }
}

/** An API refusal as the E2B-style error a caller catches. */
export function toSandboxError(err: unknown, what: string): unknown {
  if (!(err instanceof ApiError)) return err;
  const detail = `${what}: ${err.message}${err.code ? ` (${err.code})` : ''}`;
  switch (true) {
    case err.status === 401:
      return new AuthenticationError(detail);
    case err.code === 'sandbox_not_found' || (err.status === 404 && err.code === ''):
      return new SandboxNotFoundError(detail);
    case err.code === 'sandbox_entry_not_found' || err.status === 404:
      return new NotFoundError(detail);
    case err.code === 'sandbox_unsupported':
      return new UnsupportedError(what, err.message);
    case err.status === 429 || err.code === 'agent_limit':
      return new RateLimitError(detail);
    case err.status === 504:
      return new TimeoutError(detail);
    case err.status === 400 || err.status === 422:
      return new InvalidArgumentError(detail);
  }
  return new SandboxError(detail);
}

export type ApiOptions = {
  /** The Gregale API key; GREGALE_API_KEY when unset. */
  apiKey?: string;
  /** The API's origin; GREGALE_API_BASE, then https://api.gregale.dev. */
  apiBase?: string;
  /** For tests and custom transports. */
  fetch?: typeof fetch;
};

export type RequestArgs = {
  query?: Record<string, string | number | boolean | undefined>;
  json?: unknown;
  body?: Uint8Array;
  contentType?: string;
  accept?: string;
  signal?: AbortSignal;
  timeoutMs?: number;
};

export class Api {
  readonly apiBase: string;
  readonly #apiKey: string;
  readonly #fetch: typeof fetch;

  constructor(options: ApiOptions = {}) {
    const apiKey = options.apiKey ?? process.env.GREGALE_API_KEY;
    if (!apiKey) throw new AuthenticationError('a Gregale API key is needed: pass apiKey or set GREGALE_API_KEY');
    this.#apiKey = apiKey;
    this.apiBase = (options.apiBase ?? process.env.GREGALE_API_BASE ?? DEFAULT_API_BASE).replace(/\/+$/, '');
    this.#fetch = options.fetch ?? fetch;
  }

  async control<T>(method: string, path: string, args: RequestArgs = {}): Promise<T> {
    const res = await this.send(method, path, this.#apiKey, args);
    return (res.status === 204 ? undefined : await res.json()) as T;
  }

  async data(method: string, path: string, token: string, args: RequestArgs = {}): Promise<Response> {
    return await this.send(method, path, token, args);
  }

  private async send(method: string, path: string, bearer: string, args: RequestArgs): Promise<Response> {
    const url = new URL(this.apiBase + path);
    for (const [k, v] of Object.entries(args.query ?? {})) {
      if (v !== undefined) url.searchParams.set(k, String(v));
    }
    const headers: Record<string, string> = { Authorization: `Bearer ${bearer}`, Accept: args.accept ?? 'application/json' };
    let body: BodyInit | undefined;
    if (args.json !== undefined) {
      headers['Content-Type'] = 'application/json';
      body = JSON.stringify(args.json);
    } else if (args.body !== undefined) {
      headers['Content-Type'] = args.contentType ?? 'application/octet-stream';
      body = args.body;
    }
    const signals = [args.signal, args.timeoutMs ? AbortSignal.timeout(args.timeoutMs) : undefined].filter((s): s is AbortSignal => !!s);
    const signal = signals.length > 1 ? AbortSignal.any(signals) : signals[0];
    let res: Response;
    try {
      res = await this.#fetch(url, { method, headers, body, signal });
    } catch (err) {
      if (err instanceof Error && err.name === 'TimeoutError') throw new TimeoutError(`${method} ${path} timed out`);
      throw err;
    }
    if (!res.ok) {
      let problem: Problem = { status: res.status };
      try {
        problem = (await res.json()) as Problem;
      } catch {
        // Not a problem document.
      }
      throw new ApiError(res.status, problem);
    }
    return res;
  }
}
