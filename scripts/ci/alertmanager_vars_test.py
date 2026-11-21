#!/usr/bin/env python3
"""Tests for alertmanager_vars.py (ADR-20261121110000)."""

import os
import subprocess
import sys
import tempfile
import json
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import alertmanager_vars as av  # noqa: E402

def top_level(text):
    """Top-level one-line keys; values here are JSON-compatible YAML."""
    out = {}
    for line in text.splitlines():
        if line and not line[0].isspace() and ": " in line:
            key, val = line.split(": ", 1)
            try:
                out[key] = json.loads(val)
            except ValueError:
                out[key] = val
    return out


SCRIPT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "alertmanager_vars.py")


class ParseOverrideTest(unittest.TestCase):
    def test_blank_is_no_change(self):
        self.assertEqual(av.parse_override(""), {})
        self.assertEqual(av.parse_override("  \n"), {})

    def test_rejects(self):
        cases = {
            "not json": "{",
            "not an object": "[]",
            "unknown key": '{"faas_billing_mode": "live"}',
            "file outside secrets": '{"am_slack_webhook_url_file": "/etc/passwd"}',
            "traversal": '{"am_slack_webhook_url_file": "/etc/alertmanager/secrets/../x"}',
            "secrets dir itself": '{"am_heartbeat_url_file": "/etc/alertmanager/secrets/"}',
            "multi-line": '{"am_email_to_page": "a@b\\nc"}',
            "bool as string": '{"am_smtp_require_tls": "true"}',
            "number": '{"am_smtp_smarthost": 587}',
        }
        for name, raw in cases.items():
            with self.subTest(name), self.assertRaises(av.OverrideError):
                av.parse_override(raw)

    def test_accepts_receiver_settings(self):
        got = av.parse_override(
            '{"am_slack_webhook_url_file": "", "am_smtp_password_file": "/etc/alertmanager/secrets/smtp-password",'
            ' "am_smtp_require_tls": true, "am_email_from": "Alerts <a@b.dev>"}')
        self.assertEqual(got["am_slack_webhook_url_file"], "")
        self.assertIs(got["am_smtp_require_tls"], True)


class ApplyTest(unittest.TestCase):
    def test_replaces_and_keeps_the_rest(self):
        text = ("faas_x: 1\n"
                "am_slack_webhook_url_file: /etc/alertmanager/secrets/slack-webhook-url\n"
                "am_heartbeat_url_file: \"/etc/alertmanager/secrets/heartbeat-url\"\n"
                "nested:\n  am_slack_webhook_url_file: untouched\n")
        out = av.apply(text, {"am_slack_webhook_url_file": "",
                              "am_smtp_password_file": "/etc/alertmanager/secrets/smtp-password",
                              "am_smtp_require_tls": True})
        doc = top_level(out)
        self.assertEqual(doc["am_slack_webhook_url_file"], "")
        self.assertEqual(doc["am_smtp_password_file"], "/etc/alertmanager/secrets/smtp-password")
        self.assertIs(doc["am_smtp_require_tls"], True)
        self.assertEqual(doc["am_heartbeat_url_file"], "/etc/alertmanager/secrets/heartbeat-url")
        self.assertIn("nested:\n  am_slack_webhook_url_file: untouched\n", out)
        self.assertEqual(doc["faas_x"], 1)
        self.assertEqual(out.count("am_slack_webhook_url_file:"), 2)  # top level + nested

    def test_refuses_multiline_value(self):
        with self.assertRaises(av.OverrideError):
            av.apply("am_email_to_page: >-\n  a@b.dev\n", {"am_email_to_page": "c@d.dev"})

    def test_no_override_is_identity(self):
        self.assertEqual(av.apply("a: 1", {}), "a: 1")


class MainTest(unittest.TestCase):
    def run_script(self, text, override):
        with tempfile.TemporaryDirectory() as d:
            path = os.path.join(d, "vars.yml")
            with open(path, "w", encoding="utf-8") as f:
                f.write(text)
            env = dict(os.environ)
            env.pop("ALERTMANAGER_VARS_OVERRIDE", None)
            if override is not None:
                env["ALERTMANAGER_VARS_OVERRIDE"] = override
            proc = subprocess.run([sys.executable, "-I", SCRIPT, path], env=env,
                                  capture_output=True, text=True, check=False)
            with open(path, encoding="utf-8") as f:
                return proc, f.read()

    def test_unset_leaves_file_alone(self):
        proc, out = self.run_script("a: 1\n", None)
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertEqual(out, "a: 1\n")

    def test_bad_override_fails_and_leaves_file_alone(self):
        proc, out = self.run_script("a: 1\n", '{"am_slack_webhook_url_file": "/tmp/x"}')
        self.assertEqual(proc.returncode, 1)
        self.assertIn("::error::", proc.stderr)
        self.assertEqual(out, "a: 1\n")

    def test_applies(self):
        proc, out = self.run_script("am_slack_webhook_url_file: /etc/alertmanager/secrets/s\n",
                                    '{"am_slack_webhook_url_file": ""}')
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertEqual(top_level(out), {"am_slack_webhook_url_file": ""})


if __name__ == "__main__":
    unittest.main()
