from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.object_bucket_access_grant_list import ObjectBucketAccessGrantList
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    organization: str,
    slug: str,
    bucket: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/orgs/{organization}/apps/{slug}/buckets/{bucket}/access-grants".format(
            organization=quote(str(organization), safe=""),
            slug=quote(str(slug), safe=""),
            bucket=quote(str(bucket), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ObjectBucketAccessGrantList | Problem:
    if response.status_code == 200:
        response_200 = ObjectBucketAccessGrantList.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ObjectBucketAccessGrantList | Problem]:
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
) -> Response[ObjectBucketAccessGrantList | Problem]:
    """List organization API-key bucket grants

     Owners and admins manage bucket credentials, access grants and bucket lifecycle. Requires the
    corresponding storage scope and current organization membership. Mutations require organization
    billing adoption. Requires storage:manage or admin. Revoked keys remain visible until their key row
    is deleted.

    Args:
        organization (str):
        slug (str):
        bucket (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectBucketAccessGrantList | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
        slug=slug,
        bucket=bucket,
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
) -> ObjectBucketAccessGrantList | Problem | None:
    """List organization API-key bucket grants

     Owners and admins manage bucket credentials, access grants and bucket lifecycle. Requires the
    corresponding storage scope and current organization membership. Mutations require organization
    billing adoption. Requires storage:manage or admin. Revoked keys remain visible until their key row
    is deleted.

    Args:
        organization (str):
        slug (str):
        bucket (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectBucketAccessGrantList | Problem
    """

    return sync_detailed(
        organization=organization,
        slug=slug,
        bucket=bucket,
        client=client,
    ).parsed


async def asyncio_detailed(
    organization: str,
    slug: str,
    bucket: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ObjectBucketAccessGrantList | Problem]:
    """List organization API-key bucket grants

     Owners and admins manage bucket credentials, access grants and bucket lifecycle. Requires the
    corresponding storage scope and current organization membership. Mutations require organization
    billing adoption. Requires storage:manage or admin. Revoked keys remain visible until their key row
    is deleted.

    Args:
        organization (str):
        slug (str):
        bucket (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectBucketAccessGrantList | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
        slug=slug,
        bucket=bucket,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    organization: str,
    slug: str,
    bucket: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> ObjectBucketAccessGrantList | Problem | None:
    """List organization API-key bucket grants

     Owners and admins manage bucket credentials, access grants and bucket lifecycle. Requires the
    corresponding storage scope and current organization membership. Mutations require organization
    billing adoption. Requires storage:manage or admin. Revoked keys remain visible until their key row
    is deleted.

    Args:
        organization (str):
        slug (str):
        bucket (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectBucketAccessGrantList | Problem
    """

    return (
        await asyncio_detailed(
            organization=organization,
            slug=slug,
            bucket=bucket,
            client=client,
        )
    ).parsed
