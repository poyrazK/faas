from http import HTTPStatus
from typing import Any, cast

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.tool_registry_page import ToolRegistryPage
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    query: str | Unset = UNSET,
    cursor: str | Unset = UNSET,
    limit: int | Unset = 20,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["query"] = query

    params["cursor"] = cursor

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/tool-registry",
        "params": params,
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | ToolRegistryPage | None:
    if response.status_code == 200:
        response_200 = ToolRegistryPage.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400

    if response.status_code == 403:
        response_403 = cast(Any, None)
        return response_403

    if response.status_code == 404:
        response_404 = cast(Any, None)
        return response_404

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | ToolRegistryPage]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    query: str | Unset = UNSET,
    cursor: str | Unset = UNSET,
    limit: int | Unset = 20,
) -> Response[Any | ToolRegistryPage]:
    """Search private completed tool versions; apps-read plus exact grants for agent connections

     Owners read account summaries. Personal agent connections see only actively granted version/tool
    names within their current project limits. Registry membership grants no invocation permission and
    does not establish current availability. Shared organizations and tenant/app principals remain
    excluded.

    Args:
        query (str | Unset):
        cursor (str | Unset):
        limit (int | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ToolRegistryPage]
    """

    kwargs = _get_kwargs(
        query=query,
        cursor=cursor,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    query: str | Unset = UNSET,
    cursor: str | Unset = UNSET,
    limit: int | Unset = 20,
) -> Any | ToolRegistryPage | None:
    """Search private completed tool versions; apps-read plus exact grants for agent connections

     Owners read account summaries. Personal agent connections see only actively granted version/tool
    names within their current project limits. Registry membership grants no invocation permission and
    does not establish current availability. Shared organizations and tenant/app principals remain
    excluded.

    Args:
        query (str | Unset):
        cursor (str | Unset):
        limit (int | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ToolRegistryPage
    """

    return sync_detailed(
        client=client,
        query=query,
        cursor=cursor,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    query: str | Unset = UNSET,
    cursor: str | Unset = UNSET,
    limit: int | Unset = 20,
) -> Response[Any | ToolRegistryPage]:
    """Search private completed tool versions; apps-read plus exact grants for agent connections

     Owners read account summaries. Personal agent connections see only actively granted version/tool
    names within their current project limits. Registry membership grants no invocation permission and
    does not establish current availability. Shared organizations and tenant/app principals remain
    excluded.

    Args:
        query (str | Unset):
        cursor (str | Unset):
        limit (int | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ToolRegistryPage]
    """

    kwargs = _get_kwargs(
        query=query,
        cursor=cursor,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    query: str | Unset = UNSET,
    cursor: str | Unset = UNSET,
    limit: int | Unset = 20,
) -> Any | ToolRegistryPage | None:
    """Search private completed tool versions; apps-read plus exact grants for agent connections

     Owners read account summaries. Personal agent connections see only actively granted version/tool
    names within their current project limits. Registry membership grants no invocation permission and
    does not establish current availability. Shared organizations and tenant/app principals remain
    excluded.

    Args:
        query (str | Unset):
        cursor (str | Unset):
        limit (int | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ToolRegistryPage
    """

    return (
        await asyncio_detailed(
            client=client,
            query=query,
            cursor=cursor,
            limit=limit,
        )
    ).parsed
