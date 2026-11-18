#!/usr/bin/env python3
"""Fresh published install -> browser login -> Free Node Start -> verified edit.

Run on Unix with access to a browser. Terminal input is scripted; browser
sign-in uses the normal product flow. Public reports never contain terminal output,
login URLs, email, tokens, response bodies, or local paths.
"""
import argparse
import contextlib
import hashlib
import http.client
import importlib.util
import json
import math
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import time
import uuid
from urllib.parse import urlsplit

import qualify_release as release

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("start_native", ROOT / "scripts/ci/start-native.py")
native = importlib.util.module_from_spec(spec)
spec.loader.exec_module(native)
PHASES = ("isolated_environment", "published_install", "fresh_browser_login", "free_account_preflight",
          "start_node", "edit_same_app", "cleanup_app", "logout")
PRIVATE_PHASES = (*PHASES[:6], "edited_release_rollout", *PHASES[6:])
NODE = "hello-node"
PRIVATE_NODE = "hello-node-private"
INSTALLER = "https://get.gregale.dev"


class Blocked(native.Failure):
    pass


def fingerprint(value):
    return hashlib.sha256(value.encode()).hexdigest()


def digest_value(value):
    return isinstance(value, str) and re.fullmatch(r"[0-9a-f]{64}", value) is not None


def seconds(value):
    return isinstance(value, (int, float)) and not isinstance(value, bool) and math.isfinite(value) and value >= 0


def isolated_environment(private):
    # These are child-process settings only. Do not mutate the parent's home,
    # global CLI configuration, selected profile, keychain entry, or PATH.
    home = private / "home"
    home.mkdir(mode=0o700)
    env = {key: value for key, value in os.environ.items()
           if not key.startswith(("FAAS_", "GREGALE_", "XDG_", "CODEX_", "CLAUDE_", "CURSOR_", "GEMINI_"))}
    env.update(HOME=str(home), XDG_CONFIG_HOME=str(home / ".config"), XDG_CACHE_HOME=str(home / ".cache"),
               XDG_STATE_HOME=str(private / "state"), FAAS_JSON="0", NO_COLOR="1")
    return env


def documents(output):
    if not output.strip():
        return []
    try:
        values = [json.loads(output)]
    except json.JSONDecodeError:
        values = [json.loads(line) for line in output.splitlines() if line.strip()]
    result = []
    for row in values:
        if isinstance(row, list):
            result.extend(row)
        elif isinstance(row, dict) and isinstance(row.get("items"), list):
            result.extend(row["items"])
        else:
            result.append(row)
    native.require(all(isinstance(row, dict) for row in result), "CLI returned an invalid document collection")
    return result


def verify_response(document, session, greeting):
    native.require(native.identity(session.get("app_id")) and native.identity(session.get("deployment_id"))
                   and document.get("verified") is True and document.get("public_access_verified") is False
                   and document.get("http_status") == 200 and document.get("scope") == "default"
                   and document.get("app_id") == session["app_id"]
                   and document.get("deployment_id") == session["deployment_id"]
                   and native.identity(document.get("instance_id"))
                   and document.get("project_id") in (None, "", session.get("project_id"))
                   and not document.get("environment_id")
                   and not document.get("body_truncated"), "response did not prove this exact deployment")
    body = document.get("body")
    native.require(isinstance(body, str) and len(body.encode()) <= 4096, "response exceeded the bounded greeting check")
    payload = json.loads(body)
    native.require(isinstance(payload, dict) and payload.get("message") == greeting, "response did not contain the expected source greeting")
    return {"app_sha256": fingerprint(session["app_id"]), "deployment_sha256": fingerprint(session["deployment_id"]),
            "instance_sha256": fingerprint(document["instance_id"]), "greeting_sha256": fingerprint(greeting),
            "http_status": 200, "verified": True}


def anonymous_status(url, slug):
    target = urlsplit(url)
    host = slug + ".gregale.dev"
    native.require(target.scheme == "https" and target.netloc == host and target.path in ("", "/")
                   and not target.query and not target.fragment, "unexpected production ingress URL for anonymous check")
    with contextlib.closing(http.client.HTTPSConnection(host, timeout=15)) as connection:
        connection.request("GET", "/")
        with connection.getresponse() as response:
            return response.status


def validate(report):
    """A fixture, wrong plan, incomplete step, bad identity or cleanup cannot pass."""
    if report.get("provenance", {}).get("evidence_kind") != "deployed":
        return "fixture_only"
    phases, proof = report.get("phases", {}), report.get("proof", {})
    template = report["provenance"].get("template", NODE)
    native.require(template in (NODE, PRIVATE_NODE), "unexpected onboarding template")
    required_phases = PRIVATE_PHASES if template == PRIVATE_NODE else PHASES
    if not all(phases.get(name, {}).get("status") == "passed" for name in required_phases):
        return "incomplete"
    native.require(all(seconds(phases[name].get("seconds")) for name in required_phases), "phase timings are missing or invalid")
    artifact, provenance = report["artifact"], report["provenance"]
    native.require(artifact.get("status") == "passed" and artifact.get("checksum_verified") is True
                   and artifact.get("release_verify_available") is True
                   and artifact.get("archive_sha256") == artifact.get("expected_archive_sha256")
                   and provenance.get("cli_sha256") == artifact.get("cli_sha256")
                   and provenance.get("cli_version") == artifact.get("release_tag")
                   and isinstance(artifact.get("release_tag"), str) and release.TAG.fullmatch(artifact["release_tag"])
                   and isinstance(artifact.get("cli_source_sha"), str) and release.SHA.fullmatch(artifact["cli_source_sha"])
                   and all(digest_value(artifact.get(key)) for key in
                           ("installer_sha256", "archive_sha256", "cli_sha256")), "published installer provenance is incomplete")
    native.require(provenance.get("plan") == "free" and artifact.get("installer_cli_matches_archive") is True
                   and proof.get("signed_out_before_login") is True and proof.get("expected_account_verified") is True
                   and proof.get("initial_app_count") == 0 and proof.get("final_app_count") == 0
                   and proof.get("submitted_releases") == 2 and proof.get("live_app_count") == 1,
                   "Free onboarding evidence is incomplete")
    first, edited = proof.get("first_response", {}), proof.get("edited_response", {})
    for response in (first, edited):
        native.require(response.get("verified") is True and response.get("http_status") == 200
                       and all(digest_value(response.get(name)) for name in
                               ("app_sha256", "deployment_sha256", "instance_sha256", "greeting_sha256")),
                       "response evidence is incomplete")
    native.require(first["app_sha256"] == edited["app_sha256"]
                   and first["deployment_sha256"] != edited["deployment_sha256"]
                   and first["greeting_sha256"] != edited["greeting_sha256"]
                   and proof.get("source_before_sha256") != proof.get("source_after_sha256")
                   and all(digest_value(proof.get(name)) for name in
                           ("source_before_sha256", "source_after_sha256")), "edit did not prove a new source revision of the same app")
    if template == PRIVATE_NODE:
        rollout = proof.get("edited_release_rollout", {})
        native.require(artifact.get("private_starter_available") is True and proof.get("project_absent_before") is True
                       and proof.get("project_absent_after") is True
                       and digest_value(first.get("project_sha256")) and first["project_sha256"] == edited.get("project_sha256")
                       and all(row.get("anonymous_http_status") == 404 for row in (first, edited))
                       and rollout.get("deployment_sha256") == edited["deployment_sha256"] and rollout.get("traffic_percent") == 100,
                       "private project, anonymous ingress or rollout evidence is incomplete")
    metrics = report.get("metrics", {})
    names = ("install_s", "browser_login_s", "first_response_from_start_s", "edit_to_response_s", "complete_s")
    native.require(all(seconds(metrics.get(name)) for name in names), "onboarding timings are missing or invalid")
    native.require(metrics["install_s"] <= metrics["first_response_from_start_s"] <= metrics["complete_s"] <= 3600
                   and metrics["browser_login_s"] <= metrics["first_response_from_start_s"]
                   and metrics["edit_to_response_s"] <= metrics["complete_s"],
                   "onboarding exceeded its chronological one-hour acceptance window")
    return "passed"


def render(report):
    lines = ["# Free Node fresh-onboarding acceptance", "", f"Outcome: **{report['status']}**.", "",
             f"Reason: `{report['reason']}`.", "", "| Phase | Result | Seconds |", "|---|---|---|"]
    lines += [f"| {name} | {row['status']} | {row.get('seconds') if row.get('seconds') is not None else '—'} |"
              for name, row in report["phases"].items()]
    lines += ["", "| Provenance | Value |", "|---|---|"]
    for key, value in {**report["provenance"], **report["artifact"], **report["metrics"]}.items():
        lines.append(f"| {key} | {str(value).replace('|', '/').replace(chr(10), ' ')} |")
    lines += ["", "Terminal input is scripted through a real PTY; browser authorization uses the normal login flow.",
              "The installer uses its default version selection and an isolated destination; the harness adds that bin to its child PATH.",
              "Browser account signup/password entry, shell startup-file integration, other runtimes/platforms, and agent success remain unmeasured.",
              "Customer timing includes install and CLI browser authorization through the verified edit; cleanup is required and timed separately.",
              "One run is an observation, not a latency percentile or reliability estimate."]
    if report["provenance"].get("template") == PRIVATE_NODE:
        lines += ["", "Private verification checks both exact releases through the owner account and requires anonymous HTTP 404.",
                  "The edited release's rollout to 100% traffic is checked and timed after the verified response."]
    return "\n".join(lines) + "\n"


class Run:
    def __init__(self, args):
        self.args, self.out = args, args.out.resolve()
        self.template = args.template
        self.private_node = self.template == PRIVATE_NODE
        native.require(not self.out.exists(), "output must be a fresh directory")
        self.out.mkdir(parents=True, mode=0o700)
        self.private = self.out / "private"
        self.private.mkdir(mode=0o700)
        self.env = isolated_environment(self.private)
        self.profile = "fh-free-" + uuid.uuid4().hex[:16]
        self.workspace = self.private / "workspace"
        self.workspace.mkdir()
        self.slug = "fh-free-" + uuid.uuid4().hex[:16]
        self.source = self.workspace / self.slug
        self.cli = self.private / "bin/gregale"
        self.api = release.api_origin(args.api)
        self.account, self.session, self.registered = None, None, False
        self.profile_created = False
        self.started = time.monotonic()
        self.report = {"schema_version": 1, "status": "failed", "reason": "onboarding_interrupted",
                       "provenance": {"evidence_kind": "deployed", "target": self.api, "plan": "not_measured",
                                      "template": self.template, "browser_flow": "device" if args.device_login else "default",
                                      "credential_storage": "isolated_file" if args.file_credentials else "default",
                                      "interaction": "scripted_pty_and_browser", "started_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())},
                       "phases": {name: {"status": "not_run", "seconds": None}
                                  for name in (PRIVATE_PHASES if self.private_node else PHASES)},
                       "artifact": {}, "proof": {}, "metrics": {}}

    @contextlib.contextmanager
    def phase(self, name):
        started = time.monotonic()
        self.report["phases"][name]["status"] = "running"
        self.checkpoint()
        try:
            yield
        except BaseException as error:
            blocked = isinstance(error, Blocked) or isinstance(error, release.QualificationError) and error.status == "blocked"
            self.report["phases"][name]["status"] = "blocked" if blocked else "failed"
            raise
        else:
            self.report["phases"][name]["status"] = "passed"
        finally:
            self.report["phases"][name]["seconds"] = round(time.monotonic() - started, 3)
            self.checkpoint()

    def checkpoint(self):
        native.save(self.out / "onboarding.json", self.report)
        (self.out / "onboarding.md").write_text(render(self.report))

    def call(self, *arguments, expect=0, timeout=90):
        result = subprocess.run([str(self.cli), "--profile", self.profile, "--non-interactive", "--json", *arguments],
                                cwd=self.workspace, env=self.env, capture_output=True, text=True, timeout=timeout)
        if result.returncode != expect:
            path = self.private / "last-cli-failure.log"
            path.touch(mode=0o600, exist_ok=True)
            path.write_text((result.stdout + "\n" + result.stderr)[-1024 * 1024:])
            raise native.Failure("CLI check failed: " + arguments[0])
        return documents(result.stdout) if expect == 0 else result

    def require_absent(self, resource):
        missing = self.call(resource, "get", self.slug, expect=4)
        problem = documents(missing.stderr)
        native.require(len(problem) == 1 and problem[0].get("code") == "not_found",
                       "generated " + resource + " name was not proven absent")

    @contextlib.contextmanager
    def terminal(self, label, arguments):
        with native.Terminal(self.cli, self.workspace, self.env, arguments=arguments, token_login=False) as terminal:
            try:
                yield terminal
            except BaseException:
                path = self.private / (label + "-failure.log")
                path.touch(mode=0o600, exist_ok=True)
                path.write_text(terminal.output)
                raise

    def install(self):
        with self.phase("isolated_environment"):
            native.require(not self.cli.exists() and not self.source.exists(), "isolated install/source paths are not fresh")
            native.require(not any(key in self.env for key in ("FAAS_TOKEN", "FAAS_API", "GREGALE_PROFILE", "GREGALE_KEYRING")),
                           "inherited connection settings reached the clean environment")
            if self.args.file_credentials:
                self.env["GREGALE_KEYRING"] = "off"
        with self.phase("published_install"):
            start = time.monotonic()
            installer = self.private / "install.sh"
            release.download(INSTALLER, installer, 1024 * 1024)
            self.cli.parent.mkdir()
            log = self.private / "install.log"
            with log.open("x") as output:
                log.chmod(0o600)
                result = subprocess.run(["sh", str(installer), "--dir", str(self.cli.parent)], env=self.env,
                                        stdout=output, stderr=subprocess.STDOUT, timeout=300, check=False)
            native.require(result.returncode == 0 and self.cli.is_file(), "published installer did not produce a CLI")
            identity = subprocess.run([str(self.cli), "version", "--json"], env=self.env, capture_output=True, text=True,
                                      timeout=30, check=True)
            version = json.loads(identity.stdout)["version"]
            with tempfile.TemporaryDirectory(prefix="gregale-onboarding-archive-") as temporary:
                release.prepare_cli(version, Path(temporary), self.report["artifact"])
            native.require(release.digest(self.cli) == self.report["artifact"]["cli_sha256"], "installer binary differs from its published archive")
            self.report["artifact"].update(installer_url=INSTALLER, installer_sha256=release.digest(installer), installer_cli_matches_archive=True)
            self.report["provenance"].update(cli_version=version, cli_sha256=self.report["artifact"]["cli_sha256"])
            revision = subprocess.run(["git", "rev-parse", "HEAD"], cwd=ROOT, capture_output=True, text=True, check=True).stdout.strip()
            dirty = bool(subprocess.run(["git", "status", "--porcelain"], cwd=ROOT, capture_output=True, text=True, check=True).stdout.strip())
            self.report["provenance"].update(harness_revision=revision, harness_worktree_dirty=dirty)
            self.report["metrics"]["install_s"] = round(time.monotonic() - start, 3)
            self.env["PATH"] = str(self.cli.parent) + os.pathsep + self.env.get("PATH", "")
            added = subprocess.run([str(self.cli), "profile", "add", self.profile, self.api], env=self.env,
                                   capture_output=True, timeout=30, check=False)
            native.require(added.returncode == 0, "could not create the isolated connection")
            self.profile_created = True
            if self.private_node:
                templates = self.call("init", "--list")
                available = len(templates) == 1 and self.template in (templates[0].get("templates") or [])
                self.report["artifact"]["private_starter_available"] = available
                if not available:
                    raise Blocked("default_installer_does_not_include_private_node_starter")

    def login(self):
        with self.phase("fresh_browser_login"):
            self.call("whoami", expect=2)
            self.report["proof"]["signed_out_before_login"] = True
            start = time.monotonic()
            arguments = ("--profile", self.profile, "login")
            if self.args.device_login:
                arguments += ("--device",)
            with self.terminal("login", arguments) as terminal:
                terminal.expect_any(("Opening https://", "Open https://"), timeout=45)
                # A private handoff receipt lets an observer open the normal
                # approval page. Never put it in the public report/transcript.
                deadline = time.monotonic() + 10
                url = None
                while time.monotonic() < deadline and not url:
                    terminal.pump()
                    match = re.search(r"(?:Opening|Open) (https://[^\s]+)", terminal.output)
                    if match and "\n" in terminal.output[match.end():]:
                        url = match.group(1)
                native.require(url is not None, "browser authorization URL was not observed")
                handoff = {"url": url, "phase": "browser_login"}
                if match := re.search(r"Your code: ([A-Z0-9-]+)\s", terminal.output):
                    handoff["code"] = match.group(1)
                native.require(not self.args.device_login or "code" in handoff, "browser device code was not observed")
                native.save(self.private / "browser-handoff.json", handoff)
                print("Complete the isolated CLI login in your browser; the acceptance is waiting for approval.", flush=True)
                terminal.wait(0, timeout=360)
                native.require("Logged in as " in terminal.output, "normal browser login did not complete")
            self.account = self.call("whoami")[0]
            native.require(native.identity(self.account.get("id")), "account identity is missing")
            if self.account.get("email", "").strip().lower() != self.args.expected_email.strip().lower():
                raise Blocked("browser_login_selected_a_different_account")
            self.report["proof"]["expected_account_verified"] = True
            self.report["metrics"]["browser_login_s"] = round(time.monotonic() - start, 3)
            (self.private / "browser-handoff.json").unlink(missing_ok=True)

    def preflight(self):
        with self.phase("free_account_preflight"):
            plan = self.account.get("plan")
            self.report["provenance"]["plan"] = plan if plan in ("free", "hobby", "pro", "scale") else "unknown"
            if self.account.get("plan") != "free":
                raise Blocked("selected_account_is_not_on_the_free_plan")
            apps = self.call("app", "list")
            if apps:
                raise Blocked("free_account_requires_an_unused_app_slot")
            self.report["proof"]["initial_app_count"] = len(apps)
            self.require_absent("app")
            if self.private_node:
                self.require_absent("project")
                self.report["proof"]["project_absent_before"] = True

    def read_session(self):
        paths = list((Path(self.env["XDG_STATE_HOME"]) / "gregale/start").glob("*.json"))
        native.require(len(paths) == 1, "Start did not retain exactly one session")
        session = json.loads(paths[0].read_text())
        native.require(session.get("api_base", "").rstrip("/") == self.api
                       and session.get("account_id") == self.account["id"]
                       and session.get("app_slug") == self.slug and session.get("template") == self.template
                       and Path(session.get("source_path", "")).resolve() == self.source.resolve()
                       and native.identity(session.get("app_id")) and native.identity(session.get("deployment_id"))
                       and not session.get("environment") and not session.get("environment_id")
                       and session.get("verified_deployment_id") == session["deployment_id"]
                       and session.get("status") == "live", "Start session did not prove the expected accepted and verified target")
        if self.private_node:
            native.require(session.get("project") == self.slug and native.identity(session.get("project_id")),
                           "Start did not retain the private project identity")
        else:
            native.require(not session.get("project") and not session.get("project_id"), "Start selected an unexpected project")
        if self.session:
            native.require(session["app_id"] == self.session["app_id"] and session.get("project_id") == self.session.get("project_id"),
                           "edit replaced the original app or project")
        self.session = session
        native.save(self.private / "ownership.json", {key: session[key] for key in ("account_id", "app_slug", "app_id", "deployment_id")})
        return session

    def response(self, session, greeting):
        rows = self.call("release", "verify", session["deployment_id"], "--app", self.slug, "--path", "/")
        native.require(len(rows) == 1, "response check did not return one evidence object")
        proof = verify_response(rows[0], session, greeting)
        if self.private_node:
            apps = self.call("app", "get", self.slug)
            native.require(len(apps) == 1 and apps[0].get("id") == session["app_id"]
                           and apps[0].get("project_id") == session["project_id"] and apps[0].get("visibility") == "internal",
                           "owner response did not come from the expected internal project app")
            status = anonymous_status(apps[0]["url"], self.slug)
            native.require(status == 404, "anonymous ingress did not return 404")
            proof.update(project_sha256=fingerprint(session["project_id"]), anonymous_http_status=status)
        return proof

    @staticmethod
    def private_review(terminal):
        terminal.expect("Access   Internal only", timeout=120)
        terminal.answer("Deploy this app (y/N) [n]: ", "y")
        terminal.expect("Application response verified")
        terminal.expect("owner-authorized check", timeout=15)
        terminal.expect("Keep building in this project.", timeout=30)

    def start_and_edit(self):
        # Register the single random, previously absent name before approving
        # its first mutation so cleanup is attempted after any later failure.
        self.registered = True
        with self.terminal("start", ("--profile", self.profile, "start")) as terminal:
            with self.phase("start_node"):
                terminal.answer("Choose [2]: ", "2")
                if self.private_node:
                    terminal.expect("Private Node.js HTTP app (Free plan)", timeout=30)
                    choice = re.search(r"(?m)^\s*(\d+)\. Private Node\.js HTTP app \(Free plan\)", terminal.output)
                    native.require(choice is not None, "private starter choice was not observed")
                    terminal.answer("Choose [1]: ", choice[1])
                else:
                    terminal.answer("Choose [1]: ", "1")
                terminal.answer("Starter directory [", str(self.source))
                if self.private_node:
                    self.private_review(terminal)
                else:
                    terminal.answer("Deploy this app (y/N) [n]: ", "y")
                    terminal.expect("New public greeting (Enter to finish): ", timeout=1200)
                    native.require("Application response verified" in terminal.output, "Start did not verify its first response")
                first = self.read_session()
                source = self.source / ("app/server.js" if self.private_node else "handler.js")
                content = source.read_text()
                greetings = list(re.finditer(r'\bmessage\s*:\s*("(?:[^"\\]|\\.)*")', content))
                native.require(len(greetings) == 1, "Node starter greeting has an unexpected shape")
                greeting = json.loads(greetings[0][1])
                self.report["proof"]["source_before_sha256"] = release.digest(source)
                self.report["proof"]["first_response"] = self.response(first, greeting)
                self.report["metrics"]["first_response_from_start_s"] = round(time.monotonic() - self.started, 3)
            with self.phase("edit_same_app"):
                start = time.monotonic()
                changed = "first-hour-free-edit-" + uuid.uuid4().hex[:12]
                if self.private_node:
                    left, right = greetings[0].span(1)
                    source.write_text(content[:left] + json.dumps(changed) + content[right:])
                    terminal.expect("I've made changes — review and deploy again", timeout=30)
                    terminal.answer("Choose [1]: ", "2")
                    self.private_review(terminal)
                    terminal.answer("Choose [1]: ", "1")
                else:
                    os.write(terminal.master, (changed + "\n").encode())
                    terminal.answer("Apply this greeting and deploy a new revision (y/N) [n]: ", "y")
                    terminal.expect("You changed the code, deployed it, and saw the new greeting live.", timeout=1200)
                terminal.wait(0, timeout=30)
                edited = self.read_session()
                native.require(first["deployment_id"] != edited["deployment_id"], "edit did not create a distinct deployment")
                self.report["proof"]["edited_response"] = self.response(edited, changed)
                self.report["proof"]["source_after_sha256"] = release.digest(source)
                releases = self.call("release", "list", "--app", self.slug, "--limit", "3")
                native.require({row.get("id") for row in releases} == {first["deployment_id"], edited["deployment_id"]}
                               and len(releases) == 2, "Start edit submitted unexpected extra releases")
                apps = self.call("app", "list")
                native.require(len(apps) == 1 and apps[0].get("id") == edited["app_id"], "Start edit created an unexpected app")
                self.report["proof"].update(submitted_releases=2, live_app_count=len(apps))
                self.report["metrics"]["edit_to_response_s"] = round(time.monotonic() - start, 3)
                self.report["metrics"]["complete_s"] = round(time.monotonic() - self.started, 3)
        if self.private_node:
            with self.phase("edited_release_rollout"):
                deployment = self.session["deployment_id"]
                self.call("release", "wait", deployment, "--app", self.slug, "--rollout", "--timeout", "10m", timeout=630)
                traffic = self.call("release", "traffic", "status", self.slug)
                native.require(len(traffic) == 1 and traffic[0].get("total_percent") == 100
                               and any(row.get("id") == deployment and row.get("traffic_percent") == 100
                                       for row in traffic[0].get("deployments", [])), "edited release did not reach 100% traffic")
                self.report["proof"]["edited_release_rollout"] = {"deployment_sha256": fingerprint(deployment), "traffic_percent": 100}

    def cleanup(self):
        errors = []
        if self.registered:
            try:
                with self.phase("cleanup_app"):
                    apps = self.call("app", "list")
                    selected = [app for app in apps if app.get("slug") == self.slug]
                    native.require(len(selected) <= 1, "cleanup found ambiguous app ownership")
                    if selected and self.session:
                        native.require(selected[0].get("id") == self.session["app_id"], "cleanup refused a replaced app")
                    if self.private_node:
                        projects = self.call("project", "list")
                        matches = [project for project in projects if project.get("slug") == self.slug]
                        native.require(len(matches) <= 1, "cleanup found ambiguous project ownership")
                        if matches:
                            project_id = matches[0].get("id")
                            native.require(native.identity(project_id)
                                           and (not self.session or project_id == self.session["project_id"])
                                           and (not selected or selected[0].get("project_id") == project_id),
                                           "cleanup refused a replaced project")
                            self.call("project", "delete", self.slug, "--yes")
                        self.require_absent("project")
                        self.report["proof"]["project_absent_after"] = True
                    if selected:
                        self.call("app", "delete", self.slug, "--yes")
                    self.require_absent("app")
                    remaining = self.call("app", "list")
                    self.report["proof"]["final_app_count"] = len(remaining)
                    native.require(not remaining, "Free account still holds an app after the run")
            except (native.Failure, OSError, ValueError, subprocess.SubprocessError):
                errors.append("app_cleanup_failed")
        if self.profile_created:
            try:
                with self.phase("logout"):
                    self.call("logout")
                    self.call("whoami", expect=2)
                    removed = subprocess.run([str(self.cli), "profile", "remove", self.profile], cwd=self.workspace, env=self.env,
                                             capture_output=True, timeout=30, check=False)
                    native.require(removed.returncode == 0, "isolated profile removal failed")
            except (native.Failure, OSError, ValueError, subprocess.SubprocessError):
                errors.append("isolated_login_cleanup_failed")
        return errors


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--api", required=True)
    parser.add_argument("--expected-email", default="", help="expected Free account; compared in memory and excluded from reports")
    parser.add_argument("--template", choices=(NODE, PRIVATE_NODE), default=NODE)
    parser.add_argument("--device-login", action="store_true", help="authorize the isolated CLI via a browser on another machine")
    parser.add_argument("--file-credentials", action="store_true", help="use the isolated home for credentials on a headless system without a keyring")
    parser.add_argument("--install-only", action="store_true", help="check the installer only; leaves onboarding blocked and exits nonzero")
    parser.add_argument("--out", type=Path, required=True)
    args = parser.parse_args()
    if not args.install_only and not args.expected_email.strip():
        parser.error("--expected-email is required for browser/login acceptance")
    run = Run(args)
    try:
        run.install()
        if args.install_only:
            run.report["provenance"]["evidence_kind"] = "installer_only"
            run.call("whoami", expect=2)
            run.report["proof"]["signed_out_before_login"] = True
            raise Blocked("installer_only_browser_login_not_attempted")
        run.login()
        run.preflight()
        run.start_and_edit()
        run.report.update(status="incomplete", reason="awaiting_checked_cleanup")
    except Blocked as error:
        run.report.update(status="blocked", reason=str(error))
    except release.QualificationError as error:
        run.report.update(status=error.status, reason=error.code)
    except (native.Failure, OSError, ValueError, KeyError, subprocess.SubprocessError, KeyboardInterrupt):
        # Raw exceptions/transcripts can contain URLs, account data or paths.
        run.report.update(status="failed", reason="onboarding_step_failed")
    finally:
        failures = run.cleanup()
        if failures:
            run.report.update(status="failed", reason=failures[0])
        elif run.report["status"] == "incomplete":
            try:
                status = validate(run.report)
                run.report.update(status=status, reason="fresh_free_node_onboarding_passed" if status == "passed" else "onboarding_evidence_incomplete")
            except native.Failure:
                run.report.update(status="failed", reason="onboarding_evidence_invalid")
        run.checkpoint()
    print(f"Free Node onboarding: {run.report['status']} ({run.report['reason']})")
    print(f"Report: {run.out / 'onboarding.md'}")
    return int(run.report["status"] != "passed")


if __name__ == "__main__":
    raise SystemExit(main())
