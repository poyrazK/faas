from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.cell_image_release_response import CellImageReleaseResponse
from ...models.problem import Problem
from ...models.put_cell_image_release_request import PutCellImageReleaseRequest
from ...types import Response


def _get_kwargs(
    cell_id: UUID,
    app_id: UUID,
    release_id: UUID,
    *,
    body: PutCellImageReleaseRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/cells/{cell_id}/apps/{app_id}/image-releases/{release_id}".format(
            cell_id=quote(str(cell_id), safe=""),
            app_id=quote(str(app_id), safe=""),
            release_id=quote(str(release_id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    _kwargs["follow_redirects"] = False
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> CellImageReleaseResponse | Problem | None:
    if response.status_code == 202:
        response_202 = CellImageReleaseResponse.from_dict(response.json())

        return response_202

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

    if response.status_code == 410:
        response_410 = Problem.from_dict(response.json())

        return response_410

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

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
    body: PutCellImageReleaseRequest,
) -> Response[CellImageReleaseResponse | Problem]:
    """Accept a durable regional image release.

     Requires a personal regional account key with admin or deploy-write scope,
    verified email, and MFA when required. Uses the regional app configuration
    and the existing image deployment pipeline. The body is limited to 4096 bytes.
    Authorization and current app ownership are checked before durable replay.
    A matching release UUID and image return the same deployment forever,
    independently of the ordinary 24-hour request cache. A different image
    returns regional_image_release_conflict (409). A collected deployment
    returns regional_image_release_expired (410); it is never executed again.
    No Idempotency-Key header is required. Redirects must not be followed.
    This receipt proves regional acceptance, not global readiness or routing.

    Args:
        cell_id (UUID):
        app_id (UUID):
        release_id (UUID):
        body (PutCellImageReleaseRequest): Digest-pinned OCI image release using the existing
            regional app configuration.

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
        body=body,
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
    body: PutCellImageReleaseRequest,
) -> CellImageReleaseResponse | Problem | None:
    """Accept a durable regional image release.

     Requires a personal regional account key with admin or deploy-write scope,
    verified email, and MFA when required. Uses the regional app configuration
    and the existing image deployment pipeline. The body is limited to 4096 bytes.
    Authorization and current app ownership are checked before durable replay.
    A matching release UUID and image return the same deployment forever,
    independently of the ordinary 24-hour request cache. A different image
    returns regional_image_release_conflict (409). A collected deployment
    returns regional_image_release_expired (410); it is never executed again.
    No Idempotency-Key header is required. Redirects must not be followed.
    This receipt proves regional acceptance, not global readiness or routing.

    Args:
        cell_id (UUID):
        app_id (UUID):
        release_id (UUID):
        body (PutCellImageReleaseRequest): Digest-pinned OCI image release using the existing
            regional app configuration.

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
        body=body,
    ).parsed


async def asyncio_detailed(
    cell_id: UUID,
    app_id: UUID,
    release_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutCellImageReleaseRequest,
) -> Response[CellImageReleaseResponse | Problem]:
    """Accept a durable regional image release.

     Requires a personal regional account key with admin or deploy-write scope,
    verified email, and MFA when required. Uses the regional app configuration
    and the existing image deployment pipeline. The body is limited to 4096 bytes.
    Authorization and current app ownership are checked before durable replay.
    A matching release UUID and image return the same deployment forever,
    independently of the ordinary 24-hour request cache. A different image
    returns regional_image_release_conflict (409). A collected deployment
    returns regional_image_release_expired (410); it is never executed again.
    No Idempotency-Key header is required. Redirects must not be followed.
    This receipt proves regional acceptance, not global readiness or routing.

    Args:
        cell_id (UUID):
        app_id (UUID):
        release_id (UUID):
        body (PutCellImageReleaseRequest): Digest-pinned OCI image release using the existing
            regional app configuration.

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
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    cell_id: UUID,
    app_id: UUID,
    release_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: PutCellImageReleaseRequest,
) -> CellImageReleaseResponse | Problem | None:
    """Accept a durable regional image release.

     Requires a personal regional account key with admin or deploy-write scope,
    verified email, and MFA when required. Uses the regional app configuration
    and the existing image deployment pipeline. The body is limited to 4096 bytes.
    Authorization and current app ownership are checked before durable replay.
    A matching release UUID and image return the same deployment forever,
    independently of the ordinary 24-hour request cache. A different image
    returns regional_image_release_conflict (409). A collected deployment
    returns regional_image_release_expired (410); it is never executed again.
    No Idempotency-Key header is required. Redirects must not be followed.
    This receipt proves regional acceptance, not global readiness or routing.

    Args:
        cell_id (UUID):
        app_id (UUID):
        release_id (UUID):
        body (PutCellImageReleaseRequest): Digest-pinned OCI image release using the existing
            regional app configuration.

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
            body=body,
        )
    ).parsed
