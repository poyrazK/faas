import { mkdtemp, rm } from "node:fs/promises";
import net from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { once } from "node:events";
import { spawn } from "node:child_process";
import { createInterface } from "node:readline";

export async function start(script, env) {
  const child = spawn(process.execPath, [fileURLToPath(new URL(script, new URL("../", import.meta.url)))], {
    env: { ...process.env, HOST: "127.0.0.1", PORT: "0", ...env }, stdio: ["ignore", "pipe", "pipe"],
  });
  let errors = "";
  child.stderr.on("data", chunk => { errors += chunk; });
  const lines = createInterface({ input: child.stdout });
  const ready = new Promise((resolve, reject) => {
    lines.once("line", line => { try { resolve(JSON.parse(line)); } catch (error) { reject(error); } });
    child.once("error", reject);
    child.once("exit", code => reject(new Error(`process exited ${code}: ${errors}`)));
  });
  const timer = setTimeout(() => child.kill("SIGKILL"), 10000);
  try {
    const address = await ready;
    return { child, ...address };
  } finally { clearTimeout(timer); }
}

async function crash(process) {
  if (!process || process.child.exitCode !== null || process.child.signalCode !== null) return;
  const exited = once(process.child, "exit");
  process.child.kill("SIGKILL");
  await exited;
}

export async function fixture(t, externalOrders = false) {
  const directory = await mkdtemp(join(tmpdir(), "gregale-checkout-"));
  let payment;
  let app;
  let drop = false;
  let boot = 0;
  const sockets = new Set();
  const proxy = net.createServer(socket => {
    const upstream = net.connect(payment.tcp_port, "127.0.0.1");
    for (const connection of [socket, upstream]) {
      sockets.add(connection);
      connection.once("close", () => sockets.delete(connection));
      connection.on("error", () => { socket.destroy(); upstream.destroy(); });
    }
    socket.pipe(upstream);
    upstream.on("data", data => { if (!drop) socket.write(data); });
    // During a downstream timeout the caller sees neither the ack nor EOF.
    upstream.on("end", () => { if (!drop) socket.end(); });
    socket.once("close", () => upstream.destroy());
  });
  t.after(async () => {
    await crash(app);
    await crash(payment);
    for (const socket of sockets) socket.destroy();
    await new Promise(resolve => proxy.close(resolve));
    await rm(directory, { recursive: true, force: true });
  });
  proxy.listen(0, "127.0.0.1");
  await once(proxy, "listening");
  const startPayment = async () => {
    payment = await start("../payment/start.js", {
      PAYMENT_TCP_PORT: "0", PAYMENT_LEDGER_PATH: join(directory, "charges.json"),
      ORDER_LEDGER_PATH: join(directory, "external-orders.json"), ORDER_LEDGER_TOKEN: "fixture-test-token",
    });
  };
  const startApp = async () => {
    app = await start("../app/start.js", {
      PAYMENT_HOST: "127.0.0.1", PAYMENT_PORT: String(proxy.address().port),
      ORDER_LEDGER_PATH: join(directory, externalOrders ? `empty-local-ledger-${boot++}.json` : "orders.json"),
      ORDER_LEDGER_URL: externalOrders ? `http://127.0.0.1:${payment.port}` : "",
      ORDER_LEDGER_TOKEN: "fixture-test-token",
    });
  };
  await startPayment();
  await startApp();
  return {
    directory,
    urls: () => ({ app: `http://127.0.0.1:${app.port}`, payment: `http://127.0.0.1:${payment.port}` }),
    drop: value => { drop = value; },
    restartApp: async () => { await crash(app); await startApp(); },
    discardLocalOrders: async () => {
      await crash(app);
      await rm(join(directory, "orders.json"), { force: true });
      await startApp();
    },
    restartPayment: async () => { await crash(payment); await startPayment(); },
    crashApp: () => crash(app),
    checkout: async (key = "lost-ack", order) => {
      const response = await fetch(`http://127.0.0.1:${app.port}/checkout`, {
        method: "POST", headers: { "idempotency-key": key, "content-type": "application/json" }, signal: AbortSignal.timeout(2000),
        ...(order ? { body: JSON.stringify(order) } : {}),
      });
      return { status: response.status, body: await response.json() };
    },
    charge: async key => (await fetch(`http://127.0.0.1:${payment.port}/charges/${key}`)).json(),
    paymentStats: async () => (await fetch(`http://127.0.0.1:${payment.port}/stats`)).json(),
    orderStats: async () => (await fetch(`http://127.0.0.1:${app.port}/stats`)).json(),
    durability: async () => (await fetch(`http://127.0.0.1:${app.port}/durability`)).json(),
  };
}
