import { createHash } from "node:crypto";
import { writeFile } from "node:fs/promises";

// Select logical outcomes; per-run IDs, timestamps and credentials are omitted.
// This same observer is used for both implementations.
export async function observeExport({ status, created, storedReport, attempts }) {
  const bytes = Buffer.from(storedReport);
  const document = {
    version: 1,
    observations: [
      { key: "export.response", value: { status, created } },
      { key: "export.report", value: { sha256: createHash("sha256").update(bytes).digest("hex"), bytes: bytes.length } },
      { key: "notification.successful_deliveries", value: attempts.filter(({ status }) => status >= 200 && status < 300).length },
    ],
  };
  await writeFile(process.env.GREGALE_TEST_BEHAVIOR_OUTPUT, JSON.stringify(document), { mode: 0o600 });
}
