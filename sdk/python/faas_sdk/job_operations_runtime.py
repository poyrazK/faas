"""ADR-645: reporting for a single native Job Operation task."""
from __future__ import annotations

import json
import os
import re
from collections.abc import Mapping
from dataclasses import dataclass
from urllib.parse import urlsplit

import httpx

from .models.operation_artifact_request import OperationArtifactRequest
from .models.operation_job_artifact_response import OperationJobArtifactResponse
from .models.operation_job_control_response import OperationJobControlResponse
from .models.operation_report_request import OperationReportRequest
from .models.operation_response import OperationResponse
from .operations_runtime import OperationHTTPError

_UUID = re.compile(r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\Z")


@dataclass(frozen=True)
class JobOperationContext:
    id: str
    run_id: str
    instance_id: str
    account_id: str
    app_id: str
    platform_tenant_id: str
    scope: str
    generation: int
    attempt: int


class GregaleJobOperations:
    """Use only the scheduler-provided environment. Call control between units
    of work and before external writes. A prepared result needs confirmed task
    exit; reporting never settles a task or retries business work.
    """

    def __init__(self, api_url: str, client: httpx.AsyncClient, *, env: Mapping[str, str] | None = None, timeout: float = 5) -> None:
        api = urlsplit(api_url)
        if not api.hostname or api.username or api.password or api.query or api.fragment or api.path not in {"", "/"} or (api.scheme != "https" and not (api.scheme == "http" and api.hostname in {"localhost", "127.0.0.1", "::1"})):
            raise ValueError("Job Operations API requires HTTPS or loopback HTTP")
        if not 0 < timeout <= 10 or client.auth is not None or "authorization" in client.headers:
            raise ValueError("Job Operations requires a tokenless client and bounded timeout")
        values = os.environ if env is None else env
        ids = [values.get(key, "") for key in ("GREGALE_CUSTOMER_OPERATION_ID", "GREGALE_RUN_ID", "GREGALE_CUSTOMER_OPERATION_JOB_INSTANCE_ID", "GREGALE_CUSTOMER_OPERATION_ACCOUNT_ID", "GREGALE_CUSTOMER_OPERATION_APP_ID", "GREGALE_CUSTOMER_OPERATION_PLATFORM_TENANT_ID")]
        generation, capability = values.get("GREGALE_CUSTOMER_OPERATION_GENERATION", ""), values.get("GREGALE_CUSTOMER_OPERATION_JOB_CAPABILITY", "")
        scope, raw = values.get("GREGALE_CUSTOMER_OPERATION_SCOPE", ""), values.get("GREGALE_CUSTOMER_OPERATION_INPUT", "")
        if any(not _UUID.fullmatch(value) for value in ids) or not re.fullmatch(r"[1-9][0-9]{0,9}", generation) or values.get("GREGALE_TASK_ATTEMPT") != "1" or values.get("GREGALE_TASK_INDEX") != "0" or not re.fullmatch(r"[0-9a-f]{64}", capability) or not scope or len(raw.encode()) > 32 * 1024:
            raise ValueError("Missing trusted Job Operation context")
        self.context = JobOperationContext(*ids, scope, int(generation), 1)
        self.input = json.loads(raw)
        self._api, self._client, self._timeout = api_url.rstrip("/"), client, timeout
        self._headers = {"X-Gregale-Operation-Job-Run-Id": ids[1], "X-Gregale-Operation-Job-Instance-Id": ids[2], "X-Gregale-Operation-Generation": generation, "X-Gregale-Operation-Attempt": "1", "X-Gregale-Operation-Job-Capability": capability}

    async def control(self) -> OperationJobControlResponse:
        value = OperationJobControlResponse.from_dict(await self._request("control"))
        context = self.context
        if (str(value.operation_id), str(value.job_run_id), str(value.account_id), str(value.app_id), str(value.platform_tenant_id), value.scope, value.generation, value.attempt) != (context.id, context.run_id, context.account_id, context.app_id, context.platform_tenant_id, context.scope, context.generation, context.attempt) or type(value.cancellation_requested) is not bool or type(value.poll_after_ms) is not int or not 100 <= value.poll_after_ms <= 1000 or any(date.utcoffset() is None for date in (value.deadline_at, value.lease_expires_at, value.observed_at)) or not value.observed_at < value.lease_expires_at <= value.deadline_at:
            raise ValueError("Invalid Job Operation authority")
        if value.cancellation_requested:
            raise RuntimeError("Job Operation cancellation requested")
        return value

    async def progress(self, report: OperationReportRequest) -> OperationResponse:
        await self.control()
        return OperationResponse.from_dict(await self._request("progress", {"report_id": report.report_id, "progress": report.to_dict()}))

    async def prepare_result(self, result, report_id: str = "job-result") -> OperationResponse:
        await self.control()
        return OperationResponse.from_dict(await self._request("result", {"report_id": report_id, "result": result}))

    async def reuse_artifact(self, declaration: OperationArtifactRequest) -> OperationJobArtifactResponse:
        """Check the private copy before invoking an application bucket writer."""
        return await self._artifact_request("artifact-receipts", declaration)

    async def prepare_artifact(self, declaration: OperationArtifactRequest) -> OperationJobArtifactResponse:
        """Retain an existing managed source; replay with the same report ID.

        This does not publish a file or authorize repeating an uncertain upload.
        """
        return await self._artifact_request("artifacts", declaration)

    async def _artifact_request(self, kind: str, declaration: OperationArtifactRequest) -> OperationJobArtifactResponse:
        await self.control()
        report = declaration.to_dict()
        value = await self._request(kind, report)
        artifact = value.get("artifact")
        if type(value.get("available")) is not bool or value["available"] != (artifact is not None):
            raise ValueError("Invalid Job artifact receipt")
        if artifact is not None:
            if not isinstance(artifact, dict) or not _UUID.fullmatch(artifact.get("id", "")) or any(artifact.get(key) != report[key] for key in ("name", "uri", "size_bytes", "sha256")):
                raise ValueError("Job artifact receipt declaration changed")
        elif kind == "artifacts":
            raise ValueError("Verified Job artifact receipt required")
        return OperationJobArtifactResponse.from_dict(value)

    async def _request(self, kind: str, body: dict | None = None) -> dict:
        kwargs = {"headers": self._headers, "follow_redirects": False, "timeout": self._timeout, "auth": None}
        if body is not None:
            kwargs["json"] = body
        async with self._client.stream("GET" if body is None else "POST", f"{self._api}/v1/runtime/job-operations/{self.context.id}/{kind}", **kwargs) as response:
            data = bytearray()
            async for chunk in response.aiter_bytes():
                data.extend(chunk)
                if len(data) > 2 * 1024 * 1024:
                    raise ValueError("Job Operation response exceeds its byte bound")
            if not 200 <= response.status_code < 300:
                raise OperationHTTPError(response.status_code, "job_operation_request_failed")
            result = json.loads(data)
            if not isinstance(result, dict):
                raise ValueError("Job Operation response must be an object")
            return result
