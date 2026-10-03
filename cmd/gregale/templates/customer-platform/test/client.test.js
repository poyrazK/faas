import test from "node:test";
import assert from "node:assert/strict";
import { mkdtemp, readFile, stat, chmod, symlink, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { randomUUID, createHash } from "node:crypto";
import { apiClient, onboard, prepareCredential, retryCredential } from "../tools/client.js";

test("onboarding requires tenant ingress and applies the stable external reference", async () => {
  const calls = [], appID = randomUUID();
  const api = { request: async (method, path, body) => {
    calls.push({ method, path, body });
    return method === "GET" ? { id: appID, platform_tenant_required: true, require_authn: false } : { tenant_id: randomUUID() };
  } };
  await onboard(api, "my-api", "billing-customer-42", "Customer 42");
  assert.deepEqual(calls[1].body, { external_ref: "billing-customer-42", name: "Customer 42",
    consumers: [{ app_id: appID, external_ref: "billing-customer-42", name: "Customer 42" }] });
  await assert.rejects(onboard({ request: async () => ({ platform_tenant_required: false }) }, "api", "c", "C"), /Configure/);
});

test("rotation saves a private key before sending only its hash; lost responses replay unchanged", async (t) => {
  const dir = await mkdtemp(join(tmpdir(), "gregale-customer-keys-"));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const path = join(dir, "rotation.json"), tenant = randomUUID(), consumer = randomUUID(), oldKey = randomUUID();
  let firstBody;
  const api = { base: "https://api.gregale.dev", request: async (method, route, body) => {
    const journal = JSON.parse(await readFile(path, "utf8"));
    assert.equal((await stat(path)).mode & 0o777, 0o600);
    assert.match(journal.plaintext, /^ck_[0-9a-f]{8}_[0-9a-f]{64}$/);
    assert.equal(body.keys[0].hash, createHash("sha256").update(journal.plaintext).digest("hex"));
    assert.equal(JSON.stringify(body).includes(journal.plaintext), false);
    assert.deepEqual(body.revoke_key_ids, [oldKey]);
    assert.equal(route, `/v1/account/platform-tenants/${tenant}/credentials/apply`);
    if (!firstBody) { firstBody = body; throw new Error("Lost response"); }
    assert.deepEqual(body, firstBody);
    return { keys: [{ id: "same-key", action: "unchanged" }] };
  } };
  await assert.rejects(prepareCredential(api, tenant, consumer, "v2", [oldKey], path), /Lost response/);
  assert.equal((await retryCredential(api, path)).keys[0].id, "same-key");
  await assert.rejects(prepareCredential(api, tenant, consumer, "v2", [oldKey], path), /EEXIST/);
  await assert.rejects(retryCredential({ ...api, base: "https://other.example" }, path), /mismatched/);
  await chmod(path, 0o644);
  await assert.rejects(retryCredential(api, path), /private file/);
  await chmod(path, 0o600);
  const link = join(dir, "link.json"); await symlink(path, link);
  await assert.rejects(retryCredential(api, link));
  const inside = new URL("../credential.json", import.meta.url).pathname;
  await assert.rejects(prepareCredential(api, tenant, consumer, "v3", [], inside), /outside/);
});

test("owner client blocks remote plaintext HTTP and redirects; errors omit secrets", async () => {
  assert.throws(() => apiClient("http://remote.example", "token"), /HTTPS/);
  assert.throws(() => apiClient("https://user:secret@api.example", "token"));
  let options;
  const api = apiClient("https://api.gregale.dev", "owner-secret", async (_url, opts) => {
    options = opts;
    return new Response(JSON.stringify({ code: "not_found", detail: "owner-secret plaintext" }), { status: 404 });
  });
  await assert.rejects(api.request("GET", "/v1/apps/missing"), { message: "Gregale returned HTTP 404 (not_found)" });
  assert.equal(options.redirect, "error");
});
