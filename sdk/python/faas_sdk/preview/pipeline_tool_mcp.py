"""Transport-neutral MCP descriptors and calls; host authentication/lifecycle stay explicit."""

from __future__ import annotations

import asyncio
import copy
import json
import re
from collections.abc import Sequence
from typing import Any, Generic, Literal, TypeVar

from ..job_runtime import JobError, _scope
from ._pipeline_tool_protocol import WIRE
from .pipeline_output_resources import PipelineOutputResource, _PipelineOutputResources
from .pipeline_start_preview import _TOKEN_PATTERN, PipelineStartPreviewPolicy
from .pipeline_state import _hash
from .pipeline_task_manifest import (
    PipelineTaskManifestError,
    PipelineTaskRevision,
    _PipelineContractMetadata,
    _revision,
)
from .pipeline_tool_state import PipelineToolError
from .pipeline_tools import PipelineTool, PipelineToolStart, PipelineToolStatus
from .query_tool_mcp import QueryMCPError

I = TypeVar("I")
O = TypeVar("O")
A = TypeVar("A")


class PipelineToolMCPError(RuntimeError):
    def __init__(self, code: Literal[-32601, -32602]):
        self.code = code
        super().__init__("Unknown pipeline tool" if code == -32601 else "Invalid pipeline tool arguments")


class PipelineToolMCP(Generic[I, O, A]):
    def __init__(
        self,
        tool: PipelineTool[I, O, A],
        *,
        protocol_version: Literal["2025-11-25", "2026-07-28"] = "2025-11-25",
        output_resources: Sequence[PipelineOutputResource[O]] | None = None,
        revision: PipelineTaskRevision | None = None,
        preview: PipelineStartPreviewPolicy[I, Any, A] | None = None,
    ):
        if protocol_version not in ("2025-11-25", "2026-07-28"):
            raise PipelineToolError("invalid_input")
        try:
            self._revision = _revision(revision) if revision is not None else None
        except PipelineTaskManifestError:
            raise PipelineToolError("invalid_input") from None
        self._retained = bool(self._revision and self._revision["starts"] == "retained")
        if self._retained and not callable(getattr(tool, "replay_start", None)):
            raise PipelineToolError("invalid_input")
        self._preview = None if self._retained else preview
        if self._preview is not None and (
            not callable(getattr(tool, "preview_start", None)) or not callable(getattr(tool, "start_previewed", None))
        ):
            raise PipelineToolError("invalid_input")
        self._operations = [*WIRE["operations"], "preview"] if self._preview else WIRE["operations"]
        token_schema = {"type": "string", "pattern": _TOKEN_PATTERN}
        self._tool, self._protocol = tool, protocol_version
        self._resources = _PipelineOutputResources(tool, output_resources) if output_resources is not None else None
        descriptions = {
            "start": (
                "Replay an existing start with its original request_id and input. Unknown starts require reconciliation."
                if self._retained
                else "Confirm a pipeline preview using the same request_id and input." if self._preview
                else "Start once using a stable request_id; retain it for recovery. Unknown starts require reconciliation; never invent a new ID to retry."
            ),
            "preview": "Preview the current source and expected actions for this request.",
            "status": "Read current status and an authenticated human review link when approval is pending.",
            "result": "Read validated output when complete; otherwise return current status.",
            "cancel": "Explicitly cancel orchestration; already accepted Jobs may continue.",
            "reconcile": "Adopt an exact verified run after uncertain acceptance; never starts or restarts work.",
        }
        tools = []
        for op in self._operations:
            properties = {"request_id": copy.deepcopy(WIRE["request_id_schema"])}
            if op in ("start", "preview"):
                properties["input"] = tool.contract.spec["input"]
            if op == "reconcile":
                properties["run_id"] = copy.deepcopy(WIRE["request_id_schema"])
            required = list(properties)
            if op == "preview" or op == "start" and (self._revision or self._preview):
                properties["expected_contract_sha256"] = {"type": "string", "pattern": "^[a-f0-9]{64}$"}
            if op == "start" and (self._preview or self._retained):
                properties["preview_token"] = token_schema.copy()
            output = copy.deepcopy(WIRE["result_schema"])
            if op == "preview" and self._preview:
                output = dict(type="object", properties={
                    "summary": self._preview.contract.spec["output"],
                    "preview_token": token_schema.copy(),
                    "expires_at": {"type": "integer", "minimum": 1},
                }, required=["summary", "preview_token", "expires_at"], additionalProperties=False)
            if op == "result":
                output["properties"]["output"] = tool.contract.spec["output"]
            tools.append(
                dict(
                    name=tool.name + "_" + op,
                    description=tool.description + " " + descriptions[op],
                    inputSchema=dict(
                        type="object", properties=properties, required=required, additionalProperties=False
                    ),
                    outputSchema=output,
                    annotations=dict(
                        readOnlyHint=op in ("status", "result", "preview"),
                        destructiveHint=(op == "start" and not self._retained) or op == "cancel",
                        idempotentHint=op != "cancel",
                        openWorldHint=True,
                    ),
                )
            )
        self._tools = tools

    @property
    def pipeline_contract(self) -> _PipelineContractMetadata:
        metadata = _PipelineContractMetadata(tool=self._tool.name, spec=self._tool.contract.spec)
        if self._revision:
            metadata["revision"] = copy.deepcopy(self._revision)
        if self._preview:
            metadata["preview"] = self._preview.contract.spec["output"]
        return metadata

    @property
    def tools(self) -> list[dict[str, Any]]:
        return copy.deepcopy(self._tools)

    @property
    def resource_templates(self) -> list[dict[str, str]]:
        return self._resources.templates if self._resources else []

    async def read_resource(self, uri: str, access: A, *, timeout: float) -> dict[str, Any]:
        if self._resources is None:
            raise QueryMCPError(-32002)
        return await self._resources.read(uri, access, timeout=timeout)

    def _pack(self, value: dict[str, Any], links: Sequence[dict[str, str]] = ()) -> dict[str, Any]:
        result: dict[str, Any] = dict(
            content=[dict(type="text", text=json.dumps(value, ensure_ascii=False, separators=(",", ":"))), *links],
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
            if raw.source_failure:
                result["source_failure"] = raw.source_failure
        else:
            result["replayed"] = raw.replayed
        return result

    async def call(self, name: str, args: Any, access: A, *, timeout: float) -> dict[str, Any]:
        names = [t["name"] for t in self._tools]
        if name not in names:
            raise PipelineToolMCPError(-32601)
        op = self._operations[names.index(name)]
        expected = (
            {"request_id", "input"}
            if op in ("start", "preview")
            else {"request_id", "run_id"}
            if op == "reconcile"
            else {"request_id"}
        )
        if (op == "preview" or op == "start" and (self._revision or self._preview)) and isinstance(args, dict) and "expected_contract_sha256" in args:
            if not _hash(args["expected_contract_sha256"]):
                raise PipelineToolMCPError(-32602)
            expected.add("expected_contract_sha256")
        if op == "start" and (self._preview or self._retained) and isinstance(args, dict) and "preview_token" in args:
            token = args["preview_token"]
            if not isinstance(token, str) or not re.fullmatch(_TOKEN_PATTERN, token):
                raise PipelineToolMCPError(-32602)
            expected.add("preview_token")
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

                if op in ("start", "preview"):
                    digest = args.get("expected_contract_sha256")
                    if digest is not None and digest != self._tool.contract.sha256:
                        raise PipelineToolError("revision_unavailable")
                    value, id = self._tool.contract.input(args["input"]), args["request_id"]
                    if self._preview:
                        if op == "preview":
                            result = await self._tool.preview_start(value, id, access, self._preview, timeout=left())
                            return self._pack(dict(result))
                        started = await self._tool.start_previewed(
                            value, id, access, self._preview, token=args.get("preview_token"), timeout=left(),
                        )
                    else:
                        start = self._tool.replay_start if self._retained else self._tool.start
                        started = await start(value, id, access, timeout=left())
                    return self._pack(self._public(started))
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
                    if self._resources:
                        snapshot = await self._tool.result_with_receipt(args["request_id"], access, timeout=left())
                        if (
                            snapshot.receipt.intent["identity"] != status.receipt.intent["identity"]
                            or snapshot.receipt.checkpoint != status.receipt.checkpoint
                        ):
                            raise PipelineToolError("receipt_conflict")
                        return self._pack(
                            self._public(status) | dict(output=snapshot.output), self._resources.links(snapshot)
                        )
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
