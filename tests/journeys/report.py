#!/usr/bin/env python3
"""Turn `go test -json` output from tests/journeys into a live progress
feed and a final report.

Reads the event stream on stdin. While tests run it prints one line per
finished journey step; at EOF it prints a summary table, every failure
with its log, the `metric name=value` and `warning:` lines journeys emit,
and, given earlier runs, what changed since the last one.

  --known FILE        known platform bugs (known_failures.json): a listed
                      step that fails is KNOWN and does not fail the run; a
                      listed step that passes is reported as maybe fixed
  --summary FILE      write this run's results as JSON (for history)
  --history-dir DIR   compare with the newest earlier summary.json under DIR
                      and flag flaky steps across the last runs
  --markdown FILE     also write the report as Markdown
  --allowed-skips FILE  skip reasons that may skip a whole journey
                      (allowed_skips.json): a journey skipped for any other
                      reason fails the run, so a green run cannot hide a
                      journey that silently stopped running
  --now YYYY-MM-DD    today's date for known-failure review (default: UTC today)

Every report lists the journeys that did not run and why, and the known
failures to review: entries past their review date (`review_by`, else
`since` + 21 days) and entries whose journey ran without the listed step.

Exits 1 when a step fails that is not a known bug, a journey skips for a
reason --allowed-skips does not list, or nothing ran.

Usage:
  go test -json ./tests/journeys | python3 -I tests/journeys/report.py \\
      --known tests/journeys/known_failures.json --summary out/summary.json \\
      --history-dir .e2e-journeys --markdown out/report.md
"""
import argparse
import datetime
import glob
import json
import os
import re
import sys

METRIC_RE = re.compile(r"\bmetric ([a-z0-9_]+)=([0-9.]+)")
WARNING_RE = re.compile(r"\bwarning: (.*)")
FILE_LINE_RE = re.compile(r"^\s+\w+\.go:\d+: ")
ICON = {"pass": "PASS", "fail": "FAIL", "skip": "SKIP", "known": "KNOWN"}
FLAKY_WINDOW = 5
SLOWER_FACTOR = 1.5
SLOWER_MIN_SECONDS = 30
KNOWN_REVIEW_DAYS = 21
BLOCKED_PREFIX = "blocked: "
TIMEOUT_RE = re.compile(r"panic: test timed out after (\S+)")
RUNNING_RE = re.compile(r"^\t\t(\S+) \(([^)]*)\)$")


class Report:
    def __init__(self, known=None, allowed_skips=None, today=None):
        self.results = {}  # test name -> (action, elapsed seconds)
        self.output = {}   # test name -> [lines]
        self.order = []
        self.metrics = []
        self.warnings = []
        self.package_failed = False
        self.package_output = []
        self.timeout = None      # go test -timeout that killed the run
        self.still_running = []  # (test, elapsed) when it fired
        self._in_running = False
        self.known = {k["step"]: k for k in (known or [])}
        self.allowed_skips = list(allowed_skips or [])
        self.today = today or datetime.datetime.now(datetime.timezone.utc).date()

    def feed(self, event):
        test = event.get("Test")
        action = event.get("Action")
        if action == "output":
            self.scan_timeout(event.get("Output", ""))
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
        if action in ("pass", "fail", "skip"):
            if action == "fail" and test in self.known:
                action = "known"
            self.results[test] = (action, float(event.get("Elapsed", 0)))
            return test
        return None

    def scan_timeout(self, line):
        """go test's timeout panic names the tests still running; it is
        printed under whichever test happened to be current."""
        match = TIMEOUT_RE.search(line)
        if match:
            self.timeout, self._in_running = match.group(1), True
            return
        if self._in_running:
            running = RUNNING_RE.match(line.rstrip("\n"))
            if running:
                self.still_running.append((running.group(1), running.group(2)))
            elif line.strip() and "running tests:" not in line:
                self._in_running = False

    def timed_out_journeys(self):
        return sorted({t.split("/")[0] for t, _ in self.still_running})

    def progress_line(self, test):
        action, elapsed = self.results[test]
        line = f"{ICON[action]}  {test}  ({elapsed:.1f}s)"
        if action == "skip":
            reason = self.reason(test)
            if reason:
                line += f"  {reason}"
        if action == "known":
            line += f"  known bug: {self.known[test]['bug']}"
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
        counts = {"pass": 0, "fail": 0, "skip": 0, "known": 0}
        for test in self.leaves():
            counts[self.results[test][0]] += 1
        return counts

    def new_failures(self):
        return [t for t in self.leaves() if self.results[t][0] == "fail"]

    def maybe_fixed(self):
        """Known bugs whose step passed this run."""
        return [t for t in self.leaves() if t in self.known and self.results[t][0] == "pass"]

    def not_run(self):
        """Skipped steps and journeys that never ran, by reason. A step
        skipped because an earlier step failed ("blocked: ...") is reported
        with its failure, not here."""
        groups = {}
        for test in self.leaves():
            if self.results[test][0] != "skip":
                continue
            reason = self.reason(test) or "no reason given"
            if reason.startswith(BLOCKED_PREFIX):
                continue
            groups.setdefault(reason, []).append(test)
        return sorted(groups.items(), key=lambda kv: (-len(kv[1]), kv[0]))

    def unexpected_skips(self):
        """Not-run tests whose reason matches no allowed skip pattern."""
        if not self.allowed_skips:
            return []
        out = []
        for reason, tests in self.not_run():
            if not any(p["pattern"] in reason for p in self.allowed_skips):
                out.extend((t, reason) for t in tests)
        return out

    def known_to_review(self):
        """Known-failure entries a human should look at again: past their
        review date, or listed for a step this run's journey no longer has."""
        # A journey ran when it reached steps; one skipped whole has none.
        ran = {t.split("/")[0] for t in self.results if "/" in t}
        out = []
        for step, entry in sorted(self.known.items()):
            due = review_date(entry)
            if due is not None and due < self.today:
                out.append((step, f"review date {due.isoformat()} passed"))
            elif step.split("/")[0] in ran and step not in self.results:
                out.append((step, "its journey ran without this step (renamed or removed?)"))
        return out

    def failed(self):
        # A package failure with no failing step is a build error or a
        # crash outside any journey; a known failure alone is not a failure.
        leaf_failures = [t for t in self.leaves() if self.results[t][0] in ("fail", "known")]
        return (bool(self.new_failures()) or bool(self.unexpected_skips()) or bool(self.timeout)
                or (self.package_failed and not leaf_failures))

    def failure_log(self, test, limit=40):
        lines = [l.rstrip("\n") for l in self.output.get(test, [])
                 if not l.startswith(("=== RUN", "=== PAUSE", "=== CONT", "--- "))]
        return lines[-limit:]

    def summary(self, header):
        return {
            "header": header,
            "steps": {t: {"result": self.results[t][0], "seconds": round(self.results[t][1], 1)}
                      for t in self.leaves()},
            "metrics": {name: float(value) for _, name, value in self.metrics},
            "timed_out": self.timed_out_journeys(),
        }

    def text(self, changes=None):
        out = []
        width = max([len(t) for t in self.leaves()] + [10])
        out.append("")
        out.append(f"{'step'.ljust(width)}  result  seconds")
        for test in self.leaves():
            action, elapsed = self.results[test]
            out.append(f"{test.ljust(width)}  {ICON[action]:6}  {elapsed:7.1f}")
        c = self.counts()
        out.append("")
        out.append(f"{c['pass']} passed, {c['fail']} failed, {c['known']} known bugs, {c['skip']} skipped")
        if self.timeout:
            out.append("")
            out.append(f"TIMED OUT after {self.timeout}: {len(self.timed_out_journeys())} journeys never finished, "
                       "their remaining steps are missing above and their apps were not cleaned up:")
            out.extend(f"  {t} (running {d})" for t, d in self.still_running)
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
        known = [t for t in self.leaves() if self.results[t][0] == "known"]
        if known:
            out.append("")
            out.append("known bugs still failing:")
            out.extend(f"  {t}: {self.known[t]['bug']}" for t in known)
        if self.maybe_fixed():
            out.append("")
            out.append("known bugs that passed (remove from known_failures.json once confirmed):")
            out.extend(f"  {t}: {self.known[t]['bug']}" for t in self.maybe_fixed())
        if self.known_to_review():
            out.append("")
            out.append("known failures to review (known_failures.json):")
            out.extend(f"  {t}: {why}" for t, why in self.known_to_review())
        if self.not_run():
            out.append("")
            out.append("not run:")
            for reason, tests in self.not_run():
                out.append(f"  {len(tests)} x {reason}: {', '.join(tests)}")
        if self.unexpected_skips():
            out.append("")
            out.append("unexpected skips (not in allowed_skips.json; these fail the run):")
            out.extend(f"  {t}: {reason}" for t, reason in self.unexpected_skips())
        if changes:
            out.append("")
            out.extend(changes_text(changes))
        for test in self.new_failures():
            out.append("")
            out.append(f"--- FAIL {test}")
            out.extend(self.failure_log(test))
        if self.package_failed and not [t for t in self.leaves() if self.results[t][0] in ("fail", "known")]:
            out.append("")
            out.append("--- package failed outside any journey:")
            out.extend(l.rstrip("\n") for l in self.package_output[-40:])
        return "\n".join(out) + "\n"

    def markdown(self, header, changes=None):
        out = ["# Gregale journey report", ""]
        out.extend(f"- {line}" for line in header)
        c = self.counts()
        out += ["", f"**{c['pass']} passed, {c['fail']} failed, {c['known']} known bugs, {c['skip']} skipped**", ""]
        if self.timeout:
            out += [f"**Timed out after {self.timeout}:** {len(self.timed_out_journeys())} journeys never finished; "
                    "their remaining steps are missing from this report and their apps were not cleaned up.", ""]
            out.extend(f"- `{t}` (running {d})" for t, d in self.still_running)
            out.append("")
        if changes:
            out += ["## Since the last run", ""]
            out.extend(changes_text(changes, bullet="- "))
            out.append("")
        out += ["| Step | Result | Seconds | Note |", "|---|---|---|---|"]
        for test in self.leaves():
            action, elapsed = self.results[test]
            if action == "known":
                note = "known bug: " + self.known[test]["bug"]
            else:
                note = self.reason(test) if action != "pass" else ""
            note = note.replace("|", "\\|")[:200]
            out.append(f"| `{test}` | {ICON[action]} | {elapsed:.1f} | {note} |")
        if self.metrics:
            out += ["", "## Metrics", ""]
            out.extend(f"- `{name}` = {value} ({test})" for test, name, value in self.metrics)
        if self.warnings:
            out += ["", "## Warnings", ""]
            out.extend(f"- {text} (`{test}`)" for test, text in self.warnings)
        if self.maybe_fixed():
            out += ["", "## Known bugs that passed", ""]
            out.extend(f"- `{t}`: {self.known[t]['bug']}" for t in self.maybe_fixed())
        if self.known_to_review():
            out += ["", "## Known failures to review", ""]
            out.extend(f"- `{t}`: {why}" for t, why in self.known_to_review())
        if self.unexpected_skips():
            out += ["", "## Unexpected skips", "",
                    "These reasons are not in `allowed_skips.json`, so they fail the run.", ""]
            out.extend(f"- `{t}`: {reason}" for t, reason in self.unexpected_skips())
        if self.not_run():
            out += ["", "## Not run", ""]
            for reason, tests in self.not_run():
                names = ", ".join(f"`{t}`" for t in tests)
                reason = reason.replace("|", "\\|")
                out.append(f"- **{len(tests)}** × {reason}: {names}")
        if self.new_failures():
            out += ["", "## Failures"]
            for test in self.new_failures():
                out += ["", f"### {test}", "", "```"]
                out.extend(self.failure_log(test))
                out.append("```")
        return "\n".join(out) + "\n"


def review_date(entry):
    """When a known failure is due for review: review_by, else since + 21 days."""
    for key, extra in (("review_by", 0), ("since", KNOWN_REVIEW_DAYS)):
        value = entry.get(key)
        if value:
            try:
                return datetime.date.fromisoformat(value) + datetime.timedelta(days=extra)
            except ValueError:
                return None
    return None


def earlier_summaries(history_dir, exclude):
    """Earlier runs' summaries, newest first (run directories sort by time)."""
    paths = sorted(glob.glob(os.path.join(history_dir, "*", "summary.json")), reverse=True)
    out = []
    for path in paths:
        if exclude and os.path.abspath(path) == os.path.abspath(exclude):
            continue
        try:
            with open(path) as f:
                out.append(json.load(f))
        except (OSError, json.JSONDecodeError):
            continue
    return out


def compare(current, earlier):
    """What changed between this run and the newest earlier one."""
    if not earlier:
        return None
    prev = earlier[0]
    cur_steps, prev_steps = current["steps"], prev.get("steps", {})
    failing = ("fail", "known")
    changes = {"previous": " ".join(prev.get("header", [])[:1]) or "previous run",
               "new_failures": [], "fixed": [], "slower": [], "metrics": [], "flaky": []}
    for step, cur in sorted(cur_steps.items()):
        old = prev_steps.get(step)
        if not old:
            continue
        if cur["result"] in failing and old["result"] == "pass":
            changes["new_failures"].append(step)
        if cur["result"] == "pass" and old["result"] in failing:
            changes["fixed"].append(step)
        if (cur["result"] == "pass" and old["result"] == "pass" and old["seconds"] > 0
                and cur["seconds"] >= old["seconds"] * SLOWER_FACTOR
                and cur["seconds"] - old["seconds"] >= SLOWER_MIN_SECONDS):
            changes["slower"].append((step, old["seconds"], cur["seconds"]))
    for name, value in sorted(current["metrics"].items()):
        if name in prev.get("metrics", {}):
            changes["metrics"].append((name, prev["metrics"][name], value))
    window = [current] + earlier[:FLAKY_WINDOW - 1]
    for step in sorted(cur_steps):
        results = {run.get("steps", {}).get(step, {}).get("result") for run in window}
        if "pass" in results and results & set(failing):
            changes["flaky"].append(step)
    return changes


def changes_text(changes, bullet="  "):
    out = [f"since {changes['previous']}:"]
    if changes["new_failures"]:
        out.append(f"{bullet}newly failing: {', '.join(changes['new_failures'])}")
    if changes["fixed"]:
        out.append(f"{bullet}now passing: {', '.join(changes['fixed'])}")
    for step, old, cur in changes["slower"]:
        out.append(f"{bullet}slower: {step} {old:.0f}s -> {cur:.0f}s")
    for name, old, cur in changes["metrics"]:
        delta = f"{(cur - old) / old * 100:+.0f}%" if old else "n/a"
        out.append(f"{bullet}{name}: {old:g} -> {cur:g} ({delta})")
    if changes["flaky"]:
        out.append(f"{bullet}flaky over the last {FLAKY_WINDOW} runs: {', '.join(changes['flaky'])}")
    if len(out) == 1:
        out.append(f"{bullet}no changes")
    return out


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--markdown", help="also write the report as Markdown to this path")
    parser.add_argument("--header", action="append", default=[], help="a line for the report header (repeatable)")
    parser.add_argument("--quiet", action="store_true", help="no live progress lines")
    parser.add_argument("--known", help="known_failures.json")
    parser.add_argument("--summary", help="write this run's summary JSON here")
    parser.add_argument("--history-dir", help="directory of earlier runs to compare with")
    parser.add_argument("--allowed-skips", help="allowed_skips.json: skip reasons that may skip a journey")
    parser.add_argument("--now", help="today's date (YYYY-MM-DD) for known-failure review")
    args = parser.parse_args(argv)

    known = []
    if args.known:
        with open(args.known) as f:
            known = json.load(f)
    allowed = []
    if args.allowed_skips:
        with open(args.allowed_skips) as f:
            allowed = json.load(f)
    today = datetime.date.fromisoformat(args.now) if args.now else None
    report = Report(known, allowed, today)
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

    summary = report.summary(args.header)
    changes = None
    if args.history_dir:
        changes = compare(summary, earlier_summaries(args.history_dir, args.summary))
    sys.stdout.write(report.text(changes))
    if args.summary:
        with open(args.summary, "w") as f:
            json.dump(summary, f, indent=1)
    if args.markdown:
        with open(args.markdown, "w") as f:
            f.write(report.markdown(args.header, changes))
    return 1 if report.failed() or not report.results else 0


if __name__ == "__main__":
    sys.exit(main())
