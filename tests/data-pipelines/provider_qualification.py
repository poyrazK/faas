#!/usr/bin/env python3
"""Operator-only staging qualification of native replication on a real provider.

Faults are supplied by a reviewed executable adapter, never by the public API.
The runner checks observable behavior independently and always attempts restore.
A passing report remains subject to operator review and never enables production.
"""
from __future__ import annotations

import argparse
from contextlib import contextmanager
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import shutil
import subprocess
import time
import urllib.error
import urllib.request
import uuid

from staging_canary import Canary, CanaryFailure, https_url, jwt_claims, private_file, source_environment

SCENARIOS = (
    "initial_copy", "dedicated_fixture", "capacity_admission", "live_provider_replication",
    "read_only_and_subject_isolation", "verification", "pause_and_resume",
    "credential_rotation", "worker_restart", "source_outage", "destination_outage",
    "capacity_exhaustion", "retention_pressure", "replication_cleanup",
)
FAULTS = {
    "credential_rotate": "credential_restore", "worker_stop": "worker_start",
    "source_disconnect": "source_reconnect", "destination_disconnect": "destination_reconnect",
    "capacity_fill": "capacity_release", "retention_fill": "retention_release",
}
# Must track pkg/api/limits.go. A contract test checks these values.
OBSERVATION_MAX_AGE = 90
RETENTION_MAX_BYTES = 1 << 30
RETENTION_WARNING_BYTES = RETENTION_MAX_BYTES * 4 // 5


def utc() -> str:
    return datetime.now(timezone.utc).isoformat()


def executable_digest(path: str) -> str:
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(fd, "rb") as executable:
        info = os.fstat(executable.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o077 or not info.st_mode & stat.S_IXUSR:
            raise CanaryFailure("private_adapter_executable_required")
        digest = hashlib.sha256()
        for chunk in iter(lambda: executable.read(65536), b""):
            digest.update(chunk)
        return digest.hexdigest()


def prerequisites(args) -> list[str]:
    """No network, provider adapter or CLI calls; safe before live authorization."""
    blockers = []
    for binary, code in ((os.environ.get("GREGALE_BINARY", "gregale"), "gregale_binary_unavailable"), ("psql", "psql_binary_unavailable")):
        if not shutil.which(binary):
            blockers.append(code)
    if os.environ.get("FAAS_ENVIRONMENT") != "staging":
        blockers.append("staging_environment_required")
    try:
        https_url(os.environ.get("FAAS_API", ""))
    except CanaryFailure:
        blockers.append("staging_control_api_required")
    for field in ("connection", "database", "admission_database"):
        try:
            uuid.UUID(getattr(args, field) or "")
        except ValueError:
            blockers.append(field + "_id_required")
    if args.database and args.database == args.admission_database:
        blockers.append("distinct_empty_admission_database_required")
    if not re.fullmatch(r"pipeline-canary-[a-z0-9-]{1,23}", args.name or ""):
        blockers.append("unique_canary_name_required")
    try:
        https_url(args.data_api_url or "")
    except CanaryFailure:
        blockers.append("canary_data_api_required")
    for field, limit in (("source_url_file", 8192), ("rotated_source_url_file", 8192),
                         ("reader_token_file", 32768), ("other_token_file", 32768),
                         ("control_token_file", 32768)):
        try:
            value = private_file(getattr(args, field) or "", limit)
            if not value:
                raise CanaryFailure("empty_private_file")
            if "source_url" in field:
                source_environment(value)
            elif field != "control_token_file":
                jwt_claims(value)
        except (OSError, ValueError, CanaryFailure):
            blockers.append(field + "_unavailable_or_invalid")
    try:
        executable_digest(args.adapter or "")
    except (OSError, CanaryFailure):
        blockers.append("reviewed_private_adapter_required")
    if not re.fullmatch(r"[0-9a-f]{64}", args.backend_fingerprint or ""):
        blockers.append("backend_fingerprint_required")
    if args.timeout < OBSERVATION_MAX_AGE + 30 or args.timeout > 3600:
        blockers.append("timeout_must_cover_monitoring_expiry")
    if not 5 <= args.hold_seconds <= 60:
        blockers.append("fault_hold_out_of_range")
    return blockers


class Qualification(Canary):
    def __init__(self, args):
        super().__init__(args)
        self.run_id = str(uuid.uuid4())
        self.active_faults: dict[str, str] = {}
        self.restore_failures: list[str] = []
        self.adapter_sha256 = ""
        self.started_at = utc()

    def adapter(self, action: str, nonce: str) -> dict:
        # Fixed verbs, argv contains no credentials, and there is no shell.
        if action not in {*FAULTS, *FAULTS.values(), "preflight", "audit_cleanup", "audit_capacity", "data_api_refresh"}:
            raise CanaryFailure("unknown_adapter_action")
        if executable_digest(self.args.adapter) != self.adapter_sha256:
            raise CanaryFailure("adapter_changed_during_run")
        request = {"version": 1, "run_id": self.run_id, "nonce": nonce, "action": action,
                   "pipeline_id": self.args.pipeline, "source_connection_id": self.args.connection,
                   "database_id": self.args.database, "admission_database_id": self.args.admission_database,
                   "backend_fingerprint": self.args.backend_fingerprint, "data_api_url": self.args.data_api_url,
                   "control_manifest_sha256": getattr(self, "control_manifest_sha256", ""),
                   "timeout_seconds": self.args.timeout, "max_retained_wal_bytes": RETENTION_MAX_BYTES - (32 << 20)}
        result = subprocess.run([self.args.adapter], input=json.dumps(request), capture_output=True,
                                text=True, timeout=self.args.timeout, check=False)
        if result.returncode or len(result.stdout) > 8192:
            raise CanaryFailure("provider_adapter_failed")
        receipt = json.loads(result.stdout)
        if (not isinstance(receipt, dict) or receipt.get("version") != 1 or receipt.get("run_id") != self.run_id
                or receipt.get("nonce") != nonce or receipt.get("action") != action
                or receipt.get("backend_fingerprint") != self.args.backend_fingerprint
                or receipt.get("applied") is not True):
            raise CanaryFailure("provider_adapter_receipt_mismatch")
        expected_manifest = getattr(self, "control_manifest_sha256", "")
        if expected_manifest and receipt.get("control_manifest_sha256") != expected_manifest:
            raise CanaryFailure("provider_adapter_manifest_changed")
        return receipt

    @contextmanager
    def fault(self, action: str):
        nonce = str(uuid.uuid4())
        self.active_faults[action] = nonce  # Record BEFORE invocation: timeout may follow a successful mutation.
        try:
            self.adapter(action, nonce)
            yield
        finally:
            try:
                self.adapter(FAULTS[action], nonce)
                del self.active_faults[action]
            except (CanaryFailure, OSError, ValueError, subprocess.SubprocessError):
                self.restore_failures.append(FAULTS[action])
                raise CanaryFailure("fault_restore_failed") from None

    def status(self) -> dict:
        return self.cli("status", self.args.pipeline)

    def await_status(self, predicate, code: str) -> dict:
        deadline = time.monotonic() + self.args.timeout
        while time.monotonic() < deadline:
            pipeline = self.status()
            if predicate(pipeline):
                return pipeline
            if pipeline.get("state") == "resync_required":
                raise CanaryFailure("unexpected_resync_required")
            time.sleep(1)
        raise CanaryFailure(code)

    def await_live(self):
        self.require_state(self.cli("wait", self.args.pipeline, "--timeout", str(self.args.timeout) + "s"), "live")

    def write_marker(self) -> str:
        value = uuid.uuid4().hex
        self.sql("UPDATE public.pipeline_canary SET value=:'canary_value' WHERE id=1;", value)
        return value

    def unchanged_during_fault(self, baseline: str, destination: bool = False):
        deadline = time.monotonic() + self.args.hold_seconds
        while time.monotonic() < deadline:
            try:
                status, rows = self.http(self.reader)
            except (OSError, urllib.error.URLError):
                if not destination:
                    raise CanaryFailure("unexpected_data_api_outage") from None
                status, rows = 503, None
            if destination:
                if status not in (502, 503, 504):
                    raise CanaryFailure("destination_outage_not_observed")
            elif status != 200 or rows != [{"id": 1, "value": baseline}]:
                raise CanaryFailure("replication_continued_during_source_outage")
            time.sleep(1)

    def control(self, method: str, path: str, body=None) -> tuple[int, dict]:
        data = json.dumps(body).encode() if body is not None else None
        request = urllib.request.Request(https_url(os.environ["FAAS_API"]) + path, data=data, method=method,
                                        headers={"Authorization": "Bearer " + self.control_token,
                                                 "Content-Type": "application/json", "Idempotency-Key": str(uuid.uuid4())})
        try:
            response = self.opener.open(request, timeout=min(self.args.timeout, 30))
        except urllib.error.HTTPError as error:
            response = error
        with response:
            raw = response.read(65537)
            if len(raw) > 65536:
                raise CanaryFailure("control_response_too_large")
            result = json.loads(raw)
            if not isinstance(result, dict):
                raise CanaryFailure("invalid_control_response")
            return response.code, result

    def prepare(self):
        self.phase = "initial_copy"
        self.adapter_sha256 = executable_digest(self.args.adapter)
        self.source_env = source_environment(private_file(self.args.source_url_file, 8192))
        rotated = source_environment(private_file(self.args.rotated_source_url_file, 8192))
        identity = ("PGHOST", "PGPORT", "PGUSER", "PGDATABASE")
        if any(rotated[key] != self.source_env[key] for key in identity) or rotated["PGPASSWORD"] == self.source_env["PGPASSWORD"]:
            raise CanaryFailure("rotation_requires_same_identity_new_password")
        self.reader = private_file(self.args.reader_token_file, 32768)
        self.control_token = private_file(self.args.control_token_file, 32768)
        self.args.pipeline = ""
        other = jwt_claims(private_file(self.args.other_token_file, 32768))
        reader = jwt_claims(self.reader)
        if not reader.get("iss") or reader.get("iss") != other.get("iss") or reader.get("aud") != other.get("aud") or reader["sub"] == other["sub"]:
            raise CanaryFailure("distinct_canary_reader_subjects_required")
        for claims in (reader, other):
            if type(claims.get("exp")) not in (int, float) or not claims["exp"] >= time.time() + self.args.timeout + OBSERVATION_MAX_AGE:
                raise CanaryFailure("canary_token_lifetime_insufficient")
        if self.sql("SELECT count(*)=1 AND coalesce(bool_and(id=1),false) FROM public.pipeline_canary;") != "t":
            raise CanaryFailure("single_row_canary_fixture_required")
        if self.sql("SELECT NOT EXISTS(SELECT 1 FROM pg_publication) AND NOT EXISTS(SELECT 1 FROM pg_replication_slots WHERE database=current_database());") != "t":
            raise CanaryFailure("unused_isolated_source_required")
        receipt = self.adapter("preflight", str(uuid.uuid4()))
        self.control_manifest_sha256 = receipt.get("control_manifest_sha256", "")
        if not re.fullmatch(r"[0-9a-f]{64}", self.control_manifest_sha256):
            raise CanaryFailure("control_manifest_digest_required")
        source_version = self.sql("SHOW server_version_num;")
        if not source_version.isdigit() or not 160000 <= int(source_version) < 170000:
            raise CanaryFailure("postgres_16_source_required")
        self.source_version = int(source_version)
        self.target_version = receipt.get("target_postgres_version")
        if (receipt.get("provider_kind") != "neon" or type(self.target_version) is not int
                or not 160000 <= self.target_version < 170000):
            raise CanaryFailure("qualified_provider_version_required")
        self.provider_kind = "neon"
        if (receipt.get("staging") is not True or receipt.get("isolated_fixture") is not True
                or receipt.get("source_retention_bytes") != RETENTION_MAX_BYTES
                or receipt.get("shared_admission_scope") is not True):
            raise CanaryFailure("isolated_provider_fixture_required")
        # Use the API token to check the name so retries cannot count an old copy as a new one.
        code, listing = self.control("GET", "/v1/data/pipelines")
        if code != 200 or not isinstance(listing.get("items"), list):
            raise CanaryFailure("pipeline_inventory_unavailable")
        if any(item.get("name") == self.args.name for item in listing["items"]):
            raise CanaryFailure("qualification_requires_unused_pipeline_name")
        created = self.cli("create", self.args.name, "--connection", self.args.connection,
                           "--database", self.args.database, "--tables", "pipeline_canary",
                           "--read-subjects", jwt_claims(self.reader)["sub"])
        self.args.pipeline = created["id"]  # Save before waiting; failures must retain the resource ID.
        if created.get("generation") != 1 or created.get("database_id") != self.args.database or created.get("source_connection_id") != self.args.connection:
            raise CanaryFailure("initial_pipeline_identity_mismatch")
        code, owned = self.control("GET", "/v1/data/pipelines/" + self.args.pipeline)
        if code != 200 or owned.get("id") != self.args.pipeline:
            raise CanaryFailure("control_and_cli_account_mismatch")
        self.await_live()
        self.adapter("data_api_refresh", str(uuid.uuid4()))
        self.passed(self.phase)

    def credential_rotation(self):
        old_env = self.source_env
        rotated = source_environment(private_file(self.args.rotated_source_url_file, 8192))
        try:
            with self.fault("credential_rotate"):
                self.source_env = rotated
                if self.sql("SELECT 1;") != "1":
                    raise CanaryFailure("rotated_source_login_failed")
                # A rejected old login distinguishes rotation from re-sealing the same password.
                old = subprocess.run(["psql", "-X", "-At", "-c", "SELECT 1"], env=old_env,
                                     capture_output=True, timeout=self.args.timeout, check=False)
                if old.returncode == 0:
                    raise CanaryFailure("old_source_credential_still_accepted")
                self.cli("connection", "rotate", self.args.connection, "--url-file", self.args.rotated_source_url_file)
                with self.fault("source_disconnect"):
                    pass  # The adapter must close existing native sessions, forcing a new login.
                self.await_live()
                marker = self.write_marker()
                self.await_value(marker)
        finally:
            self.source_env = old_env
            # Restore intent even when rotation or the remote acknowledgement failed.
            self.cli("connection", "rotate", self.args.connection, "--url-file", self.args.source_url_file)
        self.await_live()
        self.await_value(self.write_marker())

    def worker_restart(self):
        before = self.status()
        self.require_capacity(before)
        with self.fault("worker_stop"):
            marker = self.write_marker()
            self.await_value(marker)  # Native replication must survive worker absence.
            stale = self.await_status(lambda p: p.get("observation_fresh") is False
                                      and (p.get("health") or {}).get("code") == "monitoring_stale", "worker_monitoring_never_expired")
            observed = datetime.fromisoformat(stale["observed_at"].replace("Z", "+00:00"))
            if (datetime.now(timezone.utc)-observed).total_seconds() < OBSERVATION_MAX_AGE:
                raise CanaryFailure("worker_outage_expiry_not_observed")
        self.await_live()
        after = self.status()
        if after.get("generation") != before.get("generation"):
            raise CanaryFailure("worker_restart_replaced_generation")
        self.require_capacity(after)
        for endpoint in ("source", "destination"):
            if after["capacity"][endpoint]["reserved_connections"] != before["capacity"][endpoint]["reserved_connections"]:
                raise CanaryFailure("worker_restart_changed_reservations")
        self.await_value(self.write_marker())

    def outage(self, destination: bool):
        baseline = self.write_marker()
        self.await_value(baseline)
        with self.fault("destination_disconnect" if destination else "source_disconnect"):
            marker = self.write_marker()  # Runner SQL remains reachable independently of the native path.
            self.await_status(lambda p: p.get("observation_fresh") is True and p.get("state") == "blocked", "outage_not_observed")
            self.unchanged_during_fault(baseline, destination)
        self.await_live()
        self.await_value(marker)

    def capacity_exhaustion(self):
        self.cli("connection", "check", self.args.connection, "--database", self.args.admission_database,
                 "--timeout", str(self.args.timeout) + "s")
        candidate = "pipeline-canary-" + uuid.uuid4().hex[:16]
        with self.fault("capacity_fill"):
            before = self.adapter("audit_capacity", str(uuid.uuid4()))
            code, result = self.control("POST", "/v1/data/pipelines", {
                "name": candidate, "source_connection_id": self.args.connection,
                "database_id": self.args.admission_database, "tables": ["pipeline_canary"],
                "read_subjects": [jwt_claims(self.reader)["sub"]]})
            if code != 409 or result.get("code") != "data_pipeline_insufficient_capacity":
                # If unexpectedly created, record it for manual cleanup; never silently discard an intent.
                self.unexpected_pipeline_id = result.get("id") if code in (200, 201, 202) else None
                raise CanaryFailure("atomic_capacity_denial_not_observed")
            code, listing = self.control("GET", "/v1/data/pipelines")
            if code != 200 or not isinstance(listing.get("items"), list) or any(item.get("name") == candidate for item in listing["items"]):
                raise CanaryFailure("rejected_admission_left_pipeline_intent")
            after = self.adapter("audit_capacity", str(uuid.uuid4()))
            if type(before.get("reservation_count")) is not int or before["reservation_count"] < 0 or before.get("reservation_count") != after.get("reservation_count"):
                raise CanaryFailure("rejected_admission_leaked_reservations")
        checked = self.cli("connection", "check", self.args.connection, "--database", self.args.admission_database,
                           "--timeout", str(self.args.timeout) + "s")
        if checked.get("state") != "passed" or not checked.get("fresh") or (checked.get("capacity") or {}).get("state") != "ready":
            raise CanaryFailure("capacity_not_recovered")
        self.await_live()
        self.await_value(self.write_marker())

    def retention_pressure(self):
        with self.fault("retention_fill"):
            pressure = self.await_status(lambda p: p.get("observation_fresh") is True
                                         and (p.get("health") or {}).get("code") == "retention_pressure",
                                         "retention_pressure_not_observed")
            retained = pressure.get("retained_wal_bytes")
            if type(retained) is not int or not RETENTION_WARNING_BYTES <= retained < RETENTION_MAX_BYTES:
                raise CanaryFailure("retention_pressure_evidence_invalid")
        self.await_live()
        self.await_value(self.write_marker())
        self.await_status(lambda p: p.get("observation_fresh") is True
                          and type(p.get("retained_wal_bytes")) is int
                          and p["retained_wal_bytes"] < RETENTION_WARNING_BYTES, "retention_pressure_not_recovered")

    def exercise_recovery(self):
        for name, scenario in (("credential_rotation", self.credential_rotation), ("worker_restart", self.worker_restart),
                               ("source_outage", lambda: self.outage(False)), ("destination_outage", lambda: self.outage(True)),
                               ("capacity_exhaustion", self.capacity_exhaustion), ("retention_pressure", self.retention_pressure)):
            self.phase = name
            for token in (self.reader, self.other):
                claims = jwt_claims(token)
                if type(claims.get("exp")) not in (int, float) or not claims["exp"] >= time.time() + self.args.timeout + OBSERVATION_MAX_AGE:
                    raise CanaryFailure("canary_token_lifetime_insufficient")
            scenario()
            self.passed(name)

    def confirm_cleanup(self, pipeline: dict):
        # This source was verified empty before creation. Check all slots in
        # that disposable database: native table-sync slots use pg_* names,
        # so matching only the main gregale_sync_* name would miss leaks.
        if self.sql("SELECT NOT EXISTS(SELECT 1 FROM pg_replication_slots WHERE database=current_database()) AND NOT EXISTS(SELECT 1 FROM pg_publication);") != "t":
            raise CanaryFailure("source_replication_objects_leaked")
        receipt = self.adapter("audit_cleanup", str(uuid.uuid4()))
        if (any(type(receipt.get(key)) is not int or receipt[key] != 0 for key in
                ("target_subscription_count", "pipeline_reservation_count", "fault_resource_count")) or self.active_faults):
            raise CanaryFailure("provider_cleanup_incomplete")

    def run(self):
        self.prepare()
        super().run()


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    for option in ("name", "connection", "database", "admission-database", "backend-fingerprint", "adapter",
                   "source-url-file", "rotated-source-url-file", "reader-token-file", "other-token-file",
                   "control-token-file", "data-api-url"):
        parser.add_argument("--" + option)
    parser.add_argument("--report", required=True)
    parser.add_argument("--timeout", type=int, default=240)
    parser.add_argument("--hold-seconds", type=int, default=5)
    parser.add_argument("--check-config", action="store_true")
    args = parser.parse_args()
    # Reserve a private output BEFORE any remote action so a bad/existing report
    # path cannot leave a live run without its resource identifiers.
    report_fd = os.open(args.report, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    blockers = prerequisites(args)
    runner = Qualification(args)
    report = {"version": 1, "run_id": runner.run_id, "started_at": runner.started_at,
              "state": "blocked", "production_ready": False, "pilot_ready": False,
              "expected_provider_kind": "neon", "operator_review": "required", "capacity_qualification": "operator_review_required",
              "blockers": blockers, "phases": runner.phases}
    with os.fdopen(os.dup(report_fd), "w") as initial:
        json.dump({"version": 1, "run_id": runner.run_id, "state": "running",
                   "candidate_name": args.name, "retained_mirror_id": args.database,
                   "production_ready": False, "pilot_ready": False}, initial)
        initial.flush()
        os.fsync(initial.fileno())
    if not blockers and not args.check_config:
        report["state"] = "failed"
        try:
            runner.run()
            report["state"] = "passed"
        except CanaryFailure as error:
            runner.phases.append({"phase": runner.phase, "state": "failed", "code": str(error)})
        except KeyboardInterrupt:
            runner.phases.append({"phase": runner.phase, "state": "failed", "code": "qualification_interrupted"})
        except (OSError, ValueError, KeyError, TypeError, subprocess.SubprocessError, urllib.error.URLError):
            runner.phases.append({"phase": runner.phase, "state": "failed", "code": "qualification_operation_failed"})
        report.update(candidate_name=args.name, source_connection_id=args.connection, pipeline_id=getattr(args, "pipeline", ""), retained_mirror_id=args.database,
                      adapter_sha256=runner.adapter_sha256, control_manifest_sha256=getattr(runner, "control_manifest_sha256", None), backend_fingerprint=args.backend_fingerprint,
                      active_faults=list(runner.active_faults), restore_failures=runner.restore_failures,
                      source_postgres_version=getattr(runner, "source_version", None),
                      target_postgres_version=getattr(runner, "target_version", None), provider_kind=getattr(runner, "provider_kind", None))
        if getattr(runner, "unexpected_pipeline_id", None):
            report["unexpected_pipeline_id"] = runner.unexpected_pipeline_id
    elif not blockers:
        report["state"] = "configuration_valid"
    evidence = {}
    for filename in ("provider_qualification.py", "provider_fault_adapter.py", "staging_canary.py"):
        evidence[filename] = hashlib.sha256(Path(__file__).with_name(filename).read_bytes()).hexdigest()
    binary = shutil.which(runner.binary)
    if binary:
        digest = hashlib.sha256()
        with open(binary, "rb") as source:
            for chunk in iter(lambda: source.read(65536), b""):
                digest.update(chunk)
        evidence["gregale_binary"] = digest.hexdigest()
    report["evidence_sha256"] = evidence
    completed = {p["phase"] for p in runner.phases}
    runner.phases.extend({"phase": name, "state": "not_run"} for name in SCENARIOS if name not in completed)
    if report["state"] == "passed" and (len(runner.phases) != len(SCENARIOS)
            or any(phase["state"] != "passed" for phase in runner.phases)):
        report["state"] = "failed"
        report["blockers"].append("incomplete_qualification_evidence")
    report["finished_at"] = utc()
    with os.fdopen(report_fd, "w") as output:
        output.seek(0)
        json.dump(report, output, indent=2)
        output.write("\n")
        output.truncate()
        output.flush()
        os.fsync(output.fileno())
    print(json.dumps(report))
    return 0 if report["state"] in ("passed", "configuration_valid") else 1


if __name__ == "__main__":
    raise SystemExit(main())
