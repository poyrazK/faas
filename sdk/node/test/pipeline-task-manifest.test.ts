import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { JobContract } from "../src/job-runtime.js";
import { readPipelineTaskManifest } from "../src/pipeline-task-client.js";
import {
  buildPipelineTaskManifest,
  parsePipelineTaskManifest,
  PIPELINE_TASK_MANIFEST_URI,
} from "../src/pipeline-task-manifest.js";
import { generatePipelineTaskTypes } from "../src/pipeline-task-types.js";

const f = JSON.parse(
  readFileSync(
    new URL(
      "../../../request-contracts/pipeline-task-manifest.json",
      import.meta.url,
    ),
    "utf8",
  ),
);

test("the published manifest validates both its binding and contract digests", async () => {
  const transport = {
    async request(method: string, params: unknown) {
      assert.equal(method, "resources/read");
      assert.deepEqual(params, { uri: PIPELINE_TASK_MANIFEST_URI });
      return {
        contents: [
          {
            uri: PIPELINE_TASK_MANIFEST_URI,
            mimeType: "application/json",
            text: JSON.stringify(f),
          },
        ],
      };
    },
  };
  assert.deepEqual(
    await readPipelineTaskManifest(transport, { timeoutMs: 1000 }),
    f,
  );
  assert.deepEqual(buildPipelineTaskManifest(f.pipelines), f);
  assert.throws(
    () => parsePipelineTaskManifest({ ...f, sha256: "0".repeat(64) }),
    { code: "invalid_manifest" },
  );
  const changed = structuredClone(f);
  changed.pipelines[0].contract.output.properties.rows.type = "string";
  assert.throws(() => buildPipelineTaskManifest(changed.pipelines), {
    code: "invalid_manifest",
  });
});

test("generation is deterministic and changes with the selected pipeline contract", () => {
  const ts = generatePipelineTaskTypes(f, "csv_import", "typescript");
  assert.match(ts, /readonly "rows": number/);
  assert.match(ts, /artifactNames = \["report"\] as const/);
  assert.match(ts, /createPipelineTaskClient<Input, Output, Reply>/);
  const py = generatePipelineTaskTypes(f, "csv_import", "python");
  assert.match(py, /"email_column": Literal\["contact", "alternate_contact"\]/);
  assert.match(py, /PipelineTaskClient\[Input, Output, Reply\]/);
  const reordered = JSON.parse(
    JSON.stringify(f, (_key, item) =>
      item && typeof item === "object" && !Array.isArray(item)
        ? Object.fromEntries(Object.entries(item).reverse())
        : item,
    ),
  );
  assert.equal(
    generatePipelineTaskTypes(reordered, "csv_import", "typescript"),
    ts,
  );
  assert.equal(
    generatePipelineTaskTypes(reordered, "csv_import", "python"),
    py,
  );
  const changed = structuredClone(f.pipelines);
  changed[0].contract.output.properties.rows.type = "string";
  changed[0].contract_sha256 = new JobContract(changed[0].contract).sha256;
  const current = buildPipelineTaskManifest(changed);
  assert.match(
    generatePipelineTaskTypes(current, "csv_import", "typescript"),
    /readonly "rows": string/,
  );
  assert.throws(
    () => generatePipelineTaskTypes(f, "hidden_pipeline", "typescript"),
    { code: "unknown_pipeline" },
  );
});

test("bindings reflect a caller without clarification or artifact scopes", () => {
  const entries = f.pipelines.map((entry: Record<string, unknown>) => ({
    ...entry,
    clarification: null,
    artifacts: [],
  }));
  const restricted = buildPipelineTaskManifest(entries);
  const ts = generatePipelineTaskTypes(restricted, "csv_import", "typescript");
  assert.match(ts, /createPipelineTaskClient<Input, Output, never>/);
  assert.match(ts, /artifactNames = \[\] as const/);
  assert.notEqual(restricted.sha256, f.sha256);
});

test("versioned bindings use the visible default or explicitly selected revision", () => {
  const releases = JSON.parse(readFileSync(
    new URL("../../../request-contracts/pipeline-task-revisions.json", import.meta.url),
    "utf8",
  ));
  const manifest = parsePipelineTaskManifest(releases);
  const [retained, latest] = manifest.pipelines;
  assert.equal(latest?.revision?.starts, "default");
  const selector = "family:csv_imports";
  for (const language of ["typescript", "python"] as const) {
    const generated = generatePipelineTaskTypes(manifest, selector, language);
    assert.equal(generated, generatePipelineTaskTypes(manifest, latest!.tool, language));
    assert.match(generated, /csv_import_v2/);
    assert.match(generated, /checkContractOnStart: true|check_contract_on_start=True/);
    assert.notEqual(generatePipelineTaskTypes(manifest, retained!.tool, language), generated);
  }
  const scoped = buildPipelineTaskManifest([retained!]);
  assert.throws(() => generatePipelineTaskTypes(scoped, selector, "typescript"), { code: "unknown_pipeline" });
  assert.throws(() => buildPipelineTaskManifest([
    { ...retained!, revision: { family: "csv_imports", starts: "default" } },
    latest!,
  ]), { code: "invalid_manifest" });
});
