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

  async call(path, body) {
    const res = await this.fetch(`${this.apiURL}${path}`, {
      method: "POST",
      headers: { authorization: `Bearer ${await this.token()}`, "content-type": "application/json" },
      body: JSON.stringify(body),
      signal: AbortSignal.timeout(10_000),
    });
    if (!res.ok) throw new Error(`${path}: HTTP ${res.status}`);
    return res.json();
  }

  // issueCallback returns { url, expires_at } for one thread's tool results.
  issueCallback(endpointID, threadID) {
    return this.call("/v1/agent-callbacks", { endpoint_id: endpointID, thread_id: threadID, kinds: ["tool_result"] });
  }

  // revokeCallbacks stops every URL issued for the thread so far.
  revokeCallbacks(endpointID, threadID) {
    return this.call("/v1/agent-callbacks/revocations", { endpoint_id: endpointID, thread_id: threadID });
  }
}
