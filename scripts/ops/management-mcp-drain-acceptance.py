#!/usr/bin/env python3
"""Verify CLI SIGTERM cleanup and encrypted-state reopening with local fixtures."""
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
import time
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
    fixture = load_sibling("management-mcp-client-acceptance")
    metrics = load_sibling("management-mcp-metrics-acceptance")
    entered = threading.Event()
    canceled = threading.Event()

    class API(fixture.FixtureAPI):
        def do_GET(self):
            if self.path == "/v1/apps/alpha/health":
                entered.set()
                self.connection.settimeout(10)
                if self.connection.recv(1) == b"":
                    canceled.set()
                return
            super().do_GET()

    backend = ThreadingHTTPServer(("127.0.0.1", 0), API)
    threading.Thread(target=backend.serve_forever, daemon=True).start()
    process = None
    try:
        with tempfile.TemporaryDirectory(prefix="gregale-drain-cli-") as root:
            env = dict(os.environ, HOME=root, XDG_CONFIG_HOME=root,
                       FAAS_API=f"http://127.0.0.1:{backend.server_port}",
                       FAAS_TOKEN="fp_test_codex_acceptance_fixture")

            def start(extra):
                return subprocess.Popen(
                    [args.gregale_binary, "--non-interactive", "mcp", "serve",
                     "--metrics-listen", "127.0.0.1:0"] + extra,
                    stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                    text=True, env=env)

            def terminate():
                began = time.monotonic()
                process.send_signal(signal.SIGTERM)
                assert process.wait(timeout=12) == 130
                assert time.monotonic() - began < 2, "observations did not stop promptly"

            process = start(["--transport", "stdio"])
            notice = metrics.read_line(process.stderr)
            match = re.search(r"metrics listening on (127\.0\.0\.1:\d+)", notice)
            assert match, notice
            metric_url = "http://" + match.group(1) + "/metrics"
            process.stdin.write(json.dumps({"jsonrpc": "2.0", "id": 1, "method": "initialize",
                "params": {"protocolVersion": "2026-07-28", "capabilities": {},
                           "clientInfo": {"name": "drain-smoke", "version": "1"}}}) + "\n")
            process.stdin.flush()
            assert "result" in json.loads(metrics.read_line(process.stdout))
            for message in [
                {"jsonrpc": "2.0", "method": "notifications/initialized"},
                {"jsonrpc": "2.0", "id": 2, "method": "tools/call",
                 "params": {"name": "gregale_get_health", "arguments": {"app": "alpha"}}},
            ]:
                process.stdin.write(json.dumps(message) + "\n")
            process.stdin.flush()
            assert entered.wait(5), "native read did not start"
            with urllib.request.urlopen(metric_url, timeout=5) as response:
                assert 'gregale_management_mcp_active{kind="short_requests"} 1' in response.read().decode()
            terminate()
            assert canceled.wait(2), "SIGTERM did not cancel native API read"
            try:
                with urllib.request.urlopen(metric_url, timeout=1):
                    pass
            except OSError:
                pass
            else:
                raise AssertionError("metrics listener survived SIGTERM")
            for stream in [process.stdin, process.stdout, process.stderr]:
                stream.close()

            key = Path(root) / "state.key"
            key.write_bytes(os.urandom(32))
            key.chmod(0o600)
            state = Path(root) / "state.enc"
            extra = ["--transport", "streamable-http", "--listen", "127.0.0.1:0",
                     "--resource-url", "https://management.example/mcp",
                     "--oauth-redirect-uri", "https://client.example/callback",
                     "--state-file", str(state), "--state-key-file", str(key)]
            for _ in range(2):
                process = start(extra)
                metrics.read_line(process.stderr)
                notice = metrics.read_line(process.stderr)
                match = re.search(r"MCP listening on (127\.0\.0\.1:\d+)", notice)
                assert match, notice
                request = urllib.request.Request(
                    "http://" + match.group(1) + "/.well-known/oauth-authorization-server",
                    headers={"Host": "management.example"})
                with urllib.request.urlopen(request, timeout=5) as response:
                    assert json.load(response)["issuer"] == "https://management.example"
                terminate()
                for stream in [process.stdin, process.stdout, process.stderr]:
                    stream.close()
            assert state.exists(), "encrypted state was not retained"
            assert fixture.FixtureAPI.mutations == 0
    finally:
        if process is not None and process.poll() is None:
            process.kill()
            process.wait()
        backend.shutdown()
        backend.server_close()
    print(json.dumps({"sigterm": "passed", "native_read_cancellation": "passed",
                      "state_reopen": "passed", "metrics_cleanup": "passed", "deployment_writes": 0}))


if __name__ == "__main__":
    main()
