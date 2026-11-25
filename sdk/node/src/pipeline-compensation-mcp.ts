/** A planning tool plus the existing durable start/status/result/cancel/reconcile tools. */
import { jobSnapshot } from "./job-runtime.js";
import {
  createPipelineToolMCP,
  PipelineToolMCPError,
  type PipelineToolMCP,
  type PipelineToolMCPDescriptor,
} from "./pipeline-tool-mcp.js";
import { pipelineToolRequestId } from "./pipeline-tool-state.js";
import {
  CompensationError,
  type PipelineCompensation,
} from "./pipeline-compensations.js";
export function createCompensationMCP<I, O, A>(
  recovery: PipelineCompensation<I, O, A>,
  options: {
    protocolVersion?: "2025-11-25" | "2026-07-28";
  } = {},
): PipelineToolMCP<A> {
  const inner = createPipelineToolMCP(recovery.tool, options),
    name = recovery.tool.name + "_plan";
  const descriptor: PipelineToolMCPDescriptor = {
    name,
    description:
      "Prepare an immutable compensation plan from a failed pipeline. Does not apply business changes. Start a recovery attempt with the returned input and a stable request_id; a human must approve the exact plan before effects.",
    inputSchema: {
      type: "object",
      properties: {
        source_run_id: { type: "string", minLength: 36, maxLength: 36 },
      },
      required: ["source_run_id"],
      additionalProperties: false,
    },
    outputSchema: {
      type: "object",
      properties: {
        state: { type: "string", enum: ["ready"] },
        input: recovery.contract.spec.input,
        source_run_id: { type: "string" },
        commands: recovery.reviewContract.spec.input.properties!.commands,
      },
      required: ["state", "input", "source_run_id", "commands"],
      additionalProperties: false,
    },
    annotations: {
      readOnlyHint: false,
      destructiveHint: false,
      idempotentHint: true,
      openWorldHint: true,
    },
  };
  return Object.freeze({
    ...(inner.pipelineContract && { pipelineContract: inner.pipelineContract }),
    tools: jobSnapshot([...inner.tools, descriptor]),
    async call(tool, args, access, settings) {
      if (tool !== name) return inner.call(tool, args, access, settings);
      if (
        !args ||
        typeof args !== "object" ||
        Array.isArray(args) ||
        Object.keys(args).join() !== "source_run_id"
      )
        throw new PipelineToolMCPError(-32602);
      let id: string;
      try {
        id = pipelineToolRequestId(
          (
            args as {
              source_run_id: unknown;
            }
          ).source_run_id,
        );
      } catch {
        throw new PipelineToolMCPError(-32602);
      }
      try {
        const p = await recovery.plan(id, access, settings),
          value = {
            state: "ready",
            input: { plan_id: p.identity, binding_sha256: p.binding_sha256 },
            source_run_id: p.source.run_id,
            commands: p.commands.map((c) => ({
              id: c.id,
              description: c.description,
              input_sha256: importHash(c.input),
            })),
          };
        return jobSnapshot({
          ...(options.protocolVersion === "2026-07-28"
            ? { resultType: "complete" as const }
            : {}),
          structuredContent: value,
          content: [{ type: "text" as const, text: JSON.stringify(value) }],
          isError: false,
        });
      } catch (e) {
        const code = settings.signal?.aborted
          ? "cancelled"
          : e instanceof CompensationError
            ? e.code
            : "request_failed";
        return jobSnapshot({
          ...(options.protocolVersion === "2026-07-28"
            ? { resultType: "complete" as const }
            : {}),
          content: [{ type: "text" as const, text: JSON.stringify({ code }) }],
          isError: true,
        });
      }
    },
  } satisfies PipelineToolMCP<A>);
}
import { jobValueHash as importHash } from "./job-runtime.js";
