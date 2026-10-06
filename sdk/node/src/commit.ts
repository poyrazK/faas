import { randomUUID } from "node:crypto";

export interface CommitEventRouting {
  version: 2;
  key: string | number | boolean;
  platform_tenant_id?: string;
}

export interface CommitEvent {
  id?: string;
  type: string;
  data: unknown;
  routing?: CommitEventRouting;
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
  if (event.routing !== undefined) {
    const routing = event.routing;
    if (routing.version !== 2 || !["string", "number", "boolean"].includes(typeof routing.key)
        || (typeof routing.key === "string" && (routing.key.length === 0 || Buffer.byteLength(routing.key) > 254))
        || (typeof routing.key === "number" && !boundedNumberKey(routing.key))
        || Object.keys(routing).some((field) => !["version", "key", "platform_tenant_id"].includes(field))) {
      throw new TypeError("Commit routing requires version 2 and a bounded nonempty scalar key");
    }
    if (routing.platform_tenant_id !== undefined && !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(routing.platform_tenant_id)) {
      throw new TypeError("Commit routing customer must be a UUID");
    }
    await transaction.query(
      "INSERT INTO public.gregale_outbox(event_id,event_type,payload,routing) VALUES ($1::uuid,$2,$3::jsonb,$4::jsonb)",
      [id, event.type, payload, JSON.stringify({ ...routing, platform_tenant_id: routing.platform_tenant_id?.toLowerCase() })],
    );
    return id;
  }
  await transaction.query(
    "INSERT INTO public.gregale_outbox(event_id,event_type,payload) VALUES ($1::uuid,$2,$3::jsonb)",
    [id, event.type, payload],
  );
  return id;
}

// Use the exact decimal JSON representation, matching the server's rational key.
function boundedNumberKey(key: number): boolean {
  if (!Number.isFinite(key)) return false;
  const encoded = JSON.stringify(key);
  const [mantissa = "", exponent = "0"] = encoded.toLowerCase().split("e");
  const power = Number(exponent);
  if (Math.abs(power) > 256) return false;
  const fractional = mantissa.includes(".") ? mantissa.length - mantissa.indexOf(".") - 1 : 0;
  let numerator = BigInt(mantissa.replace(".", ""));
  let denominator = 1n;
  const scale = power - fractional;
  if (scale >= 0) numerator *= 10n ** BigInt(scale);
  else denominator = 10n ** BigInt(-scale);
  let a = numerator < 0n ? -numerator : numerator;
  let b = denominator;
  while (b !== 0n) { const remainder = a % b; a = b; b = remainder; }
  numerator /= a;
  denominator /= a;
  const canonical = denominator === 1n ? String(numerator) : `${numerator}/${denominator}`;
  return canonical.length + 2 <= 256;
}
