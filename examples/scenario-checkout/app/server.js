import http from "node:http";
import net from "node:net";

// An echo acknowledgement stands in for a payment response. This example
// creates no external charges and keeps its order ledger in memory.
export function createCheckoutServer({ paymentHost, paymentPort, attemptTimeout = 350 }) {
  const orders = new Map();
  const pending = new Map();
  let attempts = 0;

  function payment(key) {
    attempts++;
    return new Promise((resolve, reject) => {
      const payload = Buffer.from(key + "\n");
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
        if (!received.equals(payload.subarray(0, received.length))) {
          finish(new Error("invalid_payment_acknowledgement"));
        } else if (received.length === payload.length) finish();
      });
    });
  }

  async function placeOrder(key) {
    for (let attempt = 0; attempt < 2; attempt++) {
      try {
        await payment(key);
        const order = { id: key, status: "confirmed" };
        orders.set(key, order);
        return { status: 200, body: order };
      } catch {
        // At most two attempts, each with a total deadline.
      }
    }
    return { status: 503, body: { error: "payment_unavailable", retryable: true } };
  }

  return http.createServer(async (request, response) => {
    const send = (status, body) => {
      response.writeHead(status, { "content-type": "application/json" });
      response.end(JSON.stringify(body));
    };
    const path = new URL(request.url, "http://checkout").pathname;
    if (request.method === "GET" && (path === "/" || path === "/catalog")) {
      send(200, { available: true });
    } else if (request.method === "GET" && path === "/stats") {
      send(200, { orders: orders.size, payment_attempts: attempts, pending: pending.size });
    } else if (request.method === "POST" && path === "/checkout") {
      // The demo sells one fixed item. The key identifies the entire operation.
      request.resume();
      const key = request.headers["idempotency-key"];
      if (typeof key !== "string" || !/^[a-zA-Z0-9_-]{1,128}$/.test(key)) {
        send(400, { error: "invalid_idempotency_key" });
        return;
      }
      if (orders.has(key)) {
        send(200, orders.get(key));
        return;
      }
      if (!pending.has(key)) {
        const operation = placeOrder(key).finally(() => pending.delete(key));
        pending.set(key, operation);
      }
      const result = await pending.get(key);
      send(result.status, result.body);
    } else {
      send(404, { error: "not_found" });
    }
  });
}
