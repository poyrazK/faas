// Public service of the multi-service journey (tests/journeys,
// TestMultiServiceProject). The project runs this code as three services
// (web, ops and batch), each declaring one dependency, and calls siblings the
// ways the multi-service guide documents. Targets are never a URL the caller
// of this app chooses: they are the injected dependency, or a sibling of the
// same project named by its role.
//   GET /                         {"service":"web"}
//   GET /whoami                   {"service":"web"}
//   GET /healthz                  readiness
//   GET /call?via=env             GREGALE_SERVICE_<DEP>_URL + /whoami
//   GET /call?via=dns             http://<dep>.internal:10081/whoami
//   GET /call?via=https           https://<dep>.internal/whoami (private TLS)
//   GET /call?...&service=<role>  the same, to the sibling <run>-<role>
//   GET /dependency               the injected variable's name and value,
//                                 and whether private HTTPS trust is set up

import http from "node:http";

// The one injected service URL: GREGALE_SERVICE_<NAME>_URL, not the HTTPS
// alias. Service names carry the run id, so match any name.
function dependency() {
  const keys = Object.keys(process.env).filter(
    (k) => /^GREGALE_SERVICE_[A-Z0-9_]+_URL$/.test(k) && !k.endsWith("_HTTPS_URL"),
  );
  if (keys.length !== 1) {
    return { keys };
  }
  return { keys, key: keys[0], url: process.env[keys[0]] };
}

function json(res, status, body) {
  res.writeHead(status, { "content-type": "application/json", "cache-control": "no-store" });
  res.end(JSON.stringify(body));
}

// target is the URL for a call: the dependency's service name, or the
// sibling with the same run prefix and another role.
function target(dep, via, role) {
  const name = new URL(dep.url).hostname.replace(/\.svc\.gregale$/, "");
  const service = role ? name.replace(/-[a-z]+$/, "-" + role) : name;
  switch (via) {
    case "dns":
      return `http://${service}.internal:10081/whoami`;
    case "https":
      return `https://${service}.internal/whoami`;
    default:
      return service === name ? dep.url.replace(/\/+$/, "") + "/whoami" : `http://${service}.svc.gregale:10081/whoami`;
  }
}

async function call(via, role) {
  const dep = dependency();
  if (!dep.url) {
    return { status: 500, body: { error: "expected one injected service URL", keys: dep.keys } };
  }
  const url = target(dep, via, role);
  const started = Date.now();
  try {
    // Well under the edge's request timeout, so a hung call reports its
    // cause instead of the edge answering 502.
    const r = await fetch(url, { signal: AbortSignal.timeout(8000) });
    const text = await r.text();
    let body;
    try {
      body = JSON.parse(text);
    } catch {
      body = { raw: text.slice(0, 300) };
    }
    return { status: 200, body: { via, target: url, status: r.status, ms: Date.now() - started, body } };
  } catch (err) {
    return { status: 502, body: { via, target: url, error: String(err), cause: String(err.cause || ""), ms: Date.now() - started } };
  }
}

const server = http.createServer(async (req, res) => {
  const url = new URL(req.url, "http://web.invalid");
  switch (url.pathname) {
    case "/":
    case "/whoami":
      return json(res, 200, { service: "web" });
    case "/healthz":
      return json(res, 200, { ok: true });
    case "/dependency": {
      const dep = dependency();
      // ca_bundle: guest-init exports GREGALE_SERVICE_CA_BUNDLE only where
      // the private HTTPS listener is enabled.
      return json(res, 200, { keys: dep.keys, url: dep.url || "", ca_bundle: Boolean(process.env.GREGALE_SERVICE_CA_BUNDLE) });
    }
    case "/call": {
      const via = ["dns", "https"].includes(url.searchParams.get("via")) ? url.searchParams.get("via") : "env";
      const role = url.searchParams.get("service") || "";
      if (role && !/^[a-z]{1,16}$/.test(role)) {
        return json(res, 400, { error: "service must be a role such as api or web" });
      }
      const out = await call(via, role);
      return json(res, out.status, out.body);
    }
    default:
      return json(res, 404, { error: "not found" });
  }
});

server.listen(Number(process.env.PORT) || 8080, () => {
  console.log("gregale-e2e-shop-web listening");
});
