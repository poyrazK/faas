#!/usr/bin/env python3
"""Fill the Go build cache with what a unit release build compiles.

release-units.yml builds with the release recipe: linux/amd64, CGO off, the
Makefile's GO_BUILD_FLAGS and, for daemons, its DAEMON_TAGS. The light
tier's caches hold none of those compile results (they build with CGO on and
no -trimpath), so on 2026-10-21 a one-daemon unit release spent 194 s
compiling gatewayd-internal from scratch to compare its code and 72 s more on
the release build. release-units keeps its own cache, one entry per base
release, and its warm-build-cache job runs this off the deploy path:

  * every release binary's import graph (go list, what affected_units.py
    reads), so the module cache holds every module a build needs;
  * every binary of a deployable lane (release_units.json), built the way
    affected_units.py --code builds it (binary_code.build);
  * the tools a unit release carries (scripts/build-unit-release.sh).

The version stamp and build ID are link flags and link results are never
cached, so the release build reuses every package compiled here.
"""
import argparse
import os
import subprocess
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import affected_units  # noqa: E402
import binary_code  # noqa: E402

# Built by scripts/build-unit-release.sh with GO_BUILD_FLAGS and no tags.
TOOLS = ("deployctl", "migrate", "gregalectl")


def lane_binaries(config):
    """The binaries a unit release can ship: those of a deployable lane."""
    return sorted(n for n, b in config["binaries"].items() if b["lane"] in affected_units.DEPLOY_LANES)


def warm(root=".", config_path=affected_units.DEFAULT_CONFIG):
    """Load every release binary's graph and build the lane binaries and the
    tools with the release recipe, discarding the binaries. Returns what it
    built."""
    with open(os.path.join(root, "Makefile"), encoding="utf-8") as fh:
        makefile = fh.read()
    daemons = affected_units.makefile_list(makefile, "DAEMONS")
    clis = affected_units.makefile_list(makefile, "CLIS")
    affected_units.binary_closures(daemons, clis, affected_units.module_path(root), root,
                                   affected_units.daemon_tags(makefile))
    head = affected_units.git("rev-parse", "HEAD", root=root).strip()
    side = affected_units.BuildSide(head, root)
    lanes = lane_binaries(affected_units.load_config(config_path))
    with tempfile.TemporaryDirectory(prefix="unit-warm-") as out:
        binary_code.build(side, lanes, out)
        env = dict(os.environ, GOOS="linux", GOARCH="amd64", CGO_ENABLED="0")
        cmd = [os.environ.get("GO", "go"), "build", *side.flags, "-o", out + os.sep] + [f"./cmd/{t}" for t in TOOLS]
        run = subprocess.run(cmd, cwd=root, env=env, capture_output=True, text=True)
        if run.returncode != 0:
            raise RuntimeError(f"go build {', '.join(TOOLS)}: {run.stderr.strip()}")
    return lanes + list(TOOLS)


def main():
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n", 1)[0])
    parser.add_argument("--root", default=".")
    args = parser.parse_args()
    built = warm(args.root)
    print(f"warm_unit_build_cache: built {len(built)} binaries with the release recipe: {', '.join(built)}")


if __name__ == "__main__":
    main()
