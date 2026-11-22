// Stored large tool results (ADR-20261030125501 §3 amendment 2026-11-22): a
// stub in the inbox is replaced by the full result, read with the app's
// workload token from this API only, and checked against its digest.
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { test } from "node:test";
import { resolveStoredResult } from "../agent.js";
import { GregalePlatform } from "../platform.js";

const api = "https://api.example.test";
const id = "0a1b2c3d-0000-4000-8000-00000000abcd";
const full = Buffer.from(JSON.stringify({ answer: "x".repeat(10_000) }));
const digest = createHash("sha256").update(full).digest("hex");
const stubContent = { truncated: true, preview: '{"answer":"xxx', result_url: `${api}/v1/agent-callbacks/results/${id}`, result_bytes: full.length, result_sha256: digest };

function platform(body = full) {
  const requests = [];
  const p = new GregalePlatform({ apiURL: api, identityURL: "https://identity.example.test",
    fetchImpl: async (url, init = {}) => {
      if (String(url).startsWith("https://identity.example.test")) return new Response(JSON.stringify({ access_token: "workload" }));
      requests.push({ url: String(url), auth: init.headers?.authorization });
      return new Response(body);
    } });
  return { p, requests };
}

test("a stub is replaced by the stored result, read with the workload token", async () => {
  const { p, requests } = platform();
  const message = { threadId: "t-1", messageId: "r-1", role: "tool", content: stubContent };
  const out = await resolveStoredResult(message, p);
  assert.deepEqual(out.content, { answer: "x".repeat(10_000) });
  assert.deepEqual(requests, [{ url: stubContent.result_url, auth: "Bearer workload" }]);
});

test("ordinary results and platforms without readResult are left alone", async () => {
  const message = { role: "tool", content: { answer: 42 } };
  assert.equal(await resolveStoredResult(message, platform().p), message);
  assert.equal(await resolveStoredResult({ role: "tool", content: stubContent }, {}).then((m) => m.content), stubContent);
});

test("the token goes only to this API's results route", async () => {
  for (const url of ["https://evil.example.test/v1/agent-callbacks/results/" + id, `${api}/v1/agents/runs/${id}`,
    `${api}/v1/agent-callbacks/results/${id}/../../x`, `${api}.evil.test/v1/agent-callbacks/results/${id}`]) {
    const { p, requests } = platform();
    await assert.rejects(resolveStoredResult({ content: { ...stubContent, result_url: url } }, p), /not a stored result/);
    assert.equal(requests.length, 0, url);
  }
});

test("a body that does not match its digest is refused", async () => {
  const { p } = platform(Buffer.from(JSON.stringify({ answer: "tampered" })));
  await assert.rejects(resolveStoredResult({ content: stubContent }, p), /size and digest/);
});
