import { readFileSync } from "node:fs";
import { randomUUID } from "node:crypto";
import {
  JobContract,
  jobHash,
  jobValueHash,
  prepareJobInput,
  type JobInput,
} from "../src/job-runtime.js";
import {
  definePipelineCompensation,
  compensationContracts,
  type CompensationStore,
  type CompensationPlan,
  type CompensationTask,
  type CompensationOutcome,
} from "../src/pipeline-compensations.js";
import {
  parsePipelineJobReceipt,
  type PipelineJobStore,
} from "../src/pipeline-state.js";
import {
  parsePipelineToolReceipt,
  type PipelineToolStore,
} from "../src/pipeline-tool-state.js";
import {
  parsePipelineGateRecord,
  type PipelineGateStore,
} from "../src/pipeline-gates.js";
import { pipelineCompensationSQL as SQL } from "../src/pipeline-compensation-protocol.js";
import { runWorkflowInvocation, type JournalEntry } from "../src/workflows.js";
export const f = JSON.parse(
  readFileSync(
    new URL(
      "../../../request-contracts/pipeline-compensations.json",
      import.meta.url,
    ),
    "utf8",
  ),
);
export const source = JSON.parse(
  readFileSync(
    new URL("../../../request-contracts/job-pipelines.json", import.meta.url),
    "utf8",
  ),
);
export type Input = {
  row_id: string;
  expected_version: number;
};
export type Output = {
  deleted: boolean;
};
export const business = new JobContract<Input, Output>(f.business_contract),
  root = new JobContract(source.root_contract),
  forward = new JobContract(source.job_contracts.validate),
  contracts = compensationContracts(business, f.name),
  scope = source.scope;
export function setup() {
  const state = {
    owner: "alice",
    revoked: false,
    executeAllowed: true,
    sourceStatus: "failed",
    jobStatus: "succeeded",
    unresolved: false,
    loseReserve: false,
    loseStart: false,
    loseCommit: false,
    hangCommit: false,
    onCommit: undefined as undefined | (() => void),
    failApply: false,
    invalidOutput: false,
    writes: 0,
    posts: 0,
    applies: 0,
    deadlineGate: undefined as undefined | (() => Promise<void>),
  };
  const plans = new Map<string, any>(),
    attempts = new Map<string, string>(),
    runs = new Map<string, any>(),
    effects = new Map<string, any>(),
    data = new Map([
      ["one", { version: 1 }],
      ["two", { version: 1 }],
    ]),
    toolRows = new Map<string, any>(),
    jobRows = new Map<string, any>(),
    gates = new Map<string, any>();
  const store: CompensationStore = {
    async load(id) {
      return plans.get(id) ?? null;
    },
    async reserve(p) {
      if (!plans.has(p.identity)) plans.set(p.identity, structuredClone(p));
      if (state.loseReserve) throw Error("private database diagnostics");
      return plans.get(p.identity);
    },
    async attempt(ns, id) {
      return attempts.get(ns + id) ?? null;
    },
    async bind(ns, id, plan) {
      const key = ns + id,
        old = attempts.get(key);
      if (old && old !== plan) throw Error("conflict");
      attempts.set(key, plan);
      return plan;
    },
    async run(ns, id) {
      return runs.get(ns + id) ?? null;
    },
    async registerRun(ns, id, request_id, plan_id) {
      const key = ns + id,
        old = runs.get(key);
      if (
        (old && old.request_id !== request_id) ||
        [...runs.values()].some(
          (v) => v.request_id === request_id && v.id !== id,
        )
      )
        throw Error("conflict");
      runs.set(key, { id, request_id, plan_id });
    },
    async effect(id) {
      return effects.get(id) ?? null;
    },
  };
  const toolStore: PipelineToolStore = {
    async load(id) {
      return toolRows.get(id) ?? null;
    },
    async reserve(intent) {
      const old = toolRows.get(intent.identity);
      if (old) return { created: false, receipt: old };
      const receipt = parsePipelineToolReceipt({ intent, checkpoint: null });
      toolRows.set(intent.identity, receipt);
      return { created: true, receipt };
    },
    async complete(intent, checkpoint) {
      const receipt = parsePipelineToolReceipt({ intent, checkpoint });
      toolRows.set(intent.identity, receipt);
      return receipt;
    },
  };
  const jobStore: PipelineJobStore = {
    async load(id) {
      return jobRows.get(id) ?? null;
    },
    async reserve(intent) {
      const old = jobRows.get(intent.identity);
      if (old) return { created: false, receipt: old };
      const receipt = parsePipelineJobReceipt({ intent, checkpoint: null });
      jobRows.set(intent.identity, receipt);
      return { created: true, receipt };
    },
    async complete(intent, checkpoint) {
      const receipt = parsePipelineJobReceipt({ intent, checkpoint });
      jobRows.set(intent.identity, receipt);
      return receipt;
    },
  };
  const gateStore: PipelineGateStore = {
    async open(intent) {
      const old = gates.get(intent.id);
      if (old) return old;
      const r = parsePipelineGateRecord({
        intent,
        expires_at: "2026-11-17T12:00:00.000Z",
        decision: null,
        delivery: "pending",
      });
      gates.set(intent.id, r);
      return r;
    },
    async resolve(id) {
      return gates.get(id) ?? null;
    },
    async decide(id, binding, proposal) {
      const r = gates.get(id);
      if (r.decision) return r;
      const saved = parsePipelineGateRecord({
        ...r,
        decision: { ...proposal, decided_at: "2026-11-17T10:00:00.000Z" },
      });
      gates.set(id, saved);
      return saved;
    },
    async mark() {},
    async pending() {
      return [];
    },
    async list(id, owner) {
      return [...gates.values()].filter(
        (g) => g.intent.workflow_run_id === id && g.intent.owner_id === owner,
      );
    },
  };
  const sourceEnvelope = {
    version: 1,
    scope,
    owner_id: "alice",
    contract_sha256: root.sha256,
    input: source.input,
  };
  const records = new Map<string, any>([
    [
      source.workflow_run_id,
      {
        id: source.workflow_run_id,
        app_id: scope.app_id,
        workflow_name: root.spec.name,
        input: sourceEnvelope,
        status: "failed",
      },
    ],
  ]);
  const fetch: typeof globalThis.fetch = async (url, init) => {
    const path = new URL(String(url)).pathname;
    if (path === "/v1/account") return Response.json({ id: scope.account_id });
    if (path === "/v1/apps/imports") return Response.json({ id: scope.app_id });
    if (path.endsWith("/steps")) return Response.json({ steps: [] });
    if (path.endsWith("/cancel")) {
      const r = records.get(path.split("/").at(-2)!);
      r.status = "failed";
      r.cancelled_at = "2026-11-17T10:00:00Z";
      return Response.json(r);
    }
    if (init?.method === "POST") {
      state.posts++;
      const id = randomUUID(),
        r = {
          id,
          app_id: scope.app_id,
          workflow_name: contracts.root.spec.name,
          input: JSON.parse(String(init.body)),
          status: "running",
        };
      records.set(id, r);
      if (state.loseStart) throw Error("private lost reply");
      return Response.json(r, { status: 201 });
    }
    const r = records.get(path.split("/").at(-1)!);
    if (r?.id === source.workflow_run_id) r.status = state.sourceStatus;
    return Response.json(r ?? {}, { status: r ? 200 : 404 });
  };
  const sourceStore = {
    async load() {
      return {
        ...source.validate_receipt,
        checkpoint: state.unresolved
          ? null
          : source.validate_receipt.checkpoint,
      };
    },
  } as unknown as PipelineJobStore;
  const sourceJob = {
    contract: forward,
    job: {
      resume(checkpoint: any) {
        return {
          checkpoint,
          async status() {
            return {
              runId: checkpoint.run_id,
              status: state.jobStatus,
              total: 1,
              succeeded: state.jobStatus === "succeeded" ? 1 : 0,
              failed: state.jobStatus === "failed" ? 1 : 0,
              cancelled: 0,
              running: state.jobStatus === "running" ? 1 : 0,
            };
          },
          async results() {
            return [
              {
                inputId: "one",
                index: 0,
                attempt: 1,
                outcome: "succeeded",
                result: { n: source.input.n + 1 },
              },
            ];
          },
        };
      },
    } as any,
  };
  let queued = Promise.resolve();
  const pool = {
    async connect() {
      const prior = queued;
      let unlock!: () => void;
      queued = new Promise((r) => (unlock = r));
      await prior;
      let localData = structuredClone(data),
        localEffects = structuredClone(effects),
        save: any;
      return {
        async query(sql: string, values: any[] = []) {
          if (
            sql.startsWith("BEGIN") ||
            sql.startsWith("SELECT set_config") ||
            sql === SQL.lock
          )
            return { rows: [] };
          if (sql === "SAVEPOINT compensation_business") {
            save = structuredClone(localData);
            return { rows: [] };
          }
          if (sql === "ROLLBACK TO SAVEPOINT compensation_business") {
            localData = save;
            return { rows: [] };
          }
          if (
            sql === "RELEASE SAVEPOINT compensation_business" ||
            sql === "ROLLBACK"
          )
            return { rows: [] };
          if (sql === "COMMIT") {
            data.clear();
            for (const [k, v] of localData) data.set(k, v);
            effects.clear();
            for (const [k, v] of localEffects) effects.set(k, v);
            state.onCommit?.();
            if (state.hangCommit) return new Promise<any>(() => {});
            if (state.loseCommit) throw Error("private commit");
            return { rows: [] };
          }
          if (sql === SQL.effect)
            return {
              rows: localEffects.has(values[0])
                ? [localEffects.get(values[0])]
                : [],
            };
          if (sql === SQL.complete) {
            const row = {
              command_sha256: values[1],
              outcome: JSON.parse(values[2]),
            };
            localEffects.set(values[0], row);
            return { rows: [row] };
          }
          if (sql === "business.read")
            return {
              rows: localData.has(values[0]) ? [localData.get(values[0])] : [],
            };
          if (sql === "business.delete") {
            localData.delete(values[0]);
            state.writes++;
            return { rows: [] };
          }
          if (sql === "business.edit") {
            localData.set(values[0], { version: 99 });
            return { rows: [] };
          }
          throw Error("unexpected SQL");
        },
        release() {
          unlock();
        },
      };
    },
  };
  let worker: any;
  const jobs = new Map<string, any>();
  const job = {
    async submit(inputs: readonly JobInput<CompensationTask>[], settings: any) {
      const refs = await Promise.all(
        inputs.map((item) =>
          prepareJobInput(contracts.worker, item, {
            inputId: item.id,
            attempt: 1,
            signal: new AbortController().signal,
          }),
        ),
      );
      const checkpoint = {
        version: 1,
        base_url: scope.base_url,
        account_id: scope.account_id,
        job_name: contracts.worker.spec.name,
        job_id: source.job_id,
        run_id: randomUUID(),
        contract_sha256: contracts.worker.sha256,
        input_digest: jobHash(Buffer.from(JSON.stringify(refs))),
        source_run_id: null,
        inputs: refs.map((r) => ({
          input_id: r.input_id,
          binding_sha256: jobHash(Buffer.from(r.input_ref)),
          source_task_index: null,
        })),
      };
      const outcomes: any[] = [];
      for (const [n, item] of inputs.entries()) {
        const result = await worker(item.value, {
          inputId: item.id,
          attempt: 1,
          signal: new AbortController().signal,
        });
        outcomes.push({
          inputId: item.id,
          index: n,
          attempt: 1,
          outcome: "succeeded",
          result,
        });
      }
      const handle = {
        checkpoint,
        async status() {
          return {
            runId: checkpoint.run_id,
            status: "succeeded",
            total: inputs.length,
            succeeded: inputs.length,
            failed: 0,
            cancelled: 0,
            running: 0,
          };
        },
        async results() {
          return outcomes;
        },
      };
      jobs.set(checkpoint.run_id, handle);
      return handle;
    },
    resume(checkpoint: any) {
      return jobs.get(checkpoint.run_id);
    },
  } as any;
  const definition = {
    name: f.name,
    revision: "v1",
    scope,
    sourceContract: root,
    sourceStages: { validate: { definition: sourceJob, readOnly: false } },
    sourceStore,
    job: { contract: contracts.worker, job },
    jobStore,
    store,
    toolStore,
    gateStore,
    token: "backend",
    fetch,
    reviewBaseURL: "https://review.example.test/",
    requiredRoles: ["reviewer"],
    pollDelayMs: 1,
    authorize: async (_run: string, actor: string) =>
      actor === "alice" && !state.revoked
        ? { actor_id: actor, owner_id: state.owner }
        : null,
    plan: async () => structuredClone(f.commands),
    authorizeExecution: async () => state.executeAllowed,
  };
  const recovery = definePipelineCompensation<Input, Output, string>(
    business,
    definition,
  );
  worker = recovery.handler({
    pool,
    authorize: async () => state.executeAllowed,
    apply: async (tx, input) => {
      state.applies++;
      if (state.deadlineGate) await state.deadlineGate();
      const row = (await tx.query("business.read", [input.row_id])).rows[0];
      if (!row)
        return { state: "already_compensated", result: { deleted: false } };
      if (row.version !== input.expected_version) {
        await tx.query("business.edit", [input.row_id]);
        return { state: "needs_review", result: { deleted: false } };
      }
      await tx.query("business.delete", [input.row_id]);
      if (state.failApply) throw Error("private SQL");
      if (state.invalidOutput)
        return { state: "compensated", result: { deleted: "bad" } } as any;
      return { state: "compensated", result: { deleted: true } };
    },
  });
  async function start() {
    const p = await recovery.plan(source.workflow_run_id, "alice", {
      timeoutMs: 1000,
    });
    const r = await recovery.tool.start(
      { plan_id: p.identity, binding_sha256: p.binding_sha256 },
      randomUUID(),
      "alice",
      { timeoutMs: 1000 },
    );
    return {
      plan: p,
      runId: r.receipt.checkpoint!.run_id,
      journal: [] as JournalEntry[],
    };
  }
  async function invoke(ctx: any) {
    return runWorkflowInvocation(recovery.definition, {
      protocol: "gregale.workflow.code/v1",
      workflow: contracts.root.spec.name,
      run_id: ctx.runId,
      input: records.get(ctx.runId).input,
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
    for (let n = 0; n < 8; n++) {
      const r = await invoke(ctx);
      if (r.ops.some((op: any) => op.kind === "event")) return r;
      commit(ctx, r);
    }
    throw Error("no gate");
  }
  async function approve(ctx: any, stateChoice = "approved") {
    const g = [...gates.values()].find(
      (g) => g.intent.workflow_run_id === ctx.runId,
    );
    await gateStore.decide(
      g.intent.id,
      g.intent.binding_sha256,
      {
        state: stateChoice as any,
        actor_id: "alice",
        payload: { reason: "Reviewed exact cleanup" },
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
    for (let n = 0; n < 30; n++) {
      const reply = await invoke(ctx);
      if (reply.result.status === "succeeded") {
        records.get(ctx.runId).status = "succeeded";
        records.get(ctx.runId).output = reply.result.output;
        return (reply.result.output as any).output;
      }
      if (reply.result.status === "failed") throw Error(JSON.stringify(reply));
      commit(ctx, reply);
    }
    throw Error("did not finish");
  }
  return {
    state,
    recovery,
    worker,
    definition,
    plans,
    store,
    toolRows,
    toolStore,
    jobRows,
    jobStore,
    gates,
    gateStore,
    records,
    runs,
    effects,
    data,
    pool,
    start,
    invoke,
    commit,
    untilGate,
    approve,
    finish,
  };
}
