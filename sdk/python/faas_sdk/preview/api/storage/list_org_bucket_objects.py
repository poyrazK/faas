from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.list_org_bucket_objects_response_200 import ListOrgBucketObjectsResponse200
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    organization: str,
    slug: str,
    bucket: UUID,
    *,
    prefix: str | Unset = UNSET,
    cursor: str | Unset = UNSET,
    limit: int | Unset = 100,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["prefix"] = prefix

    params["cursor"] = cursor

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/orgs/{organization}/apps/{slug}/buckets/{bucket}/objects".format(
            organization=quote(str(organization), safe=""),
            slug=quote(str(slug), safe=""),
            bucket=quote(str(bucket), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ListOrgBucketObjectsResponse200 | Problem:
    if response.status_code == 200:
        response_200 = ListOrgBucketObjectsResponse200.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ListOrgBucketObjectsResponse200 | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    organization: str,
    slug: str,
    bucket: UUID,
    *,
    client: AuthenticatedClient | Client,
    prefix: str | Unset = UNSET,
    cursor: str | Unset = UNSET,
    limit: int | Unset = 100,
) -> Response[ListOrgBucketObjectsResponse200 | Problem]:
    """List objects in an organization bucket

     Owners, admins, developers and viewers can inspect bucket metadata. Requires the corresponding
    storage scope and current organization membership. Mutations require organization billing adoption.
    Requires storage:read or admin. Non-admin keys also require a read or read_write grant on this
    bucket.

    Args:
        organization (str):
        slug (str):
        bucket (UUID):
        prefix (str | Unset):
        cursor (str | Unset):
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListOrgBucketObjectsResponse200 | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
        slug=slug,
        bucket=bucket,
        prefix=prefix,
        cursor=cursor,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    organization: str,
    slug: str,
    bucket: UUID,
    *,
    client: AuthenticatedClient | Client,
    prefix: str | Unset = UNSET,
    cursor: str | Unset = UNSET,
    limit: int | Unset = 100,
) -> ListOrgBucketObjectsResponse200 | Problem | None:
    """List objects in an organization bucket

     Owners, admins, developers and viewers can inspect bucket metadata. Requires the corresponding
    storage scope and current organization membership. Mutations require organization billing adoption.
    Requires storage:read or admin. Non-admin keys also require a read or read_write grant on this
    bucket.

    Args:
        organization (str):
        slug (str):
        bucket (UUID):
        prefix (str | Unset):
        cursor (str | Unset):
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListOrgBucketObjectsResponse200 | Problem
    """

    return sync_detailed(
        organization=organization,
        slug=slug,
        bucket=bucket,
        client=client,
        prefix=prefix,
        cursor=cursor,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    organization: str,
    slug: str,
    bucket: UUID,
    *,
    client: AuthenticatedClient | Client,
    prefix: str | Unset = UNSET,
    cursor: str | Unset = UNSET,
    limit: int | Unset = 100,
) -> Response[ListOrgBucketObjectsResponse200 | Problem]:
    """List objects in an organization bucket

     Owners, admins, developers and viewers can inspect bucket metadata. Requires the corresponding
    storage scope and current organization membership. Mutations require organization billing adoption.
    Requires storage:read or admin. Non-admin keys also require a read or read_write grant on this
    bucket.

    Args:
        organization (str):
        slug (str):
        bucket (UUID):
        prefix (str | Unset):
        cursor (str | Unset):
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListOrgBucketObjectsResponse200 | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
        slug=slug,
        bucket=bucket,
        prefix=prefix,
        cursor=cursor,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    organization: str,
    slug: str,
    bucket: UUID,
    *,
    client: AuthenticatedClient | Client,
    prefix: str | Unset = UNSET,
    cursor: str | Unset = UNSET,
    limit: int | Unset = 100,
) -> ListOrgBucketObjectsResponse200 | Problem | None:
    """List objects in an organization bucket

     Owners, admins, developers and viewers can inspect bucket metadata. Requires the corresponding
    storage scope and current organization membership. Mutations require organization billing adoption.
    Requires storage:read or admin. Non-admin keys also require a read or read_write grant on this
    bucket.

    Args:
        organization (str):
        slug (str):
        bucket (UUID):
        prefix (str | Unset):
        cursor (str | Unset):
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListOrgBucketObjectsResponse200 | Problem
    """

    return (
        await asyncio_detailed(
            organization=organization,
            slug=slug,
            bucket=bucket,
            client=client,
            prefix=prefix,
            cursor=cursor,
            limit=limit,
        )
    ).parsed
