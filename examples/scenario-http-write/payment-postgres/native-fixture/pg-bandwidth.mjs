import { performance } from "node:perf_hooks";
import { writeFrame } from "./pg-frame-relay.mjs";

const directions = ["request", "response"];
export function createBandwidthLimiter() {
  const plans = new Map();
  const records = [];
  const tasks = new Set();
  let buffered = 0;
  let stopped = false;
  function wait(plan, socket, milliseconds) {
    return new Promise(resolve => {
      let timer;
      const finish = () => { clearTimeout(timer); socket.off("close", finish); plan.waiters.delete(finish); resolve(); };
      plan.waiters.add(finish); socket.once("close", finish);
      timer = setTimeout(finish, Math.max(1, Math.ceil(milliseconds)));
    });
  }
  async function transfer(plan, socket, message) {
    if (socket.destroyed || stopped) { plan.canceled_transfers++; return; }
    if (plan.outcome === "active" && plan.started === null) plan.started = performance.now();
    let offset = 0;
    while (offset < message.length && !socket.destroyed && !stopped) {
      if (plan.outcome !== "active") { await writeFrame(socket, message.subarray(offset)); return; }
      const size = Math.min(message.length - offset, Math.max(1, Math.min(4096, Math.floor(plan.bytes_per_second / 100))));
      const due = performance.now() + size / plan.bytes_per_second * 1000;
      while (performance.now() < due && !socket.destroyed && !stopped && plan.outcome === "active") await wait(plan, socket, due - performance.now());
      if (socket.destroyed || stopped) break;
      if (plan.outcome !== "active") continue;
      await writeFrame(socket, message.subarray(offset, offset + size));
      plan.bytes_forwarded += size;
      offset += size;
    }
    if (offset < message.length) plan.canceled_transfers++;
  }
  function finish(plan, outcome) {
    if (plan.outcome !== "active") return;
    plan.outcome = outcome;
    plan.ended = performance.now();
    for (const wake of [...plan.waiters]) wake();
  }
  return {
    limit(value) {
      if (stopped) throw new Error("commit_proxy_closed");
      if (!value || typeof value !== "object" || Array.isArray(value) || Object.keys(value).some(key => !["direction", "bytesPerSecond"].includes(key)) ||
          !directions.includes(value.direction) || !Number.isInteger(value.bytesPerSecond) || value.bytesPerSecond < 1024 || value.bytesPerSecond > 16 * 1024 * 1024) throw new Error("invalid_bandwidth_limit");
      if (plans.has(value.direction)) throw new Error("bandwidth_already_limited");
      if (records.length >= 100) throw new Error("bandwidth_limit_count_exceeded");
      const plan = { direction: value.direction, bytes_per_second: value.bytesPerSecond, bytes_forwarded: 0, canceled_transfers: 0,
        started: null, ended: null, outcome: "active", waiters: new Set(), tail: Promise.resolve() };
      records.push(plan); plans.set(value.direction, plan);
    },
    clear(direction) {
      if (!directions.includes(direction)) throw new Error("invalid_bandwidth_direction");
      const plan = plans.get(direction);
      if (plan) { finish(plan, "cleared"); plans.delete(direction); }
    },
    write(direction, socket, message) {
      const plan = plans.get(direction);
      if (!plan) return stopped ? Promise.resolve() : writeFrame(socket, message);
      if (buffered + message.length > 8 * 1024 * 1024) throw new Error("bandwidth_buffer_limit");
      buffered += message.length;
      const work = plan.tail.then(() => transfer(plan, socket, message)).finally(() => { buffered -= message.length; tasks.delete(work); });
      tasks.add(work); plan.tail = work.catch(() => {});
      return work;
    },
    close() {
      stopped = true;
      for (const plan of plans.values()) finish(plan, "closed");
      plans.clear();
      return Promise.all([...tasks]);
    },
    evidence() {
      return { bandwidth_active_limits: plans.size, bandwidth_pending_transfers: tasks.size, bandwidth_buffered_bytes: buffered,
        bandwidth_timers: records.reduce((total, plan) => total + plan.waiters.size, 0),
        bandwidth: records.map(plan => ({ direction: plan.direction, bytes_per_second: plan.bytes_per_second,
          bytes_forwarded: plan.bytes_forwarded, canceled_transfers: plan.canceled_transfers, outcome: plan.outcome,
          elapsed_ms: plan.started === null ? 0 : (plan.ended ?? performance.now()) - plan.started })) };
    },
  };
}
