from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...types import UNSET, Response


def _get_kwargs(
    slug: str,
    *,
    registry: str,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["registry"] = registry

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "delete",
        "url": "/v1/apps/{slug}/registry-credentials".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | Problem | None:
    if response.status_code == 204:
        response_204 = cast(Any, None)
        return response_204

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


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Any | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    registry: str,
) -> Response[Any | Problem]:
    """Delete a sealed private-registry credential.

     Removes the `(app_id, registry)` row. The registry host is the
    URL resource and is supplied via the `?registry=` query param
    (URL-encoded). Hosts may carry a `:port` — path segments don't
    escape cleanly.

    Args:
        slug (str):
        registry (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        registry=registry,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    registry: str,
) -> Any | Problem | None:
    """Delete a sealed private-registry credential.

     Removes the `(app_id, registry)` row. The registry host is the
    URL resource and is supplied via the `?registry=` query param
    (URL-encoded). Hosts may carry a `:port` — path segments don't
    escape cleanly.

    Args:
        slug (str):
        registry (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        registry=registry,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    registry: str,
) -> Response[Any | Problem]:
    """Delete a sealed private-registry credential.

     Removes the `(app_id, registry)` row. The registry host is the
    URL resource and is supplied via the `?registry=` query param
    (URL-encoded). Hosts may carry a `:port` — path segments don't
    escape cleanly.

    Args:
        slug (str):
        registry (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        registry=registry,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    registry: str,
) -> Any | Problem | None:
    """Delete a sealed private-registry credential.

     Removes the `(app_id, registry)` row. The registry host is the
    URL resource and is supplied via the `?registry=` query param
    (URL-encoded). Hosts may carry a `:port` — path segments don't
    escape cleanly.

    Args:
        slug (str):
        registry (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            registry=registry,
        )
    ).parsed
