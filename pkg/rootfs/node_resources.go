package rootfs

import (
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/functionlifetime"
)

// This bridge exists only when trusted runner wiring supplies the worker
// argument. SDK copies share it without importing platform/control-plane code.
var nodeFunctionResources = strings.NewReplacer(
	"__RESOURCE_ARGUMENT__", functionlifetime.NodeResourceArgument,
	"__RESOURCE_MAX_POOLS__", strconv.Itoa(api.FunctionPostgresMaxPools),
	"__RESOURCE_BEFORE_PATH__", functionlifetime.BeforeResourcePath,
	"__RESOURCE_AFTER_PATH__", functionlifetime.AfterResourcePath,
).Replace(`
function installPostgresLifecycle() {
  if (!process.argv.slice(2).includes("__RESOURCE_ARGUMENT__")) return undefined;
  const resources = new Set();
  const unavailable = () => new Error("postgres_lifecycle_unavailable");
  let phase = "ready", operation;
  const run = (kind, signal) => {
    if (operation) return operation.kind === kind ? operation.promise : Promise.reject(unavailable());
    if (kind === "before_checkpoint" && phase === "checkpointed") return Promise.resolve();
    phase = kind === "before_checkpoint" ? "checkpointing" : "restoring";
    const method = kind === "before_checkpoint" ? "beforeCheckpoint" : "afterRestore";
    const promise = Promise.allSettled([...resources].map(resource => Promise.resolve().then(() => {
      if (signal.aborted) throw unavailable();
      if (resources.has(resource)) return resource[method]({signal});
    }))).then(results => {
      if (signal.aborted || results.some(result => result.status !== "fulfilled")) {
        phase = "failed"; throw unavailable();
      }
      phase = kind === "before_checkpoint" ? "checkpointed" : "ready";
    }).finally(() => { operation = undefined; });
    operation = {kind, promise};
    return promise;
  };
  const bridge = Object.freeze({version: 1, register(resource) {
    if (phase !== "ready" || resources.size >= __RESOURCE_MAX_POOLS__ || !resource ||
        typeof resource.beforeCheckpoint !== "function" || typeof resource.afterRestore !== "function" ||
        resources.has(resource)) throw unavailable();
    resources.add(resource);
    return () => { resources.delete(resource); };
  }});
  Object.defineProperty(globalThis, Symbol.for("gregale.function.postgres.lifecycle.v1"), {value: bridge});
  return {assertReady() { if (phase !== "ready") throw unavailable(); }, async handle(env, ctx, invoke) {
    const kind = env.lifecycle_hook;
    if (!env.forbid_background || !["before_checkpoint", "after_restore"].includes(kind)) throw unavailable();
    if (kind === "after_restore") await run(kind, ctx.signal);
    const automatic = kind === "before_checkpoint" ? "__RESOURCE_BEFORE_PATH__" : "__RESOURCE_AFTER_PATH__";
    if (env.path !== automatic) await finishCustomerLifecycle(await invoke(), ctx.signal);
    if (kind === "before_checkpoint") await run(kind, ctx.signal);
  }};
}

// Hook response bodies are consumed before resource drainage and host ACK.
// They are internal lifecycle responses and never enter the customer body.
async function finishCustomerLifecycle(value, signal) {
  const status = Number(value?.statusCode ?? value?.status ?? 200);
  const body = value && typeof value === "object" && "body" in value ? value.body : value;
  if (body && typeof body.getReader === "function") {
    const reader = body.getReader();
    let cancelling;
    const abort = () => { cancelling ||= reader.cancel().catch(() => {}); };
    signal.addEventListener("abort", abort, {once: true});
    if (signal.aborted) abort();
    try { while (!(await reader.read()).done) { if (signal.aborted) abort(); } }
    finally {
      signal.removeEventListener("abort", abort);
      if (cancelling) await cancelling;
      reader.releaseLock();
    }
  } else if (isStreamBody(body)) {
    for await (const _piece of body) { if (signal.aborted) throw new Error("postgres_lifecycle_unavailable"); }
  }
  if (signal.aborted || !Number.isInteger(status) || status < 200 || status >= 300) throw new Error("postgres_lifecycle_unavailable");
}
`)
