#!/usr/bin/env python3
"""Report which release binaries a change rebuilds: by import closure and by code.

A per-unit release (docs/adr/1650-per-unit-control-plane-releases.md) ships
a binary when a file in its import closure changed (affected_units.py).
That over-approximates: the linker keeps only the code a binary reaches, so
a fix to one scheduler function can leave most of pkg/sched's importers
byte-identical, or not, depending on what each one links. This script
measures both for a change, so a pull request can see how many services its
rollout restarts and why:

  closure  the binary's import closure contains a changed file (or a global
           input or the release build recipe changed): the per-unit release
           ships it.
  code     the binary built at head differs from the one built at base,
           with the release build flags (linux/amd64, CGO off, -trimpath,
           -buildvcs=false, -tags metal for daemons, -s -w), one fixed
           version stamp and an empty Go build ID. The build ID hashes every
           package the binary links, so a release binary's bytes change
           whenever its closure does; blanking it measures the code. A
           binary whose only differing ELF section is Go's line table
           (.gopclntab) is "lines": it runs the same code, and only stack
           trace positions moved (an edit above the functions it links).

Only binaries in the closure set are built: a binary outside it has the
same inputs at both commits, and release builds are reproducible, so its
code cannot change. --all builds every binary anyway, to check that.

The range is the merge base of --base and --head to --head, the change a
pull request introduces. Both commits are exported with `git archive`, so
the working tree, its uncommitted changes and its checkout do not matter.

Usage:
  scripts/ci/blast_radius.py --base origin/main [--head HEAD] [--closure-only]
                             [--output report.json] [--markdown report.md]
"""
import argparse
import hashlib
import json
import os
import shutil
import struct
import subprocess
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import affected_units as au  # noqa: E402

VERSION_STAMP = "blast-radius"
VERSION_VAR = "pkg/wire.Version"
# The sticky pull-request comment starts with this line.
MARKER = "<!-- blast-radius -->"
# How many changed paths a binary's row names.
REASON_LIMIT = 3
# The ELF section holding Go's line, file and function-name tables. A
# binary whose other sections are identical runs the same code; only its
# stack traces (and slog source positions) differ.
LINE_TABLE = ".gopclntab"


def export_tree(sha, dest, root="."):
    """Write the files of commit sha into dest."""
    os.makedirs(dest, exist_ok=True)
    archive = subprocess.Popen(["git", "archive", "--format=tar", sha], cwd=root, stdout=subprocess.PIPE)
    untar = subprocess.run(["tar", "-x", "-f", "-", "-C", dest], stdin=archive.stdout, capture_output=True)
    archive.stdout.close()
    if archive.wait() != 0 or untar.returncode != 0:
        raise RuntimeError(f"export {sha}: {untar.stderr.decode().strip()}")


class Tree:
    """One side of the range: its release binaries, build flags and closures."""

    def __init__(self, sha, path, root=".", extra_tags=()):
        self.sha, self.path, self.extra_tags = sha, path, list(extra_tags)
        with open(os.path.join(path, "Makefile"), encoding="utf-8") as fh:
            makefile = fh.read()
        self.recipe = au.build_recipe(makefile)
        self.daemons = au.makefile_list(makefile, "DAEMONS")
        self.clis = au.makefile_list(makefile, "CLIS")
        self.flags = au.makefile_list(makefile, "GO_BUILD_FLAGS")
        self.module = au.module_path(path)
        self.files = set(au.git("ls-tree", "-r", "--name-only", sha, root=root).splitlines())
        self.closures = {}

    def load_closures(self):
        self.closures = {}
        for names, tags in ((self.daemons, self.tags(self.daemons[0]) if self.daemons else []),
                            (self.clis, self.extra_tags)):
            if not names:
                continue
            graph = au.go_graph([f"./cmd/{n}" for n in names], tags, self.path)
            for n in names:
                self.closures[n] = au.closure(f"{self.module}/cmd/{n}", graph)

    def tags(self, name):
        return (["metal"] if name in self.daemons else []) + self.extra_tags


def candidates(paths, base, head, global_inputs):
    """{binary: [changed paths that put it in the closure set]} for every
    release binary at base or head. A binary is in the set when a changed
    path is an input at either commit (a file the change removed from its
    closure counts too), when a global input or the build recipe changed,
    or when it exists on one side only."""
    # Every changed path exists at base or head (they come from the diff),
    # so a hit is exact: an added file counts where head builds it, a
    # deleted one where base built it, and a file a build tag excludes at
    # both commits never counts.
    exists = base.files | head.files
    globals_hit = sorted(p for p in paths if p in global_inputs)
    if base.recipe != head.recipe:
        globals_hit.append("Makefile (release build recipe)")
    out = {}
    for name in sorted(set(base.closures) | set(head.closures)):
        if name not in base.closures or name not in head.closures:
            out[name] = ["added" if name in head.closures else "removed"]
            continue
        hit = list(globals_hit)
        for side in (base, head):
            dirs, files = side.closures[name]
            hit += [p for p in paths if p not in hit and au.hits(p, dirs, files, exists)]
        if hit:
            out[name] = sorted(hit)
    return out


def elf_digests(path):
    """(whole file, every section but the line table) sha256 digests of a
    64-bit little-endian ELF binary."""
    with open(path, "rb") as fh:
        data = fh.read()
    if data[:4] != b"\x7fELF" or data[4] != 2 or data[5] != 1:
        raise RuntimeError(f"{path} is not a 64-bit little-endian ELF binary")
    shoff, = struct.unpack_from("<Q", data, 0x28)
    shentsize, shnum, shstrndx = struct.unpack_from("<HHH", data, 0x3A)
    headers = [struct.unpack_from("<IIQQQQ", data, shoff + i * shentsize) for i in range(shnum)]
    names = data[headers[shstrndx][4]:headers[shstrndx][4] + headers[shstrndx][5]]
    code = hashlib.sha256()
    for name_off, kind, _, addr, offset, size in headers:
        name = names[name_off:names.index(b"\0", name_off)]
        if name.decode() == LINE_TABLE:
            continue
        body = b"" if kind == 8 else data[offset:offset + size]  # SHT_NOBITS has no bytes
        code.update(name + struct.pack("<QQ", addr, size) + hashlib.sha256(body).digest())
    return hashlib.sha256(data).hexdigest(), code.hexdigest()


def build(tree, names, out):
    """{binary: (file digest, code digest)} of names built from tree with the
    release flags (elf_digests)."""
    hashes = {}
    env = dict(os.environ, GOOS="linux", GOARCH="amd64", CGO_ENABLED="0")
    ldflags = f"-X {tree.module}/{VERSION_VAR}={VERSION_STAMP} -s -w -buildid="
    for tags in sorted({tuple(tree.tags(n)) for n in names}):
        group = sorted(n for n in names if tuple(tree.tags(n)) == tags)
        if not group:
            continue
        cmd = [os.environ.get("GO", "go"), "build", *tree.flags]
        if tags:
            cmd.append("-tags=" + ",".join(tags))
        cmd += ["-ldflags", ldflags, "-o", out + os.sep] + [f"./cmd/{n}" for n in group]
        run = subprocess.run(cmd, cwd=tree.path, env=env, capture_output=True, text=True)
        if run.returncode != 0:
            raise RuntimeError(f"go build at {tree.sha[:12]}: {run.stderr.strip()}")
        for n in group:
            hashes[n] = elf_digests(os.path.join(out, n))
            os.remove(os.path.join(out, n))
    return hashes


def compare(base_hashes, head_hashes, names):
    """{binary: "changed" | "lines" | "same" | "added" | "removed"} for
    names. "lines": only the line table differs (build's digests)."""
    out = {}
    for n in names:
        if n not in base_hashes:
            out[n] = "added"
        elif n not in head_hashes:
            out[n] = "removed"
        elif base_hashes[n] == head_hashes[n]:
            out[n] = "same"
        else:
            out[n] = "lines" if base_hashes[n][1] == head_hashes[n][1] else "changed"
    return out


def assemble(base, head, merge_base, paths, closure, code, lanes):
    """The JSON report. code is None when nothing was built."""
    binaries = {}
    for name in sorted(set(base.closures) | set(head.closures)):
        binaries[name] = {
            "lane": (lanes.get(name) or {}).get("lane", "unlisted"),
            "closure": name in closure,
            "reasons": closure.get(name, [])[:REASON_LIMIT],
            "reason_count": len(closure.get(name, [])),
            "code": None if code is None else code.get(name, "same"),
        }
    return {
        "format_version": 1,
        "base": base.sha,
        "head": head.sha,
        "merge_base": merge_base,
        "changed_paths": len(paths),
        "binaries": binaries,
        "closure": sorted(closure),
        "code": None if code is None else sorted(n for n, v in code.items() if v not in ("same", "lines")),
        "lines": None if code is None else sorted(n for n, v in code.items() if v == "lines"),
    }


def report(base_ref, head_ref, root=".", config_path=au.DEFAULT_CONFIG, measure=True, every=False, workdir=None,
           extra_tags=()):
    head_sha = au.git("rev-parse", "--verify", f"{head_ref}^{{commit}}", root=root).strip()
    base_sha = au.git("rev-parse", "--verify", f"{base_ref}^{{commit}}", root=root).strip()
    merge_base = au.git("merge-base", base_sha, head_sha, root=root).strip()
    paths = [p for p in au.git("diff", "--no-renames", "--name-only", merge_base, head_sha, root=root).splitlines() if p]
    config = au.load_config(config_path)
    lanes, global_inputs = config["binaries"], set(config.get("global_inputs") or [])
    tmp = tempfile.mkdtemp(prefix="blast-radius-", dir=workdir)
    try:
        sides = []
        for sha, name in ((merge_base, "base"), (head_sha, "head")):
            export_tree(sha, os.path.join(tmp, name), root)
            tree = Tree(sha, os.path.join(tmp, name), root, extra_tags)
            tree.load_closures()
            sides.append(tree)
        base, head = sides
        closure = candidates(paths, base, head, global_inputs)
        code = None
        if measure:
            names = sorted(set(base.closures) | set(head.closures)) if every else sorted(closure)
            os.makedirs(os.path.join(tmp, "out"))
            base_hashes = build(base, [n for n in names if n in base.closures], os.path.join(tmp, "out"))
            head_hashes = build(head, [n for n in names if n in head.closures], os.path.join(tmp, "out"))
            code = compare(base_hashes, head_hashes, names)
        return assemble(base, head, merge_base, paths, closure, code, lanes)
    finally:
        shutil.rmtree(tmp, ignore_errors=True)


def markdown(rep):
    """The pull-request comment / job summary for a report."""
    total = len(rep["binaries"])
    lines = [MARKER, "## Release binaries this change rebuilds", ""]
    span = f"`{rep['merge_base'][:12]}..{rep['head'][:12]}`, {rep['changed_paths']} changed paths"
    if not rep["closure"]:
        lines += [f"{span}: no release binary's inputs changed, so no service restarts."]
        return "\n".join(lines) + "\n"
    shipped = f"the per-unit release ships **{len(rep['closure'])} of {total}** (import closure)"
    if rep["code"] is None:
        lines += [f"{span}: {shipped}; code was not measured.", ""]
    else:
        only_lines = f" ({len(rep['lines'])} more differ only in line tables)" if rep["lines"] else ""
        lines += [f"{span}: the code of **{len(rep['code'])} of {total}** release binaries changes{only_lines}; "
                  f"{shipped}.", ""]
    lines += ["| binary | lane | code | first changed inputs |", "| --- | --- | --- | --- |"]
    order = {"added": 0, "removed": 0, "changed": 0, "lines": 1, "same": 2, None: 3}
    for name, b in sorted(rep["binaries"].items(), key=lambda kv: (order[kv[1]["code"]], kv[0])):
        if not b["closure"]:
            continue
        more = f" (+{b['reason_count'] - len(b['reasons'])} more)" if b["reason_count"] > len(b["reasons"]) else ""
        reasons = ", ".join(f"`{r}`" for r in b["reasons"]) + more
        lines.append(f"| {name} | {b['lane']} | {b['code'] or 'not built'} | {reasons} |")
    lines += ["", "Code is the binary's bytes built with the release flags, one version stamp and an empty Go "
              "build ID (the build ID hashes every linked package, so it changes with the closure). `lines`: "
              f"only `{LINE_TABLE}` differs, so the binary runs the same code and only stack-trace line "
              "numbers move. Binaries outside the table have the same inputs at both commits. A binary whose "
              "closure changed but whose code did not still ships with a per-unit release today, because the "
              "release selects by closure (`scripts/ci/affected_units.py`)."]
    return "\n".join(lines) + "\n"


def summary(rep):
    """One line per closure binary, for a terminal."""
    lines = []
    for name, b in sorted(rep["binaries"].items()):
        if b["closure"]:
            lines.append(f"  {name:20} {b['lane']:14} {b['code'] or '-':8} {', '.join(b['reasons'])}")
    measured = "" if rep["code"] is None else f", code changed in {len(rep['code'])}, lines only in {len(rep['lines'])}"
    lines.append(f"closure: {len(rep['closure'])} of {len(rep['binaries'])}{measured}")
    return "\n".join(lines)


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--base", required=True, help="the branch the change merges into (its merge base with --head is used)")
    ap.add_argument("--head", default="HEAD", help="the change's commit (default HEAD)")
    ap.add_argument("--config", default=au.DEFAULT_CONFIG, help="lane table (default scripts/ci/release_units.json)")
    ap.add_argument("--closure-only", action="store_true", help="skip the builds; report the import closure only")
    ap.add_argument("--all", action="store_true", help="build and compare every release binary, not only the closure set")
    ap.add_argument("--tags", default="", help="extra build tags for every binary, comma-separated (to measure a recipe change)")
    ap.add_argument("--workdir", help="parent directory for the exported trees and binaries (default: the system temp dir)")
    ap.add_argument("--output", help="write the JSON report here")
    ap.add_argument("--markdown", help="write the pull-request comment here")
    args = ap.parse_args()
    try:
        rep = report(args.base, args.head, config_path=args.config, measure=not args.closure_only,
                     every=args.all, workdir=args.workdir, extra_tags=[t for t in args.tags.split(",") if t])
    except (RuntimeError, ValueError, subprocess.CalledProcessError) as err:
        detail = getattr(err, "stderr", "") or ""
        print(f"blast_radius: {err} {detail}".strip(), file=sys.stderr)
        return 1
    body = json.dumps(rep, indent=2, sort_keys=True) + "\n"
    if args.output:
        with open(args.output, "w", encoding="utf-8") as fh:
            fh.write(body)
    if args.markdown:
        with open(args.markdown, "w", encoding="utf-8") as fh:
            fh.write(markdown(rep))
    if not args.output:
        sys.stdout.write(body)
    print(summary(rep), file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
