from http import HTTPStatus
from io import BytesIO
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...types import File, Response


def _get_kwargs(
    slug: str,
    id: UUID,
    artifact: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/operations/{id}/artifacts/{artifact}".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
            artifact=quote(str(artifact), safe=""),
        ),
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> File | Problem | None:
    if response.status_code == 200:
        response_200 = File(payload=BytesIO(response.content))

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

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 410:
        response_410 = Problem.from_dict(response.json())

        return response_410

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


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[File | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: UUID,
    artifact: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[File | Problem]:
    """Account download of verified operation result bytes.

     Requires account read scope and app ownership. Only succeeded operations allow downloads. The API
    checks the retained object size and SHA-256 before serving verified bytes. Changed or missing
    retained private bytes make the artifact unavailable without rewriting the business outcome. The
    reference expires with the operation projection. No signed URL is stored.

    Args:
        slug (str):
        id (UUID):
        artifact (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[File | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        artifact=artifact,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: UUID,
    artifact: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> File | Problem | None:
    """Account download of verified operation result bytes.

     Requires account read scope and app ownership. Only succeeded operations allow downloads. The API
    checks the retained object size and SHA-256 before serving verified bytes. Changed or missing
    retained private bytes make the artifact unavailable without rewriting the business outcome. The
    reference expires with the operation projection. No signed URL is stored.

    Args:
        slug (str):
        id (UUID):
        artifact (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        File | Problem
    """

    return sync_detailed(
        slug=slug,
        id=id,
        artifact=artifact,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: UUID,
    artifact: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[File | Problem]:
    """Account download of verified operation result bytes.

     Requires account read scope and app ownership. Only succeeded operations allow downloads. The API
    checks the retained object size and SHA-256 before serving verified bytes. Changed or missing
    retained private bytes make the artifact unavailable without rewriting the business outcome. The
    reference expires with the operation projection. No signed URL is stored.

    Args:
        slug (str):
        id (UUID):
        artifact (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[File | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        artifact=artifact,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: UUID,
    artifact: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> File | Problem | None:
    """Account download of verified operation result bytes.

     Requires account read scope and app ownership. Only succeeded operations allow downloads. The API
    checks the retained object size and SHA-256 before serving verified bytes. Changed or missing
    retained private bytes make the artifact unavailable without rewriting the business outcome. The
    reference expires with the operation projection. No signed URL is stored.

    Args:
        slug (str):
        id (UUID):
        artifact (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        File | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            artifact=artifact,
            client=client,
        )
    ).parsed
