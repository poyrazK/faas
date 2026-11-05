#!/usr/bin/env python3
"""Select the Go packages a change can affect, for the light PR CI tier.

Reads a newline-delimited changed-path list on stdin and prints shell-safe
package lists derived from `go list`:

  changed  packages whose directory (or a non-package subdirectory such as
           testdata/) contains a changed file
  vet      changed packages plus their direct importers (production, in-package
           test and external test imports): compile-checked by `go vet`
  test     changed packages that run in full with -race, optionally narrowed
           to one shard
  selective  the large packages pinned to a shard, when the change selects
           them: the light tier runs only their related tests (without -race,
           scripts/ci/select_package_tests.py); the mega tier runs them in full
  pinned   the large packages a shard owns, whether or not they changed

A global input (go.mod, go.sum, this selector or the light workflow) puts
every package in the vet set; tests still cover changed packages only.

A text-contract package (TEXT_CONTRACT_PACKAGES) also counts as changed when
a file its tests read as text changes: a workflow, an Ansible role, a deploy
or release script. Its inputs are read from the tests themselves. The
light tier compile-checks only the importers that use a changed exported
name (scripts/ci/select_importers.py); the mega tier (ci.yml) vets every
package and runs every package's -race tests nightly and before every
release.

Usage:
  git diff --name-only BASE HEAD | scripts/ci/affected_packages.py changed
  git diff --name-only BASE HEAD | scripts/ci/affected_packages.py test --shard 1 --shards 3
  git diff --name-only BASE HEAD | scripts/ci/affected_packages.py selective --shard 3
"""
import argparse
import glob
import json
import os
import re
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

# Suites too large for the light tier's 10-minute budget (thousands of tests
# each). A pull request runs only the tests related to its change, without
# -race; the mega tier runs them in full with -race nightly (it does not gate
# releases). Every shard owns one, and pkg/state and pkg/apid/apidserver keep
# separate PostgreSQL services (pkg/state uses the shard's second one).
# pkg/sched's full -race run reached the 10-minute package deadline on its own
# (#4153), so it joined them on the lightest shard, beside pkg/state; it uses
# that shard's first PostgreSQL service.
SELECTIVE_SHARD = {
    "cmd/gregale": 1,
    "pkg/sched": 2,
    "pkg/state": 2,
    "pkg/apid/apidserver": 3,
}
# Shard-balancing weight of a pinned selective package (compile plus capped
# test run), so the -race packages are spread around it.
SELECTIVE_WEIGHT = 6

# Packages whose tests check workflows, Ansible roles and deploy or release
# scripts as text. Those files are no Go package's input, so without this a
# change to one ran none of the tests that read it: PR #4926 changed
# scripts/ci/unit_deploy.sh and broke cmd/gregalectl's unit-lane contract
# test unseen by the light tier. PR #4950 then changed postgres_capacity's
# tasks and broke pkg/daemonunitspec's role test the same way; pkg/db's tests
# read that role's pool budgets.
TEXT_CONTRACT_PACKAGES = ("cmd/gregalectl", "pkg/daemonunitspec", "pkg/db")

# The ways those tests name a repository file: filepath.Join climbing from the
# package directory to the repository root, or from the repository root by
# name (REPO_ROOTS), readRepoFile(t, parts...) and readWorkflow(t, name). One
# level of call nesting is allowed for filepath.Join(repoRoot(t), ...).
REPO_JOIN = re.compile(r"\b(filepath\.Join|readRepoFile|readWorkflow)\(((?:[^()]|\([^()]*\))*)\)")
REPO_ROOTS = ("repoRoot", "repoRoot(t)", "repoRoot()", "root", "repo")
GO_STRING = re.compile(r'^"([^"\\*]*)"$')

# Relative cost of a package's race test run, used only to balance shards.
# Unlisted packages weigh 1. Measured from the mega tier's shard timings.
WEIGHTS = {
    # About 17 minutes each with -race on a light shard (#4153, 2026-10-24).
    # Weighed at 1, both landed beside pkg/sched and pkg/state, and that shard
    # ran out of its 40 minutes before the large-package builds finished.
    "cmd/gatewayd-internal": 8,
    "pkg/environmentsync/pgintegration": 8,
    "pkg/e2etest": 6,
    "pkg/gateway": 5,
    "cmd/gregalectl": 4,
    "pkg/operations/acceptance": 4,
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


def literal_path(parts):
    """The repository path the leading string-literal parts name: an exact
    file, or a directory prefix ending in / when a later part is a variable
    or a glob. Empty when nothing is literal."""
    names = []
    for part in parts:
        m = GO_STRING.match(part)
        if not m:
            return "/".join(names) + "/" if names else ""
        names.append(m.group(1))
    return "/".join(names)


def text_inputs(pkg_dir, root="."):
    """Repository paths (files, or prefixes ending in /) that the package's
    tests read as text."""
    climb = ['".."'] * (pkg_dir.count("/") + 1)
    found = set()
    for test in sorted(glob.glob(os.path.join(root, pkg_dir, "*_test.go"))):
        with open(test, encoding="utf-8") as fh:
            src = fh.read()
        for m in REPO_JOIN.finditer(src):
            args = [a.strip() for a in m.group(2).split(",")]
            if m.group(1) == "readRepoFile":
                parts = args[1:]
            elif m.group(1) == "readWorkflow":
                parts = ['".github"', '"workflows"'] + args[1:]
            elif args[:len(climb)] == climb:
                parts = args[len(climb):]
            elif args[:1] and args[0] in REPO_ROOTS:
                parts = args[1:]
            else:
                continue
            path = literal_path(parts)
            # A literal directory (a role) covers every file under it.
            if path and not path.endswith("/") and os.path.isdir(os.path.join(root, path)):
                path += "/"
            if path:
                found.add(path)
    return found


def reads(path, inputs):
    return any(path == i or (i.endswith("/") and path.startswith(i)) for i in inputs)


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


def select(paths, pkgs, contract_inputs=None):
    """contract_inputs maps a text-contract package to the paths its tests
    read (text_inputs); None reads them from the tests."""
    paths = [p.strip() for p in paths]
    paths = [p[2:] if p.startswith("./") else p for p in paths if p]
    everything = any(p in GLOBAL_INPUTS for p in paths)
    changed = set()
    for p in paths:
        owner = owning_package(p, pkgs)
        if owner is not None:
            changed.add(owner)
    if contract_inputs is None:
        contract_inputs = {d: text_inputs(d) for d in TEXT_CONTRACT_PACKAGES if d in pkgs}
    for d, inputs in contract_inputs.items():
        if d in pkgs and any(reads(p, inputs) for p in paths):
            changed.add(d)
    # A global input (dependency bump, CI change) vets every package but still
    # tests only changed ones: re-running the whole tree would blow the light
    # budget, and the mega tier tests everything nightly.
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
    """Longest-processing-time assignment of the -race packages: deterministic
    and roughly balanced, counting each shard's pinned selective package."""
    bins = [[0, []] for _ in range(count)]
    for pkg, pinned in SELECTIVE_SHARD.items():
        if pkg in dirs and pinned <= count:
            bins[pinned - 1][0] += SELECTIVE_WEIGHT
    dirs = [d for d in dirs if d not in SELECTIVE_SHARD]
    for d in sorted(dirs, key=lambda d: (-WEIGHTS.get(d, 1), d)):
        target = min(range(count), key=lambda b: (bins[b][0], b))
        bins[target][0] += WEIGHTS.get(d, 1)
        bins[target][1].append(d)
    return sorted(bins[index - 1][1])


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("kind", choices=["changed", "vet", "test", "selective", "pinned", "summary"])
    ap.add_argument("--shard", type=int, default=1)
    ap.add_argument("--shards", type=int, default=1)
    args = ap.parse_args()
    if not 1 <= args.shard <= args.shards:
        ap.error("--shard must be within 1..--shards")

    if args.kind == "pinned":
        # The large packages a shard owns, changed or not: main pushes warm
        # their test builds so pull requests restore them from the shard's cache.
        print(" ".join("./" + d for d, s in SELECTIVE_SHARD.items() if s == args.shard))
        return
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
    elif args.kind == "selective":
        dirs = [d for d in test if SELECTIVE_SHARD.get(d) == args.shard]
    else:
        dirs = shard(test, args.shard, args.shards)
    print(" ".join("./" + d if d != "." else "." for d in dirs))


if __name__ == "__main__":
    main()
