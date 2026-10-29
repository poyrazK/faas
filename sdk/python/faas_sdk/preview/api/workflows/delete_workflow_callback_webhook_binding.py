from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: UUID,
    callback_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "delete",
        "url": "/v1/workflows/runs/{id}/callbacks/{callback_id}/webhook-binding".format(
            id=quote(str(id), safe=""),
            callback_id=quote(str(callback_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | Problem | None:
    if response.status_code == 204:
        response_204 = cast(Any, None)
        return response_204

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

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


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Any | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: UUID,
    callback_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | Problem]:
    """Stop routing the provider event to this callback.

     Later verified provider events resume ordinary app delivery; an already verified in-flight request
    may still complete the callback.

    Args:
        id (UUID):
        callback_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        callback_id=callback_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    callback_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Any | Problem | None:
    """Stop routing the provider event to this callback.

     Later verified provider events resume ordinary app delivery; an already verified in-flight request
    may still complete the callback.

    Args:
        id (UUID):
        callback_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        id=id,
        callback_id=callback_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    callback_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | Problem]:
    """Stop routing the provider event to this callback.

     Later verified provider events resume ordinary app delivery; an already verified in-flight request
    may still complete the callback.

    Args:
        id (UUID):
        callback_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        callback_id=callback_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    callback_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Any | Problem | None:
    """Stop routing the provider event to this callback.

     Later verified provider events resume ordinary app delivery; an already verified in-flight request
    may still complete the callback.

    Args:
        id (UUID):
        callback_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            callback_id=callback_id,
            client=client,
        )
    ).parsed
