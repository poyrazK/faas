import asyncio
import json
import tempfile
import unittest
from pathlib import Path

import server
from server import add, greet, is_exact_origin, load_config, origin_allowed


class MCPStarterTests(unittest.TestCase):
    def test_sample_tools(self):
        self.assertEqual(greet("Ada"), "Hello, Ada!")
        self.assertEqual(add(2.5, 3), 5.5)
        with self.assertRaises(ValueError):
            greet("  ")

    def test_origin_policy_is_exact(self):
        self.assertTrue(origin_allowed("", []))
        self.assertTrue(origin_allowed("https://trusted.example", ["https://trusted.example"]))
        self.assertFalse(origin_allowed("https://evil.example", []))
        self.assertFalse(origin_allowed("https://trusted.example.attacker.invalid", ["https://trusted.example"]))
        self.assertTrue(is_exact_origin("https://trusted.example"))
        self.assertFalse(is_exact_origin("https://trusted.example/path"))
        self.assertFalse(is_exact_origin("https://trusted.example:bad"))
        self.assertFalse(is_exact_origin("https://trusted.example:"))
        self.assertFalse(is_exact_origin("https://trusted.example\n.evil"))

    def test_config_accepts_the_starter_contract(self):
        config = {
            "version": 1,
            "endpoint": "/mcp",
            "transport": "streamable-http",
            "mode": "stateless",
            "allowed_origins": [],
            "auth": {"mode": "open"},
        }
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "gregale-mcp.json"
            path.write_text(json.dumps(config), encoding="utf-8")
            self.assertEqual(load_config(path), config)

    def test_external_oauth_requires_complete_configuration(self):
        config = {
            "version": 1,
            "endpoint": "/mcp",
            "transport": "streamable-http",
            "mode": "stateless",
            "allowed_origins": [],
            "auth": {"mode": "external-oauth"},
        }
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "gregale-mcp.json"
            path.write_text(json.dumps(config), encoding="utf-8")
            with self.assertRaisesRegex(RuntimeError, "auth.issuer"):
                load_config(path)

    def test_legacy_and_health_route_collision_fail_closed(self):
        for change in (
            {"legacy": True},
            {"endpoint": "/healthz"},
        ):
            config = {
                "version": 1,
                "endpoint": "/mcp",
                "transport": "streamable-http",
                "mode": "stateless",
                "allowed_origins": [],
                "auth": {"mode": "open"},
                **change,
            }
            with tempfile.TemporaryDirectory() as directory:
                path = Path(directory) / "gregale-mcp.json"
                path.write_text(json.dumps(config), encoding="utf-8")
                with self.assertRaises(RuntimeError):
                    load_config(path)

    def test_non_object_config_fails_closed(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "gregale-mcp.json"
            path.write_text("[]", encoding="utf-8")
            with self.assertRaisesRegex(RuntimeError, "one object"):
                load_config(path)

    def test_health_route_and_origin_guard(self):
        async def request(headers):
            messages = []
            delivered = False

            async def receive():
                nonlocal delivered
                if not delivered:
                    delivered = True
                    return {"type": "http.request", "body": b"", "more_body": False}
                return {"type": "http.disconnect"}

            async def send(message):
                messages.append(message)

            path = "/healthz"
            scope = {
                "type": "http",
                "asgi": {"version": "3.0", "spec_version": "2.3"},
                "http_version": "1.1",
                "method": "GET",
                "scheme": "http",
                "path": path,
                "raw_path": path.encode(),
                "query_string": b"",
                "root_path": "",
                "headers": headers,
                "client": ("127.0.0.1", 1),
                "server": ("127.0.0.1", 8080),
            }
            await server.app(scope, receive, send)
            return messages

        health = asyncio.run(request([]))
        self.assertEqual(health[0]["status"], 200)
        self.assertEqual(health[1]["body"], b'{"ok":true}')
        blocked = asyncio.run(request([(b"origin", b"https://untrusted.example")]))
        self.assertEqual(blocked[0]["status"], 403)


if __name__ == "__main__":
    unittest.main()
