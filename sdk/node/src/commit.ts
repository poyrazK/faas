import { randomUUID } from "node:crypto";

export interface CommitEvent {
  id?: string;
  type: string;
  data: unknown;
}

/** A PostgreSQL client already participating in the business transaction. */
export interface CommitTransaction {
  query(sql: string, values: unknown[]): Promise<unknown>;
}

/** Insert only: caller owns BEGIN/COMMIT/ROLLBACK; no Gregale API call occurs. */
export async function insertCommitEvent(
  transaction: CommitTransaction,
  event: CommitEvent,
): Promise<string> {
  const id = event.id ?? randomUUID();
  if (!/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(id)) {
    throw new TypeError("Commit event ID must be a UUID");
  }
  if (typeof event.type !== "string" || [...event.type].length < 1 || [...event.type].length > 256) {
    throw new TypeError("Commit event type must contain 1-256 characters");
  }
  const payload = JSON.stringify(event.data);
  if (payload === undefined) throw new TypeError("Commit data must be JSON serializable");
  await transaction.query(
    "INSERT INTO public.gregale_outbox(event_id,event_type,payload) VALUES ($1::uuid,$2,$3::jsonb)",
    [id, event.type, payload],
  );
  return id;
}
