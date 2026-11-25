import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { JobContract } from "../src/job-runtime.js";
import {
  defineQueryTool,
  defineQueryResource,
  type QueryToolDefinition,
} from "../src/query-tools.js";
import { createQueryToolMCP } from "../src/query-tool-mcp.js";
const F = JSON.parse(
  readFileSync(
    new URL("../../../request-contracts/query-tools.json", import.meta.url),
    "utf8",
  ),
);
type Input = { search: string };
type Row = { id: string; value: string };
const settings = { timeoutMs: 1000 };
const pause = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));
function fixture(
  overrides: Partial<QueryToolDefinition<Input, Row, string>> = {},
) {
  const state = {
    revoked: false,
    calls: 0,
    auth: 0,
    reads: 0,
    resourceOwner: "alice",
    changeOwner: false,
    hook: undefined as undefined | (() => void),
  };
  const definition: QueryToolDefinition<Input, Row, string> = {
    name: "csv_rows",
    description: "Search imported rows.",
    revision: "v1",
    scope: F.scope,
    cursorKey: F.key,
    async authorize(_input, access) {
      state.auth++;
      return state.revoked ? null : { subject_id: access, owner_id: access };
    },
    async load(input, context) {
      state.calls++;
      state.hook?.();
      const rows: Row[] =
        context.actor.owner_id === "alice"
          ? F.rows
          : [{ id: "bob", value: "café bob" }];
      const filtered = rows.filter((r) => r.value.includes(input.search)),
        start =
          context.after === null
            ? 0
            : filtered.findIndex((r) => r.id === context.after) + 1,
        items = filtered.slice(start, start + context.limit);
      return {
        items,
        after: start + items.length < filtered.length ? items.at(-1)!.id : null,
      };
    },
    ...overrides,
  };
  const tool = defineQueryTool(
      new JobContract<Input, Row>(F.contract),
      definition,
    ),
    resource = defineQueryResource<string>({
      name: "csv_report",
      description: "Read an authorized CSV report.",
      revision: "v1",
      scope: F.scope,
      mimeType: "text/csv",
      async authorize(id, access) {
        return !state.revoked &&
          id === "report_1" &&
          access === state.resourceOwner
          ? { subject_id: access, owner_id: access }
          : null;
      },
      async read() {
        state.reads++;
        if (state.changeOwner) state.resourceOwner = "bob";
        return "report body";
      },
    });
  return { state, tool, resource, definition };
}
test("shared contracts, signed cursor, descriptors and resource identity match Python", async () => {
  const { tool, resource } = fixture(),
    mcp = createQueryToolMCP(tool, { resources: [resource] });
  assert.deepEqual(
    await tool.page(F.input, "alice", { ...settings, pageSize: 1 }),
    F.first_page,
  );
  assert.deepEqual(mcp.tools, F.tools);
  assert.deepEqual(mcp.resourceTemplates, F.resource_templates);
  assert.deepEqual(
    await resource.link("report_1", "alice", settings),
    F.resource_link,
  );
  const next = await tool.page(F.input, "alice", {
    ...settings,
    pageSize: 1,
    after: F.first_page.next_cursor,
  });
  assert.equal(next.items[0]!.id, "9007199254740995");
});
test("concurrent users and queries keep independent authorization and cursors", async () => {
  const { tool, state } = fixture();
  const pages = await Promise.all(
    Array.from({ length: 12 }, (_, i) =>
      tool.page(F.input, i % 2 ? "bob" : "alice", { ...settings, pageSize: 1 }),
    ),
  );
  assert.ok(
    pages.every((p, i) => p.items[0]!.id === (i % 2 ? "bob" : F.rows[0].id)),
  );
  assert.equal(state.calls, 12);
  await assert.rejects(
    tool.page(F.input, "bob", { ...settings, after: F.first_page.next_cursor }),
    { code: "invalid_cursor" },
  );
});
test("cursor rejects tampering and changes to input, revision, scope and signing key", async () => {
  const { tool, definition } = fixture();
  for (const after of [
    "",
    F.first_page.next_cursor + "x",
    F.first_page.next_cursor.replace(/^./, "A"),
  ])
    await assert.rejects(tool.page(F.input, "alice", { ...settings, after }), {
      code: "invalid_cursor",
    });
  await assert.rejects(
    tool.page({ search: "different" }, "alice", {
      ...settings,
      after: F.first_page.next_cursor,
    }),
    { code: "invalid_cursor" },
  );
  for (const change of [
    { revision: "v2" },
    { cursorKey: "x".repeat(32) },
    { scope: { ...F.scope, app_slug: "other" } },
  ]) {
    const other = defineQueryTool(new JobContract<Input, Row>(F.contract), {
      ...definition,
      ...change,
    });
    await assert.rejects(
      other.page(F.input, "alice", {
        ...settings,
        after: F.first_page.next_cursor,
      }),
      { code: "invalid_cursor" },
    );
  }
});
test("revocation during a query and between pages never returns retained rows", async () => {
  const { tool, state } = fixture();
  state.hook = () => {
    state.revoked = true;
  };
  await assert.rejects(tool.page(F.input, "alice", settings), {
    code: "unauthorized",
  });
  state.revoked = false;
  state.hook = undefined;
  const iter = tool
    .pages(F.input, "alice", { ...settings, pageSize: 1 })
    [Symbol.asyncIterator]();
  await iter.next();
  state.revoked = true;
  await assert.rejects(iter.next(), { code: "unauthorized" });
  assert.equal(state.calls, 2);
});
for (const [i, page] of F.invalid_pages.entries())
  test("shared invalid provider page " + i, async () => {
    const { tool } = fixture({
      async load() {
        return page;
      },
    });
    await assert.rejects(
      tool.page(F.input, "alice", { ...settings, pageSize: 1 }),
      { code: "invalid_output" },
    );
  });
test("lazy iteration honors total item/page budgets and retains a resumable cursor", async () => {
  const { tool, state } = fixture(),
    stream = tool.pages(F.input, "alice", {
      ...settings,
      pageSize: 1,
      maxPages: 1,
    });
  assert.equal(state.calls, 0);
  const pages = [];
  for await (const p of stream) pages.push(p);
  assert.equal(state.calls, 1);
  assert.equal(pages.length, 1);
  assert.ok(pages[0]!.next_cursor);
  const items = [];
  for await (const item of tool.items(F.input, "alice", {
    ...settings,
    pageSize: 2,
    maxItems: 2,
  }))
    items.push(item);
  assert.equal(items.length, 2);
  assert.equal(state.calls, 2);
});
test("deadline includes time paused between pulls and cancellation stops buffered items", async () => {
  const { tool, state } = fixture(),
    iter = tool
      .pages(F.input, "alice", { timeoutMs: 15, pageSize: 1 })
      [Symbol.asyncIterator]();
  await iter.next();
  await pause(25);
  await assert.rejects(iter.next(), { code: "deadline" });
  assert.equal(state.calls, 1);
  const controller = new AbortController(),
    items = tool
      .items(F.input, "alice", { ...settings, signal: controller.signal })
      [Symbol.asyncIterator]();
  await items.next();
  controller.abort();
  await assert.rejects(items.next(), { code: "cancelled" });
});
test("deadlines and aborts fence late callbacks without exposing private errors", async () => {
  let release!: (value: { items: Row[]; after: null }) => void;
  const { tool, state } = fixture({
    load() {
      return new Promise((resolve) => {
        release = resolve;
      });
    },
  });
  await assert.rejects(tool.page(F.input, "alice", { timeoutMs: 15 }), {
    code: "deadline",
  });
  release({ items: F.rows, after: null });
  await pause(0);
  assert.equal(state.auth, 1);
  const controller = new AbortController();
  const pending = tool.page(F.input, "alice", {
    ...settings,
    signal: controller.signal,
  });
  await pause(0);
  controller.abort();
  await assert.rejects(pending, { code: "cancelled" });
  release({ items: F.rows, after: null });
  const failed = fixture({
    async load() {
      throw Error("private SQL and credentials");
    },
  }).tool;
  await assert.rejects(
    failed.page(F.input, "alice", settings),
    (e: any) => e.code === "query_failed" && !String(e).includes("private"),
  );
});
test("bounds reject invalid arguments and over-budget results", async () => {
  const { tool } = fixture({ maxBytes: 1 });
  await assert.rejects(tool.page(F.input, "alice", settings), {
    code: "limit",
  });
  for (const pageSize of [0, 101, 1.5])
    await assert.rejects(
      tool.page(F.input, "alice", { ...settings, pageSize }),
      { code: "invalid_input" },
    );
  const repeated = fixture({
    async load() {
      return { items: F.rows.slice(0, 1), after: F.rows[0].id };
    },
  }).tool;
  await assert.rejects(
    repeated.page(F.input, "alice", {
      ...settings,
      after: F.first_page.next_cursor,
    }),
    { code: "invalid_output" },
  );
});
test("resource links require current access; guessed URIs never become fetch URLs", async () => {
  const { resource, state } = fixture();
  await assert.rejects(resource.link("report_1", "bob", settings), {
    code: "unauthorized",
  });
  const link = await resource.link("report_1", "alice", settings);
  assert.equal(
    (await resource.read(link.uri, "alice", settings)).text,
    "report body",
  );
  for (const uri of [
    "https://evil.test/",
    link.uri + "?token=1",
    link.uri + "/../x",
    link.uri.replace("csv_report", "other"),
  ])
    await assert.rejects(resource.read(uri, "alice", settings), {
      code: "not_found",
    });
  assert.equal(state.reads, 1);
  state.changeOwner = true;
  await assert.rejects(resource.read(link.uri, "alice", settings), {
    code: "unauthorized",
  });
  await assert.rejects(resource.read(link.uri, "alice", settings), {
    code: "unauthorized",
  });
});
test("MCP returns typed pages and authorized resource links with strict arguments", async () => {
  const { tool, resource, state } = fixture(),
    mcp = createQueryToolMCP(tool, {
      resources: [resource],
      links: () => [{ resource: resource.name, id: "report_1" }],
    });
  const result = await mcp.call(
    tool.name,
    { input: F.input, page_size: 1 },
    "alice",
    settings,
  );
  assert.deepEqual(result.structuredContent, F.first_page);
  assert.deepEqual(result.content[1], F.resource_link);
  assert.equal(
    result.content.filter((c) => c.type === "resource_link").length,
    1,
  );
  assert.equal(
    (await mcp.readResource(F.resource_link.uri, "alice", settings))
      .contents[0]!.text,
    "report body",
  );
  await assert.rejects(mcp.readResource(F.resource_link.uri, "bob", settings), {
    code: -32002,
  });
  await assert.rejects(mcp.call("unknown", {}, "alice", settings), {
    code: -32601,
  });
  for (const args of [
    { input: F.input, owner_id: "alice" },
    { input: { search: 1 } },
    { input: F.input, page_size: true },
    { input: F.input, after: null },
  ])
    await assert.rejects(mcp.call(tool.name, args, "alice", settings), {
      code: -32602,
    });
  state.revoked = true;
  assert.deepEqual(
    (await mcp.call(tool.name, { input: F.input }, "alice", settings)).content,
    [{ type: "text", text: '{"code":"unauthorized"}' }],
  );
  state.revoked = false;
  assert.equal(
    (
      await createQueryToolMCP(tool, { protocolVersion: "2026-07-28" }).call(
        tool.name,
        { input: F.input },
        "alice",
        settings,
      )
    ).resultType,
    "complete",
  );
});

test("complete MCP frames and resource text have independent byte bounds", async () => {
  const f = fixture();
  const huge = defineQueryTool(
    new JobContract<string, string>({
      version: 1,
      name: "query-large",
      input: { type: "string" },
      output: { type: "string", maxLength: 1000000 },
    }),
    {
      ...f.definition,
      maxBytes: 1048576,
      async authorize() {
        return { subject_id: "alice", owner_id: "alice" };
      },
      async load() {
        return { items: ["x".repeat(600000)], after: null };
      },
    },
  );
  assert.equal((await huge.page("input", "alice", settings)).items.length, 1);
  const result = await createQueryToolMCP(huge).call(
    huge.name,
    { input: "input" },
    "alice",
    settings,
  );
  assert.equal(result.isError, true);
  assert.deepEqual(result.content, [
    { type: "text", text: '{"code":"limit"}' },
  ]);
  const base = {
    name: "large_report",
    description: "Read a report.",
    revision: "v1",
    scope: F.scope,
    mimeType: "text/plain",
    async authorize() {
      return { subject_id: "alice", owner_id: "alice" };
    },
    async read() {
      return "x".repeat(100);
    },
  };
  const small = defineQueryResource<string>({ ...base, maxBytes: 1 });
  const link = await small.link("one", "alice", settings);
  await assert.rejects(small.read(link.uri, "alice", settings), {
    code: "limit",
  });
  const escaped = defineQueryResource<string>({
    ...base,
    maxBytes: 1048576,
    async read() {
      return String.fromCharCode(34).repeat(600000);
    },
  });
  const escapedLink = await escaped.link("one", "alice", settings);
  assert.equal(
    (await escaped.read(escapedLink.uri, "alice", settings)).text.length,
    600000,
  );
  await assert.rejects(
    createQueryToolMCP(f.tool, { resources: [escaped] }).readResource(
      escapedLink.uri,
      "alice",
      settings,
    ),
    { code: -32603 },
  );
});
