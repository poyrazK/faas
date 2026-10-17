import express from "express";
import pg from "pg";
import { createDatabase, poolConfig } from "./database.js";
import { createApp } from "./app.js";

let database, server, stopping = false;
async function stop() {
  if (stopping) return;
  stopping = true;
  server?.close();
  server?.closeIdleConnections();
  try { await database?.close(); }
  finally { server?.closeAllConnections(); }
}
for (const signal of ["SIGTERM", "SIGINT"]) process.once(signal, () => {
  void stop().catch(() => { console.error("Database shutdown failed"); process.exitCode = 1; });
});

try {
  if (process.env.MIGRATION_DATABASE_URL) throw new Error();
  database = createDatabase(config => new pg.Pool(poolConfig(config.connectionString)));
  await database.start();
  if (!stopping) server = createApp(express, database).listen(process.env.PORT || 8080);
} catch {
  if (!stopping) {
    console.error("Serving requires valid DATABASE_URL credentials and must not receive MIGRATION_DATABASE_URL");
    process.exitCode = 1;
  }
  await stop().catch(() => { process.exitCode = 1; });
}
