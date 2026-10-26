from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...types import Response


def _get_kwargs() -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/otel/v1/traces",
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | None:
    if response.status_code == 200:
        return None

    if response.status_code == 400:
        return None

    if response.status_code == 401:
        return None

    if response.status_code == 402:
        return None

    if response.status_code == 415:
        return None

    if response.status_code == 429:
        return None

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Any]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any]:
    """OTel spans writer (ADR-127 PR-D).

     Standard OTLP/HTTP endpoint for the OTel spans sidecar
    protocol (POST ExportTraceServiceRequest). Auth via
    Authorization: Bearer <api-key>. Plan-gated by
    DebugTelemetryEnabled; rate-capped by
    DebugTelemetryRequestsPerMinute; span-capped by
    DebugTelemetrySpansPerTrace. Body shape is defined by
    the OpenTelemetry proto — the spec documents the
    endpoint metadata only. The SDK does not model this
    route (routeExclude on sdk-coverage + spec_compliance);
    OTel SDKs speak OTLP/HTTP directly. Supports application/json
    (OTLP hexadecimal trace/span IDs) and application/x-protobuf,
    optional gzip compression, empty exports, and multi-trace batches.
    Responses use ExportTraceServiceResponse; per-trace ownership
    rejection returns partialSuccess with rejectedSpans. Error bodies
    use google.rpc.Status in the request encoding. Missing Content-Type
    retains the legacy ordinary-protobuf JSON input decoder. Acceptance
    stages an in-memory diagnostic summary; it is not durable raw-span storage.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any]
    """

    kwargs = _get_kwargs()

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any]:
    """OTel spans writer (ADR-127 PR-D).

     Standard OTLP/HTTP endpoint for the OTel spans sidecar
    protocol (POST ExportTraceServiceRequest). Auth via
    Authorization: Bearer <api-key>. Plan-gated by
    DebugTelemetryEnabled; rate-capped by
    DebugTelemetryRequestsPerMinute; span-capped by
    DebugTelemetrySpansPerTrace. Body shape is defined by
    the OpenTelemetry proto — the spec documents the
    endpoint metadata only. The SDK does not model this
    route (routeExclude on sdk-coverage + spec_compliance);
    OTel SDKs speak OTLP/HTTP directly. Supports application/json
    (OTLP hexadecimal trace/span IDs) and application/x-protobuf,
    optional gzip compression, empty exports, and multi-trace batches.
    Responses use ExportTraceServiceResponse; per-trace ownership
    rejection returns partialSuccess with rejectedSpans. Error bodies
    use google.rpc.Status in the request encoding. Missing Content-Type
    retains the legacy ordinary-protobuf JSON input decoder. Acceptance
    stages an in-memory diagnostic summary; it is not durable raw-span storage.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any]
    """

    kwargs = _get_kwargs()

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)
