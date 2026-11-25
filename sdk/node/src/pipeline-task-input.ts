/** Bridge an MCP form to a separately authorized typed external-input gate. */
import { createHmac, timingSafeEqual } from "node:crypto";
import { fileEventHash } from "./file-events.js";
import {
  JobContract,
  jobSnapshot,
  jobValueHash,
  type JobSchema,
} from "./job-runtime.js";
import {
  PipelineGateError,
  type createPipelineGateService,
} from "./pipeline-gates.js";
import type { PipelineGateHandoff } from "./pipeline-task-handoffs.js";
import { QueryToolError, type QueryOptions } from "./query-tools.js";

type Gate = {
  id: string;
  run_id: string;
  kind: "approval" | "input";
  binding_sha256: string;
  can_decide: boolean;
  decision: { state: string } | null;
};
export interface PipelineTaskInputForm<A> {
  readonly scopes: readonly string[];
  readonly request: PipelineTaskFormRequest;
  inspect(
    id: string,
    principal: A,
    options: QueryOptions,
  ): Promise<Gate | null>;
  submitHandoff?(
    id: string, binding: string, value: unknown, principal: A, handoff: PipelineGateHandoff, options: QueryOptions,
  ): Promise<void>;
  submit(
    id: string,
    binding: string,
    value: unknown,
    principal: A,
    options: QueryOptions,
  ): Promise<void>;
}
export interface PipelineTaskFormRequest {
  readonly method: "elicitation/create";
  readonly params: {
    readonly mode: "form";
    readonly message: string;
    readonly requestedSchema: JobSchema;
  };
}

export function createPipelineTaskInputForm<R, D, A>(options: {
  readonly contract: JobContract<R, D>;
  readonly service: Pick<
    ReturnType<typeof createPipelineGateService<R, D, A>>,
    "inspect" | "decide"
  > & Partial<Pick<ReturnType<typeof createPipelineGateService<R, D, A>>, "decideHandoff">>;
  readonly message: string;
  readonly scopes: readonly string[];
}): PipelineTaskInputForm<A> {
  const contract = new JobContract<R, D>(options.contract.spec);
  const { service, message } = options;
  const schema = contract.spec.output;
  if (!Array.isArray(options.scopes)) throw new QueryToolError("invalid_input");
  const scopes = Object.freeze([...options.scopes]);
  if (
    typeof message !== "string" ||
    !message.trim() ||
    message.length > 1024 ||
    scopes.length < 1 ||
    scopes.length > 32 ||
    new Set(scopes).size !== scopes.length ||
    scopes.some(
      (scope) =>
        typeof scope !== "string" ||
        !/^[\x21\x23-\x5b\x5d-\x7e]{1,128}$/.test(scope),
    ) ||
    schema.type !== "object" ||
    !Object.keys(schema.properties ?? {}).length ||
    Object.values(schema.properties ?? {}).some(
      (s) => !["string", "number", "integer", "boolean"].includes(s.type),
    ) ||
    Buffer.byteLength(JSON.stringify(schema)) > 16384
  )
    throw new QueryToolError("invalid_input");
  async function submit(id: string, binding: string, value: unknown, principal: A, options: QueryOptions, handoff?: PipelineGateHandoff) {
    let payload;
    try {
      payload = contract.output(value);
      if (Buffer.byteLength(JSON.stringify(payload)) > 16384)
        throw new Error();
    } catch {
      throw new QueryToolError("invalid_input");
    }
    try {
      const request = { binding_sha256: binding, state: "submitted" as const, payload };
      if (handoff) {
        if (!service.decideHandoff) throw new QueryToolError("not_found");
        await service.decideHandoff(id, request, principal, handoff, options);
      } else {
        await service.decide(id, request, principal, options);
      }
    } catch (error) {
      if (error instanceof PipelineGateError) {
        if (error.code === "forbidden") throw new QueryToolError("not_found");
        if (error.code === "conflict" || error.code === "inactive")
          throw new QueryToolError("invalid_input");
      }
      throw error;
    }
  }
  return Object.freeze<PipelineTaskInputForm<A>>({
    scopes,
    request: jobSnapshot({
      method: "elicitation/create",
      params: { mode: "form", message, requestedSchema: schema },
    }),
    async inspect(id, principal, options) {
      try {
        const gate = await service.inspect(id, principal, options);
        return {
          id: gate.id,
          run_id: gate.run_id,
          kind: gate.kind,
          binding_sha256: gate.binding_sha256,
          can_decide: gate.can_decide,
          decision: gate.decision && { state: gate.decision.state },
        };
      } catch (error) {
        if (error instanceof PipelineGateError && error.code === "forbidden")
          return null;
        throw error;
      }
    },
    submit,
    ...(typeof service.decideHandoff === "function" && {
      submitHandoff: (id: string, binding: string, value: unknown, principal: A, handoff: PipelineGateHandoff, opts: QueryOptions) =>
        submit(id, binding, value, principal, opts, handoff),
    }),
  });
}

type FormBinding = readonly [
  taskId: string,
  receiptIdentity: string,
  runId: string,
  handoffId?: string,
];

export function pipelineTaskFormKey(
  secret: string,
  anchor: FormBinding,
  gate: { id: string; binding_sha256: string },
): string {
  const proof = createHmac("sha256", secret)
    .update(
      jobValueHash([
        "gregale.pipeline-task-form.v1",
        ...anchor,
        gate.id,
        gate.binding_sha256,
      ]),
    )
    .digest()
    .subarray(0, 16);
  return [
    Buffer.from(gate.id, "hex"),
    Buffer.from(gate.binding_sha256, "hex"),
    proof,
  ]
    .map((part) => part.toString("base64url"))
    .join(".");
}

export function parsePipelineTaskFormReply(
  secret: string,
  anchor: FormBinding,
  responses: unknown,
) {
  if (!responses || typeof responses !== "object" || Array.isArray(responses))
    throw new QueryToolError("invalid_input");
  const pairs = Object.entries(responses);
  if (pairs.length !== 1) throw new QueryToolError("invalid_input");
  const [key, value] = pairs[0]!;
  if (
    !/^[A-Za-z0-9_-]{43}\.[A-Za-z0-9_-]{43}\.[A-Za-z0-9_-]{22}$/.test(key) ||
    !value ||
    typeof value !== "object" ||
    Array.isArray(value) ||
    Object.keys(value).sort().join() !== "action,content" ||
    value.action !== "accept"
  )
    throw new QueryToolError("invalid_input");
  const [encodedId, encodedBinding] = key.split(".") as [
    string,
    string,
    string,
  ];
  const id = Buffer.from(encodedId, "base64url").toString("hex");
  const binding_sha256 = Buffer.from(encodedBinding, "base64url").toString(
    "hex",
  );
  if (
    !fileEventHash(id) ||
    !fileEventHash(binding_sha256) ||
    !timingSafeEqual(
      Buffer.from(key),
      Buffer.from(pipelineTaskFormKey(secret, anchor, { id, binding_sha256 })),
    )
  )
    throw new QueryToolError("invalid_input");
  return { id, binding_sha256, content: value.content as unknown };
}
