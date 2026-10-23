"""On-demand heap capture through the Python preload against a fake bridge (ADR-1155)."""
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


STUCK_APP = '''
import asyncio
import threading
import time

lock = threading.Lock()
lock.acquire()


def wait_for_lock():
    lock.acquire()


async def stuck_handler():
    await asyncio.sleep(60)


async def main():
    task = asyncio.ensure_future(stuck_handler())
    await asyncio.sleep(8)
    task.cancel()

threading.Thread(target=wait_for_lock, name="worker", daemon=True).start()
asyncio.run(main())
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

    def test_stacks_snapshot(self):
        dormant = dict(Bridge.control)
        self.addCleanup(lambda: setattr(Bridge, "control", dormant))
        self.addCleanup(lambda: (setattr(Bridge, "polls", []), setattr(Bridge, "uploads", [])))
        Bridge.polls, Bridge.uploads = [], []
        Bridge.control = {"enabled": False, "suspended": False, "epoch": "b" * 32, "window_seconds": 2}
        server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Bridge)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        self.addCleanup(server.shutdown)
        with tempfile.TemporaryDirectory() as directory:
            app = Path(directory, "app.py")
            app.write_text(STUCK_APP)
            env = dict(os.environ, FAAS_PROFILING_ENABLED="1",
                       FAAS_PROFILING_ENDPOINT="http://127.0.0.1:%d" % server.server_address[1],
                       PYTHONPATH=str(ROOT / "guest/profiling/python"))
            proc = subprocess.Popen([sys.executable, str(app)], env=env)
            self.addCleanup(proc.kill)
            # Arm the capture once the app is stuck, as a slow request would.
            time.sleep(2)
            Bridge.control = {"enabled": True, "suspended": False, "epoch": "a" * 32, "window_seconds": 2,
                              "kinds": ["stacks"], "capture": True}
            deadline = time.time() + 6
            while time.time() < deadline and not Bridge.uploads:
                time.sleep(0.1)
            time.sleep(1)  # a second upload would arrive within a poll
        self.assertEqual(len(Bridge.uploads), 1, "expected one snapshot per capture epoch")
        query, body = Bridge.uploads[0]
        self.assertEqual(query["kind"], ["stacks"])
        raw = gzip.decompress(body)
        self.assertIn(b"threads", raw)
        self.assertIn(b"wait_for_lock", raw, "blocked thread missing")
        self.assertIn(b"<thread>", raw)
        self.assertIn(b"stuck_handler", raw, "pending asyncio task missing")
        self.assertIn(b"<asyncio task>", raw)
        self.assertNotIn(b"_control_loop", raw, "the collector's own thread must be excluded")

    def test_capture_checkpoints_heap(self):
        dormant = dict(Bridge.control)
        self.addCleanup(lambda: setattr(Bridge, "control", dormant))
        self.addCleanup(lambda: (setattr(Bridge, "polls", []), setattr(Bridge, "uploads", [])))
        Bridge.polls, Bridge.uploads = [], []
        Bridge.control = {"enabled": True, "suspended": False, "epoch": "e" * 32, "window_seconds": 10,
                          "kinds": ["heap"], "capture": True, "checkpoint_seconds": 1}
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
            time.sleep(6)
            # The process dies mid-window like an OOM kill: only checkpoints remain.
            proc.kill()
            proc.wait()
        checkpoints = [(q, b) for q, b in Bridge.uploads if q.get("checkpoint") == ["1"]]
        self.assertGreaterEqual(len(checkpoints), 2, "expected about one checkpoint per second")
        self.assertEqual(len(checkpoints), len(Bridge.uploads), "a killed process must not have sent a final heap")
        query, body = checkpoints[-1]
        self.assertEqual(query["kind"], ["heap"])
        self.assertIn(b"Cache.grow", gzip.decompress(body))


if __name__ == "__main__":
    unittest.main()
