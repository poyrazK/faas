from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.application_standard_exception_list import ApplicationStandardExceptionList
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    app: UUID,
    *,
    after: UUID | Unset = UNSET,
    limit: int | Unset = 100,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_after: str | Unset = UNSET
    if not isinstance(after, Unset):
        json_after = str(after)
    params["after"] = json_after

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/orgs/{slug}/application-standard-enrollments/{app}/exceptions".format(
            slug=quote(str(slug), safe=""),
            app=quote(str(app), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ApplicationStandardExceptionList | Problem | None:
    if response.status_code == 200:
        response_200 = ApplicationStandardExceptionList.from_dict(response.json())

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
) -> Response[ApplicationStandardExceptionList | Problem]:
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
    after: UUID | Unset = UNSET,
    limit: int | Unset = 100,
) -> Response[ApplicationStandardExceptionList | Problem]:
    """List approval, revocation and expiry history for a live application

     UUID-ordered history with status evaluated at as_of. A full page has a next cursor; the final
    follow-up page may be empty. Historical approval does not imply that the exception contributes to
    current effective settings.

    Args:
        slug (str):
        app (UUID):
        after (UUID | Unset):
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApplicationStandardExceptionList | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        app=app,
        after=after,
        limit=limit,
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
    after: UUID | Unset = UNSET,
    limit: int | Unset = 100,
) -> ApplicationStandardExceptionList | Problem | None:
    """List approval, revocation and expiry history for a live application

     UUID-ordered history with status evaluated at as_of. A full page has a next cursor; the final
    follow-up page may be empty. Historical approval does not imply that the exception contributes to
    current effective settings.

    Args:
        slug (str):
        app (UUID):
        after (UUID | Unset):
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApplicationStandardExceptionList | Problem
    """

    return sync_detailed(
        slug=slug,
        app=app,
        client=client,
        after=after,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    slug: str,
    app: UUID,
    *,
    client: AuthenticatedClient | Client,
    after: UUID | Unset = UNSET,
    limit: int | Unset = 100,
) -> Response[ApplicationStandardExceptionList | Problem]:
    """List approval, revocation and expiry history for a live application

     UUID-ordered history with status evaluated at as_of. A full page has a next cursor; the final
    follow-up page may be empty. Historical approval does not imply that the exception contributes to
    current effective settings.

    Args:
        slug (str):
        app (UUID):
        after (UUID | Unset):
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApplicationStandardExceptionList | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        app=app,
        after=after,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    app: UUID,
    *,
    client: AuthenticatedClient | Client,
    after: UUID | Unset = UNSET,
    limit: int | Unset = 100,
) -> ApplicationStandardExceptionList | Problem | None:
    """List approval, revocation and expiry history for a live application

     UUID-ordered history with status evaluated at as_of. A full page has a next cursor; the final
    follow-up page may be empty. Historical approval does not imply that the exception contributes to
    current effective settings.

    Args:
        slug (str):
        app (UUID):
        after (UUID | Unset):
        limit (int | Unset):  Default: 100.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApplicationStandardExceptionList | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            app=app,
            client=client,
            after=after,
            limit=limit,
        )
    ).parsed
