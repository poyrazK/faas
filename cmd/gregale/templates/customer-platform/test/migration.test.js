import test from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { mkdtemp, symlink, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { migrate } from "../app/migrate.js";

test("migration never falls back to the serving credential or prints credentials", async () => {
  await assert.rejects(migrate(""), /MIGRATION_DATABASE_URL is required/);
  const secret = "postgres://runtime:secret-password@127.0.0.1/forbidden";
  const result = spawnSync(process.execPath, [fileURLToPath(new URL("../app/migrate.js", import.meta.url))], {
    encoding: "utf8", env: { ...process.env, DATABASE_URL: secret, MIGRATION_DATABASE_URL: "" },
  });
  assert.equal(result.status, 1);
  assert.match(result.stderr, /MIGRATION_DATABASE_URL/);
  assert.ok(!result.stderr.includes(secret) && !result.stderr.includes("secret-password"));
});

test("a symlink entrypoint runs the migration instead of silently succeeding", async (t) => {
  const dir = await mkdtemp(join(tmpdir(), "gregale-migrate-link-"));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const entry = join(dir, "migrate.js");
  await symlink(fileURLToPath(new URL("../app/migrate.js", import.meta.url)), entry);
  const result = spawnSync(process.execPath, [entry], { encoding: "utf8", env: { ...process.env, MIGRATION_DATABASE_URL: "" } });
  assert.equal(result.status, 1);
  assert.match(result.stderr, /MIGRATION_DATABASE_URL/);
});
