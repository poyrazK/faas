"""Typed intent and observation tools; native workflow gates own execution approval."""

from __future__ import annotations

import copy
from typing import Any, Generic, Literal, TypeVar

from ..job_runtime import JobError
from .outbound_action_state import OutboundActionError, action_json
from .outbound_actions import OutboundAction
from .pipeline_state import _hash, _uuid
from .pipeline_tool_mcp import PipelineToolMCP, PipelineToolMCPError

I = TypeVar("I")
O = TypeVar("O")
A = TypeVar("A")


class OutboundActionMCP(Generic[I, O, A]):
    def __init__(
        self, action: OutboundAction[I, O, A], *, protocol_version: Literal["2025-11-25", "2026-07-28"] = "2025-11-25"
    ):
        self._action, self._protocol = action, protocol_version
        self._inner = PipelineToolMCP(action.tool, protocol_version=protocol_version)
        descriptions = {
            "prepare": "Save an immutable typed action intent. Start its approval workflow using the returned action reference and a separate stable workflow request ID.",
            "observe": "Read a currently authorized durable action outcome.",
            "refresh": "Reconcile through provider GETs. Never submits or retries an external write.",
            "discard": "Cancel a prepared action before dispatch. Already submitted provider work cannot be recalled.",
        }
        self._extra = [
            dict(
                name=action.tool.name + "_" + op,
                description=description,
                inputSchema=dict(
                    type="object",
                    properties=dict(
                        request_id=dict(type="string", minLength=36, maxLength=36),
                        input=action.business_contract.spec["input"],
                    ),
                    required=["request_id", "input"],
                    additionalProperties=False,
                )
                if op == "prepare"
                else dict(
                    type="object",
                    properties=dict(action_id=dict(type="string", minLength=64, maxLength=64)),
                    required=["action_id"],
                    additionalProperties=False,
                ),
                outputSchema=action.contract.spec["output"]["properties"]["action"],
                annotations=dict(
                    readOnlyHint=op == "observe", destructiveHint=False, idempotentHint=True, openWorldHint=True
                ),
            )
            for op, description in descriptions.items()
        ]

    @property
    def tools(self) -> list[dict[str, Any]]:
        return [*self._inner.tools, *copy.deepcopy(self._extra)]

    async def call(self, name: str, args: Any, access: A, *, timeout: float) -> dict[str, Any]:
        descriptor = next((t for t in self._extra if t["name"] == name), None)
        if descriptor is None:
            return await self._inner.call(name, args, access, timeout=timeout)
        op = name.removeprefix(self._action.tool.name + "_")
        if (
            not isinstance(args, dict)
            or len(action_json(args).encode()) > 1048576
            or set(args) != ({"request_id", "input"} if op == "prepare" else {"action_id"})
            or not (_uuid(args.get("request_id")) if op == "prepare" else _hash(args.get("action_id")))
        ):
            raise PipelineToolMCPError(-32602)
        args = copy.deepcopy(args)
        try:
            if op == "prepare":
                result = await self._action.prepare(
                    self._action.business_contract.input(args["input"]), args["request_id"], access, timeout=timeout
                )
            elif op == "observe":
                result = await self._action.inspect(args["action_id"], access, timeout=timeout)
            elif op == "refresh":
                result = await self._action.reconcile(args["action_id"], access, timeout=timeout)
            else:
                result = await self._action.cancel(args["action_id"], access, timeout=timeout)
            return {
                **({"resultType": "complete"} if self._protocol == "2026-07-28" else {}),
                "structuredContent": result,
                "content": [dict(type="text", text=action_json(result))],
                "isError": False,
            }
        except (OutboundActionError, JobError) as e:
            if e.code == "invalid_input":
                raise PipelineToolMCPError(-32602) from None
            return {
                **({"resultType": "complete"} if self._protocol == "2026-07-28" else {}),
                "content": [dict(type="text", text=action_json(dict(code=e.code)))],
                "isError": True,
            }
