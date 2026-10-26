#!/usr/bin/env python3
"""Turn `go test -json` output from tests/journeys into a live progress
feed and a final report.

Reads the event stream on stdin. While tests run it prints one line per
finished journey step; at EOF it prints a summary table, every failure
with its log, and the `metric name=value` lines journeys emit. With
--markdown PATH it also writes the report as Markdown.

Usage:
  go test -json ./tests/journeys | python3 -I tests/journeys/report.py --markdown out/report.md
"""
import argparse
import json
import re
import sys

METRIC_RE = re.compile(r"\bmetric ([a-z0-9_]+)=([0-9.]+)")
WARNING_RE = re.compile(r"\bwarning: (.*)")
FILE_LINE_RE = re.compile(r"^\s+\w+\.go:\d+: ")
ICON = {"pass": "PASS", "fail": "FAIL", "skip": "SKIP"}


class Report:
    def __init__(self):
        self.results = {}  # test name -> (action, elapsed seconds)
        self.output = {}   # test name -> [lines]
        self.order = []
        self.metrics = []
        self.warnings = []
        self.package_failed = False
        self.package_output = []

    def feed(self, event):
        test = event.get("Test")
        action = event.get("Action")
        if not test:
            if action == "output":
                self.package_output.append(event.get("Output", ""))
            if action == "fail":
                self.package_failed = True
            return None
        if test not in self.output:
            self.output[test] = []
            self.order.append(test)
        if action == "output":
            line = event.get("Output", "")
            self.output[test].append(line)
            for name, value in METRIC_RE.findall(line):
                self.metrics.append((test, name, value))
            for text in WARNING_RE.findall(line):
                self.warnings.append((test, text.strip()))
            return None
        if action in ICON:
            self.results[test] = (action, float(event.get("Elapsed", 0)))
            return test
        return None

    def progress_line(self, test):
        action, elapsed = self.results[test]
        line = f"{ICON[action]}  {test}  ({elapsed:.1f}s)"
        if action == "skip":
            reason = self.reason(test)
            if reason:
                line += f"  {reason}"
        return line

    def reason(self, test):
        """The last t.Log/t.Skip/t.Fatal message of a test, without file:line."""
        for line in reversed(self.output.get(test, [])):
            if FILE_LINE_RE.match(line):
                return FILE_LINE_RE.sub("", line).strip()
        return ""

    def leaves(self):
        """Tests with no subtests: the steps a reader cares about."""
        names = [t for t in self.order if t in self.results]
        return [t for t in names if not any(o.startswith(t + "/") for o in names)]

    def counts(self):
        counts = {"pass": 0, "fail": 0, "skip": 0}
        for test in self.leaves():
            counts[self.results[test][0]] += 1
        return counts

    def failed(self):
        return self.package_failed or any(self.results[t][0] == "fail" for t in self.leaves())

    def failure_log(self, test, limit=40):
        lines = [l.rstrip("\n") for l in self.output.get(test, [])
                 if not l.startswith(("=== RUN", "=== PAUSE", "=== CONT", "--- "))]
        return lines[-limit:]

    def text(self):
        out = []
        width = max([len(t) for t in self.leaves()] + [10])
        out.append("")
        out.append(f"{'step'.ljust(width)}  result  seconds")
        for test in self.leaves():
            action, elapsed = self.results[test]
            out.append(f"{test.ljust(width)}  {ICON[action]:6}  {elapsed:7.1f}")
        c = self.counts()
        out.append("")
        out.append(f"{c['pass']} passed, {c['fail']} failed, {c['skip']} skipped")
        if self.metrics:
            out.append("")
            out.append("metrics:")
            for test, name, value in self.metrics:
                out.append(f"  {name}={value}  ({test})")
        if self.warnings:
            out.append("")
            out.append("warnings:")
            for test, text in self.warnings:
                out.append(f"  {text}  ({test})")
        for test in self.leaves():
            if self.results[test][0] == "fail":
                out.append("")
                out.append(f"--- FAIL {test}")
                out.extend(self.failure_log(test))
        if self.package_failed and not any(self.results[t][0] == "fail" for t in self.leaves()):
            out.append("")
            out.append("--- package failed outside any journey:")
            out.extend(l.rstrip("\n") for l in self.package_output[-40:])
        return "\n".join(out) + "\n"

    def markdown(self, header):
        out = ["# Gregale journey report", ""]
        out.extend(f"- {line}" for line in header)
        c = self.counts()
        out += ["", f"**{c['pass']} passed, {c['fail']} failed, {c['skip']} skipped**", "",
                "| Step | Result | Seconds | Note |", "|---|---|---|---|"]
        for test in self.leaves():
            action, elapsed = self.results[test]
            note = self.reason(test) if action != "pass" else ""
            note = note.replace("|", "\\|")[:200]
            out.append(f"| `{test}` | {ICON[action]} | {elapsed:.1f} | {note} |")
        if self.metrics:
            out += ["", "## Metrics", ""]
            out.extend(f"- `{name}` = {value} ({test})" for test, name, value in self.metrics)
        if self.warnings:
            out += ["", "## Warnings", ""]
            out.extend(f"- {text} (`{test}`)" for test, text in self.warnings)
        failures = [t for t in self.leaves() if self.results[t][0] == "fail"]
        if failures:
            out += ["", "## Failures"]
            for test in failures:
                out += ["", f"### {test}", "", "```"]
                out.extend(self.failure_log(test))
                out.append("```")
        return "\n".join(out) + "\n"


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--markdown", help="also write the report as Markdown to this path")
    parser.add_argument("--header", action="append", default=[], help="a line for the report header (repeatable)")
    parser.add_argument("--quiet", action="store_true", help="no live progress lines")
    args = parser.parse_args(argv)

    report = Report()
    for raw in sys.stdin:
        raw = raw.strip()
        if not raw:
            continue
        try:
            event = json.loads(raw)
        except json.JSONDecodeError:
            # go test prints build errors as plain text
            report.package_output.append(raw + "\n")
            report.package_failed = True
            continue
        finished = report.feed(event)
        if finished and not args.quiet and finished in report.leaves():
            print(report.progress_line(finished), flush=True)

    sys.stdout.write(report.text())
    if args.markdown:
        with open(args.markdown, "w") as f:
            f.write(report.markdown(args.header))
    return 1 if report.failed() or not report.results else 0


if __name__ == "__main__":
    sys.exit(main())
