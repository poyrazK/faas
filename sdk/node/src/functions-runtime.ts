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
// `gregale function types` declares each function's input and output in
// GregaleFunctions, so `functions.users.call` is typed.

import { createGregaleFetch } from './release-context.js';

/** Header asking the service proxy to enqueue an async invocation. */
export const GREGALE_INVOKE_HEADER = 'X-Gregale-Invoke';
/** Marks a typed-client call; the callee checks only these against its input schema. */
export const GREGALE_FUNCTION_CALL_HEADER = 'X-Gregale-Function-Call';
/** Env variable a deploy sets: callable name -> app slug. */
export const GREGALE_FUNCTIONS_ENV = 'GREGALE_FUNCTIONS';

/**
 * The functions this function may call, by name. `gregale function types`
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

/** Names the function that receives a send's completion. */
export const GREGALE_ON_COMPLETE_HEADER = 'X-Gregale-On-Complete';
/** Where a function reads the invocations it sent. */
export const GREGALE_PLATFORM_API_URL = 'http://api.svc.gregale:10081';

/** What a function reads about an invocation it sent. */
export interface FunctionInvocationStatus {
  id: string;
  state: string;
  done: boolean;
  outcome?: string;
  attempts: number;
  result?: unknown;
  error?: string;
  completed_at?: string;
}

/** An accepted async invocation. */
export interface FunctionInvocation<Out = unknown> {
  invocationId: string;
  /** The invocation's current state, read from this function's VM. */
  status(): Promise<FunctionInvocationStatus>;
  /**
   * Wait until the invocation finishes and return its result. A failed
   * invocation raises FunctionCallError; timeoutMs (default 60 s) raises one
   * with status 408 when it passes first.
   */
  wait(options?: { timeoutMs?: number; signal?: AbortSignal }): Promise<Out>;
}

/** One callable function. */
export interface FunctionClient<T extends FunctionTypes = FunctionTypes> {
  /** POST the input and return the parsed output. */
  call(input: T['input'], options?: FunctionCallOptions): Promise<T['output']>;
  /** Enqueue a durable async invocation and return at once. */
  send(input: T['input'], options?: FunctionSendOptions): Promise<FunctionInvocation<T['output']>>;
  /**
   * Start a run of a workflow function (`workflow: true`) and return its
   * handle at once. Only this function reads or signals the run.
   */
  start(input: T['input'], options?: FunctionStartOptions): Promise<FunctionWorkflowRun<T['output']>>;
}

export interface FunctionStartOptions {
  /** Makes retried starts return the same run. */
  idempotencyKey?: string;
  signal?: AbortSignal;
}

/** What a function reads about a workflow run it started. */
export interface FunctionWorkflowRunStatus {
  id: string;
  status: string;
  done: boolean;
  output?: unknown;
  error?: string;
}

/** A started workflow run. */
export interface FunctionWorkflowRun<Out = unknown> {
  runId: string;
  status(): Promise<FunctionWorkflowRunStatus>;
  /**
   * Wait until the run finishes and return its output. A failed run raises
   * FunctionCallError; timeoutMs (default 60 s) raises one with status 408
   * when it passes first.
   */
  wait(options?: { timeoutMs?: number; signal?: AbortSignal }): Promise<Out>;
  /** Send the run an event its wf.waitForEvent may take. */
  signal(event: string, payload?: unknown): Promise<void>;
}

export interface FunctionSendOptions extends FunctionCallOptions {
  /**
   * A function that receives the completion when the invocation finishes:
   * a name this function may call, or "self".
   */
  onComplete?: string;
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
  /** Where sent invocations are read; defaults to GREGALE_PLATFORM_API_URL. */
  platformURL?: string;
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
  const platformURL = (options.platformURL ?? env.GREGALE_PLATFORM_API_URL ?? GREGALE_PLATFORM_API_URL).replace(/\/+$/, '');
  const clients = new Map<string, FunctionClient>();
  const clientFor = (name: string): FunctionClient => {
    const cached = clients.get(name);
    if (cached) return cached;
    const slug = names[name];
    if (!slug) {
      const known = Object.keys(names).sort();
      throw new Error(`function ${name} is not callable from here; ${known.length ? `callable: ${known.join(', ')}` : 'GREGALE_FUNCTIONS is not set'} (deploy the functions project, or add the app to calls:)`);
    }
    const request = async (input: unknown, opts: FunctionSendOptions | undefined, async: boolean): Promise<Response> => {
      const path = opts?.path ?? '/';
      const headers: Record<string, string> = { 'content-type': 'application/json', accept: 'application/json', ...opts?.headers };
      headers[GREGALE_FUNCTION_CALL_HEADER] = '1';
      if (async) headers[GREGALE_INVOKE_HEADER] = 'async';
      if (async && opts?.onComplete) headers[GREGALE_ON_COMPLETE_HEADER] = opts.onComplete;
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
        return sentInvocation(name, id, platformURL, fetchImpl);
      },
      async start(input, opts) {
        const headers: Record<string, string> = { 'content-type': 'application/json', accept: 'application/json' };
        if (opts?.idempotencyKey) headers['idempotency-key'] = opts.idempotencyKey;
        const body = await platformJSON(name, fetchImpl, `${platformURL}/v1/workflow-runs`, {
          method: 'POST', headers, body: JSON.stringify({ function: name, input: input ?? null }), ...(opts?.signal ? { signal: opts.signal } : {}),
        }) as FunctionWorkflowRunStatus;
        if (typeof body?.id !== 'string' || !body.id) throw new FunctionCallError(name, 500, undefined, 'workflow run id missing');
        return startedRun(name, body.id, platformURL, fetchImpl);
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

/** The longest single long-poll the platform holds, in seconds. */
const MAX_WAIT_SECONDS = 30;

function sentInvocation<Out>(fn: string, id: string, platformURL: string, fetchImpl: typeof globalThis.fetch): FunctionInvocation<Out> {
  const read = async (waitSeconds: number, signal?: AbortSignal): Promise<FunctionInvocationStatus> => {
    const url = `${platformURL}/v1/invocations/${encodeURIComponent(id)}` + (waitSeconds > 0 ? `?wait=${waitSeconds}` : '');
    const res = await fetchImpl(url, { headers: { accept: 'application/json' }, ...(signal ? { signal } : {}) });
    const text = await res.text();
    let body: unknown;
    try {
      body = JSON.parse(text);
    } catch {
      body = undefined;
    }
    if (!res.ok) {
      const problem = body && typeof body === 'object' && !Array.isArray(body) ? (body as Record<string, unknown>) : undefined;
      throw new FunctionCallError(fn, res.status, problem, text);
    }
    return body as FunctionInvocationStatus;
  };
  return {
    invocationId: id,
    status: () => read(0),
    async wait(options) {
      const deadline = Date.now() + (options?.timeoutMs ?? 60_000);
      for (;;) {
        const left = Math.ceil((deadline - Date.now()) / 1000);
        const status = await read(Math.max(0, Math.min(MAX_WAIT_SECONDS, left)), options?.signal);
        if (status.done) {
          if (status.state === 'completed') return status.result as Out;
          throw new FunctionCallError(fn, 500, { code: 'function_invocation_failed', detail: status.error ?? status.state, invocation: status }, status.error ?? '');
        }
        if (Date.now() >= deadline) {
          throw new FunctionCallError(fn, 408, { code: 'function_invocation_pending', detail: `invocation ${id} is still ${status.state}`, invocation: status }, '');
        }
      }
    },
  };
}

async function platformJSON(fn: string, fetchImpl: typeof globalThis.fetch, url: string, init: RequestInit): Promise<unknown> {
  const res = await fetchImpl(url, init);
  const text = await res.text();
  let body: unknown;
  try {
    body = text ? JSON.parse(text) : undefined;
  } catch {
    body = undefined;
  }
  if (!res.ok) {
    const problem = body && typeof body === 'object' && !Array.isArray(body) ? (body as Record<string, unknown>) : undefined;
    throw new FunctionCallError(fn, res.status, problem, text);
  }
  return body;
}

function startedRun<Out>(fn: string, id: string, platformURL: string, fetchImpl: typeof globalThis.fetch): FunctionWorkflowRun<Out> {
  const base = `${platformURL}/v1/workflow-runs/${encodeURIComponent(id)}`;
  const read = async (waitSeconds: number, signal?: AbortSignal) => (await platformJSON(fn, fetchImpl, base + (waitSeconds > 0 ? `?wait=${waitSeconds}` : ''),
    { headers: { accept: 'application/json' }, ...(signal ? { signal } : {}) })) as FunctionWorkflowRunStatus;
  return {
    runId: id,
    status: () => read(0),
    async wait(options) {
      const deadline = Date.now() + (options?.timeoutMs ?? 60_000);
      for (;;) {
        const left = Math.ceil((deadline - Date.now()) / 1000);
        const status = await read(Math.max(0, Math.min(MAX_WAIT_SECONDS, left)), options?.signal);
        if (status.done) {
          if (status.status === 'succeeded') return status.output as Out;
          throw new FunctionCallError(fn, 500, { code: 'workflow_run_failed', detail: status.error ?? status.status, run: status }, status.error ?? '');
        }
        if (Date.now() >= deadline) {
          throw new FunctionCallError(fn, 408, { code: 'workflow_run_pending', detail: `workflow run ${id} is still ${status.status}`, run: status }, '');
        }
      }
    },
    async signal(event, payload) {
      await platformJSON(fn, fetchImpl, `${base}/events`, {
        method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ event, payload: payload ?? {} }),
      });
    },
  };
}
