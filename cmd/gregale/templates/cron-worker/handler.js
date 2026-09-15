// cron-worker — Wave 0 PR-B stateless-contract template (function-handler
// shape, per UX spec §8).
//
// Unlike app templates (express on :8080), this template exports a
// single async handler(event, ctx) that the node22 runner invokes
// directly. The CLI forces --runtime node22 --handler handler.handler
// when deploying (commands2.go:298-300), so wiring is automatic.
//
// This is a SCAFFOLD that demonstrates the Upstash QStash wiring:
// the handler validates the QStash signature, logs the invocation,
// and increments an Upstash Redis counter to demonstrate durable
// progress across cold boots. QStash verifies the signature with
// its signing key (different from the customer's secrets) — we
// verify by recomputing HMAC-SHA256 over the raw body + the
// `Upstash-Signature` header.
//
// Required env vars (set via `gregale secrets set --app <slug> ...`):
//
//	QSTASH_TOKEN             — QStash signing key (used to verify
//	                          Upstash-Signature on incoming invokes).
//	UPSTASH_REDIS_REST_URL   — Upstash Redis REST endpoint, e.g.
//	                          https://<instance>.upstash.io
//	UPSTASH_REDIS_REST_TOKEN — Upstash Redis REST token
//
// Fail-fast: the handler throws on the first invocation if any
// required env var is missing. The runtime surfaces the error as
// a 500 to QStash, which logs it; the customer's `gregale logs <slug>`
// shows the actionable hint.

import crypto from "node:crypto";

const REQUIRED_ENV = [
  "QSTASH_TOKEN",
  "UPSTASH_REDIS_REST_URL",
  "UPSTASH_REDIS_REST_TOKEN",
];

function missingEnv() {
  return REQUIRED_ENV.filter((k) => !process.env[k] || process.env[k].length === 0);
}

function fail(msg, hint) {
  const e = new Error(msg);
  e.hint = hint;
  throw e;
}

function verifyQStashSignature(rawBody, signatureHeader) {
  if (!signatureHeader) {
    return false;
  }
  // Upstash-Signature looks like "v1=<hex>". HMAC-SHA256 over the
  // raw body with QSTASH_TOKEN as the key.
  const expected =
    "v1=" +
    crypto
      .createHmac("sha256", process.env.QSTASH_TOKEN)
      .update(rawBody || "")
      .digest("hex");
  const a = Buffer.from(expected, "utf8");
  const b = Buffer.from(signatureHeader, "utf8");
  if (a.length !== b.length) {
    return false;
  }
  return crypto.timingSafeEqual(a, b);
}

async function bumpRedisCounter(key) {
  const url = `${process.env.UPSTASH_REDIS_REST_URL}/incr/${encodeURIComponent(key)}`;
  const resp = await fetch(url, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${process.env.UPSTASH_REDIS_REST_TOKEN}`,
    },
  });
  if (!resp.ok) {
    throw new Error(`upstash redis ${resp.status}: ${await resp.text()}`);
  }
  return (await resp.json()).result;
}

export async function handler(event, ctx) {
  const missing = missingEnv();
  if (missing.length > 0) {
    fail(
      `missing env: ${missing.join(", ")}`,
      `run: gregale secrets set --app <slug> ${missing.map((k) => k + "=...").join(" ")}`,
    );
  }

  // event.body is the raw body string the runner received. The
  // QStash signature is in event.headers["Upstash-Signature"].
  const rawBody = event && typeof event.body === "string" ? event.body : "";
  const sig =
    (event.headers && (event.headers["Upstash-Signature"] || event.headers["upstash-signature"])) ||
    "";
  if (!verifyQStashSignature(rawBody, sig)) {
    fail(
      "invalid Upstash-Signature",
      "verify QSTASH_TOKEN is set correctly and QStash is signing with the same key",
    );
  }

  // Bump a single counter so a customer's "how many times has my
  // cron fired" is a single Redis GET on `cron-worker:fired`. The
  // counter survives cold boots (park + wake), which is the whole
  // point of using a managed Redis instead of local filesystem state.
  const count = await bumpRedisCounter("cron-worker:fired");

  ctx.log.info("cron-worker fired", {
    invocation_id: ctx.invocation_id,
    count,
    payload_bytes: Buffer.byteLength(rawBody, "utf8"),
    fired_at: new Date().toISOString(),
  });

  return {
    statusCode: 200,
    headers: { "content-type": "application/json" },
    body: JSON.stringify({
      ok: true,
      invocation_id: ctx.invocation_id,
      count,
    }),
  };
}
