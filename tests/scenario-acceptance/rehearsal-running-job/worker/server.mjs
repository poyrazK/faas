import http from "node:http";
import { readFile } from "node:fs/promises";
import pg from "pg";
import { S3Client, PutObjectCommand } from "@aws-sdk/client-s3";
import { createImporter } from "./importer.mjs";

const revision = JSON.parse(await readFile(new URL("revision.json", import.meta.url)));
const pool = new pg.Pool({ connectionString: process.env.DATABASE_URL, max: 1 });
// A terminated idle connection is expected during the database fault journey.
// Queries still reject normally; keep the original worker process alive.
pool.on("error", () => {});
const s3 = new S3Client({
  endpoint: process.env.AWS_ENDPOINT_URL_S3, region: process.env.AWS_REGION,
  forcePathStyle: true, maxAttempts: 1,
  requestChecksumCalculation: "WHEN_REQUIRED", responseChecksumValidation: "WHEN_REQUIRED",
});

async function simulated(path, body, key, timeout = 10000) {
  const response = await fetch(process.env.GREGALE_OUTBOUND_URL + path, {
    method: "POST", headers: { "content-type": "application/json", "idempotency-key": key },
    body: JSON.stringify(body), signal: AbortSignal.timeout(timeout),
  });
  if (response.status !== 200) throw new Error("simulation rejected");
  await response.arrayBuffer();
}

async function notify() {
  await simulated("/i/email/emails", {
    from: "imports@example.test", to: ["customer@example.test"], subject: "Import complete", text: "import-001",
  }, "import-001:notification");
}

const importOnce = createImporter({
  state: async (id) => {
    const { rows } = await pool.query("SELECT state FROM public.imports WHERE external_id = $1", [id]);
    if (rows.length !== 1) throw new Error("missing fixture row");
    return rows[0].state;
  },
  update: (id, state) => pool.query("UPDATE public.imports SET state = $2 WHERE external_id = $1", [id, state]),
  upload: (body) => s3.send(new PutObjectCommand({ Bucket: process.env.S3_BUCKET, Key: "exports/import-001.csv",
    Body: body, ContentType: "text/csv" })),
  checkpoint: () => simulated("/i/checkpoint/after-commit", {}, "import-001:checkpoint", 175000),
  notify, duplicate: revision.duplicate_notification,
  expectDisconnect: process.env.POSTGRES_RECOVERY_FIXTURE === "1",
});

http.createServer(async (request, response) => {
  if (request.method !== "POST" || request.url !== "/import") {
    response.writeHead(200); response.end("ready"); return;
  }
  try {
    let body = "";
    for await (const part of request) {
      body += part;
      if (body.length > 1024) throw new Error("oversized import");
    }
    const status = await importOnce(JSON.parse(body).external_id);
    response.writeHead(status); response.end(status === 503 ? "retry fixture" : "complete");
  } catch {
    // Keep credentials and request bodies out of acceptance logs.
    response.writeHead(500); response.end("fixture failed");
  }
}).listen(Number(process.env.PORT || 8080), "0.0.0.0");
