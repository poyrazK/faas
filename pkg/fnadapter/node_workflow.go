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
let workflowSDK;
async function loadWorkflowSDK() {
  if (!workflowSDK) workflowSDK = import("@gregale/sdk-node/workflows");
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
      type: "https://gregale.dev/docs/errors#workflow_sdk_missing", title: "Workflow SDK missing", status: 500,
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
