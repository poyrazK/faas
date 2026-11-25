/** Typed consumer for a single published pipeline tool. */
import { setTimeout as delay } from "node:timers/promises";
import { fileEventHash, fileEventUUID } from "./file-events.js";
import {
  JobContract,
  JobError,
  jobScope,
  jobSnapshot,
  jobValueHash,
  type JobSchema,
} from "./job-runtime.js";
import { LiveWorkInboxError, parseWorkInboxReview, watchInboxSnapshots, type LiveWorkInboxSource } from "./live-work-inbox.js";
import { isPipelinePreviewToken, type PipelineStartPreview } from "./pipeline-start-preview.js";
import { isPinnedPipelineSourceFailure } from "./pipeline-state.js";
import { handoffCursor, handoffPageLimit, type PipelineTaskHandoffView, type PipelineTaskHandoffPage,
  type PipelineTaskHandoffSnapshot, type PipelineTaskHandoffSummary } from "./pipeline-task-handoffs.js";
import type { PipelineTaskApprovalView, PipelineTaskApprovalReceipt, PipelineTaskApprovalChoice } from "./pipeline-task-approvals.js";
import type { PipelineTaskRecoveryPreview } from "./pipeline-task-recovery.js";
import { pipelineToolWire } from "./pipeline-tool-protocol.js";
import { pipelineToolName } from "./pipeline-tool-state.js";
import type { PipelineTaskFormRequest } from "./pipeline-task-input.js";
import { parsePipelineTaskManifest, PIPELINE_TASK_MANIFEST_MAX_BYTES, PIPELINE_TASK_MANIFEST_URI, type PipelineTaskManifest } from "./pipeline-task-manifest.js";
import type { PipelineTaskProgress } from "./pipeline-tasks.js";
import type { QueryResourceLink } from "./query-tools.js";

export type PipelineTaskClientCode =
  | "invalid_input"
  | "invalid_response"
  | "not_found"
  | "unauthorized"
  | "unsupported"
  | "request_failed"
  | "deadline"
  | "cancelled"
  | "tool_failed"
  | "revision_unavailable"
  | "preview_required"
  | "preview_stale"
  | "recovery_blocked"
  | "recovery_exists";

export class PipelineTaskClientError extends Error {
  constructor(readonly code: PipelineTaskClientCode) {
    super("Pipeline Task client: " + code);
    this.name = "PipelineTaskClientError";
  }
}

export interface PipelineTaskClientOptions {
  readonly timeoutMs: number;
  readonly signal?: AbortSignal;
}

export type PipelineTaskClientMethod =
  | "tools/call"
  | "tasks/get"
  | "tasks/update"
  | "resources/read";
export interface PipelineTaskTransport {
  request(
    method: PipelineTaskClientMethod,
    params: Readonly<Record<string, unknown>>,
    options: PipelineTaskClientOptions,
  ): Promise<unknown>;
  /** Return authorized Task snapshots. An unacknowledged ID is reported as not_found. */
  subscribe?(
    taskId: string,
    options: PipelineTaskClientOptions,
  ): AsyncGenerator<unknown, void, void>;
}

export interface PipelineTaskCheckpoint {
  readonly version: 1;
  readonly tool: string;
  readonly contract_sha256: string;
  readonly request_id: string;
  readonly task_id: string;
}

interface ObservationBase {
  readonly checkpoint: PipelineTaskCheckpoint;
  readonly progress: PipelineTaskProgress;
  readonly statusMessage: string;
  readonly pollIntervalMs: number;
}

export type PipelineTaskObservation<O> = ObservationBase &
  (
    | Readonly<{ status: "working" | "failed" | "cancelled" }>
    | Readonly<{
        status: "input_required";
        forms: Readonly<Record<string, PipelineTaskFormRequest>>;
      }>
    | Readonly<{
        status: "completed";
        output: O;
        artifacts: readonly QueryResourceLink[];
      }>
  );

export interface PipelineTaskResource {
  readonly uri: string;
  readonly mimeType: string;
  readonly text: string;
}

export interface PipelineTaskReader<O, R> {
  readonly checkpoint: PipelineTaskCheckpoint;
  get(options: PipelineTaskClientOptions): Promise<PipelineTaskObservation<O>>;
  previewRecovery<P>(recoveryRequestId: string, contract: JobContract<null, P>, options: PipelineTaskClientOptions): Promise<PipelineTaskRecoveryPreview<P>>;
  confirmRecovery(recoveryRequestId: string, previewToken: string, options: PipelineTaskClientOptions): Promise<PipelineTaskCheckpoint>;
  watch(
    options: PipelineTaskClientOptions,
  ): AsyncGenerator<PipelineTaskObservation<O>, void, void>;
  answer(
    observation: Extract<
      PipelineTaskObservation<O>,
      { status: "input_required" }
    >,
    key: string,
    value: R,
    options: PipelineTaskClientOptions,
  ): Promise<void>;
  readArtifact(
    observation: Extract<PipelineTaskObservation<O>, { status: "completed" }>,
    name: string,
    options: PipelineTaskClientOptions,
  ): Promise<PipelineTaskResource>;
}

const formKeyPattern =
  /^[A-Za-z0-9_-]{43}\.[A-Za-z0-9_-]{43}\.[A-Za-z0-9_-]{22}$/;

function record(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value))
    throw new PipelineTaskClientError("invalid_response");
  return value as Record<string, unknown>;
}

function checkToolResult(result: Record<string, unknown>, handoff = false): void {
  if (result.isError !== true) return;
  const first = Array.isArray(result.content) ? result.content[0] : undefined;
  let code: unknown;
  if (first?.type === "text" && typeof first.text === "string" && first.text.length <= 256) {
    try { code = JSON.parse(first.text)?.code; } catch { /* Unknown tool error. */ }
  }
  throw new PipelineTaskClientError(
    code === "revision_unavailable" || code === "preview_required" || code === "preview_stale" ||
    code === "recovery_blocked" || code === "recovery_exists" || code === "not_found" ||
    handoff && (code === "invalid_input" || code === "deadline") ? code : "tool_failed",
  );
}

function readPreview<P>(result: Record<string, unknown>, contract: JobContract<null, P>): PipelineStartPreview<P> {
  checkToolResult(result);
  const { summary, preview_token, expires_at } = record(result.structuredContent);
  if (result.isError !== false || !isPipelinePreviewToken(preview_token) ||
      typeof expires_at !== "number" || expires_at !== Number(preview_token.split(".", 1)[0]))
    throw new PipelineTaskClientError("invalid_response");
  return jobSnapshot({ summary: output(contract, summary, "invalid_response"), preview_token, expires_at });
}

function validTimeout(value: number): boolean {
  return Number.isSafeInteger(value) && value > 0 && value <= 2147483647;
}

function failed(error: unknown, signal?: AbortSignal): never {
  if (signal?.aborted) throw new PipelineTaskClientError("cancelled");
  if (error instanceof PipelineTaskClientError) throw error;
  if (error instanceof JobError && error.code === "deadline")
    throw new PipelineTaskClientError("deadline");
  throw new PipelineTaskClientError("request_failed");
}

async function scoped<T>(
  options: PipelineTaskClientOptions,
  work: (signal: AbortSignal) => Promise<T>,
): Promise<T> {
  if (!validTimeout(options.timeoutMs))
    throw new PipelineTaskClientError("invalid_input");
  try {
    return await jobScope(options.timeoutMs, options.signal, work);
  } catch (error) {
    return failed(error, options.signal);
  }
}

function output<I, O>(
  contract: JobContract<I, O>,
  value: unknown,
  code: "invalid_input" | "invalid_response",
): O {
  try {
    return contract.output(value);
  } catch {
    throw new PipelineTaskClientError(code);
  }
}

function formContract(value: unknown): JobContract<null, unknown> {
  const params = record(value);
  if (
    params.mode !== "form" ||
    typeof params.message !== "string" ||
    !params.message.trim() ||
    params.message.length > 1024
  )
    throw new PipelineTaskClientError("invalid_response");
  try {
    const schema = record(params.requestedSchema) as unknown as JobSchema;
    if (
      schema.type !== "object" ||
      !Object.keys(schema.properties ?? {}).length ||
      Object.values(schema.properties ?? {}).some(
        (field) =>
          !["string", "number", "integer", "boolean"].includes(field.type),
      ) ||
      Buffer.byteLength(JSON.stringify(schema)) > 16384
    )
      throw new Error();
    return new JobContract({
      version: 1,
      name: "pipeline-task-form",
      input: { type: "null" },
      output: schema,
    });
  } catch {
    throw new PipelineTaskClientError("invalid_response");
  }
}

function readLink(candidate: unknown): QueryResourceLink {
  const { type, name, description, mimeType, uri } = record(candidate);
  if (
    type !== "resource_link" ||
    !pipelineToolName(name) ||
    typeof uri !== "string" ||
    !/^gregale-pipeline:\/\/[a-f0-9]{64}\/[a-z][a-z0-9_]{0,47}\/[A-Za-z0-9_-]{64}$/.test(
      uri,
    ) ||
    uri.split("/")[3] !== name ||
    typeof description !== "string" ||
    description.length > 512 ||
    typeof mimeType !== "string" ||
    mimeType.length > 128 ||
    !/^[a-z0-9!#$&^_.+-]+\/[a-z0-9!#$&^_.+-]+$/.test(mimeType)
  )
    throw new PipelineTaskClientError("invalid_response");
  return { type, name, description, mimeType, uri };
}

function observation<I, O>(
  raw: unknown,
  checkpoint: PipelineTaskCheckpoint,
  contract: JobContract<I, O>,
): PipelineTaskObservation<O> {
  const wire = record(raw);
  if (
    wire.taskId !== checkpoint.task_id ||
    typeof wire.statusMessage !== "string" ||
    wire.statusMessage.length > 16384 ||
    typeof wire.pollIntervalMs !== "number" ||
    !validTimeout(wire.pollIntervalMs) ||
    wire.pollIntervalMs > 60000
  )
    throw new PipelineTaskClientError("invalid_response");
  const meta = record(record(wire._meta)["io.gregale/pipeline"]);
  const { request_id, run_id, state, steps } = meta;
  if (
    request_id !== checkpoint.request_id ||
    typeof state !== "string" ||
    (state === "start_unknown" ? run_id !== null : !fileEventUUID(run_id)) ||
    !Array.isArray(steps) ||
    steps.length > 1000 ||
    !steps.every((item) => {
      const step = record(item);
      return (
        typeof step.name === "string" &&
        step.name.length <= 128 &&
        typeof step.status === "string" &&
        step.status.length <= 32 &&
        Number.isSafeInteger(step.attempt) &&
        Number(step.attempt) >= 0
      );
    })
  )
    throw new PipelineTaskClientError("invalid_response");
  const failure = meta.source_failure;
  if (failure !== undefined && (state !== "failed" || !isPinnedPipelineSourceFailure(failure)))
    throw new PipelineTaskClientError("invalid_response");
  const replaces = meta.replaces_task_id;
  if (replaces !== undefined && (!fileEventUUID(replaces) || replaces === checkpoint.task_id))
    throw new PipelineTaskClientError("invalid_response");
  const progress = { request_id, run_id, state, steps,
    ...(replaces && { replaces_task_id: replaces }),
    ...(isPinnedPipelineSourceFailure(failure) && { source_failure: failure }),
  } as PipelineTaskProgress;
  if (state === "awaiting_approval" || state === "awaiting_input") {
    try {
      Object.assign(progress, {
        review: parseWorkInboxReview(meta.review, state, run_id),
      });
    } catch {
      throw new PipelineTaskClientError("invalid_response");
    }
  }
  if (state === "start_unknown" && meta.follow_up !== undefined) {
    const follow = record(meta.follow_up),
      args = record(follow.arguments);
    if (
      follow.kind !== "tool_call" ||
      follow.state !== state ||
      follow.name !== checkpoint.tool + "_reconcile" ||
      args.request_id !== request_id ||
      !fileEventUUID(args.run_id)
    )
      throw new PipelineTaskClientError("invalid_response");
    Object.assign(progress, {
      follow_up: {
        kind: follow.kind,
        state,
        name: follow.name,
        arguments: { request_id, run_id: args.run_id },
      },
    });
  }
  const base = {
    checkpoint,
    progress,
    statusMessage: wire.statusMessage,
    pollIntervalMs: wire.pollIntervalMs,
  };
  const status = wire.status;
  if (
    (status === "working" &&
      [
        "start_unknown",
        "running",
        "awaiting_approval",
        "awaiting_input",
      ].includes(state)) ||
    (status === "failed" && state === "failed") ||
    (status === "cancelled" && state === "cancelled")
  )
    return jobSnapshot({ ...base, status });
  if (status === "input_required" && state === "awaiting_input") {
    const forms = record(wire.inputRequests),
      entries = Object.entries(forms);
    if (entries.length !== 1)
      throw new PipelineTaskClientError("invalid_response");
    const [key, form] = entries[0]!;
    const request = record(form);
    if (!formKeyPattern.test(key) || request.method !== "elicitation/create")
      throw new PipelineTaskClientError("invalid_response");
    formContract(request.params);
    return jobSnapshot({
      ...base,
      status,
      forms: forms as Record<string, PipelineTaskFormRequest>,
    });
  }
  if (status === "completed" && state === "succeeded" && run_id !== null) {
    const result = record(wire.result),
      structured = record(result.structuredContent);
    const receipt = record(structured.receipt);
    if (
      result.isError !== false ||
      structured.state !== "succeeded" ||
      receipt.tool !== checkpoint.tool ||
      receipt.request_id !== request_id ||
      receipt.run_id !== run_id ||
      !fileEventHash(receipt.identity) ||
      !Array.isArray(result.content) ||
      result.content.length > 100
    )
      throw new PipelineTaskClientError("invalid_response");
    const artifacts = result.content
      .map(record)
      .filter(
        (block) =>
          block.type === "resource_link" &&
          typeof block.uri === "string" &&
          block.uri.startsWith("gregale-pipeline://"),
      )
      .map(readLink);
    if (new Set(artifacts.map(({ name }) => name)).size !== artifacts.length)
      throw new PipelineTaskClientError("invalid_response");
    return jobSnapshot({
      ...base,
      status,
      output: output(contract, structured.output, "invalid_response"),
      artifacts,
    });
  }
  throw new PipelineTaskClientError("invalid_response");
}

export function readPipelineTaskManifest(transport: PipelineTaskTransport, options: PipelineTaskClientOptions): Promise<PipelineTaskManifest> {
  return scoped(options, async (signal) => {
    const uri = PIPELINE_TASK_MANIFEST_URI;
    const result = record(await transport.request("resources/read", { uri }, { ...options, signal }));
    if (!Array.isArray(result.contents) || result.contents.length !== 1)
      throw new PipelineTaskClientError("invalid_response");
    const item = record(result.contents[0]);
    if (item.uri !== uri || item.mimeType !== "application/json" || typeof item.text !== "string" ||
        Buffer.byteLength(item.text) > PIPELINE_TASK_MANIFEST_MAX_BYTES)
      throw new PipelineTaskClientError("invalid_response");
    try { return parsePipelineTaskManifest(JSON.parse(item.text)); }
    catch { throw new PipelineTaskClientError("invalid_response"); }
  });
}

export function createPipelineTaskClient<I, O, R = unknown>(settings: {
  readonly tool: string;
  readonly contract: JobContract<I, O>;
  readonly checkContractOnStart?: boolean;
  readonly replyContract?: JobContract<unknown, R>;
  readonly transport: PipelineTaskTransport;
}) {
  const { tool, contract, replyContract, transport, checkContractOnStart } = settings;
  if (!pipelineToolName(tool) || typeof transport?.request !== "function")
    throw new PipelineTaskClientError("invalid_input");

  function resume(value: unknown): PipelineTaskReader<O, R> {
    let raw: Record<string, unknown>;
    try {
      raw = record(value);
    } catch {
      throw new PipelineTaskClientError("invalid_input");
    }
    if (
      Object.keys(raw).sort().join() !==
        "contract_sha256,request_id,task_id,tool,version" ||
      raw.version !== 1 ||
      raw.tool !== tool ||
      raw.contract_sha256 !== contract.sha256 ||
      !fileEventUUID(raw.task_id) ||
      !fileEventUUID(raw.request_id)
    )
      throw new PipelineTaskClientError("invalid_input");
    const checkpoint = jobSnapshot(raw) as unknown as PipelineTaskCheckpoint;
    const taskId = checkpoint.task_id;
    function sameTask(other: PipelineTaskCheckpoint): void {
      if (
        !other ||
        other.task_id !== taskId ||
        other.request_id !== checkpoint.request_id ||
        other.tool !== tool ||
        other.contract_sha256 !== contract.sha256
      )
        throw new PipelineTaskClientError("invalid_input");
    }
    const get = (options: PipelineTaskClientOptions) =>
      scoped(options, async (signal) => {
        const wire = record(
          await transport.request(
            "tasks/get",
            { taskId },
            { ...options, signal },
          ),
        );
        if (wire.resultType !== "complete")
          throw new PipelineTaskClientError("invalid_response");
        return observation(wire, checkpoint, contract);
      });

    function recoveryArguments(recoveryRequestId: string) {
      if (!fileEventUUID(recoveryRequestId)) throw new PipelineTaskClientError("invalid_input");
      return { task_id: taskId, recovery_request_id: recoveryRequestId, expected_contract_sha256: contract.sha256 };
    }

    return Object.freeze<PipelineTaskReader<O, R>>({
      checkpoint,
      get,
      async previewRecovery(recoveryRequestId, previewContract, options) {
        const args = recoveryArguments(recoveryRequestId);
        return scoped(options, async (signal) => {
          const result = record(await transport.request("tools/call", {
            name: pipelineToolWire.task_recovery.preview.name, arguments: args,
          }, { ...options, signal }));
          const preview = readPreview(result, previewContract);
          const { replaces_task_id, previous_source_sha256 } = record(result.structuredContent);
          if (replaces_task_id !== taskId || !fileEventHash(previous_source_sha256))
            throw new PipelineTaskClientError("invalid_response");
          return jobSnapshot({ ...preview, replaces_task_id, previous_source_sha256 });
        });
      },
      async confirmRecovery(recoveryRequestId, previewToken, options) {
        const args = recoveryArguments(recoveryRequestId);
        if (!isPipelinePreviewToken(previewToken)) throw new PipelineTaskClientError("invalid_input");
        return scoped(options, async (signal) => {
          const result = record(await transport.request("tools/call", {
            name: pipelineToolWire.task_recovery.confirm.name, arguments: { ...args, preview_token: previewToken },
          }, { ...options, signal }));
          checkToolResult(result);
          const metadata = record(record(result._meta)["io.gregale/pipeline-recovery"]);
          if (result.resultType !== "task" || result.status !== "working" || !fileEventUUID(result.taskId) ||
              result.taskId === taskId || metadata.replaces_task_id !== taskId ||
              metadata.recovery_request_id !== recoveryRequestId || metadata.tool !== tool ||
              metadata.contract_sha256 !== contract.sha256 || !fileEventUUID(metadata.request_id))
            throw new PipelineTaskClientError("invalid_response");
          return Object.freeze({
            version: 1, tool, contract_sha256: contract.sha256,
            task_id: result.taskId, request_id: metadata.request_id,
          });
        });
      },
      async *watch(options) {
        if (!validTimeout(options.timeoutMs))
          throw new PipelineTaskClientError("invalid_input");
        const deadline = performance.now() + options.timeoutMs;
        const lifetime = AbortSignal.any([
          AbortSignal.timeout(options.timeoutMs),
          ...(options.signal ? [options.signal] : []),
        ]);
        function remaining(): PipelineTaskClientOptions {
          if (options.signal?.aborted)
            throw new PipelineTaskClientError("cancelled");
          if (lifetime.aborted || performance.now() >= deadline)
            throw new PipelineTaskClientError("deadline");
          return {
            timeoutMs: Math.max(1, Math.ceil(deadline - performance.now())),
            signal: options.signal,
          };
        }
        let previous: string | undefined;
        function changed(snapshot: PipelineTaskObservation<O>): boolean {
          const fingerprint = jobValueHash(snapshot);
          const differs = previous !== fingerprint;
          previous = fingerprint;
          return differs;
        }
        const terminal = (snapshot: PipelineTaskObservation<O>) =>
          ["completed", "failed", "cancelled"].includes(snapshot.status);
        for (let attempt = 0; transport.subscribe && attempt < 2; attempt++) {
          const stream = transport.subscribe(taskId, {
            ...remaining(),
            signal: lifetime,
          });
          try {
            for (;;) {
              const { value, done } = await scoped(remaining(), () =>
                stream.next(),
              );
              remaining();
              if (done) break;
              const snapshot = observation(value, checkpoint, contract);
              if (changed(snapshot)) yield snapshot;
              if (terminal(snapshot)) return;
            }
          } catch (error) {
            if (
              error instanceof PipelineTaskClientError &&
              error.code === "unsupported"
            )
              break;
            if (
              !(
                error instanceof PipelineTaskClientError &&
                error.code === "request_failed"
              )
            )
              throw error;
          } finally {
            // A transport must observe the signal. Do not let a late close extend the deadline.
            const closing = stream.return();
            if (lifetime.aborted || performance.now() >= deadline)
              void closing.catch(() => {});
            else await scoped(remaining(), () => closing);
          }
        }
        for (;;) {
          const snapshot = await get(remaining());
          if (changed(snapshot)) yield snapshot;
          if (terminal(snapshot)) return;
          await scoped(remaining(), (signal) =>
            delay(snapshot.pollIntervalMs, undefined, { signal }),
          );
        }
      },
      async answer(view, key, value, options) {
        sameTask(view.checkpoint);
        const form =
          view.status === "input_required" &&
          Object.hasOwn(view.forms, key) &&
          view.forms[key];
        if (!form || !formKeyPattern.test(key))
          throw new PipelineTaskClientError("invalid_input");
        const validator = formContract(form.params);
        if (
          replyContract &&
          jobValueHash(replyContract.spec.output) !==
            jobValueHash(validator.spec.output)
        )
          throw new PipelineTaskClientError("invalid_response");
        const content = output(validator, value, "invalid_input");
        if (Buffer.byteLength(JSON.stringify(content)) > 16384)
          throw new PipelineTaskClientError("invalid_input");
        await scoped(options, async (signal) => {
          const result = record(
            await transport.request(
              "tasks/update",
              {
                taskId,
                inputResponses: { [key]: { action: "accept", content } },
              },
              { ...options, signal },
            ),
          );
          if (result.resultType !== "complete")
            throw new PipelineTaskClientError("invalid_response");
        });
      },
      async readArtifact(view, name, options) {
        sameTask(view.checkpoint);
        const link =
          view.status === "completed" &&
          view.artifacts.find((item) => item.name === name);
        if (!link) throw new PipelineTaskClientError("not_found");
        const { uri, mimeType } = readLink(link);
        return scoped(options, async (signal) => {
          const result = record(
            await transport.request(
              "resources/read",
              { uri },
              { ...options, signal },
            ),
          );
          if (!Array.isArray(result.contents) || result.contents.length !== 1)
            throw new PipelineTaskClientError("invalid_response");
          const body = record(result.contents[0]);
          if (
            body.uri !== uri ||
            body.mimeType !== mimeType ||
            typeof body.text !== "string" ||
            Buffer.byteLength(body.text) > 1048576
          )
            throw new PipelineTaskClientError("invalid_response");
          return { uri, mimeType, text: body.text };
        });
      },
    });
  }

  function startInput(value: I, requestId: string): I {
    if (!fileEventUUID(requestId))
      throw new PipelineTaskClientError("invalid_input");
    try { return contract.input(value); }
    catch { throw new PipelineTaskClientError("invalid_input"); }
  }

  return Object.freeze({
    async preview<P>(
      value: I,
      requestId: string,
      previewContract: JobContract<null, P>,
      options: PipelineTaskClientOptions,
    ): Promise<PipelineStartPreview<P>> {
      const input = startInput(value, requestId);
      return scoped(options, async (signal) => {
        const result = record(await transport.request("tools/call", {
          name: tool + "_preview",
          arguments: { request_id: requestId, input, expected_contract_sha256: contract.sha256 },
        }, { ...options, signal }));
        return readPreview(result, previewContract);
      });
    },
    async start(
      value: I,
      requestId: string,
      { previewToken, ...options }: PipelineTaskClientOptions & { readonly previewToken?: string },
    ): Promise<PipelineTaskCheckpoint> {
      const input = startInput(value, requestId);
      if (previewToken !== undefined && !isPipelinePreviewToken(previewToken))
        throw new PipelineTaskClientError("invalid_input");
      return scoped(options, async (signal) => {
        const result = record(
          await transport.request(
            "tools/call",
            {
              name: tool + "_start",
              arguments: {
                request_id: requestId, input,
                ...((checkContractOnStart || previewToken) && { expected_contract_sha256: contract.sha256 }),
                ...(previewToken && { preview_token: previewToken }),
              },
            },
            { ...options, signal },
          ),
        );
        checkToolResult(result);
        if (
          result.resultType !== "task" ||
          result.status !== "working" ||
          !fileEventUUID(result.taskId)
        )
          throw new PipelineTaskClientError("invalid_response");
        return Object.freeze({
          version: 1,
          tool,
          contract_sha256: contract.sha256,
          request_id: requestId,
          task_id: result.taskId,
        });
      });
    },
    resume,
  });
}

/** Reconnect to the named reviewer's one pending clarification. */
export function createPipelineTaskHandoffClient<R>(settings: {
  readonly handoffId: string;
  readonly replyContract: JobContract<unknown, R>;
  readonly transport: PipelineTaskTransport;
}) {
  const { handoffId, replyContract, transport } = settings;
  if (!fileEventUUID(handoffId) || typeof transport?.request !== "function")
    throw new PipelineTaskClientError("invalid_input");

  async function call(name: string, args: Record<string, unknown>, options: PipelineTaskClientOptions) {
    return scoped(options, async (signal) => {
      const result = record(await transport.request("tools/call", {
        name, arguments: { handoff_id: handoffId, ...args },
      }, { ...options, signal }));
      checkToolResult(result, true);
      const body = record(result.structuredContent);
      if (result.isError !== false || body.handoff_id !== handoffId)
        throw new PipelineTaskClientError("invalid_response");
      return body;
    });
  }

  return Object.freeze({
    async get(options: PipelineTaskClientOptions): Promise<PipelineTaskHandoffView> {
      const body = await call(pipelineToolWire.task_handoff.read.name, {}, options);
      const form = record(body.form), params = record(form.params), advertised = formContract(params);
      const { request_key, expires_at } = body;
      if (form.method !== "elicitation/create" || typeof request_key !== "string" || !formKeyPattern.test(request_key) ||
          request_key.length !== 110 || typeof expires_at !== "string" || !Number.isFinite(Date.parse(expires_at)) ||
          jobValueHash(replyContract.spec.output) !== jobValueHash(advertised.spec.output))
        throw new PipelineTaskClientError("invalid_response");
      return jobSnapshot({
        handoff_id: handoffId, request_key, expires_at,
        form: { method: "elicitation/create" as const, params: {
          mode: "form" as const, message: params.message as string, requestedSchema: advertised.spec.output,
        } },
      });
    },
    async answer(view: PipelineTaskHandoffView, value: R, options: PipelineTaskClientOptions): Promise<void> {
      if (view?.handoff_id !== handoffId || typeof view.request_key !== "string" ||
          view.request_key.length !== 110 || !formKeyPattern.test(view.request_key))
        throw new PipelineTaskClientError("invalid_input");
      const body = await call(pipelineToolWire.task_handoff.reply.name, {
        request_key: view.request_key, input: output(replyContract, value, "invalid_input"),
      }, options);
      if (body.submitted !== true) throw new PipelineTaskClientError("invalid_response");
    },
  });
}

export interface PipelineTaskReviewerInboxPageOptions extends PipelineTaskClientOptions {
  readonly pageSize?: number;
  readonly includeApprovals?: boolean;
  readonly cursor?: string;
}
export interface PipelineTaskReviewerInboxWatchOptions extends PipelineTaskClientOptions {
  readonly pageSize?: number;
  readonly includeApprovals?: boolean;
  readonly maxPages?: number;
  readonly maxSnapshots?: number;
  readonly refreshIntervalMs?: number;
  readonly notifications?: { readonly resourceId: string; readonly source: LiveWorkInboxSource };
}

function readHandoffSummary(raw: unknown, includeApprovals: boolean): PipelineTaskHandoffSummary {
  const entry = record(raw);
  const { handoff_id, expires_at, question } = entry;
  const approval = includeApprovals && entry.kind === "approval";
  if (Object.keys(entry).sort().join() !== (approval ? "expires_at,handoff_id,kind,question" : "expires_at,handoff_id,question") || !fileEventUUID(handoff_id) ||
      typeof question !== "string" || !question.trim() || question.length > 1024 ||
      typeof expires_at !== "string" || !Number.isFinite(Date.parse(expires_at)))
    throw new PipelineTaskClientError("invalid_response");
  return { handoff_id, expires_at, question, ...(approval && { kind: "approval" }) };
}

/** List and watch the authenticated reviewer's pending clarifications. */
export function createPipelineTaskReviewerInbox(transport: PipelineTaskTransport) {
  if (typeof transport?.request !== "function")
    throw new PipelineTaskClientError("invalid_input");

  async function page(options: PipelineTaskReviewerInboxPageOptions): Promise<PipelineTaskHandoffPage> {
    const { pageSize = 20, cursor, includeApprovals = false } = options;
    if (typeof includeApprovals !== "boolean") throw new PipelineTaskClientError("invalid_input");
    if (!Number.isInteger(pageSize) || pageSize < 1 || pageSize > handoffPageLimit ||
        (cursor !== undefined && (typeof cursor !== "string" || !handoffCursor.test(cursor))))
      throw new PipelineTaskClientError("invalid_input");
    return scoped(options, async (signal) => {
      const result = record(await transport.request("tools/call", {
        name: pipelineToolWire.task_handoff.list.name,
        arguments: { page_size: pageSize, ...(cursor && { cursor }), ...(includeApprovals && { include_approvals: true }) },
      }, { timeoutMs: options.timeoutMs, signal }));
      checkToolResult(result, true);
      const body = record(result.structuredContent);
      const { items, next_cursor } = body;
      if (result.isError !== false || Object.keys(body).sort().join() !== "items,next_cursor" ||
          !Array.isArray(items) || items.length > pageSize ||
          (next_cursor !== null && (typeof next_cursor !== "string" || !handoffCursor.test(next_cursor) || next_cursor === cursor)))
        throw new PipelineTaskClientError("invalid_response");
      const entries = items.map((item) => readHandoffSummary(item, includeApprovals));
      if (new Set(entries.map((entry) => entry.handoff_id)).size !== entries.length)
        throw new PipelineTaskClientError("invalid_response");
      return jobSnapshot({ items: entries, next_cursor });
    });
  }

  function watch(options: PipelineTaskReviewerInboxWatchOptions): AsyncIterableIterator<PipelineTaskHandoffSnapshot> {
    const { notifications, maxPages = 10, pageSize, includeApprovals } = options;
    if (!Number.isInteger(maxPages) || maxPages < 1 || maxPages > 20)
      throw new PipelineTaskClientError("invalid_input");
    return watchInboxSnapshots({
      ...options,
      target: { resource_id: notifications?.resourceId ?? "reviewer-inbox", business_ref: "reviewer-inbox" },
      source: notifications?.source ?? ((sink) => { sink.ready(); return () => {}; }),
      async loadSnapshot({ timeoutMs, signal }): Promise<PipelineTaskHandoffSnapshot> {
        const deadline = performance.now() + timeoutMs;
        const entries = new Map<string, PipelineTaskHandoffSummary>(), cursors = new Set<string>();
        let cursor: string | undefined;
        try {
          for (let n = 0; n < maxPages; n++) {
            const remaining = Math.ceil(deadline - performance.now());
            if (remaining <= 0) throw new LiveWorkInboxError("deadline");
            const result = await page({ pageSize, cursor, includeApprovals, timeoutMs: remaining, signal });
            for (const entry of result.items) {
              if (entries.has(entry.handoff_id)) throw new LiveWorkInboxError("invalid_snapshot");
              entries.set(entry.handoff_id, entry);
            }
            cursor = result.next_cursor ?? undefined;
            if (!cursor) break;
            if (cursors.has(cursor)) throw new LiveWorkInboxError("invalid_snapshot");
            cursors.add(cursor);
          }
        } catch (error) {
          if (error instanceof PipelineTaskClientError) {
            if (error.code === "unauthorized" || error.code === "not_found") throw new LiveWorkInboxError("unauthorized");
            if (error.code === "invalid_response") throw new LiveWorkInboxError("invalid_snapshot");
          }
          throw error;
        }
        return jobSnapshot({ items: [...entries.values()], truncated: cursor !== undefined });
      },
    }, (snapshot) => snapshot,
    (error) => error instanceof PipelineTaskClientError && error.code === "request_failed");
  }

  return Object.freeze({ page, watch });
}

/** Preview the bound action and submit an explicit reviewer decision. */
export function createPipelineTaskApprovalClient<R, D>(settings: {
  readonly handoffId: string;
  readonly contract: JobContract<R, D>;
  readonly transport: PipelineTaskTransport;
}) {
  const { handoffId, contract, transport } = settings;
  if (!fileEventUUID(handoffId) || typeof transport?.request !== "function")
    throw new PipelineTaskClientError("invalid_input");

  async function call(name: string, args: Record<string, unknown>, options: PipelineTaskClientOptions) {
    return scoped(options, async (signal) => {
      const result = record(await transport.request("tools/call", {
        name, arguments: { handoff_id: handoffId, ...args },
      }, { ...options, signal }));
      checkToolResult(result, true);
      const body = record(result.structuredContent);
      if (result.isError !== false || body.handoff_id !== handoffId)
        throw new PipelineTaskClientError("invalid_response");
      return body;
    });
  }

  return Object.freeze({
    async get(options: PipelineTaskClientOptions): Promise<PipelineTaskApprovalView<R>> {
      const body = await call(pipelineToolWire.task_handoff.approval.name, {}, options);
      const { expires_at, title, decision_key, review_sha256, contract_sha256 } = body;
      if (contract_sha256 !== contract.sha256 || !fileEventHash(decision_key) || !fileEventHash(review_sha256) ||
          typeof title !== "string" || !title.trim() || title.length > 1024 ||
          typeof expires_at !== "string" || !Number.isFinite(Date.parse(expires_at)))
        throw new PipelineTaskClientError("invalid_response");
      let review: R;
      try { review = contract.input(body.review); }
      catch { throw new PipelineTaskClientError("invalid_response"); }
      if (jobValueHash(review) !== review_sha256) throw new PipelineTaskClientError("invalid_response");
      return jobSnapshot({ handoff_id: handoffId, expires_at, title, review, review_sha256, contract_sha256, decision_key });
    },
    async decide(view: PipelineTaskApprovalView<R>, state: PipelineTaskApprovalChoice, value: D, options: PipelineTaskClientOptions): Promise<PipelineTaskApprovalReceipt> {
      if (view?.handoff_id !== handoffId || view.contract_sha256 !== contract.sha256 || !fileEventHash(view.decision_key) ||
          (state !== "approved" && state !== "rejected"))
        throw new PipelineTaskClientError("invalid_input");
      try {
        if (jobValueHash(contract.input(view.review)) !== view.review_sha256) throw new Error();
      } catch { throw new PipelineTaskClientError("invalid_input"); }
      const body = await call(pipelineToolWire.task_handoff.decide.name, {
        decision_key: view.decision_key, state, input: output(contract, value, "invalid_input"),
      }, options);
      if (body.state !== state || typeof body.decided_at !== "string" || !Number.isFinite(Date.parse(body.decided_at)))
        throw new PipelineTaskClientError("invalid_response");
      return jobSnapshot({ handoff_id: handoffId, state, decided_at: body.decided_at });
    },
  });
}
