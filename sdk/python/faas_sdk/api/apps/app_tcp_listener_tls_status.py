from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.tcp_listener_tls_status_response import TCPListenerTLSStatusResponse
from ...types import Response


def _get_kwargs(
    slug: str,
    name: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/tcp-listeners/{name}/tls-status".format(
            slug=quote(str(slug), safe=""),
            name=quote(str(name), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | TCPListenerTLSStatusResponse | None:
    if response.status_code == 200:
        response_200 = TCPListenerTLSStatusResponse.from_dict(response.json())

        return response_200

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


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | TCPListenerTLSStatusResponse]:
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
) -> Response[Problem | TCPListenerTLSStatusResponse]:
    """Read observed edge certificate status for a TCP listener.

     Returns customer-safe certificate evidence for each observed edge.
    Missing evidence, evidence at least sixty seconds old, and changed or
    disabled TLS intent have unknown status. This does not establish fleet
    coverage, client trust, public routing or guest availability.

    Args:
        slug (str):
        name (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | TCPListenerTLSStatusResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
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
) -> Problem | TCPListenerTLSStatusResponse | None:
    """Read observed edge certificate status for a TCP listener.

     Returns customer-safe certificate evidence for each observed edge.
    Missing evidence, evidence at least sixty seconds old, and changed or
    disabled TLS intent have unknown status. This does not establish fleet
    coverage, client trust, public routing or guest availability.

    Args:
        slug (str):
        name (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | TCPListenerTLSStatusResponse
    """

    return sync_detailed(
        slug=slug,
        name=name,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | TCPListenerTLSStatusResponse]:
    """Read observed edge certificate status for a TCP listener.

     Returns customer-safe certificate evidence for each observed edge.
    Missing evidence, evidence at least sixty seconds old, and changed or
    disabled TLS intent have unknown status. This does not establish fleet
    coverage, client trust, public routing or guest availability.

    Args:
        slug (str):
        name (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | TCPListenerTLSStatusResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | TCPListenerTLSStatusResponse | None:
    """Read observed edge certificate status for a TCP listener.

     Returns customer-safe certificate evidence for each observed edge.
    Missing evidence, evidence at least sixty seconds old, and changed or
    disabled TLS intent have unknown status. This does not establish fleet
    coverage, client trust, public routing or guest availability.

    Args:
        slug (str):
        name (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | TCPListenerTLSStatusResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            name=name,
            client=client,
        )
    ).parsed
