/** Read-only discovery over existing SDK receipts, with fresh adapter inspection. */
import { eventPipelineScope } from "./event-pipeline-state.js";
import { fileEventHash, fileEventUUID } from "./file-events.js";
import {
  JobContract,
  jobHash,
  jobInputId,
  jobSnapshot,
  jobValueHash,
} from "./job-runtime.js";
import {
  outboundActionIdentity,
  OutboundActionError,
  type OutboundAction,
  type OutboundActionState,
} from "./outbound-actions.js";
import {
  pipelineName,
  type PipelineQuery,
  type PipelineScope,
} from "./pipeline-state.js";
import {
  PipelineToolError,
  pipelineToolIdentity,
  type PipelineTool,
  type PipelineToolStatus,
  type PipelineToolStartMatch,
} from "./pipeline-tools.js";
import {
  defineAuthorizedQueryTool,
  QueryToolError,
  type QueryActor,
  type QueryAuthorizedLoadContext,
  type QueryOptions,
  type QueryTool,
  type QueryToolDefinition,
} from "./query-tools.js";
import {
  workInboxFollowUpSpec,
  workInboxSpec,
  workInboxSQL,
} from "./work-inbox-protocol.js";
export { workInboxSchema } from "./work-inbox-protocol.js";

export type WorkInboxKind = "pipeline" | "action";
export type WorkInboxState = PipelineToolStatus["state"] | OutboundActionState;
export type WorkInboxNextStep =
  | "reconcile_start"
  | "open_review"
  | "inspect"
  | "read_result"
  | "reconcile_action"
  | "review_action"
  | "none";
export interface WorkInboxFilter {
  readonly kind?: WorkInboxKind;
  readonly state?: WorkInboxState;
  readonly business_ref?: string;
}
export interface WorkInboxEntry {
  readonly kind: WorkInboxKind;
  readonly name: string;
  readonly state: WorkInboxState;
  readonly next_step: WorkInboxNextStep;
  readonly request_id: string;
  readonly business_ref?: string;
  readonly run_id?: string;
  readonly action_id?: string;
  readonly review?: Readonly<{
    kind: "approval" | "input";
    id: string;
    expires_at: string;
    review_url: string;
  }>;
}
export interface WorkInboxCandidate {
  readonly kind: WorkInboxKind;
  readonly name: string;
  readonly identity: string;
  readonly request_id: string;
  readonly position: string;
}
export interface WorkInboxSelector {
  readonly kind: WorkInboxKind;
  readonly name: string;
  readonly contract_sha256?: string;
  readonly receipt_subject_id?: string;
}
export interface WorkInboxScan {
  readonly scope: PipelineScope;
  readonly actor: QueryActor;
  readonly selectors: readonly WorkInboxSelector[];
  readonly after: string | null;
  readonly limit: number;
}
export type WorkInboxReference = Pick<
  WorkInboxEntry,
  "kind" | "name" | "request_id" | "business_ref"
>;
export interface WorkInbox<A>
  extends QueryTool<WorkInboxFilter, WorkInboxEntry, A> {
  findEntry(
    reference: WorkInboxReference,
    access: A,
    options: QueryOptions,
  ): Promise<WorkInboxEntry | null>;
  findAcceptedRun(
    reference: WorkInboxReference,
    access: A,
    options: QueryOptions,
  ): Promise<string | null>;
}
export interface WorkInboxExactLookup {
  readonly scope: PipelineScope;
  readonly actor: QueryActor;
  readonly selector: WorkInboxSelector;
  readonly identity: string;
  readonly request_id: string;
}
export interface WorkInboxStore {
  /** Exact scoped lookup for follow-ups. Existing discovery stores can omit it. */
  hasReceipt?(
    request: WorkInboxExactLookup,
    signal: AbortSignal,
  ): Promise<boolean>;
  scan(
    request: WorkInboxScan,
    signal: AbortSignal,
  ): Promise<readonly WorkInboxCandidate[]>;
}
export interface WorkInboxSource<A> extends WorkInboxSelector {
  /** Public entry name when it differs from the stored selector. */
  readonly entryName?: string;
  findAcceptedRun?(
    requestId: string,
    access: A,
    options: QueryOptions,
  ): Promise<PipelineToolStartMatch>;
  inspect(
    candidate: WorkInboxCandidate,
    actor: QueryActor,
    scope: PipelineScope,
    access: A,
    options: QueryOptions,
  ): Promise<WorkInboxEntry | null>;
}
export interface WorkInboxDefinition<A> {
  readonly scope: PipelineScope;
  readonly cursorKey: string;
  readonly sources: readonly WorkInboxSource<A>[];
  readonly store: WorkInboxStore;
  readonly authorize: QueryToolDefinition<
    WorkInboxFilter,
    WorkInboxEntry,
    A
  >["authorize"];
  readonly businessReference?: (
    candidate: WorkInboxCandidate,
    actor: QueryActor,
    signal: AbortSignal,
  ) => Promise<string | null>;
  readonly scanLimit?: number;
}
export function workInboxPosition(
  kind: WorkInboxKind,
  identity: string,
): string {
  if (!fileEventHash(identity) || !["pipeline", "action"].includes(kind))
    throw new QueryToolError("invalid_output");
  return (
    jobHash(Buffer.from(identity, "hex")) + (kind === "pipeline" ? "p" : "a")
  );
}
export function postgresWorkInboxStore(query: PipelineQuery): WorkInboxStore {
  return {
    async hasReceipt({ scope, actor, selector, identity, request_id }, signal) {
      signal.throwIfAborted();
      const pipeline = selector.kind === "pipeline";
      const { rows } = await query(
        pipeline ? workInboxSQL.pipeline_exact : workInboxSQL.action_exact,
        [
          identity,
          JSON.stringify(scope),
          actor.owner_id,
          selector.name,
          request_id,
          ...(pipeline
            ? [
                selector.receipt_subject_id ?? actor.subject_id,
                selector.contract_sha256,
              ]
            : []),
        ],
        signal,
      );
      signal.throwIfAborted();
      if (rows.length > 1) throw new QueryToolError("invalid_output");
      return rows.length === 1;
    },
    async scan(request, signal) {
      const candidates: WorkInboxCandidate[] = [];
      for (const kind of ["pipeline", "action"] as const) {
        const descriptors = request.selectors
          .filter((s) => s.kind === kind)
          .map(({ name, contract_sha256, receipt_subject_id }) => ({
            name,
            contract_sha256,
            receipt_subject_id,
          }));
        if (!descriptors.length) continue;
        signal.throwIfAborted();
        const { rows } = await query(
          workInboxSQL[kind],
          [
            JSON.stringify(descriptors),
            JSON.stringify(request.scope),
            request.actor.owner_id,
            request.actor.subject_id,
            request.after ?? "",
            request.limit,
          ],
          signal,
        );
        signal.throwIfAborted();
        if (rows.length > request.limit)
          throw new QueryToolError("invalid_output");
        candidates.push(...(rows as unknown as WorkInboxCandidate[]));
      }
      return candidates
        .sort((a, b) =>
          a.position < b.position ? -1 : a.position > b.position ? 1 : 0,
        )
        .slice(0, request.limit);
    },
  };
}
export function pipelineWorkInboxSource<I, O, A>(
  tool: PipelineTool<I, O, A>,
  options: { readonly receiptSubjectId?: string } = {},
): WorkInboxSource<A> {
  const name = tool.name,
    contract_sha256 = tool.contract.sha256,
    subject = options.receiptSubjectId;
  return {
    kind: "pipeline",
    name,
    contract_sha256,
    ...(subject === undefined ? {} : { receipt_subject_id: subject }),
    ...(tool.findAcceptedRun
      ? { findAcceptedRun: tool.findAcceptedRun.bind(tool) }
      : {}),
    async inspect(candidate, actor, scope, access, options) {
      const status = await tool.inspect(candidate.request_id, access, options);
      if (!status) return null;
      const { intent, checkpoint } = status.receipt;
      if (
        intent.identity !== candidate.identity ||
        intent.name !== name ||
        intent.contract_sha256 !== contract_sha256 ||
        intent.request_id !== candidate.request_id ||
        intent.owner_id !== actor.owner_id ||
        intent.subject_id !== (subject ?? actor.subject_id) ||
        jobValueHash(intent.scope) !== jobValueHash(scope)
      )
        return null;
      const { state, approval } = status;
      const next_step = approval
        ? "open_review"
        : state === "succeeded"
          ? "read_result"
          : state === "start_unknown"
            ? "reconcile_start"
            : state === "running"
              ? "inspect"
              : "none";
      return {
        kind: "pipeline",
        name,
        state,
        next_step,
        request_id: candidate.request_id,
        ...(checkpoint ? { run_id: checkpoint.run_id } : {}),
        ...(approval
          ? {
              review: {
                kind: approval.kind,
                id: approval.id,
                expires_at: approval.expires_at,
                review_url: approval.review_url,
              },
            }
          : {}),
      };
    },
  };
}
export function outboundWorkInboxSource<I, O, A>(
  action: OutboundAction<I, O, A>,
): WorkInboxSource<A> {
  const toolName = action.tool.name;
  if (!toolName.startsWith("action_"))
    throw new QueryToolError("invalid_input");
  return {
    kind: "action",
    name: toolName.slice(7),
    entryName: toolName,
    async inspect(candidate, _actor, _scope, access, options) {
      const view = await action.inspect(candidate.identity, access, options);
      if (view.action_id !== candidate.identity) return null;
      const { state } = view.outcome;
      const next_step =
        state === "unknown" || state === "pending"
          ? "reconcile_action"
          : state === "needs_review"
            ? "review_action"
            : state === "succeeded"
              ? "read_result"
              : state === "prepared"
                ? "inspect"
                : "none";
      return {
        kind: "action",
        name: toolName,
        state,
        next_step,
        request_id: candidate.request_id,
        action_id: view.action_id,
      };
    },
  };
}
function hidden(error: unknown): boolean {
  return (
    (error instanceof PipelineToolError && error.code === "unauthorized") ||
    (error instanceof OutboundActionError &&
      ["unauthorized", "not_found", "conflict"].includes(error.code))
  );
}
export function defineWorkInbox<A>(
  definition: WorkInboxDefinition<A>,
): WorkInbox<A> {
  const d = { ...definition },
    scope = eventPipelineScope(d.scope),
    scanLimit = d.scanLimit ?? 24;
  const entries = new Map<string, WorkInboxSource<A>>();
  if (
    !Number.isSafeInteger(scanLimit) ||
    scanLimit < 1 ||
    scanLimit > 100 ||
    !d.sources.length ||
    d.sources.length > 32
  ) {
    throw new QueryToolError("invalid_input");
  }
  for (const source of d.sources) {
    const key = source.kind + ":" + source.name;
    if (
      !["pipeline", "action"].includes(source.kind) ||
      !/^[a-z][a-z0-9_]{0,47}$/.test(source.name) ||
      (source.kind === "pipeline"
        ? !fileEventHash(source.contract_sha256) ||
          (source.receipt_subject_id !== undefined &&
            !pipelineName(source.receipt_subject_id))
        : source.contract_sha256 !== undefined ||
          source.receipt_subject_id !== undefined) ||
      entries.has(key) ||
      typeof source.inspect !== "function"
    )
      throw new QueryToolError("invalid_input");
    entries.set(key, { ...source, inspect: source.inspect.bind(source) });
  }
  const selectors = jobSnapshot(
    [...entries.keys()].sort().map((key) => {
      const { kind, name, contract_sha256, receipt_subject_id } =
        entries.get(key)!;
      return {
        kind,
        name,
        ...(contract_sha256 ? { contract_sha256 } : {}),
        ...(receipt_subject_id ? { receipt_subject_id } : {}),
      };
    }),
  );
  const contract = new JobContract<WorkInboxFilter, WorkInboxEntry>(
    workInboxSpec,
  );
  function authorize(input: WorkInboxFilter, access: A, signal: AbortSignal) {
    if (
      input.business_ref !== undefined &&
      (!d.businessReference || !jobInputId(input.business_ref))
    )
      throw new QueryToolError("invalid_input");
    return d.authorize(input, access, signal);
  }
  function receiptIdentity(
    source: WorkInboxSource<A>,
    actor: QueryActor,
    requestId: string,
  ) {
    return source.kind === "action"
      ? outboundActionIdentity(scope, source.name, requestId)
      : pipelineToolIdentity(
          scope,
          source.name,
          source.receipt_subject_id ?? actor.subject_id,
          requestId,
        );
  }
  async function inspect(
    source: WorkInboxSource<A>,
    candidate: WorkInboxCandidate,
    input: WorkInboxFilter,
    context: QueryAuthorizedLoadContext,
    access: A,
  ) {
    try {
      const item = await source.inspect(
        candidate,
        context.actor,
        scope,
        access,
        {
          signal: context.signal,
          timeoutMs: context.remainingMs(),
        },
      );
      context.signal.throwIfAborted();
      if (!item || (input.state && item.state !== input.state)) return null;
      const reference = d.businessReference
        ? await d.businessReference(candidate, context.actor, context.signal)
        : null;
      context.signal.throwIfAborted();
      if (reference !== null && !jobInputId(reference))
        throw new QueryToolError("invalid_output");
      if (input.business_ref !== undefined && input.business_ref !== reference)
        return null;
      return {
        ...item,
        ...(reference === null ? {} : { business_ref: reference }),
      };
    } catch (error) {
      if (hidden(error)) return null;
      if (
        (error instanceof PipelineToolError ||
          error instanceof OutboundActionError) &&
        error.code === "deadline"
      )
        throw new QueryToolError("deadline");
      throw error;
    }
  }

  async function load(
    input: WorkInboxFilter,
    context: QueryAuthorizedLoadContext,
    access: A,
  ) {
    const selected = selectors.filter(
      (s) => !input.kind || input.kind === s.kind,
    );
    const rows = await d.store.scan(
      {
        scope,
        actor: context.actor,
        selectors: selected,
        after: context.after,
        limit: scanLimit + 1,
      },
      context.signal,
    );
    context.signal.throwIfAborted();
    if (!Array.isArray(rows) || rows.length > scanLimit + 1)
      throw new QueryToolError("invalid_output");
    const items: WorkInboxEntry[] = [];
    let position = context.after ?? "",
      consumed = 0;
    for (const candidate of rows) {
      if (consumed >= scanLimit || items.length >= context.limit) break;
      const { kind, name, identity, request_id } = candidate;
      const source = selected.some((s) => s.kind === kind && s.name === name)
        ? entries.get(kind + ":" + name)
        : undefined;
      if (
        !source ||
        !fileEventUUID(request_id) ||
        workInboxPosition(kind, identity) !== candidate.position ||
        candidate.position <= position
      ) {
        throw new QueryToolError("invalid_output");
      }
      if (identity !== receiptIdentity(source, context.actor, request_id))
        throw new QueryToolError("invalid_output");
      position = candidate.position;
      consumed++;
      const item = await inspect(source, candidate, input, context, access);
      if (item) items.push(item);
    }
    return { items, after: consumed < rows.length ? position : null };
  }
  const query = defineAuthorizedQueryTool(contract, {
    name: "work_inbox",
    description:
      "Find existing work, pending reviews and uncertain operations by current access. Follow next_cursor even after an empty page.",
    scope,
    cursorKey: d.cursorKey,
    revision: jobValueHash([selectors, scanLimit, "work-inbox-v1"]),
    maxPageSize: 20,
    allowEmptyPages: true,
    authorize,
    load,
  });
  const referenceSpec = {
    ...workInboxSpec,
    name: "work-inbox-entry",
    input: workInboxFollowUpSpec.input,
  };
  function filters(reference: WorkInboxReference): WorkInboxFilter {
    const { kind, business_ref } = reference;
    return { kind, ...(business_ref === undefined ? {} : { business_ref }) };
  }
  function authorizeExact(
    reference: WorkInboxReference,
    access: A,
    signal: AbortSignal,
  ) {
    if (
      !fileEventUUID(reference.request_id) ||
      !/^[a-z][a-z0-9_]{0,63}$/.test(reference.name)
    )
      throw new QueryToolError("invalid_input");
    return authorize(filters(reference), access, signal);
  }
  async function lookupEntry(
    reference: WorkInboxReference,
    context: QueryAuthorizedLoadContext,
    access: A,
  ) {
    const matching = [...entries.values()].filter(
      (s) =>
        s.kind === reference.kind && (s.entryName ?? s.name) === reference.name,
    );
    const source = matching[0];
    if (!source || matching.length !== 1) return null;
    if (!d.store.hasReceipt) throw new QueryToolError("query_failed");
    const { kind, name } = source,
      request_id = reference.request_id;
    const identity = receiptIdentity(source, context.actor, request_id);
    const candidate = {
      kind,
      name,
      identity,
      request_id,
      position: workInboxPosition(kind, identity),
    };
    const found = await d.store.hasReceipt(
      {
        scope,
        actor: context.actor,
        selector: source,
        identity,
        request_id,
      },
      context.signal,
    );
    context.signal.throwIfAborted();
    if (!found) return null;
    const item = await inspect(
      source,
      candidate,
      filters(reference),
      context,
      access,
    );
    if (!item) return null;
    if (
      item.kind !== kind ||
      item.name !== reference.name ||
      item.request_id !== request_id ||
      (kind === "action" && item.action_id !== identity)
    )
      throw new QueryToolError("invalid_output");
    return { item, source };
  }
  const exactOptions = {
    scope,
    cursorKey: d.cursorKey,
    maxPageSize: 1,
    authorize: authorizeExact,
  };
  const exact = defineAuthorizedQueryTool<
    WorkInboxReference,
    WorkInboxEntry,
    A
  >(new JobContract(referenceSpec), {
    ...exactOptions,
    name: "work_inbox_entry",
    description: "Read one exact inbox entry by its receipt reference.",
    revision: "work-inbox-entry-v1",
    async load(reference, context, access) {
      const entry = await lookupEntry(reference, context, access);
      return { items: entry ? [entry.item] : [], after: null };
    },
  });
  const accepted = defineAuthorizedQueryTool<
    WorkInboxReference,
    { run_id: string },
    A
  >(
    new JobContract({
      ...referenceSpec,
      name: "work-inbox-start",
      output: {
        type: "object",
        additionalProperties: false,
        required: ["run_id"],
        properties: { run_id: workInboxSpec.output.properties.run_id },
      },
    }),
    {
      ...exactOptions,
      name: "work_inbox_start",
      description:
        "Verify the retained native start for one unresolved pipeline receipt.",
      revision: "work-inbox-start-v1",
      async load(reference, context, access) {
        const entry = await lookupEntry(reference, context, access);
        if (
          !entry ||
          entry.item.state !== "start_unknown" ||
          entry.source.kind !== "pipeline" ||
          !entry.source.findAcceptedRun
        )
          return { items: [], after: null };
        try {
          const outcome = await entry.source.findAcceptedRun(
            reference.request_id,
            access,
            {
              timeoutMs: context.remainingMs(),
              signal: context.signal,
            },
          );
          context.signal.throwIfAborted();
          if (outcome.request_id !== reference.request_id)
            throw new QueryToolError("invalid_output");
          if (outcome.state === "start_unknown")
            return { items: [], after: null };
          if (outcome.state !== "found" || !fileEventUUID(outcome.run_id))
            throw new QueryToolError("invalid_output");
          return { items: [{ run_id: outcome.run_id }], after: null };
        } catch (error) {
          if (error instanceof PipelineToolError) {
            if (error.code === "unauthorized")
              throw new QueryToolError("unauthorized");
            if (error.code === "deadline") throw new QueryToolError("deadline");
            if (error.code === "receipt_conflict")
              throw new QueryToolError("invalid_output");
          }
          throw error;
        }
      },
    },
  );
  function referenceFields(reference: WorkInboxReference) {
    if (!reference || typeof reference !== "object" || Array.isArray(reference))
      throw new QueryToolError("invalid_input");
    const { kind, name, request_id, business_ref } = reference;
    return {
      kind,
      name,
      request_id,
      ...(business_ref === undefined ? {} : { business_ref }),
    };
  }
  return Object.freeze<WorkInbox<A>>({
    ...query,
    async findEntry(reference, access, options) {
      const page = await exact.page(referenceFields(reference), access, {
        timeoutMs: options.timeoutMs,
        signal: options.signal,
      });
      return page.items[0] ?? null;
    },
    async findAcceptedRun(reference, access, options) {
      const page = await accepted.page(referenceFields(reference), access, {
        timeoutMs: options.timeoutMs,
        signal: options.signal,
      });
      return page.items[0]?.run_id ?? null;
    },
  });
}
