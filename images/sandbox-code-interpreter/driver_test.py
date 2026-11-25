# adr: 20261027090013 step 14 — a code context keeps its state between runs,
# reports stdout, stderr, rich results (a matplotlib chart as PNG) and errors,
# stops a run that passes its timeout, and contexts are created, listed and
# removed. Needs ipykernel, jupyter_client and matplotlib; skipped without.

from __future__ import annotations

import json
import os
import socket
import tempfile
import threading
import time
import unittest
from typing import Any, Dict, List

try:
    import ipykernel  # noqa: F401
    import jupyter_client  # noqa: F401
    import matplotlib  # noqa: F401

    HAVE_KERNEL = True
except ImportError:
    HAVE_KERNEL = False

if HAVE_KERNEL:
    import driver


@unittest.skipUnless(HAVE_KERNEL, "needs ipykernel, jupyter_client and matplotlib")
class DriverTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.dir = tempfile.mkdtemp()
        cls.sock = os.path.join(cls.dir, "run", "driver.sock")
        threading.Thread(target=driver.serve, args=(cls.sock, cls.dir), daemon=True).start()
        for _ in range(100):
            if os.path.exists(cls.sock):
                return
            time.sleep(0.05)
        raise RuntimeError("the driver did not listen")

    def call(self, req: Dict[str, Any]) -> List[Dict[str, Any]]:
        with socket.socket(socket.AF_UNIX) as s:
            s.connect(self.sock)
            s.sendall(json.dumps(req).encode() + b"\n")
            data = b""
            while chunk := s.recv(65536):
                data += chunk
        return [json.loads(line) for line in data.splitlines()]

    def run_code(self, code: str, **extra: Any) -> List[Dict[str, Any]]:
        return self.call({"op": "run", "code": code, "timeout_ms": 30_000, **extra})

    def test_state_output_results_and_errors(self) -> None:
        self.run_code("x = 40")
        events = self.run_code("import sys\nprint('hi')\nprint('warn', file=sys.stderr)\nx + 2")
        kinds = [e["type"] for e in events]
        self.assertIn({"type": "stdout", "text": "hi\n"}, events)
        self.assertIn({"type": "stderr", "text": "warn\n"}, events)
        main = [e for e in events if e["type"] == "result"]
        self.assertEqual(main, [{"type": "result", "data": {"text/plain": "42"}, "is_main_result": True}])
        self.assertEqual(kinds[-1], "done")
        self.assertIsInstance(events[-1]["execution_count"], int)

        err = self.run_code("1/0")
        error = [e for e in err if e["type"] == "error"]
        self.assertEqual(error[0]["name"], "ZeroDivisionError")
        self.assertIn("division by zero", error[0]["traceback"])

    def test_a_chart_is_a_png_result(self) -> None:
        events = self.run_code("import matplotlib.pyplot as plt\nplt.plot([1, 2, 3])\nplt.show()")
        pngs = [e for e in events if e["type"] == "result" and "image/png" in e["data"]]
        self.assertEqual(len(pngs), 1, events)
        self.assertFalse(pngs[0]["is_main_result"])

    def test_a_run_past_its_timeout_is_interrupted(self) -> None:
        started = time.monotonic()
        events = self.run_code("import time\ntime.sleep(60)", timeout_ms=1_000)
        self.assertTrue(events[-1]["timed_out"])
        self.assertLess(time.monotonic() - started, 15)
        self.assertEqual([e for e in self.run_code("1 + 1") if e["type"] == "result"][0]["data"]["text/plain"], "2")

    def test_output_over_the_budget_is_dropped(self) -> None:
        events = self.run_code("print('x' * 5000)", max_output_bytes=100)
        self.assertTrue(events[-1]["output_truncated"])
        self.assertFalse([e for e in events if e["type"] == "stdout"])

    def test_contexts_are_separate(self) -> None:
        created = self.call({"op": "create", "language": "python", "cwd": self.dir})[0]
        self.assertEqual(created["type"], "context")
        cid = created["id"]
        self.run_code("y = 'mine'", context=cid)
        other = self.run_code("'y' in dir()")
        self.assertEqual([e for e in other if e["type"] == "result"][0]["data"]["text/plain"], "False")
        listed = self.call({"op": "list"})[0]["contexts"]
        self.assertIn(cid, [c["id"] for c in listed])
        self.assertEqual(self.call({"op": "restart", "context": cid}), [{"type": "ok"}])
        gone = self.run_code("'y' in dir()", context=cid)
        self.assertEqual([e for e in gone if e["type"] == "result"][0]["data"]["text/plain"], "False")
        self.assertEqual(self.call({"op": "remove", "context": cid}), [{"type": "ok"}])
        self.assertEqual(self.run_code("1", context=cid)[0]["code"], "not_found")
        self.assertEqual(self.call({"op": "create", "language": "cobol"})[0]["code"], "unsupported")


    def test_an_error_after_the_budget_is_still_reported(self) -> None:
        events = self.run_code("print('x' * 5000)\nraise ValueError('bad')", max_output_bytes=100)
        self.assertTrue(events[-1]["output_truncated"])
        errors = [e for e in events if e["type"] == "error"]
        self.assertEqual(errors[0]["name"], "ValueError", events)

    def test_a_dead_kernel_ends_the_run_and_starts_again(self) -> None:
        cid = self.call({"op": "create", "language": "python", "cwd": self.dir})[0]["id"]
        started = time.monotonic()
        events = self.run_code("import os, signal\nos.kill(os.getpid(), signal.SIGKILL)", context=cid, timeout_ms=60_000)
        self.assertLess(time.monotonic() - started, 30)
        self.assertEqual([e["name"] for e in events if e["type"] == "error"], ["KernelDied"], events)
        again = self.run_code("1 + 1", context=cid)
        self.assertEqual([e for e in again if e["type"] == "result"][0]["data"]["text/plain"], "2")
        self.call({"op": "remove", "context": cid})

    def test_restart_cancels_a_run_in_progress(self) -> None:
        cid = self.call({"op": "create", "language": "python", "cwd": self.dir})[0]["id"]
        box: Dict[str, Any] = {}
        runner = threading.Thread(target=lambda: box.update(events=self.run_code("import time\ntime.sleep(300)", context=cid, timeout_ms=300_000)))
        runner.start()
        time.sleep(2)
        started = time.monotonic()
        self.assertEqual(self.call({"op": "restart", "context": cid}), [{"type": "ok"}])
        self.assertLess(time.monotonic() - started, 30)
        runner.join(30)
        self.assertIn("ContextRestarted", [e.get("name") for e in box["events"]])
        self.call({"op": "remove", "context": cid})

    def test_a_queued_run_keeps_its_timeout(self) -> None:
        cid = self.call({"op": "create", "language": "python", "cwd": self.dir})[0]["id"]
        runner = threading.Thread(target=lambda: self.run_code("import time\ntime.sleep(8)", context=cid, timeout_ms=30_000))
        runner.start()
        time.sleep(1)
        started = time.monotonic()
        events = self.run_code("print('late')", context=cid, timeout_ms=1_000)
        self.assertLess(time.monotonic() - started, 5)
        self.assertEqual(events, [{"type": "done", "execution_count": None, "timed_out": True, "output_truncated": False}])
        runner.join(30)
        self.call({"op": "remove", "context": cid})

    def test_a_run_whose_caller_left_is_interrupted(self) -> None:
        cid = self.call({"op": "create", "language": "python", "cwd": self.dir})[0]["id"]
        with socket.socket(socket.AF_UNIX) as s:
            s.connect(self.sock)
            s.sendall(json.dumps({"op": "run", "context": cid, "code": "while True: print(1)", "timeout_ms": 300_000}).encode() + b"\n")
            s.recv(1024)
        started = time.monotonic()
        events = self.run_code("2 + 2", context=cid, timeout_ms=30_000)
        self.assertLess(time.monotonic() - started, 20)
        self.assertEqual([e for e in events if e["type"] == "result"][0]["data"]["text/plain"], "4")
        self.call({"op": "remove", "context": cid})

    def test_session_env_follows_the_request(self) -> None:
        cid = self.call({"op": "create", "language": "python", "cwd": self.dir})[0]["id"]
        get = "import os\nos.environ.get('GREGALE_DRIVER_TEST')"
        first = self.run_code(get, context=cid, env={"GREGALE_DRIVER_TEST": "one"})
        self.assertEqual([e for e in first if e["type"] == "result"][0]["data"]["text/plain"], "'one'")
        cleared = self.run_code(get, context=cid, env={})
        self.assertFalse([e for e in cleared if e["type"] == "result"])
        self.call({"op": "remove", "context": cid})

    def test_the_context_cap_names_the_limit(self) -> None:
        events = self.call({"op": "create", "language": "python", "cwd": self.dir, "max_contexts": 1})
        self.assertEqual(events[0]["code"], "limit")
        self.assertIn("at most 1", events[0]["message"])
        pids = [f for f in os.listdir(os.path.dirname(self.sock)) if f.endswith(".pid")]
        self.assertIn("kernel-default.pid", pids)


if __name__ == "__main__":
    unittest.main()
