from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.mcp_readiness_attention_page import MCPReadinessAttentionPage
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    scope: str | Unset = UNSET,
    after: UUID | Unset = UNSET,
    limit: int | Unset = 20,
    include_resolved: bool | Unset = False,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["scope"] = scope

    json_after: str | Unset = UNSET
    if not isinstance(after, Unset):
        json_after = str(after)
    params["after"] = json_after

    params["limit"] = limit

    params["include_resolved"] = include_resolved

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/mcp-readiness-attention".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | MCPReadinessAttentionPage | None:
    if response.status_code == 200:
        response_200 = MCPReadinessAttentionPage.from_dict(response.json())

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
) -> Response[Any | MCPReadinessAttentionPage]:
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
    scope: str | Unset = UNSET,
    after: UUID | Unset = UNSET,
    limit: int | Unset = 20,
    include_resolved: bool | Unset = False,
) -> Response[Any | MCPReadinessAttentionPage]:
    """List incident acknowledgement and reminder state

     Read scope and MFA required. Lists open incidents by default in ascending incident UUID order.
    Environment filters include all-environment incidents. State is separate from readiness and
    notification delivery. The next reminder time is eligibility only; fresh matching unhealthy evidence
    and no outstanding notification for this incident are required.

    Args:
        slug (str):
        scope (str | Unset):
        after (UUID | Unset):
        limit (int | Unset):  Default: 20.
        include_resolved (bool | Unset):  Default: False.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | MCPReadinessAttentionPage]
    """

    kwargs = _get_kwargs(
        slug=slug,
        scope=scope,
        after=after,
        limit=limit,
        include_resolved=include_resolved,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    scope: str | Unset = UNSET,
    after: UUID | Unset = UNSET,
    limit: int | Unset = 20,
    include_resolved: bool | Unset = False,
) -> Any | MCPReadinessAttentionPage | None:
    """List incident acknowledgement and reminder state

     Read scope and MFA required. Lists open incidents by default in ascending incident UUID order.
    Environment filters include all-environment incidents. State is separate from readiness and
    notification delivery. The next reminder time is eligibility only; fresh matching unhealthy evidence
    and no outstanding notification for this incident are required.

    Args:
        slug (str):
        scope (str | Unset):
        after (UUID | Unset):
        limit (int | Unset):  Default: 20.
        include_resolved (bool | Unset):  Default: False.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | MCPReadinessAttentionPage
    """

    return sync_detailed(
        slug=slug,
        client=client,
        scope=scope,
        after=after,
        limit=limit,
        include_resolved=include_resolved,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    scope: str | Unset = UNSET,
    after: UUID | Unset = UNSET,
    limit: int | Unset = 20,
    include_resolved: bool | Unset = False,
) -> Response[Any | MCPReadinessAttentionPage]:
    """List incident acknowledgement and reminder state

     Read scope and MFA required. Lists open incidents by default in ascending incident UUID order.
    Environment filters include all-environment incidents. State is separate from readiness and
    notification delivery. The next reminder time is eligibility only; fresh matching unhealthy evidence
    and no outstanding notification for this incident are required.

    Args:
        slug (str):
        scope (str | Unset):
        after (UUID | Unset):
        limit (int | Unset):  Default: 20.
        include_resolved (bool | Unset):  Default: False.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | MCPReadinessAttentionPage]
    """

    kwargs = _get_kwargs(
        slug=slug,
        scope=scope,
        after=after,
        limit=limit,
        include_resolved=include_resolved,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    scope: str | Unset = UNSET,
    after: UUID | Unset = UNSET,
    limit: int | Unset = 20,
    include_resolved: bool | Unset = False,
) -> Any | MCPReadinessAttentionPage | None:
    """List incident acknowledgement and reminder state

     Read scope and MFA required. Lists open incidents by default in ascending incident UUID order.
    Environment filters include all-environment incidents. State is separate from readiness and
    notification delivery. The next reminder time is eligibility only; fresh matching unhealthy evidence
    and no outstanding notification for this incident are required.

    Args:
        slug (str):
        scope (str | Unset):
        after (UUID | Unset):
        limit (int | Unset):  Default: 20.
        include_resolved (bool | Unset):  Default: False.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | MCPReadinessAttentionPage
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            scope=scope,
            after=after,
            limit=limit,
            include_resolved=include_resolved,
        )
    ).parsed
