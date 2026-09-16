#!/usr/bin/env python3
from __future__ import annotations

import importlib.util
import unittest
from pathlib import Path


MODULE_PATH = Path(__file__).with_name("gcp_public_beta_iam.py")
SPEC = importlib.util.spec_from_file_location("gcp_public_beta_iam", MODULE_PATH)
assert SPEC and SPEC.loader
IAM = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(IAM)


class AuditConfigTest(unittest.TestCase):
    def test_adds_required_logs_without_dropping_policy(self) -> None:
        source = {
            "version": 1,
            "etag": "abc",
            "bindings": [{"role": "roles/viewer", "members": ["user:ops@example.com"]}],
            "auditConfigs": [
                {
                    "service": "storage.googleapis.com",
                    "auditLogConfigs": [{"logType": "DATA_READ", "exemptedMembers": ["user:test@example.com"]}],
                }
            ],
        }
        result = IAM.add_audit_logs(source)
        self.assertEqual(result["bindings"][0]["role"], "roles/viewer")
        by_service = {item["service"]: item for item in result["auditConfigs"]}
        self.assertEqual(
            {item["logType"] for item in by_service["storage.googleapis.com"]["auditLogConfigs"]},
            {"DATA_READ", "DATA_WRITE"},
        )
        self.assertEqual(
            by_service["storage.googleapis.com"]["auditLogConfigs"][0]["exemptedMembers"],
            ["user:test@example.com"],
        )
        self.assertEqual(
            {item["logType"] for item in by_service["iam.googleapis.com"]["auditLogConfigs"]},
            {"DATA_READ", "DATA_WRITE"},
        )

    def test_is_idempotent(self) -> None:
        policy = {"bindings": []}
        once = IAM.add_audit_logs(policy)
        twice = IAM.add_audit_logs(once)
        self.assertEqual(once, twice)


if __name__ == "__main__":
    unittest.main()
