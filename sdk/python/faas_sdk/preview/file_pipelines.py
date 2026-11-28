"""Typed storage-to-job pipelines with application-owned PostgreSQL receipts."""

from __future__ import annotations

import asyncio
import json
import math
import re
import uuid
from collections.abc import Awaitable, Callable, Mapping, Sequence
from dataclasses import asdict, dataclass
from typing import Any, Generic, Literal, Protocol, TypeVar, cast
from urllib.parse import unquote_to_bytes, urlsplit

from .._async_request import RequestOptions, RequestTimeoutError, run_request
from .._typed_jobs import JobCheckpoint, JobRunOptions, JobSubmitOptions, TypedJob
from ..job_runtime import (
    JobContext,
    JobContract,
    JobInput,
    JobStore,
    job_bytes,
    job_hash,
    job_value_hash,
    prepare_job_input,
)
from .files import BytesFileSource, FileBucket, FileOptions, file_source_digest

Input = TypeVar("Input")
Output = TypeVar("Output")
T = TypeVar("T")
Check = Callable[[], None]
FilePipelineCode = Literal[
    "invalid_input",
    "invalid_event",
    "unverified_delivery",
    "scope_mismatch",
    "authorization_denied",
    "source_unavailable",
    "source_changed",
    "version_required",
    "conflict",
    "store_failed",
    "acceptance_unknown",
    "deadline",
    "limit",
    "callback_failed",
]


class FilePipelineError(Exception):
    def __init__(self, code: FilePipelineCode, run_id: str | None = None):
        self.code, self.run_id = code, run_id
        super().__init__(f"SDK file pipeline stopped: {code}")


def _uuid(value: Any) -> bool:
    return (
        isinstance(value, str)
        and re.fullmatch(r"[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}", value) is not None
        and value != "00000000-0000-0000-0000-000000000000"
    )


def _hash(value: Any) -> bool:
    return isinstance(value, str) and re.fullmatch(r"[a-f0-9]{64}", value) is not None


@dataclass(frozen=True, slots=True)
class FilePipelineScope:
    base_url: str
    account_id: str
    app_id: str
    app_slug: str
    bucket: str

    def __post_init__(self) -> None:
        u = urlsplit(self.base_url)
        if (
            u.scheme not in ("http", "https")
            or not u.netloc
            or u.username
            or u.password
            or u.query
            or u.fragment
            or self.base_url.endswith("/")
            or not all(_uuid(v) for v in (self.account_id, self.app_id, self.bucket))
            or re.fullmatch(r"[a-z0-9][a-z0-9-]{0,62}", self.app_slug) is None
        ):
            raise FilePipelineError("invalid_input")


def file_pipeline_identity(name: str, scope: FilePipelineScope, receipt_id: str) -> str:
    if re.fullmatch(r"[A-Za-z0-9_.:-]{1,128}", name) is None or not _uuid(receipt_id):
        raise FilePipelineError("invalid_input")
    return job_value_hash([name, scope.account_id, scope.app_id, scope.bucket, receipt_id])


@dataclass(frozen=True, slots=True)
class FileCreatedEvent:
    version: int
    account_id: str
    app_id: str
    bucket: str
    event_id: str
    receipt_id: str
    key: str
    size_bytes: int
    etag: str
    version_id: str | None
    operation: Literal["put", "copy", "upload", "complete_multipart_upload"]


@dataclass(frozen=True, slots=True)
class FileDelivery:
    body: bytes
    invocation_id: str


def _tag(value: Any) -> str:
    if not isinstance(value, str):
        raise ValueError
    v = value.strip('"')
    if not v or len(v) > 1024 or re.search(r'["\x00-\x1f\x7f]', v):
        raise ValueError
    return '"' + v + '"'


def parse_file_created_event(value: Any, event_id: str, scope: FilePipelineScope) -> FileCreatedEvent:
    """Decode native S3 Records or gregale.storage CloudEvents; authenticate first."""
    try:
        match = re.fullmatch(r"(write|multipart):([a-f0-9-]{36})", event_id)
        if not match or not _uuid(match[2]) or not isinstance(value, dict):
            raise ValueError
        if isinstance(value.get("Records"), list):
            if len(value["Records"]) != 1:
                raise ValueError
            r = value["Records"][0]
            s = r["s3"]
            b = s["bucket"]
            o = s["object"]
            if r["eventSource"] != "aws:s3" or r["eventVersion"] != "2.1" or s["s3SchemaVersion"] != "1.0":
                raise ValueError
            arn = re.fullmatch(r"arn:gregale:s3:([a-z0-9-]+):([a-f0-9-]{36}):bucket:([a-f0-9-]{36})", b["arn"])
            if not arn or (arn[1], arn[2], arn[3], b["ownerIdentity"]["principalId"]) != (
                r["awsRegion"],
                scope.account_id,
                scope.bucket,
                scope.account_id,
            ):
                raise FilePipelineError("scope_mismatch")
            encoded = o["key"]
            if not isinstance(encoded, str) or re.search(r"%(?![0-9a-fA-F]{2})", encoded):
                raise ValueError
            key = unquote_to_bytes(encoded.replace("+", " ")).decode("utf8")
            size, etag, version = o["size"], o["eTag"], o.get("versionId")
            operation = {
                "ObjectCreated:Put": "put",
                "ObjectCreated:Copy": "copy",
                "ObjectCreated:CompleteMultipartUpload": "complete_multipart_upload",
            }[r["eventName"]]
        else:
            d = value["data"]
            if (
                value["specversion"] != "1.0"
                or value["source"] != "gregale.storage"
                or value["type"] != "object.created"
                or value["id"] != event_id
            ):
                raise ValueError
            if (value["accountid"], d["app_id"], d["bucket_id"]) != (scope.account_id, scope.app_id, scope.bucket):
                raise FilePipelineError("scope_mismatch")
            if (
                d["receipt_id"] != match[2]
                or d["cause"] != "customer"
                or d.get("delete_marker") is True
                or d["operation"] not in ("put", "copy", "upload", "complete_multipart_upload")
            ):
                raise ValueError
            key, size, etag, version, operation = (
                d["key"],
                d["size_bytes"],
                d["etag"],
                d.get("version_id"),
                d["operation"],
            )
            if operation == "upload":
                operation = "put"
        if (
            ("multipart" if operation == "complete_multipart_upload" else "write") != match[1]
            or not isinstance(key, str)
            or not key
            or len(key.encode()) > 1024
            or re.search(r"[\x00-\x1f\x7f]", key)
            or type(size) is not int
            or not 0 <= size <= 1024**4
            or version not in (None, "", "null")
            and not _uuid(version)
        ):
            raise ValueError
        return FileCreatedEvent(
            1,
            scope.account_id,
            scope.app_id,
            scope.bucket,
            event_id,
            match[2],
            key,
            size,
            _tag(etag),
            version if version not in (None, "", "null") else None,
            cast(Literal["put", "copy", "upload", "complete_multipart_upload"], operation),
        )
    except FilePipelineError:
        raise
    except (KeyError, ValueError, TypeError, IndexError, AttributeError):
        raise FilePipelineError("invalid_event") from None


def _capture_event(value: FileCreatedEvent, scope: FilePipelineScope) -> FileCreatedEvent:
    if type(value.version) is not int or value.version != 1:
        raise FilePipelineError("invalid_event")
    return parse_file_created_event(
        {
            "specversion": "1.0",
            "source": "gregale.storage",
            "type": "object.created",
            "id": value.event_id,
            "accountid": value.account_id,
            "data": {
                "receipt_id": value.receipt_id,
                "app_id": value.app_id,
                "bucket_id": value.bucket,
                "key": value.key,
                "size_bytes": value.size_bytes,
                "etag": value.etag,
                "version_id": value.version_id,
                "operation": value.operation,
                "cause": "customer",
            },
        },
        value.event_id,
        scope,
    )


def file_event_invocation_id(account: str, event: str, subscription: str) -> str:
    raw = json.dumps([account, "gregale.storage", event, subscription], ensure_ascii=False, separators=(",", ":"))
    for c in "<>&\u2028\u2029":
        raw = raw.replace(c, "\\u" + format(ord(c), "04x"))
    return str(uuid.uuid5(uuid.NAMESPACE_URL, raw))


class NativeFileVerifier:
    """lookup reads account-scoped invocations through an authenticated SDK client.
    Headers from a public HTTP caller are not proof. Missing original proof fails.
    """

    def __init__(self, scope: FilePipelineScope, lookup: Callable[[str, Check], Awaitable[Any]]):
        self.scope, self.lookup = scope, lookup

    async def __call__(self, delivery: FileDelivery, check: Check) -> FileCreatedEvent:
        try:
            check()
            if (
                not _uuid(delivery.invocation_id)
                or not isinstance(delivery.body, bytes)
                or not 0 < len(delivery.body) <= 1024 * 1024
            ):
                raise ValueError
            payload = json.loads(delivery.body.decode("utf8"))
            current = await self.lookup(delivery.invocation_id, check)
            check()
            if (
                not isinstance(current, dict)
                or (
                    current.get("id"),
                    current.get("account_id"),
                    current.get("app_id"),
                    current.get("method"),
                    current.get("path"),
                )
                != (delivery.invocation_id, self.scope.account_id, self.scope.app_id, "POST", "/")
                or current.get("source") not in ("async_invoke", "queue", "replay")
                or job_value_hash(current.get("payload")) != job_value_hash(payload)
                or not isinstance(current.get("headers"), dict)
            ):
                raise ValueError
            headers = {}
            for key, value in current["headers"].items():
                name = key.lower()
                if not isinstance(value, str) or name in headers:
                    raise ValueError
                headers[name] = value
            event, subscription = headers["x-gregale-event-id"], headers["x-gregale-event-subscription-id"]
            if (
                headers["x-gregale-event-source"] != "gregale.storage"
                or headers["x-gregale-event-type"] != "object.created"
                or not event
                or not subscription
                or len(subscription.encode()) > 4096
            ):
                raise ValueError
            original_id = file_event_invocation_id(self.scope.account_id, event, subscription)
            original = current if original_id == current["id"] else await self.lookup(original_id, check)
            check()
            if (
                not isinstance(original, dict)
                or (
                    original.get("id"),
                    original.get("account_id"),
                    original.get("app_id"),
                    original.get("method"),
                    original.get("path"),
                )
                != (original_id, self.scope.account_id, self.scope.app_id, "POST", "/")
                or original.get("source") not in ("async_invoke", "queue")
                or job_value_hash(original.get("payload")) != job_value_hash(payload)
                or not isinstance(original.get("headers"), dict)
            ):
                raise ValueError
            for name in (
                "x-gregale-event-id",
                "x-gregale-event-source",
                "x-gregale-event-type",
                "x-gregale-event-subscription-id",
            ):
                values = [v for k, v in original["headers"].items() if k.lower() == name]
                if len(values) != 1 or values[0] != headers[name]:
                    raise ValueError
            return parse_file_created_event(payload, event, self.scope)
        except FilePipelineError:
            raise
        except Exception:
            check()
            raise FilePipelineError("unverified_delivery") from None


@dataclass(frozen=True, slots=True)
class FilePipelineOptions:
    timeout: float
    max_bytes: int = 16 * 1024 * 1024
    max_items: int = 1000

    def __post_init__(self) -> None:
        if (
            type(self.timeout) not in (float, int)
            or not math.isfinite(self.timeout)
            or not 0 < self.timeout <= 2147483.647
            or type(self.max_bytes) is not int
            or not 0 <= self.max_bytes <= 1024**4
            or type(self.max_items) is not int
            or not 1 <= self.max_items <= 10000
        ):
            raise FilePipelineError("invalid_input")


async def _run(options: FilePipelineOptions, work: Callable[[Check], Awaitable[T]]) -> T:
    active = True
    deadline = asyncio.get_running_loop().time() + options.timeout

    def check() -> None:
        if not active:
            raise asyncio.CancelledError
        if asyncio.get_running_loop().time() >= deadline:
            raise FilePipelineError("deadline")

    async def run() -> T:
        check()
        result = await work(check)
        check()
        return result

    try:
        return await run_request(object(), run, RequestOptions(timeout=options.timeout, retry=False), options.timeout)
    except RequestTimeoutError:
        raise FilePipelineError("deadline") from None
    except FilePipelineError:
        raise
    except Exception:
        raise FilePipelineError("callback_failed") from None
    finally:
        active = False


@dataclass(frozen=True, slots=True)
class FilePipelineOwner:
    id: str
    sha256: str | None = None
    source_digest: str | None = None


@dataclass(frozen=True, slots=True)
class FilePipelineInput:
    input_id: str
    binding_sha256: str
    source_task_index: None = None


@dataclass(frozen=True, slots=True)
class FilePipelineIntent:
    version: int
    identity: str
    request_digest: str
    idempotency_key: str
    pipeline: str
    scope: FilePipelineScope
    owner_id: str
    event: FileCreatedEvent
    contract_sha256: str
    job_name: str
    input_digest: str
    inputs: tuple[FilePipelineInput, ...]

    def to_dict(self) -> dict[str, Any]:
        result = asdict(self)
        result["inputs"] = [asdict(i) for i in self.inputs]
        return result


@dataclass(frozen=True, slots=True)
class FilePipelineReceipt:
    intent: FilePipelineIntent
    checkpoint: JobCheckpoint | None

    def to_dict(self) -> dict[str, Any]:
        return {"intent": self.intent.to_dict(), "checkpoint": self.checkpoint.to_dict() if self.checkpoint else None}

    @classmethod
    def from_dict(cls, value: dict[str, Any]) -> FilePipelineReceipt:
        try:
            if set(value) != {"intent", "checkpoint"}:
                raise ValueError
            raw = value["intent"]
            i = FilePipelineIntent(
                **{
                    **raw,
                    "scope": FilePipelineScope(**raw["scope"]),
                    "event": FileCreatedEvent(**raw["event"]),
                    "inputs": tuple(FilePipelineInput(**x) for x in raw["inputs"]),
                }
            )
            if (
                type(i.version) is not int
                or i.version != 1
                or not all(_hash(v) for v in (i.identity, i.request_digest, i.contract_sha256, i.input_digest))
                or i.idempotency_key != "file-pipeline:" + i.identity
                or not isinstance(i.pipeline, str)
                or re.fullmatch(r"[A-Za-z0-9_.:-]{1,128}", i.pipeline) is None
                or not isinstance(i.owner_id, str)
                or not 0 < len(i.owner_id) <= 128
                or not isinstance(i.job_name, str)
                or re.fullmatch(r"[a-z0-9][a-z0-9-]{1,38}[a-z0-9]", i.job_name) is None
                or not 0 < len(i.inputs) <= 10000
                or len({x.input_id for x in i.inputs}) != len(i.inputs)
                or any(
                    not isinstance(x.input_id, str)
                    or re.fullmatch(r"[A-Za-z0-9_.:-]{1,128}", x.input_id) is None
                    or not _hash(x.binding_sha256)
                    or x.source_task_index is not None
                    for x in i.inputs
                )
            ):
                raise ValueError
            e = i.event
            if (
                e.version != 1
                or (e.account_id, e.app_id, e.bucket) != (i.scope.account_id, i.scope.app_id, i.scope.bucket)
                or not _uuid(e.receipt_id)
                or e.event_id not in ("write:" + e.receipt_id, "multipart:" + e.receipt_id)
                or not isinstance(e.key, str)
                or not e.key
                or len(e.key.encode()) > 1024
                or type(e.size_bytes) is not int
                or not 0 <= e.size_bytes <= 1024**4
                or not isinstance(e.etag, str)
                or not e.etag
                or e.version_id is not None
                and not _uuid(e.version_id)
            ):
                raise ValueError
            checkpoint = JobCheckpoint.from_dict(value["checkpoint"]) if value["checkpoint"] is not None else None
            if _capture_event(e, i.scope) != e:
                raise ValueError
            if checkpoint:
                _matches(i, checkpoint)
            return cls(i, checkpoint)
        except (ValueError, TypeError, KeyError, AttributeError, FilePipelineError):
            raise FilePipelineError("conflict") from None


def _matches(i: FilePipelineIntent, c: JobCheckpoint) -> None:
    c = JobCheckpoint.from_dict(c.to_dict())
    if (c.base_url, c.account_id, c.job_name, c.contract_sha256, c.input_digest, c.source_run_id) != (
        i.scope.base_url,
        i.scope.account_id,
        i.job_name,
        i.contract_sha256,
        i.input_digest,
        None,
    ) or job_value_hash([asdict(x) for x in c.inputs]) != job_value_hash([asdict(x) for x in i.inputs]):
        raise FilePipelineError("conflict")


class FilePipelineStore(Protocol):
    async def load(self, identity: str, check: Check) -> FilePipelineReceipt | None: ...
    async def reserve(self, intent: FilePipelineIntent, check: Check) -> tuple[bool, FilePipelineReceipt]: ...
    async def complete(
        self, intent: FilePipelineIntent, checkpoint: JobCheckpoint, check: Check
    ) -> FilePipelineReceipt: ...


@dataclass(frozen=True, slots=True)
class FilePipelineOutcome:
    outcome: Literal["ignored", "submitted", "duplicate", "unresolved"]
    reason: Literal["filter_mismatch", "unowned_file"] | None = None
    receipt: FilePipelineReceipt | None = None
    checkpoint: JobCheckpoint | None = None
    run_id: str | None = None


class FilePipeline(Generic[Input, Output]):
    def __init__(
        self,
        *,
        name: str,
        revision: str,
        scope: FilePipelineScope,
        prefix: str,
        suffix: str,
        contract: JobContract[Input, Output],
        job: TypedJob[Input, Output],
        files: FileBucket,
        store: FilePipelineStore,
        verify: Callable[[FileDelivery, Check], Awaitable[FileCreatedEvent]],
        authorize: Callable[[FileCreatedEvent, Check], Awaitable[FilePipelineOwner | None]],
        map_inputs: Callable[[FileCreatedEvent, bytes, FilePipelineOwner, Check], Awaitable[Sequence[JobInput[Input]]]],
        job_store: JobStore | None = None,
    ):
        scope = FilePipelineScope(scope.base_url, scope.account_id, scope.app_id, scope.app_slug, scope.bucket)
        if (
            re.fullmatch(r"[A-Za-z0-9_.:-]{1,128}", name) is None
            or re.fullmatch(r"[A-Za-z0-9_.:-]{1,128}", revision) is None
            or not prefix
            or len(prefix.encode()) > 1024
            or len(suffix.encode()) > 1024
            or re.search(r"[\x00*]", prefix + suffix)
            or (files.base_url, files.app_slug, files.bucket) != (scope.base_url, scope.app_slug, scope.bucket)
        ):
            raise FilePipelineError("invalid_input")
        (
            self.name,
            self.scope,
            self.prefix,
            self.suffix,
            self.contract,
            self.job,
            self.files,
            self.store,
            self.verify,
            self.authorize,
            self.map_inputs,
            self.job_store,
        ) = name, scope, prefix, suffix, contract, job, files, store, verify, authorize, map_inputs, job_store
        self._config = job_value_hash(
            {
                "revision": revision,
                "scope": asdict(scope),
                "prefix": prefix,
                "suffix": suffix,
                "contract": contract.sha256,
            }
        )

    async def _load(self, identity: str, check: Check) -> FilePipelineReceipt | None:
        try:
            check()
            r = await self.store.load(identity, check)
            check()
            if r:
                r = FilePipelineReceipt.from_dict(r.to_dict())
                i = r.intent
                if (
                    i.identity != identity
                    or i.pipeline != self.name
                    or i.scope != self.scope
                    or i.contract_sha256 != self.contract.sha256
                    or i.job_name != self.contract.spec["name"]
                ):
                    raise FilePipelineError("conflict")
            return r
        except FilePipelineError:
            raise
        except Exception:
            check()
            raise FilePipelineError("store_failed") from None

    def _outcome(self, r: FilePipelineReceipt, kind: Literal["submitted", "duplicate"]) -> FilePipelineOutcome:
        return (
            FilePipelineOutcome(kind, receipt=r, checkpoint=r.checkpoint)
            if r.checkpoint
            else FilePipelineOutcome("unresolved", receipt=r)
        )

    async def _finish(self, intent: FilePipelineIntent, checkpoint: JobCheckpoint, check: Check) -> FilePipelineReceipt:
        _matches(intent, checkpoint)
        check()
        try:
            r = await self.store.complete(intent, checkpoint, check)
            check()
            r = FilePipelineReceipt.from_dict(r.to_dict())
            if r.intent != intent or not r.checkpoint or r.checkpoint.run_id != checkpoint.run_id:
                raise FilePipelineError("conflict")
            return r
        except FilePipelineError:
            raise
        except Exception:
            check()
            raise FilePipelineError("store_failed", checkpoint.run_id) from None

    async def handle(self, delivery: FileDelivery, *, options: FilePipelineOptions) -> FilePipelineOutcome:
        async def work(check: Check) -> FilePipelineOutcome:
            event = _capture_event(await self.verify(delivery, check), self.scope)
            check()
            if (event.account_id, event.app_id, event.bucket) != (
                self.scope.account_id,
                self.scope.app_id,
                self.scope.bucket,
            ):
                raise FilePipelineError("scope_mismatch")
            if not event.key.startswith(self.prefix) or not event.key.endswith(self.suffix):
                return FilePipelineOutcome("ignored", reason="filter_mismatch")
            owner = await self.authorize(event, check)
            check()
            if owner is None:
                return FilePipelineOutcome("ignored", reason="unowned_file")
            owner = FilePipelineOwner(owner.id, owner.sha256, owner.source_digest)
            if (
                not isinstance(owner.id, str)
                or not 0 < len(owner.id) <= 128
                or owner.sha256 is not None
                and not _hash(owner.sha256)
                or owner.source_digest is not None
                and not _hash(owner.source_digest)
            ):
                raise FilePipelineError("authorization_denied")
            identity = job_value_hash(
                [self.name, self.scope.account_id, self.scope.app_id, self.scope.bucket, event.receipt_id]
            )
            owner_wire = {k: v for k, v in asdict(owner).items() if v is not None}
            request_digest = job_value_hash({"config": self._config, "event": asdict(event), "owner": owner_wire})
            existing = await self._load(identity, check)
            if existing:
                if existing.intent.request_digest != request_digest:
                    raise FilePipelineError("conflict")
                return self._outcome(existing, "duplicate")
            if event.size_bytes > options.max_bytes:
                raise FilePipelineError("limit")
            if event.version_id is None and not owner.sha256 and not owner.source_digest:
                raise FilePipelineError("version_required")
            try:
                data = await self.files.download(
                    event.key,
                    options=FileOptions(timeout=options.timeout, max_bytes=options.max_bytes),
                    version_id=event.version_id,
                )
                check()
            except Exception:
                check()
                raise FilePipelineError("source_unavailable") from None
            if (
                len(data) != event.size_bytes
                or owner.sha256
                and job_hash(data) != owner.sha256
                or owner.source_digest
                and await file_source_digest(BytesFileSource(data)) != owner.source_digest
            ):
                raise FilePipelineError("source_changed")
            mapped = await self.map_inputs(event, data, owner, check)
            check()
            if not 0 < len(mapped) <= options.max_items or len({x.id for x in mapped}) != len(mapped):
                raise FilePipelineError("limit")
            inputs = [JobInput(x.id, self.contract.input(x.value)) for x in mapped]
            refs = []
            for item in inputs:
                check()
                ref = await prepare_job_input(self.contract, item, JobContext(item.id, 0, check), self.job_store)
                refs.append(asdict(ref))
            check()
            digest = job_hash(job_bytes(refs))
            intent = FilePipelineIntent(
                1,
                identity,
                request_digest,
                "file-pipeline:" + identity,
                self.name,
                self.scope,
                owner.id,
                event,
                self.contract.sha256,
                self.contract.spec["name"],
                digest,
                tuple(FilePipelineInput(r["input_id"], job_hash(r["input_ref"].encode())) for r in refs),
            )
            try:
                created, saved = await self.store.reserve(intent, check)
                check()
            except Exception:
                check()
                raise FilePipelineError("store_failed") from None
            saved = FilePipelineReceipt.from_dict(saved.to_dict())
            if saved.intent != intent or type(created) is not bool:
                raise FilePipelineError("conflict")
            if not created:
                return self._outcome(saved, "duplicate")
            if saved.checkpoint is not None:
                raise FilePipelineError("conflict")
            check()
            try:
                run = await self.job.submit(
                    inputs,
                    options=JobSubmitOptions(
                        timeout=options.timeout,
                        max_items=options.max_items,
                        idempotency_key=intent.idempotency_key,
                        expected_account_id=self.scope.account_id,
                        expected_base_url=self.scope.base_url,
                        retry_max=0,
                    ),
                )
                check()
                checkpoint = run.checkpoint
                _matches(intent, checkpoint)
            except Exception:
                check()
                return FilePipelineOutcome("unresolved", receipt=saved)
            try:
                return self._outcome(await self._finish(intent, checkpoint, check), "submitted")
            except FilePipelineError as e:
                check()
                if e.code == "conflict":
                    raise
                return FilePipelineOutcome("unresolved", receipt=saved, run_id=checkpoint.run_id)

        return await _run(options, work)

    async def reconcile(self, identity: str, run_id: str, *, options: FilePipelineOptions) -> FilePipelineOutcome:
        async def work(check: Check) -> FilePipelineOutcome:
            if not _hash(identity) or not _uuid(run_id):
                raise FilePipelineError("invalid_input")
            saved = await self._load(identity, check)
            if saved is None:
                raise FilePipelineError("invalid_input")
            if saved.checkpoint:
                if saved.checkpoint.run_id != run_id:
                    raise FilePipelineError("conflict")
                return self._outcome(saved, "duplicate")
            run = await self.job.resume_by_id(
                run_id, options=JobRunOptions(timeout=options.timeout, max_items=options.max_items)
            )
            check()
            _matches(saved.intent, run.checkpoint)
            return self._outcome(await self._finish(saved.intent, run.checkpoint, check), "duplicate")

        return await _run(options, work)


FILE_PIPELINE_SCHEMA = """CREATE TABLE IF NOT EXISTS public.gregale_file_pipeline_receipts (
  identity text PRIMARY KEY CHECK (identity ~ '^[a-f0-9]{64}$'),
  receipt jsonb NOT NULL CHECK (jsonb_typeof(receipt) = 'object'),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
"""
PipelineQuery = Callable[[str, tuple[Any, ...]], Awaitable[Sequence[Mapping[str, Any]]]]


class PostgresFilePipelineStore:
    """query executes ONE autocommit statement and returns mapping rows. Set driver
    timeouts; outer cancellation fences late results before any job submission.
    Install FILE_PIPELINE_SCHEMA explicitly and retain receipts across replay.
    """

    def __init__(self, query: PipelineQuery):
        self.query = query

    async def load(self, identity: str, check: Check) -> FilePipelineReceipt | None:
        check()
        rows = await self.query(
            "SELECT receipt FROM public.gregale_file_pipeline_receipts WHERE identity=%s", (identity,)
        )
        check()
        if len(rows) > 1:
            raise FilePipelineError("conflict")
        return FilePipelineReceipt.from_dict(rows[0]["receipt"]) if rows else None

    async def reserve(self, intent: FilePipelineIntent, check: Check) -> tuple[bool, FilePipelineReceipt]:
        check()
        rows = await self.query(
            "INSERT INTO public.gregale_file_pipeline_receipts(identity,receipt) VALUES (%s,%s::jsonb) ON CONFLICT (identity) DO NOTHING RETURNING receipt",
            (intent.identity, json.dumps({"intent": intent.to_dict(), "checkpoint": None}, ensure_ascii=False)),
        )
        check()
        if len(rows) > 1:
            raise FilePipelineError("conflict")
        r = FilePipelineReceipt.from_dict(rows[0]["receipt"]) if rows else await self.load(intent.identity, check)
        if r is None or r.intent != intent:
            raise FilePipelineError("conflict")
        return bool(rows), r

    async def complete(
        self, intent: FilePipelineIntent, checkpoint: JobCheckpoint, check: Check
    ) -> FilePipelineReceipt:
        check()
        _matches(intent, checkpoint)
        rows = await self.query(
            "UPDATE public.gregale_file_pipeline_receipts SET receipt=jsonb_set(receipt,'{checkpoint}',%s::jsonb) WHERE identity=%s AND receipt->'intent'=%s::jsonb AND (receipt->'checkpoint'='null'::jsonb OR receipt->'checkpoint'=%s::jsonb) RETURNING receipt",
            (
                json.dumps(checkpoint.to_dict()),
                intent.identity,
                json.dumps(intent.to_dict()),
                json.dumps(checkpoint.to_dict()),
            ),
        )
        check()
        if len(rows) != 1:
            raise FilePipelineError("conflict")
        return FilePipelineReceipt.from_dict(rows[0]["receipt"])
