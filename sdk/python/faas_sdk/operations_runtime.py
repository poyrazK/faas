"""Request-scoped reporting for ordinary and Customer Operation handlers."""

from __future__ import annotations

import json
import re
from collections.abc import Awaitable, Callable, Iterator, Mapping
from contextlib import contextmanager
from contextvars import ContextVar
from dataclasses import dataclass, field
from typing import Any
from urllib.parse import parse_qsl, urlencode, urlsplit, urlunsplit

import httpx

from .customer_operation_publication import (
    apublish_customer_operation_milestones,
    apublish_customer_operation_workflow_states,
)
from .customer_operations import (
    CustomerOperationPublicationError,
    CustomerOperationTransaction,
    awith_customer_operation_transaction,
    customer_operation_request_from_headers,
)
from .models.operation_artifact_request import OperationArtifactRequest
from .models.operation_milestone import OperationMilestone
from .models.operation_milestone_request import OperationMilestoneRequest
from .models.operation_milestone_validation_request import OperationMilestoneValidationRequest
from .models.operation_milestone_validation_response import OperationMilestoneValidationResponse
from .models.operation_report_request import OperationReportRequest
from .models.operation_response import OperationResponse
from .models.operation_workflow_state_report import OperationWorkflowStateReport
from .models.operation_workflow_state_report_response import OperationWorkflowStateReportResponse
from .models.operation_workflow_state_validation_request import OperationWorkflowStateValidationRequest
from .models.operation_workflow_state_validation_response import OperationWorkflowStateValidationResponse
from .operations import OperationTransactionResult

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
            operation_id = operation_id.lower()
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
                or any(
                    name.startswith("x-gregale-customer-operation-")
                    and name
                    not in {
                        "x-gregale-customer-operation-id",
                        "x-gregale-customer-operation-transaction-version",
                        "x-gregale-customer-operation-result-max-bytes",
                        "x-gregale-customer-operation-milestone-version",
                    }
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

    async def milestone(self, report: OperationMilestoneRequest) -> OperationMilestone:
        """Publish an already committed fact using its saved ID and occurrence time."""
        return OperationMilestone.from_dict(await self._report_json("milestones", report.to_dict()))

    async def validate_milestones(
        self, batch: OperationMilestoneValidationRequest
    ) -> OperationMilestoneValidationResponse:
        return OperationMilestoneValidationResponse.from_dict(
            await self._report_json("milestones/validate", batch.to_dict())
        )

    async def workflow_state(self, report: OperationWorkflowStateReport) -> OperationWorkflowStateReportResponse:
        """Publish a workflow state report that is already committed by the application."""
        return OperationWorkflowStateReportResponse.from_dict(
            await self._report_json("workflow-states", report.to_dict())
        )

    async def validate_workflow_states(
        self, batch: OperationWorkflowStateValidationRequest
    ) -> OperationWorkflowStateValidationResponse:
        return OperationWorkflowStateValidationResponse.from_dict(
            await self._report_json("workflow-states/validate", batch.to_dict())
        )

    async def transaction(
        self,
        connection: Any,
        headers: Mapping[str, str],
        method: str,
        path: str,
        body: bytes,
        handler: Callable[[CustomerOperationTransaction], Awaitable[Any]],
    ) -> OperationTransactionResult:
        """Commit app writes and customer facts, then publish the durable outbox.

        Call this only for requests from Gregale's trusted guest listener and
        after authorization. A replay skips ``handler`` and retries pending
        publication using the current request's execution proof.
        """
        request = customer_operation_request_from_headers(headers, method, path, body)

        async def validate_milestones(reports: list[OperationMilestoneRequest]) -> None:
            response = await self.validate_milestones(OperationMilestoneValidationRequest(milestones=reports))
            if response.valid is not True:
                raise ValueError("Customer Operation milestone validation was not confirmed")

        async def validate_workflow_states(
            reports: list[OperationWorkflowStateReport], milestones: list[OperationMilestoneRequest]
        ) -> None:
            response = await self.validate_workflow_states(
                OperationWorkflowStateValidationRequest(workflow_states=reports, milestones=milestones)
            )
            if response.valid is not True:
                raise ValueError("Customer Operation workflow state validation was not confirmed")

        with self.bind_request(headers):
            result = await awith_customer_operation_transaction(
                connection,
                request,
                handler,
                validate_milestones=validate_milestones,
                validate_workflow_states=validate_workflow_states,
            )
            if request.milestones_supported:
                try:
                    await apublish_customer_operation_milestones(connection, request, self.milestone)
                except Exception as error:
                    raise CustomerOperationPublicationError(request.operation_id, "milestone", error) from error
                try:
                    await apublish_customer_operation_workflow_states(connection, request, self.workflow_state)
                except Exception as error:
                    raise CustomerOperationPublicationError(request.operation_id, "workflow-state", error) from error
        return result

    async def _report(self, suffix: str, body: dict) -> OperationResponse:
        return OperationResponse.from_dict(await self._report_json(suffix, body))

    async def _report_json(self, suffix: str, body: dict) -> dict:
        execution = self._execution.get()
        if execution is None:
            raise ValueError("Reporting requires an operation execution")
        # Streaming bounds identity and API responses before allocating JSON.
        token = await self._json("GET", self._identity, headers={"Cache-Control": "no-store"})
        bearer = token.get("access_token")
        if not isinstance(bearer, str) or not bearer or len(bearer) > 8192 or re.search(r"\s", bearer):
            raise ValueError("Invalid operation workload identity")
        context = execution.context
        result = await self._json(
            "POST",
            f"{self._api}/v1/runtime/operations/{context.id}/{suffix}",
            json=body,
            headers={
                "Authorization": f"Bearer {bearer}",
                "Cache-Control": "no-store",
                "X-Faas-Invocation-Id": context.invocation_id,
                "X-Gregale-Operation-Attempt": str(context.attempt),
                "X-Gregale-Operation-Capability": execution.capability,
            },
        )
        return result

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
