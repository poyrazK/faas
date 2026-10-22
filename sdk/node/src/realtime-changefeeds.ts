import type { CommitTransaction } from './commit.js';

export interface RealtimeChangefeedConfig {
  name: string;
  endpointId: string;
  /** Full static channel, or prefix followed by the tenant column value. */
  channel: string;
  /** Explicit allowlist; must include the primary key and tenant column. */
  columns: string[];
  tenantColumn?: string;
}

/** Explicit owner DDL. Functions must already be installed in the customer DB. */
export async function installRealtimeChangefeed(db: CommitTransaction, table: string, config: RealtimeChangefeedConfig): Promise<void> {
  await db.query('SELECT public.gregale_realtime_install_changefeed($1::regclass,$2::jsonb)', [table, JSON.stringify(config)]);
}

/** Removes future capture only; committed outbox events remain queued. */
export async function removeRealtimeChangefeed(db: CommitTransaction, table: string, name: string): Promise<void> {
  await db.query('SELECT public.gregale_realtime_remove_changefeed($1::regclass,$2)', [table, name]);
}
