"""Bridge an MCP form to a separately authorized typed external-input gate."""

from __future__ import annotations

import base64
import copy
import hashlib
import hmac
import json
import re
from collections.abc import Sequence
from dataclasses import dataclass
from typing import Any, Generic, Literal, TypedDict, TypeVar

from ..job_runtime import JobContract, JobError, job_value_hash
from .pipeline_gates import GateKind, PipelineGateError, PipelineGateService
from .pipeline_state import _hash
from .pipeline_task_handoffs import PipelineGateHandoff
from .query_tools import QueryToolError

A = TypeVar("A")


class _FormParams(TypedDict):
    mode: Literal["form"]
    message: str
    requestedSchema: dict[str, Any]


class PipelineTaskFormRequest(TypedDict):
    method: Literal["elicitation/create"]
    params: _FormParams


@dataclass(frozen=True, slots=True)
class _Gate:
    id: str
    run_id: str
    kind: GateKind
    binding_sha256: str
    can_decide: bool
    decision: str | None


class PipelineTaskInputForm(Generic[A]):
    def __init__(
        self,
        *,
        contract: JobContract[Any, Any],
        service: PipelineGateService[Any, Any, A],
        message: str,
        scopes: Sequence[str],
    ):
        self._contract = JobContract[Any, Any](contract.spec)
        self._service, self._message, self.scopes = service, message, tuple(scopes)
        schema = self._contract.spec["output"]
        if (
            not isinstance(message, str)
            or not message.strip()
            or len(message.encode("utf-16-le")) > 2048
            or isinstance(scopes, str)
            or not 1 <= len(self.scopes) <= 32
            or any(
                not isinstance(scope, str) or not re.fullmatch(r"[\x21\x23-\x5b\x5d-\x7e]{1,128}", scope)
                for scope in self.scopes
            )
            or len(set(self.scopes)) != len(self.scopes)
            or schema["type"] != "object"
            or not schema["properties"]
            or any(
                field["type"] not in ("string", "number", "integer", "boolean")
                for field in schema["properties"].values()
            )
            or len(json.dumps(schema, ensure_ascii=False, separators=(",", ":")).encode()) > 16384
        ):
            raise QueryToolError("invalid_input")

    @property
    def request(self) -> PipelineTaskFormRequest:
        return PipelineTaskFormRequest(
            method="elicitation/create",
            params=_FormParams(
                mode="form", message=self._message, requestedSchema=copy.deepcopy(self._contract.spec["output"])
            ),
        )

    async def inspect(self, identity: str, principal: A, *, timeout: float) -> _Gate | None:
        try:
            gate = await self._service.inspect(identity, principal, timeout=timeout)
        except PipelineGateError as error:
            if error.code == "forbidden":
                return None
            raise
        return _Gate(
            gate.id,
            gate.run_id,
            gate.kind,
            gate.binding_sha256,
            gate.can_decide,
            gate.decision.state if gate.decision else None,
        )

    async def submit_handoff(
        self, identity: str, binding: str, value: Any, principal: A, handoff: PipelineGateHandoff, *, timeout: float,
    ) -> None:
        await self.submit(identity, binding, value, principal, timeout=timeout, _handoff=handoff)

    async def submit(
        self, identity: str, binding: str, value: Any, principal: A, *,
        timeout: float, _handoff: PipelineGateHandoff | None = None,
    ) -> None:
        try:
            payload = self._contract.output(value)
            if len(json.dumps(payload, ensure_ascii=False, separators=(",", ":"), allow_nan=False).encode()) > 16384:
                raise ValueError
        except (JobError, ValueError, TypeError, UnicodeError):
            raise QueryToolError("invalid_input") from None
        try:
            if _handoff is None:
                await self._service.decide(
                    identity, binding_sha256=binding, state="submitted", payload=payload, auth=principal, timeout=timeout,
                )
            else:
                await self._service.decide_handoff(
                    identity, binding_sha256=binding, payload=payload, auth=principal, timeout=timeout, handoff=_handoff,
                )
        except PipelineGateError as error:
            if error.code == "forbidden":
                raise QueryToolError("not_found") from None
            if error.code in ("conflict", "inactive"):
                raise QueryToolError("invalid_input") from None
            raise


_Anchor = tuple[str, str, str] | tuple[str, str, str, str]
_KEY = re.compile(r"[A-Za-z0-9_-]{43}\.[A-Za-z0-9_-]{43}\.[A-Za-z0-9_-]{22}")


def _form_key(secret: bytes, anchor: _Anchor, gate_id: str, binding: str) -> str:
    digest = job_value_hash(["gregale.pipeline-task-form.v1", *anchor, gate_id, binding])
    proof = hmac.new(secret, digest.encode(), hashlib.sha256).digest()[:16]
    return ".".join(
        base64.urlsafe_b64encode(part).decode().rstrip("=")
        for part in (
            bytes.fromhex(gate_id),
            bytes.fromhex(binding),
            proof,
        )
    )


def _parse_form_reply(secret: bytes, anchor: _Anchor, responses: Any) -> tuple[str, str, Any]:
    if not isinstance(responses, dict) or len(responses) != 1:
        raise QueryToolError("invalid_input")
    key, value = next(iter(responses.items()))
    if (
        not isinstance(key, str)
        or not _KEY.fullmatch(key)
        or not isinstance(value, dict)
        or set(value) != {"action", "content"}
        or value["action"] != "accept"
    ):
        raise QueryToolError("invalid_input")
    encoded_id, encoded_binding, _ = key.split(".")
    gate_id = base64.urlsafe_b64decode(encoded_id + "=").hex()
    binding = base64.urlsafe_b64decode(encoded_binding + "=").hex()
    if (
        not _hash(gate_id)
        or not _hash(binding)
        or not hmac.compare_digest(key, _form_key(secret, anchor, gate_id, binding))
    ):
        raise QueryToolError("invalid_input")
    return gate_id, binding, value["content"]
