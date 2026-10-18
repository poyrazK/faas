from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.mcp_readiness_attention_history import MCPReadinessAttentionHistory
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    id: UUID,
    *,
    before: str | Unset = UNSET,
    limit: int | Unset = 20,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["before"] = before

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/mcp-readiness-incidents/{id}/attention-actions".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | MCPReadinessAttentionHistory | None:
    if response.status_code == 200:
        response_200 = MCPReadinessAttentionHistory.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400

    if response.status_code == 401:
        response_401 = cast(Any, None)
        return response_401

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
) -> Response[Any | MCPReadinessAttentionHistory]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    before: str | Unset = UNSET,
    limit: int | Unset = 20,
) -> Response[Any | MCPReadinessAttentionHistory]:
    """Read audited incident attention decisions

     Read scope and MFA required. Immutable decisions ordered by creation time and request UUID
    descending. History remains available after resolution. Ordinary evaluator observations and reminder
    creation do not add operator decisions.

    Args:
        slug (str):
        id (UUID):
        before (str | Unset):
        limit (int | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | MCPReadinessAttentionHistory]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        before=before,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    before: str | Unset = UNSET,
    limit: int | Unset = 20,
) -> Any | MCPReadinessAttentionHistory | None:
    """Read audited incident attention decisions

     Read scope and MFA required. Immutable decisions ordered by creation time and request UUID
    descending. History remains available after resolution. Ordinary evaluator observations and reminder
    creation do not add operator decisions.

    Args:
        slug (str):
        id (UUID):
        before (str | Unset):
        limit (int | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | MCPReadinessAttentionHistory
    """

    return sync_detailed(
        slug=slug,
        id=id,
        client=client,
        before=before,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    before: str | Unset = UNSET,
    limit: int | Unset = 20,
) -> Response[Any | MCPReadinessAttentionHistory]:
    """Read audited incident attention decisions

     Read scope and MFA required. Immutable decisions ordered by creation time and request UUID
    descending. History remains available after resolution. Ordinary evaluator observations and reminder
    creation do not add operator decisions.

    Args:
        slug (str):
        id (UUID):
        before (str | Unset):
        limit (int | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | MCPReadinessAttentionHistory]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        before=before,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    before: str | Unset = UNSET,
    limit: int | Unset = 20,
) -> Any | MCPReadinessAttentionHistory | None:
    """Read audited incident attention decisions

     Read scope and MFA required. Immutable decisions ordered by creation time and request UUID
    descending. History remains available after resolution. Ordinary evaluator observations and reminder
    creation do not add operator decisions.

    Args:
        slug (str):
        id (UUID):
        before (str | Unset):
        limit (int | Unset):  Default: 20.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | MCPReadinessAttentionHistory
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            client=client,
            before=before,
            limit=limit,
        )
    ).parsed
