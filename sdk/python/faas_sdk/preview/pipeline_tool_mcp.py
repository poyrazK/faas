"""Transport-neutral MCP descriptors and calls; host authentication/lifecycle stay explicit."""

from __future__ import annotations

import asyncio
import copy
import json
from typing import Any, Generic, Literal, TypeVar

from ..job_runtime import JobError, _scope
from ._pipeline_tool_protocol import WIRE
from .pipeline_tool_state import PipelineToolError
from .pipeline_tools import PipelineTool, PipelineToolStart, PipelineToolStatus

I = TypeVar("I")
O = TypeVar("O")
A = TypeVar("A")


class PipelineToolMCPError(RuntimeError):
    def __init__(self, code: Literal[-32601, -32602]):
        self.code = code
        super().__init__("Unknown pipeline tool" if code == -32601 else "Invalid pipeline tool arguments")


class PipelineToolMCP(Generic[I, O, A]):
    def __init__(
        self, tool: PipelineTool[I, O, A], *, protocol_version: Literal["2025-11-25", "2026-07-28"] = "2025-11-25"
    ):
        if protocol_version not in ("2025-11-25", "2026-07-28"):
            raise PipelineToolError("invalid_input")
        self._tool, self._protocol = tool, protocol_version
        descriptions = {
            "start": "Start once using a stable request_id; retain it for recovery. Unknown starts require reconciliation; never invent a new ID to retry.",
            "status": "Read current status and an authenticated human review link when approval is pending.",
            "result": "Read validated output when complete; otherwise return current status.",
            "cancel": "Explicitly cancel orchestration; already accepted Jobs may continue.",
            "reconcile": "Adopt an exact verified run after uncertain acceptance; never starts or restarts work.",
        }
        tools = []
        for op in WIRE["operations"]:
            properties = {"request_id": copy.deepcopy(WIRE["request_id_schema"])}
            if op == "start":
                properties["input"] = tool.contract.spec["input"]
            if op == "reconcile":
                properties["run_id"] = copy.deepcopy(WIRE["request_id_schema"])
            output = copy.deepcopy(WIRE["result_schema"])
            if op == "result":
                output["properties"]["output"] = tool.contract.spec["output"]
            tools.append(
                dict(
                    name=tool.name + "_" + op,
                    description=tool.description + " " + descriptions[op],
                    inputSchema=dict(
                        type="object", properties=properties, required=list(properties), additionalProperties=False
                    ),
                    outputSchema=output,
                    annotations=dict(
                        readOnlyHint=op in ("status", "result"),
                        destructiveHint=op in ("start", "cancel"),
                        idempotentHint=op != "cancel",
                        openWorldHint=True,
                    ),
                )
            )
        self._tools = tools

    @property
    def tools(self) -> list[dict[str, Any]]:
        return copy.deepcopy(self._tools)

    def _pack(self, value: dict[str, Any]) -> dict[str, Any]:
        result: dict[str, Any] = dict(
            content=[dict(type="text", text=json.dumps(value, ensure_ascii=False, separators=(",", ":")))],
            structuredContent=value,
            isError=False,
        )
        if self._protocol == "2026-07-28":
            result["resultType"] = "complete"
        if len(json.dumps(result, ensure_ascii=False, separators=(",", ":")).encode()) > 1048576:
            raise PipelineToolError("limit")
        return result

    @staticmethod
    def _public(raw: PipelineToolStart | PipelineToolStatus | None) -> dict[str, Any]:
        if raw is None:
            return dict(state="not_found", receipt=None, steps=[], approval=None)
        i = raw.receipt.intent
        result: dict[str, Any] = dict(
            state=raw.state,
            receipt=dict(
                identity=i["identity"],
                request_id=i["request_id"],
                tool=i["name"],
                run_id=raw.receipt.checkpoint.run_id if raw.receipt.checkpoint else None,
            ),
            steps=[],
            approval=None,
        )
        if isinstance(raw, PipelineToolStatus):
            data = raw.to_dict()
            result.update(steps=data["steps"], approval=data["approval"])
        else:
            result["replayed"] = raw.replayed
        return result

    async def call(self, name: str, args: Any, access: A, *, timeout: float) -> dict[str, Any]:
        names = [t["name"] for t in self._tools]
        if name not in names:
            raise PipelineToolMCPError(-32601)
        op = WIRE["operations"][names.index(name)]
        expected = (
            {"request_id", "input"}
            if op == "start"
            else {"request_id", "run_id"}
            if op == "reconcile"
            else {"request_id"}
        )
        if (
            not isinstance(args, dict)
            or set(args) != expected
            or not isinstance(args["request_id"], str)
            or op == "reconcile"
            and not isinstance(args["run_id"], str)
        ):
            raise PipelineToolMCPError(-32602)
        try:
            args = copy.deepcopy(args)
            if len(json.dumps(args, ensure_ascii=False, separators=(",", ":")).encode()) > 1048576:
                raise PipelineToolMCPError(-32602)

            async def work(check: Any) -> dict[str, Any]:
                end = asyncio.get_running_loop().time() + timeout

                def left() -> float:
                    check()
                    return max(0.000001, end - asyncio.get_running_loop().time())

                if op == "start":
                    return self._pack(
                        self._public(
                            await self._tool.start(
                                self._tool.contract.input(args["input"]), args["request_id"], access, timeout=left()
                            )
                        )
                    )
                if op == "reconcile":
                    return self._pack(
                        self._public(
                            await self._tool.reconcile(args["request_id"], args["run_id"], access, timeout=left())
                        )
                    )
                if op == "cancel":
                    return self._pack(self._public(await self._tool.cancel(args["request_id"], access, timeout=left())))
                status = await self._tool.inspect(args["request_id"], access, timeout=left())
                if op == "result" and status is not None and status.state == "succeeded":
                    output = await self._tool.result(args["request_id"], access, timeout=left())
                    return self._pack(self._public(status) | dict(output=output))
                return self._pack(self._public(status))

            return await _scope(timeout, work)
        except PipelineToolMCPError:
            raise
        except (PipelineToolError, JobError) as error:
            if error.code == "invalid_input":
                raise PipelineToolMCPError(-32602) from None
            return self._error(error.code)
        except Exception:
            return self._error("request_failed")

    def _error(self, code: str) -> dict[str, Any]:
        return {
            **({"resultType": "complete"} if self._protocol == "2026-07-28" else {}),
            "content": [dict(type="text", text=json.dumps(dict(code=code), separators=(",", ":")))],
            "isError": True,
        }
