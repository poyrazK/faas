// A local, monotonic deadline bounds execution even when a heartbeat is stuck.
// Start every budget before its database request so network delay cannot extend it.
export function createMcpTaskLeaseDeadline({ leaseMs, startedAt, onExpired, now = () => performance.now() }) {
  let timer;
  let closed = false;
  let deadline;
  function expire() {
    if (closed) return;
    closed = true;
    clearTimeout(timer);
    onExpired();
  }
  function renew(requestStartedAt) {
    if (closed) return;
    const current = now();
    if (deadline !== undefined && current >= deadline) { expire(); return; }
    const remaining = leaseMs - Math.max(0, current - requestStartedAt);
    if (remaining <= 0) { expire(); return; }
    deadline = current + remaining;
    clearTimeout(timer);
    timer = setTimeout(expire, remaining);
    timer.unref?.();
  }
  renew(startedAt ?? now());
  return { renew, close() { closed = true; clearTimeout(timer); } };
}
