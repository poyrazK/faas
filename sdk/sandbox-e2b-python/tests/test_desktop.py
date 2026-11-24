# adr: 20261124075005 §6 — E2B Desktop-style Python code drives a Gregale
# desktop: Desktop.create asks for preset desktop and its resolution, every
# action runs gregale-desktop as an argv with DISPLAY set (never shell text),
# a screenshot comes back as PNG bytes, and the live view is the noVNC page
# of a private preview port with its token, view-only on its own port.

from __future__ import annotations

import base64
import io
import json
import unittest
from datetime import datetime, timedelta, timezone
from typing import Any, Dict, List, Optional
from urllib.parse import parse_qs, urlsplit

from gregale_sandbox_e2b import CommandExitException, Desktop, InvalidArgumentException

API = "https://api.gregale.test"
ID = "0b0e6a1c-2f6a-4c8e-9d43-6f3f1d2a9b10"
PNG = bytes([0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 1, 2, 3])


class FakeResponse:
    def __init__(self, status: int, body: bytes = b"") -> None:
        self.status = status
        self.headers = {"Content-Type": "application/json"}
        self._body = io.BytesIO(body)

    def read(self, amt: Optional[int] = None) -> bytes:
        return self._body.read() if amt is None else self._body.read(amt)

    def close(self) -> None:
        pass


def js(status: int, body: Any) -> FakeResponse:
    return FakeResponse(status, json.dumps(body).encode())


class FakeDesktop:
    def __init__(self) -> None:
        self.created: Optional[Dict[str, Any]] = None
        self.execs: List[Dict[str, Any]] = []
        self.ports: Dict[int, str] = {}

    def view(self) -> Dict[str, Any]:
        return {
            "id": ID, "state": "ready", "kind": "ephemeral", "preset": "image", "ram_mb": 2048,
            "network": {"mode": "web"}, "idle_timeout_s": 300, "expires_at": "2026-11-25T04:00:00Z",
            "created_at": "2026-11-25T00:00:00Z", "resolution": "1280x800", "token": "sbx-1",
            "token_expires_at": (datetime.now(timezone.utc) + timedelta(minutes=15)).isoformat().replace("+00:00", "Z"),
        }

    def __call__(self, method: str, url: str, headers: Dict[str, str], body: Optional[bytes], timeout: Optional[float]) -> FakeResponse:
        path = urlsplit(url).path
        auth = headers.get("Authorization")
        base = f"/v1/sandboxes/{ID}"
        text = (body or b"").decode()
        port_view = lambda p: {"port": p, "url": f"https://{p}-{ID}.preview.test", "preview_token": f"tok-{p}"}  # noqa: E731
        if auth == "Bearer key-1":
            if method == "POST" and path == "/v1/sandboxes":
                self.created = json.loads(text)
                self.ports = {6080: "private", 6081: "private"}
                return js(201, self.view())
            if method == "GET" and path == base:
                return js(200, self.view())
            if path == f"{base}/ports" and method == "POST":
                req = json.loads(text)
                self.ports[req["port"]] = req["visibility"]
                return js(201, port_view(req["port"]))
            if path.startswith(f"{base}/ports/"):
                port = int(path.rsplit("/", 1)[1])
                if port not in self.ports:
                    return js(404, {"code": "sandbox_port_not_found", "status": 404})
                if method == "DELETE":
                    del self.ports[port]
                    return FakeResponse(204)
                return js(200, port_view(port))
            return js(404, {"code": "not_found"})
        if auth != "Bearer sbx-1" or path != f"{base}/exec" or method != "POST":
            return js(404, {"code": "not_found"})
        req = json.loads(text)
        self.execs.append(req)
        sub, rest = req["cmd"][1], req["cmd"][2:]
        out = {
            "screenshot": base64.b64encode(PNG).decode() + "\n",
            "cursor": "17 42\n",
            "size": "1280x800\n",
            "windows": "41943043\tmousepad.Mousepad\tUntitled 1 - Mousepad\n52428803\tchromium.Chromium\tNew Tab\n",
            "window": "52428803\t\tNew Tab\n",
        }.get(sub, "")
        if sub == "click" and rest[0] == "5000":
            return js(200, {"stdout": "", "stderr": "gregale-desktop: (5000, 1) is outside the 1280x800 screen\n", "exit_code": 2, "wall_time_s": 0})
        return js(200, {"stdout": out, "stderr": "", "exit_code": 0, "wall_time_s": 0.01})


class DesktopTest(unittest.TestCase):
    def setUp(self) -> None:
        self.fake = FakeDesktop()
        self.d = Desktop.create(resolution=(1280, 800), api_key="key-1", api_base=API, transport=self.fake)

    def argv(self) -> List[List[str]]:
        return [e["cmd"][1:] for e in self.fake.execs]

    def test_create_asks_for_preset_desktop(self) -> None:
        self.assertIsInstance(self.d, Desktop)
        self.assertEqual(self.fake.created, {"preset": "desktop", "resolution": "1280x800"})

    def test_actions_run_gregale_desktop_as_argv(self) -> None:
        d = self.d
        d.left_click(10, 20)
        d.right_click(1, 2)
        d.double_click(3, 4)
        d.middle_click()
        d.move_mouse(5, 6)
        d.mouse_press("right")
        d.mouse_release()
        d.drag((1, 2), (30, 40))
        d.scroll("up", 3)
        d.write('$(reboot); echo "hi"')
        d.press("enter")
        d.press(["ctrl", "c"])
        d.launch("chromium", "https://example.com")
        d.open("/workspace/report.pdf")
        self.assertEqual(self.argv(), [
            ["click", "10", "20", "--button", "left"],
            ["click", "1", "2", "--button", "right"],
            ["click", "3", "4", "--button", "left", "--double"],
            ["cursor"],
            ["click", "17", "42", "--button", "middle"],
            ["move", "5", "6"],
            ["down", "--button", "right"],
            ["up", "--button", "left"],
            ["drag", "1", "2", "30", "40"],
            ["scroll", "up", "3"],
            ["type", "--", '$(reboot); echo "hi"'],
            ["key", "--", "enter"],
            ["key", "--", "ctrl+c"],
            ["launch", "chromium", "--", "https://example.com"],
            ["open", "--", "/workspace/report.pdf"],
        ])
        for e in self.fake.execs:
            self.assertEqual(e["cmd"][0], "/usr/local/bin/gregale-desktop")
            self.assertEqual(e["env"]["DISPLAY"], ":0")

    def test_reads(self) -> None:
        d = self.d
        self.assertEqual(d.screenshot(), PNG)
        self.assertEqual(d.get_cursor_position(), (17, 42))
        self.assertEqual(d.get_screen_size(), (1280, 800))
        self.assertEqual(d.get_application_windows("mousepad"), ["41943043", "52428803"])
        self.assertEqual(d.get_current_window_id(), "52428803")
        self.assertEqual(d.get_window_title("41943043"), "Untitled 1 - Mousepad")
        with self.assertRaises(InvalidArgumentException):
            d.get_window_title("1")

    def test_bad_input(self) -> None:
        for call in (lambda: self.d.left_click(1.5, 2), lambda: self.d.left_click(-1, 2), lambda: self.d.left_click(1)):  # type: ignore[arg-type]
            with self.assertRaises(InvalidArgumentException):
                call()
        self.assertEqual(self.fake.execs, [])
        with self.assertRaises(CommandExitException) as cm:
            self.d.left_click(5000, 1)
        self.assertEqual(cm.exception.exit_code, 2)
        self.assertIn("outside the 1280x800 screen", str(cm.exception))

    def test_write_in_chunks(self) -> None:
        self.d.write("abcdefgh", chunk_size=3)
        self.assertEqual(self.argv(), [["type", "--", "abc"], ["type", "--", "def"], ["type", "--", "gh"]])

    def test_stream(self) -> None:
        url = urlsplit(self.d.stream.get_url())
        q = {k: v[0] for k, v in parse_qs(url.query).items()}
        self.assertEqual(url.netloc, f"6080-{ID}.preview.test")
        self.assertEqual(url.path, "/vnc.html")
        self.assertEqual((q["autoconnect"], q["resize"], q["path"], q["gregale_preview_token"]), ("1", "scale", "websockify", "tok-6080"))
        self.assertNotIn("view_only", q)
        view = urlsplit(self.d.stream.get_url(view_only=True, auto_connect=False))
        vq = {k: v[0] for k, v in parse_qs(view.query).items()}
        self.assertEqual(view.netloc, f"6081-{ID}.preview.test")
        self.assertEqual((vq["view_only"], vq["autoconnect"], vq["gregale_preview_token"]), ("1", "0", "tok-6081"))
        self.assertEqual(self.d.stream.get_auth_key(view_only=True), "tok-6081")
        self.d.stream.stop()
        self.assertEqual(self.fake.ports, {})
        with self.assertRaises(Exception):
            self.d.stream.get_url()
        self.d.stream.start()
        self.assertEqual(self.fake.ports, {6080: "private", 6081: "private"})


if __name__ == "__main__":
    unittest.main()
