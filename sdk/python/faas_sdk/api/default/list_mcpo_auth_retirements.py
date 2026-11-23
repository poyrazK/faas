from http import HTTPStatus
from typing import Any, cast

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.mcpo_auth_retirements_response import MCPOAuthRetirementsResponse
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    offset: int | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["offset"] = offset

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/mcp-oauth-retirements",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | MCPOAuthRetirementsResponse | None:
    if response.status_code == 200:
        response_200 = MCPOAuthRetirementsResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400

    if response.status_code == 403:
        response_403 = cast(Any, None)
        return response_403

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | MCPOAuthRetirementsResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    offset: int | Unset = UNSET,
) -> Response[Any | MCPOAuthRetirementsResponse]:
    """Read administrator-only OAuth retirement history including deleted applications

    Args:
        offset (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | MCPOAuthRetirementsResponse]
    """

    kwargs = _get_kwargs(
        offset=offset,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    offset: int | Unset = UNSET,
) -> Any | MCPOAuthRetirementsResponse | None:
    """Read administrator-only OAuth retirement history including deleted applications

    Args:
        offset (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | MCPOAuthRetirementsResponse
    """

    return sync_detailed(
        client=client,
        offset=offset,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    offset: int | Unset = UNSET,
) -> Response[Any | MCPOAuthRetirementsResponse]:
    """Read administrator-only OAuth retirement history including deleted applications

    Args:
        offset (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | MCPOAuthRetirementsResponse]
    """

    kwargs = _get_kwargs(
        offset=offset,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    offset: int | Unset = UNSET,
) -> Any | MCPOAuthRetirementsResponse | None:
    """Read administrator-only OAuth retirement history including deleted applications

    Args:
        offset (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | MCPOAuthRetirementsResponse
    """

    return (
        await asyncio_detailed(
            client=client,
            offset=offset,
        )
    ).parsed
