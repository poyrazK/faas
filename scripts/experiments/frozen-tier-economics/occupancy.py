"""Node occupancy under ADR-1151's policy (quiet pause 2 s + freeze at the
idle timeout): p50/p99/p99.9/max of active vCPU, booked vCPU (running +
quiet-paused), resident VMs and frozen VMs over the sampled instants, for a
few apps-per-node counts. Sizes QuietPauseMaxVCPUOvercommitPercent.

Usage: python3 -P -E occupancy.py apps.npz
"""
import os
import sys

import numpy as np

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import freeze_sim as S  # noqa: E402

POLICY = 'quiet pause 2 s (billed) + freeze at idle timeout -> 1 h (ADR-1151)'


def main():
    data = np.load(sys.argv[1])
    counts, dur = data['counts'], data['dur_ms']
    napps = counts.shape[0]
    rnd = np.random.default_rng(11)
    plans = rnd.choice(list(S.MIX), size=napps, p=list(S.MIX.values()))
    dur_s = np.clip(np.where(np.isfinite(dur), dur, np.nanmedian(dur)) / 1000.0, 0.005, 30.0)
    pol = S.POLICIES[POLICY]
    run_f = np.zeros((napps, S.NB), dtype=np.float32)
    pz_f = np.zeros((napps, S.NB), dtype=np.float32)
    fz_f = np.zeros((napps, S.NB), dtype=np.float32)
    vcpu = np.array([S.PLANS[p]['vcpu'] for p in plans], dtype=np.float32)
    for i in range(napps):
        starts, ends, nreq = S.busy_intervals(counts[i], dur_s[i], rnd)
        _, rf, ff, zf = S.run_policy(pol, starts, ends, nreq, plans[i])
        run_f[i], pz_f[i], fz_f[i] = rf, zf, ff

    def q(a):
        return ' '.join(f'{np.percentile(a, p):5.0f}' for p in (50, 99, 99.9)) + f' {a.max():5.0f}'

    print('per instant: p50 p99 p99.9 max')
    for n in (251, 500, 716):
        idx = np.random.default_rng(5).integers(0, napps, size=n)
        print(f'{n:4d} apps | active vCPU {q(vcpu[idx] @ run_f[idx])} | booked vCPU '
              f'{q(vcpu[idx] @ (run_f[idx] + pz_f[idx]))} | resident VMs '
              f'{q((run_f[idx] + pz_f[idx]).sum(0))} | frozen VMs {q(fz_f[idx].sum(0))}')


if __name__ == '__main__':
    main()
