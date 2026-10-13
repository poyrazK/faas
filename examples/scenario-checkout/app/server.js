import http from "node:http";
import net from "node:net";
import { openLedger } from "./ledger.js";
import { remoteLedger } from "./remote-ledger.js";
import { conflict, defaultOrder, fingerprint, normalizeOrder } from "./contract.js";

async function readOrder(request) {
  const chunks = [];
  let bytes = 0;
  await new Promise((resolve, reject) => {
    let done = false;
    const fail = error => { if (!done) { done = true; reject(error); } };
    request.on("error", fail);
    request.on("data", chunk => {
      if (done) return;
      bytes += chunk.length;
      if (bytes > 1024) {
        request.pause();
        fail(Object.assign(new Error("order_too_large"), { status: 413 }));
      } else chunks.push(chunk);
    });
    request.on("end", () => { if (!done) { done = true; resolve(); } });
    request.on("aborted", () => fail(Object.assign(new Error("invalid_order"), { status: 400 })));
  });
  if (bytes === 0) return normalizeOrder(defaultOrder);
  if (request.headers["content-type"]?.split(";", 1)[0].trim().toLowerCase() !== "application/json") {
    throw Object.assign(new Error("invalid_order"), { status: 400 });
  }
  try { return normalizeOrder(JSON.parse(Buffer.concat(chunks).toString("utf8"))); }
  catch { throw Object.assign(new Error("invalid_order"), { status: 400 }); }
}

// The fixture acknowledges a durable, idempotent simulated charge.
export function createCheckoutServer({ paymentHost, paymentPort, attemptTimeout = 350, ledgerPath, ledgerURL, ledgerToken }) {
  const orders = ledgerURL ? remoteLedger(ledgerURL, ledgerToken) : openLedger(ledgerPath);
  const pending = new Map();
  const completed = new Map();
  let attempts = 0;

  function payment(key, order) {
    attempts++;
    return new Promise((resolve, reject) => {
      const payload = Buffer.from(JSON.stringify({ key, order, fingerprint: fingerprint(order) }) + "\n");
      const socket = net.connect({ host: paymentHost, port: paymentPort });
      let received = Buffer.alloc(0);
      let done = false;
      const finish = error => {
        if (done) return;
        done = true;
        clearTimeout(timer);
        socket.destroy();
        if (error) reject(error);
        else resolve();
      };
      // Includes connection establishment and all time waiting for a response.
      const timer = setTimeout(() => finish(new Error("payment_deadline")), attemptTimeout);
      socket.once("connect", () => socket.write(payload));
      socket.on("error", finish);
      socket.once("end", () => finish(new Error("payment_closed")));
      socket.on("data", chunk => {
        received = Buffer.concat([received, chunk]);
        if (received.length > 1024) {
          finish(new Error("invalid_payment_acknowledgement"));
        } else if (received.at(-1) === 10) {
          if (received.equals(payload)) finish();
          else {
            let error = new Error("invalid_payment_acknowledgement");
            try { if (JSON.parse(received.toString("utf8")).error === "idempotency_conflict") error = conflict(); } catch {}
            finish(error);
          }
        }
      });
    });
  }

  async function placeOrder(key, order) {
    // Save intent before contacting payment. A timeout leaves an unknown
    // outcome, so later attempts reuse the exact same provider key.
    for (let attempt = 0; attempt < 2; attempt++) {
      try {
        await payment(key, order);
      } catch (error) {
        if (error.status === 409) throw error;
        // At most two attempts, each with a total deadline.
        continue;
      }
      const record = await orders.set(key, "confirmed", order);
      completed.set(key, record);
      return { status: 200, body: { id: key, status: "confirmed" } };
    }
    return { status: 503, body: { error: "payment_unavailable", payment_outcome: "unknown", retryable: true } };
  }

  return http.createServer(async (request, response) => {
    const send = (status, body) => {
      if (response.destroyed || response.writableEnded) return;
      response.writeHead(status, { "content-type": "application/json", "cache-control": "no-store", ...(status === 413 ? { connection: "close" } : {}) });
      response.end(JSON.stringify(body), () => { if (status === 413 || status >= 400 && !request.complete) request.destroy(); });
    };
    try {
      const path = new URL(request.url, "http://checkout").pathname;
      if (request.method === "GET" && (path === "/" || path === "/catalog")) {
        send(200, { available: true });
      } else if (request.method === "GET" && path === "/stats") {
        send(200, { orders: await orders.count("confirmed"), payment_attempts: attempts, pending: pending.size });
      } else if (request.method === "GET" && path === "/durability") {
        const proof = orders.proof ? await orders.proof() : { id: orders.id, confirmed: orders.count("confirmed") };
        send(200, { ledger_id: proof.id, orders: proof.confirmed, pending_orders: await orders.count("pending"), outage_state: (await orders.get("outage"))?.state || "missing" });
      } else if (request.method === "GET" && path.startsWith("/orders/")) {
        const key = path.slice("/orders/".length);
        const record = await orders.get(key);
        send(record ? 200 : 404, record ? { id: key, status: record.state, order: record.order } : { error: "not_found" });
      } else if (request.method === "POST" && path === "/checkout") {
        const key = request.headers["idempotency-key"];
        if (typeof key !== "string" || !/^[a-zA-Z0-9_-]{1,128}$/.test(key)) {
          send(400, { error: "invalid_idempotency_key" });
          return;
        }
        const order = await readOrder(request);
        const cached = completed.get(key);
        if (cached && cached.fingerprint !== fingerprint(order)) throw conflict();
        // Bind payload atomically before contacting payment or coalescing a
        // concurrent request. Pending keys cannot be repurposed after timeout.
        const record = cached || await orders.set(key, "pending", order);
        if (record.state === "confirmed" || completed.has(key)) {
          send(200, { id: key, status: "confirmed" });
          return;
        }
        if (!pending.has(key)) {
          const operation = placeOrder(key, order).finally(() => pending.delete(key));
          pending.set(key, operation);
        }
        try {
          const result = await pending.get(key);
          send(result.status, result.body);
        } catch (error) {
          send(error.status || 500, { error: error.status ? error.message : "order_storage_unavailable" });
        }
      } else {
        send(404, { error: "not_found" });
      }
    } catch (error) { send(error.status || 500, { error: error.status ? error.message : "order_storage_unavailable" }); }
  });
}
