from http import HTTPStatus
from typing import Any

import httpx

from ...client import AuthenticatedClient, Client
from ...models.account_usage_response import AccountUsageResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    month: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["month"] = month

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/account/usage",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AccountUsageResponse | Problem:
    if response.status_code == 200:
        response_200 = AccountUsageResponse.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AccountUsageResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    month: str | Unset = UNSET,
) -> Response[AccountUsageResponse | Problem]:
    """Read the account-wide usage projection

     Requires usage read scope. Returns the compute usage summary together
    with optional object-storage and managed-PostgreSQL usage views when
    those services are configured for the account. Each service keeps its
    existing freshness and guardrail semantics; omitted optional fields
    mean that service is not enabled for the account. The optional service
    views are included only for the current UTC month; historical requests
    return the compute projection alone.

    Args:
        month (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AccountUsageResponse | Problem]
    """

    kwargs = _get_kwargs(
        month=month,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    month: str | Unset = UNSET,
) -> AccountUsageResponse | Problem | None:
    """Read the account-wide usage projection

     Requires usage read scope. Returns the compute usage summary together
    with optional object-storage and managed-PostgreSQL usage views when
    those services are configured for the account. Each service keeps its
    existing freshness and guardrail semantics; omitted optional fields
    mean that service is not enabled for the account. The optional service
    views are included only for the current UTC month; historical requests
    return the compute projection alone.

    Args:
        month (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AccountUsageResponse | Problem
    """

    return sync_detailed(
        client=client,
        month=month,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    month: str | Unset = UNSET,
) -> Response[AccountUsageResponse | Problem]:
    """Read the account-wide usage projection

     Requires usage read scope. Returns the compute usage summary together
    with optional object-storage and managed-PostgreSQL usage views when
    those services are configured for the account. Each service keeps its
    existing freshness and guardrail semantics; omitted optional fields
    mean that service is not enabled for the account. The optional service
    views are included only for the current UTC month; historical requests
    return the compute projection alone.

    Args:
        month (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AccountUsageResponse | Problem]
    """

    kwargs = _get_kwargs(
        month=month,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    month: str | Unset = UNSET,
) -> AccountUsageResponse | Problem | None:
    """Read the account-wide usage projection

     Requires usage read scope. Returns the compute usage summary together
    with optional object-storage and managed-PostgreSQL usage views when
    those services are configured for the account. Each service keeps its
    existing freshness and guardrail semantics; omitted optional fields
    mean that service is not enabled for the account. The optional service
    views are included only for the current UTC month; historical requests
    return the compute projection alone.

    Args:
        month (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AccountUsageResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            month=month,
        )
    ).parsed
