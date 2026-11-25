/** Plan an explicitly invoked follow-up from a freshly authorized inbox entry. */
import { fileEventHash, fileEventUUID } from "./file-events.js";
import {
  JobContract,
  JobError,
  jobSnapshot,
  jobValueHash,
} from "./job-runtime.js";
import { parseWorkInboxReview } from "./live-work-inbox.js";
import type { PipelineToolMCPDescriptor } from "./pipeline-tool-mcp.js";
import { pipelineToolWire } from "./pipeline-tool-protocol.js";
import { QueryToolError, type QueryOptions } from "./query-tools.js";
import { workInboxFollowUpSpec } from "./work-inbox-protocol.js";
import type {
  WorkInbox,
  WorkInboxEntry,
  WorkInboxReference,
  WorkInboxState,
} from "./work-inbox.js";

export type WorkInboxFollowUp =
  | Readonly<{
      kind: "human_review";
      state: "awaiting_approval" | "awaiting_input";
      review: NonNullable<WorkInboxEntry["review"]>;
    }>
  | Readonly<{
      kind: "tool_call";
      state: WorkInboxState;
      name: string;
      arguments: Readonly<
        { request_id: string; run_id?: string } | { action_id: string }
      >;
    }>
  | Readonly<{ kind: "no_action"; state: WorkInboxState }>;

export interface WorkInboxFollowUpOptions<A> {
  readonly inbox: Pick<WorkInbox<A>, "findEntry"> &
    Partial<Pick<WorkInbox<A>, "findAcceptedRun">>;
  /** Transport scopes required to ask for a follow-up. Destination scopes also apply. */
  readonly scopes: readonly string[];
}

export const workInboxFollowUpDescriptor: PipelineToolMCPDescriptor =
  jobSnapshot({
    name: "work_inbox_follow_up",
    description:
      "Resolve a current authorized follow-up from an inbox reference. Returns a human review link, a tool call to invoke explicitly, or no action.",
    inputSchema: workInboxFollowUpSpec.input,
    outputSchema: workInboxFollowUpSpec.output,
    annotations: {
      readOnlyHint: true,
      destructiveHint: false,
      idempotentHint: true,
      openWorldHint: false,
    },
  });

const contract = new JobContract<WorkInboxReference, WorkInboxFollowUp>(
  workInboxFollowUpSpec,
);
export function parseInboxFollowUpArguments(
  value: unknown,
): WorkInboxReference {
  try {
    return contract.input(value);
  } catch (error) {
    if (error instanceof JobError) throw new QueryToolError("invalid_input");
    throw error;
  }
}
const requestSchema = jobValueHash(pipelineToolWire.request_id_schema);
const actionSchema = jobValueHash({
  type: "string",
  minLength: 64,
  maxLength: 64,
});

export async function resolveInboxFollowUp<A>(
  inbox: WorkInboxFollowUpOptions<A>["inbox"],
  reference: WorkInboxReference,
  principal: A,
  options: QueryOptions,
  visibleTool: (name: string) => PipelineToolMCPDescriptor | undefined,
): Promise<WorkInboxFollowUp> {
  const end = performance.now() + options.timeoutMs;
  let item: WorkInboxEntry | null;
  try {
    item = await inbox.findEntry(reference, principal, options);
  } catch (error) {
    if (error instanceof QueryToolError && error.code === "unauthorized")
      throw new QueryToolError("not_found");
    throw error;
  }
  if (!item) throw new QueryToolError("not_found");
  const { kind, state, name: adapter } = item;
  type ArgumentKey = "request_id" | "run_id" | "action_id";
  function published(suffix: string, keys: readonly ArgumentKey[]) {
    const name = adapter + "_" + suffix;
    const input = visibleTool(name)?.inputSchema;
    const required = input?.required;
    const properties = input?.properties as Record<string, unknown> | undefined;
    if (
      input?.type !== "object" ||
      input.additionalProperties !== false ||
      !Array.isArray(required) ||
      required.length !== keys.length ||
      !properties ||
      Object.keys(properties).length !== keys.length ||
      keys.some(
        (key) =>
          !required.includes(key) ||
          !Object.hasOwn(properties, key) ||
          jobValueHash(properties[key]) !==
            (key === "action_id" ? actionSchema : requestSchema),
      )
    )
      return undefined;
    return name;
  }
  function destination(suffix: string, key: ArgumentKey) {
    const name = published(suffix, [key]);
    if (!name) throw new QueryToolError("not_found");
    return name;
  }
  if (kind === "pipeline") {
    if (state === "start_unknown") {
      const name = published("reconcile", ["request_id", "run_id"]);
      if (name && inbox.findAcceptedRun) {
        const left = end - performance.now();
        if (left <= 0) throw new QueryToolError("deadline");
        let run_id;
        try {
          run_id = await inbox.findAcceptedRun(reference, principal, {
            ...options,
            timeoutMs: Math.ceil(left),
          });
        } catch (error) {
          if (error instanceof QueryToolError && error.code === "unauthorized")
            throw new QueryToolError("not_found");
          throw error;
        }
        if (run_id !== null) {
          if (!fileEventUUID(run_id))
            throw new QueryToolError("invalid_output");
          return jobSnapshot({
            kind: "tool_call",
            state,
            name,
            arguments: { request_id: item.request_id, run_id },
          });
        }
      }
    }
    if (state === "awaiting_approval" || state === "awaiting_input") {
      destination("status", "request_id");
      try {
        return jobSnapshot({
          kind: "human_review",
          state,
          review: parseWorkInboxReview(item.review, state, item.run_id),
        });
      } catch {
        throw new QueryToolError("invalid_output");
      }
    }
    if (state === "running" || state === "succeeded") {
      return jobSnapshot({
        kind: "tool_call",
        state,
        name: destination(
          state === "running" ? "status" : "result",
          "request_id",
        ),
        arguments: { request_id: item.request_id },
      });
    }
  } else if (
    state === "unknown" ||
    state === "pending" ||
    state === "prepared" ||
    state === "succeeded"
  ) {
    if (!fileEventHash(item.action_id))
      throw new QueryToolError("invalid_output");
    return jobSnapshot({
      kind: "tool_call",
      state,
      name: destination(
        state === "unknown" || state === "pending" ? "refresh" : "observe",
        "action_id",
      ),
      arguments: { action_id: item.action_id },
    });
  }
  return jobSnapshot({ kind: "no_action", state });
}
