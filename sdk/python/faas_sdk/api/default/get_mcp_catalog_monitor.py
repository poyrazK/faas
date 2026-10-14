from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.mcp_catalog_monitor_snapshot import MCPCatalogMonitorSnapshot
from ...models.mcp_catalog_monitor_summary import MCPCatalogMonitorSummary
from ...models.mcp_catalog_receipt import MCPCatalogReceipt
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    role: str | Unset = UNSET,
    all_roles: bool | Unset = False,
    evidence: UUID | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["role"] = role

    params["all_roles"] = all_roles

    json_evidence: str | Unset = UNSET
    if not isinstance(evidence, Unset):
        json_evidence = str(evidence)
    params["evidence"] = json_evidence

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/mcp-catalog-monitor".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | MCPCatalogMonitorSnapshot | MCPCatalogMonitorSummary | MCPCatalogReceipt | None:
    if response.status_code == 200:

        def _parse_response_200(
            data: object,
        ) -> MCPCatalogMonitorSnapshot | MCPCatalogMonitorSummary | MCPCatalogReceipt:
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                response_200_type_0 = MCPCatalogMonitorSnapshot.from_dict(data)

                return response_200_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                response_200_type_1 = MCPCatalogReceipt.from_dict(data)

                return response_200_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            if not isinstance(data, dict):
                raise TypeError()
            response_200_type_2 = MCPCatalogMonitorSummary.from_dict(data)

            return response_200_type_2

        response_200 = _parse_response_200(response.json())

        return response_200

    if response.status_code == 404:
        response_404 = cast(Any, None)
        return response_404

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | MCPCatalogMonitorSnapshot | MCPCatalogMonitorSummary | MCPCatalogReceipt]:
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
    role: str | Unset = UNSET,
    all_roles: bool | Unset = False,
    evidence: UUID | Unset = UNSET,
) -> Response[Any | MCPCatalogMonitorSnapshot | MCPCatalogMonitorSummary | MCPCatalogReceipt]:
    """Read opt-in catalog monitoring state and retained transitions

    Args:
        slug (str):
        role (str | Unset):
        all_roles (bool | Unset):  Default: False.
        evidence (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | MCPCatalogMonitorSnapshot | MCPCatalogMonitorSummary | MCPCatalogReceipt]
    """

    kwargs = _get_kwargs(
        slug=slug,
        role=role,
        all_roles=all_roles,
        evidence=evidence,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    role: str | Unset = UNSET,
    all_roles: bool | Unset = False,
    evidence: UUID | Unset = UNSET,
) -> Any | MCPCatalogMonitorSnapshot | MCPCatalogMonitorSummary | MCPCatalogReceipt | None:
    """Read opt-in catalog monitoring state and retained transitions

    Args:
        slug (str):
        role (str | Unset):
        all_roles (bool | Unset):  Default: False.
        evidence (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | MCPCatalogMonitorSnapshot | MCPCatalogMonitorSummary | MCPCatalogReceipt
    """

    return sync_detailed(
        slug=slug,
        client=client,
        role=role,
        all_roles=all_roles,
        evidence=evidence,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    role: str | Unset = UNSET,
    all_roles: bool | Unset = False,
    evidence: UUID | Unset = UNSET,
) -> Response[Any | MCPCatalogMonitorSnapshot | MCPCatalogMonitorSummary | MCPCatalogReceipt]:
    """Read opt-in catalog monitoring state and retained transitions

    Args:
        slug (str):
        role (str | Unset):
        all_roles (bool | Unset):  Default: False.
        evidence (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | MCPCatalogMonitorSnapshot | MCPCatalogMonitorSummary | MCPCatalogReceipt]
    """

    kwargs = _get_kwargs(
        slug=slug,
        role=role,
        all_roles=all_roles,
        evidence=evidence,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    role: str | Unset = UNSET,
    all_roles: bool | Unset = False,
    evidence: UUID | Unset = UNSET,
) -> Any | MCPCatalogMonitorSnapshot | MCPCatalogMonitorSummary | MCPCatalogReceipt | None:
    """Read opt-in catalog monitoring state and retained transitions

    Args:
        slug (str):
        role (str | Unset):
        all_roles (bool | Unset):  Default: False.
        evidence (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | MCPCatalogMonitorSnapshot | MCPCatalogMonitorSummary | MCPCatalogReceipt
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            role=role,
            all_roles=all_roles,
            evidence=evidence,
        )
    ).parsed
