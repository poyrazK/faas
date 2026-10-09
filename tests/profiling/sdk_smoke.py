"""Capture real SDK output through the local bridge contract, without KVM.

Usage: python tests/profiling/sdk_smoke.py --python /path/to/python --output /tmp/profiles
Install guest/profiling/node and the pinned Python requirements first.
"""
import argparse
import json
import os
from pathlib import Path
import subprocess
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlsplit

ROOT = Path(__file__).resolve().parents[2]
EPOCH = "1" * 32


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--python", required=True)
    parser.add_argument("--node-image")
    parser.add_argument("--python-image")
    parser.add_argument("--fork", action="store_true", help="Exercise Python pre-fork worker startup")
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=True)
    captured = []

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass

        def do_GET(self):
            self.send_response(200)
            self.end_headers()
            self.wfile.write(json.dumps(dict(enabled=True, suspended=False, epoch=EPOCH, window_seconds=1)).encode())

        def do_POST(self):
            data = self.rfile.read(int(self.headers.get("Content-Length", "0")))
            if urlsplit(self.path).path != "/control/ack":
                query=parse_qs(urlsplit(self.path).query);query["capture_path"]=[urlsplit(self.path).path];captured.append((query, dict(self.headers), data))
            self.send_response(204)
            self.end_headers()

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    env = dict(os.environ, FAAS_PROFILING_ENABLED="1", FAAS_PROFILING_ENDPOINT=f"http://127.0.0.1:{server.server_port}")
    env["NODE_PATH"] = str(ROOT / "guest/profiling/node/node_modules")
    env["PYTHONPATH"] = os.getenv("GREGALE_PROFILE_PYTHON_BOOTSTRAP", str(ROOT / "guest/profiling/python"))
    programs = {
        "node": ["node", "--require", os.getenv("GREGALE_PROFILE_NODE_BOOTSTRAP", str(ROOT / "guest/profiling/node.cjs")), "-e", "let x=0;function hot(){const end=Date.now()+20;while(Date.now()<end){x=Math.sqrt(x+Math.random())}}const work=setInterval(hot,25);setTimeout(()=>{clearInterval(work);process.exit(0)},6000)"],
        "python": [args.python, "-c", "import time;time.sleep(.5)\ndef hot():\n x=0\n for i in range(100000):x+=i*i\n return x\nend=time.monotonic()+15\nwhile time.monotonic()<end:hot()\nimport pyroscope;pyroscope.shutdown()"],
    }
    if args.fork:
        programs["python"] = [args.python, "-c", "import os,time;time.sleep(2);pid=os.fork()\ndef hot():\n x=0\n for i in range(100000):x+=i*i\n return x\nend=time.monotonic()+8\nwhile time.monotonic()<end:hot()\nimport pyroscope;pyroscope.shutdown()\nif pid==0:os._exit(0)\nos.waitpid(pid,0)"]
    for runtime, image in [("node", args.node_image), ("python", args.python_image)]:
        if not image:
            continue
        args_inside = programs[runtime][1:]
        extra_env = []
        if runtime == "node":
            args_inside[1] = "/opt/gregale/profiling/node.cjs"
        else:
            extra_env = ["-e", "PYTHONPATH=/opt/gregale/profiling/python"]
        programs[runtime] = ["docker", "run", "--rm", "--user", "1000:1000", "--network=host", "-e", "FAAS_PROFILING_ENABLED=1", "-e", "FAAS_PROFILING_ENDPOINT=" + env["FAAS_PROFILING_ENDPOINT"], *extra_env, "--entrypoint", runtime, image, *args_inside]
    for runtime, command in programs.items():
        captured.clear()
        subprocess.run(command, env=env, check=True, timeout=30)
        assert captured, f"{runtime}: no SDK profiles received"
        for index, (query, headers, data) in enumerate(captured):
            (args.output / f"{runtime}-{index}.bin").write_bytes(data)
            (args.output / f"{runtime}-{index}.json").write_text(json.dumps(dict(query=query, headers=headers)))
        print(f"{runtime}: captured {len(captured)} uploads")
    server.shutdown()


if __name__ == "__main__":
    main()
