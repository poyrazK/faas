// The journey checks that Bun serves from mise's Bun install on the shared
// Node runtime (ADR-20261124160000).
Bun.serve({
  port: Number(process.env.PORT) || 3000,
  hostname: "0.0.0.0",
  fetch(req) {
    const url = new URL(req.url);
    if (url.pathname === "/healthz") return Response.json({ ok: true });
    return Response.json({ bun: Bun.version, exec: process.execPath });
  },
});
