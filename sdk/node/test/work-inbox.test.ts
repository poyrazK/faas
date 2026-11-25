import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import {
  PipelineToolError,
  pipelineToolIdentity,
} from "../src/pipeline-tools.js";
import { createQueryToolMCP } from "../src/query-tool-mcp.js";
import {
  defineWorkInbox,
  workInboxPosition,
  type WorkInboxCandidate,
  type WorkInboxDefinition,
  type WorkInboxSelector,
  type WorkInboxSource,
} from "../src/work-inbox.js";

const fixture = JSON.parse(
  readFileSync(
    new URL(
      "../../../request-contracts/work-inbox-cases.json",
      import.meta.url,
    ),
    "utf8",
  ),
);
const rows: WorkInboxCandidate[] = fixture.candidates;
const selectors: WorkInboxSelector[] = fixture.selectors;
const settings = { timeoutMs: 1000 };

function setup(overrides: Partial<WorkInboxDefinition<string>> = {}) {
  const visited: string[] = [];
  const state = { canRead: true, blocked: new Set([rows[0]!.identity]) };
  const sources = selectors.map(
    (selector): WorkInboxSource<string> => ({
      ...selector,
      async inspect(row, actor, _scope, access) {
        assert.equal(access, actor.subject_id);
        visited.push(row.identity);
        if (state.blocked.has(row.identity))
          throw new PipelineToolError("unauthorized");
        return {
          kind: row.kind,
          name: row.name,
          state: row.kind === "action" ? "unknown" : "running",
          next_step: row.kind === "action" ? "reconcile_action" : "inspect",
          request_id: row.request_id,
        };
      },
    }),
  );
  const definition: WorkInboxDefinition<string> = {
    scope: fixture.scope,
    cursorKey: fixture.key,
    scanLimit: 1,
    sources,
    store: {
      async scan(request) {
        return request.actor.owner_id === "alice"
          ? rows
              .filter(
                (row) =>
                  row.position > (request.after ?? "") &&
                  request.selectors.some(
                    (s) => s.kind === row.kind && s.name === row.name,
                  ),
              )
              .slice(0, request.limit)
          : [];
      },
    },
    async authorize(_input, access) {
      return state.canRead ? { subject_id: access, owner_id: access } : null;
    },
    async businessReference() {
      return "import-1";
    },
    ...overrides,
  };
  return { definition, inbox: defineWorkInbox(definition), state, visited };
}

test("bounded sparse pages share the Python cursor and never expose the denied receipt", async () => {
  const { inbox, visited } = setup();
  const first = await inbox.page({}, "alice", settings);
  assert.deepEqual(first, fixture.first_page);
  assert.equal(visited.length, 1);
  const cursor = first.next_cursor!;
  const [payload] = cursor.split(".");
  const decoded = JSON.parse(Buffer.from(payload!, "base64url").toString());
  assert.equal(decoded.a, rows[0]!.position);
  assert.ok(!JSON.stringify(decoded).includes(rows[0]!.identity));
  const second = await inbox.page({}, "alice", { ...settings, after: cursor });
  assert.equal(second.items[0]!.request_id, rows[1]!.request_id);
  assert.ok(second.next_cursor);
  const third = await inbox.page({}, "alice", {
    ...settings,
    after: second.next_cursor!,
  });
  assert.equal(third.items[0]!.request_id, rows[2]!.request_id);
  assert.equal(third.next_cursor, null);
});

test("inbox discovery and cursor continuation are authorized per user and filter", async () => {
  const { inbox, state, visited } = setup();
  const [alice, bob] = await Promise.all([
    inbox.page({}, "alice", settings),
    inbox.page({}, "bob", settings),
  ]);
  assert.deepEqual(bob, { items: [], next_cursor: null });
  assert.equal(visited.length, 1);
  await assert.rejects(
    inbox.page({}, "bob", { ...settings, after: alice.next_cursor! }),
    { code: "invalid_cursor" },
  );
  await assert.rejects(
    inbox.page({ kind: "action" }, "alice", {
      ...settings,
      after: alice.next_cursor!,
    }),
    { code: "invalid_cursor" },
  );
  state.canRead = false;
  await assert.rejects(
    inbox.page({}, "alice", { ...settings, after: alice.next_cursor! }),
    { code: "unauthorized" },
  );
});

test("filters apply to live observations and the returned adapter is MCP read-only", async () => {
  const { definition } = setup();
  const inbox = defineWorkInbox({ ...definition, scanLimit: 10 });
  const mcp = createQueryToolMCP(inbox);
  assert.equal(mcp.tools[0]!.annotations.readOnlyHint, true);
  const page = await mcp.call(
    "work_inbox",
    { input: { business_ref: "import-1", state: "running", kind: "pipeline" } },
    "alice",
    settings,
  );
  assert.equal(page.isError, false);
  const items = page.structuredContent!.items as {
    business_ref: string;
    request_id: string;
  }[];
  assert.ok(
    items.length > 0 &&
      items.every(
        (item) =>
          item.business_ref === "import-1" &&
          item.request_id !== rows[0]!.request_id,
      ),
  );
  assert.deepEqual(
    (await inbox.page({ business_ref: "absent" }, "alice", settings)).items,
    [],
  );
});

test("a corrupt candidate never reaches an inspector", async () => {
  const { inbox, visited } = setup({
    store: {
      async scan() {
        return [{ ...rows[0]!, request_id: rows[1]!.request_id }];
      },
    },
  });
  await assert.rejects(inbox.page({}, "alice", settings), {
    code: "invalid_output",
  });
  assert.deepEqual(visited, []);
});

test("one deadline and cancellation bound the entire scan", async () => {
  let release!: () => void;
  const { inbox } = setup({
    store: {
      scan: () =>
        new Promise((resolve) => {
          release = () => resolve(rows.slice(0, 1));
        }),
    },
  });
  await assert.rejects(inbox.page({}, "alice", { timeoutMs: 20 }), {
    code: "deadline",
  });
  release();
  const abort = new AbortController();
  const pending = inbox.page({}, "alice", {
    ...settings,
    signal: abort.signal,
  });
  await new Promise((resolve) => setTimeout(resolve, 0));
  abort.abort();
  await assert.rejects(pending, { code: "cancelled" });
  release();
});

test("fixed backend receipt subjects leave the current caller and owner intact", async () => {
  const { definition } = setup();
  const source = definition.sources.find((s) => s.kind === "pipeline")!;
  const original = rows.find((r) => r.kind === "pipeline")!;
  const subject = "cleanup_namespace";
  const identity = pipelineToolIdentity(
    definition.scope,
    source.name,
    subject,
    original.request_id,
  );
  const row = {
    ...original,
    identity,
    position: workInboxPosition(source.kind, identity),
  };
  const inbox = defineWorkInbox({
    ...definition,
    sources: [{ ...source, receipt_subject_id: subject }],
    store: {
      async scan(request) {
        assert.equal(request.selectors[0]!.receipt_subject_id, subject);
        return request.actor.owner_id === "alice" ? [row] : [];
      },
    },
  });
  assert.equal(
    (await inbox.page({}, "alice", settings)).items[0]!.request_id,
    original.request_id,
  );
  assert.deepEqual((await inbox.page({}, "bob", settings)).items, []);
});
