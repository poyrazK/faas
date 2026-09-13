#!/usr/bin/env python3
from __future__ import annotations

import copy
import importlib.util
import json
import unittest
from pathlib import Path


MODULE_PATH = Path(__file__).with_name("gcp_public_beta_audit.py")
SPEC = importlib.util.spec_from_file_location("gcp_public_beta_audit", MODULE_PATH)
assert SPEC and SPEC.loader
AUDIT = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(AUDIT)
POLICY = json.loads((MODULE_PATH.parents[2] / "deploy/gcp/public-beta-policy.json").read_text())


def instance(name: str, service_account: str, *, running: bool = True) -> dict:
    return {
        "name": name,
        "status": "RUNNING" if running else "TERMINATED",
        "deletionProtection": True,
        "lastStopTimestamp": "2026-09-12T11:30:00Z",
        "metadata": {"items": []},
        "serviceAccounts": [{"email": service_account}],
        "tags": {"items": []},
        "disks": [
            {
                "boot": True,
                "autoDelete": False,
                "source": f"https://compute/zones/europe-west3-b/disks/{name}-boot",
            },
            {
                "boot": False,
                "autoDelete": False,
                "source": f"https://compute/zones/europe-west3-b/disks/{name}-data",
            },
        ],
    }


def healthy_snapshot() -> dict:
    project = POLICY["project_id"]
    compute_sa = POLICY["compute"]["service_account"]
    backup_sa = POLICY["backup"]["writer_service_account"]
    control = instance(POLICY["control_plane"]["instance"], POLICY["control_plane"]["service_account"])
    control["tags"]["items"].append(POLICY["access"]["origin_target_tag"])
    compute = instance("faas-compute-node-1", compute_sa)
    disks = []
    for vm in (control, compute):
        for attached in vm["disks"]:
            name = attached["source"].split("/")[-1]
            disk = {"name": name, "type": "https://compute/diskTypes/pd-ssd"}
            if vm is control and attached["boot"]:
                disk["resourcePolicies"] = [
                    f"https://compute/resourcePolicies/{POLICY['control_plane']['snapshot_schedule']}"
                ]
            disks.append(disk)
    return {
        "active_accounts": [{"account": POLICY["operator_account"], "status": "ACTIVE"}],
        "project": {"projectId": project},
        "billing_project": {"billingEnabled": True, "billingAccountName": "billingAccounts/ABC"},
        "control_plane_dev_only_env": [],
        "project_metadata": {
            "commonInstanceMetadata": {
                "items": [
                    {"key": "enable-oslogin", "value": "TRUE"},
                    {"key": "block-project-ssh-keys", "value": "TRUE"},
                ]
            }
        },
        "instances": [control, compute],
        "disks": disks,
        "firewalls": [
            {
                "name": "iap-ssh",
                "direction": "INGRESS",
                "sourceRanges": ["35.235.240.0/20"],
                "allowed": [{"IPProtocol": "tcp", "ports": ["22"]}],
            },
            {
                "name": POLICY["access"]["origin_firewall_rules"]["ipv4"],
                "direction": "INGRESS",
                "sourceRanges": sorted(AUDIT.pinned_cloudflare_ranges(AUDIT.CLOUDFLARE_V4)),
                "targetTags": [POLICY["access"]["origin_target_tag"]],
                "allowed": [{"IPProtocol": "tcp", "ports": ["80", "443"]}],
            },
            {
                "name": POLICY["access"]["origin_firewall_rules"]["ipv6"],
                "direction": "INGRESS",
                "sourceRanges": sorted(AUDIT.pinned_cloudflare_ranges(AUDIT.CLOUDFLARE_V6)),
                "targetTags": [POLICY["access"]["origin_target_tag"]],
                "allowed": [{"IPProtocol": "tcp", "ports": ["80", "443"]}],
            },
        ],
        "project_iam": {
            "bindings": [
                {
                    "role": "roles/logging.logWriter",
                    "members": [f"serviceAccount:{compute_sa}"],
                }
            ],
            "auditConfigs": [
                {
                    "service": "allServices",
                    "auditLogConfigs": [{"logType": "DATA_READ"}, {"logType": "DATA_WRITE"}],
                }
            ],
        },
        "backup_iam": {
            "bindings": [
                {
                    "role": POLICY["backup"]["writer_role"],
                    "members": [f"serviceAccount:{backup_sa}"],
                }
            ]
        },
        "default_log_bucket": {"retentionDays": POLICY["audit_logs"]["minimum_retention_days"]},
        "alert_policies": [
            {
                "displayName": name,
                "enabled": True,
                "notificationChannels": ["projects/test/notificationChannels/operator"],
            }
            for name in POLICY["logging"]["required_alerts"]
        ],
        "notification_channels": [
            {"name": "projects/test/notificationChannels/operator", "enabled": True, "type": "email"}
        ],
        "budgets": [
            {
                "displayName": "Gregale public beta",
                "thresholdRules": [
                    {"thresholdPercent": 0.5},
                    {"thresholdPercent": 0.8},
                    {"thresholdPercent": 1.0},
                ],
                "allUpdatesRule": {"disableDefaultIamRecipients": False},
            }
        ],
    }


class AuditTest(unittest.TestCase):
    def test_compliant_snapshot_passes(self) -> None:
        self.assertEqual(AUDIT.audit(POLICY, healthy_snapshot()), [])

    def test_reports_customer_risk_findings_together(self) -> None:
        snap = healthy_snapshot()
        control = snap["instances"][0]
        compute = snap["instances"][1]
        control["deletionProtection"] = False
        control["disks"][0]["autoDelete"] = True
        control["tags"]["items"] = []
        compute["serviceAccounts"] = [{"email": "811654175645-compute@developer.gserviceaccount.com"}]
        snap["project_metadata"]["commonInstanceMetadata"]["items"] = []
        snap["firewalls"] = [
            {
                "name": "default-allow-ssh",
                "sourceRanges": ["0.0.0.0/0"],
                "allowed": [{"IPProtocol": "tcp", "ports": ["22"]}],
            }
        ]
        snap["project_iam"]["bindings"] = []
        snap["project_iam"]["auditConfigs"] = []
        snap["backup_iam"]["bindings"].append(
            {
                "role": "roles/storage.objectAdmin",
                "members": [
                    "serviceAccount:811654175645-compute@developer.gserviceaccount.com",
                    f"serviceAccount:{POLICY['backup']['writer_service_account']}",
                ],
            }
        )
        snap["alert_policies"] = []
        snap["budgets"] = {"_error": "permission denied"}

        failures = AUDIT.audit(POLICY, snap)
        joined = "\n".join(failures)
        for expected in (
            "deletion protection is disabled",
            "boot disk is configured to auto-delete",
            "metadata enable-oslogin is not TRUE",
            "default-allow-ssh exposes",
            "missing origin target tag",
            "origin firewall rule is missing",
            "roles/logging.logWriter missing",
            "backup bucket grants access to compute identity",
            "backup writer retains destructive roles/storage.objectAdmin",
            "storage.googleapis.com: audit logs missing",
            "enabled alert policy is missing",
            "billing budgets cannot be audited",
        ):
            self.assertIn(expected, joined)

    def test_stopped_disk_age_and_ipv6_all_ports_fail(self) -> None:
        snap = healthy_snapshot()
        old = instance("faas-compute-node-2", POLICY["compute"]["service_account"], running=False)
        old["lastStopTimestamp"] = "2026-09-10T00:00:00Z"
        snap["instances"].append(old)
        snap["firewalls"].append(
            {"name": "public-all", "sourceRanges": ["::/0"], "allowed": [{"IPProtocol": "all"}]}
        )
        failures = AUDIT.audit(
            POLICY,
            snap,
            now=AUDIT.dt.datetime(2026, 9, 12, 12, 0, tzinfo=AUDIT.dt.timezone.utc),
        )
        joined = "\n".join(failures)
        self.assertIn("stopped for 60.0h", joined)
        self.assertIn("public-all exposes", joined)

    def test_dev_only_control_plane_environment_fails_without_values(self) -> None:
        snap = healthy_snapshot()
        snap["control_plane_dev_only_env"] = ["FAAS_DEV_TOKEN", "FAAS_DEV"]
        failures = AUDIT.audit(POLICY, snap)
        joined = "\n".join(failures)
        self.assertIn("FAAS_DEV, FAAS_DEV_TOKEN", joined)

    def test_unreadable_control_plane_environment_fails_closed(self) -> None:
        snap = healthy_snapshot()
        snap["control_plane_dev_only_env"] = {"_error": "ssh unavailable"}
        self.assertIn(
            "control-plane dev-only environment cannot be audited: ssh unavailable",
            AUDIT.audit(POLICY, snap),
        )

    def test_origin_rule_drift_and_public_bypass_fail(self) -> None:
        snap = healthy_snapshot()
        snap["firewalls"][1]["sourceRanges"] = ["0.0.0.0/0"]
        snap["firewalls"][1]["targetTags"] = []
        failures = "\n".join(AUDIT.audit(POLICY, snap))
        self.assertIn("exposes the HTTP origin publicly", failures)
        self.assertIn("does not match pinned Cloudflare ipv4 ranges", failures)
        self.assertIn("is not limited to target tag", failures)


if __name__ == "__main__":
    unittest.main()
