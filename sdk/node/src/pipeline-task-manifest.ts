/** Portable, scope-filtered pipeline Task metadata. */
import {
  JobContract,
  jobSnapshot,
  jobValueHash,
  type JobContractSpec,
  type JobSchema,
} from "./job-runtime.js";
import { pipelinePreviewContract } from "./pipeline-start-preview.js";
import { pipelineToolName } from "./pipeline-tool-state.js";

export const PIPELINE_TASK_MANIFEST_URI = "gregale-pipeline://manifest/v1";
export const PIPELINE_TASK_MANIFEST_MAX_BYTES = 128 * 1024;
const domain = "gregale.pipeline-task-manifest.v1";

export interface PipelineTaskArtifactDescriptor {
  readonly name: string;
  readonly mimeType: string;
  readonly description: string;
}

export interface PipelineTaskRevision {
  readonly family: string;
  readonly starts: "default" | "retained";
}

export interface PipelineTaskManifestEntry {
  readonly revision?: PipelineTaskRevision;
  readonly preview?: JobSchema;
  readonly tool: string;
  readonly contract: JobContractSpec;
  readonly contract_sha256: string;
  readonly clarification: Readonly<{
    message: string;
    schema: JobSchema;
  }> | null;
  readonly artifacts: readonly PipelineTaskArtifactDescriptor[];
}

export interface PipelineTaskManifest {
  readonly version: 1;
  readonly sha256: string;
  readonly pipelines: readonly PipelineTaskManifestEntry[];
}

/** @internal Metadata supplied by an SDK pipeline MCP adapter. */
export interface PipelineContractMetadata {
  readonly tool: string;
  readonly spec: JobContractSpec;
  readonly revision?: PipelineTaskRevision;
  readonly preview?: JobSchema;
}

export class PipelineTaskManifestError extends Error {
  constructor(
    readonly code: "invalid_manifest" | "unknown_pipeline" | "drift",
  ) {
    super("Pipeline Task manifest: " + code);
    this.name = "PipelineTaskManifestError";
  }
}

function object(value: unknown, keys: string): Record<string, unknown> {
  if (
    !value ||
    typeof value !== "object" ||
    Array.isArray(value) ||
    Object.keys(value).sort().join() !== keys
  )
    throw new PipelineTaskManifestError("invalid_manifest");
  return value as Record<string, unknown>;
}

/** @internal Validate and capture revision configuration. */
export function pipelineTaskRevision(value: unknown): PipelineTaskRevision {
  const { family, starts } = object(value, "family,starts");
  if (!pipelineToolName(family) || (starts !== "default" && starts !== "retained"))
    throw new PipelineTaskManifestError("invalid_manifest");
  return jobSnapshot({ family, starts });
}

function compare(a: string, b: string): number {
  return a < b ? -1 : a > b ? 1 : 0;
}

export function pipelineManifestReplyContract(
  schema: JobSchema,
): JobContractSpec {
  return {
    version: 1,
    name: "pipeline-task-form",
    input: { type: "null" },
    output: schema,
  };
}

function normalizeEntries(value: unknown): PipelineTaskManifestEntry[] {
  if (!Array.isArray(value) || value.length > 32)
    throw new PipelineTaskManifestError("invalid_manifest");
  const names = new Set<string>();
  const defaults = new Set<string>();
  return value
    .map((candidate) => {
      const hasRevision = !!candidate && Object.hasOwn(candidate, "revision");
      const hasPreview = !!candidate && Object.hasOwn(candidate, "preview");
      const {
        tool,
        contract: raw,
        contract_sha256,
        clarification,
        artifacts,
        revision: configured,
        preview: previewSchema,
      } = object(
        candidate,
        "artifacts,clarification,contract,contract_sha256," + (hasPreview ? "preview," : "") + (hasRevision ? "revision," : "") + "tool",
      );
      if (
        !pipelineToolName(tool) ||
        names.has(tool) ||
        !Array.isArray(artifacts) ||
        artifacts.length > 32
      )
        throw new PipelineTaskManifestError("invalid_manifest");
      names.add(tool);
      const revision = hasRevision ? pipelineTaskRevision(configured) : undefined;
      if (revision?.starts === "default") {
        if (defaults.has(revision.family))
          throw new PipelineTaskManifestError("invalid_manifest");
        defaults.add(revision.family);
      }
      const contract = new JobContract(raw as JobContractSpec);
      if (contract.sha256 !== contract_sha256)
        throw new PipelineTaskManifestError("invalid_manifest");
      const preview = hasPreview
        ? new JobContract(pipelinePreviewContract(previewSchema as JobSchema)).spec.output
        : undefined;
      let form: PipelineTaskManifestEntry["clarification"] = null;
      if (clarification !== null) {
        const { message, schema } = object(clarification, "message,schema");
        const reply = new JobContract(
          pipelineManifestReplyContract(schema as JobSchema),
        ).spec.output;
        if (
          typeof message !== "string" ||
          !message.trim() ||
          message.length > 1024 ||
          reply.type !== "object" ||
          !Object.keys(reply.properties ?? {}).length ||
          Object.values(reply.properties ?? {}).some(
            (field) =>
              !["string", "number", "integer", "boolean"].includes(field.type),
          ) ||
          Buffer.byteLength(JSON.stringify(reply)) > 16384
        )
          throw new PipelineTaskManifestError("invalid_manifest");
        form = { message, schema: reply };
      }
      const published = artifacts
        .map((candidate) => {
          const { name, mimeType, description } = object(
            candidate,
            "description,mimeType,name",
          );
          if (
            !pipelineToolName(name) ||
            typeof mimeType !== "string" ||
            mimeType.length > 128 ||
            !/^[a-z0-9!#$&^_.+-]+\/[a-z0-9!#$&^_.+-]+$/.test(mimeType) ||
            typeof description !== "string" ||
            !description.trim() ||
            description.length > 512
          )
            throw new PipelineTaskManifestError("invalid_manifest");
          return { name, mimeType, description };
        })
        .sort((a, b) => compare(a.name, b.name));
      if (new Set(published.map(({ name }) => name)).size !== published.length)
        throw new PipelineTaskManifestError("invalid_manifest");
      return {
        tool,
        contract: contract.spec,
        contract_sha256,
        clarification: form,
        artifacts: published,
        ...(revision && { revision }),
        ...(preview && { preview }),
      };
    })
    .sort((a, b) => compare(a.tool, b.tool));
}

/** @internal Build an immutable manifest after the host resolves current scope policy. */
export function buildPipelineTaskManifest(
  entries: readonly PipelineTaskManifestEntry[],
): PipelineTaskManifest {
  try {
    const pipelines = normalizeEntries(entries);
    const manifest = {
      version: 1 as const,
      sha256: jobValueHash([domain, 1, pipelines]),
      pipelines,
    };
    if (
      Buffer.byteLength(JSON.stringify(manifest)) >
      PIPELINE_TASK_MANIFEST_MAX_BYTES
    )
      throw new PipelineTaskManifestError("invalid_manifest");
    return jobSnapshot(manifest);
  } catch {
    throw new PipelineTaskManifestError("invalid_manifest");
  }
}

export function parsePipelineTaskManifest(
  value: unknown,
): PipelineTaskManifest {
  try {
    if (
      Buffer.byteLength(JSON.stringify(value)) >
      PIPELINE_TASK_MANIFEST_MAX_BYTES
    )
      throw new PipelineTaskManifestError("invalid_manifest");
    const raw = object(value, "pipelines,sha256,version");
    if (raw.version !== 1)
      throw new PipelineTaskManifestError("invalid_manifest");
    const manifest = buildPipelineTaskManifest(
      raw.pipelines as PipelineTaskManifestEntry[],
    );
    if (manifest.sha256 !== raw.sha256)
      throw new PipelineTaskManifestError("invalid_manifest");
    return manifest;
  } catch {
    throw new PipelineTaskManifestError("invalid_manifest");
  }
}
