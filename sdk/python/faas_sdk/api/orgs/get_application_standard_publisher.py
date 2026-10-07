from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.application_standard_publisher import ApplicationStandardPublisher
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    resource: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/orgs/{slug}/application-standard-publishers/{resource}".format(
            slug=quote(str(slug), safe=""),
            resource=quote(str(resource), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ApplicationStandardPublisher | Problem | None:
    if response.status_code == 200:
        response_200 = ApplicationStandardPublisher.from_dict(response.json())

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

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ApplicationStandardPublisher | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    resource: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ApplicationStandardPublisher | Problem]:
    """Read an immutable publisher key reference.

     Requires org.view_application_standards to read this publisher key in its owning organization.

    Args:
        slug (str):
        resource (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApplicationStandardPublisher | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        resource=resource,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    resource: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> ApplicationStandardPublisher | Problem | None:
    """Read an immutable publisher key reference.

     Requires org.view_application_standards to read this publisher key in its owning organization.

    Args:
        slug (str):
        resource (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApplicationStandardPublisher | Problem
    """

    return sync_detailed(
        slug=slug,
        resource=resource,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    resource: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ApplicationStandardPublisher | Problem]:
    """Read an immutable publisher key reference.

     Requires org.view_application_standards to read this publisher key in its owning organization.

    Args:
        slug (str):
        resource (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApplicationStandardPublisher | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        resource=resource,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    resource: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> ApplicationStandardPublisher | Problem | None:
    """Read an immutable publisher key reference.

     Requires org.view_application_standards to read this publisher key in its owning organization.

    Args:
        slug (str):
        resource (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApplicationStandardPublisher | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            resource=resource,
            client=client,
        )
    ).parsed
