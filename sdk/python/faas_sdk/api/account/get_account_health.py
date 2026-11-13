from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.account_health import AccountHealth
from ...models.problem import Problem
from ...types import Response


def _get_kwargs() -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/account/health",
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AccountHealth | Problem | None:
    if response.status_code == 200:
        response_200 = AccountHealth.from_dict(response.json())

        return response_200

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
) -> Response[AccountHealth | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[AccountHealth | Problem]:
    """Account health.

     Returns what may stop the calling account from working: restrictions
    such as a suspension, failed payment or unverified email with the
    next step, failed deployments from the last 7 days, custom domains
    that are not serving, and API requests refused in the last 24 hours
    with their problem codes and request ids. Internal review details are
    never included. Available to suspended and past-due accounts.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AccountHealth | Problem]
    """

    kwargs = _get_kwargs()

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
) -> AccountHealth | Problem | None:
    """Account health.

     Returns what may stop the calling account from working: restrictions
    such as a suspension, failed payment or unverified email with the
    next step, failed deployments from the last 7 days, custom domains
    that are not serving, and API requests refused in the last 24 hours
    with their problem codes and request ids. Internal review details are
    never included. Available to suspended and past-due accounts.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AccountHealth | Problem
    """

    return sync_detailed(
        client=client,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[AccountHealth | Problem]:
    """Account health.

     Returns what may stop the calling account from working: restrictions
    such as a suspension, failed payment or unverified email with the
    next step, failed deployments from the last 7 days, custom domains
    that are not serving, and API requests refused in the last 24 hours
    with their problem codes and request ids. Internal review details are
    never included. Available to suspended and past-due accounts.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AccountHealth | Problem]
    """

    kwargs = _get_kwargs()

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
) -> AccountHealth | Problem | None:
    """Account health.

     Returns what may stop the calling account from working: restrictions
    such as a suspension, failed payment or unverified email with the
    next step, failed deployments from the last 7 days, custom domains
    that are not serving, and API requests refused in the last 24 hours
    with their problem codes and request ids. Internal review details are
    never included. Available to suspended and past-due accounts.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AccountHealth | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
        )
    ).parsed
