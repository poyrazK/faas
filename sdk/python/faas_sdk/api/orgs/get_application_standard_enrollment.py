from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.application_standard_enrollment import ApplicationStandardEnrollment
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    app: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/orgs/{slug}/application-standard-enrollments/{app}".format(
            slug=quote(str(slug), safe=""),
            app=quote(str(app), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ApplicationStandardEnrollment | Problem | None:
    if response.status_code == 200:
        response_200 = ApplicationStandardEnrollment.from_dict(response.json())

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

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ApplicationStandardEnrollment | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    app: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ApplicationStandardEnrollment | Problem]:
    """Read application standards intent, installed settings and observation progress.

     Requires org.view_application_standards and a read-scoped credential, with MFA for sessions.
    Local choices and desired revision describe saved intent. installed_effective describes
    the last installed projection and is absent before installation. Neither persisted
    settings nor this read establish consumer observation. Read-only during implementation.

    Args:
        slug (str):
        app (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApplicationStandardEnrollment | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        app=app,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    app: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> ApplicationStandardEnrollment | Problem | None:
    """Read application standards intent, installed settings and observation progress.

     Requires org.view_application_standards and a read-scoped credential, with MFA for sessions.
    Local choices and desired revision describe saved intent. installed_effective describes
    the last installed projection and is absent before installation. Neither persisted
    settings nor this read establish consumer observation. Read-only during implementation.

    Args:
        slug (str):
        app (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApplicationStandardEnrollment | Problem
    """

    return sync_detailed(
        slug=slug,
        app=app,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    app: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ApplicationStandardEnrollment | Problem]:
    """Read application standards intent, installed settings and observation progress.

     Requires org.view_application_standards and a read-scoped credential, with MFA for sessions.
    Local choices and desired revision describe saved intent. installed_effective describes
    the last installed projection and is absent before installation. Neither persisted
    settings nor this read establish consumer observation. Read-only during implementation.

    Args:
        slug (str):
        app (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApplicationStandardEnrollment | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        app=app,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    app: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> ApplicationStandardEnrollment | Problem | None:
    """Read application standards intent, installed settings and observation progress.

     Requires org.view_application_standards and a read-scoped credential, with MFA for sessions.
    Local choices and desired revision describe saved intent. installed_effective describes
    the last installed projection and is absent before installation. Neither persisted
    settings nor this read establish consumer observation. Read-only during implementation.

    Args:
        slug (str):
        app (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApplicationStandardEnrollment | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            app=app,
            client=client,
        )
    ).parsed
