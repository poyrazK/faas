import { readFile } from "node:fs/promises";
import { createHash } from "node:crypto";

/** Fixed read-only handlers. The host supplies the authenticated principal and backend cursor key. */
export async function csvQueries({ sdk, pool, scope, cursorKey }) {
  const specs = JSON.parse(
    await readFile(new URL("query-contracts.json", import.meta.url), "utf8"),
  );
  async function readOnly(sql, args, signal) {
    signal.throwIfAborted();
    const connection = await pool.connect();
    try {
      await connection.query("BEGIN READ ONLY");
      await connection.query("SET LOCAL statement_timeout = '5s'");
      const result = await connection.query(sql, args);
      signal.throwIfAborted();
      return result.rows;
    } finally {
      try {
        await connection.query("ROLLBACK");
      } finally {
        connection.release();
      }
    }
  }
  async function owner(id, access, signal, payment = false) {
    if (typeof id !== "string" || !/^[a-f0-9-]{36}$/.test(id)) return null;
    const rows = await readOnly(
      payment
        ? "SELECT i.owner_id FROM sdk_csv_query_imports i JOIN sdk_csv_payments p USING(import_id) WHERE i.import_id=$1::uuid AND i.owner_id=$2 AND i.enabled AND p.owner_id=$2 AND p.enabled"
        : "SELECT owner_id FROM sdk_csv_query_imports WHERE import_id=$1::uuid AND owner_id=$2 AND enabled",
      [id, access],
      signal,
    );
    return rows.length === 1
      ? { subject_id: access, owner_id: rows[0].owner_id }
      : null;
  }
  const report = sdk.defineQueryResource({
    name: "csv_failure_report",
    description: "Read the verified import failure summary.",
    revision: "v1",
    scope,
    mimeType: "text/csv",
    authorize: (id, access, signal) => owner(id, access, signal),
    async read(id, actor, signal) {
      const rows = await readOnly(
          "SELECT failure_report,report_sha256 FROM sdk_csv_query_imports WHERE import_id=$1::uuid AND owner_id=$2 AND enabled",
          [id, actor.owner_id],
          signal,
        ),
        row = rows[0];
      if (
        !row ||
        createHash("sha256").update(row.failure_report).digest("hex") !==
          row.report_sha256
      )
        throw new sdk.QueryToolError("invalid_output");
      return row.failure_report;
    },
  });
  const tools = Object.fromEntries(
    Object.entries(specs).map(([kind, spec]) => [
      kind,
      sdk.defineQueryTool(new sdk.JobContract(spec.contract), {
        name: spec.name,
        description: spec.description,
        revision: "v1",
        scope,
        cursorKey,
        authorize: (input, access, signal) =>
          owner(input.import_id, access, signal, kind === "payment"),
        async load(input, context) {
          const args = [input.import_id, context.actor.owner_id];
          if (kind === "rows") {
            const rows = await readOnly(
              'SELECT r.input_id,r.row_number,r.email FROM sdk_csv_rows r JOIN sdk_csv_query_imports i ON left(r.input_id,37)=i.import_id::text||\':\' WHERE i.import_id=$1::uuid AND i.owner_id=$2 AND i.enabled AND strpos(r.email,$3)>0 AND ($4::text IS NULL OR r.input_id COLLATE "C">$4::text COLLATE "C") ORDER BY r.input_id COLLATE "C" LIMIT $5',
              [...args, input.search, context.after, context.limit + 1],
              context.signal,
            );
            return {
              items: rows.slice(0, context.limit),
              after:
                rows.length > context.limit
                  ? rows[context.limit - 1].input_id
                  : null,
            };
          }
          const sql =
            kind === "failure"
              ? "SELECT import_id::text,'report'::text AS stage,'failed'::text AS status FROM sdk_csv_query_imports WHERE import_id=$1::uuid AND owner_id=$2 AND enabled"
              : "SELECT p.payment_id,p.amount_cents AS amount_minor,p.currency,p.state FROM sdk_csv_payments p JOIN sdk_csv_query_imports i USING(import_id) WHERE i.import_id=$1::uuid AND i.owner_id=$2 AND i.enabled AND p.owner_id=$2 AND p.enabled";
          return {
            items: await readOnly(sql, args, context.signal),
            after: null,
          };
        },
      }),
    ]),
  );
  const adapters = Object.fromEntries(
    Object.entries(tools).map(([kind, tool]) => [
      kind,
      sdk.createQueryToolMCP(
        tool,
        kind === "failure"
          ? {
              resources: [report],
              links: (item) => [{ resource: report.name, id: item.import_id }],
            }
          : {},
      ),
    ]),
  );
  return { tools, report, adapters };
}
