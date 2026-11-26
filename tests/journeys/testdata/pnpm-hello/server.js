// The journey checks that a pnpm app runs its start script on the shared
// Node runtime (ADR-20261125170000): Node is the runner base's, not mise's,
// and the dependency resolves through pnpm's node_modules/.pnpm links.
const http = require("node:http");
const ms = require("ms");

http
  .createServer((req, res) => {
    res.setHeader("content-type", "application/json");
    if (req.url === "/healthz") return res.end(JSON.stringify({ ok: true }));
    res.end(
      JSON.stringify({
        exec: process.execPath,
        cwd: process.cwd(),
        dep: ms("2 days"),
        packageManager: process.env.npm_config_user_agent || "",
      }),
    );
  })
  .listen(Number(process.env.PORT) || 3000, "0.0.0.0");
