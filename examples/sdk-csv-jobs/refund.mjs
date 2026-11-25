import { readFile } from "node:fs/promises";
/** Backend-only definition. Request records and relay configuration are never model arguments. */
export async function csvRefund({ sdk, scope, pool, relayURL, reviewBaseURL }) {
  const query = (sql, args) => pool.query(sql, args),
    business = new sdk.JobContract(
      JSON.parse(
        await readFile(
          new URL("compensation-contract.json", import.meta.url),
          "utf8",
        ),
      ),
    ),
    cleanup = sdk.compensationContracts(business, "csv_cleanup").root;
  async function allowed(id, input, owner, signal) {
    const row = (
      await pool.query(
        "SELECT r.*,p.owner_id AS payment_owner,p.state AS payment_state,p.enabled AS payment_enabled,p.amount_cents AS payment_amount,p.currency AS payment_currency FROM sdk_csv_refund_requests r JOIN sdk_csv_payments p ON p.payment_id=r.payment_id WHERE r.id=$1",
        [id],
      )
    ).rows[0];
    if (
      !row?.enabled ||
      !row.payment_enabled ||
      row.payment_state !== "paid" ||
      row.owner_id !== owner ||
      row.payment_owner !== owner ||
      input.payment_intent !== row.payment_id ||
      input.amount_minor !== Number(row.amount_minor) ||
      input.currency !== row.currency ||
      input.currency !== row.payment_currency ||
      input.amount_minor > row.payment_amount ||
      input.reason !== "requested_by_customer"
    )
      return false;
    const client = sdk.createJobPipelineClient({
      baseURL: scope.base_url,
      scope,
      ownerId: owner,
      token: process.env.FAAS_TOKEN ?? "fixture-owner",
      contract: cleanup,
    });
    return (
      (
        await client
          .resume(row.recovery_checkpoint)
          .result({ timeoutMs: 10000, signal })
      ).state === "completed"
    );
  }
  return sdk.defineOutboundAction(sdk.stripeRefundContract, {
    name: "csv_refund",
    revision: "v1",
    scope,
    integrationId:
      process.env.GREGALE_STRIPE_INTEGRATION ??
      "33333333-3333-4333-8333-333333333333",
    relayURL,
    token: process.env.FAAS_TOKEN ?? "fixture-owner",
    provider: sdk.stripeRefundActionProvider({
      accountId: process.env.GREGALE_STRIPE_ACCOUNT ?? "acct_fixture",
      apiVersion: process.env.GREGALE_STRIPE_API_VERSION ?? "2026-10-28.endive",
    }),
    store: sdk.postgresOutboundActionStore(query),
    toolStore: sdk.postgresPipelineToolStore(query),
    gateStore: sdk.postgresPipelineGateStore(query),
    reviewBaseURL,
    requiredRoles: ["reviewer"],
    authorize: async (request, actor, _operation, signal) =>
      (await allowed(request.request_id, request.input, actor, signal))
        ? { actor_id: actor, owner_id: actor }
        : null,
    authorizeExecution: async (intent, _runId, signal) =>
      allowed(intent.request_id, intent.input, intent.owner_id, signal),
  });
}
