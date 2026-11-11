"""sandbox.files: the session's files.

Paths inside /workspace, and relative paths (which are relative to
/workspace), use Gregale's files API; other absolute paths go through a
command and are capped at OUTSIDE_MAX_BYTES.
"""

from __future__ import annotations

import base64
from dataclasses import dataclass
from datetime import datetime, timezone
from enum import Enum
from typing import IO, Any, Dict, Iterator, List, Optional, Union

from .connection import Connection
from .errors import InvalidArgumentException, NotFoundException, SandboxException, UnsupportedException

WORKSPACE = "/workspace"

#: Files outside /workspace are read and written through commands, up to this size.
OUTSIDE_MAX_BYTES = 256 << 10

WriteData = Union[str, bytes, bytearray, IO[Any]]


class FileType(str, Enum):
    FILE = "file"
    DIR = "dir"
    SYMLINK = "symlink"


@dataclass
class WriteInfo:
    name: str
    path: str
    type: Optional[FileType] = None


@dataclass
class WriteEntry:
    path: str
    data: WriteData


@dataclass
class EntryInfo:
    name: str
    type: Optional[FileType]
    path: str
    size: int
    mode: int
    permissions: str
    owner: str
    group: str
    modified_time: Optional[datetime] = None
    symlink_target: Optional[str] = None


def workspace_relative(p: str) -> Optional[str]:
    """The workspace-relative form of p, or None when p is outside it."""
    rel = p
    if p.startswith("/"):
        if p == WORKSPACE:
            return "."
        if not p.startswith(WORKSPACE + "/"):
            return None
        rel = p[len(WORKSPACE) + 1:]
    parts: List[str] = []
    for part in rel.split("/"):
        if part in ("", "."):
            continue
        if part == "..":
            if not parts:
                return None
            parts.pop()
        else:
            parts.append(part)
    return "/".join(parts) if parts else "."


def _quote(s: str) -> str:
    return "'" + s.replace("'", "'\\''") + "'"


def _permissions(mode: int, kind: str) -> str:
    bits = "rwxrwxrwx"
    out = "d" if kind == "dir" else "l" if kind == "symlink" else "-"
    for i in range(9):
        out += bits[i] if mode & (1 << (8 - i)) else "-"
    return out


def _bytes_of(data: WriteData) -> bytes:
    if isinstance(data, str):
        return data.encode()
    if isinstance(data, (bytes, bytearray)):
        return bytes(data)
    read = data.read()
    return read.encode() if isinstance(read, str) else bytes(read)


def _check_user(user: Optional[str], call: str) -> None:
    if user is not None and user not in ("root", "user"):
        raise UnsupportedException(f'{call} as "{user}"', 'Gregale sandboxes act as "user" or "root"')


class Filesystem:
    def __init__(self, conn: Connection) -> None:
        self._conn = conn

    def _entry(self, info: Dict[str, Any]) -> EntryInfo:
        path = WORKSPACE if info["path"] == "." else f"{WORKSPACE}/{info['path']}"
        kind = info.get("type", "file")
        ftype = FileType.DIR if kind == "dir" else FileType.SYMLINK if kind == "symlink" else FileType.FILE
        return EntryInfo(
            name=path.rsplit("/", 1)[-1],
            type=ftype,
            path=path,
            size=int(info.get("size", 0)),
            mode=int(info.get("mode", 0)),
            permissions=_permissions(int(info.get("mode", 0)), kind),
            owner="user",
            group="user",
            modified_time=datetime.fromtimestamp(int(info.get("mtime_unix_ms", 0)) / 1000, tz=timezone.utc),
            symlink_target=info.get("link_target"),
        )

    def _outside(self, script: str, user: Optional[str], call: str, request_timeout: Optional[float]) -> str:
        """Runs a shell command for a path outside /workspace and answers its stdout."""
        _check_user(user, call)
        res = self._conn.json(
            "POST",
            "/exec",
            json_body={"cmd": ["/bin/sh", "-c", script], "run_as": "root" if user == "root" else None},
            timeout=request_timeout,
        )
        if res.get("exit_code") == 2:
            raise NotFoundException(f"{call}: {res.get('stderr', '').strip() or 'no such file or directory'}")
        if res.get("exit_code") != 0:
            raise SandboxException(f"{call}: {res.get('stderr', '').strip()}")
        return res.get("stdout", "")

    def _inside(self, path: str, call: str) -> str:
        rel = workspace_relative(path)
        if rel is None:
            raise UnsupportedException(f"{call}({path})", f"this call works inside {WORKSPACE}; use a command for other paths")
        return rel

    def read(self, path: str, format: str = "text", user: Optional[str] = None, request_timeout: Optional[float] = None) -> Any:
        """Reads a file as text (the default), bytes, or a stream of byte chunks."""
        if format not in ("text", "bytes", "stream"):
            raise InvalidArgumentException(f'format "{format}": use "text", "bytes" or "stream"')
        rel = workspace_relative(path)
        if rel is None:
            plain = path.replace('"', "")
            b64 = self._outside(
                f'[ -e {_quote(path)} ] || {{ echo "no such file: {plain}" >&2; exit 2; }}; '
                f'[ "$(wc -c < {_quote(path)})" -le {OUTSIDE_MAX_BYTES} ] || {{ echo "larger than {OUTSIDE_MAX_BYTES} bytes outside {WORKSPACE}" >&2; exit 1; }}; '
                f"base64 < {_quote(path)}",
                user,
                f"files.read({path})",
                request_timeout,
            )
            data = base64.b64decode("".join(b64.split()))
            if format == "stream":
                return iter([data])
            return data if format == "bytes" else data.decode()
        res = self._conn.call("GET", "/files", query={"path": rel}, accept="application/octet-stream", timeout=request_timeout)
        if format == "stream":
            return _chunks(res)
        try:
            data = res.read()
        finally:
            res.close()
        return data if format == "bytes" else data.decode()

    def write(self, path: Union[str, List[WriteEntry]], data: Optional[WriteData] = None, user: Optional[str] = None, request_timeout: Optional[float] = None) -> Any:
        """Writes one file, or a list of WriteEntry; parent directories are created."""
        if isinstance(path, list):
            return self.write_files(path, user=user, request_timeout=request_timeout)
        if data is None:
            raise InvalidArgumentException(f"files.write({path}) needs data")
        return self._write_one(path, data, user, request_timeout)

    def write_files(self, files: List[WriteEntry], user: Optional[str] = None, request_timeout: Optional[float] = None) -> List[WriteInfo]:
        return [self._write_one(f.path, f.data, user, request_timeout) for f in files]

    def _write_one(self, path: str, data: WriteData, user: Optional[str], request_timeout: Optional[float]) -> WriteInfo:
        raw = _bytes_of(data)
        rel = workspace_relative(path)
        if rel == ".":
            raise InvalidArgumentException(f"{path} is a directory")
        if rel is None:
            if len(raw) > OUTSIDE_MAX_BYTES:
                raise UnsupportedException(f"files.write({path}) of {len(raw)} bytes", f"write files over {OUTSIDE_MAX_BYTES} bytes inside {WORKSPACE}")
            directory = path.rsplit("/", 1)[0] or "/"
            self._outside(
                f"mkdir -p {_quote(directory)} && printf %s {_quote(base64.b64encode(raw).decode())} | base64 -d > {_quote(path)}",
                user,
                f"files.write({path})",
                request_timeout,
            )
            return WriteInfo(name=path.rsplit("/", 1)[-1], path=path, type=FileType.FILE)
        self._conn.call("PUT", "/files", query={"path": rel, "parents": True}, body=raw, timeout=request_timeout).close()
        abs_path = f"{WORKSPACE}/{rel}"
        return WriteInfo(name=abs_path.rsplit("/", 1)[-1], path=abs_path, type=FileType.FILE)

    def list(self, path: str, depth: int = 1, user: Optional[str] = None, request_timeout: Optional[float] = None) -> List[EntryInfo]:
        rel = self._inside(path, "files.list")
        res = self._conn.json("GET", "/files:list", query={"path": rel, "depth": depth}, timeout=request_timeout)
        return [self._entry(e) for e in res.get("entries", []) if e["path"] != rel]

    def exists(self, path: str, user: Optional[str] = None, request_timeout: Optional[float] = None) -> bool:
        if workspace_relative(path) is None:
            return self._outside(f"[ -e {_quote(path)} ] && echo yes || true", user, f"files.exists({path})", request_timeout).strip() == "yes"
        try:
            self.get_info(path, request_timeout=request_timeout)
            return True
        except NotFoundException:
            return False

    def get_info(self, path: str, user: Optional[str] = None, request_timeout: Optional[float] = None) -> EntryInfo:
        rel = self._inside(path, "files.get_info")
        return self._entry(self._conn.json("GET", "/files:stat", query={"path": rel}, timeout=request_timeout))

    def make_dir(self, path: str, user: Optional[str] = None, request_timeout: Optional[float] = None) -> bool:
        """Creates a directory and its parents; False when it already existed."""
        if workspace_relative(path) is None:
            out = self._outside(f"[ -d {_quote(path)} ] && echo existed || mkdir -p {_quote(path)}", user, f"files.make_dir({path})", request_timeout)
            return out.strip() != "existed"
        if self.exists(path, request_timeout=request_timeout):
            return False
        self._conn.call("POST", "/files:mkdir", json_body={"path": self._inside(path, "files.make_dir"), "parents": True}, timeout=request_timeout).close()
        return True

    def rename(self, old_path: str, new_path: str, user: Optional[str] = None, request_timeout: Optional[float] = None) -> EntryInfo:
        src = self._inside(old_path, "files.rename")
        dst = self._inside(new_path, "files.rename")
        self._conn.call("POST", "/files:rename", json_body={"from": src, "to": dst}, timeout=request_timeout).close()
        return self.get_info(new_path, request_timeout=request_timeout)

    def remove(self, path: str, user: Optional[str] = None, request_timeout: Optional[float] = None) -> None:
        if workspace_relative(path) is None:
            self._outside(f"rm -rf -- {_quote(path)}", user, f"files.remove({path})", request_timeout)
            return
        try:
            self._conn.call("POST", "/files:remove", json_body={"path": self._inside(path, "files.remove"), "recursive": True}, timeout=request_timeout).close()
        except NotFoundException:
            pass

    def watch_dir(self, path: str, *args: Any, **kwargs: Any) -> None:
        raise UnsupportedException("files.watch_dir", "poll files.list, or run a watcher as a background command")


def _chunks(res: Any, size: int = 64 << 10) -> Iterator[bytes]:
    try:
        while True:
            chunk = res.read(size)
            if not chunk:
                return
            yield chunk
    finally:
        res.close()
