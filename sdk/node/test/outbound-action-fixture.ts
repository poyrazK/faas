import { randomUUID } from "node:crypto";
import {
  defineOutboundAction,
  type OutboundActionStore,
  OutboundActionError,
} from "../src/outbound-actions.js";
import {
  stripeRefundContract,
  stripeRefundActionProvider,
  type StripeRefundInput,
} from "../src/stripe-refund-actions.js";
import {
  parsePipelineGateRecord,
  type PipelineGateStore,
} from "../src/pipeline-gates.js";
import {
  parsePipelineToolReceipt,
  type PipelineToolStore,
} from "../src/pipeline-tool-state.js";
import { runWorkflowInvocation, type JournalEntry } from "../src/workflows.js";
export const scope = {
  base_url: "https://control.test",
  account_id: "11111111-1111-4111-8111-111111111111",
  app_id: "22222222-2222-4222-8222-222222222222",
  app_slug: "imports",
};
export const input: StripeRefundInput = {
  payment_intent: "pi_csv",
  amount_minor: 1000,
  currency: "usd",
  reason: "requested_by_customer",
};
export const requestId = "44444444-4444-4444-8444-444444444444";
export function actionFixture(
  provider?: ReturnType<typeof stripeRefundActionProvider>,
) {
  const records = new Map<string, any>(),
    runs = new Map<string, string>(),
    attempts = new Map<string, string>(),
    gates = new Map<string, any>(),
    nativeRuns = new Map<string, any>(),
    tools = new Map<string, any>();
  const state = {
    revoked: false,
    owner: "alice",
    executionAllowed: true,
    posts: 0,
    refunds: [] as any[],
    loseResponse: false,
    loseClaim: false,
    loseReserve: false,
    loseObserve: false,
    pending: false,
    badAccount: false,
    accountPadding: 0,
    duplicate: false,
    missing: false,
    hangPost: false,
    expired: false,
    gateTamper: false,
    beforeClaim: undefined as undefined | (() => Promise<void>),
    onPost: undefined as undefined | (() => void),
    onVerify: undefined as undefined | (() => void),
    httpStatus: 200,
  };
  const copy = <T>(x: T): T => structuredClone(x);
  const store: OutboundActionStore = {
    async load(id) {
      return copy(records.get(id) ?? null);
    },
    async reserve(i) {
      if (!records.has(i.identity))
        records.set(i.identity, {
          intent: copy(i),
          version: 0,
          outcome: { state: "prepared", code: "prepared" },
        });
      if (state.loseReserve) throw Error("private SQL error");
      return copy(records.get(i.identity));
    },
    async claim(id, v) {
      await state.beforeClaim?.();
      const r = records.get(id);
      if (r.version !== v || r.outcome.state !== "prepared") return null;
      r.version++;
      r.outcome = { state: "unknown", code: "dispatch_unconfirmed" };
      if (state.loseClaim) throw Error("private SQL error");
      return copy(r);
    },
    async observe(id, v, o) {
      const r = records.get(id);
      if (r.version !== v || !["unknown", "pending"].includes(r.outcome.state))
        return null;
      r.version++;
      r.outcome = copy(o);
      if (state.loseObserve) throw Error("private COMMIT error");
      return copy(r);
    },
    async cancel(id, v) {
      const r = records.get(id);
      if (r.version !== v || r.outcome.state !== "prepared") return null;
      r.version++;
      r.outcome = { state: "cancelled", code: "cancelled_before_dispatch" };
      return copy(r);
    },
    async run(id, runId) {
      return runs.get(id + runId) ?? null;
    },
    async registerRun(id, runId, digest) {
      const k = id + runId;
      if (runs.has(k) && runs.get(k) !== digest)
        throw new OutboundActionError("conflict");
      runs.set(k, digest);
    },
    async attempt(ns, id) {
      return attempts.get(ns + id) ?? null;
    },
    async bind(ns, id, action) {
      const k = ns + id;
      if (attempts.has(k) && attempts.get(k) !== action)
        throw new OutboundActionError("conflict");
      attempts.set(k, action);
    },
  };
  const toolStore: PipelineToolStore = {
    async load(id) {
      return tools.get(id) ?? null;
    },
    async reserve(intent) {
      if (tools.has(intent.identity))
        return { created: false, receipt: tools.get(intent.identity) };
      const r = parsePipelineToolReceipt({ intent, checkpoint: null });
      tools.set(intent.identity, r);
      return { created: true, receipt: r };
    },
    async complete(intent, checkpoint) {
      const r = parsePipelineToolReceipt({ intent, checkpoint });
      tools.set(intent.identity, r);
      return r;
    },
  };
  const gateStore: PipelineGateStore = {
    async open(intent) {
      if (gates.has(intent.id)) return gates.get(intent.id);
      const r = parsePipelineGateRecord({
        intent,
        expires_at: "2030-11-17T12:00:00.000Z",
        decision: null,
        delivery: "pending",
      });
      gates.set(intent.id, r);
      return r;
    },
    async resolve(id) {
      let r = gates.get(id) ?? null;
      if (r && state.expired && !r.decision) {
        r = parsePipelineGateRecord({
          ...r,
          decision: {
            state: "expired",
            actor_id: null,
            payload: null,
            decided_at: "2030-11-17T12:01:00.000Z",
          },
        });
        gates.set(id, r);
      }
      return r;
    },
    async decide(id, _binding, proposal) {
      const r = gates.get(id);
      const v = parsePipelineGateRecord({
        ...r,
        decision: { ...proposal, decided_at: "2026-11-17T10:00:00.000Z" },
      });
      gates.set(id, v);
      return v;
    },
    async mark() {},
    async pending() {
      return [];
    },
    async list(runId, owner) {
      return [...gates.values()].filter(
        (r) =>
          r.intent.workflow_run_id === runId && r.intent.owner_id === owner,
      );
    },
  };
  const fetch: typeof globalThis.fetch = async (raw, options) => {
    const url = new URL(String(raw)),
      path = url.pathname;
    if (url.origin === "http://relay.test") {
      if (new Headers(options?.headers).has("Authorization"))
        throw Error("credential leak");
      if (path.endsWith("/v1/account")) {
        state.onVerify?.();
        return Response.json({
          id: state.badAccount ? "acct_wrong" : "acct_fixture",
          metadata: { padding: "x".repeat(state.accountPadding) },
        });
      }
      if (options?.method === "POST") {
        state.posts++;
        state.onPost?.();
        const form = new URLSearchParams(String(options.body));
        const refund = {
          id: "re_" + state.posts,
          object: "refund",
          payment_intent: form.get("payment_intent"),
          amount: Number(form.get("amount")),
          currency: form.get("currency"),
          status: state.pending ? "pending" : "succeeded",
          metadata: {
            gregale_action_id: form.get("metadata[gregale_action_id]"),
            gregale_input_sha256: form.get("metadata[gregale_input_sha256]"),
          },
        };
        if (state.httpStatus === 200) state.refunds.push(refund);
        if (state.hangPost) return new Promise(() => {});
        if (state.loseResponse)
          throw Error("provider accepted; private transport error");
        return Response.json(refund, { status: state.httpStatus });
      }
      if (path.endsWith("/v1/refunds")) {
        const data = state.missing
          ? []
          : state.duplicate
            ? [...state.refunds, { ...state.refunds[0], id: "re_duplicate" }]
            : state.refunds;
        return Response.json({ object: "list", has_more: false, data });
      }
      const r = state.refunds.find((r) => path.endsWith("/" + r.id));
      return Response.json(r ?? {}, { status: r ? 200 : 404 });
    }
    if (path === "/v1/account") return Response.json({ id: scope.account_id });
    if (path === "/v1/apps/imports") return Response.json({ id: scope.app_id });
    if (path.endsWith("/steps")) return Response.json({ steps: [] });
    if (path.endsWith("/cancel")) {
      const r = nativeRuns.get(path.split("/").at(-2)!);
      r.status = "failed";
      r.cancelled_at = "2026-11-17T10:00:00.000Z";
      return Response.json(r);
    }
    if (options?.method === "POST") {
      const id = randomUUID(),
        r = {
          id,
          app_id: scope.app_id,
          workflow_name: "action-csv-refund",
          input: JSON.parse(String(options.body)),
          status: "running",
        };
      nativeRuns.set(id, r);
      return Response.json(r, { status: 201 });
    }
    const r = nativeRuns.get(path.split("/").at(-1)!);
    return Response.json(r ?? {}, { status: r ? 200 : 404 });
  };
  const make = (revision = "v1") =>
    defineOutboundAction(stripeRefundContract, {
      name: "csv_refund",
      revision,
      scope,
      integrationId: "33333333-3333-4333-8333-333333333333",
      relayURL: "http://relay.test",
      token: "fixture",
      fetch,
      store,
      provider:
        provider ??
        stripeRefundActionProvider({
          accountId: "acct_fixture",
          apiVersion: "2026-10-28.endive",
        }),
      toolStore,
      gateStore,
      reviewBaseURL: "https://review.test",
      requiredRoles: ["reviewer"],
      authorize: async (request, access: string) =>
        !state.revoked &&
        access === state.owner &&
        request.input.payment_intent === "pi_csv" &&
        request.input.amount_minor === 1000
          ? { actor_id: access, owner_id: state.owner }
          : null,
      authorizeExecution: async (i) =>
        !state.revoked && state.executionAllowed && i.owner_id === state.owner,
    });
  const action = make();
  async function start(p: any) {
    const result = await action.tool.start(
      { action_id: p.action_id, binding_sha256: p.binding_sha256 },
      randomUUID(),
      "alice",
      { timeoutMs: 1000 },
    );
    return {
      runId: result.receipt.checkpoint!.run_id,
      journal: [] as JournalEntry[],
    };
  }
  async function invoke(ctx: any) {
    return runWorkflowInvocation(action.definition, {
      protocol: "gregale.workflow.code/v1",
      workflow: action.contract.spec.name,
      run_id: ctx.runId,
      input: nativeRuns.get(ctx.runId).input,
      invocation: ctx.journal.length + 1,
      journal: ctx.journal,
    });
  }
  function commit(ctx: any, reply: any) {
    for (const op of reply.ops)
      ctx.journal[op.seq] = {
        ...op,
        status: op.kind === "sleep" ? "succeeded" : op.status,
        attempt: 1,
      };
  }
  async function untilGate(ctx: any) {
    for (let n = 0; n < 10; n++) {
      const r = await invoke(ctx);
      if (r.result.status === "failed") throw Error(JSON.stringify(r));
      if (r.ops.some((o: any) => o.kind === "event")) return r;
      commit(ctx, r);
    }
    throw Error("No gate");
  }
  async function approve(ctx: any, choice = "approved") {
    const g = [...gates.values()].find(
      (g) => g.intent.workflow_run_id === ctx.runId,
    );
    await gateStore.decide(
      g.intent.id,
      g.intent.binding_sha256,
      {
        state: choice as any,
        actor_id: choice === "expired" ? null : "alice",
        payload: ["expired", "cancelled"].includes(choice)
          ? null
          : { reason: "Approved exact refund" },
      },
      new AbortController().signal,
    );
    const r = await invoke(ctx);
    for (const op of r.ops)
      ctx.journal[op.seq] = {
        ...op,
        status:
          op.kind === "event" || op.kind === "sleep" ? "succeeded" : op.status,
        attempt: 1,
        ...(op.kind === "event" ? { output: null } : {}),
      };
  }
  async function finish(ctx: any) {
    for (let n = 0; n < 15; n++) {
      const r = await invoke(ctx);
      if (r.result.status === "failed") throw Error(JSON.stringify(r));
      if (r.result.status === "succeeded") {
        nativeRuns.get(ctx.runId).status = "succeeded";
        nativeRuns.get(ctx.runId).output = r.result.output;
        return (r.result.output as any).output;
      }
      commit(ctx, r);
    }
    throw Error("Not finished");
  }
  return {
    action,
    make,
    state,
    records,
    store,
    gates,
    gateStore,
    nativeRuns,
    start,
    invoke,
    commit,
    untilGate,
    approve,
    finish,
  };
}
