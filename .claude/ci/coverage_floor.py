#!/usr/bin/env python3
"""Floor checker for ship-blocking packages.

Reads Go cover profile files and computes statement coverage
per pkg/{fcvm,state,sched,gateway,vmmdgrpc,vmmdmount}. Exits 0
if all packages meet their floor, 1 otherwise.

Cover profile format (Go):
  github.com/onebox-faas/faas/pkg/fcvm/alloc.go:82.37,86.2 3 7
where columns are: path:start.col,end.col STMT_COUNT HIT_COUNT
"""
import argparse
import re
import sys

FLOORS = {
    # 2pp below the post-cluster-3/4 actual numbers from
    # 5-sample local noise study (2026-08-21):
    #   pkg/fcvm:       47.6 ±0
    #   pkg/state:      43.2 ±0
    #   pkg/sched:      62.3–62.5 (±0.1)
    #   pkg/gateway:    74.1 ±0
    #   pkg/vmmdgrpc:   47.8 ±0
    #   pkg/vmmdmount:  44.9 ±0
    # Floors = (min observed) − 2pp — regression tripwire with
    # headroom for cross-shard CI-noise (different go versions,
    # cache state, race-detector schedule).
    # Raise ONLY AFTER a subsequent coverage PR proves the +2pp
    # is reproducible across all 4 shards.
    "pkg/fcvm":       45,
    "pkg/state":      41,
    "pkg/sched":      60,
    "pkg/gateway":    72,
    "pkg/vmmdgrpc":   45,
    "pkg/vmmdmount":  42,
}
REPO_PREFIX = "github.com/onebox-faas/faas/"
BLOCK = re.compile(r"^(\S+):(\d+\.\d+,\d+\.\d+) (\d+) (\d+)$")


def read_blocks(paths):
    """Union block hits across test binaries and shards, as Go cover does.

    A -coverpkg profile can repeat the same source location for every test
    binary. Statement counts describe that location once; hits from any
    binary cover it. Inconsistent statement counts indicate incompatible
    inputs and must not produce a passing gate.
    """
    blocks = {}
    mode = None
    for path in paths:
        with open(path) as profile:
            header = profile.readline().strip()
            if header not in ("mode: set", "mode: count", "mode: atomic"):
                raise ValueError(f"{path}: missing or invalid coverage mode")
            if mode is not None and header != mode:
                raise ValueError(f"{path}: coverage modes differ")
            mode = header
            for line_number, raw in enumerate(profile, start=2):
                match = BLOCK.fullmatch(raw.strip())
                if match is None:
                    raise ValueError(f"{path}:{line_number}: invalid coverage block")
                file_, position, statements, hits = match.groups()
                key = (file_, position)
                count, covered = int(statements), int(hits) > 0
                previous = blocks.get(key)
                if previous is not None:
                    if previous[0] != count:
                        raise ValueError(f"{path}:{line_number}: statement counts differ for {file_}:{position}")
                    covered = covered or previous[1]
                blocks[key] = (count, covered)
    return blocks


def package_stats(blocks, exact_state=False):
    stats = {k: [0, 0] for k in FLOORS}  # [total_stmts, covered_stmts]
    for (file_, _), (count, covered) in blocks.items():
        if file_.startswith(REPO_PREFIX):
            file_ = file_[len(REPO_PREFIX):]
        if file_.endswith("_test.go"):
            continue
        for pkg in FLOORS:
            if file_.startswith(pkg + "/") or file_ == pkg:
                if pkg == "pkg/state":
                    if "/sqlc/" in file_:
                        continue  # exclude generated sqlc
                    if exact_state and "/" in file_[len(pkg) + 1:]:
                        continue  # exact package excludes every subpackage
                stats[pkg][0] += count
                if covered:
                    stats[pkg][1] += count
                break
    return stats


def main(paths, state_only=False):
    try:
        stats = package_stats(read_blocks(paths), exact_state=state_only)
    except (OSError, ValueError) as error:
        print(f"coverage-floor: {error}", file=sys.stderr)
        return 1

    if state_only:
        total, hit = stats["pkg/state"]
        # Compare integer counts before rounding the displayed percentage.
        passed = total > 0 and hit * 100 >= total * 70
        pct = hit * 100.0 / total if total else 0.0
        marker = "✓" if passed else "✗"
        print(f"pkg/state coverage: {pct:.1f}% {marker} (target ≥ 70%, exact package only)")
        return 0 if passed else 1

    ok = True
    print("coverage-floor:")
    for pkg, floor in FLOORS.items():
        tot, hit = stats[pkg]
        if tot == 0:
            print(f"  {pkg}: (no statements in any shard — skip)")
            continue
        pct = hit * 100.0 / tot
        marker = "✓" if pct >= floor else "✗"
        if pct < floor:
            ok = False
        print(f"  {pkg}: {pct:.1f}% (floor ≥ {floor}%) {marker}")
    if not ok:
        print("coverage-floor: at least one package below floor")
        return 1
    print("coverage-floor: all ship-blocking packages ≥ floor ✓")
    return 0


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--state-only", action="store_true", help="check exact pkg/state against its 70%% floor")
    parser.add_argument("profiles", nargs="+")
    args = parser.parse_args()
    sys.exit(main(args.profiles, state_only=args.state_only))
