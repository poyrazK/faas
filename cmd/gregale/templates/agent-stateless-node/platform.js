// Callback URLs (ADR-20261030125501 §3). The app asks Gregale for them with
// its own workload-identity token, so it holds no account API key. A URL
// carries only one thread's tool results into agent-inbox, and expires.
const audience = "gregale:agents";

export class GregalePlatform {
  constructor({ apiURL = process.env.GREGALE_API_URL || "https://api.gregale.dev",
    identityURL = process.env.FAAS_WORKLOAD_IDENTITY_ENDPOINT, fetchImpl = fetch } = {}) {
    this.apiURL = apiURL.replace(/\/+$/, "");
    this.identityURL = identityURL;
    this.fetch = fetchImpl;
  }

  async token() {
    if (!this.identityURL) throw new Error("FAAS_WORKLOAD_IDENTITY_ENDPOINT is not set; run this on Gregale");
    const url = new URL(this.identityURL);
    url.searchParams.set("audience", audience);
    const res = await this.fetch(url, { signal: AbortSignal.timeout(5_000) });
    if (!res.ok) throw new Error(`workload identity: HTTP ${res.status}`);
    return (await res.json()).access_token;
  }

  async call(path, body, { timeoutMS = 10_000 } = {}) {
    const res = await this.fetch(`${this.apiURL}${path}`, {
      method: "POST",
      headers: { authorization: `Bearer ${await this.token()}`, "content-type": "application/json" },
      body: JSON.stringify(body),
      signal: AbortSignal.timeout(timeoutMS),
    });
    if (!res.ok) throw new Error(`${path}: HTTP ${res.status}`);
    const text = await res.text();
    return text ? JSON.parse(text) : undefined;
  }

  // issueCallback returns { url, expires_at } for one thread's tool results.
  issueCallback(endpointID, threadID) {
    return this.call("/v1/agent-callbacks", { endpoint_id: endpointID, thread_id: threadID, kinds: ["tool_result"] });
  }

  // revokeCallbacks stops every URL issued for the thread so far.
  revokeCallbacks(endpointID, threadID) {
    return this.call("/v1/agent-callbacks/revocations", { endpoint_id: endpointID, thread_id: threadID });
  }

  // App-scoped reads only reach invocations linked to this app's endpoint.
  deliverRunArtifact(app, route, request) {
    return this.call(`/v1/apps/${encodeURIComponent(app)}/run-artifact-deliveries/${encodeURIComponent(route)}`, request, { timeoutMS: 120_000 });
  }
  async artifactDelivery(app, route, id) {
    const response = await this.taskRead(`/v1/apps/${encodeURIComponent(app)}/run-artifact-deliveries/${encodeURIComponent(route)}/${encodeURIComponent(id)}`);
    return response.json();
  }
  signArtifactDelivery(app, route, id) {
    return this.call(`/v1/apps/${encodeURIComponent(app)}/run-artifact-deliveries/${encodeURIComponent(route)}/${encodeURIComponent(id)}/download-url`);
  }
  async pipelineEvents(pipelineID, after = 0) {
    if (!Number.isSafeInteger(after) || after < 0) throw new Error("Invalid pipeline cursor");
    const res = await this.taskRead(`/v1/agent-tools/pipelines/${encodeURIComponent(pipelineID)}/events?after=${after}`);
    return res.json();
  }
  async pipelineReceipt(pipelineID) {
    const res = await this.taskRead(`/v1/agent-tools/pipelines/${encodeURIComponent(pipelineID)}`);
    return res.json();
  }
  async taskReceipt(executionID) {
    const res = await this.taskRead(`/v1/agent-tools/runs/${encodeURIComponent(executionID)}`);
    return res.json();
  }
  async taskArtifact(executionID, name) {
    if (name.split("/").some(part => !part || part === "." || part === "..")) throw new Error("invalid output path");
    return this.taskRead(`/v1/agent-tools/runs/${encodeURIComponent(executionID)}/artifacts/${name.split("/").map(encodeURIComponent).join("/")}`);
  }
  async taskRead(path) {
    const res = await this.fetch(`${this.apiURL}${path}`, {
      headers: { authorization: `Bearer ${await this.token()}` }, redirect: "error",
      signal: AbortSignal.timeout(30_000),
    });
    if (!res.ok) throw new Error(`${path}: HTTP ${res.status}`);
    return res;
  }
}
