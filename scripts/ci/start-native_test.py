#!/usr/bin/env python3
"""Portable failure/ownership contracts, not evidence of a native fleet run."""
import importlib.util
import hashlib
import io
import json
import os
from pathlib import Path
import signal
import shlex
import shutil
import socket
import stat
import subprocess
import sys
import tarfile
import tempfile
import time
from types import SimpleNamespace
import unittest
from unittest.mock import patch
import uuid

spec = importlib.util.spec_from_file_location("start_native", Path(__file__).with_name("start-native.py"))
native = importlib.util.module_from_spec(spec)
spec.loader.exec_module(native)


def uid():
    return str(uuid.uuid4())


class FixtureAPI:
    def __init__(self):
        self.origin, self.token = "https://api.fixture.example", "fixture-account-credential"
        self.project, self.project_id, self.environment_id = "start-contract", uid(), uid()
        self.apps = {self.project + "-" + case: uid() for case in native.CASES}
        self.calls, self.deleted, self.plan, self.receipt = [], set(), None, None
        self.created, self.project_deleted, self.foreign_member = False, False, False
        self.changed_app, self.response, self.rows, self.timeline = None, None, [], {}
        self.configuration = {}
        self.uploads, self.deployment_rows = {}, {}

    def request(self, method, path, body=None, headers=None, expected=(200,)):
        self.calls.append((method, path))
        route = path.split("?")[0]
        if method == "GET" and route.startswith("/v1/uploads/"):
            return 200, self.uploads[route.split("/")[-1]]
        if method == "GET" and route.startswith("/v1/deployments/"):
            return 200, self.deployment_rows[route.split("/")[-1]]
        if method == "POST" and route == "/v1/projects/scan":
            return 200, self.plan or {"project_slug": self.project, "can_apply": True, "plan_token": "ephemeral-plan",
                                      "workloads": [{"name": name, "action": "create"} for name in self.apps]}
        if method == "POST" and route == "/v1/projects":
            self.created = True
            return 201, self.receipt or {"project_id": self.project_id, "apps": [{"slug": name, "id": value} for name, value in self.apps.items()]}
        if method == "POST" and route.endswith("/environments"):
            return 201, {"id": self.environment_id, "project_id": self.project_id, "slug": "staging"}
        if method == "POST" and route.endswith("/response-check"):
            return 200, self.response
        if method == "GET" and route == "/v1/projects/" + self.project:
            if not self.created or self.project_deleted:
                return 404, None
            members = [name for name in self.apps if name not in self.deleted]
            if self.foreign_member:
                members.append("someone-elses-app")
            return 200, {"id": self.project_id, "workloads": [{"slug": name} for name in members]}
        if method == "DELETE" and route == "/v1/projects/" + self.project:
            self.project_deleted = True
            return 204, None
        if method == "GET" and route == "/v1/instances":
            return 200, {"instances": self.rows}
        if route.startswith("/v1/apps/"):
            slug = route.split("/")[3]
            if route.endswith(("/secrets", "/env")):
                scope = native.urllib.parse.parse_qs(native.urllib.parse.urlsplit(path).query)["scope"][0]
                field = "secrets" if route.endswith("/secrets") else "env"
                return 200, {field: self.configuration.get((scope, field), [])}
            if route.endswith("/timeline"):
                return 200, self.timeline
            if route.endswith("/instances"):
                return 200, self.rows
            if method == "DELETE":
                self.deleted.add(slug)
                return 204, None
            if method == "PATCH":
                return 200, {"slug": slug, "id": self.apps[slug]}
            if not self.created or slug in self.deleted:
                return 404, None
            return 200, {"slug": slug, "id": uid() if slug == self.changed_app else self.apps[slug]}
        raise AssertionError("unhandled fixture request: " + method + " " + path)

    def get(self, path, expected=(200,)):
        return self.request("GET", path, expected=expected)


class RunContracts(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.api = FixtureAPI()
        self.target = {"api_origin": self.api.origin, "account_id": uid(), "project_slug": self.api.project, "named_environment": "staging"}
        self.report = {"resources": {"operation_id": uid(), "provisioning": "not_started", "apps": {}}}
        self.run = native.Run(self.api, self.target, self.root / "evidence.json", self.report)

    def sources(self):
        source = self.root / "source"
        native.prepare_sources(source, self.api.project, "portable")
        return source

    def owned(self):
        self.api.created = True
        self.report["resources"].update(apps=dict(self.api.apps), project_id=self.api.project_id,
                                        environment_id=self.api.environment_id, provisioning="accepted")

    def test_provision_cleanup_pins_ids_and_preserves_unrelated_resources(self):
        self.run.provision(self.sources())
        self.assertEqual(self.report["resources"]["apps"], self.api.apps)
        self.run.cleanup()
        self.assertEqual(self.report["cleanup"]["status"], "passed")
        self.assertEqual(self.api.deleted, set(self.api.apps))
        self.assertTrue(self.api.project_deleted)
        encoded = self.run.path.read_text()
        self.assertNotIn(self.api.token, encoded)
        self.assertNotIn("ephemeral-plan", encoded)
        self.assertEqual(stat.S_IMODE(self.run.path.stat().st_mode), 0o600)

    def test_unsafe_plan_never_applies(self):
        for modification in ({"removed": [{"slug": "unrelated"}]}, {"managed": [{"name": "database"}]},
                             {"workloads": [{"name": name, "action": "update", "existing_app_id": uid()} for name in self.api.apps]}):
            with self.subTest(modification=modification):
                self.api.plan = {"project_slug": self.api.project, "can_apply": True, "plan_token": "ephemeral-plan",
                                 "workloads": [{"name": name, "action": "create"} for name in self.api.apps], **modification}
                with self.assertRaises(native.Failure):
                    self.run.provision(self.sources() if not (self.root / "source").exists() else self.root / "source")
                self.assertFalse(self.api.created)
                self.assertEqual(self.report["resources"]["provisioning"], "not_started")

    def test_failed_ownership_write_prevents_apply(self):
        with patch.object(self.run, "save", side_effect=OSError("receipt disk full")):
            with self.assertRaises(OSError):
                self.run.provision(self.sources())
        self.assertFalse(self.api.created)
        self.assertNotIn(("POST", "/v1/projects"), self.api.calls)

    def test_reused_slug_blocks_deletion_and_project_removal(self):
        self.owned()
        self.api.changed_app = next(iter(self.api.apps))
        with self.assertRaises(native.Failure):
            self.run.cleanup()
        self.assertNotIn(self.api.changed_app, self.api.deleted)
        self.assertFalse(self.api.project_deleted)
        self.assertEqual(self.report["cleanup"]["status"], "failed")

    def test_new_project_member_keeps_project_and_foreign_app(self):
        self.owned()
        self.api.foreign_member = True
        with self.assertRaises(native.Failure):
            self.run.cleanup()
        self.assertEqual(self.api.deleted, set(self.api.apps))
        self.assertFalse(self.api.project_deleted)

    def test_lost_apply_reply_keeps_unknown_outcome_visible(self):
        self.report["resources"]["provisioning"] = "outcome_unknown"
        self.api.created = True
        with self.assertRaises(native.Failure):
            self.run.cleanup()
        self.assertEqual(self.api.deleted, set())
        self.assertFalse(self.api.project_deleted)
        self.assertEqual(self.report["cleanup"]["status"], "failed")

    def test_partial_apply_keeps_known_ownership_before_failure(self):
        slug, app_id = next(iter(self.api.apps.items()))
        self.api.receipt = {"project_id": self.api.project_id, "apps": [{"slug": slug, "id": app_id}]}
        with self.assertRaises(native.Failure):
            self.run.provision(self.sources())
        saved = json.loads(self.run.path.read_bytes())
        self.assertEqual(saved["resources"]["apps"], {slug: app_id})
        self.assertEqual(saved["resources"]["provisioning"], "outcome_unknown")
        with self.assertRaises(native.Failure):
            self.run.cleanup()
        self.assertEqual(self.api.deleted, {slug})
        self.assertFalse(self.api.project_deleted)

    def response(self):
        self.owned()
        slug, app_id = next(iter(self.api.apps.items()))
        deployment = uid()
        response = {"app_id": app_id, "deployment_id": deployment, "scope": "staging", "project_id": self.api.project_id,
                    "environment_id": self.api.environment_id, "instance_id": uid(), "http_status": 200,
                    "body_truncated": False, "body": json.dumps({"marker": "nonce-v2", "authorization_seen": False})}
        return slug, deployment, response

    def test_http_200_requires_exact_served_identity_scope_and_guest_marker(self):
        slug, deployment, response = self.response()
        self.api.response = response
        proof = self.run.response(slug, "staging", deployment, "nonce-v2")
        self.assertNotIn("body", proof)
        for modification in ({"deployment_id": uid()}, {"app_id": uid()}, {"project_id": uid()}, {"environment_id": uid()},
                             {"scope": "default"}, {"instance_id": "latest"}, {"body_truncated": True},
                             {"body": json.dumps({"marker": "nonce-v1", "authorization_seen": False})},
                             {"body": json.dumps({"marker": "nonce-v2", "authorization_seen": True})}):
            with self.subTest(modification=modification):
                self.api.response = {**response, **modification}
                with self.assertRaises(native.Failure):
                    self.run.response(slug, "staging", deployment, "nonce-v2")

    def test_response_failure_requires_guest_fault_proof_and_cannot_pass_as_success(self):
        slug, deployment, response = self.response()
        self.api.response = {**response, "http_status": 503, "body": json.dumps({"marker": "nonce-v2", "response_failed": True, "authorization_seen": False})}
        proof = self.run.response(slug, "staging", deployment, "nonce-v2", http_status=503, fault=True)
        self.assertEqual(proof["http_status"], 503)
        self.assertEqual(proof["environment_id"], self.api.environment_id)
        self.assertNotIn("body", proof)
        with self.assertRaises(native.Failure):
            self.run.response(slug, "staging", deployment, "nonce-v2")
        self.api.response["body"] = json.dumps({"marker": "nonce-v2", "response_failed": False, "authorization_seen": False})
        with self.assertRaises(native.Failure):
            self.run.response(slug, "staging", deployment, "nonce-v2", http_status=503, fault=True)

    def test_configuration_requires_exact_guest_value_and_executed_source_guard(self):
        slug, deployment, response = self.response()
        payload = json.loads(response["body"])
        for fields in ({}, {"configuration_ok": False, "source_checked": True},
                       {"configuration_ok": True, "source_checked": False}, {"configuration_ok": "true", "source_checked": True}):
            self.api.response = {**response, "body": json.dumps({**payload, **fields})}
            with self.subTest(fields=fields), self.assertRaises(native.Failure):
                self.run.response(slug, "staging", deployment, "nonce-v2", configured=True)
        self.api.response = {**response, "body": json.dumps({**payload, "configuration_ok": True, "source_checked": True})}
        proof = self.run.response(slug, "staging", deployment, "nonce-v2", configured=True)
        self.assertNotIn("body", proof)
        self.assertNotIn("configuration_ok", proof)

    def test_configuration_metadata_rejects_wrong_scope_and_plaintext(self):
        slug = next(iter(self.api.apps))
        self.assertEqual(self.run.configuration_metadata(slug, ("fixture-private-value",)),
                         {"default": {"secrets": [], "env": []}, "staging": {"secrets": [], "env": []}})
        for row in ({"key": native.CONFIGURATION_KEY, "scope": "default"},
                    {"key": native.CONFIGURATION_KEY, "scope": "staging", "value": "fixture-private-value"},
                    {"key": native.CONFIGURATION_KEY, "scope": "staging", "path": "/private/selected-file"}):
            self.api.configuration[("staging", "secrets")] = [row]
            with self.subTest(row=row), self.assertRaises(native.Failure):
                self.run.configuration_metadata(slug, ("fixture-private-value", "/private/selected-file"))

    def test_configuration_privacy_checks_final_output_state_cache_and_evidence(self):
        environment = {"XDG_STATE_HOME": str(self.root / "state"), "XDG_CACHE_HOME": str(self.root / "cache"),
                       "XDG_CONFIG_HOME": str(self.root / "config")}
        launch = native.Launch(None, self.root, self.root / "state", environment, "fixture", "staging")
        self.run.save()
        for root in environment.values():
            Path(root).mkdir()
        forbidden = ("fixture-private-value", "/private/selected-file")
        self.run.configuration_privacy(launch, forbidden, SimpleNamespace(output="safe output"))
        for value in forbidden:
            for root in environment.values():
                path = Path(root) / "recovery.json"
                path.write_text(json.dumps({"unexpected": value}))
                with self.subTest(root=root, value=value), self.assertRaises(native.Failure) as raised:
                    self.run.configuration_privacy(launch, forbidden)
                self.assertNotIn(value, str(raised.exception))
                path.unlink()
            with self.assertRaises(native.Failure):
                self.run.configuration_privacy(launch, forbidden, SimpleNamespace(output="last output " + value))
            self.run.path.write_text(json.dumps({"unexpected": value}))
            with self.assertRaises(native.Failure):
                self.run.configuration_privacy(launch, forbidden)
            self.run.save()
            self.report["unexpected"] = value
            with self.assertRaises(native.Failure):
                self.run.configuration_privacy(launch, forbidden)
            del self.report["unexpected"]

    def test_failed_guest_infrastructure_or_foreign_build_is_not_user_build_proof(self):
        self.owned()
        slug, app_id = next(iter(self.api.apps.items()))
        deployment, build_id = uid(), uid()
        row = {"id": deployment, "app_id": app_id, "scope": "staging", "status": "failed", "build_id": build_id}
        build = {"id": build_id, "deployment_id": deployment, "status": "failed", "failure_class": "user_error"}
        def verify(current_row, current_build):
            def get(path):
                return 200, current_build if path.startswith("/v1/builds/") else current_row
            with patch.object(self.api, "get", side_effect=get):
                return self.run.build_failure(slug, "staging", deployment)
        self.assertEqual(verify(row, build)[1], build)
        for modification in ({"status": "live"}, {"build_id": ""}, {"scope": "default"}, {"app_id": uid()}):
            with self.subTest(row=modification), self.assertRaises(native.Failure):
                verify({**row, **modification}, build)
        for modification in ({"failure_class": "infra_error"}, {"status": "succeeded"}, {"id": uid()}, {"deployment_id": uid()}):
            with self.subTest(build=modification), self.assertRaises(native.Failure):
                verify(row, {**build, **modification})

    def test_saved_failure_receipt_requires_the_previous_verified_id(self):
        self.owned()
        slug, app_id = next(iter(self.api.apps.items()))
        accepted, previous = uid(), uid()
        state = self.root / "state"
        directory = state / "gregale/start"
        directory.mkdir(parents=True)
        path = directory / "session.json"
        document = {"api_base": self.api.origin, "account_id": self.target["account_id"], "app_slug": slug, "app_id": app_id,
                    "project": self.api.project, "project_id": self.api.project_id, "environment": "staging", "environment_id": self.api.environment_id,
                    "deployment_id": accepted, "verified_deployment_id": previous}
        launch = native.Launch(None, None, state, {}, slug, "staging")
        native.save(path, document)
        self.assertEqual(self.run.saved_ids(launch, accepted, previous), {"accepted_id": accepted, "verified_id": previous})
        for modification in ({"verified_deployment_id": accepted}, {"deployment_id": previous}, {"pending_submission": {"upload_id": uid()}}):
            native.save(path, {**document, **modification})
            with self.subTest(modification=modification), self.assertRaises(native.Failure):
                self.run.saved_ids(launch, accepted, previous)

    def test_wake_requires_fresh_id_and_correlated_vm_scheduler_events(self):
        slug, deployment, response = self.response()
        wake_id = uid()
        self.api.rows = [{"id": response["instance_id"], "app_id": response["app_id"], "deployment_id": deployment, "wake_id": wake_id}]
        self.api.timeline = {"app_id": response["app_id"], "wake_id": wake_id, "events": [
            {"kind": kind, "actor": actor, "at": "2026-10-15T00:00:00Z", "data": {"app_id": response["app_id"],
                "instance_id": response["instance_id"], "wake_id": wake_id, "method": "restore"}}
            for kind, actor in (("wake.boot_started", "schedd"), ("wake.boot_observed", "vmmd"), ("wake.readiness_200", "vmmd"), ("wake.boot_completed", "schedd"))]}
        self.assertEqual(self.run.wake(slug, response, {"wake_id": uid()})["wake_id"], wake_id)
        with self.assertRaises(native.Failure):
            self.run.wake(slug, response, {"wake_id": wake_id})
        self.api.timeline["events"][1]["data"]["instance_id"] = uid()
        with patch.object(native.time, "monotonic", side_effect=[0, 1, 31]), patch.object(native.time, "sleep"):
            with self.assertRaises(native.Failure):
                self.run.wake(slug, response, {"wake_id": uid()})

    def test_session_rejects_scope_changes_and_credentials(self):
        self.owned()
        slug, app_id = next(iter(self.api.apps.items()))
        state = self.root / "state"
        directory = state / "gregale/start"
        directory.mkdir(parents=True)
        path = directory / "session.json"
        document = {"api_base": self.api.origin, "account_id": self.target["account_id"], "app_slug": slug, "app_id": app_id,
                    "project": self.api.project, "project_id": self.api.project_id, "environment": "staging", "environment_id": self.api.environment_id}
        native.save(path, document)
        self.assertIsNotNone(self.run.session(state, slug, "staging"))
        for modification in ({"environment_id": uid()}, {"account_id": uid()}, {"api_base": "https://elsewhere.example"}, {"token": self.api.token}):
            native.save(path, {**document, **modification})
            with self.assertRaises(native.Failure):
                self.run.session(state, slug, "staging")
        native.save(path, document)
        path.chmod(0o644)
        with self.assertRaises(native.Failure):
            self.run.session(state, slug, "staging")

    def test_archive_has_scannable_root_and_no_ownership_or_evidence_files(self):
        source = self.sources()
        body, headers = native.source_multipart(source, self.api.project)
        boundary = headers["Content-Type"].split("boundary=")[1].encode()
        part = next(part for part in body.split(b"--" + boundary) if b'filename="fixture.tar.gz"' in part)
        compressed = part.split(b"\r\n\r\n", 1)[1][:-2]
        with tarfile.open(fileobj=io.BytesIO(compressed), mode="r:gz") as archive:
            names = archive.getnames()
            self.assertTrue(all(name.startswith("fixture/") for name in names))
            names = [name.removeprefix("fixture/") for name in names]
        self.assertIn("package.json", names)
        self.assertEqual(len(names), 13)
        self.assertTrue(all(name == "package.json" or name.startswith("apps/start-contract-") for name in names))
        self.assertNotIn(self.api.token.encode(), body)


class TerminalContracts(unittest.TestCase):
    def helper(self, root, body):
        path = root / "gregale"
        path.write_text("#!" + sys.executable + "\n" + body)
        path.chmod(0o700)
        return path

    def test_real_terminal_prompt_input_and_interrupted_exit(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            cli = self.helper(root, 'import os, signal, sys\nassert os.isatty(0)\n'
                              'signal.signal(signal.SIGINT, lambda *_: sys.exit(130))\n'
                              'print("Choose [1]: ", end="", flush=True)\nassert input() == "1"\n'
                              'print("Deploy this app (y/N) [n]: ", end="", flush=True)\nassert input() == "y"\n'
                              'print("accepted", flush=True)\nsignal.pause()\n')
            with native.Terminal(cli, root, os.environ.copy()) as terminal:
                terminal.answer("Choose [1]: ", "1")
                terminal.answer("Deploy this app (y/N) [n]: ", "y")
                terminal.expect("accepted", timeout=5)
                terminal.process.send_signal(signal.SIGINT)
                terminal.wait(130, timeout=5)
            self.assertEqual({path.name for path in root.iterdir()}, {"gregale"})

    def test_unbounded_cli_output_fails_without_recording_transcript(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            cli = self.helper(root, 'import sys, signal\nsys.stdout.write("x" * (2 * 1024 * 1024))\nsys.stdout.flush()\nsignal.pause()\n')
            with native.Terminal(cli, root, os.environ.copy()) as terminal:
                with self.assertRaises(native.Failure):
                    terminal.expect("unreachable milestone", timeout=5)
            self.assertEqual({path.name for path in root.iterdir()}, {"gregale"})

    def test_exit_drains_last_output_for_privacy_checks(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            cli = self.helper(root, 'print("final-private-data", flush=True)\n')
            with native.Terminal(cli, root, os.environ.copy()) as terminal:
                terminal.process.wait(timeout=5)
                terminal.wait(0, timeout=5)
                self.assertIn("final-private-data", terminal.output)

    def test_milestone_can_be_read_after_child_exits(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            cli = self.helper(root, 'print("temporarily unavailable", flush=True)\n')
            with native.Terminal(cli, root, os.environ.copy()) as terminal:
                terminal.process.wait(timeout=5)
                terminal.expect("temporarily unavailable", timeout=5)
                terminal.wait(0, timeout=5)


class CompatibilityContracts(unittest.TestCase):
    def test_mutation_attempts_fail_even_if_the_relay_blocked_them(self):
        before = {key: 0 for key in native.RELAY_COUNTERS}
        after = {**before, "capabilities": 1, "capability_faults": 1}
        self.assertEqual(native.relay_delta(before, after, "capability_faults"), after)
        for key in ("writes", "app_creates", "secret_writes", "source_writes", "other_writes", "blocked_writes", "forwarded_writes", "commit_faults"):
            with self.subTest(key=key), self.assertRaises(native.Failure):
                native.relay_delta(before, {**after, key: 1}, "capability_faults")
        for invalid in ({**after, "reads": -1}, {**after, "checks": True}, {**after, "capability_faults": 0},
                        {key: value for key, value in after.items() if key != "writes"}):
            with self.subTest(invalid=invalid), self.assertRaises(native.Failure):
                native.relay_delta(before, invalid, "capability_faults")

    def test_verification_posts_are_reads_and_late_outage_needs_real_fault_evidence(self):
        before = {key: 0 for key in native.RELAY_COUNTERS}
        after = {**before, "reads": 1, "capabilities": 1, "checks": 1, "response_faults": 1}
        self.assertEqual(native.relay_delta(before, after, "response_faults"), after)
        with self.assertRaises(native.Failure):
            native.relay_delta(before, {**after, "response_faults": 0}, "response_faults")

    def test_configuration_comparison_ignores_delivery_but_detects_rewrites(self):
        before = {"default": {"secrets": [], "env": []}, "staging": {"env": [{"key": "PUBLIC", "updated_at": "first"}],
                  "secrets": [{"key": native.CONFIGURATION_KEY, "scope": "staging", "updated_at": "first",
                               "delivery_version": 1, "delivered_version": 0}]}}
        after = json.loads(json.dumps(before))
        after["staging"]["secrets"][0].update(delivered_version=1, last_delivered_at="later")
        self.assertEqual(native.configuration_versions(before), native.configuration_versions(after))
        after["staging"]["secrets"][0]["delivery_version"] = 2
        self.assertNotEqual(native.configuration_versions(before), native.configuration_versions(after))
        after = json.loads(json.dumps(before))
        after["staging"]["env"][0]["updated_at"] = "later"
        self.assertNotEqual(native.configuration_versions(before), native.configuration_versions(after))
        after = json.loads(json.dumps(before))
        after["default"]["secrets"] = after["staging"]["secrets"]
        self.assertNotEqual(native.configuration_versions(before), native.configuration_versions(after))

    def relay_helper(self, root, ready, ignore_commands=False, extra=None):
        path = root / "relay"
        path.write_text("#!" + sys.executable + "\nimport json,sys\n"
                        "print(" + repr(json.dumps(ready)) + ", flush=True)\n"
                        "for line in sys.stdin:\n"
                        " command = {} if " + repr(ignore_commands) + " else json.loads(line)\n"
                        " print(json.dumps({'mode': command.get('mode', 'healthy'), 'block_writes': command.get('block_writes', True),"
                        "'counts': " + repr({key: 0 for key in native.RELAY_COUNTERS}) + ", **" + repr(extra or {}) + "}), flush=True)\n")
        path.chmod(0o700)
        return path

    def test_relay_control_is_bounded_private_and_keeps_its_origin(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            path = self.relay_helper(root, {"version": 1, "origin": "http://127.0.0.1:12345"})
            api = SimpleNamespace(origin="https://api.fixture.example", token="private-fixture-credential")
            with native.Relay(path, api, os.environ.copy()) as relay:
                origin = relay.origin
                self.assertEqual(relay.command("old-api", True)["writes"], 0)
                self.assertEqual(relay.command("healthy", True)["writes"], 0)
                self.assertEqual(relay.origin, origin)
            self.assertEqual({item.name for item in root.iterdir()}, {"relay"})
            for origin in ("http://0.0.0.0:12345", "https://api.fixture.example", "http://user:password@127.0.0.1:12345",
                           "http://127.0.0.1:12345/path"):
                path = self.relay_helper(root, {"version": 1, "origin": origin})
                with self.subTest(origin=origin), self.assertRaises(native.Failure):
                    native.Relay(path, api, os.environ.copy())

    def test_relay_must_acknowledge_the_selected_fault_and_guard(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            path = self.relay_helper(root, {"version": 1, "origin": "http://127.0.0.1:12345"}, ignore_commands=True)
            api = SimpleNamespace(origin="https://api.fixture.example", token="private-fixture-credential")
            with native.Relay(path, api, os.environ.copy()) as relay, self.assertRaises(native.Failure):
                relay.command("old-api", False)

    def test_relay_commit_control_allows_only_exact_protocol_identities(self):
        receipt = {key: uid() for key in ("upload_id", "deployment_id", "app_id")}
        receipt["upload_id"] = uuid.uuid4().hex[:24]
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            api = SimpleNamespace(origin="https://api.fixture.example", token="private-fixture-credential")
            for extra in ({"commit": {**receipt, "token": api.token}}, {"commit": {**receipt, "deployment_id": "latest"}},
                          {"commit": receipt, "commit_error": True}):
                path = self.relay_helper(root, {"version": 1, "origin": "http://127.0.0.1:12345"}, extra=extra)
                with self.subTest(extra=extra), native.Relay(path, api, os.environ.copy()) as relay, self.assertRaises(native.Failure):
                    relay.command()
            path = self.relay_helper(root, {"version": 1, "origin": "http://127.0.0.1:12345"}, extra={"commit": receipt})
            with native.Relay(path, api, os.environ.copy()) as relay:
                relay.command("hold-commit-reply", False)
                self.assertEqual(relay.commit, receipt)


class SubmissionContracts(unittest.TestCase):
    setUp = RunContracts.setUp
    owned = RunContracts.owned

    def pending(self):
        self.owned()
        slug, app_id = next(iter(self.api.apps.items()))
        receipt = {"upload_id": uid(), "deployment_id": uid(), "app_id": app_id}
        receipt["upload_id"] = uuid.uuid4().hex[:24]
        previous = uid()
        state = self.root / "state"
        directory = state / "gregale/start"
        directory.mkdir(parents=True)
        path = directory / "session.json"
        document = {"version": 2, "api_base": self.api.origin, "account_id": self.target["account_id"], "app_slug": slug, "app_id": app_id,
                    "project": self.api.project, "project_id": self.api.project_id, "environment": "staging", "environment_id": self.api.environment_id,
                    "deployment_id": previous, "verified_deployment_id": previous,
                    "pending_submission": {"transport": "resumable", "upload_id": receipt["upload_id"], "key": uid(), "total_size": 512}}
        native.save(path, document)
        self.api.uploads[receipt["upload_id"]] = {"upload_id": receipt["upload_id"], "app_slug": slug, "status": "committed",
                                                 "deployment_id": receipt["deployment_id"], "total_size": 512, "received_bytes": 512}
        launch = native.Launch(None, None, state, {}, slug, "staging")
        return launch, path, document, receipt, previous

    def test_pending_upload_preserves_previous_ids_and_rejects_incomplete_or_foreign_acceptance(self):
        launch, path, document, receipt, previous = self.pending()
        proof = self.run.pending_submission(launch, receipt, previous)
        self.assertEqual(proof, {"upload_id": receipt["upload_id"], "total_size": 512, "accepted_id": previous, "verified_id": previous})
        for modification in ({"deployment_id": receipt["deployment_id"]}, {"verified_deployment_id": receipt["deployment_id"]},
                             {"version": 1}, {"pending_submission": None},
                             *({"pending_submission": {**document["pending_submission"], **change}} for change in
                               ({"upload_id": uid()}, {"transport": "legacy"}, {"total_size": True}, {"key": ""}))):
            native.save(path, {**document, **modification})
            with self.subTest(modification=modification), self.assertRaises(native.Failure):
                self.run.pending_submission(launch, receipt, previous)
        native.save(path, document)
        accepted = self.api.uploads[receipt["upload_id"]]
        for modification in ({"upload_id": uid()}, {"deployment_id": uid()}, {"app_slug": "foreign"}, {"status": "uploading"},
                             {"received_bytes": 511}, {"total_size": 513}):
            self.api.uploads[receipt["upload_id"]] = {**accepted, **modification}
            with self.subTest(modification=modification), self.assertRaises(native.Failure):
                self.run.pending_submission(launch, receipt, previous)

    def test_commit_proof_requires_new_selected_app_and_scope(self):
        launch, _, _, receipt, _ = self.pending()
        counts = {key: 0 for key in native.RELAY_COUNTERS}
        relay = SimpleNamespace(commit=receipt, command=lambda: {**counts, "commit_faults": 1})
        terminal = SimpleNamespace(process=SimpleNamespace(poll=lambda: 3))
        row = {"id": receipt["deployment_id"], "app_id": receipt["app_id"], "scope": "staging"}
        self.api.deployment_rows[row["id"]] = row
        self.assertEqual(self.run.committed_reply(launch, terminal, relay, counts, set()), (receipt, row))
        for history, change, deployment in (({row["id"]}, {}, row), (set(), {"app_id": uid()}, row),
                                            (set(), {}, {**row, "scope": "default"}), (set(), {}, {**row, "app_id": uid()})):
            relay.commit = {**receipt, **change}
            self.api.deployment_rows[row["id"]] = deployment
            with self.subTest(history=history, change=change, deployment=deployment), self.assertRaises(native.Failure):
                self.run.committed_reply(launch, terminal, relay, counts, history)

    def test_removing_source_and_cache_keeps_link_and_pending_record(self):
        launch, path, _, _, _ = self.pending()
        launch.source = self.root / "source"
        native.write_fixture(launch.source, "submission-contract")
        link = launch.source / ".gregale/project.json"
        link.parent.mkdir()
        link.write_text('{"project":"start-contract"}')
        launch.environment["XDG_CACHE_HOME"] = str(self.root / "cache")
        for directory in (launch.state / "gregale/uploads", self.root / "cache"):
            directory.mkdir(parents=True)
            (directory / "archive").write_text("cached-source")
        before, binding = path.read_bytes(), link.read_bytes()
        (launch.source / "unexpected").write_text("foreign-source")
        with self.assertRaises(native.Failure):
            native.remove_submission_inputs(launch)
        self.assertTrue((launch.source / "handler.js").exists())
        (launch.source / "unexpected").unlink()
        native.remove_submission_inputs(launch)
        self.assertEqual(path.read_bytes(), before)
        self.assertEqual(link.read_bytes(), binding)
        self.assertEqual({item.name for item in launch.source.iterdir()}, {".gregale"})
        self.assertFalse((launch.state / "gregale/uploads").exists())
        self.assertFalse((self.root / "cache").exists())

    def test_upload_discovery_is_read_only_and_requires_an_actual_read(self):
        counts = {key: 0 for key in native.RELAY_COUNTERS}
        after = {**counts, "reads": 1, "upload_reads": 1}
        self.assertEqual(native.relay_delta(counts, after, "upload_reads"), after)
        for modification in ({"upload_reads": 0}, {"writes": 1}, {"source_writes": 1}, {"secret_writes": 1}, {"commit_faults": 1}):
            with self.subTest(modification=modification), self.assertRaises(native.Failure):
                native.relay_delta(counts, {**after, **modification}, "upload_reads")


class FixtureExecution(unittest.TestCase):
    def setUp(self):
        self.node = shutil.which("node")
        if not self.node:
            self.skipTest("Node is needed to execute the portable guest fixture")
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)

    def test_build_script_fails_with_42_and_is_removed_by_source_fix(self):
        native.write_fixture(self.root, "broken", fail_build=True)
        script = json.loads((self.root / "package.json").read_text())["scripts"]["build"]
        command = shlex.split(script)
        command[0] = self.node
        outcome = subprocess.run(command, cwd=self.root, capture_output=True, timeout=10, check=False)
        self.assertEqual(outcome.returncode, 42)
        self.assertIn(b"start-native intentional build failure", outcome.stderr)
        native.write_fixture(self.root, "fixed")
        self.assertNotIn("build", json.loads((self.root / "package.json").read_text())["scripts"])
        self.assertIn('const marker = "fixed"', (self.root / "handler.js").read_text())

    def guest(self, environment=None):
        with socket.socket() as address:
            address.bind(("127.0.0.1", 0))
            port = address.getsockname()[1]
        process = subprocess.Popen([self.node, "handler.js"], cwd=self.root,
                                   env={**os.environ, "PORT": str(port), native.CONFIGURATION_KEY: "", **(environment or {})},
                                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        self.addCleanup(lambda: process.wait(timeout=5))
        self.addCleanup(process.kill)
        # This test's loopback guest has no proxy or credentials. Production
        # requests continue to use the inherited proxy/CA configuration.
        client = native.urllib.request.build_opener(native.urllib.request.ProxyHandler({}), native.NoRedirect())
        def get(path):
            try:
                with client.open("http://127.0.0.1:" + str(port) + path, timeout=1) as response:
                    self.assertEqual(response.headers.get("Cache-Control"), "no-store")
                    return response.status, json.loads(response.read(4096))
            except native.urllib.error.HTTPError as error:
                with error:
                    self.assertEqual(error.headers.get("Cache-Control"), "no-store")
                    return error.code, json.loads(error.read(4096))
        deadline = time.monotonic() + 5
        while True:
            try:
                self.assertEqual(get("/")[0], 200)
                break
            except OSError:
                if time.monotonic() >= deadline:
                    self.fail("portable guest did not start")
                time.sleep(0.02)
        return get

    def test_guest_emits_503_until_control_request_restores_200(self):
        native.write_fixture(self.root, "portable-nonce")
        get = self.guest()
        control = "/__start_acceptance/portable-nonce"
        self.assertEqual(get(control + "/arm")[0], 200)
        for _ in range(2):
            status, body = get("/")
            self.assertEqual(status, 503)
            self.assertEqual(body, {"marker": "portable-nonce", "response_failed": True, "authorization_seen": False})
        self.assertEqual(get(control + "/recover")[0], 200)
        status, body = get("/")
        self.assertEqual(status, 200)
        self.assertFalse(body["response_failed"])

    def test_source_guard_rejects_leaked_file_or_assignment_before_stamping(self):
        secret = "portable-fixture-private-value"
        digest = hashlib.sha256(secret.encode()).hexdigest()
        native.write_fixture(self.root, "configured", configuration_digest=digest)
        for name in (native.CONFIGURATION_FILE, "renamed.txt", "nested/leak.txt"):
            path = self.root / name
            path.parent.mkdir(exist_ok=True)
            path.write_text(native.CONFIGURATION_KEY + "=" + secret + "\n")
            result = subprocess.run([self.node, "check-source.js"], cwd=self.root, capture_output=True, timeout=10, check=False)
            with self.subTest(name=name):
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse((self.root / native.SOURCE_STAMP).exists())
                self.assertNotIn(secret.encode(), result.stderr + result.stdout)
            path.unlink()
        subprocess.run([self.node, "check-source.js"], cwd=self.root, capture_output=True, timeout=10, check=True)
        self.assertTrue(json.loads((self.root / native.SOURCE_STAMP).read_text())["source_checked"])
        native.write_fixture(self.root, "plain")
        self.assertFalse((self.root / native.SOURCE_STAMP).exists())
        self.assertFalse((self.root / "check-source.js").exists())

    def test_guest_proves_exact_configuration_and_executed_guard_without_echoing_secret(self):
        secret = "portable-fixture-private-value"
        native.write_fixture(self.root, "configured", configuration_digest=hashlib.sha256(secret.encode()).hexdigest())
        for path in self.root.iterdir():
            self.assertNotIn(secret, path.read_text())
        before = self.guest({native.CONFIGURATION_KEY: secret})("/")[1]
        self.assertTrue(before["configuration_ok"])
        self.assertFalse(before["source_checked"])
        subprocess.run([self.node, "check-source.js"], cwd=self.root, capture_output=True, timeout=10, check=True)
        for value in ("", "incorrect-value", secret):
            body = self.guest({native.CONFIGURATION_KEY: value})("/")[1]
            with self.subTest(configured=value == secret):
                self.assertIs(body["configuration_ok"], value == secret)
                self.assertTrue(body["source_checked"])
                self.assertNotIn(secret, json.dumps(body))


class HTTPContracts(unittest.TestCase):
    def test_mutating_request_is_not_retried_when_reply_is_lost(self):
        api = native.API("https://api.fixture.example", "credential")
        with patch.object(api.opener, "open", side_effect=OSError("sensitive wire output")) as send:
            with self.assertRaises(native.Failure) as raised:
                api.request("POST", "/v1/projects", {})
        self.assertEqual(send.call_count, 1)
        self.assertNotIn("sensitive", str(raised.exception))

    def test_redirects_never_forward_the_token(self):
        self.assertIsNone(native.NoRedirect().redirect_request(None, None, 302, "redirect", {}, "https://elsewhere.example"))

    def test_cleanup_deadline_prevents_additional_network_work(self):
        api = native.API("https://api.fixture.example", "credential")
        api.deadline = 1
        with patch.object(api.opener, "open") as send:
            with self.assertRaises(native.Failure):
                api.get("/v1/instances")
        send.assert_not_called()


class ConfigurationContracts(unittest.TestCase):
    def setUp(self):
        self.account = uid()
        self.marker = {"native_x86": True, "account_id": self.account, "api_origin": "https://api.fixture.example", "platform_sha": "a" * 40}
        self.environment = {"FAAS_API": self.marker["api_origin"], "FAAS_TOKEN": "credential", "FAAS_START_ACCEPTANCE_ACCOUNT": self.account,
                            "FAAS_START_ACCEPTANCE_PROJECT": "start-contract", "FAAS_START_ACCEPTANCE_ENVIRONMENT": "staging"}
        self.calls = []
        self.host = SimpleNamespace(lstat=lambda: SimpleNamespace(st_mode=stat.S_IFREG | 0o644, st_uid=0, st_size=256),
                                    read_bytes=lambda: json.dumps(self.marker).encode())
        def get(path):
            self.calls.append(path)
            if path == "/v1/account":
                return 200, {"id": self.account, "plan": "free"}
            raise AssertionError("cleanup must not depend on gateway capabilities")
        self.api = SimpleNamespace(get=get)

    def test_cleanup_keeps_identity_checks_without_runtime_or_capability_dependency(self):
        with patch.dict(os.environ, self.environment), patch.object(native, "HOST_MARKER", self.host), patch.object(native, "API", return_value=self.api), \
                patch.object(native.Path, "is_char_device", return_value=False), patch.object(native.subprocess, "run") as virtual:
            _, target = native.configuration(cleanup=True)
        self.assertEqual(target["account_id"], self.account)
        self.assertEqual(self.calls, ["/v1/account"])
        virtual.assert_not_called()

    def test_cleanup_rejects_another_target_before_any_account_request(self):
        self.marker["api_origin"] = "https://wrong-fleet.example"
        with patch.dict(os.environ, self.environment), patch.object(native, "HOST_MARKER", self.host), patch.object(native, "API", return_value=self.api):
            with self.assertRaises(native.Failure):
                native.configuration(cleanup=True)
        self.assertEqual(self.calls, [])

    def test_native_gate_cannot_turn_missing_kvm_into_a_pass(self):
        with patch.object(native.platform, "system", return_value="Linux"), patch.object(native.platform, "machine", return_value="x86_64"), \
                patch.object(native.Path, "is_char_device", return_value=False), patch.object(native, "API") as api:
            with self.assertRaises(native.Failure):
                native.configuration()
        api.assert_not_called()


if __name__ == "__main__":
    unittest.main()
