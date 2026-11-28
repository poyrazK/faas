from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.event_schema import EventSchema
from ...types import UNSET, Response


def _get_kwargs(
    *,
    source: str,
    type_: str,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["source"] = source

    params["type"] = type_

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/event-schemas",
        "params": params,
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> list[EventSchema] | None:
    if response.status_code == 200:
        response_200 = []
        _response_200 = response.json()
        for response_200_item_data in _response_200:
            response_200_item = EventSchema.from_dict(response_200_item_data)

            response_200.append(response_200_item)

        return response_200

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[list[EventSchema]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient,
    source: str,
    type_: str,
) -> Response[list[EventSchema]]:
    """List registered versions for one event source and type.

     Requires apps:read or admin.

    Args:
        source (str):
        type_ (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[list[EventSchema]]
    """

    kwargs = _get_kwargs(
        source=source,
        type_=type_,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient,
    source: str,
    type_: str,
) -> list[EventSchema] | None:
    """List registered versions for one event source and type.

     Requires apps:read or admin.

    Args:
        source (str):
        type_ (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        list[EventSchema]
    """

    return sync_detailed(
        client=client,
        source=source,
        type_=type_,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    source: str,
    type_: str,
) -> Response[list[EventSchema]]:
    """List registered versions for one event source and type.

     Requires apps:read or admin.

    Args:
        source (str):
        type_ (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[list[EventSchema]]
    """

    kwargs = _get_kwargs(
        source=source,
        type_=type_,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient,
    source: str,
    type_: str,
) -> list[EventSchema] | None:
    """List registered versions for one event source and type.

     Requires apps:read or admin.

    Args:
        source (str):
        type_ (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        list[EventSchema]
    """

    return (
        await asyncio_detailed(
            client=client,
            source=source,
            type_=type_,
        )
    ).parsed
