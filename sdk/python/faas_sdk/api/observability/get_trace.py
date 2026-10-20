from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.trace import Trace
from ...types import Response


def _get_kwargs(
    trace_id: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/traces/{trace_id}".format(
            trace_id=quote(str(trace_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Problem | Trace | None:
    if response.status_code == 200:
        response_200 = Trace.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Problem | Trace]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    trace_id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | Trace]:
    """Retrieve a stored OpenTelemetry trace.

     Issue #555. Returns the full span tree for a single trace_id
    (32-hex, the same as the request's `wake_id`). The trace is
    sourced from the gatewayd-public in-memory ring (24h
    retention, 100k default cap). When no OTLP endpoint is set
    the ring is the only source — when OTLP is set, the ring
    still operates as the customer-facing query layer.

    Authentication: the `X-Faas-Trace-Auth` header must carry
    the operator's observer token (env: `FAAS_TRACE_OBSERVER_TOKEN`).
    The endpoint is gated even when the dashboard session cookie is
    present — tracing is an operator surface, not a customer one.
    An empty token disables the endpoint (returns 404).

    Args:
        trace_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | Trace]
    """

    kwargs = _get_kwargs(
        trace_id=trace_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    trace_id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | Trace | None:
    """Retrieve a stored OpenTelemetry trace.

     Issue #555. Returns the full span tree for a single trace_id
    (32-hex, the same as the request's `wake_id`). The trace is
    sourced from the gatewayd-public in-memory ring (24h
    retention, 100k default cap). When no OTLP endpoint is set
    the ring is the only source — when OTLP is set, the ring
    still operates as the customer-facing query layer.

    Authentication: the `X-Faas-Trace-Auth` header must carry
    the operator's observer token (env: `FAAS_TRACE_OBSERVER_TOKEN`).
    The endpoint is gated even when the dashboard session cookie is
    present — tracing is an operator surface, not a customer one.
    An empty token disables the endpoint (returns 404).

    Args:
        trace_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | Trace
    """

    return sync_detailed(
        trace_id=trace_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    trace_id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | Trace]:
    """Retrieve a stored OpenTelemetry trace.

     Issue #555. Returns the full span tree for a single trace_id
    (32-hex, the same as the request's `wake_id`). The trace is
    sourced from the gatewayd-public in-memory ring (24h
    retention, 100k default cap). When no OTLP endpoint is set
    the ring is the only source — when OTLP is set, the ring
    still operates as the customer-facing query layer.

    Authentication: the `X-Faas-Trace-Auth` header must carry
    the operator's observer token (env: `FAAS_TRACE_OBSERVER_TOKEN`).
    The endpoint is gated even when the dashboard session cookie is
    present — tracing is an operator surface, not a customer one.
    An empty token disables the endpoint (returns 404).

    Args:
        trace_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | Trace]
    """

    kwargs = _get_kwargs(
        trace_id=trace_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    trace_id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | Trace | None:
    """Retrieve a stored OpenTelemetry trace.

     Issue #555. Returns the full span tree for a single trace_id
    (32-hex, the same as the request's `wake_id`). The trace is
    sourced from the gatewayd-public in-memory ring (24h
    retention, 100k default cap). When no OTLP endpoint is set
    the ring is the only source — when OTLP is set, the ring
    still operates as the customer-facing query layer.

    Authentication: the `X-Faas-Trace-Auth` header must carry
    the operator's observer token (env: `FAAS_TRACE_OBSERVER_TOKEN`).
    The endpoint is gated even when the dashboard session cookie is
    present — tracing is an operator surface, not a customer one.
    An empty token disables the endpoint (returns 404).

    Args:
        trace_id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | Trace
    """

    return (
        await asyncio_detailed(
            trace_id=trace_id,
            client=client,
        )
    ).parsed
