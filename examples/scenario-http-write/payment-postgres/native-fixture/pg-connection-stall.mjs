import { performance } from "node:perf_hooks";

// Hold a validated SSLRequest response or the initial authentication challenge.
export function createConnectionStalls(enabled) {
  let plan;
  let stopped = false;
  let controls = 0;
  const records = [];
  const active = new Set();
  return {
    enable(value) {
      if (stopped) throw new Error("commit_proxy_closed");
      if (!enabled) throw new Error("connection_stall_requires_tls");
      if (!value || typeof value !== "object" || Array.isArray(value) || Object.keys(value).some(key => !["phase", "durationMS"].includes(key)) ||
          !["ssl_request", "authentication"].includes(value.phase) || !Number.isInteger(value.durationMS) || value.durationMS < 1 || value.durationMS > 5000) throw new Error("invalid_connection_stall");
      if (plan) throw new Error("connection_stall_already_enabled");
      if (controls >= 100) throw new Error("connection_stall_control_limit");
      plan = { phase: value.phase, durationMS: value.durationMS }; controls++;
    },
    clear() {
      plan = undefined;
      for (const hold of [...active]) hold.finish("released");
    },
    wait(socket, phase = "ssl_request") {
      if (stopped || socket.destroyed) return Promise.resolve(false);
      if (!plan || plan.phase !== phase) return null;
      if (records.length >= 100) return Promise.reject(new Error("connection_stall_attempt_limit"));
      const started = performance.now();
      const record = { phase, leg: "client", configured_ms: plan.durationMS, elapsed_ms: 0, outcome: "holding" };
      records.push({ record, started });
      return new Promise(resolve => {
        let timer;
        const canceled = () => hold.finish("canceled");
        const hold = { finish(outcome) {
          if (record.outcome !== "holding") return;
          clearTimeout(timer); socket.off("close", canceled); active.delete(hold);
          record.elapsed_ms = performance.now() - started; record.outcome = outcome;
          resolve(outcome === "released" && !stopped && !socket.destroyed);
        } };
        active.add(hold); socket.once("close", canceled);
        const expire = () => {
          const remaining = record.configured_ms - (performance.now() - started);
          if (remaining > 0) timer = setTimeout(expire, Math.ceil(remaining)); else hold.finish("timed_out");
        };
        expire();
      });
    },
    close() {
      stopped = true; plan = undefined;
      for (const hold of [...active]) hold.finish("canceled");
    },
    evidence() {
      return { connection_stall_enabled: Boolean(plan), active_connection_stalls: active.size,
        connection_stall_timers: active.size, connection_stalls_fired: records.length,
        connection_stalls: records.map(({ record, started }) => ({ ...record,
          elapsed_ms: record.outcome === "holding" ? performance.now() - started : record.elapsed_ms })) };
    },
  };
}
