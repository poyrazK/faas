const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const tenantHeader = "x-faas-platform-tenant-id";

class RequestError extends Error {
  constructor(status, message) { super(message); this.status = status; }
}

function json(response, status, body) {
  response.writeHead(status, { "content-type": "application/json", "cache-control": "no-store" });
  response.end(JSON.stringify(body));
}

async function documentBody(request) {
  if (request.headers["content-type"]?.split(";")[0].trim() !== "application/json") {
    throw new RequestError(415, "Use application/json");
  }
  let size = 0;
  const chunks = [];
  for await (const chunk of request) {
    size += chunk.length;
    if (size > 20 * 1024) throw new RequestError(413, "Document body is too large");
    chunks.push(chunk);
  }
  let body;
  try { body = JSON.parse(Buffer.concat(chunks).toString("utf8")); }
  catch { throw new RequestError(400, "Invalid JSON"); }
  if (!body || typeof body !== "object" || Array.isArray(body) ||
      Object.keys(body).some((key) => key !== "title" && key !== "content") ||
      typeof body.title !== "string" || !body.title.trim() || [...body.title].length > 200 ||
      typeof body.content !== "string" || Buffer.byteLength(body.content) > 16384) {
    throw new RequestError(400, "Expected title (1–200 characters) and content (up to 16 KiB)");
  }
  return { title: body.title, content: body.content };
}

async function documents(request, response, store, tenantID, path) {
  if (path === "/documents" && request.method === "GET") {
    return json(response, 200, { documents: await store.list(tenantID) });
  }
  if (path === "/documents" && request.method === "POST") {
    return json(response, 201, await store.create(tenantID, await documentBody(request)));
  }
  const id = path.startsWith("/documents/") ? path.slice("/documents/".length) : "";
  if (!uuid.test(id)) return json(response, 404, { error: "Not found" });
  let result;
  switch (request.method) {
    case "GET": result = await store.get(tenantID, id); break;
    case "PUT": result = await store.update(tenantID, id, await documentBody(request)); break;
    case "DELETE": result = await store.delete(tenantID, id); break;
    default: return json(response, 405, { error: "Method not allowed" });
  }
  if (!result) return json(response, 404, { error: "Not found" });
  return json(response, 200, request.method === "DELETE" ? { deleted: true } : result);
}

// This header is trusted ONLY on the private guest listener behind Gregale.
// The gateway strips caller values and supplies a verified tenant identity.
export function createHandler(store) {
  return async (request, response) => {
    try {
      const url = new URL(request.url, "http://guest.local");
      if ((url.pathname === "/healthz" || url.pathname === "/") &&
          (request.method === "GET" || request.method === "HEAD")) {
        await store.health();
        return json(response, 200, { ok: true });
      }
      const tenantID = request.headers[tenantHeader];
      if (typeof tenantID !== "string" || !uuid.test(tenantID)) {
        return json(response, 403, { error: "Verified platform tenant identity required" });
      }
      if (url.search) return json(response, 400, { error: "Query parameters are not supported" });
      await documents(request, response, store, tenantID.toLowerCase(), url.pathname);
    } catch (error) {
      // Never return driver errors, connection URLs, or customer document data.
      json(response, error instanceof RequestError ? error.status : 503,
        { error: error instanceof RequestError ? error.message : "Service unavailable" });
    }
  };
}
