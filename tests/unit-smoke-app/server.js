// The unit lane's smoke app (docs/adr/20261122170000-continuous-unit-lane.md,
// "Critical smoke checks"). scripts/ci/unit_smoke.py sets SMOKE_TOKEN to a
// value unique to the deploy, restarts the app fresh, and expects this
// answer, which proves the new configuration reached a freshly booted
// instance through the gateways.
const http = require("http");

const port = Number(process.env.PORT || 8080);
http
  .createServer((req, res) => {
    res.writeHead(200, { "content-type": "text/plain; charset=utf-8", "cache-control": "no-store" });
    res.end(`smoke:${process.env.SMOKE_TOKEN || ""}\n`);
  })
  .listen(port);
