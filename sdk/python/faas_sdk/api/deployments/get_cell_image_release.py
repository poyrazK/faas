from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.cell_image_release_response import CellImageReleaseResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    cell_id: UUID,
    app_id: UUID,
    release_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/cells/{cell_id}/apps/{app_id}/image-releases/{release_id}".format(
            cell_id=quote(str(cell_id), safe=""),
            app_id=quote(str(app_id), safe=""),
            release_id=quote(str(release_id), safe=""),
        ),
    }

    _kwargs["follow_redirects"] = False
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> CellImageReleaseResponse | Problem | None:
    if response.status_code == 200:
        response_200 = CellImageReleaseResponse.from_dict(response.json())

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
) -> Response[CellImageReleaseResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    cell_id: UUID,
    app_id: UUID,
    release_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[CellImageReleaseResponse | Problem]:
    """Read a regional image release receipt and current deployment status.

     Requires a personal regional account key with admin, apps-read or deploy-write
    scope, verified email, and MFA when required. Current app ownership and cell
    identity are checked before returning the receipt. Collected deployments
    have a null deployment_id and expired status. Previous owners' receipts
    are hidden after transfer. Redirects must not be followed.

    Args:
        cell_id (UUID):
        app_id (UUID):
        release_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CellImageReleaseResponse | Problem]
    """

    kwargs = _get_kwargs(
        cell_id=cell_id,
        app_id=app_id,
        release_id=release_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    cell_id: UUID,
    app_id: UUID,
    release_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> CellImageReleaseResponse | Problem | None:
    """Read a regional image release receipt and current deployment status.

     Requires a personal regional account key with admin, apps-read or deploy-write
    scope, verified email, and MFA when required. Current app ownership and cell
    identity are checked before returning the receipt. Collected deployments
    have a null deployment_id and expired status. Previous owners' receipts
    are hidden after transfer. Redirects must not be followed.

    Args:
        cell_id (UUID):
        app_id (UUID):
        release_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CellImageReleaseResponse | Problem
    """

    return sync_detailed(
        cell_id=cell_id,
        app_id=app_id,
        release_id=release_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    cell_id: UUID,
    app_id: UUID,
    release_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[CellImageReleaseResponse | Problem]:
    """Read a regional image release receipt and current deployment status.

     Requires a personal regional account key with admin, apps-read or deploy-write
    scope, verified email, and MFA when required. Current app ownership and cell
    identity are checked before returning the receipt. Collected deployments
    have a null deployment_id and expired status. Previous owners' receipts
    are hidden after transfer. Redirects must not be followed.

    Args:
        cell_id (UUID):
        app_id (UUID):
        release_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CellImageReleaseResponse | Problem]
    """

    kwargs = _get_kwargs(
        cell_id=cell_id,
        app_id=app_id,
        release_id=release_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    cell_id: UUID,
    app_id: UUID,
    release_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> CellImageReleaseResponse | Problem | None:
    """Read a regional image release receipt and current deployment status.

     Requires a personal regional account key with admin, apps-read or deploy-write
    scope, verified email, and MFA when required. Current app ownership and cell
    identity are checked before returning the receipt. Collected deployments
    have a null deployment_id and expired status. Previous owners' receipts
    are hidden after transfer. Redirects must not be followed.

    Args:
        cell_id (UUID):
        app_id (UUID):
        release_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CellImageReleaseResponse | Problem
    """

    return (
        await asyncio_detailed(
            cell_id=cell_id,
            app_id=app_id,
            release_id=release_id,
            client=client,
        )
    ).parsed
