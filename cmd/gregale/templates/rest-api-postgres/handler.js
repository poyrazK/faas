import express from "express";
import pg from "pg";
import { createDatabase, poolConfig } from "./database.js";
import { createApp } from "./app.js";

if (!process.env.DATABASE_URL || process.env.MIGRATION_DATABASE_URL) {
  console.error("Serving requires DATABASE_URL and must not receive MIGRATION_DATABASE_URL");
  process.exit(1);
}
let database;
try {
  database = createDatabase(() => new pg.Pool(poolConfig(process.env.DATABASE_URL)));
} catch {
  console.error("Invalid DATABASE_URL configuration");
  process.exit(1);
}
const server = createApp(express, database).listen(process.env.PORT || 8080);
let stopping = false;
async function stop() {
  if (stopping) return;
  stopping = true;
  server.close();
  server.closeIdleConnections();
  try { await database.close(); }
  finally { server.closeAllConnections(); }
}
for (const signal of ["SIGTERM", "SIGINT"]) process.on(signal, () => {
  void stop().catch(() => { console.error("Database shutdown failed"); process.exitCode = 1; });
});
