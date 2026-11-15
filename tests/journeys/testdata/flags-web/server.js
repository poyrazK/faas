// Flags service of TestFeatureFlagsJourney (tests/journeys). It evaluates
// flags with the Node runtime SDK, which the journey copies to
// vendor/flags.js from sdk/node/dist before it deploys. Zero dependencies.
//   GET /healthz                      readiness, no flag check
//   GET /check?flag=KEY[&plan=VALUE]  boolean decision for KEY with
//                                     fallback false; plan, when present,
//                                     is the app.plan attribute
// The decision is the SDK's: value, reason, rule_id and config_version.
// `kills` echoes the X-Faas-Flag-Kills header the gateway stamped, so the
// journey can tell a kill delivered by the gateway from one picked up on a
// refresh.

import http from "node:http";
import { GregaleFlags } from "./vendor/flags.js";

const apiURL = process.env.GREGALE_API_URL;
const flags = apiURL ? new GregaleFlags({ apiURL }) : null;
if (flags) {
  await flags.start().catch((error) => console.error("flags start failed:", error?.message ?? error));
}

function send(res, status, body) {
  res.writeHead(status, { "content-type": "application/json", "cache-control": "no-store" });
  res.end(JSON.stringify(body));
}

const server = http.createServer(async (req, res) => {
  const url = new URL(req.url, "http://localhost");
  if (url.pathname === "/healthz") {
    return send(res, 200, { ok: true });
  }
  if (url.pathname !== "/check") {
    return send(res, 404, { error: "not found" });
  }
  if (!flags) {
    return send(res, 500, { error: "GREGALE_API_URL is not set" });
  }
  const key = url.searchParams.get("flag") ?? "";
  const plan = url.searchParams.get("plan");
  try {
    const decision = await flags.runRequest(req.headers, () =>
      plan === null
        ? flags.boolean(key, false)
        : flags.withAttributes({ plan }, () => flags.boolean(key, false)),
    );
    return send(res, 200, { ...decision, kills: req.headers["x-faas-flag-kills"] ?? "" });
  } catch (error) {
    return send(res, 500, { error: String(error?.message ?? error) });
  }
});

server.listen(Number(process.env.PORT || 8080), "0.0.0.0");
