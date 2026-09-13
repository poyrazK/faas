#!/usr/bin/env python3
"""Verify deployed documentation by content, including the unknown-route 404."""

from __future__ import annotations

import argparse
import html.parser
import json
import re
import sys
import urllib.error
import urllib.request
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
CATALOG = ROOT / "docs/customer-pages.json"


def normalize(value: str) -> str:
    return " ".join(value.split()).strip().casefold()


def source_heading(path: Path) -> str:
    for line in path.read_text().splitlines():
        match = re.match(r"^#\s+(.+?)\s*$", line)
        if match:
            return normalize(re.sub(r"[`*_]", "", match.group(1)))
    raise ValueError(f"{path}: source has no level-one heading")


class FirstH1(html.parser.HTMLParser):
    def __init__(self) -> None:
        super().__init__()
        self.in_h1 = False
        self.parts: list[str] = []
        self.done = False

    def handle_starttag(self, tag: str, attrs: list[tuple[str, str | None]]) -> None:
        if tag.lower() == "h1" and not self.done:
            self.in_h1 = True

    def handle_endtag(self, tag: str) -> None:
        if tag.lower() == "h1" and self.in_h1:
            self.in_h1 = False
            self.done = True

    def handle_data(self, data: str) -> None:
        if self.in_h1:
            self.parts.append(data)

    @property
    def heading(self) -> str:
        return normalize(" ".join(self.parts))


def get(url: str) -> tuple[int, str]:
    request = urllib.request.Request(url, headers={"User-Agent": "gregale-docs-contract/1"})
    try:
        with urllib.request.urlopen(request, timeout=20) as response:
            return response.status, response.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as exc:
        try:
            return exc.code, exc.read().decode("utf-8", "replace")
        finally:
            exc.close()


def check(base_url: str, selected: set[str] | None = None) -> list[str]:
    catalog = json.loads(CATALOG.read_text())
    failures: list[str] = []
    for route in catalog["routes"]:
        if route.get("external") or (selected is not None and route["path"] not in selected):
            continue
        expected = source_heading(ROOT / route["source"])
        url = f"{base_url.rstrip('/')}/{route['path']}".rstrip("/") or base_url
        status, body = get(url)
        parser = FirstH1()
        parser.feed(body)
        if status != 200:
            failures.append(f"{route['path'] or '/'}: HTTP {status}, expected 200")
        elif parser.heading != expected:
            failures.append(
                f"{route['path'] or '/'}: first h1 is {parser.heading!r}, expected {expected!r}"
            )

    missing_url = f"{base_url.rstrip('/')}/__gregale_missing_contract_probe__"
    missing_status, _ = get(missing_url)
    if missing_status != 404:
        failures.append(f"unknown docs route: HTTP {missing_status}, expected 404")
    return failures


def main() -> int:
    catalog = json.loads(CATALOG.read_text())
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", default=catalog["base_url"])
    parser.add_argument("--route", action="append", help="check only this catalog route; repeatable")
    args = parser.parse_args()
    failures = check(args.base_url, set(args.route) if args.route else None)
    if failures:
        print(f"live-docs-check: FAIL ({len(failures)} finding(s))", file=sys.stderr)
        for failure in failures:
            print(f"  - {failure}", file=sys.stderr)
        return 1
    print("live-docs-check: OK")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
