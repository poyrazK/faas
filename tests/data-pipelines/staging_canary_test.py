import json
import io
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from datetime import datetime, timedelta, timezone
import urllib.error
from unittest.mock import patch

import staging_canary as canary


class StagingCanaryTests(unittest.TestCase):
    def test_capacity_requires_both_fresh_reserved_endpoints(self):
        now = datetime.now(timezone.utc)
        endpoint = {"state": "ready", "required_connections": 0, "reserved_connections": 5,
                    "usable_connections": 100, "observed_at": now.isoformat(),
                    "expires_at": (now + timedelta(seconds=30)).isoformat()}
        pipeline = {"capacity": {"state": "ready", "source": dict(endpoint), "destination": dict(endpoint)}}
        canary.Canary.require_capacity(pipeline)
        for field, value in (("expires_at", now.isoformat()), ("required_connections", 5),
                             ("reserved_connections", 101), ("state", "needs_qualification")):
            invalid = json.loads(json.dumps(pipeline))
            invalid["capacity"]["destination"][field] = value
            with self.subTest(field=field), self.assertRaises(canary.CanaryFailure):
                canary.Canary.require_capacity(invalid)

    def test_refuses_non_staging_without_remote_actions(self):
        args = type("Args", (), {"timeout": 1})()
        with patch.dict(os.environ, {"FAAS_ENVIRONMENT": "production"}), patch("subprocess.run") as remote:
            with self.assertRaisesRegex(canary.CanaryFailure, "staging_environment_required"):
                canary.Canary(args).run()
            remote.assert_not_called()

    def test_credentials_use_private_files_and_process_environment(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "source"
            path.write_text("postgres://reader:private-sentinel@db.example/source?sslmode=verify-full")
            path.chmod(0o644)
            with self.assertRaisesRegex(canary.CanaryFailure, "private_file_permissions"):
                canary.private_file(str(path), 8192)
            path.chmod(0o600)
            raw = canary.private_file(str(path), 8192)
            env = canary.source_environment(raw)
            fixture = canary.Canary(type("Args", (), {"timeout": 1})())
            fixture.source_env = env
            with patch("subprocess.run", return_value=subprocess.CompletedProcess([], 0, "t\n", "")) as command:
                self.assertEqual(fixture.sql("SELECT true;"), "t")
                invocation = command.call_args
                self.assertNotIn("private-sentinel", json.dumps(invocation.args))
                self.assertEqual(invocation.kwargs["env"]["PGPASSWORD"], "private-sentinel")
                self.assertEqual(invocation.kwargs["env"]["PGSSLMODE"], "verify-full")

    def test_cannot_qualify_stale_or_wrong_revision_observations(self):
        for result in ({}, {"state": "live", "observation_fresh": True}, {"state": "live", "observation_fresh": False, "revision": 1, "observed_revision": 1},
                       {"state": "live", "observation_fresh": True, "revision": 2, "observed_revision": 1}):
            with self.subTest(result=result), self.assertRaises(canary.CanaryFailure):
                canary.Canary.require_state(result, "live")

    def test_tls_and_redirects_cannot_forward_canary_tokens(self):
        for url in ("http://api.example", "https://u:p@api.example", "https://api.example?token=secret"):
            with self.subTest(url=url), self.assertRaises(canary.CanaryFailure):
                canary.https_url(url)
        self.assertIsNone(canary.NoRedirect().redirect_request(None, None, 302, None, None, "https://elsewhere.example"))

    def test_remote_failure_does_not_disclose_stderr(self):
        fixture = canary.Canary(type("Args", (), {"timeout": 1})())
        with patch("subprocess.run", return_value=subprocess.CompletedProcess([], 1, "", "postgres://u:private-sentinel@source")):
            with self.assertRaises(canary.CanaryFailure) as error:
                fixture.cli("status", "id")
        self.assertEqual(str(error.exception), "cli_operation_failed")

    def test_read_only_problem_body_survives_http_error_handling(self):
        fixture = canary.Canary(type("Args", (), {"timeout": 1})())
        fixture.api_url = "https://canary.example"
        body = io.BytesIO(b'{"code":"read_only_identity","status":403}')
        response = urllib.error.HTTPError(fixture.api_url, 403, "Forbidden", {}, body)
        with patch.object(fixture.opener, "open", side_effect=response):
            status, problem = fixture.http("private-canary-token", "PATCH")
        self.assertEqual(status, 403)
        self.assertEqual(problem["code"], "read_only_identity")
        self.assertTrue(body.closed)


if __name__ == "__main__":
    unittest.main()
