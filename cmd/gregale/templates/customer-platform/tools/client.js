import { randomBytes, createHash } from "node:crypto";
import { constants } from "node:fs";
import { open, realpath } from "node:fs/promises";
import { dirname, resolve, relative, isAbsolute } from "node:path";
import { fileURLToPath } from "node:url";

const projectRoot = fileURLToPath(new URL("../", import.meta.url));
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const digest = (key) => createHash("sha256").update(key).digest("hex");

export function id(value) {
  if (!uuid.test(value || "")) throw new Error("Expected a UUID");
  return value;
}

export function apiClient(apiBase, token, fetchAPI = fetch) {
  const base = new URL(apiBase || "https://api.gregale.dev");
  if (base.username || base.password || base.search || base.hash || base.pathname !== "/" ||
      (base.protocol !== "https:" && !(base.protocol === "http:" &&
        ["127.0.0.1", "localhost", "[::1]"].includes(base.hostname)))) {
    throw new Error("FAAS_API must be an HTTPS origin (HTTP is allowed only on loopback)");
  }
  if (!token) throw new Error("Set FAAS_TOKEN on the operator machine");
  return {
    base: base.origin,
    async request(method, path, body) {
      let response;
      try {
        response = await fetchAPI(new URL(path, base), { method,
          headers: { authorization: `Bearer ${token}`, "content-type": "application/json" },
          body: body === undefined ? undefined : JSON.stringify(body),
          redirect: "error", signal: AbortSignal.timeout(10000) });
      } catch { throw new Error("Gregale request failed; retry the same operation"); }
      let data;
      try { data = await response.json(); } catch { throw new Error(`Gregale returned HTTP ${response.status}`); }
      if (!response.ok) {
        const code = /^[a-z0-9_]+$/.test(data.code || "") ? ` (${data.code})` : "";
        throw new Error(`Gregale returned HTTP ${response.status}${code}`);
      }
      return data;
    },
  };
}

export async function onboard(api, slug, externalRef, name) {
  if (!slug || !externalRef || !name) throw new Error("Supply app slug, external reference, and customer name");
  const app = await api.request("GET", `/v1/apps/${encodeURIComponent(slug)}`);
  if (!app.platform_tenant_required || app.require_authn ||
      (app.public_auth?.mode && app.public_auth.mode !== "open")) {
    throw new Error("Configure the app with --platform-tenant-required --no-require-authn before onboarding");
  }
  return api.request("POST", "/v1/account/platform-tenants/apply", {
    external_ref: externalRef, name,
    consumers: [{ app_id: id(app.id), external_ref: externalRef, name }],
  });
}

// Resolve the parent, so symlinked directories cannot put owner credentials
// inside the source bundle. The journal itself is opened without symlinks.
async function journalPath(path) {
  if (!path) throw new Error("Supply a private credential journal path outside the source directory");
  const target = resolve(await realpath(dirname(resolve(path))), resolve(path).split(/[/\\]/).at(-1));
  const rel = relative(await realpath(projectRoot), target);
  if (!rel || (!rel.startsWith(".." + (process.platform === "win32" ? "\\" : "/")) && !isAbsolute(rel))) {
    throw new Error("Credential journals must be outside the source directory");
  }
  return target;
}

export async function prepareCredential(api, tenantID, consumerID, name, revokeKeyIDs, path) {
  id(tenantID); id(consumerID); revokeKeyIDs.forEach(id);
  if (!name || name.length > 120) throw new Error("Supply a key name of 1–120 characters");
  const prefix = randomBytes(4).toString("hex");
  const plaintext = `ck_${prefix}_${randomBytes(32).toString("hex")}`;
  const journal = { version: 1, api: api.base, tenant_id: tenantID, plaintext,
    intent: { consumer_id: consumerID, name, prefix, hash: digest(plaintext), scopes: ["write"] },
    revoke_key_ids: revokeKeyIDs };
  const target = await journalPath(path);
  const file = await open(target, constants.O_WRONLY | constants.O_CREAT | constants.O_EXCL | constants.O_NOFOLLOW, 0o600);
  try { await file.writeFile(JSON.stringify(journal, null, 2) + "\n"); await file.sync(); }
  finally { await file.close(); }
  const parent = await open(dirname(target), constants.O_RDONLY);
  try { await parent.sync(); } finally { await parent.close(); }
  // The plaintext is durable before the first API call. A failed call leaves
  // the journal in place; retry never generates a different key.
  return retryCredential(api, target);
}

export async function retryCredential(api, path) {
  const target = await journalPath(path);
  const file = await open(target, constants.O_RDONLY | constants.O_NOFOLLOW);
  let journal;
  try {
    const stat = await file.stat();
    if (!stat.isFile() || (stat.mode & 0o077) !== 0 || stat.size > 16384) {
      throw new Error("Credential journal must be a private file (chmod 600), at most 16 KiB");
    }
    journal = JSON.parse(await file.readFile("utf8"));
  } finally { await file.close(); }
  const intent = journal.intent;
  if (journal.version !== 1 || journal.api !== api.base ||
      !/^ck_[0-9a-f]{8}_[0-9a-f]{64}$/.test(journal.plaintext || "") ||
      intent?.prefix !== journal.plaintext.slice(3, 11) || intent?.hash !== digest(journal.plaintext) ||
      !intent.name || JSON.stringify(intent.scopes) !== '["write"]' ||
      !Array.isArray(journal.revoke_key_ids) || journal.revoke_key_ids.length > 100) {
    throw new Error("Invalid credential journal or mismatched FAAS_API");
  }
  id(journal.tenant_id); id(intent.consumer_id); journal.revoke_key_ids.forEach(id);
  // Build an allowlisted body: never serialize the journal or its plaintext.
  return api.request("POST", `/v1/account/platform-tenants/${journal.tenant_id}/credentials/apply`, {
    keys: [{ consumer_id: intent.consumer_id, name: intent.name, prefix: intent.prefix,
      hash: intent.hash, scopes: intent.scopes }], revoke_key_ids: journal.revoke_key_ids,
  });
}
