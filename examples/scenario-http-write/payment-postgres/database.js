export function poolConfig(connectionString) {
  const url = new URL(connectionString);
  if (!["postgres:", "postgresql:"].includes(url.protocol)) throw new Error("invalid_database_configuration");
  const mode = url.searchParams.get("sslmode");
  if (mode && !["disable", "require", "verify-ca", "verify-full"].includes(mode)) throw new Error("invalid_database_configuration");
  for (const key of ["ssl", "sslcert", "sslkey", "sslrootcert", "uselibpqcompat"]) {
    if (url.searchParams.has(key)) throw new Error("invalid_database_configuration");
  }
  url.searchParams.delete("sslmode");
  return { connectionString: url.href, ssl: mode === "disable" ? false : { rejectUnauthorized: true },
    max: 5, connectionTimeoutMillis: 2000, idleTimeoutMillis: 1000,
    statement_timeout: 3000, query_timeout: 4000, lock_timeout: 1500, idle_in_transaction_session_timeout: 4000 };
}
export function namespaceValue(namespace) {
  if (typeof namespace !== "string" || !/^[0-9a-f]{32}$/.test(namespace)) throw new Error("invalid_ledger_namespace");
  return namespace;
}
