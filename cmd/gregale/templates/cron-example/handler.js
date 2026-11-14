// cron-example handler for gregale.
//
// Designed to be hit by `gregale cron create` so you can verify your
// schedule fires: each run logs one fired_at line. Returns the UTC timestamp + a per-invocation UUID
// (the guest RNG is reseeded post-restore, so collisions across
// restores are vanishingly rare — see guest/init/main.go).

import express from "express";
import { randomUUID } from "node:crypto";

const app = express();
const port = process.env.PORT || 8080;

app.post("/", express.json(), (req, res) => {
  const run = { fired_at: new Date().toISOString(), invocation_id: randomUUID() };
  // `gregale logs <slug> --follow` shows this line on every run.
  console.log(JSON.stringify(run));
  res.json({ ...run, received: req.body ?? null });
});

app.get("/healthz", (_req, res) => {
  res.status(200).json({ ok: true });
});

app.listen(port, () => {
  console.log(`cron-gregale listening on :${port}`);
});