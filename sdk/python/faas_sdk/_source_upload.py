"""Checkpointed source archives and explicit, receipt-reconciled deployment commit."""

from __future__ import annotations

import asyncio
import copy
import hashlib
import inspect
import json
import math
import os
import re
import stat
import struct
import tempfile
from collections.abc import Awaitable, Callable, Coroutine, Mapping
from dataclasses import asdict, dataclass, replace
from pathlib import Path
from typing import TYPE_CHECKING, Any, Literal, TypeVar
from urllib.parse import urlsplit
from uuid import uuid4

import httpx

from ._async_request import (
    RequestOptions,
    RequestTimeoutError,
    owned_async_transport,
    run_request,
    snapshot_async_client,
)
from ._rfc7807 import FaasProblemError
from .types import UNSET

if TYPE_CHECKING:
    from ._wrapper import FaaSClient
    from .models.deployment_submission_receipt import DeploymentSubmissionReceipt
    from .models.upload_deploy_options import UploadDeployOptions

_GRAIN = 1024 * 1024
_SAFE = 2**53 - 1
T = TypeVar("T")
SourceUploadStage = Literal["new", "uploading", "ready", "committing", "accepted"]
SourceUploadCode = Literal[
    "invalid_checkpoint",
    "source_changed",
    "invalid_response",
    "session_expired",
    "session_closed",
    "recovery_limit",
    "request_failed",
    "checkpoint_failed",
    "acceptance_unknown",
    "not_ready",
    "timeout",
]


def _base(value: str) -> str:
    parsed = urlsplit(value)
    if (
        parsed.scheme not in ("http", "https")
        or not parsed.netloc
        or parsed.username
        or parsed.password
        or parsed.query
        or parsed.fragment
    ):
        raise ValueError("API base URL must be HTTP(S) without credentials, query or fragment")
    return str(httpx.URL(value)).rstrip("/")


def _integer(value: Any) -> bool:
    return type(value) is int and 0 <= value <= _SAFE


def _id(value: Any) -> bool:
    return isinstance(value, str) and bool(re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.:-]{0,254}", value))


def _canonical(value: Any) -> Any:
    # Tagged binary64 numbers make metadata fingerprints portable across SDKs.
    if value is None:
        return ["null"]
    if isinstance(value, bool):
        return ["bool", value]
    if isinstance(value, str):
        value.encode("utf-8")
        return ["str", value]
    if isinstance(value, (int, float)) and not isinstance(value, bool):
        if not -_SAFE <= value <= _SAFE or not math.isfinite(value):
            raise ValueError("Deployment metadata requires finite safe numbers")
        return ["num", struct.pack(">d", float(value) if value else 0.0).hex()]
    if isinstance(value, list):
        return ["list", [_canonical(item) for item in value]]
    if isinstance(value, dict) and all(isinstance(key, str) for key in value):
        return ["dict", [[key, _canonical(value[key])] for key in sorted(value)]]
    raise TypeError("Deployment metadata must be JSON data")


def _options_hash(value: dict[str, Any]) -> str:
    return hashlib.sha256(json.dumps(_canonical(value), ensure_ascii=False, separators=(",", ":")).encode()).hexdigest()


@dataclass(frozen=True, slots=True)
class SourceUploadCheckpoint:
    version: int
    base_url: str
    app_slug: str
    total_size: int
    sha256_hex: str
    options_sha256: str
    start_key: str
    attempt_id: str
    commit_key: str
    stage: SourceUploadStage = "new"
    upload_id: str | None = None
    chunk_size: int = 0
    received_bytes: int = 0
    pending_end: int | None = None
    deployment_id: str | None = None
    expires_at: str | None = None

    def __post_init__(self) -> None:
        if (
            type(self.version) is not int
            or self.version != 1
            or not isinstance(self.base_url, str)
            or _base(self.base_url) != self.base_url
            or not isinstance(self.app_slug, str)
            or not re.fullmatch(r"[a-z0-9](?:[a-z0-9-]*[a-z0-9])?", self.app_slug)
            or not _integer(self.total_size)
            or not self.total_size
            or not isinstance(self.sha256_hex, str)
            or not re.fullmatch("[0-9a-f]{64}", self.sha256_hex)
            or not isinstance(self.options_sha256, str)
            or not re.fullmatch("[0-9a-f]{64}", self.options_sha256)
            or not all(_id(item) for item in (self.start_key, self.attempt_id, self.commit_key))
            or self.stage not in ("new", "uploading", "ready", "committing", "accepted")
            or not _integer(self.received_bytes)
            or self.received_bytes > self.total_size
            or not _integer(self.chunk_size)
        ):
            raise ValueError("Invalid source upload checkpoint")
        if self.stage == "new":
            valid = (
                self.upload_id is None
                and self.chunk_size == self.received_bytes == 0
                and self.pending_end is None
                and self.expires_at is None
            )
        else:
            valid = (
                _id(self.upload_id)
                and self.chunk_size > 0
                and isinstance(self.expires_at, str)
                and bool(self.expires_at)
            )
        if self.pending_end is not None:
            valid = (
                valid
                and self.stage == "uploading"
                and _integer(self.pending_end)
                and self.received_bytes < self.pending_end <= self.total_size
            )
        if self.stage in ("ready", "committing", "accepted"):
            valid = valid and self.received_bytes == self.total_size and self.pending_end is None
        if self.stage == "accepted":
            valid = valid and _id(self.deployment_id)
        elif self.deployment_id is not None:
            valid = False
        if not valid:
            raise ValueError("Invalid source upload checkpoint")

    def to_dict(self) -> dict[str, Any]:
        return asdict(self)

    @classmethod
    def from_dict(cls, value: Mapping[str, Any]) -> SourceUploadCheckpoint:
        try:
            if set(value) != set(cls.__dataclass_fields__):
                raise ValueError("Invalid source upload checkpoint")
            return cls(**dict(value))
        except (TypeError, ValueError, AttributeError):
            raise ValueError("Invalid source upload checkpoint") from None


def save_source_upload_checkpoint(path: str | os.PathLike[str], checkpoint: SourceUploadCheckpoint) -> None:
    """Atomic local save (0600). Use one writer per checkpoint; do not store credentials."""
    destination = Path(path)
    descriptor, temporary = tempfile.mkstemp(prefix="." + destination.name + ".", dir=destination.parent)
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8") as output:
            json.dump(checkpoint.to_dict(), output, ensure_ascii=False, separators=(",", ":"))
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary, destination)
        if os.name != "nt":
            directory = os.open(destination.parent, os.O_RDONLY)
            try:
                os.fsync(directory)
            finally:
                os.close(directory)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def load_source_upload_checkpoint(path: str | os.PathLike[str]) -> SourceUploadCheckpoint:
    return SourceUploadCheckpoint.from_dict(json.loads(Path(path).read_text(encoding="utf-8")))


@dataclass(frozen=True, slots=True)
class SourceUploadProgress:
    stage: SourceUploadStage
    received_bytes: int
    total_size: int


@dataclass(frozen=True, kw_only=True)
class SourceUploadOptions(RequestOptions):
    timeout: float
    on_checkpoint: Callable[[SourceUploadCheckpoint], None | Awaitable[None]]
    max_recoveries: int = 3
    on_progress: Callable[[SourceUploadProgress], None | Awaitable[None]] | None = None

    def __post_init__(self) -> None:
        if (
            not isinstance(self.timeout, (float, int))
            or isinstance(self.timeout, bool)
            or not 0 < self.timeout <= (2**31 - 1) / 1000
        ):
            raise ValueError("timeout must be positive finite seconds at most 2147483.647")
        RequestOptions.__post_init__(self)
        if not _integer(self.max_recoveries) or self.max_recoveries > 100:
            raise ValueError("max_recoveries must be an integer from 0 through 100")
        if not callable(self.on_checkpoint) or self.on_progress is not None and not callable(self.on_progress):
            raise TypeError("Checkpoint/progress callbacks must be callable")


class SourceUploadError(Exception):
    def __init__(
        self, code: SourceUploadCode, checkpoint: SourceUploadCheckpoint | None, cause: Exception | None = None
    ):
        self.code, self.checkpoint, self.cause = code, checkpoint, cause
        super().__init__(f"SDK source upload stopped: {code}")


class SourceUploadTimeoutError(SourceUploadError, RequestTimeoutError):
    pass


def _status(error: Exception) -> int | None:
    if isinstance(error, FaasProblemError):
        return error.status
    if isinstance(error, httpx.HTTPStatusError):
        return error.response.status_code
    return None


def _read(path: str, offset: int, length: int, total: int) -> bytes:
    # The thread owns its descriptor, including after caller cancellation.
    with open(path, "rb") as source:
        info = os.fstat(source.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_size != total:
            raise ValueError("Source changed")
        source.seek(offset)
        data = source.read(length)
        if len(data) != length:
            raise ValueError("Source changed")
        return data


class _Operation:
    def __init__(self, owner: FaaSClient, options: SourceUploadOptions, checkpoint: SourceUploadCheckpoint | None):
        if not isinstance(options, SourceUploadOptions):
            raise TypeError("options must be SourceUploadOptions")
        source = owner.async_httpx_client
        self.owner = owned_async_transport(source).owner
        self.client = snapshot_async_client(source)
        self.client.follow_redirects = False
        self.base = _base(str(source.base_url))
        self.options, self.checkpoint = options, checkpoint
        self.active = True
        self.deadline = float("inf")
        self.recoveries = 0
        if checkpoint is not None and (
            not isinstance(checkpoint, SourceUploadCheckpoint) or checkpoint.base_url != self.base
        ):
            raise SourceUploadError("invalid_checkpoint", None)

    def check(self) -> None:
        if not self.active:
            raise asyncio.CancelledError()
        if asyncio.get_running_loop().time() >= self.deadline:
            raise SourceUploadTimeoutError("timeout", self.checkpoint)

    def error(self, code: SourceUploadCode, cause: Exception | None = None) -> SourceUploadError:
        return SourceUploadError(code, self.checkpoint, cause)

    async def emit(self, checkpoint: SourceUploadCheckpoint) -> None:
        self.check()
        self.checkpoint = checkpoint
        try:
            result = self.options.on_checkpoint(checkpoint)
            if inspect.isawaitable(result):
                await result
        except Exception as error:
            raise self.error("checkpoint_failed", error) from error
        self.check()
        if self.options.on_progress:
            progress = self.options.on_progress(
                SourceUploadProgress(checkpoint.stage, checkpoint.received_bytes, checkpoint.total_size)
            )
            if inspect.isawaitable(progress):
                await progress
            self.check()

    async def request(self, kwargs: dict[str, Any]) -> httpx.Response:
        self.check()

        async def send() -> httpx.Response:
            self.check()
            response = await self.client.request(**kwargs)
            response.raise_for_status()
            return response

        return await run_request(
            self.owner,
            send,
            RequestOptions(
                timeout=0,
                retry=False if kwargs["method"].lower() != "get" else self.options.retry,
                on_response=self.options.on_response,
            ),
            0,
        )

    def recover(self, error: Exception) -> None:
        status = _status(error)
        if status == 410:
            raise self.error("session_expired", error) from error
        if status is not None and status != 409 and status < 500:
            raise self.error("request_failed", error) from error
        self.recoveries += 1
        if self.recoveries > self.options.max_recoveries:
            raise self.error("recovery_limit", error) from error

    async def session(self) -> SourceUploadCheckpoint:
        from .api.uploads import get_upload_session

        checkpoint = self.checkpoint
        assert checkpoint is not None and checkpoint.upload_id is not None
        try:
            body = (await self.request(get_upload_session._get_kwargs(checkpoint.upload_id))).json()
        except Exception as error:
            if _status(error) == 410:
                raise self.error("session_expired", error) from error
            raise
        if (
            not isinstance(body, dict)
            or body.get("upload_id") != checkpoint.upload_id
            or body.get("app_slug") != checkpoint.app_slug
            or body.get("total_size") != checkpoint.total_size
            or type(body.get("total_size")) is not int
            or body.get("chunk_size") != checkpoint.chunk_size
            or not _integer(body.get("received_bytes"))
            or body["received_bytes"] > checkpoint.total_size
            or body["received_bytes"] < checkpoint.received_bytes
            or not isinstance(body.get("expires_at"), str)
        ):
            raise self.error("invalid_response")
        state = body.get("status")
        if state == "expired":
            raise self.error("session_expired")
        if state == "cancelled":
            raise self.error("session_closed")
        if state == "committed":
            if not _id(body.get("deployment_id")) or body["received_bytes"] != checkpoint.total_size:
                raise self.error("invalid_response")
            return replace(
                checkpoint,
                stage="accepted",
                received_bytes=checkpoint.total_size,
                pending_end=None,
                deployment_id=body["deployment_id"],
            )
        if state != "open" or body["received_bytes"] > (checkpoint.pending_end or checkpoint.received_bytes):
            raise self.error("invalid_response")
        return replace(checkpoint, received_bytes=body["received_bytes"], pending_end=None)

    async def run(self, work: Callable[[], Coroutine[Any, Any, T]]) -> T:
        self.deadline = asyncio.get_running_loop().time() + self.options.timeout
        try:
            return await run_request(object(), work, RequestOptions(timeout=self.options.timeout), self.options.timeout)
        except RequestTimeoutError as error:
            if isinstance(error, SourceUploadTimeoutError):
                raise
            if asyncio.get_running_loop().time() < self.deadline:
                raise self.error("request_failed", error) from error
            raise SourceUploadTimeoutError("timeout", self.checkpoint, error) from None
        except SourceUploadError:
            raise
        except Exception as error:
            raise self.error("request_failed", error) from error
        finally:
            self.active = False
            await self.client.aclose()


def upload_source(
    owner: FaaSClient,
    path: str | os.PathLike[str],
    app_slug: str,
    *,
    options: SourceUploadOptions,
    checkpoint: SourceUploadCheckpoint | None,
    deploy_options: UploadDeployOptions | None,
) -> Coroutine[Any, Any, SourceUploadCheckpoint]:
    from .api.uploads import append_upload, start_upload
    from .models.upload_start_request import UploadStartRequest

    operation = _Operation(owner, options, checkpoint)
    source_path = os.path.abspath(os.fspath(path))
    deploy_options = copy.deepcopy(deploy_options, {id(UNSET): UNSET})
    metadata = deploy_options.to_dict() if deploy_options is not None else {}
    metadata_hash = _options_hash({"present": deploy_options is not None, "value": metadata})

    async def work() -> SourceUploadCheckpoint:
        try:
            info = await asyncio.to_thread(os.stat, source_path)
            if not stat.S_ISREG(info.st_mode) or not _integer(info.st_size) or not info.st_size:
                raise ValueError("Source must be a nonempty regular archive")
            size = info.st_size
            fingerprint = hashlib.sha256()
            grains = []
            for offset in range(0, size, _GRAIN):
                operation.check()
                data = await asyncio.to_thread(_read, source_path, offset, min(_GRAIN, size - offset), size)
                fingerprint.update(data)
                grains.append(hashlib.sha256(data).digest())
            operation.check()
        except (OSError, ValueError) as error:
            raise operation.error("source_changed", error) from error
        current = operation.checkpoint
        if current is None:
            current = SourceUploadCheckpoint(
                1,
                operation.base,
                app_slug,
                size,
                fingerprint.hexdigest(),
                metadata_hash,
                str(uuid4()),
                str(uuid4()),
                str(uuid4()),
            )
            await operation.emit(current)
        elif current.app_slug != app_slug or current.options_sha256 != metadata_hash:
            raise operation.error("invalid_checkpoint")
        elif current.total_size != size or current.sha256_hex != fingerprint.hexdigest():
            raise operation.error("source_changed")
        if current.stage == "new":
            body = UploadStartRequest(
                app_slug=app_slug,
                total_size=size,
                sha256_hex=current.sha256_hex,
                deploy_options=deploy_options if deploy_options is not None else UNSET,
            )
            kwargs = start_upload._get_kwargs(body=body)
            kwargs["headers"]["Idempotency-Key"] = current.start_key
            while True:
                try:
                    response = await operation.request(kwargs)
                    break
                except Exception as error:
                    operation.recover(error)
            value = response.json()
            if (
                not isinstance(value, dict)
                or not _id(value.get("upload_id"))
                or value.get("total_size") != size
                or not _integer(value.get("chunk_size"))
                or not value["chunk_size"]
                or not isinstance(value.get("expires_at"), str)
                or not value["expires_at"]
            ):
                raise operation.error("invalid_response")
            current = replace(
                current,
                stage="uploading",
                upload_id=value["upload_id"],
                chunk_size=value["chunk_size"],
                expires_at=value["expires_at"],
            )
            await operation.emit(current)
        current = await operation.session()
        await operation.emit(current)
        if current.stage in ("committing", "accepted"):
            return current  # Commit recovery is a separate, explicit operation.
        while current.received_bytes < size:
            offset = current.received_bytes
            end = min(size, offset + current.chunk_size, offset + 16 * _GRAIN)
            parts = []
            position = offset
            while position < end:
                grain_offset = position // _GRAIN * _GRAIN
                try:
                    grain = await asyncio.to_thread(
                        _read, source_path, grain_offset, min(_GRAIN, size - grain_offset), size
                    )
                except (OSError, ValueError) as error:
                    raise operation.error("source_changed", error) from error
                operation.check()
                if hashlib.sha256(grain).digest() != grains[position // _GRAIN]:
                    raise operation.error("source_changed")
                piece = grain[position - grain_offset : min(len(grain), end - grain_offset)]
                parts.append(piece)
                position += len(piece)
            data = b"".join(parts)
            current = replace(current, stage="uploading", pending_end=offset + len(data))
            await operation.emit(current)
            assert current.upload_id is not None
            kwargs = append_upload._get_kwargs(current.upload_id, body=data, upload_offset=offset)
            kwargs["headers"]["Idempotency-Key"] = hashlib.sha256(
                f"{current.upload_id}:{offset}:{hashlib.sha256(data).hexdigest()}".encode()
            ).hexdigest()
            try:
                response = await operation.request(kwargs)
            except Exception as error:
                operation.recover(error)
                current = await operation.session()
                await operation.emit(current)
                if current.stage == "accepted":
                    return current
                continue
            new_offset = response.headers.get("Upload-Offset", "")
            if (
                len(new_offset) > 16
                or not re.fullmatch(r"0|[1-9][0-9]*", new_offset)
                or int(new_offset) != offset + len(data)
            ):
                raise operation.error("invalid_response")
            current = replace(current, received_bytes=int(new_offset), pending_end=None)
            await operation.emit(current)
        current = replace(current, stage="ready")
        await operation.emit(current)
        return current

    return operation.run(work)


def commit_source(
    owner: FaaSClient, checkpoint: SourceUploadCheckpoint, *, options: SourceUploadOptions
) -> Coroutine[Any, Any, DeploymentSubmissionReceipt]:
    from .api.deployments import get_deployment_submission
    from .api.uploads import commit_upload
    from .models.deployment_submission_receipt import DeploymentSubmissionReceipt

    operation = _Operation(owner, options, checkpoint)

    async def receipt() -> DeploymentSubmissionReceipt | None:
        current = operation.checkpoint
        assert current is not None
        try:
            response = await operation.request(
                get_deployment_submission._get_kwargs(current.app_slug, current.attempt_id)
            )
        except Exception as error:
            if _status(error) == 404:
                return None
            raise
        body = response.json()
        if (
            not isinstance(body, dict)
            or body.get("attempt_id") != current.attempt_id
            or not _id(body.get("deployment_id"))
            or not _id(body.get("app_id"))
            or not isinstance(body.get("accepted_at"), str)
            or current.deployment_id is not None
            and body["deployment_id"] != current.deployment_id
        ):
            raise operation.error("invalid_response")
        try:
            result = DeploymentSubmissionReceipt.from_dict(body)
        except (ValueError, TypeError, KeyError):
            raise operation.error("invalid_response") from None
        await operation.emit(
            replace(
                current,
                stage="accepted",
                received_bytes=current.total_size,
                pending_end=None,
                deployment_id=body["deployment_id"],
            )
        )
        return result

    async def work() -> DeploymentSubmissionReceipt:
        current = operation.checkpoint
        assert current is not None
        if current.stage == "new":
            raise operation.error("not_ready")
        found = await receipt()
        if found is not None:
            return found
        if current.stage in ("committing", "accepted"):
            raise operation.error("acceptance_unknown")
        current = await operation.session()
        await operation.emit(current)
        if current.stage == "accepted":
            found = await receipt()
            if found is None:
                raise operation.error("acceptance_unknown")
            return found
        if current.received_bytes != current.total_size:
            raise operation.error("not_ready")
        current = replace(current, stage="committing")
        await operation.emit(current)  # Must be durable before the only commit attempt.
        assert current.upload_id is not None
        kwargs = commit_upload._get_kwargs(current.upload_id, deployment_attempt_id=current.attempt_id)
        kwargs["headers"]["Idempotency-Key"] = current.commit_key
        commit_error = None
        try:
            await operation.request(kwargs)
        except Exception as error:
            commit_error = error
            operation.check()
            # A failed reply does not prove rejection. Only look up acceptance.
        found = await receipt()
        if found is None:
            raise operation.error("acceptance_unknown", commit_error)
        return found

    return operation.run(work)
