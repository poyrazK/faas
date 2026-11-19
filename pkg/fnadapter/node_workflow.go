package fnadapter

// NodeWorkflow serves a workflow function (ADR-20261113120824 §1): an engine
// call (X-Faas-Workflow-Mode: code) runs the handler, or a defineWorkflow
// default export, under the protocol core of the @gregale/sdk-node in the
// function's dependencies. The SDK checks the engine's headers.
const NodeWorkflow = `
const isWorkflowCall = (headers) => {
  for (const [key, value] of Object.entries(headers || {})) {
    if (key.toLowerCase() === "x-faas-workflow-mode") return value === "code" || (Array.isArray(value) && value[0] === "code");
  }
  return false;
};
// A workflow function (FAAS_FUNCTION_WORKFLOW=1, stamped by imaged) runs
// only through workflow runs. Any other request, such as a browser GET, the
// deploy's smoke probe or a sibling's call(), gets a 404 problem instead of
// handler(event, ctx), which would reach wf.call on the ctx and answer 500
// (2026-11-16 prod journeys).
const isWorkflowFunction = process.env.FAAS_FUNCTION_WORKFLOW === "1";
function emitWorkflowFunctionPlainCall(out) {
  emitWhole(out, 404, { "content-type": "application/problem+json" }, Buffer.from(JSON.stringify({
    type: "https://gregale.dev/docs/error-codes#workflow_function_not_invocable",
    title: "Workflow function",
    status: 404,
    code: "workflow_function_not_invocable",
    detail: "this function is a workflow: start a run with functions.<name>.start() or gregale workflow run",
    docs_url: "https://gregale.dev/docs/error-codes#workflow_function_not_invocable",
  })));
}
let workflowSDK;
// The SDK is the function's dependency: resolve it beside the adapter (a
// deployed function), else from the handler's directory upward (the adapter
// gregale dev --local writes lives outside the function's tree).
async function importWorkflowSDKFromHandler() {
  const fs = await import("node:fs");
  for (let dir = path.dirname(handlerFile); ; dir = path.dirname(dir)) {
    const root = path.join(dir, "node_modules", "@gregale", "sdk-node");
    try {
      const pkg = JSON.parse(fs.readFileSync(path.join(root, "package.json"), "utf8"));
      const entry = pkg.exports && pkg.exports["./workflows"];
      const rel = typeof entry === "string" ? entry : entry && (entry.import || entry.default);
      if (rel) return import(urlModule.pathToFileURL(path.join(root, rel)).href);
    } catch (_) {}
    if (path.dirname(dir) === dir) throw new Error("@gregale/sdk-node is not installed");
  }
}
async function loadWorkflowSDK() {
  if (!workflowSDK) workflowSDK = import("@gregale/sdk-node/workflows").catch(() => importWorkflowSDKFromHandler());
  return workflowSDK;
}
function workflowDefinition(sdk, name) {
  if (defaultExport && typeof defaultExport === "object" && typeof defaultExport.run === "function" && typeof defaultExport.name === "string") {
    return defaultExport;
  }
  if (typeof eventHandler !== "function") throw new Error("a workflow function exports its handler(input, wf) or a defineWorkflow default");
  return sdk.defineWorkflow(name, (input, wf) => eventHandler(input, wf));
}
async function serveWorkflowCall(env, out) {
  let sdk;
  try {
    sdk = await loadWorkflowSDK();
  } catch (err) {
    workflowSDK = undefined;
    emitWhole(out, 500, { "content-type": "application/problem+json" }, Buffer.from(JSON.stringify({
      type: "https://gregale.dev/docs/error-codes#workflow_sdk_missing", title: "Workflow SDK missing", status: 500,
      code: "workflow_sdk_missing", detail: "a workflow function needs @gregale/sdk-node in its dependencies",
    })));
    return;
  }
  const bytes = Buffer.from(env.body_b64 || "", "base64");
  let name = "";
  try { name = String(JSON.parse(bytes.toString("utf8")).workflow || ""); } catch (_) {}
  const definition = workflowDefinition(sdk, name);
  const request = new Request(new URL(env.path || "/", "http://faas.local"),
    { method: env.method || "POST", headers: env.headers || {}, body: bytes.length > 0 ? bytes : undefined });
  const response = await inGregaleRequestContext(env.headers || {}, () => sdk.serveWorkflows([definition])(request));
  const headers = {};
  for (const [key, item] of response.headers.entries()) headers[key] = item;
  emitWhole(out, response.status, headers, Buffer.from(await response.arrayBuffer()));
}
`
