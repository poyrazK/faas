# Challenge solve time: browser estimate for H3

ADR-20261024082000 criterion H3 asks that a 2020 mid-range Android phone,
running current Chrome, solve the challenge at the default difficulty (16
bits) within 2 s at the 95th percentile. That is a manual measurement on the
device. This report is a browser estimate taken before it. The estimate
showed the original solver would miss the target, so ADR-831 amendment 15
changed the solver.

## Method

`h3.mjs` rebuilds the challenge page from
`pkg/gateway/edge_rules_challenge_page.go`, with the same parts, solver
script and Content-Security-Policy the gateway serves. It serves the page to
headless Chrome from a local server.

- **Tokens:** each navigation gets a fresh token shaped like the gateway's.
  It is 225 bytes: `c1.` + base64url JSON with two UUIDs, a 16-byte nonce,
  the difficulty, the pass TTL and the expiry, then `.` + a 16-byte MAC.
- **Verification:** the server checks every submitted counter with SHA-256,
  as the gateway does. All 200 were valid.
- **What each trial records:**
  - the wall time from serving the page to receiving the solution;
  - the counter (the solver tries 0, 1, 2, ..., so hashes = counter + 1);
  - the renderer's CPU time, from `SystemInfo.getProcessInfo`.
- **Throttling:** trials ran at Chrome's CPU throttling rates of 1x, 6x and
  10x. Throttling also inflates the renderer's CPU time, so the CPU cost per
  hash comes from the 40 unthrottled trials alone.
- **Analysis:** `analyze.py` reports wall-time quantiles per rate and fits
  CPU = a + b x hashes over the unthrottled trials. It then extrapolates. The
  work a solve needs is geometric with p = 2^-16: the median is 45,426
  hashes and the 95th percentile is 196,328.
- **Host:** an Apple M3 Pro, with Chrome on 2026-10-25. Other work held the
  load average at 60-100, so wall times here are inflated. CPU time is not.

**Device factors.** Geekbench 6 single-core scores were about 3,000 for the
M3 Pro (two published uploads: 2,975 and 3,071). For 2020 mid-range phones
they were about 325-335 (Galaxy A51 with Exynos 9611: 325, runs 261-372;
Snapdragon 665: 334). That is about 9x slower, and 11.5x on the slowest
runs. The upper end of 2020 mid-range (Pixel 4a, Snapdragon 730G, about 550)
is about 5.5x slower. Geekbench is a proxy for JavaScript speed on these
phones, not a measure of it.

## Results

| | Before (`before.json`) | After amendment 15 (`after.json`) |
|---|---|---|
| Renderer CPU per hash | 2.03 us (+26 ms per page) | 0.39 us (+21 ms per page) |
| Chrome 6x throttle, wall p50 / p95 | 644 ms / 2,607 ms | 230 ms / 439 ms |
| Chrome 10x throttle, wall p50 / p95 | 1,750 ms / 6,427 ms | 440 ms / 679 ms |
| Estimated p95 on a phone 5.5x slower (Pixel 4a class) | 2.34 s | 0.53 s |
| Estimated p95 on a phone 9x slower (Galaxy A51 class) | 3.83 s | 0.88 s |
| Estimated p95 on a phone 11.5x slower (slowest runs) | 4.89 s | 1.12 s |

The before column is main's solver before this change, which had not changed
since amendment 7. The after column is this change's. Each column had 100 trials: 40 at 1x, 30 at 6x and 30 at 10x.

**Why the original was slow.** At 225 bytes, `token + ":" + counter` spans
four 64-byte SHA-256 blocks. The original solver hashed all four, and
allocated a new buffer, on every attempt, although only the last block
changes. The new solver hashes the first three blocks once per page and
reuses its buffers. It finds exactly the counters the original found.
`TestChallengeSolverJS` checks that against Go for token lengths 1-140 and
for starts where the counter gains a digit.

**Not measured here:** a real phone, including its thermal state and
whether Chrome schedules the page on a big or a little core. H3 stays open
until that measurement is recorded in `docs/STATUS.md`.

## Reproduce

```sh
node h3.mjs <repo> out.json 1:40 6:30 10:30   # CHROME=<binary> elsewhere than macOS
python3 analyze.py out.json
```

`SOLVER=<file> node h3.mjs ...` swaps in a candidate solver that defines
`solve(token, difficulty, start, limit)`, keeping the page part.
