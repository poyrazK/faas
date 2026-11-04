from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.platform_tenant_offboarding_receipt_list_response import PlatformTenantOffboardingReceiptListResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    id: UUID,
    *,
    page_size: int | Unset = 50,
    page_token: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["page_size"] = page_size

    params["page_token"] = page_token

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/account/platform-tenants/{id}/offboardings".format(
            id=quote(str(id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> PlatformTenantOffboardingReceiptListResponse | Problem | None:
    if response.status_code == 200:
        response_200 = PlatformTenantOffboardingReceiptListResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[PlatformTenantOffboardingReceiptListResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    page_size: int | Unset = 50,
    page_token: str | Unset = UNSET,
) -> Response[PlatformTenantOffboardingReceiptListResponse | Problem]:
    """List durable platform-tenant offboarding receipts.

    Args:
        id (UUID):
        page_size (int | Unset):  Default: 50.
        page_token (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantOffboardingReceiptListResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        page_size=page_size,
        page_token=page_token,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    page_size: int | Unset = 50,
    page_token: str | Unset = UNSET,
) -> PlatformTenantOffboardingReceiptListResponse | Problem | None:
    """List durable platform-tenant offboarding receipts.

    Args:
        id (UUID):
        page_size (int | Unset):  Default: 50.
        page_token (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantOffboardingReceiptListResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        page_size=page_size,
        page_token=page_token,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    page_size: int | Unset = 50,
    page_token: str | Unset = UNSET,
) -> Response[PlatformTenantOffboardingReceiptListResponse | Problem]:
    """List durable platform-tenant offboarding receipts.

    Args:
        id (UUID):
        page_size (int | Unset):  Default: 50.
        page_token (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlatformTenantOffboardingReceiptListResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        page_size=page_size,
        page_token=page_token,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    page_size: int | Unset = 50,
    page_token: str | Unset = UNSET,
) -> PlatformTenantOffboardingReceiptListResponse | Problem | None:
    """List durable platform-tenant offboarding receipts.

    Args:
        id (UUID):
        page_size (int | Unset):  Default: 50.
        page_token (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlatformTenantOffboardingReceiptListResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            page_size=page_size,
            page_token=page_token,
        )
    ).parsed
