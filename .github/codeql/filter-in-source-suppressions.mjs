import fs from "node:fs";

const sarifPath = process.argv[2];
if (!sarifPath) {
  throw new Error("usage: filter-in-source-suppressions.mjs <sarif-file>");
}

const sarif = JSON.parse(fs.readFileSync(sarifPath, "utf8"));
if (!Array.isArray(sarif.runs)) {
  throw new Error(`invalid SARIF document: ${sarifPath} has no runs array`);
}

let removed = 0;
for (const run of sarif.runs) {
  if (!Array.isArray(run.results)) continue;

  run.results = run.results.filter((result) => {
    const isSourceSuppressed = (result.suppressions ?? []).some(
      (suppression) => String(suppression.kind ?? "").toLowerCase() === "insource",
    );
    if (isSourceSuppressed) removed += 1;
    return !isSourceSuppressed;
  });
}

fs.writeFileSync(sarifPath, JSON.stringify(sarif));
console.log(`Removed ${removed} source-suppressed SARIF result(s) before upload.`);
