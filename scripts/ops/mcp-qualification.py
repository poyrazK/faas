#!/usr/bin/env python3
"""Prepare, run MCP staging qualification probes, and validate redacted evidence."""

import argparse
import base64
import hashlib
import importlib.util
import json
import math
import os
from pathlib import Path
import re
import secrets
import subprocess
import tempfile
from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, HTTPServer
import threading
import time
from urllib.error import HTTPError, URLError
from urllib.parse import parse_qs, quote_plus, urlencode, urlparse, urlunparse
from urllib.request import HTTPRedirectHandler, Request, build_opener


CHECKER_PATH = Path(__file__).resolve().parents[1] / "ci" / "mcp-qualification-check.py"
SPEC = importlib.util.spec_from_file_location("mcp_qualification_checker", CHECKER_PATH)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError(f"could not load qualification checker at {CHECKER_PATH}")
CHECKER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CHECKER)

NATIVE_PROBE_RUNNER = "gregale-mcp-native-v1"
OAUTH_PROBE_RUNNER = "gregale-mcp-oauth-v1"
OAUTH_LIFECYCLE_RUNNER = "gregale-mcp-oauth-lifecycle-v1"
SDK_INTEROP_RUNNER = "gregale-mcp-sdk-interop-v3"
SDK_INTEROP_VERSION = "2.3.1"
TASKS_RESTART_RUNNER = "gregale-mcp-tasks-restart-v1"
TASKS_SCALE_ZERO_RUNNER = "gregale-mcp-tasks-scale-zero-v2"
NATIVE_WAKE_TIERS = {
    "native_cold_boot": "cold",
    "native_park_restore": "restored",
}
AUTOMATED_RUNNERS = {
    **{name: NATIVE_PROBE_RUNNER for name in NATIVE_WAKE_TIERS},
    "oauth_resource_server": OAUTH_PROBE_RUNNER,
    "oauth_provider_lifecycle": OAUTH_LIFECYCLE_RUNNER,
    "official_sdk_interop": SDK_INTEROP_RUNNER,
    "tasks_restart_replicas": TASKS_RESTART_RUNNER,
    "tasks_scale_from_zero": TASKS_SCALE_ZERO_RUNNER,
}
NATIVE_DOCTOR_CHECKS = {
    "modern_catalog_discovery", "untrusted_origin", "live_progress",
    "gateway_streaming", "oauth_resource_discovery", "legacy_stateless_compatibility",
}


def load_manifest(directory):
    root = directory.resolve()
    manifest_path = root / "manifest.json"
    if not manifest_path.is_file() or manifest_path.is_symlink():
        raise ValueError(f"{manifest_path} must be a regular manifest file")
    evidence = json.loads(manifest_path.read_text(encoding="utf-8"))
    if not isinstance(evidence, dict):
        raise ValueError("manifest must be a JSON object")
    if evidence.get("version") != 1:
        raise ValueError("manifest version must be 1")
    if not re.fullmatch(r"[0-9a-f]{40}", evidence.get("commit", "")):
        raise ValueError("manifest must identify a full lowercase 40-character commit SHA")
    records = evidence.get("checks")
    if not isinstance(records, list):
        raise ValueError("manifest checks must be a list")
    by_name = {record.get("name"): record for record in records if isinstance(record, dict)}
    if len(by_name) != len(records) or set(by_name) != CHECKER.REQUIRED:
        raise ValueError("manifest must contain exactly one record for every required qualification")
    return root, manifest_path, evidence, by_name


def atomic_write(path, evidence):
    body = json.dumps(evidence, indent=2, sort_keys=True) + "\n"
    temp_path = None
    try:
        with tempfile.NamedTemporaryFile(
            mode="w", encoding="utf-8", dir=path.parent, prefix=".manifest-", delete=False
        ) as temp:
            temp_path = Path(temp.name)
            os.chmod(temp_path, 0o600)
            temp.write(body)
            temp.flush()
            os.fsync(temp.fileno())
        os.replace(temp_path, path)
    finally:
        if temp_path is not None and temp_path.exists():
            temp_path.unlink()


def atomic_write_bytes(path, body):
    temp_path = None
    try:
        with tempfile.NamedTemporaryFile(mode="wb", dir=path.parent, prefix=".receipt-", delete=False) as temp:
            temp_path = Path(temp.name)
            os.chmod(temp_path, 0o600)
            temp.write(body)
            temp.flush()
            os.fsync(temp.fileno())
        os.replace(temp_path, path)
    finally:
        if temp_path is not None and temp_path.exists():
            temp_path.unlink()


def init(args):
    if not re.fullmatch(r"[0-9a-f]{40}", args.commit):
        raise ValueError("--commit must be a full lowercase 40-character commit SHA")
    root = args.directory
    root.mkdir(parents=True, exist_ok=True, mode=0o700)
    manifest = root / "manifest.json"
    if manifest.exists() or manifest.is_symlink():
        raise ValueError(f"{manifest} already exists")
    evidence = {
        "version": 1,
        "commit": args.commit,
        "checks": [
            {"name": name, "status": "pending", "target": ""}
            for name in sorted(CHECKER.REQUIRED)
        ],
    }
    atomic_write(manifest, evidence)
    print(json.dumps({"ok": True, "manifest": str(manifest), "commit": args.commit, "checks": len(evidence["checks"])}))


def artifact_path(root, relative):
    path = Path(relative)
    if path.is_absolute() or not path.parts or ".." in path.parts:
        raise ValueError("artifact path must be a nonempty relative path inside the evidence directory")
    candidate = root / path
    resolved = candidate.resolve(strict=True)
    if not resolved.is_relative_to(root) or not resolved.is_file():
        raise ValueError("artifact must be a regular file inside the evidence directory")
    current = root
    for part in path.parts:
        current = current / part
        if current.is_symlink():
            raise ValueError("artifact paths cannot contain symlinks")
    return path, resolved


def write_receipt(root, relative, body):
    path = Path(relative)
    if path.is_absolute() or not path.parts or ".." in path.parts:
        raise ValueError("receipt path must be a nonempty relative path inside the evidence directory")
    candidate = root / path
    current = root
    for part in path.parts[:-1]:
        current = current / part
        if current.is_symlink():
            raise ValueError("receipt paths cannot contain symlinks")
        if not current.exists():
            current.mkdir(mode=0o700)
        if not current.resolve().is_relative_to(root):
            raise ValueError("receipt directory must stay inside the evidence directory")
    if candidate.is_symlink():
        raise ValueError("receipt paths cannot contain symlinks")
    atomic_write_bytes(candidate, body)
    return path


def sha256_file(path):
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def record_evidence(directory, name, target, artifact, status):
    root, manifest, evidence, by_name = load_manifest(directory)
    if name not in CHECKER.REQUIRED:
        raise ValueError(f"unknown qualification name: {name}")
    if not target.strip() or len(target) > 512 or any(ord(char) < 32 for char in target):
        raise ValueError("target must identify the test app, native host/build, or client version")
    relative, receipt = artifact_path(root, artifact)
    evidence_type = None
    if receipt.suffix == ".json":
        try:
            body = json.loads(receipt.read_text(encoding="utf-8"))
        except (UnicodeDecodeError, json.JSONDecodeError):
            body = None
        if isinstance(body, dict) and body.get("runner") in set(AUTOMATED_RUNNERS.values()):
            expected_runner = AUTOMATED_RUNNERS.get(name)
            if body.get("runner") != expected_runner:
                raise ValueError("automated receipt runner does not match the selected qualification")
            if body.get("check") != name or body.get("target") != target.strip():
                raise ValueError("automated probe receipt does not match the selected check and target")
            if body.get("status") != status:
                raise ValueError("automated probe status cannot be changed when recording its receipt")
            if expected_runner == TASKS_RESTART_RUNNER:
                CHECKER.validate_tasks_restart_receipt(body, name, target.strip(), require_passed=False)
            elif expected_runner == TASKS_SCALE_ZERO_RUNNER:
                CHECKER.validate_tasks_scale_zero_receipt(body, name, target.strip(), require_passed=False)
            evidence_type = expected_runner
    if name == "tasks_scale_from_zero" and evidence_type != TASKS_SCALE_ZERO_RUNNER:
        raise ValueError("tasks_scale_from_zero requires a validated staging-runner receipt")
    record = by_name[name]
    record.update({
        "status": status,
        "target": target.strip(),
        "artifact": relative.as_posix(),
        "sha256": sha256_file(receipt),
    })
    if evidence_type:
        record["evidence_type"] = evidence_type
    else:
        record.pop("evidence_type", None)
    atomic_write(manifest, evidence)
    return {"ok": True, "name": name, "status": status, "artifact": relative.as_posix(), "sha256": record["sha256"]}


def record(args):
    if not args.target.strip():
        raise ValueError("--target must identify the test app, native host/build, or client version")
    print(json.dumps(record_evidence(args.directory, args.name, args.target, args.artifact, args.status)))


def native_probe(args):
    root, _, _, _ = load_manifest(args.directory)
    expected_wake_tier = NATIVE_WAKE_TIERS.get(args.name)
    if expected_wake_tier is None:
        raise ValueError("native probe supports only native_cold_boot and native_park_restore")
    if not re.fullmatch(r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?", args.app):
        raise ValueError("--app must be a valid lowercase Gregale app slug")
    if args.confirm_staging_app != args.app:
        raise ValueError("--confirm-staging-app must exactly match --app to authorize parking the staging app")
    if not args.target.strip() or len(args.target) > 512 or any(ord(char) < 32 for char in args.target):
        raise ValueError("--target must identify the test app, native host/build, or client version")
    if not args.stream_tool.strip():
        raise ValueError("--stream-tool must select a harmless streaming qualification tool")
    if args.timeout < 1 or args.timeout > 1200:
        raise ValueError("--timeout must be between 1 and 1200 seconds")
    if args.token_env:
        token = os.environ.get(args.token_env, "")
        if not token or "\n" in token or "\r" in token:
            raise ValueError("the named MCP token environment variable must contain a valid token")
    if args.arguments_file and not Path(args.arguments_file).is_file():
        raise ValueError("--arguments-file must name an existing regular file")

    parked = None
    final_park = None
    instances = None
    doctor = None
    doctor_body = None
    try:
        parked = subprocess.run(
            [args.gregale, "park", args.app],
            stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
            timeout=args.timeout, check=False,
        )
        if parked.returncode == 0:
            command = [args.gregale, "mcp", "doctor", "--app", args.app,
                       "--stream-tool", args.stream_tool, "--timeout", f"{args.timeout}s", "--json"]
            if args.token_env:
                command.extend(["--token-env", args.token_env])
            if args.arguments_file:
                command.extend(["--arguments-file", args.arguments_file])
            doctor = subprocess.run(
                command, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
                text=True, encoding="utf-8", errors="replace", timeout=args.timeout + 30, check=False,
            )
            try:
                doctor_body = json.loads(doctor.stdout)
            except json.JSONDecodeError:
                doctor_body = None
    except (OSError, subprocess.TimeoutExpired):
        pass

    if parked is not None and parked.returncode == 0:
        try:
            final_park = subprocess.run(
                [args.gregale, "park", args.app],
                stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                timeout=args.timeout, check=False,
            )
            if final_park.returncode == 0:
                instances = subprocess.run(
                    [args.gregale, "ps", args.app, "--json"],
                    stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
                    text=True, encoding="utf-8", errors="replace", timeout=args.timeout, check=False,
                )
        except (OSError, subprocess.TimeoutExpired):
            pass

    checks = []
    wake_tier = ""
    stream = None
    if isinstance(doctor_body, dict):
        checks = [
            {"name": item["name"], "status": "passed" if item.get("status") == "passed" else "failed"}
            for item in doctor_body.get("checks", [])
            if isinstance(item, dict) and isinstance(item.get("name"), str)
            and item["name"] in NATIVE_DOCTOR_CHECKS
        ]
        discovery = doctor_body.get("discovery")
        if isinstance(discovery, dict):
            observed = discovery.get("wake_tier", "")
            wake_tier = observed if observed in {"cold", "restored", "hot"} else ("unknown" if observed else "")
        raw_stream = doctor_body.get("stream")
        if isinstance(raw_stream, dict):
            progress = raw_stream.get("progress_ms", [])
            stream = {
                "status": "passed" if isinstance(progress, list) and len(progress) >= 2 else "failed",
                "progress_events": len(progress) if isinstance(progress, list) else 0,
                "duration_ms": (
                    raw_stream.get("duration_ms")
                    if type(raw_stream.get("duration_ms")) is int and raw_stream["duration_ms"] >= 0
                    else 0
                ),
            }

    doctor_ok = (
        doctor is not None and doctor.returncode == 0 and isinstance(doctor_body, dict)
        and doctor_body.get("ok") is True
    )
    stream_ok = stream is not None and stream["status"] == "passed"
    active_instances = None
    if instances is not None and instances.returncode == 0:
        try:
            active_instances = [json.loads(line) for line in instances.stdout.splitlines() if line.strip()]
            if any(not isinstance(instance, dict) for instance in active_instances):
                active_instances = None
        except json.JSONDecodeError:
            active_instances = None
    no_active_instances = active_instances == []
    passed = (
        parked is not None and parked.returncode == 0 and doctor_ok
        and wake_tier == expected_wake_tier and stream_ok
        and final_park is not None and final_park.returncode == 0 and no_active_instances
    )
    receipt = {
        "version": 1,
        "runner": NATIVE_PROBE_RUNNER,
        "check": args.name,
        "status": "passed" if passed else "failed",
        "target": args.target.strip(),
        "app": args.app,
        "observed_at": datetime.now(timezone.utc).isoformat(),
        "expected_wake_tier": expected_wake_tier,
        "observed_wake_tier": wake_tier,
        "park_exit_code": parked.returncode if parked is not None else None,
        "final_park_exit_code": final_park.returncode if final_park is not None else None,
        "instance_list_exit_code": instances.returncode if instances is not None else None,
        "active_instance_count": len(active_instances) if active_instances is not None else None,
        "no_active_instances": no_active_instances,
        "doctor_exit_code": doctor.returncode if doctor is not None else None,
        "doctor_ok": doctor_ok,
        "checks": checks,
        "stream": stream,
    }
    relative = f"{args.name}.json"
    write_receipt(root, relative, (json.dumps(receipt, indent=2, sort_keys=True) + "\n").encode("utf-8"))
    result = record_evidence(args.directory, args.name, args.target, relative, receipt["status"])
    print(json.dumps({**result, "observed_wake_tier": wake_tier, "expected_wake_tier": expected_wake_tier}))
    if not passed:
        raise SystemExit(1)


def oauth_probe(args):
    _, _, _, _ = load_manifest(args.directory)
    if not re.fullmatch(r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?", args.app):
        raise ValueError("--app must be a valid lowercase Gregale app slug")
    if args.confirm_staging_app != args.app:
        raise ValueError("--confirm-staging-app must exactly match --app to confirm the disposable staging target")
    if not args.target.strip() or len(args.target) > 512 or any(ord(char) < 32 for char in args.target):
        raise ValueError("--target must identify the provider, staging app, and MCP client/tool version")
    if not 1 <= args.timeout <= 1200:
        raise ValueError("--timeout must be between 1 and 1200 seconds")
    if not args.stream_tool or len(args.stream_tool) > 128 or any(ord(char) < 32 for char in args.stream_tool):
        raise ValueError("--stream-tool must identify a harmless OAuth scope probe")
    if args.arguments_file and not Path(args.arguments_file).is_file():
        raise ValueError("--arguments-file must name an existing regular file")

    token_envs = {
        "valid_token": args.valid_token_env,
        "expired_token": args.expired_token_env,
        "wrong_audience_token": args.wrong_audience_token_env,
        "insufficient_scope_token": args.insufficient_scope_token_env,
    }
    if len(set(token_envs.values())) != len(token_envs):
        raise ValueError("each OAuth case must use a distinct token environment variable")
    for case_name, env_name in token_envs.items():
        if not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", env_name or ""):
            raise ValueError(f"the {case_name} case requires a valid token environment variable name")
        if not os.environ.get(env_name, "").strip():
            raise ValueError(f"the {case_name} token environment variable is unset or empty")
    token_values = [os.environ[env_name] for env_name in token_envs.values()]
    if any(not token.strip() or token != token.strip() or any(char.isspace() for char in token) for token in token_values):
        raise ValueError("OAuth token environment variables must contain bearer tokens without whitespace")
    if len(set(token_values)) != len(token_values):
        raise ValueError("each OAuth case must use a distinct token value")

    def run_cli(command):
        try:
            return subprocess.run(
                command, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                text=True, encoding="utf-8", errors="replace", timeout=args.timeout, check=False,
            )
        except (OSError, subprocess.TimeoutExpired):
            return None

    def doctor(token_env, stream=False):
        command = [args.gregale, "mcp", "doctor", "--app", args.app, "--token-env", token_env]
        if stream:
            command.extend(["--stream-tool", args.stream_tool])
            if args.arguments_file:
                command.extend(["--arguments-file", args.arguments_file])
        command.extend(["--timeout", f"{args.timeout}s", "--json"])
        result = run_cli(command)
        if result is None:
            return None, None, {}
        try:
            report = json.loads(result.stdout)
        except json.JSONDecodeError:
            report = None
        statuses = {}
        if isinstance(report, dict) and isinstance(report.get("checks"), list):
            statuses = {
                item["name"]: item.get("status", "missing")
                for item in report["checks"]
                if isinstance(item, dict) and isinstance(item.get("name"), str)
            }
        return result, report, statuses

    cases = []
    valid_result, valid_report, valid_checks = doctor(token_envs["valid_token"], stream=True)
    valid_stream = valid_report.get("stream") if isinstance(valid_report, dict) else None
    progress = valid_stream.get("progress_ms", []) if isinstance(valid_stream, dict) else []
    valid_passed = (
        valid_result is not None and valid_result.returncode == 0
        and isinstance(valid_report, dict) and valid_report.get("ok") is True
        and all(valid_checks.get(name) == "passed" for name in (
            "oauth_resource_discovery", "modern_catalog_discovery", "live_progress",
        ))
        and isinstance(progress, list) and len(progress) >= 2
    )
    cases.append({
        "name": "valid_token",
        "expected": "accepted",
        "observed": "accepted" if valid_passed else "rejected",
        "status": "passed" if valid_passed else "failed",
        "cli_exit_code": valid_result.returncode if valid_result is not None else -1,
        "doctor_checks": {
            name: valid_checks.get(name, "missing")
            for name in ("oauth_resource_discovery", "modern_catalog_discovery", "live_progress")
        },
        "progress_events": len(progress) if isinstance(progress, list) else 0,
    })

    for case_name in ("expired_token", "wrong_audience_token"):
        result, report, statuses = doctor(token_envs[case_name])
        failed_check = None
        if isinstance(report, dict):
            failed_check = next((
                item for item in report.get("checks", [])
                if isinstance(item, dict) and item.get("name") == "modern_catalog_discovery"
            ), None)
        detail = failed_check.get("detail", "") if isinstance(failed_check, dict) else ""
        match = re.search(r"HTTP\s+(\d{3})", detail)
        status_code = int(match.group(1)) if match else None
        rejected = (
            result is not None and result.returncode != 0
            and isinstance(report, dict) and report.get("ok") is False
            and statuses.get("oauth_resource_discovery") == "passed"
            and statuses.get("modern_catalog_discovery") == "failed"
            and status_code == 401
        )
        cases.append({
            "name": case_name,
            "expected": "rejected",
            "observed": "rejected" if rejected else "accepted",
            "status": "passed" if rejected else "failed",
            "cli_exit_code": result.returncode if result is not None else -1,
            "doctor_checks": {
                name: statuses.get(name, "missing")
                for name in ("oauth_resource_discovery", "modern_catalog_discovery")
            },
            "http_status": status_code,
        })

    scope_command = [
        args.gregale, "mcp", "call", "--app", args.app,
        "--token-env", token_envs["insufficient_scope_token"],
        "--tool", args.stream_tool, "--timeout", f"{args.timeout}s", "--json",
    ]
    if args.arguments_file:
        scope_command.extend(["--arguments-file", args.arguments_file])
    scope_result = run_cli(scope_command)
    scope_output = ""
    if scope_result is not None:
        scope_output = scope_result.stdout + "\n" + scope_result.stderr
    scope_match = re.search(r"HTTP\s+(\d{3})", scope_output)
    scope_status = int(scope_match.group(1)) if scope_match else None
    scope_rejected = scope_result is not None and scope_result.returncode != 0 and scope_status == 403
    cases.append({
        "name": "insufficient_scope_token",
        "expected": "rejected",
        "observed": "rejected" if scope_rejected else "accepted",
        "status": "passed" if scope_rejected else "failed",
        "cli_exit_code": scope_result.returncode if scope_result is not None else -1,
        "http_status": scope_status,
    })

    passed = all(case["status"] == "passed" for case in cases)
    receipt = {
        "version": 1,
        "runner": OAUTH_PROBE_RUNNER,
        "check": "oauth_resource_server",
        "status": "passed" if passed else "failed",
        "target": args.target.strip(),
        "app": args.app,
        "observed_at": datetime.now(timezone.utc).isoformat(),
        "cases": cases,
    }
    relative = "oauth_resource_server.json"
    write_receipt(args.directory.resolve(), relative, (json.dumps(receipt, indent=2, sort_keys=True) + "\n").encode("utf-8"))
    result = record_evidence(args.directory, "oauth_resource_server", args.target, relative, receipt["status"])
    print(json.dumps({**result, "app": args.app, "cases": [
        {"name": case["name"], "status": case["status"], "http_status": case.get("http_status")}
        for case in cases
    ]}))
    if not passed:
        raise SystemExit(1)


class OAuthLifecycleFailure(Exception):
    def __init__(self, stage, reason):
        super().__init__(reason)
        self.stage = stage
        self.reason = reason


class _NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, request, file, code, message, headers, new_url):
        return None


_OAUTH_HTTP = build_opener(_NoRedirect())
_OAUTH_RESPONSE_LIMIT = 1 << 20


def _oauth_https_url(value, stage, field, allow_query=True):
    parsed = urlparse(value)
    if (
        parsed.scheme != "https" or not parsed.hostname or parsed.username is not None
        or parsed.password is not None or parsed.fragment or (not allow_query and parsed.query)
        or any(ord(char) <= 32 or ord(char) == 127 for char in value)
    ):
        raise OAuthLifecycleFailure(stage, f"invalid_{field}")
    return parsed


def _oauth_http(method, url, body=None, headers=None, timeout=30, stage="discovery"):
    request = Request(url, data=body, headers=headers or {}, method=method)
    try:
        response = _OAUTH_HTTP.open(request, timeout=timeout)
    except HTTPError as error:
        try:
            payload = error.read(_OAUTH_RESPONSE_LIMIT + 1)
        except OSError:
            payload = b""
        if len(payload) > _OAUTH_RESPONSE_LIMIT:
            raise OAuthLifecycleFailure(stage, "response_too_large") from error
        return error.code, payload
    except (URLError, TimeoutError, OSError, ValueError) as error:
        raise OAuthLifecycleFailure(stage, "provider_unreachable") from error
    try:
        with response:
            payload = response.read(_OAUTH_RESPONSE_LIMIT + 1)
            if len(payload) > _OAUTH_RESPONSE_LIMIT:
                raise OAuthLifecycleFailure(stage, "response_too_large")
            return response.status, payload
    except OAuthLifecycleFailure:
        raise
    except OSError as error:
        raise OAuthLifecycleFailure(stage, "provider_unreachable") from error


def _oauth_json(method, url, body=None, headers=None, timeout=30, stage="discovery"):
    status, payload = _oauth_http(method, url, body, headers, timeout, stage)
    if status != 200:
        raise OAuthLifecycleFailure(stage, f"http_{status}")
    try:
        document = json.loads(payload)
    except (UnicodeDecodeError, json.JSONDecodeError) as error:
        raise OAuthLifecycleFailure(stage, "invalid_json") from error
    if not isinstance(document, dict):
        raise OAuthLifecycleFailure(stage, "invalid_json_object")
    return document


def _oauth_metadata_urls(issuer):
    parsed = urlparse(issuer)
    path = parsed.path.rstrip("/")
    oidc_path = (path or "") + "/.well-known/openid-configuration"
    rfc8414_path = "/.well-known/oauth-authorization-server" + path
    return [
        urlunparse((parsed.scheme, parsed.netloc, oidc_path, "", "", "")),
        urlunparse((parsed.scheme, parsed.netloc, rfc8414_path, "", "", "")),
    ]


def _oauth_discover(issuer, timeout):
    last_error = None
    for metadata_url in _oauth_metadata_urls(issuer):
        try:
            document = _oauth_json("GET", metadata_url, timeout=timeout, stage="discovery")
        except OAuthLifecycleFailure as error:
            last_error = error
            if error.reason.startswith("http_"):
                continue
            raise
        if document.get("issuer") != issuer:
            last_error = OAuthLifecycleFailure("discovery", "issuer_mismatch")
            continue
        response_types = document.get("response_types_supported")
        pkce_methods = document.get("code_challenge_methods_supported")
        if not isinstance(response_types, list) or "code" not in response_types:
            raise OAuthLifecycleFailure("discovery", "authorization_code_not_supported")
        if not isinstance(pkce_methods, list) or "S256" not in pkce_methods:
            raise OAuthLifecycleFailure("discovery", "pkce_s256_not_supported")
        for field in ("authorization_endpoint", "token_endpoint", "revocation_endpoint"):
            if not isinstance(document.get(field), str):
                raise OAuthLifecycleFailure("discovery", f"missing_{field}")
            _oauth_https_url(document[field], "discovery", field)
        if document.get("grant_types_supported") is not None:
            grants = document["grant_types_supported"]
            if not isinstance(grants, list) or "authorization_code" not in grants or "refresh_token" not in grants:
                raise OAuthLifecycleFailure("discovery", "required_grants_not_supported")
        if document.get("jwks_uri") is not None:
            if not isinstance(document["jwks_uri"], str):
                raise OAuthLifecycleFailure("discovery", "invalid_jwks_uri")
            _oauth_https_url(document["jwks_uri"], "discovery", "jwks_uri")
        return document, metadata_url
    if last_error is not None:
        raise last_error
    raise OAuthLifecycleFailure("discovery", "metadata_not_found")


def _oauth_resource_metadata(resource, issuer, timeout):
    parsed = urlparse(resource)
    metadata_path = "/.well-known/oauth-protected-resource" + parsed.path
    metadata_url = urlunparse((parsed.scheme, parsed.netloc, metadata_path, "", "", ""))
    document = _oauth_json("GET", metadata_url, timeout=timeout, stage="discovery")
    issuers = document.get("authorization_servers")
    if document.get("resource") != resource or not isinstance(issuers, list) or issuer not in issuers:
        raise OAuthLifecycleFailure("discovery", "resource_metadata_does_not_advertise_configured_issuer")
    return document


def _oauth_jwks(url, timeout):
    document = _oauth_json("GET", url, timeout=timeout, stage="discovery")
    keys = document.get("keys")
    if not isinstance(keys, list) or not keys:
        raise OAuthLifecycleFailure("discovery", "jwks_has_no_keys")
    usable = {
        key.get("kid")
        for key in keys
        if isinstance(key, dict)
        and not {"d", "p", "q", "dp", "dq", "qi", "oth", "k"}.intersection(key)
        and isinstance(key.get("kid"), str)
        and key.get("kid")
        and key.get("kty") in {"RSA", "EC"}
        and key.get("use", "sig") == "sig"
        and key.get("alg", "RS256" if key.get("kty") == "RSA" else "ES256") == ("RS256" if key.get("kty") == "RSA" else "ES256")
    }
    if not usable:
        raise OAuthLifecycleFailure("discovery", "jwks_has_no_supported_signing_keys")
    return usable


def _oauth_jwt_parts(token):
    parts = token.split(".")
    if len(parts) != 3:
        raise OAuthLifecycleFailure("authorization", "access_token_is_not_a_signed_jwt")
    try:
        header_data = base64.urlsafe_b64decode(parts[0] + "=" * (-len(parts[0]) % 4))
        claims_data = base64.urlsafe_b64decode(parts[1] + "=" * (-len(parts[1]) % 4))
        header = json.loads(header_data)
        claims = json.loads(claims_data)
    except (ValueError, UnicodeDecodeError, json.JSONDecodeError) as error:
        raise OAuthLifecycleFailure("authorization", "malformed_access_token") from error
    if not isinstance(header, dict) or not isinstance(claims, dict):
        raise OAuthLifecycleFailure("authorization", "malformed_access_token")
    if header.get("alg") not in {"RS256", "ES256"} or not isinstance(header.get("kid"), str) or not header["kid"]:
        raise OAuthLifecycleFailure("authorization", "unsupported_access_token_signing_key")
    return header, claims


def _oauth_validate_access_token(token, issuer, resource, jwks_kids, stage):
    if len(token) > 32 << 10:
        raise OAuthLifecycleFailure(stage, "access_token_too_large")
    header, claims = _oauth_jwt_parts(token)
    audience = claims.get("aud")
    audiences = [audience] if isinstance(audience, str) else audience
    expiration = claims.get("exp")
    if (
        claims.get("iss") != issuer
        or not isinstance(audiences, list)
        or resource not in audiences
        or not isinstance(claims.get("sub"), str)
        or not claims["sub"]
        or type(expiration) not in {int, float}
        or not math.isfinite(expiration)
        or expiration <= time.time()
        or header["kid"] not in jwks_kids
    ):
        raise OAuthLifecycleFailure(stage, "access_token_claims_or_jwks_mismatch")
    return header["kid"]


def _oauth_token_request(endpoint, values, client_id, client_secret, timeout, stage):
    form = dict(values)
    headers = {"Accept": "application/json", "Content-Type": "application/x-www-form-urlencoded"}
    if client_secret:
        raw = f"{quote_plus(client_id, safe='')}:{quote_plus(client_secret, safe='')}".encode("ascii")
        headers["Authorization"] = "Basic " + base64.b64encode(raw).decode("ascii")
    else:
        form["client_id"] = client_id
    encoded = urlencode(form).encode("utf-8")
    if len(encoded) > _OAUTH_RESPONSE_LIMIT:
        raise OAuthLifecycleFailure(stage, "request_too_large")
    status, payload = _oauth_http("POST", endpoint, encoded, headers, timeout, stage)
    try:
        response = json.loads(payload)
    except (UnicodeDecodeError, json.JSONDecodeError):
        response = None
    if status != 200:
        return status, response if isinstance(response, dict) else None
    if not isinstance(response, dict):
        raise OAuthLifecycleFailure(stage, "invalid_token_response")
    return status, response


def _oauth_parse_redirect_uri(value):
    parsed = urlparse(value)
    if (
        parsed.scheme != "http"
        or parsed.hostname != "127.0.0.1"
        or parsed.port is None
        or not 1 <= parsed.port <= 65535
        or parsed.username is not None
        or parsed.password is not None
        or parsed.params
        or parsed.query
        or parsed.fragment
        or not parsed.path.startswith("/")
        or parsed.path == "/"
        or any(ord(char) <= 32 or ord(char) == 127 for char in parsed.path)
    ):
        raise ValueError("--redirect-uri must be a registered http://127.0.0.1:<port>/<path> loopback URI")
    return parsed


def _oauth_wait_for_code(redirect_uri, expected_state, authorization_url, args):
    parsed = _oauth_parse_redirect_uri(redirect_uri)
    callback = {}
    completed = threading.Event()

    class CallbackHandler(BaseHTTPRequestHandler):
        def log_message(self, _format, *args):
            return

        def do_GET(self):
            request = urlparse(self.path)
            query = parse_qs(request.query, keep_blank_values=True, strict_parsing=False)
            status = 400
            message = "Authorization response was not accepted. Return to the terminal."
            if request.path == parsed.path and len(self.path) <= 8192:
                returned_state = query.get("state", [])
                if returned_state == [expected_state]:
                    errors = query.get("error", [])
                    codes = query.get("code", [])
                    if len(errors) == 1 and errors[0]:
                        callback["error"] = "authorization_denied"
                        completed.set()
                        status = 200
                        message = "Authorization was declined. Return to the terminal."
                    elif len(codes) == 1 and codes[0]:
                        callback["code"] = codes[0]
                        completed.set()
                        status = 200
                        message = "Authorization received. You may close this window."
            body = message.encode("utf-8")
            self.send_response(status)
            self.send_header("Content-Type", "text/plain; charset=utf-8")
            self.send_header("Cache-Control", "no-store")
            self.send_header("Pragma", "no-cache")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

    try:
        server = HTTPServer(("127.0.0.1", parsed.port), CallbackHandler)
    except OSError as error:
        raise OAuthLifecycleFailure("authorization", "callback_listener_failed") from error
    server.timeout = 1
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        print("Open this staging authorization URL and complete the login/consent prompt:")
        print(authorization_url, flush=True)
        if args.open_browser:
            import webbrowser
            try:
                webbrowser.open(authorization_url)
            except webbrowser.Error:
                # The URL is already printed; the operator can still open it manually.
                pass
        deadline = time.monotonic() + args.timeout
        while not completed.is_set() and time.monotonic() < deadline:
            completed.wait(min(1, max(0, deadline - time.monotonic())))
        if not completed.is_set():
            raise OAuthLifecycleFailure("authorization", "callback_timeout")
        if callback.get("error"):
            raise OAuthLifecycleFailure("authorization", callback["error"])
        code = callback.get("code")
        if not code:
            raise OAuthLifecycleFailure("authorization", "callback_missing_code")
        return code
    except OSError as error:
        raise OAuthLifecycleFailure("authorization", "callback_listener_failed") from error
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=2)


def _oauth_authorization_code(args, metadata, scopes, client_secret):
    verifier = secrets.token_urlsafe(64)
    challenge = base64.urlsafe_b64encode(hashlib.sha256(verifier.encode("ascii")).digest()).rstrip(b"=").decode("ascii")
    state = secrets.token_urlsafe(32)
    query = [
        ("client_id", args.client_id),
        ("response_type", "code"),
        ("redirect_uri", args.redirect_uri),
        ("scope", " ".join(scopes)),
        ("state", state),
        ("code_challenge", challenge),
        ("code_challenge_method", "S256"),
        ("resource", args.resource),
        ("prompt", "consent"),
    ]
    for item in args.authorize_param:
        key, value = item.split("=", 1)
        if key in {name for name, _ in query}:
            raise ValueError(f"--authorize-param cannot override reserved parameter {key!r}")
        query.append((key, value))
    endpoint = metadata["authorization_endpoint"]
    parsed_endpoint = urlparse(endpoint)
    existing = parse_qs(parsed_endpoint.query, keep_blank_values=True)
    if any(key in existing for key, _ in query):
        raise OAuthLifecycleFailure("discovery", "authorization_endpoint_has_reserved_query")
    query_string = "&".join(part for part in (parsed_endpoint.query, urlencode(query)) if part)
    authorization_url = urlunparse((
        parsed_endpoint.scheme, parsed_endpoint.netloc, parsed_endpoint.path, parsed_endpoint.params,
        query_string, parsed_endpoint.fragment,
    ))
    code = _oauth_wait_for_code(args.redirect_uri, state, authorization_url, args)
    status, response = _oauth_token_request(metadata["token_endpoint"], {
        "grant_type": "authorization_code",
        "code": code,
        "redirect_uri": args.redirect_uri,
        "code_verifier": verifier,
        "resource": args.resource,
    }, args.client_id, client_secret, args.timeout, "authorization")
    if status != 200:
        raise OAuthLifecycleFailure("authorization", "authorization_code_exchange_rejected")
    return response


def _oauth_refresh(args, metadata, refresh_token, client_secret):
    return _oauth_token_request(metadata["token_endpoint"], {
        "grant_type": "refresh_token",
        "refresh_token": refresh_token,
        "resource": args.resource,
    }, args.client_id, client_secret, args.timeout, "refresh")


def _oauth_probe_access_token(args, token, stream=False, retries=0):
    env_name = "GREG_MCP_OAUTH_" + secrets.token_hex(12).upper()
    command = [args.gregale, "mcp", "doctor", "--app", args.app, "--token-env", env_name,
               "--timeout", f"{args.timeout}s", "--json"]
    if stream and args.stream_tool:
        command.extend(["--stream-tool", args.stream_tool])
        if args.arguments_file:
            command.extend(["--arguments-file", args.arguments_file])
    child_env = os.environ.copy()
    if args.client_secret_env:
        child_env.pop(args.client_secret_env, None)
    child_env[env_name] = token
    deadline = time.monotonic() + retries
    while True:
        try:
            result = subprocess.run(command, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                                    stderr=subprocess.DEVNULL, text=True, encoding="utf-8",
                                    errors="replace", timeout=args.timeout, check=False, env=child_env)
        except (OSError, subprocess.TimeoutExpired):
            result = None
        report = None
        if result is not None:
            try:
                report = json.loads(result.stdout)
            except json.JSONDecodeError:
                pass
        checks = {}
        if isinstance(report, dict) and isinstance(report.get("checks"), list):
            checks = {
                item.get("name"): item.get("status")
                for item in report["checks"] if isinstance(item, dict)
            }
        required = ["oauth_resource_discovery", "modern_catalog_discovery"]
        if stream and args.stream_tool:
            required.append("live_progress")
        progress = []
        stream_report = report.get("stream") if isinstance(report, dict) else None
        if isinstance(stream_report, dict) and isinstance(stream_report.get("progress_ms"), list):
            progress = stream_report["progress_ms"]
        passed = (
            result is not None and result.returncode == 0
            and isinstance(report, dict) and report.get("ok") is True
            and all(checks.get(name) == "passed" for name in required)
            and (not stream or not args.stream_tool or len(progress) >= 2)
        )
        if passed:
            return True, len(progress)
        if time.monotonic() >= deadline:
            return False, 0
        time.sleep(min(10, max(0, deadline - time.monotonic())))


def oauth_flow(args):
    _, _, _, _ = load_manifest(args.directory)
    if not re.fullmatch(r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?", args.app):
        raise ValueError("--app must be a valid lowercase Gregale app slug")
    if args.confirm_staging_app != args.app:
        raise ValueError("--confirm-staging-app must exactly match --app for this staging login")
    if not args.target.strip() or len(args.target) > 512 or any(ord(char) < 32 for char in args.target):
        raise ValueError("--target must identify the provider, staging app, and test client")
    if not args.client_id or len(args.client_id) > 512 or any(char.isspace() for char in args.client_id):
        raise ValueError("--client-id must be a nonempty OAuth client identifier")
    if args.previous_kid and (len(args.previous_kid) > 256 or any(ord(char) < 33 or ord(char) == 127 for char in args.previous_kid)):
        raise ValueError("--previous-kid must be a short key identifier without controls")
    if args.stream_tool and (len(args.stream_tool) > 128 or any(ord(char) < 32 for char in args.stream_tool)):
        raise ValueError("--stream-tool must identify a harmless progress-capable tool")
    if not 1 <= args.timeout <= 1200:
        raise ValueError("--timeout must be between 1 and 1200 seconds")
    if not 0 <= args.key_rotation_timeout <= 600:
        raise ValueError("--key-rotation-timeout must be between 0 and 600 seconds")
    if args.key_rotation_timeout and not args.previous_kid:
        raise ValueError("--key-rotation-timeout requires --previous-kid")
    if args.arguments_file and not Path(args.arguments_file).is_file():
        raise ValueError("--arguments-file must name an existing regular file")
    issuer = args.issuer
    if len(issuer) > 2048 or len(args.resource) > 2048 or len(args.jwks_url) > 2048:
        raise ValueError("issuer, resource, and JWKS URLs must not exceed 2048 characters")
    _oauth_https_url(issuer, "discovery", "issuer", allow_query=False)
    _oauth_https_url(args.jwks_url, "discovery", "jwks_uri", allow_query=False)
    _oauth_https_url(args.resource, "discovery", "resource", allow_query=False)
    _oauth_parse_redirect_uri(args.redirect_uri)
    scopes = args.scope
    if (
        not scopes or len(scopes) > 64
        or any(not re.fullmatch(r"[\x21-\x7e]{1,128}", scope) for scope in scopes)
        or len(set(scopes)) != len(scopes)
    ):
        raise ValueError("provide one or more distinct OAuth scopes without whitespace")
    if len(args.authorize_param) > 32:
        raise ValueError("no more than 32 provider-specific authorization parameters may be supplied")
    reserved_parameters = {"client_id", "response_type", "redirect_uri", "scope", "state", "code_challenge", "code_challenge_method", "resource", "prompt"}
    for item in args.authorize_param:
        if "=" not in item:
            raise ValueError("--authorize-param values must use NAME=VALUE")
        key, value = item.split("=", 1)
        if not re.fullmatch(r"[A-Za-z][A-Za-z0-9_.-]{0,63}", key) or not value or len(value) > 512 or any(ord(char) < 32 for char in value):
            raise ValueError("--authorize-param must contain a valid short parameter name and value")
        if key in reserved_parameters:
            raise ValueError(f"--authorize-param cannot override reserved parameter {key!r}")
    client_secret = ""
    if args.client_secret_env:
        if not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", args.client_secret_env):
            raise ValueError("--client-secret-env must name an environment variable")
        client_secret = os.environ.get(args.client_secret_env, "")
        if not client_secret:
            raise ValueError("the named OAuth client secret environment variable is unset or empty")

    discovery = {"status": "failed", "response_type": "", "pkce_method": "", "jwks_key_count": 0, "resource_matches": False, "issuer_advertised": False}
    authorization = {"status": "failed", "state_validated": False, "code_exchange": "failed", "resource_probe": "failed", "progress_events": 0, "consent_prompt_requested": True}
    refresh = {"status": "failed", "token_issued": False, "resource_probe": "failed", "rotation": "stable"}
    revocation = {"status": "failed", "endpoint_response": 0, "refresh_rejected": False}
    key_rotation = {
        "status": "failed" if args.previous_kid else "not_requested",
        "previous_kid": args.previous_kid or "", "current_kid": "", "jwks_confirmed": False,
    }
    failure = None
    try:
        resource_metadata = _oauth_resource_metadata(args.resource, issuer, args.timeout)
        metadata, _ = _oauth_discover(issuer, args.timeout)
        if metadata.get("jwks_uri") and metadata["jwks_uri"] != args.jwks_url:
            raise OAuthLifecycleFailure("discovery", "configured_jwks_url_does_not_match_provider_metadata")
        token_methods = metadata.get("token_endpoint_auth_methods_supported")
        if isinstance(token_methods, list) and token_methods:
            method = "client_secret_basic" if client_secret else "none"
            if method not in token_methods:
                raise OAuthLifecycleFailure("discovery", "client_auth_method_not_supported")
        initial_kids = _oauth_jwks(args.jwks_url, args.timeout)
        discovery.update({"status": "passed", "response_type": "code", "pkce_method": "S256", "jwks_key_count": len(initial_kids), "resource_matches": resource_metadata.get("resource") == args.resource, "issuer_advertised": issuer in resource_metadata.get("authorization_servers", [])})

        token_response = _oauth_authorization_code(args, metadata, scopes, client_secret)
        access_token = token_response.get("access_token")
        refresh_token = token_response.get("refresh_token")
        if (
            not isinstance(access_token, str) or not access_token or len(access_token) > 32 << 10
            or not isinstance(refresh_token, str) or not refresh_token or len(refresh_token) > 32 << 10
            or str(token_response.get("token_type", "")).lower() != "bearer"
        ):
            raise OAuthLifecycleFailure("authorization", "access_or_refresh_token_missing")
        current_kids = _oauth_jwks(args.jwks_url, args.timeout)
        key_id = _oauth_validate_access_token(access_token, issuer, args.resource, current_kids, "authorization")
        authorization["state_validated"] = True
        authorization["code_exchange"] = "passed"
        rotation_wait = args.key_rotation_timeout if args.previous_kid else 0
        catalog_ok, _ = _oauth_probe_access_token(args, access_token, retries=rotation_wait)
        if not catalog_ok:
            raise OAuthLifecycleFailure("authorization", "resource_rejected_authorization_code_token")
        authorization["resource_probe"] = "passed"
        if args.stream_tool:
            stream_ok, progress_events = _oauth_probe_access_token(args, access_token, stream=True)
            if not stream_ok:
                raise OAuthLifecycleFailure("authorization", "harmless_progress_tool_rejected")
            authorization["progress_events"] = progress_events
        authorization["status"] = "passed"
        if args.previous_kid:
            if key_id == args.previous_kid or key_id not in current_kids:
                raise OAuthLifecycleFailure("key_rotation", "new_signing_key_not_published")
            key_rotation.update({"status": "passed", "previous_kid": args.previous_kid, "current_kid": key_id, "jwks_confirmed": True})

        refresh_status, refreshed = _oauth_refresh(args, metadata, refresh_token, client_secret)
        if refresh_status != 200:
            raise OAuthLifecycleFailure("refresh", "refresh_grant_rejected")
        refreshed_access = refreshed.get("access_token")
        rotated_refresh = refreshed.get("refresh_token")
        if (
            not isinstance(refreshed_access, str) or not refreshed_access
            or str(refreshed.get("token_type", "")).lower() != "bearer"
        ):
            raise OAuthLifecycleFailure("refresh", "refresh_response_missing_access_token")
        refresh_kids = _oauth_jwks(args.jwks_url, args.timeout)
        _oauth_validate_access_token(refreshed_access, issuer, args.resource, refresh_kids, "refresh")
        if "refresh_token" in refreshed:
            if not isinstance(rotated_refresh, str) or len(rotated_refresh) > 32 << 10:
                raise OAuthLifecycleFailure("refresh", "invalid_rotated_refresh_token")
            if not rotated_refresh:
                raise OAuthLifecycleFailure("refresh", "invalid_rotated_refresh_token")
            refresh["rotation"] = "rotated" if rotated_refresh != refresh_token else "stable"
            active_refresh = rotated_refresh
            if refresh["rotation"] == "rotated":
                replay_status, replay = _oauth_refresh(args, metadata, refresh_token, client_secret)
                if replay_status != 400 or not isinstance(replay, dict) or replay.get("error") != "invalid_grant":
                    raise OAuthLifecycleFailure("refresh", "rotated_refresh_token_was_reusable")
        else:
            active_refresh = refresh_token
            refresh["rotation"] = "stable"
        accepted_refresh, _ = _oauth_probe_access_token(args, refreshed_access)
        if not accepted_refresh:
            raise OAuthLifecycleFailure("refresh", "resource_rejected_refreshed_token")
        refresh.update({"status": "passed", "token_issued": True, "resource_probe": "passed"})

        revoke_status, _ = _oauth_http("POST", metadata["revocation_endpoint"], urlencode({
            "token": active_refresh, "token_type_hint": "refresh_token",
            **({} if client_secret else {"client_id": args.client_id}),
        }).encode("utf-8"), {
            "Accept": "application/json", "Content-Type": "application/x-www-form-urlencoded",
            **({"Authorization": "Basic " + base64.b64encode(f"{quote_plus(args.client_id, safe='')}:{quote_plus(client_secret, safe='')}".encode("ascii")).decode("ascii")} if client_secret else {}),
        }, args.timeout, "revocation")
        if revoke_status != 200:
            raise OAuthLifecycleFailure("revocation", "revocation_endpoint_rejected_token")
        revocation["endpoint_response"] = revoke_status
        rejected_status, rejected_body = _oauth_refresh(args, metadata, active_refresh, client_secret)
        if rejected_status != 400 or not isinstance(rejected_body, dict) or rejected_body.get("error") != "invalid_grant":
            raise OAuthLifecycleFailure("revocation", "revoked_refresh_token_was_not_rejected")
        revocation.update({"status": "passed", "refresh_rejected": True})
    except OAuthLifecycleFailure as error:
        failure = {"stage": error.stage, "reason": error.reason}
    except (OSError, ValueError, TypeError) as error:
        # Do not include provider-controlled error text or credential values in evidence.
        failure = {"stage": "qualification", "reason": "local_probe_error"}

    passed = (
        failure is None
        and discovery["status"] == "passed"
        and authorization["status"] == "passed"
        and refresh["status"] == "passed"
        and revocation["status"] == "passed"
        and key_rotation["status"] in {"passed", "not_requested"}
    )
    receipt = {
        "version": 1,
        "runner": OAUTH_LIFECYCLE_RUNNER,
        "check": "oauth_provider_lifecycle",
        "status": "passed" if passed else "failed",
        "target": args.target.strip(),
        "app": args.app,
        "issuer": issuer,
        "observed_at": datetime.now(timezone.utc).isoformat(),
        "discovery": discovery,
        "authorization": authorization,
        "refresh": refresh,
        "revocation": revocation,
        "key_rotation": key_rotation,
    }
    relative = "oauth_provider_lifecycle.json"
    write_receipt(args.directory.resolve(), relative, (json.dumps(receipt, indent=2, sort_keys=True) + "\n").encode("utf-8"))
    result = record_evidence(args.directory, "oauth_provider_lifecycle", args.target, relative, receipt["status"])
    summary = {"ok": passed, "name": "oauth_provider_lifecycle", "status": receipt["status"], "artifact": result["artifact"], "sha256": result["sha256"], "key_rotation": key_rotation["status"]}
    if failure:
        summary["failure"] = failure
    print(json.dumps(summary))
    if not passed:
        raise SystemExit(1)


def _sdk_loopback_url(value, field):
    try:
        parsed = urlparse(value)
        port = parsed.port
    except ValueError as error:
        raise ValueError(f"{field} must be a valid loopback MCP URL") from error
    if (
        parsed.scheme != "http" or parsed.hostname != "127.0.0.1" or port is None
        or not 1 <= port <= 65535 or parsed.username is not None or parsed.password is not None
        or parsed.path != "/mcp" or parsed.params or parsed.query or parsed.fragment
        or any(ord(char) <= 32 or ord(char) == 127 for char in value)
    ):
        raise ValueError(f"{field} must use http://127.0.0.1:<port>/mcp without credentials or query data")
    return value


def sdk_interop(args):
    load_manifest(args.directory)
    if not args.target.strip() or len(args.target) > 512 or any(ord(char) < 32 for char in args.target):
        raise ValueError("--target must identify the local starter source and official SDK version")
    if not 1 <= args.timeout <= 600:
        raise ValueError("--timeout must be between 1 and 600 seconds")
    endpoints = {
        "node": _sdk_loopback_url(args.node_url, "--node-url"),
        "go": _sdk_loopback_url(args.go_url, "--go-url"),
        "python": _sdk_loopback_url(args.python_url, "--python-url"),
    }
    client_dir = Path(__file__).resolve().parent / "mcp-sdk-client"
    script = client_dir / "interop.mjs"
    package_manifest = client_dir / "node_modules" / "@modelcontextprotocol" / "client" / "package.json"
    if not script.is_file() or not package_manifest.is_file():
        raise ValueError("install the pinned client with npm ci --prefix scripts/ops/mcp-sdk-client --ignore-scripts")
    command = ["node", str(script)]
    for runtime, option in (("node", "--node-url"), ("go", "--go-url"), ("python", "--python-url")):
        command.extend([option, endpoints[runtime]])
    command.extend(["--target", args.target.strip()])
    try:
        result = subprocess.run(
            command, cwd=client_dir, env={"PATH": os.environ.get("PATH", os.defpath)},
            stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
            text=True, encoding="utf-8", errors="replace", timeout=args.timeout, check=False,
        )
    except (OSError, subprocess.TimeoutExpired):
        result = None

    receipt = None
    if result is not None:
        try:
            receipt = json.loads(result.stdout)
        except json.JSONDecodeError:
            receipt = None
    valid_runner_output = (
        isinstance(receipt, dict)
        and receipt.get("runner") == SDK_INTEROP_RUNNER
        and receipt.get("check") == "official_sdk_interop"
        and receipt.get("target") == args.target.strip()
        and receipt.get("sdk") == {"package": "@modelcontextprotocol/client", "version": SDK_INTEROP_VERSION}
        and receipt.get("status") in {"passed", "failed"}
    )
    if not valid_runner_output:
        receipt = {
            "version": 1,
            "runner": SDK_INTEROP_RUNNER,
            "check": "official_sdk_interop",
            "status": "failed",
            "target": args.target.strip(),
            "observed_at": datetime.now(timezone.utc).isoformat(),
            "sdk": {"package": "@modelcontextprotocol/client", "version": SDK_INTEROP_VERSION},
            "runtimes": [],
            "failures": [{"runtime": "runner", "mode": "execution", "stage": "sdk_client", "reason": "sdk_runner_failed"}],
        }
    elif result is None or result.returncode != 0:
        receipt["status"] = "failed"
        if not receipt.get("failures"):
            receipt["failures"] = [{"runtime": "runner", "mode": "execution", "stage": "sdk_client", "reason": "sdk_runner_failed"}]

    relative = "official_sdk_interop.json"
    write_receipt(args.directory.resolve(), relative, (json.dumps(receipt, indent=2, sort_keys=True) + "\n").encode("utf-8"))
    result_row = record_evidence(args.directory, "official_sdk_interop", args.target, relative, receipt["status"])
    summary = {
        "ok": receipt["status"] == "passed",
        "name": "official_sdk_interop",
        "status": receipt["status"],
        "artifact": result_row["artifact"],
        "sha256": result_row["sha256"],
        "sdk_version": SDK_INTEROP_VERSION,
        "runtimes": [item.get("runtime") for item in receipt.get("runtimes", []) if isinstance(item, dict)],
    }
    print(json.dumps(summary))
    if receipt["status"] != "passed":
        raise SystemExit(1)


def _tasks_restart_failure_receipt(target):
    return {
        "version": 1,
        "runner": TASKS_RESTART_RUNNER,
        "check": "tasks_restart_replicas",
        "status": "failed",
        "target": target,
        "observed_at": datetime.now(timezone.utc).isoformat(),
        "checks": {
            "initial_worker_crash": "failed",
            "two_replica_recovery": "failed",
            "stale_lease_fenced": "failed",
            "result_owner_isolation": "failed",
            "final_attempt_input_resume": "failed",
        },
        "observations": {
            "replacement_replicas": 0,
            "recovery_attempt": 0,
            "recovered_handler_executions": 0,
            "final_attempt": 0,
            "resumed_attempt": 0,
        },
        "failures": [{"stage": "qualification_runner", "reason": "qualification_failed"}],
    }


def tasks_restart(args):
    load_manifest(args.directory)
    target = args.target.strip()
    if not target or len(target) > 512 or any(ord(char) < 32 for char in target):
        raise ValueError("--target must identify the starter source and disposable PostgreSQL qualification target")
    if not 1 <= args.timeout <= 600:
        raise ValueError("--timeout must be between 1 and 600 seconds")
    if not args.confirm_disposable_database:
        raise ValueError("--confirm-disposable-database is required because the runner creates and drops one isolated schema")
    database_url = os.environ.get("MCP_TASKS_TEST_DATABASE_URL", "")
    if not database_url or urlparse(database_url).scheme not in {"postgres", "postgresql"}:
        raise ValueError("MCP_TASKS_TEST_DATABASE_URL must contain a PostgreSQL connection URL")

    starter = args.starter_dir.resolve()
    runner = starter / "test" / "tasks-restart-qualification.mjs"
    pg_package = starter / "node_modules" / "pg" / "package.json"
    if not runner.is_file() or runner.is_symlink() or not pg_package.is_file() or pg_package.is_symlink():
        raise ValueError("--starter-dir must be a materialized MCP Node starter with npm dependencies installed")

    try:
        result = subprocess.run(
            [args.node, str(runner), "--target", target, "--confirm-disposable-database"],
            cwd=starter,
            env={"PATH": os.environ.get("PATH", os.defpath), "MCP_TASKS_TEST_DATABASE_URL": database_url},
            stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            text=True,
            encoding="utf-8",
            errors="replace",
            timeout=args.timeout,
            check=False,
        )
    except (OSError, subprocess.TimeoutExpired):
        result = None

    receipt = None
    if result is not None:
        try:
            receipt = json.loads(result.stdout)
        except json.JSONDecodeError:
            receipt = None
    try:
        CHECKER.validate_tasks_restart_receipt(receipt, "tasks_restart_replicas", target, require_passed=False)
    except (ValueError, TypeError):
        receipt = _tasks_restart_failure_receipt(target)
    if result is None or result.returncode != 0:
        if receipt["status"] == "passed":
            receipt["status"] = "failed"
            receipt["failures"].append({"stage": "qualification_runner", "reason": "qualification_failed"})

    relative = "tasks_restart_replicas.json"
    write_receipt(args.directory.resolve(), relative, (json.dumps(receipt, indent=2, sort_keys=True) + "\n").encode("utf-8"))
    result_row = record_evidence(args.directory, "tasks_restart_replicas", target, relative, receipt["status"])
    summary = {
        "ok": receipt["status"] == "passed",
        "name": "tasks_restart_replicas",
        "status": receipt["status"],
        "artifact": result_row["artifact"],
        "sha256": result_row["sha256"],
        "replacement_replicas": receipt["observations"]["replacement_replicas"],
        "recovery_attempt": receipt["observations"]["recovery_attempt"],
    }
    print(json.dumps(summary))
    if receipt["status"] != "passed":
        raise SystemExit(1)


TASKS_SCALE_ZERO_CHECKS = {
    "observer_app_linked",
    "observer_app_ready_before",
    "observer_app_ready_throughout",
    "observer_heartbeat_fresh_before",
    "observer_heartbeat_fresh_throughout",
    "scale_to_zero_policy",
    "zero_workers_before",
    "fresh_zero_metric_before",
    "backlog_metric_observed",
    "worker_scaled_up",
    "backlog_and_worker_observed_together",
    "task_completed",
    "fresh_zero_metric_after",
    "workers_returned_to_zero",
}


class TasksScaleZeroFailure(Exception):
    def __init__(self, stage):
        super().__init__(stage)
        self.stage = stage


def _tasks_scale_zero_cli(args, arguments, stage, timeout=None):
    try:
        result = subprocess.run(
            [args.gregale, *arguments],
            stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            text=True,
            encoding="utf-8",
            errors="replace",
            timeout=timeout or min(args.timeout, 30),
            check=False,
        )
    except (OSError, subprocess.TimeoutExpired) as error:
        raise TasksScaleZeroFailure(stage) from error
    if result.returncode != 0:
        raise TasksScaleZeroFailure(stage)
    try:
        value = json.loads(result.stdout)
    except json.JSONDecodeError as error:
        raise TasksScaleZeroFailure(stage) from error
    if not isinstance(value, dict):
        raise TasksScaleZeroFailure(stage)
    return value


def _tasks_scale_zero_metric(status, name, freshness_failure="metric_not_fresh"):
    metrics = status.get("metrics")
    if not isinstance(metrics, list):
        raise TasksScaleZeroFailure("metric_status_invalid")
    matches = [metric for metric in metrics if isinstance(metric, dict) and metric.get("name") == name]
    if len(matches) != 1:
        raise TasksScaleZeroFailure("metric_status_invalid")
    metric = matches[0]
    value = metric.get("value")
    if (
        type(metric.get("present")) is not bool
        or type(metric.get("stale")) is not bool
        or not isinstance(value, (int, float))
        or isinstance(value, bool)
        or not math.isfinite(value)
        or value < 0
    ):
        raise TasksScaleZeroFailure("metric_status_invalid")
    if not metric["present"] or metric["stale"]:
        raise TasksScaleZeroFailure(freshness_failure)
    return value


def _tasks_scale_zero_observer_heartbeat(status):
    value = _tasks_scale_zero_metric(
        status,
        "mcp_tasks_observer_heartbeat",
        freshness_failure="observer_heartbeat_not_fresh",
    )
    if value != 1:
        raise TasksScaleZeroFailure("observer_heartbeat_unhealthy")


def _tasks_scale_zero_observer_app(status, worker_app, expected_slug=None):
    observer = status.get("observer_app")
    if not isinstance(observer, dict):
        raise TasksScaleZeroFailure("observer_app_status_invalid")
    slug = observer.get("app_slug")
    if (
        observer.get("linked") is not True
        or observer.get("ready") is not True
        or not isinstance(slug, str)
        or not re.fullmatch(r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?", slug)
        or slug == worker_app
        or (expected_slug is not None and slug != expected_slug)
        or observer.get("status") != "active"
        or observer.get("deployment_availability") != "live"
        or type(observer.get("min_replicas")) is not int
        or observer["min_replicas"] < 1
        or type(observer.get("replicas")) is not int
        or observer["replicas"] < 1
    ):
        raise TasksScaleZeroFailure("observer_app_not_ready")
    return slug, observer["min_replicas"], observer["replicas"]


def _tasks_scale_zero_worker_count(status, app):
    if status.get("slug") != app or type(status.get("replicas")) is not int or status["replicas"] < 0:
        raise TasksScaleZeroFailure("worker_status_invalid")
    if type(status.get("min_replicas")) is not int or status["min_replicas"] != 0:
        raise TasksScaleZeroFailure("scale_to_zero_policy_changed")
    if (
        status.get("metric") != "custom"
        or type(status.get("max_replicas")) is not int
        or status["max_replicas"] < 1
    ):
        raise TasksScaleZeroFailure("scale_to_zero_policy_changed")
    return status["replicas"]


def _tasks_scale_zero_policy(status, app):
    if status.get("app_slug") != app or status.get("scale_to_zero_configured") is not True:
        raise TasksScaleZeroFailure("scale_to_zero_policy_changed")
    scaling = status.get("scaling")
    if (
        not isinstance(scaling, dict)
        or type(scaling.get("min")) is not int
        or scaling["min"] != 0
        or scaling.get("metric") != "custom"
        or scaling.get("name") != "mcp_tasks_outstanding"
        or type(scaling.get("target")) not in {int, float}
        or scaling["target"] <= 0
    ):
        raise TasksScaleZeroFailure("scale_to_zero_policy_changed")
    freshness = status.get("freshness_seconds")
    if type(freshness) is not int or freshness <= 0:
        raise TasksScaleZeroFailure("metric_freshness_invalid")
    return freshness


def _tasks_scale_zero_task_id(exchange):
    task = exchange.get("result")
    if (
        not isinstance(task, dict)
        or task.get("resultType") != "task"
        or not isinstance(task.get("taskId"), str)
        or not task["taskId"]
        or len(task["taskId"]) > 256
        or any(ord(char) < 32 or ord(char) == 127 for char in task["taskId"])
    ):
        raise TasksScaleZeroFailure("task_submission_invalid")
    return task["taskId"]


def _tasks_scale_zero_failure_receipt(target, app, stage, checks=None, observations=None, observer_app=""):
    receipt_checks = {name: "failed" for name in TASKS_SCALE_ZERO_CHECKS}
    if checks:
        receipt_checks.update({name: value for name, value in checks.items() if name in receipt_checks})
    return {
        "version": 2,
        "runner": TASKS_SCALE_ZERO_RUNNER,
        "check": "tasks_scale_from_zero",
        "status": "failed",
        "target": target,
        "app": app,
        "observer_app": observer_app,
        "observed_at": datetime.now(timezone.utc).isoformat(),
        "checks": receipt_checks,
        "observations": {
            "initial_replicas": 0,
            "peak_replicas": 0,
            "peak_outstanding": 0,
            "final_replicas": 0,
            "freshness_seconds": 0,
            "completed_tasks": 0,
            "observer_min_replicas": 0,
            "observer_initial_replicas": 0,
            **(observations or {}),
        },
        "failures": [{"stage": stage, "reason": "qualification_failed"}],
    }


def tasks_scale_zero(args):
    load_manifest(args.directory)
    app = args.app
    target = args.target.strip()
    if not re.fullmatch(r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?", app):
        raise ValueError("--app must be a valid lowercase Gregale app slug")
    if args.confirm_staging_app != app:
        raise ValueError("--confirm-staging-app must exactly match --app to authorize staging task execution")
    if not target or len(target) > 512 or any(ord(char) < 32 for char in target):
        raise ValueError("--target must identify the reviewed build, staging app, and task probe")
    if not args.tool.strip() or len(args.tool) > 256 or any(ord(char) < 32 for char in args.tool):
        raise ValueError("--tool must select a harmless asynchronous MCP task probe")
    if not 1 <= args.timeout <= 3600:
        raise ValueError("--timeout must be between 1 and 3600 seconds")
    if not 1 <= args.poll_interval <= 60:
        raise ValueError("--poll-interval must be between 1 and 60 seconds")
    if args.arguments_file:
        arguments_path = Path(args.arguments_file)
        if not arguments_path.is_file() or arguments_path.is_symlink():
            raise ValueError("--arguments-file must name an existing regular file")
    if args.token_env:
        token = os.environ.get(args.token_env, "")
        if not token or "\n" in token or "\r" in token:
            raise ValueError("the named MCP token environment variable must contain a valid token")

    checks = {name: "failed" for name in TASKS_SCALE_ZERO_CHECKS}
    observations = {
        "initial_replicas": 0,
        "peak_replicas": 0,
        "peak_outstanding": 0,
        "final_replicas": 0,
        "freshness_seconds": 0,
        "completed_tasks": 0,
        "observer_min_replicas": 0,
        "observer_initial_replicas": 0,
    }
    task_id = ""
    observer_app = ""
    failure_stage = "qualification_runner"
    try:
        status_args = ["mcp", "tasks", "status", "--app", app, "--json"]
        worker_args = ["workers", "status", app, "--json"]
        metric_status = _tasks_scale_zero_cli(args, status_args, "initial_metric_status_failed")
        observations["freshness_seconds"] = _tasks_scale_zero_policy(metric_status, app)
        checks["scale_to_zero_policy"] = "passed"
        observer_app, observer_min, observer_replicas = _tasks_scale_zero_observer_app(metric_status, app)
        observations["observer_min_replicas"] = observer_min
        observations["observer_initial_replicas"] = observer_replicas
        checks["observer_app_linked"] = "passed"
        checks["observer_app_ready_before"] = "passed"
        _tasks_scale_zero_observer_heartbeat(metric_status)
        checks["observer_heartbeat_fresh_before"] = "passed"
        initial_outstanding = _tasks_scale_zero_metric(metric_status, "mcp_tasks_outstanding")
        _tasks_scale_zero_metric(metric_status, "mcp_tasks_oldest_age_seconds")
        initial_without_compatible_worker = _tasks_scale_zero_metric(
            metric_status, "mcp_tasks_without_compatible_worker"
        )
        if initial_without_compatible_worker != 0:
            raise TasksScaleZeroFailure("initial_task_has_no_compatible_worker")
        if initial_outstanding != 0:
            raise TasksScaleZeroFailure("initial_backlog_not_empty")
        checks["fresh_zero_metric_before"] = "passed"

        worker_status = _tasks_scale_zero_cli(args, worker_args, "initial_worker_status_failed")
        initial_replicas = _tasks_scale_zero_worker_count(worker_status, app)
        observations["initial_replicas"] = initial_replicas
        if initial_replicas != 0:
            raise TasksScaleZeroFailure("worker_pool_not_at_zero")
        checks["zero_workers_before"] = "passed"

        call_args = ["mcp", "call", "--app", app, "--tool", args.tool, "--tasks",
                     "--timeout", "180s"]
        if args.arguments_file:
            call_args.extend(["--arguments-file", args.arguments_file])
        if args.token_env:
            call_args.extend(["--token-env", args.token_env])
        exchange = _tasks_scale_zero_cli(args, call_args, "task_submission_failed", timeout=min(args.timeout, 185))
        task_id = _tasks_scale_zero_task_id(exchange)
        deadline = time.monotonic() + args.timeout
        backlog_seen = False
        task_complete = False

        while time.monotonic() < deadline:
            metric_status = _tasks_scale_zero_cli(args, status_args, "metric_poll_failed")
            _tasks_scale_zero_policy(metric_status, app)
            _tasks_scale_zero_observer_app(metric_status, app, observer_app)
            _tasks_scale_zero_observer_heartbeat(metric_status)
            outstanding = _tasks_scale_zero_metric(metric_status, "mcp_tasks_outstanding")
            _tasks_scale_zero_metric(metric_status, "mcp_tasks_oldest_age_seconds")
            without_compatible_worker = _tasks_scale_zero_metric(
                metric_status, "mcp_tasks_without_compatible_worker"
            )
            if outstanding > 0:
                observations["peak_outstanding"] = max(observations["peak_outstanding"], int(math.ceil(outstanding)))
            worker_status = _tasks_scale_zero_cli(args, worker_args, "worker_poll_failed")
            replicas = _tasks_scale_zero_worker_count(worker_status, app)
            observations["peak_replicas"] = max(observations["peak_replicas"], replicas)
            task_status = _tasks_scale_zero_cli(
                args,
                ["mcp", "task-get", "--app", app, "--task-id", task_id,
                 "--timeout", "15s", *(["--token-env", args.token_env] if args.token_env else [])],
                "task_status_poll_failed",
                timeout=min(args.timeout, 20),
            )
            if task_status.get("taskId") != task_id or not isinstance(task_status.get("status"), str):
                raise TasksScaleZeroFailure("task_status_invalid")
            state = task_status["status"]
            if state in {"failed", "cancelled", "input_required"}:
                raise TasksScaleZeroFailure("task_did_not_complete")
            if outstanding > 0:
                checks["backlog_metric_observed"] = "passed"
            if replicas > 0:
                checks["worker_scaled_up"] = "passed"
            if outstanding > 0 and replicas > 0 and state == "working" and without_compatible_worker == 0:
                backlog_seen = True
                checks["backlog_and_worker_observed_together"] = "passed"
            if state == "completed":
                if not backlog_seen:
                    raise TasksScaleZeroFailure("backlog_or_scale_up_not_observed")
                task_complete = True
                observations["completed_tasks"] = 1
                checks["task_completed"] = "passed"
                break
            if state != "working":
                raise TasksScaleZeroFailure("task_status_invalid")
            time.sleep(min(args.poll_interval, max(0, deadline - time.monotonic())))

        if not task_complete:
            raise TasksScaleZeroFailure("task_completion_timeout")

        while time.monotonic() < deadline:
            metric_status = _tasks_scale_zero_cli(args, status_args, "drain_metric_poll_failed")
            _tasks_scale_zero_policy(metric_status, app)
            _tasks_scale_zero_observer_app(metric_status, app, observer_app)
            _tasks_scale_zero_observer_heartbeat(metric_status)
            outstanding = _tasks_scale_zero_metric(metric_status, "mcp_tasks_outstanding")
            _tasks_scale_zero_metric(metric_status, "mcp_tasks_oldest_age_seconds")
            without_compatible_worker = _tasks_scale_zero_metric(
                metric_status, "mcp_tasks_without_compatible_worker"
            )
            if without_compatible_worker != 0:
                raise TasksScaleZeroFailure("task_has_no_compatible_worker")
            worker_status = _tasks_scale_zero_cli(args, worker_args, "drain_worker_poll_failed")
            replicas = _tasks_scale_zero_worker_count(worker_status, app)
            observations["final_replicas"] = replicas
            if outstanding == 0:
                checks["fresh_zero_metric_after"] = "passed"
            if outstanding == 0 and replicas == 0:
                observations["final_replicas"] = replicas
                checks["workers_returned_to_zero"] = "passed"
                break
            time.sleep(min(args.poll_interval, max(0, deadline - time.monotonic())))

        if checks["workers_returned_to_zero"] != "passed":
            raise TasksScaleZeroFailure("worker_scale_down_timeout")
        checks["observer_app_ready_throughout"] = "passed"
        checks["observer_heartbeat_fresh_throughout"] = "passed"
    except TasksScaleZeroFailure as error:
        failure_stage = error.stage
        if task_id:
            cancel_args = ["mcp", "task-cancel", "--app", app, "--task-id", task_id, "--timeout", "10s"]
            if args.token_env:
                cancel_args.extend(["--token-env", args.token_env])
            try:
                subprocess.run(
                    [args.gregale, *cancel_args], stdin=subprocess.DEVNULL,
                    stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                    timeout=12, check=False,
                )
            except (OSError, subprocess.TimeoutExpired):
                pass

    passed = all(value == "passed" for value in checks.values())
    if passed:
        receipt = {
            "version": 2,
            "runner": TASKS_SCALE_ZERO_RUNNER,
            "check": "tasks_scale_from_zero",
            "status": "passed",
            "target": target,
            "app": app,
            "observer_app": observer_app,
            "observed_at": datetime.now(timezone.utc).isoformat(),
            "checks": checks,
            "observations": observations,
            "failures": [],
        }
    else:
        receipt = _tasks_scale_zero_failure_receipt(
            target, app, failure_stage, checks=checks, observations=observations,
            observer_app=observer_app,
        )
    try:
        CHECKER.validate_tasks_scale_zero_receipt(
            receipt, "tasks_scale_from_zero", target, require_passed=False,
        )
    except (ValueError, TypeError) as error:
        raise RuntimeError("generated task scale-from-zero receipt failed validation") from error
    relative = "tasks_scale_from_zero.json"
    write_receipt(args.directory.resolve(), relative, (json.dumps(receipt, indent=2, sort_keys=True) + "\n").encode("utf-8"))
    result_row = record_evidence(args.directory, "tasks_scale_from_zero", target, relative, receipt["status"])
    summary = {
        "ok": passed,
        "name": "tasks_scale_from_zero",
        "status": receipt["status"],
        "artifact": result_row["artifact"],
        "sha256": result_row["sha256"],
        "peak_replicas": observations["peak_replicas"],
        "peak_outstanding": observations["peak_outstanding"],
        "final_replicas": observations["final_replicas"],
    }
    if not passed:
        summary["failure_stage"] = failure_stage
    print(json.dumps(summary))
    if not passed:
        raise SystemExit(1)


def status(args):
    _, _, evidence, by_name = load_manifest(args.directory)
    rows = [
        {"name": name, "status": by_name[name].get("status", "pending"), "target": by_name[name].get("target", "")}
        for name in sorted(CHECKER.REQUIRED)
    ]
    complete = all(row["status"] == "passed" for row in rows)
    result = {"ok": False, "complete": complete, "commit": evidence["commit"], "checks": rows}
    if complete:
        try:
            result.update(CHECKER.check(args.directory.resolve() / "manifest.json", args.commit or evidence["commit"]))
        except (ValueError, KeyError, TypeError, OSError) as error:
            result["error"] = str(error)
    else:
        result["error"] = "qualification is incomplete; record every required check as passed"
    print(json.dumps(result))
    if not result.get("ok"):
        raise SystemExit(1)


def check(args):
    root, _, evidence, _ = load_manifest(args.directory)
    try:
        result = CHECKER.check(root / "manifest.json", args.commit or evidence["commit"])
    except (ValueError, KeyError, TypeError, OSError) as error:
        print(json.dumps({"ok": False, "error": str(error)}))
        raise SystemExit(1)
    print(json.dumps(result))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)

    init_parser = commands.add_parser("init", help="create an empty evidence manifest")
    init_parser.add_argument("--dir", dest="directory", type=Path, required=True, help="private qualification evidence directory")
    init_parser.add_argument("--commit", required=True, help="full reviewed source commit SHA")
    init_parser.set_defaults(run=init)

    record_parser = commands.add_parser("record", help="hash a redacted receipt and update one evidence row")
    record_parser.add_argument("--dir", dest="directory", type=Path, required=True, help="qualification evidence directory")
    record_parser.add_argument("--name", required=True, help="required qualification row name")
    record_parser.add_argument("--target", required=True, help="test app, native host/build, or client version")
    record_parser.add_argument("--artifact", required=True, help="redacted receipt path relative to --dir")
    record_parser.add_argument("--status", choices=("passed", "failed"), required=True)
    record_parser.set_defaults(run=record)

    probe_parser = commands.add_parser("probe", help="run a guarded native staging wake qualification")
    probe_parser.add_argument("--dir", dest="directory", type=Path, required=True, help="private qualification evidence directory")
    probe_parser.add_argument("--name", required=True, choices=sorted(NATIVE_WAKE_TIERS), help="native wake qualification row")
    probe_parser.add_argument("--app", required=True, help="staging Gregale app slug to park and wake")
    probe_parser.add_argument("--confirm-staging-app", required=True, help="must exactly match --app; confirms the app is disposable staging")
    probe_parser.add_argument("--target", required=True, help="native host/build and test app identifiers")
    probe_parser.add_argument("--stream-tool", required=True, help="explicit harmless tool used to verify live progress")
    probe_parser.add_argument("--token-env", help="environment variable containing an MCP client token")
    probe_parser.add_argument("--arguments-file", help="JSON arguments for the selected harmless stream probe")
    probe_parser.add_argument("--timeout", type=int, default=300, help="per-command timeout in seconds (default 300; max 1200)")
    probe_parser.add_argument("--gregale", default="gregale", help="Gregale CLI executable (default: gregale on PATH)")
    probe_parser.set_defaults(run=native_probe)

    oauth_parser = commands.add_parser("oauth-probe", help="run OAuth token acceptance and rejection checks against staging")
    oauth_parser.add_argument("--dir", dest="directory", type=Path, required=True, help="private qualification evidence directory")
    oauth_parser.add_argument("--app", required=True, help="staging Gregale app slug")
    oauth_parser.add_argument("--confirm-staging-app", required=True, help="must exactly match --app; confirms the disposable staging target")
    oauth_parser.add_argument("--target", required=True, help="provider, staging app, and MCP client/tool versions")
    oauth_parser.add_argument("--valid-token-env", required=True, help="environment variable with an active token authorized for the probe tool")
    oauth_parser.add_argument("--expired-token-env", required=True, help="environment variable with an expired token for this resource")
    oauth_parser.add_argument("--wrong-audience-token-env", required=True, help="environment variable with a token for a different resource audience")
    oauth_parser.add_argument("--insufficient-scope-token-env", required=True, help="environment variable with a valid resource token lacking the probe tool scope")
    oauth_parser.add_argument("--stream-tool", required=True, help="explicit harmless, progress-capable tool protected by the tested scope")
    oauth_parser.add_argument("--arguments-file", help="JSON arguments for the harmless probe tool")
    oauth_parser.add_argument("--timeout", type=int, default=300, help="per-command timeout in seconds (default 300; max 1200)")
    oauth_parser.add_argument("--gregale", default="gregale", help="Gregale CLI executable (default: gregale on PATH)")
    oauth_parser.set_defaults(run=oauth_probe)

    oauth_flow_parser = commands.add_parser(
        "oauth-flow", help="run an operator-assisted OAuth authorization-code + PKCE staging flow"
    )
    oauth_flow_parser.add_argument("--dir", dest="directory", type=Path, required=True, help="private qualification evidence directory")
    oauth_flow_parser.add_argument("--app", required=True, help="staging Gregale app slug")
    oauth_flow_parser.add_argument("--confirm-staging-app", required=True, help="must exactly match --app before staging login")
    oauth_flow_parser.add_argument("--target", required=True, help="provider, staging app, and OAuth test-client version")
    oauth_flow_parser.add_argument("--issuer", required=True, help="configured HTTPS authorization-server issuer")
    oauth_flow_parser.add_argument("--jwks-url", required=True, help="HTTPS JWKS URL configured in gregale-mcp.json")
    oauth_flow_parser.add_argument("--resource", required=True, help="canonical HTTPS MCP resource audience")
    oauth_flow_parser.add_argument("--client-id", required=True, help="registered public OAuth test-client identifier")
    oauth_flow_parser.add_argument("--client-secret-env", help="optional environment variable for confidential-client Basic auth")
    oauth_flow_parser.add_argument("--redirect-uri", default="http://127.0.0.1:8765/callback", help="pre-registered loopback callback URI")
    oauth_flow_parser.add_argument("--scope", action="append", required=True, help="requested OAuth scope; repeat for multiple scopes")
    oauth_flow_parser.add_argument("--authorize-param", action="append", default=[], help="provider-specific authorization parameter as NAME=VALUE")
    oauth_flow_parser.add_argument("--stream-tool", help="optional harmless, progress-capable tool to call with the access token")
    oauth_flow_parser.add_argument("--arguments-file", help="JSON arguments for the harmless stream tool")
    oauth_flow_parser.add_argument("--previous-kid", help="prior signing-key ID; assert the new token uses a rotated JWKS key")
    oauth_flow_parser.add_argument("--key-rotation-timeout", type=int, default=0, help="retry read-only discovery while the gateway JWKS cache refreshes (0-600 seconds)")
    oauth_flow_parser.add_argument("--timeout", type=int, default=300, help="per-request and callback timeout in seconds (default 300; max 1200)")
    oauth_flow_parser.add_argument("--open-browser", action="store_true", help="open the authorization URL in the system browser")
    oauth_flow_parser.add_argument("--gregale", default="gregale", help="Gregale CLI executable (default: gregale on PATH)")
    oauth_flow_parser.set_defaults(run=oauth_flow)

    sdk_parser = commands.add_parser(
        "sdk-interop", help="qualify the Node, Go and Python starters with the pinned official MCP SDK client"
    )
    sdk_parser.add_argument("--dir", dest="directory", type=Path, required=True, help="private qualification evidence directory")
    sdk_parser.add_argument("--target", required=True, help="starter source commit and official SDK client version")
    sdk_parser.add_argument("--node-url", required=True, help="Node starter loopback MCP URL")
    sdk_parser.add_argument("--go-url", required=True, help="Go starter loopback MCP URL")
    sdk_parser.add_argument("--python-url", required=True, help="Python starter loopback MCP URL")
    sdk_parser.add_argument("--timeout", type=int, default=180, help="whole-run timeout in seconds (default 180; max 600)")
    sdk_parser.set_defaults(run=sdk_interop)

    tasks_restart_parser = commands.add_parser(
        "tasks-restart", help="qualify PostgreSQL task recovery after a worker process crash across two replicas"
    )
    tasks_restart_parser.add_argument("--dir", dest="directory", type=Path, required=True, help="private qualification evidence directory")
    tasks_restart_parser.add_argument("--target", required=True, help="starter source and disposable PostgreSQL qualification target")
    tasks_restart_parser.add_argument("--starter-dir", type=Path, required=True, help="materialized Node MCP starter with npm dependencies installed")
    tasks_restart_parser.add_argument("--confirm-disposable-database", action="store_true", help="confirm the database can accept a temporary schema that will be dropped")
    tasks_restart_parser.add_argument("--timeout", type=int, default=180, help="whole-run timeout in seconds (default 180; max 600)")
    tasks_restart_parser.add_argument("--node", default="node", help="Node executable (default: node on PATH)")
    tasks_restart_parser.set_defaults(run=tasks_restart)

    tasks_scale_zero_parser = commands.add_parser(
        "tasks-scale-zero", help="qualify a real MCP task scaling a confirmed staging worker pool from zero and back"
    )
    tasks_scale_zero_parser.add_argument("--dir", dest="directory", type=Path, required=True, help="private qualification evidence directory")
    tasks_scale_zero_parser.add_argument("--app", required=True, help="staging Gregale worker app slug")
    tasks_scale_zero_parser.add_argument("--confirm-staging-app", required=True, help="must exactly match --app before executing the MCP task")
    tasks_scale_zero_parser.add_argument("--target", required=True, help="reviewed build, staging app, and task probe identifiers")
    tasks_scale_zero_parser.add_argument("--tool", required=True, help="harmless MCP tool that returns an asynchronous task and runs long enough to observe scaling")
    tasks_scale_zero_parser.add_argument("--arguments-file", help="JSON arguments for the harmless task probe")
    tasks_scale_zero_parser.add_argument("--token-env", help="environment variable containing an MCP client access token")
    tasks_scale_zero_parser.add_argument("--timeout", type=int, default=1200, help="whole-run timeout in seconds (default 1200; max 3600)")
    tasks_scale_zero_parser.add_argument("--poll-interval", type=int, default=5, help="status polling interval in seconds (default 5; max 60)")
    tasks_scale_zero_parser.add_argument("--gregale", default="gregale", help="Gregale CLI executable (default: gregale on PATH)")
    tasks_scale_zero_parser.set_defaults(run=tasks_scale_zero)

    for name, help_text, function in (
        ("status", "show recorded rows and evidence integrity", status),
        ("check", "run the fail-closed release evidence gate", check),
    ):
        command_parser = commands.add_parser(name, help=help_text)
        command_parser.add_argument("--dir", dest="directory", type=Path, required=True, help="qualification evidence directory")
        command_parser.add_argument("--commit", help="expected reviewed source commit SHA (defaults to manifest)")
        command_parser.set_defaults(run=function)

    args = parser.parse_args()
    try:
        args.run(args)
    except (ValueError, KeyError, TypeError, OSError, json.JSONDecodeError) as error:
        print(json.dumps({"ok": False, "error": str(error)}))
        raise SystemExit(1)


if __name__ == "__main__":
    main()
