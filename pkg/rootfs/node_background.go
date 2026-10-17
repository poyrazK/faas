package rootfs

// nodeFunctionBackground is the negotiated execution-completion contract
// (ADR-1571). A response can finish while registered promises still own their
// invocation slot. Only actual settlement permits execution completion.
const nodeFunctionBackground = `
function setupBackground(env, out) {
  out.forbidBackground = env.forbid_background === true;
  if (env.background === undefined) return;
  const { timeout_ms: timeout, max_tasks: maxTasks } = env.background || {};
  if (!Number.isSafeInteger(timeout) || timeout <= 0 ||
      !Number.isSafeInteger(maxTasks) || maxTasks <= 0) {
    throw new Error("invalid runner background policy");
  }
  out.background = { timeout, maxTasks, tasks: new Map(), nextID: 0, accepting: true, deadline: undefined };
}

function registerBackground(out, value) {
  if (out.forbidBackground) {
    Promise.resolve(value).catch(() => {});
    throw new Error("waitUntil is unavailable in lifecycle hooks; await cleanup");
  }
  // Preserve the legacy adapter surface when no completion contract was
  // negotiated. This scaffold still carries no post-response guarantee.
  if (!out.background) {
    if (value && typeof value.then === "function") Promise.resolve(value).catch((err) => console.error(err));
    return;
  }
  const promise = Promise.resolve(value);
  const b = out.background;
  if (!b.accepting || out.cancelled || (b.deadline !== undefined && Date.now() >= b.deadline)) {
    promise.catch(() => {});
    throw new Error("waitUntil registration is closed");
  }
  if (b.tasks.size >= b.maxTasks || !Number.isSafeInteger(b.nextID + 1)) {
    promise.catch(() => {});
    throw new Error("waitUntil task limit reached");
  }
  const id = ++b.nextID;
  const task = { timedOut: false, done: undefined };
  b.tasks.set(id, task);
  out.emit({ frame: "background", background: { task: id, event: "register" } });
  const remaining = b.deadline === undefined ? b.timeout : Math.min(b.timeout, b.deadline - Date.now());
  const timer = setTimeout(() => {
    task.timedOut = true;
    out.emit({ frame: "background", background: { task: id, event: "timeout" } });
    // Promise cancellation is cooperative. Keep the task and worker slot
    // occupied until actual cleanup, or the runner's retirement deadline.
    out.controller.abort();
  }, remaining);
  task.done = promise.then(
    () => "completed",
    (err) => { console.error(err && err.stack ? err.stack : err); return "failed"; },
  ).then((outcome) => {
    clearTimeout(timer);
    b.tasks.delete(id);
    out.emit({ frame: "background", background: {
      task: id, event: "settled", outcome: task.timedOut ? "timeout" : outcome,
    } });
  });
}

async function drainBackground(out) {
  const b = out.background;
  if (!b) return;
  try {
    // A registered task may register a child before it settles. Re-read the
    // bounded map after each wave; the post-response deadline never extends.
    while (b.tasks.size) await Promise.all(Array.from(b.tasks.values(), (task) => task.done));
  } finally {
    b.accepting = false;
  }
  out.emit({ frame: "complete" });
}
`
