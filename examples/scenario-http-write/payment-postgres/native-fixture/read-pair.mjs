// Bounded rendezvous after two independent database clients have been acquired.
export function createReadPair() {
  let pair, waiters = 0, completed = 0;
  const finish = value => {
    if (!pair) return;
    clearTimeout(pair.timer);
    const current = pair; pair = undefined;
    if (value) completed++;
    current.resolve(value);
  };
  return {
    arm() {
      if (pair || waiters) throw new Error("read_pair_busy");
      let resolve;
      const done = new Promise(complete => { resolve = complete; });
      pair = { resolve, done, arrived: 0, timer: undefined };
    },
    async arrive() {
      if (!pair) return;
      const current = pair; waiters++; current.arrived++;
      if (current.arrived === 1) current.timer = setTimeout(() => finish(false), 2000);
      else finish(true);
      try { if (!await current.done) throw new Error("read_pair_incomplete"); }
      finally { waiters--; }
    },
    cancel() { finish(false); },
    evidence: () => ({ read_pair_armed: Boolean(pair), read_pair_waiters: waiters, read_pair_timers: pair?.timer ? 1 : 0, concurrent_read_pairs: completed }),
  };
}
