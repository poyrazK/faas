/** Registration helpers for an existing authenticated MCP host. No transport or credential proxy. */
import { jobSnapshot } from "./job-runtime.js";
import {
  QueryToolError,
  queryScope,
  type QueryTool,
  type QueryResource,
  type QueryOptions,
  type QueryResourceLink,
} from "./query-tools.js";
import type { PipelineToolMCPDescriptor } from "./pipeline-tool-mcp.js";

export class QueryMCPError extends Error {
  constructor(readonly code: -32601 | -32602 | -32603 | -32002) {
    super("SDK query MCP error: " + code);
    this.name = "QueryMCPError";
  }
}
export interface QueryMCPResult {
  readonly resultType?: "complete";
  readonly isError: boolean;
  readonly structuredContent?: Readonly<Record<string, unknown>>;
  readonly content: readonly (
    | Readonly<{ type: "text"; text: string }>
    | QueryResourceLink
  )[];
}
export interface QueryToolMCP<A> {
  readonly tools: readonly PipelineToolMCPDescriptor[];
  readonly resourceTemplates: readonly QueryResource<A>["template"][];
  call(
    name: string,
    args: unknown,
    access: A,
    options: QueryOptions,
  ): Promise<QueryMCPResult>;
  readResource(
    uri: string,
    access: A,
    options: QueryOptions,
  ): Promise<
    Readonly<{
      contents: readonly { uri: string; mimeType: string; text: string }[];
    }>
  >;
}
export function createQueryToolMCP<I, O, A>(
  tool: QueryTool<I, O, A>,
  options: {
    readonly protocolVersion?: "2025-11-25" | "2026-07-28";
    readonly resources?: readonly QueryResource<A>[];
    readonly links?: (
      item: O,
    ) => readonly { readonly resource: string; readonly id: string }[];
  } = {},
): QueryToolMCP<A> {
  const protocol = options.protocolVersion ?? "2025-11-25",
    resources = [...(options.resources ?? [])],
    links = options.links;
  if (
    !["2025-11-25", "2026-07-28"].includes(protocol) ||
    new Set(resources.map((r) => r.name)).size !== resources.length ||
    resources.length > 100
  )
    throw new QueryToolError("invalid_input");
  const outputSchema = {
    type: "object",
    properties: {
      items: {
        type: "array",
        items: tool.contract.spec.output,
        maxItems: tool.maxPageSize,
      },
      next_cursor: { type: ["string", "null"], maxLength: 8192 },
    },
    required: ["items", "next_cursor"],
    additionalProperties: false,
  };
  const tools = jobSnapshot([
    {
      name: tool.name,
      description: tool.description,
      inputSchema: {
        type: "object",
        properties: {
          input: tool.contract.spec.input,
          after: { type: "string", minLength: 1, maxLength: 8192 },
          page_size: { type: "integer", minimum: 1, maximum: tool.maxPageSize },
        },
        required: ["input"],
        additionalProperties: false,
      },
      outputSchema,
      annotations: {
        readOnlyHint: true,
        destructiveHint: false,
        idempotentHint: true,
        openWorldHint: true,
      },
    },
  ]);
  function frame<T>(value: T): T {
    if (Buffer.byteLength(JSON.stringify(value)) > 1048576)
      throw new QueryToolError("limit");
    return jobSnapshot(value);
  }
  return Object.freeze({
    tools,
    resourceTemplates: jobSnapshot(resources.map((r) => r.template)),
    async call(name, args, access, settings) {
      if (name !== tool.name) throw new QueryMCPError(-32601);
      let a: Record<string, unknown>;
      try {
        if (
          !args ||
          typeof args !== "object" ||
          Array.isArray(args) ||
          Buffer.byteLength(JSON.stringify(args)) > 16384
        )
          throw Error();
        a = jobSnapshot(args) as Record<string, unknown>;
        if (
          !Object.hasOwn(a, "input") ||
          Object.keys(a).some(
            (k) => !["input", "after", "page_size"].includes(k),
          ) ||
          (Object.hasOwn(a, "after") && typeof a.after !== "string") ||
          (Object.hasOwn(a, "page_size") &&
            (!Number.isSafeInteger(a.page_size) ||
              Number(a.page_size) < 1 ||
              Number(a.page_size) > tool.maxPageSize))
        )
          throw Error();
        tool.contract.input(a.input);
      } catch {
        throw new QueryMCPError(-32602);
      }
      try {
        return await queryScope(settings, async (signal) => {
          const end = performance.now() + settings.timeoutMs,
            remaining = () => ({
              timeoutMs: Math.max(1, Math.ceil(end - performance.now())),
              signal,
            });
          const result = await tool.page(a.input as I, access, {
              ...remaining(),
              ...(a.after !== undefined ? { after: a.after as string } : {}),
              ...(a.page_size !== undefined
                ? { pageSize: a.page_size as number }
                : {}),
            }),
            found = new Map<string, QueryResourceLink>();
          if (links)
            for (const item of result.items) {
              const refs = links(item);
              if (!Array.isArray(refs) || refs.length > 100)
                throw new QueryToolError("invalid_output");
              for (const ref of refs) {
                const resource = resources.find(
                  (r) => r.name === ref?.resource,
                );
                if (!resource) throw new QueryToolError("invalid_output");
                const link = await resource.link(ref.id, access, remaining());
                found.set(link.uri, link);
                if (found.size > 100) throw new QueryToolError("limit");
              }
            }
          return frame({
            ...(protocol === "2026-07-28"
              ? { resultType: "complete" as const }
              : {}),
            structuredContent: result as unknown as Record<string, unknown>,
            content: [
              { type: "text" as const, text: JSON.stringify(result) },
              ...found.values(),
            ],
            isError: false,
          });
        });
      } catch (e) {
        if (
          e instanceof QueryToolError &&
          ["invalid_input", "invalid_cursor"].includes(e.code)
        )
          throw new QueryMCPError(-32602);
        return frame({
          ...(protocol === "2026-07-28"
            ? { resultType: "complete" as const }
            : {}),
          content: [
            {
              type: "text" as const,
              text: JSON.stringify({
                code: e instanceof QueryToolError ? e.code : "query_failed",
              }),
            },
          ],
          isError: true,
        });
      }
    },
    async readResource(uri, access, settings) {
      const resource = resources.find(
        (r) =>
          typeof uri === "string" &&
          uri.startsWith(r.template.uriTemplate.slice(0, -4)),
      );
      if (!resource) throw new QueryMCPError(-32002);
      try {
        return frame({
          contents: [await resource.read(uri, access, settings)],
        });
      } catch (e) {
        throw new QueryMCPError(
          e instanceof QueryToolError &&
          ["not_found", "unauthorized"].includes(e.code)
            ? -32002
            : -32603,
        );
      }
    },
  } satisfies QueryToolMCP<A>);
}
