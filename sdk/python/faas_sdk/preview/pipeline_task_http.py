"""Streamable HTTP transport for the pipeline Task client."""

from __future__ import annotations

import json
from collections.abc import AsyncGenerator
from contextlib import aclosing
from typing import Any, Literal

import httpx

from .._sse import aiter_sse
from .pipeline_task_client import PipelineTaskClientError, PipelineTaskClientMethod, _record

_PROTOCOL = "2026-07-28"
_SUBSCRIPTION: Literal["subscriptions/listen"] = "subscriptions/listen"
_MAX_FRAME = 1024 * 1024 + 4096
_ID = 1
_Method = PipelineTaskClientMethod | Literal["subscriptions/listen"]


def _parse(text: str) -> dict[str, Any]:
    try:
        frame = _record(json.loads(text))
    except (ValueError, RecursionError):
        raise PipelineTaskClientError("invalid_response") from None
    if frame.get("jsonrpc") != "2.0":
        raise PipelineTaskClientError("invalid_response")
    return frame


def _result(frame: dict[str, Any], method: _Method) -> Any:
    if type(frame.get("id")) is not int or frame["id"] != _ID or ("result" in frame) == ("error" in frame):
        raise PipelineTaskClientError("invalid_response")
    if "error" not in frame:
        return frame["result"]
    code = _record(frame["error"]).get("code")
    if type(code) is not int:
        raise PipelineTaskClientError("invalid_response")
    if code in (-32601, -32021):
        raise PipelineTaskClientError("unsupported")
    if code == -32002 or code == -32602 and method in ("tasks/get", "resources/read"):
        raise PipelineTaskClientError("not_found")
    if code == -32602:
        raise PipelineTaskClientError("invalid_input")
    raise PipelineTaskClientError("request_failed")


class PipelineTaskHttpTransport:
    def __init__(self, endpoint: str, client: httpx.AsyncClient, *, supports_forms: bool = True):
        try:
            url = httpx.URL(endpoint)
        except (TypeError, httpx.InvalidURL):
            raise PipelineTaskClientError("invalid_input") from None
        if not url.host or url.scheme not in ("http", "https") or url.userinfo or url.fragment:
            raise PipelineTaskClientError("invalid_input")
        self._endpoint, self._client = url, client
        self._capabilities: dict[str, Any] = {"extensions": {"io.modelcontextprotocol/tasks": {}}}
        if supports_forms:
            self._capabilities["elicitation"] = {"form": {}}

    async def _frames(
        self, method: _Method, params: dict[str, Any], *, timeout: float
    ) -> AsyncGenerator[dict[str, Any], None]:
        headers = {
            "Accept": "application/json, text/event-stream",
            "MCP-Protocol-Version": _PROTOCOL,
            "MCP-Method": method,
        }
        name = params.get("name", params.get("taskId", params.get("uri")))
        if isinstance(name, str):
            headers["MCP-Name"] = name
        body = {
            "jsonrpc": "2.0",
            "id": _ID,
            "method": method,
            "params": {
                **params,
                "_meta": {
                    "io.modelcontextprotocol/protocolVersion": _PROTOCOL,
                    "io.modelcontextprotocol/clientInfo": {"name": "gregale-sdk", "version": "1"},
                    "io.modelcontextprotocol/clientCapabilities": self._capabilities,
                },
            },
        }
        try:
            async with self._client.stream(
                "POST",
                self._endpoint,
                headers=headers,
                json=body,
                timeout=timeout,
                follow_redirects=False,
            ) as response:
                if response.status_code in (401, 403):
                    raise PipelineTaskClientError("unauthorized")
                if response.status_code == 404 and method != _SUBSCRIPTION:
                    raise PipelineTaskClientError("not_found")
                media = response.headers.get("Content-Type", "").split(";", 1)[0].strip().lower()
                if not response.is_success and media != "application/json":
                    raise PipelineTaskClientError("request_failed")
                if media == "application/json":
                    data = bytearray()
                    async for chunk in response.aiter_bytes():
                        if len(data) + len(chunk) > _MAX_FRAME:
                            raise PipelineTaskClientError("invalid_response")
                        data.extend(chunk)
                    frame = _parse(data.decode("utf-8"))
                    if not response.is_success and "error" not in frame:
                        raise PipelineTaskClientError("request_failed")
                    yield frame
                elif media == "text/event-stream":
                    async for event in aiter_sse(response, max_frame_bytes=_MAX_FRAME):
                        if event.data:
                            yield _parse(event.data)
                else:
                    raise PipelineTaskClientError("invalid_response")
        except (ValueError, UnicodeError):
            raise PipelineTaskClientError("invalid_response") from None
        except httpx.HTTPError:
            raise PipelineTaskClientError("request_failed") from None

    async def request(self, method: PipelineTaskClientMethod, params: dict[str, Any], *, timeout: float) -> Any:
        async with aclosing(self._frames(method, params, timeout=timeout)) as frames:
            async for frame in frames:
                return _result(frame, method)
        raise PipelineTaskClientError("request_failed")

    async def subscribe(self, task_id: str, *, timeout: float) -> AsyncGenerator[object, None]:
        accepted = False
        async with aclosing(
            self._frames(_SUBSCRIPTION, {"notifications": {"taskIds": [task_id]}}, timeout=timeout)
        ) as frames:
            async for frame in frames:
                if "id" in frame:
                    terminal = _record(_result(frame, _SUBSCRIPTION))
                    if terminal.get("resultType") != "complete":
                        raise PipelineTaskClientError("invalid_response")
                    return
                params = _record(frame.get("params"))
                subscription = _record(params.get("_meta")).get("io.modelcontextprotocol/subscriptionId")
                if type(subscription) is not int or subscription != _ID:
                    raise PipelineTaskClientError("invalid_response")
                if frame.get("method") == "notifications/subscriptions/acknowledged":
                    ids = _record(params.get("notifications")).get("taskIds")
                    if ids is None or ids == []:
                        raise PipelineTaskClientError("not_found")
                    if accepted or ids != [task_id]:
                        raise PipelineTaskClientError("invalid_response")
                    accepted = True
                elif frame.get("method") == "notifications/tasks":
                    if not accepted or params.get("taskId") != task_id:
                        raise PipelineTaskClientError("invalid_response")
                    yield params
