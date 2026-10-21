#!/usr/bin/env python3
"""Prepare two dedicated hosts, execute native cost drills, retain all evidence."""
import hashlib
import importlib.util
import ipaddress
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import signal
import subprocess
import sys
import tarfile
import threading
import time

ROOT = Path(__file__).resolve().parents[2]
BUILD_TIMEOUT = 30 * 60
TRANSPORT_BUILD_TIMEOUT = 60 * 60
FILESYSTEM_TIMEOUT = 5 * 60
SINGLE_HOST_TIMEOUT = 79 * 60
PEER_NETWORK_TIMEOUT = 5 * 60
NATIVE_TIMEOUT = 68 * 60
TRANSPORT_PACKAGES = (
    "./cmd/vmmd", "./cmd/vmmd-stream-bridge", "./cmd/vmmd-raw-bridge",
    "./cmd/vmmd-tcp-bridge", "./cmd/vmmd-udp-bridge", "./pkg/e2etest/financialguest",
)
spec = importlib.util.spec_from_file_location("financial_pair_evidence", ROOT / "scripts/ci/financial-pair-evidence.py")
evidence = importlib.util.module_from_spec(spec)
spec.loader.exec_module(evidence)


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def remote(target, args):
    # SSH invokes a remote shell; quote every argv element exactly once.
    return ["ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "-o", "ServerAliveInterval=10", "-o", "ServerAliveCountMax=3", "--", target, shlex.join(args)]


def jail_root(source_sha, run_id):
    # Reserve a short, exclusive namespace: versioned Firecracker + instance
    # UUID + vsock port must still fit Linux's 107-byte Unix socket pathname.
    return "/srv/f" + hashlib.sha256((source_sha + "-" + run_id).encode()).hexdigest()[:6]


def run(args, log, env=None, timeout=BUILD_TIMEOUT):
    print("financial pair: " + log.name, flush=True)
    with log.open("wb") as output:
        try:
            return subprocess.run(args, cwd=ROOT, env=env, stdout=output, stderr=subprocess.STDOUT, timeout=timeout, check=False).returncode
        except subprocess.TimeoutExpired:
            output.write(b"\nfinancial pair: command deadline expired\n")
            return 124


class Hold:
    def __init__(self, args, log):
        self.output = log.open("wb")
        self.process = subprocess.Popen(args, stdin=subprocess.PIPE, stdout=self.output, stderr=subprocess.STDOUT)
        self.stopping = threading.Event()
        self.writer = threading.Thread(target=self.heartbeat, daemon=True)
        self.writer.start()

    def heartbeat(self):
        while not self.stopping.is_set():
            try:
                self.process.stdin.write(b"alive\n")
                self.process.stdin.flush()
            except (BrokenPipeError, OSError, ValueError):
                return
            self.stopping.wait(5)

    def finish(self):
        self.stopping.set()
        self.writer.join()
        try:
            self.process.stdin.write(b"finish\n")
            self.process.stdin.flush()
            self.process.stdin.close()
        except (BrokenPipeError, OSError):
            pass
        finally:
            try:
                self.process.stdin.close()
            except OSError:
                pass
        try:
            return self.process.wait(timeout=180)
        except subprocess.TimeoutExpired:
            self.process.kill()
            self.process.wait()
            return 124
        finally:
            self.output.close()


def read_remote_json(target, path):
    result = subprocess.run(remote(target, ["sudo", "-n", "cat", str(path)]), stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, timeout=20, check=False)
    if result.returncode != 0:
        return None
    return json.loads(result.stdout)


def await_ready(hold, path, target=None):
    until = time.monotonic() + 90
    while time.monotonic() < until:
        require(hold.process.poll() is None, "host hold exited before becoming ready")
        try:
            report = read_remote_json(target, path) if target else json.loads(path.read_text())
            if report:
                return report
        except (OSError, ValueError, subprocess.TimeoutExpired):
            pass
        time.sleep(1)
    raise RuntimeError("host hold did not become ready within 90 seconds")


def build_fixture(bundle, go, env, output):
    for name, args, cgo, timeout in (
        ("guest-init", ["build", "-trimpath", "-buildvcs=false", "-o", str(bundle / "guest-init"), "./guest/init"], "0", BUILD_TIMEOUT),
        ("transports", ["build", "-p=1", "-trimpath", "-buildvcs=false", "-o", str(bundle) + "/", *TRANSPORT_PACKAGES], "0", TRANSPORT_BUILD_TIMEOUT),
        ("fcvm", ["test", "-c", "-race", "-tags=metal", "-p=1", "-o", str(bundle / "fcvm.test"), "./pkg/fcvm"], "1", BUILD_TIMEOUT),
    ):
        build_env = {**env, "CGO_ENABLED": cgo}
        require(run([go, *args], output / f"build-{name}.log", build_env, timeout=timeout) == 0, f"{name} build failed")
    kernel = Path(env.get("FAAS_TEST_KERNEL", "/srv/fc/base/vmlinux-6.18.54"))
    shutil.copyfile(kernel, bundle / "kernel")
    busybox = Path(env.get("FAAS_METAL_BUSYBOX", "/bin/busybox"))
    kind = subprocess.check_output(["file", str(busybox)], text=True)
    require("statically linked" in kind or "static-pie linked" in kind, "guest busybox must be static")
    base, layer, traffic = bundle / "base", bundle / "layer", bundle / "traffic"
    for name in ("bin", "sbin", "etc/faas", "dev", "overlay", "proc", "run", "sys/fs/cgroup", "tmp"):
        (base / name).mkdir(parents=True, exist_ok=True)
    (layer / "upper/etc/faas").mkdir(parents=True)
    (layer / "work").mkdir()
    (traffic / "upper/etc/faas").mkdir(parents=True)
    (traffic / "work").mkdir()
    (traffic / "upper/etc/faas/app.json").write_text(json.dumps({"entrypoint": ["/bin/financial-guest"], "port": 9000}) + "\n")
    shutil.copyfile(busybox, base / "bin/busybox")
    (base / "bin/busybox").chmod(0o755)
    shutil.copyfile(bundle / "financialguest", base / "bin/financial-guest")
    (base / "bin/financial-guest").chmod(0o755)
    shutil.copyfile(bundle / "guest-init", base / "sbin/init")
    (base / "sbin/init").chmod(0o755)
    for name in ("sh", "ash", "cat", "true"):
        (base / "bin" / name).symlink_to("/bin/busybox")
    (layer / "upper/etc/faas/app.json").write_text(json.dumps({"entrypoint": ["/bin/busybox", "httpd", "-f", "-p", "8080", "-h", "/"], "port": 8080}) + "\n")
    (layer / "upper/index.html").write_text("financial pair ready\n")
    for name, source, size in (("base", base, 128 << 20), ("layer", layer, 16 << 20), ("traffic", traffic, 16 << 20)):
        image = bundle / (name + ".ext4")
        with image.open("wb") as stream:
            stream.truncate(size)
        require(run(["mkfs.ext4", "-q", "-O", "^has_journal", "-d", str(source), "-F", str(image)], output / f"mkfs-{name}.log", env, timeout=FILESYSTEM_TIMEOUT) == 0, "fixture mkfs failed")
        require(run(["e2fsck", "-fn", str(image)], output / f"e2fsck-{name}.log", env, timeout=FILESYSTEM_TIMEOUT) == 0, "fixture filesystem check failed")
    shutil.rmtree(base)
    shutil.rmtree(layer)
    shutil.rmtree(traffic)


def main():
    env = os.environ.copy()
    source_sha, run_id = env.get("FAAS_E2E_SOURCE_SHA", ""), env.get("FAAS_E2E_RUN_ID", "")
    require(re.fullmatch(r"[a-f0-9]{40}", source_sha) is not None, "invalid source SHA")
    require(re.fullmatch(r"[A-Za-z0-9._-]{1,32}", run_id) is not None, "invalid run ID")
    require(os.geteuid() == 0 and Path("/etc/faas/financial-pair-acceptance-host").is_file(), "requires a dedicated root controller")
    require((ROOT / ".faas-e2e-source-sha").read_text().strip() == source_sha, "source archive differs from reviewed SHA")
    go = env["FAAS_E2E_GO"]
    require(subprocess.check_output([go, "env", "GOVERSION"], text=True).strip() == "go" + re.search(r"^go (\S+)$", (ROOT / "go.mod").read_text(), re.M)[1], "Go toolchain differs from go.mod")
    target = env["FAAS_FINANCIAL_PAIR_SSH_B"]
    require(re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.@-]*", target) is not None, "invalid SSH peer target")
    addresses = [ipaddress.ip_address(env[name]) for name in ("FAAS_FINANCIAL_PAIR_ADDR_A", "FAAS_FINANCIAL_PAIR_ADDR_B")]
    require(addresses[0] != addresses[1] and all(not (addr.is_loopback or addr.is_unspecified or addr.is_multicast or addr.is_link_local) for addr in addresses), "pair traffic requires distinct, reachable fixture addresses")
    require(env.get("FAAS_SKIP_PG_TESTS", "") == "", "financial acceptance cannot skip PostgreSQL")
    require(env.get("DATABASE_URL"), "disposable fixture DATABASE_URL is required")
    transfer = Path(env["FAAS_E2E_TRANSFER_ROOT"])
    require(re.fullmatch(r"/var/tmp/faas-native-e2e-[A-Za-z0-9._-]+", str(transfer)) is not None, "invalid transfer root")
    output = transfer / "financial-pair-evidence"
    output.mkdir(parents=True, exist_ok=False)
    bundle = Path(f"/var/tmp/faas-financial-pair-{source_sha}-{run_id}")
    bundle.mkdir(mode=0o700)
    jail = jail_root(source_sha, run_id)
    statuses = {name: 125 for name in ("single_host", "native", "peer_network", "host_a", "host_b")}
    holds = []
    try:
        # This runner supplies only explicit host-owned SQL; never fall back
        # to a production daemon's metering/provider credentials.
        single_script = bundle / "single-host.sh"
        single_script.write_text(
            "#!/usr/bin/env bash\nset -Eeuo pipefail\nset -a\nsource /etc/faas/financial-pair.env\nset +a\n"
            + "".join("export " + name + "=" + shlex.quote(env[name]) + "\n" for name in ("FAAS_E2E_SOURCE_SHA", "FAAS_E2E_GO", "FAAS_E2E_RUN_ID", "FAAS_E2E_TRANSFER_ROOT"))
            + 'export FAAS_E2E_PHASE=financial-system FAAS_E2E_DATABASE_URL="${DATABASE_URL:?}"\n'
            + "exec bash " + shlex.quote(str(ROOT / "scripts/ci/run-native-e2e.sh")) + "\n"
        )
        statuses["single_host"] = run([
            "systemd-run", "--quiet", "--pipe", "--wait", "--collect",
            "--unit=faas-financial-" + run_id + "-single-host", "--property=RuntimeMaxSec=75m",
            "--property=TimeoutStopSec=120s", "--working-directory=" + str(ROOT), "bash", str(single_script),
        ], output / "single-host.log", env, timeout=SINGLE_HOST_TIMEOUT)
        single_path = transfer / f"financial-system-verdict-{run_id}.json"
        if single_path.is_file():
            shutil.copyfile(single_path, output / "single-host.json")
        collected = output / "single-host"
        collected.mkdir()
        for path in (single_path, transfer / "cmd-e2e-financial-system.log", transfer / "financial-system-provider.log"):
            if path.is_file():
                shutil.copyfile(path, collected / path.name)
        # Retain raw reconciliation vectors, not just a successful verdict.
        records = transfer / "financial-system-records"
        if records.is_dir():
            shutil.copytree(records, output / "reconciliation")
            shutil.copytree(records, collected / "financial-system-records")
        build_fixture(bundle, go, env, output)
        for name in ("financial-pair-host.sh", "financial-pair-evidence.py", "financial-pair-traffic.py", "native-e2e-reap.sh"):
            shutil.copyfile(ROOT / "scripts/ci" / name, bundle / name)
        scripts = bundle / "deploy/scripts"
        scripts.mkdir(parents=True)
        for name in ("leakcheck.sh", "leakcheck_native_loops.py"):
            shutil.copyfile(ROOT / "deploy/scripts" / name, scripts / name)
        support = evidence.traffic_sources(ROOT)
        evidence.write_json(bundle / "source.json", {"source_sha": source_sha, "run_id": run_id, "traffic_sources": support})
        peer_env = {
            "FAAS_FINANCIAL_PAIR": "1", "FAAS_FINANCIAL_COMPUTE_ACCEPTANCE": "1",
            "FAAS_FINANCIAL_PAIR_RUN_ID": run_id, "FAAS_FINANCIAL_PAIR_BUNDLE": str(bundle),
            "FAAS_FINANCIAL_ACCEPTANCE_ROOT": str(bundle / "owned"),
            "FAAS_FINANCIAL_PAIR_JAIL_ROOT": jail, "TMPDIR": str(bundle / "tmp"),
            "FAAS_TEST_KERNEL": str(bundle / "kernel"), "FAAS_TEST_BASE_ROOTFS": str(bundle / "base.ext4"),
            "FAAS_TEST_LAYER_ROOTFS": str(bundle / "layer.ext4"), "FAAS_TEST_VMMD_BINARY": str(bundle / "vmmd"),
            "FAAS_TEST_FC_VERSION": env.get("FAAS_TEST_FC_VERSION", "1.17.0"),
            "FAAS_STREAM_BRIDGE_VERSION": "v2", "FAAS_STREAM_BRIDGE_PERSISTENT": "1",
            **{"FAAS_VMMD_" + kind.upper() + "_BRIDGE_PATH": str(bundle / ("vmmd-" + kind + "-bridge")) for kind in ("stream", "raw", "tcp", "udp")},
            "PATH": "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
        }
        env_file = bundle / "peer.env"
        env_file.write_text("".join("export " + name + "=" + shlex.quote(value) + "\n" for name, value in peer_env.items()))
        env_file.chmod(0o600)
        archive = transfer / "financial-pair-fixture.tar.gz"
        with tarfile.open(archive, "w:gz") as tar:
            for path in sorted(bundle.iterdir()):
                tar.add(path, arcname=path.name)
        evidence.write_json(output / "fixture.json", {"archive_sha256": evidence.digest(archive), "assets": {name: evidence.digest(bundle / name) for name in evidence.ASSETS}, "traffic_host_b": str(addresses[1]), "traffic_sources": support})
        # Refuse an existing run path. No untrusted archive entries are used:
        # this archive was built above from this exact source and its assets.
        prepare = ["sudo", "-n", "bash", "-c", 'set -euo pipefail; test -f /etc/faas/financial-pair-acceptance-host; umask 077; mkdir -- "$1"; tar -xz --no-same-owner -C "$1"', "_", str(bundle)]
        with archive.open("rb") as stream:
            result = subprocess.run(remote(target, prepare), stdin=stream, stdout=subprocess.DEVNULL, timeout=180, check=False)
        require(result.returncode == 0, "peer fixture transfer failed")
        local_hold = Hold(["bash", str(bundle / "financial-pair-host.sh"), "hold", str(bundle)], output / "host-a-hold.log")
        holds.append(("host_a", local_hold))
        a = await_ready(local_hold, bundle / "ready.json")
        peer_hold = Hold(remote(target, ["sudo", "-n", "bash", str(bundle / "financial-pair-host.sh"), "hold", str(bundle)]), output / "host-b-hold.log")
        holds.append(("host_b", peer_hold))
        b = await_ready(peer_hold, bundle / "ready.json", target)
        require(a["machine_id_sha256"] != b["machine_id_sha256"] and a["boot_id_sha256"] != b["boot_id_sha256"], "SSH peer is the controller host")
        require(a["assets"] == b["assets"] and a["tools"] == b["tools"], "host assets/tools differ")
        native_env = {**env, **peer_env, "FAAS_FINANCIAL_PAIR_MACHINE_B": b["machine_id_sha256"]}
        if not native_env.get("FAAS_PUBLIC_IFACE"):
            route = subprocess.check_output(["ip", "-j", "route", "show", "default"], text=True)
            native_env["FAAS_PUBLIC_IFACE"] = json.loads(route)[0]["dev"]
        component, _ = evidence.source_tests(ROOT)
        pair, _ = evidence.source_tests(ROOT, pair=True)
        pattern = "^(" + "|".join(sorted({**component, **pair})) + ")$"
        statuses["peer_network"] = run(remote(target, ["sudo", "-n", "bash", str(bundle / "financial-pair-host.sh"), "network", str(bundle)]), output / "peer-network.log", timeout=PEER_NETWORK_TIMEOUT)
        # Run the controller test in an owned unit too. A Go alarm or a lost
        # SSH channel must not leave its producer children running into host
        # restoration. Credentials are sourced on-host, never argv values.
        local_script = bundle / "controller.sh"
        local_script.write_text(
            "#!/usr/bin/env bash\nset -Eeuo pipefail\nset -a\nsource /etc/faas/financial-pair.env\nset +a\n"
            + "source " + shlex.quote(str(env_file)) + "\n"
            + "export FAAS_FINANCIAL_PAIR_MACHINE_B=" + shlex.quote(b["machine_id_sha256"]) + "\n"
            + "export FAAS_PUBLIC_IFACE=" + shlex.quote(native_env["FAAS_PUBLIC_IFACE"]) + "\n"
            + "exec " + shlex.join([str(bundle / "fcvm.test"), "-test.v", "-test.run=" + pattern, "-test.timeout=60m"]) + "\n"
        )
        statuses["native"] = run([
            "systemd-run", "--quiet", "--pipe", "--wait", "--collect",
            "--unit=faas-financial-" + run_id + "-controller", "--property=RuntimeMaxSec=65m",
            "--property=KillMode=mixed", "--property=TimeoutStopSec=15s", "bash", str(local_script),
        ], output / "native.log", native_env, timeout=NATIVE_TIMEOUT)
    except (RuntimeError, OSError, ValueError, KeyError, subprocess.SubprocessError) as error:
        # Exception messages must never include SQL DSNs or full environments.
        (output / "failure.txt").write_text(type(error).__name__ + ": " + str(error) + "\n")
        print("financial pair: preparation/execution failed; see failure.txt", file=sys.stderr)
    finally:
        for name, hold in reversed(holds):
            statuses[name] = hold.finish()
        for node in ("a", "b"):
            directory = output / ("host-" + node)
            directory.mkdir(exist_ok=True)
            for name in ("ready.json", "cleanup.json", "leakcheck.log", "baseline.log"):
                if node == "a":
                    if (bundle / name).is_file():
                        shutil.copyfile(bundle / name, directory / name)
                else:
                    with (directory / name).open("wb") as stream:
                        try:
                            subprocess.run(remote(target, ["sudo", "-n", "cat", str(bundle / name)]), stdout=stream, stderr=subprocess.DEVNULL, timeout=20, check=False)
                        except subprocess.SubprocessError:
                            pass
        evidence.write_json(output / "commands.json", statuses)
        report = evidence.build_report(ROOT, output, source_sha, run_id)
        if report["qualified"]:
            # Evidence is already copied and hashed. Successful runs need not
            # accumulate hundreds of MB of test executables on either host.
            for node, command in (
                ("b", remote(target, ["sudo", "-n", "bash", str(bundle / "financial-pair-host.sh"), "retire", str(bundle)])),
                ("a", ["bash", str(bundle / "financial-pair-host.sh"), "retire", str(bundle)]),
            ):
                code = run(command, output / f"host-{node}-retire.log", timeout=120)
                if code != 0:
                    report["qualified"] = False
                    report["failures"].append(f"host {node}: completed fixture removal failed")
            if report["qualified"]:
                stage = Path("/srv/fc/acceptance") / f"e2e-{source_sha}-{run_id}"
                if stage.is_dir():
                    try:
                        shutil.rmtree(stage)
                    except OSError:
                        report["qualified"] = False
                        report["failures"].append("completed full-system staging removal failed")
        evidence.write_json(output / "qualification.json", report)
        print(f"financial pair: {'PASS' if report['qualified'] else 'FAIL'}; promotion_ready=false; evidence={output}", flush=True)
    return 0 if report["qualified"] else 1


if __name__ == "__main__":
    def interrupted(signum, _frame):
        raise RuntimeError(f"controller interrupted by signal {signum}")
    signal.signal(signal.SIGTERM, interrupted)
    signal.signal(signal.SIGINT, interrupted)
    sys.exit(main())
