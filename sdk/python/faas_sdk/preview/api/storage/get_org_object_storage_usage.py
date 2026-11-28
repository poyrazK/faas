from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.object_storage_usage_response import ObjectStorageUsageResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    organization: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/orgs/{organization}/object-storage-usage".format(
            organization=quote(str(organization), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ObjectStorageUsageResponse | Problem:
    if response.status_code == 200:
        response_200 = ObjectStorageUsageResponse.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ObjectStorageUsageResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    organization: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ObjectStorageUsageResponse | Problem]:
    """Read aggregate organization object storage usage

     Owners, admins, developers, viewers and billing seats can read usage with usage:read or admin.
    Requires billing adoption. Aggregates company buckets across their retained storage namespaces;
    excludes personal buckets. Reservations are capacity commitments. Fresh false blocks further
    admission until allocated provider evidence arrives.

    Args:
        organization (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectStorageUsageResponse | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    organization: str,
    *,
    client: AuthenticatedClient | Client,
) -> ObjectStorageUsageResponse | Problem | None:
    """Read aggregate organization object storage usage

     Owners, admins, developers, viewers and billing seats can read usage with usage:read or admin.
    Requires billing adoption. Aggregates company buckets across their retained storage namespaces;
    excludes personal buckets. Reservations are capacity commitments. Fresh false blocks further
    admission until allocated provider evidence arrives.

    Args:
        organization (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectStorageUsageResponse | Problem
    """

    return sync_detailed(
        organization=organization,
        client=client,
    ).parsed


async def asyncio_detailed(
    organization: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ObjectStorageUsageResponse | Problem]:
    """Read aggregate organization object storage usage

     Owners, admins, developers, viewers and billing seats can read usage with usage:read or admin.
    Requires billing adoption. Aggregates company buckets across their retained storage namespaces;
    excludes personal buckets. Reservations are capacity commitments. Fresh false blocks further
    admission until allocated provider evidence arrives.

    Args:
        organization (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectStorageUsageResponse | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    organization: str,
    *,
    client: AuthenticatedClient | Client,
) -> ObjectStorageUsageResponse | Problem | None:
    """Read aggregate organization object storage usage

     Owners, admins, developers, viewers and billing seats can read usage with usage:read or admin.
    Requires billing adoption. Aggregates company buckets across their retained storage namespaces;
    excludes personal buckets. Reservations are capacity commitments. Fresh false blocks further
    admission until allocated provider evidence arrives.

    Args:
        organization (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectStorageUsageResponse | Problem
    """

    return (
        await asyncio_detailed(
            organization=organization,
            client=client,
        )
    ).parsed
