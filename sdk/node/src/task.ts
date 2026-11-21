import { createHash } from 'node:crypto';
import { RunsService } from './generated/services/RunsService.js';
import type { ExecutionResponse } from './generated/models/ExecutionResponse.js';
import type { InvokeExecutionTaskRequest } from './generated/models/InvokeExecutionTaskRequest.js';
import type { RunArtifactDelivery } from './generated/models/RunArtifactDelivery.js';
import { prepareExecutionFile, uploadPreparedExecutionFile } from './execution-files.js';

export interface TaskOptions {
  /** Persist and reuse this identity after any uncertain submission. */
  idempotencyKey: string;
  files?: Record<string, Blob>;
  customerRef?: string;
  signal?: AbortSignal;
  /** Called before admission; persist the exact request for process recovery. */
  onPrepared?: (intent: TaskIntent) => void | Promise<void>;
}
export interface TaskIntent { idempotencyKey: string; request: InvokeExecutionTaskRequest }
export interface TaskWaitOptions {
  signal?: AbortSignal;
  timeoutMs?: number;
  pollIntervalMs?: number;
  onReceipt?: (receipt: ExecutionResponse) => void | Promise<void>;
}
export interface TaskDestination { app: string; route: string; artifacts: Record<string, string> }
export interface TaskResult<T> { result: T; receipt: ExecutionResponse; deliveries: Record<string, RunArtifactDelivery> }

export class TaskExecutionError extends Error {
  constructor(public readonly receipt: ExecutionResponse) {
    super(`Task ${receipt.id}: ${receipt.result_validation?.status === 'invalid' ? 'invalid result contract' : receipt.status}`);
    this.name = 'TaskExecutionError';
  }
}
export class TaskDeliveryError extends Error {
  constructor(public readonly receipt: ExecutionResponse, public readonly delivery: RunArtifactDelivery, cause?: unknown) {
    super(`Artifact delivery ${delivery.id}: ${delivery.error_code ?? delivery.status}`, { cause });
    this.name = 'TaskDeliveryError';
  }
}
export class TaskSubmissionError extends Error {
  constructor(public readonly intent: TaskIntent, cause: unknown) {
    super('Task submission was not confirmed; retry the same intent and key', { cause });
    this.name = 'TaskSubmissionError';
  }
}

async function request<T>(pending: PromiseLike<T> & { cancel(): void }, signal?: AbortSignal): Promise<T> {
  const abort = () => pending.cancel();
  signal?.addEventListener('abort', abort, { once: true });
  if (signal?.aborted) abort();
  try { return await pending; } finally { signal?.removeEventListener('abort', abort); }
}
function delay(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    const abort = () => { clearTimeout(timer); signal?.removeEventListener('abort', abort); reject(signal?.reason ?? new Error('Task wait aborted')); };
    const timer = setTimeout(() => { signal?.removeEventListener('abort', abort); resolve(); }, ms);
    signal?.addEventListener('abort', abort, { once: true });
    if (signal?.aborted) abort();
  });
}
function successful(receipt: ExecutionResponse): void {
  if (receipt.status !== 'succeeded' || receipt.result_validation?.status === 'invalid') throw new TaskExecutionError(receipt);
}
function waitLimits(options: TaskWaitOptions): { interval: number; timeout: number } {
  const interval = options.pollIntervalMs ?? 1000, timeout = options.timeoutMs ?? 360_000;
  if (!Number.isFinite(interval) || interval <= 0 || interval > 2_147_483_647 || !Number.isFinite(timeout) || timeout <= 0 || timeout > 2_147_483_647) throw new Error('Invalid task wait limits');
  return { interval, timeout };
}
function waitSettings(options: TaskWaitOptions): { interval: number; deadline: number; signal: AbortSignal } {
  const { interval, timeout } = waitLimits(options);
  const deadlineSignal = AbortSignal.timeout(Math.ceil(timeout));
  return { interval, deadline: Date.now() + timeout, signal: options.signal ? AbortSignal.any([options.signal, deadlineSignal]) : deadlineSignal };
}

/** A pinned task version. The server enforces published schemas; decodeResult
 * supplies a runtime-checked application type, e.g. value => schema.parse(value). */
export class Task<Input, Output> {
  constructor(readonly name: string, readonly version: number, private readonly decodeResult: (value: unknown) => Output) {
    if (!name || !Number.isSafeInteger(version) || version < 1) throw new Error('An exact task name and positive version are required');
  }

  async start(input: Input, options: TaskOptions): Promise<TaskRun<Output>> {
    if (!options.idempotencyKey || options.idempotencyKey.trim() !== options.idempotencyKey || Buffer.byteLength(options.idempotencyKey) > 128 || /[\x00-\x1f\x7f]/.test(options.idempotencyKey)) throw new Error('A bounded explicit task idempotency key is required');
    options.signal?.throwIfAborted();
    const files = Object.entries(options.files ?? {});
    if (files.length > 8 || files.some(([path]) => !path || Buffer.byteLength(path) > 256 || /[\\\x00]/.test(path) || path.startsWith('/') || path.split('/').some(p => !p || p === '.' || p === '..'))) throw new Error('Invalid task input file paths or count');
    const prepared = [];
    for (const [path, blob] of files) prepared.push({ path, file: await prepareExecutionFile(blob, options.signal) });
    const body: InvokeExecutionTaskRequest = { name: this.name, version: this.version, input, ...(options.customerRef ? { customer_ref: options.customerRef } : {}), ...(prepared.length ? { input_files: prepared.map(p => ({ path: p.path, file_id: p.file.metadata.id })) } : {}) };
    const intent: TaskIntent = JSON.parse(JSON.stringify({ idempotencyKey: options.idempotencyKey, request: body }));
    await options.onPrepared?.(JSON.parse(JSON.stringify(intent)) as TaskIntent);
    const invoke = () => request(RunsService.invokeExecutionTask({ idempotencyKey: intent.idempotencyKey, requestBody: intent.request }), options.signal);
    let receipt: ExecutionResponse;
    try {
      try { receipt = await invoke(); }
      catch (error) {
        const e = error as { status?: number; problem?: { code?: string }; body?: { code?: string } };
        if (!prepared.length || e.status !== 409 || (e.problem?.code ?? e.body?.code) !== 'execution_files_not_ready') throw error;
        for (const { file } of prepared) if (file.metadata.upload_required) await uploadPreparedExecutionFile(file, options.signal);
        receipt = await invoke();
      }
      if (!receipt?.id || !receipt.task) throw new Error('Malformed task admission receipt');
      return this.attach(receipt);
    } catch (cause) { throw new TaskSubmissionError(intent, cause); }
  }

  /** Replay a persisted wire intent without reopening its original input files. */
  async submit(intent: TaskIntent, signal?: AbortSignal): Promise<TaskRun<Output>> {
    if (intent.request.name !== this.name || intent.request.version !== this.version || !intent.idempotencyKey) throw new Error('Intent does not belong to this task version');
    const snapshot = JSON.parse(JSON.stringify(intent)) as TaskIntent;
    try {
      const receipt = await request(RunsService.invokeExecutionTask({ idempotencyKey: snapshot.idempotencyKey, requestBody: snapshot.request }), signal);
      if (!receipt?.id || !receipt.task) throw new Error('Malformed task admission receipt');
      return this.attach(receipt);
    } catch (cause) { throw new TaskSubmissionError(snapshot, cause); }
  }

  attach(receipt: ExecutionResponse): TaskRun<Output> {
    if (receipt.task?.name !== this.name || receipt.task.version !== this.version) throw new Error('Run receipt does not belong to this task version');
    return new TaskRun(receipt, this.decodeResult);
  }

  async run(input: Input, options: TaskOptions & TaskWaitOptions & { deliver?: TaskDestination }): Promise<TaskResult<Output>> {
    waitLimits(options);
    const run = await this.start(input, options);
    const result = await run.result(options);
    const deliveries = options.deliver ? await run.deliver(options.deliver, options) : {};
    return { result, receipt: run.receipt, deliveries };
  }
}

export class TaskRun<Output> {
  constructor(public receipt: ExecutionResponse, private readonly decodeResult: (value: unknown) => Output) {}
  get id(): string { return this.receipt.id; }

  async result(options: TaskWaitOptions = {}): Promise<Output> {
    const { interval, deadline, signal } = waitSettings(options);
    await options.onReceipt?.(this.receipt);
    while (['queued', 'restoring', 'running'].includes(this.receipt.status)) {
      signal.throwIfAborted();
      if (Date.now() >= deadline) throw new Error(`Task wait expired; resume Run ${this.id}`);
      const next = await request(RunsService.getExecution({ id: this.id }), signal);
      if (next.id !== this.id || next.task?.id !== this.receipt.task?.id) throw new Error('Task receipt identity changed');
      this.receipt = next;
      await options.onReceipt?.(next);
      if (['queued', 'restoring', 'running'].includes(next.status)) await delay(Math.min(interval, Math.max(1, deadline - Date.now())), signal);
    }
    successful(this.receipt);
    return this.decodeResult(this.receipt.result);
  }

  async cancel(signal?: AbortSignal): Promise<void> { await request(RunsService.cancelExecution({ id: this.id }), signal); }

  async deliver(destination: TaskDestination, options: TaskWaitOptions = {}): Promise<Record<string, RunArtifactDelivery>> {
    successful(this.receipt);
    const { interval, deadline, signal } = waitSettings(options);
    const outputs: Record<string, RunArtifactDelivery> = Object.create(null) as Record<string, RunArtifactDelivery>;
    for (const [name, contentType] of Object.entries(destination.artifacts)) {
      const artifact = this.receipt.artifacts?.find(a => a.name === name);
      if (!artifact) throw new Error(`Task has no declared artifact ${name}`);
      const key = `task:${this.id}:${createHash('sha256').update(name).digest('hex')}`;
      let delivery = await request(RunsService.deliverRunArtifact({ slug: destination.app, route: destination.route, requestBody: { idempotency_key: key, execution_id: this.id, artifact_name: name, size_bytes: artifact.size_bytes, sha256: artifact.sha256, content_type: contentType } }), signal);
      const deliveryID = delivery.id;
      try {
        while (delivery.status === 'pending') {
          if (Date.now() >= deadline) throw new TaskDeliveryError(this.receipt, delivery);
          await delay(Math.min(interval, Math.max(1, deadline - Date.now())), signal);
          const next = await request(RunsService.getRunArtifactDelivery({ slug: destination.app, route: destination.route, delivery: deliveryID }), signal);
          if (next.id !== deliveryID) throw new Error('Delivery receipt identity changed');
          delivery = next;
        }
      } catch (cause) {
        if (cause instanceof TaskDeliveryError) throw cause;
        throw new TaskDeliveryError(this.receipt, delivery, cause);
      }
      if (delivery.status !== 'completed') throw new TaskDeliveryError(this.receipt, delivery);
      outputs[name] = delivery;
    }
    return outputs;
  }
}
