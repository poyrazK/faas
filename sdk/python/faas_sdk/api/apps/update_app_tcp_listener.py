from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.tcp_listener_response import TCPListenerResponse
from ...models.update_tcp_listener_request import UpdateTCPListenerRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    name: str,
    *,
    body: UpdateTCPListenerRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "patch",
        "url": "/v1/apps/{slug}/tcp-listeners/{name}".format(
            slug=quote(str(slug), safe=""),
            name=quote(str(name), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | TCPListenerResponse | None:
    if response.status_code == 200:
        response_200 = TCPListenerResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

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
) -> Response[Problem | TCPListenerResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateTCPListenerRequest,
) -> Response[Problem | TCPListenerResponse]:
    """Enable or disable an app TCP listener.

     Disabling a listener fail-closes new connections without releasing its stable public port.

    Args:
        slug (str):
        name (str):
        body (UpdateTCPListenerRequest): Request to change whether an app TCP listener accepts
            connections.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | TCPListenerResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateTCPListenerRequest,
) -> Problem | TCPListenerResponse | None:
    """Enable or disable an app TCP listener.

     Disabling a listener fail-closes new connections without releasing its stable public port.

    Args:
        slug (str):
        name (str):
        body (UpdateTCPListenerRequest): Request to change whether an app TCP listener accepts
            connections.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | TCPListenerResponse
    """

    return sync_detailed(
        slug=slug,
        name=name,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateTCPListenerRequest,
) -> Response[Problem | TCPListenerResponse]:
    """Enable or disable an app TCP listener.

     Disabling a listener fail-closes new connections without releasing its stable public port.

    Args:
        slug (str):
        name (str):
        body (UpdateTCPListenerRequest): Request to change whether an app TCP listener accepts
            connections.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | TCPListenerResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateTCPListenerRequest,
) -> Problem | TCPListenerResponse | None:
    """Enable or disable an app TCP listener.

     Disabling a listener fail-closes new connections without releasing its stable public port.

    Args:
        slug (str):
        name (str):
        body (UpdateTCPListenerRequest): Request to change whether an app TCP listener accepts
            connections.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | TCPListenerResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            name=name,
            client=client,
            body=body,
        )
    ).parsed
