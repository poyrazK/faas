import { readFile } from "node:fs/promises";

/** All credentials and membership lookups stay in the application backend. */
export async function csvCompensation({
  sdk,
  runtime,
  client,
  scope,
  pool,
  jobStore,
  reviewBaseURL,
  reviewed = process.env.GREGALE_REVIEWED_PIPELINE === "1",
}) {
  const business = new sdk.JobContract(
    JSON.parse(
      await readFile(
        new URL("compensation-contract.json", import.meta.url),
        "utf8",
      ),
    ),
  );
  const worker = sdk.compensationContracts(business, "csv_cleanup").worker;
  const original = new sdk.JobContract(
      JSON.parse(
        await readFile(
          new URL(
            reviewed
              ? "reviewed-pipeline-contract.json"
              : "pipeline-contract.json",
            import.meta.url,
          ),
          "utf8",
        ),
      ),
    ),
    stages = {};
  for (const [step, file] of [
    ["validate", "validate-contract.json"],
    ["import", "contract.json"],
    ["report", "report-contract.json"],
  ]) {
    const contract = new sdk.JobContract(
      JSON.parse(await readFile(new URL(file, import.meta.url), "utf8")),
    );
    stages[step] = {
      readOnly: step !== "import",
      definition: {
        contract,
        job: client.jobs.defineJob(contract, { store: jobStore }),
        jobStore,
      },
    };
  }
  const lookup = async (id) =>
    (
      await pool.query(
        "SELECT * FROM sdk_csv_recovery_owners WHERE source_run_id=$1",
        [id],
      )
    ).rows[0];
  const query = (sql, values) => pool.query(sql, values);
  const recovery = sdk.definePipelineCompensation(business, {
    name: "csv_cleanup",
    revision: "v1",
    scope,
    sourceContract: original,
    sourceStages: stages,
    sourceStore: sdk.postgresPipelineJobStore(query),
    job: {
      contract: worker,
      job: client.jobs.defineJob(worker, { store: jobStore }),
      jobStore,
      retryMax: 2,
    },
    jobStore: sdk.postgresPipelineJobStore(query),
    store: sdk.postgresCompensationStore(query),
    toolStore: sdk.postgresPipelineToolStore(query),
    gateStore: sdk.postgresPipelineGateStore(query),
    token: process.env.FAAS_TOKEN ?? "fixture-owner",
    reviewBaseURL,
    requiredRoles: ["reviewer"],
    pollDelayMs: 50,
    authorize: async (sourceId, actor) => {
      const row = await lookup(sourceId);
      return row?.enabled && row.owner_id === actor
        ? { actor_id: actor, owner_id: row.owner_id }
        : null;
    },
    authorizeExecution: async (plan) => {
      const row = await lookup(plan.source.run_id);
      return row?.enabled && row.owner_id === plan.owner_id;
    },
    plan: async (source) => {
      const owner = await lookup(source.checkpoint.run_id),
        validated = source.stages.find((s) => s.step === "validate")?.results[0]
          ?.result.rows;
      const inputs = new Map(validated.map((item) => [item.id, item.value]));
      return source.stages
        .find((s) => s.step === "import")
        .results.filter((item) => item.result.saved)
        .slice()
        .reverse()
        .map((item) => {
          if (!item.input_id.startsWith(owner.import_id + ":"))
            throw Error("source_import_mismatch");
          const input = inputs.get(item.input_id);
          if (!input) throw Error("missing_verified_input");
          return {
            id: "delete_" + item.index,
            effect_id: runtime.jobValueHash(item.input_id),
            description: "Remove unchanged row " + input.row,
            input: {
              input_id: item.input_id,
              expected_sha256: runtime.jobValueHash([input.row, input.email]),
            },
          };
        });
    },
  });
  const handler = recovery.handler({
    pool,
    authorize: async (tx, plan, command) => {
      const {
        rows: [row],
      } = await tx.query(
        "SELECT * FROM sdk_csv_recovery_owners WHERE source_run_id=$1 FOR SHARE",
        [plan.source.run_id],
      );
      return (
        row?.enabled &&
        row.owner_id === plan.owner_id &&
        command.input.input_id.startsWith(row.import_id + ":")
      );
    },
    apply: async (tx, input) => {
      const {
        rows: [row],
      } = await tx.query(
        "SELECT row_number,email FROM sdk_csv_rows WHERE input_id=$1 FOR UPDATE",
        [input.input_id],
      );
      if (!row)
        return { state: "already_compensated", result: { deleted: false } };
      if (
        runtime.jobValueHash([row.row_number, row.email]) !==
        input.expected_sha256
      )
        return { state: "needs_review", result: { deleted: false } };
      await tx.query("DELETE FROM sdk_csv_rows WHERE input_id=$1", [
        input.input_id,
      ]);
      await tx.query(
        "INSERT INTO sdk_csv_recovery_audit VALUES ($1,1) ON CONFLICT(input_id) DO UPDATE SET actions=sdk_csv_recovery_audit.actions+1",
        [input.input_id],
      );
      return { state: "compensated", result: { deleted: true } };
    },
  });
  return { recovery, handler, business };
}
