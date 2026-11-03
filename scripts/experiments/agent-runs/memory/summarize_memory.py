#!/usr/bin/env python3
"""Summarise TestMetalAgentMemory JSON lines into the experiment's tables.

usage: summarize_memory.py RESULTS.jsonl [MORE.jsonl ...]

Private memory is anon + shmem in the VM's cgroup (ADR-20261027090011's meter). File
pages charged to the VM are listed apart; pages of the snapshot file mapped
by a restored VM but charged elsewhere show up as Pss_File in smaps.
"""

from __future__ import annotations

import collections
import json
import statistics
import sys

MB = 1 << 20


def mean(xs):
    xs = [x for x in xs if x is not None]
    return statistics.fmean(xs) if xs else None


def fmt(x, nd=0):
    return "—" if x is None else f"{x:.{nd}f}"


def private_mb(s):
    return (s.get("anon", 0) + s.get("shmem", 0)) / MB


def load(paths):
    rows = []
    for p in paths:
        with open(p, encoding="utf-8") as f:
            rows += [json.loads(line) for line in f if line.strip()]
    return rows


def samples_by_vm(rows):
    by = collections.defaultdict(list)
    for r in rows:
        if r["kind"] == "sample":
            by[(r.get("label"), r["vm"])].append(r)
    return by


def cell_a(rows, by):
    runs = {(r.get("label"), r["vm"]): r for r in rows if r["kind"] == "run" and r.get("cell") == "A"}
    groups = collections.defaultdict(list)
    for key, ss in by.items():
        first = ss[0]
        if first.get("cell") != "A":
            continue
        active = [s for s in ss if s.get("phase") == "running"] or ss
        run = runs.get(key)
        st = run["status"] if run else {}
        tl = st.get("timeline") or []
        holds = sum((c["released"] or 0) - (c["hold_start"] or 0) for c in tl if c.get("hold_start"))
        groups[(first["preset"], first.get("start"))].append({
            "priv_avg": mean(private_mb(s) for s in active),
            "priv_max": max(private_mb(s) for s in active),
            "cur_peak": max(s.get("current", 0) for s in ss) / MB,
            "file_max": max(s.get("file", 0) for s in ss) / MB,
            "pss_file": max(s.get("smaps_kb", {}).get("Pss_File", 0) for s in ss) / 1024,
            "tree": max(s.get("tree_rss_kb", 0) or 0 for s in ss) / 1024,
            "first_call_s": (tl[0]["arrived"] - st["started"]) if tl else None,
            "overhead_s": (st["ended"] - st["started"] - holds) if st.get("ended") else None,
            "ok": run["ok"] if run else None,
        })
    print("\n## Cell A: active profile (MB; s)\n")
    print("| preset | start | n | private avg | private max | charge peak | file charged | snapshot pages mapped | guest harness RSS | to first model call | harness time excl. holds | ok |")
    print("|---|---|---|---|---|---|---|---|---|---|---|---|")
    for (preset, start), xs in sorted(groups.items()):
        print(f"| {preset} | {start} | {len(xs)} | {fmt(mean(x['priv_avg'] for x in xs))} | {fmt(mean(x['priv_max'] for x in xs))} "
              f"| {fmt(mean(x['cur_peak'] for x in xs))} | {fmt(mean(x['file_max'] for x in xs))} | {fmt(mean(x['pss_file'] for x in xs))} "
              f"| {fmt(mean(x['tree'] for x in xs))} | {fmt(mean(x['first_call_s'] for x in xs), 2)} | {fmt(mean(x['overhead_s'] for x in xs), 1)} "
              f"| {sum(1 for x in xs if x['ok'])}/{len(xs)} |")


def cpu_windows(ss, t0, t1, key="cpu_usec", scale=1000, window=2.0):
    """CPU ms per window from a cumulative counter in the samples."""
    pts = [(s["t"], s[key]) for s in ss if t0 <= s["t"] <= t1 and (s.get(key) or -1) >= 0]
    out = []
    j = 0
    for i, (t, u) in enumerate(pts):
        while j < i and t - pts[j][0] > window:
            j += 1
        if t - pts[j][0] >= window * 0.9:
            out.append((u - pts[j][1]) / scale * window / (t - pts[j][0]))
    return out


def cell_c(rows, by):
    """CPU while the harness waits on a held response, without a pause."""
    starts = {(r["label"], r["vm"]): r["t"] for r in rows if r.get("event") == "wait_start"}
    ends = [r for r in rows if r.get("event") == "wait_end" and r.get("mode") == "none"]
    print("\n## Cell C: CPU per 2 s while waiting (ms; ADR-20261027090012 pauses under 50)\n")
    print("| label | preset | VM cgroup mean / max | harness tree mean / max | probe mean / max |")
    print("|---|---|---|---|---|")
    for ev in sorted(ends, key=lambda r: (r["preset"], r["label"])):
        key = (ev["label"], ev["vm"])
        ss, t0 = by.get(key, []), starts.get(key)
        if not t0:
            continue
        cols = []
        for k, scale in (("cpu_usec", 1000), ("tree_cpu_ms", 1), ("probe_cpu_ms", 1)):
            w = cpu_windows(ss, t0, ev["t"] - 1, k, scale)
            cols.append(f"{fmt(mean(w), 1)} / {fmt(max(w) if w else None, 1)}")
        print(f"| {ev['label']} | {ev['preset']} | " + " | ".join(cols) + " |")


def cell_b(rows, by):
    starts = {(r["label"], r["vm"]): r["t"] for r in rows if r.get("event") == "wait_start"}
    ends = {(r["label"], r["vm"]): r for r in rows if r.get("event") == "wait_end"}
    print("\n## Cell B: waiting on the model (MB; ms)\n")
    print("| label | preset | mode | charge while waiting | after freeze | swapped | zswap pool | ratio | reclaim ms | thaw ms | thaw→status ms | release→next request s | zswpin 5 s after | CPU per 2 s while waiting (mean / max ms) |")
    print("|---|---|---|---|---|---|---|---|---|---|---|---|---|---|")
    for key, ev in sorted(ends.items(), key=lambda kv: (kv[1]["preset"], kv[0][0], kv[1]["mode"])):
        ss = by.get(key, [])
        t0 = starts.get(key)
        waiting = [s for s in ss if t0 and t0 <= s["t"] <= ev["t"]]
        fr = ev.get("freeze") or {}
        fs = ev.get("frozen_sample") or {}
        zs, zp = fs.get("zswapped", 0), fs.get("zswap", 0)
        after = [s for s in ss if s["t"] >= ev["t"] - 6 and s["t"] <= ev["t"]]
        zin = (max(s.get("zswpin", 0) for s in after) - min(s.get("zswpin", 0) for s in after)) if after else None
        cpu = cpu_windows(ss, t0, ev["t"] - 1) if t0 and ev["mode"] == "none" else []
        charge = mean(s.get("current", 0) / MB for s in waiting if not s.get("frozen")) if waiting else None
        print(f"| {key[0]} | {ev['preset']} | {ev['mode']} | {fmt(charge)} | {fmt(fr.get('after', 0) / MB if fr else None)} "
              f"| {fmt(fr.get('swapped', 0) / MB if fr else None)} | {fmt(zp / MB if fs else None)} | {fmt(zs / zp if zp else None, 1)} "
              f"| {fmt(fr.get('reclaim_ms'))} | {fmt(ev.get('thaw_ms'), 1)} | {fmt(ev.get('thaw_to_status_ms'), 1)} "
              f"| {fmt(ev.get('next_request_s'), 2)} | {fmt(zin)} | {fmt(mean(cpu), 1)} / {fmt(max(cpu) if cpu else None, 1)} |")


def cell_d(rows, by):
    print("\n## Cell D: growth over 20 long turns (private MB at call n)\n")
    print("| preset | call 1 | call 5 | call 10 | call 15 | call 20 | max | ok |")
    print("|---|---|---|---|---|---|---|---|")
    runs = {(r.get("label"), r["vm"]): r for r in rows if r["kind"] == "run" and r.get("cell") == "D"}
    for key, ss in sorted(by.items()):
        if ss[0].get("cell") != "D":
            continue
        at = {}
        for s in ss:
            if s.get("calls"):
                at.setdefault(s["calls"], private_mb(s))
        pick = [at.get(n) for n in (1, 5, 10, 15, 20)]
        run = runs.get(key)
        print(f"| {ss[0]['preset']} | " + " | ".join(fmt(x) for x in pick)
              + f" | {fmt(max(private_mb(s) for s in ss))} | {run['ok'] if run else '—'} |")


def cell_e(rows):
    print("\n## Cell E: k restores of one snapshot (MB)\n")
    print("| label | preset | k | phase | Σ charge | Σ private | Σ Rss | Σ Pss | shared (1 − Pss/Rss) | host used Δ |")
    print("|---|---|---|---|---|---|---|---|---|---|")
    for r in rows:
        if r["kind"] != "group":
            continue
        vms = r["vms"]
        rss = sum(v.get("smaps_kb", {}).get("Rss", 0) for v in vms) / 1024
        pss = sum(v.get("smaps_kb", {}).get("Pss", 0) for v in vms) / 1024
        hb, h = r["host_before"], r["host"]
        used = ((hb["MemAvailable"] - h["MemAvailable"]) / 1024) if hb and h else None
        print(f"| {r['label']} | {r['preset']} | {r['k']} | {r['phase']} | {fmt(sum(v.get('current', 0) for v in vms) / MB)} "
              f"| {fmt(sum(private_mb(v) for v in vms))} | {fmt(rss)} | {fmt(pss)} | {fmt(1 - pss / rss if rss else None, 2)} | {fmt(used)} |")
    bursts = collections.defaultdict(list)
    for r in rows:
        if r["kind"] == "burst":
            bursts[(r["label"], r["preset"])].append(r)
    for (label, preset), bs in bursts.items():
        peak = max(b["sum_current"] for b in bs) / MB
        print(f"\nburst after release ({label}, {preset}): Σ charge peak {peak:.0f} MB")


def snapshots(rows):
    print("\n## Snapshots\n")
    print("| label | preset | warm run s | memory file | stored |")
    print("|---|---|---|---|---|")
    for r in rows:
        if r["kind"] == "snapshot":
            print(f"| {r['label']} | {r['preset']} | {r['warm_s']:.1f} | {r['mem_bytes'] / MB:.0f} | {r['stored_bytes'] / MB:.0f} |")


def main() -> int:
    rows = load(sys.argv[1:])
    by = samples_by_vm(rows)
    for r in rows:
        if r["kind"] == "config":
            print(f"config {r['label']}: fpr={r['fpr']} swap_ready={r['swap_ready']} zswap={r['zswap']} mem={r['mem_mib']} MiB hold={r['hold_s']} s")
    snapshots(rows)
    cell_a(rows, by)
    cell_b(rows, by)
    cell_c(rows, by)
    cell_d(rows, by)
    cell_e(rows)
    return 0


if __name__ == "__main__":
    sys.exit(main())
