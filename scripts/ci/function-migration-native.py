#!/usr/bin/env python3
"""Run successful migrations and controlled reply-boundary faults on two native hosts."""

import argparse
import hashlib
import ipaddress
import json
import os
import pathlib
import re
import shlex
import subprocess
import sys
import tempfile
import time
import uuid
from urllib.parse import unquote, urlsplit, parse_qs


ASSETS = ("vmmd", "schedd", "guest_init", "node22", "node24", "kernel",
          "builder_base", "firecracker", "leakcheck", "leakcheck_native_loops")
AGENT = pathlib.Path(__file__).with_name("function-migration-native-host.py")
ROOT = pathlib.Path(__file__).resolve().parents[2]
REPORT_VERSION = 2
REPORT_SCOPE = "native-migration-reply-boundaries-v1"
SCENARIOS = ("success", "lost-readiness-reply", "lost-commit-reply", "duplicate-adoption")


def require(condition, message):
    if not condition:
        raise ValueError(message)


def digest(path):
    result = hashlib.sha256()
    with open(path, "rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            result.update(block)
    return result.hexdigest()


def clean_revision():
    require(not subprocess.check_output(["git", "status", "--porcelain"], cwd=ROOT).strip(),
            "native evidence requires a clean committed source tree")
    return subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip()


def load_config(path):
    c = json.loads(pathlib.Path(path).read_text())
    fixture = str(uuid.UUID(c["fixture_id"]))
    require(c["version"] == 1 and fixture == c["fixture_id"] and uuid.UUID(fixture).int != 0,
            "native fixture identity is invalid")
    require(c["catalog"] == "faas_function_migration_native_" + fixture.replace("-", ""),
            "requires a dedicated disposable migration catalog")
    for key in ("api_url", "gateway_url"):
        url = urlsplit(c[key])
        require(url.scheme == "http" and url.hostname and ipaddress.ip_address(url.hostname).is_loopback
                and url.port and not url.username and not url.password and not url.path and not url.query and not url.fragment,
                "API and gateway must use explicit loopback HTTP ports")
    require(re.fullmatch(r"[A-Za-z0-9.-]+", c["app_domain"]), "invalid fixture app domain")
    ip = ipaddress.ip_address(c["sql_ip"])
    require(ip.version == 4 and ip.is_global, "SQL witness requires a prepared, guest-reachable public IPv4 interface")
    require(c["fc_version"] and pathlib.Path(c["sql_ca_path"]).is_absolute(), "missing native SQL CA or Firecracker version")
    require(len(c["hosts"]) == 2, "requires two native compute hosts")
    ids = set()
    for h in c["hosts"]:
        node = str(uuid.UUID(h["node_id"]))
        require(node == h["node_id"] and uuid.UUID(node).int != 0 and node not in ids, "compute node identities differ")
        ids.add(node)
        require(re.fullmatch(r"[A-Za-z0-9_@.:-]+", h["ssh"]) and not h["ssh"].startswith("-"), "invalid SSH target")
        require(set(h["assets"]) == set(ASSETS), "native asset paths are incomplete")
        for path in [h["remote_socket"], *h["assets"].values()]:
            require(path.startswith("/") and str(pathlib.PurePosixPath(path)) == path and
                    ".." not in pathlib.PurePosixPath(path).parts and not any(ch in path for ch in "\x00\r\n\t :"),
                    "invalid native fixture path")
    return c


def check_catalog(c, dsn):
    url = urlsplit(dsn)
    require(url.scheme in ("postgres", "postgresql") and url.hostname and unquote(url.path) == "/" + c["catalog"]
            and parse_qs(url.query).get("sslmode") == ["verify-full"],
            "DATABASE_URL must explicitly select the disposable catalog with sslmode=verify-full")


def remote(c, host, action, owner=None):
    request = {"action": action, "fixture_id": c["fixture_id"], "revision": c["revision"],
               "catalog": c["catalog"], "builds": c["builds"], "host": host}
    if owner is not None:
        request["owner"] = owner
    command = "python3 -c " + shlex.quote(AGENT.read_text())
    completed = subprocess.run(["ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", "-o",
                                "StrictHostKeyChecking=yes", host["ssh"], command], input=json.dumps(request),
                               capture_output=True, text=True, timeout=60)
    require(completed.returncode == 0, "native host witness failed: " + action)
    return json.loads(completed.stdout)


def check_test_results(path):
    conclusions = {}
    for line in path.read_text().splitlines():
        event = json.loads(line)
        if event["Action"] in ("pass", "fail", "skip"):
            require(event["Action"] == "pass", "a native test failed or skipped")
            conclusions[event.get("Test", "package")] = event["Action"]
    root = "TestFunctionMigrationDestinationPostgresMetal"
    cases = {root + "/" + runtime + "/" + scenario for runtime in ("node22", "node24") for scenario in SCENARIOS}
    required = cases | {root, root + "/node22", root + "/node24", "package"}
    observed = {name for name in conclusions if name.startswith(root + "/") and name.count("/") == 2}
    require(all(conclusions.get(name) == "pass" for name in required) and observed == cases,
            "native gate did not execute every runtime and reply-boundary case")


def check_report(path, config):
    report = json.loads(path.read_text())
    require(report.get("version") == REPORT_VERSION and report.get("scope") == REPORT_SCOPE
            and report.get("config") == config and report.get("both_hosts_leak_checked") is True
            and report.get("failure_matrix_qualified") is False and report.get("production_ready") is False,
            "native test evidence has a different fixture, source, binary or qualification scope")
    expected = sorted((runtime, scenario) for runtime in ("node22", "node24") for scenario in SCENARIOS)
    require(sorted((r.get("runtime", ""), r.get("scenario", "")) for r in report.get("runs", [])) == expected,
            "native report omitted or duplicated a reply-boundary case")


def run(config_path, output):
    require(os.geteuid() == 0, "requires root on the isolated native fixture coordinator")
    require(os.environ.get("FAAS_FUNCTION_MIGRATION_NATIVE_CONFIRM") == "isolated-two-hosts",
            "set FAAS_FUNCTION_MIGRATION_NATIVE_CONFIRM=isolated-two-hosts for the prepared fixture")
    require(os.environ.get("DATABASE_URL") and not os.environ.get("FAAS_SKIP_PG_TESTS"), "requires the prepared disposable SQL catalog")
    c = load_config(config_path)
    check_catalog(c, os.environ["DATABASE_URL"])
    revision = clean_revision()
    out = pathlib.Path(output).resolve()
    require(not out.is_relative_to(ROOT), "native evidence must stay outside the committed source checkout")
    require(not out.exists(), "native evidence destination must be new")
    out.mkdir(mode=0o700, parents=True)
    processes = []
    failure = None
    with tempfile.TemporaryDirectory(prefix="function-migration-native-") as work:
        work = pathlib.Path(work)
        work.chmod(0o700)
        try:
            c["revision"] = revision
            builds = {}
            env = os.environ.copy()
            env["CGO_ENABLED"] = "0"
            with (out / "build.log").open("wb") as log:
                for name, package in (("vmmd", "./cmd/vmmd"), ("schedd", "./cmd/schedd"),
                                      ("guest_init", "./guest/init"), ("node22", "./guest/runners/node22"), ("node24", "./guest/runners/node24")):
                    binary = work / name
                    subprocess.run(["go", "build", "-trimpath", "-buildvcs=false", "-tags", "metal,function_background_native", "-o", str(binary), package],
                                   cwd=ROOT, env=env, stdout=log, stderr=log, check=True)
                    builds[name] = digest(binary)
                binary = work / "native.test"
                subprocess.run(["go", "test", "-p", "1", "-race", "-tags", "metal,function_background_native", "-c", "-o", str(binary), "./cmd/e2e"],
                               cwd=ROOT, stdout=log, stderr=log, check=True)
            for name, key in (("kernel", "FAAS_TEST_KERNEL"), ("builder_base", "FAAS_BUILDER_BASE_PATH"), ("firecracker", "FAAS_TEST_FIRECRACKER")):
                require(os.environ.get(key), "missing staged native prerequisite " + key)
                builds[name] = digest(os.environ[key])
            builds["leakcheck"] = digest(ROOT / "deploy/scripts/leakcheck.sh")
            builds["leakcheck_native_loops"] = digest(ROOT / "deploy/scripts/leakcheck_native_loops.py")
            c["builds"], c["test_binary_sha256"] = builds, digest(binary)
            require(c["fc_version"] == os.environ.get("FAAS_TEST_FC_VERSION"), "Firecracker version must match the staged fixture")
            for index, host in enumerate(c["hosts"]):
                host["proof"] = remote(c, host, "preflight")
                require(remote(c, host, "leakcheck").get("leakcheck") is True, "native host was not clean before the run")
                host["local_socket"] = str(work / (str(index) + ".sock"))
                processes.append(subprocess.Popen(["ssh", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o",
                                                    "ExitOnForwardFailure=yes", "-o", "StreamLocalBindUnlink=no", "-N", "-L",
                                                    host["local_socket"] + ":" + host["remote_socket"], host["ssh"]],
                                                   stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL))
            require(c["hosts"][0]["proof"]["machine_sha256"] != c["hosts"][1]["proof"]["machine_sha256"] and
                    c["hosts"][0]["proof"]["boot_sha256"] != c["hosts"][1]["proof"]["boot_sha256"], "SSH aliases must reach different physical hosts")
            deadline = time.monotonic() + 10
            while not all(pathlib.Path(h["local_socket"]).exists() for h in c["hosts"]):
                require(time.monotonic() < deadline and all(p.poll() is None for p in processes), "native RPC tunnel did not become ready")
                time.sleep(0.05)
            config = work / "config.json"
            config.write_text(json.dumps(c))
            config.chmod(0o600)
            require(clean_revision() == revision, "source changed while native binaries were built")
            case_env = os.environ.copy()
            case_env.update({"FAAS_REQUIRE_FUNCTION_MIGRATION_NATIVE": "1", "FAAS_FUNCTION_MIGRATION_CONFIG": str(config),
                             "FAAS_FUNCTION_MIGRATION_DRIVER": str(pathlib.Path(__file__).resolve()),
                             "FAAS_FUNCTION_MIGRATION_REPORT": str(out / "migration-replies.json")})
            with (out / "tests.raw").open("wb") as log, (out / "tests.stderr").open("wb") as err:
                completed = subprocess.run([str(binary), "-test.v=test2json", "-test.run=^TestFunctionMigrationDestinationPostgresMetal$", "-test.count=1", "-test.timeout=120m"],
                                           cwd=ROOT, env=case_env, stdout=log, stderr=err)
            # The compiled binary emits test2json protocol text. Convert it
            # through the tool that Go uses, rather than accepting PASS text.
            converted = subprocess.run(["go", "tool", "test2json", "-p", "github.com/onebox-faas/faas/cmd/e2e"],
                                       input=(out / "tests.raw").read_bytes(), capture_output=True, check=True)
            events = out / "tests.jsonl"
            events.write_bytes(converted.stdout)
            require(completed.returncode == 0, "native suite failed; inspect private test evidence")
            check_test_results(events)
            check_report(out / "migration-replies.json", c)
            require(clean_revision() == revision, "source changed during native execution")
        except Exception as error:
            failure = error
        finally:
            leaks = []
            if c.get("builds"):
                for host in c["hosts"]:
                    try:
                        leaks.append(remote(c, host, "leakcheck").get("leakcheck") is True)
                    except (ValueError, OSError, subprocess.SubprocessError):
                        leaks.append(False)
            for process in processes:
                process.terminate()
                try:
                    process.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait()
    removed = not work.exists() and all(process.poll() is not None for process in processes)
    verdict = {"source_revision": revision, "scope": REPORT_SCOPE, "reply_boundaries_qualified": failure is None and leaks == [True, True] and removed,
               "failure_matrix_qualified": False, "production_ready": False, "host_leakchecks": leaks,
               "native_test_binary_sha256": c.get("test_binary_sha256"), "temporary_binaries_removed": removed}
    (out / "verdict.json").write_text(json.dumps(verdict, indent=2) + "\n")
    if failure is not None:
        raise failure
    require(verdict["reply_boundaries_qualified"], "native host cleanup did not pass")
    print(json.dumps(verdict))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", required=True)
    parser.add_argument("--out")
    parser.add_argument("--probe", nargs=2, metavar=("NODE_ID", "ACTION"))
    args = parser.parse_args()
    if args.probe:
        c = load_config(args.config)
        host = next(h for h in c["hosts"] if h["node_id"] == args.probe[0])
        owner = json.loads(sys.stdin.read()) if args.probe[1] in ("owner", "retired") else None
        print(json.dumps(remote(c, host, args.probe[1], owner)))
    else:
        require(args.out, "--out is required for native evidence")
        run(args.config, args.out)


if __name__ == "__main__":
    os.umask(0o077)
    try:
        main()
    except ValueError as error:
        print("native function migration gate failed: " + str(error), file=sys.stderr)
        sys.exit(1)
    except (OSError, KeyError, TypeError, StopIteration, subprocess.SubprocessError):
        print("native function migration gate failed; verify fixture prerequisites and private evidence logs", file=sys.stderr)
        sys.exit(1)
