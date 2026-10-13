import http from "node:http";
import net from "node:net";
import { openLedger } from "./ledger.js";
import { fingerprint, normalizeOrder } from "./contract.js";

export function createPaymentFixture({ ledgerPath, orderLedgerPath, ledgerToken }) {
  const charges = openLedger(ledgerPath);
  const orders = openLedger(orderLedgerPath);
  let attempts = 0;
  const sockets = new Set();
  const tcp = net.createServer(socket => {
    sockets.add(socket);
    socket.once("close", () => sockets.delete(socket));
    socket.on("error", () => {});
    let data = "";
    socket.on("data", chunk => {
      data += chunk.toString("utf8");
      if (data.length > 1024) { socket.destroy(); return; }
      if (!data.endsWith("\n")) return;
      try {
        const { key, order, fingerprint: hash } = JSON.parse(data);
        if (typeof key !== "string" || !/^[a-zA-Z0-9_-]{1,128}$/.test(key) || fingerprint(order) !== hash) { socket.destroy(); return; }
        attempts++;
        // Charge is committed BEFORE acknowledgement: dropping downstream
        // bytes now creates a genuinely ambiguous application-side outcome.
        charges.set(key, "confirmed", order);
        socket.end(data);
      } catch (error) {
        if (error.status === 409) socket.end(JSON.stringify({ error: "idempotency_conflict" }) + "\n");
        else socket.destroy();
      }
    });
  });
  const web = http.createServer(async (request, response) => {
    const path = new URL(request.url, "http://payment").pathname;
    const send = (status, body) => {
      response.writeHead(status, { "content-type": "application/json", "cache-control": "no-store" });
      response.end(JSON.stringify(body));
    };
    if (path === "/order-ledger" || path === "/orders" || path.startsWith("/orders/")) {
      if (ledgerToken && request.headers.authorization !== `Bearer ${ledgerToken}`) { send(403, { error: "forbidden" }); return; }
      try {
        if (request.method === "GET" && path === "/order-ledger") {
          send(200, { id: orders.id, confirmed: orders.count("confirmed"), pending: orders.count("pending") });
        } else if (request.method === "GET" && path.startsWith("/orders/")) {
          const record = orders.get(path.slice("/orders/".length));
          send(record ? 200 : 404, record || { error: "not_found" });
        } else if (request.method === "POST" && path === "/orders") {
          let data = "";
          for await (const chunk of request) {
            data += chunk.toString("utf8");
            if (data.length > 1024) { send(413, { error: "too_large" }); return; }
          }
          const { key, state, order } = JSON.parse(data);
          if (typeof key !== "string" || !/^[a-zA-Z0-9_-]{1,128}$/.test(key) || !["pending", "confirmed"].includes(state)) {
            send(400, { error: "invalid_order" }); return;
          }
          send(200, orders.set(key, state, normalizeOrder(order)));
        } else send(405, { error: "method_not_allowed" });
      } catch (error) { send(error.status || 500, { error: error.status ? error.message : "order_storage_unavailable" }); }
      return;
    }
    if (request.method === "GET" && path.startsWith("/charges/")) {
      const key = path.slice("/charges/".length);
      const record = charges.get(key);
      send(record ? 200 : 404, record ? { id: key, status: record.state, order: record.order } : { error: "not_found" });
      return;
    }
    response.writeHead(path === "/" || path === "/stats" ? 200 : 404, { "content-type": "application/json" });
    response.end(JSON.stringify(path === "/stats" ? { charges: charges.count("confirmed"), attempts } : { ready: true }));
  });
  return { tcp, web, stopSockets: () => { for (const socket of sockets) socket.destroy(); } };
}
