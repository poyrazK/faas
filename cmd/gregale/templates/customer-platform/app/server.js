import { createServer } from "node:http";
import { createHandler } from "./http.js";
import { openStore } from "./store.js";

try {
  const store = await openStore(process.env.DATABASE_URL);
  const server = createServer({ requestTimeout: 15000, headersTimeout: 10000 }, createHandler(store));
  const port = Number(process.env.PORT || 8080);
  server.listen(port, process.env.HOST || "0.0.0.0", () => {
    console.log(JSON.stringify({ event: "listening", port: server.address().port }));
  });
  let stopping = false;
  const stop = () => {
    if (stopping) return;
    stopping = true;
    server.close(async () => { await store.close(); process.exit(0); });
    setTimeout(() => process.exit(1), 5000).unref();
  };
  process.on("SIGTERM", stop);
  process.on("SIGINT", stop);
} catch {
  console.error("Startup failed: check DATABASE_URL, database role, and npm run migrate");
  process.exitCode = 1;
}
