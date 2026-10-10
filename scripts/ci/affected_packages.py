#!/usr/bin/env python3
"""Select the Go packages a change can affect, for the light PR CI tier.

Reads a newline-delimited changed-path list on stdin and prints shell-safe
package lists derived from `go list`:

  changed  packages whose directory (or a non-package subdirectory such as
           testdata/) contains a changed file
  vet      changed packages plus their direct importers (production, in-package
           test and external test imports): compile-checked by `go vet`
  test     changed packages, optionally narrowed to one shard: tested
  split    selected packages too slow for one shard, tested by name per shard

Global inputs (go.mod, go.sum, this selector and the light workflow) vet
every package; tests still cover changed packages only. The light tier keeps a pull request under ~10 minutes by
compile-checking importers instead of testing them; the mega tier (ci.yml)
runs every package's -race tests nightly and before every release.

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

# CLAUDE.md §6.2 invariants (property-based, never deleted). They are tested
# whenever any package in their transitive import tree changes, not only when
# they import a changed package directly, and on every global-input change.
INVARIANT_PACKAGES = ("tests/property",)

# Packages too slow under -race for one shard's budget. Instead of being
# assigned whole, each one's tests are split by name across every shard
# (scripts/ci/e2eshard, as the mega tier's state shards do). `split` lists
# the ones a change selects.
SPLIT_PACKAGES = ("cmd/apid", "pkg/state")

# Relative cost of a package's race test run, used only to balance shards.
# Unlisted packages weigh 1. Measured from the mega tier's shard timings.
WEIGHTS = {
    "cmd/apid": 40,
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
    # A global input (dependency bump, CI change) vets every package but still
    # tests only changed ones: re-running the whole tree would blow the light
    # budget, and the mega tier tests everything nightly and before release.
    if everything:
        vet = set(pkgs)
    else:
        changed_paths = {pkgs[d]["path"] for d in changed}
        vet = set(changed) | {d for d, info in pkgs.items() if info["imports"] & changed_paths}
    test = set(changed)
    for inv in INVARIANT_PACKAGES:
        if inv in pkgs and (everything or (internal_deps(inv, pkgs) | {inv}) & changed):
            test.add(inv)
    test = {d for d in test if not d.startswith(EXCLUDED_TEST_PREFIXES)}
    return everything, sorted(changed), sorted(vet), sorted(test)


def internal_deps(root, pkgs):
    """Module-internal package dirs reachable from root's imports (test imports
    included; over-approximating only ever selects more)."""
    by_path = {info["path"]: d for d, info in pkgs.items()}
    seen, stack = set(), [root]
    while stack:
        for imp in pkgs[stack.pop()]["imports"]:
            d = by_path.get(imp)
            if d is not None and d not in seen:
                seen.add(d)
                stack.append(d)
    return seen


def shard(dirs, index, count):
    """Longest-processing-time assignment: deterministic and roughly balanced."""
    bins = [[0, []] for _ in range(count)]
    dirs = [d for d in dirs if d not in SPLIT_PACKAGES]
    for d in sorted(dirs, key=lambda d: (-WEIGHTS.get(d, 1), d)):
        target = min(range(count), key=lambda b: (bins[b][0], b))
        bins[target][0] += WEIGHTS.get(d, 1)
        bins[target][1].append(d)
    return sorted(bins[index - 1][1])


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("kind", choices=["changed", "vet", "test", "split", "summary"])
    ap.add_argument("--shard", type=int, default=1)
    ap.add_argument("--shards", type=int, default=1)
    args = ap.parse_args()
    if not 1 <= args.shard <= args.shards:
        ap.error("--shard must be within 1..--shards")

    everything, changed, vet, test = select(sys.stdin.read().splitlines(), go_packages())
    if args.kind == "summary":
        print(f"global inputs changed: {str(everything).lower()}")
        print(f"changed packages ({len(changed)}): {' '.join(changed) or '<none>'}")
        print(f"vet packages ({len(vet)}): {' '.join(vet) or '<none>'}")
        print(f"test packages ({len(test)}): {' '.join(test) or '<none>'}")
        return
    if args.kind == "changed":
        dirs = changed
    elif args.kind == "vet":
        dirs = vet
    elif args.kind == "split":
        dirs = [d for d in test if d in SPLIT_PACKAGES]
    else:
        dirs = shard(test, args.shard, args.shards)
    print(" ".join("./" + d if d != "." else "." for d in dirs))


if __name__ == "__main__":
    main()
