"""Transport-neutral query tools and resource templates for an authenticated MCP host."""

from __future__ import annotations

import asyncio
import copy
from collections.abc import Callable, Sequence
from typing import Any, Generic, Literal, TypedDict, TypeVar

from .query_tools import QueryResource, QueryTool, QueryToolError, _json, _query_scope

I = TypeVar("I")
O = TypeVar("O")
A = TypeVar("A")


class QueryMCPError(Exception):
    def __init__(self, code: Literal[-32601, -32602, -32603, -32002]):
        super().__init__("SDK query MCP error: " + str(code))
        self.code = code


class QueryResourceReference(TypedDict):
    resource: str
    id: str


def _frame(value: dict[str, Any]) -> dict[str, Any]:
    if len(_json(value).encode()) > 1048576:
        raise QueryToolError("limit")
    return copy.deepcopy(value)


class QueryToolMCP(Generic[I, O, A]):
    def __init__(
        self,
        tool: QueryTool[I, O, A],
        *,
        protocol_version: Literal["2025-11-25", "2026-07-28"] = "2025-11-25",
        resources: Sequence[QueryResource[A]] = (),
        links: Callable[[O], Sequence[QueryResourceReference]] | None = None,
    ):
        if (
            protocol_version not in ("2025-11-25", "2026-07-28")
            or len(resources) > 100
            or len({r.name for r in resources}) != len(resources)
        ):
            raise QueryToolError("invalid_input")
        self._tool, self._resources, self._links, self._protocol = tool, tuple(resources), links, protocol_version
        self._tools = [
            dict(
                name=tool.name,
                description=tool.description,
                inputSchema=dict(
                    type="object",
                    properties=dict(
                        input=tool.contract.spec["input"],
                        after=dict(type="string", minLength=1, maxLength=8192),
                        page_size=dict(type="integer", minimum=1, maximum=tool.max_page_size),
                    ),
                    required=["input"],
                    additionalProperties=False,
                ),
                outputSchema=dict(
                    type="object",
                    properties=dict(
                        items=dict(type="array", items=tool.contract.spec["output"], maxItems=tool.max_page_size),
                        next_cursor=dict(type=["string", "null"], maxLength=8192),
                    ),
                    required=["items", "next_cursor"],
                    additionalProperties=False,
                ),
                annotations=dict(readOnlyHint=True, destructiveHint=False, idempotentHint=True, openWorldHint=True),
            )
        ]

    @property
    def tools(self) -> list[dict[str, Any]]:
        return copy.deepcopy(self._tools)

    @property
    def resource_templates(self) -> list[dict[str, str]]:
        return [r.template for r in self._resources]

    async def call(self, name: str, args: Any, access: A, *, timeout: float) -> dict[str, Any]:
        if name != self._tool.name:
            raise QueryMCPError(-32601)
        try:
            if (
                not isinstance(args, dict)
                or "input" not in args
                or set(args) - {"input", "after", "page_size"}
                or len(_json(args).encode()) > 16384
                or "after" in args
                and not isinstance(args["after"], str)
                or "page_size" in args
                and (type(args["page_size"]) is not int or not 0 < args["page_size"] <= self._tool.max_page_size)
            ):
                raise ValueError
            args = copy.deepcopy(args)
            self._tool.contract.input(args["input"])
        except Exception:
            raise QueryMCPError(-32602) from None

        async def work(check: Callable[[], None]) -> dict[str, Any]:
            end = asyncio.get_running_loop().time() + timeout

            def left() -> float:
                check()
                return max(0.000001, end - asyncio.get_running_loop().time())

            result = await self._tool.page(
                args["input"], access, timeout=left(), after=args.get("after"), page_size=args.get("page_size")
            )
            found = {}
            if self._links:
                for item in result["items"]:
                    refs = self._links(item)
                    if not isinstance(refs, (list, tuple)) or len(refs) > 100:
                        raise QueryToolError("invalid_output")
                    for ref in refs:
                        resource = next((r for r in self._resources if r.name == ref.get("resource")), None)
                        if resource is None:
                            raise QueryToolError("invalid_output")
                        link = await resource.link(ref["id"], access, timeout=left())
                        found[link["uri"]] = link
                        if len(found) > 100:
                            raise QueryToolError("limit")
            return _frame(
                dict(
                    **({"resultType": "complete"} if self._protocol == "2026-07-28" else {}),
                    structuredContent=result,
                    content=[dict(type="text", text=_json(result)), *found.values()],
                    isError=False,
                )
            )

        try:
            return await _query_scope(timeout, work)
        except QueryToolError as e:
            if e.code in ("invalid_input", "invalid_cursor"):
                raise QueryMCPError(-32602) from None
            return _frame(
                dict(
                    **({"resultType": "complete"} if self._protocol == "2026-07-28" else {}),
                    content=[dict(type="text", text=_json(dict(code=e.code)))],
                    isError=True,
                )
            )

    async def read_resource(self, uri: str, access: A, *, timeout: float) -> dict[str, Any]:
        resource = next(
            (r for r in self._resources if isinstance(uri, str) and uri.startswith(r.template["uriTemplate"][:-4])),
            None,
        )
        if resource is None:
            raise QueryMCPError(-32002)
        try:
            return _frame(dict(contents=[await resource.read(uri, access, timeout=timeout)]))
        except QueryToolError as e:
            raise QueryMCPError(-32002 if e.code in ("not_found", "unauthorized") else -32603) from None
