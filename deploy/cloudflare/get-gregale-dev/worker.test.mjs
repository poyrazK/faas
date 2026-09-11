import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import test from "node:test";

import { handleRequest } from "./worker.mjs";

// This endpoint's body is piped into `sh`, so the assertions that matter most
// are the negative ones: a digest mismatch, an unpinned ref, and an upstream
// error must each fail closed with no body.

const INSTALLER = "#!/bin/sh\necho install gregale\n";
const DIGEST = createHash("sha256").update(INSTALLER).digest("hex");
const PINNED = { INSTALLER_REF: "v1.2.3", INSTALLER_SHA256: DIGEST };

const request = (path = "/", method = "GET") =>
  new Request(`https://get.gregale.dev${path}`, { method });

const upstream = (body = INSTALLER, { ok = true, status = 200 } = {}) => {
  const calls = [];
  const impl = async (url, init) => {
    calls.push({ url: String(url), init });
    return {
      ok,
      status,
      arrayBuffer: async () => new TextEncoder().encode(body).buffer,
    };
  };
  impl.calls = calls;
  return impl;
};

test("serves the installer when the pinned digest matches", async () => {
  const response = await handleRequest(request(), PINNED, upstream());

  assert.equal(response.status, 200);
  assert.equal(await response.text(), INSTALLER);
  assert.match(response.headers.get("content-type"), /x-shellscript/);
  assert.equal(response.headers.get("x-installer-ref"), "v1.2.3");
  assert.equal(response.headers.get("cache-control"), "public, max-age=300");
});

test("fetches the pinned tag, never main", async () => {
  const fetchImpl = upstream();
  await handleRequest(request(), PINNED, fetchImpl);

  assert.equal(fetchImpl.calls.length, 1);
  assert.match(fetchImpl.calls[0].url, /\/v1\.2\.3\/scripts\/install\.sh$/);
  assert.doesNotMatch(fetchImpl.calls[0].url, /\/main\//);
});

test("a digest mismatch serves 503 with no body", async () => {
  const response = await handleRequest(
    request(),
    PINNED,
    upstream("#!/bin/sh\nrm -rf /\n"),
  );

  assert.equal(response.status, 503);
  assert.equal(await response.text(), "", "a mismatched script must never reach the client");
});

test("an unpinned ref or digest serves 503 and does not fetch", async () => {
  for (const env of [
    { INSTALLER_SHA256: DIGEST },
    { INSTALLER_REF: "v1.2.3" },
    { INSTALLER_REF: "REPLACE_ME_RUN_PIN_SH", INSTALLER_SHA256: DIGEST },
    { INSTALLER_REF: "v1.2.3", INSTALLER_SHA256: "REPLACE_ME_RUN_PIN_SH" },
  ]) {
    const fetchImpl = upstream();
    const response = await handleRequest(request(), env, fetchImpl);

    assert.equal(response.status, 503, `env ${JSON.stringify(env)} should fail closed`);
    assert.equal(await response.text(), "");
    assert.equal(fetchImpl.calls.length, 0, "must not fetch before the pin is validated");
  }
});

test("an upstream error serves 503 with no body", async () => {
  const response = await handleRequest(
    request(),
    PINNED,
    upstream(INSTALLER, { ok: false, status: 404 }),
  );

  assert.equal(response.status, 503);
  assert.equal(await response.text(), "");
  assert.equal(response.headers.get("retry-after"), "30");
});

test("every documented path serves the installer", async () => {
  for (const path of ["/", "/install.sh", "/install", "/sh"]) {
    const response = await handleRequest(request(path), PINNED, upstream());
    assert.equal(response.status, 200, `${path} should serve the installer`);
  }
});

test("an unknown path 404s with a hint and never leaks the script", async () => {
  const response = await handleRequest(request("/etc/passwd"), PINNED, upstream());

  assert.equal(response.status, 404);
  const body = await response.text();
  assert.doesNotMatch(body, /#!\/bin\/sh/);
  assert.match(body, /get\.gregale\.dev/);
});

test("non-GET methods are rejected", async () => {
  const response = await handleRequest(request("/", "POST"), PINNED, upstream());

  assert.equal(response.status, 405);
  assert.equal(response.headers.get("allow"), "GET, HEAD");
});

test("HEAD returns the headers with no body", async () => {
  const response = await handleRequest(request("/", "HEAD"), PINNED, upstream());

  assert.equal(response.status, 200);
  assert.equal(await response.text(), "");
  assert.equal(
    response.headers.get("content-length"),
    String(new TextEncoder().encode(INSTALLER).length),
  );
});
