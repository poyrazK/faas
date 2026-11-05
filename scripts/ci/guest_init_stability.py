#!/usr/bin/env python3
"""Fail when a change that cannot alter what guest-init runs alters its code.

Every runtime base embeds guest-init as PID 1 and ADR-510 refuses to restore a
snapshot onto a base whose bytes changed, so each release that changes
guest-init retires every snapshot (docs/adr/20261105150000-guest-init-reuse-
keeps-bases-and-snapshots.md). The release keeps the previous release's bytes
while the code is the same (scripts/reuse-release-guest-init.sh), which only
helps if an edit elsewhere in the repository does not change the code.

This check builds guest-init at HEAD with the release recipe (the Makefile's
GO_BUILD_FLAGS and GUEST_TAGS, linux/amd64, CGO off), adds a file to pkg/api
that nothing references (a function, a constant, a type without methods), builds
it again, and fails if the code digest differs. pkg/api is the package every
fix touches and guest-init links code from 26 of its files. What it catches:

  * a build tag or flag that stamps something per build;
  * a dependency that makes the linker keep every exported method of every
    type guest-init converts to an interface (a template package, or gRPC's
    trace package, which Makefile GUEST_TAGS drops), so that a change to any
    method body changes the code;
  * anything else that ties the code of guest-init to code it does not run.

A type with a method, new or added to a linked type, changes the code in any
Go program (the type descriptor lists the method's name, even when nothing
uses the type), which is why the probe adds none.

Usage: scripts/ci/guest_init_stability.py            (builds twice; ~2 minutes warm)
"""
import os
import re
import subprocess
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import check_daemon_deps as cdd  # noqa: E402
import guest_init_reuse as gir  # noqa: E402

REPO = os.path.dirname(os.path.dirname(HERE))
PROBE_PATH = "pkg/api/zz_guest_init_stability_probe.go"
PROBE = """package api

// Written by scripts/ci/guest_init_stability.py. Nothing references it.

func GuestInitStabilityProbeFunc() int { return 42 }

type GuestInitStabilityProbeType struct{ A, B int }

const GuestInitStabilityProbeConst = "never used"
"""
# The inputs of `go build ./guest/init`: the package, what it imports and the
# module files.
EXPORT = ["guest", "pkg", "go.mod", "go.sum"]


def go_build_flags(makefile):
    """The Makefile's GO_BUILD_FLAGS as a list."""
    m = re.search(r"^GO_BUILD_FLAGS\s*:=\s*(.*?)\s*$", makefile, re.M)
    if not m:
        raise ValueError("Makefile has no GO_BUILD_FLAGS := assignment")
    return m.group(1).split()


def build_command(flags, tags, out):
    """The release build of guest-init (Makefile build-release-batch)."""
    cmd = [os.environ.get("GO", "go"), "build", *flags]
    if tags:
        cmd.append("-tags=" + ",".join(tags))
    return cmd + ["-o", out, "./guest/init"]


def build(tree, out, flags, tags):
    env = dict(os.environ, GOOS="linux", GOARCH="amd64", CGO_ENABLED="0")
    run = subprocess.run(build_command(flags, tags, out), cwd=tree, env=env, capture_output=True, text=True)
    if run.returncode != 0:
        raise RuntimeError(f"go build ./guest/init: {run.stderr.strip()}")


def export_head(dest):
    archive = subprocess.Popen(["git", "archive", "--format=tar", "HEAD", *EXPORT], cwd=REPO, stdout=subprocess.PIPE)
    untar = subprocess.run(["tar", "-x", "-f", "-", "-C", dest], stdin=archive.stdout, capture_output=True)
    archive.stdout.close()
    if archive.wait() != 0 or untar.returncode != 0:
        raise RuntimeError(f"git archive HEAD: {untar.stderr.decode().strip()}")


def check(tree, flags, tags, builder=build):
    """The verdict (guest_init_reuse's) of the probe build against the base
    build of the tree."""
    base, probe = os.path.join(tree, "guest-init.base"), os.path.join(tree, "guest-init.probe")
    builder(tree, base, flags, tags)
    with open(os.path.join(tree, PROBE_PATH), "w", encoding="utf-8") as fh:
        fh.write(PROBE)
    builder(tree, probe, flags, tags)
    return gir.classify(probe, base)


def main():
    with open(os.path.join(REPO, "Makefile"), encoding="utf-8") as fh:
        makefile = fh.read()
    flags, tags = go_build_flags(makefile), cdd.guest_tags(makefile)
    with tempfile.TemporaryDirectory(prefix="guest-init-stability-") as tmp:
        export_head(tmp)
        verdict = check(tmp, flags, tags)
    if verdict == gir.CHANGED:
        print("guest-init's code changed when a file nothing references was added to pkg/api.\n"
              "Something ties PID 1 to code it does not run: look for a template package or\n"
              "gRPC's trace package in its import closure (make daemon-deps-check) and for a\n"
              "per-build stamp in the release build flags "
              "(docs/adr/20261105150000-guest-init-reuse-keeps-bases-and-snapshots.md).", file=sys.stderr)
        return 1
    print(f"guest-init code is unchanged by a file nothing references ({verdict}; tags {','.join(tags) or 'none'})")
    return 0


if __name__ == "__main__":
    sys.exit(main())
