"""Economics of the freeze tiers vs today's idle-timeout parking.

Replays a sample of Azure Functions 2019 HTTP apps (per-minute invocation
counts, 7 days; arrivals spread uniformly inside each minute) through each
policy and reports billed GB-h, revenue, apps per node, infra cost per app
and wake latency. Per-tier costs and latencies are the values measured on
the n2 node (2026-10-12).

Usage: FROZEN_OWN_FRAC=0.15 python3 -P -E freeze_sim.py apps.npz out.json
"""
import json
import os
import sys

import numpy as np

DAYS = 7
SEC = DAYS * 86400
INSTANTS = 2000                    # capacity: exact state at random instants after day 1
NB = INSTANTS
K_CAP = 120                        # >120 req/min: treat the minute as continuously busy

PLANS = {  # RAM MB, guest vCPU, idle timeout s, €/month, included GB-h
    'free':  dict(ram=128,  vcpu=2, idle=30,  price=0.0,  incl=5),
    'hobby': dict(ram=256,  vcpu=2, idle=60,  price=9.0,  incl=50),
    'pro':   dict(ram=512,  vcpu=2, idle=300, price=29.0, incl=250),
    'scale': dict(ram=1024, vcpu=4, idle=600, price=99.0, incl=1500),
}
# FUNCTIONS=1: every app is a function app, which idles out after
# FunctionIdleTimeoutDefaultSeconds (30 s, ADR-974) on every plan.
if os.environ.get('FUNCTIONS') == '1':
    for _plan in PLANS.values():
        _plan['idle'] = min(_plan['idle'], 30)
MIX ={'free': 0.80, 'hobby': 0.11, 'pro': 0.08, 'scale': 0.01}   # 4 free riders per payer, 55/40/5
OVERAGE_EUR_PER_GBH = 0.01
NODES = {
    'EX44 (€44/mo, 47.6 GB, 160 vCPU slots)': dict(cost=44.0, ceiling=47600, vcpu=160, vms=10000),
    'GCP n2-standard-4 (~€130/mo, 8.6 GB, 32 vCPU slots)': dict(cost=130.0, ceiling=8593, vcpu=32, vms=10000),
}
# Measured wake latency to first byte by the tier the request found.
LAT = {'warm': 0.0, 'paused': 2.0, 'frozen': 8.0, 'parked': 340.0, 'cold': 3000.0}
FROZEN_OWN_FRAC = float(os.environ.get('FROZEN_OWN_FRAC', '0.15'))  # frozen VM's own RAM vs running (measured ~5% for restored VMs)
MEASURED_FLOOR, HEADROOM = 64, 1.25

POLICIES = {
    'today (idle timeout -> park), reservation admission': dict(kind='today', admission='reservation'),
    'today (idle timeout -> park), measured admission': dict(kind='today', admission='measured'),
    'freeze at plan idle timeout -> frozen 1 h -> park (ADR-906 as proposed)': dict(kind='freeze', thr='plan', ttl=3600, billed=False),
    'freeze 2 s -> frozen 1 h -> park, frozen unbilled': dict(kind='freeze', thr=2, ttl=3600, billed=False),
    'freeze 2 s -> frozen 1 h, frozen BILLED': dict(kind='freeze', thr=2, ttl=3600, billed=True),
    'freeze 2 s -> frozen 10 min, unbilled': dict(kind='freeze', thr=2, ttl=600, billed=False),
    'freeze 10 s -> frozen 1 h, unbilled': dict(kind='freeze', thr=10, ttl=3600, billed=False),
    'freeze 0.5 s -> frozen 1 h, unbilled': dict(kind='freeze', thr=0.5, ttl=3600, billed=False),
    # ADR-1151: a quiet pause stays RUNNING (billed, RAM held) but books no
    # vCPU; qp=0 models dropping idle VMs' vCPU bookings without pausing.
    'idle running VMs hold no vCPU, no pause, park at idle timeout': dict(kind='quiet', qp=0, freeze=False, ttl=0),
    'quiet pause 2 s (billed), park at idle timeout': dict(kind='quiet', qp=2, freeze=False, ttl=0),
    'quiet pause 2 s (billed) + freeze at idle timeout -> 1 h (ADR-1151)': dict(kind='quiet', qp=2, freeze=True, ttl=3600),
}
COLD_AFTER = 7 * 86400


def busy_intervals(counts, dur_s, rnd):
    """Merged busy intervals [start, end) from per-minute counts."""
    minutes = np.nonzero(counts)[0]
    if len(minutes) == 0:
        return np.empty(0), np.empty(0), 0
    k = counts[minutes]
    hot = minutes[k > K_CAP]
    light = minutes[k <= K_CAP]
    kl = k[k <= K_CAP]
    t = np.repeat(light * 60.0, kl) + rnd.random(int(kl.sum())) * 60.0
    starts = np.concatenate([t, hot * 60.0])
    ends = np.concatenate([t + dur_s, hot * 60.0 + 60.0])
    order = np.argsort(starts, kind='stable')
    starts, ends = starts[order], ends[order]
    run_end = np.maximum.accumulate(ends)
    new = np.ones(len(starts), dtype=bool)
    new[1:] = starts[1:] > run_end[:-1]
    idx = np.nonzero(new)[0]
    m_starts = starts[idx]
    m_ends = np.maximum.reduceat(run_end, idx) if len(idx) else run_end
    m_ends = np.minimum(m_ends, SEC)
    return m_starts, m_ends, int(counts.sum())


T_INSTANTS = np.sort(np.random.default_rng(3).uniform(86400, SEC, INSTANTS))


def mark(flags, a, b):
    """Set flags for the sampled instants that fall inside [a, b); a is
    sorted and the intervals do not overlap."""
    if len(a) == 0:
        return
    i = np.searchsorted(a, T_INSTANTS, side='right') - 1
    ok = i >= 0
    inside = np.zeros(NB, dtype=bool)
    inside[ok] = T_INSTANTS[ok] < b[i[ok]]
    flags |= inside


def run_policy(pol, starts, ends, nreq, plan):
    """Per-app billed seconds, wake tiers and bucket flags under one policy."""
    n = len(starts)
    run_flags = np.zeros(NB, dtype=bool)
    frozen_flags = np.zeros(NB, dtype=bool)
    paused_flags = np.zeros(NB, dtype=bool)
    out = {'wakes': {'warm': 0, 'paused': 0, 'frozen': 0, 'parked': 0, 'cold': 0}, 'billed_s': 0.0, 'frozen_s': 0.0, 'requests': nreq}
    if n == 0:
        return out, run_flags, frozen_flags, paused_flags
    busy = float((ends - starts).sum())
    # gap after each busy interval (last one runs to the end of the window)
    gaps = np.empty(n)
    gaps[:-1] = starts[1:] - ends[:-1]
    gaps[-1] = SEC - ends[-1]
    lead = starts[0]  # the first request's wake comes from an unknown past: count it as parked
    if pol['kind'] == 'quiet':
        # Billed and RAM-resident through the idle timeout as today; vCPU
        # only for the first qp seconds; then frozen (unbilled) or parked.
        idle = PLANS[plan]['idle']
        qp = pol['qp']
        active_tail = np.minimum(gaps, qp)
        idle_tail = np.minimum(gaps, idle)
        frz = np.clip(gaps - idle, 0, pol['ttl']) if pol['freeze'] else np.zeros(n)
        out['frozen_s'] = float(frz.sum())
        out['billed_s'] = busy + float(idle_tail.sum())
        mark(run_flags, starts, ends + active_tail)
        mark(paused_flags, ends + active_tail, ends + idle_tail)
        mark(frozen_flags, ends + idle_tail, ends + idle_tail + frz)
        wg = gaps[:-1]
        ttl = pol['ttl'] if pol['freeze'] else 0
        if qp > 0:  # with qp=0 nothing is paused: those requests find a running VM
            out['wakes']['paused'] = int(((wg > qp) & (wg <= idle)).sum())
        out['wakes']['frozen'] = int(((wg > idle) & (wg <= idle + ttl)).sum())
        out['wakes']['parked'] = int(((wg > idle + ttl) & (wg <= COLD_AFTER)).sum()) + (1 if lead < COLD_AFTER else 0)
        out['wakes']['cold'] = int((wg > COLD_AFTER).sum()) + (0 if lead < COLD_AFTER else 1)
        out['wakes']['warm'] = nreq - sum(v for k, v in out['wakes'].items() if k != 'warm')
    elif pol['kind'] == 'today':
        idle = PLANS[plan]['idle']
        tail = np.minimum(gaps, idle)
        out['billed_s'] = busy + float(tail.sum())
        mark(run_flags, starts, ends + tail)
        wake_gaps = gaps[:-1]
        out['wakes']['parked'] = int((wake_gaps > idle).sum()) + 1
        out['wakes']['warm'] = nreq - out['wakes']['parked']
    else:
        thr = PLANS[plan]['idle'] if pol['thr'] == 'plan' else pol['thr']
        ttl = pol['ttl']
        tail = np.minimum(gaps, thr)
        frz = np.clip(gaps - thr, 0, ttl)
        out['frozen_s'] = float(frz.sum())
        out['billed_s'] = busy + float(tail.sum()) + (out['frozen_s'] if pol['billed'] else 0.0)
        mark(run_flags, starts, ends + tail)
        mark(frozen_flags, ends + tail, ends + tail + frz)
        wg = gaps[:-1]
        frozen_hit = (wg > thr) & (wg <= thr + ttl)
        parked_hit = (wg > thr + ttl) & (wg <= COLD_AFTER)
        cold_hit = wg > COLD_AFTER
        out['wakes']['frozen'] = int(frozen_hit.sum())
        out['wakes']['parked'] = int(parked_hit.sum()) + (1 if lead < COLD_AFTER else 0)
        out['wakes']['cold'] = int(cold_hit.sum()) + (0 if lead < COLD_AFTER else 1)
        out['wakes']['warm'] = nreq - out['wakes']['frozen'] - out['wakes']['parked'] - out['wakes']['cold']
    frozen_flags &= ~run_flags
    paused_flags &= ~run_flags
    return out, run_flags, frozen_flags, paused_flags


def charges(pol, plan, real_mb):
    p = PLANS[plan]
    res = p['ram'] + 8
    if pol.get('admission', 'measured') == 'reservation':
        return res, 0.0, res
    run = min(res, max(MEASURED_FLOOR, real_mb * HEADROOM))
    frozen = min(res, max(MEASURED_FLOOR, real_mb * FROZEN_OWN_FRAC * HEADROOM))
    return run, frozen, res


def capacity(run_f, frz_f, pz_f, charge_run, charge_frz, res, vcpu, node, rnd, admission):
    """Apps per node: largest N whose random draws fit the node at every
    bucket after warm-up (RAM ceiling, 2x reservation cap under measured
    admission, vCPU slots, resident VM slots). Quiet-paused instances (pz_f)
    hold RAM and reservations like running ones but no vCPU. Median of 5
    draws."""
    napps = run_f.shape[0]
    runf = run_f.astype(np.float32)
    frzf = frz_f.astype(np.float32)
    pzf = pz_f.astype(np.float32)

    def why(idx):
        """None when idx fits the node, else the first limit it breaks."""
        r, f, z = runf[idx], frzf[idx], pzf[idx]
        q = 99.9
        ram = charge_run[idx] @ (r + z) + charge_frz[idx] @ f
        if np.percentile(ram, q) > node['ceiling']:
            return 'ram'
        resv = res[idx] @ (r + z)
        cap = node['ceiling'] * (2 if admission == 'measured' else 1)
        if np.percentile(resv, q) > cap:
            return 'reservations'
        if np.percentile(vcpu[idx] @ r, q) > node['vcpu']:
            return 'vcpu_slots'
        if np.percentile(r.sum(0) + f.sum(0) + z.sum(0), q) > node['vms']:
            return 'vm_slots'
        return None

    results, binding = [], {}
    for _ in range(5):
        lo, hi, last = 0, 20000, None
        while lo < hi:
            mid = (lo + hi + 1) // 2
            reason = why(rnd.integers(0, napps, size=mid))
            if reason is None:
                lo = mid
            else:
                hi, last = mid - 1, reason
        results.append(lo)
        binding[last] = binding.get(last, 0) + 1
    return int(np.median(results)), max(binding, key=binding.get)


def main():
    data = np.load(sys.argv[1])
    counts, mem, dur = data['counts'], data['mem_mb'], data['dur_ms']
    napps = counts.shape[0]
    rnd = np.random.default_rng(11)
    plans = rnd.choice(list(MIX), size=napps, p=list(MIX.values()))
    mem = np.where(np.isfinite(mem), mem, np.nanmedian(mem))
    dur_s = np.clip(np.where(np.isfinite(dur), dur, np.nanmedian(dur)) / 1000.0, 0.005, 30.0)
    intervals = [busy_intervals(counts[i], dur_s[i], rnd) for i in range(napps)]
    report = {'apps': napps, 'invocations': int(counts.sum()), 'policies': {}}
    for name, pol in POLICIES.items():
        run_f = np.zeros((napps, NB), dtype=bool)
        frz_f = np.zeros((napps, NB), dtype=bool)
        pz_f = np.zeros((napps, NB), dtype=bool)
        cr = np.zeros(napps, dtype=np.float32)
        cf = np.zeros(napps, dtype=np.float32)
        res = np.zeros(napps, dtype=np.float32)
        vcpu = np.zeros(napps, dtype=np.float32)
        wakes = {'warm': 0, 'paused': 0, 'frozen': 0, 'parked': 0, 'cold': 0}
        billed_gbh = np.zeros(napps)
        frozen_s = 0.0
        for i in range(napps):
            starts, ends, nreq = intervals[i]
            plan = plans[i]
            real = min(PLANS[plan]['ram'], max(32.0, mem[i]))
            o, rf, ff, zf = run_policy(pol, starts, ends, nreq, plan)
            run_f[i], frz_f[i], pz_f[i] = rf, ff, zf
            cr[i], cf[i], res[i] = charges(pol, plan, real)
            vcpu[i] = PLANS[plan]['vcpu']
            for k in wakes:
                wakes[k] += o['wakes'][k]
            billed_gbh[i] = (PLANS[plan]['ram'] + 8) / 1024 * o['billed_s'] / 3600 * (30 / DAYS)
            frozen_s += o['frozen_s']
        # revenue per app-month
        incl = np.array([PLANS[p]['incl'] for p in plans])
        price = np.array([PLANS[p]['price'] for p in plans])
        overage = np.maximum(0, billed_gbh - incl) * OVERAGE_EUR_PER_GBH
        total_req = sum(wakes.values())
        mean_lat = sum(LAT[k] * v for k, v in wakes.items()) / max(total_req, 1)
        wake_events = total_req - wakes['warm']
        per_wake = {k: round(100 * v / max(wake_events, 1), 1) for k, v in wakes.items() if k != 'warm'}
        mean_wake_lat = sum(LAT[k] * v for k, v in wakes.items() if k != 'warm') / max(wake_events, 1)
        nodes = {}
        for nname, node in NODES.items():
            apps_per_node, bound = capacity(run_f, frz_f, pz_f, cr, cf, res, vcpu, node, np.random.default_rng(5),
                                            pol.get('admission', 'measured'))
            nodes[nname] = {'apps_per_node': apps_per_node, 'binding': bound,
                            'infra_eur_per_app_month': round(node['cost'] / max(apps_per_node, 1), 3)}
        report['policies'][name] = {
            'billed_gbh_per_app_month_mean': round(float(billed_gbh.mean()), 2),
            'billed_gbh_by_plan': {p: round(float(billed_gbh[plans == p].mean()), 2) for p in PLANS},
            'revenue_eur_per_app_month': round(float((price + overage).mean()), 3),
            'overage_eur_per_app_month': round(float(overage.mean()), 4),
            'wakes_per_app_week': round(wake_events / napps, 1),
            'wake_tier_share_pct': per_wake,
            'mean_wake_latency_ms': round(mean_wake_lat, 1),
            'mean_added_latency_per_request_ms': round(mean_lat, 3),
            'frozen_hours_per_app_week': round(frozen_s / 3600 / napps, 2),
            'nodes': nodes,
        }
        print(name, json.dumps(report['policies'][name]), flush=True)
    with open(sys.argv[2], 'w') as f:
        json.dump(report, f, indent=1)


if __name__ == '__main__':
    main()
