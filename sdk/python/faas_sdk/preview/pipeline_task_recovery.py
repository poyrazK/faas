"""Protocol and authorization inputs for linked pipeline Task recovery."""

from __future__ import annotations

import copy
import hmac
import re
import uuid
from collections.abc import Awaitable, Callable, Sequence
from dataclasses import dataclass
from typing import Any, Generic, Literal, NotRequired, TypedDict, TypeVar, cast

from ..job_runtime import job_value_hash
from .pipeline_start_preview import _TOKEN_PATTERN, PipelineStartPreview
from .pipeline_state import _hash, _uuid
from .query_tools import QueryToolError

A = TypeVar("A")
P = TypeVar("P")


class PipelineTaskRecoverySource(TypedDict):
    task_id: str
    tool: str
    request_id: str
    run_id: str
    receipt_identity: str
    failure: Literal["source_unavailable"]


class PipelineTaskRecoveryPreview(PipelineStartPreview[P]):
    replaces_task_id: str
    previous_source_sha256: str


class PipelineTaskRecoveryInput(TypedDict):
    input: Any
    previous_source_sha256: str


@dataclass(frozen=True, kw_only=True)
class PipelineTaskRecoveryPolicy(Generic[A]):
    scopes: Sequence[str]
    prepare: Callable[[PipelineTaskRecoverySource, A, float], Awaitable[PipelineTaskRecoveryInput | None]]


class _RecoveryRequest(TypedDict):
    task_id: str
    recovery_request_id: str
    expected_contract_sha256: str
    preview_token: NotRequired[str]


def _recovery_request(value: Any, confirm: bool) -> _RecoveryRequest:
    value = copy.deepcopy(value)
    keys = {"task_id", "recovery_request_id", "expected_contract_sha256"}
    if confirm:
        keys.add("preview_token")
    if (
        not isinstance(value, dict) or set(value) != keys or not _uuid(value["task_id"])
        or not _uuid(value["recovery_request_id"]) or not _hash(value["expected_contract_sha256"])
    ):
        raise QueryToolError("invalid_input")
    if confirm:
        token = value["preview_token"]
        if not isinstance(token, str) or not re.fullmatch(_TOKEN_PATTERN, token):
            raise QueryToolError("invalid_input")
    return cast(_RecoveryRequest, value)


def _recovery_request_id(key: bytes, owner: str, original: str, request_id: str) -> str:
    fingerprint = job_value_hash(["gregale.pipeline-task-recovery.v1", owner, original, request_id])
    digest = hmac.digest(key, fingerprint.encode(), "sha256")
    return str(uuid.UUID(bytes=digest[:16], version=4))
