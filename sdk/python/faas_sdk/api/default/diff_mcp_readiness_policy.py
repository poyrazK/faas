from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.mcp_readiness_policy_diff import MCPReadinessPolicyDiff
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    from_revision: str,
    to_revision: str | Unset = UNSET,
    scope: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["from_revision"] = from_revision

    params["to_revision"] = to_revision

    params["scope"] = scope

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/mcp-readiness-policy/diff".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | MCPReadinessPolicyDiff | None:
    if response.status_code == 200:
        response_200 = MCPReadinessPolicyDiff.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400

    if response.status_code == 404:
        response_404 = cast(Any, None)
        return response_404

    if response.status_code == 409:
        response_409 = cast(Any, None)
        return response_409

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | MCPReadinessPolicyDiff]:
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
    from_revision: str,
    to_revision: str | Unset = UNSET,
    scope: str | Unset = UNSET,
) -> Response[Any | MCPReadinessPolicyDiff]:
    """Compare configuration between two revisions; omitted to_revision selects current

    Args:
        slug (str):
        from_revision (str):
        to_revision (str | Unset):
        scope (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | MCPReadinessPolicyDiff]
    """

    kwargs = _get_kwargs(
        slug=slug,
        from_revision=from_revision,
        to_revision=to_revision,
        scope=scope,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    from_revision: str,
    to_revision: str | Unset = UNSET,
    scope: str | Unset = UNSET,
) -> Any | MCPReadinessPolicyDiff | None:
    """Compare configuration between two revisions; omitted to_revision selects current

    Args:
        slug (str):
        from_revision (str):
        to_revision (str | Unset):
        scope (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | MCPReadinessPolicyDiff
    """

    return sync_detailed(
        slug=slug,
        client=client,
        from_revision=from_revision,
        to_revision=to_revision,
        scope=scope,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    from_revision: str,
    to_revision: str | Unset = UNSET,
    scope: str | Unset = UNSET,
) -> Response[Any | MCPReadinessPolicyDiff]:
    """Compare configuration between two revisions; omitted to_revision selects current

    Args:
        slug (str):
        from_revision (str):
        to_revision (str | Unset):
        scope (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | MCPReadinessPolicyDiff]
    """

    kwargs = _get_kwargs(
        slug=slug,
        from_revision=from_revision,
        to_revision=to_revision,
        scope=scope,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    from_revision: str,
    to_revision: str | Unset = UNSET,
    scope: str | Unset = UNSET,
) -> Any | MCPReadinessPolicyDiff | None:
    """Compare configuration between two revisions; omitted to_revision selects current

    Args:
        slug (str):
        from_revision (str):
        to_revision (str | Unset):
        scope (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | MCPReadinessPolicyDiff
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            from_revision=from_revision,
            to_revision=to_revision,
            scope=scope,
        )
    ).parsed
