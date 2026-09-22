#!/usr/bin/env python3
"""check_shell_quoting.py — reject values interpolated into hand-written shell quotes.

Inside single quotes a POSIX shell treats every byte literally except the
single quote itself, which ends the quoted run. So a format string of the
shape

    fmt.Sprintf("curl -d '%s'", body)

is safe only while `body` contains no apostrophe. When it does, the argument
terminates early and the remainder is parsed as shell — by whichever operator
pastes the command in. CodeQL flagged exactly this as go/unsafe-quoting
(critical) in pkg/gateway/dns_provider_manual.go; this gate is the local
tripwire so the next one is caught before it reaches a scan.

JSON encoding does not help: an apostrophe requires no escaping in JSON and
passes through json.Marshal untouched. The fix is safetext.ShellSingleQuote,
which supplies the quotes itself — so a call site cannot keep the literal
quotes and double-wrap.

Why Python rather than a grep in check_text_encoding.sh: the shape that
actually shipped had its `'%s'` and its `fmt.Sprintf(` ten lines apart, with
the format string assembled from `+`-concatenated literals. A line-oriented
grep sees one or the other, never both. This joins the concatenation first.

Scope is POSIX shell only. cmd/gregale/completion_powershell.go interpolates
into PowerShell single quotes, which escape by doubling ('') rather than by
the POSIX close-escape-reopen dance, so ShellSingleQuote would be wrong there.
Its inputs are CLI command names — compile-time literals in cli_meta.go, none
containing an apostrophe — so the shape is inert. See POWERSHELL_MARKERS.
"""

from __future__ import annotations

import os
import re
import sys

SCAN_ROOTS = ("pkg", "cmd", "guest")

# Formatting calls whose first string argument is a format string.
FORMAT_CALL = re.compile(r"\bfmt\.(?:Sprintf|Fprintf|Printf|Errorf)\s*\(")

# A Go string literal: interpreted ("...") or raw (`...`).
STRING_LITERAL = re.compile(r'"((?:[^"\\]|\\.)*)"|`([^`]*)`')

# A format verb sitting inside single quotes.
QUOTED_VERB = re.compile(r"'%[-+ #0-9.*]*[sqvdxX]'")

# Tokens that mark the literal as a shell command rather than SQL or prose.
SHELL_MARKERS = re.compile(
    r"\b(curl|wget|ssh|scp|rsync|bash|zsh|docker|podman|gcloud|aws|kubectl"
    r"|systemctl|journalctl|psql|pg_dump|openssl|nft|iptables|tar|mount"
    r"|umount|chmod|chown|install|apt-get|dnf)\b"
    r"|\s-[A-Za-z]\s|\s--[a-z-]+[= ]|\$\(|&&|\|\|"
)

# PowerShell completion output — different quoting rules, closed-set input.
POWERSHELL_MARKERS = re.compile(r"\$tokens|CompletionResult|-like|ParameterName")

ALLOW_PATH = re.compile(r"(^|/)safetext/|_test\.go$|/testdata/")


def go_files(root: str):
    for scan in SCAN_ROOTS:
        base = os.path.join(root, scan)
        if not os.path.isdir(base):
            continue
        for dirpath, _dirnames, filenames in os.walk(base):
            for name in sorted(filenames):
                if not name.endswith(".go"):
                    continue
                path = os.path.join(dirpath, name)
                rel = os.path.relpath(path, root)
                if ALLOW_PATH.search(rel):
                    continue
                yield rel, path


def format_string_at(src: str, start: int) -> tuple[str, int]:
    """Join the `+`-concatenated string literals that open a format call.

    Returns (decoded format string, line number of the call). Stops at the
    first token that is not a literal, a `+`, or whitespace — which is where
    the format string ends and the arguments begin.
    """
    i = start
    parts: list[str] = []
    while i < len(src):
        while i < len(src) and src[i] in " \t\r\n":
            i += 1
        m = STRING_LITERAL.match(src, i)
        if not m:
            break
        raw = m.group(1)
        if raw is not None:
            # Interpreted literal: unescape just enough that \" and \\ do not
            # confuse the verb and marker searches.
            parts.append(raw.replace('\\"', '"').replace("\\\\", "\\"))
        else:
            parts.append(m.group(2))
        i = m.end()
        j = i
        while j < len(src) and src[j] in " \t\r\n":
            j += 1
        if j < len(src) and src[j] == "+":
            i = j + 1
            continue
        break
    return "".join(parts), src.count("\n", 0, start) + 1


def scan(root: str) -> list[tuple[str, int, str]]:
    findings = []
    for rel, path in go_files(root):
        with open(path, encoding="utf-8", errors="replace") as handle:
            src = handle.read()
        for call in FORMAT_CALL.finditer(src):
            fmt_str, line = format_string_at(src, call.end())
            if not fmt_str or not QUOTED_VERB.search(fmt_str):
                continue
            if POWERSHELL_MARKERS.search(fmt_str):
                continue
            if not SHELL_MARKERS.search(fmt_str):
                continue
            findings.append((rel, line, " ".join(fmt_str.split())[:100]))
    return findings


def main() -> int:
    root = sys.argv[1] if len(sys.argv) > 1 else "."
    findings = scan(root)
    for rel, line, excerpt in findings:
        print(
            f"::error file={rel},line={line}::value interpolated into hand-written "
            f"shell quotes — an apostrophe in the value ends the argument and the "
            f"rest is parsed as shell. Use safetext.ShellSingleQuote, which supplies "
            f"the quotes itself, and drop the literal quotes from the format string. "
            f"[{excerpt}]",
            file=sys.stderr,
        )
    if findings:
        print(f"shell-quoting-check: {len(findings)} violation(s)", file=sys.stderr)
        return 1
    print("shell-quoting-check: clean")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
