from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.storage_usage_list_response import StorageUsageListResponse
from ...types import UNSET, Response


def _get_kwargs(
    *,
    day: str,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["day"] = day

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/usage/storage",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | StorageUsageListResponse | None:
    if response.status_code == 200:
        response_200 = StorageUsageListResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | StorageUsageListResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    day: str,
) -> Response[Problem | StorageUsageListResponse]:
    """Per-app daily storage rollup (informational).

    Args:
        day (str):  Example: 2026-07-29.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | StorageUsageListResponse]
    """

    kwargs = _get_kwargs(
        day=day,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    day: str,
) -> Problem | StorageUsageListResponse | None:
    """Per-app daily storage rollup (informational).

    Args:
        day (str):  Example: 2026-07-29.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | StorageUsageListResponse
    """

    return sync_detailed(
        client=client,
        day=day,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    day: str,
) -> Response[Problem | StorageUsageListResponse]:
    """Per-app daily storage rollup (informational).

    Args:
        day (str):  Example: 2026-07-29.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | StorageUsageListResponse]
    """

    kwargs = _get_kwargs(
        day=day,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    day: str,
) -> Problem | StorageUsageListResponse | None:
    """Per-app daily storage rollup (informational).

    Args:
        day (str):  Example: 2026-07-29.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | StorageUsageListResponse
    """

    return (
        await asyncio_detailed(
            client=client,
            day=day,
        )
    ).parsed
