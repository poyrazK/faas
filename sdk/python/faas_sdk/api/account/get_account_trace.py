from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.account_trace_lookup_response import AccountTraceLookupResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    trace_id: str,
    *,
    limit: int | Unset = 100,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/account/traces/{trace_id}".format(
            trace_id=quote(str(trace_id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AccountTraceLookupResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AccountTraceLookupResponse.from_dict(response.json())

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

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AccountTraceLookupResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    trace_id: str,
    *,
    client: AuthenticatedClient,
    limit: int | Unset = 100,
) -> Response[AccountTraceLookupResponse | Problem]:
    """Look up one distributed trace across the account.

     Returns retained request telemetry, bounded span evidence, and
    durable invocation lifecycle rows linked by the platform trace id. Request
    payloads and arbitrary invocation headers are never returned.

    Args:
        trace_id (str):
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AccountTraceLookupResponse | Problem]
    """

    kwargs = _get_kwargs(
        trace_id=trace_id,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    trace_id: str,
    *,
    client: AuthenticatedClient,
    limit: int | Unset = 100,
) -> AccountTraceLookupResponse | Problem | None:
    """Look up one distributed trace across the account.

     Returns retained request telemetry, bounded span evidence, and
    durable invocation lifecycle rows linked by the platform trace id. Request
    payloads and arbitrary invocation headers are never returned.

    Args:
        trace_id (str):
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AccountTraceLookupResponse | Problem
    """

    return sync_detailed(
        trace_id=trace_id,
        client=client,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    trace_id: str,
    *,
    client: AuthenticatedClient,
    limit: int | Unset = 100,
) -> Response[AccountTraceLookupResponse | Problem]:
    """Look up one distributed trace across the account.

     Returns retained request telemetry, bounded span evidence, and
    durable invocation lifecycle rows linked by the platform trace id. Request
    payloads and arbitrary invocation headers are never returned.

    Args:
        trace_id (str):
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AccountTraceLookupResponse | Problem]
    """

    kwargs = _get_kwargs(
        trace_id=trace_id,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    trace_id: str,
    *,
    client: AuthenticatedClient,
    limit: int | Unset = 100,
) -> AccountTraceLookupResponse | Problem | None:
    """Look up one distributed trace across the account.

     Returns retained request telemetry, bounded span evidence, and
    durable invocation lifecycle rows linked by the platform trace id. Request
    payloads and arbitrary invocation headers are never returned.

    Args:
        trace_id (str):
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AccountTraceLookupResponse | Problem
    """

    return (
        await asyncio_detailed(
            trace_id=trace_id,
            client=client,
            limit=limit,
        )
    ).parsed
