export const report = "external_id,state\nimport-001,complete\n";

// Ports keep the deterministic retry fixture independently testable. Native
// acceptance supplies PostgreSQL, signed object writes and the actual relay.
export function createImporter({ state, update, upload, checkpoint, notify, duplicate, expectDisconnect = false }) {
  return async (id) => {
    if (id !== "import-001") throw new Error("unknown fixture");
    const current = await state(id);
    if (current === "pending") {
      await update(id, "committed");
      await upload(report);
      await checkpoint();
      if (expectDisconnect) {
        let disconnected = false;
        try { await state(id); } catch { disconnected = true; }
        if (!disconnected) throw new Error("database outage was not observed by the worker");
      }
      await notify();
      return 503;
    }
    if (current !== "committed") throw new Error("unexpected additional dispatch");
    if (duplicate) await notify();
    await update(id, "complete");
    return 200;
  };
}
