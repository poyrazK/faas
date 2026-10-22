export function createCombinedPolicies() {
  let state = "closed", generation = 0, openedAt = 0, failures = 0, blocked = 0, afterAcquire = 0;
  let opens = 0, halves = 0, closes = 0, probes = 0, activeProbes = 0, peakProbes = 0, probeFailures = 0, probeCancellations = 0, probeSuccesses = 0;
  let windowStart, windowID = 0, remaining = 2, admitted = 0, denied = 0;
  const open = () => { state = "open"; generation++; openedAt = performance.now(); opens++; failures = 4; };
  const unavailable = retry => state === "half-open" && activeProbes || state === "open" && (retry || performance.now() - openedAt < 3000);
  return {
    precheck(retry) { if (unavailable(retry)) { blocked++; return false; } return true; },
    admit(retry) {
      if (unavailable(retry)) { blocked++; afterAcquire++; return null; }
      if (state === "open") { state = "half-open"; generation++; halves++; }
      const probe = state === "half-open";
      if (probe) { probes++; activeProbes++; peakProbes = Math.max(peakProbes, activeProbes); }
      return { generation, probe };
    },
    settle(admission, succeeded, canceled) {
      if (admission.probe) { activeProbes--; if (canceled) probeCancellations++; else if (succeeded) probeSuccesses++; else probeFailures++; }
      if (admission.generation !== generation) return;
      if (canceled) { if (admission.probe) open(); return; }
      if (succeeded) { failures = 0; if (admission.probe) { state = "closed"; generation++; closes++; } }
      else if (admission.probe || ++failures >= 4) open();
    },
    retry() {
      const now = performance.now();
      if (windowStart === undefined || now - windowStart >= 30000) { windowStart = now; windowID++; remaining = 2; }
      if (!remaining) { denied++; return false; }
      remaining--; admitted++; return true;
    },
    evidence: () => ({ state, generation, consecutive_failures: failures, blocked, blocked_after_acquire: afterAcquire,
      cooldown_remaining_ms: state === "open" ? Math.max(0, 3000 - (performance.now() - openedAt)) : 0,
      open_transitions: opens, half_open_transitions: halves, closed_transitions: closes, probes_started: probes, active_probes: activeProbes,
      peak_probes: peakProbes, probe_failures: probeFailures, probe_cancellations: probeCancellations, probe_successes: probeSuccesses,
      retry_window_age_ms: windowStart === undefined ? null : performance.now() - windowStart,
      retry_window_remaining_ms: windowStart === undefined ? null : Math.max(0, 30000 - (performance.now() - windowStart)),
      retries_admitted: admitted, retries_denied: denied, remaining, window_id: windowID }),
  };
}
