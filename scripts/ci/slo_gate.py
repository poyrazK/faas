#!/usr/bin/env python3
"""Post-deploy SLO gate: watch Prometheus after a unit deploy, exit non-zero
to roll it back (docs/release-compatibility.md, ADR-1344 §6 release safety).

The thresholds AND their evaluation are Prometheus's own: the gate reads the
alerting rules Prometheus has loaded (deploy/ansible/roles/prometheus/files/
*.rules.yml) through /api/v1/rules, including each rule's active alerts and
when each became true (activeAt). It keeps no copy of a threshold and runs no
PromQL of its own beyond one scrape check, so watching a deploy adds one HTTP
request per interval to the control plane's Prometheus.

An alert instance that became true after --deployed-at breaches the gate
once it has stayed true for the rule's `for:`, capped at --max-hold so a
10-minute alert can still stop a 10-minute gate. Instances already true
before the deploy are reported and ignored: rolling back would not fix them.

Selected rules:
  * the platform SLOs, for every unit: 5xx availability, wake latency,
    restore failures (cold-boot fallback), daemon liveness, readiness,
    restart loops and database pool starvation;
  * every page or warn rule whose `component` label is the unit's;
  * and the unit must still be scraped (up == 1 for one of its jobs).

Exit codes, for the deploy workflow:
  0  pass: promote
  1  breach: roll the unit back
  2  usage or configuration error
  3  inconclusive (Prometheus unreachable, a platform rule not loaded):
     no evidence is not a pass; treat as a failure

Usage (from the control-plane runner, where Prometheus listens on loopback):
  scripts/ci/slo_gate.py --unit schedd --deployed-at "$(date +%s)" \\
      --duration 10m --interval 15s
  scripts/ci/slo_gate.py --unit vmmd --deployed-at 0 --list   # print the selected rules
"""
from __future__ import annotations

import argparse
import json
import os
import re
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass, field
from datetime import datetime

PASS, FAIL, USAGE, INCONCLUSIVE = 0, 1, 2, 3

# Evaluated for every unit: a regression in any unit shows up here first.
PLATFORM_ALERTS = (
    "FaasApiAvailabilityLow",            # platform-produced 5xx rate
    "FaasWakeLatencyHigh",               # end-to-end wake p95
    "FaasPlatformWakeLatencySLOBreach",  # platform restore interval p95 (the 350 ms SLO)
    "FaasColdBootFallbackHigh",          # snapshot restores failing over to cold boot
    "FaasDaemonDown",                    # a scrape target stopped answering
    "FaasDaemonNotReady",                # a daemon answers but is not ready
    "FaasRestartLoop",                   # a daemon is crash-looping
    "FaasDBPoolStarved",                 # a daemon cannot get database connections
)

# Deploy units: scrape jobs (prometheus.yml.j2 prom_targets) and the alert
# `component` labels that are the unit's own health signals.
UNITS = {
    "apid": {"jobs": ["apid"], "components": ["apid"]},
    "schedd": {"jobs": ["schedd", "schedd-compute"], "components": ["schedd"]},
    "vmmd": {"jobs": ["vmmd"], "components": ["vmmd"]},
    "gatewayd-public": {"jobs": ["gatewayd"], "components": ["gatewayd-public"]},
    "gatewayd-internal": {"jobs": ["gatewayd-internal"], "components": ["gatewayd-internal"]},
    "imaged": {"jobs": ["imaged"], "components": ["imaged"]},
    "builderd": {"jobs": ["builderd"], "components": ["builderd"]},
    "meterd": {"jobs": ["meterd"], "components": ["meterd"]},
    "realtimed": {"jobs": ["realtimed"], "components": ["realtimed"]},
    "outboundd": {"jobs": ["outboundd"], "components": ["outboundd"]},
    "githubd": {"jobs": ["githubd"], "components": ["githubd"]},
    "s3-gatewayd": {"jobs": ["s3-gatewayd"], "components": []},
    # Schema migrations: the data tier has no daemon of its own to scrape.
    "migrations": {"jobs": [], "components": ["postgres"]},
    # Guest-init, runner and base images: judged by wake and restore SLOs.
    "runtime": {"jobs": [], "components": []},
}

SEVERITIES = ("page", "warn")
SCRAPE_HOLD_SECONDS = 60
MAX_ERROR_ROUNDS = 4


@dataclass
class Rule:
    name: str
    hold: float
    query: str
    summary: str = ""


@dataclass
class Prometheus:
    url: str
    timeout: float = 10.0

    def get(self, path: str, params: dict) -> dict:
        query = urllib.parse.urlencode(params)
        req = urllib.request.Request(f"{self.url.rstrip('/')}{path}?{query}", headers={"Accept": "application/json"})
        try:
            with urllib.request.urlopen(req, timeout=self.timeout) as resp:  # noqa: S310 - operator-supplied URL
                body = json.load(resp)
        except urllib.error.HTTPError as err:
            # Prometheus answers query errors (bad_data, timeout, execution)
            # with a JSON body on a 4xx/5xx status.
            with err:
                try:
                    body = json.load(err)
                except ValueError:
                    body = {"error": f"HTTP {err.code}"}
        if body.get("status") != "success":
            raise RuntimeError(f"{path}: {body.get('errorType', '')} {body.get('error', body)}")
        return body["data"]

    def rules(self) -> list[dict]:
        """Every loaded alerting rule with its health, last evaluation and
        active alerts."""
        groups = self.get("/api/v1/rules", {"type": "alert"})["groups"]
        return [r for g in groups for r in g.get("rules", []) if r.get("type", "alerting") == "alerting"]

    def query(self, expr: str) -> list[dict]:
        data = self.get("/api/v1/query", {"query": expr})
        return data.get("result", []) if data.get("resultType") == "vector" else []


def parse_duration(text: str) -> float:
    m = re.fullmatch(r"(\d+(?:\.\d+)?)(ms|s|m|h)?", text.strip())
    if not m:
        raise argparse.ArgumentTypeError(f"bad duration {text!r} (want e.g. 30s, 10m)")
    return float(m.group(1)) * {"ms": 0.001, "s": 1, None: 1, "m": 60, "h": 3600}[m.group(2)]


def parse_time(text: str | None) -> float | None:
    """Prometheus RFC 3339 (nanosecond fraction, Z or offset) to unix
    seconds; None for the zero time Prometheus uses for "never"."""
    m = re.fullmatch(r"(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d)(?:\.(\d+))?(Z|[+-]\d\d:\d\d)", text or "")
    if not m or m.group(1).startswith("0001-"):
        return None
    zone = "+00:00" if m.group(3) == "Z" else m.group(3)
    return datetime.fromisoformat(m.group(1) + zone).timestamp() + (float("0." + m.group(2)) if m.group(2) else 0.0)


def select_rules(loaded: list[dict], components: list[str], max_hold: float) -> tuple[list[Rule], list[str]]:
    """Pick the platform SLO rules and the unit's own rules; report platform
    rules Prometheus has not loaded."""
    selected, seen = [], set()
    for r in loaded:
        labels = r.get("labels") or {}
        name = r.get("name", "")
        own = labels.get("component") in components and labels.get("severity") in SEVERITIES
        if (name not in PLATFORM_ALERTS and not own) or (name, r.get("query")) in seen:
            continue
        seen.add((name, r.get("query")))
        hold = min(float(r.get("duration") or 0), max_hold)
        selected.append(Rule(name, hold, r.get("query", ""), (r.get("annotations") or {}).get("summary", "")))
    names = {r.name for r in selected}
    return selected, [n for n in PLATFORM_ALERTS if n not in names]


@dataclass
class Gate:
    prom: Prometheus
    rules: list[Rule]
    jobs: list[str]
    duration: float
    interval: float
    deployed_at: float
    max_hold: float
    log: object = print
    now: object = time.time
    sleep: object = time.sleep
    preexisting: set = field(default_factory=set)
    unscraped_since: float | None = None
    blind_rounds: int = 0

    def run(self) -> int:
        start = self.now()
        names = {r.name for r in self.rules}
        while True:
            verdict = self.judge_rules(names)
            if verdict is None and self.jobs:
                verdict = self.judge_scrape()
            if verdict is not None:
                return verdict
            elapsed = self.now() - start
            if elapsed >= self.duration:
                self.log(f"slo-gate: PASS - {len(names)} rules held for {elapsed:.0f}s after the deploy")
                return PASS
            self.sleep(min(self.interval, self.duration - elapsed))

    def judge_rules(self, names: set[str]) -> int | None:
        """One round: fail on a new alert held long enough; go inconclusive
        when the platform rules cannot be read or have stopped evaluating."""
        try:
            loaded = [r for r in self.prom.rules() if r.get("name") in names]
        except (OSError, RuntimeError, ValueError, KeyError) as err:
            return self.blind(f"reading rule state failed: {err}")
        now = self.now()
        blind = set(PLATFORM_ALERTS)
        for r in loaded:
            evaluated = parse_time(r.get("lastEvaluation"))
            if r.get("health") != "err" and evaluated is not None and now - evaluated <= max(3 * self.interval, 120):
                blind.discard(r["name"])
            for alert in r.get("alerts") or []:
                verdict = self.judge_alert(r, alert, evaluated or now)
                if verdict is not None:
                    return verdict
        if blind:
            return self.blind(f"platform rules not evaluating (error, stale or unloaded): {', '.join(sorted(blind))}")
        self.blind_rounds = 0
        return None

    def judge_alert(self, rule: dict, alert: dict, evaluated: float) -> int | None:
        active = parse_time(alert.get("activeAt"))
        if alert.get("state") not in ("pending", "firing") or active is None:
            return None
        labels = alert.get("labels") or {}
        if active < self.deployed_at:
            key = (rule["name"], json.dumps(sorted(labels.items())))
            if key not in self.preexisting:
                self.preexisting.add(key)
                self.log(f"slo-gate: true before the deploy, ignored: {rule['name']} {labels}")
            return None
        hold = min(float(rule.get("duration") or 0), self.max_hold)
        if evaluated - active < hold:
            return None
        summary = (rule.get("annotations") or {}).get("summary") or (alert.get("annotations") or {}).get("summary", "")
        self.log(f"::error::slo-gate: {rule['name']} true for {evaluated - active:.0f}s since the deploy "
                 f"(hold {hold:.0f}s): {summary} {labels}")
        self.log(f"slo-gate: expression: {' '.join(rule.get('query', '').split())}")
        return FAIL

    def judge_scrape(self) -> int | None:
        try:
            up = self.prom.query(f'up{{job=~"{"|".join(self.jobs)}"}} == 1')
        except (OSError, RuntimeError, ValueError) as err:
            return self.blind(f"scrape check failed: {err}")
        if up:
            self.unscraped_since = None
        elif self.unscraped_since is None:
            self.unscraped_since = self.now()
        elif self.now() - self.unscraped_since >= SCRAPE_HOLD_SECONDS:
            self.log(f"::error::slo-gate: no healthy scrape target for {', '.join(self.jobs)} for {SCRAPE_HOLD_SECONDS}s")
            return FAIL
        return None

    def blind(self, why: str) -> int | None:
        self.blind_rounds += 1
        self.log(f"slo-gate: {why}")
        if self.blind_rounds >= MAX_ERROR_ROUNDS:
            self.log(f"::error::slo-gate: blind for {MAX_ERROR_ROUNDS} rounds; inconclusive")
            return INCONCLUSIVE
        return None


def unit_scope(args) -> tuple[list[str], list[str]]:
    cfg = UNITS.get(args.unit, {"jobs": [], "components": []})
    jobs = args.job if args.job is not None else cfg["jobs"]
    components = args.component if args.component is not None else cfg["components"]
    return jobs, components


def main(argv: list[str], prom_factory=Prometheus, now=time.time, sleep=time.sleep, log=print) -> int:
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    parser.add_argument("--unit", required=True, help=f"deploy unit: {', '.join(UNITS)} (others need --job/--component)")
    parser.add_argument("--prometheus-url", default=os.environ.get("PROMETHEUS_URL", "http://127.0.0.1:9090"))
    parser.add_argument("--deployed-at", type=float, required=True,
                        help="unix time the new version started; series already breaching just before it are ignored")
    parser.add_argument("--duration", type=parse_duration, default=parse_duration("10m"))
    parser.add_argument("--interval", type=parse_duration, default=parse_duration("15s"))
    parser.add_argument("--max-hold", type=parse_duration, default=parse_duration("2m"),
                        help="cap on how long a rule must stay true (its for:, at most this)")
    parser.add_argument("--job", action="append", help="scrape job of the unit (repeatable; overrides the unit table)")
    parser.add_argument("--component", action="append", help="alert component label of the unit (repeatable)")
    parser.add_argument("--list", action="store_true", help="print the selected rules and exit")
    try:
        args = parser.parse_args(argv)
    except SystemExit as exc:
        return USAGE if exc.code else PASS
    if args.unit not in UNITS and args.job is None and args.component is None:
        log(f"::error::slo-gate: unknown unit {args.unit!r}; known: {', '.join(UNITS)} (or pass --job/--component)")
        return USAGE
    if args.interval <= 0 or args.duration <= 0:
        log("::error::slo-gate: --interval and --duration must be positive")
        return USAGE

    jobs, components = unit_scope(args)
    if any(not re.fullmatch(r"[A-Za-z0-9_-]+", j) for j in jobs):
        log(f"::error::slo-gate: scrape job names are letters, digits, '_' and '-': {jobs}")
        return USAGE
    prom = prom_factory(args.prometheus_url)
    try:
        rules, missing = select_rules(prom.rules(), components, args.max_hold)
    except (OSError, RuntimeError, ValueError, KeyError) as err:
        log(f"::error::slo-gate: cannot read alert rules from {args.prometheus_url}: {err}")
        return INCONCLUSIVE
    for rule in rules:
        if args.list:
            log(f"{rule.name}\thold={rule.hold:.0f}s\t{' '.join(rule.query.split())}")
    if missing:
        log(f"::error::slo-gate: platform SLO rules not loaded in Prometheus: {', '.join(missing)}")
        return INCONCLUSIVE
    if args.list:
        return PASS
    log(f"slo-gate: unit {args.unit}: {len(rules)} rules, jobs {jobs or '-'}, {args.duration:.0f}s at {args.interval:.0f}s intervals")
    gate = Gate(prom, rules, jobs, args.duration, args.interval, args.deployed_at, args.max_hold, log=log, now=now, sleep=sleep)
    return gate.run()


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
