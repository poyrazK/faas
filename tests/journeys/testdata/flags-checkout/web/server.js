// The checkout probe of the Flags journey (tests/journeys,
// TestFlagsAndExperiments). It uses the repository's Node flags SDK source,
// which the journey copies in as gregale-flags.mts (Node 24 strips its
// types), and the API URL the journey writes to config.json.
//
//   GET /                    {"probe", "customer"}: the verified customer
//   GET /debug               why the SDK has no configuration: the refresh
//                            error and the identity and runtime statuses
//   GET /checkout            checks the "checkout" variant flag, marks it used
//   GET /checkout?convert=1  also tracks an order_completed conversion
//
// /checkout answers the served variant, its reason and the verified customer,
// and returns the request's flag evidence so the gateway records the exposure.
import http from "node:http";
import { readFileSync } from "node:fs";
import { GregaleFlags, GREGALE_FLAG_EVIDENCE_HEADER, GREGALE_FLAG_SDK_CAPABILITIES } from "./gregale-flags.mts";

const config = JSON.parse(readFileSync(new URL("./config.json", import.meta.url), "utf8"));
const flags = new GregaleFlags({ apiURL: config.apiURL, refreshMs: 2000, maxStaleMs: 10000 });
await flags.start();

// debug reports what the SDK's refresh sees, without the token.
async function debug() {
  const out = { identity_endpoint: Boolean(process.env.FAAS_WORKLOAD_IDENTITY_ENDPOINT) };
  try {
    await flags.refresh();
    out.refresh = "ok";
  } catch (err) {
    out.refresh = String(err && err.message ? err.message : err);
  }
  try {
    const id = new URL(process.env.FAAS_WORKLOAD_IDENTITY_ENDPOINT || "http://127.0.0.1:1/");
    id.searchParams.set("audience", "gregale:flags");
    const ir = await fetch(id, { signal: AbortSignal.timeout(3000) });
    out.identity_status = ir.status;
    const token = ir.ok ? (await ir.json()).access_token : "";
    if (!ir.ok) out.identity_body = (await ir.text()).slice(0, 300);
    if (token) {
      const fr = await fetch(new URL("/v1/runtime/flags", config.apiURL), {
        signal: AbortSignal.timeout(5000),
        headers: { Authorization: `Bearer ${token}`, "X-Faas-Flags-Capabilities": GREGALE_FLAG_SDK_CAPABILITIES },
      });
      out.runtime_status = fr.status;
      out.runtime_body = (await fr.text()).slice(0, 400);
    }
  } catch (err) {
    out.probe_error = String(err && err.message ? err.message : err);
  }
  return out;
}

function send(res, status, body, headers = {}) {
  res.writeHead(status, { "content-type": "application/json", ...headers });
  res.end(JSON.stringify(body));
}

http
  .createServer((req, res) => {
    const url = new URL(req.url, "http://localhost");
    const customer = req.headers["x-faas-platform-tenant-id"] || "";
    if (url.pathname === "/debug") {
      debug().then((body) => send(res, 200, body), (err) => send(res, 500, { error: String(err) }));
      return;
    }
    // Edge routing (ADR-20261124212701): two plain pages the gateway picks
    // between; the code never checks a flag here.
    if (url.pathname === "/page" || url.pathname === "/page-b") {
      send(res, 200, { probe: "gregale-e2e-flags", customer, page: url.pathname === "/page" ? "a" : "b" });
      return;
    }
    if (url.pathname !== "/checkout") {
      send(res, 200, { probe: "gregale-e2e-flags", customer });
      return;
    }
    flags
      .runRequest(req.headers, async () => {
        const decision = flags.variant("checkout", "fallback");
        flags.used("checkout");
        const convert = url.searchParams.get("convert") === "1";
        const tracked = convert ? flags.track("order_completed", { value: 1200 }) : false;
        await flags.flush();
        send(
          res,
          200,
          {
            probe: "gregale-e2e-flags",
            customer,
            variant: decision.value,
            reason: decision.reason,
            rule_id: decision.rule_id || "",
            config_version: decision.config_version,
            tracked,
            dropped: flags.droppedEvents(),
          },
          { [GREGALE_FLAG_EVIDENCE_HEADER]: flags.responseEvidence() },
        );
      })
      .catch((err) => send(res, 500, { probe: "gregale-e2e-flags", error: String(err) }));
  })
  .listen(Number(process.env.PORT) || 8080);
