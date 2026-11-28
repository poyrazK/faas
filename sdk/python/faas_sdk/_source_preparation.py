"""Local source selection and deterministic archives, independent of clients and the CLI."""

from __future__ import annotations

import asyncio
import hashlib
import math
import os
import re
import shutil
import stat
import tempfile
import time
import zlib
from collections import Counter
from collections.abc import AsyncIterator, Awaitable, Callable, Coroutine
from dataclasses import dataclass, replace
from pathlib import Path
from typing import Any, Literal, TypeVar

SourcePreparationCode = Literal[
    "invalid_input",
    "unsafe_path",
    "unsupported_file",
    "source_changed",
    "limit_exceeded",
    "secret_detected",
    "deadline_exceeded",
    "invalid_source",
]


@dataclass(frozen=True, slots=True)
class SourceIssue:
    path: str
    code: SourcePreparationCode
    message: str


@dataclass(frozen=True, slots=True)
class SourceEntry:
    path: str
    kind: Literal["file", "directory"]
    size: int
    mode: int
    included: bool
    reason: str
    rule_file: str | None = None
    rule_line: int | None = None
    symlink: bool = False
    sha256: str | None = None
    generated: bool = False


@dataclass(frozen=True, slots=True)
class SourcePlan:
    context_root: str
    source_root: str
    dockerfile: bool
    entries: tuple[SourceEntry, ...]
    issues: tuple[SourceIssue, ...]
    file_count: int
    total_bytes: int


@dataclass(frozen=True, slots=True)
class SourcePreparationProgress:
    phase: Literal["scanning", "packing"]
    path: str
    bytes: int


@dataclass(frozen=True, kw_only=True)
class SourcePreparationOptions:
    # Retain the context; source_root chooses the app within it, including monorepos.
    source_root: str = "."
    dockerfile: bool | None = None
    exclude: tuple[str, ...] = ()
    secret_scan: Literal["strict", "off"] = "strict"
    timeout: float = 120.0
    max_entries: int = 10_000
    max_bytes: int = 2 << 30
    max_archive_bytes: int = (1 << 53) - 1
    on_progress: Callable[[SourcePreparationProgress], Awaitable[None] | None] | None = None
    on_plan: Callable[[SourcePlan], Awaitable[None] | None] | None = None


@dataclass(frozen=True, slots=True)
class PreparedSource:
    path: str
    size: int
    sha256: str
    plan: SourcePlan


class SourcePreparationError(Exception):
    def __init__(self, code: SourcePreparationCode, message: str, plan: SourcePlan | None = None) -> None:
        super().__init__(message)
        self.code = code
        self.plan = plan


# pkg/api/limits.go: archive safety caps, not plan quotas.
_CHUNK = 64 * 1024
_DIRS = {".git", "node_modules", "vendor", "__pycache__", "dist", ".next", "coverage", "target", ".venv", ".cache"}
_BUILD = {"dist", ".next", "coverage", "target", ".cache"}
_FILES = {".git", ".DS_Store", "Thumbs.db"}
_PROVIDERS = (
    ("deploy", "gregale_deploy_token", r"fp_deploy_[0-9a-f]{48}"),
    ("stripe", "stripe_live", r"sk_live_[A-Za-z0-9]{24,}"),
    ("stripe", "stripe_test", r"sk_test_[A-Za-z0-9]{24,}"),
    ("github", "github_pat", r"gh[pousr]_[A-Za-z0-9]{30,}"),
    ("aws", "aws_access", r"AKIA[0-9A-Z]{16}"),
    ("openai", "openai", r"sk-[A-Za-z0-9]{20,}T3BlbkFJ[A-Za-z0-9]{20,}"),
    ("anthropic", "anthropic", r"sk-ant-[A-Za-z0-9-]{32,}"),
    ("google", "google_api", r"AIza[0-9A-Za-z_-]{35}"),
    ("", "private_key_block", r"-----BEGIN (RSA |EC |DSA |OPENSSH |PGP )?PRIVATE KEY-----"),
)


def _secret_line(line: str, in_pem: bool) -> str | None:
    stripped = line.strip()
    if stripped.startswith("#"):
        return None
    stripped = stripped.removeprefix("export ")
    key, eq, value = stripped.partition("=")
    if not eq:
        return None
    if len(value) >= 2 and value[0] == value[-1] and value[0] in "\"'":
        value = value[1:-1]
    for hint, provider, pattern in _PROVIDERS:
        if hint in key.strip().lower() and re.search(pattern, value):
            return provider
    raw = value.encode("latin1")
    if not in_pem and len(raw) >= 20 and not re.match(r"^[a-z][a-z0-9+.-]+:", value):
        entropy = -sum((n / len(raw)) * math.log2(n / len(raw)) for n in Counter(raw).values())
        if entropy >= 4.5:
            return "high_entropy"
    return None


def _name(value: str) -> bool:
    return (
        bool(value)
        and "\\" not in value
        and not any(ord(c) < 32 or ord(c) == 127 or 0xD800 <= ord(c) <= 0xDFFF for c in value)
    )


def _root_name(value: str) -> str:
    if value in ("", "."):
        return "."
    if not _name(value) or ":" in value or value.startswith("/") or any(p in ("", ".", "..") for p in value.split("/")):
        raise SourcePreparationError("invalid_input", "source_root must be a relative slash-separated directory")
    return value


_T = TypeVar("_T")


class _Operation:
    def __init__(self, options: SourcePreparationOptions):
        self.options = options
        self.active = True
        if isinstance(options.timeout, bool) or not math.isfinite(options.timeout) or options.timeout <= 0:
            raise SourcePreparationError("invalid_input", "timeout must be positive and finite")
        for name, value, limit in (
            ("max_entries", options.max_entries, 10_000),
            ("max_bytes", options.max_bytes, 2 << 30),
            ("max_archive_bytes", options.max_archive_bytes, (1 << 53) - 1),
        ):
            if type(value) is not int or not 1 <= value <= limit:
                raise SourcePreparationError("invalid_input", f"{name} must be a positive integer at most {limit}")
        if options.secret_scan not in ("strict", "off"):
            raise SourcePreparationError("invalid_input", "secret_scan must be strict or off")
        _root_name(options.source_root)
        self.excludes = tuple(options.exclude)
        for path in self.excludes:
            if _root_name(path) == ".":
                raise SourcePreparationError("invalid_input", "exclude must name entries inside the context")
        self.deadline = time.monotonic() + options.timeout

    def check(self) -> None:
        if not self.active:
            raise asyncio.CancelledError()
        if time.monotonic() >= self.deadline:
            raise SourcePreparationError("deadline_exceeded", "Source preparation deadline exceeded")

    async def callback(self, callback: Callable[[_T], Awaitable[None] | None] | None, value: _T) -> None:
        self.check()
        if callback:
            result = callback(value)
            if result is not None:
                await result
        self.check()


async def _run(op: _Operation, operation: Coroutine[Any, Any, _T]) -> _T:
    worker = asyncio.create_task(operation)
    try:
        done, _ = await asyncio.wait({worker}, timeout=max(0, op.deadline - time.monotonic()))
        if not done:
            raise SourcePreparationError("deadline_exceeded", "Source preparation deadline exceeded")
        return worker.result()
    finally:
        op.active = False
        if not worker.done():
            worker.cancel()

        def retire(task: asyncio.Task) -> None:
            if not task.cancelled():
                task.exception()

        worker.add_done_callback(retire)


@dataclass(frozen=True)
class _Rule:
    parts: tuple[str, ...]
    anchor: bool
    dir_only: bool
    negate: bool
    file: str
    line: int


def _rules(data: str, file: str) -> list[_Rule]:
    result = []
    for number, raw in enumerate(data.split("\n"), 1):
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        negate = line.startswith("!")
        if negate:
            line = line[1:]
        anchor = line.startswith("/")
        if anchor:
            line = line[1:]
        directory = line.endswith("/")
        if directory:
            line = line[:-1]
        if not line:
            continue
        parts = line.split("/")
        if file == ".dockerignore":
            anchor = True
            while len(parts) > 1 and parts[0] == ".":
                parts.pop(0)
        result.append(_Rule(tuple(parts), anchor, directory, negate, file, number))
    return result


def _segment(pattern: str, value: str) -> bool:
    # Go filepath.Match, rather than Python fnmatch's different [!...] syntax.
    regex, i = "^", 0
    while i < len(pattern):
        c = pattern[i]
        if c == "*":
            regex += ".*"
        elif c == "?":
            regex += "."
        elif c == "\\":
            i += 1
            if i >= len(pattern):
                return False
            regex += re.escape(pattern[i])
        elif c == "[":
            cls, count = "[", 0
            if i + 1 < len(pattern) and pattern[i + 1] == "^":
                cls += "^"
                i += 1
            i += 1
            while i < len(pattern) and pattern[i] != "]":
                ch = pattern[i]
                if ch == "\\":
                    i += 1
                    if i >= len(pattern):
                        return False
                    cls += "\\" + pattern[i]
                else:
                    cls += "\\[" if ch == "[" else ch
                count += 1
                i += 1
            if not count or i >= len(pattern):
                return False
            regex += cls + "]"
        else:
            regex += re.escape(c)
        i += 1
    try:
        return re.fullmatch(regex, value) is not None
    except re.error:
        return False


def _match(parts: list[str], pattern: tuple[str, ...]) -> bool:
    memo: dict[tuple[int, int], bool] = {}

    def visit(i: int, j: int) -> bool:
        if (i, j) in memo:
            return memo[i, j]
        if j == len(pattern):
            result = i == len(parts)
        elif pattern[j] == "**":
            result = i < len(parts) if j == len(pattern) - 1 else visit(i, j + 1) or i < len(parts) and visit(i + 1, j)
        else:
            result = i < len(parts) and _segment(pattern[j], parts[i]) and visit(i + 1, j + 1)
        memo[i, j] = result
        return result

    return visit(0, 0)


def _selection(
    path: str, directory: bool, docker: bool, vendor: bool, patterns: list[_Rule]
) -> tuple[bool, str, str | None, int | None]:
    base = path.split("/")[-1]
    if base == ".git":
        return False, "VCS metadata is always excluded", None, None
    included, reason = True, "source directory" if directory else "source file"
    if directory and docker and base in _BUILD:
        reason = "Dockerfile build output"
    elif directory and base == "vendor" and vendor:
        reason = "Go module vendoring"
    elif directory and base in _DIRS:
        included, reason = False, "default build-artifact exclusion"
    elif not directory and (base in _FILES or base.endswith(".pyc")):
        included, reason = False, "default generated/metadata exclusion"
    result: tuple[bool, str, str | None, int | None] = included, reason, None, None
    parts = path.split("/")
    for rule in patterns:
        if (not rule.dir_only or directory) and (
            _match(parts, rule.parts) if rule.anchor else any(_match(parts[i:], rule.parts) for i in range(len(parts)))
        ):
            result = rule.negate, "negated ignore rule" if rule.negate else "ignore rule", rule.file, rule.line
    return result


@dataclass(frozen=True)
class _Snapshot:
    path: Path
    target: Path
    signature: tuple[int, ...]


def _signature(info: os.stat_result) -> tuple[int, ...]:
    return info.st_dev, info.st_ino, info.st_size, info.st_mode, info.st_mtime_ns, info.st_ctime_ns


def _snapshot(root: Path, path: Path) -> _Snapshot:
    target = path.resolve(strict=True)
    if not target.is_relative_to(root):
        raise SourcePreparationError("unsafe_path", "Path leaves source context")
    return _Snapshot(path, target, _signature(target.lstat()))


def _verify(root: Path, snapshot: _Snapshot) -> None:
    try:
        actual = _snapshot(root, snapshot.path)
    except (OSError, RuntimeError, SourcePreparationError):
        raise SourcePreparationError("source_changed", "Source path changed during preparation") from None
    if actual != snapshot:
        raise SourcePreparationError("source_changed", "Source changed during preparation")


def _read(root: Path, snapshot: _Snapshot, offset: int) -> bytes:
    # Each worker owns its descriptor; cancellation cannot close a descriptor reused by another task.
    _verify(root, snapshot)
    fd = os.open(snapshot.target, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0) | getattr(os, "O_NONBLOCK", 0))
    try:
        if _signature(os.fstat(fd)) != snapshot.signature:
            raise SourcePreparationError("source_changed", "Source changed before reading")
        os.lseek(fd, offset, os.SEEK_SET)
        data = os.read(fd, _CHUNK)
        if _signature(os.fstat(fd)) != snapshot.signature:
            raise SourcePreparationError("source_changed", "Source changed while reading")
        _verify(root, snapshot)
        return data
    finally:
        os.close(fd)


async def _contents(root: Path, snapshot: _Snapshot, op: _Operation) -> AsyncIterator[bytes]:
    offset = 0
    while True:
        op.check()
        data = await asyncio.to_thread(_read, root, snapshot, offset)
        op.check()
        if not data:
            break
        offset += len(data)
        yield data


async def _inspect(
    directory: str | os.PathLike[str], op: _Operation, virtual_paths: tuple[str, ...] = ()
) -> tuple[SourcePlan, dict[str, _Snapshot]]:
    op.check()
    root = Path(directory).resolve(strict=True)
    if not root.is_dir():
        raise SourcePreparationError("invalid_input", "Source context must be a directory")
    source_root = _root_name(op.options.source_root)
    selected = (root / source_root).resolve(strict=True)
    if not selected.is_relative_to(root) or not selected.is_dir():
        raise SourcePreparationError("unsafe_path", "source_root must stay inside the context")
    snapshots = {"": _snapshot(root, root)}
    docker = op.options.dockerfile
    if docker is None:
        docker = False
        with os.scandir(selected) as children:
            for index, child in enumerate(children):
                await asyncio.sleep(0)
                op.check()
                if index >= 40_000:
                    raise SourcePreparationError("limit_exceeded", "Application root exceeds inspection entry limit")
                if child.name.lower() == "dockerfile" and not child.is_dir(follow_symlinks=False):
                    docker = True
                    break
    patterns = []
    for name in ([".dockerignore"] if docker else []) + [".gregaleignore"]:
        try:
            s = _snapshot(root, root / name)
        except FileNotFoundError:
            continue
        snapshots["\0" + name] = s
        chunks, size = [], 0
        async for data in _contents(root, s, op):
            size += len(data)
            if size > 1024 * 1024:
                raise SourcePreparationError("limit_exceeded", "Ignore file exceeds 1 MiB")
            chunks.append(data)
        patterns.extend(_rules(b"".join(chunks).decode("utf8", errors="replace"), name))
    for path in virtual_paths:
        if path in op.options.exclude or not _selection(path, False, docker, False, patterns)[0]:
            raise SourcePreparationError("invalid_source", "Project transport file is excluded")
    entries: list[SourceEntry] = []
    issues: list[SourceIssue] = []
    file_count = total_bytes = visited = included_count = 0

    async def walk(physical: Path, prefix: str, ancestors: set[Path], depth: int, link_depth: int = 0) -> None:
        nonlocal file_count, total_bytes, visited, included_count
        op.check()
        # Stream directory names; the final plan and archive have UTF-8 ordering.
        with os.scandir(physical) as children:
            for child in children:
                await asyncio.sleep(0)
                lexical = physical / child.name
                op.check()
                name = lexical.name
                path = prefix + "/" + name if prefix else name
                visited += 1
                if visited > op.options.max_entries * 4:
                    raise SourcePreparationError("limit_exceeded", "Source selection exceeds inspection entry limit")
                if not _name(path) or ":" in path:
                    issues.append(SourceIssue(path, "unsafe_path", "Unsupported archive path"))
                    continue
                try:
                    link = lexical.is_symlink()
                    s = _snapshot(root, lexical)
                    info = s.target.lstat()
                except (OSError, RuntimeError, SourcePreparationError):
                    issues.append(SourceIssue(path, "unsafe_path", "Path does not resolve inside the source context"))
                    continue
                directory = stat.S_ISDIR(info.st_mode)
                vendor = False
                if directory and name == "vendor":
                    try:
                        vendor = stat.S_ISREG((root / path / "modules.txt").lstat().st_mode)
                    except OSError:
                        pass
                included, reason, rule_file, rule_line = _selection(path, directory, docker, vendor, patterns)
                if path in op.excludes:
                    included, reason, rule_file, rule_line = False, "command-scoped exclusion", None, None
                kind: Literal["file", "directory"] = "directory" if directory else "file"
                size, mode = (0, 0o755) if directory else (info.st_size, 0o755 if info.st_mode & 0o111 else 0o644)
                if not included:
                    entries.append(SourceEntry(path, kind, size, mode, included, reason, rule_file, rule_line, link))
                    continue
                if not directory and not stat.S_ISREG(info.st_mode):
                    issues.append(SourceIssue(path, "unsupported_file", "Device, socket or pipe cannot be packed"))
                    continue
                if directory and (s.target in ancestors or depth >= 64 or link and link_depth >= 40):
                    issues.append(SourceIssue(path, "unsafe_path", "Directory link cycle or excessive depth"))
                    continue
                included_count += 1
                if included_count > op.options.max_entries:
                    raise SourcePreparationError("limit_exceeded", "Source has too many archive entries")
                snapshots[path] = s
                if directory:
                    entries.append(SourceEntry(path, kind, size, mode, included, reason, rule_file, rule_line, link))
                    await walk(s.target, path, ancestors | {s.target}, depth + 1, link_depth + int(link))
                    continue
                total_bytes += size
                file_count += 1
                if total_bytes > op.options.max_bytes:
                    raise SourcePreparationError("limit_exceeded", "Source exceeds expanded byte limit")
                digest, count, pending, secret, in_pem = hashlib.sha256(), 0, "", False, False
                env = name in ("env", ".env") or name.startswith(".env.")

                def scan(line: str) -> None:
                    nonlocal secret, in_pem
                    if "-----BEGIN " in line and "PRIVATE KEY" in line:
                        in_pem = True
                    if "-----END " in line and "PRIVATE KEY" in line:
                        in_pem = False
                    secret = secret or _secret_line(line, in_pem) is not None

                async for data in _contents(root, s, op):
                    count += len(data)
                    if count > size:
                        raise SourcePreparationError("source_changed", "Source file grew during inspection")
                    digest.update(data)
                    if op.options.secret_scan != "off" and env:
                        pending += data.decode("latin1")
                        while "\n" in pending:
                            line, pending = pending.split("\n", 1)
                            if len(line) > _CHUNK:
                                raise SourcePreparationError("limit_exceeded", "Dotenv line exceeds 64 KiB")
                            scan(line)
                        if len(pending) > _CHUNK:
                            raise SourcePreparationError("limit_exceeded", "Dotenv line exceeds 64 KiB")
                    await op.callback(op.options.on_progress, SourcePreparationProgress("scanning", path, count))
                if count != size:
                    raise SourcePreparationError("source_changed", "Source file shrank during inspection")
                if op.options.secret_scan != "off" and env:
                    scan(pending)
                    if secret:
                        issues.append(
                            SourceIssue(
                                path,
                                "secret_detected",
                                "Credential-shaped dotenv value; move it to Gregale environment configuration or exclude the file",
                            )
                        )
                entries.append(
                    SourceEntry(
                        path, kind, size, mode, included, reason, rule_file, rule_line, link, digest.hexdigest()
                    )
                )

    await walk(root, "", {root}, 0)
    if not file_count:
        issues.append(SourceIssue(".", "invalid_source", "Source context contains no included regular files"))
    if source_root != "." and source_root not in snapshots:
        issues.append(
            SourceIssue(source_root, "invalid_source", "Application source root is excluded from the archive")
        )
    for s in snapshots.values():
        await asyncio.sleep(0)
        op.check()
        _verify(root, s)
    entries.sort(key=lambda e: e.path.encode("utf8"))
    return SourcePlan(str(root), source_root, docker, tuple(entries), tuple(issues), file_count, total_bytes), snapshots


async def inspect_source(
    directory: str | os.PathLike[str], *, options: SourcePreparationOptions | None = None
) -> SourcePlan:
    """Read-only selection plan, including blocking issues; creates no archive."""
    op = _Operation(options or SourcePreparationOptions())

    async def inspect() -> SourcePlan:
        return (await _inspect(directory, op))[0]

    return await _run(op, inspect())


def _header(name: str, size: int, mode: int, kind: str) -> bytes:
    header = bytearray(512)

    def write(offset: int, value: bytes) -> None:
        header[offset : offset + len(value)] = value

    write(0, name.encode("utf8"))
    for offset, width, value in ((100, 8, mode), (108, 8, 0), (116, 8, 0), (124, 12, size), (136, 12, 0)):
        write(offset, f"{value:0{width - 1}o}\0".encode("ascii"))
    write(148, b"        ")
    write(156, kind.encode("ascii"))
    write(257, b"ustar\0")
    write(263, b"00")
    write(148, f"{sum(header):06o}\0 ".encode("ascii"))
    return bytes(header)


def _pax(path: str) -> bytes:
    record = (" path=" + path + "\n").encode("utf8")
    size = len(record) + 1
    while len(str(size)) + len(record) != size:
        size = len(str(size)) + len(record)
    return str(size).encode("ascii") + record


async def _tar(
    plan: SourcePlan, snapshots: dict[str, _Snapshot], op: _Operation, files: dict[str, str]
) -> AsyncIterator[bytes]:
    root = Path(plan.context_root)
    for entry in plan.entries:
        await asyncio.sleep(0)
        op.check()
        if not entry.included:
            continue
        name = entry.path + ("/" if entry.kind == "directory" else "")
        if len(name.encode("utf8")) > 100 or not name.isascii():
            pax = _pax(name)
            yield _header("PaxHeaders/source", len(pax), 0o644, "x")
            yield pax
            if len(pax) % 512:
                yield bytes(512 - len(pax) % 512)
            name = "PaxEntry"
        yield _header(name, entry.size, entry.mode, "5" if entry.kind == "directory" else "0")
        if entry.kind == "directory":
            continue
        digest, count = hashlib.sha256(), 0

        async def chunks() -> AsyncIterator[bytes]:
            if entry.path not in files:
                async for data in _contents(root, snapshots[entry.path], op):
                    yield data
            else:
                data = files[entry.path].encode()
                for offset in range(0, len(data), _CHUNK):
                    op.check()
                    yield data[offset : offset + _CHUNK]

        async for data in chunks():
            count += len(data)
            if count > entry.size:
                raise SourcePreparationError("source_changed", "Source file grew during packing")
            digest.update(data)
            yield data
            await op.callback(op.options.on_progress, SourcePreparationProgress("packing", entry.path, count))
        if count != entry.size or digest.hexdigest() != entry.sha256:
            raise SourcePreparationError("source_changed", "Source content changed after inspection")
        if entry.size % 512:
            yield bytes(512 - entry.size % 512)
    yield bytes(1024)


async def prepare_source(
    directory: str | os.PathLike[str],
    output_path: str | os.PathLike[str],
    *,
    options: SourcePreparationOptions | None = None,
) -> PreparedSource:
    """Atomically write a 0600 archive outside directory; task cancellation stops publication."""
    return await _prepare_source_with_files(directory, output_path, options=options)


async def _prepare_source_with_files(
    directory: str | os.PathLike[str],
    output_path: str | os.PathLike[str],
    *,
    options: SourcePreparationOptions | None = None,
    files: dict[str, str] | None = None,
) -> PreparedSource:
    files = dict(files or {})
    op = _Operation(options or SourcePreparationOptions())

    async def prepare() -> PreparedSource:
        root = Path(directory).resolve(strict=True)
        output = Path(output_path).absolute()
        parent = output.parent.resolve(strict=True)
        output = parent / output.name
        if parent.is_relative_to(root):
            raise SourcePreparationError("invalid_input", "Archive destination must be outside the source context")
        plan, snapshots = await _inspect(root, op, tuple(files))
        if files:
            entries = list(plan.entries)
            for name, generated_text in files.items():
                _root_name(name)
                if Path(name).name not in ("go.mod", "gregale.yaml"):
                    raise SourcePreparationError("invalid_input", "Unsupported project transport file")
                if not isinstance(generated_text, str) or len(generated_text.encode()) > 1024 * 1024:
                    raise SourcePreparationError("limit_exceeded", "Project transport file exceeds 1 MiB")
                previous = next((e for e in entries if e.path == name), None)
                if previous is not None and (not previous.included or previous.kind != "file"):
                    raise SourcePreparationError(
                        "invalid_source", "Project transport file is excluded or conflicts with a directory"
                    )
                parents = name.split("/")[:-1]
                while parents:
                    parent_entry = next((e for e in entries if e.path == "/".join(parents)), None)
                    if parent_entry is None or not parent_entry.included or parent_entry.kind != "directory":
                        raise SourcePreparationError("invalid_source", "Project transport parent is excluded")
                    parents.pop()
                entry = SourceEntry(
                    name,
                    "file",
                    len(generated_text.encode()),
                    0o644,
                    True,
                    "project transport file",
                    sha256=hashlib.sha256(generated_text.encode()).hexdigest(),
                    generated=True,
                )
                if previous is not None:
                    entries[entries.index(previous)] = entry
                else:
                    entries.append(entry)
            entries.sort(key=lambda e: e.path.encode())
            included = [e for e in entries if e.included]
            total = sum(e.size for e in included)
            if len(included) > op.options.max_entries or total > op.options.max_bytes:
                raise SourcePreparationError("limit_exceeded", "Project transport files exceed source limits")
            plan = replace(
                plan, entries=tuple(entries), file_count=sum(e.kind == "file" for e in included), total_bytes=total
            )
        await op.callback(op.options.on_plan, plan)
        if plan.issues:
            raise SourcePreparationError("invalid_source", "Source plan has blocking issues", plan)
        temporary = Path(tempfile.mkdtemp(prefix=".gregale-source-", dir=parent))
        path = temporary / "archive"
        digest, size, gzip = hashlib.sha256(), 0, zlib.compressobj(6, zlib.DEFLATED, 31)
        try:
            fd = os.open(path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
            with os.fdopen(fd, "wb") as file:

                def write(data: bytes) -> None:
                    nonlocal size
                    op.check()
                    size += len(data)
                    if size > op.options.max_archive_bytes:
                        raise SourcePreparationError("limit_exceeded", "Compressed archive exceeds byte limit")
                    digest.update(data)
                    file.write(data)

                async for data in _tar(plan, snapshots, op, files):
                    write(gzip.compress(data))
                write(gzip.flush())
                for s in snapshots.values():
                    await asyncio.sleep(0)
                    op.check()
                    _verify(root, s)
                file.flush()
                os.fsync(file.fileno())
            await asyncio.sleep(0)
            op.check()
            os.replace(path, output)
            return PreparedSource(str(output), size, digest.hexdigest(), plan)
        finally:
            shutil.rmtree(temporary)

    return await _run(op, prepare())
