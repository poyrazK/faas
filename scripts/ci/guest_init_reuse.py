#!/usr/bin/env python3
"""Decide whether a release can ship the previous release's guest-init.

Every runtime base embeds guest-init as PID 1, and ADR-510 refuses to restore
a snapshot onto a base whose bytes differ from the base it was captured on. A
release's guest-init is rebuilt from its commit, and its bytes change with
anything in its import closure: the Go build ID hashes every linked package,
and the line table moves when any linked file gains a line. On production-us
that was 26 new guest-init digests in 12 days, each followed by a refused
snapshot per app
(docs/adr/20261105150000-guest-init-reuse-keeps-bases-and-snapshots.md).

Like the kernel (scripts/reuse-release-kernel.sh) and per-unit daemon
selection (docs/adr/1650-per-unit-control-plane-releases.md, "Selection by
code"), the release keeps the bytes it already ships while the code is the
same. The code digest is binary_code.py's: the name, size and contents of
every section the program loads, except the line table and the module data.
A guest-init build does not blank its build ID, so the code digest here also
leaves out the two notes the Go linker derives from it.

  guest_init_reuse.py compare --head PATH --prior PATH

prints "same" (identical bytes), "lines" (the same code, so the prior
binary is reusable), or "changed" (different code), and exits 0 for "same"
and "lines", 1 for "changed" and 2 when a file is not a 64-bit
little-endian ELF binary.

The check stays on the safe side. A binary whose code digest differs for a
reason that does not change what it runs (a section that moved because a
dependency grew) is "changed", and the release ships the new bytes: one
refused snapshot per app, the cost of every release before this one.
"""
import argparse
import importlib.util
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
_spec = importlib.util.spec_from_file_location("binary_code", os.path.join(HERE, "binary_code.py"))
bc = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(bc)

# The ELF notes that carry the Go build ID: the Go one, and the GNU one the
# linker derives from it. Daemon code builds blank the ID with -buildid=; the
# release's guest-init is built with the Makefile's flags, which leave it in
# (rc.320 and rc.321 built with the release recipe differ only in these two).
BUILD_ID_NOTES = (".note.go.buildid", ".note.gnu.build-id")
IGNORED_SECTIONS = BUILD_ID_NOTES

SAME, LINES, CHANGED = "same", "lines", "changed"


def digests(path):
    """(file digest, code digest) of a guest-init binary."""
    return bc.elf_digests(path, ignore=IGNORED_SECTIONS)


def classify(head, prior):
    """SAME when the two binaries are byte-identical, LINES when only what
    the code digest leaves out differs, else CHANGED."""
    head_digests, prior_digests = digests(head), digests(prior)
    if head_digests[0] == prior_digests[0]:
        return SAME
    return LINES if head_digests[1] == prior_digests[1] else CHANGED


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    sub = ap.add_subparsers(dest="command", required=True)
    cmp_ = sub.add_parser("compare", help="classify a head guest-init against the prior release's")
    cmp_.add_argument("--head", required=True, help="the guest-init built at the release commit")
    cmp_.add_argument("--prior", required=True, help="the guest-init the previous release shipped")
    args = ap.parse_args(argv)
    try:
        verdict = classify(args.head, args.prior)
    except (OSError, RuntimeError) as err:
        print(f"guest-init code digest: {err}", file=sys.stderr)
        return 2
    print(verdict)
    return 1 if verdict == CHANGED else 0


if __name__ == "__main__":
    sys.exit(main())
