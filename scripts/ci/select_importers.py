#!/usr/bin/env python3
"""Pick the unchanged importers a change could break, for the light PR tier.

golangci-lint compiles only the packages a pull request changed. An importer
breaks when a changed package's exported API changes under it: a renamed
function, a removed field, a new parameter. Compiling every direct importer
costs too much (pkg/api has over 200), so this selects only the importers
whose code mentions an exported name a diff hunk touched, on either side of
the diff:

  - a function or method whose signature changed (a body edit cannot break
    an importer's build);
  - a type, and the fields or interface methods on changed lines;
  - variables and constants, including entries on changed lines of a block.

A file added by the change cannot break an importer; a deleted file counts
all of its declarations. References are matched as selectors (`.Name`), which
covers qualified identifiers, field and method access. Importers that only
reach the API through another package, or that implement a touched interface
without naming it, are left to the mega tier, which vets the whole tree
nightly.

Reads changed paths on stdin and prints the selected package directories
(./a ./b). A summary goes to stderr.

In CI, selection shares the importer compile budget. If it expires, unchanged
importers are deferred to the mega tier with a warning, just as vet batches are.

Usage:
  git diff --name-only BASE HEAD | scripts/ci/select_importers.py --base BASE
"""
import argparse
import os
import re
import signal
import subprocess
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import affected_packages  # noqa: E402
import select_package_tests as spt  # noqa: E402

SELECTOR = re.compile(r"\.([A-Z]\w*)")
HUNK = re.compile(r"^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@", re.M)
MEMBER = re.compile(r"^\s+([A-Z]\w*)\b")
BLOCK_HEAD = re.compile(r"^(?:var|const|type) \($")
BLOCK_ENTRY = re.compile(r"^\t([A-Za-z_]\w*)")


def git(*args, root="."):
    out = subprocess.run(["git", *args], capture_output=True, text=True, cwd=root)
    return out.stdout if out.returncode == 0 else None


def hunk_lines(diff):
    """Old-side and new-side line numbers a -U0 diff changes (pure additions
    touch nothing on the old side, pure deletions nothing on the new side)."""
    old, new = set(), set()
    for a, b, c, d in HUNK.findall(diff):
        b = 1 if b == "" else int(b)
        d = 1 if d == "" else int(d)
        old.update(range(int(a), int(a) + b))
        new.update(range(int(c), int(c) + d))
    return old, new


def touched_names(source, lines, old_side=True):
    """Exported names whose declaration a changed line touches (every
    declaration when lines is None). A function counts only when its
    signature changed, a type through the members on changed lines, a
    var/const/type block through the entries on changed lines: a body edit
    or a comment cannot break an importer's build. A struct's own name
    counts (for composite literals) only when an existing field changed,
    which is the old side; an interface's name counts on either side, since
    a new method breaks implementations that assert it."""
    rows = source.split("\n")
    starts = [i for i, row in enumerate(rows) if spt.TOP_DECL.match(row)] + [len(rows)]
    names = set()
    for begin, end in zip(starts, starts[1:]):
        decl = rows[begin:end]
        # The declaration proper ends at its closing line; what follows is
        # blank space and the next declaration's doc comment.
        close = next((i for i, row in enumerate(decl) if row in ("}", ")")), len(decl) - 1)
        if not decl[0].rstrip().endswith(("{", "(")):
            close = 0
        hit = {i for i in range(close + 1) if lines is None or begin + 1 + i in lines}
        hit = {i for i in hit if decl[i].strip() and not decl[i].lstrip().startswith("//")}
        if not hit:
            continue
        head = decl[0]
        if head.startswith("func"):
            signature_end = next((i for i, row in enumerate(decl) if row.rstrip().endswith("{")), 0)
            if min(hit) <= signature_end:
                names.update(spt.FUNC_DECL.findall(head + "("))
        elif head.rstrip().endswith("(") and BLOCK_HEAD.match(head):
            entry = None
            for i in range(1, close + 1):
                m = BLOCK_ENTRY.match(decl[i])
                entry = m.group(1) if m else entry
                if i in hit and entry:
                    names.add(entry)
        elif head.startswith("type"):
            if old_side or " interface" in head or close == 0:
                names.update(spt.TYPE_DECL.findall(head))
            names.update(n for i in hit for n in MEMBER.findall(decl[i]))
        else:
            names.update(spt.SINGLE_DECL.findall(head))
    return {n for n in names if n[:1].isupper()}


def changed_exports(path, base, head, root="."):
    """Exported names an importer could break on after path changed."""
    new_src = git("show", f"{head}:{path}", root=root)
    old_src = git("show", f"{base}:{path}", root=root)
    if old_src is None:
        return set()  # an added file cannot break an existing importer
    if new_src is None:
        return touched_names(old_src, None)
    old_lines, new_lines = hunk_lines(git("diff", "-U0", "--no-color", base, head, "--", path, root=root) or "")
    old_rows, new_rows = old_src.split("\n"), new_src.split("\n")
    # gofmt re-aligns a struct or block when one entry grows: lines whose
    # text survives on the other side apart from spacing did not change.
    old_text = {normal(old_rows[n - 1]) for n in old_lines if n <= len(old_rows)}
    new_text = {normal(new_rows[n - 1]) for n in new_lines if n <= len(new_rows)}
    old_lines = {n for n in old_lines if n <= len(old_rows) and normal(old_rows[n - 1]) not in new_text}
    new_lines = {n for n in new_lines if n <= len(new_rows) and normal(new_rows[n - 1]) not in old_text}
    # A name the old file did not declare is new: no unchanged importer can
    # use it yet, so only changed or removed declarations count.
    existing = touched_names(old_src, None)
    return touched_names(old_src, old_lines) | (touched_names(new_src, new_lines, old_side=False) & existing)


def normal(row):
    return " ".join(row.split())


def select(paths, pkgs, base, head="HEAD", root="."):
    """Return (selected importer dirs, most specific first; {changed package:
    names}; direct importer count). An importer scores 1/n for each changed
    name it uses that n importers use, so the light tier's time budget
    reaches the likeliest breakages first."""
    _, changed, _, _ = affected_packages.select(paths, pkgs)
    changed = set(changed)
    names_by_pkg = {}
    for p in paths:
        p = p.strip()
        p = p[2:] if p.startswith("./") else p
        if not p.endswith(".go") or p.endswith("_test.go"):
            continue
        d = os.path.dirname(p) or "."
        if d not in pkgs:
            continue
        names = changed_exports(p, base, head, root)
        if names:
            names_by_pkg.setdefault(d, set()).update(names)

    uses, importers, selectors = {}, set(), {}
    for d, names in names_by_pkg.items():
        for imp, info in pkgs.items():
            if imp in changed or pkgs[d]["path"] not in info["imports"]:
                continue
            importers.add(imp)
            if imp not in selectors:
                selectors[imp] = selector_names(os.path.join(root, imp))
            hits = selectors[imp] & names
            if hits:
                uses.setdefault(imp, set()).update((d, n) for n in hits)
    users = {}
    for refs in uses.values():
        for ref in refs:
            users[ref] = users.get(ref, 0) + 1
    score = {imp: sum(1.0 / users[ref] for ref in refs) for imp, refs in uses.items()}
    return sorted(uses, key=lambda imp: (-score[imp], imp)), names_by_pkg, len(importers)


def selector_names(pkgdir):
    """Every exported name the package's files (tests included) select with
    `.Name`: one tokenizing pass instead of one search per changed name."""
    names = set()
    for f in os.listdir(pkgdir):
        if f.endswith(".go"):
            with open(os.path.join(pkgdir, f), encoding="utf-8", errors="replace") as fh:
                names.update(SELECTOR.findall(fh.read()))
    return names


def run_with_budget():
    started = os.environ.get("LINT_SELECTED_AT")
    budget = os.environ.get("BUDGET_SECONDS")
    if started is None or budget is None:
        return None
    remaining = float(started) + float(budget) - time.time()
    warning = ("::warning::importer selection reached the light job's compile budget; "
               "unchanged importers are deferred to the mega tier")
    if remaining <= 0:
        print(warning, file=sys.stderr)
        return 0

    # Supervise the whole selection, including go list and lazy blob fetches.
    # The child runs the ordinary CLI without recursively supervising itself.
    env = os.environ.copy()
    env.pop("LINT_SELECTED_AT")
    env.pop("BUDGET_SECONDS")
    command = [sys.executable, os.path.abspath(__file__), *sys.argv[1:]]
    with subprocess.Popen(command, env=env, stdout=subprocess.PIPE, text=True,
                          start_new_session=True) as child:
        try:
            output, _ = child.communicate(timeout=remaining)
        except subprocess.TimeoutExpired:
            # Git can have transport children holding the output pipes open.
            # Stop the process group so an expired selection cannot hang cleanup.
            try:
                os.killpg(child.pid, signal.SIGTERM)
            except ProcessLookupError:
                pass
            try:
                child.communicate(timeout=1)
            except subprocess.TimeoutExpired:
                try:
                    os.killpg(child.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                child.communicate()
            print(warning, file=sys.stderr)
            return 0
        sys.stdout.write(output)
        return child.returncode


def main():
    budget_status = run_with_budget()
    if budget_status is not None:
        return budget_status
    ap = argparse.ArgumentParser()
    ap.add_argument("--base", required=True, help="diff base commit")
    ap.add_argument("--head", default="HEAD", help="diff head (default HEAD)")
    args = ap.parse_args()
    selected, names_by_pkg, importers = select(sys.stdin.read().splitlines(), affected_packages.go_packages(),
                                               args.base, args.head)
    for d, names in sorted(names_by_pkg.items()):
        shown = " ".join(sorted(names)[:12]) + (" ..." if len(names) > 12 else "")
        print(f"{d}: {len(names)} changed exported names ({shown})", file=sys.stderr)
    print(f"importers to compile: {len(selected)} of {importers} unchanged direct importers", file=sys.stderr)
    print(" ".join("./" + d if d != "." else "." for d in selected))


if __name__ == "__main__":
    raise SystemExit(main())
