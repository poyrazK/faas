#!/usr/bin/env python3
"""Check private CLI metrics and shutdown against an isolated API fixture."""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import re
import selectors
import subprocess
import tempfile
import threading
import urllib.request
from http.server import ThreadingHTTPServer


def read_line(stream):
    with selectors.DefaultSelector() as selector:
        selector.register(stream, selectors.EVENT_READ)
        if not selector.select(10):
            raise RuntimeError("CLI output timed out")
    value = stream.readline()
    if not value:
        raise RuntimeError("CLI ended early")
    return value


def check_stdio(binary, fixture, backend, root):
    env = dict(os.environ, HOME=root, XDG_CONFIG_HOME=root,
               FAAS_API=f"http://127.0.0.1:{backend.server_port}",
               FAAS_TOKEN="fp_test_codex_acceptance_fixture")
    process = subprocess.Popen(
        [binary, "--non-interactive", "mcp", "serve", "--transport", "stdio",
         "--metrics-listen", "127.0.0.1:0"],
        stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        text=True, env=env)

    def send(message):
        process.stdin.write(json.dumps(message) + "\n")
        process.stdin.flush()

    def rpc(request_id, method, params):
        send({"jsonrpc": "2.0", "id": request_id, "method": method, "params": params})
        response = json.loads(read_line(process.stdout))
        assert response["id"] == request_id, response
        return response

    try:
        notice = read_line(process.stderr)
        match = re.search(r"metrics listening on (127\.0\.0\.1:\d+)", notice)
        assert match, notice
        endpoint = "http://" + match.group(1) + "/metrics"
        initial = rpc(1, "initialize", {
            "protocolVersion": "2026-07-28", "capabilities": {},
            "clientInfo": {"name": "metrics-smoke", "version": "1"}})
        assert "result" in initial, initial
        send({"jsonrpc": "2.0", "method": "notifications/initialized"})
        result = rpc(2, "tools/call", {"name": "gregale_get_app", "arguments": {"app": "alpha"}})
        assert not result.get("result", {}).get("isError"), result
        private = "private_customer_identifier_123"
        rpc(3, "tools/call", {"name": private, "arguments": {"token": private}})
        with urllib.request.urlopen(endpoint, timeout=5) as response:
            text = response.read().decode()
        assert 'outcome="success",tool="gregale_get_app"} 1' in text, text
        assert 'outcome="error",tool="unknown"} 1' in text, text
        assert 'gregale_management_mcp_active{kind="short_requests"} 0' in text, text
        assert all(value not in text for value in [private, env["FAAS_TOKEN"], "fixture-alpha"])
        process.stdin.close()
        assert process.wait(timeout=10) == 0
        try:
            with urllib.request.urlopen(endpoint, timeout=1):
                pass
        except OSError:
            pass
        else:
            raise AssertionError("metrics listener survived CLI shutdown")
        assert fixture.FixtureAPI.mutations == 0
    finally:
        if process.poll() is None:
            process.kill()
            process.wait()
        process.stdout.close()
        process.stderr.close()
        if not process.stdin.closed:
            process.stdin.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--gregale-binary", required=True)
    args = parser.parse_args()
    for address in ["0.0.0.0:8788", "192.0.2.1:8788", "localhost:8788",
                    "127.0.0.1:65536", "127.0.0.1:-1"]:
        result = subprocess.run(
            [args.gregale_binary, "--non-interactive", "mcp", "serve", "--metrics-listen", address],
            capture_output=True, text=True, timeout=10)
        assert result.returncode != 0 and "--metrics-listen requires" in result.stderr
    spec = importlib.util.spec_from_file_location(
        "fixture", Path(__file__).with_name("management-mcp-client-acceptance.py"))
    fixture = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(fixture)
    backend = ThreadingHTTPServer(("127.0.0.1", 0), fixture.FixtureAPI)
    threading.Thread(target=backend.serve_forever, daemon=True).start()
    try:
        with tempfile.TemporaryDirectory(prefix="gregale-telemetry-cli-") as root:
            check_stdio(args.gregale_binary, fixture, backend, root)
    finally:
        backend.shutdown()
        backend.server_close()
    print(json.dumps({"cli_metrics": "passed", "loopback_validation": "passed",
                      "privacy": "passed", "shutdown": "passed", "deployment_writes": 0}))


if __name__ == "__main__":
    main()
