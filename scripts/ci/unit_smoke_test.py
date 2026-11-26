#!/usr/bin/env python3
"""Unit tests for unit_smoke.py against a fake API."""
import importlib.util
import json
import pathlib
import unittest

HERE = pathlib.Path(__file__).parent
spec = importlib.util.spec_from_file_location("unit_smoke", HERE / "unit_smoke.py")
us = importlib.util.module_from_spec(spec)
spec.loader.exec_module(us)


class FakeClient:
    def __init__(self, serve_after=1, usage=None, account=None, restart=202):
        self.calls, self.env, self.gets, self.serve_after = [], {}, 0, serve_after
        self.usage = usage if usage is not None else {"gb_hours": 1.5, "apps": [{"gb_seconds": 10}]}
        self.account = account if account is not None else {"id": "a" * 32, "plan": "pro", "status": "active"}
        self.restart = restart

    def request(self, method, path, body=None, auth=True, url=None):
        self.calls.append((method, url or path))
        if url:
            if not auth:
                # The smoke app is private: the gateway wants the key.
                return 401, '{"code":"unauthorized"}'
            self.gets += 1
            if self.gets <= self.serve_after:
                return 503, "waking"
            return 200, f"smoke:{self.env.get('SMOKE_TOKEN', '')}"
        if path == "/v1/account":
            return 200, json.dumps(self.account)
        if path.startswith("/v1/apps/") and path.endswith("/env/SMOKE_TOKEN"):
            self.env["SMOKE_TOKEN"] = body["value"]
            return 200, "{}"
        if path.endswith("/restart?fresh=true"):
            return self.restart, "{}"
        if path.startswith("/v1/apps/"):
            return 200, "{}"
        if path == "/v1/account/usage":
            return 200, json.dumps(self.usage)
        return 404, "no"


class Clock:
    def __init__(self):
        self.t = 0.0

    def now(self):
        return self.t

    def sleep(self, s):
        self.t += s


def run(client, wait=30):
    clock = Clock()
    return us.run(client, "unit-smoke", "gregale.app", "tok123", wait, sleep=clock.sleep, now=clock.now)


class SmokeTest(unittest.TestCase):
    def test_every_check_passes(self):
        c = FakeClient()
        ok, rows = run(c)
        self.assertTrue(ok, rows)
        self.assertEqual([r[0] for r in rows], ["account", "app", "deploy", "usage"])
        self.assertIn(("GET", "https://unit-smoke.gregale.app/"), c.calls)
        self.assertEqual(c.env["SMOKE_TOKEN"], "tok123")

    def test_an_app_that_never_serves_the_new_configuration_fails(self):
        ok, rows = run(FakeClient(serve_after=10**6), wait=10)
        self.assertFalse(ok)
        self.assertEqual(dict((r[0], r[1]) for r in rows)["deploy"], "FAIL")

    def test_negative_usage_or_a_bad_account_or_restart_fail(self):
        for kw, check in (({"usage": {"gb_hours": -1}}, "usage"), ({"account": {}}, "account"),
                          ({"account": {"id": "a" * 32, "plan": "pro", "status": "suspended"}}, "account"),
                          ({"account": {"id": "a" * 32, "plan": "gold", "status": "active"}}, "account"),
                          ({"restart": 500}, "deploy")):
            with self.subTest(check=check):
                ok, rows = run(FakeClient(**kw))
                self.assertFalse(ok)
                self.assertEqual(dict((r[0], r[1]) for r in rows)[check], "FAIL")

    def test_non_negative_numbers(self):
        self.assertTrue(us.non_negative_numbers({"a": [0, 1.5, {"b": True}], "c": "x"}))
        self.assertFalse(us.non_negative_numbers({"a": [{"b": -0.01}]}))

    def test_usage_error_without_key(self):
        self.assertEqual(us.main(["--app", "x"]), 2)


if __name__ == "__main__":
    unittest.main()
