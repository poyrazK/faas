from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.outbound_ai_trace_response import OutboundAITraceResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    integration: UUID,
    trace: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/outbound/integrations/{integration}/ai-traces/{trace}".format(
            integration=quote(str(integration), safe=""),
            trace=quote(str(trace), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> OutboundAITraceResponse | Problem | None:
    if response.status_code == 200:
        response_200 = OutboundAITraceResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[OutboundAITraceResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    integration: UUID,
    trace: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[OutboundAITraceResponse | Problem]:
    """Read an AI trace with its logged calls.

     Requires MFA and read scope (ADR 20261020202035). Calls are the trace's request-log entries, oldest
    first, at most 500; calls_truncated marks a longer trace.

    Args:
        integration (UUID):
        trace (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OutboundAITraceResponse | Problem]
    """

    kwargs = _get_kwargs(
        integration=integration,
        trace=trace,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    integration: UUID,
    trace: str,
    *,
    client: AuthenticatedClient | Client,
) -> OutboundAITraceResponse | Problem | None:
    """Read an AI trace with its logged calls.

     Requires MFA and read scope (ADR 20261020202035). Calls are the trace's request-log entries, oldest
    first, at most 500; calls_truncated marks a longer trace.

    Args:
        integration (UUID):
        trace (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OutboundAITraceResponse | Problem
    """

    return sync_detailed(
        integration=integration,
        trace=trace,
        client=client,
    ).parsed


async def asyncio_detailed(
    integration: UUID,
    trace: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[OutboundAITraceResponse | Problem]:
    """Read an AI trace with its logged calls.

     Requires MFA and read scope (ADR 20261020202035). Calls are the trace's request-log entries, oldest
    first, at most 500; calls_truncated marks a longer trace.

    Args:
        integration (UUID):
        trace (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[OutboundAITraceResponse | Problem]
    """

    kwargs = _get_kwargs(
        integration=integration,
        trace=trace,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    integration: UUID,
    trace: str,
    *,
    client: AuthenticatedClient | Client,
) -> OutboundAITraceResponse | Problem | None:
    """Read an AI trace with its logged calls.

     Requires MFA and read scope (ADR 20261020202035). Calls are the trace's request-log entries, oldest
    first, at most 500; calls_truncated marks a longer trace.

    Args:
        integration (UUID):
        trace (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        OutboundAITraceResponse | Problem
    """

    return (
        await asyncio_detailed(
            integration=integration,
            trace=trace,
            client=client,
        )
    ).parsed
