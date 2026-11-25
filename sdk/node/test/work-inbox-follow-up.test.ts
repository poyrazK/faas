import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import {
  createApplicationToolkit,
  type ToolkitAdapter,
} from "../src/application-toolkit.js";
import type { PipelineToolStartMatch } from "../src/pipeline-tools.js";
import { pipelineToolWire } from "../src/pipeline-tool-protocol.js";
import { defineWorkInbox, type WorkInboxEntry } from "../src/work-inbox.js";

const cases = JSON.parse(
  readFileSync(
    new URL(
      "../../../request-contracts/work-inbox-follow-up-cases.json",
      import.meta.url,
    ),
    "utf8",
  ),
);
const options = { timeoutMs: 1000 };
const access = {
  principal: "alice",
  scopes: ["inbox:read", "status:read", "refund:read", "refund:reconcile"],
};
const read = {
  ...access,
  scopes: access.scopes.filter((scope) => scope !== "refund:reconcile"),
};
const followUp = "work_inbox_follow_up";

function fixture() {
  let owner = "alice",
    reads = 0,
    lookups = 0;
  let match: PipelineToolStartMatch = {
    state: "start_unknown",
    request_id: cases.review_ref.request_id,
  };
  const current: { review: WorkInboxEntry; refund: WorkInboxEntry } = {
    review: structuredClone(cases.review_entry),
    refund: structuredClone(cases.refund_entry),
  };
  const inbox = defineWorkInbox<string>({
    scope: cases.scope,
    cursorKey: "fixture-key-32-bytes-long-fixture-key",
    store: {
      async scan() {
        return assert.fail("follow-up must not page through receipts");
      },
      async hasReceipt(ref) {
        reads++;
        const { actor, selector, request_id, identity } = ref;
        return (
          actor.owner_id === "alice" &&
          (selector.kind === "pipeline"
            ? selector.receipt_subject_id === "csv_cleanup" &&
              request_id === current.review.request_id
            : request_id === current.refund.request_id &&
              identity === cases.refund_entry.action_id)
        );
      },
    },
    async authorize(_filters, principal) {
      return owner
        ? {
            subject_id: principal,
            owner_id: principal === "alice" ? owner : principal,
          }
        : null;
    },
    async businessReference() {
      return "import-1";
    },
    sources: [
      {
        kind: "pipeline",
        name: "recover_csv_cleanup",
        receipt_subject_id: "csv_cleanup",
        contract_sha256: "a".repeat(64),
        async inspect() {
          return current.review;
        },
        async findAcceptedRun(requestId, principal) {
          assert.equal(principal, "alice");
          assert.equal(requestId, cases.review_ref.request_id);
          lookups++;
          return match;
        },
      },
      {
        kind: "action",
        name: "csv_refund",
        entryName: "action_csv_refund",
        async inspect() {
          return current.refund;
        },
      },
    ],
  });
  const bindings = [
    {
      name: "recover_csv_cleanup_status",
      key: "request_id",
      scope: "status:read",
    },
    {
      name: "action_csv_refund_observe",
      key: "action_id",
      scope: "refund:read",
    },
    {
      name: "action_csv_refund_refresh",
      key: "action_id",
      scope: "refund:reconcile",
    },
  ];
  const reconcile = "recover_csv_cleanup_reconcile";
  const requestSchema = pipelineToolWire.request_id_schema;
  const adapter: ToolkitAdapter<string> = {
    tools: [
      ...bindings.map(({ name, key }) => ({
        name,
        description: name,
        inputSchema: {
          type: "object",
          additionalProperties: false,
          required: [key],
          properties: {
            [key]:
              key === "request_id"
                ? pipelineToolWire.request_id_schema
                : { type: "string", minLength: 64, maxLength: 64 },
          },
        },
        outputSchema: { type: "object" },
        annotations: {
          readOnlyHint: true,
          destructiveHint: false,
          idempotentHint: true,
          openWorldHint: true,
        },
      })),
      {
        name: reconcile,
        description: reconcile,
        inputSchema: {
          type: "object",
          properties: { request_id: requestSchema, run_id: requestSchema },
          required: ["request_id", "run_id"],
          additionalProperties: false,
        },
        outputSchema: { type: "object" },
        annotations: {
          readOnlyHint: false,
          destructiveHint: false,
          idempotentHint: true,
          openWorldHint: true,
        },
      },
    ],
    async call() {
      return assert.fail("planning invoked a tool");
    },
  };
  const toolkit = createApplicationToolkit(
    [
      {
        adapter,
        toolScopes: {
          ...Object.fromEntries(
            bindings.map(({ name, scope }) => [name, [scope]]),
          ),
          [reconcile]: ["cleanup:recover"],
        },
      },
    ],
    { workInboxFollowUp: { inbox, scopes: ["inbox:read"] } },
  );
  return {
    toolkit,
    current,
    reads: () => reads,
    lookups: () => lookups,
    matchStart: () => {
      match = { state: "found", ...cases.start_reconcile.arguments };
      current.review = {
        ...current.review,
        state: "start_unknown",
        next_step: "reconcile_start",
      };
    },
    revoke: () => {
      owner = "";
    },
  };
}

test("resolves shared review and refund plans from current state without calling tools", async () => {
  const { toolkit, current } = fixture();
  assert.deepEqual(
    await toolkit.resolveWorkInboxFollowUp(current.review, access, options),
    cases.review,
  );
  const plan = await toolkit.call(followUp, cases.action_ref, access, options);
  assert.equal(plan.isError, false);
  assert.deepEqual(plan.structuredContent, cases.refresh);
  const stale = current.refund;
  current.refund = {
    ...current.refund,
    state: "succeeded",
    next_step: "read_result",
  };
  assert.deepEqual(
    await toolkit.resolveWorkInboxFollowUp(stale, read, options),
    cases.observe,
  );
  current.review = {
    ...current.review,
    state: "start_unknown",
    next_step: "reconcile_start",
  };
  assert.deepEqual(
    await toolkit.resolveWorkInboxFollowUp(cases.review_ref, access, options),
    cases.no_action,
  );
});

test("withholds follow-ups for other viewers, wrong business records and missing scopes", async () => {
  const { toolkit, reads, revoke } = fixture();
  assert.ok(
    !toolkit
      .listTools({ ...access, scopes: [] })
      .some((tool) => tool.name === followUp),
  );
  await assert.rejects(
    toolkit.resolveWorkInboxFollowUp(
      cases.action_ref,
      { ...access, scopes: [] },
      options,
    ),
    { code: "tool_not_found" },
  );
  assert.equal(reads(), 0);
  for (const [ref, principal] of [
    [cases.action_ref, read],
    [cases.action_ref, { ...access, principal: "bob" }],
    [{ ...cases.action_ref, business_ref: "another-import" }, access],
  ]) {
    const result = await toolkit.call(followUp, ref, principal, options);
    assert.equal(result.isError, true);
    assert.equal(result.structuredContent, undefined);
    assert.deepEqual(
      JSON.parse(
        result.content[0]!.type === "text" ? result.content[0]!.text : "{}",
      ),
      { code: "not_found" },
    );
  }
  revoke();
  await assert.rejects(
    toolkit.resolveWorkInboxFollowUp(cases.review_ref, access, options),
    { code: "not_found" },
  );
});

test("validates MCP references and refuses unsafe review links", async () => {
  const { toolkit, current, reads } = fixture();
  await assert.rejects(
    toolkit.call(
      followUp,
      { ...cases.review_ref, state: "succeeded" },
      access,
      options,
    ),
    { code: -32602 },
  );
  assert.equal(reads(), 0);
  current.review = {
    ...current.review,
    review: {
      ...current.review.review!,
      review_url: current.review.review!.review_url + "&token=secret",
    },
  };
  await assert.rejects(
    toolkit.resolveWorkInboxFollowUp(cases.review_ref, access, options),
    { code: "invalid_output" },
  );
});

test("plans verified start reconciliation only with the destination scope", async () => {
  const { toolkit, matchStart, lookups, current } = fixture();
  const operator = { ...access, scopes: [...access.scopes, "cleanup:recover"] };
  current.review = {
    ...current.review,
    state: "start_unknown",
    next_step: "reconcile_start",
  };
  assert.deepEqual(
    await toolkit.resolveWorkInboxFollowUp(cases.review_ref, operator, options),
    cases.no_action,
  );
  matchStart();
  assert.deepEqual(
    await toolkit.resolveWorkInboxFollowUp(cases.review_ref, access, options),
    cases.no_action,
  );
  assert.equal(lookups(), 1);
  const result = await toolkit.call(
    followUp,
    cases.review_ref,
    operator,
    options,
  );
  assert.deepEqual(result.structuredContent, cases.start_reconcile);
  assert.equal(lookups(), 2);
});
