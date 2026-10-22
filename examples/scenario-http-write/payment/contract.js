import { createHash } from "node:crypto";

export const defaultOrder = Object.freeze({ customer: "demo-customer", sku: "demo-item", quantity: 1 });
export function normalizeOrder(order) {
  if (!order || typeof order !== "object" || Array.isArray(order) ||
      Object.keys(order).sort().join(",") !== "customer,quantity,sku" ||
      ![order.customer, order.sku].every(value => typeof value === "string" && /^[a-zA-Z0-9_-]{1,64}$/.test(value)) ||
      !Number.isSafeInteger(order.quantity) || order.quantity < 1 || order.quantity > 100) {
    throw Object.assign(new Error("invalid_order"), { status: 400 });
  }
  return { customer: order.customer, sku: order.sku, quantity: order.quantity };
}
export const fingerprint = order => createHash("sha256").update(JSON.stringify(normalizeOrder(order))).digest("hex");
export function conflict() {
  return Object.assign(new Error("idempotency_conflict"), { status: 409 });
}
