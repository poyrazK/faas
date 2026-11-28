from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.tool_publication_record import ToolPublicationRecord
from ...types import Response


def _get_kwargs(
    id: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/tool-publications/{id}".format(
            id=quote(str(id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | ToolPublicationRecord | None:
    if response.status_code == 200:
        response_200 = ToolPublicationRecord.from_dict(response.json())

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
) -> Response[Any | ToolPublicationRecord]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | ToolPublicationRecord]:
    """Read a private publication journal; requires account-wide apps-read scope

     Unscoped personal-account connections only. Agent and MCP-login keys additionally require explicit
    deploy-write scope to inspect the account-wide publication journal. This does not grant registry
    discovery or invocation. An abandoned attempt is outcome_unknown after its deadline and never
    authorizes replay.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ToolPublicationRecord]
    """

    kwargs = _get_kwargs(
        id=id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Any | ToolPublicationRecord | None:
    """Read a private publication journal; requires account-wide apps-read scope

     Unscoped personal-account connections only. Agent and MCP-login keys additionally require explicit
    deploy-write scope to inspect the account-wide publication journal. This does not grant registry
    discovery or invocation. An abandoned attempt is outcome_unknown after its deadline and never
    authorizes replay.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ToolPublicationRecord
    """

    return sync_detailed(
        id=id,
        client=client,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | ToolPublicationRecord]:
    """Read a private publication journal; requires account-wide apps-read scope

     Unscoped personal-account connections only. Agent and MCP-login keys additionally require explicit
    deploy-write scope to inspect the account-wide publication journal. This does not grant registry
    discovery or invocation. An abandoned attempt is outcome_unknown after its deadline and never
    authorizes replay.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ToolPublicationRecord]
    """

    kwargs = _get_kwargs(
        id=id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
) -> Any | ToolPublicationRecord | None:
    """Read a private publication journal; requires account-wide apps-read scope

     Unscoped personal-account connections only. Agent and MCP-login keys additionally require explicit
    deploy-write scope to inspect the account-wide publication journal. This does not grant registry
    discovery or invocation. An abandoned attempt is outcome_unknown after its deadline and never
    authorizes replay.

    Args:
        id (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ToolPublicationRecord
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
        )
    ).parsed
