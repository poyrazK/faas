import express from "express";
import pg from "pg";
import {createDatabase, poolConfig} from "./database.js";

const app = express();
let database, server, stopping = false;
async function stop() {
  if (stopping) return;
  stopping = true;
  server?.close();
  server?.closeIdleConnections();
  try {await database?.close();}
  finally {server?.closeAllConnections();}
}
for (const signal of ["SIGTERM", "SIGINT"]) process.once(signal, () => {
  void stop().catch(() => {console.error("Database shutdown failed"); process.exitCode = 1;});
});

app.get("/healthz", async (_req, res) => {
  try {
    await database.query("SELECT 1");
    res.status(200).json({ok: true, db: "ok"});
  } catch {res.status(503).json({ok: false, db: "unavailable"});}
});
try {
  database = createDatabase(config => {
    const url = new URL(config.connectionString);
    if (!["require", "verify-full"].includes(url.searchParams.get("sslmode"))) throw new Error();
    return new pg.Pool(poolConfig(config.connectionString));
  }, {requireReload: true, readinessQuery: "SELECT 1"});
  await database.start();
  if (!stopping) server = app.listen(Number(process.env.PORT || 8080), () => console.log("secret-reload-node listening"));
} catch {
  if (!stopping) {console.error("initial secret application could not be confirmed"); process.exitCode = 1;}
  await stop().catch(() => {process.exitCode = 1;});
}
