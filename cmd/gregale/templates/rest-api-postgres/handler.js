// rest-api-postgres — Wave 0 PR-B stateless-contract template.
//
// This is a SCAFFOLD, not a production REST API. It demonstrates the
// Express + pg wiring against a managed PostgreSQL provider (Neon,
// Supabase, PlanetScale, CockroachDB Cloud) and the fail-fast
// contract (UX spec §8): if `DATABASE_URL` is missing, the handler
// exits at startup with an actionable hint.
//
// The release command creates the schema using MIGRATION_DATABASE_URL.
// Serving instances receive only the restricted DATABASE_URL binding.
//
// Required env vars (set via `gregale secrets set --app <slug> ...`):
//
//	DATABASE_URL — postgres://user:pass@host:port/dbname?sslmode=require
//	             For Neon / Supabase / PlanetScale / CockroachDB Cloud,
//	             the host is provider-supplied; the URL usually carries
//	             `?sslmode=require` for TLS.

import express from "express";
import pg from "pg";

const REQUIRED_ENV = ["DATABASE_URL"];

function missingEnv() {
  return REQUIRED_ENV.filter((k) => !process.env[k] || process.env[k].length === 0);
}

const missing = missingEnv();
if (missing.length > 0) {
  console.error("Missing required env vars:", missing.join(", "));
  console.error("Set it via:");
  console.error(
    "  gregale secrets set --app <slug> DATABASE_URL=postgres://user:pass@host/db?sslmode=require",
  );
  process.exit(1);
}

// Reject the connection string in the response — we never log the
// value, only the key (per the platform's secret-leak contract).
const pool = new pg.Pool({
  connectionString: process.env.DATABASE_URL,
  ssl:
    process.env.DATABASE_URL.includes("sslmode=require") ||
    process.env.DATABASE_URL.includes("sslmode=verify-full")
      ? { rejectUnauthorized: true }
      : false,
  // Cold-boot budget: we get ~6s before guest-init gives up. Keep
  // the pool small + idle-timeout short so a wake doesn't burn the
  // budget on a half-dead connection.
  max: 5,
  idleTimeoutMillis: 30_000,
  connectionTimeoutMillis: 5_000,
});

const app = express();
const port = process.env.PORT || 8080;
app.use(express.json({ limit: "64kb" }));

app.get("/notes", async (_req, res) => {
  try {
    const { rows } = await pool.query("SELECT id, body, created_at FROM notes ORDER BY id DESC LIMIT 50");
    res.json({ ok: true, notes: rows });
  } catch (err) {
    console.error("list failed:", err.message);
    res.status(500).json({ ok: false, error: err.message });
  }
});

app.post("/notes", async (req, res) => {
  const body = (req.body && req.body.body) || "";
  if (!body) {
    return res.status(400).json({ ok: false, error: "missing 'body'" });
  }
  try {
    const { rows } = await pool.query(
      "INSERT INTO notes (body) VALUES ($1) RETURNING id, body, created_at",
      [body],
    );
    res.status(201).json({ ok: true, note: rows[0] });
  } catch (err) {
    console.error("insert failed:", err.message);
    res.status(500).json({ ok: false, error: err.message });
  }
});

// Health probe actually pings the pool — distinguishes "process is up"
// from "DB connection works", which is what we want for the platform's
// readiness signal.
app.get("/healthz", async (_req, res) => {
  try {
    await pool.query("SELECT 1");
    res.status(200).json({ ok: true, db: "ok" });
  } catch (err) {
    res.status(503).json({ ok: false, db: err.message });
  }
});

app.listen(port, () => {
  console.log(`rest-api-postgres listening on :${port}`);
});
