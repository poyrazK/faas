import { readFile } from "node:fs/promises";

export const mappingSourceKey = (importId) => `pipelines/${importId}/mapping-source.csv`;
export const clarificationFamily = "csv_column_mapping";
export const clarificationTool = "clarify_csv_columns";
export const clarificationScopes = {
  clarify_csv_columns_start: ["imports:recover"],
  clarify_csv_columns_status: ["imports:read"],
  clarify_csv_columns_result: ["imports:read"],
};

export function mappingReportSpec(specs, revised) {
  const report = structuredClone(specs.reportJob);
  if (revised) {
    report.name = specs.revision2.reportJob;
    const source = structuredClone(report.output);
    source.required.push("version_id");
    report.input.properties.pinned_source = source;
    report.input.required.push("pinned_source");
    report.output = {
      type: "object",
      properties: {
        state: { type: "string", enum: ["mapped", "source_unavailable", "source_forbidden"] },
        email: { type: "string", minLength: 1, maxLength: 128 },
        report: report.output,
      },
      required: ["state"],
      additionalProperties: false,
    };
  }
  return report;
}

export async function csvClarification({
  sdk,
  pool,
  client,
  files,
  jobStore,
  scope,
  importId,
  reviewBaseURL,
  version = 1,
  previewSigningKey,
  revisionStarts = "default",
}) {
  const specs = JSON.parse(
    await readFile(
      new URL("clarification-contracts.json", import.meta.url),
      "utf8",
    ),
  );
  const revised = version === 2 ? specs.revision2 : undefined;
  if (revised) {
    specs.pipeline.name = revised.workflow;
    specs.pipeline.input.properties.source_system = revised.sourceSystem;
    specs.pipeline.input.required.push("source_system");
    specs.inputGate.output.properties.email_column.enum = revised.emailColumns;
  }
  const contract = new sdk.JobContract(specs.pipeline);
  const clarification = new sdk.JobContract(specs.inputGate);
  const reportContract = new sdk.JobContract(mappingReportSpec(specs, revised));
  const query = (statement, values) => pool.query(statement, values);
  const gateStore = sdk.postgresPipelineGateStore(query);
  const token = "fixture-owner";
  async function ownsImport(actor, source = importId) {
    const result = await pool.query(
      "SELECT 1 FROM sdk_csv_query_imports WHERE import_id=$1 AND owner_id=$2 AND enabled",
      [source, actor],
    );
    return result.rowCount === 1;
  }
  async function sourceFor(owner) {
    const result = await query(
      "SELECT artifact FROM sdk_csv_mapping_sources WHERE import_id=$1 AND owner_id=$2 AND enabled",
      [importId, owner],
    );
    return result.rows[0]?.artifact;
  }
  const mayReadSource = (owner) => async (source) =>
    source.key === mappingSourceKey(importId) && Boolean(await sourceFor(owner));

  const tool = sdk.definePipelineTool(contract, {
    name: revised?.tool ?? clarificationTool,
    description: "Select the email column for a CSV import.",
    scope,
    token,
    store: sdk.postgresPipelineToolStore(query),
    gateStore,
    reviewBaseURL,
    async authorize(request, actor) {
      let source = request.input?.import_id;
      if (!revised && !source) {
        const identity = sdk.pipelineToolIdentity(scope, clarificationTool, actor, request.request_id);
        const { rows } = await query(
          "SELECT record->'input'->'input'->>'import_id' AS import_id FROM sdk_pipeline_runs "
          + "WHERE record->'input'->'trigger'->>'identity'=$1 AND record->'input'->>'owner_id'=$2 LIMIT 2",
          [identity, actor],
        );
        if (rows.length > 1) return null;
        source = rows[0]?.import_id;
      }
      if ((revised && source && source !== importId) || !(await ownsImport(actor, source)))
        return null;
      return { subject_id: actor, owner_id: actor };
    },
  });
  const gates = sdk.createPipelineGateService({
    scope,
    pipelineSha256: contract.sha256,
    contract: clarification,
    store: gateStore,
    transport: sdk.nativePipelineGateTransport({
      scope,
      token,
      contract,
      timeoutMs: 10000,
    }),
    async authorize(record, _action, actor) {
      const owner = record.intent.owner_id;
      const source = record.intent.review.import_id;
      if ((revised && source !== importId) || !(await ownsImport(owner, source)))
        throw new sdk.PipelineGateError("forbidden");
      if (owner !== actor) {
        const member = await query(
          "SELECT 1 FROM sdk_csv_clarification_reviewers WHERE import_id=$1 AND actor_id=$2 AND enabled",
          [source, actor],
        );
        if (!member.rowCount) throw new sdk.PipelineGateError("forbidden");
      }
      return { actor_id: actor, owner_id: owner, roles: ["clarifier"] };
    },
  });
  const input = sdk.createPipelineTaskInputForm({
    contract: clarification,
    service: gates,
    scopes: ["imports:clarify"],
    message: "Which CSV column contains the email address?",
  });
  const pipeline = sdk.defineJobPipeline(
    contract,
    {
      scope,
      store: sdk.postgresPipelineJobStore(query),
      jobs: {
        report: {
          contract: reportContract,
          job: client.jobs.defineJob(reportContract, { store: jobStore }),
          jobStore,
          retryMax: 0,
        },
      },
      gateStore,
    },
    async (row, wf) => {
      const choice = await wf.waitForInput(
        "email_column",
        clarification,
        { import_id: row.import_id },
        { timeoutMs: 600_000, requiredRoles: ["clarifier"] },
      );
      if (choice.state !== "submitted") return { state: choice.state };
      const column = choice.payload.email_column;
      if (revised && !wf.pinnedSource) throw new sdk.PipelineError("source_unavailable");
      const report = await wf.job("report", [
        {
          id: row.import_id,
          value: {
            import_id: row.import_id,
            run_id: wf.runId,
            owner_id: wf.ownerId,
            email_column: column,
            ...(revised && { pinned_source: wf.pinnedSource }),
          },
        },
      ]);
      const result = report.results[0].result;
      if (revised) {
        if (result.state !== "mapped") throw new sdk.PipelineError(result.state);
        if (!result.email || !result.report) throw new sdk.PipelineError("invalid_result");
        return result;
      }
      return { state: "mapped", email: row[column], report: result };
    },
  );
  const adapter = sdk.createPipelineToolMCP(tool, {
    revision: { family: clarificationFamily, starts: revisionStarts },
    ...(revised && {
      preview: {
        contract: new sdk.JobContract(specs.startPreview),
        signingKey: previewSigningKey,
        async read(_input, actor, options) {
          const pinnedSource = await sourceFor(actor);
          if (!pinnedSource) throw new sdk.PipelineToolError("unauthorized");
          const bytes = await sdk.readPinnedPipelineSource(files, pinnedSource, actor, {
            ...options, maxBytes: 65536, authorize: mayReadSource(actor),
          });
          const sourceVersion = pinnedSource.sha256;
          const headers = Buffer.from(bytes).toString("utf8").split("\n", 1)[0].trim().split(",");
          return { sourceVersion, pinnedSource, summary: {
            source_sha256: sourceVersion,
            email_columns: ["contact", "alternate_contact"].filter((column) => headers.includes(column)),
            action: "write_mapping_report",
            requires_clarification: true,
          } };
        },
      },
    }),
    outputResources: [
      {
        name: revised?.artifact ?? "mapping_report",
        description: "CSV report identifying the chosen email column.",
        mimeType: "text/csv",
        files,
        select: (output) => output.report,
        maxBytes: 65536,
      },
    ],
  });
  async function prepareReplacement(source, actor, options) {
    if (!revised || source.tool !== revised.tool || !await ownsImport(actor) || !await sourceFor(actor))
      return null;
    const { rows } = await pool.query("SELECT record FROM sdk_pipeline_runs WHERE id=$1", [source.run_id]);
    const record = rows[0]?.record, envelope = record?.input;
    if (record?.last_error !== "pipeline:source_unavailable" || record.status !== "failed" ||
        record.workflow_name !== revised.workflow || envelope?.contract_sha256 !== contract.sha256 ||
        envelope.owner_id !== actor || envelope.trigger?.identity !== source.receipt_identity ||
        envelope.input?.import_id !== importId)
      return null;
    try {
      await files.metadata(`pipelines/${importId}/${source.run_id}-mapping.csv`, options);
      return null;
    } catch (error) {
      if (error.status !== 404) throw error;
      return { input: contract.input(envelope.input), previous_source_sha256: envelope.pinned_source?.sha256 };
    }
  }
  return { pipeline, adapter, input, gates, reportContract, contractHash: contract.sha256, prepareReplacement };
}
