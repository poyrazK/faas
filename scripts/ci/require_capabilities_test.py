#!/usr/bin/env python3
"""Tests for require-capabilities.py (ADR-20261023115741)."""
import importlib.util
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import re
import shlex
import threading
import unittest

HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("require_capabilities", HERE / "require-capabilities.py")
rc = importlib.util.module_from_spec(spec)
spec.loader.exec_module(rc)

PARENTS = {"projects": "", "environments": "projects", "dev-bridge": "", "apps": ""}


def serve(hidden):
    """Start a fake apid that hides the given capabilities from token 't'."""
    class Handler(BaseHTTPRequestHandler):
        def do_GET(self):
            if self.headers.get("Authorization") != "Bearer t":
                self.send_response(401)
                self.end_headers()
                return
            body = {"/v1/capabilities": {"plan": "pro", "hidden_capabilities": hidden},
                    "/v1/account": {"id": "acct-1"}}.get(self.path)
            self.send_response(200 if body is not None else 404)
            self.end_headers()
            self.wfile.write(json.dumps(body or {}).encode())

        def log_message(self, *_):
            pass

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    return server, f"http://127.0.0.1:{server.server_port}"


class RequireCapabilitiesTest(unittest.TestCase):
    def run_check(self, hidden, required):
        server, api = serve(hidden)
        try:
            return rc.check(api, "t", required, PARENTS)
        finally:
            server.shutdown()

    def test_released_capabilities_pass(self):
        self.assertEqual(self.run_check([], ["dev-bridge", "environments"]), [])

    def test_hidden_capability_and_its_parent_name_their_allow_commands(self):
        self.assertEqual(self.run_check(["projects", "environments", "apps"], ["environments", "dev-bridge"]), [
            'gregalectl flags allow --capability projects --account-id acct-1 --reason "native acceptance account" --yes',
            'gregalectl flags allow --capability environments --account-id acct-1 --reason "native acceptance account" --yes',
        ])

    def test_unknown_capability_is_refused(self):
        with self.assertRaises(SystemExit):
            self.run_check([], ["no-such-capability"])

    def test_ancestors_come_first_once(self):
        self.assertEqual(rc.with_ancestors(["environments", "projects"], PARENTS), ["projects", "environments"])

    def test_acceptance_runs_name_catalog_capabilities(self):
        parents = rc.load_parents()
        calls = 0
        for script in sorted(HERE.glob("run-*.sh")):
            for line in script.read_text().splitlines():
                if "require-capabilities.py" not in line:
                    continue
                calls += 1
                words = shlex.split(line)
                words = words[next(i for i, w in enumerate(words) if w.endswith("require-capabilities.py")) + 1:]
                ids = [w for w in words if re.fullmatch(r"[a-z][a-z0-9-]*", w)]
                self.assertTrue(ids, f"{script.name}: no capability ids in {line!r}")
                for capability in ids:
                    self.assertIn(capability, parents, f"{script.name}: {capability} is not a catalog capability")
        self.assertGreaterEqual(calls, 3, "the native acceptance runs no longer check their capabilities")


if __name__ == "__main__":
    unittest.main()
