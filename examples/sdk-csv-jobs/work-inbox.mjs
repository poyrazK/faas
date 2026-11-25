import { readFile } from "node:fs/promises";

export async function csvWorkInbox({
  sdk,
  runtime,
  scope,
  pool,
  cursorKey,
  recovery,
  refund,
}) {
  const sql = JSON.parse(
    await readFile(new URL("work-inbox-queries.json", import.meta.url), "utf8"),
  );
  const cleanup = runtime.jobValueHash([scope, "csv_cleanup"]);
  const refundWorkflow = runtime.jobValueHash([
    "gregale.outbound-tool.v1",
    scope,
    "csv_refund",
  ]);
  async function query(statement, args, signal) {
    signal.throwIfAborted();
    const connection = await pool.connect();
    try {
      await connection.query("BEGIN READ ONLY");
      await connection.query("SET LOCAL statement_timeout = '5s'");
      const result = await connection.query(statement, args);
      signal.throwIfAborted();
      return result;
    } finally {
      try {
        await connection.query("ROLLBACK");
      } finally {
        connection.release();
      }
    }
  }
  return sdk.defineWorkInbox({
    scope,
    cursorKey,
    store: sdk.postgresWorkInboxStore(query),
    sources: [
      sdk.pipelineWorkInboxSource(recovery.tool, {
        receiptSubjectId: "csv_cleanup",
      }),
      sdk.pipelineWorkInboxSource(refund.tool, {
        receiptSubjectId: "csv_refund",
      }),
      sdk.outboundWorkInboxSource(refund),
    ],
    async authorize(_filter, principal) {
      return principal === "alice" || principal === "bob"
        ? { subject_id: principal, owner_id: principal }
        : null;
    },
    async businessReference(candidate, actor, signal) {
      const { kind, name, request_id } = candidate;
      const { rows } =
        kind === "action"
          ? await query(sql.refund, [request_id, actor.owner_id], signal)
          : await query(
              name === recovery.tool.name ? sql.cleanup : sql.refund_workflow,
              [
                name === recovery.tool.name ? cleanup : refundWorkflow,
                request_id,
                actor.owner_id,
              ],
              signal,
            );
      return rows[0]?.business_ref ?? null;
    },
  });
}
