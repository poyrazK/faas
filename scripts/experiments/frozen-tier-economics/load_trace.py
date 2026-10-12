"""Load a sample of HTTP apps from the Azure Functions 2019 trace.

Writes apps.npz: per-app per-minute invocation counts (7 days), average
allocated memory (MB) and average function duration (ms).
Usage: python3 -I load_trace.py <trace-dir> <out.npz> [sample] [seed]
"""
import csv
import random
import sys

import numpy as np

trace, out = sys.argv[1], sys.argv[2]
sample = int(sys.argv[3]) if len(sys.argv) > 3 else 3000
seed = int(sys.argv[4]) if len(sys.argv) > 4 else 7
days = 7
csv.field_size_limit(10**8)


def rows(day):
    with open(f'{trace}/invocations_per_function_md.anon.d{day:02d}.csv', newline='') as f:
        r = csv.reader(f)
        next(r)
        for row in r:
            yield row


# Apps with an HTTP trigger on day 1.
http_apps = set()
for row in rows(1):
    if row[3] == 'http':
        http_apps.add(row[1])
rnd = random.Random(seed)
chosen = sorted(rnd.sample(sorted(http_apps), min(sample, len(http_apps))))
index = {a: i for i, a in enumerate(chosen)}
counts = np.zeros((len(chosen), days * 1440), dtype=np.int32)
for day in range(1, days + 1):
    base = (day - 1) * 1440
    for row in rows(day):
        i = index.get(row[1])
        if i is None:
            continue
        counts[i, base:base + 1440] += np.asarray(row[4:4 + 1440], dtype=np.int32)

mem = np.full(len(chosen), np.nan)
with open(f'{trace}/app_memory_percentiles.anon.d01.csv', newline='') as f:
    r = csv.reader(f)
    next(r)
    for row in r:
        i = index.get(row[1])
        if i is not None:
            mem[i] = float(row[3])

dur_sum = np.zeros(len(chosen))
dur_n = np.zeros(len(chosen))
with open(f'{trace}/function_durations_percentiles.anon.d01.csv', newline='') as f:
    r = csv.reader(f)
    next(r)
    for row in r:
        i = index.get(row[1])
        if i is not None:
            dur_sum[i] += float(row[3]) * float(row[4])
            dur_n[i] += float(row[4])
dur = np.where(dur_n > 0, dur_sum / np.maximum(dur_n, 1), np.nan)
np.savez_compressed(out, counts=counts, mem_mb=mem, dur_ms=dur)
print(f'apps={len(chosen)} http_apps_day1={len(http_apps)} invocations={counts.sum():,} '
      f'mem_known={np.isfinite(mem).sum()} dur_known={np.isfinite(dur).sum()}')
