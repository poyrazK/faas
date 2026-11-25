/** Durable MCP Task views over the existing typed pipeline lifecycle. */
import { createHmac, randomUUID, timingSafeEqual } from "node:crypto";
import {
  ApplicationToolkitError,
  type ApplicationToolkit,
  type ApplicationToolkitWithWorkInbox,
  type ToolkitAccess,
} from "./application-toolkit.js";
import { fileEventHash, fileEventUUID } from "./file-events.js";
import { jobSnapshot, jobValueHash } from "./job-runtime.js";
import { parseWorkInboxReview } from "./live-work-inbox.js";
import { isPinnedPipelineSourceFailure, type PinnedPipelineSourceFailure, type PipelineQuery } from "./pipeline-state.js";
import {
  pipelineTaskFormKey,
  parsePipelineTaskFormReply,
  type PipelineTaskFormRequest,
  type PipelineTaskInputForm,
} from "./pipeline-task-input.js";
import { buildPipelineTaskManifest, PIPELINE_TASK_MANIFEST_URI } from "./pipeline-task-manifest.js";
import {
  handoffPageLimit, handoffPosition, handoffScanLimit, parseTaskHandoff, taskHandoffAfter, taskHandoffApprovalKey, taskHandoffCursor,
  taskHandoffReviewer, type PipelineTaskHandoff, type PipelineTaskHandoffSettings, type PipelineTaskHandoffPage,
  type PipelineTaskHandoffRecord,
} from "./pipeline-task-handoffs.js";
import type { PipelineTaskApproval, PipelineTaskApprovalChoice } from "./pipeline-task-approvals.js";
import { pipelineRecoveryRequest, pipelineRecoveryRequestId, type PipelineTaskRecoveryPolicy } from "./pipeline-task-recovery.js";
import { isPipelinePreviewToken } from "./pipeline-start-preview.js";
import { pipelineToolSQL, pipelineToolWire } from "./pipeline-tool-protocol.js";
import type { QueryMCPResult } from "./query-tool-mcp.js";
import {
  QueryToolError,
  queryScope,
  type QueryOptions,
} from "./query-tools.js";
import type { WorkInboxFollowUp } from "./work-inbox-follow-up.js";
import type { WorkInboxEntry } from "./work-inbox.js";

export { pipelineTaskSchema, pipelineTaskRecoverySchema } from "./pipeline-tool-protocol.js";

export interface PipelineTaskCaller<A> {
  readonly resource: string;
  readonly subject: string;
  readonly clientId: string;
  readonly access: ToolkitAccess<A>;
  readonly supportsForms?: boolean;
}
export interface PipelineTaskBinding {
  readonly task_id: string;
  readonly tool_name: string;
  readonly request_id: string;
  readonly receipt_identity: string;
  readonly created_at: string;
  readonly ttl_ms: number;
  readonly replaces_task_id?: string;
}
type TaskLookup =
  | { readonly task_id: string }
  | { readonly tool_name: string; readonly request_id: string };

type NewTaskBinding = Pick<PipelineTaskBinding, "task_id" | "tool_name" | "request_id" | "receipt_identity" | "ttl_ms">;

export interface PipelineTaskStore {
  load(
    namespace: string,
    owner: string,
    lookup: TaskLookup,
    signal: AbortSignal,
  ): Promise<PipelineTaskBinding | null>;
  bind(
    namespace: string,
    owner: string,
    binding: NewTaskBinding,
    signal: AbortSignal,
  ): Promise<PipelineTaskBinding | null>;
  loadLinked?(namespace: string, owner: string, lookup: TaskLookup, signal: AbortSignal): Promise<PipelineTaskBinding | null>;
  bindLinked?(namespace: string, owner: string, binding: NewTaskBinding & { readonly replaces_task_id: string }, signal: AbortSignal): Promise<PipelineTaskBinding | null>;
}

export interface PipelineTaskSettings<A = unknown> {
  readonly namespace: string;
  readonly ownerKey: string;
  readonly tools: readonly string[];
  readonly store: PipelineTaskStore;
  readonly ttlMs?: number;
  readonly pollIntervalMs?: number;
  readonly publishManifest?: boolean;
  readonly recovery?: PipelineTaskRecoveryPolicy<A>;
  readonly handoffs?: PipelineTaskHandoffSettings;
  readonly approvalHandoffs?: Readonly<Record<string, PipelineTaskApproval<A>>>;
  readonly inputForms?: Readonly<Record<string, PipelineTaskInputForm<A>>>;
}
interface TaskBase {
  readonly taskId: string;
  readonly createdAt: string;
  readonly lastUpdatedAt: string;
  readonly ttlMs: number;
  readonly pollIntervalMs: number;
  readonly statusMessage: string;
}
export interface PipelineTaskHandle extends TaskBase {
  readonly resultType: "task";
  readonly status: "working";
  readonly _meta?: { readonly "io.gregale/pipeline-recovery": {
    readonly replaces_task_id: string; readonly request_id: string; readonly tool: string;
    readonly contract_sha256: string; readonly recovery_request_id: string;
  } };
}
export interface PipelineTaskProgress {
  readonly replaces_task_id?: string;
  readonly source_failure?: PinnedPipelineSourceFailure;
  readonly request_id: string;
  readonly run_id: string | null;
  readonly state:
    | "start_unknown"
    | "running"
    | "awaiting_approval"
    | "awaiting_input"
    | "succeeded"
    | "failed"
    | "cancelled";
  readonly steps: readonly { name: string; status: string; attempt: number }[];
  readonly review?: WorkInboxEntry["review"];
  readonly follow_up?: Extract<WorkInboxFollowUp, { kind: "tool_call" }>;
}
export type PipelineTaskSnapshot = TaskBase & {
  readonly resultType: "complete";
  readonly _meta: { readonly "io.gregale/pipeline": PipelineTaskProgress };
} & (
    | Readonly<{ status: "working" | "cancelled" }>
    | Readonly<{
        status: "input_required";
        inputRequests: Readonly<Record<string, PipelineTaskFormRequest>>;
      }>
    | Readonly<{ status: "completed"; result: QueryMCPResult }>
    | Readonly<{ status: "failed"; error: { code: -32603; message: string } }>
  );
export interface PipelineTaskRuntime<A> {
  readonly manifestResource?: Readonly<{
    uri: string;
    read(access: ToolkitAccess<A>): { contents: { uri: string; mimeType: string; text: string }[] };
  }>;
  readonly recoveryTools?: Readonly<{
    preview: typeof pipelineToolWire.task_recovery.preview;
    confirm: typeof pipelineToolWire.task_recovery.confirm;
    scopes: readonly string[];
  }>;
  previewRecovery?(args: unknown, caller: PipelineTaskCaller<A>, options: QueryOptions): Promise<QueryMCPResult>;
  readonly handoffTools?: Readonly<{
    read: typeof pipelineToolWire.task_handoff.read;
    reply: typeof pipelineToolWire.task_handoff.reply;
    list?: typeof pipelineToolWire.task_handoff.list;
    approval?: typeof pipelineToolWire.task_handoff.approval;
    decide?: typeof pipelineToolWire.task_handoff.decide;
    scopes: readonly string[];
  }>;
  grantHandoff?(taskId: string, grant: { reviewerSubject: string; ttlMs: number }, caller: PipelineTaskCaller<A>, options: QueryOptions): Promise<PipelineTaskHandoff>;
  revokeHandoff?(id: string, caller: PipelineTaskCaller<A>, options: QueryOptions): Promise<void>;
  callHandoff?(name: string, args: unknown, caller: PipelineTaskCaller<A>, options: QueryOptions): Promise<QueryMCPResult>;
  canCreate(name: string): boolean;
  create(
    name: string,
    args: unknown,
    caller: PipelineTaskCaller<A>,
    options: QueryOptions,
  ): Promise<PipelineTaskHandle | QueryMCPResult>;
  get(
    taskId: string,
    caller: PipelineTaskCaller<A>,
    options: QueryOptions,
  ): Promise<PipelineTaskSnapshot>;
  update(
    taskId: string,
    responses: unknown,
    caller: PipelineTaskCaller<A>,
    options: QueryOptions,
  ): Promise<void>;
}

const tId = (v: unknown): v is string =>
  fileEventUUID(v) && v[14] === "4" && /^[89ab]$/.test(v[19] ?? "");
const safeName = (v: string) => /^[a-z][a-z0-9_]{0,63}$/.test(v);
const maxTTL = 7 * 86400000;
const positive = (v: number, max: number) =>
  Number.isSafeInteger(v) && v > 0 && v <= max;

export function postgresPipelineTaskStore(
  query: PipelineQuery,
): PipelineTaskStore {
  async function one(queryText: string, args: unknown[], signal: AbortSignal) {
    signal.throwIfAborted();
    const { rows } = await query(queryText, args, signal);
    signal.throwIfAborted();
    if (rows.length > 1) throw new QueryToolError("invalid_output");
    const row = rows[0] as unknown as (PipelineTaskBinding & { replaces_task_id?: string | null }) | undefined;
    if (!row) return null;
    const { replaces_task_id, ...binding } = row;
    return { ...binding, ...(replaces_task_id != null && { replaces_task_id }) };
  }
  function load(namespace: string, owner: string, lookup: TaskLookup, signal: AbortSignal, linked: boolean) {
    return "task_id" in lookup
      ? one(linked ? pipelineToolSQL.task_recovery_by_id : pipelineToolSQL.task_by_id, [namespace, owner, lookup.task_id], signal)
      : one(linked ? pipelineToolSQL.task_recovery_by_request : pipelineToolSQL.task_by_request, [namespace, owner, lookup.tool_name, lookup.request_id], signal);
  }
  function args(namespace: string, owner: string, row: NewTaskBinding) {
    return [namespace, owner, row.tool_name, row.request_id, row.receipt_identity, row.task_id, row.ttl_ms];
  }
  return {
    load: (namespace, owner, lookup, signal) => load(namespace, owner, lookup, signal, false),
    bind: (namespace, owner, row, signal) => one(pipelineToolSQL.task_bind, args(namespace, owner, row), signal),
    loadLinked: (namespace, owner, lookup, signal) => load(namespace, owner, lookup, signal, true),
    bindLinked: (namespace, owner, row, signal) =>
      one(pipelineToolSQL.task_recovery_bind, [...args(namespace, owner, row), row.replaces_task_id], signal),
  };
}

export function createPipelineTasks<A>(
  toolkit: ApplicationToolkit<A>,
  settings: PipelineTaskSettings<A>,
): PipelineTaskRuntime<A> {
  const { namespace, ownerKey, store } = settings;
  const ttlMs = settings.ttlMs ?? 86400000;
  const pollIntervalMs = settings.pollIntervalMs ?? 2000;
  const starts = new Map<string, string>();
  if (
    !/^[A-Za-z0-9_.:-]{1,96}$/.test(namespace) ||
    typeof ownerKey !== "string" ||
    Buffer.byteLength(ownerKey) < 32 ||
    !positive(ttlMs, maxTTL) ||
    !positive(pollIntervalMs, 60000) ||
    !settings.tools.length ||
    settings.tools.length > 32
  )
    throw new QueryToolError("invalid_input");
  for (const tool of settings.tools) {
    const start = tool + "_start";
    if (
      !safeName(tool) ||
      starts.has(start) ||
      ["start", "status", "result"].some(
        (op) => !Object.hasOwn(toolkit.toolScopes, tool + "_" + op),
      )
    )
      throw new QueryToolError("invalid_input");
    starts.set(start, tool);
  }
  const forms = new Map(Object.entries(settings.inputForms ?? {}));
  for (const [tool, form] of forms) {
    if (
      !starts.has(tool + "_start") ||
      !form ||
      typeof form.inspect !== "function" ||
      typeof form.submit !== "function"
    )
      throw new QueryToolError("invalid_input");
  }
  const recovery = settings.recovery;
  if (recovery && (typeof recovery.prepare !== "function" || typeof store.loadLinked !== "function" ||
      typeof store.bindLinked !== "function" || !Array.isArray(recovery.scopes) || !recovery.scopes.length ||
      new Set(recovery.scopes).size !== recovery.scopes.length ||
      recovery.scopes.some((scope) => typeof scope !== "string" || !/^[\x21\x23-\x5b\x5d-\x7e]{1,128}$/.test(scope))))
    throw new QueryToolError("invalid_input");
  const recoveryScopes = Object.freeze([...(recovery?.scopes ?? [])]);
  const recoveryTools = pipelineToolWire.task_recovery;
  const handoffs = settings.handoffs;
  if (handoffs && (!Array.isArray(handoffs.scopes) || !handoffs.scopes.length ||
      new Set(handoffs.scopes).size !== handoffs.scopes.length ||
      handoffs.scopes.some((scope) => typeof scope !== "string" || !/^[\x21\x23-\x5b\x5d-\x7e]{1,128}$/.test(scope)) ||
      ["grant", "load", "revoke"].some((method) => typeof handoffs.store?.[method as keyof typeof handoffs.store] !== "function")))
    throw new QueryToolError("invalid_input");
  const handoffScopes = Object.freeze([...(handoffs?.scopes ?? [])]);
  const handoffTools = pipelineToolWire.task_handoff;
  const approvals = new Map(Object.entries(settings.approvalHandoffs ?? {}));
  for (const [tool, approval] of approvals) {
    if (!handoffs || !starts.has(tool + "_start") || typeof approval?.inspect !== "function" ||
        typeof approval.decide !== "function" || !Array.isArray(approval.scopes) || !fileEventHash(approval.contractSha256))
      throw new QueryToolError("invalid_input");
  }
  function formFor(tool: string, access: ToolkitAccess<A>) {
    const form = forms.get(tool);
    return form?.scopes.every((scope) => access.scopes.includes(scope))
      ? form
      : undefined;
  }
  function owner(caller: PipelineTaskCaller<A>) {
    if (
      !caller.access ||
      caller.access.principal == null ||
      [caller.resource, caller.subject, caller.clientId].some(
        (value) => typeof value !== "string" || value.length > 2048,
      ) ||
      !caller.resource ||
      !caller.subject
    )
      throw new QueryToolError("not_found");
    return createHmac("sha256", ownerKey)
      .update(
        jobValueHash([
          "gregale.pipeline-task.v1",
          namespace,
          caller.resource,
          caller.subject,
          caller.clientId,
        ]),
      )
      .digest("hex");
  }
  function readable(start: string, access: ToolkitAccess<A>) {
    const tool = starts.get(start);
    if (!tool) throw new QueryToolError("not_found");
    const names = new Set(toolkit.listTools(access).map((t) => t.name));
    if (!names.has(start) || !names.has(tool + "_status"))
      throw new QueryToolError("not_found");
    return tool;
  }
  function receipt(result: QueryMCPResult, tool: string, requestId: string) {
    const content = result.structuredContent;
    if (result.isError || !content || content.state === "not_found")
      throw new QueryToolError("not_found");
    const value = content.receipt as Record<string, unknown> | undefined;
    if (
      !value ||
      value.tool !== tool ||
      value.request_id !== requestId ||
      !fileEventHash(value.identity) ||
      (value.run_id !== null && !fileEventUUID(value.run_id))
    )
      throw new QueryToolError("invalid_output");
    return { content, identity: value.identity, runId: value.run_id };
  }
  function bound(row: PipelineTaskBinding | null, lookup: TaskLookup) {
    if (!row) throw new QueryToolError("not_found");
    if (
      !tId(row.task_id) ||
      (row.replaces_task_id !== undefined && (!tId(row.replaces_task_id) || row.replaces_task_id === row.task_id)) ||
      !starts.has(row.tool_name) ||
      !fileEventUUID(row.request_id) ||
      !fileEventHash(row.receipt_identity) ||
      !positive(row.ttl_ms, maxTTL) ||
      typeof row.created_at !== "string" ||
      !Number.isFinite(Date.parse(row.created_at)) ||
      ("task_id" in lookup
        ? row.task_id !== lookup.task_id
        : row.tool_name !== lookup.tool_name ||
          row.request_id !== lookup.request_id)
    )
      throw new QueryToolError("invalid_output");
    return row;
  }
  function taskBase(
    row: PipelineTaskBinding,
    message: string,
    sampled = row.created_at,
  ): TaskBase {
    return {
      taskId: row.task_id,
      createdAt: row.created_at,
      lastUpdatedAt: sampled,
      ttlMs: row.ttl_ms,
      pollIntervalMs,
      statusMessage: message,
    };
  }
  function scoped<T>(
    options: QueryOptions,
    work: (settings: () => QueryOptions) => Promise<T>,
  ) {
    const end = performance.now() + options.timeoutMs;
    return queryScope(options, (signal) =>
      work(() => {
        signal.throwIfAborted();
        const left = end - performance.now();
        if (left <= 0) throw new QueryToolError("deadline");
        return { signal, timeoutMs: Math.ceil(left) };
      }),
    );
  }
  async function readTask(
    taskId: string,
    caller: PipelineTaskCaller<A>,
    current: () => QueryOptions,
  ) {
    if (!tId(taskId)) throw new QueryToolError("not_found");
    const principal = owner(caller),
      lookup = { task_id: taskId };
    const row = bound(
      await (recovery
        ? store.loadLinked!(namespace, principal, lookup, current().signal!)
        : store.load(namespace, principal, lookup, current().signal!)),
      lookup,
    );
    const tool = readable(row.tool_name, caller.access),
      args = { request_id: row.request_id };
    const result = await toolkit.call(
      tool + "_status",
      args,
      caller.access,
      current(),
    );
    const { content, identity, runId } = receipt(result, tool, row.request_id);
    const state = content.state as PipelineTaskProgress["state"];
    if (identity !== row.receipt_identity)
      throw new QueryToolError("not_found");
    return { row, tool, args, content, identity, runId, state };
  }
  function recoveryError(code: string): QueryMCPResult {
    return { isError: true, content: [{ type: "text", text: JSON.stringify({ code }) }] };
  }

  async function recover(raw: unknown, caller: PipelineTaskCaller<A>, options: QueryOptions, confirm: boolean) {
    if (!recovery) throw new QueryToolError("not_found");
    const args = pipelineRecoveryRequest(raw, confirm);
    return scoped(options, async (current): Promise<PipelineTaskHandle | QueryMCPResult> => {
      const principal = owner(caller);
      if (!recoveryScopes.every((scope) => caller.access.scopes.includes(scope)))
        return recoveryError("not_found");
      let original;
      try { original = await readTask(args.task_id, caller, current); }
      catch (error) {
        if (error instanceof QueryToolError && error.code === "not_found") return recoveryError("not_found");
        throw error;
      }
      const { tool, runId, identity, row } = original;
      if (original.state !== "failed" || original.content.source_failure !== "source_unavailable" || !runId)
        return recoveryError("recovery_blocked");
      const entry = toolkit.listPipelineContracts?.(caller.access).find((item) => item.tool === tool);
      if (!entry || entry.contract_sha256 !== args.expected_contract_sha256)
        return recoveryError("revision_unavailable");

      const request_id = pipelineRecoveryRequestId(ownerKey, principal, row.task_id, args.recovery_request_id);
      const lookup = { tool_name: tool + "_start", request_id };
      const previous = await store.loadLinked!(namespace, principal, lookup, current().signal!);
      const existing = await toolkit.call(tool + "_status", { request_id }, caller.access, current());
      if (existing.isError) return recoveryError("not_found");

      const link = async (receiptIdentity: string, retained?: PipelineTaskBinding | null): Promise<PipelineTaskHandle> => {
        const replacement = bound(retained ?? await store.bindLinked!(namespace, principal, {
          ...lookup, task_id: randomUUID(), receipt_identity: receiptIdentity, ttl_ms: ttlMs, replaces_task_id: row.task_id,
        }, current().signal!), lookup);
        if (replacement.receipt_identity !== receiptIdentity || replacement.replaces_task_id !== row.task_id)
          throw new QueryToolError("not_found");
        return {
          ...taskBase(replacement, "Pipeline replacement accepted"), resultType: "task", status: "working",
          _meta: { "io.gregale/pipeline-recovery": {
            replaces_task_id: row.task_id, request_id, tool, contract_sha256: entry.contract_sha256,
            recovery_request_id: args.recovery_request_id,
          } },
        };
      };

      if (existing.structuredContent?.state !== "not_found") {
        const view = receipt(existing, tool, request_id);
        return confirm ? await link(view.identity, previous) : recoveryError("recovery_exists");
      }
      if (previous) return recoveryError("not_found");
      if (entry.revision?.starts === "retained" || !entry.preview)
        return recoveryError("recovery_blocked");
      const prepared = await recovery.prepare({
        task_id: row.task_id, tool, request_id: row.request_id,
        run_id: runId, receipt_identity: identity, failure: "source_unavailable",
      }, caller.access.principal, current());
      if (!prepared || !Object.hasOwn(prepared, "input") || !fileEventHash(prepared.previous_source_sha256))
        return recoveryError("recovery_blocked");
      const started = await toolkit.call(confirm ? lookup.tool_name : tool + "_preview", {
        request_id, input: prepared.input, expected_contract_sha256: entry.contract_sha256,
        ...(confirm && { preview_token: args.preview_token }),
      }, caller.access, current());
      if (started.isError) return started;
      if (!confirm) {
        const preview = started.structuredContent;
        const token = preview?.preview_token;
        if (!preview || !Object.hasOwn(preview, "summary") || !isPipelinePreviewToken(token) ||
            preview.expires_at !== Number(token.split(".", 1)[0]))
          throw new QueryToolError("invalid_output");
        const content = {
          summary: preview.summary, preview_token: token, expires_at: preview.expires_at,
          replaces_task_id: row.task_id, previous_source_sha256: prepared.previous_source_sha256,
        };
        return { isError: false, structuredContent: content, content: [{ type: "text", text: JSON.stringify(content) }] };
      }
      const view = receipt(started, tool, request_id);
      if (view.content.state !== "started" && view.content.state !== "start_unknown")
        throw new QueryToolError("invalid_output");
      return link(view.identity);
    });
  }

  async function grantHandoff(
    taskId: string, grant: { reviewerSubject: string; ttlMs: number }, caller: PipelineTaskCaller<A>, options: QueryOptions,
  ): Promise<PipelineTaskHandoff> {
    if (!handoffs || typeof grant?.reviewerSubject !== "string" || !grant.reviewerSubject ||
        grant.reviewerSubject.length > 2048 || !positive(grant.ttlMs, maxTTL))
      throw new QueryToolError("invalid_input");
    const { reviewerSubject, ttlMs: lifetime } = grant;
    return scoped(options, async (current) => {
      const issuer_hash = owner(caller);
      const { row, tool, content, state, runId } = await readTask(taskId, caller, current);
      const pending = content.approval as { id?: unknown; expires_at?: unknown } | null;
      const approval = state === "awaiting_approval";
      const provider = approval ? approvals.get(tool) : state === "awaiting_input" ? forms.get(tool) : undefined;
      if (!provider || (!approval && !forms.get(tool)?.submitHandoff) || !runId ||
          !fileEventHash(pending?.id) || typeof pending.expires_at !== "string")
        throw new QueryToolError("not_found");
      const gate = await provider.inspect(pending.id, caller.access.principal, current());
      if (!gate || gate.kind !== (approval ? "approval" : "input") || gate.id !== pending.id || gate.run_id !== runId || gate.decision ||
          !fileEventHash(gate.binding_sha256))
        throw new QueryToolError("not_found");
      const expires = Math.min(Date.now() + lifetime, Date.parse(row.created_at) + row.ttl_ms, Date.parse(pending.expires_at));
      if (!Number.isFinite(expires) || expires <= Date.now()) throw new QueryToolError("not_found");
      const intent: PipelineTaskHandoffRecord = {
        id: randomUUID(), issuer_hash,
        reviewer_hash: taskHandoffReviewer(ownerKey, namespace, caller.resource, reviewerSubject),
        task_id: taskId, tool, run_id: runId, gate_id: gate.id,
        binding_sha256: gate.binding_sha256, expires_at: new Date(expires).toISOString(),
      };
      const saved = parseTaskHandoff(await handoffs.store.grant(namespace, jobSnapshot(intent), current()));
      if (jobValueHash(saved) !== jobValueHash(intent)) throw new QueryToolError("invalid_output");
      return { handoff_id: saved.id, expires_at: saved.expires_at };
    });
  }

  async function revokeHandoff(id: string, caller: PipelineTaskCaller<A>, options: QueryOptions): Promise<void> {
    if (!handoffs || !tId(id)) throw new QueryToolError("not_found");
    await scoped(options, async (current) => {
      if (!await handoffs.store.revoke(namespace, id, owner(caller), current()))
        throw new QueryToolError("not_found");
    });
  }

  function handoffReviewer(caller: PipelineTaskCaller<A>): string {
    owner(caller);
    if (!handoffScopes.every((scope) => caller.access.scopes.includes(scope)))
      throw new QueryToolError("not_found");
    return taskHandoffReviewer(ownerKey, namespace, caller.resource, caller.subject);
  }

  async function activeHandoff(id: string, reviewer: string, options: QueryOptions) {
    const raw = await handoffs!.store.load(namespace, id, reviewer, options);
    if (!raw) return null;
    const row = parseTaskHandoff(raw);
    return row.id === id && row.reviewer_hash === reviewer && Date.parse(row.expires_at) > Date.now() ? row : null;
  }

  async function handoffGate(row: PipelineTaskHandoffRecord, caller: PipelineTaskCaller<A>, options: QueryOptions, retry: boolean) {
    const form = formFor(row.tool, caller.access);
    if (!form?.submitHandoff) return null;
    const gate = await form.inspect(row.gate_id, caller.access.principal, options);
    return gate && gate.kind === "input" && gate.id === row.gate_id && gate.run_id === row.run_id &&
      gate.binding_sha256 === row.binding_sha256 && (gate.can_decide || (retry && gate.decision?.state === "submitted"))
      ? { gate, form } : null;
  }

  async function approvalGate(row: PipelineTaskHandoffRecord, caller: PipelineTaskCaller<A>, options: QueryOptions, retry: boolean) {
    const approval = approvals.get(row.tool);
    if (!approval || !approval.scopes.every((scope) => caller.access.scopes.includes(scope))) return null;
    const gate = await approval.inspect(row.gate_id, caller.access.principal, options);
    return gate && gate.kind === "approval" && gate.id === row.gate_id && gate.run_id === row.run_id &&
      gate.binding_sha256 === row.binding_sha256 && (gate.can_decide ||
        (retry && (gate.decision?.state === "approved" || gate.decision?.state === "rejected")))
      ? { gate, approval } : null;
  }

  async function callApproval(name: string, raw: unknown, caller: PipelineTaskCaller<A>, options: QueryOptions): Promise<QueryMCPResult> {
    const decide = name === handoffTools.decide.name;
    const args = jobSnapshot(raw) as { handoff_id?: unknown; state?: unknown; decision_key?: unknown; input?: unknown };
    if (!args || Object.keys(args).sort().join() !== (decide ? "decision_key,handoff_id,input,state" : "handoff_id") ||
        !tId(args.handoff_id) || (decide && (!fileEventHash(args.decision_key) || (args.state !== "approved" && args.state !== "rejected"))))
      throw new QueryToolError("invalid_input");
    const id = args.handoff_id;
    return scoped(options, async (current) => {
      const reviewer = handoffReviewer(caller);
      const row = await activeHandoff(id, reviewer, current());
      const open = row && await approvalGate(row, caller, current(), decide);
      if (!row || !open) throw new QueryToolError("not_found");
      const { gate, approval } = open;
      const review_sha256 = jobValueHash(gate.review);
      const decision_key = taskHandoffApprovalKey(ownerKey, row, review_sha256);
      let content;
      if (decide) {
        if (!timingSafeEqual(Buffer.from(args.decision_key as string), Buffer.from(decision_key)))
          throw new QueryToolError("invalid_input");
        const receipt = await approval.decide(gate.id, gate.binding_sha256, args.state as PipelineTaskApprovalChoice,
          args.input, caller.access.principal, { namespace, id, reviewer_hash: reviewer, run_id: row.run_id }, current());
        content = { handoff_id: id, ...receipt };
      } else {
        if (jobValueHash(await activeHandoff(id, reviewer, current())) !== jobValueHash(row))
          throw new QueryToolError("not_found");
        content = { handoff_id: id, expires_at: row.expires_at, title: approval.title, review: gate.review,
          review_sha256, contract_sha256: approval.contractSha256, decision_key };
      }
      return { isError: false, structuredContent: content, content: [{ type: "text", text: JSON.stringify(content) }] };
    });
  }

  async function listHandoffs(raw: unknown, caller: PipelineTaskCaller<A>, options: QueryOptions): Promise<PipelineTaskHandoffPage> {
    const store = handoffs?.store;
    if (!store?.scan) throw new QueryToolError("not_found");
    const args = jobSnapshot(raw) as { cursor?: unknown; page_size?: unknown; include_approvals?: unknown };
    if (!args || typeof args !== "object" || Array.isArray(args) ||
        Object.keys(args).some((key) => key !== "cursor" && key !== "page_size" && key !== "include_approvals") ||
        (args.include_approvals !== undefined && typeof args.include_approvals !== "boolean") ||
        (args.page_size !== undefined && (typeof args.page_size !== "number" || !positive(args.page_size, handoffPageLimit))))
      throw new QueryToolError("invalid_input");
    const size = args.page_size === undefined ? 20 : args.page_size as number;
    return scoped(options, async (current) => {
      const reviewer = handoffReviewer(caller);
      const cursorBinding = args.include_approvals ? reviewer + ":approval" : reviewer;
      let after = taskHandoffAfter(ownerKey, cursorBinding, args.cursor);
      const rows = await store.scan!(namespace, reviewer, after, handoffScanLimit + 1, current());
      if (rows.length > handoffScanLimit + 1) throw new QueryToolError("invalid_output");
      const items: PipelineTaskHandoffPage["items"][number][] = [];
      let scanned = 0;
      for (const candidate of rows.slice(0, handoffScanLimit)) {
        const { position } = candidate, row = parseTaskHandoff(candidate.grant);
        if (!handoffPosition.test(position) || position <= after || row.reviewer_hash !== reviewer)
          throw new QueryToolError("invalid_output");
        after = position;
        scanned++;
        const open = await handoffGate(row, caller, current(), false);
        const applicationApproval = !open && args.include_approvals ? await approvalGate(row, caller, current(), false) : null;
        if ((!open && !applicationApproval) || jobValueHash(await activeHandoff(row.id, reviewer, current())) !== jobValueHash(row)) continue;
        const entry = { handoff_id: row.id, expires_at: row.expires_at };
        items.push(open ? { ...entry, question: open.form.request.params.message }
          : { ...entry, question: applicationApproval!.approval.title, kind: "approval" });
        if (items.length === size) break;
      }
      return { items, next_cursor: scanned < rows.length ? taskHandoffCursor(ownerKey, cursorBinding, after) : null };
    });
  }

  async function callHandoff(name: string, raw: unknown, caller: PipelineTaskCaller<A>, options: QueryOptions): Promise<QueryMCPResult> {
    if (name === handoffTools.list.name) {
      const page = await listHandoffs(raw, caller, options);
      return { isError: false, structuredContent: { ...page }, content: [{ type: "text", text: JSON.stringify(page) }] };
    }
    if (approvals.size && (name === handoffTools.approval.name || name === handoffTools.decide.name))
      return callApproval(name, raw, caller, options);
    const submit = name === handoffTools.reply.name;
    if (!handoffs || (!submit && name !== handoffTools.read.name)) throw new QueryToolError("not_found");
    const args = jobSnapshot(raw) as { handoff_id?: unknown; request_key?: unknown; input?: unknown };
    if (!args || Object.keys(args).sort().join() !== (submit ? "handoff_id,input,request_key" : "handoff_id") ||
        !tId(args.handoff_id) || (submit && typeof args.request_key !== "string"))
      throw new QueryToolError("invalid_input");
    const id = args.handoff_id;
    return scoped(options, async (current) => {
      const reviewer = handoffReviewer(caller);
      const row = await activeHandoff(id, reviewer, current());
      const open = row && await handoffGate(row, caller, current(), submit);
      if (!row || !open?.form.submitHandoff) throw new QueryToolError("not_found");
      const { gate, form } = open;
      const anchor = [row.task_id, row.gate_id, row.run_id, row.id] as const;
      if (submit) {
        const reply = parsePipelineTaskFormReply(ownerKey, anchor, {
          [args.request_key as string]: { action: "accept", content: args.input },
        });
        if (reply.id !== gate.id || reply.binding_sha256 !== gate.binding_sha256)
          throw new QueryToolError("invalid_input");
        await open.form.submitHandoff(reply.id, reply.binding_sha256, reply.content, caller.access.principal, {
          namespace, id, reviewer_hash: reviewer, run_id: row.run_id,
        }, current());
      } else if (jobValueHash(await activeHandoff(id, reviewer, current())) !== jobValueHash(row)) {
        throw new QueryToolError("not_found");
      }
      const content = submit ? { handoff_id: id, submitted: true } : {
        handoff_id: id, expires_at: row.expires_at, form: form.request, request_key: pipelineTaskFormKey(ownerKey, anchor, gate),
      };
      return { isError: false, structuredContent: content, content: [{ type: "text", text: JSON.stringify(content) }] };
    });
  }

  return Object.freeze<PipelineTaskRuntime<A>>({
    ...(handoffs && {
      handoffTools: Object.freeze({
        read: handoffTools.read, reply: handoffTools.reply, scopes: handoffScopes,
        ...(approvals.size > 0 && { approval: handoffTools.approval, decide: handoffTools.decide }),
        ...(typeof handoffs.store.scan === "function" && { list: handoffTools.list }),
      }),
      grantHandoff, revokeHandoff, callHandoff,
    }),
    ...(settings.publishManifest === true && {
      manifestResource: Object.freeze({
        uri: PIPELINE_TASK_MANIFEST_URI,
        read(access: ToolkitAccess<A>) {
          const entries = (toolkit.listPipelineContracts?.(access) ?? [])
            .filter(({ tool }) => starts.has(tool + "_start"))
            .map((entry) => {
              const params = formFor(entry.tool, access)?.request.params;
              return { ...entry, clarification: params ? { message: params.message, schema: params.requestedSchema } : null };
            });
          return { contents: [{
            uri: PIPELINE_TASK_MANIFEST_URI, mimeType: "application/json",
            text: JSON.stringify(buildPipelineTaskManifest(entries)),
          }] };
        },
      }),
    }),
    ...(recovery && {
      recoveryTools: Object.freeze({ ...recoveryTools, scopes: recoveryScopes }),
      async previewRecovery(args: unknown, caller: PipelineTaskCaller<A>, options: QueryOptions) {
        const result = await recover(args, caller, options, false);
        if ("isError" in result) return result;
        throw new QueryToolError("invalid_output");
      },
    }),
    canCreate: (name) => starts.has(name) || (!!recovery && name === recoveryTools.confirm.name),
    async create(name, args, caller, options) {
      if (recovery && name === recoveryTools.confirm.name)
        return recover(args, caller, options, true);
      const input = jobSnapshot(args) as { request_id?: unknown } | null;
      const request_id = input?.request_id;
      if (!fileEventUUID(request_id)) throw new QueryToolError("invalid_input");
      const lookup = { tool_name: name, request_id };
      return scoped(options, async (current) => {
        const principal = owner(caller),
          tool = readable(name, caller.access);
        const previous = await store.load(
          namespace,
          principal,
          lookup,
          current().signal!,
        );
        if (previous) {
          const row = bound(previous, lookup);
          const status = receipt(
            await toolkit.call(
              tool + "_status",
              { request_id },
              caller.access,
              current(),
            ),
            tool,
            request_id,
          );
          if (status.identity !== row.receipt_identity)
            throw new QueryToolError("not_found");
        }
        const started = await toolkit.call(
          name,
          input,
          caller.access,
          current(),
        );
        if (started.isError) return started;
        const view = receipt(started, tool, request_id);
        if (
          view.content.state !== "started" &&
          view.content.state !== "start_unknown"
        )
          throw new QueryToolError("invalid_output");
        const row = bound(
          await store.bind(
            namespace,
            principal,
            {
              ...lookup,
              task_id: randomUUID(),
              receipt_identity: view.identity,
              ttl_ms: ttlMs,
            },
            current().signal!,
          ),
          lookup,
        );
        if (row.receipt_identity !== view.identity)
          throw new QueryToolError("not_found");
        return {
          ...taskBase(row, "Pipeline Task accepted"),
          resultType: "task",
          status: "working",
        };
      });
    },
    get(taskId, caller, options) {
      if (!tId(taskId)) return Promise.reject(new QueryToolError("not_found"));
      return scoped(options, async (current) => {
        const { row, tool, args, content, identity, runId, state } =
          await readTask(taskId, caller, current);
        if (
          ![
            "start_unknown",
            "running",
            "awaiting_approval",
            "awaiting_input",
            "succeeded",
            "failed",
            "cancelled",
          ].includes(state)
        )
          throw new QueryToolError("invalid_output");
        const rawSteps = content.steps;
        if (!Array.isArray(rawSteps) || rawSteps.length > 1000)
          throw new QueryToolError("invalid_output");
        const steps = rawSteps.map(({ name, status, attempt }) => {
          if (
            typeof name !== "string" ||
            name.length > 128 ||
            typeof status !== "string" ||
            status.length > 32 ||
            !Number.isSafeInteger(attempt) ||
            attempt < 0
          )
            throw new QueryToolError("invalid_output");
          return { name, status, attempt };
        });
        const failure = content.source_failure;
        if (failure !== undefined && (state !== "failed" || !isPinnedPipelineSourceFailure(failure)))
          throw new QueryToolError("invalid_output");
        const progress: PipelineTaskProgress = {
          ...(row.replaces_task_id && { replaces_task_id: row.replaces_task_id }),
          ...(isPinnedPipelineSourceFailure(failure) && { source_failure: failure }),
          ...args,
          run_id: runId,
          state,
          steps,
        };
        let message = "Pipeline " + state.replaceAll("_", " ");
        if (state === "awaiting_approval" || state === "awaiting_input") {
          const raw = content.approval as Record<string, unknown> | null;
          const review = parseWorkInboxReview(
            raw && {
              kind: raw.kind,
              id: raw.id,
              expires_at: raw.expires_at,
              review_url: raw.review_url,
            },
            state,
            runId,
          );
          Object.assign(progress, { review });
          message += " · " + review.review_url;
        }
        const resolve = (toolkit as Partial<ApplicationToolkitWithWorkInbox<A>>)
          .resolveWorkInboxFollowUp;
        if (state === "start_unknown") {
          message =
            "Pipeline start uncertain. Resolve the existing receipt before retrying.";
          if (resolve) {
            try {
              const follow = await resolve(
                { kind: "pipeline", name: tool, ...args },
                caller.access,
                current(),
              );
              if (
                follow.kind === "tool_call" &&
                follow.name === tool + "_reconcile"
              )
                Object.assign(progress, { follow_up: follow });
            } catch (error) {
              if (
                !(
                  error instanceof QueryToolError && error.code === "not_found"
                ) &&
                !(
                  error instanceof ApplicationToolkitError &&
                  error.code === "tool_not_found"
                )
              )
                throw error;
            }
          }
        }
        const base = {
          ...taskBase(row, message, new Date().toISOString()),
          resultType: "complete" as const,
          _meta: { "io.gregale/pipeline": progress },
        };
        const form =
          state === "awaiting_input" &&
          caller.supportsForms &&
          formFor(tool, caller.access);
        if (form && runId && progress.review) {
          const gate = await form.inspect(
            progress.review.id,
            caller.access.principal,
            current(),
          );
          if (
            gate &&
            gate.kind === "input" &&
            gate.id === progress.review.id &&
            gate.run_id === runId &&
            gate.can_decide &&
            !gate.decision &&
            fileEventHash(gate.binding_sha256)
          ) {
            const key = pipelineTaskFormKey(
              ownerKey,
              [taskId, identity, runId],
              gate,
            );
            return {
              ...base,
              status: "input_required",
              inputRequests: { [key]: form.request },
            };
          }
        }
        if (state === "succeeded") {
          if (
            !toolkit
              .listTools(caller.access)
              .some((entry) => entry.name === tool + "_result")
          )
            throw new QueryToolError("not_found");
          const output = await toolkit.call(
            tool + "_result",
            args,
            caller.access,
            current(),
          );
          const view = receipt(output, tool, row.request_id);
          if (
            view.identity !== identity ||
            view.runId !== runId ||
            view.content.state !== "succeeded" ||
            !Object.hasOwn(view.content, "output")
          )
            throw new QueryToolError("invalid_output");
          return {
            ...base,
            status: "completed",
            result: { ...output, resultType: "complete" },
          };
        }
        if (state === "failed")
          return {
            ...base,
            status: "failed",
            error: { code: -32603, message: "Pipeline failed" },
          };
        return {
          ...base,
          status: state === "cancelled" ? "cancelled" : "working",
        };
      });
    },
    async update(taskId, responses, caller, options) {
      const captured = structuredClone(responses);
      return scoped(options, async (current) => {
        const { tool, content, identity, runId, state } = await readTask(
          taskId,
          caller,
          current,
        );
        if (!forms.has(tool)) throw new QueryToolError("invalid_input");
        const form = formFor(tool, caller.access);
        if (!form) throw new QueryToolError("not_found");
        if (!runId) throw new QueryToolError("invalid_input");
        const reply = parsePipelineTaskFormReply(
          ownerKey,
          [taskId, identity, runId],
          captured,
        );
        const gate = await form.inspect(
          reply.id,
          caller.access.principal,
          current(),
        );
        if (
          !gate ||
          gate.kind !== "input" ||
          gate.id !== reply.id ||
          gate.run_id !== runId ||
          gate.binding_sha256 !== reply.binding_sha256
        )
          throw new QueryToolError("invalid_input");
        const pending = content.approval as { id?: unknown } | null;
        if (
          gate.decision?.state !== "submitted" &&
          (gate.decision ||
            !gate.can_decide ||
            state !== "awaiting_input" ||
            pending?.id !== reply.id)
        )
          throw new QueryToolError("invalid_input");
        await form.submit(
          reply.id,
          reply.binding_sha256,
          reply.content,
          caller.access.principal,
          current(),
        );
      });
    },
  });
}
