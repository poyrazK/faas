import { createHash } from "node:crypto";
import {
  ProtocolError,
  ResourceNotFoundError,
  ResourceTemplate,
} from "@modelcontextprotocol/server";
import * as z from "zod/v4";
import { verifiedCallerIdentity } from "./auth.js";
import { canonicalJSON } from "./catalog.js";

const PIPELINE_POLL_MS = 2000;
const MAX_WATCHED_PIPELINE_TASKS = 512;

function mergeScopes(existing, added) {
  for (const key of Object.keys(added)) {
    if (existing && Object.hasOwn(existing, key))
      throw new Error("Duplicate toolkit scope policy: " + key);
  }
  return { ...existing, ...added };
}

// Preserve the wire schema while providing the MCP SDK with a validator.
function standardSchema(schema) {
  const validator = z.fromJSONSchema(schema);
  return {
    "~standard": {
      version: 1,
      vendor: "gregale-toolkit",
      validate(value) {
        return validator.safeParse(value).success
          ? { value }
          : {
              issues: [{ message: "Value does not match the toolkit schema" }],
            };
      },
      jsonSchema: { input: () => schema, output: () => schema },
    },
  };
}

export function applicationToolkitRegistration(
  config,
  toolkit,
  resolvePrincipal = (caller) => caller.subject,
  pipelineTasks,
  inputMethodsFor,
) {
  if (config.auth?.mode !== "external-oauth")
    throw new Error("Application toolkits require external-oauth");
  if (typeof resolvePrincipal !== "function")
    throw new TypeError("Expected a toolkit principal resolver");
  const manifest = pipelineTasks?.manifestResource;
  const recovery = pipelineTasks?.recoveryTools;
  const handoffs = pipelineTasks?.handoffTools;
  const handoffOperations = handoffs ? [handoffs.read, handoffs.reply, handoffs.list, handoffs.approval, handoffs.decide].filter(Boolean) : [];
  const handoffScopes = Object.fromEntries(handoffOperations.map(({ name }) => [name, handoffs.scopes]));
  const recoveryScopes = recovery ? {
    [recovery.preview.name]: recovery.scopes,
    [recovery.confirm.name]: recovery.scopes,
  } : {};
  const auth = {
    ...config.auth,
    tool_scopes: mergeScopes(config.auth.tool_scopes, mergeScopes(toolkit.toolScopes, mergeScopes(recoveryScopes, handoffScopes))),
    resource_scopes: mergeScopes(
      config.auth.resource_scopes,
      mergeScopes(toolkit.resourceScopes, manifest ? { [manifest.uri]: config.auth.scopes ?? [] } : {}),
    ),
  };

  async function accessFor(authInfo, signal) {
    const caller = verifiedCallerIdentity(authInfo);
    if (!caller) return { principal: null, scopes: [] };
    const scopes = Object.freeze([...authInfo.scopes]);
    const principal = await resolvePrincipal(
      Object.freeze({ ...caller, scopes }),
      signal,
    );
    return { principal, scopes };
  }

  async function caller(authInfo, signal, capabilities) {
    const identity = verifiedCallerIdentity(authInfo);
    return {
      resource: auth.resource,
      subject: identity?.subject,
      clientId: identity?.clientId,
      access: await accessFor(authInfo, signal),
      supportsForms: inputMethodsFor(capabilities).includes(
        "elicitation/create:form",
      ),
    };
  }

  function taskRuntime(native) {
    if (!pipelineTasks) return native;
    if (
      ["canCreate", "create", "get"].some(
        (name) => typeof pipelineTasks[name] !== "function",
      )
    )
      throw new TypeError("Invalid pipeline Task runtime");
    let watchedPipelineTasks = 0;
    async function getPipelineTask(
      id,
      authInfo,
      capabilities,
      signal,
      timeoutMs,
    ) {
      return pipelineTasks.get(
        id,
        await caller(authInfo, signal, capabilities),
        { timeoutMs, signal },
      );
    }

    async function subscribePipelineTasks(
      ids,
      authInfo,
      capabilities,
      signal,
      onTask,
      onEmpty,
    ) {
      const pending = new Set(ids);
      if (watchedPipelineTasks + pending.size > MAX_WATCHED_PIPELINE_TASKS)
        throw new Error("Pipeline Task subscription capacity reached");
      watchedPipelineTasks += pending.size;
      const fingerprints = new Map();
      let timer;
      const close = () => {
        clearTimeout(timer);
        watchedPipelineTasks -= pending.size;
        pending.clear();
        fingerprints.clear();
        signal.removeEventListener("abort", close);
      };
      signal.addEventListener("abort", close, { once: true });
      function remove(id) {
        if (pending.delete(id)) watchedPipelineTasks--;
        fingerprints.delete(id);
      }
      async function snapshot(id, readSignal) {
        const wire = await getPipelineTask(
          id,
          authInfo,
          capabilities,
          readSignal,
          5000,
        );
        readSignal.throwIfAborted();
        const { resultType, ...detail } = wire;
        const { lastUpdatedAt, ...state } = detail;
        // The SDK timestamps each read, even when the pipeline has not advanced.
        const fingerprint = createHash("sha256").update(canonicalJSON(state)).digest("hex");
        const changed = fingerprints.get(id) !== fingerprint;
        fingerprints.set(id, fingerprint);
        return changed ? detail : null;
      }
      async function poll() {
        for (const id of pending) {
          try {
            const detail = await snapshot(
              id,
              AbortSignal.any([signal, AbortSignal.timeout(5000)]),
            );
            if (!signal.aborted && pending.has(id) && detail) onTask(detail);
          } catch (error) {
            if (error?.code === "not_found") remove(id);
          }
        }
        if (signal.aborted) return;
        if (pending.size) schedule();
        else {
          close();
          onEmpty();
        }
      }
      function schedule() {
        timer = setTimeout(() => {
          void poll();
        }, PIPELINE_POLL_MS);
        timer.unref?.();
      }

      try {
        const setupSignal = AbortSignal.any([
          signal,
          AbortSignal.timeout(30000),
        ]);
        const tasks = [];
        for (const id of pending) {
          setupSignal.throwIfAborted();
          try {
            tasks.push(
              await snapshot(
                id,
                AbortSignal.any([setupSignal, AbortSignal.timeout(5000)]),
              ),
            );
          } catch (error) {
            if (error?.code !== "not_found") throw error;
            remove(id);
          }
        }
        setupSignal.throwIfAborted();
        if (pending.size) schedule();
        else close();
        return { taskIds: [...pending], tasks };
      } catch (error) {
        close();
        throw error;
      }
    }

    return {
      subscribePipelineTasks,
      supportsTaskSubmission: native?.supportsTaskSubmission?.bind(native),
      getBySubmissionKey: native?.getBySubmissionKey?.bind(native),
      supportsTaskGroupOwnerBinding: native?.supportsTaskGroupOwnerBinding?.bind(native),
      taskGroupOwnerBinding: native?.taskGroupOwnerBinding?.bind(native),
      supportsTaskInbox: native?.supportsTaskInbox?.bind(native),
      inbox: native?.inbox?.bind(native),
      supportsReviewedInput: native?.supportsReviewedInput?.bind(native),
      inputReview: native?.inputReview?.bind(native),
      reviewedResponse: native?.reviewedResponse?.bind(native),
      canCreate(name) {
        return pipelineTasks.canCreate(name) || !!native?.canCreate?.(name);
      },
      async create(name, args, authInfo, mode, capabilities, schema, submissionKey, signal) {
        if (!pipelineTasks.canCreate(name)) {
          if (!native)
            throw new ProtocolError(-32601, "Task creation unavailable");
          return native.create(
            name,
            args,
            authInfo,
            mode,
            capabilities,
            schema,
            submissionKey,
          );
        }
        try {
          return await pipelineTasks.create(
            name,
            args,
            await caller(authInfo, signal),
            { timeoutMs: 30000, signal },
          );
        } catch (error) {
          if (error?.code === "invalid_input" || error?.code === -32602)
            throw new ProtocolError(-32602, "Invalid pipeline Task arguments");
          if (error?.code === "not_found")
            throw new ProtocolError(-32002, "Task not found");
          throw new ProtocolError(-32603, "Could not create pipeline Task");
        }
      },
      async get(id, authInfo, mode, canAccessTool, capabilities) {
        const signal = AbortSignal.timeout(30000);
        try {
          return await getPipelineTask(
            id,
            authInfo,
            capabilities,
            signal,
            30000,
          );
        } catch (error) {
          if (error?.code === "not_found") {
            if (native) return native.get(id, authInfo, mode, canAccessTool);
            throw new ProtocolError(-32002, "Task not found");
          }
          throw new ProtocolError(-32603, "Could not read pipeline Task");
        }
      },
      cancel:
        native?.cancel?.bind(native) ??
        (() => {
          throw new ProtocolError(-32002, "Task not found");
        }),
      async update(id, responses, authInfo, mode, canAccessTool) {
        if (typeof pipelineTasks.update === "function") {
          const signal = AbortSignal.timeout(30000);
          try {
            await pipelineTasks.update(id, responses, await caller(authInfo, signal), { timeoutMs: 30000, signal });
            return { resultType: "complete" };
          } catch (error) {
            if (error?.code === "invalid_input")
              throw new ProtocolError(-32602, "Invalid or stale pipeline Task response");
            if (error?.code !== "not_found")
              throw new ProtocolError(-32603, "Could not update pipeline Task");
          }
        }
        if (native) return native.update(id, responses, authInfo, mode, canAccessTool);
        throw new ProtocolError(-32002, "Task not found");
      },
    };
  }

  return {
    config: { ...config, auth },
    taskRuntime,
    install(catalog) {
      if (manifest) catalog.registerResource(
        "pipeline_task_manifest", manifest.uri,
        { mimeType: "application/json", description: "Discover authorized pipeline Task contracts." },
        async (_uri, ctx, { authInfo }) =>
          manifest.read(await accessFor(authInfo, ctx.mcpReq.signal)),
      );
      if (handoffs) {
        for (const { name, inputSchema, outputSchema, ...definition } of handoffOperations) {
          catalog.registerTool(name, { ...definition, inputSchema: standardSchema(inputSchema),
            ...(outputSchema && { outputSchema: standardSchema(outputSchema) }) },
            async (args, ctx, { authInfo }) => {
              const signal = ctx.mcpReq.signal;
              try {
                return await pipelineTasks.callHandoff(name, args, await caller(authInfo, signal), { timeoutMs: 30000, signal });
              } catch (error) {
                const code = ["not_found", "invalid_input", "deadline"].includes(error?.code) ? error.code : "request_failed";
                return { isError: true, content: [{ type: "text", text: JSON.stringify({ code }) }] };
              }
            },
          );
        }
      }
      if (recovery) {
        const { name: preview, inputSchema: previewInput, ...previewDef } = recovery.preview;
        const { name: confirm, inputSchema: confirmInput, ...confirmDef } = recovery.confirm;
        catalog.registerTool(preview, { ...previewDef, inputSchema: standardSchema(previewInput) },
          async (args, ctx, { authInfo }) => {
            const signal = ctx.mcpReq.signal;
            try {
              return await pipelineTasks.previewRecovery(args, await caller(authInfo, signal), { timeoutMs: 30000, signal });
            } catch (error) {
              if (error?.code === "invalid_input")
                throw new ProtocolError(-32602, "Invalid pipeline recovery arguments");
              return { isError: true, content: [{ type: "text", text: JSON.stringify({
                code: error?.code === "not_found" ? "not_found" : "request_failed",
              }) }] };
            }
          },
        );
        catalog.registerTool(confirm, { ...confirmDef, inputSchema: standardSchema(confirmInput) },
          async () => ({ isError: true, content: [{ type: "text", text: '{"code":"task_mode_required"}' }] }),
        );
      }
      for (const {
        name,
        inputSchema,
        outputSchema,
        ...definition
      } of toolkit.tools) {
        catalog.registerTool(
          name,
          {
            ...definition,
            inputSchema: standardSchema(inputSchema),
            outputSchema: standardSchema(outputSchema),
          },
          async (args, ctx, { authInfo }) => {
            const signal = ctx.mcpReq.signal;
            try {
              const access = await accessFor(authInfo, signal);
              return await toolkit.call(name, args, access, {
                timeoutMs: 30000,
                signal,
              });
            } catch (error) {
              if (error?.code === -32602)
                throw new ProtocolError(-32602, "Invalid toolkit arguments");
              const code =
                error?.code === "tool_not_found"
                  ? "tool_not_found"
                  : error?.code === "limit"
                    ? "limit"
                    : "request_failed";
              return {
                isError: true,
                content: [{ type: "text", text: JSON.stringify({ code }) }],
              };
            }
          },
        );
      }
      for (const {
        name,
        uriTemplate,
        ...definition
      } of toolkit.resourceTemplates) {
        catalog.registerResource(
          name,
          new ResourceTemplate(uriTemplate, { list: undefined }),
          definition,
          async (uri, _variables, ctx, { authInfo }) => {
            const signal = ctx.mcpReq.signal;
            try {
              const access = await accessFor(authInfo, signal);
              return await toolkit.readResource(uri.href, access, {
                timeoutMs: 30000,
                signal,
              });
            } catch (error) {
              if (
                error?.code === "resource_not_found" ||
                error?.code === -32002
              )
                throw new ResourceNotFoundError(uri.href);
              throw new ProtocolError(-32603, "Toolkit resource read failed");
            }
          },
        );
      }
    },
  };
}
