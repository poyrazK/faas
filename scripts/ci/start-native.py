#!/usr/bin/env python3
"""Live Start lifecycle qualification. Portable tests never certify a native run."""
import argparse
import contextlib
import hashlib
import io
import json
import os
from pathlib import Path
import platform
import pty
import re
import selectors
import shutil
import signal
import stat
import subprocess
import sys
import tarfile
import tempfile
import termios
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid


CASES = ("public-default", "private-default", "public-named", "private-named")
ACTIVE = {"WAKING", "COLD_BOOTING", "RUNNING", "PARKING"}
HOST_MARKER = Path("/etc/faas/start-acceptance-host.json")
MAX_WIRE = 1024 * 1024
CONFIGURATION_KEY = "START_NATIVE_REQUIRED"
CONFIGURATION_FILE = "start-native-keys.txt"
SOURCE_STAMP = "start-native-source-checked.json"
COMPATIBILITY_KEY = "START_NATIVE_COMPAT_REQUIRED"
COMPATIBILITY_MODES = ("old-api", "old-gateway", "outage")
SUBMISSION_MODES = {"lost-reply": "lost-commit-reply", "process-killed": "hold-commit-reply"}
RELAY_COUNTERS = ("reads", "capabilities", "capability_faults", "checks", "response_faults", "writes",
                  "app_creates", "secret_writes", "source_writes", "other_writes", "blocked_writes", "forwarded_writes",
                  "commit_faults", "upload_reads")


class Failure(Exception):
    """Only fixed, credential-free diagnostics may leave this harness."""


def require(condition, message):
    if not condition:
        raise Failure(message)


def identity(value):
    try:
        parsed = uuid.UUID(value)
        return parsed.int != 0 and value in (str(parsed), parsed.hex)
    except (ValueError, TypeError, AttributeError):
        return False


def upload_identity(value):
    return isinstance(value, str) and re.fullmatch(r"[0-9a-f]{24}", value) is not None


def save(path, document):
    """Atomic evidence/ownership receipt; never put raw CLI or API output here."""
    path = Path(path)
    with tempfile.NamedTemporaryFile(dir=path.parent, prefix=".start-", delete=False) as handle:
        temporary = Path(handle.name)
        try:
            os.fchmod(handle.fileno(), 0o600)
            handle.write((json.dumps(document, indent=2) + "\n").encode())
            handle.flush()
            os.fsync(handle.fileno())
            os.replace(temporary, path)
        finally:
            temporary.unlink(missing_ok=True)


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


class API:
    def __init__(self, origin, token):
        self.origin, self.token = origin, token
        # Preserve the host's proxy and CA configuration. Never follow an
        # authenticated redirect to a different origin.
        self.opener = urllib.request.build_opener(NoRedirect())

    def request(self, method, path, body=None, headers=None, expected=(200,)):
        outgoing = {"Authorization": "Bearer " + self.token, **(headers or {})}
        if isinstance(body, dict):
            body = json.dumps(body).encode()
            outgoing["Content-Type"] = "application/json"
        request = urllib.request.Request(self.origin + path, data=body, headers=outgoing, method=method)
        remaining = getattr(self, "deadline", None)
        timeout = min(15, remaining - time.monotonic()) if remaining else 15
        require(timeout > 0, "API operation exceeded the cleanup deadline")
        try:
            with self.opener.open(request, timeout=timeout) as response:
                status = response.status
                require(status in expected, "unexpected API response status")
                wire = response.read(MAX_WIRE + 1)
                require(len(wire) <= MAX_WIRE, "API response exceeded the bounded read limit")
                return status, json.loads(wire) if wire else None
        except urllib.error.HTTPError as error:
            status = error.code
            error.close()
            require(status in expected, "API operation failed (HTTP " + str(status) + ")")
            return status, None
        except (OSError, ValueError) as error:
            raise Failure("API operation failed; its outcome may be unknown") from error

    def get(self, path, expected=(200,)):
        return self.request("GET", path, expected=expected)


def configuration(cleanup=False):
    if not cleanup:
        require(platform.system() == "Linux" and platform.machine() == "x86_64", "requires native x86_64 Linux")
        require(Path("/dev/kvm").is_char_device() and os.access("/dev/kvm", os.R_OK | os.W_OK),
                "requires accessible /dev/kvm on the native acceptance host")
    try:
        metadata = HOST_MARKER.lstat()
        require(stat.S_ISREG(metadata.st_mode) and metadata.st_uid == 0 and not metadata.st_mode & 0o022,
                "Start host marker must be a root-owned regular file without group/other write access")
        require(metadata.st_size <= 4096, "Start host marker is too large")
        marker = json.loads(HOST_MARKER.read_bytes())
    except (OSError, ValueError) as error:
        raise Failure("valid Start native host marker is missing") from error
    if not cleanup:
        try:
            virtual = subprocess.run(["systemd-detect-virt", "--vm", "--quiet"], check=False, timeout=10,
                                     stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            require(virtual.returncode == 1, "native Start acceptance does not support nested virtualization")
        except (OSError, subprocess.TimeoutExpired) as error:
            raise Failure("could not verify the native acceptance host") from error
    required = ("FAAS_API", "FAAS_TOKEN", "FAAS_START_ACCEPTANCE_ACCOUNT", "FAAS_START_ACCEPTANCE_PROJECT", "FAAS_START_ACCEPTANCE_ENVIRONMENT")
    require(all(os.environ.get(name) for name in required), "set API, token, acceptance account, new project, and named environment")
    origin = os.environ["FAAS_API"].rstrip("/")
    parsed = urllib.parse.urlsplit(origin)
    require(parsed.scheme == "https" and parsed.hostname and not parsed.username and not parsed.password and
            not parsed.path and not parsed.query and not parsed.fragment, "FAAS_API must be a public HTTPS origin")
    account = os.environ["FAAS_START_ACCEPTANCE_ACCOUNT"]
    project = os.environ["FAAS_START_ACCEPTANCE_PROJECT"]
    environment = os.environ["FAAS_START_ACCEPTANCE_ENVIRONMENT"]
    require(identity(account), "acceptance account must be an exact UUID")
    require(re.fullmatch(r"start-[a-z0-9](?:[a-z0-9-]{0,12}[a-z0-9])?", project), "use a fresh start- project slug of at most 20 characters")
    require(re.fullmatch(r"[a-z][a-z0-9-]{0,30}[a-z0-9]", environment) and environment != "default", "select a named environment")
    require(marker.get("native_x86") is True and marker.get("api_origin") == origin and marker.get("account_id") == account,
            "API/account do not match the designated nonproduction native fleet")
    require(re.fullmatch(r"[0-9a-f]{40}", marker.get("platform_sha", "")), "host marker needs the installed fleet release SHA")
    api = API(origin, os.environ["FAAS_TOKEN"])
    _, who = api.get("/v1/account")
    require(isinstance(who, dict) and who.get("id") == account, "token must identify the selected fixture account")
    if not cleanup:
        require(who.get("plan") in ("pro", "scale"), "select a Pro/Scale fixture account")
        _, capabilities = api.get("/v1/deployments/response-check/capabilities")
        require(isinstance(capabilities, dict) and capabilities.get("protocol_version") == 1 and capabilities.get("available") is True,
                "install the compatible API/gateway response-check capability first")
    return api, {"api_origin": origin, "account_id": account, "project_slug": project,
                 "named_environment": environment, "installed_platform_sha": marker["platform_sha"], "native_x86": True}


def source_identity(repo, cli):
    def git(*args):
        return subprocess.check_output(["git", "-C", str(repo), *args], timeout=15)
    patch = git("diff", "--binary", "HEAD")
    digest = hashlib.sha256(patch)
    untracked = git("ls-files", "--others", "--exclude-standard", "-z").split(b"\0")
    for name in sorted(filter(None, untracked)):
        path = repo / os.fsdecode(name)
        require(path.is_file() and not path.is_symlink(), "source fingerprint requires regular untracked files")
        digest.update(name + b"\0")
        digest.update(path.read_bytes())
    return {"checkout_sha": git("rev-parse", "HEAD").decode().strip(), "dirty": bool(patch or any(untracked)),
            "changes_sha256": digest.hexdigest(), "cli_sha256": hashlib.sha256(cli.read_bytes()).hexdigest()}


def write_fixture(directory, marker, fail_build=False, configuration_digest=None):
    directory.mkdir(parents=True, exist_ok=True)
    package = {"name": directory.name, "private": True, "type": "module", "main": "handler.js",
               "engines": {"node": ">=22"}, "scripts": {"start": "node handler.js"}}
    guard, stamp = directory / "check-source.js", directory / SOURCE_STAMP
    guard.unlink(missing_ok=True)
    stamp.unlink(missing_ok=True)
    configuration_imports, configuration_check, configuration_response = "", "", ""
    if configuration_digest is not None:
        require(not fail_build and re.fullmatch(r"[0-9a-f]{64}", configuration_digest), "invalid fixture configuration digest")
        package["scripts"]["build"] = "node check-source.js"
        # This runs inside the real builder VM. A stamp absent from uploaded
        # source makes the guest prove the guard actually ran before serving.
        guard.write_text(
            'import fs from "node:fs";\n'
            'import path from "node:path";\n'
            'function check(directory) {\n'
            '  for (const entry of fs.readdirSync(directory, {withFileTypes: true})) {\n'
            '    if (entry.name === "node_modules" || entry.name === ".git") continue;\n'
            '    if (entry.name === ' + json.dumps(CONFIGURATION_FILE) + ') throw new Error("fixture secret file reached build source");\n'
            '    const file = path.join(directory, entry.name);\n'
            '    if (entry.isDirectory()) check(file);\n'
            '    else if (entry.isFile() && /^' + CONFIGURATION_KEY + '=/m.test(fs.readFileSync(file, "utf8")))\n'
            '      throw new Error("fixture secret assignment reached build source");\n'
            '  }\n'
            '}\n'
            'check(".");\n'
            'fs.writeFileSync(' + json.dumps(SOURCE_STAMP) + ', JSON.stringify({source_checked: true}));\n')
        configuration_imports = 'import fs from "node:fs";\nimport {createHash} from "node:crypto";\n'
        configuration_check = (
            'const runtimeConfiguration = process.env.' + CONFIGURATION_KEY + ';\n'
            'const configurationOK = createHash("sha256").update(runtimeConfiguration || "").digest("hex") === ' + json.dumps(configuration_digest) + ';\n'
            'let sourceChecked = false;\n'
            'try { sourceChecked = JSON.parse(fs.readFileSync(new URL("./' + SOURCE_STAMP + '", import.meta.url))).source_checked === true; } catch {}\n')
        configuration_response = ', configuration_ok: configurationOK, source_checked: sourceChecked'
    if fail_build:
        package["scripts"]["build"] = 'node -e "console.error(\'start-native intentional build failure\');process.exit(42)"'
    (directory / "package.json").write_text(json.dumps(package))
    (directory / "gregale.yaml").write_text("hosting:\n  start: node handler.js\n  port: 8080\n  health: /\n")
    # No external packages. Only the optional configuration case needs a
    # disposable runtime secret; its value is never embedded in source/output.
    (directory / "handler.js").write_text(
        'import http from "node:http";\n'
        + configuration_imports +
        'const marker = ' + json.dumps(marker) + ';\n'
        + configuration_check +
        'let responseFailed = false;\n'
        'http.createServer((req, res) => {\n'
        '  const control = "/__start_acceptance/" + marker;\n'
        '  if (req.url === control + "/arm") responseFailed = true;\n'
        '  if (req.url === control + "/recover") responseFailed = false;\n'
        '  if (req.url === "/" && responseFailed) res.statusCode = 503;\n'
        '  res.setHeader("Cache-Control", "no-store");\n'
        '  res.setHeader("Content-Type", "application/json");\n'
        '  res.end(JSON.stringify({marker, response_failed: responseFailed, authorization_seen: !!req.headers.authorization' + configuration_response + '}));\n'
        '}).listen(Number(process.env.PORT || 8080), "0.0.0.0");\n')


def prepare_sources(root, project, run_id):
    root.mkdir()
    (root / "package.json").write_text(json.dumps({"name": "start-native-fixture", "private": True, "workspaces": ["apps/*"]}))
    for case in CASES:
        write_fixture(root / "apps" / (project + "-" + case), run_id + "-" + case + "-v1")


def source_multipart(root, project):
    archive = io.BytesIO()
    with tarfile.open(fileobj=archive, mode="w:gz") as tar:
        for path in sorted(root.rglob("*")):
            if path.is_file():
                # The source API consumes one archive wrapper. Give it an
                # explicit wrapper so the first apps/ member stays nested.
                tar.add(path, arcname="fixture/" + path.relative_to(root).as_posix())
    boundary = "start-" + uuid.uuid4().hex
    body = bytearray()
    for name, value in (("project_slug", project), ("production_branch", "main"), ("no_triggers", "true")):
        body.extend(('--' + boundary + '\r\nContent-Disposition: form-data; name="' + name + '"\r\n\r\n' + value + '\r\n').encode())
    body.extend(('--' + boundary + '\r\nContent-Disposition: form-data; name="source"; filename="fixture.tar.gz"\r\nContent-Type: application/gzip\r\n\r\n').encode())
    body.extend(archive.getvalue())
    body.extend(('\r\n--' + boundary + '--\r\n').encode())
    return bytes(body), {"Content-Type": "multipart/form-data; boundary=" + boundary}


class Terminal:
    """Real CLI, terminal stdin, plain bounded output kept only in memory."""
    def __init__(self, cli, directory, environment):
        master, slave = pty.openpty()
        settings = termios.tcgetattr(slave)
        settings[3] &= ~termios.ECHO
        termios.tcsetattr(slave, termios.TCSANOW, settings)
        try:
            self.process = subprocess.Popen([str(cli), "start"], cwd=directory, env=environment, stdin=slave,
                                            stdout=subprocess.PIPE, stderr=subprocess.STDOUT, start_new_session=True)
        except BaseException:
            os.close(master)
            raise
        finally:
            os.close(slave)
        self.master, self.output, self.cursor = master, "", 0
        self.selector = selectors.DefaultSelector()
        self.selector.register(self.process.stdout, selectors.EVENT_READ)

    def pump(self, seconds=0.1):
        for key, _ in self.selector.select(seconds):
            chunk = os.read(key.fd, 16384)
            if not chunk:
                self.selector.unregister(key.fileobj)
            self.output += chunk.decode(errors="replace")
            require(len(self.output) <= MAX_WIRE, "CLI output exceeded the bounded in-memory limit")

    def expect_any(self, choices, timeout=1200):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            for choice in choices:
                at = self.output.find(choice, self.cursor)
                if at >= 0:
                    self.cursor = at + len(choice)
                    return choice
            require(self.process.poll() is None or self.selector.get_map(), "CLI exited before the expected lifecycle milestone")
            self.pump()
        raise Failure("CLI timed out before the expected lifecycle milestone")

    def expect(self, text, timeout=1200):
        self.expect_any((text,), timeout)

    def answer(self, text, value):
        self.expect(text)
        os.write(self.master, (value + "\n").encode())

    def wait(self, code, timeout=30):
        deadline = time.monotonic() + timeout
        while self.process.poll() is None and time.monotonic() < deadline:
            self.pump()
        require(self.process.poll() == code, "CLI exited with an unexpected status or did not stop")
        # Include the last output bytes in privacy checks even when the child
        # exits before the next pump. Never persist this in-memory transcript.
        while self.selector.get_map() and time.monotonic() < deadline:
            self.pump()
        require(not self.selector.get_map(), "CLI output did not close after exit")

    def close(self):
        if self.process.poll() is None:
            with contextlib.suppress(ProcessLookupError):
                os.killpg(self.process.pid, signal.SIGKILL)
        self.process.wait(timeout=10)
        self.selector.close()
        self.process.stdout.close()
        os.close(self.master)

    def __enter__(self):
        return self

    def __exit__(self, *args):
        self.close()


class Launch:
    def __init__(self, cli, source, state, environment, slug, scope):
        self.cli, self.source, self.state, self.environment, self.slug, self.scope = cli, source, state, environment, slug, scope

    def open(self):
        return Terminal(self.cli, self.source, self.environment)


class Relay:
    """A separate fixture connection; core sessions retain their actual API."""
    def __init__(self, executable, api, environment):
        self.process = subprocess.Popen([str(executable)], env={**environment, "FAAS_API": api.origin, "FAAS_TOKEN": api.token},
                                        stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
        self.selector = selectors.DefaultSelector()
        self.selector.register(self.process.stdout, selectors.EVENT_READ)
        self.buffer = b""
        try:
            ready = self.read()
            origin = urllib.parse.urlsplit(ready.get("origin", ""))
            require(ready.get("version") == 1 and origin.scheme == "http" and origin.hostname == "127.0.0.1" and
                    origin.port and not origin.username and not origin.password and not origin.path and not origin.query and not origin.fragment,
                    "acceptance relay did not publish a private loopback connection")
            self.origin = ready["origin"]
        except BaseException:
            self.close()
            raise

    def read(self):
        deadline = time.monotonic() + 15
        while time.monotonic() < deadline:
            if b"\n" in self.buffer:
                line, self.buffer = self.buffer.split(b"\n", 1)
                document = json.loads(line)
                require(isinstance(document, dict), "invalid acceptance relay reply")
                return document
            require(self.process.poll() is None, "acceptance relay exited before its control reply")
            for key, _ in self.selector.select(0.1):
                chunk = os.read(key.fd, 4096)
                require(chunk, "acceptance relay closed its control channel")
                self.buffer += chunk
                require(len(self.buffer) <= 16384, "acceptance relay reply exceeded its bound")
        raise Failure("acceptance relay control timed out")

    def command(self, mode=None, block_writes=None):
        command = {}
        if mode is not None:
            command["mode"] = mode
        if block_writes is not None:
            command["block_writes"] = block_writes
        self.process.stdin.write((json.dumps(command) + "\n").encode())
        self.process.stdin.flush()
        result = self.read()
        require(result.get("mode") in (*COMPATIBILITY_MODES, *SUBMISSION_MODES.values(), "healthy", "response-outage") and
                type(result.get("block_writes")) is bool, "invalid acceptance relay state")
        require((mode is None or result["mode"] == mode) and
                (block_writes is None or result["block_writes"] is block_writes), "acceptance relay did not apply its requested fault or guard")
        counts = result.get("counts", {})
        require(all(type(counts.get(key)) is int and counts[key] >= 0 for key in RELAY_COUNTERS), "invalid acceptance relay counters")
        require(result.get("commit_error", False) is False, "acceptance relay received contradictory or invalid commit evidence")
        self.commit = result.get("commit")
        require(self.commit is None or (isinstance(self.commit, dict) and set(self.commit) == {"upload_id", "deployment_id", "app_id"} and
                upload_identity(self.commit["upload_id"]) and identity(self.commit["deployment_id"]) and identity(self.commit["app_id"])),
                "acceptance relay exposed invalid commit identities")
        return {key: counts[key] for key in RELAY_COUNTERS}

    def close(self):
        self.process.stdin.close()
        try:
            self.process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            self.process.kill()
            self.process.wait(timeout=5)
        self.selector.close()
        self.process.stdout.close()

    def __enter__(self):
        return self

    def __exit__(self, *args):
        self.close()


def relay_delta(before, after, fault=None):
    require(all(type(before.get(key)) is int and type(after.get(key)) is int and after[key] >= before[key]
                for key in RELAY_COUNTERS), "acceptance relay counters moved backwards or became incomplete")
    delta = {key: after[key] - before[key] for key in RELAY_COUNTERS}
    require(all(delta[key] == 0 for key in RELAY_COUNTERS if key not in
                ("reads", "capabilities", "capability_faults", "checks", "response_faults", "upload_reads")),
            "Start attempted a mutation during a guarded recovery or compatibility check")
    require(fault is None or delta[fault] > 0, "Start did not exercise the selected compatibility fault")
    return delta


def configuration_versions(metadata):
    # Runtime delivery acknowledgements may advance after a read. Secret write
    # versions and user-intent timestamps must stay unchanged while blocked.
    return {scope: {kind: sorted([{key: row.get(key) for key in
                                  (("key", "scope", "created_at", "updated_at", "secret_class", "delivery_version")
                                   if kind == "secrets" else ("key", "created_at", "updated_at"))}
                                 for row in rows], key=lambda row: row["key"])
                    for kind, rows in fields.items()} for scope, fields in metadata.items()}


def remove_submission_inputs(launch):
    # Only this runner's generated source lives here. Keep the link so the
    # API/account/project context still resolves to the same recovery record.
    require({path.name for path in launch.source.iterdir()} == {".gregale", "package.json", "handler.js", "gregale.yaml"},
            "submission fixture contains unexpected source files")
    for name in ("package.json", "handler.js", "gregale.yaml"):
        path = launch.source / name
        require(path.is_file() and not path.is_symlink(), "submission fixture source changed type")
        path.unlink()
    caches = (launch.state / "gregale/uploads", Path(launch.environment["XDG_CACHE_HOME"]))
    for path in caches:
        require(not path.is_symlink(), "submission fixture cache changed type")
        if path.exists():
            shutil.rmtree(path)
    require({path.name for path in launch.source.iterdir()} == {".gregale"} and not any(path.exists() for path in caches),
            "submission recovery still has local source or upload cache")


class Run:
    def __init__(self, api, target, report_path, report):
        self.api, self.target, self.path, self.report = api, target, report_path, report

    def save(self):
        save(self.path, self.report)

    def checkpoint(self, milestone):
        self.report["milestone"] = milestone
        self.save()

    def app(self, slug, app_id):
        status, app = self.api.get("/v1/apps/" + slug, expected=(200, 404))
        if status == 200:
            require(isinstance(app, dict) and app.get("id") == app_id and app.get("slug") == slug,
                    "app identity changed; refusing to mutate or delete it")
        return status, app

    def provision(self, root):
        project = self.target["project_slug"]
        expected = {project + "-" + case for case in CASES}
        status, _ = self.api.get("/v1/projects/" + project, expected=(200, 404))
        require(status == 404, "select a fresh project; existing projects cannot be used")
        for slug in sorted(expected):
            status, _ = self.api.get("/v1/apps/" + slug, expected=(200, 404))
            require(status == 404, "a proposed fixture app already exists")
        body, headers = source_multipart(root, project)
        _, plan = self.api.request("POST", "/v1/projects/scan", body, headers)
        require(isinstance(plan, dict) and plan.get("can_apply") is True and plan.get("plan_token"), "fixture scan returned a blocked or incomplete plan")
        require(plan.get("project_slug") == project, "fixture plan changed its project")
        require(len(plan.get("workloads", [])) == len(expected) and {workload.get("name") for workload in plan["workloads"]} == expected,
                "fixture plan has unexpected workload names")
        require(all(workload.get("action") == "create" and not workload.get("existing_app_id") for workload in plan.get("workloads", [])),
                "fixture plan would update an existing workload")
        require(not plan.get("removed") and not plan.get("managed") and not plan.get("crons") and not plan.get("async_routes"),
                "fixture plan would change resources beyond the expected new apps")
        resources = self.report["resources"]
        resources["provisioning"] = "outcome_unknown"
        self.save()  # Durable intent BEFORE the first mutating API operation.
        _, receipt = self.api.request("POST", "/v1/projects?" + urllib.parse.urlencode({"plan_token": plan["plan_token"]}),
                                      body, {**headers, "Idempotency-Key": resources["operation_id"]}, expected=(200, 201, 202))
        require(isinstance(receipt, dict) and identity(receipt.get("project_id")), "project apply omitted its durable identity")
        resources["project_id"] = receipt["project_id"]
        for app in receipt.get("apps", []):
            if app.get("slug") in expected and identity(app.get("id")):
                resources["apps"][app["slug"]] = app["id"]
        self.save()  # Preserve known identities even when a partial apply fails.
        require(set(resources["apps"]) == expected and len(receipt.get("apps", [])) == len(expected) and
                len(set(resources["apps"].values())) == len(expected),
                "project apply did not return every expected app identity")
        resources["provisioning"] = "accepted"
        self.save()
        _, project_row = self.api.get("/v1/projects/" + project)
        require(project_row.get("id") == resources["project_id"] and
                {row.get("slug") for row in project_row.get("workloads", [])} == expected,
                "created project membership differs from the apply receipt")
        _, environment = self.api.request("POST", "/v1/projects/" + project + "/environments",
                                          {"slug": self.target["named_environment"]},
                                          {"Idempotency-Key": resources["operation_id"] + "-environment"}, expected=(200, 201))
        require(identity(environment.get("id")) and environment.get("project_id") == resources["project_id"] and
                environment.get("slug") == self.target["named_environment"], "environment creation returned a different catalog identity")
        resources["environment_id"] = environment["id"]
        self.save()
        # All four apps are newly owned. Set both access gates explicitly;
        # defaults vary by plan and must not turn a private fixture public.
        for case in CASES:
            slug, private = project + "-" + case, case.startswith("private")
            self.app(slug, resources["apps"][slug])
            patch = {"require_authn": private, "public_auth": {"mode": "bearer" if private else "open"},
                     "min_instances": 0, "max_concurrency": 1, "idle_timeout_s": 30,
                     "execution_mode": "request", "pause": "when_idle"}
            for scope in ("", self.target["named_environment"]):
                suffix = "?" + urllib.parse.urlencode({"environment": scope}) if scope else ""
                self.api.request("PATCH", "/v1/apps/" + slug + suffix, patch)

    def deployments(self, slug):
        rows, cursor = [], ""
        for _ in range(20):
            _, page = self.api.get("/v1/apps/" + slug + "/deployments?" + urllib.parse.urlencode({"limit": 100, "before": cursor}))
            require(isinstance(page, dict) and isinstance(page.get("items"), list), "invalid deployment inventory")
            rows.extend(page["items"])
            next_cursor = page.get("next_before", "")
            if not next_cursor:
                return rows
            require(next_cursor != cursor, "deployment pagination did not advance")
            cursor = next_cursor
        raise Failure("deployment inventory exceeded the bounded page limit")

    def session(self, state, slug, scope):
        paths = list((state / "gregale/start").glob("*.json"))
        if not paths:
            return None
        require(len(paths) == 1 and paths[0].stat().st_size <= 65536 and stat.S_IMODE(paths[0].stat().st_mode) == 0o600,
                "Start recovery metadata is missing isolation or private permissions")
        raw = paths[0].read_bytes()
        require(self.api.token.encode() not in raw, "Start recovery metadata contains the account credential")
        document = json.loads(raw)
        resources = self.report["resources"]
        expected = {"api_base": self.target["api_origin"], "account_id": self.target["account_id"], "app_slug": slug,
                    "app_id": resources["apps"][slug], "project": self.target["project_slug"],
                    "project_id": resources["project_id"], "environment": scope,
                    "environment_id": resources["environment_id"] if scope else ""}
        require(all(document.get(key, "") == value for key, value in expected.items()), "Start recovery metadata changed target identity")
        return document

    def accepted(self, terminal, state, slug, scope, previous):
        deadline = time.monotonic() + 60
        while time.monotonic() < deadline:
            document = self.session(state, slug, scope)
            if document and identity(document.get("deployment_id")) and document["deployment_id"] not in previous and not document.get("pending_submission"):
                deployment = document["deployment_id"]
                return self.deployment(slug, scope, deployment)
            require(terminal.process.poll() is None, "CLI exited before saving deployment acceptance")
            terminal.pump()
        raise Failure("CLI did not save an accepted deployment")

    def deployment(self, slug, scope, deployment):
        _, row = self.api.get("/v1/deployments/" + deployment)
        require(row.get("id") == deployment and row.get("app_id") == self.report["resources"]["apps"][slug] and
                row.get("scope", "default") == (scope or "default"), "deployment does not match the reviewed target")
        return row

    def saved_ids(self, launch, accepted, verified):
        document = self.session(launch.state, launch.slug, launch.scope)
        require(document and document.get("deployment_id") == accepted and document.get("verified_deployment_id") == verified and
                not document.get("pending_submission"), "failure/recovery changed accepted or previously verified deployment IDs")
        return {"accepted_id": accepted, "verified_id": verified}

    def build_failure(self, slug, scope, deployment):
        row = self.deployment(slug, scope, deployment)
        require(row.get("status") == "failed" and identity(row.get("build_id")), "expected a failed build, not an unready guest")
        _, build = self.api.get("/v1/builds/" + row["build_id"])
        require(build.get("id") == row["build_id"] and build.get("deployment_id") == deployment and
                build.get("status") == "failed" and build.get("failure_class") == "user_error", "failure did not come from this deployment's user build")
        return row, {key: build[key] for key in ("id", "deployment_id", "status", "failure_class")}

    def verified(self, terminal, state, slug, scope, deployment, marker):
        step = terminal.expect_any(("Keep building in this project.", "Your deployment is saved. Let's check its response."))
        if step != "Keep building in this project.":
            require("The gateway answered this health check" in terminal.output, "Start could not verify the guest response")
            terminal.answer("Choose [1]: ", "2")
            terminal.answer("GET path [/healthz]: ", "/")
            terminal.expect("Keep building in this project.")
        require(marker in terminal.output, "CLI response did not contain the current guest source marker")
        document = self.session(state, slug, scope)
        require(document and document.get("deployment_id") == document.get("verified_deployment_id") == deployment and
                not document.get("pending_submission"), "Start did not durably verify its accepted deployment")

    def response(self, slug, scope, deployment, marker, path="/", http_status=200, fault=None, configured=False):
        resources = self.report["resources"]
        request = {"app_id": resources["apps"][slug], "scope": scope or "default", "project_id": resources["project_id"], "path": path}
        if scope:
            request["environment_id"] = resources["environment_id"]
        _, response = self.api.request("POST", "/v1/apps/" + slug + "/deployments/" + deployment + "/response-check", request)
        require(isinstance(response, dict) and all(response.get(key, "") == request.get(key, "") for key in
                ("app_id", "scope", "project_id", "environment_id")) and response.get("deployment_id") == deployment and
                identity(response.get("instance_id")) and response.get("http_status") == http_status and response.get("body_truncated") is False,
                "guest response lacks exact app, deployment, environment, or instance proof")
        payload = json.loads(response["body"])
        require(payload.get("marker") == marker and payload.get("authorization_seen") is False, "guest source or credential-forwarding check failed")
        require(fault is None or payload.get("response_failed") is fault, "fixture guest did not confirm the selected response fault state")
        require(not configured or (payload.get("configuration_ok") is True and payload.get("source_checked") is True),
                "fixture guest lacks exact configuration delivery or a completed build source guard")
        return {key: response.get(key, "") for key in
                ("app_id", "deployment_id", "scope", "project_id", "environment_id", "instance_id", "http_status")}

    def failed_build(self, launch, baseline, marker, result):
        previous = baseline["id"]
        history = {row["id"] for row in self.deployments(launch.slug)}
        self.saved_ids(launch, previous, previous)
        evidence = {"status": "running", "previous_verified_id": previous}
        result.setdefault("recovery", {})["failed_build"] = evidence
        self.checkpoint("review-failing-build")
        with launch.open() as terminal:
            terminal.answer("Choose [1]: ", "1")
            self.verified(terminal, launch.state, launch.slug, launch.scope, previous, marker)
            write_fixture(launch.source, marker + "-build-failed", fail_build=True)
            terminal.answer("Choose [1]: ", "2")
            terminal.answer("Deploy this app (y/N) [n]: ", "y")
            failed = self.accepted(terminal, launch.state, launch.slug, launch.scope, history)
            evidence["failed_id"] = failed["id"]
            self.checkpoint("wait-for-failed-build")
            terminal.expect("The deployment failed. Your source and deployment ID are saved.")
            evidence["saved_on_failure"] = self.saved_ids(launch, failed["id"], previous)
            row, evidence["build"] = self.build_failure(launch.slug, launch.scope, failed["id"])
            evidence["failed_source_sha256"] = row.get("source_sha256", "")
            require(re.fullmatch(r"[0-9a-f]{64}", evidence["failed_source_sha256"]) and
                    evidence["failed_source_sha256"] != baseline.get("source_sha256"), "failed build did not use the newly reviewed source")
            # Inspect both durable IDs before leaving the failed launch.
            self.deployment(launch.slug, launch.scope, previous)
            self.checkpoint("finish-failed-build")
            terminal.answer("Choose [1]: ", "3")
            terminal.wait(1)
        expected = history | {failed["id"]}
        require({row["id"] for row in self.deployments(launch.slug)} == expected, "failed launch created an unexpected deployment")
        with launch.open() as terminal:
            terminal.answer("Choose [1]: ", "1")
            terminal.expect("The deployment failed. Your source and deployment ID are saved.")
            evidence["saved_after_restart"] = self.saved_ids(launch, failed["id"], previous)
            require({row["id"] for row in self.deployments(launch.slug)} == expected, "reopening the failed build submitted another deployment")
            marker += "-build-fixed"
            write_fixture(launch.source, marker)
            self.checkpoint("review-fixed-build")
            terminal.answer("Choose [1]: ", "1")
            terminal.answer("Deploy this app (y/N) [n]: ", "y")
            repaired = self.accepted(terminal, launch.state, launch.slug, launch.scope, expected)
            evidence["repaired_id"] = repaired["id"]
            evidence["saved_before_repair_verified"] = self.saved_ids(launch, repaired["id"], previous)
            self.checkpoint("verify-fixed-build")
            self.verified(terminal, launch.state, launch.slug, launch.scope, repaired["id"], marker)
            evidence["verified"] = self.response(launch.slug, launch.scope, repaired["id"], marker)
            repaired = self.deployment(launch.slug, launch.scope, repaired["id"])
            digest = repaired.get("source_sha256", "")
            require(re.fullmatch(r"[0-9a-f]{64}", digest) and digest != evidence["failed_source_sha256"], "fixed build reused failed source")
            evidence["repaired_source_sha256"] = digest
            terminal.answer("Choose [1]: ", "1")
            terminal.wait(0)
        require({row["id"] for row in self.deployments(launch.slug)} == expected | {repaired["id"]}, "repair created duplicate deployments")
        require(self.deployment(launch.slug, launch.scope, failed["id"]).get("status") == "failed", "repair replaced the failed deployment's history")
        self.deployment(launch.slug, launch.scope, previous)
        evidence.update(status="passed", failure_exit=1, recovery_exit=0, previous_ids_inspectable=True, no_duplicate_deployments=True)
        self.save()
        return repaired, marker

    def failed_response(self, launch, baseline, marker, result):
        deployment = baseline["id"]
        history = {row["id"] for row in self.deployments(launch.slug)}
        self.saved_ids(launch, deployment, deployment)
        evidence = {"status": "running", "deployment_id": deployment}
        result.setdefault("recovery", {})["response_retry"] = evidence
        control = "/__start_acceptance/" + marker
        self.checkpoint("arm-guest-response-failure")
        armed = self.response(launch.slug, launch.scope, deployment, marker, path=control + "/arm", fault=True)
        evidence["armed_instance_id"] = armed["instance_id"]
        self.save()
        with launch.open() as terminal:
            terminal.answer("Choose [1]: ", "1")
            terminal.expect("HTTP 503")
            terminal.expect("Your deployment is saved. Let's check its response.")
            require(marker in terminal.output, "failed CLI response did not identify the fixture source")
            evidence["saved_on_failure"] = self.saved_ids(launch, deployment, deployment)
            failed = self.response(launch.slug, launch.scope, deployment, marker, http_status=503, fault=True)
            require(failed["instance_id"] == armed["instance_id"], "response failure moved to another guest")
            evidence["failed_response"] = failed
            self.checkpoint("finish-failed-response")
            terminal.answer("Choose [1]: ", "4")
            terminal.wait(1)
        self.saved_ids(launch, deployment, deployment)
        with launch.open() as terminal:
            terminal.answer("Choose [1]: ", "1")
            terminal.expect("HTTP 503")
            terminal.expect("Your deployment is saved. Let's check its response.")
            self.saved_ids(launch, deployment, deployment)
            failed = self.response(launch.slug, launch.scope, deployment, marker, http_status=503, fault=True)
            require(failed["instance_id"] == armed["instance_id"], "restarted check lost the armed guest")
            self.checkpoint("restore-guest-response-and-retry")
            cleared = self.response(launch.slug, launch.scope, deployment, marker, path=control + "/recover", fault=False)
            require(cleared["instance_id"] == armed["instance_id"], "response recovery moved to another guest")
            terminal.answer("Choose [1]: ", "1")
            self.verified(terminal, launch.state, launch.slug, launch.scope, deployment, marker)
            healthy = self.response(launch.slug, launch.scope, deployment, marker, fault=False)
            require(healthy["instance_id"] == armed["instance_id"], "retry verified another guest")
            evidence["recovered_response"] = healthy
            terminal.answer("Choose [1]: ", "1")
            terminal.wait(0)
        require({row["id"] for row in self.deployments(launch.slug)} == history, "response retry submitted another deployment")
        evidence.update(status="passed", failure_exit=1, recovery_exit=0, no_duplicate_deployments=True)
        self.save()

    def configuration_metadata(self, slug, forbidden):
        """Inspect only names/metadata in the two disposable fixture scopes."""
        scopes = ("default", self.target["named_environment"])
        result = {}
        for scope in scopes:
            query = "?" + urllib.parse.urlencode({"scope": scope})
            _, metadata = self.api.get("/v1/apps/" + slug + "/secrets" + query)
            require(isinstance(metadata, dict) and isinstance(metadata.get("secrets"), list), "invalid scoped secret metadata")
            require(all(row.get("scope") == scope and isinstance(row.get("key"), str) for row in metadata["secrets"]),
                    "secret metadata returned a different scope or incomplete key")
            _, environment = self.api.get("/v1/apps/" + slug + "/env" + query)
            require(isinstance(environment, dict) and isinstance(environment.get("env"), list), "invalid scoped environment metadata")
            raw = json.dumps([metadata, environment])
            require(all(value not in raw for value in forbidden), "configuration metadata exposed private fixture data")
            result[scope] = {"secrets": metadata["secrets"], "env": environment["env"]}
        return result

    def configuration_privacy(self, launch, forbidden, terminal=None):
        """Fail with fixed diagnostics; never record any offending bytes."""
        buffers = [json.dumps(self.report).encode(), self.path.read_bytes()]
        if terminal is not None:
            buffers.append(terminal.output.encode())
        for key in ("XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_CONFIG_HOME"):
            root = Path(launch.environment[key])
            paths = list(root.rglob("*.json"))
            require(len(paths) <= 100, "configuration privacy inventory exceeded its bound")
            for path in paths:
                require(path.is_file() and not path.is_symlink() and path.stat().st_size <= MAX_WIRE,
                        "configuration recovery data exceeded its private bounded inventory")
                buffers.append(path.read_bytes())
        require(all(value.encode() not in raw for value in forbidden for raw in buffers),
                "configuration value or file path reached CLI output, recovery data, or evidence")

    def missing_configuration(self, launch, baseline, marker, result):
        previous = baseline["id"]
        history = {row["id"] for row in self.deployments(launch.slug)}
        self.saved_ids(launch, previous, previous)
        secret = "start-fixture-only-" + uuid.uuid4().hex
        secret_path = launch.source / CONFIGURATION_FILE
        forbidden = (secret, str(secret_path))
        metadata = self.configuration_metadata(launch.slug, forbidden)
        require(all(not row["secrets"] and not row["env"] for row in metadata.values()),
                "configuration fixture scopes must begin empty")
        selected = launch.scope or "default"
        other = "default" if launch.scope else self.target["named_environment"]
        evidence = {"status": "running", "key": CONFIGURATION_KEY, "scope": selected, "other_scope": other,
                    "previous_verified_id": previous}
        result.setdefault("recovery", {})["missing_configuration"] = evidence
        self.checkpoint("review-missing-configuration")

        def unchanged():
            require({row["id"] for row in self.deployments(launch.slug)} == history,
                    "configuration blocker or unconfirmed review submitted a deployment")
            require(self.configuration_metadata(launch.slug, forbidden) == metadata,
                    "configuration changed before deployment confirmation")
            self.saved_ids(launch, previous, previous)

        try:
            with launch.open() as terminal:
                terminal.answer("Choose [1]: ", "1")
                self.verified(terminal, launch.state, launch.slug, launch.scope, previous, marker)
                marker += "-configured"
                write_fixture(launch.source, marker, configuration_digest=hashlib.sha256(secret.encode()).hexdigest())
                terminal.answer("Choose [1]: ", "2")
                terminal.expect("Required runtime keys need configuration: " + CONFIGURATION_KEY)
                terminal.expect("Let's resolve the source blockers before deploying.")
                unchanged()
                evidence["blocked_before_submission"] = True
                self.checkpoint("supply-required-configuration")
                # Deliberately inside source and without a default-excluded
                # extension: only the normal selected-file exclusion protects it.
                with open(secret_path, "x", opener=lambda path, flags: os.open(path, flags, 0o600)) as handle:
                    handle.write(CONFIGURATION_KEY + "=" + secret + "\n")
                terminal.answer("Choose [4]: ", "4")
                terminal.answer("Secrets file path: ", str(secret_path))
                terminal.expect("Secrets  Seal these keys before deployment: " + CONFIGURATION_KEY)
                unchanged()
                evidence["reviewed_before_mutation"] = True
                self.checkpoint("confirm-configured-deployment")
                terminal.answer("Deploy this app (y/N) [n]: ", "y")
                configured = self.accepted(terminal, launch.state, launch.slug, launch.scope, history)
                evidence["configured_id"] = configured["id"]
                self.checkpoint("verify-configured-deployment")
                self.verified(terminal, launch.state, launch.slug, launch.scope, configured["id"], marker)
                evidence["verified"] = self.response(launch.slug, launch.scope, configured["id"], marker, configured=True)
                evidence["anonymous_http_status"] = self.public_access(launch.slug, configured["id"], result["case"].startswith("private"), marker)
                configured = self.deployment(launch.slug, launch.scope, configured["id"])
                digest = configured.get("source_sha256", "")
                require(re.fullmatch(r"[0-9a-f]{64}", digest) and digest != baseline.get("source_sha256"),
                        "configured deployment did not use the newly reviewed fixture source")
                evidence["source_sha256"] = digest
                terminal.answer("Choose [1]: ", "1")
                terminal.wait(0)
                self.configuration_privacy(launch, forbidden, terminal)
        finally:
            secret_path.unlink(missing_ok=True)
        after = self.configuration_metadata(launch.slug, forbidden)
        require(after[other] == metadata[other] and after[selected]["env"] == metadata[selected]["env"] and
                len(after[selected]["secrets"]) == 1 and after[selected]["secrets"][0]["key"] == CONFIGURATION_KEY,
                "configured keys did not remain isolated to the selected secret scope")
        expected = history | {configured["id"]}
        require({row["id"] for row in self.deployments(launch.slug)} == expected,
                "configuration resolution created duplicate deployments")
        self.checkpoint("resume-without-secret-file")
        with launch.open() as terminal:
            terminal.answer("Choose [1]: ", "1")
            self.verified(terminal, launch.state, launch.slug, launch.scope, configured["id"], marker)
            evidence["resumed"] = self.response(launch.slug, launch.scope, configured["id"], marker, configured=True)
            terminal.answer("Choose [1]: ", "1")
            terminal.wait(0)
            self.configuration_privacy(launch, forbidden, terminal)
        require({row["id"] for row in self.deployments(launch.slug)} == expected,
                "configuration resume submitted another deployment")
        resumed_metadata = self.configuration_metadata(launch.slug, forbidden)
        require(resumed_metadata[other] == metadata[other] and resumed_metadata[selected]["env"] == metadata[selected]["env"] and
                len(resumed_metadata[selected]["secrets"]) == 1 and
                all(resumed_metadata[selected]["secrets"][0].get(key) == after[selected]["secrets"][0].get(key)
                    for key in ("key", "scope", "updated_at", "delivery_version")),
                "configuration resume rewrote a secret or changed the other scope")
        evidence.update(status="passed", guest_configuration_verified=True, build_source_guard_verified=True,
                        other_scope_unchanged=True, private_data_excluded=True, resumed_without_secret_file=True,
                        no_duplicate_deployments=True, exit=0)
        self.save()
        self.configuration_privacy(launch, forbidden)

    def compatibility_fresh(self, launch, relay, mode):
        before = relay.command(mode, True)
        with launch.open() as terminal:
            terminal.answer("Choose [1]: ", "1")
            terminal.expect("temporarily unavailable" if mode == "outage" else "incompatible deployment verification support")
            terminal.wait(1)
            require("App ready" not in terminal.output and "The deployment failed" not in terminal.output,
                    "fresh compatibility failure was reported as an app result")
        for path in launch.state.rglob("*.json"):
            require(path.stat().st_size <= 65536, "blocked launch metadata exceeded its bound")
            document = json.loads(path.read_bytes())
            require(not document.get("deployment_id") and not document.get("pending_submission"),
                    "blocked fresh launch persisted a source submission")
        return {"exit": 1, "audit": relay_delta(before, relay.command(), "capability_faults")}

    def compatibility_boundary(self, launch, relay, mode, baseline, marker):
        relay.command("healthy", True)
        secret, path = "start-fixture-only-" + uuid.uuid4().hex, launch.source / CONFIGURATION_FILE
        forbidden = (secret, str(path))
        metadata = configuration_versions(self.configuration_metadata(launch.slug, forbidden))
        try:
            with launch.open() as terminal:
                terminal.answer("Choose [1]: ", "1")
                self.verified(terminal, launch.state, launch.slug, launch.scope, baseline, marker)
                write_fixture(launch.source, marker + "-blocked")
                with (launch.source / "handler.js").open("a") as handle:
                    handle.write("void process.env." + COMPATIBILITY_KEY + ";\n")
                terminal.answer("Choose [1]: ", "2")
                terminal.expect("Required runtime keys need configuration: " + COMPATIBILITY_KEY)
                with open(path, "x", opener=lambda name, flags: os.open(name, flags, 0o600)) as handle:
                    handle.write(COMPATIBILITY_KEY + "=" + secret + "\n")
                terminal.answer("Choose [4]: ", "4")
                terminal.answer("Secrets file path: ", str(path))
                terminal.expect("Secrets  Seal these keys before deployment: " + COMPATIBILITY_KEY)
                before = relay.command(mode, True)
                terminal.answer("Deploy this app (y/N) [n]: ", "y")
                terminal.expect("temporarily unavailable" if mode == "outage" else "incompatible deployment verification support")
                terminal.wait(1)
                self.configuration_privacy(launch, forbidden, terminal)
        finally:
            path.unlink(missing_ok=True)
        audit = relay_delta(before, relay.command(), "capability_faults")
        require(configuration_versions(self.configuration_metadata(launch.slug, forbidden)) == metadata,
                "compatibility failure changed configuration after review")
        saved = self.saved_ids(launch, baseline, baseline)
        return {"exit": 1, "audit": audit, "saved": saved, "configuration_unchanged": True, "private_data_excluded": True}

    def compatibility_resume(self, launch, relay, mode, baseline, marker, private):
        retryable = mode in ("outage", "response-outage")
        diagnostic = "temporarily unavailable" if retryable else "incompatible deployment verification support"
        before = relay.command(mode, True)
        direct = False
        with launch.open() as terminal:
            terminal.answer("Choose [1]: ", "1")
            cursor = terminal.cursor
            outcome = terminal.expect_any((diagnostic, "Keep building in this project."))
            if outcome == "Keep building in this project.":
                # A public guest can still provide exact deployment evidence
                # without the owner verification service. Preserve that success.
                require(not private, "private verification ignored its injected service failure")
                direct = True
                terminal.cursor = cursor
                self.verified(terminal, launch.state, launch.slug, launch.scope, baseline, marker)
                terminal.answer("Choose [1]: ", "1")
                terminal.wait(0)
            else:
                if retryable:
                    terminal.expect("Retry this GET check")
                    terminal.answer("Choose [1]: ", "2")
                terminal.wait(1)
                require("Show recent app logs" not in terminal.output and "Check another GET path" not in terminal.output and
                        "The deployment failed" not in terminal.output, "verification service failure was presented as an app error")
        fault = None if direct else ("response_faults" if mode == "response-outage" else "capability_faults")
        audit = relay_delta(before, relay.command(), fault)
        saved = self.saved_ids(launch, baseline, baseline)
        if retryable and not direct:
            # Reopen while still unavailable, then recover through the offered
            # retry in that same process. The previous failure already exited.
            with launch.open() as terminal:
                terminal.answer("Choose [1]: ", "1")
                terminal.expect(diagnostic)
                terminal.expect("Retry this GET check")
                relay.command("healthy", True)
                terminal.answer("Choose [1]: ", "1")
                self.verified(terminal, launch.state, launch.slug, launch.scope, baseline, marker)
                terminal.answer("Choose [1]: ", "1")
                terminal.wait(0)
        else:
            relay.command("healthy", True)
            with launch.open() as terminal:
                terminal.answer("Choose [1]: ", "1")
                self.verified(terminal, launch.state, launch.slug, launch.scope, baseline, marker)
                terminal.answer("Choose [1]: ", "1")
                terminal.wait(0)
        total = relay_delta(before, relay.command(), fault)
        require(mode != "response-outage" or audit["capabilities"] > 0, "late outage did not follow a capability preflight")
        return {"failure_exit": 0 if direct else 1, "recovery_exit": 0, "audit": audit, "including_recovery": total,
                "public_verification_available": direct, "saved_on_failure": saved,
                "saved_after_recovery": self.saved_ids(launch, baseline, baseline),
                "verified": self.response(launch.slug, launch.scope, baseline, marker)}

    def compatibility(self, original, executable, marker, result):
        original_session = {path.name: path.read_bytes() for path in (original.state / "gregale/start").glob("*.json")}
        require(original_session, "compatibility coverage requires a saved original launch")
        root = original.state.parent / (result["case"] + "-compatibility")
        root.mkdir(mode=0o700)
        source = root / "source"
        marker += "-compatibility"
        write_fixture(source, marker)
        link = source / ".gregale/project.json"
        link.parent.mkdir()
        link.write_text(json.dumps({"version": 1, "project": self.target["project_slug"], "app": original.slug, "environment": original.scope}))
        evidence = {"status": "running", "fault_layer": "loopback-api-replies", "modes": {}}
        result.setdefault("recovery", {})["compatibility"] = evidence
        self.checkpoint("compatibility-relay-start")
        with Relay(executable, self.api, original.environment) as relay:
            env = {**original.environment, "FAAS_API": relay.origin, "XDG_STATE_HOME": str(root / "state"),
                   "XDG_CONFIG_HOME": str(root / "config"), "XDG_CACHE_HOME": str(root / "cache")}
            launch = Launch(original.cli, source, root / "state", env, original.slug, original.scope)
            # The new baseline is created through this stable fixture origin.
            # Never rewrite a session to move it between API identities.
            run = Run(self.api, {**self.target, "api_origin": relay.origin}, self.path, self.report)
            history = {row["id"] for row in self.deployments(original.slug)}
            relay.command("healthy", False)
            with launch.open() as terminal:
                terminal.answer("Choose [1]: ", "1")
                terminal.answer("Deploy this app (y/N) [n]: ", "y")
                accepted = run.accepted(terminal, launch.state, launch.slug, launch.scope, history)
                baseline = accepted["id"]
                evidence["baseline_id"] = baseline
                self.checkpoint("verify-compatibility-baseline")
                run.verified(terminal, launch.state, launch.slug, launch.scope, baseline, marker)
                evidence["baseline"] = run.response(launch.slug, launch.scope, baseline, marker)
                evidence["source_sha256"] = run.deployment(launch.slug, launch.scope, baseline).get("source_sha256", "")
                require(re.fullmatch(r"[0-9a-f]{64}", evidence["source_sha256"]), "compatibility baseline lacks its source digest")
                terminal.answer("Choose [1]: ", "1")
                terminal.wait(0)
            initial = relay.command("healthy", True)
            require(initial["writes"] > 0 and initial["source_writes"] > 0 and initial["app_creates"] == 0,
                    "compatibility baseline did not exercise real source submission to its owned app")
            expected = history | {baseline}
            fresh_source = root / (self.target["project_slug"] + "-blocked")
            write_fixture(fresh_source, marker + "-fresh")
            fresh_env = {**env, "XDG_STATE_HOME": str(root / "fresh-state"), "XDG_CONFIG_HOME": str(root / "fresh-config"),
                         "XDG_CACHE_HOME": str(root / "fresh-cache")}
            fresh = Launch(original.cli, fresh_source, root / "fresh-state", fresh_env, fresh_source.name, "")
            require(self.api.get("/v1/apps/" + fresh.slug, expected=(200, 404))[0] == 404, "blocked-launch fixture name already exists")
            private = result["case"].startswith("private")
            modes = (*COMPATIBILITY_MODES, "response-outage") if private else COMPATIBILITY_MODES
            for mode in modes:
                item = {"status": "running"}
                evidence["modes"][mode] = item
                self.checkpoint("compatibility-" + mode)
                if mode != "response-outage":
                    item["fresh"] = run.compatibility_fresh(fresh, relay, mode)
                    item["before_mutation"] = run.compatibility_boundary(launch, relay, mode, baseline, marker)
                item["resume"] = run.compatibility_resume(launch, relay, mode, baseline, marker, private)
                require({row["id"] for row in self.deployments(original.slug)} == expected and
                        self.api.get("/v1/apps/" + fresh.slug, expected=(200, 404))[0] == 404,
                        "compatibility failure or recovery changed remote deployment or app inventory")
                item["status"] = "passed"
                self.save()
            evidence["anonymous_http_status"] = self.public_access(launch.slug, baseline, private, marker)
            require({path.name: path.read_bytes() for path in (original.state / "gregale/start").glob("*.json")} == original_session,
                    "compatibility fixture rewrote the original API-bound recovery record")
            evidence.update(status="passed", original_session_unchanged=True, session_origin_unchanged=True,
                            no_duplicate_deployments=True, configuration_unchanged=True)
            self.save()

    def pending_submission(self, launch, receipt, previous):
        document = self.session(launch.state, launch.slug, launch.scope)
        pending = document.get("pending_submission") if document else None
        require(document and document.get("version") == 2 and isinstance(pending, dict) and
                pending.get("transport") == "resumable" and identity(pending.get("key")) and
                pending.get("upload_id") == receipt["upload_id"] and type(pending.get("total_size")) is int and pending["total_size"] > 0 and
                document.get("deployment_id", "") == document.get("verified_deployment_id", "") == previous,
                "lost acceptance did not preserve its durable upload and previous deployment IDs")
        _, upload = self.api.get("/v1/uploads/" + receipt["upload_id"])
        require(isinstance(upload, dict) and upload.get("upload_id") == pending["upload_id"] and upload.get("app_slug") == launch.slug and
                upload.get("status") in ("committed", "complete", "completed") and upload.get("deployment_id") == receipt["deployment_id"] and
                upload.get("received_bytes") == upload.get("total_size") == pending["total_size"],
                "pending upload does not identify the exact server-committed deployment")
        return {"upload_id": pending["upload_id"], "total_size": pending["total_size"],
                "accepted_id": previous, "verified_id": previous}

    def committed_reply(self, launch, terminal, relay, before, history):
        deadline = time.monotonic() + 60
        while time.monotonic() < deadline:
            counts = relay.command()
            if relay.commit is not None:
                receipt = relay.commit
                require(counts["commit_faults"] > before["commit_faults"] and receipt["deployment_id"] not in history and
                        receipt["app_id"] == self.report["resources"]["apps"][launch.slug],
                        "commit fault did not identify a new deployment for the selected app")
                return receipt, self.deployment(launch.slug, launch.scope, receipt["deployment_id"])
            require(terminal.process.poll() is None, "CLI exited before the relay captured a committed deployment")
            terminal.pump()
        raise Failure("API did not commit the reviewed source before the reply fault deadline")

    def submission_recovery(self, original, executable, marker, result, modes=None):
        modes = SUBMISSION_MODES if modes is None else modes
        require(modes and all(SUBMISSION_MODES.get(mode) == fault for mode, fault in modes.items()), "invalid submission fault selection")
        original_session = {path.name: path.read_bytes() for path in (original.state / "gregale/start").glob("*.json")}
        root = original.state.parent / (result["case"] + "-submission")
        root.mkdir(mode=0o700)
        evidence = {"status": "running", "fault_layer": "loopback-commit-reply", "modes": {}}
        result.setdefault("recovery", {})["lost_commit_reply"] = evidence
        self.checkpoint("submission-relay-start")
        with Relay(executable, self.api, original.environment) as relay:
            run = Run(self.api, {**self.target, "api_origin": relay.origin}, self.path, self.report)
            for mode, fault in modes.items():
                directory = root / mode
                directory.mkdir(mode=0o700)
                source = directory / "source"
                write_fixture(source, marker + "-submission-" + mode + "-first")
                link = source / ".gregale/project.json"
                link.parent.mkdir()
                link.write_text(json.dumps({"version": 1, "project": self.target["project_slug"], "app": original.slug, "environment": original.scope}))
                env = {**original.environment, "FAAS_API": relay.origin, "XDG_STATE_HOME": str(directory / "state"),
                       "XDG_CONFIG_HOME": str(directory / "config"), "XDG_CACHE_HOME": str(directory / "cache")}
                launch = Launch(original.cli, source, directory / "state", env, original.slug, original.scope)
                previous, previous_marker, previous_digest = "", "", ""
                for stage in ("first", "update"):
                    item = {"status": "running", "launch": stage, "fault": mode}
                    evidence["modes"][mode + "-" + stage] = item
                    self.checkpoint("review-submission-" + mode + "-" + stage)
                    history = {row["id"] for row in self.deployments(original.slug)}
                    configuration = configuration_versions(self.configuration_metadata(original.slug, []))
                    current_marker = marker + "-submission-" + mode + "-" + stage
                    # Restore the fixture files deleted for the previous recovery.
                    write_fixture(source, previous_marker or current_marker)
                    relay.command("healthy", False)
                    with launch.open() as terminal:
                        terminal.answer("Choose [1]: ", "1")
                        if previous:
                            run.verified(terminal, launch.state, launch.slug, launch.scope, previous, previous_marker)
                            write_fixture(source, current_marker)
                            terminal.answer("Choose [1]: ", "2")
                        terminal.expect("Deploy this app (y/N) [n]: ")
                        before = relay.command(fault, False)
                        os.write(terminal.master, b"y\n")
                        receipt, deployment = run.committed_reply(launch, terminal, relay, before, history)
                        item["committed_id"] = receipt["deployment_id"]
                        digest = deployment.get("source_sha256", "")
                        require(re.fullmatch(r"[0-9a-f]{64}", digest) and digest != previous_digest,
                                "interrupted acceptance lacks the newly reviewed source digest")
                        # Inspect durable pending identity while the held CLI is
                        # still alive, then again after SIGKILL or a lost reply.
                        run.pending_submission(launch, receipt, previous)
                        if mode == "process-killed":
                            require(terminal.process.poll() is None, "CLI did not remain blocked on its held commit reply")
                            os.killpg(terminal.process.pid, signal.SIGKILL)
                            terminal.wait(-signal.SIGKILL)
                        else:
                            terminal.wait(3, timeout=60)
                        require(receipt["deployment_id"] not in terminal.output, "CLI received the acceptance that should have been withheld")
                        item["exit_status"] = terminal.process.returncode
                        item["pending"] = run.pending_submission(launch, receipt, previous)
                        counts = relay.command("healthy", True)
                        require(counts["commit_faults"] > before["commit_faults"] and
                                all(counts[key] == before[key] for key in ("app_creates", "secret_writes", "other_writes", "blocked_writes")) and
                                counts["source_writes"] > before["source_writes"],
                                "reviewed submission changed unexpected resources or missed its reply fault")
                        item["withheld_replies"] = counts["commit_faults"] - before["commit_faults"]
                        run.configuration_privacy(launch, [self.api.token], terminal)
                    remove_submission_inputs(launch)
                    recovery_before = relay.command()
                    self.checkpoint("recover-submission-without-source")
                    with launch.open() as terminal:
                        terminal.expect("A source submission needs recovery before another launch.")
                        terminal.answer("Choose [1]: ", "1")
                        recovered = run.accepted(terminal, launch.state, launch.slug, launch.scope, history)
                        require(recovered["id"] == receipt["deployment_id"], "recovery selected a different deployment from the withheld commit")
                        self.checkpoint("verify-submission-recovered")
                        run.verified(terminal, launch.state, launch.slug, launch.scope, recovered["id"], current_marker)
                        item["verified"] = run.response(launch.slug, launch.scope, recovered["id"], current_marker)
                        terminal.answer("Choose [1]: ", "1")
                        terminal.wait(0)
                        run.configuration_privacy(launch, [self.api.token], terminal)
                    item["recovery_requests"] = relay_delta(recovery_before, relay.command(), "upload_reads")
                    item["saved_after_recovery"] = run.saved_ids(launch, recovered["id"], recovered["id"])
                    require({row["id"] for row in self.deployments(original.slug)} == history | {recovered["id"]},
                            "lost acceptance recovery created a duplicate deployment")
                    require(configuration_versions(self.configuration_metadata(original.slug, [])) == configuration,
                            "lost acceptance recovery changed configuration versions")
                    require({path.name for path in source.iterdir()} == {".gregale"} and
                            not (launch.state / "gregale/uploads").exists() and not Path(env["XDG_CACHE_HOME"]).exists(),
                            "recovery recreated local source or upload cache")
                    item.update(status="passed", source_sha256=digest, source_and_cache_removed=True,
                                no_duplicate_deployments=True, configuration_unchanged=True, session_origin_unchanged=True)
                    previous, previous_marker, previous_digest = recovered["id"], current_marker, digest
                    self.save()
            require({path.name: path.read_bytes() for path in (original.state / "gregale/start").glob("*.json")} == original_session,
                    "submission fixture rewrote the original API-bound recovery record")
            evidence.update(status="passed", original_session_unchanged=True, no_duplicate_deployments=True)
            self.save()

    def public_access(self, slug, deployment, private, marker):
        _, preview = self.api.get("/v1/deployments/" + deployment + "/url")
        require(preview.get("deployment_id") == deployment and preview.get("app_id") == self.report["resources"]["apps"][slug] and
                preview.get("alive") is True, "preview URL has a different app/deployment identity")
        parsed = urllib.parse.urlsplit(preview.get("url", ""))
        require(parsed.scheme == "https" and parsed.hostname and not parsed.username and not parsed.password and
                parsed.path in ("", "/") and not parsed.query and not parsed.fragment, "preview URL is not a public HTTPS origin")
        request = urllib.request.Request(preview["url"].rstrip("/") + "/")  # Deliberately no account credentials.
        try:
            with urllib.request.build_opener(NoRedirect()).open(request, timeout=30) as response:
                require(not private and response.status == 200, "private ingress permitted an anonymous guest response")
                wire = response.read(65537)
                require(len(wire) <= 65536, "public guest response exceeded the bounded read limit")
                payload = json.loads(wire)
                require(payload.get("marker") == marker and payload.get("authorization_seen") is False, "public response did not reach the current fixture guest")
                return response.status
        except urllib.error.HTTPError as error:
            status = error.code
            error.close()
            require(private and status in (401, 403), "public/private ingress did not enforce the expected access policy")
            return status

    def instances(self, slug):
        _, rows = self.api.get("/v1/apps/" + slug + "/instances?history=true")
        require(isinstance(rows, list), "invalid scheduler instance inventory")
        require(all(row.get("app_id") == self.report["resources"]["apps"][slug] for row in rows), "instance inventory contains a different app")
        return rows

    def parked(self, slug, response):
        deadline = time.monotonic() + 600
        while time.monotonic() < deadline:
            rows = self.instances(slug)
            selected = [row for row in rows if row.get("deployment_id") == response["deployment_id"]]
            parked = [row for row in selected if row.get("id") == response["instance_id"] and row.get("state") == "PARKED" and
                      row.get("resident") is False and identity(row.get("wake_id")) and row.get("parked_at")]
            if parked and not any(row.get("resident") or row.get("state") in ACTIVE for row in selected):
                return {key: parked[0][key] for key in ("id", "deployment_id", "wake_id", "state", "resident", "parked_at")}
            time.sleep(1)
        raise Failure("selected deployment did not park without resident memory")

    def wake(self, slug, response, parked):
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline:
            rows = self.instances(slug)
            selected = next((row for row in rows if row.get("id") == response["instance_id"] and row.get("deployment_id") == response["deployment_id"]), {})
            require(identity(selected.get("wake_id")) and selected.get("wake_id") != parked["wake_id"], "response reused stale pre-park wake evidence")
            _, timeline = self.api.get("/v1/apps/" + slug + "/wakes/" + selected["wake_id"] + "/timeline?limit=1000")
            require(timeline.get("app_id") == response["app_id"] and timeline.get("wake_id") == selected["wake_id"] and
                    not timeline.get("next_cursor"), "wake timeline has a different identity or incomplete pagination")
            events = timeline.get("events", [])
            relevant = [event for event in events if all(event.get("data", {}).get(key) == value for key, value in
                        (("app_id", response["app_id"]), ("instance_id", response["instance_id"]), ("wake_id", selected["wake_id"])))]
            required = {("wake.boot_started", "schedd"), ("wake.boot_observed", "vmmd"), ("wake.readiness_200", "vmmd"), ("wake.boot_completed", "schedd")}
            if required <= {(event.get("kind"), event.get("actor")) for event in relevant}:
                frames = [{"kind": event["kind"], "actor": event["actor"], "at": event.get("at"), "method": event.get("data", {}).get("method", "")} for event in relevant if (event.get("kind"), event.get("actor")) in required]
                require(all(frame["method"] in ("", "cold_boot", "restore") and isinstance(frame["at"], str) and
                            re.fullmatch(r"[0-9TZ:.+\-]{20,40}", frame["at"]) for frame in frames) and
                        any(frame["kind"] == "wake.boot_completed" and frame["method"] in ("cold_boot", "restore") for frame in frames),
                        "wake timeline omitted its native boot method or timestamp")
                return {"wake_id": selected["wake_id"], "instance_id": response["instance_id"], "events": frames}
            time.sleep(0.5)
        raise Failure("native wake lacks scheduler, VM RPC, and guest readiness evidence")

    def scenario(self, case, root, cli, relay, environment):
        slug = self.target["project_slug"] + "-" + case
        scope = self.target["named_environment"] if case.endswith("named") else ""
        source, state = root / "apps" / slug, root.parent / (case + "-state")
        link = source / ".gregale/project.json"
        link.parent.mkdir()
        link.write_text(json.dumps({"version": 1, "project": self.target["project_slug"], "app": slug, "environment": scope}))
        env = {**environment, "XDG_CONFIG_HOME": str(root.parent / (case + "-config")), "XDG_CACHE_HOME": str(root.parent / (case + "-cache")),
               "XDG_STATE_HOME": str(state), "TERM": "dumb", "NO_COLOR": "1"}
        result = self.report["scenarios"][-1]
        result.update(app_id=self.report["resources"]["apps"][slug], scope=scope or "default", accepted_ids=[], verified=[], source_sha256=[])
        self.submission_recovery(Launch(cli, source, state, env, slug, scope), relay,
                                 self.report["run_id"] + "-" + case, result)
        self.checkpoint("launch-review")
        before = {row["id"] for row in self.deployments(slug)}
        marker = self.report["run_id"] + "-" + case + "-v1"
        with Terminal(cli, source, env) as terminal:
            terminal.answer("Choose [1]: ", "1")
            terminal.answer("Deploy this app (y/N) [n]: ", "y")
            first = self.accepted(terminal, state, slug, scope, before)
            result["accepted_ids"].append(first["id"])
            result["source_sha256"].append(first.get("source_sha256", ""))
            self.checkpoint("interrupt-after-acceptance")
            terminal.process.send_signal(signal.SIGINT)
            terminal.wait(130)
            result["interrupted_exit"] = 130
        accepted = before | {first["id"]}
        with Terminal(cli, source, env) as terminal:
            terminal.answer("Choose [1]: ", "1")
            self.verified(terminal, state, slug, scope, first["id"], marker)
            require({row["id"] for row in self.deployments(slug)} == accepted, "resuming accepted deployment created a duplicate")
            first_response = self.response(slug, scope, first["id"], marker)
            first_access = self.public_access(slug, first["id"], case.startswith("private"), marker)
            result["verified"].append(first_response)
            result["anonymous_http_status"] = [first_access]
            self.checkpoint("edit-and-review")
            marker = self.report["run_id"] + "-" + case + "-v2"
            write_fixture(source, marker)
            terminal.answer("Choose [1]: ", "2")
            terminal.answer("Deploy this app (y/N) [n]: ", "y")
            second = self.accepted(terminal, state, slug, scope, accepted)
            result["accepted_ids"].append(second["id"])
            result["source_sha256"].append(second.get("source_sha256", ""))
            self.checkpoint("verify-edited-deployment")
            require(all(re.fullmatch(r"[0-9a-f]{64}", digest) for digest in result["source_sha256"]) and
                    second["source_sha256"] != first["source_sha256"], "edit lacks a new immutable source archive digest")
            self.verified(terminal, state, slug, scope, second["id"], marker)
            second_response = self.response(slug, scope, second["id"], marker)
            second_access = self.public_access(slug, second["id"], case.startswith("private"), marker)
            result["verified"].append(second_response)
            result["anonymous_http_status"].append(second_access)
            terminal.answer("Choose [1]: ", "1")
            terminal.wait(0)
        accepted.add(second["id"])
        self.checkpoint("wait-for-park")
        parked = self.parked(slug, second_response)
        result["parked"] = parked
        self.checkpoint("resume-and-wake")
        with Terminal(cli, source, env) as terminal:
            terminal.answer("Choose [1]: ", "1")
            self.verified(terminal, state, slug, scope, second["id"], marker)
            terminal.answer("Choose [1]: ", "1")
            terminal.wait(0)
        response = self.response(slug, scope, second["id"], marker)
        wake = self.wake(slug, response, parked)
        require({row["id"] for row in self.deployments(slug)} == accepted, "park/wake recovery changed deployment history")
        result["verified"].append(response)
        result["wake"] = wake
        launch = Launch(cli, source, state, env, slug, scope)
        recovered, marker = self.failed_build(launch, second, marker, result)
        self.failed_response(launch, recovered, marker, result)
        self.missing_configuration(launch, recovered, marker, result)
        self.compatibility(launch, relay, marker, result)
        result.update(status="passed", no_duplicate_deployments=True)
        return result

    def cleanup(self):
        """Never select deletions by a slug prefix or by deployment recency."""
        resources, project = self.report["resources"], self.target["project_slug"]
        self.api.deadline = time.monotonic() + 180
        failures = []
        for slug, app_id in resources["apps"].items():
            try:
                require(slug in {project + "-" + case for case in CASES} and identity(app_id), "invalid pinned cleanup identity")
                status, _ = self.app(slug, app_id)
                if status == 200:
                    self.api.request("DELETE", "/v1/apps/" + slug, expected=(200, 202, 204))
                require(self.api.get("/v1/apps/" + slug, expected=(200, 404))[0] == 404, "app deletion did not converge")
            except Failure:
                failures.append("owned app deletion needs inspection")
        if resources.get("project_id"):
            try:
                status, project_row = self.api.get("/v1/projects/" + project, expected=(200, 404))
                if status == 200:
                    require(not failures and project_row.get("id") == resources["project_id"] and
                            not project_row.get("workloads"), "project identity or membership changed; refusing project deletion")
                    self.api.request("DELETE", "/v1/projects/" + project, expected=(200, 204))
                require(self.api.get("/v1/projects/" + project, expected=(200, 404))[0] == 404, "project deletion did not converge")
            except Failure:
                failures.append("owned project deletion needs inspection")
        if resources.get("provisioning") == "outcome_unknown":
            failures.append("project apply outcome is unknown; unpinned resources require inspection")
        owned = set(resources["apps"].values())
        deadline = time.monotonic() + 120
        while owned and time.monotonic() < deadline:
            try:
                live, cursor = [], ""
                for _ in range(100):
                    _, page = self.api.get("/v1/instances?" + urllib.parse.urlencode({"limit": 100, "before": cursor}))
                    require(isinstance(page, dict) and isinstance(page.get("instances"), list), "invalid cleanup instance inventory")
                    live.extend(row for row in page["instances"] if row.get("app_id") in owned and (row.get("resident") or row.get("state") in ACTIVE))
                    next_cursor = page.get("next_before", "")
                    if not next_cursor:
                        break
                    require(next_cursor != cursor, "cleanup instance pagination did not advance")
                    cursor = next_cursor
                else:
                    raise Failure("cleanup instance inventory exceeded the page limit")
                if not live:
                    break
            except Failure:
                failures.append("could not verify native instance cleanup")
                break
            time.sleep(1)
        else:
            if owned:
                failures.append("owned instances still hold runtime resources")
        self.report["cleanup"] = {"status": "failed" if failures else "passed", "diagnostics": failures}
        self.save()
        require(not failures, "cleanup is incomplete; use the saved ownership receipt to retry")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check-runner", action="store_true", help="read-only native host, account, and capability checks")
    parser.add_argument("--cleanup", action="store_true", help="retry cleanup of IDs pinned in an existing evidence directory")
    parser.add_argument("--repo", type=Path)
    parser.add_argument("--out", type=Path)
    parser.add_argument("--cli", type=Path)
    parser.add_argument("--relay", type=Path)
    args = parser.parse_args()
    api, target = configuration(cleanup=args.cleanup)
    if args.check_runner:
        print("Native Start prerequisites verified; no resources changed.")
        return
    require(args.out and args.out.is_dir() and stat.S_IMODE(args.out.stat().st_mode) & 0o077 == 0, "select a private evidence directory")
    path = args.out / "acceptance.json"
    if args.cleanup:
        require(path.stat().st_size <= MAX_WIRE, "ownership receipt is too large")
        report = json.loads(path.read_bytes())
        require(report.get("version") == 1 and report.get("coverage") == "start-core-live-lifecycle", "unsupported ownership receipt")
        require(all(report["target"].get(key) == target[key] for key in ("api_origin", "account_id", "project_slug", "named_environment")),
                "cleanup target differs from the saved ownership receipt")
        Run(api, target, path, report).cleanup()
        return
    require(args.repo and args.cli and args.cli.is_file() and args.relay and args.relay.is_file() and not path.exists(),
            "select a checkout, CLI, acceptance relay, and fresh evidence directory")
    report = {"version": 1, "run_id": uuid.uuid4().hex, "status": "running", "coverage": "start-core-live-lifecycle",
              "started_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
              "target": target, "source": source_identity(args.repo, args.cli), "scenarios": [],
              "remaining_live_matrix": [],
              "resources": {"operation_id": str(uuid.uuid4()), "provisioning": "not_started", "apps": {}}, "cleanup": {"status": "pending"}}
    report["source"]["relay_sha256"] = hashlib.sha256(args.relay.read_bytes()).hexdigest()
    run = Run(api, target, path, report)
    run.save()
    try:
        with tempfile.TemporaryDirectory(prefix="gregale-start-native-") as temporary:
            root = Path(temporary) / "source"
            prepare_sources(root, target["project_slug"], report["run_id"])
            run.checkpoint("fixture-provisioning")
            run.provision(root)
            for case in CASES:
                print("Native Start: " + case, flush=True)
                report["scenarios"].append({"case": case, "status": "running"})
                run.save()
                report["scenarios"][-1] = run.scenario(case, root, args.cli.resolve(), args.relay.resolve(), os.environ.copy())
                run.save()
        report["status"] = "passed"
    except BaseException as error:
        report["status"] = "failed"
        report["failure"] = str(error) if isinstance(error, Failure) else "runner interrupted or failed; raw diagnostics omitted"
        if report["scenarios"] and report["scenarios"][-1]["status"] == "running":
            report["scenarios"][-1]["status"] = "failed"
            for recovery in report["scenarios"][-1].get("recovery", {}).values():
                if recovery.get("status") == "running":
                    recovery["status"] = "failed"
                for mode in recovery.get("modes", {}).values():
                    if mode.get("status") == "running":
                        mode["status"] = "failed"
        raise
    finally:
        # Cleanup has its own bounded wait, including after Ctrl-C/termination.
        signal.signal(signal.SIGINT, signal.SIG_IGN)
        signal.signal(signal.SIGTERM, signal.SIG_IGN)
        try:
            run.cleanup()
        except BaseException:
            report["status"] = "failed"
            raise
        finally:
            report["finished_at"] = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
            run.save()
    print("Native Start lifecycle and recovery passed; the remaining live matrix is still required. Evidence: " + str(path))


def interrupted(*_):
    raise KeyboardInterrupt()


if __name__ == "__main__":
    signal.signal(signal.SIGTERM, interrupted)
    try:
        main()
    except KeyboardInterrupt:
        print("Native Start interrupted; inspect cleanup evidence.", file=sys.stderr)
        raise SystemExit(130)
    except Failure as error:
        print("Native Start: " + str(error), file=sys.stderr)
        raise SystemExit(1)
    except Exception:
        # API failures and terminal output can contain credentials or bodies.
        # Keep the operator-facing failure bounded to our own diagnostics.
        print("Native Start failed; inspect acceptance.json and retry pinned cleanup if needed.", file=sys.stderr)
        raise SystemExit(1)
