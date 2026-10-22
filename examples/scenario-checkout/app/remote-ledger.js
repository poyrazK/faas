export function remoteLedger(url, token) {
  const request = async (path, options = {}) => {
    const response = await fetch(`${url}${path}`, {
      signal: AbortSignal.timeout(2000),
      headers: { authorization: `Bearer ${token}`, "content-type": "application/json" }, ...options,
    });
    if (response.status === 404) return undefined;
    if (response.status === 409) throw Object.assign(new Error("idempotency_conflict"), { status: 409 });
    if (!response.ok) throw new Error("order_storage_unavailable");
    return response.json();
  };
  return {
    get: async key => request(`/orders/${encodeURIComponent(key)}`),
    set: async (key, state, order) => request("/orders", { method: "POST", body: JSON.stringify({ key, state, order }) }),
    count: async state => (await request("/order-ledger"))[state],
    proof: async () => request("/order-ledger"),
  };
}
