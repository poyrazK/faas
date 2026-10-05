import express from "express";
import pg from "pg";
import { applyInitialSecretSnapshot, applyLatestSecretSnapshot, markSecretReloadReady } from "./secret-reload.js";

const app = express();
const port = Number(process.env.PORT || 8080);
let pool;
let server;
let stopping = false;
const shutdown = new AbortController();

function stop() {
  if (stopping) return;
  stopping = true;
  shutdown.abort();
  server?.close();
  pool?.end().catch(() => {});
}

process.once("SIGTERM", stop);
process.once("SIGINT", stop);

function newPool(connectionString) {
  let parsed;
  try {
    parsed = new URL(connectionString);
  } catch {
    throw new Error("DATABASE_URL is invalid");
  }
  if (
    !["postgres:", "postgresql:"].includes(parsed.protocol) ||
    !parsed.hostname ||
    !["require", "verify-full"].includes(parsed.searchParams.get("sslmode"))
  ) {
    throw new Error("DATABASE_URL must be a TLS-enabled PostgreSQL URL");
  }
  return new pg.Pool({
    connectionString,
    ssl: { rejectUnauthorized: true },
    max: 5,
    idleTimeoutMillis: 30_000,
    connectionTimeoutMillis: 5_000,
  });
}

// Keep the live pool until a candidate has authenticated successfully. This
// makes applying a new credential all-or-nothing from the app's perspective.
async function applyDatabaseSecrets({ secrets }) {
  if (!secrets.DATABASE_URL) throw new Error("DATABASE_URL is missing");
  const candidate = newPool(secrets.DATABASE_URL);
  try {
    await candidate.query("SELECT 1");
    if (shutdown.signal.aborted) throw new Error("database initialization cancelled");
  } catch {
    await candidate.end().catch(() => {});
    throw new Error("new database credentials were rejected");
  }

  const previous = pool;
  pool = candidate;
  if (previous) {
    previous.end().catch(() => {
      console.warn("previous database pool did not drain cleanly");
    });
  }
}

app.get("/healthz", async (_req, res) => {
  try {
    await pool.query("SELECT 1");
    res.status(200).json({ ok: true, db: "ok" });
  } catch {
    // Never include a driver error here: it may contain the connection URL.
    res.status(503).json({ ok: false, db: "unavailable" });
  }
});

// Node installs a SIGHUP listener so the default POSIX action (process exit)
// is replaced with serialized in-process pool replacement.
// Start with one serialized initial application. The listener is installed
// synchronously before either readiness or an awaited database operation.
const initialApplication = Promise.resolve().then(async () => {
  await markSecretReloadReady();
  return applyInitialSecretSnapshot({ apply: applyDatabaseSecrets, signal: shutdown.signal });
});
let reloadQueue = initialApplication;
process.on("SIGHUP", () => {
  if (stopping) return;
  reloadQueue = reloadQueue
    .then(() => applyLatestSecretSnapshot({ apply: applyDatabaseSecrets, signal: shutdown.signal }))
    .catch(() => {
      if (!stopping) console.error("secret reload could not be confirmed");
    });
});

try {
  await initialApplication;
  if (!stopping) {
    server = app.listen(port, () => {
      console.log(`secret-reload-node listening on :${port}`);
    });
  }
} catch {
  if (!stopping) {
    console.error("initial secret application could not be confirmed");
    process.exitCode = 1;
  }
  stop();
}
