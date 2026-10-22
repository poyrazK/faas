#!/usr/bin/env python3
"""Real Postgres fixture acceptance; owns one disposable local container."""
import json
import os
from pathlib import Path
import signal
import subprocess
import time
import tempfile
import uuid


def setup_tls(docker, container, environment, directory):
    """Install a fresh loopback test identity; private material stays ephemeral."""
    cert = Path(directory) / "cert.pem"
    key = Path(directory) / "key.pem"
    subprocess.run(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1",
                    "-subj", "/CN=localhost", "-addext", "subjectAltName=DNS:localhost,IP:127.0.0.1",
                    "-keyout", str(key), "-out", str(cert)], check=True, timeout=10,
                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    for source, destination in ((cert, "/tmp/gregale-cert.pem"), (key, "/tmp/gregale-key.pem")):
        subprocess.run(docker + ["exec", "--interactive", "--user", "postgres", container,
                                "sh", "-c", "umask 077; cat > " + destination],
                       input=source.read_bytes(), env=environment, check=True, timeout=10)
    configuration = "ssl=on\nssl_cert_file='/tmp/gregale-cert.pem'\nssl_key_file='/tmp/gregale-key.pem'\n"
    subprocess.run(docker + ["exec", "--interactive", "--user", "postgres", container,
                            "sh", "-c", "cat >> /var/lib/postgresql/data/postgresql.conf"],
                   input=configuration, text=True, env=environment, check=True, timeout=10)
    subprocess.run(docker + ["exec", "--user", "postgres", container, "psql", "-d", "gregale_write_test",
                            "-c", "SELECT pg_reload_conf()"], env=environment, check=True, timeout=10,
                   stdout=subprocess.DEVNULL)
    trust = Path(directory) / "trust.pem"
    # Retain any inherited additional public CA roots alongside the fixture CA.
    inherited = environment.get("NODE_EXTRA_CA_CERTS")
    trust.write_bytes((Path(inherited).read_bytes() + b"\n" if inherited else b"") + cert.read_bytes())
    environment.update(NODE_EXTRA_CA_CERTS=str(trust), GREGALE_DEPENDENCY_TLS_DIRECTORY=directory)


def main():
    repo = Path(__file__).resolve().parents[2]
    environment = dict(os.environ)
    for name in ("DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_TLS", "DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH"):
        environment.pop(name, None)
    docker = ["docker", "--host=unix:///var/run/docker.sock"]
    subprocess.run(docker + ["info", "--format", "{{.ServerVersion}}"], check=True, env=environment, timeout=20)
    provider = repo / "examples/scenario-http-write/payment-postgres"
    subprocess.run(["npm", "ci", "--ignore-scripts", "--no-audit", "--no-fund"], cwd=provider, env=environment, check=True, timeout=120)
    name = "gregale-write-pg-" + uuid.uuid4().hex[:12]
    password = uuid.uuid4().hex
    container = None
    attempted = False
    environment["POSTGRES_PASSWORD"] = password
    def interrupted(_signal, _frame):
        raise KeyboardInterrupt()
    signal.signal(signal.SIGTERM, interrupted)
    try:
        attempted = True
        container = subprocess.check_output(docker + ["run", "--detach", "--rm", "--name", name,
            "--memory", "256m", "--read-only", "--tmpfs", "/var/lib/postgresql/data:rw,size=128m",
            "--tmpfs", "/var/run/postgresql:rw,size=8m", "--tmpfs", "/tmp:rw,size=8m", "--publish", "127.0.0.1::5432",
            "--env", "POSTGRES_DB=gregale_write_test", "--env", "POSTGRES_PASSWORD", "postgres:16"],
            env=environment, text=True, timeout=120).strip()
        ready = False
        for _ in range(60):
            result = subprocess.run(docker + ["exec", container, "pg_isready", "-U", "postgres", "-d", "gregale_write_test"],
                env=environment, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=5)
            if result.returncode == 0:
                ready = True
                break
            time.sleep(0.2)
        if not ready:
            raise RuntimeError("disposable Postgres did not become ready")
        port = subprocess.check_output(docker + ["inspect", "--format", '{{(index (index .NetworkSettings.Ports "5432/tcp") 0).HostPort}}', container],
            env=environment, text=True, timeout=10).strip()
        url = "postgres://postgres:" + password + "@127.0.0.1:" + port + "/gregale_write_test?sslmode=disable"
        environment.update(GREGALE_DEPENDENCY_DATABASE_ADMIN_URL=url, GREGALE_DEPENDENCY_DATABASE_DISPOSABLE="1")
        with tempfile.TemporaryDirectory(prefix="gregale-pg-tls-") as directory:
            setup_tls(docker, container, environment, directory)
            subprocess.run(["node", "--test", str(repo / "examples/scenario-http-write/test/dependency-postgres.integration.mjs")],
                cwd=repo, env=environment, check=True, timeout=240)
    finally:
        if attempted:
            # Own the unique name before submission, including a lost startup receipt.
            result = subprocess.run(docker + ["rm", "--force", name], env=environment, timeout=30,
                                    stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
            if result.returncode != 0:
                remaining = subprocess.check_output(docker + ["ps", "--all", "--filter", "name=^/" + name + "$", "--format", "{{.Names}}"],
                                                    env=environment, text=True, timeout=10).strip()
                if remaining:
                    raise RuntimeError("disposable Postgres container cleanup failed")
    # Do not emit database URLs, passwords, or container logs.
    print(json.dumps({"status": "passed", "database": "Postgres 16", "container_removed": True}))


if __name__ == "__main__":
    main()
