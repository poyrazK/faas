from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    organization: str,
    slug: str,
    bucket: UUID,
    key: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "delete",
        "url": "/v1/orgs/{organization}/apps/{slug}/buckets/{bucket}/access-grants/{key}".format(
            organization=quote(str(organization), safe=""),
            slug=quote(str(slug), safe=""),
            bucket=quote(str(bucket), safe=""),
            key=quote(str(key), safe=""),
        ),
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | Problem:
    if response.status_code == 204:
        response_204 = cast(Any, None)
        return response_204

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Any | Problem]:
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
    key: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | Problem]:
    """Revoke an organization API-key bucket grant

     Owners and admins manage bucket credentials, access grants and bucket lifecycle. Requires the
    corresponding storage scope and current organization membership. Mutations require organization
    billing adoption. Requires storage:manage or admin. Revocation takes effect before this response
    returns; already-signed provider URLs remain valid until their short expiry.

    Args:
        organization (str):
        slug (str):
        bucket (UUID):
        key (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
        slug=slug,
        bucket=bucket,
        key=key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    organization: str,
    slug: str,
    bucket: UUID,
    key: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Any | Problem | None:
    """Revoke an organization API-key bucket grant

     Owners and admins manage bucket credentials, access grants and bucket lifecycle. Requires the
    corresponding storage scope and current organization membership. Mutations require organization
    billing adoption. Requires storage:manage or admin. Revocation takes effect before this response
    returns; already-signed provider URLs remain valid until their short expiry.

    Args:
        organization (str):
        slug (str):
        bucket (UUID):
        key (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        organization=organization,
        slug=slug,
        bucket=bucket,
        key=key,
        client=client,
    ).parsed


async def asyncio_detailed(
    organization: str,
    slug: str,
    bucket: UUID,
    key: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | Problem]:
    """Revoke an organization API-key bucket grant

     Owners and admins manage bucket credentials, access grants and bucket lifecycle. Requires the
    corresponding storage scope and current organization membership. Mutations require organization
    billing adoption. Requires storage:manage or admin. Revocation takes effect before this response
    returns; already-signed provider URLs remain valid until their short expiry.

    Args:
        organization (str):
        slug (str):
        bucket (UUID):
        key (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
        slug=slug,
        bucket=bucket,
        key=key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    organization: str,
    slug: str,
    bucket: UUID,
    key: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Any | Problem | None:
    """Revoke an organization API-key bucket grant

     Owners and admins manage bucket credentials, access grants and bucket lifecycle. Requires the
    corresponding storage scope and current organization membership. Mutations require organization
    billing adoption. Requires storage:manage or admin. Revocation takes effect before this response
    returns; already-signed provider URLs remain valid until their short expiry.

    Args:
        organization (str):
        slug (str):
        bucket (UUID):
        key (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return (
        await asyncio_detailed(
            organization=organization,
            slug=slug,
            bucket=bucket,
            key=key,
            client=client,
        )
    ).parsed
