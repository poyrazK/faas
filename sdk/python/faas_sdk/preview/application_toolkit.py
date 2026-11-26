"""Compose application-owned MCP adapters under explicit publication and scope rules."""

from __future__ import annotations

import copy
import re
from collections.abc import Awaitable, Callable, Mapping, Sequence
from dataclasses import dataclass, field
from typing import Any, Generic, Literal, Protocol, TypeVar

from ..job_runtime import JobContract, job_value_hash
from .pipeline_task_manifest import (
    PipelineTaskArtifactDescriptor,
    PipelineTaskRevision,
    _PipelineContractEntry,
    _revision,
)
from .query_tool_mcp import QueryMCPError
from .query_tools import QueryToolError, _json
from .work_inbox import WorkInboxReference
from .work_inbox_follow_up import _DESCRIPTOR, WorkInboxFollowUp, WorkInboxFollowUpOptions, _arguments, _resolve

A = TypeVar("A")
A_contra = TypeVar("A_contra", contravariant=True)
ScopeRules = Mapping[str, Sequence[str]]
_TEMPLATE = re.compile(r"gregale-(?:query|pipeline)://[a-f0-9]{64}/[a-z][a-z0-9_]{0,47}/\{id\}")


@dataclass(frozen=True)
class ToolkitAccess(Generic[A]):
    principal: A
    scopes: Sequence[str]


class ToolkitAdapter(Protocol[A_contra]):
    @property
    def tools(self) -> Sequence[dict[str, Any]]: ...

    async def call(self, name: str, args: Any, access: A_contra, *, timeout: float) -> dict[str, Any]: ...


@dataclass(frozen=True)
class ToolkitMount(Generic[A]):
    adapter: ToolkitAdapter[A]
    tool_scopes: ScopeRules = field(default_factory=dict)
    resource_scopes: ScopeRules = field(default_factory=dict)


class ApplicationToolkitError(Exception):
    def __init__(self, code: Literal["tool_not_found", "resource_not_found", "limit"]):
        super().__init__("Application toolkit: " + code)
        self.code = code


def _scopes(value: Sequence[str]) -> tuple[str, ...]:
    if (
        not isinstance(value, (list, tuple))
        or not 1 <= len(value) <= 32
        or any(
            not isinstance(s, str) or not 1 <= len(s) <= 128 or not re.fullmatch(r"[\x21\x23-\x5b\x5d-\x7e]+", s)
            for s in value
        )
        or len(set(value)) != len(value)
    ):
        raise ValueError("Each toolkit operation requires distinct OAuth scopes")
    return tuple(sorted(value))


def _allowed(access: ToolkitAccess[A], required: tuple[str, ...]) -> bool:
    return (
        access.principal is not None
        and isinstance(access.scopes, (list, tuple))
        and all(s in access.scopes for s in required)
    )


def _bound(result: dict[str, Any]) -> dict[str, Any]:
    if len(_json(result).encode()) > 1048576:
        raise ApplicationToolkitError("limit")
    return copy.deepcopy(result)


@dataclass(frozen=True)
class _Tool(Generic[A]):
    descriptor: dict[str, Any]
    scopes: tuple[str, ...]
    call: Callable[..., Awaitable[dict[str, Any]]]


@dataclass(frozen=True)
class _Resource:
    template: dict[str, Any]
    scopes: tuple[str, ...]
    read: Callable[..., Awaitable[dict[str, Any]]]


class ApplicationToolkit(Generic[A]):
    def __init__(
        self, mounts: Sequence[ToolkitMount[A]], *, work_inbox_follow_up: WorkInboxFollowUpOptions[A] | None = None
    ):
        if not 1 <= len(mounts) <= 100:
            raise ValueError("Expected 1–100 toolkit mounts")
        tools: dict[str, _Tool[A]] = {}
        resources: dict[str, _Resource] = {}
        resource_names: set[str] = set()
        pipelines: dict[str, tuple[JobContract[Any, Any], list[str], PipelineTaskRevision | None, dict[str, Any] | None]] = {}
        defaults: set[str] = set()
        for mount in mounts:
            descriptors = mount.adapter.tools
            for name, rule in mount.tool_scopes.items():
                matches = [d for d in descriptors if d["name"] == name]
                if len(matches) != 1 or not re.fullmatch(r"[A-Za-z0-9_.-]{1,128}", name) or name in tools:
                    raise ValueError("Unknown or duplicate toolkit tool: " + name)
                tools[name] = _Tool(copy.deepcopy(matches[0]), _scopes(rule), mount.adapter.call)
            templates = getattr(mount.adapter, "resource_templates", ())
            read = getattr(mount.adapter, "read_resource", None)
            for pattern, rule in mount.resource_scopes.items():
                resource_matches = [d for d in templates if d["uriTemplate"] == pattern]
                if (
                    len(resource_matches) != 1
                    or not _TEMPLATE.fullmatch(pattern)
                    or pattern in resources
                    or not callable(read)
                ):
                    raise ValueError("Unknown, duplicate or unsupported toolkit resource: " + pattern)
                template = resource_matches[0]
                if template["name"] in resource_names:
                    raise ValueError("Duplicate toolkit resource name: " + template["name"])
                resource_names.add(template["name"])
                resources[pattern] = _Resource(copy.deepcopy(template), _scopes(rule), read)
            metadata = getattr(mount.adapter, "pipeline_contract", None)
            if metadata:
                tool = metadata["tool"]
                if all(f"{tool}_{op}" in mount.tool_scopes for op in ("start", "status", "result")):
                    contract = JobContract[Any, Any](metadata["spec"])
                    start = tools[tool + "_start"].descriptor["inputSchema"].get("properties", {})
                    result = tools[tool + "_result"].descriptor["outputSchema"].get("properties", {})
                    if (job_value_hash(start.get("input")) != job_value_hash(contract.spec["input"])
                            or job_value_hash(result.get("output")) != job_value_hash(contract.spec["output"])):
                        raise ValueError("Pipeline contract does not match the mounted tool schemas")
                    revision = _revision(metadata["revision"]) if "revision" in metadata else None
                    if revision and revision["starts"] == "default":
                        family = revision["family"]
                        if family in defaults:
                            raise ValueError("Duplicate default pipeline revision: " + family)
                        defaults.add(family)
                    uris = [uri for uri in mount.resource_scopes if uri.startswith("gregale-pipeline://")]
                    preview = metadata.get("preview") if tool + "_preview" in mount.tool_scopes else None
                    if preview is not None:
                        published = tools[tool + "_preview"].descriptor["outputSchema"].get("properties", {})
                        if job_value_hash(published.get("summary")) != job_value_hash(preview):
                            raise ValueError("Pipeline preview contract does not match the mounted tool schema")
                    pipelines[tool] = (contract, uris, revision, copy.deepcopy(preview))
        self._pipelines = pipelines
        self._follow_up_inbox = work_inbox_follow_up.inbox if work_inbox_follow_up else None
        if work_inbox_follow_up:
            name = _DESCRIPTOR["name"]
            if name in tools or not callable(getattr(self._follow_up_inbox, "find_entry", None)):
                raise ValueError("Invalid or duplicate work inbox follow-up")
            tools[name] = _Tool(copy.deepcopy(_DESCRIPTOR), _scopes(work_inbox_follow_up.scopes), self._call_follow_up)
        if not 1 <= len(tools) + len(resources) <= 256:
            raise ValueError("Expected 1–256 published toolkit operations")
        self._tools = dict(sorted(tools.items()))
        self._resources = dict(sorted(resources.items()))

    @property
    def tools(self) -> list[dict[str, Any]]:
        return [copy.deepcopy(entry.descriptor) for entry in self._tools.values()]

    @property
    def resource_templates(self) -> list[dict[str, Any]]:
        return [copy.deepcopy(entry.template) for entry in self._resources.values()]

    @property
    def tool_scopes(self) -> dict[str, list[str]]:
        return {name: list(entry.scopes) for name, entry in self._tools.items()}

    @property
    def resource_scopes(self) -> dict[str, list[str]]:
        return {uri: list(entry.scopes) for uri, entry in self._resources.items()}

    def list_pipeline_contracts(self, access: ToolkitAccess[A]) -> list[_PipelineContractEntry]:
        entries = []
        for tool, (contract, uris, revision, preview) in self._pipelines.items():
            if not all(_allowed(access, self._tools[f"{tool}_{op}"].scopes) for op in ("start", "status", "result")):
                continue
            artifacts: list[PipelineTaskArtifactDescriptor] = []
            for uri in uris:
                resource = self._resources[uri]
                if _allowed(access, resource.scopes):
                    template = resource.template
                    artifacts.append(PipelineTaskArtifactDescriptor(
                        name=template["name"], description=template["description"], mimeType=template["mimeType"],
                    ))
            entry = _PipelineContractEntry(
                tool=tool, contract=contract.spec, contract_sha256=contract.sha256, artifacts=artifacts,
            )
            if revision:
                entry["revision"] = copy.deepcopy(revision)
            if preview is not None and _allowed(access, self._tools[tool + "_preview"].scopes):
                entry["preview"] = copy.deepcopy(preview)
            entries.append(entry)
        return entries

    def list_tools(self, access: ToolkitAccess[A]) -> list[dict[str, Any]]:
        return [copy.deepcopy(entry.descriptor) for entry in self._tools.values() if _allowed(access, entry.scopes)]

    def list_resource_templates(self, access: ToolkitAccess[A]) -> list[dict[str, Any]]:
        return [copy.deepcopy(entry.template) for entry in self._resources.values() if _allowed(access, entry.scopes)]

    def _resource(self, uri: Any) -> _Resource | None:
        if not isinstance(uri, str):
            return None
        prefix, separator, resource_id = uri.rpartition("/")
        if not separator or not re.fullmatch(r"[A-Za-z0-9_-]{1,128}", resource_id):
            return None
        return self._resources.get(prefix + "/{id}")

    async def resolve_work_inbox_follow_up(
        self, reference: WorkInboxReference, access: ToolkitAccess[A], *, timeout: float
    ) -> WorkInboxFollowUp:
        entry = self._tools.get(_DESCRIPTOR["name"])
        inbox = self._follow_up_inbox
        if inbox is None or entry is None or not _allowed(access, entry.scopes):
            raise ApplicationToolkitError("tool_not_found")
        granted = frozenset(access.scopes)

        def visible(name: str) -> dict[str, Any] | None:
            destination = self._tools.get(name)
            return destination.descriptor if destination and granted.issuperset(destination.scopes) else None

        return await _resolve(inbox, reference, access.principal, timeout, visible)

    async def _call_follow_up(
        self, _name: str, args: Any, access: ToolkitAccess[A], *, timeout: float
    ) -> dict[str, Any]:
        try:
            result = await self.resolve_work_inbox_follow_up(_arguments(args), access, timeout=timeout)
        except QueryToolError as error:
            if error.code == "invalid_input":
                raise QueryMCPError(-32602) from None
            return {"isError": True, "content": [{"type": "text", "text": _json({"code": error.code})}]}
        return {
            "isError": False,
            "content": [{"type": "text", "text": _json(result)}],
            "structuredContent": result,
        }

    async def call(self, name: str, args: Any, access: ToolkitAccess[A], *, timeout: float) -> dict[str, Any]:
        entry = self._tools.get(name)
        if entry is None or not _allowed(access, entry.scopes):
            raise ApplicationToolkitError("tool_not_found")
        granted = frozenset(access.scopes)
        caller = access if self._follow_up_inbox is not None and name == _DESCRIPTOR["name"] else access.principal
        result = await entry.call(name, args, caller, timeout=timeout)
        content = []
        for block in result["content"]:
            if block["type"] == "resource_link":
                resource = self._resource(block.get("uri"))
                if resource is None or not granted.issuperset(resource.scopes):
                    continue
            content.append(block)
        return _bound({**result, "content": content})

    async def read_resource(self, uri: str, access: ToolkitAccess[A], *, timeout: float) -> dict[str, Any]:
        entry = self._resource(uri)
        if entry is None or not _allowed(access, entry.scopes):
            raise ApplicationToolkitError("resource_not_found")
        return _bound(await entry.read(uri, access.principal, timeout=timeout))
