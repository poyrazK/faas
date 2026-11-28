"""Portable typed job contracts. Storage adapters own their credentials."""

from __future__ import annotations

import asyncio
import base64
import copy
import hashlib
import inspect
import json
import math
import os
import re
import signal
import struct
import uuid
from collections.abc import Awaitable, Callable
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Generic, Literal, Protocol, TypeVar, cast
from urllib.parse import urlsplit

from ._async_request import RequestOptions, RequestTimeoutError, run_request

Input = TypeVar("Input")
Output = TypeVar("Output")
JOB_MAX_BYTES = 1024 * 1024
JobCode = Literal[
    "invalid_contract",
    "invalid_input",
    "invalid_output",
    "invalid_reference",
    "integrity_failed",
    "store_required",
    "handler_failed",
    "deadline",
    "cancelled",
    "invalid_receipt",
    "invalid_response",
    "request_failed",
    "acceptance_unknown",
    "limit",
    "poll_limit",
    "progress_failed",
    "not_terminal",
    "no_failed_inputs",
]


class JobError(Exception):
    def __init__(self, code: JobCode, status: int | None = None):
        super().__init__(f"SDK job stopped: {code}")
        self.code, self.status = code, status


def job_hash(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def job_input_id(value: Any) -> bool:
    return isinstance(value, str) and re.fullmatch(r"[A-Za-z0-9_.:-]{1,128}", value) is not None


def _stable(value: Any) -> str:
    if isinstance(value, (float, int)) and not isinstance(value, bool):
        return "n" + struct.pack(">d", 0 if value == 0 else value).hex()
    if isinstance(value, list):
        return "[" + ",".join(_stable(v) for v in value) + "]"
    if isinstance(value, dict):
        return "{" + ",".join(json.dumps(k) + ":" + _stable(value[k]) for k in sorted(value)) + "}"
    return json.dumps(value, ensure_ascii=False, separators=(",", ":"), allow_nan=False)


def job_value_hash(value: Any) -> str:
    """Internal canonical JSON value hash; validate values before hashing."""
    return job_hash(_stable(value).encode())


def _schema_check(schema: Any, depth: int = 0) -> None:
    if (
        not isinstance(schema, dict)
        or depth > 12
        or schema.get("type") not in ("object", "array", "string", "integer", "number", "boolean", "null")
    ):
        raise JobError("invalid_contract")
    kind = schema["type"]
    allowed = {"type", "enum"} | {
        "object": {"properties", "required", "additionalProperties"},
        "array": {"items", "minItems", "maxItems"},
        "string": {"minLength", "maxLength"},
        "integer": {"minimum", "maximum"},
        "number": {"minimum", "maximum"},
    }.get(kind, set())
    if set(schema) - allowed:
        raise JobError("invalid_contract")
    if kind == "object":
        props, required = schema.get("properties"), schema.get("required")
        if (
            not isinstance(props, dict)
            or len(props) > 128
            or schema.get("additionalProperties") is not False
            or not isinstance(required, list)
            or any(not isinstance(k, str) or k not in props for k in required)
            or len(set(required)) != len(required)
        ):
            raise JobError("invalid_contract")
        for name, child in props.items():
            if not re.fullmatch(r"[A-Za-z][A-Za-z0-9_]{0,63}", name) or name in (
                "constructor",
                "prototype",
                "__proto__",
            ):
                raise JobError("invalid_contract")
            _schema_check(child, depth + 1)
    if kind == "array":
        _schema_check(schema.get("items"), depth + 1)
    for minimum, maximum in (("minLength", "maxLength"), ("minItems", "maxItems"), ("minimum", "maximum")):
        for key in (minimum, maximum):
            if key in schema:
                value = schema[key]
                if (
                    type(value) not in (int, float)
                    or not math.isfinite(value)
                    or key not in ("minimum", "maximum")
                    and (int(value) != value or value < 0 or value > 9007199254740991)
                ):
                    raise JobError("invalid_contract")
        if minimum in schema and maximum in schema and schema[minimum] > schema[maximum]:
            raise JobError("invalid_contract")
    if "enum" in schema:
        values = schema["enum"]
        if not isinstance(values, list) or not 1 <= len(values) <= 128 or kind in ("object", "array"):
            raise JobError("invalid_contract")
        for v in values:
            if type(v) in (float, int) and (not math.isfinite(v) or int(v) != v or abs(v) > 9007199254740991):
                raise JobError("invalid_contract")
            _validate(v, {k: v for k, v in schema.items() if k != "enum"}, "invalid_contract")


def _validate(value: Any, schema: dict[str, Any], code: JobCode) -> None:
    def bad() -> None:
        raise JobError(code)

    kind = schema["type"]
    if kind == "object":
        if (
            not isinstance(value, dict)
            or set(schema["required"]) - value.keys()
            or value.keys() - schema["properties"].keys()
        ):
            bad()
        for k, child in schema["properties"].items():
            if k in value:
                _validate(value[k], child, code)
    elif kind == "array":
        if not isinstance(value, list) or not schema.get("minItems", 0) <= len(value) <= schema.get(
            "maxItems", JOB_MAX_BYTES
        ):
            bad()
        for child in value:
            _validate(child, schema["items"], code)
    elif kind == "string":
        if not isinstance(value, str) or not schema.get("minLength", 0) <= len(value) <= schema.get(
            "maxLength", JOB_MAX_BYTES
        ):
            bad()
        try:
            value.encode("utf-8")
        except UnicodeError:
            bad()
    elif kind in ("integer", "number"):
        if (
            type(value) not in (int, float)
            or not math.isfinite(value)
            or kind == "integer"
            and (int(value) != value or abs(value) > 9007199254740991)
            or not schema.get("minimum", -math.inf) <= value <= schema.get("maximum", math.inf)
        ):
            bad()
    elif (type(value) is not bool) if kind == "boolean" else (value is not None):
        bad()
    if "enum" in schema and not any(
        type(value) is type(v) and value == v or type(value) in (int, float) and type(v) in (int, float) and value == v
        for v in schema["enum"]
    ):
        bad()


def job_bytes(value: Any) -> bytes:
    try:
        data = json.dumps(value, ensure_ascii=False, separators=(",", ":"), allow_nan=False).encode("utf-8")
    except (ValueError, TypeError, UnicodeError):
        raise JobError("invalid_input") from None
    if len(data) > JOB_MAX_BYTES:
        raise JobError("limit")
    return data


def job_uri(value: Any) -> bool:
    try:
        u = urlsplit(value)
        return (
            isinstance(value, str)
            and len(value.encode()) <= 1024
            and u.scheme in ("s3", "obj", "gs")
            and bool(u.hostname)
            and len(u.path) > 1
            and not (u.username or u.password or u.query or u.fragment)
        )
    except (ValueError, TypeError, AttributeError):
        return False


class JobContract(Generic[Input, Output]):
    def __init__(self, spec: dict[str, Any]):
        try:
            if (
                set(spec) != {"version", "name", "input", "output"}
                or type(spec["version"]) is not int
                or spec["version"] != 1
                or not re.fullmatch(r"[a-z0-9][a-z0-9-]{1,38}[a-z0-9]", spec["name"])
            ):
                raise ValueError
            _schema_check(spec["input"])
            _schema_check(spec["output"])
            self._json = job_bytes(spec).decode()
            self._sha256 = job_hash(_stable(spec).encode())
        except (ValueError, TypeError, KeyError, JobError, OverflowError, UnicodeError):
            raise JobError("invalid_contract") from None

    @property
    def spec(self) -> dict[str, Any]:
        return cast(dict[str, Any], json.loads(self._json))

    @property
    def sha256(self) -> str:
        return self._sha256

    def input(self, value: Any) -> Input:
        _validate(value, self.spec["input"], "invalid_input")
        job_bytes(value)
        return cast(Input, copy.deepcopy(value))

    def output(self, value: Any) -> Output:
        _validate(value, self.spec["output"], "invalid_output")
        job_bytes(value)
        return cast(Output, copy.deepcopy(value))


@dataclass(frozen=True, slots=True)
class JobContext:
    input_id: str
    attempt: int
    _check: Callable[[], None]

    def check_cancelled(self) -> None:
        self._check()


class JobStore(Protocol):
    async def put(self, data: bytes, sha256: str, context: JobContext) -> str: ...
    async def get(self, uri: str, context: JobContext) -> bytes: ...


@dataclass(frozen=True, slots=True)
class JobInput(Generic[Input]):
    id: str
    value: Input


@dataclass(frozen=True, slots=True)
class JobInputBinding:
    input_id: str
    input_ref: str

    def to_dict(self) -> dict[str, str]:
        return {"input_id": self.input_id, "input_ref": self.input_ref}


async def _scope(timeout: float, work: Callable[[Callable[[], None]], Awaitable[Output]]) -> Output:
    if type(timeout) not in (int, float) or not math.isfinite(timeout) or not 0 < timeout <= 2147483.647:
        raise ValueError("A positive bounded timeout is required")
    active = True
    start = asyncio.get_running_loop().time()

    def check() -> None:
        if not active:
            raise asyncio.CancelledError
        if asyncio.get_running_loop().time() - start >= timeout:
            raise JobError("deadline")

    async def run() -> Output:
        check()
        result = await work(check)
        check()
        return result

    try:
        return await run_request(object(), run, RequestOptions(timeout=timeout), timeout)
    except RequestTimeoutError:
        raise JobError("deadline") from None
    finally:
        active = False


def _encode(value: Any) -> str:
    return "gregale-job:v1:" + base64.urlsafe_b64encode(job_bytes(value)).decode().rstrip("=")


async def prepare_job_input(
    contract: JobContract[Input, Output], input: JobInput[Input], context: JobContext, store: JobStore | None = None
) -> JobInputBinding:
    if not job_input_id(input.id):
        raise JobError("invalid_input")
    envelope = {"version": 1, "contract": contract.sha256, "input_id": input.id, "data": contract.input(input.value)}
    reference = _encode(envelope)
    if len(reference) > 2048:
        if not store:
            raise JobError("store_required")
        data = job_bytes(envelope)
        context.check_cancelled()
        ref = await store.put(data, job_hash(data), context)
        context.check_cancelled()
        if not job_uri(ref):
            raise JobError("invalid_reference")
        reference = _encode(
            {
                "version": 1,
                "contract": contract.sha256,
                "input_id": input.id,
                "ref": ref,
                "sha256": job_hash(data),
                "size": len(data),
            }
        )
    if len(reference) > 2048:
        raise JobError("limit")
    return JobInputBinding(input.id, reference)


def inspect_job_input(contract: JobContract[Any, Any], binding: JobInputBinding) -> dict[str, Any]:
    try:
        if (
            not job_input_id(binding.input_id)
            or not re.fullmatch(r"gregale-job:v1:[A-Za-z0-9_-]+", binding.input_ref)
            or len(binding.input_ref) > 2048
        ):
            raise ValueError
        encoded = binding.input_ref[len("gregale-job:v1:") :]
        raw = base64.urlsafe_b64decode(encoded + "=" * (-len(encoded) % 4))
        if base64.urlsafe_b64encode(raw).decode().rstrip("=") != encoded:
            raise ValueError
        envelope = json.loads(raw)
        if (
            envelope["version"] != 1
            or envelope["contract"] != contract.sha256
            or envelope["input_id"] != binding.input_id
        ):
            raise ValueError
        if "data" in envelope:
            if set(envelope) != {"version", "contract", "input_id", "data"}:
                raise ValueError
            contract.input(envelope["data"])
        elif (
            set(envelope) != {"version", "contract", "input_id", "ref", "sha256", "size"}
            or not job_uri(envelope["ref"])
            or not re.fullmatch(r"[a-f0-9]{64}", envelope["sha256"])
            or type(envelope["size"]) is not int
            or not 1 <= envelope["size"] <= JOB_MAX_BYTES
        ):
            raise ValueError
        return cast(dict[str, Any], envelope)
    except (ValueError, TypeError, KeyError, JobError, AttributeError):
        raise JobError("invalid_reference") from None


async def read_job_input(
    contract: JobContract[Input, Output], binding: JobInputBinding, context: JobContext, store: JobStore | None = None
) -> Input:
    envelope = inspect_job_input(contract, binding)
    if "data" in envelope:
        return contract.input(envelope["data"])
    if not store:
        raise JobError("store_required")
    context.check_cancelled()
    raw = await store.get(envelope["ref"], context)
    context.check_cancelled()
    if len(raw) != envelope["size"] or job_hash(raw) != envelope["sha256"]:
        raise JobError("integrity_failed")
    return cast(Input, _decode(contract, binding.input_id, raw, output=False))


def _decode(contract: JobContract[Input, Output], input_id: str, raw: bytes, *, output: bool) -> Any:
    if len(raw) > JOB_MAX_BYTES:
        raise JobError("limit")
    try:
        e = json.loads(raw)
        if (
            set(e) != {"version", "contract", "input_id", "data"}
            or type(e["version"]) is bool
            or e["version"] != 1
            or e["contract"] != contract.sha256
            or e["input_id"] != input_id
        ):
            raise ValueError
        return contract.output(e["data"]) if output else contract.input(e["data"])
    except (ValueError, TypeError, KeyError, JobError):
        raise JobError("invalid_output" if output else "invalid_input") from None


def decode_job_output(contract: JobContract[Input, Output], input_id: str, raw: bytes) -> Output:
    return cast(Output, _decode(contract, input_id, raw, output=True))


@dataclass(frozen=True, slots=True)
class LocalJobOutcome(Generic[Output]):
    input_id: str
    outcome: Literal["succeeded", "failed"]
    attempts: int
    result: Output | None = None
    code: JobCode | None = None


async def run_local_job_batch(
    contract: JobContract[Input, Output],
    inputs: list[JobInput[Input]],
    handler: Callable[[Input, JobContext], Output | Awaitable[Output]],
    *,
    timeout: float,
    concurrency: int = 1,
    max_attempts: int = 1,
) -> tuple[LocalJobOutcome[Output], ...]:
    if (
        type(concurrency) is not int
        or not 1 <= concurrency <= 64
        or type(max_attempts) is not int
        or not 1 <= max_attempts <= 10
        or not 1 <= len(inputs) <= 10000
        or len({i.id for i in inputs}) != len(inputs)
        or any(not job_input_id(i.id) for i in inputs)
    ):
        raise JobError("invalid_input")
    captured = [JobInput(i.id, contract.input(i.value)) for i in inputs]

    async def work(check: Callable[[], None]) -> tuple[LocalJobOutcome[Output], ...]:
        semaphore = asyncio.Semaphore(concurrency)

        async def item(input: JobInput[Input]) -> LocalJobOutcome[Output]:
            async with semaphore:
                for attempt in range(1, max_attempts + 1):
                    check()
                    try:
                        value = handler(copy.deepcopy(input.value), JobContext(input.id, attempt, check))
                        result = await value if inspect.isawaitable(value) else value
                        check()
                        return LocalJobOutcome(input.id, "succeeded", attempt, contract.output(result))
                    except Exception as error:
                        check()
                        if attempt == max_attempts:
                            return LocalJobOutcome(
                                input.id,
                                "failed",
                                attempt,
                                code=error.code if isinstance(error, JobError) else "handler_failed",
                            )
                raise AssertionError

        tasks = [asyncio.create_task(item(i)) for i in captured]
        try:
            return tuple(await asyncio.gather(*tasks))
        finally:
            for task in tasks:
                if not task.done():
                    task.cancel()

    return await _scope(timeout, work)


async def run_job_task(
    contract: JobContract[Input, Output],
    handler: Callable[[Input, JobContext], Output | Awaitable[Output]],
    *,
    timeout: float,
    store: JobStore,
    env: dict[str, str] | None = None,
    manage_signals: bool = True,
) -> Output:
    env = dict(os.environ if env is None else env)
    try:
        input_id, ref, path = (
            env[k] for k in ("GREGALE_INPUT_ID", "GREGALE_INPUT_REF", "GREGALE_OUTPUT_MANIFEST_PATH")
        )
        attempt, index, count = (
            int(env[k]) for k in ("GREGALE_TASK_ATTEMPT", "GREGALE_TASK_INDEX", "GREGALE_TASK_COUNT")
        )
        if (
            not job_input_id(input_id)
            or not ref
            or not path
            or attempt < 1
            or not 0 <= index < count
            or not env.get("GREGALE_RUN_ID")
        ):
            raise ValueError
    except (ValueError, KeyError):
        raise JobError("invalid_input") from None
    task = asyncio.current_task()
    old = None
    if manage_signals:
        old = signal.signal(signal.SIGTERM, lambda *_: task.cancel() if task else None)

    async def work(check: Callable[[], None]) -> Output:
        context = JobContext(input_id, attempt, check)
        value = await read_job_input(contract, JobInputBinding(input_id, ref), context, store)
        check()
        try:
            output = handler(value, context)
            result = contract.output(await output if inspect.isawaitable(output) else output)
        except Exception as error:
            check()
            raise error if isinstance(error, JobError) else JobError("handler_failed") from None
        check()
        data = job_bytes({"version": 1, "contract": contract.sha256, "input_id": input_id, "data": result})
        sha = job_hash(data)
        uri = await store.put(data, sha, context)
        check()
        if not job_uri(uri):
            raise JobError("invalid_reference")
        manifest = job_bytes(
            {
                "version": 1,
                "artifacts": [{"name": "result", "uri": uri, "size_bytes": len(data), "sha256": "sha256:" + sha}],
                "outcome_code": "typed_job_succeeded",
            }
        )
        if len(manifest) > 4096:
            raise JobError("limit")
        temporary = Path(path).with_name(".gregale-job-" + uuid.uuid4().hex)
        try:
            check()
            with temporary.open("xb") as file:
                os.chmod(temporary, 0o600)
                file.write(manifest)
            check()
            temporary.replace(path)
        finally:
            temporary.unlink(missing_ok=True)
        return result

    try:
        return await _scope(timeout, work)
    finally:
        if manage_signals and old is not None:
            signal.signal(signal.SIGTERM, old)


class LocalJobStore:
    """Local tests only. Maps s3://gregale-local/<digest> to private files."""

    def __init__(self, directory: str | Path):
        self.directory = Path(directory)

    async def put(self, data: bytes, sha256: str, context: JobContext) -> str:
        context.check_cancelled()
        if job_hash(data) != sha256 or len(data) > JOB_MAX_BYTES:
            raise JobError("integrity_failed")
        self.directory.mkdir(mode=0o700, parents=True, exist_ok=True)
        path = self.directory / sha256
        try:
            with path.open("xb") as file:
                os.chmod(path, 0o600)
                file.write(data)
        except FileExistsError:
            if job_hash(path.read_bytes()) != sha256:
                raise JobError("integrity_failed") from None
        context.check_cancelled()
        return "s3://gregale-local/" + sha256

    async def get(self, uri: str, context: JobContext) -> bytes:
        context.check_cancelled()
        match = re.fullmatch(r"s3://gregale-local/([a-f0-9]{64})", uri)
        if not match:
            raise JobError("invalid_reference")
        path = self.directory / match[1]
        with path.open("rb") as file:
            data = file.read(JOB_MAX_BYTES + 1)
        context.check_cancelled()
        if len(data) > JOB_MAX_BYTES or job_hash(data) != match[1]:
            raise JobError("integrity_failed")
        return data
