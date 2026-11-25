/** Portable action identity, immutable request and monotonic observation protocol. */
import {
  JobContract,
  jobSnapshot,
  jobValueHash,
  type JobSchema,
} from "./job-runtime.js";
import { eventPipelineScope } from "./event-pipeline-state.js";
import {
  pipelineName,
  type PipelineScope,
  type PipelineQuery,
} from "./pipeline-state.js";
import { fileEventHash, fileEventUUID } from "./file-events.js";
import { outboundActionSQL as SQL } from "./outbound-action-protocol.js";
export { outboundActionSchema } from "./outbound-action-protocol.js";
export type OutboundActionState =
  | "prepared"
  | "unknown"
  | "pending"
  | "succeeded"
  | "failed"
  | "needs_review"
  | "cancelled";
export type OutboundActionOperation =
  | "prepare"
  | "inspect"
  | "reconcile"
  | "cancel"
  | "start"
  | "result";
export class OutboundActionError extends Error {
  constructor(
    readonly code:
      | "invalid_input"
      | "unauthorized"
      | "conflict"
      | "not_found"
      | "store_failed"
      | "approval_required"
      | "execution_inactive"
      | "provider_failed"
      | "limit"
      | "deadline"
      | "cancelled",
  ) {
    super("Outbound action: " + code);
    this.name = "OutboundActionError";
  }
}
export interface OutboundActionActor {
  readonly actor_id: string;
  readonly owner_id: string;
}
export interface OutboundActionRequest {
  readonly path: string;
  readonly content_type:
    | "application/json"
    | "application/x-www-form-urlencoded";
  readonly body: string;
  readonly api_version?: string;
}
export interface OutboundActionObservation<O> {
  readonly state: OutboundActionState;
  readonly code: string;
  readonly reference?: string;
  readonly result?: O;
}
export interface OutboundActionReference {
  readonly action_id: string;
  readonly binding_sha256: string;
}
export interface OutboundActionReview<I> extends OutboundActionReference {
  readonly name: string;
  readonly revision: string;
  readonly input: I;
}
export interface OutboundActionView<O> extends OutboundActionReference {
  readonly outcome: OutboundActionObservation<O>;
}
export interface OutboundActionResult<O> {
  readonly decision: "approved" | "rejected" | "expired" | "cancelled";
  readonly action: OutboundActionView<O>;
}
export interface OutboundActionIntent<I> {
  readonly version: 1;
  readonly identity: string;
  readonly scope: PipelineScope;
  readonly name: string;
  readonly revision: string;
  readonly config_sha256: string;
  readonly owner_id: string;
  readonly request_id: string;
  readonly input: I;
  readonly input_sha256: string;
  readonly request: OutboundActionRequest;
  readonly binding_sha256: string;
}
export interface OutboundActionRecord<I, O> {
  readonly intent: OutboundActionIntent<I>;
  readonly version: number;
  readonly outcome: OutboundActionObservation<O>;
}
export interface OutboundActionStore {
  attempt(
    namespace: string,
    requestId: string,
    signal: AbortSignal,
  ): Promise<string | null>;
  bind(
    namespace: string,
    requestId: string,
    identity: string,
    signal: AbortSignal,
  ): Promise<void>;
  run(
    identity: string,
    runId: string,
    signal: AbortSignal,
  ): Promise<string | null>;
  registerRun(
    identity: string,
    runId: string,
    digest: string,
    signal: AbortSignal,
  ): Promise<void>;
  load(identity: string, signal: AbortSignal): Promise<unknown | null>;
  reserve(
    intent: OutboundActionIntent<unknown>,
    signal: AbortSignal,
  ): Promise<unknown>;
  claim(
    identity: string,
    version: number,
    signal: AbortSignal,
  ): Promise<unknown | null>;
  observe(
    identity: string,
    version: number,
    outcome: OutboundActionObservation<unknown>,
    signal: AbortSignal,
  ): Promise<unknown | null>;
  cancel(
    identity: string,
    version: number,
    signal: AbortSignal,
  ): Promise<unknown | null>;
}
export const outboundActionIdentity = (
  scope: PipelineScope,
  name: string,
  id: string,
) =>
  jobValueHash([
    "gregale.outbound-action.v1",
    eventPipelineScope(scope),
    name,
    id,
  ]);
export const actionReference = (
  r: OutboundActionRecord<unknown, unknown>,
): OutboundActionReference => ({
  action_id: r.intent.identity,
  binding_sha256: r.intent.binding_sha256,
});
export function actionPath(path: string): string {
  if (
    typeof path !== "string" ||
    path.length > 2048 ||
    !/^\/[A-Za-z0-9_/?=&.%-]*$/.test(path) ||
    path.includes("//") ||
    path.includes("%") ||
    path
      .split("?")[0]!
      .split("/")
      .some((x) => x === "." || x === "..") ||
    path.split("?").length > 2
  )
    throw new OutboundActionError("invalid_input");
  return path;
}
export function actionRequest(
  raw: OutboundActionRequest,
): OutboundActionRequest {
  if (
    !raw ||
    Object.keys(raw).some(
      (k) => !["path", "content_type", "body", "api_version"].includes(k),
    ) ||
    !["application/json", "application/x-www-form-urlencoded"].includes(
      raw.content_type,
    ) ||
    typeof raw.body !== "string" ||
    Buffer.byteLength(raw.body) > 16384 ||
    (raw.api_version !== undefined &&
      !/^[A-Za-z0-9.-]{1,64}$/.test(raw.api_version))
  )
    throw new OutboundActionError("invalid_input");
  actionPath(raw.path);
  if (raw.path.includes("?")) throw new OutboundActionError("invalid_input");
  return jobSnapshot(raw);
}
export function actionObservation<I, O>(
  raw: OutboundActionObservation<O>,
  business: JobContract<I, O>,
): OutboundActionObservation<O> {
  if (
    !raw ||
    Object.keys(raw).some(
      (k) => !["state", "code", "reference", "result"].includes(k),
    ) ||
    ![
      "prepared",
      "unknown",
      "pending",
      "succeeded",
      "failed",
      "needs_review",
      "cancelled",
    ].includes(raw.state) ||
    !pipelineName(raw.code) ||
    (raw.reference !== undefined &&
      !/^[A-Za-z0-9_.:-]{1,128}$/.test(raw.reference)) ||
    (["pending", "succeeded"].includes(raw.state) &&
      (!raw.reference || !Object.hasOwn(raw, "result"))) ||
    (["prepared", "cancelled", "unknown"].includes(raw.state) &&
      (raw.reference !== undefined || Object.hasOwn(raw, "result")))
  )
    throw new OutboundActionError("conflict");
  const value = {
    ...raw,
    ...(Object.hasOwn(raw, "result")
      ? { result: business.output(raw.result) }
      : {}),
  };
  if (Buffer.byteLength(JSON.stringify(value)) > 32768)
    throw new OutboundActionError("limit");
  return jobSnapshot(value);
}
export function parseOutboundActionRecord<I, O>(
  raw: unknown,
  business: JobContract<I, O>,
): OutboundActionRecord<I, O> {
  try {
    const r = raw as OutboundActionRecord<I, O>,
      i = r.intent,
      { binding_sha256, ...base } = i;
    if (
      Object.keys(r).sort().join() !== "intent,outcome,version" ||
      Object.keys(i).sort().join() !==
        "binding_sha256,config_sha256,identity,input,input_sha256,name,owner_id,request,request_id,revision,scope,version" ||
      i.version !== 1 ||
      !pipelineName(i.name) ||
      !pipelineName(i.revision) ||
      !pipelineName(i.owner_id) ||
      !fileEventUUID(i.request_id) ||
      ![i.identity, i.binding_sha256, i.config_sha256, i.input_sha256].every(
        fileEventHash,
      ) ||
      !Number.isSafeInteger(r.version) ||
      r.version < 0 ||
      r.version > 2147483647 ||
      i.identity !== outboundActionIdentity(i.scope, i.name, i.request_id) ||
      jobValueHash(base) !== binding_sha256 ||
      jobValueHash(business.input(i.input)) !== i.input_sha256
    )
      throw Error();
    actionRequest(i.request);
    eventPipelineScope(i.scope);
    const outcome = actionObservation(r.outcome, business);
    if ((outcome.state === "prepared") !== (r.version === 0)) throw Error();
    return jobSnapshot({ ...r, outcome });
  } catch {
    throw new OutboundActionError("conflict");
  }
}
export function outboundActionContracts<I, O>(
  business: JobContract<I, O>,
  name: string,
) {
  const str = { type: "string" } as const,
    hash = { type: "string", minLength: 64, maxLength: 64 } as const;
  const reference = {
    type: "object",
    properties: { action_id: hash, binding_sha256: hash },
    required: ["action_id", "binding_sha256"],
    additionalProperties: false,
  } as const;
  const outcome: JobSchema = {
    type: "object",
    properties: {
      state: {
        type: "string",
        enum: [
          "prepared",
          "unknown",
          "pending",
          "succeeded",
          "failed",
          "needs_review",
          "cancelled",
        ],
      },
      code: str,
      reference: str,
      result: business.spec.output,
    },
    required: ["state", "code"],
    additionalProperties: false,
  };
  const view: JobSchema = {
    type: "object",
    properties: { ...reference.properties, outcome },
    required: ["action_id", "binding_sha256", "outcome"],
    additionalProperties: false,
  };
  const root = new JobContract<
    OutboundActionReference,
    OutboundActionResult<O>
  >({
    version: 1,
    name: "action-" + name.replaceAll("_", "-"),
    input: reference,
    output: {
      type: "object",
      properties: {
        decision: {
          type: "string",
          enum: ["approved", "rejected", "expired", "cancelled"],
        },
        action: view,
      },
      required: ["decision", "action"],
      additionalProperties: false,
    },
  });
  const review = new JobContract<OutboundActionReview<I>, { reason: string }>({
    version: 1,
    name: "review-" + name.replaceAll("_", "-"),
    input: {
      type: "object",
      properties: {
        ...reference.properties,
        name: str,
        revision: str,
        input: business.spec.input,
      },
      required: ["action_id", "binding_sha256", "name", "revision", "input"],
      additionalProperties: false,
    },
    output: {
      type: "object",
      properties: { reason: { type: "string", maxLength: 1000 } },
      required: ["reason"],
      additionalProperties: false,
    },
  });
  return { root, review, view };
}
export function postgresOutboundActionStore(
  query: PipelineQuery,
): OutboundActionStore {
  async function one(
    sql: string,
    args: readonly unknown[],
    signal: AbortSignal,
  ) {
    signal.throwIfAborted();
    let rows;
    try {
      rows = (await query(sql, [...args], signal)).rows;
    } catch {
      throw new OutboundActionError("store_failed");
    }
    signal.throwIfAborted();
    if (rows.length > 1) throw new OutboundActionError("store_failed");
    return rows[0] ?? null;
  }
  const load = (id: string, s: AbortSignal) => one(SQL.load, [id], s);
  const run = async (id: string, runId: string, s: AbortSignal) =>
    ((await one(SQL.run, [id, runId], s))?.input_sha256 as string | null) ??
    null;
  const attempt = async (ns: string, id: string, s: AbortSignal) =>
    ((await one(SQL.attempt, [ns, id], s))?.action_id as string | null) ?? null;
  return {
    load,
    run,
    attempt,
    async bind(ns, id, identity, s) {
      const found =
        (await one(SQL.bind, [ns, id, identity], s))?.action_id ??
        (await attempt(ns, id, s));
      if (found !== identity) throw new OutboundActionError("conflict");
    },
    async registerRun(id, runId, digest, s) {
      const found =
        (await one(SQL.register_run, [id, runId, digest], s))?.input_sha256 ??
        (await run(id, runId, s));
      if (found !== digest) throw new OutboundActionError("conflict");
    },
    async reserve(i, s) {
      return (
        (await one(SQL.reserve, [i.identity, JSON.stringify(i)], s)) ??
        (await load(i.identity, s))
      );
    },
    claim: (id, v, s) => one(SQL.claim, [id, v], s),
    observe: (id, v, o, s) =>
      one(SQL.observe, [id, v, JSON.stringify(o), o.state], s),
    cancel: (id, v, s) => one(SQL.cancel, [id, v], s),
  };
}
