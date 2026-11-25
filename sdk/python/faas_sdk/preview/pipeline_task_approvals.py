"""Typed configuration and outcomes for an assigned approval gate."""

import re
from collections.abc import Sequence
from typing import Generic, Literal, TypedDict, TypeVar

from ..job_runtime import JobContract, JobError
from .pipeline_gates import PipelineGateError, PipelineGateService, PipelineGateView
from .pipeline_task_handoffs import PipelineGateHandoff, PipelineTaskHandoff
from .query_tools import QueryToolError

R = TypeVar("R")
D = TypeVar("D")
A = TypeVar("A")
PipelineTaskApprovalChoice = Literal["approved", "rejected"]


class PipelineTaskApprovalView(PipelineTaskHandoff, Generic[R]):
    title: str
    review: R
    contract_sha256: str
    review_sha256: str
    decision_key: str


class PipelineTaskApprovalReceipt(TypedDict):
    handoff_id: str
    state: PipelineTaskApprovalChoice
    decided_at: str


class PipelineTaskApproval(Generic[R, D, A]):
    def __init__(self, *, contract: JobContract[R, D], service: PipelineGateService[R, D, A],
                 title: str, scopes: Sequence[str]):
        if (not isinstance(title, str) or not title.strip() or len(title.encode("utf-16-le")) > 2048
            or isinstance(scopes, str) or not 1 <= len(scopes) <= 32
            or any(not isinstance(scope, str) or not re.fullmatch(r"[\x21\x23-\x5b\x5d-\x7e]{1,128}", scope) for scope in scopes)
            or len(set(scopes)) != len(scopes)):
            raise QueryToolError("invalid_input")
        self._contract: JobContract[R, D] = JobContract(contract.spec)
        self._service, self.title, self.scopes = service, title, tuple(scopes)
        self.contract_sha256 = self._contract.sha256

    async def inspect(self, identity: str, principal: A, *, timeout: float) -> PipelineGateView[R, D] | None:
        try:
            gate = await self._service.inspect(identity, principal, timeout=timeout)
            if gate.kind == "approval":
                self._contract.input(gate.review)
                return gate
            return None
        except PipelineGateError as error:
            if error.code == "forbidden":
                return None
            raise

    async def decide(self, identity: str, binding: str, state: PipelineTaskApprovalChoice, value: object,
                     principal: A, handoff: PipelineGateHandoff, *, timeout: float) -> str:
        try:
            payload = self._contract.output(value)
        except JobError:
            raise QueryToolError("invalid_input") from None
        try:
            gate = await self._service.decide_handoff(identity, binding_sha256=binding, state=state, payload=payload,
                                                      auth=principal, handoff=handoff, timeout=timeout)
        except PipelineGateError as error:
            if error.code == "forbidden":
                raise QueryToolError("not_found") from None
            if error.code in ("conflict", "inactive", "invalid_input"):
                raise QueryToolError("invalid_input") from None
            raise
        if gate.decision is None or gate.decision.state != state:
            raise QueryToolError("invalid_output")
        return gate.decision.decided_at
