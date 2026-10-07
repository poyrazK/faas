// Shared application PostgreSQL receipt engine (ADR-586, ADR-638).
export interface OperationTransaction {
  query(sql: string, values?: unknown[]): Promise<{ rows: Record<string, unknown>[] }>;
}
export interface OperationConnection extends OperationTransaction {
  release(discard?: boolean): void;
}
export interface OperationPool {
  connect(): Promise<OperationConnection>;
}
export interface OperationTransactionResult {
  /** Send these exact JSON bytes using res.type('application/json').send(body). */
  body: string;
  replayed: boolean;
}

export class OperationConflictError extends Error {
  readonly code = "operation_receipt_conflict";
  constructor() { super("operation receipt scope or input differs"); this.name = "OperationConflictError"; }
}
export class OperationCommitUnknownError extends Error {
  readonly code = "operation_commit_unknown";
  constructor(cause: unknown) {
    super("operation commit outcome unknown; retry with the same operation identity", { cause });
    this.name = "OperationCommitUnknownError";
  }
}

interface ReceiptIdentity {
  operationId: string;
  accountId: string;
  appId: string;
  platformTenantId?: string;
}

// Protocol is selected by SDK code, never by customer headers or SQL identifiers.
export async function operationReceiptTransaction(
  pool: OperationPool, request: ReceiptIdentity, digest: Buffer,
  protocol: 'managed' | 'customer', handler: (transaction: OperationTransaction) => Promise<string>,
  validate: (body: string) => void,
): Promise<OperationTransactionResult> {
  const table = protocol === 'managed' ? 'public.gregale_operation_inbox' : 'public.gregale_customer_operation_inbox';
  const namespace = protocol === 'managed' ? 'gregale.operation-inbox.v1:' : 'gregale.customer-operation-inbox.v1:';
  const connection = await pool.connect();
  let discard = false;
  try {
    await connection.query("BEGIN ISOLATION LEVEL READ COMMITTED");
    await connection.query("SELECT pg_advisory_xact_lock(hashtextextended($1 || $2::uuid::text, 0))", [namespace, request.operationId]);
    const stored = (await connection.query(
      `SELECT account_id::text,app_id::text,coalesce(platform_tenant_id::text,'') AS platform_tenant_id,request_digest,response_body FROM ${table} WHERE operation_id=$1::uuid`,
      [request.operationId],
    )).rows[0];
    let body: string;
    if (stored) {
      if (stored.account_id !== request.accountId || stored.app_id !== request.appId
          || stored.platform_tenant_id !== (request.platformTenantId ?? "") || !(stored.request_digest instanceof Uint8Array)
          || !digest.equals(Buffer.from(stored.request_digest))) throw new OperationConflictError();
      if (typeof stored.response_body !== "string") throw new TypeError("invalid saved operation response");
      body = stored.response_body;
      validate(body);
    } else {
      body = await handler(connection);
      validate(body);
      await connection.query(
        `INSERT INTO ${table}(operation_id,account_id,app_id,platform_tenant_id,request_digest,response_body) VALUES ($1::uuid,$2::uuid,$3::uuid,nullif($4,'')::uuid,$5,$6)`,
        [request.operationId, request.accountId, request.appId, request.platformTenantId ?? "", digest, body],
      );
    }
    try { await connection.query("COMMIT"); }
    catch (error) { throw new OperationCommitUnknownError(error); }
    return { body, replayed: stored !== undefined };
  } catch (error) {
    try { await connection.query("ROLLBACK"); } catch { discard = true; }
    throw error;
  } finally {
    connection.release(discard);
  }
}
