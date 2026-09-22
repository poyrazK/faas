#!/usr/bin/env python3
"""Fixture tests for check_shell_quoting.py.

A gate that cannot fail is not a gate, and one that cries wolf gets ignored.
Each fixture is a throwaway tree containing one shape; the checker must fire
on the dangerous ones and stay quiet on the near-misses.

The load-bearing case is PRE_FIX_DNS: the verbatim shape that shipped and that
CodeQL caught as go/unsafe-quoting (critical). Its `'%s'` and its
`fmt.Sprintf(` are ten lines apart with the format string assembled from
`+`-concatenated literals, which is why a line-oriented grep could not see it.
"""

from __future__ import annotations

import os
import shutil
import subprocess
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
CHECKER = os.path.join(HERE, "check_shell_quoting.py")
REPO_ROOT = os.path.abspath(os.path.join(HERE, "..", ".."))

# The defect as it shipped, before PR #3167's fix.
PRE_FIX_DNS = '''package gateway

import "fmt"

func upsert(providerURL, zone, body string) string {
	return fmt.Sprintf(
		"# FAAS_DNS_PROVIDER=manual: UpsertRecord\\n"+
			"# ProviderURL: %s\\n"+
			"# Find zone id:\\n"+
			"#   curl -H 'Authorization: Bearer $CF_API_TOKEN' '%s/zones?name=%s'\\n"+
			"# Then create the A record (replace <ZONE_ID>):\\n"+
			"curl -X POST '%s/zones/<ZONE_ID>/dns_records' \\\\\\n"+
			"  -H 'Content-Type: application/json' \\\\\\n"+
			"  -d '%s'\\n",
		providerURL, providerURL, zone, providerURL, body)
}
'''

# The same function after the fix: quotes come from the helper, not the format.
POST_FIX_DNS = '''package gateway

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/safetext"
)

func upsert(providerURL, zone, body string) string {
	return fmt.Sprintf(
		"# ProviderURL: %s\\n"+
			"#   curl -H 'Authorization: Bearer $CF_API_TOKEN' %s\\n"+
			"curl -X POST %s \\\\\\n"+
			"  -d %s\\n",
		providerURL,
		safetext.ShellSingleQuote(providerURL+"/zones?name="+zone),
		safetext.ShellSingleQuote(providerURL+"/zones/<ZONE_ID>/dns_records"),
		safetext.ShellSingleQuote(body))
}
'''

ONE_LINER = '''package thing

import "fmt"

func cmd(body string) string {
	return fmt.Sprintf("curl -X POST https://example.test -d '%s'", body)
}
'''

SQL_NOT_SHELL = '''package thing

import "fmt"

func partition(suffix string) string {
	return fmt.Sprintf("CREATE TABLE probes_%s PARTITION OF probes FOR VALUES IN ('%s')", suffix, suffix)
}
'''

POWERSHELL = '''package main

import (
	"fmt"
	"io"
)

func render(w io.Writer, name string) {
	_, _ = fmt.Fprintf(w, "  if ($tokens[1] -eq '%s') {\\n", name)
}
'''

INERT_SHELL_CONSTANT = '''package runnerparity

// A shell script held as a plain constant. %s here is shell printf's own verb,
// not a Go format verb — nothing interpolates it.
const script = `#!/bin/sh
method=$(printf '%s' "$env" | sed -n 's/.*method.*/x/p')
curl -sS -d '%s' http://127.0.0.1:8080
`
'''

PROSE = '''package thing

import "fmt"

func describe(name string) string {
	return fmt.Sprintf("the app '%s' could not be resolved", name)
}
'''


def run_checker(tree: str) -> int:
    proc = subprocess.run(
        [sys.executable, CHECKER, tree], capture_output=True, text=True
    )
    return proc.returncode


def make_tree(relpath: str, content: str) -> str:
    tree = tempfile.mkdtemp(prefix="shellquote-")
    full = os.path.join(tree, relpath)
    os.makedirs(os.path.dirname(full), exist_ok=True)
    with open(full, "w", encoding="utf-8") as handle:
        handle.write(content)
    return tree


CASES = [
    ("pre-fix DNS shape (multi-line concatenation)", "pkg/gateway/a.go", PRE_FIX_DNS, "reject"),
    ("post-fix DNS shape", "pkg/gateway/b.go", POST_FIX_DNS, "accept"),
    ("single-line curl -d", "pkg/thing/c.go", ONE_LINER, "reject"),
    ("SQL literal, not a shell command", "pkg/thing/d.go", SQL_NOT_SHELL, "accept"),
    ("PowerShell completion", "cmd/gregale/e.go", POWERSHELL, "accept"),
    ("inert shell constant, not a format string", "guest/f.go", INERT_SHELL_CONSTANT, "accept"),
    ("prose with a quoted verb", "pkg/thing/g.go", PROSE, "accept"),
]


def main() -> int:
    failures = 0
    for name, relpath, content, expect in CASES:
        tree = make_tree(relpath, content)
        try:
            code = run_checker(tree)
        finally:
            shutil.rmtree(tree, ignore_errors=True)
        rejected = code != 0
        if (expect == "reject") != rejected:
            print(
                f"FAIL: {name}: expected {expect}, checker "
                f"{'rejected' if rejected else 'passed'}",
                file=sys.stderr,
            )
            failures += 1
        else:
            print(f"ok: {name}")

    if run_checker(REPO_ROOT) != 0:
        print("FAIL: repository has shell-quoting violations", file=sys.stderr)
        failures += 1
    else:
        print("ok: repository is clean")

    if failures:
        print(f"check_shell_quoting_test: {failures} failure(s)", file=sys.stderr)
        return 1
    print("check_shell_quoting_test: all passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
