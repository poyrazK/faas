import { JobContract, jobSnapshot, jobValueHash } from "./job-runtime.js";
import { eventPipelineScope } from "./event-pipeline-state.js";
import {
  pipelineName,
  type PipelineScope,
  type PipelineQuery,
} from "./pipeline-state.js";
import { pipelineCompensationSQL as SQL } from "./pipeline-compensation-protocol.js";
import { fileEventHash, fileEventUUID } from "./file-events.js";
import type { PipelineCheckpoint } from "./job-pipeline-client.js";
export { pipelineCompensationSchema } from "./pipeline-compensation-protocol.js";
export type CompensationAction =
  | "plan"
  | "start"
  | "inspect"
  | "result"
  | "cancel"
  | "reconcile";
export type CompensationCode =
  | "invalid_input"
  | "unauthorized"
  | "source_active"
  | "source_unresolved"
  | "source_effects_unknown"
  | "conflict"
  | "store_failed"
  | "approval_required"
  | "execution_inactive"
  | "commit_unknown"
  | "deadline"
  | "cancelled"
  | "request_failed"
  | "limit";
export class CompensationError extends Error {
  constructor(readonly code: CompensationCode) {
    super("Pipeline compensation: " + code);
    this.name = "CompensationError";
  }
}
export interface CompensationActor {
  readonly actor_id: string;
  readonly owner_id: string;
}
export interface CompensationCommand<I> {
  readonly id: string;
  readonly effect_id: string;
  readonly description: string;
  readonly input: I;
}
export interface CompensationOutcome<O> {
  readonly state: "compensated" | "already_compensated" | "needs_review";
  readonly result: O;
}
export interface CompensationTask {
  readonly plan_id: string;
  readonly command_id: string;
  readonly recovery_run_id: string;
}
export interface CompensationInput {
  readonly plan_id: string;
  readonly binding_sha256: string;
}
export interface CompensationResult<O> {
  readonly state:
    | "completed"
    | "needs_review"
    | "rejected"
    | "expired"
    | "cancelled";
  readonly results: readonly {
    readonly id: string;
    readonly outcome: CompensationOutcome<O>;
  }[];
}
export interface CompensationPlan<I> {
  readonly version: 1;
  readonly identity: string;
  readonly scope: PipelineScope;
  readonly name: string;
  readonly revision: string;
  readonly config_sha256: string;
  readonly owner_id: string;
  readonly source: PipelineCheckpoint;
  readonly source_sha256: string;
  readonly commands: readonly CompensationCommand<I>[];
  readonly binding_sha256: string;
}
export interface CompensationStore {
  load(id: string, signal: AbortSignal): Promise<unknown | null>;
  reserve(
    plan: CompensationPlan<unknown>,
    signal: AbortSignal,
  ): Promise<unknown>;
  attempt(
    namespace: string,
    id: string,
    signal: AbortSignal,
  ): Promise<string | null>;
  bind(
    namespace: string,
    id: string,
    planId: string,
    signal: AbortSignal,
  ): Promise<string>;
  run(
    namespace: string,
    id: string,
    signal: AbortSignal,
  ): Promise<{
    plan_id: string;
    request_id: string;
  } | null>;
  registerRun(
    namespace: string,
    id: string,
    requestId: string,
    planId: string,
    signal: AbortSignal,
  ): Promise<void>;
  effect(
    identity: string,
    signal: AbortSignal,
  ): Promise<{
    command_sha256: string;
    outcome: unknown;
  } | null>;
}
export const compensationIdentity = (
  scope: PipelineScope,
  name: string,
  run: string,
) =>
  jobValueHash([
    "gregale.compensation.v1",
    eventPipelineScope(scope),
    name,
    run,
  ]);
export const compensationEffectIdentity = (
  plan: CompensationPlan<unknown>,
  effect: string,
) =>
  jobValueHash([
    "gregale.compensation-effect.v1",
    plan.scope,
    plan.name,
    plan.source.run_id,
    effect,
  ]);
export function compensationContracts<I, O>(
  business: JobContract<I, O>,
  name: string,
) {
  const str = { type: "string" } as const,
    hash = { type: "string", minLength: 64, maxLength: 64 } as const,
    uuid = { type: "string", minLength: 36, maxLength: 36 } as const;
  const outcome = {
    type: "object",
    properties: {
      state: {
        type: "string",
        enum: ["compensated", "already_compensated", "needs_review"],
      },
      result: business.spec.output,
    },
    required: ["state", "result"],
    additionalProperties: false,
  } as const;
  const input = {
    type: "object",
    properties: { plan_id: hash, binding_sha256: hash },
    required: ["plan_id", "binding_sha256"],
    additionalProperties: false,
  } as const;
  const worker = new JobContract<CompensationTask, CompensationOutcome<O>>({
    version: 1,
    name: business.spec.name,
    input: {
      type: "object",
      properties: { plan_id: hash, command_id: str, recovery_run_id: uuid },
      required: ["plan_id", "command_id", "recovery_run_id"],
      additionalProperties: false,
    },
    output: outcome,
  });
  const root = new JobContract<CompensationInput, CompensationResult<O>>({
    version: 1,
    name: "recover-" + name.replaceAll("_", "-"),
    input,
    output: {
      type: "object",
      properties: {
        state: {
          type: "string",
          enum: [
            "completed",
            "needs_review",
            "rejected",
            "expired",
            "cancelled",
          ],
        },
        results: {
          type: "array",
          maxItems: 20,
          items: {
            type: "object",
            properties: { id: str, outcome },
            required: ["id", "outcome"],
            additionalProperties: false,
          },
        },
      },
      required: ["state", "results"],
      additionalProperties: false,
    },
  });
  const review = new JobContract<
    {
      plan_id: string;
      binding_sha256: string;
      source_run_id: string;
      revision: string;
      commands: readonly {
        id: string;
        description: string;
        input_sha256: string;
      }[];
    },
    {
      reason: string;
    }
  >({
    version: 1,
    name: "review-" + name.replaceAll("_", "-"),
    input: {
      type: "object",
      properties: {
        plan_id: hash,
        binding_sha256: hash,
        source_run_id: uuid,
        revision: str,
        commands: {
          type: "array",
          maxItems: 20,
          items: {
            type: "object",
            properties: { id: str, description: str, input_sha256: hash },
            required: ["id", "description", "input_sha256"],
            additionalProperties: false,
          },
        },
      },
      required: [
        "plan_id",
        "binding_sha256",
        "source_run_id",
        "revision",
        "commands",
      ],
      additionalProperties: false,
    },
    output: {
      type: "object",
      properties: { reason: { type: "string", maxLength: 1000 } },
      required: ["reason"],
      additionalProperties: false,
    },
  });
  return { worker, root, review };
}
export function parseCompensationPlan<I, O>(
  raw: unknown,
  business: JobContract<I, O>,
): CompensationPlan<I> {
  try {
    const p = raw as CompensationPlan<I>,
      { binding_sha256, ...base } = p;
    if (
      Object.keys(p).sort().join() !==
        "binding_sha256,commands,config_sha256,identity,name,owner_id,revision,scope,source,source_sha256,version" ||
      p.version !== 1 ||
      !pipelineName(p.name) ||
      !pipelineName(p.revision) ||
      !pipelineName(p.owner_id) ||
      !fileEventHash(p.config_sha256) ||
      !fileEventHash(p.source_sha256) ||
      !fileEventUUID(p.source.run_id) ||
      p.source.owner_id !== p.owner_id ||
      !fileEventHash(p.source.input_sha256) ||
      !fileEventHash(p.source.contract_sha256) ||
      p.source.version !== 1 ||
      jobValueHash(p.source.scope) !== jobValueHash(p.scope) ||
      jobValueHash(p.scope) !== jobValueHash(eventPipelineScope(p.scope)) ||
      p.identity !== compensationIdentity(p.scope, p.name, p.source.run_id) ||
      binding_sha256 !== jobValueHash(base) ||
      !Array.isArray(p.commands) ||
      p.commands.length > 20 ||
      new Set(p.commands.map((c) => c.id)).size !== p.commands.length ||
      new Set(p.commands.map((c) => c.effect_id)).size !== p.commands.length
    )
      throw Error();
    for (const c of p.commands) {
      if (
        Object.keys(c).sort().join() !== "description,effect_id,id,input" ||
        !pipelineName(c.id) ||
        !pipelineName(c.effect_id) ||
        typeof c.description !== "string" ||
        !c.description.trim() ||
        [...c.description].length > 256
      )
        throw Error();
      business.input(c.input);
    }
    if (Buffer.byteLength(JSON.stringify(p)) > 65536) throw Error();
    return jobSnapshot(p);
  } catch {
    throw new CompensationError("conflict");
  }
}
export function postgresCompensationStore(
  query: PipelineQuery,
): CompensationStore {
  const read = async (sql: string, values: unknown[], signal: AbortSignal) => {
    signal.throwIfAborted();
    const r = await query(sql, values, signal);
    signal.throwIfAborted();
    if (r.rows.length > 1) throw new CompensationError("conflict");
    return r.rows[0];
  };
  const load = async (id: string, signal: AbortSignal) =>
    (await read(SQL.load, [id], signal))?.plan ?? null;
  const attempt = async (ns: string, id: string, signal: AbortSignal) =>
    ((await read(SQL.attempt, [ns, id], signal))?.plan_id as
      | string
      | undefined) ?? null;
  const run = async (ns: string, id: string, signal: AbortSignal) =>
    ((await read(SQL.run, [ns, id], signal)) as
      | {
          plan_id: string;
          request_id: string;
        }
      | undefined) ?? null;
  return Object.freeze({
    load,
    attempt,
    run,
    async registerRun(ns, id, requestId, planId, signal) {
      const row =
        (await read(SQL.register_run, [ns, id, requestId, planId], signal)) ??
        (await run(ns, id, signal));
      if (!row || row.plan_id !== planId || row.request_id !== requestId)
        throw new CompensationError("conflict");
    },
    async effect(id, signal) {
      return (
        ((await read(SQL.effect, [id], signal)) as
          | {
              command_sha256: string;
              outcome: unknown;
            }
          | undefined) ?? null
      );
    },
    async reserve(plan, signal) {
      return (
        (await read(SQL.reserve, [plan.identity, JSON.stringify(plan)], signal))
          ?.plan ?? (await load(plan.identity, signal))
      );
    },
    async bind(ns, id, plan, signal) {
      const found =
        (await read(SQL.bind, [ns, id, plan], signal))?.plan_id ??
        (await attempt(ns, id, signal));
      if (found !== plan) throw new CompensationError("conflict");
      return plan;
    },
  } satisfies CompensationStore);
}
