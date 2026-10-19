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
// guest-init calls these over loopback with one exact marker header. The drain
// keeps SQL sessions out of a capture; restore replaces any captured pool.
for (const [path, marker, run] of [
  ["/internal/before-checkpoint", "X-Faas-Before-Checkpoint", () => database.checkpoint()],
  ["/internal/after-restore", "X-Faas-After-Restore", () => database.restore()],
]) {
  app.post(path, async (req, res) => {
    if (!["127.0.0.1", "::1", "::ffff:127.0.0.1"].includes(req.socket.remoteAddress) ||
        req.get(marker) !== "1") return res.sendStatus(403);
    try {await run(); res.sendStatus(204);}
    catch {res.sendStatus(503);}
  });
}
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
