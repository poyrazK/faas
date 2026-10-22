import { performance } from "node:perf_hooks";

export function faultPlan(value = { mode: "drop", direction: "response" }) {
  if (!value || typeof value !== "object" || Array.isArray(value) ||
      Object.keys(value).some(key => !["mode", "direction", "durationMS"].includes(key))) throw new Error("invalid_commit_fault");
  const { mode, direction = "response", durationMS } = value;
  if (mode === "drop" && direction === "response" && durationMS === undefined) return { mode, direction, durationMS: 0 };
  if (!["delay", "stall"].includes(mode) || !["request", "response"].includes(direction) ||
      !Number.isInteger(durationMS) || durationMS < 1 || durationMS > 5000) throw new Error("invalid_commit_fault");
  return { mode, direction, durationMS };
}

// Records only configuration/timing/outcome; never SQL, keys or credentials.
export function createFaultClock() {
  const active = new Set();
  const records = [];
  function begin(plan) {
    const started = performance.now();
    const record = { mode: plan.mode, direction: plan.direction, configured_ms: plan.durationMS, elapsed_ms: 0, outcome: "holding" };
    records.push({ record, started });
    let timer;
    let resolve;
    const done = new Promise(complete => { resolve = complete; });
    const finish = outcome => {
      if (record.outcome !== "holding") return;
      clearTimeout(timer);
      active.delete(cancel);
      record.elapsed_ms = performance.now() - started;
      record.outcome = outcome;
      resolve(outcome);
    };
    const cancel = () => finish("canceled");
    active.add(cancel);
    const expire = () => {
      const remaining = plan.durationMS - (performance.now() - started);
      if (remaining > 0) timer = setTimeout(expire, Math.ceil(remaining));
      else finish(plan.mode === "delay" ? "released" : plan.mode === "stall" ? "timed_out" : "dropped");
    };
    expire();
    return { done, cancel };
  }
  return {
    begin,
    cancel() { for (const cancel of [...active]) cancel(); },
    evidence() { return { active_faults: active.size, fault_timers: active.size, faults_fired: records.length,
      faults: records.map(({ record, started }) => ({ ...record, elapsed_ms: record.outcome === "holding" ? performance.now() - started : record.elapsed_ms })) }; },
  };
}
