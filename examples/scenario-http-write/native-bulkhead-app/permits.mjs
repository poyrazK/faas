export function createBulkhead() {
  const queue = [];
  let active = 0, peakActive = 0, peakQueue = 0, granted = 0, released = 0, admissions = 0, rejected = 0, expired = 0, canceled = 0, timers = 0;
  const grant = () => { active++; granted++; peakActive = Math.max(peakActive, active); };
  return {
    acquire(signal) {
      if (signal.aborted) return Promise.resolve("canceled");
      if (active < 2) { grant(); return Promise.resolve("granted"); }
      if (queue.length >= 2) { rejected++; return Promise.resolve("rejected"); }
      admissions++; timers++;
      return new Promise(resolve => {
        let done = false;
        const abort = () => finish("canceled");
        const finish = result => {
          if (done) return; done = true;
          clearTimeout(timer); timers--; signal.removeEventListener("abort", abort);
          const index = queue.indexOf(entry); if (index >= 0) queue.splice(index, 1);
          if (result === "granted") grant(); else if (result === "expired") expired++; else canceled++;
          resolve(result);
        };
        const timer = setTimeout(() => finish("expired"), 1000);
        const entry = { finish }; queue.push(entry); peakQueue = Math.max(peakQueue, queue.length);
        signal.addEventListener("abort", abort, { once: true });
      });
    },
    release() {
      if (active <= 0) throw new Error("bulkhead_permit_underflow");
      active--; released++;
      if (queue.length) queue.shift().finish("granted");
    },
    evidence: () => ({ active_dependencies: active, queued_requests: queue.length, peak_active: peakActive, peak_queue: peakQueue,
      permits_granted: granted, permits_released: released, queued_admissions: admissions, rejected, queue_timeouts: expired, queue_cancellations: canceled, queue_timers: timers }),
  };
}
