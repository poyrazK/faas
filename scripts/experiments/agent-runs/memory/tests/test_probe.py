import http.client
import json
import os
import subprocess
import sys
import threading
import time
import unittest
from http.server import ThreadingHTTPServer
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE.parent))

import agent_probe  # noqa: E402

BASH_TOOL = {"name": "Bash", "description": "run", "input_schema": {
    "type": "object", "properties": {"command": {"type": "string"}, "description": {"type": "string"}}}}
CODEX_SHELL = {"type": "function", "name": "shell", "parameters": {
    "type": "object", "properties": {"command": {"type": "array", "items": {"type": "string"}}}}}


def parse_sse(raw: str) -> list[dict]:
    events = []
    for block in raw.split("\n\n"):
        for line in block.splitlines():
            if line.startswith("data: ") and line != "data: [DONE]":
                events.append(json.loads(line[6:]))
    return events


class ProviderTest(unittest.TestCase):
    def setUp(self) -> None:
        self.p = agent_probe.Provider()
        self.srv = ThreadingHTTPServer(("127.0.0.1", 0), agent_probe.provider_handler(self.p))
        self.srv.daemon_threads = True
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()
        self.port = self.srv.server_address[1]
        self.p.configure(agent_probe.load_script("s1"), 0, [], 30)

    def tearDown(self) -> None:
        self.srv.shutdown()

    def post(self, path: str, body: dict) -> str:
        c = http.client.HTTPConnection("127.0.0.1", self.port, timeout=30)
        c.request("POST", path, json.dumps(body), {"content-type": "application/json"})
        r = c.getresponse()
        return r.read().decode()

    def anthropic_loop(self) -> tuple[str, list[str]]:
        """A minimal Claude-Code-like loop: run each tool call, send its result back."""
        messages = [{"role": "user", "content": "go"}]
        commands = []
        for _ in range(10):
            events = parse_sse(self.post("/v1/messages", {"model": "m", "stream": True, "tools": [BASH_TOOL],
                                                         "messages": messages}))
            types = [e["type"] for e in events]
            self.assertEqual(types[0], "message_start")
            self.assertEqual(types[-1], "message_stop")
            start = next(e for e in events if e["type"] == "content_block_start")["content_block"]
            stop = next(e for e in events if e["type"] == "message_delta")["delta"]["stop_reason"]
            if stop == "end_turn":
                return next(e for e in events if e["type"] == "content_block_delta")["delta"]["text"], commands
            delta = next(e for e in events if e["type"] == "content_block_delta")["delta"]["partial_json"]
            cmd = json.loads(delta)["command"]
            commands.append(cmd)
            out = cmd.split("&&")[0].replace("echo", "").strip()
            messages += [{"role": "assistant", "content": [dict(start, input=json.loads(delta))]},
                         {"role": "user", "content": [{"type": "tool_result", "tool_use_id": start["id"],
                                                       "content": out}]}]
        self.fail("loop did not end")

    def test_anthropic_script_followed(self) -> None:
        text, commands = self.anthropic_loop()
        self.assertIn(agent_probe.DONE_MARK, text)
        self.assertEqual(len(commands), 3)
        snap = self.p.snapshot()
        self.assertEqual([e["turn"] for e in snap["timeline"]], [1, 2, 3, 4])
        self.assertTrue(all(e["prev_tool_ok"] in (True, None) for e in snap["timeline"]))

    def test_side_request_not_counted(self) -> None:
        events = parse_sse(self.post("/v1/messages", {"model": "haiku", "stream": True,
                                                      "messages": [{"role": "user", "content": "title"}]}))
        self.assertEqual(events[-1]["type"], "message_stop")
        self.assertEqual(self.p.snapshot()["calls"], 0)
        self.assertEqual(self.p.snapshot()["side_requests"], 1)

    def test_responses_function_call_uses_array_schema(self) -> None:
        events = parse_sse(self.post("/v1/responses", {"model": "gpt", "stream": True, "tools": [CODEX_SHELL],
                                                       "input": [{"type": "message", "role": "user",
                                                                  "content": "go"}]}))
        self.assertEqual(events[0]["type"], "response.created")
        self.assertEqual(events[-1]["type"], "response.completed")
        item = next(e for e in events if e["type"] == "response.output_item.done")["item"]
        self.assertEqual(item["type"], "function_call")
        self.assertEqual(json.loads(item["arguments"])["command"][:2], ["bash", "-lc"])
        # A finished tool call moves the script to turn 2.
        events = parse_sse(self.post("/v1/responses", {"model": "gpt", "stream": True, "tools": [CODEX_SHELL],
                                                       "input": [{"type": "function_call_output",
                                                                  "call_id": item["call_id"],
                                                                  "output": "MARK-1"}]}))
        self.assertEqual(self.p.snapshot()["timeline"][-1]["turn"], 2)
        self.assertTrue(self.p.snapshot()["timeline"][-1]["prev_tool_ok"])

    def test_manual_hold_waits_for_release(self) -> None:
        self.p.configure(agent_probe.load_script("s1"), 0, [1], 30)
        done = threading.Event()

        def call() -> None:
            self.post("/v1/messages", {"model": "m", "stream": True, "tools": [BASH_TOOL],
                                       "messages": [{"role": "user", "content": "go"}]})
            done.set()

        threading.Thread(target=call, daemon=True).start()
        deadline = time.time() + 5
        while self.p.snapshot()["held"] == 0 and time.time() < deadline:
            time.sleep(0.02)
        self.assertEqual(self.p.snapshot()["held"], 1)
        time.sleep(0.3)
        self.assertFalse(done.is_set())
        self.p.do_release()
        self.assertTrue(done.wait(5))
        entry = self.p.snapshot()["timeline"][0]
        self.assertTrue(entry["manual"])
        self.assertGreaterEqual(entry["released"] - entry["hold_start"], 0.3)
        self.assertGreaterEqual(entry["sent"], entry["released"])

    def test_shell_args_follow_schema(self) -> None:
        self.assertEqual(agent_probe.shell_args({"properties": {"cmd": {}}}, "ls"), {"cmd": "ls"})
        self.assertEqual(agent_probe.shell_args({"properties": {"command": {"type": "string"},
                                                                "description": {}}}, "ls"),
                         {"command": "ls", "description": "experiment step"})


class ScriptsTest(unittest.TestCase):
    def test_scripts_end_with_done_text(self) -> None:
        for name in ("s1", "s2", "warm"):
            turns = agent_probe.load_script(name)
            self.assertIn(agent_probe.DONE_MARK, turns[-1]["text"])
            self.assertTrue(all("mark" in t and t["mark"] in t["command"] for t in turns[:-1]))

    def test_probe_starts_and_answers(self) -> None:
        env = dict(os.environ, AGENT_PROBE_ROOT=str(HERE / ".tmp-root"))
        proc = subprocess.Popen([sys.executable, str(HERE.parent / "agent_probe.py"), "--port", "18981",
                                 "--provider-port", "18982"], env=env)
        try:
            deadline = time.time() + 10
            while time.time() < deadline:
                try:
                    c = http.client.HTTPConnection("127.0.0.1", 18981, timeout=2)
                    c.request("GET", "/status")
                    st = json.loads(c.getresponse().read())
                    break
                except OSError:
                    time.sleep(0.1)
            else:
                self.fail("probe did not start")
            self.assertEqual(st["phase"], "idle")
        finally:
            proc.terminate()
            proc.wait(5)


if __name__ == "__main__":
    unittest.main()
