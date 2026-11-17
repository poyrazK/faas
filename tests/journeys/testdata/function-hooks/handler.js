// Snapshot hooks in function code (ADR-20261110190000): init finishes before
// the snapshot, beforeSnapshot runs before it is captured, and afterRestore
// runs in every restored worker before it serves.
let initDone = false;
let beforeSnapshots = 0;
let restores = 0;

export default {
  async init() {
    await new Promise((resolve) => setTimeout(resolve, 200));
    initDone = true;
  },
  beforeSnapshot() {
    beforeSnapshots++;
  },
  afterRestore() {
    restores++;
  },
  async fetch() {
    return Response.json({ probe: "gregale-e2e-hooks", init: initDone, before_snapshots: beforeSnapshots, restores });
  },
};
