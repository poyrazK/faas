// Refresh the immutable process receipt so native releases can recover after
// startup logs leave the platform's bounded per-instance log ring.
export function startMcpTaskWorkerReceipts({ workerID, shutdownTimeoutMs }, {
  log = console.log, schedule = setInterval, cancel = clearInterval,
} = {}) {
  const receipt = JSON.stringify({ event: 'mcp_task_worker_started', workerID, claimFence: true, shutdownTimeoutMs });
  log(receipt);
  const timer = schedule(() => log(receipt), 10_000);
  timer.unref?.();
  let closed = false;
  return () => {
    if (closed) return;
    closed = true;
    cancel(timer);
  };
}
