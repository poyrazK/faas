// webhook-receiver — Move 1 stateless-contract template.
//
// A SCAFFOLD for receiving arbitrary inbound webhooks on a public
// port-8080 endpoint. If `WEBHOOK_SECRET` is set, every request must
// carry a matching `X-Webhook-Secret` header; requests without it are
// rejected 401. If the env var is unset, the handler accepts every
// request — useful for development or for providers that don't sign
// their payloads.
//
// The body is captured RAW (express.raw) so providers that send
// non-JSON payloads (form-encoded, plain text, signed JWT bodies)
// pass through unparsed. Successful receipt is acknowledged with a
// small metadata-only JSON receipt. Request headers and bodies are never
// echoed or logged by the starter because they commonly contain secrets.
//
// Required env vars (set via `gregale secrets set --app <slug> ...`):
//
//	WEBHOOK_SECRET — optional shared secret. When set, requests
//	                 must include a matching `X-Webhook-Secret`
//	                 header. Constant-time compared.
//	WEBHOOK_ALLOWED_PATHS — optional comma-separated allowlist,
//	                        e.g. "/stripe,/github". When set, only
//	                        POSTs to these paths are accepted; other
//	                        paths return 404. Defaults to "/*"
//	                        (accept any path) when unset.

import express from "express";
import crypto from "node:crypto";

const SECRET = process.env.WEBHOOK_SECRET || "";
const ALLOWED_PATHS = (process.env.WEBHOOK_ALLOWED_PATHS || "/*")
  .split(",")
  .map((p) => p.trim())
  .filter((p) => p.length > 0);

// constantTimeEqual — fed equal-length buffers into timingSafeEqual
// so a wrong-length secret doesn't crash, just returns false.
function constantTimeEqual(a, b) {
  const ab = Buffer.from(a, "utf8");
  const bb = Buffer.from(b, "utf8");
  if (ab.length !== bb.length) {
    return false;
  }
  return crypto.timingSafeEqual(ab, bb);
}

// pathAllowed honours the WEBHOOK_ALLOWED_PATHS list. Supports two
// shapes per entry: "/foo" exact match, "/foo/*" prefix match.
// Anything else is treated as exact.
function pathAllowed(reqPath) {
  if (ALLOWED_PATHS.includes("/*")) {
    return true;
  }
  for (const entry of ALLOWED_PATHS) {
    if (entry.endsWith("/*")) {
      const prefix = entry.slice(0, -2);
      if (reqPath === prefix || reqPath.startsWith(prefix + "/")) {
        return true;
      }
    } else if (reqPath === entry) {
      return true;
    }
  }
  return false;
}

const app = express();
const port = process.env.PORT || 8080;

// Capture the raw body so signature checks downstream (or providers
// that POST form-encoded payloads) aren't broken by JSON parsing.
// 1 MiB ceiling matches the slack-bot template.
app.use(
  express.raw({
    type: () => true,
    limit: "1mb",
  }),
);

function verifySecret(req, res) {
  if (!SECRET) {
    return true;
  }
  const got = req.get("X-Webhook-Secret") || "";
  if (!constantTimeEqual(got, SECRET)) {
    res.status(401).json({ ok: false, error: "invalid or missing X-Webhook-Secret" });
    return false;
  }
  return true;
}

// Accept POSTs to any path under /, subject to the path allowlist.
// Other verbs return 405. The receipt contains only bounded routing metadata;
// customers can explicitly select application fields after provider-specific
// signature verification when their use case requires it.
app.post(/.*/, (req, res) => {
  if (!verifySecret(req, res)) {
    return;
  }
  if (!pathAllowed(req.path)) {
    return res.status(404).json({ ok: false, error: "path not in WEBHOOK_ALLOWED_PATHS" });
  }
  const raw = Buffer.isBuffer(req.body) ? req.body : Buffer.from("");
  const receiptId = crypto.randomUUID();
  console.log(
    `webhook-receiver: receipt_id=${receiptId} method=${req.method} path=${req.path} bytes=${raw.length}`,
  );
  return res.status(200).json({
    ok: true,
    receipt_id: receiptId,
    received: {
      method: req.method,
      path: req.path,
      bytes: raw.length,
      content_type: req.get("content-type") || "",
    },
  });
});

app.get("/healthz", (_req, res) => {
  res.status(200).json({
    ok: true,
    secret_configured: SECRET.length > 0,
    allowed_paths: ALLOWED_PATHS,
  });
});

app.listen(port, () => {
  console.log(
    `webhook-receiver listening on :${port} (secret_configured=${SECRET.length > 0})`,
  );
});
