// The run-handle wire contract the SDKs' start() speaks.
const api = () => process.env.GREGALE_PLATFORM_API_URL + "/v1/workflow-runs";
const relay = async (res) => Response.json({ status: res.status, body: res.status === 202 ? null : await res.json() });
export default {
  async fetch(request) {
    const url = new URL(request.url);
    const id = url.searchParams.get("id") || "";
    const json = { "content-type": "application/json" };
    if (url.pathname === "/start") {
      return relay(await fetch(api(), { method: "POST", headers: { ...json, "idempotency-key": url.searchParams.get("key") || "" },
        body: JSON.stringify({ function: "orders", input: { email: "a@example.com" } }) }));
    }
    if (url.pathname === "/signal") {
      return relay(await fetch(api() + "/" + id + "/events", { method: "POST", headers: json,
        body: JSON.stringify({ event: "approved", payload: { ok: true } }) }));
    }
    if (url.pathname === "/wait") {
      return relay(await fetch(api() + "/" + id + "?wait=20"));
    }
    // Health and the deploy's smoke probe: every other path answers 200.
    // The long-poll fallthrough threw on a missing run id and the smoke
    // failed the deploy with HTTP 500 (2026-11-16 journeys).
    return Response.json({ ok: true });
  },
};
