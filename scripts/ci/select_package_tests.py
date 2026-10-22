#!/usr/bin/env python3
"""Pick the tests of one large Go package that a change most likely affects.

The light PR tier cannot run the complete suites of pkg/apid/apidserver,
pkg/state or cmd/gregale (thousands of tests each) inside its 10-minute
budget; the mega tier (ci.yml) runs them in full nightly and before every
release. This selector keeps PR feedback relevant by choosing, in priority
order and up to --cap tests:

  1. every test declared in a changed test file of the package;
  2. tests in files named after a changed source file
     (foo.go -> foo_test.go, and foo_*_test.go when few files match);
  3. tests in files that mention a distinctive identifier declared in a
     changed source file, most specific identifiers first. With --base, only
     the top-level declarations a changed hunk touches count.

Reads changed paths on stdin. Prints a `go test -run` pattern, or nothing
when no test is selected. A summary goes to stderr.

Usage:
  git diff --name-only BASE HEAD | scripts/ci/select_package_tests.py --package pkg/apid/apidserver --cap 150
"""
import argparse
import os
import re
import subprocess
import sys

TEST_FUNC = re.compile(r"^func (Test\w+)\(\w+ \*testing\.T\)", re.M)
# Top-level declarations whose names tests are likely to mention.
FUNC_DECL = re.compile(r"^func (?:\([^)]*\) )?([A-Za-z_]\w*)\s*[\[(]", re.M)
TYPE_DECL = re.compile(r"^type ([A-Za-z_]\w*)", re.M)
BLOCK_DECL = re.compile(r"^(?:var|const|type) \(\n(.*?)^\)", re.M | re.S)
SINGLE_DECL = re.compile(r"^(?:var|const) ([A-Za-z_]\w*)", re.M)
BLOCK_NAME = re.compile(r"^\t([A-Za-z_]\w*)", re.M)
BUILD_SUFFIX = re.compile(r"_(linux|darwin|unix|windows|amd64|arm64)$")
WORD = re.compile(r"[A-Za-z_]\w*")

# Identifiers shorter than this, or mentioned by more than this many test
# files, are too generic to say which tests exercise the changed code.
MIN_IDENT_LEN = 6
MAX_IDENT_FILES = 12
# A source file's stem selects foo_*_test.go only when this few files share
# it; generic stems (pgstore, handlers) would otherwise select hundreds.
MAX_STEM_PREFIX_FILES = 8
TOP_DECL = re.compile(r"^(func|type|var|const)\b")
HUNK = re.compile(r"^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@", re.M)


def changed_lines(base, head, path):
    """New-side line numbers a diff touches; None when the file is new or the
    diff cannot be read (every declaration then counts)."""
    out = subprocess.run(["git", "diff", "-U0", "--no-color", base, head, "--", path],
                         capture_output=True, text=True)
    if out.returncode != 0:
        return None
    if "\nnew file mode" in out.stdout or out.stdout.startswith("new file mode"):
        return None
    lines = set()
    for start, count in HUNK.findall(out.stdout):
        start, count = int(start), int(count) if count != "" else 1
        lines.update(range(start, start + max(count, 1)))
    return lines


def touched_source(source, lines):
    """The top-level declarations of source that overlap lines, as text."""
    if lines is None:
        return source
    rows = source.split("\n")
    starts = [i for i, row in enumerate(rows) if TOP_DECL.match(row)] + [len(rows)]
    parts = []
    for begin, end in zip(starts, starts[1:]):
        if any(begin + 1 <= n <= end for n in lines):
            parts.append("\n".join(rows[begin:end]))
    return "\n".join(parts)


def declared_identifiers(source):
    names = set(FUNC_DECL.findall(source)) | set(TYPE_DECL.findall(source)) | set(SINGLE_DECL.findall(source))
    for block in BLOCK_DECL.findall(source):
        names |= set(BLOCK_NAME.findall(block))
    return {n for n in names if len(n) >= MIN_IDENT_LEN and n not in ("init", "main") and not n.startswith("Test")}


def stem(filename):
    return BUILD_SUFFIX.sub("", filename[:-len(".go")])


def select(package, changed_paths, cap, base=None, head="HEAD", root="."):
    """Return (selected test names in priority order, total tests, reasons,
    candidate count before the cap)."""
    pkgdir = os.path.join(root, package)
    files = sorted(f for f in os.listdir(pkgdir) if f.endswith(".go"))
    test_files = [f for f in files if f.endswith("_test.go")]
    tests_in = {}
    text_of = {}
    for f in test_files:
        with open(os.path.join(pkgdir, f), encoding="utf-8") as fh:
            text_of[f] = fh.read()
        tests_in[f] = TEST_FUNC.findall(text_of[f])
    total = sum(len(v) for v in tests_in.values())

    prefix = package.rstrip("/") + "/"
    changed = []
    for p in changed_paths:
        p = p.strip()
        if p.startswith("./"):
            p = p[2:]
        if p.startswith(prefix) and "/" not in p[len(prefix):] and p.endswith(".go"):
            changed.append(p[len(prefix):])

    ordered, seen = [], set()
    reasons = {"changed test files": 0, "file-name match": 0, "identifier match": 0}

    def add(names, reason):
        for name in names:
            if name not in seen:
                seen.add(name)
                ordered.append(name)
                reasons[reason] += 1

    changed_tests = [f for f in changed if f.endswith("_test.go") and f in tests_in]
    changed_sources = [f for f in changed if not f.endswith("_test.go") and os.path.exists(os.path.join(pkgdir, f))]
    for f in changed_tests:
        add(tests_in[f], "changed test files")
    for f in changed_sources:
        s = stem(f)
        prefixed = [t for t in test_files if t.startswith(s + "_") and t != s + "_test.go"]
        matched = [t for t in test_files if t == s + "_test.go"]
        if len(prefixed) <= MAX_STEM_PREFIX_FILES:
            matched += prefixed
        for t in matched:
            add(tests_in[t], "file-name match")

    # Rank test files by how many specific identifiers of the changed sources
    # they mention; rarer identifiers count for more.
    idents = set()
    for f in changed_sources:
        with open(os.path.join(pkgdir, f), encoding="utf-8") as fh:
            source = fh.read()
        lines = changed_lines(base, head, package.rstrip("/") + "/" + f) if base else None
        idents |= declared_identifiers(touched_source(source, lines))
    score = {}
    files_with = {}
    if idents:
        # One tokenizing pass over the test files instead of one regex scan
        # per identifier: the large packages carry ~200k lines of tests.
        for t in test_files:
            for token in set(WORD.findall(text_of[t])) & idents:
                files_with.setdefault(token, []).append(t)
    for ident in sorted(idents):
        hits = files_with.get(ident, [])
        if not hits or len(hits) > MAX_IDENT_FILES:
            continue
        for t in hits:
            score[t] = score.get(t, 0) + 1.0 / len(hits)
    for t in sorted(score, key=lambda t: (-score[t], t)):
        add(tests_in[t], "identifier match")

    return ordered[:cap], total, reasons, len(ordered)


def test_patterns(selected, batch_size=None):
    """Partition the same selected tests without changing priority or coverage."""
    if batch_size is not None and batch_size <= 0:
        raise ValueError("batch size must be positive")
    size = batch_size or max(1, len(selected))
    return ["^(" + "|".join(selected[i:i + size]) + ")$"
            for i in range(0, len(selected), size)]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--package", required=True, help="package directory, e.g. pkg/apid/apidserver")
    ap.add_argument("--cap", type=int, default=150, help="maximum number of tests to select")
    ap.add_argument("--base", help="diff base; limits identifiers to declarations a changed hunk touches")
    ap.add_argument("--head", default="HEAD", help="diff head (default HEAD)")
    ap.add_argument("--root", default=".", help="tree to read package files from (default the checkout)")
    ap.add_argument("--batch-size", type=int, help="emit one bounded test pattern per batch")
    args = ap.parse_args()
    if args.batch_size is not None and args.batch_size <= 0:
        ap.error("--batch-size must be positive")
    selected, total, reasons, candidates = select(args.package, sys.stdin.read().splitlines(), args.cap,
                                                  base=args.base, head=args.head, root=args.root)
    detail = ", ".join(f"{k}: {v}" for k, v in reasons.items())
    capped = f"; capped from {candidates}" if candidates > len(selected) else ""
    print(f"{args.package}: selected {len(selected)} of {total} tests ({detail}{capped}); "
          "the full suite runs in the mega tier", file=sys.stderr)
    for pattern in test_patterns(selected, args.batch_size):
        print(pattern)


if __name__ == "__main__":
    main()
