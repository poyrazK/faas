"""Desktop: an E2B Desktop-style handle on a Gregale sandbox desktop.

ADR-20261124075005 §6. Written from E2B Desktop's public documentation.
Every action runs gregale-desktop in the session as an argv, never shell
text, and the live view is noVNC behind the session's private preview ports.
"""

from __future__ import annotations

import base64
import time
from typing import Any, Dict, List, Optional, Tuple, Union
from urllib.parse import urlencode, urljoin

from .connection import Connection
from .errors import CommandExitException, CommandResult, InvalidArgumentException
from .sandbox import Sandbox

DESKTOP_VIEW_PORT = 6080
DESKTOP_VIEW_ONLY_PORT = 6081
_GREGALE_DESKTOP = "/usr/local/bin/gregale-desktop"


def _coord(name: str, v: int) -> str:
    if isinstance(v, bool) or not isinstance(v, int) or v < 0:
        raise InvalidArgumentException(f"{name} must be a non-negative integer; got {v!r}")
    return str(v)


class DesktopStream:
    """The noVNC live view: watch the desktop, or take it over."""

    def __init__(self, conn: Connection) -> None:
        self._conn = conn

    def start(self, require_auth: bool = True) -> None:
        """Exposes both views privately; a desktop has them from create, so
        this brings them back after stop()."""
        for port in (DESKTOP_VIEW_PORT, DESKTOP_VIEW_ONLY_PORT):
            self._conn.control("POST", "/ports", json_body={"port": port, "visibility": "private"})

    def get_url(self, auto_connect: bool = True, view_only: bool = False, resize: str = "scale") -> str:
        """The view's URL with its preview token. Give view_only=True links to
        people who should only watch: that port's server takes no input, and
        its token opens no other port."""
        port = DESKTOP_VIEW_ONLY_PORT if view_only else DESKTOP_VIEW_PORT
        view = self._conn.control("GET", f"/ports/{port}")
        query = {"autoconnect": "1" if auto_connect else "0", "resize": resize, "reconnect": "1", "path": "websockify"}
        if view_only:
            query["view_only"] = "1"
        if view.get("preview_token"):
            query["gregale_preview_token"] = view["preview_token"]
        return urljoin(view["url"], "/vnc.html") + "?" + urlencode(query)

    def get_auth_key(self, view_only: bool = False) -> Optional[str]:
        port = DESKTOP_VIEW_ONLY_PORT if view_only else DESKTOP_VIEW_PORT
        return self._conn.control("GET", f"/ports/{port}").get("preview_token")

    def stop(self) -> None:
        """Unexposes both views; open viewers are cut off."""
        for port in (DESKTOP_VIEW_PORT, DESKTOP_VIEW_ONLY_PORT):
            self._conn.control("DELETE", f"/ports/{port}")


class Desktop(Sandbox):
    """A sandbox session with a desktop: Xfce on a virtual screen (Pro and Scale).

    Desktop(resolution=(1280, 800), api_key=...) or Desktop.create(...)
    creates one and waits until it runs.
    """

    def __init__(self, *args: Any, resolution: Optional[Tuple[int, int]] = None, **opts: Any) -> None:
        if opts.get("_conn") is None:
            # Any template a caller passes is the desktop.
            opts.pop("template", None)
            super().__init__("desktop", resolution=resolution, **opts)
        else:
            super().__init__(**opts)

    def _attach(self, conn: Connection) -> None:
        super()._attach(conn)
        self.stream = DesktopStream(conn)

    @classmethod
    def create(cls, template: Optional[str] = None, **opts: Any) -> "Desktop":  # type: ignore[override]
        """Creates a desktop session and waits until it runs."""
        return cls(**opts)

    def _run(self, *args: str, timeout: float = 30.0) -> str:
        res = self._conn.json(
            "POST",
            "/exec",
            json_body={"cmd": [_GREGALE_DESKTOP, *args], "env": {**self._conn.envs, "DISPLAY": ":0"}, "timeout_ms": int(timeout * 1000)},
        )
        if res["exit_code"] != 0:
            error = "the command timed out" if res.get("timed_out") else (res.get("stderr") or "").strip() or None
            raise CommandExitException(CommandResult(stdout=res.get("stdout", ""), stderr=res.get("stderr", ""), exit_code=res["exit_code"], error=error))
        return res.get("stdout", "")

    def screenshot(self, format: str = "bytes") -> Union[bytes, bytearray]:
        """A PNG of the screen: bytes (the default) or a bytearray."""
        data = base64.b64decode(self._run("screenshot", "--base64").strip())
        return bytearray(data) if format == "bytearray" else data

    def _click(self, button: str, x: Optional[int], y: Optional[int], double: bool = False) -> None:
        if (x is None) != (y is None):
            raise InvalidArgumentException("pass both x and y, or neither")
        if x is None:
            x, y = self.get_cursor_position()
        self._run("click", _coord("x", x), _coord("y", y), "--button", button, *(["--double"] if double else []))  # type: ignore[arg-type]

    def left_click(self, x: Optional[int] = None, y: Optional[int] = None) -> None:
        """Clicks at (x, y), or where the pointer is."""
        self._click("left", x, y)

    def right_click(self, x: Optional[int] = None, y: Optional[int] = None) -> None:
        self._click("right", x, y)

    def middle_click(self, x: Optional[int] = None, y: Optional[int] = None) -> None:
        self._click("middle", x, y)

    def double_click(self, x: Optional[int] = None, y: Optional[int] = None) -> None:
        self._click("left", x, y, double=True)

    def move_mouse(self, x: int, y: int) -> None:
        self._run("move", _coord("x", x), _coord("y", y))

    def mouse_press(self, button: str = "left") -> None:
        self._run("down", "--button", button)

    def mouse_release(self, button: str = "left") -> None:
        self._run("up", "--button", button)

    def drag(self, fr: Tuple[int, int], to: Tuple[int, int]) -> None:
        """Presses at fr, moves, releases at to."""
        self._run("drag", _coord("x", fr[0]), _coord("y", fr[1]), _coord("x", to[0]), _coord("y", to[1]))

    def scroll(self, direction: str = "down", amount: int = 1) -> None:
        self._run("scroll", direction, str(amount))

    def write(self, text: str, chunk_size: int = 4096, delay_in_ms: int = 0) -> None:
        """Types text into the focused window, chunk_size characters at a time."""
        size = max(1, chunk_size)
        for i in range(0, len(text), size):
            if i and delay_in_ms:
                time.sleep(delay_in_ms / 1000)
            self._run("type", "--", text[i : i + size], timeout=120.0)

    def press(self, key: Union[str, List[str]]) -> None:
        """Presses a key ("enter"), or a chord as a list (["ctrl", "c"])."""
        self._run("key", "--", "+".join(key) if isinstance(key, list) else key)

    def get_cursor_position(self) -> Tuple[int, int]:
        x, y = self._run("cursor").split()
        return int(x), int(y)

    def get_screen_size(self) -> Tuple[int, int]:
        w, h = self._run("size").strip().split("x")
        return int(w), int(h)

    def launch(self, application: str, uri: Optional[str] = None) -> None:
        """Starts an application (chromium, xfce4-terminal, mousepad, ...), optionally on a URI."""
        self._run("launch", application, *(["--", uri] if uri else []))

    def open(self, file_or_url: str) -> None:
        """Opens a file or URL with its default application."""
        self._run("open", "--", file_or_url)

    def _windows(self, app: Optional[str] = None) -> List[Dict[str, str]]:
        out = self._run("windows", *(["--app", app] if app else []))
        windows = []
        for line in out.splitlines():
            if not line.strip():
                continue
            wid, cls, title = (line.split("\t", 2) + ["", ""])[:3]
            windows.append({"id": wid, "app": cls, "title": title})
        return windows

    def get_application_windows(self, application: str) -> List[str]:
        """The IDs of an application's windows."""
        return [w["id"] for w in self._windows(application)]

    def get_current_window_id(self) -> str:
        return self._run("window").split("\t")[0].strip()

    def get_window_title(self, window_id: str) -> str:
        for w in self._windows():
            if w["id"] == window_id:
                return w["title"]
        wid, _, title = (self._run("window").rstrip("\n").split("\t", 2) + ["", ""])[:3]
        if wid == window_id:
            return title
        raise InvalidArgumentException(f"no window {window_id}")

    def wait(self, ms: int) -> None:
        """Waits ms milliseconds."""
        time.sleep(ms / 1000)
