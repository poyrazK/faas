#!/usr/bin/env python3
"""Exercise a dedicated staging pipeline through the CLI and signed Data API.

This is an operator acceptance tool, not production approval. The source must be
an isolated, disposable database containing only the documented canary dataset.
Source credentials and JWTs are read from private files, never command arguments.
"""
from __future__ import annotations

import argparse
import base64
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid


class CanaryFailure(Exception):
    """Safe, fixed failure code; never wrap credentials or remote response text."""


def private_file(path: str, limit: int) -> str:
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(fd, "rb") as source:
        info = os.fstat(source.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o077:
            raise CanaryFailure("private_file_permissions")
        data = source.read(limit + 1)
    if len(data) > limit:
        raise CanaryFailure("private_file_size")
    return data.decode("utf-8").strip()


def https_url(raw: str) -> str:
    parsed = urllib.parse.urlsplit(raw)
    if parsed.scheme != "https" or not parsed.hostname or parsed.username or parsed.password or parsed.query or parsed.fragment:
        raise CanaryFailure("verified_https_url_required")
    return raw.rstrip("/")


def source_environment(raw: str) -> dict[str, str]:
    source = urllib.parse.urlsplit(raw)
    if source.scheme not in ("postgres", "postgresql") or not source.hostname or not source.username or not source.password or not source.path.strip("/"):
        raise CanaryFailure("invalid_source_connection")
    if urllib.parse.parse_qs(source.query) != {"sslmode": ["verify-full"]} or source.fragment:
        raise CanaryFailure("verified_source_tls_required")
    env = os.environ.copy()
    for key in ("PGSERVICE", "PGSERVICEFILE", "PGPASSFILE", "PGHOSTADDR", "PGOPTIONS"):
        env.pop(key, None)
    env.update(PGHOST=source.hostname, PGPORT=str(source.port or 5432),
               PGUSER=urllib.parse.unquote(source.username), PGPASSWORD=urllib.parse.unquote(source.password),
               PGDATABASE=urllib.parse.unquote(source.path[1:]), PGSSLMODE="verify-full")
    return env


def jwt_claims(token: str) -> dict:
    if not re.fullmatch(r"[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+", token):
        raise CanaryFailure("invalid_canary_token")
    payload = token.split(".")[1]
    claims = json.loads(base64.urlsafe_b64decode(payload + "=" * (-len(payload) % 4)))
    if not isinstance(claims, dict) or not isinstance(claims.get("sub"), str):
        raise CanaryFailure("invalid_canary_subject")
    return claims


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


class Canary:
    def __init__(self, args):
        self.args = args
        self.binary = os.environ.get("GREGALE_BINARY", "gregale")
        self.phases: list[dict] = []
        self.phase = "configuration"
        self.opener = urllib.request.build_opener(NoRedirect())

    @staticmethod
    def require_capacity(pipeline: dict):
        capacity = pipeline.get("capacity") or {}
        if capacity.get("state") != "ready":
            raise CanaryFailure("qualified_reserved_capacity_required")
        now = datetime.now(timezone.utc)
        for endpoint in ("source", "destination"):
            observation = capacity.get(endpoint) or {}
            try:
                observed = datetime.fromisoformat(observation["observed_at"].replace("Z", "+00:00"))
                expires = datetime.fromisoformat(observation["expires_at"].replace("Z", "+00:00"))
                valid = (observation.get("state") == "ready" and observation.get("required_connections") == 0
                         and observation["usable_connections"] >= observation["reserved_connections"] >= 5
                         and observed <= now < expires and 0 < (expires-observed).total_seconds() <= 30)
            except (KeyError, TypeError, ValueError):
                valid = False
            if not valid:
                raise CanaryFailure("qualified_reserved_capacity_required")

    def cli(self, *args) -> dict:
        completed = subprocess.run([self.binary, "pipeline", *args], capture_output=True,
                                   text=True, timeout=self.args.timeout + 10, check=False)
        if completed.returncode:
            raise CanaryFailure("cli_operation_failed")
        if not completed.stdout.strip():
            return {}
        result = json.loads(completed.stdout)
        if not isinstance(result, dict):
            raise CanaryFailure("invalid_cli_result")
        return result

    def sql(self, statement: str, value: str | None = None) -> str:
        command = ["psql", "-X", "-v", "ON_ERROR_STOP=1", "-At"]
        if value is not None:
            command.extend(["-v", "canary_value=" + value])
        completed = subprocess.run(command, input=statement, env=self.source_env,
                                   capture_output=True, text=True, timeout=self.args.timeout, check=False)
        if completed.returncode:
            raise CanaryFailure("source_fixture_sql_failed")
        return completed.stdout.strip()

    def http(self, token: str, method: str = "GET") -> tuple[int, object]:
        path = "/rest/v1/pipeline_canary?id=eq.1&select=id,value"
        body = b'{"value":"write-must-be-denied"}' if method == "PATCH" else None
        request = urllib.request.Request(self.api_url + path, data=body, method=method,
                                         headers={"Authorization": "Bearer " + token, "Content-Type": "application/json"})
        try:
            with self.opener.open(request, timeout=min(self.args.timeout, 15)) as response:
                data = response.read(4097)
                if len(data) > 4096:
                    raise CanaryFailure("unexpected_canary_response_size")
                return response.status, json.loads(data)
        except urllib.error.HTTPError as error:
            with error:
                data = error.read(4097)
                if len(data) > 4096:
                    return error.code, None
                try:
                    return error.code, json.loads(data)
                except ValueError:
                    return error.code, None

    def mirrored(self, expected: str) -> bool:
        status, rows = self.http(self.reader)
        return status == 200 and isinstance(rows, list) and len(rows) == 1 and isinstance(rows[0], dict) and str(rows[0].get("id")) == "1" and rows[0].get("value") == expected

    @staticmethod
    def require_state(pipeline: dict, expected: str):
        revision = pipeline.get("revision")
        if pipeline.get("state") != expected or not pipeline.get("observation_fresh") or type(revision) is not int or revision < 1 or pipeline.get("observed_revision") != revision:
            raise CanaryFailure("fresh_pipeline_state_required")

    def await_value(self, expected: str):
        deadline = time.monotonic() + self.args.timeout
        while time.monotonic() < deadline:
            if self.mirrored(expected):
                return
            time.sleep(1)
        raise CanaryFailure("change_capture_timeout")

    def passed(self, phase: str):
        self.phases.append({"phase": phase, "state": "passed"})

    def exercise_recovery(self):
        """Extension point for the operator-only provider qualification runner."""

    def confirm_cleanup(self, pipeline: dict):
        """Extension point for independent source and destination cleanup checks."""

    def run(self):
        if os.environ.get("FAAS_ENVIRONMENT") != "staging":
            raise CanaryFailure("staging_environment_required")
        https_url(os.environ.get("FAAS_API", ""))
        self.api_url = https_url(self.args.data_api_url)
        self.source_env = source_environment(private_file(self.args.source_url_file, 8192))
        self.reader = private_file(self.args.reader_token_file, 32768)
        self.other = private_file(self.args.other_token_file, 32768)
        reader, other = jwt_claims(self.reader), jwt_claims(self.other)
        self.phase = "dedicated_fixture"
        pipeline = self.cli("status", self.args.pipeline)
        if not pipeline.get("name", "").startswith("pipeline-canary-") or pipeline.get("tables") != ["pipeline_canary"]:
            raise CanaryFailure("dedicated_canary_pipeline_required")
        subjects = pipeline.get("read_subjects", [])
        if reader["sub"] not in subjects or other["sub"] in subjects or not reader.get("iss") or reader.get("iss") != other.get("iss") or reader.get("aud") != other.get("aud"):
            raise CanaryFailure("distinct_canary_reader_subjects_required")
        if self.sql("SELECT count(*)=1 AND coalesce(bool_and(id=1),false) FROM public.pipeline_canary;") != "t":
            raise CanaryFailure("single_row_canary_fixture_required")
        self.database = pipeline["database_id"]
        self.passed(self.phase)
        self.phase = "capacity_admission"
        self.require_capacity(pipeline)
        self.passed(self.phase)
        self.phase = "live_provider_replication"
        self.require_state(self.cli("wait", self.args.pipeline, "--timeout", str(self.args.timeout) + "s"), "live")
        first, second = uuid.uuid4().hex, uuid.uuid4().hex
        self.sql("UPDATE public.pipeline_canary SET value=:'canary_value' WHERE id=1;", first)
        self.await_value(first)
        self.passed(self.phase)
        self.phase = "read_only_and_subject_isolation"
        status, problem = self.http(self.reader, "PATCH")
        if status != 403 or not isinstance(problem, dict) or problem.get("code") != "read_only_identity":
            raise CanaryFailure("mirror_write_guard_failed")
        status, rows = self.http(self.other)
        if status != 200 or rows != []:
            raise CanaryFailure("mirror_subject_isolation_failed")
        self.passed(self.phase)
        self.phase = "verification"
        verified = self.cli("verify", self.args.pipeline, "--wait", "--timeout", str(self.args.timeout) + "s")
        if verified.get("verification") != "passed" or not verified.get("observation_fresh"):
            raise CanaryFailure("bounded_verification_failed")
        self.passed(self.phase)
        self.phase = "pause_and_resume"
        self.require_state(self.cli("pause", self.args.pipeline, "--wait", "--timeout", str(self.args.timeout) + "s"), "paused")
        self.sql("UPDATE public.pipeline_canary SET value=:'canary_value' WHERE id=1;", second)
        if not self.mirrored(first):
            raise CanaryFailure("paused_snapshot_changed")
        self.require_state(self.cli("resume", self.args.pipeline, "--wait", "--timeout", str(self.args.timeout) + "s"), "live")
        self.await_value(second)
        self.passed(self.phase)
        self.exercise_recovery()
        self.phase = "replication_cleanup"
        deleted = self.cli("delete", self.args.pipeline, "--wait", "--timeout", str(self.args.timeout) + "s")
        self.require_state(deleted, "deleted")
        self.confirm_cleanup(pipeline)
        self.cli("connection", "delete", pipeline["source_connection_id"])
        self.passed(self.phase)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--pipeline", required=True)
    parser.add_argument("--source-url-file", required=True)
    parser.add_argument("--reader-token-file", required=True)
    parser.add_argument("--other-token-file", required=True)
    parser.add_argument("--data-api-url", required=True)
    parser.add_argument("--report", required=True)
    parser.add_argument("--timeout", type=int, default=180)
    args = parser.parse_args()
    if not 1 <= args.timeout <= 3600:
        parser.error("timeout must be between 1 and 3600 seconds")
    canary = Canary(args)
    report = {"version": 1, "pipeline_id": args.pipeline, "state": "failed", "production_ready": False,
              "capacity_qualification": "operator_review_required", "phases": canary.phases}
    try:
        canary.run()
        report["state"] = "passed"
        report["retained_mirror_id"] = canary.database
    except CanaryFailure as error:
        report["phases"].append({"phase": canary.phase, "state": "failed", "code": str(error)})
    except (OSError, ValueError, KeyError, TypeError, subprocess.SubprocessError, urllib.error.URLError):
        report["phases"].append({"phase": canary.phase, "state": "failed", "code": "canary_operation_failed"})
    output = Path(args.report)
    output.write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps(report))
    return 0 if report["state"] == "passed" else 1


if __name__ == "__main__":
    raise SystemExit(main())
