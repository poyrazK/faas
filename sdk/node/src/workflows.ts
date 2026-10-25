// Code-first workflows (ADR-20261021204307).
//
// A workflow is an async function that calls `wf.step`, `wf.sleep` and
// `wf.waitForEvent`. Gregale calls the workflow's entry handler at every
// durable boundary with the run's journal. Recorded operations return their
// stored result; the first new ones run (steps) or are reported (waits), and
// the function is suspended until Gregale calls again. Code outside steps
// must therefore be deterministic and must not await I/O.

import type { IncomingMessage, ServerResponse } from 'node:http';

export const WORKFLOW_CODE_PROTOCOL = 'gregale.workflow.code/v1';

const OP_NAME = /^[A-Za-z0-9][A-Za-z0-9_.:#/-]{0,127}$/;
const MAX_REQUEST_BYTES = 8 << 20;
// One call's answer must fit the platform's 960 KiB response bound.
export const WORKFLOW_RESPONSE_MAX_BYTES = 960 * 1024;

/** Milliseconds, or a string such as "30s", "15m", "2h" or "3d". */
export type Duration = number | string;

export interface RetryPolicy {
  /** Total attempts, 1-25. */
  maxAttempts: number;
  backoff?: 'fixed' | 'exponential';
}

export interface StepOptions {
  /** Overrides the workflow's `code.retry` policy for this step. */
  retry?: RetryPolicy;
}

export interface StepContext {
  /** 1 on the first attempt. */
  readonly attempt: number;
  /** `workflow/<run-id>/<step>`, stable across attempts; pass it to external APIs. */
  readonly idempotencyKey: string;
}

export interface WaitForEventOptions {
  /** Event name to wait for; defaults to the operation name. */
  event?: string;
  /** Defaults to the plan's maximum wait. */
  timeout?: Duration;
}

export interface WorkflowContext {
  readonly runId: string;
  readonly workflow: string;
  /** 1 on the first call of this run's entry handler. */
  readonly invocation: number;
  /** Runs `fn` once (at least once on failure) and records its JSON result. */
  step<T>(name: string, fn: (context: StepContext) => T | Promise<T>, options?: StepOptions): Promise<T>;
  /** Parks the run; its app may scale to zero meanwhile. */
  sleep(name: string, duration: Duration): Promise<void>;
  /**
   * Resolves with the payload of the next event of that name sent to the run
   * (`gregale workflows events RUN_ID NAME`, or `POST /v1/workflows/runs/{id}/events`),
   * or null on timeout. Each wait for a name consumes one event of that name.
   */
  waitForEvent<T = unknown>(name: string, options?: WaitForEventOptions): Promise<T | null>;
}

export interface WorkflowDefinition<I = unknown, O = unknown> {
  readonly name: string;
  readonly run: (input: I, wf: WorkflowContext) => Promise<O> | O;
}

/** Fails a step without retrying it. */
export class NonRetryableError extends Error {
  override readonly name = 'NonRetryableError';
}

/** Thrown into the workflow when a step has exhausted its attempts. */
export class WorkflowStepError extends Error {
  override readonly name = 'WorkflowStepError';
  constructor(readonly step: string, message: string, readonly attempts: number) {
    super(message);
  }
}

export function defineWorkflow<I = unknown, O = unknown>(name: string, run: WorkflowDefinition<I, O>['run']): WorkflowDefinition<I, O> {
  if (!name) throw new Error('workflow name is required');
  return { name, run };
}

type Kind = 'step' | 'sleep' | 'event';

export interface JournalEntry {
  seq: number;
  name: string;
  kind: Kind;
  /** `scheduled` is a retrying step whose back-off has not elapsed yet. */
  status: 'succeeded' | 'failed' | 'retrying' | 'scheduled' | 'waiting';
  attempt?: number;
  output?: unknown;
  error?: string;
  timed_out?: boolean;
  event?: string;
}

export interface WorkflowInvocationRequest {
  protocol: string;
  run_id: string;
  workflow: string;
  input: unknown;
  invocation: number;
  journal: JournalEntry[];
}

export interface ReportedOperation {
  seq: number;
  name: string;
  kind: Kind;
  status?: 'succeeded' | 'failed';
  output?: unknown;
  error?: string;
  non_retryable?: boolean;
  retry?: { max_attempts: number; backoff?: string };
  duration_ms?: number;
  event?: string;
  timeout_ms?: number;
}

export interface WorkflowInvocationResponse {
  protocol: string;
  ops: ReportedOperation[];
  result: { status: 'running' | 'succeeded' | 'failed'; output?: unknown; error?: string };
}

/** Parses a Duration into milliseconds. */
export function parseDuration(value: Duration): number {
  if (typeof value === 'number') {
    if (!Number.isFinite(value) || value < 0) throw new Error(`invalid duration ${value}`);
    return Math.round(value);
  }
  const match = /^(\d+(?:\.\d+)?)(ms|s|m|h|d)$/.exec(value.trim());
  if (!match) throw new Error(`invalid duration "${value}"; use a number of milliseconds or a value such as "30s", "15m", "2h" or "3d"`);
  const units: Record<string, number> = { ms: 1, s: 1_000, m: 60_000, h: 3_600_000, d: 86_400_000 };
  return Math.round(Number(match[1]) * units[match[2] as string]!);
}

// A promise that never settles: the function is suspended at this await
// until Gregale calls again with the operation's result.
const suspended = <T>(): Promise<T> => new Promise<T>(() => {});

const nextMacrotask = (): Promise<void> => new Promise((resolve) => setImmediate(resolve));

function errorMessage(error: unknown): string {
  if (error instanceof Error) return error.message || error.name;
  return String(error);
}

function toJSONValue(value: unknown): unknown {
  if (value === undefined) return undefined;
  return JSON.parse(JSON.stringify(value)) as unknown;
}

class Invocation implements WorkflowContext {
  readonly ops: ReportedOperation[] = [];
  readonly pending: Promise<void>[] = [];
  activity = 0;
  private seq = 0;
  private readonly names = new Map<string, number>();

  constructor(private readonly request: WorkflowInvocationRequest) {}

  get runId(): string { return this.request.run_id; }
  get workflow(): string { return this.request.workflow; }
  get invocation(): number { return this.request.invocation; }

  // claim assigns the next sequence number. A recorded operation that does
  // not match is reported as asked, so Gregale refuses the replay.
  private claim(name: string, kind: Kind, event?: string): { seq: number; name: string; entry?: JournalEntry; conflict: boolean } {
    if (!OP_NAME.test(name) || name.includes('#')) throw new Error(`invalid workflow operation name "${name}": use 1-128 of A-Z a-z 0-9 _ . : / - starting with a letter or digit; "#" is reserved for repeated names (poll, poll#2, …)`);
    const count = (this.names.get(name) ?? 0) + 1;
    this.names.set(name, count);
    const opName = count === 1 ? name : `${name}#${count}`;
    const seq = this.seq++;
    const entry = this.request.journal[seq];
    const conflict = entry !== undefined && (entry.name !== opName || entry.kind !== kind || (kind === 'event' && entry.event !== event));
    return { seq, name: opName, entry: conflict ? undefined : entry, conflict };
  }

  private report(op: ReportedOperation): void {
    this.ops.push(op);
    this.activity++;
  }

  step<T>(name: string, fn: (context: StepContext) => T | Promise<T>, options?: StepOptions): Promise<T> {
    const { seq, name: opName, entry, conflict } = this.claim(name, 'step');
    if (conflict) {
      this.report({ seq, name: opName, kind: 'step', status: 'succeeded' });
      return suspended<T>();
    }
    if (entry?.status === 'succeeded') return Promise.resolve(entry.output as T);
    if (entry?.status === 'failed') return Promise.reject(new WorkflowStepError(opName, entry.error ?? 'step failed', entry.attempt ?? 1));
    // Its back-off has not elapsed; Gregale calls again when it is due.
    if (entry?.status === 'scheduled') return suspended<T>();
    const attempt = (entry?.attempt ?? 0) + 1;
    const retry = options?.retry ? { max_attempts: options.retry.maxAttempts, ...(options.retry.backoff ? { backoff: options.retry.backoff } : {}) } : undefined;
    this.activity++;
    this.pending.push((async () => {
      try {
        const value = await fn({ attempt, idempotencyKey: `workflow/${this.request.run_id}/${opName}` });
        let output: unknown;
        try {
          output = toJSONValue(value);
        } catch (error) {
          this.report({ seq, name: opName, kind: 'step', status: 'failed', error: `step result is not JSON: ${errorMessage(error)}`, non_retryable: true, ...(retry ? { retry } : {}) });
          return;
        }
        this.report({ seq, name: opName, kind: 'step', status: 'succeeded', ...(output === undefined ? {} : { output }) });
      } catch (error) {
        this.report({ seq, name: opName, kind: 'step', status: 'failed', error: errorMessage(error), ...(error instanceof NonRetryableError ? { non_retryable: true } : {}), ...(retry ? { retry } : {}) });
      }
    })());
    return suspended<T>();
  }

  sleep(name: string, duration: Duration): Promise<void> {
    const { seq, name: opName, entry, conflict } = this.claim(name, 'sleep');
    if (entry?.status === 'succeeded') return Promise.resolve();
    if (!entry) this.report({ seq, name: opName, kind: 'sleep', duration_ms: conflict ? 1_000 : parseDuration(duration) });
    return suspended<void>();
  }

  waitForEvent<T = unknown>(name: string, options?: WaitForEventOptions): Promise<T | null> {
    const event = options?.event ?? name;
    const { seq, name: opName, entry, conflict } = this.claim(name, 'event', event);
    if (entry?.status === 'succeeded') return Promise.resolve(entry.timed_out ? null : ((entry.output ?? null) as T | null));
    if (!entry) {
      const op: ReportedOperation = { seq, name: opName, kind: 'event', event };
      if (options?.timeout !== undefined && !conflict) op.timeout_ms = parseDuration(options.timeout);
      this.report(op);
    }
    return suspended<T | null>();
  }
}

/**
 * Runs one invocation of a workflow against its journal. Use it to serve the
 * protocol from any framework; `serveWorkflows` wraps it for Fetch handlers.
 */
export async function runWorkflowInvocation(definitions: readonly WorkflowDefinition<any, any>[] | WorkflowDefinition<any, any>, body: unknown): Promise<WorkflowInvocationResponse> {
  const request = parseRequest(body);
  const list = Array.isArray(definitions) ? definitions : [definitions as WorkflowDefinition<any, any>];
  const definition = list.find((item) => item.name === request.workflow);
  if (!definition) throw new UnknownWorkflowError(request.workflow);
  const invocation = new Invocation(request);
  let finished: { ok: true; value: unknown } | { ok: false; error: unknown } | undefined;
  const done = (async () => definition.run(request.input, invocation))().then(
    (value) => { finished = { ok: true, value }; },
    (error: unknown) => { finished = { ok: false, error }; },
  );
  // Suspend once the function has finished or nothing can make progress:
  // every started step has settled and a macrotask passed without activity.
  for (;;) {
    const before = invocation.activity;
    const steps = invocation.pending.length;
    await Promise.race([done, Promise.allSettled(invocation.pending)]);
    await Promise.allSettled(invocation.pending);
    await nextMacrotask();
    await nextMacrotask();
    if (finished && invocation.pending.length === steps) break;
    if (invocation.activity === before && invocation.pending.length === steps) break;
  }
  await Promise.allSettled(invocation.pending);
  const ops = [...invocation.ops].sort((a, b) => a.seq - b.seq);
  const response: WorkflowInvocationResponse = { protocol: WORKFLOW_CODE_PROTOCOL, ops, result: { status: 'running' } };
  if (finished?.ok) {
    let output: unknown;
    try {
      output = toJSONValue(finished.value);
      response.result = output === undefined ? { status: 'succeeded' } : { status: 'succeeded', output };
    } catch (error) {
      response.result = { status: 'failed', error: `workflow result is not JSON: ${errorMessage(error)}` };
    }
  } else if (finished && !finished.ok) {
    response.result = { status: 'failed', error: errorMessage(finished.error) };
  }
  return fitResponse(response);
}

const byteLength = (value: unknown): number => Buffer.byteLength(JSON.stringify(value));

// fitResponse fails, without retry, any step result (and the workflow
// output) that would push the answer past WORKFLOW_RESPONSE_MAX_BYTES, so an
// oversized result surfaces as a step error instead of a failed call.
function fitResponse(response: WorkflowInvocationResponse): WorkflowInvocationResponse {
  const budget = WORKFLOW_RESPONSE_MAX_BYTES - 1024;
  let used = byteLength({ protocol: response.protocol, ops: [], result: { status: 'running' } });
  response.ops = response.ops.map((op) => {
    let size = byteLength(op) + 1;
    if (op.kind === 'step' && op.status === 'succeeded' && used + size > budget) {
      const { output: _dropped, ...rest } = op;
      op = { ...rest, status: 'failed', error: `step result does not fit in one call's ${WORKFLOW_RESPONSE_MAX_BYTES} byte answer; store large data elsewhere and return its key`, non_retryable: true };
      size = byteLength(op) + 1;
    }
    used += size;
    return op;
  });
  if (response.result.status === 'succeeded' && used + byteLength(response.result) > budget) {
    response.result = { status: 'failed', error: `workflow output does not fit in one call's ${WORKFLOW_RESPONSE_MAX_BYTES} byte answer` };
  }
  return response;
}

export class UnknownWorkflowError extends Error {
  override readonly name = 'UnknownWorkflowError';
  constructor(readonly workflow: string) {
    super(`no workflow named "${workflow}" is served here`);
  }
}

function parseRequest(body: unknown): WorkflowInvocationRequest {
  const value = (typeof body === 'string' ? JSON.parse(body) : body) as Partial<WorkflowInvocationRequest> | null;
  if (!value || typeof value !== 'object' || value.protocol !== WORKFLOW_CODE_PROTOCOL) {
    throw new Error(`expected a ${WORKFLOW_CODE_PROTOCOL} request`);
  }
  if (typeof value.workflow !== 'string' || typeof value.run_id !== 'string' || !Array.isArray(value.journal)) {
    throw new Error('workflow request is missing workflow, run_id or journal');
  }
  return {
    protocol: value.protocol, run_id: value.run_id, workflow: value.workflow,
    input: value.input ?? null, invocation: Number(value.invocation ?? 1), journal: value.journal,
  };
}

export interface ServeWorkflowsOptions {
  /**
   * Accept calls without Gregale's internal workflow headers. Gregale strips
   * `x-faas-*` headers from public requests, so the default refuses callers
   * other than the platform. Enable only for local testing.
   */
  allowUnverifiedCallers?: boolean;
}

/** Returns a Fetch handler serving the given workflows' entry protocol. */
export function serveWorkflows(definitions: readonly WorkflowDefinition<any, any>[] | WorkflowDefinition<any, any>, options: ServeWorkflowsOptions = {}): (request: Request) => Promise<Response> {
  return async (request: Request): Promise<Response> => {
    if (request.method !== 'POST') return Response.json({ error: 'method not allowed' }, { status: 405, headers: { allow: 'POST' } });
    if (!options.allowUnverifiedCallers && (request.headers.get('x-faas-workflow-mode') !== 'code' || !request.headers.get('x-faas-workflow-run-id'))) {
      return Response.json({ error: 'workflow entry handlers only accept Gregale workflow calls' }, { status: 403 });
    }
    const text = await request.text();
    if (text.length > MAX_REQUEST_BYTES) return Response.json({ error: 'workflow request too large' }, { status: 413 });
    let body: unknown;
    try {
      body = JSON.parse(text);
    } catch {
      return Response.json({ error: 'workflow request is not JSON' }, { status: 400 });
    }
    try {
      return Response.json(await runWorkflowInvocation(definitions, body));
    } catch (error) {
      const status = error instanceof UnknownWorkflowError ? 404 : 400;
      return Response.json({ error: errorMessage(error) }, { status });
    }
  };
}

/** Adapts a Fetch handler to `http.createServer`, Express or similar. */
export function toNodeListener(handler: (request: Request) => Promise<Response>): (req: IncomingMessage, res: ServerResponse) => void {
  return (req, res) => {
    const chunks: Buffer[] = [];
    req.on('data', (chunk: Buffer) => chunks.push(chunk));
    req.on('end', () => {
      const headers = new Headers();
      for (const [key, value] of Object.entries(req.headers)) {
        if (Array.isArray(value)) value.forEach((item) => headers.append(key, item));
        else if (value !== undefined) headers.set(key, value);
      }
      const url = new URL(req.url ?? '/', `http://${req.headers.host ?? 'localhost'}`);
      const method = req.method ?? 'GET';
      const init: RequestInit = { method, headers };
      if (method !== 'GET' && method !== 'HEAD') init.body = Buffer.concat(chunks);
      handler(new Request(url, init)).then(async (response) => {
        res.statusCode = response.status;
        response.headers.forEach((value, key) => res.setHeader(key, value));
        res.end(Buffer.from(await response.arrayBuffer()));
      }, (error: unknown) => {
        res.statusCode = 500;
        res.end(errorMessage(error));
      });
    });
  };
}

export interface LocalRunOptions {
  /** Event payloads by event name, consumed in order by waits for that name. */
  events?: Record<string, unknown[]>;
  /** Retry policy for steps without their own (default 3 attempts). */
  retry?: RetryPolicy;
  /** Stops a run that does not finish (default 1000). */
  maxInvocations?: number;
  runId?: string;
}

export interface LocalRunResult<O = unknown> {
  status: 'succeeded' | 'failed';
  output?: O;
  error?: string;
  journal: JournalEntry[];
  invocations: number;
}

/**
 * Runs a workflow to completion in-process, the way Gregale would: sleeps
 * finish at once, waits take the given events in order or time out, and
 * failed steps retry under their policy. Use it in unit tests.
 */
export async function runWorkflowLocally<I, O>(definition: WorkflowDefinition<I, O>, input: I, options: LocalRunOptions = {}): Promise<LocalRunResult<O>> {
  const runId = options.runId ?? 'local-run';
  const journal: JournalEntry[] = [];
  const consumed = new Map<string, number>();
  const defaultRetry = options.retry ?? { maxAttempts: 3 };
  const limit = options.maxInvocations ?? 1000;
  for (let invocation = 1; invocation <= limit; invocation++) {
    const response = await runWorkflowInvocation(definition, {
      protocol: WORKFLOW_CODE_PROTOCOL, run_id: runId, workflow: definition.name, input, invocation, journal: journal.map((entry) => ({ ...entry })),
    });
    for (const op of response.ops) {
      const recorded = journal[op.seq];
      if (recorded ? recorded.name !== op.name || recorded.kind !== op.kind : op.seq !== journal.length) {
        return { status: 'failed', error: `nondeterministic workflow: operation ${op.seq} "${op.name}"`, journal, invocations: invocation };
      }
      if (op.kind === 'step') {
        const attempt = (recorded?.attempt ?? 0) + 1;
        const maxAttempts = op.retry?.max_attempts ?? defaultRetry.maxAttempts;
        let entry: JournalEntry;
        if (op.status === 'succeeded') entry = { seq: op.seq, name: op.name, kind: 'step', status: 'succeeded', attempt, ...(op.output === undefined ? {} : { output: op.output }) };
        else if (!op.non_retryable && attempt < maxAttempts) entry = { seq: op.seq, name: op.name, kind: 'step', status: 'retrying', attempt, error: op.error ?? 'step failed' };
        else entry = { seq: op.seq, name: op.name, kind: 'step', status: 'failed', attempt, error: op.error ?? 'step failed' };
        journal[op.seq] = entry;
      } else if (op.kind === 'sleep') {
        journal[op.seq] = { seq: op.seq, name: op.name, kind: 'sleep', status: 'succeeded' };
      } else {
        const event = op.event ?? op.name;
        const index = consumed.get(event) ?? 0;
        const payloads = options.events?.[event] ?? [];
        consumed.set(event, index + 1);
        journal[op.seq] = index < payloads.length
          ? { seq: op.seq, name: op.name, kind: 'event', event, status: 'succeeded', output: toJSONValue(payloads[index]) }
          : { seq: op.seq, name: op.name, kind: 'event', event, status: 'succeeded', timed_out: true };
      }
    }
    if (response.result.status === 'succeeded') return { status: 'succeeded', output: response.result.output as O, journal, invocations: invocation };
    if (response.result.status === 'failed') return { status: 'failed', error: response.result.error ?? 'workflow function failed', journal, invocations: invocation };
    if (response.ops.length === 0) return { status: 'failed', error: 'workflow made no progress', journal, invocations: invocation };
  }
  return { status: 'failed', error: `workflow did not finish within ${limit} invocations`, journal, invocations: limit };
}
