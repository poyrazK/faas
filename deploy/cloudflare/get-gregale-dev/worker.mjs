// Serves the gregale CLI installer at https://get.gregale.dev (ADR-172).
//
// Why a Worker and not a Gregale app: this is the install path for Gregale's
// own CLI. Serving it from the platform would mean that when the platform is
// down, nobody can install the tool they need to debug it. Keep this channel
// independent of our own uptime.
//
// Why it proxies a pinned tag rather than embedding the script: the
// repository stays the single source of truth, and pinning to a tag (never
// main) means a merge cannot change what `curl | sh` executes on other
// people's machines. Bump INSTALLER_REF deliberately, as a release step.
//
// INSTALLER_SHA256 is verified before anything is served, which is what
// makes the pinned ref meaningful — a force-pushed tag would otherwise still
// be served verbatim. Any failure returns 503 with no body: a partial or
// unexpected response piped into `sh` is worse than a clean curl -f failure.

const UPSTREAM = (ref) =>
  `https://raw.githubusercontent.com/poyrazK/faas/${ref}/scripts/install.sh`;

const SCRIPT_PATHS = new Set(["/", "/install.sh", "/install", "/sh"]);

async function sha256Hex(buffer) {
  const digest = await crypto.subtle.digest("SHA-256", buffer);
  return Array.from(new Uint8Array(digest))
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");
}

async function loadInstaller(env, fetchImpl) {
  const ref = env.INSTALLER_REF;
  if (!ref || ref.startsWith("REPLACE_ME")) {
    throw new Error("INSTALLER_REF is not pinned");
  }
  const expected = env.INSTALLER_SHA256;
  if (!expected || expected.startsWith("REPLACE_ME")) {
    throw new Error("INSTALLER_SHA256 is not pinned");
  }

  const upstream = await fetchImpl(UPSTREAM(ref), {
    // The ref is an immutable tag, so a long edge TTL is safe and keeps an
    // install burst off GitHub.
    cf: { cacheTtl: 3600, cacheEverything: true },
    headers: { "user-agent": "get.gregale.dev installer proxy" },
  });
  if (!upstream.ok) {
    throw new Error(`upstream ${upstream.status} for ref ${ref}`);
  }

  const body = await upstream.arrayBuffer();
  const actual = await sha256Hex(body);
  if (actual !== expected.toLowerCase()) {
    throw new Error(
      `installer digest mismatch for ref ${ref}: expected ${expected}, got ${actual}`,
    );
  }
  return body;
}

export async function handleRequest(request, env, fetchImpl = fetch) {
  const url = new URL(request.url);

  if (request.method !== "GET" && request.method !== "HEAD") {
    return new Response("method not allowed\n", {
      status: 405,
      headers: { allow: "GET, HEAD", "content-type": "text/plain" },
    });
  }

  if (!SCRIPT_PATHS.has(url.pathname)) {
    return new Response(
      "not found\n\nThe gregale installer is served from the root:\n" +
        "  curl -fsSL https://get.gregale.dev | sh\n",
      { status: 404, headers: { "content-type": "text/plain" } },
    );
  }

  let body;
  try {
    body = await loadInstaller(env, fetchImpl);
  } catch (err) {
    console.error(`installer unavailable: ${err.message}`);
    return new Response(null, { status: 503, headers: { "retry-after": "30" } });
  }

  return new Response(request.method === "HEAD" ? null : body, {
    status: 200,
    headers: {
      "content-type": "text/x-shellscript; charset=utf-8",
      "content-length": String(body.byteLength),
      // Short client cache, long edge cache (set above), so a deliberate
      // INSTALLER_REF bump becomes visible within minutes.
      "cache-control": "public, max-age=300",
      "x-installer-ref": env.INSTALLER_REF,
      "x-content-type-options": "nosniff",
    },
  });
}

export default {
  async fetch(request, env) {
    return handleRequest(request, env);
  },
};
