/** Generate both standalone consumer modules using the authenticated live manifest. */
import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { mkdir, symlink, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { promisify } from "node:util";
import { buildPipelineTaskManifest } from "../../sdk/node/dist/pipeline-task-manifest.js";
import { clarificationFamily } from "./clarification.mjs";

const run = promisify(execFile);
const sdkRoot = fileURLToPath(new URL("../../sdk/node/", import.meta.url));
const timeout = { timeoutMs: 10000 };

export async function preparePipelineTaskBindings({
  sdk,
  node,
  python,
  home,
  env,
  pythonCommand,
  tool,
}) {
  const manifest = await sdk.readPipelineTaskManifest(
    node.taskTransport(),
    timeout,
  );
  assert.deepEqual(
    await sdk.readPipelineTaskManifest(python.taskTransport(), timeout),
    manifest,
  );
  assert.deepEqual(
    manifest.pipelines.map((entry) => entry.tool),
    [tool, "recover_csv_cleanup"],
  );
  const selected = manifest.pipelines.find((entry) => entry.tool === tool);
  assert.ok(selected.clarification.schema.properties.email_column);
  assert.deepEqual(
    selected.artifacts.map(({ name }) => name),
    ["mapping_report"],
  );
  for (const host of [node, python]) {
    assert.deepEqual(
      (
        await sdk.readPipelineTaskManifest(
          host.taskTransport([], ["imports:read"]),
          timeout,
        )
      ).pipelines,
      [],
    );
    const limited = await sdk.readPipelineTaskManifest(
      host.taskTransport([], ["imports:read", "imports:recover"]),
      timeout,
    );
    const entry = limited.pipelines.find((item) => item.tool === tool);
    assert.equal(entry.contract_sha256, selected.contract_sha256);
    assert.equal(entry.clarification, null);
    assert.deepEqual(entry.artifacts, []);
  }

  return generateBindings({
    manifest, selector: "family:" + clarificationFamily, home, env, pythonCommand,
    changed: () => {
      const entries = structuredClone(manifest.pipelines);
      const entry = entries.find((item) => item.tool === tool);
      entry.contract.input.properties.contact.maxLength--;
      entry.contract_sha256 = new sdk.JobContract(entry.contract).sha256;
      return buildPipelineTaskManifest(entries);
    },
  });
}

export async function prepareRevisedTaskBindings({ sdk, host, home, env, pythonCommand }) {
  const manifest = await sdk.readPipelineTaskManifest(host.taskTransport(), timeout);
  const scoped = await sdk.readPipelineTaskManifest(
    host.taskTransport([], ["imports:read", "imports:recover"]), timeout,
  );
  const selection = ({ revision }) => revision?.family === clarificationFamily && revision.starts === "default";
  assert.ok(manifest.pipelines.find(selection).preview);
  assert.equal(scoped.pipelines.find(selection).preview, undefined);
  return generateBindings({
    manifest, selector: "family:" + clarificationFamily, home, env, pythonCommand, revised: true,
  });
}

async function generateBindings({ manifest, selector, home, env, pythonCommand, revised, changed }) {
  const cwd = join(home, revised ? "generated-task-consumer-v2" : "generated-task-consumer");
  const packages = join(cwd, "node_modules", "@gregale");
  await mkdir(packages, { recursive: true });
  await symlink(sdkRoot, join(packages, "sdk-node"));
  const source = join(cwd, "manifest.json");
  const ts = join(cwd, "csv_task.mts");
  const py = join(cwd, "csv_task.py");
  const nodeCLI = [join(sdkRoot, "dist", "pipeline-task-types.js")];
  const pythonCLI = ["-m", "faas_sdk.preview.pipeline_task_types"];
  const options = { cwd, env, timeout: 30000 };
  await writeFile(source, JSON.stringify(manifest));
  const commands = [
    [process.execPath, nodeCLI, "typescript", ts],
    [pythonCommand, pythonCLI, "python", py],
  ];
  for (const [executable, args, language, output] of commands) {
    await run(executable, [...args, source, selector, language, output], options);
    await run(
      executable,
      [...args, source, selector, language, output, "--check"],
      options,
    );
  }
  if (changed) {
    const changedSource = join(cwd, "changed.json");
    await writeFile(changedSource, JSON.stringify(changed()));
    for (const [executable, args, language, output] of commands) {
      await assert.rejects(
        run(
          executable,
          [...args, changedSource, selector, language, output, "--check"],
          options,
        ),
        (error) => error.code === 1 && error.stderr.includes("drift"),
      );
    }
  }
  await run(
    process.execPath,
    [
      join(sdkRoot, "node_modules", "typescript", "bin", "tsc"),
      "--module",
      "nodenext",
      "--target",
      "es2022",
      "--strict",
      "--skipLibCheck",
      ts,
    ],
    options,
  );
  const module = await import(pathToFileURL(join(cwd, "csv_task.mjs")));
  return { createClient: module.createClient, previewContract: module.previewContract, replyContract: module.replyContract, pythonBinding: py, artifactNames: module.artifactNames, manifest };
}
