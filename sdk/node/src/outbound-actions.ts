/** Typed external writes with exact approval and durable, read-only reconciliation. */
import {
  JobContract,
  JobError,
  jobScope,
  jobSnapshot,
  jobValueHash,
} from "./job-runtime.js";
import { defineWorkflow, type WorkflowDefinition } from "./workflows.js";
import { defineJobPipeline } from "./job-pipelines.js";
import {
  createJobPipelineClient,
  type PipelineRunOptions,
} from "./job-pipeline-client.js";
import { definePipelineTool, type PipelineTool } from "./pipeline-tools.js";
import {
  parsePipelineGateRecord,
  type PipelineGateStore,
} from "./pipeline-gates.js";
import { eventPipelineScope } from "./event-pipeline-state.js";
import {
  pipelineName,
  type PipelineScope,
  type PipelineJobStore,
} from "./pipeline-state.js";
import type { PipelineToolStore } from "./pipeline-tool-state.js";
import { fileEventHash, fileEventUUID } from "./file-events.js";
import {
  OutboundActionError,
  actionReference,
  actionPath,
  actionRequest,
  actionObservation,
  outboundActionContracts,
  outboundActionIdentity,
  parseOutboundActionRecord,
  type OutboundActionActor,
  type OutboundActionIntent,
  type OutboundActionRecord,
  type OutboundActionRequest,
  type OutboundActionObservation,
  type OutboundActionReference,
  type OutboundActionReview,
  type OutboundActionView,
  type OutboundActionResult,
  type OutboundActionOperation,
  type OutboundActionStore,
} from "./outbound-action-state.js";
export * from "./outbound-action-state.js";
export interface OutboundActionResponse {
  readonly status: number;
  readonly body: unknown;
}
export interface OutboundActionReader {
  get(path: string): Promise<OutboundActionResponse>;
}
export interface OutboundActionProvider<I, O> {
  readonly name: string;
  readonly configuration: Readonly<Record<string, string | number | boolean>>;
  request(input: I, identity: string): OutboundActionRequest;
  verify(reader: OutboundActionReader): Promise<boolean>;
  decode(
    response: OutboundActionResponse,
    intent: OutboundActionIntent<I>,
  ): OutboundActionObservation<O>;
  reconcile(
    reader: OutboundActionReader,
    record: OutboundActionRecord<I, O>,
  ): Promise<OutboundActionObservation<O>>;
}
export interface OutboundActionDefinition<I, O, A> {
  readonly name: string;
  readonly revision: string;
  readonly scope: PipelineScope;
  readonly integrationId: string;
  readonly relayURL?: string;
  readonly token: string;
  readonly fetch?: typeof fetch;
  readonly store: OutboundActionStore;
  readonly provider: OutboundActionProvider<I, O>;
  readonly toolStore: PipelineToolStore;
  readonly gateStore: PipelineGateStore;
  readonly reviewBaseURL: string;
  readonly requiredRoles: readonly string[];
  readonly approvalTimeoutMs?: number;
  readonly requestTimeoutMs?: number;
  readonly maxReads?: number;
  readonly authorize: (
    request: { readonly request_id: string; readonly input: I },
    access: A,
    operation: OutboundActionOperation,
    signal: AbortSignal,
  ) => Promise<OutboundActionActor | null>;
  readonly authorizeExecution: (
    intent: OutboundActionIntent<I>,
    runId: string,
    signal: AbortSignal,
  ) => Promise<boolean>;
}
export interface OutboundAction<I, O, A> {
  readonly contract: JobContract<
    OutboundActionReference,
    OutboundActionResult<O>
  >;
  readonly reviewContract: JobContract<
    OutboundActionReview<I>,
    { reason: string }
  >;
  readonly definition: WorkflowDefinition;
  readonly tool: PipelineTool<
    OutboundActionReference,
    OutboundActionResult<O>,
    A
  >;
  readonly businessContract: JobContract<I, O>;
  prepare(
    input: I,
    requestId: string,
    access: A,
    options: PipelineRunOptions,
  ): Promise<OutboundActionView<O>>;
  inspect(
    id: string,
    access: A,
    options: PipelineRunOptions,
  ): Promise<OutboundActionView<O>>;
  reconcile(
    id: string,
    access: A,
    options: PipelineRunOptions,
  ): Promise<OutboundActionView<O>>;
  cancel(
    id: string,
    access: A,
    options: PipelineRunOptions,
  ): Promise<OutboundActionView<O>>;
  /** Backend/Job entry; requires a currently active native action run and exact stored approval. */
  execute(
    id: string,
    runId: string,
    options: PipelineRunOptions,
  ): Promise<OutboundActionView<O>>;
}
export function defineOutboundAction<I, O, A>(
  source: JobContract<I, O>,
  definition: OutboundActionDefinition<I, O, A>,
): OutboundAction<I, O, A> {
  const business = new JobContract<I, O>(source.spec),
    scope = eventPipelineScope(definition.scope),
    d = {
      ...definition,
      scope,
      provider: {
        name: definition.provider.name,
        configuration: jobSnapshot(definition.provider.configuration),
        request: definition.provider.request.bind(definition.provider),
        verify: definition.provider.verify.bind(definition.provider),
        decode: definition.provider.decode.bind(definition.provider),
        reconcile: definition.provider.reconcile.bind(definition.provider),
      },
    };
  const roles = [...new Set(d.requiredRoles)].sort(),
    approvalTimeout = d.approvalTimeoutMs ?? 3600000,
    timeout = d.requestTimeoutMs ?? 10000,
    maxReads = d.maxReads ?? 10;
  let relay: URL;
  try {
    relay = new URL(d.relayURL ?? process.env.GREGALE_OUTBOUND_URL ?? "");
    if (
      !["http:", "https:"].includes(relay.protocol) ||
      relay.username ||
      relay.password ||
      relay.search ||
      relay.hash
    )
      throw Error();
    actionPath(relay.pathname);
  } catch {
    throw new OutboundActionError("invalid_input");
  }
  if (
    !/^[a-z][a-z0-9_]{0,29}$/.test(d.name) ||
    !pipelineName(d.revision) ||
    !pipelineName(d.provider.name) ||
    !fileEventUUID(d.integrationId) ||
    !roles.length ||
    roles.length > 16 ||
    roles.some((r) => !pipelineName(r)) ||
    !Number.isSafeInteger(approvalTimeout) ||
    approvalTimeout < 1 ||
    approvalTimeout > 86400000 ||
    !Number.isSafeInteger(timeout) ||
    timeout < 1 ||
    !Number.isSafeInteger(maxReads) ||
    maxReads < 1 ||
    maxReads > 20
  )
    throw new OutboundActionError("invalid_input");
  const contracts = outboundActionContracts(business, d.name),
    configuration = jobValueHash([
      "gregale.outbound-action-config.v1",
      scope,
      d.name,
      d.revision,
      d.integrationId,
      d.provider.name,
      d.provider.configuration,
      business.sha256,
      roles,
      approvalTimeout,
      maxReads,
    ]),
    base = relay.href.replace(/\/+$/, "") + "/i/" + d.integrationId;
  const client = (owner: string) =>
    createJobPipelineClient({
      baseURL: scope.base_url,
      scope,
      ownerId: owner,
      contract: contracts.root,
      token: d.token,
      ...(d.fetch ? { fetch: d.fetch } : {}),
    });
  async function operation<T>(
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
              throw new OutboundActionError("deadline");
          };
          check();
          const result = await work(signal, check, () => {
            check();
            return Math.max(1, Math.ceil(end - performance.now()));
          });
          check();
          return result;
        },
      );
    } catch (e) {
      if (settings.signal?.aborted) throw new OutboundActionError("cancelled");
      if (e instanceof OutboundActionError) throw e;
      if (e instanceof JobError && e.code === "deadline")
        throw new OutboundActionError("deadline");
      throw new OutboundActionError("provider_failed");
    }
  }
  async function stored<T>(fn: () => Promise<T>, check: () => void) {
    check();
    try {
      const v = await fn();
      check();
      return v;
    } catch (e) {
      check();
      if (e instanceof OutboundActionError) throw e;
      throw new OutboundActionError("store_failed");
    }
  }
  function bound(raw: unknown) {
    const r = parseOutboundActionRecord(raw, business),
      i = r.intent;
    if (
      i.name !== d.name ||
      i.revision !== d.revision ||
      i.config_sha256 !== configuration ||
      jobValueHash(i.scope) !== jobValueHash(scope) ||
      jobValueHash(i.request) !==
        jobValueHash(actionRequest(d.provider.request(i.input, i.identity)))
    )
      throw new OutboundActionError("conflict");
    return r;
  }
  async function load(id: string, signal: AbortSignal, check: () => void) {
    if (!fileEventHash(id)) throw new OutboundActionError("invalid_input");
    const raw = await stored(() => d.store.load(id, signal), check);
    if (raw === null) throw new OutboundActionError("not_found");
    const r = bound(raw);
    if (r.intent.identity !== id) throw new OutboundActionError("conflict");
    return r;
  }
  const view = (r: OutboundActionRecord<I, O>): OutboundActionView<O> =>
    jobSnapshot({ ...actionReference(r), outcome: r.outcome });
  const review = (r: OutboundActionRecord<I, O>): OutboundActionReview<I> => ({
    ...actionReference(r),
    name: d.name,
    revision: d.revision,
    input: r.intent.input,
  });
  async function authorize(
    id: string,
    input: I,
    access: A,
    action: OutboundActionOperation,
    signal: AbortSignal,
    check: () => void,
  ) {
    let a;
    try {
      a = jobSnapshot(
        await d.authorize({ request_id: id, input }, access, action, signal),
      );
    } catch {
      check();
      throw new OutboundActionError("unauthorized");
    }
    check();
    if (!a || !pipelineName(a.actor_id) || !pipelineName(a.owner_id))
      throw new OutboundActionError("unauthorized");
    return a;
  }
  async function owned(
    id: string,
    access: A,
    action: OutboundActionOperation,
    signal: AbortSignal,
    check: () => void,
  ) {
    const r = await load(id, signal, check),
      a = await authorize(
        r.intent.request_id,
        r.intent.input,
        access,
        action,
        signal,
        check,
      );
    if (a.owner_id !== r.intent.owner_id)
      throw new OutboundActionError("unauthorized");
    return r;
  }
  async function permit(
    r: OutboundActionRecord<I, O>,
    runId: string,
    signal: AbortSignal,
    check: () => void,
  ) {
    let allowed = false;
    try {
      allowed = await d.authorizeExecution(r.intent, runId, signal);
    } catch {}
    check();
    if (allowed !== true) throw new OutboundActionError("unauthorized");
  }
  async function native(
    r: OutboundActionRecord<I, O>,
    runId: string,
    signal: AbortSignal,
    check: () => void,
    left: () => number,
  ) {
    await permit(r, runId, signal, check);
    const run = await client(r.intent.owner_id).resumeById(runId, {
        timeoutMs: left(),
        signal,
      }),
      info = await run.inspect({ timeoutMs: left(), signal });
    check();
    if (
      info.cancelled ||
      !["pending", "running", "awaiting_event"].includes(info.status)
    )
      throw new OutboundActionError("execution_inactive");
    return run.checkpoint;
  }
  async function proof(
    r: OutboundActionRecord<I, O>,
    runId: string,
    signal: AbortSignal,
    check: () => void,
    left: () => number,
  ) {
    const checkpoint = await native(r, runId, signal, check, left),
      digest = await stored(
        () => d.store.run(r.intent.identity, runId, signal),
        check,
      );
    if (digest !== checkpoint.input_sha256)
      throw new OutboundActionError("conflict");
    const all = await stored(
      () => d.gateStore.list(runId, r.intent.owner_id, signal),
      check,
    );
    if (all.length > 100) throw new OutboundActionError("limit");
    const gates = all
      .map(parsePipelineGateRecord)
      .filter((g) => g.intent.name === "review");
    if (gates.length !== 1) throw new OutboundActionError("approval_required");
    const g = parsePipelineGateRecord(
        await stored(
          () => d.gateStore.resolve(gates[0]!.intent.id, signal),
          check,
        ),
      ),
      i = g.intent;
    if (
      i.kind !== "approval" ||
      i.workflow_run_id !== runId ||
      i.owner_id !== r.intent.owner_id ||
      jobValueHash(i.scope) !== jobValueHash(scope) ||
      i.pipeline_sha256 !== contracts.root.sha256 ||
      i.input_sha256 !== checkpoint.input_sha256 ||
      i.contract_sha256 !== contracts.review.sha256 ||
      i.timeout_ms !== approvalTimeout ||
      jobValueHash(i.required_roles) !== jobValueHash(roles) ||
      jobValueHash(i.review) !== jobValueHash(review(r)) ||
      g.decision?.state !== "approved"
    )
      throw new OutboundActionError("approval_required");
    contracts.review.output(g.decision.payload);
  }
  function transport(
    signal: AbortSignal,
    check: () => void,
    apiVersion?: string,
  ) {
    let reads = 0;
    const send = async (
      path: string,
      request?: OutboundActionRequest,
      key?: string,
    ): Promise<OutboundActionResponse> => {
      check();
      actionPath(path);
      const response = await (d.fetch ?? fetch)(base + path, {
        method: request ? "POST" : "GET",
        redirect: "error",
        credentials: "omit",
        signal,
        headers: {
          Accept: "application/json",
          ...(apiVersion ? { "Stripe-Version": apiVersion } : {}),
          ...(request
            ? {
                "Content-Type": request.content_type,
                "Idempotency-Key": key!,
                ...(request.api_version
                  ? { "Stripe-Version": request.api_version }
                  : {}),
              }
            : {}),
        },
        ...(request ? { body: request.body } : {}),
      });
      check();
      let bytes = 0;
      const chunks: Uint8Array[] = [];
      const reader = response.body?.getReader();
      if (reader) {
        try {
          for (;;) {
            const next = await reader.read();
            check();
            if (next.done) break;
            bytes += next.value.byteLength;
            if (bytes > 1048576) throw new OutboundActionError("limit");
            chunks.push(next.value);
          }
        } finally {
          await reader.cancel().catch(() => {});
        }
      }
      check();
      let body: unknown;
      try {
        body = JSON.parse(
          new TextDecoder("utf-8", { fatal: true }).decode(
            Buffer.concat(chunks),
          ),
        );
      } catch {
        throw new OutboundActionError("provider_failed");
      }
      return { status: response.status, body };
    };
    return {
      send,
      reader: {
        get: async (path: string) => {
          if (++reads > maxReads) throw new OutboundActionError("limit");
          return send(path);
        },
      },
    };
  }
  async function save(
    r: OutboundActionRecord<I, O>,
    observation: OutboundActionObservation<O>,
    signal: AbortSignal,
    check: () => void,
  ) {
    let o = actionObservation(observation, business);
    if (["prepared", "cancelled"].includes(o.state))
      throw new OutboundActionError("conflict");
    if (
      r.outcome.reference &&
      o.reference &&
      r.outcome.reference !== o.reference
    )
      o = {
        state: "needs_review",
        code: "provider_reference_conflict",
        reference: r.outcome.reference,
      };
    if (
      r.outcome.reference &&
      !o.reference &&
      ["needs_review", "failed"].includes(o.state)
    )
      o = { ...o, reference: r.outcome.reference };
    if (
      (o.state === "unknown" && r.outcome.state === "pending") ||
      jobValueHash(o) === jobValueHash(r.outcome)
    )
      return r;
    const raw = await stored(
      () => d.store.observe(r.intent.identity, r.version, o, signal),
      check,
    );
    return raw === null
      ? await load(r.intent.identity, signal, check)
      : bound(raw);
  }
  let api: OutboundAction<I, O, A>;
  const namespace = jobValueHash(["gregale.outbound-tool.v1", scope, d.name]);
  const tool = definePipelineTool<
    OutboundActionReference,
    OutboundActionResult<O>,
    A
  >(contracts.root, {
    name: "action_" + d.name,
    description: "Review and execute a typed external action.",
    scope,
    store: d.toolStore,
    gateStore: d.gateStore,
    reviewBaseURL: d.reviewBaseURL,
    token: d.token,
    ...(d.fetch ? { fetch: d.fetch } : {}),
    authorize: async (request, access, action, signal) => {
      const check = () => signal.throwIfAborted();
      let r: OutboundActionRecord<I, O>;
      if (request.input) {
        r = await owned(request.input.action_id, access, action, signal, check);
        if (request.input.binding_sha256 !== r.intent.binding_sha256)
          throw new OutboundActionError("conflict");
        await stored(
          () =>
            d.store.bind(
              namespace,
              request.request_id,
              r.intent.identity,
              signal,
            ),
          check,
        );
      } else {
        const id = await stored(
          () => d.store.attempt(namespace, request.request_id, signal),
          check,
        );
        if (id === null) throw new OutboundActionError("not_found");
        r = await owned(id, access, action, signal, check);
      }
      return { subject_id: d.name, owner_id: r.intent.owner_id };
    },
  });
  const definitionWorkflow = defineWorkflow(
    contracts.root.spec.name,
    async (envelope: any, wf) => {
      const ref = contracts.root.input(envelope.input);
      const r = bound(
        await wf.step("action/prepare", () =>
          operation({ timeoutMs: timeout }, async (signal, check, left) => {
            const record = await load(ref.action_id, signal, check);
            if (
              ref.binding_sha256 !== record.intent.binding_sha256 ||
              envelope.owner_id !== record.intent.owner_id
            )
              throw new OutboundActionError("conflict");
            const checkpoint = await native(
              record,
              wf.runId,
              signal,
              check,
              left,
            );
            if (checkpoint.input_sha256 !== jobValueHash(envelope))
              throw new OutboundActionError("conflict");
            await stored(
              () =>
                d.store.registerRun(
                  record.intent.identity,
                  wf.runId,
                  checkpoint.input_sha256,
                  signal,
                ),
              check,
            );
            return record;
          }),
        ),
      );
      const pipeline = defineJobPipeline(
        contracts.root,
        {
          scope,
          jobs: {},
          store: {} as PipelineJobStore,
          gateStore: d.gateStore,
          requestTimeoutMs: timeout,
          maxJournalBytes: 262144,
        },
        async (_ref, context) => {
          const decision = await context.approval(
            "review",
            contracts.review,
            review(r),
            { timeoutMs: approvalTimeout, requiredRoles: roles },
          );
          const action =
            decision.state === "approved"
              ? await wf.step(
                  "action/dispatch",
                  () =>
                    api.execute(ref.action_id, wf.runId, {
                      timeoutMs: timeout,
                    }),
                  { retry: { maxAttempts: 1 } },
                )
              : view(r);
          return {
            decision: decision.state as OutboundActionResult<O>["decision"],
            action,
          };
        },
      );
      return pipeline.definition.run(envelope, wf);
    },
  );
  api = {
    businessContract: business,
    contract: contracts.root,
    reviewContract: contracts.review,
    definition: definitionWorkflow,
    tool,
    prepare(input, id, access, settings) {
      return operation(settings, async (signal, check) => {
        if (!fileEventUUID(id)) throw new OutboundActionError("invalid_input");
        const captured = business.input(input);
        if (Buffer.byteLength(JSON.stringify(captured)) > 8192)
          throw new OutboundActionError("limit");
        const a = await authorize(
            id,
            captured,
            access,
            "prepare",
            signal,
            check,
          ),
          identity = outboundActionIdentity(scope, d.name, id),
          baseIntent = {
            version: 1 as const,
            identity,
            scope,
            name: d.name,
            revision: d.revision,
            config_sha256: configuration,
            owner_id: a.owner_id,
            request_id: id,
            input: captured,
            input_sha256: jobValueHash(captured),
            request: actionRequest(d.provider.request(captured, identity)),
          },
          intent = { ...baseIntent, binding_sha256: jobValueHash(baseIntent) };
        const r = bound(
          await stored(() => d.store.reserve(intent, signal), check),
        );
        if (jobValueHash(r.intent) !== jobValueHash(intent))
          throw new OutboundActionError("conflict");
        return view(r);
      });
    },
    inspect(id, access, settings) {
      return operation(settings, async (signal, check) =>
        view(await owned(id, access, "inspect", signal, check)),
      );
    },
    cancel(id, access, settings) {
      return operation(settings, async (signal, check) => {
        const r = await owned(id, access, "cancel", signal, check);
        if (r.outcome.state !== "prepared") return view(r);
        const raw = await stored(
          () => d.store.cancel(id, r.version, signal),
          check,
        );
        return view(raw === null ? await load(id, signal, check) : bound(raw));
      });
    },
    reconcile(id, access, settings) {
      return operation(settings, async (signal, check) => {
        const r = await owned(id, access, "reconcile", signal, check);
        if (!["unknown", "pending"].includes(r.outcome.state)) return view(r);
        const t = transport(signal, check, r.intent.request.api_version);
        if (!(await d.provider.verify(t.reader)))
          throw new OutboundActionError("conflict");
        check();
        const o = await d.provider.reconcile(t.reader, r);
        check();
        await owned(id, access, "reconcile", signal, check);
        return view(await save(r, o, signal, check));
      });
    },
    async execute(id, runId, settings) {
      let dispatched: OutboundActionRecord<I, O> | undefined;
      try {
        return await operation(settings, async (signal, check, left) => {
          let r = await load(id, signal, check);
          await proof(r, runId, signal, check, left);
          if (r.outcome.state !== "prepared") return view(r);
          const t = transport(signal, check, r.intent.request.api_version);
          if (!(await d.provider.verify(t.reader)))
            throw new OutboundActionError("conflict");
          await proof(r, runId, signal, check, left);
          const raw = await stored(
            () => d.store.claim(id, r.version, signal),
            check,
          );
          if (raw === null) return view(await load(id, signal, check));
          r = bound(raw);
          if (r.outcome.state !== "unknown" || r.version !== 1)
            throw new OutboundActionError("conflict");
          dispatched = r;
          await proof(r, runId, signal, check, left);
          const response = await t.send(
            r.intent.request.path,
            r.intent.request,
            "gregale-action:" + id,
          );
          check();
          return view(
            await save(r, d.provider.decode(response, r.intent), signal, check),
          );
        });
      } catch (e) {
        if (dispatched) return view(dispatched);
        throw e;
      }
    },
  };
  return Object.freeze(api);
}
