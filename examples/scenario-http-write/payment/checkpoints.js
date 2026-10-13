import { conflict, fingerprint } from "./contract.js";

export const holdStages = Object.freeze(["before-acceptance", "pending", "committed"]);

// Single-writer fixture controls: only matching business replay releases a gate.
// Socket closure does not revoke work already received by the provider.
export function createCheckpoints({ holdStage, holdMS }) {
  if (!holdStages.includes(holdStage) || !Number.isSafeInteger(holdMS) || holdMS < 1 || holdMS > 120000) {
    throw new Error("invalid_checkpoint_configuration");
  }
  const gates = new Map();
  const expired = new Map();
  return {
    get size() { return gates.size; },
    has: key => gates.has(key) || expired.has(key),
    check(key, order) {
      const hash = gates.get(key)?.hash || expired.get(key);
      if (hash && hash !== fingerprint(order)) throw conflict();
    },
    isExpired: key => expired.has(key),
    describe(key, record) {
      return { received: !!record || gates.has(key) || expired.has(key), accepted: !!record,
        pending: record?.state === "pending", committed: record?.state === "confirmed", held: gates.has(key) };
    },
    wait(key, order) {
      const hash = fingerprint(order);
      return new Promise(resolve => {
        const timer = setTimeout(() => {
          gates.delete(key);
          expired.set(key, hash);
          resolve(false);
        }, holdMS);
        gates.set(key, { hash, finish(ok) { clearTimeout(timer); resolve(ok); } });
      });
    },
    release(key) {
      const gate = gates.get(key);
      gates.delete(key);
      gate?.finish(true);
    },
    cancel() {
      for (const [key, gate] of gates) { expired.set(key, gate.hash); gate.finish(false); }
      gates.clear();
    },
  };
}
