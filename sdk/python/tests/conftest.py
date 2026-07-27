"""conftest — spawn `sdk/fakeapid` for the smoke suite.

Mirrors `sdk/node/test/helpers/spawn-fakeapid.ts` and
`sdk/fakeapid/main_test.go::spawnFakeAPID`. The fixture is built
on-demand via `go build -o bin/fakeapid .`; the path is computed
relative to the `sdk/python/` test cwd so the helper is cwd-independent.

Usage::

    def test_foo(fakeapid):
        # `fakeapid.base_url` is a live http://127.0.0.1:<port> string.
        resp = httpx.get(fakeapid.base_url + "/__healthz")
        assert resp.json() == {"ok": True}
"""

from __future__ import annotations

import os
import shutil
import signal
import socket
import subprocess
import sys
import time
from collections.abc import Iterator
from pathlib import Path

import httpx
import pytest


def _free_port() -> int:
    """Allocate a free TCP port on 127.0.0.1 and release it.

    Uses the canonical `socket-bind-then-close` pattern; race-prone but
    sufficient for the test (the kernel will not reassign the same
    port for ~60 s after close, and the next bind by fakeapid will
    succeed deterministically given a port we just freed).
    """
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def _fakeapid_bin_path() -> Path:
    """Path to the built fakeapid binary. Builds it on first use.

    `<repo>/sdk/fakeapid/bin/fakeapid` is the canonical output of
    `cd sdk/fakeapid && go build -o bin/fakeapid .`. We resolve the
    repo root from this file (tests/conftest.py -> tests/ -> sdk/python/ ->
    sdk/ -> <repo>).
    """
    # conftest.py -> tests/ -> sdk/python/ -> sdk/ -> <repo>
    repo_root = Path(__file__).resolve().parent.parent.parent.parent
    bin_path = repo_root / "sdk" / "fakeapid" / "bin" / "fakeapid"
    if not bin_path.exists():
        src_dir = repo_root / "sdk" / "fakeapid"
        if not (src_dir / "main.go").exists():
            pytest.skip(f"fakeapid source not found at {src_dir}; cannot build fixture")
        bin_path.parent.mkdir(parents=True, exist_ok=True)
        result = subprocess.run(
            ["go", "build", "-o", str(bin_path), "."],
            cwd=str(src_dir),
            capture_output=True,
            text=True,
        )
        if result.returncode != 0:
            pytest.fail(f"failed to build fakeapid:\nstdout={result.stdout}\nstderr={result.stderr}")
    return bin_path


def _spawn_fakeapid(bin_path: Path, port: int) -> subprocess.Popen:
    """Start the fixture on `port`, returning the subprocess handle.

    `start_new_session=True` (POSIX) puts the child in its own
    process group so `os.killpg(pid, SIGKILL)` reaches the child
    only, even if it has spawned helpers. Mirrors the
    `Setpgid: true` shape in `sdk/fakeapid/main_test.go::spawnFakeAPID`
    (memory `e2e-harness-daemon-leak.md`).
    """
    env = dict(os.environ)
    env["PORT"] = str(port)
    # `nohup=False` because the test owns the lifecycle. `DEV=1` keeps
    # the fixture quiet.
    env.setdefault("DEV", "1")
    return subprocess.Popen(
        [str(bin_path)],
        env=env,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        start_new_session=True,
    )


def _wait_healthz(base_url: str, deadline: float = 5.0) -> None:
    """Poll `/__healthz` until 200 or `deadline` seconds elapse."""
    end = time.monotonic() + deadline
    while time.monotonic() < end:
        try:
            resp = httpx.get(base_url + "/__healthz", timeout=1.0)
            if resp.status_code == 200:
                return
        except httpx.HTTPError:
            pass
        time.sleep(0.05)
    raise RuntimeError(f"fakeapid did not become healthy at {base_url} within {deadline}s")


class FakeApidHandle:
    """Context handle returned by the `fakeapid` fixture.

    Attributes:
      base_url — http://127.0.0.1:<port>
      port     — the bound port
    """

    def __init__(self, base_url: str, port: int, proc: subprocess.Popen) -> None:
        self.base_url = base_url
        self.port = port
        self._proc = proc

    def stop(self) -> None:
        """Tear the fixture down. SIGKILL the process group; the
        fixture is a stdlib-only binary with no cleanup hooks."""
        if self._proc.poll() is not None:
            return
        if sys.platform == "win32":
            self._proc.terminate()
        else:
            try:
                os.killpg(self._proc.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
        try:
            self._proc.wait(timeout=2.0)
        except subprocess.TimeoutExpired:
            self._proc.kill()


@pytest.fixture
def fakeapid() -> Iterator[FakeApidHandle]:
    """Pytest fixture: spawn fakeapid on a free port; yield the handle;
    tear down on exit.
    """
    if not shutil.which("go"):
        pytest.skip("go toolchain not on PATH; sdk-gen-python test suite requires it to build the fixture")
    bin_path = _fakeapid_bin_path()
    port = _free_port()
    proc = _spawn_fakeapid(bin_path, port)
    base_url = f"http://127.0.0.1:{port}"
    try:
        _wait_healthz(base_url)
        handle = FakeApidHandle(base_url, port, proc)
        yield handle
    finally:
        if proc.poll() is None:
            handle.stop()


__all__ = ["FakeApidHandle", "fakeapid"]
