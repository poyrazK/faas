#!/usr/bin/env python3
"""Critical smoke checks for a unit release, before it counts as deployed.

docs/adr/20261122170000-continuous-unit-lane.md, "Critical smoke checks".
The SLO gate sees errors and latency; a release can serve a wrong answer
with a 200. These checks drive the business paths through the public API
with a dedicated smoke account's key, right after activation:

  account   GET /v1/account answers 200 with the account's id, an active
            plan and status (sign-in, API keys, the account and plan read
            path billing reads)
  app       GET /v1/apps/{slug} answers 200 for the smoke app
  deploy    PUT /v1/apps/{slug}/env/SMOKE_TOKEN to a value unique to this
            run, POST /v1/apps/{slug}/restart?fresh=true, then the app's
            public URL must answer with that value: apid's write path,
            schedd, vmmd's cold boot, wake and the gateways, end to end,
            without a build
  usage     GET /v1/account/usage answers 200 with only non-negative
            numbers (metering's read model)

The smoke app (tests/unit-smoke-app) answers `smoke:<SMOKE_TOKEN>`.

Exit 0 when every check passed, 1 when one failed (the caller rolls the
release back), 2 on a usage error. Writes a Markdown table to --summary.

Usage:
  UNIT_SMOKE_API_KEY=... scripts/ci/unit_smoke.py --app unit-smoke \\
      [--api https://api.gregale.dev] [--apps-domain gregale.app] [--token RUN-ID]
"""
from __future__ import annotations

import argparse
import json
import os
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

USER_AGENT = "gregale-unit-smoke/1"
PLANS = ("free", "hobby", "pro", "scale")  # AccountResponse.plan


class Client:
    def __init__(self, api: str, key: str, timeout: float = 15.0):
        self.api, self.key, self.timeout = api.rstrip("/"), key, timeout

    def request(self, method: str, path: str, body: dict | None = None, auth: bool = True,
                url: str | None = None) -> tuple[int, str]:
        data = json.dumps(body).encode() if body is not None else None
        headers = {"User-Agent": USER_AGENT, "Accept": "application/json"}
        if body is not None:
            headers["Content-Type"] = "application/json"
        if auth:
            headers["Authorization"] = f"Bearer {self.key}"
        req = urllib.request.Request(url or f"{self.api}{path}", data=data, method=method, headers=headers)
        try:
            with urllib.request.urlopen(req, timeout=self.timeout) as resp:  # noqa: S310 - fixed hosts
                return resp.status, resp.read().decode("utf-8", "replace")
        except urllib.error.HTTPError as err:
            return err.code, err.read().decode("utf-8", "replace")


def non_negative_numbers(value) -> bool:
    if isinstance(value, bool):
        return True
    if isinstance(value, (int, float)):
        return value >= 0
    if isinstance(value, dict):
        return all(non_negative_numbers(v) for v in value.values())
    if isinstance(value, list):
        return all(non_negative_numbers(v) for v in value)
    return True


def check_account(c: Client, app: str, **_) -> str:
    code, body = c.request("GET", "/v1/account")
    if code != 200:
        raise AssertionError(f"GET /v1/account: HTTP {code}: {body[:200]}")
    data = json.loads(body)
    if not data.get("id"):
        raise AssertionError("GET /v1/account: no account id in the answer")
    if data.get("plan") not in PLANS:
        raise AssertionError(f"GET /v1/account: plan {data.get('plan')!r} is none of {', '.join(PLANS)}")
    if data.get("status") != "active":
        raise AssertionError(f"GET /v1/account: the smoke account is {data.get('status')!r}, not active")
    return f"200, plan {data['plan']}, active"


def check_app(c: Client, app: str, **_) -> str:
    code, body = c.request("GET", f"/v1/apps/{urllib.parse.quote(app)}")
    if code != 200:
        raise AssertionError(f"GET /v1/apps/{app}: HTTP {code}: {body[:200]}")
    return "200"


def check_deploy(c: Client, app: str, apps_domain: str, token: str, wait: float, sleep=time.sleep,
                 now=time.monotonic, **_) -> str:
    slug = urllib.parse.quote(app)
    code, body = c.request("PUT", f"/v1/apps/{slug}/env/SMOKE_TOKEN", {"value": token})
    if code not in (200, 201):
        raise AssertionError(f"PUT env SMOKE_TOKEN: HTTP {code}: {body[:200]}")
    code, body = c.request("POST", f"/v1/apps/{slug}/restart?fresh=true", {})
    if code not in (200, 202):
        raise AssertionError(f"POST restart?fresh=true: HTTP {code}: {body[:200]}")
    url = f"https://{app}.{apps_domain}/"
    want = f"smoke:{token}"
    started, last = now(), ""
    while True:
        try:
            # The smoke app stays private (protection by default): the
            # gateway takes the owning account's API key as its bearer.
            code, body = c.request("GET", "", auth=True, url=url)
            last = f"HTTP {code}: {body[:120]!r}"
            if code == 200 and want in body:
                return f"served the new configuration after {now() - started:.0f} s"
        except (OSError, urllib.error.URLError) as err:
            last = str(err)
        if now() - started >= wait:
            raise AssertionError(f"{url} did not serve {want!r} within {wait:.0f} s (last: {last})")
        sleep(2)


def check_usage(c: Client, app: str, **_) -> str:
    code, body = c.request("GET", "/v1/account/usage")
    if code != 200:
        raise AssertionError(f"GET /v1/account/usage: HTTP {code}: {body[:200]}")
    if not non_negative_numbers(json.loads(body)):
        raise AssertionError("GET /v1/account/usage: a negative number in the usage projection")
    return "200, no negative figure"


CHECKS = (("account", check_account), ("app", check_app), ("deploy", check_deploy),
          ("usage", check_usage))


def run(c: Client, app: str, apps_domain: str, token: str, wait: float, checks=CHECKS, **kw) -> tuple[bool, list]:
    rows, ok = [], True
    for name, fn in checks:
        try:
            rows.append((name, "pass", fn(c, app, apps_domain=apps_domain, token=token, wait=wait, **kw)))
        except (AssertionError, OSError, ValueError, urllib.error.URLError) as err:
            ok = False
            rows.append((name, "FAIL", str(err)))
    return ok, rows


def main(argv: list[str]) -> int:
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--app", default=os.environ.get("UNIT_SMOKE_APP", ""))
    ap.add_argument("--api", default=os.environ.get("UNIT_SMOKE_API", "") or "https://api.gregale.dev")
    ap.add_argument("--apps-domain", default=os.environ.get("UNIT_SMOKE_APPS_DOMAIN", "") or "gregale.app")
    ap.add_argument("--token", default="", help="the value the app must serve (default: a fresh one)")
    ap.add_argument("--wait", type=float, default=90.0, help="seconds the app has to serve the new configuration")
    ap.add_argument("--summary", default=os.environ.get("GITHUB_STEP_SUMMARY", ""))
    args = ap.parse_args(argv)
    key = os.environ.get("UNIT_SMOKE_API_KEY", "")
    if not args.app or not key:
        print("unit_smoke: needs --app (UNIT_SMOKE_APP) and UNIT_SMOKE_API_KEY", file=sys.stderr)
        return 2
    token = args.token or uuid.uuid4().hex
    ok, rows = run(Client(args.api, key), args.app, args.apps_domain, token, args.wait)
    table = ["## Critical smoke checks", "", "| check | result | detail |", "| --- | --- | --- |"]
    table += [f"| {n} | {r} | {d.replace('|', '/')} |" for n, r, d in rows]
    text = "\n".join(table) + "\n"
    print(text)
    if args.summary:
        with open(args.summary, "a", encoding="utf-8") as fh:
            fh.write(text)
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
