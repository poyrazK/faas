#!/usr/bin/env python3
"""Execute explicitly configured staging fault controls without shell expansion.

Install a private owner-executable copy and set PIPELINE_QUALIFICATION_ADAPTER_CONFIG
 to a private JSON manifest. Every command is fixed argv; the scoped request is
passed on stdin. Commands must enforce isolation and return bounded JSON evidence.
There are deliberately no default systemd, network or provider mutation commands.
"""
from __future__ import annotations

import json
import hashlib
import os
import subprocess
import sys
import uuid

from staging_canary import CanaryFailure, private_file
from provider_qualification import FAULTS

ACTIONS = {*FAULTS, *FAULTS.values(), "preflight", "audit_cleanup", "audit_capacity", "data_api_refresh"}
EVIDENCE_FIELDS = {"staging", "isolated_fixture", "source_retention_bytes", "shared_admission_scope",
                   "provider_kind", "target_postgres_version", "target_subscription_count", "pipeline_reservation_count", "fault_resource_count", "reservation_count"}


def execute(request: dict, config: dict) -> dict:
    if os.environ.get("FAAS_ENVIRONMENT") != "staging" or request.get("version") != 1 or config.get("version") != 1:
        raise CanaryFailure("staging_adapter_required")
    for field in ("source_connection_id", "database_id", "admission_database_id", "backend_fingerprint", "data_api_url"):
        if not config.get(field) or request.get(field) != config[field]:
            raise CanaryFailure("adapter_scope_mismatch")
    for field in ("run_id", "nonce"):
        uuid.UUID(request[field])
    if request.get("pipeline_id"):
        uuid.UUID(request["pipeline_id"])
    manifest_digest = hashlib.sha256(json.dumps(config, sort_keys=True).encode()).hexdigest()
    if request.get("control_manifest_sha256") and request["control_manifest_sha256"] != manifest_digest:
        raise CanaryFailure("adapter_manifest_changed")
    action = request.get("action")
    if action == "preflight":
        for required in ACTIONS:
            entry = config.get("commands", {}).get(required)
            if not isinstance(entry, list) or not entry or any(not isinstance(arg, str) or not arg for arg in entry) or not os.path.isabs(entry[0]):
                raise CanaryFailure("complete_adapter_commands_required")
    command = config.get("commands", {}).get(action)
    if action not in ACTIONS or not isinstance(command, list) or not command or any(not isinstance(arg, str) or not arg for arg in command) or not os.path.isabs(command[0]):
        raise CanaryFailure("adapter_command_not_configured")
    timeout = request.get("timeout_seconds")
    if type(timeout) is not int or not 1 <= timeout <= 3600:
        raise CanaryFailure("adapter_timeout_invalid")
    completed = subprocess.run(command, input=json.dumps(request), capture_output=True,
                               text=True, timeout=timeout, check=False)
    if completed.returncode or len(completed.stdout) > 8192:
        raise CanaryFailure("adapter_command_failed")
    evidence = json.loads(completed.stdout or "{}")
    if not isinstance(evidence, dict):
        raise CanaryFailure("adapter_command_evidence_invalid")
    # Never echo arbitrary provider stdout or credential/configuration content.
    receipt = {}
    for key in EVIDENCE_FIELDS & evidence.keys():
        value = evidence[key]
        if key == "provider_kind":
            valid = value == "neon"
        elif key in ("staging", "isolated_fixture", "shared_admission_scope"):
            valid = type(value) is bool
        else:
            valid = type(value) is int and value >= 0
        if not valid:
            raise CanaryFailure("adapter_command_evidence_invalid")
        receipt[key] = value
    receipt["control_manifest_sha256"] = manifest_digest
    receipt.update(version=1, run_id=request["run_id"], nonce=request["nonce"], action=action,
                   backend_fingerprint=request["backend_fingerprint"], applied=True)
    return receipt


def main() -> int:
    try:
        raw = sys.stdin.read(16385)
        if len(raw) > 16384:
            raise CanaryFailure("adapter_request_too_large")
        request = json.loads(raw)
        config = json.loads(private_file(os.environ.get("PIPELINE_QUALIFICATION_ADAPTER_CONFIG", ""), 65536))
        if not isinstance(request, dict) or not isinstance(config, dict):
            raise CanaryFailure("adapter_request_invalid")
        print(json.dumps(execute(request, config)))
        return 0
    except (CanaryFailure, OSError, ValueError, KeyError, TypeError, subprocess.SubprocessError):
        print('{"code":"adapter_operation_failed"}')
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
