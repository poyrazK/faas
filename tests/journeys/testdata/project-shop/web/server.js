// Public service of the multi-service journey (tests/journeys,
// TestMultiServiceProject). It calls the project's api service, which has no
// public URL, the two ways the multi-service guide documents. The target is
// never chosen by the caller of this app: it comes from the variable the
// platform injects for the declared dependency.
//   GET /             {"service":"web"}
//   GET /healthz      readiness
//   GET /call?via=env  calls GREGALE_SERVICE_<API>_URL + /whoami
//   GET /call?via=dns  calls http://<api>.internal:10081/whoami
//   GET /dependency    the injected variable's name and value (no secrets)

import http from "node:http";

// The one injected service URL: GREGALE_SERVICE_<NAME>_URL, not the HTTPS
// alias. The api's service name carries the run id, so match by suffix.
function dependency() {
  const keys = Object.keys(process.env).filter(
    (k) => /^GREGALE_SERVICE_[A-Z0-9_]+_API_URL$/.test(k) && !k.endsWith("_HTTPS_URL"),
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

async function call(via) {
  const dep = dependency();
  if (!dep.url) {
    return { status: 500, body: { error: "expected one injected api URL", keys: dep.keys } };
  }
  let target = dep.url.replace(/\/+$/, "") + "/whoami";
  if (via === "dns") {
    const host = new URL(dep.url).hostname.replace(/\.svc\.gregale$/, "");
    target = `http://${host}.internal:10081/whoami`;
  }
  const started = Date.now();
  try {
    const r = await fetch(target, { signal: AbortSignal.timeout(60000) });
    const text = await r.text();
    let body;
    try {
      body = JSON.parse(text);
    } catch {
      body = { raw: text.slice(0, 300) };
    }
    return { status: 200, body: { via, target, status: r.status, ms: Date.now() - started, body } };
  } catch (err) {
    return { status: 502, body: { via, target, error: String(err), ms: Date.now() - started } };
  }
}

const server = http.createServer(async (req, res) => {
  const url = new URL(req.url, "http://web.invalid");
  switch (url.pathname) {
    case "/":
      return json(res, 200, { service: "web" });
    case "/healthz":
      return json(res, 200, { ok: true });
    case "/dependency": {
      const dep = dependency();
      return json(res, 200, { keys: dep.keys, url: dep.url || "" });
    }
    case "/call": {
      const via = url.searchParams.get("via") === "dns" ? "dns" : "env";
      const out = await call(via);
      return json(res, out.status, out.body);
    }
    default:
      return json(res, 404, { error: "not found" });
  }
});

server.listen(Number(process.env.PORT) || 8080, () => {
  console.log("gregale-e2e-shop-web listening");
});
