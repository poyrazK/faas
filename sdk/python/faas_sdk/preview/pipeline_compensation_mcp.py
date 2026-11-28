"""Planning plus the existing durable MCP lifecycle; human approval stays separate."""

from __future__ import annotations

import copy
import json
from typing import Any, Generic, Literal, TypeVar

from ..job_runtime import job_value_hash
from .pipeline_compensation_state import CompensationError
from .pipeline_compensations import PipelineCompensation
from .pipeline_task_manifest import _PipelineContractMetadata
from .pipeline_tool_mcp import PipelineToolMCP, PipelineToolMCPError
from .pipeline_tool_state import request_id

I = TypeVar("I")
O = TypeVar("O")
A = TypeVar("A")


class CompensationMCP(Generic[I, O, A]):
    def __init__(
        self,
        recovery: PipelineCompensation[I, O, A],
        *,
        protocol_version: Literal["2025-11-25", "2026-07-28"] = "2025-11-25",
    ):
        self._recovery, self._protocol = recovery, protocol_version
        self._inner = PipelineToolMCP(recovery.tool, protocol_version=protocol_version)
        self._name = recovery.tool.name + "_plan"
        self._descriptor = dict(
            name=self._name,
            description="Prepare an immutable compensation plan from a failed pipeline. Does not apply business changes. Start a recovery attempt with the returned input and a stable request_id; a human must approve the exact plan before effects.",
            inputSchema=dict(
                type="object",
                properties=dict(source_run_id=dict(type="string", minLength=36, maxLength=36)),
                required=["source_run_id"],
                additionalProperties=False,
            ),
            outputSchema=dict(
                type="object",
                properties=dict(
                    state=dict(type="string", enum=["ready"]),
                    input=recovery.contract.spec["input"],
                    source_run_id=dict(type="string"),
                    commands=recovery.review_contract.spec["input"]["properties"]["commands"],
                ),
                required=["state", "input", "source_run_id", "commands"],
                additionalProperties=False,
            ),
            annotations=dict(readOnlyHint=False, destructiveHint=False, idempotentHint=True, openWorldHint=True),
        )

    @property
    def pipeline_contract(self) -> _PipelineContractMetadata:
        return self._inner.pipeline_contract

    @property
    def tools(self) -> list[dict[str, Any]]:
        return [*self._inner.tools, copy.deepcopy(self._descriptor)]

    async def call(self, name: str, args: Any, access: A, *, timeout: float) -> dict[str, Any]:
        if name != self._name:
            return await self._inner.call(name, args, access, timeout=timeout)
        if not isinstance(args, dict) or set(args) != {"source_run_id"}:
            raise PipelineToolMCPError(-32602)
        try:
            id = request_id(args["source_run_id"])
        except Exception:
            raise PipelineToolMCPError(-32602) from None
        try:
            p = await self._recovery.plan(id, access, timeout=timeout)
            value = dict(
                state="ready",
                input=p.input,
                source_run_id=p.source.run_id,
                commands=[
                    dict(id=c.id, description=c.description, input_sha256=job_value_hash(c.input)) for c in p.commands
                ],
            )
            return {
                **({"resultType": "complete"} if self._protocol == "2026-07-28" else {}),
                "structuredContent": value,
                "content": [dict(type="text", text=json.dumps(value, ensure_ascii=False, separators=(",", ":")))],
                "isError": False,
            }
        except CompensationError as e:
            return {
                **({"resultType": "complete"} if self._protocol == "2026-07-28" else {}),
                "content": [dict(type="text", text=json.dumps(dict(code=e.code), separators=(",", ":")))],
                "isError": True,
            }
