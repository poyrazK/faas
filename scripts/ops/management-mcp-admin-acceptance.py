#!/usr/bin/env python3
"""Verify private operator control sockets and CLI metadata without customer writes."""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import tempfile
import threading
import urllib.error
import urllib.request
from http.server import ThreadingHTTPServer


def load_sibling(name):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name(name + ".py"))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--gregale-binary", required=True)
    args = parser.parse_args()
    metrics = load_sibling("management-mcp-metrics-acceptance")
    fixture = load_sibling("management-mcp-client-acceptance")
    backend = ThreadingHTTPServer(("127.0.0.1", 0), fixture.FixtureAPI)
    threading.Thread(target=backend.serve_forever, daemon=True).start()
    process = None
    try:
        with tempfile.TemporaryDirectory(prefix="gmcp-admin-") as root:
            socket = Path(root) / "control.sock"
            env = dict(os.environ, HOME=root, XDG_CONFIG_HOME=root,
                       FAAS_API=f"http://127.0.0.1:{backend.server_port}", FAAS_TOKEN="")
            argv = [args.gregale_binary, "--non-interactive", "mcp", "serve",
                    "--transport", "streamable-http", "--listen", "127.0.0.1:0",
                    "--resource-url", "https://management.example/mcp",
                    "--oauth-redirect-uri", "https://client.example/callback",
                    "--admin-socket", str(socket)]
            process = subprocess.Popen(argv, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                       stderr=subprocess.PIPE, bufsize=0, env=env)
            notice = metrics.read_line(process.stderr).decode()
            assert "control socket" in notice, notice
            notice = metrics.read_line(process.stderr).decode()
            match = re.search(r"MCP listening on (127\.0\.0\.1:\d+)", notice)
            assert match, notice
            assert socket.stat().st_mode & 0o777 == 0o600

            def command(verb, extra=()):
                return subprocess.run(
                    [args.gregale_binary, "--non-interactive", "mcp", "connections", verb,
                     "--socket", str(socket)] + list(extra), capture_output=True, text=True,
                    env=env, timeout=10)

            listed = command("list")
            assert listed.returncode == 0, listed.stderr
            assert json.loads(listed.stdout) == {"items": [], "total": 0}
            refused = command("revoke", ["--grant-id", "gmcp_grant_missing"])
            assert refused.returncode != 0 and "HTTP 404" in refused.stderr
            collision = subprocess.run(argv, capture_output=True, text=True, env=env, timeout=10)
            assert collision.returncode != 0 and "already exists" in collision.stderr
            assert command("list").returncode == 0, "collision affected existing bridge"
            request = urllib.request.Request(
                "http://" + match.group(1) + "/connections", headers={"Host": "management.example"})
            try:
                with urllib.request.urlopen(request, timeout=5):
                    pass
            except urllib.error.HTTPError as error:
                assert error.code == 404
            else:
                raise AssertionError("operator endpoint exposed on public HTTP listener")
            os.chmod(root, 0o755)
            try:
                assert command("list").returncode != 0, "unsafe parent permissions accepted"
            finally:
                os.chmod(root, 0o700)
            process.send_signal(signal.SIGTERM)
            assert process.wait(timeout=12) == 130
            assert not socket.exists(), "control socket survived shutdown"
            assert fixture.FixtureAPI.calls == 0 and fixture.FixtureAPI.mutations == 0
    finally:
        if process is not None:
            if process.poll() is None:
                process.kill()
                process.wait()
            for stream in [process.stdin, process.stdout, process.stderr]:
                stream.close()
        backend.shutdown()
        backend.server_close()
    print(json.dumps({"operator_cli": "passed", "socket_permissions": "passed",
                      "public_isolation": "passed", "collision_guard": "passed",
                      "shutdown_cleanup": "passed", "customer_api_calls": 0, "deployment_writes": 0}))


if __name__ == "__main__":
    main()
