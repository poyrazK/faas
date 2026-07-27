from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.account_export_response import AccountExportResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    include_secrets: bool | Unset = True,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["include_secrets"] = include_secrets

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/account/export",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AccountExportResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AccountExportResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AccountExportResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    include_secrets: bool | Unset = True,
) -> Response[AccountExportResponse | Problem]:
    """Export full account data (GDPR).

     Returns a JSON bundle containing every resource the account owns
    (apps, deployments, builds, instances, usage, domains, crons, API
    keys, app secrets) plus the GDPR audit trail. Available to
    `deleted_pending` accounts so the customer can take a final export
    during the 30-day grace window.

    Args:
        include_secrets (bool | Unset):  Default: True.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AccountExportResponse | Problem]
    """

    kwargs = _get_kwargs(
        include_secrets=include_secrets,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    include_secrets: bool | Unset = True,
) -> AccountExportResponse | Problem | None:
    """Export full account data (GDPR).

     Returns a JSON bundle containing every resource the account owns
    (apps, deployments, builds, instances, usage, domains, crons, API
    keys, app secrets) plus the GDPR audit trail. Available to
    `deleted_pending` accounts so the customer can take a final export
    during the 30-day grace window.

    Args:
        include_secrets (bool | Unset):  Default: True.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AccountExportResponse | Problem
    """

    return sync_detailed(
        client=client,
        include_secrets=include_secrets,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    include_secrets: bool | Unset = True,
) -> Response[AccountExportResponse | Problem]:
    """Export full account data (GDPR).

     Returns a JSON bundle containing every resource the account owns
    (apps, deployments, builds, instances, usage, domains, crons, API
    keys, app secrets) plus the GDPR audit trail. Available to
    `deleted_pending` accounts so the customer can take a final export
    during the 30-day grace window.

    Args:
        include_secrets (bool | Unset):  Default: True.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AccountExportResponse | Problem]
    """

    kwargs = _get_kwargs(
        include_secrets=include_secrets,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    include_secrets: bool | Unset = True,
) -> AccountExportResponse | Problem | None:
    """Export full account data (GDPR).

     Returns a JSON bundle containing every resource the account owns
    (apps, deployments, builds, instances, usage, domains, crons, API
    keys, app secrets) plus the GDPR audit trail. Available to
    `deleted_pending` accounts so the customer can take a final export
    during the 30-day grace window.

    Args:
        include_secrets (bool | Unset):  Default: True.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AccountExportResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            include_secrets=include_secrets,
        )
    ).parsed
