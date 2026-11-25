/** Compose application-owned MCP adapters under explicit publication and scope rules. */
import { JobContract, jobSnapshot, jobValueHash } from "./job-runtime.js";
import { pipelineTaskRevision, type PipelineContractMetadata, type PipelineTaskManifestEntry, type PipelineTaskRevision } from "./pipeline-task-manifest.js";
import type { PipelineToolMCPDescriptor } from "./pipeline-tool-mcp.js";
import {
  QueryMCPError,
  type QueryMCPResult,
  type QueryToolMCP,
} from "./query-tool-mcp.js";
import { QueryToolError, type QueryOptions } from "./query-tools.js";
import {
  parseInboxFollowUpArguments,
  resolveInboxFollowUp,
  workInboxFollowUpDescriptor,
  type WorkInboxFollowUp,
  type WorkInboxFollowUpOptions,
} from "./work-inbox-follow-up.js";
import type { WorkInboxReference } from "./work-inbox.js";
export type {
  WorkInboxFollowUp,
  WorkInboxFollowUpOptions,
} from "./work-inbox-follow-up.js";

export interface ToolkitAccess<A> {
  readonly principal: A;
  readonly scopes: readonly string[];
}

export interface ToolkitAdapter<A> {
  readonly pipelineContract?: PipelineContractMetadata;
  readonly tools: readonly PipelineToolMCPDescriptor[];
  readonly resourceTemplates?: QueryToolMCP<A>["resourceTemplates"];
  call(
    name: string,
    args: unknown,
    access: A,
    options: QueryOptions,
  ): Promise<QueryMCPResult>;
  readResource?: QueryToolMCP<A>["readResource"];
}

type ScopeRules = Readonly<Record<string, readonly string[]>>;
export interface ToolkitMount<A> {
  readonly adapter: ToolkitAdapter<A>;
  readonly toolScopes?: ScopeRules;
  /** Keys are the resource templates supplied by the query adapter. */
  readonly resourceScopes?: ScopeRules;
}

export class ApplicationToolkitError extends Error {
  constructor(
    readonly code: "tool_not_found" | "resource_not_found" | "limit",
  ) {
    super("Application toolkit: " + code);
    this.name = "ApplicationToolkitError";
  }
}

export interface ApplicationToolkit<A> {
  readonly tools: readonly PipelineToolMCPDescriptor[];
  readonly resourceTemplates: QueryToolMCP<A>["resourceTemplates"];
  readonly toolScopes: ScopeRules;
  readonly resourceScopes: ScopeRules;
  listPipelineContracts?(access: ToolkitAccess<A>): readonly Omit<PipelineTaskManifestEntry, "clarification">[];
  listTools(access: ToolkitAccess<A>): readonly PipelineToolMCPDescriptor[];
  listResourceTemplates(
    access: ToolkitAccess<A>,
  ): QueryToolMCP<A>["resourceTemplates"];
  call(
    name: string,
    args: unknown,
    access: ToolkitAccess<A>,
    options: QueryOptions,
  ): Promise<QueryMCPResult>;
  readResource(
    uri: string,
    access: ToolkitAccess<A>,
    options: QueryOptions,
  ): ReturnType<QueryToolMCP<A>["readResource"]>;
}

export interface ApplicationToolkitWithWorkInbox<A>
  extends ApplicationToolkit<A> {
  resolveWorkInboxFollowUp(
    reference: WorkInboxReference,
    access: ToolkitAccess<A>,
    options: QueryOptions,
  ): Promise<WorkInboxFollowUp>;
}

const templatePattern =
  /^gregale-(?:query|pipeline):\/\/[a-f0-9]{64}\/[a-z][a-z0-9_]{0,47}\/\{id\}$/;
const resourceID = /^[A-Za-z0-9_-]{1,128}$/;

function scopes(value: readonly string[]): readonly string[] {
  if (
    !Array.isArray(value) ||
    value.length < 1 ||
    value.length > 32 ||
    value.some(
      (s) =>
        typeof s !== "string" ||
        s.length > 128 ||
        !/^[\x21\x23-\x5b\x5d-\x7e]+$/.test(s),
    ) ||
    new Set(value).size !== value.length
  ) {
    throw new TypeError(
      "Each toolkit operation requires distinct OAuth scopes",
    );
  }
  return Object.freeze([...value].sort());
}

function allowed<A>(
  access: ToolkitAccess<A>,
  required: readonly string[],
): boolean {
  return (
    access?.principal != null &&
    Array.isArray(access.scopes) &&
    required.every((s) => access.scopes.includes(s))
  );
}

function bounded(result: unknown): void {
  if (Buffer.byteLength(JSON.stringify(result)) > 1048576)
    throw new ApplicationToolkitError("limit");
}

export function createApplicationToolkit<A>(
  mounts: readonly ToolkitMount<A>[],
  options: { readonly workInboxFollowUp: WorkInboxFollowUpOptions<A> },
): ApplicationToolkitWithWorkInbox<A>;
export function createApplicationToolkit<A>(
  mounts: readonly ToolkitMount<A>[],
): ApplicationToolkit<A>;
export function createApplicationToolkit<A>(
  mounts: readonly ToolkitMount<A>[],
  options?: { readonly workInboxFollowUp?: WorkInboxFollowUpOptions<A> },
): ApplicationToolkit<A> | ApplicationToolkitWithWorkInbox<A> {
  type Template = QueryToolMCP<A>["resourceTemplates"][number];
  const tools = new Map<
    string,
    {
      descriptor: PipelineToolMCPDescriptor;
      scopes: readonly string[];
      call(
        args: unknown,
        access: ToolkitAccess<A>,
        options: QueryOptions,
      ): Promise<QueryMCPResult>;
    }
  >();
  const resources = new Map<
    string,
    {
      template: Template;
      scopes: readonly string[];
      read: NonNullable<ToolkitAdapter<A>["readResource"]>;
    }
  >();
  const resourceNames = new Set<string>();
  const pipelines = new Map<string, { contract: JobContract<unknown, unknown>; resources: string[]; revision?: PipelineTaskRevision; preview?: PipelineContractMetadata["preview"] }>();
  const defaults = new Set<string>();
  if (!Array.isArray(mounts) || mounts.length < 1 || mounts.length > 100)
    throw new TypeError("Expected 1–100 toolkit mounts");
  for (const { adapter, toolScopes = {}, resourceScopes = {} } of mounts) {
    const {
      tools: descriptors,
      resourceTemplates = [],
      call,
      readResource,
      pipelineContract,
    } = adapter as ToolkitAdapter<A>;
    for (const [name, rule] of Object.entries(toolScopes as ScopeRules)) {
      const matches = descriptors.filter((d) => d.name === name);
      if (
        matches.length !== 1 ||
        !/^[A-Za-z0-9_.-]{1,128}$/.test(name) ||
        tools.has(name) ||
        typeof call !== "function"
      ) {
        throw new TypeError("Unknown or duplicate toolkit tool: " + name);
      }
      tools.set(name, {
        descriptor: jobSnapshot(matches[0]!),
        scopes: scopes(rule),
        call: (args, access, options) =>
          call.call(adapter, name, args, access.principal, options),
      });
    }
    for (const [pattern, rule] of Object.entries(
      resourceScopes as ScopeRules,
    )) {
      const matches = resourceTemplates.filter(
        (d) => d.uriTemplate === pattern,
      );
      const template = matches[0];
      if (
        matches.length !== 1 ||
        !template ||
        !templatePattern.test(pattern) ||
        resources.has(pattern) ||
        resourceNames.has(template.name) ||
        typeof readResource !== "function"
      ) {
        throw new TypeError(
          "Unknown, duplicate or unsupported toolkit resource: " + pattern,
        );
      }
      resourceNames.add(template.name);
      resources.set(pattern, {
        template: jobSnapshot(template),
        scopes: scopes(rule),
        read: readResource.bind(adapter),
      });
    }
    if (pipelineContract) {
      const tool = pipelineContract.tool;
      if (["start", "status", "result"].every((operation) => Object.hasOwn(toolScopes, tool + "_" + operation))) {
        const contract = new JobContract(pipelineContract.spec);
        const start = tools.get(tool + "_start")!.descriptor.inputSchema.properties as Record<string, unknown> | undefined;
        const result = tools.get(tool + "_result")!.descriptor.outputSchema.properties as Record<string, unknown> | undefined;
        if (jobValueHash(start?.input) !== jobValueHash(contract.spec.input) ||
            jobValueHash(result?.output) !== jobValueHash(contract.spec.output))
          throw new TypeError("Pipeline contract does not match the mounted tool schemas");
        const revision = pipelineContract.revision === undefined
          ? undefined
          : pipelineTaskRevision(pipelineContract.revision);
        if (revision?.starts === "default") {
          if (defaults.has(revision.family))
            throw new TypeError("Duplicate default pipeline revision: " + revision.family);
          defaults.add(revision.family);
        }
        const publishedPreview = tools.get(tool + "_preview")?.descriptor.outputSchema.properties as Record<string, unknown> | undefined;
        const preview = Object.hasOwn(toolScopes, tool + "_preview") ? pipelineContract.preview : undefined;
        if (preview && jobValueHash(publishedPreview?.summary) !== jobValueHash(preview))
          throw new TypeError("Pipeline preview contract does not match the mounted tool schema");
        pipelines.set(tool, { contract, ...(revision && { revision }), ...(preview && { preview: jobSnapshot(preview) }), resources: Object.keys(resourceScopes).filter((uri) => uri.startsWith("gregale-pipeline://")) });
      }
    }
  }
  const followUp = options?.workInboxFollowUp;
  const followUpName = workInboxFollowUpDescriptor.name;
  const inbox = followUp?.inbox;
  if (followUp) {
    if (
      !inbox ||
      typeof inbox.findEntry !== "function" ||
      tools.has(followUpName)
    )
      throw new TypeError("Invalid or duplicate work inbox follow-up");
    tools.set(followUpName, {
      descriptor: workInboxFollowUpDescriptor,
      scopes: scopes(followUp.scopes),
      async call(args, access, options) {
        try {
          const result = await resolveWorkInboxFollowUp(
            parseInboxFollowUpArguments(args),
            access,
            options,
          );
          return {
            isError: false,
            content: [{ type: "text", text: JSON.stringify(result) }],
            structuredContent: result,
          };
        } catch (error) {
          if (error instanceof QueryToolError && error.code === "invalid_input")
            throw new QueryMCPError(-32602);
          const code =
            error instanceof QueryToolError ? error.code : "query_failed";
          return {
            isError: true,
            content: [{ type: "text", text: JSON.stringify({ code }) }],
          };
        }
      },
    });
  }
  function resolveWorkInboxFollowUp(
    reference: WorkInboxReference,
    access: ToolkitAccess<A>,
    options: QueryOptions,
  ) {
    const required = tools.get(followUpName)?.scopes;
    if (!inbox || !required || !allowed(access, required))
      return Promise.reject(new ApplicationToolkitError("tool_not_found"));
    const granted = new Set(access.scopes);
    return resolveInboxFollowUp(
      inbox,
      reference,
      access.principal,
      options,
      (name) => {
        const destination = tools.get(name);
        return destination?.scopes.every((scope) => granted.has(scope))
          ? destination.descriptor
          : undefined;
      },
    );
  }
  if (tools.size + resources.size < 1 || tools.size + resources.size > 256) {
    throw new TypeError("Expected 1–256 published toolkit operations");
  }
  const toolEntries = [...tools.keys()].sort().map((name) => tools.get(name)!);
  const resourceEntries = [...resources.keys()]
    .sort()
    .map((uri) => resources.get(uri)!);
  function resourceFor(uri: unknown) {
    if (typeof uri !== "string") return;
    const separator = uri.lastIndexOf("/");
    if (separator < 0 || !resourceID.test(uri.slice(separator + 1))) return;
    return resources.get(uri.slice(0, separator + 1) + "{id}");
  }
  return Object.freeze<ApplicationToolkitWithWorkInbox<A>>({
    resolveWorkInboxFollowUp,
    tools: Object.freeze(toolEntries.map((entry) => entry.descriptor)),
    resourceTemplates: Object.freeze(
      resourceEntries.map((entry) => entry.template),
    ),
    toolScopes: Object.freeze(
      Object.fromEntries(
        toolEntries.map((entry) => [entry.descriptor.name, entry.scopes]),
      ),
    ),
    resourceScopes: Object.freeze(
      Object.fromEntries(
        resourceEntries.map((entry) => [
          entry.template.uriTemplate,
          entry.scopes,
        ]),
      ),
    ),
    listPipelineContracts(access) {
      return [...pipelines].flatMap(([tool, pipeline]) => {
        if (!["start", "status", "result"].every((operation) => allowed(access, tools.get(tool + "_" + operation)!.scopes)))
          return [];
        const artifacts = pipeline.resources.filter((uri) => allowed(access, resources.get(uri)!.scopes))
          .map((uri) => {
            const { name, mimeType, description } = resources.get(uri)!.template;
            return { name, mimeType, description };
          });
        const preview = pipeline.preview && allowed(access, tools.get(tool + "_preview")!.scopes) ? pipeline.preview : undefined;
        return [{ ...(preview && { preview }), tool, contract: pipeline.contract.spec, contract_sha256: pipeline.contract.sha256, artifacts, ...(pipeline.revision && { revision: pipeline.revision }) }];
      });
    },
    listTools(access) {
      return toolEntries
        .filter((entry) => allowed(access, entry.scopes))
        .map((entry) => entry.descriptor);
    },
    listResourceTemplates(access) {
      return resourceEntries
        .filter((entry) => allowed(access, entry.scopes))
        .map((entry) => entry.template);
    },
    async call(name, args, access, options) {
      const entry = tools.get(name);
      if (!entry || !allowed(access, entry.scopes))
        throw new ApplicationToolkitError("tool_not_found");
      // Capture transport permission once for this call. Each adapter separately
      // checks the principal's current application ownership and permissions.
      const granted = new Set(access.scopes);
      const result = await entry.call(args, access, options);
      const content = result.content.filter((block) => {
        if (block.type !== "resource_link") return true;
        const resource = resourceFor(block.uri);
        return resource && resource.scopes.every((scope) => granted.has(scope));
      });
      const response = { ...result, content };
      bounded(response);
      return jobSnapshot(response);
    },
    async readResource(uri, access, options) {
      const entry = resourceFor(uri);
      if (!entry || !allowed(access, entry.scopes))
        throw new ApplicationToolkitError("resource_not_found");
      const result = await entry.read(uri, access.principal, options);
      bounded(result);
      return jobSnapshot(result);
    },
  });
}
