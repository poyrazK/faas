from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...types import File, Response


def _get_kwargs(
    id: str,
    *,
    body: File | bytes,
    upload_offset: int,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    headers["Upload-Offset"] = str(upload_offset)

    _kwargs: dict[str, Any] = {
        "method": "patch",
        "url": "/v1/uploads/{id}".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["content"] = body if isinstance(body, bytes) else body.payload.read()
    headers["Content-Type"] = "application/offset+octet-stream"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Problem | None:
    if response.status_code == 200:
        return None

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 410:
        response_410 = Problem.from_dict(response.json())

        return response_410

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: File | bytes,
    upload_offset: int,
) -> Response[Problem]:
    """Append a chunk to an open session.

    Args:
        id (str):
        upload_offset (int):
        body (File | bytes):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
        upload_offset=upload_offset,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: File | bytes,
    upload_offset: int,
) -> Problem | None:
    """Append a chunk to an open session.

    Args:
        id (str):
        upload_offset (int):
        body (File | bytes):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
        upload_offset=upload_offset,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: File | bytes,
    upload_offset: int,
) -> Response[Problem]:
    """Append a chunk to an open session.

    Args:
        id (str):
        upload_offset (int):
        body (File | bytes):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
        upload_offset=upload_offset,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: File | bytes,
    upload_offset: int,
) -> Problem | None:
    """Append a chunk to an open session.

    Args:
        id (str):
        upload_offset (int):
        body (File | bytes):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
            upload_offset=upload_offset,
        )
    ).parsed
