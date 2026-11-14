// The caller. It speaks the SDK functions client's wire contract directly
// (the SDK is not a fixture dependency): GREGALE_FUNCTIONS names the callee's
// slug, the call goes to its private binding URL, X-Gregale-Function-Call
// marks it for the callee's input check, and X-Gregale-Invoke: async makes it
// a send. Each route reports what the callee or the service proxy answered.
const functions = (() => {
  try {
    return JSON.parse(process.env.GREGALE_FUNCTIONS || "{}");
  } catch {
    return {};
  }
})();

function baseURL(slug) {
  const key = "GREGALE_SERVICE_" + slug.toUpperCase().replace(/[^A-Z0-9]/g, "_") + "_URL";
  return (process.env[key] || `http://${slug}.internal:10081`).replace(/\/+$/, "");
}

async function callUsers(input, send) {
  const slug = functions.users;
  if (!slug) return { status: 0, body: { error: "users is not in GREGALE_FUNCTIONS", functions } };
  const headers = { "content-type": "application/json", accept: "application/json", "X-Gregale-Function-Call": "1" };
  if (send) headers["X-Gregale-Invoke"] = "async";
  const res = await fetch(baseURL(slug) + "/", { method: "POST", headers, body: JSON.stringify(input) });
  const text = await res.text();
  let body = text;
  try {
    body = JSON.parse(text);
  } catch {}
  return { status: res.status, body };
}

export default {
  async fetch(request) {
    const url = new URL(request.url);
    switch (url.pathname) {
      case "/functions":
        return Response.json({ probe: "gregale-e2e-web", functions });
      case "/call":
        return Response.json(await callUsers({ email: url.searchParams.get("email") || "a@example.com" }, false));
      case "/bad":
        return Response.json(await callUsers({ email: "no-at-sign" }, false));
      case "/send":
        return Response.json(await callUsers({ email: url.searchParams.get("email") || "sent@example.com" }, true));
      default:
        return Response.json({ probe: "gregale-e2e-web" });
    }
  },
};
