/** Additional intent/observation tools; execution stays behind the native approval workflow. */
import { JobError, jobSnapshot } from "./job-runtime.js";
import {
  createPipelineToolMCP,
  PipelineToolMCPError,
  type PipelineToolMCP,
  type PipelineToolMCPDescriptor,
} from "./pipeline-tool-mcp.js";
import {
  OutboundActionError,
  type OutboundAction,
} from "./outbound-actions.js";
import { fileEventHash, fileEventUUID } from "./file-events.js";
export function createOutboundActionMCP<I, O, A>(
  action: OutboundAction<I, O, A>,
  options: { protocolVersion?: "2025-11-25" | "2026-07-28" } = {},
): PipelineToolMCP<A> {
  const inner = createPipelineToolMCP(action.tool, options),
    prefix = action.tool.name;
  const operations = ["prepare", "observe", "refresh", "discard"] as const;
  const extra = operations.map(
    (op): PipelineToolMCPDescriptor => ({
      name: prefix + "_" + op,
      description: {
        prepare:
          "Save an immutable typed action intent. Start its approval workflow using the returned action reference and a separate stable workflow request ID.",
        observe: "Read a currently authorized durable action outcome.",
        refresh:
          "Reconcile through provider GETs. Never submits or retries an external write.",
        discard:
          "Cancel a prepared action before dispatch. Already submitted provider work cannot be recalled.",
      }[op],
      inputSchema:
        op === "prepare"
          ? {
              type: "object",
              properties: {
                request_id: { type: "string", minLength: 36, maxLength: 36 },
                input: action.businessContract.spec.input,
              },
              required: ["request_id", "input"],
              additionalProperties: false,
            }
          : {
              type: "object",
              properties: {
                action_id: { type: "string", minLength: 64, maxLength: 64 },
              },
              required: ["action_id"],
              additionalProperties: false,
            },
      outputSchema: { ...action.contract.spec.output.properties!.action! },
      annotations: {
        readOnlyHint: op === "observe",
        destructiveHint: false,
        idempotentHint: true,
        openWorldHint: true,
      },
    }),
  );
  return Object.freeze<PipelineToolMCP<A>>({
    tools: jobSnapshot([...inner.tools, ...extra]),
    async call(name, args, access, settings) {
      const idx = extra.findIndex((t) => t.name === name);
      if (idx < 0) return inner.call(name, args, access, settings);
      if (
        !args ||
        typeof args !== "object" ||
        Array.isArray(args) ||
        Buffer.byteLength(JSON.stringify(args)) > 1048576
      )
        throw new PipelineToolMCPError(-32602);
      const a = jobSnapshot(args) as Record<string, unknown>,
        op = operations[idx]!;
      if (
        Object.keys(a).sort().join() !==
          (op === "prepare" ? "input,request_id" : "action_id") ||
        !(op === "prepare"
          ? fileEventUUID(a.request_id)
          : fileEventHash(a.action_id))
      )
        throw new PipelineToolMCPError(-32602);
      try {
        const value =
          op === "prepare"
            ? await action.prepare(
                action.businessContract.input(a.input),
                a.request_id as string,
                access,
                settings,
              )
            : op === "observe"
              ? await action.inspect(a.action_id as string, access, settings)
              : op === "refresh"
                ? await action.reconcile(
                    a.action_id as string,
                    access,
                    settings,
                  )
                : await action.cancel(a.action_id as string, access, settings);
        return jobSnapshot({
          ...(options.protocolVersion === "2026-07-28"
            ? { resultType: "complete" as const }
            : {}),
          structuredContent: value as unknown as Record<string, unknown>,
          content: [{ type: "text" as const, text: JSON.stringify(value) }],
          isError: false,
        });
      } catch (e) {
        if (
          (e instanceof OutboundActionError || e instanceof JobError) &&
          e.code === "invalid_input"
        )
          throw new PipelineToolMCPError(-32602);
        return jobSnapshot({
          ...(options.protocolVersion === "2026-07-28"
            ? { resultType: "complete" as const }
            : {}),
          content: [
            {
              type: "text" as const,
              text: JSON.stringify({
                code: settings.signal?.aborted
                  ? "cancelled"
                  : e instanceof OutboundActionError
                    ? e.code
                    : "request_failed",
              }),
            },
          ],
          isError: true,
        });
      }
    },
  });
}
