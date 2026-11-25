"""Read named files from a currently authorized pipeline result."""

from __future__ import annotations

import asyncio
import base64
import json
import re
from collections.abc import Callable, Mapping, Sequence
from dataclasses import dataclass
from typing import Any, Generic, TypeVar
from uuid import UUID

from ..job_runtime import _scope, job_value_hash
from .files import FileBucket, FileOptions
from .pipeline_state import _uuid, parse_pipeline_artifact, read_pipeline_artifact
from .pipeline_tool_state import PipelineToolError
from .pipeline_tools import PipelineTool, PipelineToolResult
from .query_tool_mcp import QueryMCPError

I = TypeVar("I")
O = TypeVar("O")
A = TypeVar("A")
_DOMAIN = "gregale.pipeline-output-resource.v1"


@dataclass(frozen=True, slots=True)
class PipelineOutputResource(Generic[O]):
    name: str
    description: str
    mime_type: str
    files: FileBucket
    select: Callable[[O], Mapping[str, Any] | None]
    max_bytes: int = 262144


@dataclass(frozen=True, slots=True)
class _Resource(Generic[O]):
    definition: PipelineOutputResource[O]
    prefix: str

    @property
    def template(self) -> dict[str, str]:
        return {
            "uriTemplate": self.prefix + "{id}",
            "name": self.definition.name,
            "description": self.definition.description,
            "mimeType": self.definition.mime_type,
        }

    def locate(self, snapshot: PipelineToolResult[O]) -> tuple[str, dict[str, Any]] | None:
        value = self.definition.select(snapshot.output)
        if value is None:
            return None
        receipt = snapshot.receipt
        intent, checkpoint = receipt.intent, receipt.checkpoint
        if checkpoint is None:
            raise PipelineToolError("receipt_conflict")
        artifact = parse_pipeline_artifact(self.definition.files, value, intent["owner_id"])
        binding = job_value_hash([_DOMAIN, self.prefix, intent["identity"], checkpoint.run_id, artifact])
        identity = base64.urlsafe_b64encode(UUID(intent["request_id"]).bytes + bytes.fromhex(binding)).decode("ascii")
        return self.prefix + identity, artifact


class _PipelineOutputResources(Generic[I, O, A]):
    def __init__(self, tool: PipelineTool[I, O, A], definitions: Sequence[PipelineOutputResource[O]]) -> None:
        if not 1 <= len(definitions) <= 32 or not callable(getattr(tool, "result_with_receipt", None)):
            raise PipelineToolError("invalid_input")
        self._tool = tool
        self._resources: list[_Resource[O]] = []
        names: set[str] = set()
        for d in definitions:
            if (
                not isinstance(d, PipelineOutputResource)
                or not isinstance(d.name, str)
                or not re.fullmatch(r"[a-z][a-z0-9_]{0,47}", d.name)
                or d.name in names
                or not isinstance(d.description, str)
                or not d.description.strip()
                or len(d.description.encode("utf-16-le")) > 1024
                or not isinstance(d.mime_type, str)
                or len(d.mime_type) > 128
                or not re.fullmatch(r"[a-z0-9!#$&^_.+-]+/[a-z0-9!#$&^_.+-]+", d.mime_type)
                or type(d.max_bytes) is not int
                or not 1 <= d.max_bytes <= 1048576
                or not callable(getattr(d.files, "download", None))
                or not callable(d.select)
            ):
                raise PipelineToolError("invalid_input")
            names.add(d.name)
            scope = {"base_url": d.files.base_url, "app_slug": d.files.app_slug, "bucket": d.files.bucket}
            namespace = job_value_hash([_DOMAIN, tool.name, tool.contract.sha256, scope])
            self._resources.append(_Resource(d, f"gregale-pipeline://{namespace}/{d.name}/"))

    @property
    def templates(self) -> list[dict[str, str]]:
        return [r.template for r in self._resources]

    def links(self, snapshot: PipelineToolResult[O]) -> list[dict[str, str]]:
        links = []
        for r in self._resources:
            located = r.locate(snapshot)
            if located:
                links.append(
                    {
                        "type": "resource_link",
                        "uri": located[0],
                        "name": r.definition.name,
                        "description": r.definition.description,
                        "mimeType": r.definition.mime_type,
                    }
                )
        return links

    async def read(self, uri: str, access: A, *, timeout: float) -> dict[str, Any]:
        resource = next((r for r in self._resources if isinstance(uri, str) and uri.startswith(r.prefix)), None)
        if resource is None:
            raise QueryMCPError(-32002)
        id = uri[len(resource.prefix) :]
        if not re.fullmatch(r"[A-Za-z0-9_-]{64}", id):
            raise QueryMCPError(-32002)
        request_id = str(UUID(bytes=base64.urlsafe_b64decode(id)[:16]))
        if not _uuid(request_id):
            raise QueryMCPError(-32002)

        async def work(check: Callable[[], None]) -> dict[str, Any]:
            assert resource is not None
            end = asyncio.get_running_loop().time() + timeout

            def left() -> float:
                check()
                return max(0.000001, end - asyncio.get_running_loop().time())

            async def authorized_artifact() -> dict[str, Any]:
                snapshot = await self._tool.result_with_receipt(request_id, access, timeout=left())
                found = resource.locate(snapshot)
                if found is None or found[0] != uri:
                    raise QueryMCPError(-32002)
                return found[1]

            artifact = await authorized_artifact()
            data = await read_pipeline_artifact(
                resource.definition.files,
                artifact,
                artifact["owner_id"],
                options=FileOptions(timeout=left(), max_bytes=resource.definition.max_bytes),
            )
            text = data.decode("utf-8")
            await authorized_artifact()
            frame = {"contents": [{"uri": uri, "mimeType": resource.definition.mime_type, "text": text}]}
            if len(json.dumps(frame, ensure_ascii=False, separators=(",", ":")).encode()) > 1048576:
                raise QueryMCPError(-32603)
            return frame

        try:
            return await _scope(timeout, work)
        except QueryMCPError:
            raise
        except PipelineToolError as error:
            hidden = error.code in ("unauthorized", "receipt_conflict", "start_unknown", "not_terminal")
            raise QueryMCPError(-32002 if hidden else -32603) from None
        except Exception:
            raise QueryMCPError(-32603) from None
