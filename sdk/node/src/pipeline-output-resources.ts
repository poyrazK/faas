import type { FileBucket } from "./files.js";
import { fileEventUUID } from "./file-events.js";
import { jobScope, jobSnapshot, jobValueHash } from "./job-runtime.js";
import {
  parsePipelineArtifact,
  readPipelineArtifact,
  type PipelineArtifact,
} from "./pipeline-state.js";
import {
  PipelineToolError,
  type PipelineToolWithResult,
  type PipelineToolResult,
} from "./pipeline-tools.js";
import { QueryMCPError } from "./query-tool-mcp.js";
import {
  type QueryOptions,
  type QueryResource,
  type QueryResourceLink,
} from "./query-tools.js";

export interface PipelineOutputResource<O> {
  readonly name: string;
  readonly description: string;
  readonly mimeType: string;
  readonly maxBytes?: number;
  readonly files: FileBucket;
  readonly select: (output: O) => PipelineArtifact | undefined | null;
}

const uriID = /^[A-Za-z0-9_-]{64}$/;
const outputResourceDomain = "gregale.pipeline-output-resource.v1";

export function createPipelineOutputResources<I, O, A>(
  tool: PipelineToolWithResult<I, O, A>,
  definitions: readonly PipelineOutputResource<O>[],
) {
  if (
    typeof tool.resultWithReceipt !== "function" ||
    !Array.isArray(definitions) ||
    !definitions.length ||
    definitions.length > 32
  )
    throw new PipelineToolError("invalid_input");
  const result = tool.resultWithReceipt.bind(tool);
  const resources = definitions.map((def: PipelineOutputResource<O>) => {
    const {
      name,
      description,
      mimeType,
      files,
      select,
      maxBytes = 262144,
    } = def;
    if (
      typeof name !== "string" ||
      !/^[a-z][a-z0-9_]{0,47}$/.test(name) ||
      typeof description !== "string" ||
      !description.trim() ||
      description.length > 512 ||
      typeof mimeType !== "string" ||
      mimeType.length > 128 ||
      !/^[a-z0-9!#$&^_.+-]+\/[a-z0-9!#$&^_.+-]+$/.test(mimeType) ||
      !Number.isSafeInteger(maxBytes) ||
      maxBytes < 1 ||
      maxBytes > 1048576 ||
      typeof files?.download !== "function" ||
      typeof select !== "function"
    )
      throw new PipelineToolError("invalid_input");
    const prefix = `gregale-pipeline://${jobValueHash([
      outputResourceDomain,
      tool.name,
      tool.contract.sha256,
      files.scope,
    ])}/${name}/`;
    const template: QueryResource<A>["template"] = Object.freeze({
      uriTemplate: prefix + "{id}",
      name,
      description,
      mimeType,
    });
    function locate({ receipt, output }: PipelineToolResult<O>) {
      const raw = select(output);
      if (raw == null) return null;
      const { intent, checkpoint } = receipt;
      if (!checkpoint) throw new PipelineToolError("receipt_conflict");
      const artifact = parsePipelineArtifact(raw, files, intent.owner_id);
      const binding = jobValueHash([
        outputResourceDomain,
        prefix,
        intent.identity,
        checkpoint.run_id,
        artifact,
      ]);
      const id = Buffer.concat([
        Buffer.from(intent.request_id.replaceAll("-", ""), "hex"),
        Buffer.from(binding, "hex"),
      ]).toString("base64url");
      return { uri: prefix + id, artifact };
    }
    return { name, prefix, template, maxBytes, files, locate };
  });
  if (new Set(resources.map(({ name }) => name)).size !== resources.length)
    throw new PipelineToolError("invalid_input");

  return {
    resourceTemplates: Object.freeze(resources.map(({ template }) => template)),
    result,
    links(snapshot: PipelineToolResult<O>): QueryResourceLink[] {
      return resources.flatMap((resource) => {
        const found = resource.locate(snapshot);
        const { name, description, mimeType } = resource.template;
        return found
          ? [
              {
                type: "resource_link" as const,
                uri: found.uri,
                name,
                description,
                mimeType,
              },
            ]
          : [];
      });
    },
    async readResource(uri: string, access: A, options: QueryOptions) {
      const resource = resources.find(
        ({ prefix }) => typeof uri === "string" && uri.startsWith(prefix),
      );
      const id = resource && uri.slice(resource.prefix.length);
      if (!resource || !id || !uriID.test(id)) throw new QueryMCPError(-32002);
      const hex = Buffer.from(id, "base64url").subarray(0, 16).toString("hex");
      const requestId = [
        hex.slice(0, 8),
        hex.slice(8, 12),
        hex.slice(12, 16),
        hex.slice(16, 20),
        hex.slice(20),
      ].join("-");
      if (!fileEventUUID(requestId)) throw new QueryMCPError(-32002);
      const selected = resource;
      try {
        return await jobScope(options.timeoutMs, options.signal, async (signal) => {
          const end = performance.now() + options.timeoutMs;
          const remaining = () => ({
            timeoutMs: Math.max(1, Math.ceil(end - performance.now())),
            signal,
          });
          async function authorizedArtifact() {
            const snapshot = await result(requestId, access, remaining());
            const found = selected.locate(snapshot);
            if (!found || found.uri !== uri) throw new QueryMCPError(-32002);
            return found.artifact;
          }
          const artifact = await authorizedArtifact();
          const bytes = await readPipelineArtifact(
            resource.files,
            artifact,
            artifact.owner_id,
            {
              ...remaining(),
              maxBytes: resource.maxBytes,
            },
          );
          const text = new TextDecoder("utf-8", {
            fatal: true,
            ignoreBOM: true,
          }).decode(bytes);
          await authorizedArtifact();
          const frame = {
            contents: [{ uri, mimeType: resource.template.mimeType, text }],
          };
          if (Buffer.byteLength(JSON.stringify(frame)) > 1048576)
            throw new QueryMCPError(-32603);
          return jobSnapshot(frame);
        });
      } catch (error) {
        if (error instanceof QueryMCPError) throw error;
        const hidden =
          error instanceof PipelineToolError &&
          [
            "unauthorized",
            "receipt_conflict",
            "start_unknown",
            "not_terminal",
          ].includes(error.code);
        throw new QueryMCPError(hidden ? -32002 : -32603);
      }
    },
  };
}
