import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import {
  createApplicationToolkit,
  type ToolkitAdapter,
} from "../src/application-toolkit.js";

const fixture = JSON.parse(
  readFileSync(
    new URL(
      "../../../request-contracts/application-toolkit.json",
      import.meta.url,
    ),
    "utf8",
  ),
);
const uri = fixture.uri_prefix + "owned",
  uriTemplate = fixture.uri_prefix + "{id}";
const settings = { timeoutMs: 500 };
type Principal = { subject: string };
function access(role: string, subject = "alice") {
  return {
    principal: { subject },
    scopes: fixture.scope_sets[role] as string[],
  };
}
function setup() {
  const calls: { subject: string; name: string; options: typeof settings }[] =
    [];
  const descriptor = (name: string) => ({
    name,
    description: "Application operation",
    inputSchema: { type: "object" },
    outputSchema: { type: "object" },
    annotations: {
      readOnlyHint: false,
      destructiveHint: false,
      idempotentHint: true,
      openWorldHint: false,
    },
  });
  const template = {
    uriTemplate,
    name: "report",
    description: "Owned report",
    mimeType: "text/plain",
  };
  const adapter: ToolkitAdapter<Principal> = {
    tools: [...Object.keys(fixture.policies), "refund_execute_unpublished"].map(
      descriptor,
    ),
    resourceTemplates: [template],
    async call(name, _args, principal, options) {
      calls.push({ name, subject: principal.subject, options });
      return {
        isError: false,
        structuredContent: { subject: principal.subject },
        content: [
          {
            type: "resource_link",
            uri,
            name: "report",
            description: "",
            mimeType: "text/plain",
          },
        ],
      };
    },
    async readResource(uri, principal) {
      return {
        contents: [{ uri, mimeType: "text/plain", text: principal.subject }],
      };
    },
  };
  const toolScopes = structuredClone(fixture.policies),
    resourceScopes = { [uriTemplate]: [...fixture.resource_scopes] };
  return {
    calls,
    adapter,
    toolScopes,
    resourceScopes,
    toolkit: createApplicationToolkit([
      { adapter, toolScopes, resourceScopes },
    ]),
  };
}

test("shared scope matrix controls discovery and actual dispatch", async () => {
  const { toolkit, calls } = setup();
  for (const [role, expected] of Object.entries(fixture.authorized_tools)) {
    assert.deepEqual(
      toolkit.listTools(access(role)).map((t) => t.name),
      expected,
    );
  }
  for (const name of [
    "recover_start",
    "refund_prepare",
    "refund_execute_unpublished",
  ]) {
    await assert.rejects(toolkit.call(name, {}, access("support"), settings), {
      code: "tool_not_found",
    });
  }
  assert.equal(calls.length, 0);
  await Promise.all([
    toolkit.call("recover_start", {}, access("operator"), settings),
    toolkit.call("query_records", {}, access("support", "bob"), settings),
  ]);
  assert.deepEqual(
    calls.map((call) => call.subject),
    ["alice", "bob"],
  );
  assert.equal(calls[0]!.options, settings);
});

test("resource discovery, links and reads use the same exact scope and URI", async () => {
  const { toolkit } = setup();
  assert.equal(toolkit.listResourceTemplates(access("support")).length, 0);
  assert.equal(
    toolkit.listResourceTemplates(access("operator"))[0]!.uriTemplate,
    uriTemplate,
  );
  assert.equal(
    (await toolkit.call("query_records", {}, access("support"), settings))
      .content.length,
    0,
  );
  assert.equal(
    (await toolkit.call("query_records", {}, access("operator"), settings))
      .content.length,
    1,
  );
  await assert.rejects(toolkit.readResource(uri, access("support"), settings), {
    code: "resource_not_found",
  });
  for (const invalid of [
    uri + "?x=1",
    uri + "/other",
    uri.replace("owned", "%6fwned"),
    uri.replace("report", "other"),
  ]) {
    await assert.rejects(
      toolkit.readResource(invalid, access("operator"), settings),
      { code: "resource_not_found" },
    );
  }
  assert.equal(
    (await toolkit.readResource(uri, access("operator", "bob"), settings))
      .contents[0]!.text,
    "bob",
  );
});

test("registration snapshots mutable inputs and rejects unknown or conflicting selections", async () => {
  const { adapter, toolScopes, resourceScopes, toolkit } = setup();
  toolScopes.query_records.push("changed");
  resourceScopes[uriTemplate]!.push("changed");
  assert.deepEqual(toolkit.toolScopes.query_records, ["records:read"]);
  assert.deepEqual(toolkit.resourceScopes[uriTemplate], ["reports:read"]);
  for (const mounts of [
    [{ adapter, toolScopes: { missing: ["records:read"] } }],
    [{ adapter, toolScopes: { query_records: [] } }],
    [{ adapter, resourceScopes: { [uriTemplate]: [] } }],
    [
      { adapter, toolScopes },
      { adapter, toolScopes },
    ],
    [
      { adapter, resourceScopes },
      { adapter, resourceScopes },
    ],
  ])
    assert.throws(() => createApplicationToolkit<Principal>(mounts), TypeError);
});
