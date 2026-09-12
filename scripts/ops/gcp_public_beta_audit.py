#!/usr/bin/env python3
"""Read-only, fail-closed audit of Gregale's public-beta GCP boundary.

The collector deliberately uses gcloud instead of a provider SDK so the same
identity an operator uses for a rollout is also the identity whose access is
verified.  ``--snapshot`` makes the policy engine deterministic and testable.
"""

from __future__ import annotations

import argparse
import datetime as dt
import json
import subprocess
import sys
from pathlib import Path
from typing import Any


ROOT = Path(__file__).resolve().parents[2]
DEFAULT_POLICY = ROOT / "deploy/gcp/public-beta-policy.json"


def gcloud(*args: str, allow_error: bool = False) -> Any:
    command = ["gcloud", *args, "--format=json", "--quiet"]
    proc = subprocess.run(command, text=True, capture_output=True, check=False)
    if proc.returncode:
        if allow_error:
            return {"_error": proc.stderr.strip() or f"exit {proc.returncode}"}
        raise RuntimeError(f"{' '.join(command)}: {proc.stderr.strip()}")
    try:
        return json.loads(proc.stdout or "null")
    except json.JSONDecodeError as exc:
        raise RuntimeError(f"{' '.join(command)} returned invalid JSON: {exc}") from exc


def collect(policy: dict[str, Any]) -> dict[str, Any]:
    project = policy["project_id"]
    bucket = policy["backup"]["bucket"]
    billing = gcloud("billing", "projects", "describe", project, allow_error=True)
    billing_account = ""
    if isinstance(billing, dict):
        billing_account = str(billing.get("billingAccountName", "")).split("/")[-1]

    result = {
        "collected_at": dt.datetime.now(dt.timezone.utc).isoformat(),
        "active_accounts": gcloud("auth", "list", "--filter=status:ACTIVE"),
        "project": gcloud("projects", "describe", project),
        "billing_project": billing,
        "project_metadata": gcloud("compute", "project-info", "describe", "--project", project),
        "instances": gcloud("compute", "instances", "list", "--project", project),
        "disks": gcloud("compute", "disks", "list", "--project", project),
        "firewalls": gcloud("compute", "firewall-rules", "list", "--project", project),
        "project_iam": gcloud("projects", "get-iam-policy", project),
        "backup_iam": gcloud("storage", "buckets", "get-iam-policy", f"gs://{bucket}", allow_error=True),
        "default_log_bucket": gcloud(
            "logging", "buckets", "describe", "_Default", "--location", "global", "--project", project,
            allow_error=True,
        ),
        "alert_policies": gcloud("monitoring", "policies", "list", "--project", project, allow_error=True),
        "notification_channels": gcloud(
            "beta", "monitoring", "channels", "list", "--project", project, allow_error=True
        ),
        "budgets": (
            gcloud("beta", "billing", "budgets", "list", "--billing-account", billing_account, allow_error=True)
            if billing_account
            else {"_error": "project billing account is unavailable"}
        ),
    }
    return result


def metadata_map(resource: dict[str, Any]) -> dict[str, str]:
    return {
        str(item.get("key")): str(item.get("value"))
        for item in resource.get("commonInstanceMetadata", resource.get("metadata", {})).get("items", [])
    }


def role_members(policy: dict[str, Any], role: str) -> set[str]:
    for binding in policy.get("bindings", []):
        if binding.get("role") == role:
            return set(binding.get("members", []))
    return set()


def zone_name(value: str) -> str:
    return value.rstrip("/").split("/")[-1]


def disk_name(value: str) -> str:
    return value.rstrip("/").split("/")[-1]


def parse_timestamp(value: str) -> dt.datetime | None:
    if not value:
        return None
    try:
        return dt.datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError:
        return None


def port_is_public(rule: dict[str, Any], forbidden: set[int]) -> bool:
    if rule.get("disabled") or rule.get("direction", "INGRESS") != "INGRESS":
        return False
    if not ({"0.0.0.0/0", "::/0"} & set(rule.get("sourceRanges", []))):
        return False
    for allowed in rule.get("allowed", []):
        if allowed.get("IPProtocol", allowed.get("ipProtocol", "")).lower() not in {"tcp", "all"}:
            continue
        ports = allowed.get("ports")
        if not ports:
            return True
        for item in ports:
            lo, _, hi = str(item).partition("-")
            low, high = int(lo), int(hi or lo)
            if any(low <= port <= high for port in forbidden):
                return True
    return False


def budget_destinations(budget: dict[str, Any]) -> list[str]:
    rule = budget.get("allUpdatesRule", {})
    out = list(rule.get("monitoringNotificationChannels", []))
    if rule.get("pubsubTopic"):
        out.append(rule["pubsubTopic"])
    if not rule.get("disableDefaultIamRecipients", False):
        out.append("billing-account-IAM-recipients")
    return out


def audit(policy: dict[str, Any], snap: dict[str, Any], now: dt.datetime | None = None) -> list[str]:
    failures: list[str] = []
    project = policy["project_id"]
    now = now or dt.datetime.now(dt.timezone.utc)

    active = [a.get("account") for a in snap.get("active_accounts", []) if a.get("status") == "ACTIVE"]
    if active != [policy["operator_account"]]:
        failures.append(f"active gcloud account is {active or 'none'}, expected only {policy['operator_account']}")
    if snap.get("project", {}).get("projectId") != project:
        failures.append("gcloud returned a different project than the policy")
    if not snap.get("billing_project", {}).get("billingEnabled"):
        failures.append("project billing is disabled or cannot be read")

    instances = {item.get("name"): item for item in snap.get("instances", [])}
    disks = {item.get("name"): item for item in snap.get("disks", [])}
    project_metadata = metadata_map(snap.get("project_metadata", {}))

    def check_instance(item: dict[str, Any] | None, expected: dict[str, Any], role: str) -> None:
        if not item:
            failures.append(f"{role} instance is missing")
            return
        name = item.get("name", role)
        if expected.get("require_deletion_protection") and not item.get("deletionProtection"):
            failures.append(f"{name}: deletion protection is disabled")
        boot = next((d for d in item.get("disks", []) if d.get("boot")), None)
        if expected.get("retain_boot_disk") and (not boot or boot.get("autoDelete", True)):
            failures.append(f"{name}: boot disk is configured to auto-delete")
        if expected.get("require_snapshot_schedule") and boot:
            disk = disks.get(disk_name(str(boot.get("source", ""))), {})
            if not disk.get("resourcePolicies"):
                failures.append(f"{name}: boot disk has no snapshot schedule")
        effective_metadata = dict(project_metadata)
        effective_metadata.update(metadata_map(item))
        for key, wanted in policy["access"]["required_metadata"].items():
            if effective_metadata.get(key, "").upper() != wanted.upper():
                failures.append(f"{name}: metadata {key} is not {wanted}")
        accounts = [sa.get("email") for sa in item.get("serviceAccounts", [])]
        expected_sa = expected.get("service_account")
        if expected_sa and accounts != [expected_sa]:
            failures.append(f"{name}: service account is {accounts or 'missing'}, expected {expected_sa}")

    control = policy["control_plane"]
    check_instance(instances.get(control["instance"]), control, "control-plane")

    compute_policy = policy["compute"]
    compute = [i for name, i in instances.items() if name.startswith(compute_policy["instance_prefix"])]
    running = [i for i in compute if i.get("status") == "RUNNING"]
    if len(running) < compute_policy["minimum_running"]:
        failures.append(f"running compute nodes={len(running)}, require at least {compute_policy['minimum_running']}")
    for item in compute:
        check_instance(item, compute_policy, "compute")
        if item.get("status") == "RUNNING" and compute_policy.get("require_ssd_for_running_nodes"):
            attached = [disks.get(disk_name(str(d.get("source", ""))), {}) for d in item.get("disks", [])]
            types = {zone_name(str(d.get("type", ""))) for d in attached}
            if not types & {"pd-ssd", "hyperdisk-balanced", "hyperdisk-extreme"}:
                failures.append(f"{item['name']}: running compute node has no SSD data disk")
        if item.get("status") == "TERMINATED" and item.get("disks"):
            stopped = parse_timestamp(str(item.get("lastStopTimestamp", "")))
            age = (now - stopped).total_seconds() / 3600 if stopped else float("inf")
            if age > compute_policy["maximum_stopped_hours_with_disks"]:
                failures.append(f"{item['name']}: stopped for {age:.1f}h while retaining {len(item['disks'])} disk(s)")

    forbidden = set(policy["access"]["forbidden_public_tcp_ports"])
    for rule in snap.get("firewalls", []):
        if port_is_public(rule, forbidden):
            failures.append(f"firewall {rule.get('name', '<unnamed>')} exposes an administrative TCP port publicly")

    project_iam = snap.get("project_iam", {})
    compute_members = {f"serviceAccount:{i['serviceAccounts'][0]['email']}" for i in compute if i.get("serviceAccounts")}
    for role in policy["logging"]["required_project_roles"]:
        missing = compute_members - role_members(project_iam, role)
        if missing:
            failures.append(f"{role} missing for {', '.join(sorted(missing))}")

    backup_iam = snap.get("backup_iam", {})
    if backup_iam.get("_error"):
        failures.append(f"backup bucket IAM cannot be audited: {backup_iam['_error']}")
    else:
        all_bucket_members = {member for binding in backup_iam.get("bindings", []) for member in binding.get("members", [])}
        for account in policy["backup"]["forbidden_service_accounts"]:
            if f"serviceAccount:{account}" in all_bucket_members:
                failures.append(f"backup bucket grants access to compute identity {account}")
        writer = f"serviceAccount:{policy['backup']['writer_service_account']}"
        writer_role = policy["backup"]["writer_role"]
        if writer not in role_members(backup_iam, writer_role):
            failures.append(f"backup writer lacks append-only {writer_role}")
        for role in ("roles/storage.objectAdmin", "roles/storage.admin"):
            if writer in role_members(backup_iam, role):
                failures.append(f"backup writer retains destructive {role}")

    audit_configs = {entry.get("service"): entry for entry in project_iam.get("auditConfigs", [])}
    all_services = audit_configs.get("allServices", {})
    for service, wanted_types in policy["audit_logs"]["services"].items():
        entry = audit_configs.get(service, {})
        available = {cfg.get("logType") for cfg in all_services.get("auditLogConfigs", [])}
        available.update(cfg.get("logType") for cfg in entry.get("auditLogConfigs", []))
        missing = set(wanted_types) - available
        if missing:
            failures.append(f"{service}: audit logs missing {', '.join(sorted(missing))}")

    log_bucket = snap.get("default_log_bucket", {})
    if isinstance(log_bucket, dict) and log_bucket.get("_error"):
        failures.append(f"default log retention cannot be audited: {log_bucket['_error']}")
    elif int(log_bucket.get("retentionDays", 0)) < int(policy["audit_logs"]["minimum_retention_days"]):
        failures.append(
            "default log retention is "
            f"{log_bucket.get('retentionDays', 0)} days, expected at least "
            f"{policy['audit_logs']['minimum_retention_days']}"
        )

    alerts = snap.get("alert_policies", {})
    if isinstance(alerts, dict) and alerts.get("_error"):
        failures.append(f"monitoring policies cannot be audited: {alerts['_error']}")
    else:
        enabled_names = {a.get("displayName") for a in alerts if a.get("enabled", True)}
        for name in policy["logging"]["required_alerts"]:
            if name not in enabled_names:
                failures.append(f"enabled alert policy is missing: {name}")
        if policy["logging"].get("require_notification_destination"):
            for alert in alerts:
                if alert.get("displayName") in policy["logging"]["required_alerts"] and not alert.get(
                    "notificationChannels"
                ):
                    failures.append(f"alert policy has no notification destination: {alert.get('displayName')}")

    channels = snap.get("notification_channels", {})
    if isinstance(channels, dict) and channels.get("_error"):
        failures.append(f"notification channels cannot be audited: {channels['_error']}")

    budgets = snap.get("budgets", {})
    if isinstance(budgets, dict) and budgets.get("_error"):
        failures.append(f"billing budgets cannot be audited: {budgets['_error']}")
    else:
        matching = [b for b in budgets if b.get("displayName") == policy["billing"]["budget_display_name"]]
        if not matching:
            failures.append(f"billing budget is missing: {policy['billing']['budget_display_name']}")
        else:
            budget = matching[0]
            thresholds = {float(rule.get("thresholdPercent", -1)) for rule in budget.get("thresholdRules", [])}
            missing = set(policy["billing"]["minimum_thresholds"]) - thresholds
            if missing:
                failures.append(f"billing budget thresholds missing: {', '.join(map(str, sorted(missing)))}")
            if policy["billing"].get("require_notification_destination") and not budget_destinations(budget):
                failures.append("billing budget has no notification destination")

    return failures


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--policy", type=Path, default=DEFAULT_POLICY)
    parser.add_argument("--snapshot", type=Path, help="audit a saved collector snapshot")
    parser.add_argument("--write-snapshot", type=Path, help="write the fresh read-only collector result")
    parser.add_argument("--json", action="store_true", help="emit machine-readable findings")
    args = parser.parse_args()
    policy = json.loads(args.policy.read_text())
    snapshot = json.loads(args.snapshot.read_text()) if args.snapshot else collect(policy)
    if args.write_snapshot:
        args.write_snapshot.write_text(json.dumps(snapshot, indent=2, sort_keys=True) + "\n")
    failures = audit(policy, snapshot)
    if args.json:
        print(json.dumps({"ok": not failures, "findings": failures}, indent=2))
    elif failures:
        print(f"gcp-public-beta-audit: FAIL ({len(failures)} finding(s))", file=sys.stderr)
        for failure in failures:
            print(f"  - {failure}", file=sys.stderr)
    else:
        print("gcp-public-beta-audit: OK")
    return 1 if failures else 0


if __name__ == "__main__":
    raise SystemExit(main())
