from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.object_bucket_list import ObjectBucketList
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    organization: str,
    slug: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/orgs/{organization}/apps/{slug}/buckets".format(
            organization=quote(str(organization), safe=""),
            slug=quote(str(slug), safe=""),
        ),
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> ObjectBucketList | Problem:
    if response.status_code == 200:
        response_200 = ObjectBucketList.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ObjectBucketList | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    organization: str,
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ObjectBucketList | Problem]:
    """List organization buckets and creation capabilities

     Owners, admins, developers and viewers can inspect bucket metadata. Requires the corresponding
    storage scope and current organization membership. Mutations require organization billing adoption.
    storage:manage lists every bucket. storage:read/storage:write keys see only buckets with an explicit
    grant. Admin and dashboard sessions list every bucket.

    Args:
        organization (str):
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectBucketList | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
        slug=slug,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    organization: str,
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> ObjectBucketList | Problem | None:
    """List organization buckets and creation capabilities

     Owners, admins, developers and viewers can inspect bucket metadata. Requires the corresponding
    storage scope and current organization membership. Mutations require organization billing adoption.
    storage:manage lists every bucket. storage:read/storage:write keys see only buckets with an explicit
    grant. Admin and dashboard sessions list every bucket.

    Args:
        organization (str):
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectBucketList | Problem
    """

    return sync_detailed(
        organization=organization,
        slug=slug,
        client=client,
    ).parsed


async def asyncio_detailed(
    organization: str,
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ObjectBucketList | Problem]:
    """List organization buckets and creation capabilities

     Owners, admins, developers and viewers can inspect bucket metadata. Requires the corresponding
    storage scope and current organization membership. Mutations require organization billing adoption.
    storage:manage lists every bucket. storage:read/storage:write keys see only buckets with an explicit
    grant. Admin and dashboard sessions list every bucket.

    Args:
        organization (str):
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectBucketList | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
        slug=slug,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    organization: str,
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> ObjectBucketList | Problem | None:
    """List organization buckets and creation capabilities

     Owners, admins, developers and viewers can inspect bucket metadata. Requires the corresponding
    storage scope and current organization membership. Mutations require organization billing adoption.
    storage:manage lists every bucket. storage:read/storage:write keys see only buckets with an explicit
    grant. Admin and dashboard sessions list every bucket.

    Args:
        organization (str):
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectBucketList | Problem
    """

    return (
        await asyncio_detailed(
            organization=organization,
            slug=slug,
            client=client,
        )
    ).parsed
