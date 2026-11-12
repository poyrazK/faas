#!/usr/bin/env python3
from __future__ import annotations

import importlib.util
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path


MODULE_PATH = Path(__file__).with_name("check_live_docs.py")
SPEC = importlib.util.spec_from_file_location("check_live_docs", MODULE_PATH)
assert SPEC and SPEC.loader
CHECK = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CHECK)


ROOT = Path(__file__).resolve().parents[2]


def page(slug: str, extra: str = "") -> str:
    """A prerendered docs page with the site's title and its Markdown copy."""
    return (
        f'<html><head><link rel="alternate" type="text/markdown" href="/docs/{slug}.md" /></head>'
        f"<h1>Site title for {slug}</h1>{extra}</html>"
    )


def copy(source: str) -> str:
    """The site's Markdown copy: its own title, then the source body."""
    body = (ROOT / source).read_text().split("\n", 1)[1]
    return "# Site title\n\n> Summary.\n" + body


class Handler(BaseHTTPRequestHandler):
    homepage_fallback = False

    routes = {
        "/docs/security": page("security"),
        "/docs/security.md": copy("docs/security.md"),
        # crons serves another document's copy.
        "/docs/crons": page("crons"),
        "/docs/crons.md": copy("docs/security.md"),
        # jobs renders without a Markdown copy.
        "/docs/jobs": "<html><h1>Jobs</h1></html>",
        "/docs/preview-environments": page("preview-environments", "<code>pr-{N}.{slug}.apps</code>"),
        "/docs/preview-environments.md": copy("docs/preview-environments.md"),
    }

    def do_GET(self) -> None:
        if self.path in self.routes:
            body = self.routes[self.path]
            self.send_response(200)
        elif self.homepage_fallback:
            body = "<html><h1>Serverless on real microVMs</h1></html>"
            self.send_response(200)
        else:
            body = "missing"
            self.send_response(404)
        self.end_headers()
        self.wfile.write(body.encode())

    def log_message(self, format: str, *args: object) -> None:
        pass


class LiveDocsTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        cls.thread = threading.Thread(target=cls.server.serve_forever, daemon=True)
        cls.thread.start()
        cls.base = f"http://127.0.0.1:{cls.server.server_port}/docs"

    @classmethod
    def tearDownClass(cls) -> None:
        cls.server.shutdown()
        cls.server.server_close()

    def test_matching_document_and_real_404_pass(self) -> None:
        Handler.homepage_fallback = False
        self.assertEqual(CHECK.check(self.base, {"security"}), [])

    def test_soft_404_homepage_fallback_fails(self) -> None:
        Handler.homepage_fallback = True
        failures = CHECK.check(self.base, {"security"})
        self.assertTrue(any("unknown docs route: HTTP 200" in item for item in failures), failures)

    def test_wrong_document_fails_even_with_200(self) -> None:
        Handler.homepage_fallback = False
        failures = CHECK.check(self.base, {"crons"})
        self.assertTrue(any("lacks the opening of docs/crons.md" in item for item in failures), failures)

    def test_page_without_markdown_copy_fails(self) -> None:
        Handler.homepage_fallback = False
        failures = CHECK.check(self.base, {"jobs"})
        self.assertTrue(any("links no Markdown copy" in item for item in failures), failures)

    def test_identity_ignores_link_targets_and_generated_comments(self) -> None:
        # The site rewrites relative links to absolute ones in its copy.
        self.assertIn(
            CHECK.source_identity(ROOT / "docs/handler-api.md"),
            CHECK.markdown_text(
                "# Handler API\n\nEverything a Node or Python function handler receives and can use, on one\n"
                "page. For deploying and configuring functions see [functions](https://gregale.dev/docs/functions.md);"
                " for runtime details see [node24](https://gregale.dev/docs/runtime-node.md) and "
                "[python313](https://gregale.dev/docs/runtime-python.md)."
            ),
        )
        # plans.md opens with a GENERATED comment; the identity skips it.
        self.assertFalse(CHECK.source_identity(ROOT / "docs/plans.md").startswith("<!--"))

    def test_unpublished_route_is_not_fetched(self) -> None:
        # object-storage documents a preview capability; the catalog marks
        # it unpublished, so its 404 here is expected (#5709).
        Handler.homepage_fallback = False
        self.assertEqual(CHECK.check(self.base, {"object-storage"}), [])

    def test_preview_url_shape_drift_fails(self) -> None:
        Handler.homepage_fallback = False
        failures = CHECK.check(self.base, {"preview-environments"})
        self.assertTrue(any("missing contract text 'pr-{N}-{slug}'" in item for item in failures), failures)


if __name__ == "__main__":
    unittest.main()
