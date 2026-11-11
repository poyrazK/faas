// src/functions-runtime.ts — typed calls between functions of one project
// (ADR-20261111150000).
//
//   import { functions } from '@gregale/sdk-node/functions/runtime';
//   const user = await functions.users.call({ id: '42' });
//   const { invocationId } = await functions.mailer.send({ to: user.email });
//
// A deploy binds each function to its siblings (and its `calls:` apps) and
// sets GREGALE_FUNCTIONS, a JSON map from each callable name to its app slug.
// Calls go over the private binding (`http://<slug>.internal:10081`), never
// the public internet; the platform authorizes them by the calling VM, wakes
// a parked callee, and the request's deadline and release travel with them.
// `gregale functions types` declares each function's input and output in
// GregaleFunctions, so `functions.users.call` is typed.

import { createGregaleFetch } from './release-context.js';

/** Header asking the service proxy to enqueue an async invocation. */
export const GREGALE_INVOKE_HEADER = 'X-Gregale-Invoke';
/** Env variable a deploy sets: callable name -> app slug. */
export const GREGALE_FUNCTIONS_ENV = 'GREGALE_FUNCTIONS';

/**
 * The functions this function may call, by name. `gregale functions types`
 * augments it from each function's input and output schema; without it every
 * name is callable with unknown input and output.
 */
// eslint-disable-next-line @typescript-eslint/no-empty-interface
export interface GregaleFunctions {}

/** A function's input and output types. */
export interface FunctionTypes {
  input: unknown;
  output: unknown;
}

/** The callee answered with a non-2xx status. */
export class FunctionCallError extends Error {
  readonly status: number;
  readonly code: string;
  readonly problem: Record<string, unknown> | undefined;
  readonly function: string;

  constructor(fn: string, status: number, problem: Record<string, unknown> | undefined, body: string) {
    const code = typeof problem?.code === 'string' ? problem.code : '';
    const detail = typeof problem?.detail === 'string' ? problem.detail : body.slice(0, 200);
    super(`function ${fn} answered ${status}${code ? ` (${code})` : ''}${detail ? `: ${detail}` : ''}`);
    this.name = 'FunctionCallError';
    this.status = status;
    this.code = code;
    this.problem = problem;
    this.function = fn;
  }
}

/** An accepted async invocation. */
export interface FunctionInvocation {
  invocationId: string;
}

/** One callable function. */
export interface FunctionClient<T extends FunctionTypes = FunctionTypes> {
  /** POST the input and return the parsed output. */
  call(input: T['input'], options?: FunctionCallOptions): Promise<T['output']>;
  /** Enqueue a durable async invocation and return at once. */
  send(input: T['input'], options?: FunctionCallOptions): Promise<FunctionInvocation>;
}

export interface FunctionCallOptions {
  /** Request path on the callee; defaults to "/". */
  path?: string;
  /** Extra request headers. */
  headers?: Record<string, string>;
  signal?: AbortSignal;
}

/**
 * Typed by GregaleFunctions (or an explicit map); untyped, every name is a
 * FunctionClient with unknown input and output.
 */
export type FunctionsClient<M> = [keyof M] extends [never]
  ? { readonly [name: string]: FunctionClient }
  : { readonly [K in keyof M]: FunctionClient<M[K] extends FunctionTypes ? M[K] : FunctionTypes> };

export interface CreateFunctionsOptions {
  /** The name -> slug map; defaults to GREGALE_FUNCTIONS. */
  functions?: Record<string, string>;
  /** Base URL for a slug; defaults to its binding URL. */
  baseURL?: (slug: string) => string;
  /** fetch used under the release transport. */
  fetch?: typeof globalThis.fetch;
  env?: Record<string, string | undefined>;
}

/** Parses GREGALE_FUNCTIONS; a missing or malformed value is an empty map. */
export function functionsFromEnv(env: Record<string, string | undefined> = process.env): Record<string, string> {
  const raw = env[GREGALE_FUNCTIONS_ENV];
  if (!raw) return {};
  try {
    const parsed: unknown = JSON.parse(raw);
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return {};
    const out: Record<string, string> = {};
    for (const [name, slug] of Object.entries(parsed as Record<string, unknown>)) {
      if (typeof slug === 'string' && slug) out[name] = slug;
    }
    return out;
  } catch {
    return {};
  }
}

/** The binding URL for slug: its injected GREGALE_SERVICE_<SLUG>_URL, else the alias. */
export function functionBaseURL(slug: string, env: Record<string, string | undefined> = process.env): string {
  const key = 'GREGALE_SERVICE_' + slug.toUpperCase().replace(/[^A-Z0-9]/g, '_') + '_URL';
  return (env[key] || `http://${slug}.internal:10081`).replace(/\/+$/, '');
}

/** Creates a client for the functions this function may call. */
export function createFunctions<M = GregaleFunctions>(options: CreateFunctionsOptions = {}): FunctionsClient<M> {
  const env = options.env ?? process.env;
  const names = options.functions ?? functionsFromEnv(env);
  const baseURL = options.baseURL ?? ((slug: string) => functionBaseURL(slug, env));
  const fetchImpl = createGregaleFetch(options.fetch ?? globalThis.fetch);
  const clients = new Map<string, FunctionClient>();
  const clientFor = (name: string): FunctionClient => {
    const cached = clients.get(name);
    if (cached) return cached;
    const slug = names[name];
    if (!slug) {
      const known = Object.keys(names).sort();
      throw new Error(`function ${name} is not callable from here; ${known.length ? `callable: ${known.join(', ')}` : 'GREGALE_FUNCTIONS is not set'} (deploy the functions project, or add the app to calls:)`);
    }
    const request = async (input: unknown, opts: FunctionCallOptions | undefined, async: boolean): Promise<Response> => {
      const path = opts?.path ?? '/';
      const headers: Record<string, string> = { 'content-type': 'application/json', accept: 'application/json', ...opts?.headers };
      if (async) headers[GREGALE_INVOKE_HEADER] = 'async';
      const res = await fetchImpl(baseURL(slug) + (path.startsWith('/') ? path : '/' + path), {
        method: 'POST', headers, body: JSON.stringify(input ?? null), ...(opts?.signal ? { signal: opts.signal } : {}),
      });
      if (res.ok) return res;
      const body = await res.text();
      let problem: Record<string, unknown> | undefined;
      try {
        const parsed: unknown = JSON.parse(body);
        if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) problem = parsed as Record<string, unknown>;
      } catch {
        problem = undefined;
      }
      throw new FunctionCallError(name, res.status, problem, body);
    };
    const client: FunctionClient = {
      async call(input, opts) {
        const res = await request(input, opts, false);
        const text = await res.text();
        if (!text) return undefined;
        return (res.headers.get('content-type') ?? '').includes('json') ? JSON.parse(text) : text;
      },
      async send(input, opts) {
        const res = await request(input, opts, true);
        const body = (await res.json().catch(() => ({}))) as Record<string, unknown>;
        const id = body.invocation_id ?? body.id;
        if (typeof id !== 'string' || !id) throw new FunctionCallError(name, res.status, body, 'async invocation id missing');
        return { invocationId: id };
      },
    };
    clients.set(name, client);
    return client;
  };
  return new Proxy({} as FunctionsClient<M>, {
    get(_target, prop) {
      if (typeof prop !== 'string' || prop === 'then') return undefined;
      return clientFor(prop);
    },
  });
}

/** The client for this function's siblings, from GREGALE_FUNCTIONS. */
export const functions: FunctionsClient<GregaleFunctions> = createFunctions();
