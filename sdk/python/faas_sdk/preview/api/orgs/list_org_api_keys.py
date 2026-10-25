from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.list_org_api_keys_response import ListOrgAPIKeysResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/orgs/{slug}/keys".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ListOrgAPIKeysResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ListOrgAPIKeysResponse.from_dict(response.json())

        return response_200

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

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ListOrgAPIKeysResponse | Problem]:
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
) -> Response[ListOrgAPIKeysResponse | Problem]:
    """List API keys minted against the active org.

     Returns every key the org owns (active + grace + revoked).
    Mirrors `GET /v1/keys`; PR 6's canonical path. The `org_id`
    on every row will match `{slug}` because the store filters
    server-side on the loaded membership.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListOrgAPIKeysResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> ListOrgAPIKeysResponse | Problem | None:
    """List API keys minted against the active org.

     Returns every key the org owns (active + grace + revoked).
    Mirrors `GET /v1/keys`; PR 6's canonical path. The `org_id`
    on every row will match `{slug}` because the store filters
    server-side on the loaded membership.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListOrgAPIKeysResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ListOrgAPIKeysResponse | Problem]:
    """List API keys minted against the active org.

     Returns every key the org owns (active + grace + revoked).
    Mirrors `GET /v1/keys`; PR 6's canonical path. The `org_id`
    on every row will match `{slug}` because the store filters
    server-side on the loaded membership.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListOrgAPIKeysResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
) -> ListOrgAPIKeysResponse | Problem | None:
    """List API keys minted against the active org.

     Returns every key the org owns (active + grace + revoked).
    Mirrors `GET /v1/keys`; PR 6's canonical path. The `org_id`
    on every row will match `{slug}` because the store filters
    server-side on the loaded membership.

    Args:
        slug (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListOrgAPIKeysResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
        )
    ).parsed
