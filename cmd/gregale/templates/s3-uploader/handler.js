// s3-uploader — Wave 0 PR-B stateless-contract template.
//
// This is a SCAFFOLD, not a production-ready uploader. It demonstrates
// the AWS SDK v3 client against an S3-compatible object store (AWS S3,
// Cloudflare R2, Backblaze B2). The fail-fast contract (UX spec §8) is
// that the handler exits at startup with an actionable `gregale secrets
// set` hint if any of the required env vars are missing — the customer
// should never hit an opaque "credentials not found" error from the SDK
// in production.
//
// Required env vars (set via `gregale secrets set --app <slug> ...`):
//
//	S3_BUCKET          — bucket name (e.g. "my-app-uploads")
//	S3_REGION          — region (e.g. "us-east-1", or "auto" for R2)
//	S3_ACCESS_KEY_ID   — IAM / R2 / B2 access key id
//	S3_SECRET_ACCESS_KEY — secret access key
//	S3_ENDPOINT        — optional, set for R2 (https://<acct>.r2.cloudflarestorage.com)
//	                    or B2 (https://s3.<region>.backblazeb2.com). Omit for AWS S3.

import express from "express";
import { S3Client, PutObjectCommand } from "@aws-sdk/client-s3";

const REQUIRED_ENV = [
  "S3_BUCKET",
  "S3_REGION",
  "S3_ACCESS_KEY_ID",
  "S3_SECRET_ACCESS_KEY",
];

function missingEnv() {
  return REQUIRED_ENV.filter((k) => !process.env[k] || process.env[k].length === 0);
}

const missing = missingEnv();
if (missing.length > 0) {
  console.error("Missing required env vars:", missing.join(", "));
  console.error("Set them via:");
  console.error(
    "  gregale secrets set --app <slug> " +
      "S3_BUCKET=... S3_REGION=... S3_ACCESS_KEY_ID=... S3_SECRET_ACCESS_KEY=...",
  );
  if (!process.env.S3_ENDPOINT) {
    console.error(
      "  (and S3_ENDPOINT=https://<account>.r2.cloudflarestorage.com if you're using Cloudflare R2)",
    );
  }
  process.exit(1);
}

const s3 = new S3Client({
  region: process.env.S3_REGION,
  endpoint: process.env.S3_ENDPOINT || undefined,
  forcePathStyle: !!process.env.S3_ENDPOINT, // R2/B2 prefer path-style over virtual-hosted
  credentials: {
    accessKeyId: process.env.S3_ACCESS_KEY_ID,
    secretAccessKey: process.env.S3_SECRET_ACCESS_KEY,
  },
});

const app = express();
const port = process.env.PORT || 8080;

// Simple text upload endpoint. For a real app, use express-fileupload
// or busboy to handle multipart/form-data — kept minimal here so the
// scaffold stays < 100 LOC.
// Keep the request bytes exactly as sent, whatever the Content-Type.
// express.text() only parses text/plain: `curl --data` (form-encoded),
// images and every other binary upload arrived as an empty object, and
// PutObject failed.
app.use(express.raw({ type: () => true, limit: "10mb" }));

app.post("/upload/:filename", async (req, res) => {
  const key = req.params.filename;
  try {
    await s3.send(
      new PutObjectCommand({
        Bucket: process.env.S3_BUCKET,
        Key: key,
        Body: Buffer.isBuffer(req.body) ? req.body : Buffer.alloc(0),
        ContentType: req.get("content-type") || "application/octet-stream",
      }),
    );
    res.status(201).json({ ok: true, key, bucket: process.env.S3_BUCKET });
  } catch (err) {
    console.error("upload failed:", err.message);
    res.status(500).json({ ok: false, error: err.message });
  }
});

app.get("/healthz", (_req, res) => {
  res.status(200).json({ ok: true });
});

app.listen(port, () => {
  console.log(`s3-uploader listening on :${port}, bucket=${process.env.S3_BUCKET}`);
});
