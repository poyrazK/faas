// cron-worker — Wave 0 PR-B stateless-contract template (function-handler
// shape, per UX spec §8).
//
// Unlike app templates (express on :8080), this template is a function the
// node22 runner invokes directly. It exports the Fetch API form
// (`export default { fetch }`) because QStash signs the exact request bytes:
// the event-handler form receives a JSON body already parsed into an object,
// so the raw body a signature covers is not available there.
//
// This is a SCAFFOLD that demonstrates the Upstash QStash wiring: the handler
// verifies the QStash signature, logs the invocation, and increments an
// Upstash Redis counter to demonstrate durable progress across cold boots.
//
// QStash signs every request with a JWT in the `Upstash-Signature` header:
// HS256 over `<header>.<payload>` keyed by your QStash *signing key*, with
// claims iss="Upstash", nbf/exp, and `body` = base64url(SHA-256(raw body)).
// QStash rotates between a current and a next signing key, so both are
// accepted. (The QSTASH_TOKEN API token only authenticates calls you make TO
// QStash, e.g. creating the schedule; it does not sign deliveries.)
//
// Required env vars (set via `gregale secrets set --app <slug> ...`):
//
//	QSTASH_CURRENT_SIGNING_KEY — current QStash signing key
//	QSTASH_NEXT_SIGNING_KEY    — next QStash signing key (rotation)
//	UPSTASH_REDIS_REST_URL     — Upstash Redis REST endpoint, e.g.
//	                             https://<instance>.upstash.io
//	UPSTASH_REDIS_REST_TOKEN   — Upstash Redis REST token
//
// Fail-fast: the handler throws on the first invocation if any required env
// var is missing. The runtime surfaces the error as a 500 to QStash, which
// logs it; the customer's `gregale logs <slug>` shows the actionable hint.

import crypto from "node:crypto";

const REQUIRED_ENV = [
  "QSTASH_CURRENT_SIGNING_KEY",
  "QSTASH_NEXT_SIGNING_KEY",
  "UPSTASH_REDIS_REST_URL",
  "UPSTASH_REDIS_REST_TOKEN",
];

// Seconds of clock skew tolerated on nbf/exp.
const CLOCK_TOLERANCE_S = 60;

function missingEnv() {
  return REQUIRED_ENV.filter((k) => !process.env[k] || process.env[k].length === 0);
}

function fail(msg, hint) {
  const e = new Error(msg);
  e.hint = hint;
  throw e;
}

const stripPadding = (s) => s.replace(/=+$/, "");

function base64url(buf) {
  return stripPadding(Buffer.from(buf).toString("base64").replace(/\+/g, "-").replace(/\//g, "_"));
}

// verifyWithKey checks one QStash JWT against one signing key and the exact
// raw request body. Returns an error string, or "" when valid.
function verifyWithKey(jwt, key, rawBody, nowS) {
  const parts = jwt.split(".");
  if (parts.length !== 3) {
    return "malformed signature";
  }
  const [encodedHeader, encodedClaims, signature] = parts;
  const expected = base64url(crypto.createHmac("sha256", key).update(`${encodedHeader}.${encodedClaims}`).digest());
  const a = Buffer.from(expected);
  const b = Buffer.from(stripPadding(signature));
  if (a.length !== b.length || !crypto.timingSafeEqual(a, b)) {
    return "signature mismatch";
  }
  let claims;
  try {
    claims = JSON.parse(Buffer.from(encodedClaims, "base64url").toString("utf8"));
  } catch {
    return "malformed claims";
  }
  if (claims.iss !== "Upstash") {
    return "unexpected issuer";
  }
  if (typeof claims.exp === "number" && nowS - CLOCK_TOLERANCE_S > claims.exp) {
    return "signature expired";
  }
  if (typeof claims.nbf === "number" && nowS + CLOCK_TOLERANCE_S < claims.nbf) {
    return "signature not yet valid";
  }
  const bodyHash = base64url(crypto.createHash("sha256").update(rawBody).digest());
  if (typeof claims.body !== "string" || stripPadding(claims.body) !== bodyHash) {
    return "body hash mismatch";
  }
  return "";
}

// verifyQStashSignature accepts a signature from either the current or the
// next signing key, so deliveries keep verifying across a key rotation.
export function verifyQStashSignature(jwt, rawBody, nowS = Math.floor(Date.now() / 1000)) {
  if (!jwt) {
    return false;
  }
  for (const key of [process.env.QSTASH_CURRENT_SIGNING_KEY, process.env.QSTASH_NEXT_SIGNING_KEY]) {
    if (key && verifyWithKey(jwt, key, rawBody, nowS) === "") {
      return true;
    }
  }
  return false;
}

async function bumpRedisCounter(key) {
  const url = `${process.env.UPSTASH_REDIS_REST_URL}/incr/${encodeURIComponent(key)}`;
  const resp = await globalThis.fetch(url, {
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

export default {
  async fetch(request, _env, ctx) {
    const missing = missingEnv();
    if (missing.length > 0) {
      fail(
        `missing env: ${missing.join(", ")}`,
        `run: gregale secrets set --app <slug> ${missing.map((k) => k + "=...").join(" ")}`,
      );
    }

    // The Fetch API form preserves the exact request bytes the signature
    // covers; re-serializing a parsed JSON body would not.
    const rawBody = await request.text();
    if (!verifyQStashSignature(request.headers.get("upstash-signature") || "", rawBody)) {
      return new Response(JSON.stringify({ ok: false, error: "invalid Upstash-Signature" }), {
        status: 401,
        headers: { "content-type": "application/json" },
      });
    }

    // Bump a single counter so a customer's "how many times has my cron
    // fired" is a single Redis GET on `cron-worker:fired`. The counter
    // survives cold boots (park + wake), which is the whole point of using a
    // managed Redis instead of local filesystem state.
    const count = await bumpRedisCounter("cron-worker:fired");
    const invocationID = request.headers.get("x-faas-invocation-id") || "";

    console.error("cron-worker fired", {
      invocation_id: invocationID,
      count,
      payload_bytes: Buffer.byteLength(rawBody, "utf8"),
      fired_at: new Date().toISOString(),
    });

    return new Response(JSON.stringify({ ok: true, invocation_id: invocationID, count }), {
      status: 200,
      headers: { "content-type": "application/json" },
    });
  },
};
