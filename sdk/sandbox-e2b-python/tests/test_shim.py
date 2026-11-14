# adr: 20261027090013 §12 — E2B-style Python code runs on Gregale by changing
# the import and the API key: Sandbox.create/connect/list/kill, commands in
# the foreground (streamed when handlers are given) and background, files in
# and outside /workspace, ports and their preview tokens; unsupported calls
# fail with an error naming the call and the alternative.

from __future__ import annotations

import base64
import io
import json
import time
import unittest
from datetime import datetime, timedelta, timezone
from typing import Any, Dict, List, Optional
from urllib.parse import parse_qs, urlsplit

from gregale_sandbox_e2b import (
    CommandExitException,
    NotFoundException,
    Sandbox,
    SandboxNotFoundException,
    SandboxQuery,
    SandboxState,
    UnsupportedException,
    WriteEntry,
)
from gregale_sandbox_e2b import commands as commands_module

API = "https://api.gregale.test"
ID = "0b0e6a1c-2f6a-4c8e-9d43-6f3f1d2a9b10"
MISSING = "11111111-0000-0000-0000-000000000000"


class FakeResponse:
    def __init__(self, status: int, body: bytes = b"", content_type: str = "application/json") -> None:
        self.status = status
        self.headers = {"Content-Type": content_type}
        self._body = io.BytesIO(body)

    def read(self, amt: Optional[int] = None) -> bytes:
        return self._body.read() if amt is None else self._body.read(amt)

    def __iter__(self):
        return iter(self._body.readlines())

    def close(self) -> None:
        pass


def js(status: int, body: Any) -> FakeResponse:
    return FakeResponse(status, json.dumps(body).encode())


class FakeGregale:
    def __init__(self) -> None:
        self.state = "queued"
        self.created: Optional[Dict[str, Any]] = None
        self.patched: Optional[Dict[str, Any]] = None
        self.deleted = 0
        self.execs: List[Dict[str, Any]] = []
        self.files: Dict[str, bytes] = {}
        self.ports: List[Dict[str, Any]] = []
        self.background: Dict[str, Dict[str, Any]] = {}

    def view(self) -> Dict[str, Any]:
        v: Dict[str, Any] = {
            "id": ID,
            "state": self.state,
            "preset": "codex",
            "ram_mb": 2048,
            "network": {"mode": "registries"},
            "idle_timeout_s": 300,
            "expires_at": "2026-11-05T04:00:00.123456789Z",
            "created_at": "2026-11-05T00:00:00Z",
            "labels": {"team": "evals"},
        }
        if self.state != "closed":
            v["token"] = "sbx-1"
            v["token_expires_at"] = (datetime.now(timezone.utc) + timedelta(minutes=15)).isoformat().replace("+00:00", "Z")
        return v

    def __call__(self, method: str, url: str, headers: Dict[str, str], body: Optional[bytes], timeout: Optional[float]) -> FakeResponse:
        parts = urlsplit(url)
        assert url.startswith(API)
        query = {k: v[0] for k, v in parse_qs(parts.query).items()}
        path = parts.path
        auth = headers.get("Authorization")
        base = f"/v1/sandboxes/{ID}"
        text = (body or b"").decode() if headers.get("Content-Type") == "application/json" else ""
        if auth == "Bearer key-1":
            if method == "POST" and path == "/v1/sandboxes":
                self.created = json.loads(text)
                return js(201, self.view())
            if method == "GET" and path == "/v1/sandboxes":
                return js(200, {"items": [self.view()], "next_before": ""})
            if path == f"/v1/sandboxes/{MISSING}":
                return js(404, {"code": "sandbox_not_found", "status": 404})
            if method == "GET" and path == base:
                if self.state == "queued":
                    self.state = "ready"
                return js(200, self.view())
            if method == "PATCH" and path == base:
                self.patched = json.loads(text)
                return js(200, self.view())
            if method == "DELETE" and path == base:
                if self.state == "closed":
                    return js(404, {"code": "sandbox_not_found", "status": 404})
                self.deleted += 1
                self.state = "closed"
                return js(200, self.view())
            if method == "POST" and path == f"{base}/ports":
                req = json.loads(text)
                self.ports.append(req)
                view = {"port": req["port"], "url": f"https://{req['port']}-{ID}.preview.test"}
                if req["visibility"] == "private":
                    view["preview_token"] = "sbxp1.tok"
                return js(201, view)
            if method == "GET" and path == f"{base}/ports":
                return js(200, {"items": [{"port": 8080, "url": f"https://8080-{ID}.preview.test"}]})
        if auth != "Bearer sbx-1" or not path.startswith(base):
            return FakeResponse(404, b"404 page not found", "text/plain")
        sub = path[len(base):]
        rel = query.get("path", "")
        if method == "POST" and sub == "/exec":
            req = json.loads(text)
            self.execs.append(req)
            script = req["cmd"][-1]
            if req.get("background"):
                handle = f"c{len(self.background) + 1}"
                self.background[handle] = {"out": "serving\n", "polls": 0, "finish_after": 100 if "server" in script else 1, "exit": 0}
                return js(202, {"handle": handle, "exit_code": 0, "wall_time_ms": 0})
            code = 1 if "false" in script else 0
            if "base64 < " in script:
                return js(200, {"stdout": base64.b64encode(b"outside").decode() + "\n", "stderr": "", "exit_code": 0, "wall_time_s": 0})
            if headers.get("Accept") == "text/event-stream":
                frames = 'event: stdout\ndata: "line 1\\n"\n\nevent: stderr\ndata: "warn\\n"\n\n' + f'event: result\ndata: {{"exit_code":{code},"stdout":"","stderr":"","wall_time_s":0.1}}\n\n'
                return FakeResponse(200, frames.encode(), "text/event-stream")
            return js(200, {"stdout": f"out:{script}", "stderr": "boom" if code else "", "exit_code": code, "wall_time_s": 0.1})
        if method == "GET" and sub == "/exec":
            return js(200, {"commands": [{"handle": h, "argv": ["/bin/bash", "-l", "-c", "npm run server"], "running": c["polls"] < c["finish_after"] and not c.get("killed")} for h, c in self.background.items()]})
        if sub.startswith("/exec/"):
            handle = sub[len("/exec/"):]
            c = self.background.get(handle)
            if c is None:
                return js(404, {"code": "sandbox_entry_not_found", "status": 404})
            if method == "DELETE":
                c["killed"], c["exit"] = True, 137
                return js(200, {})
            c["polls"] += 1
            offset = int(query.get("stdout_offset", "0"))
            return js(200, {
                "handle": handle,
                "running": c["polls"] < c["finish_after"] and not c.get("killed"),
                "exit_code": c["exit"],
                "stdout": base64.b64encode(c["out"][offset:].encode()).decode(),
                "stderr": None,
                "stdout_offset": len(c["out"]),
                "stderr_offset": 0,
            })
        key = f"{method} {sub}"
        if key == "GET /files":
            return FakeResponse(200, self.files[rel], "application/octet-stream") if rel in self.files else js(404, {"code": "sandbox_entry_not_found", "status": 404})
        if key == "PUT /files":
            self.files[rel] = body or b""
            return js(200, {"bytes": len(body or b"")})
        if key == "GET /files:stat":
            if rel not in self.files:
                return js(404, {"code": "sandbox_entry_not_found", "status": 404})
            return js(200, {"path": rel, "type": "file", "size": len(self.files[rel]), "mode": 0o644, "mtime_unix_ms": 0})
        if key == "GET /files:list":
            return js(200, {"entries": [{"path": p, "type": "file", "size": 1, "mode": 0o644, "mtime_unix_ms": 0} for p in self.files]})
        if key == "POST /files:mkdir":
            return js(200, {})
        if key == "POST /files:rename":
            req = json.loads(text)
            self.files[req["to"]] = self.files.pop(req["from"])
            return js(200, {})
        if key == "POST /files:remove":
            self.files.pop(json.loads(text)["path"], None)
            return js(200, {})
        if key == "POST /workspace:persist":
            return js(200, {"snapshot_id": "snap-1"})
        return js(404, {"code": "not_found"})


class ShimTest(unittest.TestCase):
    def setUp(self) -> None:
        self.fake = FakeGregale()
        self.opts = {"api_key": "key-1", "api_base": API, "transport": self.fake}
        commands_module.POLL_S = 0
        time_sleep = time.sleep
        self.addCleanup(setattr, time, "sleep", time_sleep)
        time.sleep = lambda s: None  # the fake starts queued; skip the backoff

    def test_create_maps_e2b_options_and_waits_for_the_vm(self) -> None:
        sbx = Sandbox.create("base", timeout=120.5, metadata={"team": "evals"}, envs={"CI": "1"}, allow_internet_access=False, ports=[3000], **self.opts)
        self.assertEqual(sbx.sandbox_id, ID)
        self.assertEqual(self.fake.created, {"preset": "codex", "network": {"mode": "none"}, "idle_timeout_s": 121, "labels": {"team": "evals"}})
        self.assertEqual(sbx.get_host(3000), f"3000-{ID}.preview.test")
        self.assertEqual(sbx.preview_token(3000), "sbxp1.tok")
        with self.assertRaisesRegex(UnsupportedException, r"expose_port\(5173\)"):
            sbx.get_host(5173)
        sbx.commands.run("env")
        self.assertEqual(self.fake.execs[-1]["env"], {"CI": "1"})
        custom = Sandbox("my-template", **self.opts)
        self.assertEqual(custom.sandbox_id, ID)
        self.assertEqual(self.fake.created, {"template": "my-template"})

    def test_foreground_commands_answer_stream_and_raise(self) -> None:
        sbx = Sandbox.create(**self.opts)
        res = sbx.commands.run("ls -la", cwd="/workspace/app", user="root", timeout=5, envs={"A": "b"})
        self.assertEqual((res.stdout, res.exit_code), ("out:ls -la", 0))
        self.assertEqual(self.fake.execs[-1], {"cmd": ["/bin/bash", "-l", "-c", "ls -la"], "workdir": "/workspace/app", "env": {"A": "b"}, "run_as": "root", "timeout_ms": 5000})
        seen: List[str] = []
        streamed = sbx.commands.run("make", on_stdout=lambda d: seen.append(f"o:{d}"), on_stderr=lambda d: seen.append(f"e:{d}"))
        self.assertEqual(seen, ["o:line 1\n", "e:warn\n"])
        self.assertEqual(streamed.stdout, "line 1\n")
        with self.assertRaises(CommandExitException) as failed:
            sbx.commands.run("false")
        self.assertEqual((failed.exception.exit_code, failed.exception.stderr), (1, "boom"))
        with self.assertRaises(UnsupportedException):
            sbx.commands.run("id", user="nobody")
        with self.assertRaises(UnsupportedException):
            sbx.commands.run("cat", stdin=True)

    def test_background_commands_give_a_pid_that_list_kill_and_wait_use(self) -> None:
        sbx = Sandbox.create(**self.opts)
        quick = sbx.commands.run("echo hi", background=True)
        self.assertEqual(quick.wait().stdout, "serving\n")
        server = sbx.commands.run("npm run server", background=True)
        self.assertEqual(server.pid, 2)
        self.assertEqual([p.pid for p in sbx.commands.list()], [2])
        self.assertTrue(sbx.commands.kill(server.pid))
        with self.assertRaises(CommandExitException) as killed:
            server.wait()
        self.assertEqual(killed.exception.exit_code, 137)
        self.assertFalse(sbx.commands.kill(99))

    def test_files_inside_use_the_api_and_outside_go_through_commands(self) -> None:
        sbx = Sandbox.create(**self.opts)
        info = sbx.files.write("notes/a.txt", "hello")
        self.assertEqual((info.name, info.path, info.type), ("a.txt", "/workspace/notes/a.txt", "file"))
        self.assertEqual(sbx.files.read("/workspace/notes/a.txt"), "hello")
        self.assertEqual(sbx.files.read("notes/a.txt", format="bytes"), b"hello")
        self.assertEqual(b"".join(sbx.files.read("notes/a.txt", format="stream")), b"hello")
        self.assertTrue(sbx.files.exists("notes/a.txt"))
        self.assertFalse(sbx.files.exists("notes/missing"))
        self.assertEqual(sbx.files.get_info("notes/a.txt").permissions, "-rw-r--r--")
        self.assertEqual([e.path for e in sbx.files.list("/workspace")], ["/workspace/notes/a.txt"])
        sbx.files.rename("notes/a.txt", "notes/b.txt")
        self.assertIn("notes/b.txt", self.fake.files)
        sbx.files.remove("notes/b.txt")
        self.assertEqual(self.fake.files, {})
        sbx.files.write_files([WriteEntry(path="x/1", data=b"1"), WriteEntry(path="x/2", data=io.BytesIO(b"2"))])
        self.assertEqual(self.fake.files, {"x/1": b"1", "x/2": b"2"})
        with self.assertRaises(NotFoundException):
            sbx.files.read("/workspace/missing")
        self.assertEqual(sbx.files.read("/home/user/outside.txt"), "outside")
        sbx.files.write("/home/user/x.txt", "abc")
        self.assertRegex(self.fake.execs[-1]["cmd"][-1], r"base64 -d > '/home/user/x.txt'")
        with self.assertRaises(UnsupportedException):
            sbx.files.write("/tmp/big", bytes(300 << 10))
        with self.assertRaises(UnsupportedException):
            sbx.files.list("/etc")
        with self.assertRaises(UnsupportedException):
            sbx.files.watch_dir("/workspace", lambda e: None)

    def test_connect_list_set_timeout_and_kill(self) -> None:
        first = Sandbox.create(**self.opts)
        again = Sandbox.connect(first.sandbox_id, **self.opts)
        self.assertEqual(again.get_host(8080), f"8080-{ID}.preview.test")
        page = Sandbox.list(query=SandboxQuery(metadata={"team": "evals"}), **self.opts)
        items = page.next_items()
        self.assertEqual([(i.sandbox_id, i.state, i.memory_mb, i.metadata["team"]) for i in items], [(ID, SandboxState.RUNNING, 2048, "evals")])
        self.assertFalse(page.has_next)
        self.assertEqual(items[0].end_at, datetime(2026, 11, 5, 4, 0, 0, 123456, tzinfo=timezone.utc))
        self.assertEqual(Sandbox.get_info(ID, **self.opts).template_id, "codex")
        self.assertEqual(again.create_snapshot().snapshot_id, "snap-1")
        again.set_timeout(60)
        self.assertEqual(self.fake.patched, {"idle_timeout_s": 60})
        Sandbox.set_timeout(ID, 90, **self.opts)
        self.assertEqual(self.fake.patched, {"idle_timeout_s": 90})
        self.assertTrue(again.is_running())
        self.assertTrue(again.kill())
        self.assertEqual(self.fake.deleted, 1)
        self.assertFalse(again.is_running())
        self.assertFalse(Sandbox.kill(ID, **self.opts))
        with self.assertRaises(SandboxNotFoundException):
            Sandbox.connect(MISSING, **self.opts)
        with self.assertRaisesRegex(Exception, "not a Gregale sandbox ID"):
            Sandbox.connect("../../v1/keys", **self.opts)

    def test_context_manager_closes_the_session(self) -> None:
        with Sandbox.create(**self.opts) as sbx:
            sbx.commands.run("true")
        self.assertEqual(self.fake.deleted, 1)

    def test_unsupported_calls_name_the_call_and_the_alternative(self) -> None:
        sbx = Sandbox.create(**self.opts)
        with self.assertRaisesRegex(UnsupportedException, "freeze on their own"):
            sbx.beta_pause()
        with self.assertRaises(UnsupportedException) as pty:
            sbx.pty.create()
        self.assertEqual(pty.exception.call, "pty.create")
        with self.assertRaises(UnsupportedException):
            sbx.commands.send_stdin(1, "x")


if __name__ == "__main__":
    unittest.main()
