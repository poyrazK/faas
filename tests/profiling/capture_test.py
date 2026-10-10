"""On-demand heap capture through the Python preload against a fake bridge (ADR-967)."""
import gzip
import http.server
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import threading
import time
import unittest
import urllib.parse

ROOT = Path(__file__).resolve().parents[2]
EPOCH = "c" * 32

APP = '''
import time

class Cache:
    def __init__(self):
        self.items = []

    def grow(self):
        self.items.append(bytearray(4096))

cache = Cache()
deadline = time.time() + 8
while time.time() < deadline:
    cache.grow()
    time.sleep(0.001)
'''


class Bridge(http.server.BaseHTTPRequestHandler):
    control = {"enabled": False, "suspended": False, "epoch": "d" * 32, "window_seconds": 2}
    polls = []
    uploads = []

    def log_message(self, *args):
        pass

    def do_GET(self):
        Bridge.polls.append(time.monotonic())
        body = json.dumps(Bridge.control).encode()
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_POST(self):
        length = int(self.headers.get("Content-Length", "0"))
        query = urllib.parse.parse_qs(urllib.parse.urlparse(self.path).query)
        Bridge.uploads.append((query, self.rfile.read(length)))
        self.send_response(204)
        self.end_headers()


class CaptureTests(unittest.TestCase):
    def test_dormant_then_heap_capture(self):
        server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Bridge)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        self.addCleanup(server.shutdown)
        with tempfile.TemporaryDirectory() as directory:
            app = Path(directory, "app.py")
            app.write_text(APP)
            env = dict(os.environ, FAAS_PROFILING_ENABLED="1",
                       FAAS_PROFILING_ENDPOINT="http://127.0.0.1:%d" % server.server_address[1],
                       PYTHONPATH=str(ROOT / "guest/profiling/python"))
            proc = subprocess.Popen([sys.executable, str(app)], env=env)
            self.addCleanup(proc.kill)
            time.sleep(2.5)
            dormant = list(Bridge.polls)
            self.assertGreaterEqual(len(dormant), 2)
            self.assertLessEqual(len(dormant), 4, "dormant collector polled faster than 1/s")
            Bridge.control = {"enabled": True, "suspended": False, "epoch": EPOCH, "window_seconds": 2,
                              "kinds": ["heap"], "capture": True}
            time.sleep(2)
            Bridge.control = dict(Bridge.control, enabled=False)
            deadline = time.time() + 4
            while time.time() < deadline and not Bridge.uploads:
                time.sleep(0.1)
        self.assertEqual(len(Bridge.uploads), 1)
        query, body = Bridge.uploads[0]
        self.assertEqual(query["kind"], ["heap"])
        self.assertIn("gregale_epoch=" + EPOCH, query["name"][0])
        raw = gzip.decompress(body)
        self.assertIn(b"inuse_space", raw)
        self.assertIn(b"Cache.grow", raw, "allocation site lost its function name")


if __name__ == "__main__":
    unittest.main()
