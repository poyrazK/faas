"""Typed job submission, observation, replay and verified result collection."""

from __future__ import annotations

import inspect
import json
import math
import re
import uuid
from collections.abc import Awaitable, Callable, Coroutine
from dataclasses import asdict, dataclass
from datetime import UTC, datetime
from typing import TYPE_CHECKING, Any, Generic, Literal, TypeVar
from urllib.parse import quote, urlsplit

import httpx

from ._async_request import RequestOptions, owned_async_transport, run_request, snapshot_async_client
from ._source_upload import _base
from .job_runtime import (
    JOB_MAX_BYTES,
    JobContext,
    JobContract,
    JobError,
    JobInput,
    JobInputBinding,
    JobStore,
    _scope,
    decode_job_output,
    inspect_job_input,
    job_bytes,
    job_hash,
    job_input_id,
    job_uri,
    prepare_job_input,
)

if TYPE_CHECKING:
    from ._wrapper import FaaSClient

Input = TypeVar("Input")
Output = TypeVar("Output")
T = TypeVar("T")
JobStatus = Literal["queued", "running", "succeeded", "failed", "cancelled", "dead_letter"]
_TERMINAL = {"succeeded", "failed", "cancelled", "dead_letter"}
_FAILED = {"failed", "timeout", "oom", "cancelled"}


def _uuid(value: Any) -> bool:
    return isinstance(value, str) and re.fullmatch(r"[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}", value) is not None


def _hash(value: Any) -> bool:
    return isinstance(value, str) and re.fullmatch(r"[a-f0-9]{64}", value) is not None


def _nonnegative(value: Any) -> bool:
    return type(value) is int and 0 <= value <= 9007199254740991


@dataclass(frozen=True, slots=True)
class JobCheckpointInput:
    input_id: str
    binding_sha256: str
    source_task_index: int | None


@dataclass(frozen=True, slots=True)
class JobCheckpoint:
    version: Literal[1]
    base_url: str
    job_name: str
    job_id: str
    account_id: str
    run_id: str
    contract_sha256: str
    input_digest: str
    inputs: tuple[JobCheckpointInput, ...]
    source_run_id: str | None

    def to_dict(self) -> dict[str, Any]:
        value = asdict(self)
        value["inputs"] = [asdict(i) for i in self.inputs]
        return value

    @classmethod
    def from_dict(cls, value: dict[str, Any]) -> JobCheckpoint:
        try:
            if (
                set(value)
                != {
                    "version",
                    "base_url",
                    "job_name",
                    "job_id",
                    "account_id",
                    "run_id",
                    "contract_sha256",
                    "input_digest",
                    "inputs",
                    "source_run_id",
                }
                or type(value["version"]) is not int
                or value["version"] != 1
                or _base(value["base_url"]) != value["base_url"]
                or not re.fullmatch(r"[a-z0-9][a-z0-9-]{1,38}[a-z0-9]", value["job_name"])
                or any(not _uuid(value[k]) for k in ("job_id", "account_id", "run_id"))
                or not _hash(value["contract_sha256"])
                or not _hash(value["input_digest"])
                or value["source_run_id"] is not None
                and not _uuid(value["source_run_id"])
                or not isinstance(value["inputs"], list)
                or not 1 <= len(value["inputs"]) <= 10000
            ):
                raise ValueError
            inputs = []
            for i in value["inputs"]:
                if (
                    set(i) != {"input_id", "binding_sha256", "source_task_index"}
                    or not job_input_id(i["input_id"])
                    or not _hash(i["binding_sha256"])
                    or i["source_task_index"] is not None
                    and not _nonnegative(i["source_task_index"])
                    or (i["source_task_index"] is None) != (value["source_run_id"] is None)
                ):
                    raise ValueError
                inputs.append(JobCheckpointInput(**i))
            if len({i.input_id for i in inputs}) != len(inputs):
                raise ValueError
            return cls(**{**value, "inputs": tuple(inputs)})
        except (TypeError, ValueError, KeyError, AttributeError):
            raise JobError("invalid_receipt") from None


@dataclass(frozen=True, slots=True)
class JobProgress:
    run_id: str
    status: JobStatus
    total: int
    succeeded: int
    failed: int
    cancelled: int
    running: int


@dataclass(frozen=True, slots=True, kw_only=True)
class JobRunOptions:
    timeout: float
    max_requests: int = 256
    max_pages: int = 100
    max_items: int = 1000
    max_polls: int = 100
    poll_delay: float = 1
    on_progress: Callable[[JobProgress], None | Awaitable[None]] | None = None

    def __post_init__(self) -> None:
        if (
            type(self.timeout) not in (int, float)
            or not math.isfinite(self.timeout)
            or not 0 < self.timeout <= 2147483.647
            or type(self.poll_delay) not in (int, float)
            or not math.isfinite(self.poll_delay)
            or not 0 <= self.poll_delay <= 60
        ):
            raise ValueError("Invalid job duration")
        for value, maximum in (
            (self.max_requests, 10000),
            (self.max_pages, 1000),
            (self.max_items, 10000),
            (self.max_polls, 10000),
        ):
            if type(value) is not int or not 1 <= value <= maximum:
                raise ValueError("Invalid job limit")
        if self.on_progress is not None and not callable(self.on_progress):
            raise ValueError("Invalid progress handler")


def _key(value: str) -> str:
    if not isinstance(value, str) or not re.fullmatch(r"[\x21-\x7e]{1,255}", value):
        raise JobError("invalid_input")
    return value


@dataclass(frozen=True, slots=True, kw_only=True)
class JobSubmitOptions(JobRunOptions):
    idempotency_key: str
    expected_account_id: str | None = None
    expected_base_url: str | None = None
    expected_contract_sha256: str | None = None
    parallelism: int | None = None
    retry_max: int | None = None
    failure_policy: Literal["continue", "fail_fast"] | None = None

    def __post_init__(self) -> None:
        super(JobSubmitOptions, self).__post_init__()
        _key(self.idempotency_key)
        if self.expected_account_id is not None and not _uuid(self.expected_account_id):
            raise JobError('invalid_input')
        if self.expected_base_url is not None:
            _base(self.expected_base_url)
        if self.expected_contract_sha256 is not None and not _hash(self.expected_contract_sha256):
            raise JobError('invalid_input')
        if (
            any(v is not None and not _nonnegative(v) for v in (self.parallelism, self.retry_max))
            or self.parallelism == 0
            or self.failure_policy not in (None, "continue", "fail_fast")
        ):
            raise JobError("invalid_input")


@dataclass(frozen=True, slots=True)
class JobItemOutcome(Generic[Output]):
    input_id: str
    index: int
    attempt: int
    outcome: Literal["succeeded", "failed", "cancelled", "pending", "invalid_result"]
    result: Output | None = None
    code: str | None = None


class _Operation:
    def __init__(
        self, client: httpx.AsyncClient, transport_owner: object, options: JobRunOptions, check: Callable[[], None]
    ):
        self.client, self.transport_owner, self.options, self.check, self.count = (
            client,
            transport_owner,
            options,
            check,
            0,
        )

    async def request(
        self, path: str, method: str = "GET", body: Any = None, idempotency_key: str | None = None
    ) -> dict[str, Any]:
        self.check()
        if self.count >= self.options.max_requests:
            raise JobError("limit")
        self.count += 1

        async def send() -> dict[str, Any]:
            kwargs: dict[str, Any] = {"headers": {"Accept-Encoding": "identity"}}
            if method == "POST":
                kwargs["headers"]["Idempotency-Key"] = idempotency_key or str(uuid.uuid4())
            if body is not None:
                kwargs["json"] = body
            async with self.client.stream(method, path, **kwargs) as response:
                response.raise_for_status()
                raw = bytearray()
                async for chunk in response.aiter_bytes(chunk_size=8192):
                    self.check()
                    if len(raw) + len(chunk) > JOB_MAX_BYTES:
                        raise JobError("limit")
                    raw.extend(chunk)
                value = json.loads(raw)
                if not isinstance(value, dict):
                    raise JobError("invalid_response")
                return value

        try:
            return await run_request(self.transport_owner, send, RequestOptions(timeout=0, retry=False), 0)
        except JobError:
            if method == "POST":
                raise JobError("acceptance_unknown") from None
            raise
        except Exception as error:
            self.check()
            status = error.response.status_code if isinstance(error, httpx.HTTPStatusError) else None
            raise JobError(
                "acceptance_unknown" if method == "POST" and (status is None or status >= 500) else "request_failed",
                status,
            ) from None


def _invoke(
    owner: FaaSClient, options: JobRunOptions, base: str | None, work: Callable[[_Operation], Awaitable[T]]
) -> Coroutine[Any, Any, T]:
    source = owner.async_httpx_client
    if base is not None and _base(str(source.base_url)) != base:
        raise JobError("invalid_receipt")
    client = snapshot_async_client(source)
    client.follow_redirects = False
    transport_owner = owned_async_transport(source).owner

    async def execute() -> T:
        async def operation(check: Callable[[], None]) -> T:
            return await work(_Operation(client, transport_owner, options, check))

        try:
            return await _scope(options.timeout, operation)
        finally:
            await client.aclose()

    return execute()


def _path(name: str, run_id: str | None = None) -> str:
    return "/v1/jobs/" + quote(name, safe="") + ("/runs/" + run_id if run_id else "")


def _progress(run: dict[str, Any], c: JobCheckpoint) -> JobProgress:
    try:
        counters = [run[k] for k in ("tasks_succeeded", "tasks_failed", "tasks_cancelled", "tasks_running")]
        if (
            run["id"] != c.run_id
            or run["job_id"] != c.job_id
            or run["account_id"] != c.account_id
            or run["input_digest"] != c.input_digest
            or type(run["input_manifest_version"]) is bool
            or run["input_manifest_version"] != 1
            or not _nonnegative(run["tasks"])
            or run["tasks"] != len(c.inputs)
            or run.get("source_run_id") != c.source_run_id
            or run["aggregate_status"] not in ("queued", "running", *_TERMINAL)
            or any(not _nonnegative(v) for v in counters)
            or sum(counters) > run["tasks"]
            or run["aggregate_status"] in _TERMINAL
            and (counters[3] != 0 or sum(counters[:3]) != run["tasks"])
            or run["aggregate_status"] == "succeeded"
            and counters[0] != run["tasks"]
        ):
            raise ValueError
        return JobProgress(c.run_id, run["aggregate_status"], run["tasks"], *counters)
    except (KeyError, TypeError, ValueError):
        raise JobError("invalid_response") from None


async def _tasks(op: _Operation, c: JobCheckpoint, contract: JobContract[Any, Any]) -> list[dict[str, Any]]:
    offset, tasks, seen = 0, [], set()
    for _ in range(op.options.max_pages):
        data = await op.request(_path(c.job_name, c.run_id) + f"/tasks?limit=200&offset={offset}")
        if (
            not isinstance(data.get("tasks"), list)
            or len(data["tasks"]) > 200
            or data.get("offset") != offset
            or data.get("total") != len(c.inputs)
            or type(data.get("next_offset")) is not int
        ):
            raise JobError("invalid_response")
        for task in data["tasks"]:
            try:
                index = task["task_index"]
                if not _nonnegative(index) or index >= len(c.inputs):
                    raise ValueError
                expected = c.inputs[index]
                if (
                    task["run_id"] != c.run_id
                    or index in seen
                    or task.get("input_id") != expected.input_id
                    or not isinstance(task.get("input_ref"), str)
                    or job_hash(task["input_ref"].encode()) != expected.binding_sha256
                    or task.get("source_task_index") != expected.source_task_index
                    or task["status"] not in ("queued", "claimed", "succeeded", *_FAILED)
                    or not _nonnegative(task["attempt"])
                ):
                    raise ValueError
                inspect_job_input(contract, JobInputBinding(task["input_id"], task["input_ref"]))
            except (KeyError, ValueError, TypeError, IndexError):
                raise JobError("invalid_response") from None
            seen.add(index)
            tasks.append(task)
            if len(tasks) > op.options.max_items:
                raise JobError("limit")
        if data["next_offset"] == -1:
            if len(tasks) != len(c.inputs):
                raise JobError("invalid_response")
            return sorted(tasks, key=lambda t: t["task_index"])
        if data["next_offset"] != offset + len(data["tasks"]) or not offset < data["next_offset"] < len(c.inputs):
            raise JobError("invalid_response")
        offset = data["next_offset"]
    raise JobError("limit")


class TypedJobRun(Generic[Input, Output]):
    def __init__(
        self, owner: FaaSClient, contract: JobContract[Input, Output], checkpoint: JobCheckpoint, store: JobStore | None
    ):
        self._owner, self._contract, self._store = owner, contract, store
        self._checkpoint = JobCheckpoint.from_dict(checkpoint.to_dict())
        if self._checkpoint.job_name != contract.spec["name"] or self._checkpoint.contract_sha256 != contract.sha256:
            raise JobError("invalid_receipt")

    @property
    def checkpoint(self) -> JobCheckpoint:
        return self._checkpoint

    async def _status(self, op: _Operation) -> JobProgress:
        return _progress(await op.request(_path(self.checkpoint.job_name, self.checkpoint.run_id)), self.checkpoint)

    def status(self, *, options: JobRunOptions) -> Coroutine[Any, Any, JobProgress]:
        return _invoke(self._owner, options, self.checkpoint.base_url, self._status)

    def wait(self, *, options: JobRunOptions) -> Coroutine[Any, Any, JobProgress]:
        async def work(op: _Operation) -> JobProgress:
            for poll in range(options.max_polls):
                state = await self._status(op)
                if options.on_progress:
                    try:
                        value = options.on_progress(state)
                        if inspect.isawaitable(value):
                            await value
                    except Exception:
                        op.check()
                        raise JobError("progress_failed") from None
                op.check()
                if state.status in _TERMINAL:
                    return state
                if poll + 1 < options.max_polls:
                    import asyncio

                    await asyncio.sleep(options.poll_delay)
            raise JobError("poll_limit")

        return _invoke(self._owner, options, self.checkpoint.base_url, work)

    def cancel(self, *, options: JobRunOptions, idempotency_key: str | None = None) -> Coroutine[Any, Any, JobProgress]:
        key = _key(idempotency_key or str(uuid.uuid4()))

        async def work(op: _Operation) -> JobProgress:
            await self._status(op)
            data = await op.request(
                _path(self.checkpoint.job_name, self.checkpoint.run_id) + "/cancel", "POST", idempotency_key=key
            )
            try:
                return _progress(data["run"], self.checkpoint)
            except (JobError, KeyError, TypeError):
                raise JobError("acceptance_unknown") from None

        return _invoke(self._owner, options, self.checkpoint.base_url, work)

    def results(self, *, options: JobRunOptions) -> Coroutine[Any, Any, tuple[JobItemOutcome[Output], ...]]:
        async def work(op: _Operation) -> tuple[JobItemOutcome[Output], ...]:
            state = await self._status(op)
            if state.status not in _TERMINAL:
                raise JobError("not_terminal")
            tasks = await _tasks(op, self.checkpoint, self._contract)
            outcomes: list[JobItemOutcome[Output]] = []
            for t in tasks:
                op.check()
                if t["status"] != "succeeded":
                    code = t.get("outcome_code")
                    outcomes.append(
                        JobItemOutcome(
                            t["input_id"],
                            t["task_index"],
                            t["attempt"],
                            "cancelled"
                            if t["status"] == "cancelled"
                            else "failed"
                            if t["status"] in _FAILED
                            else "pending",
                            code=code if isinstance(code, str) and re.fullmatch(r"[a-z0-9_.-]{1,64}", code) else None,
                        )
                    )
                    continue
                try:
                    manifest = t["output_manifest"]
                    candidates = [a for a in manifest["artifacts"] if a["name"] == "result"]
                    if type(manifest["version"]) is bool or manifest["version"] != 1 or len(candidates) != 1:
                        raise JobError("invalid_output")
                    artifact = candidates[0]
                    sha = artifact["sha256"].removeprefix("sha256:")
                    if (
                        not job_uri(artifact["uri"])
                        or not _hash(sha)
                        or not _nonnegative(artifact["size_bytes"])
                        or artifact["size_bytes"] > JOB_MAX_BYTES
                    ):
                        raise JobError("invalid_output")
                    if artifact["uri"].startswith("obj://"):
                        signed = await op.request(
                            _path(self.checkpoint.job_name, self.checkpoint.run_id)
                            + f"/tasks/{t['task_index']}/artifacts/result/download"
                        )
                        download = signed["download"]
                        if (
                            signed["name"] != "result"
                            or signed["size_bytes"] != artifact["size_bytes"]
                            or signed["sha256"] != artifact["sha256"]
                            or download["method"] != "GET"
                            or datetime.fromisoformat(download["expires_at"].replace("Z", "+00:00")).timestamp()
                            <= datetime.now(UTC).timestamp()
                        ):
                            raise JobError("invalid_output")
                        u = urlsplit(download["url"])
                        headers = httpx.Headers(download["headers"])
                        if (
                            u.scheme not in ("http", "https")
                            or u.username
                            or u.password
                            or u.fragment
                            or "cookie" in headers
                            or "proxy-authorization" in headers
                        ):
                            raise JobError("invalid_output")
                        # An independent data client receives only the signed request.
                        async with httpx.AsyncClient(follow_redirects=False, trust_env=False) as client:
                            async with client.stream("GET", download["url"], headers=headers) as response:
                                response.raise_for_status()
                                data = bytearray()
                                async for chunk in response.aiter_bytes(chunk_size=8192):
                                    op.check()
                                    if len(data) + len(chunk) > artifact["size_bytes"]:
                                        raise JobError("limit")
                                    data.extend(chunk)
                        raw = bytes(data)
                    else:
                        if not self._store:
                            raise JobError("store_required")
                        raw = await self._store.get(artifact["uri"], JobContext(t["input_id"], t["attempt"], op.check))
                    op.check()
                    if len(raw) != artifact["size_bytes"] or job_hash(raw) != sha:
                        raise JobError("integrity_failed")
                    result = decode_job_output(self._contract, t["input_id"], raw)
                    outcomes.append(JobItemOutcome(t["input_id"], t["task_index"], t["attempt"], "succeeded", result))
                except Exception as error:
                    op.check()
                    outcomes.append(
                        JobItemOutcome(
                            t["input_id"],
                            t["task_index"],
                            t["attempt"],
                            "invalid_result",
                            code=error.code if isinstance(error, JobError) else "invalid_output",
                        )
                    )
            return tuple(outcomes)

        return _invoke(self._owner, options, self.checkpoint.base_url, work)

    def retry_failed(
        self, *, options: JobRunOptions, idempotency_key: str
    ) -> Coroutine[Any, Any, TypedJobRun[Input, Output]]:
        key = _key(idempotency_key)

        async def work(op: _Operation) -> TypedJobRun[Input, Output]:
            state = await self._status(op)
            if state.status not in _TERMINAL:
                raise JobError("not_terminal")
            tasks = [t for t in await _tasks(op, self.checkpoint, self._contract) if t["status"] in _FAILED]
            if not tasks:
                raise JobError("no_failed_inputs")
            refs = [{"input_id": t["input_id"], "input_ref": t["input_ref"]} for t in tasks]
            run = await op.request(
                _path(self.checkpoint.job_name, self.checkpoint.run_id) + "/replay-failed", "POST", idempotency_key=key
            )
            try:
                c = JobCheckpoint.from_dict(
                    {
                        **self.checkpoint.to_dict(),
                        "run_id": run["id"],
                        "source_run_id": self.checkpoint.run_id,
                        "input_digest": job_hash(job_bytes(refs)),
                        "inputs": [
                            {
                                "input_id": t["input_id"],
                                "binding_sha256": job_hash(t["input_ref"].encode()),
                                "source_task_index": t["task_index"],
                            }
                            for t in tasks
                        ],
                    }
                )
                _progress(run, c)
                return TypedJobRun(self._owner, self._contract, c, self._store)
            except (JobError, KeyError, TypeError):
                raise JobError("acceptance_unknown") from None

        return _invoke(self._owner, options, self.checkpoint.base_url, work)


class TypedJob(Generic[Input, Output]):
    def __init__(self, owner: FaaSClient, contract: JobContract[Input, Output], store: JobStore | None = None):
        self._owner, self._contract, self._store = owner, contract, store

    def resume(self, checkpoint: JobCheckpoint) -> TypedJobRun[Input, Output]:
        return TypedJobRun(self._owner, self._contract, checkpoint, self._store)

    def resume_by_id(self, run_id: str, *, options: JobRunOptions) -> Coroutine[Any, Any, TypedJobRun[Input, Output]]:
        if not _uuid(run_id):
            raise JobError("invalid_receipt")
        base = _base(str(self._owner.async_httpx_client.base_url))

        async def work(op: _Operation) -> TypedJobRun[Input, Output]:
            job = await op.request(_path(self._contract.spec["name"]))
            if (
                not _uuid(job.get("id"))
                or not _uuid(job.get("account_id"))
                or job.get("name") != self._contract.spec["name"]
            ):
                raise JobError("invalid_response")
            run = await op.request(_path(job["name"], run_id))
            if (
                run.get("id") != run_id
                or run.get("job_id") != job["id"]
                or run.get("account_id") != job["account_id"]
                or not _nonnegative(run.get("tasks"))
                or run["tasks"] < 1
                or not _hash(run.get("input_digest"))
                or run.get("input_manifest_version") != 1
            ):
                raise JobError("invalid_response")
            if run["tasks"] > options.max_items:
                raise JobError("limit")
            offset = 0
            tasks: list[dict[str, Any]] = []
            seen: set[int] = set()
            for _ in range(options.max_pages):
                data = await op.request(_path(job["name"], run_id) + f"/tasks?limit=200&offset={offset}")
                if (
                    not isinstance(data.get("tasks"), list)
                    or len(data["tasks"]) > 200
                    or data.get("offset") != offset
                    or data.get("total") != run["tasks"]
                    or type(data.get("next_offset")) is not int
                ):
                    raise JobError("invalid_response")
                for task in data["tasks"]:
                    if (
                        not isinstance(task, dict)
                        or task.get("run_id") != run_id
                        or not _nonnegative(task.get("task_index"))
                        or task["task_index"] >= run["tasks"]
                        or task["task_index"] in seen
                    ):
                        raise JobError("invalid_response")
                    try:
                        inspect_job_input(self._contract, JobInputBinding(task["input_id"], task["input_ref"]))
                    except (JobError, KeyError, TypeError):
                        raise JobError("invalid_response") from None
                    seen.add(task["task_index"])
                    tasks.append(task)
                if data["next_offset"] == -1:
                    if len(tasks) != run["tasks"]:
                        raise JobError("invalid_response")
                    break
                if (
                    data["next_offset"] != offset + len(data["tasks"])
                    or not offset < data["next_offset"] < run["tasks"]
                ):
                    raise JobError("invalid_response")
                offset = data["next_offset"]
            if len(tasks) != run["tasks"]:
                raise JobError("limit")
            tasks.sort(key=lambda t: t["task_index"])
            refs = [{"input_id": t["input_id"], "input_ref": t["input_ref"]} for t in tasks]
            if job_hash(job_bytes(refs)) != run["input_digest"]:
                raise JobError("invalid_response")
            try:
                c = JobCheckpoint.from_dict(
                    {
                        "version": 1,
                        "base_url": base,
                        "job_name": job["name"],
                        "job_id": job["id"],
                        "account_id": job["account_id"],
                        "run_id": run_id,
                        "contract_sha256": self._contract.sha256,
                        "input_digest": run["input_digest"],
                        "source_run_id": run.get("source_run_id"),
                        "inputs": [
                            {
                                "input_id": t["input_id"],
                                "binding_sha256": job_hash(t["input_ref"].encode()),
                                "source_task_index": t.get("source_task_index"),
                            }
                            for t in tasks
                        ],
                    }
                )
                _progress(run, c)
                return TypedJobRun(self._owner, self._contract, c, self._store)
            except (JobError, KeyError, TypeError):
                raise JobError("invalid_response") from None

        return _invoke(self._owner, options, base, work)

    def submit(
        self, inputs: list[JobInput[Input]], *, options: JobSubmitOptions
    ) -> Coroutine[Any, Any, TypedJobRun[Input, Output]]:
        if not isinstance(inputs, list) or not 1 <= len(inputs) <= options.max_items:
            raise JobError("invalid_input")
        captured = [JobInput(i.id, self._contract.input(i.value)) for i in inputs]
        if (
            not captured
            or len(captured) > options.max_items
            or len({i.id for i in captured}) != len(captured)
            or any(not job_input_id(i.id) for i in captured)
        ):
            raise JobError("invalid_input")
        base = _base(str(self._owner.async_httpx_client.base_url))
        if options.expected_base_url is not None and base != _base(options.expected_base_url):
            raise JobError('invalid_receipt')
        if options.expected_contract_sha256 is not None and options.expected_contract_sha256 != self._contract.sha256:
            raise JobError('invalid_receipt')

        async def work(op: _Operation) -> TypedJobRun[Input, Output]:
            job = await op.request(_path(self._contract.spec["name"]))
            if (
                not _uuid(job.get("id"))
                or not _uuid(job.get("account_id"))
                or job.get("name") != self._contract.spec["name"]
                or options.expected_account_id is not None and job.get('account_id') != options.expected_account_id
            ):
                raise JobError("invalid_response")
            refs = []
            for input in captured:
                op.check()
                refs.append(
                    (
                        await prepare_job_input(self._contract, input, JobContext(input.id, 0, op.check), self._store)
                    ).to_dict()
                )
            body: dict[str, Any] = {"inputs": refs}
            for key in ("parallelism", "retry_max", "failure_policy"):
                if getattr(options, key) is not None:
                    body[key] = getattr(options, key)
            run = await op.request(_path(job["name"]) + "/runs", "POST", body, options.idempotency_key)
            try:
                c = JobCheckpoint.from_dict(
                    {
                        "version": 1,
                        "base_url": base,
                        "job_name": job["name"],
                        "job_id": job["id"],
                        "account_id": job["account_id"],
                        "run_id": run["id"],
                        "contract_sha256": self._contract.sha256,
                        "input_digest": job_hash(job_bytes(refs)),
                        "source_run_id": None,
                        "inputs": [
                            {
                                "input_id": r["input_id"],
                                "binding_sha256": job_hash(r["input_ref"].encode()),
                                "source_task_index": None,
                            }
                            for r in refs
                        ],
                    }
                )
                _progress(run, c)
                return TypedJobRun(self._owner, self._contract, c, self._store)
            except (JobError, KeyError, TypeError):
                raise JobError("acceptance_unknown") from None

        return _invoke(self._owner, options, base, work)
