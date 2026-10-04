from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.regional_release_grant_response import RegionalReleaseGrantResponse
from ...types import Response


def _get_kwargs(
    cell_id: UUID,
    app_id: UUID,
    grant_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/cells/{cell_id}/apps/{app_id}/release-grants/{grant_id}".format(
            cell_id=quote(str(cell_id), safe=""),
            app_id=quote(str(app_id), safe=""),
            grant_id=quote(str(grant_id), safe=""),
        ),
    }

    _kwargs["follow_redirects"] = False
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | RegionalReleaseGrantResponse | None:
    if response.status_code == 200:
        response_200 = RegionalReleaseGrantResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | RegionalReleaseGrantResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    cell_id: UUID,
    app_id: UUID,
    grant_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | RegionalReleaseGrantResponse]:
    """Read regional release delegation metadata.

     Requires the personal regional account-admin key or the active delegated token for this exact grant.
    Returns no token or token hash. A token can read only its own grant metadata. Redirects must not be
    followed.

    Args:
        cell_id (UUID):
        app_id (UUID):
        grant_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RegionalReleaseGrantResponse]
    """

    kwargs = _get_kwargs(
        cell_id=cell_id,
        app_id=app_id,
        grant_id=grant_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    cell_id: UUID,
    app_id: UUID,
    grant_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | RegionalReleaseGrantResponse | None:
    """Read regional release delegation metadata.

     Requires the personal regional account-admin key or the active delegated token for this exact grant.
    Returns no token or token hash. A token can read only its own grant metadata. Redirects must not be
    followed.

    Args:
        cell_id (UUID):
        app_id (UUID):
        grant_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RegionalReleaseGrantResponse
    """

    return sync_detailed(
        cell_id=cell_id,
        app_id=app_id,
        grant_id=grant_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    cell_id: UUID,
    app_id: UUID,
    grant_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | RegionalReleaseGrantResponse]:
    """Read regional release delegation metadata.

     Requires the personal regional account-admin key or the active delegated token for this exact grant.
    Returns no token or token hash. A token can read only its own grant metadata. Redirects must not be
    followed.

    Args:
        cell_id (UUID):
        app_id (UUID):
        grant_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | RegionalReleaseGrantResponse]
    """

    kwargs = _get_kwargs(
        cell_id=cell_id,
        app_id=app_id,
        grant_id=grant_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    cell_id: UUID,
    app_id: UUID,
    grant_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | RegionalReleaseGrantResponse | None:
    """Read regional release delegation metadata.

     Requires the personal regional account-admin key or the active delegated token for this exact grant.
    Returns no token or token hash. A token can read only its own grant metadata. Redirects must not be
    followed.

    Args:
        cell_id (UUID):
        app_id (UUID):
        grant_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | RegionalReleaseGrantResponse
    """

    return (
        await asyncio_detailed(
            cell_id=cell_id,
            app_id=app_id,
            grant_id=grant_id,
            client=client,
        )
    ).parsed
