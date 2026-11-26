"""Typed read-only previews bound to a concrete pipeline start."""

from __future__ import annotations

import hmac
import re
import time
from collections.abc import Awaitable, Callable
from dataclasses import dataclass
from typing import Any, Generic, TypedDict, TypeVar

from ..job_runtime import JobContract, job_value_hash
from .pipeline_state import PinnedPipelineSource, _hash, _parse_pinned_source
from .pipeline_tool_state import PipelineToolError

I = TypeVar("I")
P = TypeVar("P")
A = TypeVar("A")
_DOMAIN = "gregale.pipeline-start-preview.v1"
_LIFETIME_SECONDS = 300
_TOKEN_PATTERN = r"^[1-9][0-9]{0,12}\.[a-f0-9]{64}$"


class PipelineStartPreview(TypedDict, Generic[P]):
    summary: P
    preview_token: str
    expires_at: int


@dataclass(frozen=True, kw_only=True)
class PipelineStartPreviewSource(Generic[P]):
    source_version: str
    summary: P
    pinned_source: PinnedPipelineSource | None = None


@dataclass(frozen=True, kw_only=True)
class PipelineStartPreviewPolicy(Generic[I, P, A]):
    contract: JobContract[None, P]
    signing_key: str
    read: Callable[[I, A, float], Awaitable[PipelineStartPreviewSource[P]]]

    def __post_init__(self) -> None:
        if (
            not isinstance(self.signing_key, str) or len(self.signing_key.encode()) < 32
            or not callable(self.read) or self.contract.spec["input"]["type"] != "null"
        ):
            raise PipelineToolError("invalid_input")


def _preview_expired(expires: int) -> bool:
    return expires <= int(time.time())


def _preview_contract(schema: dict[str, Any]) -> dict[str, Any]:
    return {"version": 1, "name": "pipeline-start-preview", "input": {"type": "null"}, "output": schema}


async def _check_preview(
    policy: PipelineStartPreviewPolicy[I, P, A], intent: dict[str, Any], input: I, access: A, timeout: float,
    *, confirm: bool = False, token: str | None = None,
) -> tuple[PipelineStartPreview[P], PinnedPipelineSource | None]:
    if confirm and not token:
        raise PipelineToolError("preview_required")
    now = int(time.time())
    expires = now + _LIFETIME_SECONDS
    if token:
        if not re.fullmatch(_TOKEN_PATTERN, token):
            raise PipelineToolError("preview_stale")
        expires = int(token.split(".", 1)[0])
        if _preview_expired(expires):
            raise PipelineToolError("preview_stale")
    prepared = await policy.read(input, access, timeout)
    if not _hash(prepared.source_version):
        raise PipelineToolError("request_failed")
    summary = policy.contract.output(prepared.summary)
    scope = intent["scope"]
    pinned = _parse_pinned_source(
        prepared.pinned_source, scope["base_url"], scope["app_slug"], intent["owner_id"],
    ) if prepared.pinned_source is not None else None
    fingerprint = job_value_hash([
        _DOMAIN, intent["identity"], intent["input_sha256"],
        policy.contract.sha256, prepared.source_version, summary, expires,
        *([pinned] if pinned is not None else []),
    ])
    proof = str(expires) + "." + hmac.digest(policy.signing_key.encode(), fingerprint.encode(), "sha256").hex()
    if _preview_expired(expires) or (token and not hmac.compare_digest(token, proof)):
        raise PipelineToolError("preview_stale")
    return PipelineStartPreview(summary=summary, preview_token=proof, expires_at=expires), pinned
