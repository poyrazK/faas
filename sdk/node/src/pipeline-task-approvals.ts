import { JobContract } from "./job-runtime.js";
import { PipelineGateError, type createPipelineGateService, type PipelineGateView } from "./pipeline-gates.js";
import type { PipelineGateHandoff, PipelineTaskHandoff } from "./pipeline-task-handoffs.js";
import { QueryToolError, type QueryOptions } from "./query-tools.js";

export type PipelineTaskApprovalChoice = "approved" | "rejected";
export interface PipelineTaskApprovalView<R> extends PipelineTaskHandoff {
  readonly title: string;
  readonly review: R;
  readonly contract_sha256: string;
  readonly review_sha256: string;
  readonly decision_key: string;
}
export interface PipelineTaskApprovalReceipt {
  readonly handoff_id: string;
  readonly state: PipelineTaskApprovalChoice;
  readonly decided_at: string;
}

export interface PipelineTaskApproval<A> {
  readonly title: string;
  readonly scopes: readonly string[];
  readonly contractSha256: string;
  inspect(id: string, principal: A, options: QueryOptions): Promise<PipelineGateView<unknown, unknown> | null>;
  decide(id: string, binding: string, state: PipelineTaskApprovalChoice, input: unknown, principal: A,
    handoff: PipelineGateHandoff, options: QueryOptions): Promise<Omit<PipelineTaskApprovalReceipt, "handoff_id">>;
}

export function createPipelineTaskApproval<R, D, A>(settings: {
  readonly contract: JobContract<R, D>;
  readonly service: Pick<ReturnType<typeof createPipelineGateService<R, D, A>>, "inspect" | "decideHandoff">;
  readonly title: string;
  readonly scopes: readonly string[];
}): PipelineTaskApproval<A> {
  const { service, title } = settings;
  const scopes = Object.freeze([...settings.scopes]);
  if (typeof title !== "string" || !title.trim() || title.length > 1024 || !scopes.length || scopes.length > 32 ||
      new Set(scopes).size !== scopes.length || scopes.some((scope) => typeof scope !== "string" || !/^[\x21\x23-\x5b\x5d-\x7e]{1,128}$/.test(scope)))
    throw new QueryToolError("invalid_input");
  const contract = new JobContract<R, D>(settings.contract.spec);
  return Object.freeze<PipelineTaskApproval<A>>({
    title, scopes, contractSha256: contract.sha256,
    async inspect(id, principal, options) {
      try {
        const gate = await service.inspect(id, principal, options);
        return gate.kind === "approval" ? { ...gate, review: contract.input(gate.review) } : null;
      } catch (error) {
        if (error instanceof PipelineGateError && error.code === "forbidden") return null;
        throw error;
      }
    },
    async decide(id, binding, state, input, principal, handoff, options) {
      let payload: D;
      try { payload = contract.output(input); }
      catch { throw new QueryToolError("invalid_input"); }
      try {
        const { decision } = await service.decideHandoff(id, { binding_sha256: binding, state, payload }, principal, handoff, options);
        if (decision?.state !== state) throw new QueryToolError("invalid_output");
        return { state, decided_at: decision.decided_at };
      } catch (error) {
        if (error instanceof PipelineGateError) {
          if (error.code === "forbidden") throw new QueryToolError("not_found");
          if (["conflict", "inactive", "invalid_input"].includes(error.code)) throw new QueryToolError("invalid_input");
        }
        throw error;
      }
    },
  });
}
