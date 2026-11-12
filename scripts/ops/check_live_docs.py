#!/usr/bin/env python3
"""Verify deployed documentation by content, including the unknown-route 404.

The site titles pages from its own table of contents and serves a Markdown
copy of each one (``<link rel="alternate" type="text/markdown">``) with its
own title in place of the source's level-one heading. A route passes when that
copy carries the opening paragraph of the route's source, so the check proves
the page is the right document without depending on the site's titles.
"""

from __future__ import annotations

import argparse
import html.parser
import json
import re
import sys
import urllib.error
import urllib.request
from pathlib import Path
from urllib.parse import urljoin


ROOT = Path(__file__).resolve().parents[2]
CATALOG = ROOT / "docs/customer-pages.json"


def normalize(value: str) -> str:
    return " ".join(value.split()).strip().casefold()


MARKDOWN_LINK = re.compile(r"\[([^\]]*)\]\([^)]*\)")

# Enough of the opening paragraph to identify a document; the site rewrites
# link targets in its copy, so links are reduced to their text on both sides.
IDENTITY_CHARS = 160


def markdown_text(value: str) -> str:
    return normalize(MARKDOWN_LINK.sub(r"\1", value))


def source_identity(path: Path) -> str:
    """The source's first paragraph after its level-one heading."""
    lines = path.read_text().splitlines()
    start = next((i for i, line in enumerate(lines) if line.startswith("# ")), None)
    if start is None:
        raise ValueError(f"{path}: source has no level-one heading")
    paragraph: list[str] = []
    in_comment = False
    for line in lines[start + 1 :]:
        text = line.strip()
        if in_comment:
            in_comment = "-->" not in text
            continue
        if text.startswith("<!--"):
            in_comment = "-->" not in text
            continue
        if not text or text.startswith("#"):
            if paragraph:
                break
            continue
        paragraph.append(text)
    if not paragraph:
        raise ValueError(f"{path}: source has no paragraph after its heading")
    return markdown_text(" ".join(paragraph))[:IDENTITY_CHARS]


class MarkdownAlternate(html.parser.HTMLParser):
    def __init__(self) -> None:
        super().__init__()
        self.href: str | None = None

    def handle_starttag(self, tag: str, attrs: list[tuple[str, str | None]]) -> None:
        values = dict(attrs)
        if (
            tag.lower() == "link"
            and self.href is None
            and values.get("rel") == "alternate"
            and values.get("type") == "text/markdown"
        ):
            self.href = values.get("href")


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


class PageText(html.parser.HTMLParser):
    def __init__(self) -> None:
        super().__init__()
        self.parts: list[str] = []

    def handle_data(self, data: str) -> None:
        self.parts.append(data)

    @property
    def text(self) -> str:
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
        # An unpublished route is deliberately not served (#5709); its reason
        # is in the catalog.
        if route.get("external") or route.get("unpublished") or (selected is not None and route["path"] not in selected):
            continue
        name = route["path"] or "/"
        url = f"{base_url.rstrip('/')}/{route['path']}".rstrip("/") or base_url
        status, body = get(url)
        if status != 200:
            failures.append(f"{name}: HTTP {status}, expected 200")
        else:
            failures.extend(check_document(name, url, body, ROOT / route["source"]))
        if status == 200:
            page = PageText()
            page.feed(body)
            for fragment in route.get("required_text", []):
                if normalize(fragment) not in page.text:
                    failures.append(f"{route['path'] or '/'}: missing contract text {fragment!r}")

    missing_url = f"{base_url.rstrip('/')}/__gregale_missing_contract_probe__"
    missing_status, _ = get(missing_url)
    if missing_status != 404:
        failures.append(f"unknown docs route: HTTP {missing_status}, expected 404")
    return failures


def check_document(name: str, url: str, body: str, source: Path) -> list[str]:
    """Check that the page at url serves source's document."""
    alternate = MarkdownAlternate()
    alternate.feed(body)
    if alternate.href is None:
        # Only the docs home is a landing page rather than a document.
        heading = FirstH1()
        heading.feed(body)
        if name != "/":
            return [f"{name}: page links no Markdown copy (rel=alternate type=text/markdown)"]
        return [] if heading.heading else ["/: docs home has no h1"]
    copy_url = urljoin(url, alternate.href)
    copy_status, copy = get(copy_url)
    if copy_status != 200:
        return [f"{name}: Markdown copy {alternate.href}: HTTP {copy_status}, expected 200"]
    identity = source_identity(source)
    if identity not in markdown_text(copy):
        return [f"{name}: serves {alternate.href}, which lacks the opening of {source.relative_to(ROOT)}: {identity[:60]!r}"]
    return []


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
