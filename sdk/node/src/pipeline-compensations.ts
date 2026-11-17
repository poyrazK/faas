/** Reviewed recovery workflows with atomic, application-owned SQL effect receipts. */
import {
  JobContract,
  JobError,
  jobScope,
  jobSnapshot,
  jobValueHash,
  type JobContext,
} from "./job-runtime.js";
import {
  defineWorkflow,
  type WorkflowDefinition,
} from "./workflows.js";
import { defineJobPipeline, type PipelineJob } from "./job-pipelines.js";
import {
  createJobPipelineClient,
  type PipelineCheckpoint,
  type PipelineRunOptions,
} from "./job-pipeline-client.js";
import {
  definePipelineTool,
  parsePipelineToolReceipt,
  type PipelineTool,
} from "./pipeline-tools.js";
import {
  parsePipelineGateRecord,
  PipelineGateError,
  type PipelineGateStore,
} from "./pipeline-gates.js";
import { eventPipelineScope } from "./event-pipeline-state.js";
import {
  parsePipelineJobReceipt,
  pipelineJobIdentity,
  pipelineName,
  type PipelineScope,
  type PipelineJobStore,
} from "./pipeline-state.js";
import {
  withEventOutboxTransaction,
  eventOutboxTransactionQuery,
  type EventOutboxPool,
  type EventOutboxTransaction,
} from "./event-outbox-transaction.js";
import { EventOutboxError } from "./event-outbox-state.js";
import { pipelineCompensationSQL as SQL } from "./pipeline-compensation-protocol.js";
import {
  CompensationError,
  compensationContracts,
  parseCompensationPlan,
  compensationIdentity,
  compensationEffectIdentity,
  type CompensationActor,
  type CompensationAction,
  type CompensationCommand,
  type CompensationPlan,
  type CompensationTask,
  type CompensationOutcome,
  type CompensationInput,
  type CompensationResult,
  type CompensationStore,
} from "./pipeline-compensation-state.js";
export * from "./pipeline-compensation-state.js";
export interface CompensationSourceStage {
  readonly definition: PipelineJob<any, any>;
  readonly readOnly: boolean;
}
export interface CompensationSource {
  readonly checkpoint: PipelineCheckpoint;
  readonly stages: readonly {
    readonly step: string;
    readonly read_only: boolean;
    readonly receipt: unknown;
    readonly status: string;
    readonly results: readonly unknown[];
  }[];
}
export interface PipelineCompensationDefinition<I, O, A> {
  readonly name: string;
  readonly revision: string;
  readonly scope: PipelineScope;
  readonly sourceContract: JobContract<any, any>;
  readonly sourceStages: Readonly<Record<string, CompensationSourceStage>>;
  readonly sourceStore: PipelineJobStore;
  readonly job: PipelineJob<CompensationTask, CompensationOutcome<O>>;
  readonly jobStore: PipelineJobStore;
  readonly store: CompensationStore;
  readonly toolStore: import("./pipeline-tools.js").PipelineToolStore;
  readonly gateStore: PipelineGateStore;
  readonly token: string;
  readonly fetch?: typeof fetch;
  readonly reviewBaseURL: string;
  readonly authorize: (
    sourceRunId: string,
    access: A,
    action: CompensationAction,
    signal: AbortSignal,
  ) => Promise<CompensationActor | null>;
  /** Read-only, deterministic projection from verified source results/current business records. */
  readonly plan: (
    source: CompensationSource,
    access: A,
    signal: AbortSignal,
  ) => Promise<readonly CompensationCommand<I>[]>;
  /** Backend lookup before new dispatch. Workers additionally authorize inside the business transaction. */
  readonly authorizeExecution: (
    plan: CompensationPlan<I>,
    recoveryRunId: string,
    signal: AbortSignal,
  ) => Promise<boolean>;
  readonly requiredRoles: readonly string[];
  readonly approvalTimeoutMs?: number;
  readonly requestTimeoutMs?: number;
  readonly pollDelayMs?: number;
}
export interface PipelineCompensation<I, O, A> {
  readonly contract: JobContract<CompensationInput, CompensationResult<O>>;
  readonly workerContract: JobContract<
    CompensationTask,
    CompensationOutcome<O>
  >;
  readonly reviewContract: ReturnType<
    typeof compensationContracts<I, O>
  >["review"];
  readonly definition: WorkflowDefinition;
  readonly tool: PipelineTool<CompensationInput, CompensationResult<O>, A>;
  plan(
    sourceRunId: string,
    access: A,
    options: PipelineRunOptions,
  ): Promise<CompensationPlan<I>>;
  inspectPlan(
    id: string,
    access: A,
    options: PipelineRunOptions,
  ): Promise<CompensationPlan<I>>;
  effects(
    id: string,
    access: A,
    options: PipelineRunOptions,
  ): Promise<
    readonly {
      id: string;
      outcome: CompensationOutcome<O> | null;
    }[]
  >;
  /** SQL only: business writes and outcome receipt commit together. needs_review rolls back callback writes. */
  handler(options: {
    pool: EventOutboxPool;
    authorize: (
      tx: EventOutboxTransaction,
      plan: CompensationPlan<I>,
      command: CompensationCommand<I>,
      signal: AbortSignal,
    ) => Promise<boolean>;
    apply: (
      tx: EventOutboxTransaction,
      input: I,
      context: {
        readonly effectId: string;
        readonly ownerId: string;
        readonly signal: AbortSignal;
      },
    ) => Promise<CompensationOutcome<O>>;
  }): (
    input: CompensationTask,
    context: JobContext,
  ) => Promise<CompensationOutcome<O>>;
}
export function definePipelineCompensation<I, O, A>(
  businessSource: JobContract<I, O>,
  definition: PipelineCompensationDefinition<I, O, A>,
): PipelineCompensation<I, O, A> {
  const business = new JobContract<I, O>(businessSource.spec),
    scope = eventPipelineScope(definition.scope),
    d = {
      ...definition,
      scope,
      sourceContract: new JobContract(definition.sourceContract.spec),
      job: { ...definition.job },
    },
    stages = Object.entries(d.sourceStages)
      .map(
        ([name, s]) =>
          [
            name,
            { readOnly: s.readOnly, definition: { ...s.definition } },
          ] as const,
      )
      .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0)),
    roles = [...new Set(d.requiredRoles)].sort(),
    timeout = d.requestTimeoutMs ?? 10000,
    approvalTimeout = d.approvalTimeoutMs ?? 3600000;
  if (
    !/^[a-z][a-z0-9_]{0,29}$/.test(d.name) ||
    !pipelineName(d.revision) ||
    !stages.length ||
    stages.length > 10 ||
    stages.some(
      ([n, s]) => !pipelineName(n) || typeof s.readOnly !== "boolean",
    ) ||
    !roles.length ||
    roles.length > 16 ||
    roles.some((r) => !pipelineName(r)) ||
    !Number.isSafeInteger(timeout) ||
    timeout < 1 ||
    !Number.isSafeInteger(approvalTimeout) ||
    approvalTimeout < 1 ||
    approvalTimeout > 86400000
  )
    throw new CompensationError("invalid_input");
  const contracts = compensationContracts(business, d.name);
  if (d.job.contract.sha256 !== contracts.worker.sha256)
    throw new CompensationError("conflict");
  const config = jobValueHash([
      "gregale.compensation-config.v1",
      scope,
      d.name,
      d.revision,
      business.sha256,
      d.sourceContract.sha256,
      stages.map(([step, s]) => [
        step,
        s.readOnly,
        s.definition.contract.sha256,
      ]),
      roles,
      approvalTimeout,
    ]),
    namespace = jobValueHash([scope, d.name]);
  const sourceClient = (ownerId: string) =>
    createJobPipelineClient({
      baseURL: scope.base_url,
      scope,
      ownerId,
      contract: d.sourceContract,
      token: d.token,
      ...(d.fetch ? { fetch: d.fetch } : {}),
    });
  const recoveryClient = (ownerId: string) =>
    createJobPipelineClient({
      baseURL: scope.base_url,
      scope,
      ownerId,
      contract: contracts.root,
      token: d.token,
      ...(d.fetch ? { fetch: d.fetch } : {}),
    });
  async function execute<T>(
    settings: PipelineRunOptions,
    work: (
      signal: AbortSignal,
      check: () => void,
      left: () => number,
    ) => Promise<T>,
  ): Promise<T> {
    const end = performance.now() + settings.timeoutMs;
    try {
      return await jobScope(
        settings.timeoutMs,
        settings.signal,
        async (signal) => {
          const check = () => {
            signal.throwIfAborted();
            if (performance.now() >= end)
              throw new CompensationError("deadline");
          };
          check();
          const r = await work(signal, check, () => {
            check();
            return Math.max(1, Math.ceil(end - performance.now()));
          });
          check();
          return r;
        },
      );
    } catch (e) {
      if (e instanceof CompensationError) throw e;
      if (settings.signal?.aborted) throw new CompensationError("cancelled");
      if (e instanceof JobError && e.code === "deadline")
        throw new CompensationError("deadline");
      throw new CompensationError("request_failed");
    }
  }
  async function stored<T>(fn: () => Promise<T>, check: () => void) {
    check();
    try {
      const r = await fn();
      check();
      return r;
    } catch (e) {
      check();
      if (e instanceof CompensationError) throw e;
      throw new CompensationError("store_failed");
    }
  }
  function bound(raw: unknown): CompensationPlan<I> {
    const p = parseCompensationPlan(raw, business);
    if (
      p.name !== d.name ||
      p.revision !== d.revision ||
      p.config_sha256 !== config ||
      p.source.contract_sha256 !== d.sourceContract.sha256 ||
      jobValueHash(p.scope) !== jobValueHash(scope)
    )
      throw new CompensationError("conflict");
    return p;
  }
  async function actor(
    runId: string,
    access: A,
    action: CompensationAction,
    signal: AbortSignal,
    check: () => void,
  ) {
    try {
      const a = jobSnapshot(await d.authorize(runId, access, action, signal));
      check();
      if (!a || !pipelineName(a.actor_id) || !pipelineName(a.owner_id))
        throw Error();
      return a;
    } catch {
      check();
      throw new CompensationError("unauthorized");
    }
  }
  async function owned(
    id: string,
    access: A,
    action: CompensationAction,
    signal: AbortSignal,
    check: () => void,
  ) {
    const p = bound(await stored(() => d.store.load(id, signal), check)),
      a = await actor(p.source.run_id, access, action, signal, check);
    if (a.owner_id !== p.owner_id) throw new CompensationError("unauthorized");
    return p;
  }
  async function source(
    runId: string,
    owner: string,
    full: boolean,
    signal: AbortSignal,
    check: () => void,
    left: () => number,
  ): Promise<CompensationSource> {
    const run = await sourceClient(owner).resumeById(runId, {
        timeoutMs: left(),
        signal,
      }),
      info = await run.inspect({ timeoutMs: left(), signal });
    check();
    if (!["failed", "dead"].includes(info.status))
      throw new CompensationError("source_active");
    const evidence: CompensationSource["stages"][number][] = [];
    for (const [step, s] of stages) {
      const raw = await stored(
        () =>
          d.sourceStore.load(pipelineJobIdentity(scope, runId, step), signal),
        check,
      );
      if (raw === null) continue;
      const r = parsePipelineJobReceipt(raw),
        i = r.intent;
      if (
        i.workflow_run_id !== runId ||
        i.owner_id !== owner ||
        i.pipeline_sha256 !== d.sourceContract.sha256 ||
        i.contract_sha256 !== s.definition.contract.sha256 ||
        i.job_name !== s.definition.contract.spec.name ||
        jobValueHash(i.scope) !== jobValueHash(scope)
      )
        throw new CompensationError("conflict");
      if (!r.checkpoint) throw new CompensationError("source_unresolved");
      if (r.checkpoint.inputs.length > 50) throw new CompensationError("limit");
      const handle = s.definition.job.resume(r.checkpoint),
        progress = await handle.status({ timeoutMs: left(), signal });
      check();
      if (["queued", "running"].includes(progress.status))
        throw new CompensationError("source_active");
      if (!s.readOnly && progress.status !== "succeeded")
        throw new CompensationError("source_effects_unknown");
      let results: unknown[] = [];
      if (full && progress.status === "succeeded") {
        const items = await handle.results({
          timeoutMs: left(),
          signal,
          maxItems: 50,
        });
        check();
        if (
          items.length !== progress.total ||
          items.some((x) => x.outcome !== "succeeded")
        )
          throw new CompensationError("source_effects_unknown");
        results = items.map((x) => ({
          input_id: x.inputId,
          index: x.index,
          attempt: x.attempt,
          outcome: x.outcome,
          result: x.result,
        }));
      }
      evidence.push({
        step,
        read_only: s.readOnly,
        receipt: r,
        status: progress.status,
        results,
      });
      if (Buffer.byteLength(JSON.stringify(evidence)) > 262144)
        throw new CompensationError("limit");
    }
    return jobSnapshot({ checkpoint: run.checkpoint, stages: evidence });
  }
  async function loadExecution(
    p: CompensationPlan<I>,
    runId: string,
    signal: AbortSignal,
    check: () => void,
    left: () => number,
  ) {
    const r = await recoveryClient(p.owner_id).resumeById(runId, {
        timeoutMs: left(),
        signal,
      }),
      info = await r.inspect({ timeoutMs: left(), signal });
    check();
    const expected = jobValueHash({
      version: 1,
      scope,
      owner_id: p.owner_id,
      contract_sha256: contracts.root.sha256,
      input: { plan_id: p.identity, binding_sha256: p.binding_sha256 },
      trigger: {
        version: 1,
        kind: "tool",
        identity: jobValueHash([
          "gregale.pipeline-tool.v1",
          scope,
          "recover_" + d.name,
          d.name,
          await attemptForRun(p, runId, signal, check),
        ]),
      },
    });
    // The native checkpoint binds the retained tool envelope, including its attempt identity.
    if (r.checkpoint.input_sha256 !== expected)
      throw new CompensationError("conflict");
    if (
      info.cancelled ||
      !["pending", "running", "awaiting_event"].includes(info.status)
    )
      throw new CompensationError("execution_inactive");
    const rows = await stored(
      () => d.gateStore.list(runId, p.owner_id, signal),
      check,
    );
    const gates = rows
      .map(parsePipelineGateRecord)
      .filter((g) => g.intent.name === "review");
    if (gates.length !== 1) throw new CompensationError("approval_required");
    const g = parsePipelineGateRecord(
      await stored(
        () => d.gateStore.resolve(gates[0]!.intent.id, signal),
        check,
      ),
    );
    if (
      g.intent.kind !== "approval" ||
      g.intent.timeout_ms !== approvalTimeout ||
      jobValueHash(g.intent.required_roles) !== jobValueHash(roles) ||
      g.intent.workflow_run_id !== runId ||
      g.intent.owner_id !== p.owner_id ||
      g.intent.pipeline_sha256 !== contracts.root.sha256 ||
      g.intent.contract_sha256 !== contracts.review.sha256 ||
      g.intent.input_sha256 !== r.checkpoint.input_sha256 ||
      jobValueHash(g.intent.scope) !== jobValueHash(scope) ||
      jobValueHash(g.intent.review) !== jobValueHash(review(p)) ||
      g.decision?.state !== "approved"
    )
      throw new CompensationError("approval_required");
    contracts.review.output(g.decision.payload);
    await source(p.source.run_id, p.owner_id, false, signal, check, left);
    if (!(await d.authorizeExecution(p, runId, signal)))
      throw new CompensationError("unauthorized");
    check();
  }
  // Resolve the attempt through the native envelope, then require the shared request binding.
  async function attemptForRun(
    p: CompensationPlan<I>,
    runId: string,
    signal: AbortSignal,
    check: () => void,
  ): Promise<string> {
    // Store adapters expose an exact, immutable run binding after tool acceptance.
    const value = await stored(
      () => d.store.run(namespace, runId, signal),
      check,
    );
    if (!value || value.plan_id !== p.identity)
      throw new CompensationError("conflict");
    return value.request_id;
  }
  const review = (p: CompensationPlan<I>) => ({
    plan_id: p.identity,
    binding_sha256: p.binding_sha256,
    source_run_id: p.source.run_id,
    revision: p.revision,
    commands: p.commands.map((c) => ({
      id: c.id,
      description: c.description,
      input_sha256: jobValueHash(c.input),
    })),
  });
  const tool = definePipelineTool<CompensationInput, CompensationResult<O>, A>(
    contracts.root,
    {
      name: "recover_" + d.name,
      description:
        "Review and compensate verified effects of a failed pipeline.",
      scope,
      store: d.toolStore,
      token: d.token,
      gateStore: d.gateStore,
      reviewBaseURL: d.reviewBaseURL,
      ...(d.fetch ? { fetch: d.fetch } : {}),
      authorize: async (request, access, action, signal) => {
        const check = () => signal.throwIfAborted();
        let p: CompensationPlan<I>;
        if (action === "start") {
          if (!request.input) throw new CompensationError("invalid_input");
          p = await owned(request.input.plan_id, access, action, signal, check);
          if (request.input.binding_sha256 !== p.binding_sha256)
            throw new CompensationError("conflict");
          await source(
            p.source.run_id,
            p.owner_id,
            false,
            signal,
            check,
            () => timeout,
          );
          await stored(
            () =>
              d.store.bind(namespace, request.request_id, p.identity, signal),
            check,
          );
        } else {
          const id = await stored(
            () => d.store.attempt(namespace, request.request_id, signal),
            check,
          );
          if (!id) throw new CompensationError("conflict");
          p = await owned(id, access, action, signal, check);
        }
        return { subject_id: d.name, owner_id: p.owner_id };
      },
    },
  );
  const definitionWorkflow = defineWorkflow(
    contracts.root.spec.name,
    async (envelope: any, wf) => {
      const ref = contracts.root.input(envelope.input);
      const p = bound(
        await wf.step(
          "recovery/prepare",
          () =>
            execute({ timeoutMs: timeout }, async (signal, check) => {
              const plan = bound(
                await stored(() => d.store.load(ref.plan_id, signal), check),
              );
              if (
                ref.binding_sha256 !== plan.binding_sha256 ||
                envelope.owner_id !== plan.owner_id ||
                envelope.contract_sha256 !== contracts.root.sha256 ||
                jobValueHash(envelope.scope) !== jobValueHash(scope)
              )
                throw new CompensationError("conflict");
              if (!(await d.authorizeExecution(plan, wf.runId, signal)))
                throw new CompensationError("unauthorized");
              const receipt = parsePipelineToolReceipt(
                  await d.toolStore.load(envelope.trigger?.identity, signal),
                ),
                i = receipt.intent;
              if (
                i.name !== "recover_" + d.name ||
                i.subject_id !== d.name ||
                i.owner_id !== plan.owner_id ||
                i.contract_sha256 !== contracts.root.sha256 ||
                i.input_sha256 !== jobValueHash(envelope) ||
                jobValueHash(i.scope) !== jobValueHash(scope) ||
                (receipt.checkpoint !== null &&
                  receipt.checkpoint.run_id !== wf.runId) ||
                (await d.store.attempt(namespace, i.request_id, signal)) !==
                  plan.identity
              )
                throw new CompensationError("conflict");
              await d.store.registerRun(
                namespace,
                wf.runId,
                i.request_id,
                plan.identity,
                signal,
              );
              check();
              return plan;
            }),
          { retry: { maxAttempts: 3, backoff: "exponential" } },
        ),
      );
      const jobs: Record<
        string,
        PipelineJob<CompensationTask, CompensationOutcome<O>>
      > = {};
      for (let n = 0; n < 20; n++)
        jobs["undo_" + String(n).padStart(2, "0")] = {
          ...d.job,
          parallelism: 1,
          retryMax: d.job.retryMax ?? 2,
          job: {
            ...d.job.job,
            submit: async (inputs, settings) => {
              try {
                await execute(
                  { timeoutMs: settings.timeoutMs, signal: settings.signal },
                  async (signal, check, left) =>
                    loadExecution(p, wf.runId, signal, check, left),
                );
              } catch {
                throw new PipelineGateError("forbidden");
              }
              return d.job.job.submit(inputs, settings);
            },
          },
        };
      const pipeline = defineJobPipeline(
        contracts.root,
        {
          scope,
          store: d.jobStore,
          jobs,
          gateStore: d.gateStore,
          requestTimeoutMs: timeout,
          pollDelayMs: d.pollDelayMs ?? 1000,
          maxJournalBytes: 262144,
        },
        async (_input, context): Promise<CompensationResult<O>> => {
          const decision = await context.approval(
            "review",
            contracts.review,
            review(p),
            { timeoutMs: approvalTimeout, requiredRoles: roles },
          );
          if (decision.state !== "approved")
            return {
              state: decision.state as "rejected" | "expired" | "cancelled",
              results: [],
            };
          const results: {
            id: string;
            outcome: CompensationOutcome<O>;
          }[] = [];
          for (let index = 0; index < p.commands.length; index++) {
            const c = p.commands[index]!,
              result = await context.job(
                "undo_" + String(index).padStart(2, "0"),
                [
                  {
                    id: compensationEffectIdentity(p, c.effect_id),
                    value: {
                      plan_id: p.identity,
                      command_id: c.id,
                      recovery_run_id: wf.runId,
                    },
                  },
                ],
              );
            const outcome = result.results[0]!.result;
            results.push({ id: c.id, outcome });
            if (outcome.state === "needs_review")
              return { state: "needs_review", results };
          }
          return { state: "completed", results };
        },
      );
      return pipeline.definition.run(envelope, wf);
    },
  );
  const api: PipelineCompensation<I, O, A> = {
    contract: contracts.root,
    workerContract: contracts.worker,
    reviewContract: contracts.review,
    definition: definitionWorkflow,
    tool,
    plan(runId, access, settings) {
      return execute(settings, async (signal, check, left) => {
        const a = await actor(runId, access, "plan", signal, check),
          evidence = await source(runId, a.owner_id, true, signal, check, left),
          identity = compensationIdentity(scope, d.name, runId);
        const old = await stored(() => d.store.load(identity, signal), check);
        if (old) {
          const p = bound(old);
          if (
            p.owner_id !== a.owner_id ||
            p.source_sha256 !== jobValueHash(evidence)
          )
            throw new CompensationError("conflict");
          return p;
        }
        const commands = jobSnapshot(await d.plan(evidence, access, signal));
        check();
        const base = {
          version: 1 as const,
          identity,
          scope,
          name: d.name,
          revision: d.revision,
          config_sha256: config,
          owner_id: a.owner_id,
          source: evidence.checkpoint,
          source_sha256: jobValueHash(evidence),
          commands,
        };
        const p = parseCompensationPlan(
          { ...base, binding_sha256: jobValueHash(base) },
          business,
        );
        contracts.review.input(review(p));
        if (Buffer.byteLength(JSON.stringify(review(p))) > 16384)
          throw new CompensationError("limit");
        const saved = bound(
          await stored(() => d.store.reserve(p, signal), check),
        );
        if (jobValueHash(saved) !== jobValueHash(p))
          throw new CompensationError("conflict");
        return saved;
      });
    },
    inspectPlan: (id, access, settings) =>
      execute(settings, (signal, check) =>
        owned(id, access, "inspect", signal, check),
      ),
    effects: (id, access, settings) =>
      execute(settings, async (signal, check) => {
        const p = await owned(id, access, "inspect", signal, check),
          out = [];
        for (const c of p.commands) {
          const raw = await stored(
            () =>
              d.store.effect(
                compensationEffectIdentity(p, c.effect_id),
                signal,
              ),
            check,
          );
          if (raw && raw.command_sha256 !== jobValueHash([config, c]))
            throw new CompensationError("conflict");
          out.push({
            id: c.id,
            outcome: raw ? contracts.worker.output(raw.outcome) : null,
          });
        }
        return out;
      }),
    handler(options) {
      return async (task, context) => {
        let transactionPending = false;
        try {
          return await execute(
            { timeoutMs: timeout, signal: context.signal },
            async (signal, check, left) => {
              contracts.worker.input(task);
              const p = bound(
                  await stored(() => d.store.load(task.plan_id, signal), check),
                ),
                c = p.commands.find((c) => c.id === task.command_id);
              if (!c) throw new CompensationError("conflict");
              await loadExecution(p, task.recovery_run_id, signal, check, left);
              const effectId = compensationEffectIdentity(p, c.effect_id),
                digest = jobValueHash([config, c]);
              if (context.inputId !== effectId)
                throw new CompensationError("conflict");
              try {
                transactionPending = true;
                return await withEventOutboxTransaction(
                  options.pool,
                  async (tx) => {
                    const query = eventOutboxTransactionQuery(tx);
                    await query(SQL.lock, ["compensation:" + effectId]);
                    check();
                    if (!(await options.authorize(tx, p, c, signal)))
                      throw new EventOutboxError("scope_mismatch");
                    check();
                    for (const prior of p.commands.slice(
                      0,
                      p.commands.findIndex((x) => x.id === c.id),
                    )) {
                      const rows = (
                        await query(SQL.effect, [
                          compensationEffectIdentity(p, prior.effect_id),
                        ])
                      ).rows;
                      if (
                        rows.length !== 1 ||
                        rows[0]!.command_sha256 !==
                          jobValueHash([config, prior]) ||
                        contracts.worker.output(rows[0]!.outcome).state ===
                          "needs_review"
                      )
                        throw new EventOutboxError("conflict");
                    }
                    const old = (await query(SQL.effect, [effectId])).rows;
                    if (old.length > 1) throw new EventOutboxError("conflict");
                    if (old.length) {
                      if (old[0]!.command_sha256 !== digest)
                        throw new EventOutboxError("conflict");
                      return contracts.worker.output(old[0]!.outcome);
                    }
                    await tx.query("SAVEPOINT compensation_business");
                    const outcome = contracts.worker.output(
                      await options.apply(tx, business.input(c.input), {
                        effectId,
                        ownerId: p.owner_id,
                        signal,
                      }),
                    );
                    check();
                    if (outcome.state === "needs_review")
                      await tx.query(
                        "ROLLBACK TO SAVEPOINT compensation_business",
                      );
                    await tx.query("RELEASE SAVEPOINT compensation_business");
                    const rows = (
                      await query(SQL.complete, [
                        effectId,
                        digest,
                        JSON.stringify(outcome),
                      ])
                    ).rows;
                    if (rows.length !== 1)
                      throw new EventOutboxError("store_failed");
                    return contracts.worker.output(rows[0]!.outcome);
                  },
                  { timeoutMs: left(), signal },
                );
              } catch (e) {
                if (e instanceof EventOutboxError)
                  throw new CompensationError(
                    e.code === "scope_mismatch"
                      ? "unauthorized"
                      : e.code === "conflict"
                        ? "conflict"
                        : e.code === "commit_unknown"
                          ? "commit_unknown"
                          : e.code === "cancelled"
                            ? "cancelled"
                            : e.code === "deadline"
                              ? "deadline"
                              : "store_failed",
                  );
                throw e;
              } finally {
                transactionPending = false;
              }
            },
          );
        } catch (e) {
          if (transactionPending) throw new CompensationError("commit_unknown");
          throw e;
        }
      };
    },
  };
  return Object.freeze(api);
}
