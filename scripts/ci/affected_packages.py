#!/usr/bin/env python3
"""Select the Go packages a change can affect, for the light PR CI tier.

Reads a newline-delimited changed-path list on stdin and prints shell-safe
package lists derived from `go list`:

  changed  packages whose directory (or a non-package subdirectory such as
           testdata/) contains a changed file
  test     changed packages plus their direct importers (production, in-package
           test and external test imports), optionally narrowed to one shard

Global inputs (go.mod, go.sum, this selector and the light workflow) select
every package. The mega tier (ci.yml) still runs the complete suite nightly
and before every release, so transitive importers beyond one level are caught
there rather than on every push.

Usage:
  git diff --name-only BASE HEAD | scripts/ci/affected_packages.py changed
  git diff --name-only BASE HEAD | scripts/ci/affected_packages.py test --shard 1 --shards 3
"""
import argparse
import json
import os
import subprocess
import sys

MODULE = "github.com/onebox-faas/faas"

# Changes here can alter any package's build or test outcome.
GLOBAL_INPUTS = {
    "go.mod",
    "go.sum",
    ".github/workflows/ci-light.yml",
    "scripts/ci/affected_packages.py",
}

# Packages with their own dedicated jobs or harnesses: cmd/e2e runs as the
# mega tier's sharded e2e suite and migrations as the light migration gate
# plus the mega history suite.
EXCLUDED_TEST_PREFIXES = ("cmd/e2e", "migrations")

# Relative cost of a package's race test run, used only to balance shards.
# Unlisted packages weigh 1. Measured from the mega tier's shard timings.
WEIGHTS = {
    "cmd/apid": 40,
    "pkg/state": 30,
    "cmd/gregale": 8,
    "pkg/e2etest": 6,
    "pkg/sched": 5,
    "pkg/gateway": 5,
    "cmd/gregalectl": 4,
    "pkg/reconcile": 4,
    "pkg/meter": 3,
}


def go_packages():
    out = subprocess.run(
        [os.environ.get("GO", "go"), "list", "-e", "-json=ImportPath,Dir,Imports,TestImports,XTestImports", "./..."],
        check=True, capture_output=True, text=True).stdout
    root = os.getcwd()
    decoder = json.JSONDecoder()
    pkgs, i = {}, 0
    while i < len(out):
        while i < len(out) and out[i].isspace():
            i += 1
        if i >= len(out):
            break
        obj, i = decoder.raw_decode(out, i)
        rel = os.path.relpath(obj["Dir"], root)
        imports = set(obj.get("Imports") or []) | set(obj.get("TestImports") or []) | set(obj.get("XTestImports") or [])
        pkgs[rel] = {"path": obj["ImportPath"], "imports": imports}
    return pkgs


def owning_package(path, pkg_dirs):
    """Nearest enclosing package directory for a changed file, if any."""
    d = os.path.dirname(path)
    while d:
        if d in pkg_dirs:
            return d
        d = os.path.dirname(d)
    # Root-level files belong to the root package only when one exists; files
    # under non-package trees (docs/, deploy/ templates) select nothing here.
    return "." if os.path.dirname(path) == "" and "." in pkg_dirs else None


def select(paths, pkgs):
    paths = [p.strip() for p in paths]
    paths = [p[2:] if p.startswith("./") else p for p in paths if p]
    everything = any(p in GLOBAL_INPUTS for p in paths)
    changed = set()
    for p in paths:
        owner = owning_package(p, pkgs)
        if owner is not None:
            changed.add(owner)
    if everything:
        test = set(pkgs)
    else:
        changed_paths = {pkgs[d]["path"] for d in changed}
        test = set(changed) | {d for d, info in pkgs.items() if info["imports"] & changed_paths}
    test = {d for d in test if not d.startswith(EXCLUDED_TEST_PREFIXES)}
    return everything, sorted(changed), sorted(test)


def shard(dirs, index, count):
    """Longest-processing-time assignment: deterministic and roughly balanced."""
    bins = [[0, []] for _ in range(count)]
    for d in sorted(dirs, key=lambda d: (-WEIGHTS.get(d, 1), d)):
        target = min(range(count), key=lambda b: (bins[b][0], b))
        bins[target][0] += WEIGHTS.get(d, 1)
        bins[target][1].append(d)
    return sorted(bins[index - 1][1])


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("kind", choices=["changed", "test", "summary"])
    ap.add_argument("--shard", type=int, default=1)
    ap.add_argument("--shards", type=int, default=1)
    args = ap.parse_args()
    if not 1 <= args.shard <= args.shards:
        ap.error("--shard must be within 1..--shards")

    everything, changed, test = select(sys.stdin.read().splitlines(), go_packages())
    if args.kind == "summary":
        print(f"global inputs changed: {str(everything).lower()}")
        print(f"changed packages ({len(changed)}): {' '.join(changed) or '<none>'}")
        print(f"test packages ({len(test)}): {' '.join(test) or '<none>'}")
        return
    dirs = changed if args.kind == "changed" else shard(test, args.shard, args.shards)
    print(" ".join("./" + d if d != "." else "." for d in dirs))


if __name__ == "__main__":
    main()
