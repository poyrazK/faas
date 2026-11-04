"""Summarise h3.mjs trials and extrapolate the challenge solve time to slower phones.

Per throttle rate: empirical wall-time quantiles. Across all trials: renderer
CPU per hash (least squares over the unthrottled trials, cpu = a + b *
hashes), contention-free. The
hashes a solve needs are geometric with p = 2^-d, so the q-quantile is
ln(1/(1-q)) * 2^d; p95 needs about 3x the mean work.
"""
import json
import math
import sys

rows = json.load(open(sys.argv[1]))
assert all(r["valid"] for r in rows), "a submitted counter failed the SHA-256 check"


def q(xs, p):
    xs = sorted(xs)
    return xs[min(int(p * len(xs)), len(xs) - 1)]


print(f"trials {len(rows)}, token length {rows[0]['tokenLen']}, load average {min(r['load1'] for r in rows)}-{max(r['load1'] for r in rows)}")
for rate in sorted({r["rate"] for r in rows}):
    rs = [r for r in rows if r["rate"] == rate]
    w = [r["wallMs"] for r in rs]
    h = [r["hashes"] for r in rs]
    print(f"throttle {rate:>2}x: {len(rs)} trials; hashes p50 {q(h, .5):,} p95 {q(h, .95):,}; wall p50 {q(w, .5):,.0f} ms, p95 {q(w, .95):,.0f} ms, max {max(w):,.0f} ms")

# Chrome's CPU throttling also inflates the renderer's CPU time, so the cost
# per hash comes from the unthrottled trials only.
base = [r for r in rows if r["rate"] == 1]
xs = [r["hashes"] for r in base]
ys = [r["cpuMs"] for r in base]
n = len(xs)
mx, my = sum(xs) / n, sum(ys) / n
b = sum((x - mx) * (y - my) for x, y in zip(xs, ys)) / sum((x - mx) ** 2 for x in xs)
a = my - b * mx
print(f"renderer CPU = {a:.1f} ms + {b * 1000:.2f} us/hash (unthrottled trials, {n})")

print("\nextrapolated solve time = (a + b * hashes) * device slowdown vs this M3 Pro")
print("difficulty  p50 hashes  p95 hashes   " + "   ".join(f"p95 @{f}x" for f in (5.5, 9, 11.5)))
for d in (14, 15, 16):
    p50h = math.log(2) * 2**d
    p95h = math.log(20) * 2**d
    cells = "   ".join(f"{(a + b * p95h) * f / 1000:6.2f} s" for f in (5.5, 9, 11.5))
    print(f"{d:>10}  {p50h:>10,.0f}  {p95h:>10,.0f}   {cells}")
