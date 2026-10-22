import { performance } from "node:perf_hooks";

// Flush locally queued plaintext/TLS writes before making the connection cut.
// This is not proof of peer delivery; the receiving tests verify the prefix.
function flush(socket) {
  if (socket.destroyed) return Promise.resolve(false);
  return new Promise(resolve => {
    const closed = () => { socket.off("close", closed); resolve(false); };
    socket.once("close", closed);
    socket.write(Buffer.alloc(0), error => {
      socket.off("close", closed);
      resolve(!error && !socket.destroyed);
    });
  });
}

export function createReadCuts() {
  let armed;
  let stopped = false;
  const records = [];
  const active = new Set();
  function finish(plan, outcome) {
    if (!["armed", "forwarding"].includes(plan.record.outcome)) return;
    plan.record.outcome = outcome;
    plan.record.elapsed_ms = plan.started === null ? 0 : performance.now() - plan.started;
  }
  return {
    arm(value) {
      if (stopped) throw new Error("commit_proxy_closed");
      if (!value || typeof value !== "object" || Array.isArray(value) || Object.keys(value).some(key => key !== "afterBytes") ||
          !Number.isInteger(value.afterBytes) || value.afterBytes < 1 || value.afterBytes >= 1024 * 1024) throw new Error("invalid_read_cut");
      if (armed || active.size) throw new Error("read_cut_already_armed");
      if (records.length >= 100) throw new Error("read_cut_limit");
      const record = { after_bytes: value.afterBytes, frame_bytes: null, prefix_flushed_bytes: 0, elapsed_ms: 0, outcome: "armed" };
      armed = { record, started: null }; records.push(armed);
    },
    clear() {
      if (armed) { finish(armed, "cleared"); armed = undefined; }
      for (const plan of active) finish(plan, "cleared");
    },
    async write(message, socket, write, destroy) {
      // Select one sufficiently large DataRow, never startup/auth/short rows.
      if (!armed || message[0] !== 68 || message.length <= armed.record.after_bytes) return false;
      const plan = armed; armed = undefined; active.add(plan);
      plan.started = performance.now(); plan.record.frame_bytes = message.length; plan.record.outcome = "forwarding";
      try {
        const offset = plan.record.after_bytes;
        await write(message.subarray(0, offset));
        if (socket.destroyed || stopped) { finish(plan, "canceled"); return true; }
        if (plan.record.outcome === "cleared") { await write(message.subarray(offset)); return true; }
        if (!await flush(socket)) { finish(plan, "canceled"); return true; }
        plan.record.prefix_flushed_bytes = offset;
        if (plan.record.outcome === "cleared") { await write(message.subarray(offset)); return true; }
        finish(plan, "cut"); destroy();
        return true;
      } catch (error) { finish(plan, "canceled"); throw error; }
      finally { active.delete(plan); }
    },
    close() {
      stopped = true;
      if (armed) { finish(armed, "canceled"); armed = undefined; }
      for (const plan of active) finish(plan, "canceled");
    },
    evidence() {
      return { read_cut_armed: Boolean(armed), active_read_cuts: active.size,
        read_cuts_fired: records.filter(plan => plan.record.outcome === "cut").length,
        read_cuts: records.map(plan => ({ ...plan.record, elapsed_ms: plan.record.outcome === "forwarding" ? performance.now() - plan.started : plan.record.elapsed_ms })) };
    },
  };
}
