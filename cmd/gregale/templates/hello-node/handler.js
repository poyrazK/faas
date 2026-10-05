// Hello-world handler for gregale.
//
// Listens on :8080 (the port guest-init forwards to). Returns a tiny
// JSON greeting so a curl check is enough to verify a deploy landed.
// Secrets set with `gregale env push` arrive as environment variables;
// check which keys an app has with `gregale secrets list --app <slug>`.

import express from "express";

const app = express();
const port = process.env.PORT || 8080;

app.get("/", (_req, res) => {
  // This URL is public: do not echo environment variable names here
  // (secret names reveal integrations). `gregale secrets list --app
  // <slug>` shows which keys the app has.
  res.json({
    message: "hello from gregale",
    node: process.version,
  });
});

app.get("/healthz", (_req, res) => {
  res.status(200).json({ ok: true });
});

app.listen(port, () => {
  console.log(`hello-gregale listening on :${port}`);
});