from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.global_runtime_quota_response import GlobalRuntimeQuotaResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    allocation_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "delete",
        "url": "/v1/global-apps/{slug}/runtime-quotas/{allocation_id}".format(
            slug=quote(str(slug), safe=""),
            allocation_id=quote(str(allocation_id), safe=""),
        ),
    }

    _kwargs["follow_redirects"] = False
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> GlobalRuntimeQuotaResponse | Problem | None:
    if response.status_code == 200:
        response_200 = GlobalRuntimeQuotaResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[GlobalRuntimeQuotaResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    allocation_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[GlobalRuntimeQuotaResponse | Problem]:
    """Retire a quota allocation without erasing its identity.

     Requires the same current tenant write and owner/admin consent as
    reservation. Unissued shares are released immediately. Issued shares
    remain charged until the internal regional verifier confirms a durable
    admission fence and zero counted instances for the original identity.
    Detach, registry changes, expiry and outages never prove drain. Repeated
    retirement returns the same tombstone. Customers cannot assert drain.

    Args:
        slug (str):
        allocation_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[GlobalRuntimeQuotaResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        allocation_id=allocation_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    allocation_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> GlobalRuntimeQuotaResponse | Problem | None:
    """Retire a quota allocation without erasing its identity.

     Requires the same current tenant write and owner/admin consent as
    reservation. Unissued shares are released immediately. Issued shares
    remain charged until the internal regional verifier confirms a durable
    admission fence and zero counted instances for the original identity.
    Detach, registry changes, expiry and outages never prove drain. Repeated
    retirement returns the same tombstone. Customers cannot assert drain.

    Args:
        slug (str):
        allocation_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        GlobalRuntimeQuotaResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        allocation_id=allocation_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    allocation_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[GlobalRuntimeQuotaResponse | Problem]:
    """Retire a quota allocation without erasing its identity.

     Requires the same current tenant write and owner/admin consent as
    reservation. Unissued shares are released immediately. Issued shares
    remain charged until the internal regional verifier confirms a durable
    admission fence and zero counted instances for the original identity.
    Detach, registry changes, expiry and outages never prove drain. Repeated
    retirement returns the same tombstone. Customers cannot assert drain.

    Args:
        slug (str):
        allocation_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[GlobalRuntimeQuotaResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        allocation_id=allocation_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    allocation_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> GlobalRuntimeQuotaResponse | Problem | None:
    """Retire a quota allocation without erasing its identity.

     Requires the same current tenant write and owner/admin consent as
    reservation. Unissued shares are released immediately. Issued shares
    remain charged until the internal regional verifier confirms a durable
    admission fence and zero counted instances for the original identity.
    Detach, registry changes, expiry and outages never prove drain. Repeated
    retirement returns the same tombstone. Customers cannot assert drain.

    Args:
        slug (str):
        allocation_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        GlobalRuntimeQuotaResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            allocation_id=allocation_id,
            client=client,
        )
    ).parsed
