#!/usr/bin/env node
/** Generate a single typed client module from a saved scoped manifest. */
import { readFile, realpath, writeFile } from "node:fs/promises";
import { pathToFileURL } from "node:url";
import { jobValueHash } from "./job-runtime.js";
import { renderJobTypes } from "./job-types.js";
import {
  parsePipelineTaskManifest,
  pipelineManifestReplyContract,
  PipelineTaskManifestError,
} from "./pipeline-task-manifest.js";

type Language = "typescript" | "python";

import { pipelinePreviewContract } from "./pipeline-start-preview.js";

function sorted<T>(value: T): T {
  return JSON.parse(
    JSON.stringify(value, (_key, item: unknown) =>
      item && typeof item === "object" && !Array.isArray(item)
        ? Object.fromEntries(
            Object.keys(item)
              .sort()
              .map((name) => [name, (item as Record<string, unknown>)[name]]),
          )
        : item,
    ),
  ) as T;
}

export function generatePipelineTaskTypes(
  value: unknown,
  tool: string,
  language: Language,
): string {
  if (language !== "typescript" && language !== "python")
    throw new TypeError("Unsupported language");
  const family = tool.startsWith("family:") ? tool.slice(7) : undefined;
  const entry = parsePipelineTaskManifest(value).pipelines.find((item) =>
    family === undefined
      ? item.tool === tool
      : item.revision?.family === family && item.revision.starts === "default",
  );
  if (!entry) throw new PipelineTaskManifestError("unknown_pipeline");
  const typescript = language === "typescript";
  const [header, body] = renderJobTypes(sorted(entry.contract), language, {
    input: "Input",
    output: "Output",
    contract: "contract",
  });
  const replyName = typescript ? "replyContract" : "reply_contract";
  const form = entry.clarification;
  const reply = form
    ? "\n" +
      renderJobTypes(
        sorted(pipelineManifestReplyContract(form.schema)),
        language,
        {
          input: "ReplyInput",
          output: "Reply",
          contract: replyName,
        },
      )[1]
    : "";
  const preview = entry.preview
    ? "\n" + renderJobTypes(sorted(pipelinePreviewContract(entry.preview)), language, {
        input: "PreviewInput", output: "Preview", contract: typescript ? "previewContract" : "preview_contract",
      })[1]
    : "";
  const fingerprint = jobValueHash(["gregale.pipeline-task-binding.v1", entry]);
  const names = entry.artifacts.map(({ name }) => name);
  if (typescript) {
    return (
      header +
      'import {createPipelineTaskClient, type PipelineTaskTransport} from "@gregale/sdk-node/preview";\n' +
      `\n// Pipeline Task binding SHA-256 ${fingerprint}\n` +
      body +
      reply +
      preview +
      `\nexport const artifactNames = ${JSON.stringify(names)} as const;\n` +
      "export type ArtifactName = (typeof artifactNames)[number];\n" +
      "export function createClient(transport: PipelineTaskTransport) {\n" +
      `  return createPipelineTaskClient<Input, Output, ${form ? "Reply" : "never"}>({ tool: ${JSON.stringify(entry.tool)}, contract, ${form ? "replyContract, " : ""}${entry.revision ? "checkContractOnStart: true, " : ""}transport });\n` +
      "}\n"
    );
  }
  const pyNames = names.map((name) => JSON.stringify(name)).join(", ");
  return (
    header +
    "from typing import Never\nfrom faas_sdk.preview import PipelineTaskClient, PipelineTaskTransport\n" +
    `\n# Pipeline Task binding SHA-256 ${fingerprint}\n` +
    body +
    reply +
    preview +
    `\nArtifactName: TypeAlias = ${pyNames ? `Literal[${pyNames}]` : "Never"}\n` +
    `artifact_names: tuple[ArtifactName, ...] = (${pyNames}${pyNames ? "," : ""})\n\n` +
    `def create_client(transport: PipelineTaskTransport) -> PipelineTaskClient[Input, Output, ${form ? "Reply" : "Never"}]:\n` +
    `    return PipelineTaskClient(contract, tool=${JSON.stringify(entry.tool)}, ${form ? "reply_contract=reply_contract, " : ""}${entry.revision ? "check_contract_on_start=True, " : ""}transport=transport)\n`
  );
}

if (
  process.argv[1] &&
  pathToFileURL(await realpath(process.argv[1]).catch(() => "")).href ===
    import.meta.url
) {
  try {
    const [source, tool, language, output, flag, ...extra] =
      process.argv.slice(2);
    if (
      !source ||
      !tool ||
      !output ||
      (language !== "typescript" && language !== "python") ||
      (flag !== undefined && flag !== "--check") ||
      extra.length
    )
      throw new TypeError("Invalid command arguments");
    const generated = generatePipelineTaskTypes(
      JSON.parse(await readFile(source, "utf8")),
      tool,
      language,
    );
    if (flag === "--check") {
      if ((await readFile(output, "utf8").catch(() => undefined)) !== generated)
        throw new PipelineTaskManifestError("drift");
    } else await writeFile(output, generated);
  } catch (error) {
    const detail =
      error instanceof PipelineTaskManifestError ? " " + error.message : "";
    console.error(
      "Use gregale-pipeline-task-types MANIFEST TOOL|family:NAME typescript|python OUTPUT [--check]." +
        detail,
    );
    process.exitCode = 1;
  }
}
