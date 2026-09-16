#!/usr/bin/env python3
from __future__ import annotations

import importlib.util
import io
import json
import os
import tempfile
import unittest
from pathlib import Path
from unittest import mock


MODULE_PATH = Path(__file__).with_name("faas-rclone-backup-identity.py")
SPEC = importlib.util.spec_from_file_location("faas_rclone_backup_identity", MODULE_PATH)
assert SPEC and SPEC.loader
HELPER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(HELPER)


class Response(io.BytesIO):
    def __enter__(self):
        return self

    def __exit__(self, *_args):
        self.close()


class BackupIdentityTest(unittest.TestCase):
    def test_non_gcs_config_passes_through(self) -> None:
        with tempfile.NamedTemporaryFile("w", delete=False) as config:
            config.write("[offhostbox]\ntype = sftp\n")
        try:
            with mock.patch.object(HELPER.os, "execvp", side_effect=OSError("exec")) as execute:
                with mock.patch.object(HELPER.sys, "argv", [str(MODULE_PATH), "lsf", f"--config={config.name}"]):
                    with self.assertRaisesRegex(OSError, "exec"):
                        HELPER.main()
            execute.assert_called_once_with("rclone", ["rclone", "lsf", f"--config={config.name}"])
        finally:
            os.unlink(config.name)

    def test_gcs_config_injects_impersonated_token(self) -> None:
        with tempfile.NamedTemporaryFile("w", delete=False) as config:
            config.write("[gregale_gcs]\ntype = google cloud storage\nenv_auth = true\n")
        replies = [
            Response(b"project-test1"),
            Response(json.dumps({"access_token": "caller"}).encode()),
            Response(json.dumps({"accessToken": "writer", "expireTime": "2026-09-14T07:00:00Z"}).encode()),
        ]
        token_var = "RCLONE_CONFIG_GREGALE_GCS_TOKEN"
        auth_var = "RCLONE_CONFIG_GREGALE_GCS_ENV_AUTH"
        try:
            with mock.patch.object(HELPER.urllib.request, "urlopen", side_effect=replies), \
                    mock.patch.object(HELPER.os, "execvp", side_effect=OSError("exec")):
                with mock.patch.object(HELPER.sys, "argv", [str(MODULE_PATH), "lsf", f"--config={config.name}"]):
                    with self.assertRaisesRegex(OSError, "exec"):
                        HELPER.main()
            self.assertEqual(json.loads(os.environ[token_var])["access_token"], "writer")
            self.assertEqual(os.environ[auth_var], "false")
        finally:
            os.environ.pop(token_var, None)
            os.environ.pop(auth_var, None)
            os.unlink(config.name)


if __name__ == "__main__":
    unittest.main()
