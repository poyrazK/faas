"""Preview managed files: bounded transfers, portable multipart recovery, job storage.

Async methods use one total deadline; cancel the asyncio task to stop locally.
Only abort() discards a remote multipart session. Application user authorization
belongs to the backend that issues file capabilities.
"""

from __future__ import annotations

import asyncio
import hashlib
import inspect
import json
import math
import re
import uuid
from collections.abc import Awaitable, Callable, Coroutine
from dataclasses import asdict, dataclass
from datetime import datetime
from pathlib import Path
from typing import TYPE_CHECKING, Any, Literal, NotRequired, Protocol, TypedDict, TypeVar, cast
from urllib.parse import quote, urlencode, urlsplit

import httpx

from .._async_request import (
    RequestOptions,
    RequestTimeoutError,
    owned_async_transport,
    run_request,
    snapshot_async_client,
)
from ..job_runtime import JOB_MAX_BYTES, JobContext, JobError, JobStore

if TYPE_CHECKING:
    from .._wrapper import FaaSClient

T = TypeVar("T")
BLOCK = 1024 * 1024
MAX = 1024**4
FileCode = Literal[
    "invalid_input",
    "invalid_checkpoint",
    "invalid_response",
    "source_changed",
    "session_closed",
    "expired",
    "request_failed",
    "acceptance_unknown",
    "limit",
    "poll_limit",
    "deadline",
    "callback_failed",
    "integrity_failed",
]


class FileError(Exception):
    def __init__(self, code: FileCode, status: int | None = None):
        self.code, self.status = code, status
        super().__init__(f"SDK file operation stopped: {code}")


def _integer(n: Any, minimum: int, maximum: int) -> bool:
    return type(n) is int and minimum <= n <= maximum


def _uuid(v: Any) -> bool:
    return isinstance(v, str) and re.fullmatch(r"[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}", v) is not None


def _hash(v: Any) -> bool:
    return isinstance(v, str) and re.fullmatch(r"[a-f0-9]{64}", v) is not None


def _key(v: str) -> None:
    if not isinstance(v, str) or not v or len(v.encode()) > 1024 or re.search(r"[\x00-\x1f\x7f]", v):
        raise FileError("invalid_input")


def _content_type(v: str) -> str:
    if not isinstance(v, str) or not v or len(v) > 255 or re.search(r"[\x00-\x1f\x7f]", v):
        raise FileError("invalid_input")
    return v


def _etag(v: Any) -> bool:
    return isinstance(v, str) and 0 < len(v) <= 1024 and re.search(r"[\x00-\x1f\x7f]", v) is None


def _date(v: Any) -> float:
    try:
        date = datetime.fromisoformat(v.replace("Z", "+00:00"))
        if date.tzinfo is None:
            raise ValueError
        return date.timestamp()
    except (ValueError, TypeError, AttributeError):
        raise FileError("invalid_response") from None


def _scope(base: str, app: str, bucket: str) -> None:
    u = urlsplit(base)
    if (
        u.scheme not in ("https", "http")
        or not u.netloc
        or u.username
        or u.password
        or u.query
        or u.fragment
        or base.endswith("/")
        or not re.fullmatch(r"[a-z0-9][a-z0-9-]{0,62}", app)
        or not _uuid(bucket)
    ):
        raise FileError("invalid_input")


class FileSource(Protocol):
    @property
    def size(self) -> int: ...
    async def read(self, start: int, end: int) -> bytes: ...


@dataclass(frozen=True, slots=True)
class BytesFileSource:
    data: bytes

    @property
    def size(self) -> int:
        return len(self.data)

    async def read(self, start: int, end: int) -> bytes:
        return self.data[start:end]


class PathFileSource:
    """Random access without loading the entire file. Keep the path stable."""

    def __init__(self, path: str | Path):
        self.path = Path(path)
        self.size = self.path.stat().st_size

    async def read(self, start: int, end: int) -> bytes:
        def load() -> bytes:
            with self.path.open("rb") as stream:
                stream.seek(start)
                return stream.read(end - start)

        return await asyncio.to_thread(load)


@dataclass(frozen=True, slots=True)
class FilePart:
    part_number: int
    etag: str
    size_bytes: int


@dataclass(frozen=True, slots=True)
class FileCheckpoint:
    version: int
    base_url: str
    app_slug: str
    bucket: str
    key: str
    size_bytes: int
    source_digest: str
    part_digests: tuple[str, ...]
    upload_id: str
    part_size_bytes: int
    part_count: int
    content_type: str
    parts: tuple[FilePart, ...]

    def to_dict(self) -> dict[str, Any]:
        value = asdict(self)
        value["part_digests"] = list(self.part_digests)
        value["parts"] = [asdict(p) for p in self.parts]
        return value

    @classmethod
    def from_dict(cls, value: dict[str, Any]) -> FileCheckpoint:
        try:
            c = cls(
                **{
                    **value,
                    "parts": tuple(FilePart(**p) for p in value["parts"]),
                    "part_digests": tuple(value["part_digests"]),
                }
            )
            _scope(c.base_url, c.app_slug, c.bucket)
            _key(c.key)
            _content_type(c.content_type)
            if (
                type(c.version) is not int
                or c.version != 1
                or not _uuid(c.upload_id)
                or not _hash(c.source_digest)
                or not _integer(c.size_bytes, 1, MAX)
                or not _integer(c.part_size_bytes, 1, 64 * BLOCK)
                or not _integer(c.part_count, 1, 10000)
                or (c.size_bytes + c.part_size_bytes - 1) // c.part_size_bytes != c.part_count
                or len(c.part_digests) != c.part_count
                or any(not _hash(h) for h in c.part_digests)
                or len(c.parts) > c.part_count
            ):
                raise ValueError
            last = 0
            for p in c.parts:
                if (
                    not _integer(p.part_number, last + 1, c.part_count)
                    or p.size_bytes != _part_size(c.size_bytes, c.part_size_bytes, p.part_number)
                    or not _etag(p.etag)
                ):
                    raise ValueError
                last = p.part_number
            return c
        except (ValueError, TypeError, KeyError, AttributeError, FileError):
            raise FileError("invalid_checkpoint") from None


@dataclass(frozen=True, slots=True)
class FileProgress:
    key: str
    confirmed_bytes: int
    total_bytes: int


@dataclass(frozen=True, slots=True)
class FileUploadResult:
    key: str
    size_bytes: int
    etag: str
    version_id: str | None = None


class FileMetadata(TypedDict):
    size_bytes: int
    etag: str
    content_type: str


class FileObject(TypedDict):
    key: str
    size_bytes: int
    last_modified: str


class FilePage(TypedDict):
    items: list[FileObject]
    next_cursor: NotRequired[str]


@dataclass(frozen=True, slots=True)
class FileOptions:
    timeout: float
    max_bytes: int = 100 * BLOCK
    max_parts: int = 10000
    max_polls: int = 60
    poll_delay: float = 1.0
    on_checkpoint: Callable[[FileCheckpoint], None | Awaitable[None]] | None = None
    on_progress: Callable[[FileProgress], None | Awaitable[None]] | None = None

    def __post_init__(self) -> None:
        if (
            type(self.timeout) not in (float, int)
            or not math.isfinite(self.timeout)
            or not 0 < self.timeout <= 2147483.647
            or not _integer(self.max_bytes, 0, MAX)
            or not _integer(self.max_parts, 1, 10000)
            or not _integer(self.max_polls, 1, 10000)
            or type(self.poll_delay) not in (float, int)
            or not math.isfinite(self.poll_delay)
            or not 0 <= self.poll_delay <= 60
        ):
            raise FileError("invalid_input")


def _part_size(size: int, block: int, n: int) -> int:
    return min(block, size - (n - 1) * block)


async def _read(source: FileSource, start: int, end: int, check: Callable[[], None]) -> bytes:
    check()
    data = await source.read(start, end)
    check()
    if not isinstance(data, bytes) or len(data) != end - start:
        raise FileError("source_changed")
    return data


async def _fingerprint(source: FileSource, block: int, check: Callable[[], None]) -> tuple[str, tuple[str, ...]]:
    if not _integer(source.size, 0, MAX):
        raise FileError("invalid_input")
    digests = [
        hashlib.sha256(await _read(source, i, min(i + block, source.size), check)).digest()
        for i in range(0, source.size, block)
    ]
    return hashlib.sha256(b"".join(digests)).hexdigest(), tuple(h.hex() for h in digests)


async def file_source_digest(source: FileSource) -> str:
    """SHA-256 of concatenated binary SHA-256 digests of fixed 1 MiB blocks."""
    return (await _fingerprint(source, BLOCK, lambda: None))[0]


async def _callback(fn: Callable[[Any], Any] | None, value: Any, check: Callable[[], None]) -> None:
    check()
    if fn:
        try:
            result = fn(value)
            if inspect.isawaitable(result):
                await result
        except Exception:
            check()
            raise FileError("callback_failed") from None
    check()


class _Operation:
    def __init__(
        self,
        owner: object,
        client: httpx.AsyncClient,
        data: httpx.AsyncClient,
        path: str,
        options: FileOptions,
        check: Callable[[], None],
    ):
        self.owner, self.client, self.data, self.path, self.options, self.check = (
            owner,
            client,
            data,
            path,
            options,
            check,
        )

    async def request(self, suffix: str, method: str = "GET", body: Any = None, *, absolute: bool = False) -> Any:
        self.check()

        async def send() -> Any:
            kwargs: dict[str, Any] = {"headers": {"Accept-Encoding": "identity"}}
            if method != "GET":
                kwargs["headers"]["Idempotency-Key"] = str(uuid.uuid4())
            if body is not None:
                kwargs["json"] = body
            async with self.client.stream(method, suffix if absolute else self.path + suffix, **kwargs) as response:
                response.raise_for_status()
                raw = bytearray()
                async for chunk in response.aiter_bytes(chunk_size=8192):
                    self.check()
                    if len(raw) + len(chunk) > BLOCK:
                        raise FileError("limit")
                    raw.extend(chunk)
                self.check()
                return json.loads(raw) if raw else None

        try:
            return await run_request(self.owner, send, RequestOptions(timeout=0, retry=False), 0)
        except Exception as e:
            self.check()
            status = e.response.status_code if isinstance(e, httpx.HTTPStatusError) else getattr(e, "status", None)
            raise FileError(
                "acceptance_unknown" if method != "GET" and (status is None or status >= 500) else "request_failed",
                status,
            ) from None

    async def signed(self, key: str, method: str, **options: Any) -> dict[str, Any]:
        _key(key)
        value = await self.request("/signed-url", "POST", {"key": key, "method": method, **options})
        if not isinstance(value, dict):
            raise FileError("invalid_response")
        return value

    async def transfer(self, signed: dict[str, Any], method: str, data: bytes | None = None) -> httpx.Response:
        self.check()
        try:
            u = urlsplit(signed["url"])
            headers = httpx.Headers(signed["headers"])
            if (
                signed["method"] != method
                or u.username
                or u.password
                or u.fragment
                or not (u.scheme == "https" or u.scheme == "http" and u.hostname in ("localhost", "127.0.0.1", "::1"))
                or _date(signed["expires_at"]) <= datetime.now().timestamp()
                or any(h in headers for h in ("authorization", "cookie", "proxy-authorization", "host"))
            ):
                raise FileError("invalid_response")
            if data is not None and "content-length" in headers and headers["content-length"] != str(len(data)):
                raise FileError("invalid_response")
            # A raw request excludes the client's cookie jar and default credentials.
            request = httpx.Request(method, signed["url"], headers=headers, content=data)
            response = await self.data.send(request, stream=True)
            try:
                self.check()
                response.raise_for_status()
                return response
            except BaseException:
                await response.aclose()
                raise
        except (KeyError, ValueError, TypeError):
            raise FileError("invalid_response") from None
        except FileError:
            raise
        except Exception as e:
            self.check()
            status = e.response.status_code if isinstance(e, httpx.HTTPStatusError) else getattr(e, "status", None)
            raise FileError(
                "acceptance_unknown" if method == "PUT" and (status is None or status >= 500) else "request_failed",
                status,
            ) from None


class FileBucket:
    """An app/bucket handle. Credentials and HTTP clients are snapped per operation."""

    def __init__(
        self, owner: FaaSClient, *, app_slug: str, bucket: str, data_transport: httpx.AsyncBaseTransport | None = None
    ):
        self._owner, self.app_slug, self.bucket = owner, app_slug, bucket
        self.base_url = str(owner.async_httpx_client.base_url).rstrip("/")
        _scope(self.base_url, app_slug, bucket)
        self._data_transport = data_transport
        self._path = f"/v1/apps/{quote(app_slug, safe='')}/buckets/{bucket}"

    def _invoke(self, options: FileOptions, work: Callable[[_Operation], Awaitable[T]]) -> Coroutine[Any, Any, T]:
        source = self._owner.async_httpx_client
        if str(source.base_url).rstrip("/") != self.base_url:
            raise FileError("invalid_checkpoint")
        client = snapshot_async_client(source)
        client.follow_redirects = False
        transport_owner = owned_async_transport(source).owner

        async def execute() -> T:
            active = True
            deadline = asyncio.get_running_loop().time() + options.timeout

            def check() -> None:
                if not active:
                    raise asyncio.CancelledError
                if asyncio.get_running_loop().time() >= deadline:
                    raise FileError("deadline")

            async def run() -> T:
                async with httpx.AsyncClient(
                    transport=self._data_transport,
                    follow_redirects=False,
                    trust_env=False,
                    timeout=None,
                    headers={"Accept-Encoding": "identity"},
                ) as data:
                    try:
                        check()
                        result = await work(_Operation(transport_owner, client, data, self._path, options, check))
                        check()
                        return result
                    finally:
                        await client.aclose()

            try:
                return await run_request(
                    object(), run, RequestOptions(timeout=options.timeout, retry=False), options.timeout
                )
            except RequestTimeoutError:
                raise FileError("deadline") from None
            except FileError:
                raise
            except Exception:
                raise FileError("request_failed") from None
            finally:
                active = False

        return execute()

    def _bound(self, c: FileCheckpoint) -> FileCheckpoint:
        c = FileCheckpoint.from_dict(c.to_dict())
        if (c.base_url, c.app_slug, c.bucket) != (self.base_url, self.app_slug, self.bucket):
            raise FileError("invalid_checkpoint")
        return c

    def _session(self, s: Any, key: str, size: int, c: FileCheckpoint | None = None) -> dict[str, Any]:
        try:
            if (
                not isinstance(s, dict)
                or not _uuid(s["id"])
                or s["key"] != key
                or s["size_bytes"] != size
                or not _integer(s["part_size_bytes"], 1, 64 * BLOCK)
                or not _integer(s["part_count"], 1, 10000)
                or (size + s["part_size_bytes"] - 1) // s["part_size_bytes"] != s["part_count"]
                or s["state"]
                not in (
                    "initiating",
                    "active",
                    "completing",
                    "completing_conditional",
                    "aborting",
                    "completed",
                    "aborted",
                )
                or c
                and (s["id"], s["part_size_bytes"], s["part_count"], s["content_type"])
                != (c.upload_id, c.part_size_bytes, c.part_count, c.content_type)
            ):
                raise ValueError
            _content_type(s["content_type"])
            expiry = _date(s["expires_at"])
            if s["state"] != "completed" and expiry <= datetime.now().timestamp():
                raise FileError("expired")
            return s
        except (KeyError, ValueError, TypeError):
            raise FileError("invalid_response") from None

    async def _upload(
        self, key: str, source: FileSource, op: _Operation, c: FileCheckpoint | None, content_type: str
    ) -> FileUploadResult:
        _key(key)
        if not _integer(source.size, 1, op.options.max_bytes):
            raise FileError("limit")
        suffix = "/multipart-uploads" + ("/" + c.upload_id if c else "")
        s = self._session(
            await op.request(
                suffix,
                "GET" if c else "POST",
                None if c else {"key": key, "size_bytes": source.size, "content_type": _content_type(content_type)},
            ),
            key,
            source.size,
            c,
        )
        for i in range(op.options.max_polls):
            if s["state"] != "initiating":
                break
            if i + 1 == op.options.max_polls:
                raise FileError("poll_limit")
            await asyncio.sleep(op.options.poll_delay)
            s = self._session(await op.request("/multipart-uploads/" + s["id"]), key, source.size, c)
        if (c is None and s["state"] != "active") or (
            c is not None
            and s["state"] in ("completed", "completing", "completing_conditional")
            and len(c.parts) != c.part_count
        ):
            raise FileError("session_closed")
        if s["part_count"] > op.options.max_parts:
            raise FileError("limit")
        digest, part_digests = await _fingerprint(source, s["part_size_bytes"], op.check)
        if c and (digest != c.source_digest or part_digests != c.part_digests):
            raise FileError("source_changed")
        c = c or FileCheckpoint(
            1,
            self.base_url,
            self.app_slug,
            self.bucket,
            key,
            source.size,
            digest,
            part_digests,
            s["id"],
            s["part_size_bytes"],
            s["part_count"],
            s["content_type"],
            (),
        )
        await _callback(op.options.on_checkpoint, c, op.check)
        suffix = "/multipart-uploads/" + c.upload_id
        if s["state"] in ("aborted", "aborting"):
            raise FileError("session_closed")
        if s["state"] == "active":
            known: dict[int, dict[str, Any]] = {}
            marker = 0
            for page in range((op.options.max_parts + 999) // 1000 + 1):
                p = await op.request(
                    suffix + "/parts?limit=1000" + ("&part_number_marker=" + str(marker) if marker else "")
                )
                if not isinstance(p, dict) or not isinstance(p.get("items"), list) or len(p["items"]) > 1000:
                    raise FileError("invalid_response")
                last = marker
                for item in p["items"]:
                    if (
                        not _integer(item.get("part_number"), last + 1, c.part_count)
                        or item.get("size_bytes") != _part_size(c.size_bytes, c.part_size_bytes, item["part_number"])
                        or not _etag(item.get("etag"))
                    ):
                        raise FileError("invalid_response")
                    last = item["part_number"]
                    known[last] = item
                if "next_part_number_marker" not in p:
                    break
                if (
                    not _integer(p["next_part_number_marker"], marker + 1, c.part_count)
                    or p["next_part_number_marker"] != last
                ):
                    raise FileError("invalid_response")
                marker = last
            else:
                raise FileError("limit")
            parts = [p for p in c.parts if known.get(p.part_number, {}).get("etag") == p.etag]
            c = FileCheckpoint.from_dict({**c.to_dict(), "parts": [asdict(p) for p in parts]})
            await _callback(op.options.on_checkpoint, c, op.check)
            for n in range(1, c.part_count + 1):
                if any(p.part_number == n for p in parts):
                    continue
                start = (n - 1) * c.part_size_bytes
                data = await _read(source, start, start + _part_size(c.size_bytes, c.part_size_bytes, n), op.check)
                if hashlib.sha256(data).hexdigest() != c.part_digests[n - 1]:
                    raise FileError("source_changed")
                signed = await op.request(f"{suffix}/parts/{n}/signed-url", "POST", {})
                response = await op.transfer(signed, "PUT", data)
                tag = response.headers.get("etag")
                await response.aclose()
                if not _etag(tag):
                    raise FileError("invalid_response")
                parts.append(FilePart(n, str(tag), len(data)))
                parts.sort(key=lambda p: p.part_number)
                c = FileCheckpoint.from_dict({**c.to_dict(), "parts": [asdict(p) for p in parts]})
                await _callback(op.options.on_checkpoint, c, op.check)
                await _callback(
                    op.options.on_progress, FileProgress(key, sum(p.size_bytes for p in parts), source.size), op.check
                )
            s = self._session(
                await op.request(
                    suffix + "/complete",
                    "POST",
                    {"parts": [{"part_number": p.part_number, "etag": p.etag} for p in parts]},
                ),
                key,
                source.size,
                c,
            )
        for i in range(op.options.max_polls):
            op.check()
            if s["state"] == "completed":
                if not _etag(s.get("etag")):
                    raise FileError("invalid_response")
                return FileUploadResult(key, source.size, s["etag"], s.get("version_id"))
            if s["state"] not in ("initiating", "completing", "completing_conditional"):
                raise FileError("session_closed")
            if i + 1 < op.options.max_polls:
                await asyncio.sleep(op.options.poll_delay)
                s = self._session(await op.request(suffix), key, source.size, c)
        raise FileError("poll_limit")

    def upload(
        self, key: str, source: FileSource, *, options: FileOptions, content_type: str = "application/octet-stream"
    ) -> Coroutine[Any, Any, FileUploadResult]:
        return self._invoke(options, lambda op: self._upload(key, source, op, None, content_type))

    def resume(
        self, checkpoint: FileCheckpoint, source: FileSource, *, options: FileOptions
    ) -> Coroutine[Any, Any, FileUploadResult]:
        c = self._bound(checkpoint)
        if source.size != c.size_bytes:
            raise FileError("source_changed")
        return self._invoke(options, lambda op: self._upload(c.key, source, op, c, c.content_type))

    def abort(self, checkpoint: FileCheckpoint, *, options: FileOptions) -> Coroutine[Any, Any, None]:
        c = self._bound(checkpoint)

        async def work(op: _Operation) -> None:
            await op.request("/multipart-uploads/" + c.upload_id, "DELETE")

        return self._invoke(options, work)

    async def _confirm(self, receipt: str, key: str, size: int, op: _Operation) -> FileUploadResult:
        _key(key)
        if not _uuid(receipt) or not _integer(size, 0, op.options.max_bytes):
            raise FileError("invalid_input")
        for i in range(op.options.max_polls):
            r = await op.request("/write-receipts/" + receipt)
            if (
                not isinstance(r, dict)
                or (r.get("id"), r.get("bucket_id"), r.get("key"), r.get("bytes"), r.get("operation"))
                != (receipt, self.bucket, key, size, "put")
                or r.get("status") not in ("pending", "completed", "failed")
            ):
                raise FileError("invalid_response")
            if r["status"] == "failed":
                raise FileError("request_failed")
            if r["status"] == "completed":
                if not _etag(r.get("etag")):
                    raise FileError("invalid_response")
                return FileUploadResult(key, size, r["etag"])
            if i + 1 < op.options.max_polls:
                await asyncio.sleep(op.options.poll_delay)
        raise FileError("poll_limit")

    def confirm(
        self, receipt: str, key: str, size: int, *, options: FileOptions
    ) -> Coroutine[Any, Any, FileUploadResult]:
        return self._invoke(options, lambda op: self._confirm(receipt, key, size, op))

    def put(
        self,
        key: str,
        data: bytes,
        *,
        options: FileOptions,
        content_type: str = "application/octet-stream",
        if_none_match: str | None = None,
        on_receipt: Callable[[str], Any] | None = None,
    ) -> Coroutine[Any, Any, FileUploadResult]:
        data = bytes(data)

        async def work(op: _Operation) -> FileUploadResult:
            if len(data) > options.max_bytes:
                raise FileError("limit")
            if if_none_match not in (None, "*"):
                raise FileError("invalid_input")
            signed = await op.signed(
                key,
                "PUT",
                size_bytes=len(data),
                content_type=_content_type(content_type),
                **({"if_none_match": "*"} if if_none_match else {}),
            )
            if not _uuid(signed.get("upload_id")):
                raise FileError("invalid_response")
            await _callback(on_receipt, signed["upload_id"], op.check)
            response = await op.transfer(signed, "PUT", data)
            await response.aclose()
            return await self._confirm(signed["upload_id"], key, len(data), op)

        return self._invoke(options, work)

    async def _download(self, key: str, sink: Callable[[bytes], Any], version_id: str | None, op: _Operation) -> int:
        if version_id is not None and not _uuid(version_id):
            raise FileError("invalid_input")
        signed = await op.signed(key, "GET", **({"version_id": version_id} if version_id else {}))
        response = await op.transfer(signed, "GET")
        size = 0
        try:
            length = response.headers.get("content-length")
            if length is not None and (not re.fullmatch(r"\d+", length) or int(length) > op.options.max_bytes):
                raise FileError("limit")
            async for chunk in response.aiter_bytes(chunk_size=65536):
                op.check()
                size += len(chunk)
                if size > op.options.max_bytes:
                    raise FileError("limit")
                await _callback(sink, chunk, op.check)
            if length is not None and size != int(length):
                raise FileError("integrity_failed")
            return size
        finally:
            await response.aclose()

    def download_to(
        self, key: str, sink: Callable[[bytes], Any], *, options: FileOptions, version_id: str | None = None
    ) -> Coroutine[Any, Any, int]:
        return self._invoke(options, lambda op: self._download(key, sink, version_id, op))

    def download(self, key: str, *, options: FileOptions, version_id: str | None = None) -> Coroutine[Any, Any, bytes]:
        async def work(op: _Operation) -> bytes:
            chunks: list[bytes] = []
            await self._download(key, chunks.append, version_id, op)
            return b"".join(chunks)

        return self._invoke(options, work)

    def metadata(
        self, key: str, *, options: FileOptions, version_id: str | None = None
    ) -> Coroutine[Any, Any, FileMetadata]:
        async def work(op: _Operation) -> FileMetadata:
            if version_id is not None and not _uuid(version_id):
                raise FileError("invalid_input")
            r = await op.transfer(
                await op.signed(key, "HEAD", **({"version_id": version_id} if version_id else {})), "HEAD"
            )
            await r.aclose()
            n, tag = r.headers.get("content-length"), r.headers.get("etag")
            if n is None or not re.fullmatch(r"\d+", n) or not _integer(int(n), 0, MAX) or not _etag(tag):
                raise FileError("invalid_response")
            return {
                "size_bytes": int(n),
                "etag": cast(str, tag),
                "content_type": r.headers.get("content-type", "application/octet-stream"),
            }

        return self._invoke(options, work)

    def list(
        self, *, options: FileOptions, prefix: str | None = None, cursor: str | None = None, limit: int = 100
    ) -> Coroutine[Any, Any, FilePage]:
        async def work(op: _Operation) -> FilePage:
            if (
                not _integer(limit, 1, 1000)
                or cursor is not None
                and (not isinstance(cursor, str) or len(cursor) > 8192)
            ):
                raise FileError("invalid_input")
            query = {
                "limit": str(limit),
                **({"prefix": prefix} if prefix is not None else {}),
                **({"cursor": cursor} if cursor is not None else {}),
            }
            p = await op.request("/objects?" + urlencode(query))
            if (
                not isinstance(p, dict)
                or not isinstance(p.get("items"), list)
                or len(p["items"]) > limit
                or any(
                    not isinstance(i.get("key"), str)
                    or not _integer(i.get("size_bytes"), 0, MAX)
                    or not _date(i.get("last_modified"))
                    for i in p["items"]
                )
                or "next_cursor" in p
                and (not isinstance(p["next_cursor"], str) or not 0 < len(p["next_cursor"]) <= 8192)
            ):
                raise FileError("invalid_response")
            return cast(FilePage, p)

        return self._invoke(options, work)

    def delete(self, key: str, *, options: FileOptions) -> Coroutine[Any, Any, None]:
        _key(key)

        async def work(op: _Operation) -> None:
            await op.request("/objects?" + urlencode({"key": key}), "DELETE")

        return self._invoke(options, work)

    def job_store(self, *, options: FileOptions, prefix: str = "jobs/") -> Coroutine[Any, Any, JobStore]:
        if not re.fullmatch(r"[A-Za-z0-9_-][A-Za-z0-9/_-]{0,127}/", prefix):
            raise FileError("invalid_input")

        async def work(op: _Operation) -> JobStore:
            app = await op.request("/v1/apps/" + quote(self.app_slug, safe=""), absolute=True)
            if not isinstance(app, dict) or not _uuid(app.get("id")):
                raise FileError("invalid_response")
            return ManagedFileJobStore(self, f"obj://{app['id']}/{self.bucket}/{prefix}", prefix, options.timeout)

        return self._invoke(options, work)


class ManagedFileJobStore:
    def __init__(self, files: FileBucket, root: str, prefix: str, timeout: float):
        self.files, self.root, self.prefix, self.timeout = files, root, prefix, timeout

    async def put(self, data: bytes, sha256: str, context: JobContext) -> str:
        context.check_cancelled()
        if len(data) > JOB_MAX_BYTES or hashlib.sha256(data).hexdigest() != sha256:
            raise JobError("integrity_failed")
        options = FileOptions(timeout=self.timeout, max_bytes=JOB_MAX_BYTES)
        try:
            await self.files.put(
                self.prefix + sha256, data, options=options, content_type="application/json", if_none_match="*"
            )
        except FileError as e:
            if e.status not in (409, 412):
                raise
        context.check_cancelled()
        uri = self.root + sha256
        await self.get(uri, context)
        return uri

    async def get(self, uri: str, context: JobContext) -> bytes:
        context.check_cancelled()
        if not uri.startswith(self.root) or not _hash(uri[len(self.root) :]):
            raise JobError("invalid_reference")
        sha = uri[len(self.root) :]
        data = await self.files.download(
            self.prefix + sha, options=FileOptions(timeout=self.timeout, max_bytes=JOB_MAX_BYTES)
        )
        context.check_cancelled()
        if hashlib.sha256(data).hexdigest() != sha:
            raise JobError("integrity_failed")
        return data
