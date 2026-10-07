"""ADR-521: request-scoped reporting for ordinary HTTP Operations handlers."""

from __future__ import annotations

import json
import re
from collections.abc import Iterator, Mapping
from contextlib import contextmanager
from contextvars import ContextVar
from dataclasses import dataclass, field
from urllib.parse import parse_qsl, urlencode, urlsplit, urlunsplit

import httpx

from .models.operation_artifact_request import OperationArtifactRequest
from .models.operation_execution_control_response import OperationExecutionControlResponse
from .models.operation_report_request import OperationReportRequest
from .models.operation_response import OperationResponse

_UUID = re.compile(r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\Z")


@dataclass(frozen=True)
class OperationExecutionContext:
    id: str
    invocation_id: str
    attempt: int


@dataclass(frozen=True)
class _Execution:
    context: OperationExecutionContext
    capability: str = field(repr=False)


class OperationHTTPError(Exception):
    def __init__(self, status: int, code: str) -> None:
        self.status, self.code = status, code
        super().__init__(f"Operation request failed ({status}: {code})")


class GregaleOperations:
    """Use bind_request only on the trusted Gregale guest listener.

    Supply stable report_id values when retrying reports. The client fetches a
    fresh workload assertion for each report and never retries business work.
    The caller owns the injected AsyncClient and must close it after use.
    """

    def __init__(self, api_url: str, identity_endpoint: str, client: httpx.AsyncClient, timeout: float = 5) -> None:
        api, identity = urlsplit(api_url), urlsplit(identity_endpoint)
        loopback = {"localhost", "127.0.0.1", "::1"}
        if (
            not api.hostname
            or api.username
            or api.password
            or api.query
            or api.fragment
            or api.path not in {"", "/"}
            or (api.scheme != "https" and not (api.scheme == "http" and api.hostname in loopback))
        ):
            raise ValueError("Operations API requires HTTPS or loopback HTTP")
        if (
            identity.scheme != "http"
            or identity.hostname not in loopback
            or identity.username
            or identity.password
            or identity.fragment
        ):
            raise ValueError("Workload identity requires loopback HTTP")
        if not 0 < timeout <= 10:
            raise ValueError("Invalid operation report timeout")
        self._api = api_url.rstrip("/")
        query = dict(parse_qsl(identity.query))
        query["audience"] = "gregale:operations"
        self._identity = urlunsplit(identity._replace(query=urlencode(query)))
        self._client, self._timeout = client, timeout
        self._execution: ContextVar[_Execution | None] = ContextVar("gregale_operation", default=None)

    @contextmanager
    def bind_request(self, headers: Mapping[str, str]) -> Iterator[OperationExecutionContext | None]:
        normalized: dict[str, str] = {}
        for name, value in headers.items():
            lower = name.lower()
            if lower in normalized:
                raise ValueError("Duplicate operation execution header")
            normalized[lower] = value
        execution = None
        operation_id = normalized.get("x-gregale-customer-operation-id", "")
        if operation_id:
            invocation = normalized.get("x-faas-invocation-id", "")
            attempt = normalized.get("x-gregale-operation-attempt", "")
            capability = normalized.get("x-gregale-operation-capability", "")
            if (
                not _UUID.fullmatch(operation_id)
                or not _UUID.fullmatch(invocation)
                or not re.fullmatch(r"[1-9][0-9]{0,9}", attempt)
                or int(attempt) > 2_147_483_647
                or not re.fullmatch(r"[0-9a-f]{64}", capability)
                or any(
                    name.startswith("x-gregale-operation-")
                    and name not in {"x-gregale-operation-attempt", "x-gregale-operation-capability"}
                    for name in normalized
                )
            ):
                raise ValueError("Invalid operation execution context")
            execution = _Execution(OperationExecutionContext(operation_id, invocation, int(attempt)), capability)
        binding = self._execution.set(execution)
        try:
            yield self.context()
        finally:
            self._execution.reset(binding)

    def context(self) -> OperationExecutionContext | None:
        execution = self._execution.get()
        return execution.context if execution else None

    async def progress(self, report: OperationReportRequest) -> OperationResponse:
        return await self._report("progress", report.to_dict())

    async def artifact(self, report: OperationArtifactRequest) -> OperationResponse:
        return await self._report("artifacts", report.to_dict())

    async def control(self) -> OperationExecutionControlResponse:
        """Observe cancellation and time bounds without renewing or settling work.

        The application cooperates with intent and bounds its own I/O. This read
        does not certify that external effects can be repeated safely.
        """
        result = await self._request("control")
        execution = self._execution.get()
        if execution is None:
            raise ValueError("Control requires an operation execution")
        control = OperationExecutionControlResponse.from_dict(result)
        context = execution.context
        if (
            str(control.operation_id) != context.id
            or str(control.invocation_id) != context.invocation_id
            or type(control.attempt) is not int
            or control.attempt != context.attempt
            or type(control.cancellation_requested) is not bool
            or type(control.poll_after_ms) is not int
            or not 100 <= control.poll_after_ms <= 1000
            or any(value.utcoffset() is None for value in (control.observed_at, control.deadline_at, control.lease_expires_at))
            or control.lease_expires_at > control.deadline_at
        ):
            raise ValueError("Invalid operation control observation")
        control.additional_properties.clear()
        return control

    async def _report(self, suffix: str, body: dict) -> OperationResponse:
        return OperationResponse.from_dict(await self._request(suffix, body))

    async def _request(self, suffix: str, body: dict | None = None) -> dict:
        execution = self._execution.get()
        if execution is None:
            raise ValueError("Reporting requires an operation execution")
        # Streaming bounds identity and API responses before allocating JSON.
        token = await self._json("GET", self._identity, headers={"Cache-Control": "no-store"})
        bearer = token.get("access_token")
        if not isinstance(bearer, str) or not bearer or len(bearer) > 8192 or re.search(r"\s", bearer):
            raise ValueError("Invalid operation workload identity")
        context = execution.context
        return await self._json(
            "GET" if body is None else "POST",
            f"{self._api}/v1/runtime/operations/{context.id}/{suffix}",
            headers={
                "Authorization": f"Bearer {bearer}",
                "Cache-Control": "no-store",
                "X-Faas-Invocation-Id": context.invocation_id,
                "X-Gregale-Operation-Attempt": str(context.attempt),
                "X-Gregale-Operation-Capability": execution.capability,
            },
            **({"json": body} if body is not None else {}),
        )

    async def _json(self, method: str, url: str, **kwargs) -> dict:
        async with self._client.stream(
            method, url, follow_redirects=False, timeout=self._timeout, **kwargs
        ) as response:
            data = bytearray()
            async for chunk in response.aiter_bytes():
                data.extend(chunk)
                if len(data) > 2 * 1024 * 1024:
                    raise ValueError("Operation response exceeds its byte bound")
            if not 200 <= response.status_code < 300:
                try:
                    code = json.loads(data).get("code", "operation_request_failed")
                except (ValueError, AttributeError):
                    code = "operation_request_failed"
                raise OperationHTTPError(response.status_code, str(code))
            result = json.loads(data)
            if not isinstance(result, dict):
                raise ValueError("Operation response must be an object")
            return result
