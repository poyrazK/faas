// The CLI starter bundles the canonical SDK runtime so it works before an npm
// SDK release. Published packages expose @gregale/sdk-node/postgres/runtime.
import { createPostgresPool } from "./runtime/postgres-pool-runtime.js";
export { PostgresPoolError as DatabaseError, postgresPoolConfig as poolConfig } from "./runtime/postgres-pool-runtime.js";

export function createDatabase(poolFactory, { timeoutMS = 5_000, restoreTimeoutMS = 1_500, log = console.error } = {}) {
  const pool = createPostgresPool(poolFactory, {
    timeoutMs: timeoutMS,
    lifecycleTimeoutMs: restoreTimeoutMS,
    readinessQuery: { text: "SELECT id FROM notes LIMIT 1" },
    onEvent: ({ type }) => log(`database_${type}`),
  });
  return {
    query: pool.query.bind(pool),
    beforeCheckpoint: pool.beforeCheckpoint.bind(pool),
    restore: pool.afterRestore.bind(pool),
    close: pool.close.bind(pool),
    get generation() { return pool.generation; },
    get diagnostics() { return pool.diagnostics; },
  };
}
