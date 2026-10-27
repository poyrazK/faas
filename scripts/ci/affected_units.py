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
(linux/amd64, CGO off, the Makefile's DAEMON_TAGS for daemons). Test files and files a
build constraint excludes are not inputs. go.mod, go.sum and the Makefile's
release build recipe are inputs of every binary. A deleted file under a
package in the closure counts too, since the closure is loaded at head.

Each binary gets a unit version: the newest first-parent commit in
(base, head] that changed one of its inputs. A binary without one is
unchanged since base. Release builds stamp `unit-<commit>` into
pkg/wire.Version, so a unit's bytes depend only on the source it was built
from.

The import closure over-approximates what a binary runs. With --code, each
binary of a deployable lane whose closure changed is built at base and at
head with the release flags, a neutral version stamp and an empty Go build
ID (binary_code.py), and the report records its code status and digests:

  changed  its code differs from base's: a host on the base release
           restarts it.
  same     identical code; the closure change linked nothing it runs.
  lines    only the line table differs (an edit above functions it
           links): the same code, so it is treated like same, and the
           running binary keeps line numbers that match the source its
           version names.

The release still carries every closure binary of an eligible lane, the
unchanged ones too, so a host that runs an earlier unit release of it can
get back to base's code after a revert. The deploy plan
(plan_unit_deploy.py) restarts a unit only when the code digest the host
runs differs from the release's; unit versions name the source of the
binary each host runs. docs/adr/1650-per-unit-control-plane-releases.md
("Selection by code").

Non-Go inputs map to flags (scripts/ci/release_units.json):

  blocking     the release units deployctl installs at activation (unit
               files and tmpfiles rule of the control-plane bundle), other
               files cd-controlplane copies into its bundle that no
               control-plane role installs, the release tarball's own
               contents, migrations that are not lane-eligible, and deploy/
               paths of the `blocking` class (below). Any of these since
               base makes the range ineligible: the per-unit lane reuses
               them from the base release.
  data         migrations added since base when every one is lane-eligible
               (check_migration_expand_contract.classify_range: declared
               `-- migration-compat: expand`, no contract change, no
               unwaived finding;
               docs/adr/20261018084150-unit-lane-expand-migrations.md). The
               control-plane lane applies them before it restarts anything;
               the edge lane waits for that.
  stateful_restart
               blocking: the range changed a control-plane role that can
               restart PostgreSQL or pgbouncer (host_config.
               stateful_services, host_roles.restart_roles), or every
               role's globals. The lane never restarts those; only a full
               release or a maintenance window does.
  host_config  deploy/ paths and bundle files a control-plane host role
               reads (scripts/ci/host_roles.py). The control-plane lane converges
               the roles whose content hash changed before anything else
               (docs/adr/20261018133100-host-configuration-lane.md).
  pending      guest/, images/, kernel, and deploy/ paths a compute host
               role reads or of the `compute_only` class (compute_deploy).
               They ship with the next full release and do not stop
               control-plane units from deploying alone. compute_deploy
               holds the edge lane while it restarts a gateway on a compute
               host, which it does without converging that host's roles.

Every deploy/ path has exactly one class (deploy_class), decided in this
order: a release unit (host_config.release_units: blocking); a path a host
role reads (host_config for a converged group, compute_deploy for compute);
then the first pattern class of release_units.json deploy_classes that
matches it: `inert` (documentation, tests, samples no daemon reads, trees
no release installs: no flag), `compute_only` (compute playbooks:
compute_deploy) or `blocking` (deploy). A path no rule names blocks too,
and the repository test fails until the table names it. A path no host role
reads whose change since base only touches comment lines is inert whatever
its class (comment_only_paths). docs/adr/20261027151755-unit-lane-deploy-
path-classes.md.

Binaries outside both lanes (shared with compute hosts, compute-only, CLIs)
are reported as pending: the per-unit lanes leave them at base, which is the
mixed state every rolling rollout already passes through while compute hosts
trail the control plane.

The checkout must be at --head (`go list` reads the working tree).

Usage:
  scripts/ci/affected_units.py --base v0.1.18-rc.273 --head HEAD [--code [--code-cache FILE]]
                               [--output report.json]
"""
import argparse
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
DEFAULT_CONFIG = os.path.join(HERE, "release_units.json")
sys.path.insert(0, HERE)
import binary_code  # noqa: E402
import check_migration_expand_contract as migration_lint  # noqa: E402
import host_roles  # noqa: E402

LANES = ("control-plane", "edge", "shared", "compute", "tool")
# Lanes a unit release deploys, in deploy order.
DEPLOY_LANES = ("control-plane", "edge")
HOST_ROLES = ("control-plane", "compute-only")  # pkg/daemonunitspec roles
LIST_FIELDS = ",".join([
    "ImportPath", "Dir", "Imports", "GoFiles", "CgoFiles", "SFiles", "CFiles",
    "HFiles", "SysoFiles", "EmbedFiles", "Module", "Error", "DepsErrors",
])
FILE_FIELDS = ("GoFiles", "CgoFiles", "SFiles", "CFiles", "HFiles", "SysoFiles", "EmbedFiles")
RECIPE_VARS = re.compile(r"^(GO|GO_BUILD_FLAGS|DAEMON_TAGS|LDFLAGS|BUILD_CGO_ENABLED|DAEMONS|CLIS)\s*[:?+]?=")
RECIPE_TARGETS = ("build-release-batch:", "build-release-unit:")
# How many paths a flag lists in the report; the count is always complete.
FLAG_PATH_LIMIT = 20
# Host groups (pkg/daemonunitspec roles) whose roles a unit lane converges.
HOST_GROUP_ROLES = {"control_plane": "control-plane", "compute_nodes": "compute-only"}
# The pattern classes of release_units.json deploy_classes, in match order,
# and the flag each maps a changed path to.
DEPLOY_CLASSES = {"inert": None, "compute_only": "compute_deploy", "blocking": "deploy"}
# Files whose `#` lines are comments to every reader: a change confined to
# them changes nothing a host runs (comment_only_paths).
COMMENT_SUFFIXES = (".yml", ".yaml", ".ini", ".toml", ".cfg", ".conf", ".nft", ".sh")


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
    for group in (config.get("host_config") or {}).get("converged_groups") or []:
        if group not in HOST_GROUP_ROLES:
            raise ValueError(f"{path}: host_config converges unknown host group {group!r}")
    for cls, patterns in (config.get("deploy_classes") or {}).items():
        if cls.startswith("_"):
            continue
        if cls not in DEPLOY_CLASSES or not isinstance(patterns, list):
            raise ValueError(f"{path}: deploy_classes has unknown class {cls!r}")
    return config


def daemon_tags(text):
    """The build tags release builds give daemons: the Makefile's DAEMON_TAGS
    (comma-separated), else metal."""
    m = re.search(r"^DAEMON_TAGS\s*:=\s*(\S+)\s*$", text, re.M)
    return m.group(1).split(",") if m else ["metal"]


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


def binary_closures(daemons, clis, module, root=".", tags_for_daemons=("metal",)):
    out = {}
    for names, tags in ((daemons, list(tags_for_daemons)), (clis, [])):
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


_GLOBS = {}


def glob_match(path, pattern):
    """Whether a deploy_classes pattern names path: a directory (trailing
    /), an exact path, or a glob where * stays inside one path segment and
    **/ spans any number of them."""
    if pattern.endswith("/"):
        return path.startswith(pattern)
    if "*" not in pattern:
        return path == pattern
    if pattern not in _GLOBS:
        out, i = [], 0
        while i < len(pattern):
            if pattern.startswith("**/", i):
                out.append("(?:[^/]+/)*")
                i += 3
            elif pattern.startswith("**", i):
                out.append(".*")
                i += 2
            elif pattern[i] == "*":
                out.append("[^/]*")
                i += 1
            else:
                out.append(re.escape(pattern[i]))
                i += 1
        _GLOBS[pattern] = re.compile("".join(out) + r"\Z")
    return bool(_GLOBS[pattern].match(path))


def data_step(commits, config, verdict):
    """The migrations of a range and whether a unit release may apply them.
    verdict is classify_range's result, or None when the range was not
    classified (every migration then blocks)."""
    directory = config.get("migrations_dir")
    paths = sorted({p for _, ps in commits for p in ps
                    if directory and migration_lint.is_migration_file(p, directory)})
    if not paths:
        return {"paths": [], "eligible": True, "migrations": [], "blockers": [], "commit": None, "version": None}
    verdict = verdict or {"eligible": False, "migrations": [], "blockers": ["migrations were not classified"]}
    eligible = bool(verdict["eligible"])
    migrations = verdict["migrations"] if eligible else []
    # The data version moves only when a migration file does.
    commit = next((sha for sha, ps in commits if any(p in paths for p in ps)), None) if migrations else None
    return {"paths": paths, "eligible": eligible, "migrations": verdict["migrations"],
            "blockers": verdict["blockers"], "commit": commit, "version": f"data-{commit}" if commit else None}


def host_config_step(commits, config, host):
    """The control-plane host roles the range changed, and the newest commit
    that changed a path they read: {"group", "roles", "all", "commit",
    "version"}. Roles come from the content hashes at base and head when
    the host view has them, else from the owners of the changed paths."""
    out = {"group": None, "roles": [], "all": False, "commit": None, "version": None}
    groups = (config.get("host_config") or {}).get("converged_groups") or []
    group = next((g for g in groups if HOST_GROUP_ROLES[g] == "control-plane"), None)
    if not group or group not in host:
        return out
    owners, release_units = host[group]["owners"], set(config["host_config"].get("release_units") or [])
    owned = [p for _, ps in commits for p in ps if p in owners and p not in release_units]
    diff = host[group].get("changed") or changed_roles(owners, owned)
    out.update(group=group, roles=list(diff["roles"]), all=bool(diff["all"]))
    if out["roles"]:
        # The host-config version moves only when a role input does.
        commit = next((sha for sha, ps in commits if any(p in owners for p in ps)), None)
        out.update(commit=commit, version=f"host-{commit}" if commit else None)
    return out


def stateful_step(commits, host_step, config, host):
    """Changed paths of the control-plane roles that can restart a stateful
    service (PostgreSQL, pgbouncer), when the range changed one of those
    roles, or every role through the group's globals. The lane never runs
    such a change."""
    group = host_step["group"]
    if not group or not host_step["roles"]:
        return []
    restart = set((host.get(group) or {}).get("restart_roles") or [])
    hit = restart & set(host_step["roles"])
    if not hit:
        return []
    owners = host[group]["owners"]
    return sorted({p for _, ps in commits for p in ps
                   if p in owners and (hit & set(owners[p]) or "*" in owners[p])})


def changed_roles(owners, paths):
    """The roles owning any of paths, for a host view without hash diffs; a
    global path changes every role the owners name."""
    roles = {r for p in paths for r in owners.get(p, ())}
    if "*" in roles:
        roles = {r for names in owners.values() for r in names}
    return {"all": "*" in roles, "roles": sorted(roles - {"*"})}


def deploy_class(path, config, host, comment_only=frozenset()):
    """(class, flags) of one deploy/ path. Classes, first match wins:
    release_units (blocking), host_roles (host_config for a converged
    group's role, compute_deploy for a compute role), then the pattern
    classes of deploy_classes in DEPLOY_CLASSES order, and unclassified
    (blocking) for a path no rule names. A path no host role reads whose
    change is in comment_only is `comments`, with no flag."""
    hc = config.get("host_config") or {}
    if path in set(hc.get("release_units") or []):
        return "release_units", ["release_units"]
    converged = set(hc.get("converged_groups") or [])
    flags = [("host_config" if group in converged else "compute_deploy")
             for group, view in sorted(host.items()) if path in view["owners"]]
    if flags:
        return "host_roles", list(dict.fromkeys(flags))
    classes = config.get("deploy_classes") or {}
    cls = next((c for c in DEPLOY_CLASSES if any(glob_match(path, p) for p in classes.get(c) or [])),
               "unclassified")
    if cls == "inert":
        return cls, []
    if path in comment_only:
        return "comments", []
    return cls, [DEPLOY_CLASSES.get(cls) or "deploy"]


def classify_deploy(paths, config, host, comment_only=frozenset()):
    """({flag: [paths]}, {class: [paths]}) for changed deploy/ paths
    (deploy_class): release_units and deploy block both lanes,
    host_config rides the control-plane lane, compute_deploy is pending
    and holds the edge lane on compute hosts. Inert and comment-only paths
    map to no flag."""
    flags, classes = {}, {}
    for path in sorted(paths):
        cls, names = deploy_class(path, config, host, comment_only)
        classes.setdefault(cls, []).append(path)
        for flag in names:
            flags.setdefault(flag, []).append(path)
    return flags, classes


def comment_free(text):
    """Lines of text that are not blank or `#` comments; a #! line is
    content."""
    return [line.rstrip() for line in text.split("\n")
            if line.strip() and not (line.lstrip().startswith("#") and not line.lstrip().startswith("#!"))]


def comment_only_paths(base, head, paths, root="."):
    """The paths of a COMMENT_SUFFIXES kind that exist at base and head and
    differ only in blank and `#` comment lines."""
    out = set()
    for path in sorted(paths):
        if not path.endswith(COMMENT_SUFFIXES):
            continue
        try:
            old, new = git("show", f"{base}:{path}", root=root), git("show", f"{head}:{path}", root=root)
        except RuntimeError:
            continue  # added or deleted
        if comment_free(old) == comment_free(new):
            out.add(path)
    return out


def analyze(commits, closures, config, exists, recipe_shas=frozenset(), data=None, host=None, code=None,
            comment_only=frozenset()):
    """The report for a range. commits are newest first; closures map each
    binary to (package dirs, input files) at head; exists is head's file set;
    data is classify_range's verdict on the range's migrations; host maps
    each host group to {"owners": {path: [roles]}, "changed": {"all",
    "roles"}} (host_view), and without it no deploy/ path is host
    configuration; code maps measured binaries to {"code", "code_digest",
    "base_code_digest"} (measure_code); comment_only is the deploy/ paths
    whose change since base touches only comments (comment_only_paths)."""
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
        # closure: an input changed since base. changed: its code did, when
        # measured; otherwise the closure stands in for it.
        binary = {
            "lane": entry["lane"],
            "closure": version is not None,
            "changed": version is not None,
            "code": None,
            "commit": version,
            "version": f"unit-{version}" if version else None,
        }
        measured = (code or {}).get(name)
        if measured and version:
            binary.update(measured)
            binary["changed"] = measured["code"] in binary_code.CODE_CHANGED
        binaries[name] = binary
    for name in closures:
        if name not in config["binaries"]:
            raise ValueError(f"release binary {name} has no lane in the lane table")

    flags = {}
    host = host or {}
    step = data_step(commits, config, data)
    # Lane-eligible migrations are the data step, not a blocking change.
    claimed = set(step["paths"]) if step["eligible"] else set()
    changed_paths = {p for _, paths in commits for p in paths} - claimed
    deploy_prefixes = (config.get("blocking_paths") or {}).get("deploy") or []
    # A bundle file a control-plane role installs is host configuration:
    # the lane's convergence installs it, and no unit activation does.
    converged_owned = {p for g in (config.get("host_config") or {}).get("converged_groups") or []
                       for p in (host.get(g) or {}).get("owners", {})}
    bundle_prefixes = (config.get("blocking_paths") or {}).get("control_plane_bundle") or []
    for kind in ("blocking_paths", "pending_paths"):
        for flag, prefixes in sorted((config.get(kind) or {}).items()):
            if kind == "blocking_paths" and flag == "deploy":
                continue
            found = sorted(p for p in changed_paths if matches(p, prefixes)
                           and not (flag == "control_plane_bundle" and p in converged_owned))
            if found:
                flags[flag] = {"blocking": kind == "blocking_paths", "count": len(found), "paths": found[:FLAG_PATH_LIMIT]}
    # deploy/ by its class (deploy_class): a role a lane converges
    # (host_config), a compute role or compute playbook (compute_deploy,
    # which waits for the full release and holds the edge lane), the
    # release's own units, nothing at activation (inert), or anything else
    # (deploy, blocking).
    deploy_paths = {p for p in changed_paths if matches(p, deploy_prefixes)
                    or (matches(p, bundle_prefixes) and p in converged_owned)}
    found_flags, deploy_classes = classify_deploy(deploy_paths, config, host, comment_only)
    for flag, found in found_flags.items():
        flags[flag] = {"blocking": flag in ("deploy", "release_units"), "count": len(found),
                       "paths": found[:FLAG_PATH_LIMIT]}
    host_step = host_config_step(commits, config, host)
    stateful = stateful_step(commits, host_step, config, host)
    if stateful:
        flags["stateful_restart"] = {"blocking": True, "count": len(stateful), "paths": stateful[:FLAG_PATH_LIMIT]}
    if recipe_shas:
        flags["build_recipe"] = {"blocking": False, "count": len(recipe_shas), "paths": ["Makefile"]}
    if claimed:
        flags["data"] = {"blocking": False, "count": len(claimed), "paths": step["paths"][:FLAG_PATH_LIMIT]}

    blocking = sorted(f for f, v in flags.items() if v["blocking"])
    has_data = bool(step["version"])
    has_host = bool(host_step["version"])
    lanes = {}
    for lane in DEPLOY_LANES:
        changed = sorted(n for n, b in binaries.items() if b["changed"] and b["lane"] == lane)
        # A lane carries every closure binary, the ones whose code did not
        # change too: a host on an earlier unit release may run other code.
        carried = sorted(n for n, b in binaries.items() if b["closure"] and b["lane"] == lane)
        # The edge lane lands on compute hosts as they are: while it restarts
        # a gateway there, a change to a role or playbook those hosts run
        # waits for the full release that converges it. A lane that only
        # restarts control-plane gateways does not wait.
        on_compute = any(svc.get("host") == "compute-only" for n in carried
                         for svc in config["binaries"][n].get("services") or [])
        blocked = sorted(blocking + (["compute_deploy"] if lane == "edge" and on_compute and "compute_deploy" in flags
                                     else []))
        reasons = [f"{f} changed since base ({flags[f]['count']} paths, e.g. {flags[f]['paths'][0]})" for f in blocked]
        if "migrations" in blocked and step["blockers"]:
            reasons.append(f"migrations are not lane-eligible: {step['blockers'][0]}")
        # Deploy order: the control-plane lane converges its host roles and
        # applies the data step, so it deploys even when no daemon of its
        # own changed. Whatever blocks it blocks the edge lane too, which
        # therefore never goes ahead of it (cd-edge-units.yml checks the
        # control plane before any gateway).
        carries = bool(carried) or (lane == "control-plane" and (has_data or has_host))
        if not carries:
            reasons.append(f"no {lane} daemon changed since base")
        lanes[lane] = {"changed": changed, "carried": carried, "blocked_by": blocked,
                       "eligible": carries and not blocked, "reasons": reasons}
    deployable = sorted(n for lane in lanes.values() if lane["eligible"] for n in lane["carried"])
    pending = {lane: sorted(n for n, b in binaries.items() if b["changed"] and b["lane"] == lane)
               for lane in ("shared", "compute", "tool")}
    for lane, v in lanes.items():
        pending[lane] = [] if v["eligible"] else v["changed"]
    eligible = any(lane["eligible"] for lane in lanes.values())
    del step["paths"]
    return {
        "format_version": 2,
        "binaries": binaries,
        "data": step,
        "host_config": host_step,
        "deploy_classes": {cls: {"count": len(found), "paths": found[:FLAG_PATH_LIMIT]}
                           for cls, found in sorted(deploy_classes.items())},
        "flags": flags,
        "lanes": lanes,
        "deployable": deployable,
        "pending": pending,
        "blocked_by": blocking,
        "eligible": eligible,
        "reasons": [] if eligible else [r for lane in lanes.values() for r in lane["reasons"]],
        "commits": len(commits),
    }


def host_view(base_sha, head_sha, root=".", services=()):
    """{group: {"owners", "changed"}} for every host group: the paths each
    group's roles read at base or head, and the roles whose content hash
    differs between them (host_roles.py), and the roles that can restart one
    of services at base or head. Empty when head has no bootstrap
    playbook."""
    head_tree = host_roles.git_tree(head_sha, root)
    if not head_tree.is_file(host_roles.BOOTSTRAP):
        return {}
    base_tree = host_roles.git_tree(base_sha, root)
    view = {}
    for group in host_roles.GROUPS:
        roles = host_roles.Roles(head_tree)
        try:
            new = host_roles.group_hashes(head_tree, group, roles)
        except ValueError:
            continue  # no play for the group: none of its paths are host config
        owners = {p: set(r) for p, r in host_roles.group_owners(head_tree, group, roles).items()}
        try:
            old_roles = host_roles.Roles(base_tree)
            old = host_roles.group_hashes(base_tree, group, old_roles)
            for path, names in host_roles.group_owners(base_tree, group, old_roles).items():
                owners.setdefault(path, set()).update(names)
        except (KeyError, ValueError):
            old = None
        restart = set(host_roles.restart_roles(head_tree, group, services, roles))
        try:
            restart |= set(host_roles.restart_roles(base_tree, group, services))
        except (KeyError, ValueError):
            pass
        view[group] = {"owners": {p: sorted(r) for p, r in owners.items()},
                       "changed": host_roles.changed(old, new, host_roles.notify_targets(head_tree, new["order"])),
                       "restart_roles": sorted(restart)}
    return view


class BuildSide:
    """One commit's release build recipe (its Makefile), for binary_code.build."""

    def __init__(self, sha, path):
        self.sha, self.path = sha, path
        with open(os.path.join(path, "Makefile"), encoding="utf-8") as fh:
            makefile = fh.read()
        self.daemons = set(makefile_list(makefile, "DAEMONS"))
        self.binaries = self.daemons | set(makefile_list(makefile, "CLIS"))
        self.flags = makefile_list(makefile, "GO_BUILD_FLAGS")
        self.daemon_tags = daemon_tags(makefile)
        self.module = module_path(path)

    def tags(self, name):
        return self.daemon_tags if name in self.daemons else []


CODE_CACHE_FORMAT = 1


def load_code_cache(path, base_sha, go):
    """{binary: (file digest, code digest)} built at base_sha by toolchain
    go, from a cache file an earlier run wrote; {} when there is none or it
    describes other builds."""
    if not path or not os.path.exists(path):
        return {}
    try:
        with open(path, encoding="utf-8") as fh:
            cache = json.load(fh)
    except (OSError, ValueError):
        return {}
    if (cache.get("format_version"), cache.get("base"), cache.get("neutral_version"), cache.get("go")) != \
            (CODE_CACHE_FORMAT, base_sha, binary_code.NEUTRAL_VERSION, go):
        return {}
    return {name: tuple(pair) for name, pair in (cache.get("digests") or {}).items()}


def write_code_cache(path, base_sha, go, digests):
    with open(path, "w", encoding="utf-8") as fh:
        json.dump({"format_version": CODE_CACHE_FORMAT, "base": base_sha,
                   "neutral_version": binary_code.NEUTRAL_VERSION, "go": go,
                   "digests": {name: list(pair) for name, pair in sorted(digests.items())}},
                  fh, indent=2, sort_keys=True)
        fh.write("\n")


def measure_code(names, base_sha, head_sha, go, root=".", cache_path=None, workdir=None):
    """({binary: {"code", "code_digest", "base_code_digest"}}, binaries
    whose base digests came from the cache, binaries built at base) for
    names, built at base (an exported tree) and at head (the checkout) with
    binary_code.build and toolchain go. The base builds are the same for
    every range over one base, so a cache file keeps their digests between
    runs."""
    base_hashes = load_code_cache(cache_path, base_sha, go)
    cached = sorted(n for n in names if n in base_hashes)
    buildable = []
    tmp = tempfile.mkdtemp(prefix="unit-code-", dir=workdir)
    try:
        out = os.path.join(tmp, "out")
        os.makedirs(out)
        missing = [n for n in names if n not in base_hashes]
        if missing:
            binary_code.prefetch_tree(base_sha, head_sha, root)
            binary_code.export_tree(base_sha, os.path.join(tmp, "base"), root)
            base = BuildSide(base_sha, os.path.join(tmp, "base"))
            # A binary base does not build is new: no base digest, "added".
            buildable = [n for n in missing if n in base.binaries and os.path.isdir(os.path.join(base.path, "cmd", n))]
            if buildable:
                base_hashes.update(binary_code.build(base, buildable, out))
        head_hashes = binary_code.build(BuildSide(head_sha, root), names, out)
    finally:
        shutil.rmtree(tmp, ignore_errors=True)
    if cache_path:
        write_code_cache(cache_path, base_sha, go, base_hashes)
    status = binary_code.compare(base_hashes, head_hashes, names)
    measured = {}
    for n in names:
        measured[n] = {"code": status[n], "code_digest": head_hashes[n][1],
                       "base_code_digest": base_hashes[n][1] if n in base_hashes else None}
    return measured, cached, sorted(buildable)


def code_candidates(rep):
    """Binaries worth building at both commits: those of a deployable lane,
    whose closure changed, in a lane nothing blocks. A blocked lane ships
    nothing, and the full release ships every other binary."""
    return sorted(n for n, b in rep["binaries"].items()
                  if b["closure"] and b["lane"] in DEPLOY_LANES and not rep["lanes"][b["lane"]]["blocked_by"])


def report(base, head, config_path=DEFAULT_CONFIG, root=".", code=False, code_cache=None, workdir=None):
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
    closures = binary_closures(daemons, clis, module_path(root), root, daemon_tags(makefile))
    commits = range_commits(base_sha, head_sha, root)
    exists = set(git("ls-tree", "-r", "--name-only", head_sha, root=root).splitlines())
    directory = config.get("migrations_dir")
    data = None
    if directory and any(migration_lint.is_migration_file(p, directory) for _, paths in commits for p in paths):
        data = migration_lint.classify_range(root, base_sha, head_sha, directory)
    hc = config.get("host_config")
    host = host_view(base_sha, head_sha, root, hc.get("stateful_services") or ()) if hc else {}
    recipes = recipe_commits(commits, root)
    deploy_prefixes = (config.get("blocking_paths") or {}).get("deploy") or []
    owned = {p for view in host.values() for p in view["owners"]}
    comments = comment_only_paths(base_sha, head_sha, {p for _, paths in commits for p in paths
                                                       if matches(p, deploy_prefixes) and p not in owned}, root)
    out = analyze(commits, closures, config, exists, recipes, data, host, comment_only=comments)
    selection = None
    if code:
        names = code_candidates(out)
        selection = {"neutral_version": binary_code.NEUTRAL_VERSION, "go": None, "measured": names,
                     "base_cached": [], "base_built": [], "error": None}
        try:
            selection["go"] = binary_code.go_version(root)
            if names:
                measured, selection["base_cached"], selection["base_built"] = measure_code(
                    names, base_sha, head_sha, selection["go"], root, code_cache, workdir)
                out = analyze(commits, closures, config, exists, recipes, data, host, measured, comments)
        except (RuntimeError, subprocess.CalledProcessError) as err:
            # Selecting by closure restarts more units, never fewer: the
            # release carries no digests and hosts compare unit versions.
            detail = getattr(err, "stderr", "") or ""
            selection.update(measured=[], base_cached=[], base_built=[], error=f"{err} {detail}".strip())
            print(f"warning: code selection failed, selecting by closure: {selection['error']}", file=sys.stderr)
    out.update(base=base_sha, head=head_sha, base_ref=base, code_selection=selection)
    return out


def summary(rep):
    lines = [f"range {rep['base'][:12]}..{rep['head'][:12]}: {rep['commits']} commits"]
    for name, b in sorted(rep["binaries"].items()):
        if b["closure"]:
            code = f" code {b['code']}" if b.get("code") else ""
            lines.append(f"  {name:20} {b['lane']:14} {b['commit'][:12]}{code}")
    for flag, v in sorted(rep["flags"].items()):
        if flag in ("data", "host_config"):
            continue
        kind = "blocking" if v["blocking"] else "pending"
        lines.append(f"  flag {flag} ({kind}): {v['count']} paths, e.g. {v['paths'][0]}")
    data = rep.get("data") or {}
    if data.get("migrations"):
        state = f"applied first, {data['version']}" if data["eligible"] else "blocking: " + "; ".join(data["blockers"][:3])
        lines.append(f"  data: {len(data['migrations'])} migrations ({state})")
    classes = rep.get("deploy_classes") or {}
    if classes:
        lines.append("  deploy/ classes: " + ", ".join(f"{cls} {v['count']}" for cls, v in sorted(classes.items())))
    hc = rep.get("host_config") or {}
    if hc.get("roles"):
        lines.append(f"  host config: {'every role' if hc['all'] else ' '.join(hc['roles'])} "
                     f"(converged first, {hc['version']})")
    for lane, v in rep["lanes"].items():
        state = "eligible" if v["eligible"] else "not eligible: " + "; ".join(v["reasons"])
        same = sorted(set(v["carried"]) - set(v["changed"]))
        unchanged = f"; carried with unchanged code: {' '.join(same)}" if same else ""
        lines.append(f"{lane} lane: {' '.join(v['changed']) or '<none>'} ({state}{unchanged})")
    lines.append(f"deployable: {' '.join(rep['deployable']) or '<none>'}")
    lines.append(f"eligible: {str(rep['eligible']).lower()}" +
                 ("" if rep["eligible"] else f" ({'; '.join(rep['reasons'])})"))
    return "\n".join(lines)


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--base", required=True, help="the full release the host runs (tag or commit)")
    ap.add_argument("--head", default="HEAD", help="the commit to release (default HEAD; must be checked out)")
    ap.add_argument("--config", default=DEFAULT_CONFIG, help="lane table (default scripts/ci/release_units.json)")
    ap.add_argument("--code", action="store_true",
                    help="build each deployable closure binary at base and head and select by code")
    ap.add_argument("--code-cache", help="JSON file of base code digests, read and updated (with --code)")
    ap.add_argument("--workdir", help="parent directory for the exported base tree and binaries (with --code)")
    ap.add_argument("--output", help="write the JSON report here instead of stdout")
    args = ap.parse_args()
    try:
        rep = report(args.base, args.head, args.config, code=args.code, code_cache=args.code_cache,
                     workdir=args.workdir)
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
