#!/usr/bin/env python3
"""Map a commit range to the release binaries it changed, for per-unit releases.

A per-unit release (docs/adr/1650-per-unit-control-plane-releases.md)
ships only the daemons whose code changed since the full release the hosts
run, on top of that release, through two lanes: control-plane daemons, then
the edge (the gateways, one host at a time). This script decides which
ones, and whether anything outside Go code changed that only a full release
can carry.

A binary's inputs are the files `go build` reads for it: the Go, cgo,
assembly and embedded files of every module package in its import closure,
loaded with `go list -deps` under the release build configuration
(linux/amd64, CGO off, `-tags metal` for daemons). Test files and files a
build constraint excludes are not inputs. go.mod, go.sum and the Makefile's
release build recipe are inputs of every binary. A deleted file under a
package in the closure counts too, since the closure is loaded at head.

Each binary gets a unit version: the newest first-parent commit in
(base, head] that changed one of its inputs. A binary without one is
unchanged since base. Release builds stamp `unit-<commit>` into
pkg/wire.Version, so a unit's bytes depend only on the source it was built
from, and a deploy can tell whether the host already runs that version.

Non-Go inputs map to flags (scripts/ci/release_units.json):

  blocking  migrations, deploy/ (units, roles, rules), the files
            cd-controlplane copies into its bundle, and the release
            tarball's own contents. Any of these since base makes the range
            ineligible: the per-unit lane reuses them from the base release.
  pending   guest/, images/, kernel, and deploy/ roles only compute hosts
            run (compute_only_paths). They ship with the next full release
            and do not stop control-plane units from deploying alone. A
            compute-only role does block the edge lane, which deploys onto
            compute hosts without converging their roles.

Binaries outside both lanes (shared with compute hosts, compute-only, CLIs)
are reported as pending: the per-unit lanes leave them at base, which is the
mixed state every rolling rollout already passes through while compute hosts
trail the control plane.

The checkout must be at --head (`go list` reads the working tree).

Usage:
  scripts/ci/affected_units.py --base v0.1.18-rc.273 --head HEAD [--output report.json]
"""
import argparse
import json
import os
import re
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
DEFAULT_CONFIG = os.path.join(HERE, "release_units.json")

LANES = ("control-plane", "edge", "shared", "compute", "tool")
# Lanes a unit release deploys, in deploy order.
DEPLOY_LANES = ("control-plane", "edge")
HOST_ROLES = ("control-plane", "compute-only")  # pkg/daemonunitspec roles
LIST_FIELDS = ",".join([
    "ImportPath", "Dir", "Imports", "GoFiles", "CgoFiles", "SFiles", "CFiles",
    "HFiles", "SysoFiles", "EmbedFiles", "Module", "Error", "DepsErrors",
])
FILE_FIELDS = ("GoFiles", "CgoFiles", "SFiles", "CFiles", "HFiles", "SysoFiles", "EmbedFiles")
RECIPE_VARS = re.compile(r"^(GO|GO_BUILD_FLAGS|LDFLAGS|BUILD_CGO_ENABLED|DAEMONS|CLIS)\s*[:?+]?=")
RECIPE_TARGETS = ("build-release-batch:", "build-release-unit:")
# How many paths a flag lists in the report; the count is always complete.
FLAG_PATH_LIMIT = 20


def git(*args, root="."):
    out = subprocess.run(["git", *args], capture_output=True, text=True, cwd=root)
    if out.returncode != 0:
        raise RuntimeError(f"git {' '.join(args)}: {out.stderr.strip()}")
    return out.stdout


def load_config(path=DEFAULT_CONFIG):
    with open(path, encoding="utf-8") as fh:
        config = json.load(fh)
    for name, entry in config["binaries"].items():
        if entry.get("lane") not in LANES:
            raise ValueError(f"{path}: binary {name} has unknown lane {entry.get('lane')!r}")
        if entry.get("services") and entry["lane"] not in DEPLOY_LANES:
            raise ValueError(f"{path}: only deployable lanes list services ({name})")
        for service in entry.get("services") or []:
            if service.get("host", "control-plane") not in HOST_ROLES:
                raise ValueError(f"{path}: {name} service {service.get('name')} has unknown host {service['host']!r}")
    return config


def makefile_list(text, var):
    """Words of a single-line `VAR := a b c` assignment."""
    m = re.search(rf"^{var}\s*:=\s*(.*)$", text, re.M)
    if not m:
        raise ValueError(f"Makefile has no {var} := assignment")
    return m.group(1).split()


def build_recipe(text):
    """The Makefile lines that decide how release binaries are built: the
    toolchain and flag variables, the binary lists and the release build
    targets' recipes. Everything else in the Makefile (test, lint and ops
    targets) cannot change a release binary."""
    lines, inside = [], False
    for line in text.split("\n"):
        if inside:
            if line.startswith("\t") or line.startswith("  "):
                lines.append(line)
                continue
            inside = False
        if line.startswith(RECIPE_TARGETS):
            inside = True
            lines.append(line)
        elif RECIPE_VARS.match(line):
            lines.append(line)
    return "\n".join(lines)


def module_path(root="."):
    with open(os.path.join(root, "go.mod"), encoding="utf-8") as fh:
        for line in fh:
            if line.startswith("module "):
                return line.split()[1]
    raise ValueError("go.mod has no module line")


def decode_stream(out):
    decoder = json.JSONDecoder()
    i = 0
    while i < len(out):
        while i < len(out) and out[i].isspace():
            i += 1
        if i >= len(out):
            break
        obj, i = decoder.raw_decode(out, i)
        yield obj


def go_graph(mains, tags, root="."):
    """Module packages reachable from the main packages under the release
    build configuration: {import path: {"dir", "imports", "files"}}."""
    env = dict(os.environ, GOOS="linux", GOARCH="amd64", CGO_ENABLED="0")
    cmd = [os.environ.get("GO", "go"), "list", "-e", "-deps", f"-json={LIST_FIELDS}"]
    if tags:
        cmd.append("-tags=" + ",".join(tags))
    out = subprocess.run(cmd + mains, check=True, capture_output=True, text=True, cwd=root, env=env).stdout
    top = os.path.realpath(root)
    graph = {}
    for obj in decode_stream(out):
        module = obj.get("Module") or {}
        if not module.get("Main"):
            continue
        if obj.get("Error") or obj.get("DepsErrors"):
            err = obj.get("Error") or obj["DepsErrors"][0]
            raise RuntimeError(f"go list {obj['ImportPath']}: {err.get('Err', err)}")
        rel = os.path.relpath(os.path.realpath(obj["Dir"]), top)
        rel = "" if rel == "." else rel
        files = {os.path.join(rel, f) for field in FILE_FIELDS for f in obj.get(field) or []}
        graph[obj["ImportPath"]] = {"dir": rel, "imports": set(obj.get("Imports") or []), "files": files}
    return graph


def closure(main, graph):
    """(package dirs, input files) of everything main imports, itself included."""
    dirs, files, seen, stack = set(), set(), {main}, [main]
    while stack:
        pkg = graph[stack.pop()]
        dirs.add(pkg["dir"])
        files |= pkg["files"]
        for imp in pkg["imports"]:
            if imp in graph and imp not in seen:
                seen.add(imp)
                stack.append(imp)
    return dirs, files


def binary_closures(daemons, clis, module, root="."):
    out = {}
    for names, tags in ((daemons, ["metal"]), (clis, [])):
        if not names:
            continue
        mains = [f"./cmd/{n}" for n in names]
        graph = go_graph(mains, tags, root)
        for n in names:
            path = f"{module}/cmd/{n}"
            if path not in graph:
                raise RuntimeError(f"go list did not load {path}")
            out[n] = closure(path, graph)
    return out


def range_commits(base, head, root="."):
    """[(sha, [paths])] for first-parent commits in (base, head], newest first.
    Merge commits are diffed against their first parent."""
    out = git("log", "--first-parent", "-m", "--no-renames", "--name-only",
              "--format=%x00%H", f"{base}..{head}", root=root)
    commits = []
    for chunk in out.split("\0")[1:]:
        lines = [line for line in chunk.split("\n") if line.strip()]
        if lines:
            commits.append((lines[0].strip(), lines[1:]))
    return commits


def recipe_commits(commits, root="."):
    """Commits whose Makefile change altered the release build recipe."""
    changed = set()
    for sha, paths in commits:
        if "Makefile" not in paths:
            continue
        try:
            before = git("show", f"{sha}^:Makefile", root=root)
        except RuntimeError:
            before = ""
        if build_recipe(before) != build_recipe(git("show", f"{sha}:Makefile", root=root)):
            changed.add(sha)
    return changed


def hits(path, dirs, files, exists):
    """Whether a changed path is an input of a binary with this closure."""
    if path in files:
        return True
    if path in exists or path.endswith("_test.go"):
        return False
    # A deleted file under a package the binary builds: conservatively an
    # input, since head no longer lists what it was.
    d = os.path.dirname(path)
    while True:
        if d in dirs:
            return True
        if not d:
            return False
        d = os.path.dirname(d)


def matches(path, prefixes):
    """Whether a flag's path prefixes cover path. Test files never ship, so
    a migration test or a role test does not need a full release."""
    if path.endswith("_test.go"):
        return False
    return any(path == p or (p.endswith("/") and path.startswith(p)) for p in prefixes)


def analyze(commits, closures, config, exists, recipe_shas=frozenset()):
    """The report for a range. commits are newest first; closures map each
    binary to (package dirs, input files) at head; exists is head's file set."""
    global_inputs = set(config.get("global_inputs") or [])
    binaries = {}
    for name, entry in sorted(config["binaries"].items()):
        if name not in closures:
            raise ValueError(f"binary {name} is in the lane table but was not loaded")
        dirs, files = closures[name]
        version = None
        for sha, paths in commits:
            if sha in recipe_shas or any(p in global_inputs or hits(p, dirs, files, exists) for p in paths):
                version = sha
                break
        binaries[name] = {
            "lane": entry["lane"],
            "changed": version is not None,
            "commit": version,
            "version": f"unit-{version}" if version else None,
        }
    for name in closures:
        if name not in config["binaries"]:
            raise ValueError(f"release binary {name} has no lane in the lane table")

    flags = {}
    compute_only = config.get("compute_only_paths") or []
    changed_paths = {p for _, paths in commits for p in paths}
    # A deploy/ role that only compute hosts run cannot change the control
    # plane: it waits for the full release instead of blocking the lane.
    groups = [("compute_deploy", False, compute_only)]
    for kind in ("blocking_paths", "pending_paths"):
        for flag, prefixes in sorted((config.get(kind) or {}).items()):
            groups.append((flag, kind == "blocking_paths", prefixes))
    for flag, blocking, prefixes in groups:
        found = sorted(p for p in changed_paths if matches(p, prefixes)
                       and (flag == "compute_deploy" or not matches(p, compute_only)))
        if found:
            flags[flag] = {"blocking": blocking, "count": len(found), "paths": found[:FLAG_PATH_LIMIT]}
    if recipe_shas:
        flags["build_recipe"] = {"blocking": False, "count": len(recipe_shas), "paths": ["Makefile"]}

    blocking = sorted(f for f, v in flags.items() if v["blocking"])
    lanes = {}
    for lane in DEPLOY_LANES:
        changed = sorted(n for n, b in binaries.items() if b["changed"] and b["lane"] == lane)
        # The edge lane lands on compute hosts as they are: a change to a
        # role those hosts run waits for the full release that converges it.
        blocked = sorted(blocking + (["compute_deploy"] if lane == "edge" and "compute_deploy" in flags else []))
        reasons = [f"{f} changed since base ({flags[f]['count']} paths, e.g. {flags[f]['paths'][0]})" for f in blocked]
        if not changed:
            reasons.append(f"no {lane} daemon changed since base")
        lanes[lane] = {"changed": changed, "blocked_by": blocked, "eligible": bool(changed) and not blocked,
                       "reasons": reasons}
    deployable = sorted(n for lane in lanes.values() if lane["eligible"] for n in lane["changed"])
    pending = {lane: sorted(n for n, b in binaries.items() if b["changed"] and b["lane"] == lane)
               for lane in ("shared", "compute", "tool")}
    for lane, v in lanes.items():
        pending[lane] = [] if v["eligible"] else v["changed"]
    eligible = any(lane["eligible"] for lane in lanes.values())
    return {
        "format_version": 2,
        "binaries": binaries,
        "flags": flags,
        "lanes": lanes,
        "deployable": deployable,
        "pending": pending,
        "blocked_by": blocking,
        "eligible": eligible,
        "reasons": [] if eligible else [r for lane in lanes.values() for r in lane["reasons"]],
        "commits": len(commits),
    }


def report(base, head, config_path=DEFAULT_CONFIG, root="."):
    config = load_config(config_path)
    base_sha = git("rev-parse", "--verify", f"{base}^{{commit}}", root=root).strip()
    head_sha = git("rev-parse", "--verify", f"{head}^{{commit}}", root=root).strip()
    checked_out = git("rev-parse", "HEAD", root=root).strip()
    if checked_out != head_sha:
        raise RuntimeError(f"checkout is at {checked_out}, not --head {head_sha}: go list reads the working tree")
    if git("status", "--porcelain", "--untracked-files=no", root=root).strip():
        print("warning: the working tree has uncommitted changes; go list sees them", file=sys.stderr)
    try:
        git("merge-base", "--is-ancestor", base_sha, head_sha, root=root)
    except RuntimeError:
        raise RuntimeError(f"--base {base_sha} is not an ancestor of --head {head_sha}") from None

    with open(os.path.join(root, "Makefile"), encoding="utf-8") as fh:
        makefile = fh.read()
    daemons, clis = makefile_list(makefile, "DAEMONS"), makefile_list(makefile, "CLIS")
    closures = binary_closures(daemons, clis, module_path(root), root)
    commits = range_commits(base_sha, head_sha, root)
    exists = set(git("ls-tree", "-r", "--name-only", head_sha, root=root).splitlines())
    out = analyze(commits, closures, config, exists, recipe_commits(commits, root))
    out.update(base=base_sha, head=head_sha, base_ref=base)
    return out


def summary(rep):
    lines = [f"range {rep['base'][:12]}..{rep['head'][:12]}: {rep['commits']} commits"]
    for name, b in sorted(rep["binaries"].items()):
        if b["changed"]:
            lines.append(f"  {name:20} {b['lane']:14} {b['commit'][:12]}")
    for flag, v in sorted(rep["flags"].items()):
        kind = "blocking" if v["blocking"] else "pending"
        lines.append(f"  flag {flag} ({kind}): {v['count']} paths, e.g. {v['paths'][0]}")
    for lane, v in rep["lanes"].items():
        state = "eligible" if v["eligible"] else "not eligible: " + "; ".join(v["reasons"])
        lines.append(f"{lane} lane: {' '.join(v['changed']) or '<none>'} ({state})")
    lines.append(f"deployable: {' '.join(rep['deployable']) or '<none>'}")
    lines.append(f"eligible: {str(rep['eligible']).lower()}" +
                 ("" if rep["eligible"] else f" ({'; '.join(rep['reasons'])})"))
    return "\n".join(lines)


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--base", required=True, help="the full release the host runs (tag or commit)")
    ap.add_argument("--head", default="HEAD", help="the commit to release (default HEAD; must be checked out)")
    ap.add_argument("--config", default=DEFAULT_CONFIG, help="lane table (default scripts/ci/release_units.json)")
    ap.add_argument("--output", help="write the JSON report here instead of stdout")
    args = ap.parse_args()
    try:
        rep = report(args.base, args.head, args.config)
    except (RuntimeError, ValueError, subprocess.CalledProcessError) as err:
        detail = getattr(err, "stderr", "") or ""
        print(f"affected_units: {err} {detail}".strip(), file=sys.stderr)
        return 1
    body = json.dumps(rep, indent=2, sort_keys=True) + "\n"
    if args.output:
        with open(args.output, "w", encoding="utf-8") as fh:
            fh.write(body)
    else:
        sys.stdout.write(body)
    print(summary(rep), file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
